package persona.feature.chat

import persona.core.designsystem.text
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.sort.SessionRename
import persona.core.domain.sort.canAttach
import persona.core.domain.sort.canInterrupt
import persona.core.domain.sort.canTerminal
import persona.core.domain.sort.closeConfirmation
import persona.core.domain.sort.deleteConfirmation
import persona.core.domain.sort.isTern
import persona.core.domain.sort.resumeCommand
import persona.core.domain.sort.slotLabel
import persona.core.domain.sort.source
import persona.core.domain.sort.sourceSubtitle

@Composable
internal fun ChatHeader(
    session: Session?, machine: Machine?, sessions: List<Session>, state: ChatChromeState,
    connection: ConnectionState?, readOnly: Boolean, onBack: () -> Unit,
    onOpenSession: ((SessionKey) -> Unit)?, onConnectionDetails: (() -> Unit)?,
    onOverlay: (Boolean) -> Unit,
    onFiles: (() -> Unit)? = null, onGit: (() -> Unit)? = null, gitLabel: String = "Git",
    onSearch: (() -> Unit)? = null,
    statusState: StatusLineState = StatusLineState(), onDetails: () -> Unit = {},
    onOpenTodo: ((String) -> Unit)? = null,
    agentShares: (() -> AgentShares)? = null,
    worktrees: (() -> persona.core.contracts.api.AgentCreationService?)? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    var menu by remember { mutableStateOf(false) }
    var sharing by remember(session?.key, agentShares) { mutableStateOf<AgentShares?>(null) }
    var dialog by rememberSaveable { mutableStateOf<String?>(null) }
    var title by rememberSaveable { mutableStateOf("") }
    var tabName by rememberSaveable { mutableStateOf("") }
    val copy = rememberCopy()
    val peers = remember(session, sessions) { spaceCompanions(session, sessions) }
    val places = remember(session, peers) { companionPlaces(session, peers) }
    val children = session?.subagents.orEmpty().filter { it.running && it.sessionId.isNotBlank() }
    val linkTitle = connectionTitle(connection)
    val online = connection == null || connection == ConnectionState.Online
    val canAct = state.available && online && machine?.online != false && !readOnly && session != null
    // Renaming changes the terminal tab and Claude's /rename: a session without a terminal has neither.
    val canRename = state.canRename && online && machine?.online != false && !readOnly && session != null && session.canTerminal
    LaunchedEffect(session?.key, menu) { if (session != null && menu) state.loadMute(session) }
    var modeOverlay by remember { mutableStateOf(false) }
    LaunchedEffect(menu, dialog, sharing, modeOverlay) { onOverlay(menu || dialog != null || sharing != null || modeOverlay) }
    DisposableEffect(Unit) { onDispose { onOverlay(false) } }
    val line = resolveStatusLine(statusState)
    val status = line?.status ?: Status.entries.firstOrNull { it.name == session?.agentState?.name } ?: Status.Unknown
    // How long it has worked this turn, in minutes (the work line in the chat has the seconds): read again every 30 s.
    val since = session?.takeIf { status == Status.Working && it.backgroundWorking != true }?.stateSince?.let(::parseTime)
    val now by produceState(java.time.Instant.now(), since) {
        while (since != null) { kotlinx.coroutines.delay(30_000); value = java.time.Instant.now() }
    }
    val worked = workedMinutes(since, now)?.let { m ->
        if (m < 60) stringResource(R.string.chat_chrome_worked_minutes, m) else stringResource(R.string.chat_chrome_worked_hours, m / 60, m % 60)
    }
    // While the hub is out of reach the status line under the title says so ("Waiting for network" / "Connecting…" / "Updating…") instead of
    // the session's state, which may be stale; it is back the moment the connection is.
    ChatTopBar(
        title = session?.displayTitle ?: stringResource(R.string.chat_chrome_title_default), onBack = onBack,
        status = if (linkTitle != null) null else status,
        statusText = linkTitle ?: when (line?.kind) {
            StatusLineKind.Ended -> stringResource(if (statusState.replaced) R.string.chat_chrome_status_replaced else R.string.chat_chrome_status_ended)
            StatusLineKind.MachineOffline -> stringResource(R.string.chat_chrome_status_machine_offline)
            StatusLineKind.Submitting -> stringResource(R.string.chat_chrome_status_submitting)
            StatusLineKind.Unconfirmed -> stringResource(R.string.chat_chrome_status_unconfirmed)
            StatusLineKind.NeedsYou -> stringResource(R.string.chat_chrome_status_needs_you)
            StatusLineKind.Stopping -> stringResource(R.string.chat_chrome_status_stopping)
            // As iOS (#4339 C4): Seen and the other quiet states say nothing in words, the dot carries them; a turn
            // that has run a minute says for how long ("Working 22 min").
            else -> when {
                status in QuietStatuses -> null
                worked != null -> "${status.localizedLabel} $worked"
                else -> status.localizedLabel
            }
        },
        source = session?.let { chatPlace(it, machine?.displayName, it.sourceSubtitle?.text()) },
        sourceShort = session?.let { chatPlace(it, machine?.displayName, it.sourceSubtitle?.text(), short = true) },
        busy = connection in setOf(ConnectionState.Connecting, ConnectionState.Updating),
        onTitleClick = onDetails,
        agentMark = session?.agentMark,
    ) {
        actions()
        // The permission mode, always in view and changed here (#3957, iOS #3719).
        if (session != null && SessionModes.showsInHeader(session)) HeaderModeButton(session, state, online && !readOnly) { modeOverlay = it }
        JIconButton(PhIcons.DotsThree, stringResource(R.string.chat_chrome_more), { menu = true })
    }
    val resumeCopied = stringResource(R.string.chat_chrome_resume_copied)
    if (menu) ChatActionsSheet(title = stringResource(R.string.chat_chrome_actions_title), onDismiss = { menu = false }, actions = buildList {
        session?.todo?.let { todo ->
            add(JAction(stringResource(R.string.chat_chrome_todo, todo.title), PhIcons.ListChecks) { onOpenTodo?.invoke(todo.id) })
        }
        onSearch?.let { add(JAction(stringResource(R.string.chat_chrome_search), PhIcons.MagnifyingGlass, onClick = it)) }
        onFiles?.let { add(JAction(stringResource(R.string.chat_chrome_files), PhIcons.FolderSimple, onClick = it)) }
        onGit?.let { add(JAction(gitLabel, onClick = it)) }
        add(JAction(stringResource(R.string.chat_chrome_details), onClick = onDetails))
        add(JAction(stringResource(R.string.chat_chrome_info), PhIcons.Info, enabled = session != null) { dialog = "info" })
        if (session?.assistant == false) {
            val unavailable = stringResource(R.string.chat_agent_share_authority)
            add(JAction(stringResource(R.string.chat_agent_share_title), PhIcons.ShareNetwork, enabled = agentShares != null && online) {
                try { sharing = agentShares?.invoke() } catch (_: Exception) { state.notice = unavailable }
            })
        }
        // A Codex app-server thread the phone may join live (iOS "Join live"); joined, it can answer its approvals.
        session?.takeIf { it.canAttach }?.let { target ->
            add(JAction(stringResource(R.string.chat_chrome_attach), PhIcons.PlugsConnected, enabled = canAct) { state.run(target, ChatAction.Attach) })
        }
        add(JAction(pluralStringResource(R.plurals.chat_chrome_peers, peers.size, peers.size), enabled = session != null) { dialog = "peers" })
        add(JAction(stringResource(R.string.chat_chrome_switch_session), enabled = session != null) { dialog = "peers" })
        children.forEach { child -> add(JAction(stringResource(R.string.chat_chrome_subagent, child.description?.takeIf(String::isNotBlank) ?: child.type),
            enabled = onOpenSession != null) { onOpenSession?.invoke(SessionKey(session!!.machineId, child.sessionId)) }) }
        if (linkTitle != null) add(JAction(linkTitle) { dialog = "connection" })
        add(JAction(stringResource(R.string.chat_chrome_interrupt), enabled = canAct && session?.status in listOf("working", "blocked") && session?.canInterrupt != false) {
            state.run(session!!, ChatAction.Interrupt)
        })
        add(JAction(stringResource(R.string.chat_chrome_rename), PhIcons.NotePencil, enabled = canRename) {
            title = session!!.title; tabName = session.slotName.orEmpty(); dialog = "rename"
        })
        add(JAction(stringResource(if (state.muted == true) R.string.chat_chrome_unmute else R.string.chat_chrome_mute), enabled = canAct && state.muted != null) {
            state.run(session!!, ChatAction.Mute(state.muted != true))
        })
        // Only a session in a terminal resumes there; a Codex thread by its own thread id.
        session?.resumeCommand()?.let { command ->
            add(JAction(stringResource(R.string.chat_chrome_copy_resume), enabled = !readOnly) {
                copy(command, resumeCopied)
            })
        }
        // Persona's conversations are not agents in a pane, and a session without a terminal has no pane to close.
        if (session?.assistant != true && session?.canTerminal != false)
            add(JAction(stringResource(R.string.chat_chrome_close_agent), destructive = true, enabled = canAct && session?.isLive == true) { dialog = "close" })
        // Only one that is not running (core:contracts Session.canDelete); the same words as the Agents list's.
        if (session?.canDelete == true) add(JAction(stringResource(R.string.chat_chrome_delete_chat), destructive = true, enabled = canAct) { dialog = "delete" })
    })
    sharing?.let { api -> session?.let { AgentShareSheet(it.key, api, onDismiss = { sharing = null }) } }
    val feedback = LocalAppFeedback.current
    LaunchedEffect(state.notice) {
        state.notice?.let { if (feedback?.notice(it) == true && state.pending == null) state.notice = null }
    }
    state.notice?.let { notice ->
        JNotice(notice, actionLabel = stringResource(if (state.pending != null && session != null) R.string.chat_chrome_notice_verify else R.string.chat_chrome_notice_dismiss), onAction = {
            if (state.pending != null && session != null) {
                if (!state.busy && online) state.run(session, state.pending!!, verify = true)
            } else state.notice = null
        })
    }
    when (dialog) {
        "rename" -> JDialog(onDismiss = { dialog = null }, title = stringResource(R.string.chat_chrome_rename),
            text = stringResource(if (session?.isTern == true) R.string.chat_chrome_rename_text_tern else R.string.chat_chrome_rename_text),
            confirm = stringResource(R.string.chat_chrome_save) to { dialog = null; state.rename(session!!, title, tabName) },
            confirmEnabled = canRename && session?.let { SessionRename.changes(it, title, tabName).changed } == true,
            content = {
                Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                    if (!session?.paneId.isNullOrBlank()) JTextField(tabName, { tabName = it }, singleLine = true,
                        label = stringResource(if (session?.isTern == true) R.string.chat_chrome_tab_name_tern else R.string.chat_chrome_tab_name))
                    JTextField(title, { title = it }, label = stringResource(R.string.chat_chrome_session_name), singleLine = true)
                }
            })
        // The same words as the Agents list's long press and swipe (core:domain closeConfirmation).
        // Keep or remove the worktree Persona made for it, when it worked in one (#4532); removed once it is gone.
        "close", "delete" -> session?.let { target ->
            val closing = dialog == "close"
            val confirmation = if (closing) target.closeConfirmation(machine?.displayName) else target.deleteConfirmation(machine?.displayName)
            val service = remember(worktrees) { worktrees?.invoke() }
            val rawMachine = persona.core.contracts.GroupKeys.raw(target.machineId)
            val worktree = rememberOurWorktree(service, rawMachine, target.cwd, machine?.online == true)
            var cleanup by remember(target.key) { mutableStateOf(WorktreeCleanup()) }
            val res = androidx.compose.ui.platform.LocalResources.current
            JDialog(onDismiss = { dialog = null }, title = confirmation.title.text(), text = confirmation.message.text(),
                confirm = stringResource(if (closing) R.string.chat_chrome_close_agent else R.string.chat_chrome_delete) to {
                    dialog = null
                    state.run(target, if (closing) ChatAction.Close else ChatAction.Delete) { done -> if (done && service != null && worktree != null)
                        WorktreeCleanups.cleanUp(service, rawMachine, worktree, cleanup, feedback, res) }
                }, destructive = true, confirmEnabled = canAct && WorktreeCleanups.ready(worktree, cleanup),
                content = worktree?.let { st -> { WorktreeCleanupSection(st, cleanup, { cleanup = it }, machine?.home) } })
        }
        "info" -> session?.let { target -> ChatSessionInfoSheet(target, machine, state, onOpenTodo = onOpenTodo, onDismiss = { dialog = null }) }
        "peers" -> JSheet(onDismiss = { dialog = null }, title = stringResource(R.string.chat_chrome_peers_title)) {
            if (peers.isEmpty()) JEmptyState(stringResource(R.string.chat_chrome_peers_empty_title), stringResource(R.string.chat_chrome_peers_empty_text))
            LazyColumn(Modifier.fillMaxWidth().heightIn(max = 400.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
                items(peers, key = { it.key.path }) { peer ->
                    val place = places[peer.key]
                    val where = peer.slotName ?: peer.agentLabel
                    // the launcher says so; the panes it split off sit one step in, as on the Agents list (#2515)
                    JRow(peer.displayTitle,
                        modifier = if (place == CompanionPlace.Branch) Modifier.padding(start = Tokens.Space.l) else Modifier,
                        subtitle = if (place == CompanionPlace.Launcher) stringResource(R.string.chat_tabs_launcher_subtitle, where) else where,
                        leading = { JStatusDot(Status.entries.firstOrNull { it.name == peer.agentState.name } ?: Status.Unknown) },
                        enabled = onOpenSession != null, onClick = { dialog = null; onOpenSession?.invoke(peer.key) })
                }
            }
        }
        "connection" -> JDialog(onDismiss = { dialog = null }, title = linkTitle ?: stringResource(R.string.chat_chrome_connected), text = stringResource(when (connection) {
            ConnectionState.CertificateMismatch -> R.string.chat_chrome_conn_cert
            ConnectionState.Revoked -> R.string.chat_chrome_conn_revoked
            ConnectionState.NeedsLocalNetworkPermission -> R.string.chat_chrome_conn_local_network
            else -> R.string.chat_chrome_conn_other
        }), confirm = stringResource(if (onConnectionDetails != null) R.string.chat_chrome_conn_details else R.string.chat_chrome_got_it) to { dialog = null; onConnectionDetails?.invoke() }, dismiss = null)
    }
}

/** A scrollable sheet until JActionSheet supports long action lists. */
@Composable
internal fun ChatActionsSheet(actions: List<JAction>, onDismiss: () -> Unit, title: String) {
    // Actions without an icon keep the icon's place (its 22dp and the 16dp after it, less the 14dp JRow puts after a
    // leading slot), so every label starts in one column.
    val iconColumn = actions.any { it.icon != null }
    JSheet(onDismiss = onDismiss, title = title) {
        LazyColumn(Modifier.fillMaxWidth(), contentPadding = PaddingValues(bottom = 16.dp)) {
            items(actions) { action ->
                JRow(action.label, leadingIcon = action.icon, enabled = action.enabled, destructive = action.destructive,
                    leading = if (action.icon == null && iconColumn) ({ Spacer(Modifier.width(24.dp)) }) else null,
                    chevron = false, onClick = { onDismiss(); action.onClick() })
            }
        }
    }
}

/** The states the line under the title names by its dot only (iOS `ChatView.quiet`). */
internal val QuietStatuses = setOf(Status.Idle, Status.Offline, Status.Unknown)

/**
 * Where a conversation runs, as the line under its title says it (iOS `devSubtitle`, #4339 C4): its [source] first (a
 * Codex app-server, Codex history or an agent runtime, `sourceSubtitle` in words), then the computer and "space › tab".
 * Never the working directory, which DESIGN_SYSTEM.md 5.6 keeps off primary screens; null when there is nothing to say.
 */
internal fun chatPlace(session: Session, machine: String?, source: String? = null, short: Boolean = false): String? {
    val place = listOfNotNull(session.spaceName?.takeIf(String::isNotBlank), session.slotLabel).joinToString(" › ")
    // [short], for a line too narrow for all of it: the place without the computer, or the computer when there is no place.
    val computer = machine?.takeIf { it.isNotBlank() && (!short || place.isEmpty()) }
    return listOfNotNull(source?.takeIf(String::isNotBlank), computer, place.takeIf(String::isNotEmpty))
        .joinToString(" · ").takeIf(String::isNotEmpty)
}

/** Whole minutes from [since] to [now], null under one minute or without a start. */
internal fun workedMinutes(since: java.time.Instant?, now: java.time.Instant): Long? =
    since?.let { java.time.Duration.between(it, now).toMinutes() }?.takeIf { it >= 1 }
