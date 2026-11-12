package persona.feature.agents

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.material3.Text
import androidx.compose.ui.draw.alpha
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.res.stringResource
import android.content.res.Resources
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.Machine
import persona.core.designsystem.*
import persona.feature.chat.UltracodeQuotaLines
import persona.feature.chat.rememberUltracodeQuota
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons
import java.net.URI

@Composable
internal fun NewAgentDialog(
    model: NewAgentViewModel, machines: List<Machine>, online: Boolean,
    /** Settings › Model providers, for a launcher the terminal can't run (#4530); null where none can be added. */
    onAddProvider: (() -> Unit)? = null,
    onDismiss: () -> Unit,
) {
    val state by model.state.collectAsStateWithLifecycle()
    var query by remember { mutableStateOf("") }
    var helping by remember { mutableStateOf<persona.core.contracts.api.AgentLauncher?>(null) }
    // Outside the list: the model list stays open whatever the form below it scrolls away.
    var picking by remember { mutableStateOf(false) }
    val list = rememberLazyListState()
    val prompt = rememberPromptFocus(state, list)
    val close = { if (!state.starting) { if (state.browsing) model.closeBrowser() else onDismiss() } }
    JSheet(onDismiss = { if (!state.starting) onDismiss() }, title = stringResource(R.string.agents_action_new_session)) {
        BackHandler(onBack = close)
        Column(Modifier.fillMaxWidth().weight(1f).imePadding()) {
            LazyColumn(Modifier.weight(1f), list, contentPadding = PaddingValues(bottom = 16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                newAgentItems(state, model, machines, query, { query = it }, close, onModels = { picking = true }, prompt, onBlocked = { helping = it })
            }
            NewAgentFooter(state, model, machines, online, Modifier.fillMaxWidth().padding(top = 8.dp))
        }
        if (picking) NewAgentModelSheet(state, model::pickModel, model::reloadModels, onDismiss = { picking = false })
        LauncherHelp(helping, state, model, onAddProvider?.let { add -> { helping = null; onDismiss(); add() } }) { helping = null }
    }
}

/** The help of the blocked launcher chosen ([launcher]); Try Anyway picks it after all. */
@Composable
private fun LauncherHelp(
    launcher: persona.core.contracts.api.AgentLauncher?, state: NewAgentState, model: NewAgentViewModel,
    onAddProvider: (() -> Unit)?, onDismiss: () -> Unit,
) {
    val block = launcher?.block ?: return
    // ponytail: a provider added in Settings goes to the primary group's hub; other groups' computers only get the words.
    val adds = onAddProvider.takeIf { persona.core.contracts.GroupKeys.namespaceOf(state.machineId) == null }
    LauncherBlockedSheet(launcher, block, adds, onTryAnyway = {
        model.selection(state.selection.copy(launcher = launcher.name))
        onDismiss()
    }, onDismiss = onDismiss)
}

/**
 * The same form as a page (`NewAgentRoute`), for callers outside Agents that fill some of it in ahead (the knowledge
 * base's "Open Agent"). Back closes the folder browser first.
 */
@Composable
internal fun NewAgentPage(
    model: NewAgentViewModel, machines: List<Machine>, online: Boolean, onBack: () -> Unit,
    /** Opens the model list at once, searching for this (captures). */
    initialModelQuery: String? = null,
    /** Settings › Model providers, for a launcher the terminal can't run (#4530). */
    onAddProvider: (() -> Unit)? = null,
) {
    val state by model.state.collectAsStateWithLifecycle()
    var query by remember { mutableStateOf("") }
    var helping by remember { mutableStateOf<persona.core.contracts.api.AgentLauncher?>(null) }
    var picking by remember(initialModelQuery) { mutableStateOf(initialModelQuery != null) }
    val list = rememberLazyListState()
    val prompt = rememberPromptFocus(state, list)
    val close = { if (!state.starting) { if (state.browsing) model.closeBrowser() else onBack() } }
    BackHandler(onBack = close)
    JListPage(title = stringResource(R.string.agents_action_new_session), onBack = close, largeTitle = false, state = list, bottomBar = {
        NewAgentFooter(state, model, machines, online, Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s))
    }) {
        newAgentItems(state, model, machines, query, { query = it }, close, onModels = { picking = true }, prompt, onBlocked = { helping = it })
    }
    if (picking) NewAgentModelSheet(state, model::pickModel, model::reloadModels, onDismiss = { picking = false },
        initialQuery = initialModelQuery.orEmpty())
    LauncherHelp(helping, state, model, onAddProvider?.let { add -> { helping = null; add() } }) { helping = null }
}

/**
 * The first message's focus: once a "Recent" card has filled the form ([NewAgentState.focusPrompt] went up), the list
 * scrolls to the message, its last item, and the cursor goes there. Opening the form again does not take it there.
 */
@Composable
private fun rememberPromptFocus(state: NewAgentState, list: LazyListState): FocusRequester {
    val requester = remember { FocusRequester() }
    var handled by remember { mutableIntStateOf(state.focusPrompt) }
    LaunchedEffect(state.focusPrompt) {
        if (state.focusPrompt == handled) return@LaunchedEffect
        handled = state.focusPrompt
        list.animateScrollToItem((list.layoutInfo.totalItemsCount - 1).coerceAtLeast(0))
        withFrameNanos { }
        runCatching { requester.requestFocus() }
    }
    return requester
}

private fun LazyListScope.newAgentItems(
    state: NewAgentState, model: NewAgentViewModel, machines: List<Machine>, query: String, onQuery: (String) -> Unit, close: () -> Unit,
    onModels: () -> Unit, prompt: FocusRequester, onBlocked: (persona.core.contracts.api.AgentLauncher) -> Unit,
) {
    val editable = !state.locked && !state.loading
    if (state.loading) item("loading") { JLinearProgress(null) }
    if (state.browsing) {
        item("browser-title") {
            JSectionHeader(stringResource(R.string.agents_new_choose_directory), action = { JTextButton(stringResource(R.string.agents_new_back), close, enabled = !state.starting) })
            Text(state.browser?.path ?: state.browsePath, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
        state.browser?.let { directory ->
            if (directory.parent.isNotBlank() && directory.parent != directory.path) item("parent") {
                JTextButton(stringResource(R.string.agents_new_parent), { model.browse(directory.parent) }, enabled = !state.loading)
            }
            if (directory.entries.isEmpty()) item("empty-directory") {
                Text(stringResource(R.string.agents_new_no_subdirectories), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
            }
            itemsIndexed(directory.entries, key = { _, entry -> entry.path }) { index, entry ->
                JGroupItem(GroupPosition.of(index, directory.entries.size)) {
                    JRow(entry.name, subtitle = if (entry.isGit) stringResource(R.string.agents_new_git_repo) else null, leadingIcon = PhIcons.FolderSimple,
                        enabled = !state.loading, onClick = { model.browse(entry.path) })
                }
            }
        }
        if (!state.loading && state.browser == null) item("retry-browser") {
            JButton(stringResource(R.string.agents_new_retry_open), { model.browse(state.browsePath) }, kind = JButtonKind.Secondary)
        }
        return
    }
    recentSetupItems(state, machines, model)
    val space = state.space
    // Existing spaces keep their machine and directory fixed.
    if (space != null) item("space") {
        val machine = machines.firstOrNull { it.id == space.machineId }
        JSectionHeader(stringResource(R.string.agents_new_tab_in, space.name))
        JRow(machine?.displayName ?: space.machineId, value = stringResource(if (machine?.online == true) R.string.agents_new_online else R.string.agents_new_offline), leadingIcon = PhIcons.Laptop)
        JRow(stringResource(R.string.agents_directory), subtitle = space.cwd, leadingIcon = PhIcons.FolderSimple)
        if (machine?.online != true) Text(stringResource(R.string.agents_new_space_offline), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
    } else item("machines") {
        JSectionHeader(stringResource(R.string.agents_new_machine))
        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            machines.forEach { machine ->
                JChip(machine.displayName, selected = machine.id == state.machineId, icon = PhIcons.Laptop,
                    status = if (machine.online) Status.Idle else Status.Offline,
                    onClick = if (editable && machine.online) ({ onQuery(""); model.selectMachine(machine.id) }) else null)
            }
        }
        if (machines.none { it.online }) Text(stringResource(R.string.agents_new_no_online), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        state.prefillOffline?.let {
            Text(stringResource(R.string.agents_new_prefill_offline, it), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        }
    }
    if (state.error != null && !state.loading && state.pending == null && state.machineId.isNotBlank()) item("reload") {
        JButton(stringResource(R.string.agents_new_reload), { model.selectMachine(state.machineId) }, enabled = !state.locked, kind = JButtonKind.Secondary)
    }
    if (space == null) {
        item("directory") {
            JSectionHeader(stringResource(R.string.agents_directory))
            JGroupItem(GroupPosition.Single) {
                JRow(stringResource(R.string.agents_new_choose_directory), subtitle = state.selection.cwd.takeIf { it.isNotBlank() }, leadingIcon = PhIcons.FolderSimple,
                    enabled = editable && state.machineId.isNotBlank(), onClick = { model.browse() })
            }
        }
        itemsIndexed(state.recent, key = { _, recent -> "recent:${recent.path}" }) { index, recent ->
            JGroupItem(GroupPosition.of(index, state.recent.size)) {
                JRow(recent.name.ifEmpty { recent.path.substringAfterLast('/') }, subtitle = recent.path,
                    leading = { JRadio(state.selection.cwd == recent.path, null, enabled = editable) }, enabled = editable,
                    onClick = { model.selection(state.selection.copy(cwd = recent.path)) }, chevron = false)
            }
        }
    }
    // A branch and a checkout of its own (#4532, iOS NewAgentWorktreeSection), for a folder in a Git repository.
    state.repo?.takeIf { state.worktreeOffered }?.let { repo -> item("worktree") {
        val suggested = AgentWorktree.branch(AgentWorktree.nameHint(state.prompt, state.selection.cwd))
        JSectionHeader(stringResource(R.string.agents_worktree_title))
        JGroupItem(GroupPosition.of(0, if (state.inWorktree) 2 else 1)) {
            JSwitchRow(stringResource(R.string.agents_worktree_switch), state.inWorktree, model::inWorktree,
                leadingIcon = PersonaIcons.gitBranch, enabled = editable)
        }
        if (state.inWorktree) JGroupItem(GroupPosition.of(1, 2)) {
            JTextField(state.worktreeBranch, model::worktreeBranch, placeholder = suggested,
                label = stringResource(R.string.agents_worktree_branch), singleLine = true, enabled = editable,
                modifier = Modifier.fillMaxWidth().padding(Tokens.Space.s))
        }
        Text(stringResource(R.string.agents_worktree_footer), Modifier.padding(top = Tokens.Space.xs),
            style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
        val path = AgentWorktree.checkout(repo, state.worktreeBranch.trim().ifEmpty { suggested })
        if (state.inWorktree && path.isNotEmpty()) {
            val home = machines.firstOrNull { it.id == state.machineId }?.home
            Text(stringResource(R.string.agents_worktree_checks_out, home?.takeIf { it.isNotEmpty() && path.startsWith("$it/") }
                ?.let { "~" + path.removePrefix(it) } ?: path), maxLines = 2,
                style = PersonaTheme.type.codeSmall, color = PersonaTheme.palette.ink2)
        }
    } }
    item("agent-types-title") { JSectionHeader(stringResource(R.string.agents_new_agent_type)) }
    val types = state.launchers.map { it.agentType }.distinct()
    val selectedType = state.agentType.takeIf { state.following || state.selection.launcher.isNotEmpty() }
    itemsIndexed(types, key = { _, type -> "type:$type" }) { index, type ->
        JGroupItem(GroupPosition.of(index, types.size)) {
            JRow(type.agentLabel(), leading = { JRadio(selectedType == type, null, enabled = editable) }, enabled = editable,
                // The type the configuration already starts keeps following it; another type picks its first launcher.
                onClick = { if (!(state.following && state.agentType == type))
                    model.selection(state.selection.copy(launcher = initialLauncher(null, state.launchers.filter { it.agentType == type }))) },
                chevron = false)
        }
    }
    item("launcher-search") {
        JSectionHeader(stringResource(if (state.follow != null) R.string.agents_new_launcher_this_time else R.string.agents_new_launcher))
        JSearchField(query, { if (editable) onQuery(it) }, stringResource(R.string.agents_new_search_launcher))
        if (!state.loading && state.launchers.isEmpty()) Text(stringResource(R.string.agents_new_no_launchers), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
    }
    val launchers = state.launchers.filter { launcher ->
        launcher.agentType == (selectedType ?: "claude") &&
            (query.isBlank() || launcher.name.contains(query, true) || launcher.model.orEmpty().contains(query, true))
    }
    // Models are configured by use (Settings › Model configuration): a new agent follows it unless a launcher is picked for this start.
    val follow = state.follow
    val rows = launchers.size + if (follow != null) 1 else 0
    if (follow != null) item("launcher:follow") {
        JGroupItem(GroupPosition.of(0, rows)) {
            JRow(stringResource(R.string.agents_new_follow_config), subtitle = followSummary(agentResources(), state), subtitleLines = 3,
                leading = { JRadio(state.following, null, enabled = editable) }, enabled = editable,
                onClick = { model.selection(state.selection.copy(launcher = "")) }, chevron = false)
        }
    }
    itemsIndexed(launchers, key = { _, launcher -> "launcher:${launcher.name}" }) { index, launcher ->
        val host = launcher.baseUrl?.let { runCatching { URI(it).host }.getOrNull() }
        // Blocked on this computer (#4530, iOS): listed grayed with why; choosing it opens its help instead of picking it.
        val block = launcher.block
        val reason = block?.let { stringResource(it.reason) }
        JGroupItem(GroupPosition.of(index + rows - launchers.size, rows)) {
            JRow(launcher.name, Modifier.alpha(if (block != null && state.selection.launcher != launcher.name) 0.5f else 1f),
                subtitle = listOfNotNull(reason, launcher.agentType.agentLabel(), launcher.model, host).joinToString("\n"),
                subtitleLines = 4, leading = { JRadio(state.selection.launcher == launcher.name, null, enabled = editable) },
                enabled = editable, onClick = {
                    if (block != null && state.selection.launcher != launcher.name) onBlocked(launcher)
                    else model.selection(state.selection.copy(launcher = launcher.name))
                }, chevron = false)
        }
    }
    newAgentModelItems(state, editable, onModels, model::reloadModels)
    if (state.agentType == "claude") item("claude-options") {
        ChoiceMenu(stringResource(R.string.agents_new_permission_mode), state.selection.mode, permissionModes, editable) { model.selection(state.selection.copy(mode = it)) }
        if (state.selection.mode == "bypassPermissions") Text(stringResource(R.string.agents_new_bypass_warning), color = PersonaTheme.palette.cautionText, style = PersonaTheme.type.caption)
        // Only the levels the model that will run takes; none: it has no effort setting.
        val efforts = state.modelEfforts
        if (efforts?.isEmpty() == true) JRow(stringResource(R.string.agents_new_effort), subtitle = stringResource(R.string.agents_new_no_effort), enabled = false)
        else ChoiceMenu(stringResource(R.string.agents_new_effort), state.selection.effort, effortChoices(efforts), editable) { model.selection(state.selection.copy(effort = it)) }
        // Claude Code's ultracode for this start (#582, iOS UltracodeStartRow): off each time, never saved; a known CLI
        // too old for it says so and can't be switched on.
        val shown = NewAgentUltracode.offered(LocalHubFeatures.current, state.agentType, efforts)
        val version = machines.firstOrNull { it.id == state.machineId }?.claudeVersion?.trim()
        val blocked = version?.takeIf { shown && NewAgentUltracode.tooOld(it) }
        LaunchedEffect(shown, blocked) { if (!shown || blocked != null) model.ultracode(false) }
        if (shown) JSwitchRow("ultracode", state.ultracode && blocked == null, model::ultracode, // i18n-ignore: a proper name, never translated
            subtitle = blocked?.let { stringResource(R.string.agents_new_ultracode_old, it, NewAgentUltracode.MIN_VERSION.joinToString(".")) }
                ?: stringResource(R.string.agents_new_ultracode_subtitle),
            enabled = editable && blocked == null, subtitleLines = Int.MAX_VALUE)
        // The Claude subscription's use on this computer's hub (iOS UltracodeStartRow); no warning under a CLI too old for it.
        val read = model.usage?.let { usage -> state.machineId.takeIf { shown && it.isNotEmpty() }?.let { id -> suspend { usage(id) } } }
        rememberUltracodeQuota(read, state.machineId)?.let { quota ->
            UltracodeQuotaLines(quota, blocked == null, Modifier.padding(horizontal = rowPadding).padding(bottom = Tokens.Space.m))
        }
    }
    item("prompt") {
        JTextField(state.prompt, model::prompt, enabled = !state.locked,
            label = stringResource(if (state.agentType == "codex") R.string.agents_new_first_message_codex else R.string.agents_new_first_message),
            minLines = 3, maxLines = 8, modifier = Modifier.fillMaxWidth().focusRequester(prompt))
    }
}

/** What following the configuration starts: the configured provider and model, and whether this machine has that launcher. */
private fun followSummary(res: Resources, state: NewAgentState): String {
    val detail = state.follow ?: return ""
    val summary = detail.purpose.summaryParts(detail.providers).joinToString(res.getString(R.string.agents_new_summary_separator))
        .ifEmpty { res.getString(R.string.agents_new_not_configured) }
    val provider = detail.purpose.effective.provider.orEmpty()
    val missing = provider.startsWith("launcher.") && state.launchers.isNotEmpty() && state.configuredLauncher == null
    val text = if (missing) res.getString(R.string.agents_new_launcher_missing, summary) else summary
    // The configured launcher is blocked here (#4530, iOS LauncherBlockLine): said under it; Start stays possible.
    return state.configuredLauncher?.block?.let { text + "\n" + res.getString(it.reason) } ?: text
}

@Composable
private fun NewAgentFooter(state: NewAgentState, model: NewAgentViewModel, machines: List<Machine>, online: Boolean, modifier: Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(8.dp)) {
        if (state.pending != null) PendingBadge(if (state.starting) PendingStatus.Verifying else PendingStatus.Unconfirmed)
        state.error?.let { Text(it.resolve(), color = PersonaTheme.palette.ink2, style = PersonaTheme.type.callout) }
        if (!online) Text(stringResource(R.string.agents_new_offline_footer), color = PersonaTheme.palette.ink2, style = PersonaTheme.type.callout)
        if (state.browsing) {
            JButton(stringResource(R.string.agents_new_use_directory), { model.closeBrowser(use = true) }, enabled = state.browser != null && !state.loading, modifier = Modifier.fillMaxWidth())
        } else {
            JButton(stringResource(if (state.pending != null) R.string.agents_new_retry_check else R.string.agents_new_start), model::submit, loading = state.starting,
                enabled = online && !state.starting && !state.loading && state.initialized && (state.pending != null || startable(state, machines)),
                modifier = Modifier.fillMaxWidth())
        }
    }
}

/** A folder, a launcher or the configuration to follow, an online machine, and Codex's first message. */
private fun startable(state: NewAgentState, machines: List<Machine>): Boolean =
    state.selection.cwd.isNotBlank() && (state.selection.launcher.isNotBlank() || state.following) &&
        machines.any { it.id == state.machineId && it.online } && (state.agentType != "codex" || state.prompt.isNotBlank())

internal fun String.agentLabel() = when (this) { "claude" -> "Claude Code"; "codex" -> "Codex"; "traex" -> "TRAEX"; else -> this }
internal val permissionModes = listOf("" to R.string.agents_mode_follow, "default" to R.string.agents_mode_default,
    "acceptEdits" to R.string.agents_mode_accept_edits, "plan" to R.string.agents_mode_plan, "auto" to R.string.agents_mode_auto,
    "bypassPermissions" to R.string.agents_mode_bypass)
internal val effortLevels = listOf("" to R.string.agents_mode_follow, "low" to R.string.agents_effort_low, "medium" to R.string.agents_effort_medium,
    "high" to R.string.agents_effort_high, "xhigh" to R.string.agents_effort_xhigh, "max" to R.string.agents_effort_max)

@Composable
private fun ChoiceMenu(title: String, value: String, options: List<Pair<String, Int>>, enabled: Boolean, onSelect: (String) -> Unit) {
    var expanded by remember { mutableStateOf(false) }
    JRow(title, value = options.firstOrNull { it.first == value }?.second?.let { stringResource(it) } ?: value, enabled = enabled, onClick = { expanded = true })
    if (expanded) JActionSheet(options.map { (id, label) -> JAction(stringResource(label), checked = id == value, onClick = { onSelect(id) }) },
        { expanded = false }, title = title)
}
