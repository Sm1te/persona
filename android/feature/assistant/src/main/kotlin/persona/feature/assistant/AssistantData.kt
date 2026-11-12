package persona.feature.assistant

import androidx.annotation.StringRes
import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import persona.core.contracts.Session
import persona.core.contracts.AgentState
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantRepository
import persona.core.contracts.api.AssistantSnapshot
import persona.core.contracts.api.LeaderCounts
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.MainConversationSender
import persona.core.contracts.api.DraftSnapshot
import persona.core.contracts.api.PendingMainConversation
import persona.core.designsystem.LocalizedFormats
import persona.core.designsystem.PendingStatus
import persona.core.designsystem.Status
import persona.core.designsystem.StatusLineState
import persona.core.designsystem.shownName
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import java.util.UUID

/**
 * The drawer's Persona chats (#4331): [main] the main conversation (its own row, above the search), [directions] the
 * leader conversations under their [names] ("常驻", in the hub's order), and [others] every other conversation, which
 * "All chats" lists and a search narrows. [counts] each leader's open tasks, by session id. [recent] the user's own newest
 * chats between 常驻 and All chats (#4922), none while searching. [background] the built-in background supervisor's
 * conversation id (#4927): it is one of [others], under its own name, and its row has no pin, archive or end.
 */
internal data class ConversationGroups(
    val main: Session?, val directions: List<Session> = emptyList(), val others: List<Session> = emptyList(),
    val names: Map<String, String> = emptyMap(), val counts: Map<String, LeaderCounts> = emptyMap(),
    val recent: List<Session> = emptyList(),
    val background: String? = null,
)

/** How many chats the drawer's Recent lists (#4922, the web's `RECENT_LIMIT`, iOS). */
internal const val RECENT_LIMIT = 5

/** Origins of the user's own chats (docs/protocol/protocol.md "Origin of assistant chats"): the app, a channel's /new, or none from an older hub. */
private val USER_ORIGINS = setOf("", "user", "channel")

/**
 * The drawer's Recent (#4922, web `recentConversations`, iOS `AssistantSidebarGroups.recent`): the conversations that are
 * not the main one, not a leader, not archived and not opened by Persona itself (the inbox, a task, Lark /agent, a spawned
 * chat, an extension, the background supervisor: those stay in All chats only), newest first, equal times by session id, at most [limit].
 */
internal fun recentConversations(snapshot: AssistantSnapshot, limit: Int = RECENT_LIMIT): List<Session> {
    val leaders = snapshot.leaders.toSet()
    return snapshot.conversations.distinctBy { it.key }
        .filter { it.key != snapshot.mainSession && it.sessionId !in leaders && !it.archived && it.origin?.trim().orEmpty() in USER_ORIGINS &&
            it.sessionId != snapshot.backgroundSupervisor }
        .map { it to it.updated }
        .sortedWith(compareByDescending<Pair<Session, java.time.Instant?>> { it.second }.thenBy { it.first.sessionId })
        .take(limit).map { it.first }
}

internal fun conversationGroups(
    snapshot: AssistantSnapshot,
    query: String,
    words: ConversationWords,
): ConversationGroups {
    // The background supervisor (#4927) is built in: a conversation with the scheduled tasks' ones under All chats, found
    // by its name too, and never a direction whatever the leaders say.
    val background = snapshot.backgroundSupervisor?.takeIf(String::isNotBlank)
    val matches = snapshot.conversations.distinctBy { it.key }.filter {
        !it.archived && (query.isBlank() || it.conversationTitle(words).contains(query.trim(), ignoreCase = true) ||
                it.lastText.orEmpty().contains(query.trim(), ignoreCase = true) ||
                (it.sessionId == background && words.background.contains(query.trim(), ignoreCase = true)))
    }.map { it to it.updated } // each time parsed once, not on every comparison
        .sortedWith(compareByDescending<Pair<Session, java.time.Instant?>> { it.second }.thenBy { it.first.key.path }).map { it.first }
    // The leaders are the drawer's 常驻 group, under their names; a search matches the name too. Every other
    // conversation waits under one "All chats" row (there is no all-conversations page on this app): main and the
    // leaders are not listed twice.
    val names = snapshot.directionNames
    val directions = snapshot.leaders.filter { it != background }.mapNotNull { id ->
        snapshot.conversations.firstOrNull { it.sessionId == id && it.key != snapshot.mainSession && !it.archived }
    }.filter { query.isBlank() || it in matches || names[it.sessionId].orEmpty().contains(query.trim(), ignoreCase = true) }
    val listed = directions.mapTo(HashSet()) { it.key }
    val others = matches.filter { it.key != snapshot.mainSession && it.key !in listed }
    return ConversationGroups(
        snapshot.conversations.firstOrNull { it.key == snapshot.mainSession && !it.archived },
        directions, others, names, snapshot.leaderCounts,
        recent = if (query.isBlank()) recentConversations(snapshot) else emptyList(),
        background = background,
    )
}

/** A leader's open tasks 「在做 1 · 等你 2」 (#4331): only the parts that are not zero, null when both are. */
internal fun leaderCountsLabel(counts: LeaderCounts?, resources: android.content.res.Resources): String? {
    counts ?: return null
    return listOfNotNull(
        counts.working.takeIf { it > 0 }?.let { resources.getString(R.string.assistant_leader_working, it) },
        counts.needsYou.takeIf { it > 0 }?.let { resources.getString(R.string.assistant_leader_needs_you, it) },
    ).takeIf { it.isNotEmpty() }?.joinToString(" · ")
}

@StringRes
internal fun greeting(hour: Int): Int = when (hour) {
    in 5..10 -> R.string.assistant_greeting_morning
    in 11..13 -> R.string.assistant_greeting_noon
    in 14..17 -> R.string.assistant_greeting_afternoon
    else -> R.string.assistant_greeting_evening
}

/** The demo's sample conversations, in the language on screen. */
internal data class DemoWords(
    val weekly: String, val weeklyLast: String, val travel: String, val document: String, val newChat: String,
)

@Composable
internal fun demoWords() = DemoWords(
    stringResource(R.string.assistant_demo_weekly), stringResource(R.string.assistant_demo_weekly_last),
    stringResource(R.string.assistant_demo_travel), stringResource(R.string.assistant_demo_document),
    stringResource(R.string.assistant_new_conversation),
)

/** Local fixtures never submit messages to a hub. */
internal class DemoAssistantRepository(private val words: DemoWords) : AssistantRepository, MessageSender, MainConversationSender {
    private val snapshot = MutableStateFlow(AssistantSnapshot(conversations = listOf(
        Session(machineId = "demo", sessionId = "weekly", title = words.weekly, assistant = true,
            lastText = words.weeklyLast, updatedAt = "2026-10-30T09:00:00Z"),
        Session(machineId = "demo", sessionId = "travel", title = words.travel, assistant = true,
            updatedAt = "2026-10-29T09:00:00Z"),
        Session(machineId = "demo", sessionId = "document", title = words.document, assistant = true,
            updatedAt = "2026-10-28T09:00:00Z"),
    )))
    override val assistant = snapshot.asStateFlow()
    private val messages = MutableStateFlow<List<OutboxMessage>>(emptyList())
    override val outbox = messages.asStateFlow()

    override suspend fun refresh() = Unit

    override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) {
        send(assistant.value.mainSession ?: start(), text, clientMessageId)
        messages.update { all -> all.map { if (it.clientMessageId == clientMessageId) it.copy(mainTarget = true) else it } }
    }

    suspend fun start(): SessionKey {
        val key = SessionKey("demo", UUID.randomUUID().toString())
        snapshot.update {
            it.copy(mainSession = it.mainSession ?: key, conversations = listOf(
                Session(machineId = key.machineId, sessionId = key.sessionId,
                    title = words.newChat, assistant = true),
            ) + it.conversations)
        }
        return key
    }

    override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) {
        messages.update { previous ->
            previous.filterNot { it.clientMessageId == clientMessageId } +
                OutboxMessage(clientMessageId, session, text, DeliveryState.Sent)
        }
        snapshot.update { current -> current.copy(conversations = current.conversations.map {
            if (it.key == session) it.copy(title = text.take(24), lastText = text) else it
        }) }
    }
}


/**
 * A conversation's name in the history: its title, else the first line of its last message (30 characters), else
 * "Chat". A conversation started from "Hand to Persona" begins with a `<persona-context … title="X">` tag, and Claude names it
 * after that message: it reads "Hand to Persona: X" instead of the raw tag.
 */
internal fun Session.conversationTitle(words: ConversationWords): String {
    title.takeIf { it.isNotBlank() && it != "Claude Code" }?.let { return handoffTitle(it, words) ?: it }
    val line = lastText?.lineSequence()?.firstOrNull { it.isNotBlank() }?.trim() ?: return words.untitled
    return handoffTitle(line, words) ?: line.take(30)
}

/**
 * The words a conversation's name falls back to: "Chat", "Hand to Persona" and "Hand to Persona: X" ([handoffNamed] has `%1$s`);
 * [background] is what the built-in background supervisor's conversation is called (#4927).
 */
internal data class ConversationWords(val untitled: String, val handoff: String, val handoffNamed: String, val background: String = "") {
    fun handoff(title: String): String = String.format(handoffNamed, title)
}

@Composable
internal fun conversationWords() = ConversationWords(stringResource(R.string.assistant_untitled),
    stringResource(R.string.assistant_handoff), stringResource(R.string.assistant_handoff_named),
    stringResource(R.string.assistant_background_supervisor))

private val handoffTag = Regex("""<persona-context\b[^>]*?\btitle="([^"]*)"""")

/** "Hand to Persona: X" for text that is a `<persona-context title="X">` tag (attributes decoded), null for any other text. */
internal fun handoffTitle(text: String, words: ConversationWords): String? {
    handoffTag.find(text)?.let { match ->
        val title = match.groupValues[1].replace("&quot;", "\"").replace("&lt;", "<").replace("&gt;", ">")
            .replace("&amp;", "&").trim()
        return if (title.isEmpty()) words.handoff else words.handoff(title)
    }
    val start = text.trimStart()
    return if (start.startsWith("<persona-context")) words.handoff else null
}

internal fun Session.conversationStatus(): Status = when (agentState) {
    AgentState.NeedsYou -> Status.NeedsYou
    AgentState.Working -> Status.Working
    AgentState.Unread -> Status.Unread
    AgentState.Idle -> Status.Idle
    AgentState.Offline -> Status.Offline
    AgentState.Unknown -> Status.Unknown
}

/**
 * Where a home-composer message written while [manager] (not the default) was on the page goes: that manager's main
 * conversation, from the snapshot only when the snapshot is that manager's, else asked of it alone ([resolve]). Never
 * the default's queue or another manager's; if it cannot be found the call fails and the words stay in the composer.
 */
internal suspend fun managerMainTarget(manager: String, snapshot: AssistantSnapshot, resolve: suspend (String) -> SessionKey): SessionKey =
    snapshot.mainSession?.takeIf { snapshot.managerId == manager } ?: resolve(manager)

/** Null targets always enter the durable main queue before any network request. */
internal suspend fun sendAssistantMessage(
    main: MainConversationSender?, sender: MessageSender, session: SessionKey?, text: String, id: String,
) {
    if (session == null || session == PendingMainConversation) {
        checkNotNull(main) { "main conversation sender is not wired" }.sendToMain(text.trim(), id) // A wiring bug, not for the user.
    } else sender.send(session, text.trim(), id)
}

internal fun DeliveryState.pendingStatus(): PendingStatus? = when (this) {
    DeliveryState.Queued, DeliveryState.WaitingForNetwork -> PendingStatus.Waiting
    DeliveryState.Sending -> PendingStatus.Sending
    DeliveryState.Unknown -> PendingStatus.Unconfirmed
    DeliveryState.Verifying -> PendingStatus.Verifying
    DeliveryState.NeedsConfirmation -> PendingStatus.NeedsReview
    DeliveryState.Rejected, DeliveryState.Refused -> PendingStatus.Refused
    DeliveryState.Sent -> null
}

internal fun assistantStatus(session: Session?, messages: List<OutboxMessage>) = StatusLineState(
    ended = session?.agentState == AgentState.Offline,
    submitting = messages.any { it.state == DeliveryState.Sending },
    unconfirmed = messages.any { it.state in setOf(DeliveryState.Unknown, DeliveryState.Verifying, DeliveryState.NeedsConfirmation) },
    blocked = session?.agentState == AgentState.NeedsYou,
    working = session?.agentState == AgentState.Working,
)

internal fun conversationTime(at: java.time.Instant?, locale: java.util.Locale,
    zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String? =
    at?.let { LocalizedFormats.format(it, LocalizedFormats.MONTH_DAY_TIME, locale, zone) }

/** The composition root adapts these options to the shared chat lane's presentation parameters. */
data class AssistantChatOptions(
    val assistant: Boolean = true,
    val embedded: Boolean = true,
    val showTopBar: Boolean = false,
    /** The conversation's manager is offline (docs/assistant/managers.md step 4): history only, this note where the composer was. */
    val readOnlyNote: String? = null,
)

/**
 * The managers as the assistant page sees them: [other] when it shows one that is not the default (its main
 * conversation is reached directly, not through this phone's main queue), [offline] the composer's words while the
 * shown one is offline (「工作 · 最后同步于 3 分钟前」), null while it can take messages. With one manager
 * both are off and the page is as before.
 */
internal data class AssistantManagers(
    val other: Boolean = false, val offline: String? = null,
    /** The empty conversation's line while it is offline: 「家里离线，能看历史，暂时不能发消息。」 */
    val offlineEmpty: String? = null,
)

internal fun assistantManagers(
    roster: persona.core.contracts.api.ManagerRoster?, choice: persona.core.contracts.api.AssistantManagerChoice.Choice,
    res: android.content.res.Resources, now: java.time.Instant = java.time.Instant.now(),
): AssistantManagers {
    if (roster == null || !roster.isMulti) return AssistantManagers()
    val shown = roster.manager(choice.shown)
    val offline = shown?.let { persona.core.designsystem.managerOfflineText(res, it, now) }
        ?.let { res.getString(R.string.assistant_manager_offline, shown.shownName, it) }
    return AssistantManagers(other = choice.manager != null, offline = offline,
        offlineEmpty = offline?.let { res.getString(R.string.assistant_manager_offline_empty, shown.shownName) })
}

/** Cancellation must reach the owning screen instead of turning into an error message. */
internal suspend inline fun <T> assistantAttempt(block: () -> T): Result<T> = try {
    Result.success(block())
} catch (cancelled: kotlinx.coroutines.CancellationException) {
    throw cancelled
} catch (error: Exception) {
    Result.failure(error)
}
