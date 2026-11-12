package persona.feature.assistant.settings

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import persona.core.contracts.Machine
import persona.core.contracts.SessionKey
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.BackgroundSupervisor
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PersonaIcons
import persona.core.designsystem.icons.PhIcons
import persona.feature.assistant.LocalAssistantHost
import persona.feature.assistant.R
import persona.feature.assistant.conversationTime
import persona.feature.assistant.proactive.AssistantProactiveScreen
import kotlinx.coroutines.launch

/** Debug builds open one part of the page directly for captures (`debug.assistantSettings`); release never sets it. */
object AssistantSettingsPreview {
    @Volatile var open: String? = null
}

private enum class SettingsPart { Home, Notes, Memory, MemoryFile, Proactive, Skills }
private enum class SettingsSheet { Machine, Zone }

/**
 * Where the assistant runs (machine, directory, and what lives where; its model is Model configuration › Main chat, #1291), what it knows about the user
 * ("About me", memory) and its time zone. Opened in place from the Persona tab; sub-pages keep their own back handling.
 */
@Composable
internal fun AssistantSettingsScreen(
    management: AssistantManagement,
    machines: List<Machine>,
    onBack: () -> Unit,
    onMoved: suspend () -> Unit = {},
    /** Shown in place of another page (the Persona tab) rather than as a navigation destination: back closes it here. */
    inPlace: Boolean = false,
    /** Opens an assistant conversation (one a run of "Proactive" started, or a follow-up continues). */
    onOpenConversation: (SessionKey) -> Unit = {},
    /** Opens Settings › Models for a purpose (a task's model); null leaves those rows out. */
    onModelConfiguration: ((String, String) -> Unit)? = null,
    initialManager: String? = null,
) {
    // The current assistant's profile (docs/assistant/managers.md "Clients"); another one current, the page reads that one's.
    val owner = initialManager?.takeIf(String::isNotBlank) ?: persona.core.designsystem.currentAssistant().id
    key(management, owner) {
        val scoped = remember(management, owner) { management.forManager(owner) }
        AssistantSettingsDetails(scoped, machines, onBack, onMoved, inPlace, onOpenConversation, onModelConfiguration, owner)
    }
}

@Composable
private fun AssistantSettingsDetails(
    management: AssistantManagement, machines: List<Machine>, onBack: () -> Unit, onMoved: suspend () -> Unit,
    inPlace: Boolean, onOpenConversation: (SessionKey) -> Unit, onModelConfiguration: ((String, String) -> Unit)?,
    owner: String,
) {
    val context = LocalContext.current
    val moved by rememberUpdatedState(onMoved)
    // The outbox that binds the main conversation: told when a move clears it.
    val sender by rememberUpdatedState(LocalAssistantHost.current?.mainSender)
    val controller = remember(management) {
        AssistantSettingsState(management.settingsApi(), StoredTimezoneChoice(context), mainChanged = { sender?.mainChanged() }) { moved() }
    }
    val state by controller.state.collectAsStateWithLifecycle()
    val scope = rememberCoroutineScope()
    val feedback = LocalAppFeedback.current
    val preview = remember { AssistantSettingsPreview.open.also { AssistantSettingsPreview.open = null } }
    var part by rememberSaveable { mutableStateOf(when (preview) {
        "notes" -> SettingsPart.Notes
        "memory", "memory-file", "memory-edit", "memory-delete" -> SettingsPart.Memory
        "proactive" -> SettingsPart.Proactive
        "skills" -> SettingsPart.Skills
        else -> SettingsPart.Home
    }) }
    var memoryName by rememberSaveable { mutableStateOf<String?>(null) }
    var sheet by remember { mutableStateOf(when (preview) {
        "machine" -> SettingsSheet.Machine
        "timezone" -> SettingsSheet.Zone
        else -> null
    }) }
    LaunchedEffect(controller) {
        controller.load()
        controller.loadMemory()
    }
    // Read each time the page opens, like iOS: a skill put on the machine shows up without a refresh.
    LaunchedEffect(part == SettingsPart.Skills) {
        if (part == SettingsPart.Skills) controller.loadSkills()
    }
    // Captures of a memory file and of the move warning wait for the data they show.
    LaunchedEffect(state.memory.entries, state.config, machines) {
        if (preview in setOf("memory-file", "memory-edit", "memory-delete") && memoryName == null) state.memory.entries?.firstOrNull()?.let {
            memoryName = it.file.name; part = SettingsPart.MemoryFile
        }
        if (preview == "move" && state.move == null && state.config != null) {
            machines.firstOrNull { it.id != state.config?.machineId }?.let { controller.requestMove(it, machines) }
        }
    }
    // Results go to the shell's notice when there is one, else stay on the page until the next action.
    val resources = LocalResources.current
    LaunchedEffect(state.notice) {
        val notice = state.notice ?: return@LaunchedEffect
        if (feedback?.notice(notice.text.resolve(resources)) == true) controller.clearNotice()
    }
    BackHandler(part != SettingsPart.Home || inPlace) {
        when (part) {
            SettingsPart.Home -> onBack()
            SettingsPart.MemoryFile -> part = SettingsPart.Memory
            else -> part = SettingsPart.Home
        }
    }
    // With the shell's notice the page draws none of its own (not even for the frame before it is handed over).
    val shown = if (feedback == null) state else state.copy(notice = null)
    val machine = machines.firstOrNull { it.id == state.config?.machineId }
    val machineName = machine?.displayName ?: state.config?.machineId?.takeIf { it.isNotBlank() } ?: stringResource(R.string.assistant_settings_machine_unknown)
    fun refresh() {
        scope.launch {
            controller.load()
            controller.loadMemory()
        }
    }
    when (part) {
        SettingsPart.Home -> SettingsHome(shown, machine, machineName, controller.phoneZone, onBack, ::refresh,
            open = { part = it }, sheet = { sheet = it }, follow = { scope.launch { controller.setFollowPhone(it) } },
            quietTasks = { scope.launch { controller.setMainTasksQuiet(it) } },
            foldBackground = { scope.launch { controller.setMainBackgroundFolded(it) } },
            superviseOwn = { scope.launch { controller.setMainSupervisesOwnTasks(it) } },
            supervisorModel = onModelConfiguration?.let { open -> { open(BackgroundSupervisor.PURPOSE, owner) } },
            owner = owner, permission = { scope.launch { controller.setDefaultPermission(it) } })
        SettingsPart.Notes -> NotesPart(shown, onBack = { part = SettingsPart.Home }) { text -> controller.saveNotes(text) }
        SettingsPart.Memory -> MemoryPart(shown, machine, machineName, onBack = { part = SettingsPart.Home },
            onRefresh = { scope.launch { controller.loadMemory() } },
            onOpen = { memoryName = it; part = SettingsPart.MemoryFile })
        SettingsPart.MemoryFile -> MemoryFilePart(shown, memoryName, preview, onBack = { part = SettingsPart.Memory },
            onSave = { name, content -> controller.saveMemory(name, content) },
            onDelete = { name -> if (controller.deleteMemory(name)) part = SettingsPart.Memory })
        SettingsPart.Skills -> SkillsPart(shown.skills, machineName, onBack = { part = SettingsPart.Home },
            onRefresh = { scope.launch { controller.loadSkills() } })
        // Keeps its own sub-pages and back; back on its first page returns here (the handler above).
        SettingsPart.Proactive -> AssistantProactiveScreen(onBack = { part = SettingsPart.Home }, onOpenConversation = onOpenConversation,
            onModelConfiguration = onModelConfiguration?.let { open -> { purpose -> open(purpose, owner) } }, management = management, machines = machines)
    }
    when (sheet) {
        SettingsSheet.Machine -> MachineSheet(machines, state.config?.machineId, onDismiss = { sheet = null }) {
            sheet = null
            controller.requestMove(it, machines)
        }
        SettingsSheet.Zone -> ZoneSheet(state.config?.timezone, controller.phoneZone, onDismiss = { sheet = null }) {
            sheet = null
            scope.launch { controller.chooseZone(it) }
        }
        null -> Unit
    }
    state.move?.let { move ->
        JDialog(onDismiss = controller::cancelMove, title = move.title(resources), text = move.text(resources),
            confirm = stringResource(R.string.assistant_settings_move_confirm) to { scope.launch { controller.confirmMove() }; Unit },
            dismiss = stringResource(R.string.assistant_settings_cancel) to controller::cancelMove)
    }
}

@Composable
internal fun Caption(text: String) {
    Text(text, Modifier.padding(top = Tokens.Space.s, start = Tokens.Space.xs, end = Tokens.Space.xs),
        style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
}

/** A group of rows, one lazy item each (design rule 5). */
private fun LazyListScope.rows(key: String, rows: List<@Composable () -> Unit>) {
    rows.forEachIndexed { index, row ->
        item("$key-$index") { JGroupItem(GroupPosition.of(index, rows.size)) { row() } }
    }
}

private fun machineIcon(machine: Machine?): PhIcon = when {
    machine == null -> PhIcons.Desktop
    machine.icon == "laptop" || machine.os == "darwin" -> PhIcons.Laptop
    machine.icon == "server" || machine.kind == "local" -> PhIcons.HardDrives
    else -> PhIcons.Desktop
}

@Composable
private fun machineState(machine: Machine?): String = stringResource(when {
    machine == null -> R.string.assistant_settings_machine_not_listed
    machine.online && machine.kind == "local" -> R.string.assistant_settings_machine_online_hub
    machine.online -> R.string.assistant_settings_machine_online
    else -> R.string.assistant_settings_machine_offline
})

@Composable
private fun zoneLabel(zone: String): String =
    zoneOffset(zone)?.let { stringResource(R.string.assistant_settings_zone_with_offset, zone, it) } ?: zone

@Composable
private fun SettingsHome(
    state: AssistantSettingsUiState, machine: Machine?, machineName: String, phoneZone: String,
    onBack: () -> Unit, onRefresh: () -> Unit, open: (SettingsPart) -> Unit, sheet: (SettingsSheet) -> Unit,
    follow: (Boolean) -> Unit,
    quietTasks: (Boolean) -> Unit = {},
    foldBackground: (Boolean) -> Unit = {},
    superviseOwn: (Boolean) -> Unit = {},
    /** Opens the background supervisor's model (Settings › Models); null leaves the row out. */
    supervisorModel: (() -> Unit)? = null,
    owner: String = persona.core.contracts.api.ManagerRoster.ASSISTANT_MANAGER,
    permission: (String) -> Unit = {},
) {
    val config = state.config
    val ready = config != null && !state.busy
    val resources = LocalResources.current
    JListPage(
        title = stringResource(R.string.assistant_settings_title), onBack = onBack, refreshing = state.loading && config != null, onRefresh = onRefresh,
        summary = config?.let { stringResource(R.string.assistant_settings_summary, machineName) },
    ) {
        if (state.loadFailed) item("load-failed") {
            JNotice(stringResource(R.string.assistant_settings_load_failed), status = Status.NeedsYou,
                actionLabel = stringResource(R.string.assistant_settings_retry), onAction = onRefresh)
        }
        if (config == null && state.loading) item("loading") { JLoading(text = stringResource(R.string.assistant_settings_loading)) }
        state.notice?.let { notice -> item("notice") { JNotice(notice.text.resolve(resources), status = if (notice.error) Status.NeedsYou else Status.Unknown) } }
        if (config != null) {
            item("default-permission") {
                AssistantDefaultPermission(config.defaultPermissionMode ?: "default", ready,
                    state.permissionSupported && config.manager == owner, permission)
            }
            item("where") { JSectionHeader(stringResource(R.string.assistant_settings_where_header)) }
            rows("where", listOf(
                { JRow(stringResource(R.string.assistant_settings_machine), value = machineName, subtitle = machineState(machine), leadingIcon = machineIcon(machine),
                    enabled = ready, onClick = { sheet(SettingsSheet.Machine) }) },
                { JRow(stringResource(R.string.assistant_settings_cwd), value = homeRelative(config.cwd, machine?.home).ifBlank { "~/.persona/assistant" },
                    leadingIcon = PhIcons.FolderSimple) },
                { JRow(stringResource(R.string.assistant_settings_skills), subtitle = stringResource(R.string.assistant_settings_skills_subtitle),
                    leadingIcon = PhIcons.PuzzlePiece, onClick = { open(SettingsPart.Skills) }) },
            ))
            if (machine != null && !machine.online) item("offline") {
                JNotice(stringResource(R.string.assistant_settings_offline_notice, machineName), Modifier.padding(top = Tokens.Space.m),
                    status = Status.Offline)
            }
            item("where-caption") {
                Caption(stringResource(R.string.assistant_settings_where_caption))
            }

            item("proactive") { JSectionHeader(stringResource(R.string.assistant_settings_proactive_header)) }
            rows("proactive", listOf(
                { JRow(stringResource(R.string.assistant_settings_proactive), subtitle = stringResource(R.string.assistant_settings_proactive_subtitle),
                    leadingIcon = PhIcons.Clock, onClick = { open(SettingsPart.Proactive) }) },
            ))
            item("proactive-caption") { Caption(stringResource(R.string.assistant_settings_proactive_caption)) }

            item("data") { JSectionHeader(stringResource(R.string.assistant_settings_data_header)) }
            rows("data", listOf(
                { JRow(stringResource(R.string.assistant_settings_data_conversations), value = machineName, leadingIcon = PhIcons.ChatCircle) },
                { JRow(stringResource(R.string.assistant_settings_data_hub), value = stringResource(R.string.assistant_settings_hub), leadingIcon = PhIcons.SlidersHorizontal) },
                { JRow(stringResource(R.string.assistant_settings_data_knowledge), value = stringResource(R.string.assistant_settings_hub_machine), leadingIcon = PhIcons.BookOpen) },
            ))
            item("data-caption") { Caption(stringResource(R.string.assistant_settings_data_caption)) }

            item("you") { JSectionHeader(stringResource(R.string.assistant_settings_you_header)) }
            val memoryCount = state.memory.entries?.size
            rows("you", listOf(
                { JRow(stringResource(R.string.assistant_settings_notes), value = stringResource(when {
                    !config.notes.isNullOrBlank() -> R.string.assistant_settings_notes_filled
                    config.notes == null && config.notesEffective.isNotBlank() -> R.string.assistant_settings_notes_from_hub
                    else -> R.string.assistant_settings_notes_empty
                }), leadingIcon = PhIcons.UserCircle, enabled = ready, onClick = { open(SettingsPart.Notes) }) },
                { JRow(stringResource(R.string.assistant_settings_memory), value = when {
                    memoryCount != null -> if (memoryCount == 0) stringResource(R.string.assistant_settings_memory_none)
                        else pluralStringResource(R.plurals.assistant_settings_memory_count, memoryCount, memoryCount)
                    state.memory.failed -> stringResource(R.string.assistant_settings_memory_unreadable)
                    else -> null
                }, leadingIcon = PhIcons.Brain, onClick = { open(SettingsPart.Memory) }) },
            ))
            item("you-caption") {
                Caption(stringResource(R.string.assistant_settings_you_caption, machineName))
            }

            config.mainTasksQuiet?.let { quiet ->
                item("quiet") { JSectionHeader(stringResource(R.string.assistant_settings_quiet_header)) }
                rows("quiet", listOf(
                    { JSwitchRow(stringResource(R.string.assistant_settings_quiet), quiet, quietTasks,
                        leadingIcon = PhIcons.BellSimple, enabled = !state.busy) },
                ))
                item("quiet-caption") { Caption(stringResource(R.string.assistant_settings_quiet_caption)) }
            }

            // #4314: shown only by a hub that has the setting (an older one folds with no way to change it here).
            config.mainBackgroundFolded?.let { folded ->
                rows("background", listOf(
                    { JSwitchRow(stringResource(R.string.assistant_settings_background_folded), folded, foldBackground,
                        leadingIcon = PhIcons.Stack, enabled = !state.busy) },
                ))
                item("background-caption") { Caption(stringResource(R.string.assistant_settings_background_folded_caption)) }
            }

            // #4927 / #5084: the background supervisor's off switch and its model, shown only by a hub that has it.
            // The model row is there whichever way the switch is, as Settings › Model configuration lists the use: one
            // rule for both pages. Its current value is on the page it opens.
            config.mainSupervisesOwnTasks?.let { own ->
                item("supervise") { JSectionHeader(stringResource(R.string.assistant_settings_supervise_header)) }
                val superviseRows = mutableListOf<@Composable () -> Unit>({
                    JSwitchRow(stringResource(R.string.assistant_settings_supervise_own), own, superviseOwn,
                        leadingIcon = PersonaIcons.todo, enabled = !state.busy)
                })
                if (supervisorModel != null) superviseRows += {
                    JRow(stringResource(R.string.assistant_settings_supervise_model),
                        leadingIcon = PersonaIcons.model, enabled = ready, onClick = supervisorModel)
                }
                rows("supervise", superviseRows)
                item("supervise-caption") { Caption(stringResource(R.string.assistant_settings_supervise_caption)) }
            }

            item("zone") { JSectionHeader(stringResource(R.string.assistant_settings_zone)) }
            val hubZone = config.timezone?.takeIf { it.isNotBlank() }
            val zoneRows = mutableListOf<@Composable () -> Unit>({
                JSwitchRow(stringResource(R.string.assistant_settings_zone_follow), state.followPhone, follow,
                    subtitle = stringResource(R.string.assistant_settings_zone_phone_now, zoneLabel(phoneZone)),
                    leadingIcon = PhIcons.GlobeSimple, enabled = !state.busy)
            })
            if (!state.followPhone) zoneRows += {
                JRow(stringResource(R.string.assistant_settings_zone), value = hubZone ?: stringResource(R.string.assistant_settings_zone_unset),
                    subtitle = hubZone?.let { zone -> zoneName(zone, currentLocale) ?: zoneOffset(zone) },
                    leadingIcon = PhIcons.Clock, enabled = ready, onClick = { sheet(SettingsSheet.Zone) })
            }
            rows("zone", zoneRows)
            if (state.followPhone && hubZone != null && hubZone != phoneZone) item("zone-behind") {
                JNotice(stringResource(R.string.assistant_settings_zone_behind, zoneLabel(hubZone)), Modifier.padding(top = Tokens.Space.m))
            }
            item("zone-caption") { Caption(stringResource(R.string.assistant_settings_zone_caption)) }

            item("reach") { JSectionHeader(stringResource(R.string.assistant_settings_reach_header)) }
            rows("reach", listOf(
                { JRow(stringResource(R.string.assistant_settings_reach_agents), subtitle = stringResource(R.string.assistant_settings_reach_agents_subtitle), leadingIcon = PhIcons.Desktop) },
                { JRow(stringResource(R.string.assistant_settings_reach_lark), subtitle = stringResource(R.string.assistant_settings_reach_lark_subtitle), leadingIcon = PhIcons.ChatCircle) },
                { JRow(stringResource(R.string.assistant_settings_reach_mail), subtitle = stringResource(R.string.assistant_settings_reach_mail_subtitle), leadingIcon = PhIcons.EnvelopeSimple) },
                { JRow(stringResource(R.string.assistant_settings_reach_phone), subtitle = stringResource(R.string.assistant_settings_reach_phone_subtitle), leadingIcon = PhIcons.DeviceMobile) },
            ))
            item("reach-caption") { Caption(stringResource(R.string.assistant_settings_reach_caption)) }
        }
    }
}

@Composable
private fun NotesPart(state: AssistantSettingsUiState, onBack: () -> Unit, onSave: suspend (String) -> Boolean) {
    val config = state.config
    val scope = rememberCoroutineScope()
    var text by rememberSaveable { mutableStateOf<String?>(null) }
    var saved by rememberSaveable { mutableStateOf<String?>(null) }
    var leaving by remember { mutableStateOf(false) }
    LaunchedEffect(config) {
        if (text == null && config != null) config.notesShown().let { text = it; saved = it }
    }
    val changed = text != null && text != saved
    fun save(then: () -> Unit = {}) {
        val value = text ?: return
        scope.launch { if (onSave(value)) { saved = value; then() } }
    }
    val back = { if (changed) leaving = true else onBack() }
    BackHandler(changed) { leaving = true }
    JListPage(title = stringResource(R.string.assistant_settings_notes), onBack = back, bottomBar = {
        Row(Modifier.fillMaxWidth().padding(horizontal = pagePadding, vertical = Tokens.Space.s), verticalAlignment = Alignment.CenterVertically) {
            JButton(stringResource(R.string.assistant_settings_save), { save() }, Modifier.fillMaxWidth(), enabled = changed && !state.busy, loading = state.busy)
        }
    }) {
        item("intro") {
            Caption(stringResource(R.string.assistant_settings_notes_intro))
        }
        if (config != null && config.notes == null && config.notesEffective.isNotBlank()) item("from-hub") {
            JNotice(stringResource(R.string.assistant_settings_notes_hub_notice), Modifier.padding(top = Tokens.Space.m))
        }
        state.notice?.takeIf { it.error }?.let { notice -> item("notice") { JNotice(notice.text.resolve(LocalResources.current), Modifier.padding(top = Tokens.Space.m), status = Status.NeedsYou) } }
        item("text") {
            JTextField(text.orEmpty(), { text = it }, Modifier.fillMaxWidth().padding(top = Tokens.Space.m), label = stringResource(R.string.assistant_settings_notes),
                placeholder = stringResource(R.string.assistant_settings_notes_placeholder), enabled = text != null && !state.busy,
                minLines = 12, maxLines = Int.MAX_VALUE)
        }
    }
    if (leaving) JDialog(onDismiss = { leaving = false }, title = stringResource(R.string.assistant_settings_notes_unsaved_title),
        text = stringResource(R.string.assistant_settings_notes_unsaved_text), confirmEnabled = !state.busy,
        confirm = stringResource(R.string.assistant_settings_save) to { leaving = false; save(onBack) },
        dismiss = stringResource(R.string.assistant_settings_discard) to { leaving = false; onBack() })
}

@Composable
private fun MemoryPart(
    state: AssistantSettingsUiState, machine: Machine?, machineName: String, onBack: () -> Unit,
    onRefresh: () -> Unit, onOpen: (String) -> Unit,
) {
    val memory = state.memory
    val entries = memory.entries.orEmpty()
    JListPage(title = stringResource(R.string.assistant_settings_memory), onBack = onBack, refreshing = memory.loading && memory.entries != null, onRefresh = onRefresh) {
        item("intro") {
            Caption(stringResource(R.string.assistant_settings_memory_intro, machineName))
        }
        if (memory.failed) item("failed") {
            JNotice(if (machine != null && !machine.online) stringResource(R.string.assistant_settings_memory_offline, machineName)
                else stringResource(R.string.assistant_settings_memory_failed),
                Modifier.padding(top = Tokens.Space.m), status = Status.NeedsYou, actionLabel = stringResource(R.string.assistant_settings_retry), onAction = onRefresh)
        }
        if (memory.entries == null && memory.loading) item("loading") { JLoading(text = stringResource(R.string.assistant_settings_loading)) }
        if (memory.entries != null && entries.isEmpty() && !memory.failed) item("empty") {
            JEmptyState(stringResource(R.string.assistant_settings_memory_empty_title), stringResource(R.string.assistant_settings_memory_empty_text), icon = PhIcons.Brain)
        }
        if (entries.isNotEmpty()) item("entries") { JSectionHeader(stringResource(R.string.assistant_settings_memory_entries), count = entries.size) }
        itemsIndexed(entries, key = { _, entry -> entry.file.name }) { index, entry ->
            JGroupItem(GroupPosition.of(index, entries.size)) {
                JRow(entry.title, subtitle = entry.description ?: entry.body.lineSequence().firstOrNull { it.isNotBlank() }?.trim(),
                    leadingIcon = PhIcons.Brain, trailing = entry.typeLabel(LocalResources.current)?.let { label -> @Composable { JTag(label) } },
                    chevron = true, onClick = { onOpen(entry.file.name) })
            }
        }
        memory.index?.takeIf { entries.isNotEmpty() }?.let { index ->
            item("index") { JSectionHeader(stringResource(R.string.assistant_settings_memory_index)) }
            item("index-row") {
                JGroupItem(GroupPosition.Single) {
                    JRow(index.name, subtitle = stringResource(R.string.assistant_settings_memory_index_subtitle), leadingIcon = PhIcons.ListChecks,
                        onClick = { onOpen(index.name) })
                }
            }
        }
    }
}

/** The skills on the assistant's machine, read only (iOS SkillsPage): name, description, built in or the user's. */
@Composable
private fun SkillsPart(state: SkillsState, machineName: String, onBack: () -> Unit, onRefresh: () -> Unit) {
    val skills = state.list?.skills.orEmpty()
    val machine = state.list?.machine?.takeIf { it.isNotBlank() } ?: machineName
    JListPage(title = stringResource(R.string.assistant_settings_skills), onBack = onBack, refreshing = state.loading && state.list != null, onRefresh = onRefresh) {
        if (state.list == null && state.loading) item("loading") { JLoading(text = stringResource(R.string.assistant_settings_loading)) }
        if (state.empty) item("empty") {
            if (state.failed) JEmptyState(stringResource(R.string.assistant_settings_skills_failed_title),
                state.reason ?: stringResource(R.string.assistant_settings_skills_failed_text), icon = PhIcons.PuzzlePiece,
                action = { JButton(stringResource(R.string.assistant_settings_retry), onRefresh, kind = JButtonKind.Secondary) })
            else JEmptyState(stringResource(R.string.assistant_settings_skills_empty_title),
                stringResource(R.string.assistant_settings_skills_empty_text, machine), icon = PhIcons.PuzzlePiece)
        }
        itemsIndexed(skills, key = { _, skill -> "skill-${skill.name}" }) { index, skill ->
            JGroupItem(GroupPosition.of(index, skills.size)) {
                JRow(skill.name, subtitle = skill.description?.takeIf { it.isNotBlank() }, subtitleLines = 3, leadingIcon = PhIcons.PuzzlePiece,
                    trailing = { JTag(stringResource(if (skill.builtin) R.string.assistant_settings_skills_builtin else R.string.assistant_settings_skills_user)) })
            }
        }
        if (skills.isNotEmpty()) item("caption") { Caption(stringResource(R.string.assistant_settings_skills_caption, machine)) }
    }
}

@Composable
private fun MachineSheet(machines: List<Machine>, current: String?, onDismiss: () -> Unit, onPick: (Machine) -> Unit) {
    val list = machines.distinctBy { it.id }
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.assistant_settings_machine_sheet_title)) {
        Text(stringResource(R.string.assistant_settings_move_stays), style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
        Spacer(Modifier.height(Tokens.Space.m))
        LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false), contentPadding = PaddingValues(bottom = Tokens.Space.l)) {
            if (list.isEmpty()) item { JEmptyState(stringResource(R.string.assistant_settings_machines_empty_title), stringResource(R.string.assistant_settings_machines_empty_text), icon = PhIcons.Desktop) }
            itemsIndexed(list, key = { _, machine -> machine.id }) { index, machine ->
                JGroupItem(GroupPosition.of(index, list.size)) {
                    JRow(machine.displayName, subtitle = machineState(machine),
                        leading = { JRadio(machine.id == current, null) },
                        trailing = { JStatusDot(if (machine.online) Status.Unread else Status.Offline) },
                        chevron = false, onClick = { if (machine.id == current) onDismiss() else onPick(machine) })
                }
            }
        }
    }
}

@Composable
private fun ZoneSheet(current: String?, phoneZone: String, onDismiss: () -> Unit, onPick: (String) -> Unit) {
    var query by remember { mutableStateOf("") }
    val locale = currentLocale
    val zones = remember { pickableZones() }
    // The zone in use and the phone's own come first; the full list (by offset) follows.
    val pinned = remember(current, phoneZone) { listOfNotNull(current?.takeIf { it.isNotBlank() }, phoneZone.takeIf { it.isNotBlank() }).distinct() }
    val shown = remember(query, pinned, locale) { zones.filter { zoneMatches(it, query, locale) && (query.isNotBlank() || it !in pinned) } }
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.assistant_settings_zone)) {
        JSearchField(query, { query = it }, stringResource(R.string.assistant_settings_zone_search))
        Spacer(Modifier.height(Tokens.Space.m))
        LazyColumn(Modifier.fillMaxWidth().weight(1f, fill = false), contentPadding = PaddingValues(bottom = Tokens.Space.l)) {
            if (query.isBlank() && pinned.isNotEmpty()) {
                itemsIndexed(pinned, key = { _, zone -> "pinned:$zone" }) { index, zone ->
                    JGroupItem(GroupPosition.of(index, pinned.size)) { ZoneRow(zone, current, phoneZone, onPick) }
                }
                item("all") { JSectionHeader(stringResource(R.string.assistant_settings_zone_all)) }
            }
            if (shown.isEmpty()) item { JEmptyState(stringResource(R.string.assistant_settings_zone_none_title), stringResource(R.string.assistant_settings_zone_none_text), icon = PhIcons.GlobeSimple) }
            itemsIndexed(shown, key = { _, zone -> zone }) { index, zone ->
                JGroupItem(GroupPosition.of(index, shown.size)) { ZoneRow(zone, current, phoneZone, onPick) }
            }
        }
    }
}

@Composable
private fun ZoneRow(zone: String, current: String?, phoneZone: String, onPick: (String) -> Unit) {
    val phone = stringResource(R.string.assistant_settings_zone_phone)
    val separator = stringResource(R.string.assistant_settings_list_separator)
    JRow(zone, subtitle = listOfNotNull(zoneOffset(zone), zoneName(zone, currentLocale), phone.takeIf { zone == phoneZone })
        .joinToString(separator).ifBlank { null },
        leading = { JRadio(zone == current, null) }, chevron = false, onClick = { onPick(zone) })
}

/** What "About me" opens with: what the user saved in the app, else what the assistant is told from hub.json. */
internal fun AssistantConfig.notesShown(): String = notes ?: notesEffective
