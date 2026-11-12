package persona.core.domain.answer

import persona.core.contracts.Interaction
import persona.core.contracts.Question
import persona.core.contracts.Resolution
import persona.core.contracts.SecretRunRequest

enum class AnswerAction(val wire: String) {
    ApproveAll("approve_all"), ApproveEdits("approve_edits"), ApproveStepwise("approve_stepwise"), Reject("reject"), Allow("allow"),
    Deny("deny"), Always("always"), CancelQuestion("cancel_question"), Dismiss("dismiss"), PickOption("pick_option"),
    Advance("advance"), Submit("submit"), ScreenOption("screen_option"), ScreenSubmit("screen_submit"),
    TerminalText("terminal_text"), TerminalKeys("terminal_keys"), SubmitElicitation("submit_elicitation"),
    DeclineElicitation("decline_elicitation"), CancelElicitation("cancel_elicitation"),
}

data class AnswerContext(
    val entry: AnswerEntry = AnswerEntry.Conversation,
    val locked: Boolean = false,
    val state: String = "pending",
    val sending: Boolean = false,
    val fullPlanRead: Boolean = false,
    val rejecting: Boolean = false,
)

/** A form snapshot; question keys and selected labels always retain their original text. */
data class AnswerDraft(
    val feedback: String = "",
    val message: String = "",
    val page: Int = 0,
    val picks: Map<String, Set<String>> = emptyMap(),
    val other: Map<String, String> = emptyMap(),
    val optionIndex: Int = 0,
    val option: String = "",
    /** A checkbox dialog's ticked option keys, sent whole by [AnswerAction.ScreenSubmit]. */
    val selected: Set<String> = emptySet(),
    val text: String = "",
    val keys: List<String> = emptyList(),
    val fields: kotlinx.serialization.json.JsonObject = kotlinx.serialization.json.JsonObject(emptyMap()),
    // Runtime evidence only; never written to the draft journal or sent as content.
    val openedElicitationUrl: String? = null,
    /** A password card's typed password, from its own form at the moment of Run; never written to the draft journal. */
    val password: String = "",
    /**
     * A fields card's values by declared key ([hasFieldsForm], #4641), from its own form at the moment of Allow; never
     * written to the draft journal (some are secrets).
     */
    val values: Map<String, String> = emptyMap(),
)

data class AnswerResult(
    val allowed: Boolean,
    val resolution: Resolution? = null,
    val draft: AnswerDraft,
    val nextPage: Int? = null,
)

data class OptionDisplay(val text: String, val recommended: Boolean)

object AnswerBuilder {
    fun display(label: String): OptionDisplay {
        val suffix = listOf("(Recommended)", "（推荐）", "(推荐)").firstOrNull(label::endsWith) // i18n-ignore: marks the agent writes on its options
        return if (suffix == null) OptionDisplay(label, false)
        else OptionDisplay(label.dropLast(suffix.length).trim(), true)
    }

    /** Both entry policy and form validation must pass before a Resolution is produced. */
    fun build(
        interaction: Interaction,
        action: AnswerAction,
        draft: AnswerDraft = AnswerDraft(),
        context: AnswerContext = AnswerContext(),
    ): AnswerResult {
        fun blocked() = AnswerResult(false, draft = draft)
        fun resolved(resolution: Resolution) = AnswerResult(true, resolution, draft)
        if (context.state != "pending" || context.sending) return blocked()
        // A password card takes only its own two answers (#1581); no other card's answer carries a typed password.
        val passwordRulesMet = if (interaction.isSecretRun) secretRunAnswer(action, draft, context) else draft.password.isEmpty()
        if (!passwordRulesMet) return blocked()
        // A fields card's values go only with its own Allow in the conversation; no other answer carries any.
        if (draft.values.isNotEmpty() && !(interaction.hasFieldsForm && action == AnswerAction.Allow &&
                context.entry == AnswerEntry.Conversation)) return blocked()
        if (action in setOf(AnswerAction.SubmitElicitation, AnswerAction.DeclineElicitation, AnswerAction.CancelElicitation)) {
            if (interaction.kind != "elicitation" || context.entry != AnswerEntry.Conversation || context.locked) return blocked()
            return when (action) {
                AnswerAction.DeclineElicitation -> resolved(Resolution(action = "decline"))
                AnswerAction.CancelElicitation -> resolved(Resolution(action = "cancel"))
                else -> {
                    val form = interaction.elicitation ?: return blocked()
                    if (form.mode == "url") {
                        val url = ElicitationUrl.target(form.url) ?: return blocked()
                        return if (draft.openedElicitationUrl == url) resolved(Resolution(action = "accept")) else blocked()
                    }
                    if (form.mode != "form") return blocked()
                    val validation = ElicitationForm.validate(form.requestedSchema, draft.fields)
                    if (!validation.valid) blocked() else resolved(Resolution(action = "accept", content = validation.content))
                }
            }
        }
        if (action == AnswerAction.CancelQuestion) {
            if (interaction.kind != "question" || interaction.canCancel != true ||
                context.entry != AnswerEntry.Conversation || context.locked) return blocked()
            return resolved(Resolution(cancel = true))
        }
        val policyId = when (action) {
            AnswerAction.PickOption, AnswerAction.Advance, AnswerAction.Submit ->
                if (context.entry == AnswerEntry.Notification) ApprovalAction.ChooseOption else ApprovalAction.Answer
            AnswerAction.ScreenOption, AnswerAction.ScreenSubmit, AnswerAction.TerminalText, AnswerAction.TerminalKeys ->
                if (context.entry == AnswerEntry.Notification) ApprovalAction.ChooseOption else ApprovalAction.ScreenAnswer
            AnswerAction.SubmitElicitation, AnswerAction.DeclineElicitation, AnswerAction.CancelElicitation -> return blocked()
            else -> ApprovalAction.entries.single { it.wire == action.wire }
        }
        val policy = ApprovalPolicy.actions(context.entry, interaction.kind, context.locked)
            .firstOrNull { it.id == policyId } ?: return blocked()
        if (policy.availableNow != true) return blocked()
        if (policy.requirements.any { requirement ->
            when (requirement) {
                AnswerRequirement.FullPlanRead -> !context.fullPlanRead
                AnswerRequirement.FeedbackEmpty -> draft.feedback.isNotEmpty()
                AnswerRequirement.ReasonEmpty -> draft.message.isNotEmpty()
                AnswerRequirement.SessionPresent -> interaction.sessionId.isEmpty()
                AnswerRequirement.SingleQuestionSingleChoice -> interaction.questions?.let {
                    it.size == 1 && !it.single().multiSelect
                } != true
                AnswerRequirement.PlainDialogOption -> action != AnswerAction.ScreenOption ||
                    screenOptions(interaction).firstOrNull { it.key == draft.option }?.kind != ScreenOptionKind.Plain
                AnswerRequirement.AlwaysOffered -> !interaction.offersAlways
            }
        }) return blocked()

        // Reasons and feedback are mutually exclusive with approval across all entries.
        if (action in listOf(AnswerAction.ApproveAll, AnswerAction.ApproveEdits, AnswerAction.ApproveStepwise) &&
            (context.rejecting || draft.feedback.isNotEmpty())) return blocked()
        // Allowing a card with fields needs their values: a password card's Run carries its password (checked above), any
        // other's Fill In & Allow every required field (#4641). Such a card is never allowed for good: no Always.
        if (action in listOf(AnswerAction.Allow, AnswerAction.Always) && (draft.message.isNotEmpty() ||
                (interaction.isSecretRun && draft.password.isEmpty()) ||
                (interaction.hasFieldsForm && (action == AnswerAction.Always || !fieldsFilled(interaction, draft))))) return blocked()
        if (action == AnswerAction.Deny && draft.message.length > 4000) return blocked()
        if (context.entry == AnswerEntry.Notification && policyId == ApprovalAction.ChooseOption) {
            val validOption = when (action) {
                AnswerAction.PickOption -> draft.optionIndex in 0 until minOf(
                    policy.maxOptions!!, interaction.questions.orEmpty().single().options.size)
                AnswerAction.ScreenOption -> screenOptions(interaction).filter { it.kind == ScreenOptionKind.Plain }
                    .take(policy.maxOptions!!).any { it.key == draft.option }
                else -> false
            }
            if (!validOption) return blocked()
        }

        return when (action) {
            AnswerAction.ApproveAll, AnswerAction.ApproveEdits, AnswerAction.ApproveStepwise -> resolved(Resolution(approve = true, mode = policy.mode))
            AnswerAction.Reject -> resolved(Resolution(approve = false, feedback =
                if (context.entry == AnswerEntry.Notification) draft.feedback.trim() else draft.feedback))
            AnswerAction.Allow -> resolved(Resolution(allow = true,
                values = draft.password.takeIf { it.isNotEmpty() }?.let { mapOf(SecretRunRequest.PASSWORD to it) }
                    ?: draft.values.filterValues(String::isNotEmpty).takeIf { it.isNotEmpty() }))
            AnswerAction.Always -> resolved(Resolution(allow = true, always = true))
            AnswerAction.Deny -> {
                if (policy.input == "none" && draft.message.isNotEmpty()) return blocked()
                resolved(Resolution(allow = false, message = draft.message.takeIf { it.isNotEmpty() }))
            }
            AnswerAction.CancelQuestion -> blocked() // handled before the answer policy
            AnswerAction.Dismiss -> resolved(Resolution(dismiss = true))
            AnswerAction.PickOption, AnswerAction.Advance, AnswerAction.Submit -> question(interaction.questions.orEmpty(), action, draft)
            AnswerAction.ScreenOption -> {
                val option = screenOptions(interaction).firstOrNull { it.key == draft.option } ?: return blocked()
                // A checkbox dialog: the tap ticks or unticks, and ScreenSubmit sends the whole set.
                // "Chat about this" still goes as it is, as dialogMulti presses it.
                if (interaction.dialog?.multi == true && option.kind != ScreenOptionKind.Chat) {
                    if (option.kind != ScreenOptionKind.Plain) return blocked()
                    val selected = if (option.key in draft.selected) draft.selected - option.key else draft.selected + option.key
                    return AnswerResult(true, draft = draft.copy(selected = selected))
                }
                val text = draft.text.trim().takeIf { option.kind == ScreenOptionKind.TypeSomething }
                if (option.kind == ScreenOptionKind.TypeSomething && text.isNullOrEmpty()) return blocked()
                resolved(if (interaction.dialog != null) Resolution(option = option.key, text = text)
                    else if (text != null) Resolution(preKeys = listOf(option.key), text = text, keys = listOf("enter"))
                    else Resolution(keys = listOf(option.key)))
            }
            AnswerAction.ScreenSubmit -> {
                val dialog = interaction.dialog ?: return blocked()
                if (!dialog.multi) return blocked()
                val options = screenOptions(interaction).map { it.key }.filter { it in draft.selected }
                val text = draft.text.trim().takeIf { it.isNotEmpty() }
                if (options.isEmpty() && text == null) return blocked()
                resolved(Resolution(options = options, text = text))
            }
            AnswerAction.TerminalText -> if (draft.text.isEmpty()) blocked()
                else resolved(Resolution(text = draft.text, keys = listOf("enter")))
            AnswerAction.TerminalKeys -> if (draft.keys.isEmpty()) blocked() else resolved(Resolution(keys = draft.keys))
            AnswerAction.SubmitElicitation, AnswerAction.DeclineElicitation, AnswerAction.CancelElicitation -> blocked()
        }
    }

    /**
     * A password card (#1137, #1581) takes two answers. Run: an Allow from the card's own form in the conversation, carrying
     * the typed password and nothing else (sent under [SecretRunRequest.PASSWORD]). Decline: a Deny with nothing typed,
     * from any entry. Every other answer, and a Run from a quick entry (notification, lock screen, watch, shortcut), is
     * refused. A password of only spaces is refused too: the hub would answer the card with `refused` and run nothing.
     */
    private fun secretRunAnswer(action: AnswerAction, draft: AnswerDraft, context: AnswerContext): Boolean = when (action) {
        AnswerAction.Allow -> context.entry == AnswerEntry.Conversation && draft.password.isNotBlank() &&
            draft == AnswerDraft(password = draft.password)
        AnswerAction.Deny -> draft.message.isEmpty() && draft.password.isEmpty()
        else -> false
    }

    /** Every required field has something besides spaces, and nothing is filled in for a key the card does not declare. */
    private fun fieldsFilled(interaction: Interaction, draft: AnswerDraft): Boolean {
        val fields = interaction.fields.orEmpty()
        return fields.all { it.optional || draft.values[it.key].orEmpty().isNotBlank() } &&
            draft.values.keys.all { key -> fields.any { it.key == key } }
    }

    private fun question(questions: List<Question>, action: AnswerAction, draft: AnswerDraft): AnswerResult {
        val current = questions.getOrNull(draft.page) ?: return AnswerResult(false, draft = draft)
        var updated = draft
        if (action == AnswerAction.PickOption) {
            val label = current.options.getOrNull(draft.optionIndex)?.label ?: return AnswerResult(false, draft = draft)
            val selected = draft.picks[current.question].orEmpty()
            val picks = if (!current.multiSelect) setOf(label)
                else if (label in selected) selected - label else selected + label
            updated = draft.copy(picks = draft.picks + (current.question to picks),
                other = if (current.multiSelect) draft.other else draft.other - current.question)
            if (current.multiSelect) return AnswerResult(true, draft = updated)
            if (questions.size > 1) {
                val nextPage = (draft.page + 1).coerceAtMost(questions.lastIndex)
                return AnswerResult(true, draft = updated.copy(page = nextPage), nextPage = nextPage)
            }
        }
        fun answer(q: Question): String = updated.other[q.question]?.trim()?.takeIf { it.isNotEmpty() }
            ?: q.options.filter { it.label in updated.picks[q.question].orEmpty() }.joinToString(", ") { it.label }
        if (answer(current).isEmpty()) return AnswerResult(false, draft = updated)
        val missing = questions.indexOfFirst { answer(it).isEmpty() }
        if (missing == -1) return AnswerResult(true, Resolution(answers = questions.associate { it.question to answer(it) }), updated)
        val nextPage = if (action != AnswerAction.Submit && draft.page < questions.lastIndex) draft.page + 1 else missing
        return AnswerResult(true, draft = updated.copy(page = nextPage), nextPage = nextPage)
    }

    private fun screenOptions(interaction: Interaction): List<ParsedScreenOption> = interaction.dialog?.options?.map {
        ParsedScreenOption(it.key, it.label, it.detail, when (it.kind) {
            "type_something" -> ScreenOptionKind.TypeSomething
            "chat" -> ScreenOptionKind.Chat
            null, "", "plain" -> ScreenOptionKind.Plain
            else -> ScreenOptionKind.Unknown
        })
    } ?: ScreenAnswers.parse(interaction.screen.orEmpty())?.options.orEmpty()
}
