package persona.feature.assistant.settings

import android.content.Context
import android.content.res.Resources
import androidx.annotation.StringRes
import persona.feature.assistant.R
import persona.core.contracts.Machine
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantConfigUpdate
import persona.core.contracts.api.AssistantManagement
import persona.core.contracts.api.AssistantRefused
import persona.core.contracts.api.AssistantSkills
import persona.core.contracts.api.MemoryFile
import persona.core.contracts.parseTime
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import java.time.Instant
import java.time.ZoneId
import java.util.Locale
import java.util.TimeZone

/** The calls Settings › Assistant makes: narrower than [AssistantManagement], so it stays put while that one grows. */
internal interface AssistantSettingsApi {
    suspend fun config(): AssistantConfig
    suspend fun updateConfig(update: AssistantConfigUpdate): AssistantConfig
    suspend fun memory(): List<MemoryFile>
    suspend fun saveMemory(name: String, content: String)
    suspend fun deleteMemory(name: String)
    suspend fun skills(): AssistantSkills = throw UnsupportedOperationException("Skills unavailable")
}

internal fun AssistantManagement.settingsApi(): AssistantSettingsApi {
    val management = this
    return object : AssistantSettingsApi {
        override suspend fun config() = management.config()
        override suspend fun updateConfig(update: AssistantConfigUpdate) = management.updateConfig(update)
        override suspend fun memory() = management.memory()
        override suspend fun saveMemory(name: String, content: String) = management.saveMemory(name, content)
        override suspend fun deleteMemory(name: String) = management.deleteMemory(name)
        override suspend fun skills() = management.skills()
    }
}

/** Whether the hub's time zone follows this phone's (the default, as on iOS); the choice stays on the phone. */
internal interface TimezoneChoice {
    val phoneZone: String
    var followPhone: Boolean
}

internal class StoredTimezoneChoice(context: Context) : TimezoneChoice {
    private val preferences = context.applicationContext.getSharedPreferences("persona-assistant", Context.MODE_PRIVATE)
    override val phoneZone: String get() = TimeZone.getDefault().id
    override var followPhone: Boolean
        get() = preferences.getBoolean("timezone.follow_phone", true)
        set(value) { preferences.edit().putBoolean("timezone.follow_phone", value).apply() }
}

/** The zone to send so the hub follows the phone; null when it already does or the user picked one. */
internal fun zoneToSync(choice: TimezoneChoice, hubZone: String?): String? =
    choice.phoneZone.takeIf { choice.followPhone && it.isNotBlank() && it != hubZone }

/** Routines and the assistant's sense of "today" follow the phone's zone unless the user picked another one. */
internal suspend fun followPhoneZone(api: AssistantSettingsApi, choice: TimezoneChoice, known: AssistantConfig? = null): AssistantConfig {
    val config = known ?: api.config()
    val zone = zoneToSync(choice, config.timezone) ?: return config
    return api.updateConfig(AssistantConfigUpdate(timezone = zone))
}

/** Moving the assistant to another machine, waiting for the user to confirm. */
internal data class MachineMove(val fromName: SettingsText, val to: Machine) {
    fun title(resources: Resources): String = resources.getString(R.string.assistant_settings_move_title, to.displayName)
    fun text(resources: Resources): String = machineMoveWarning(resources, fromName.resolve(resources), to.displayName, to.online)
}

/** Conversations and memory are files on the machine the assistant runs on: they never move with it. */
internal fun machineMoveWarning(resources: Resources, from: String, to: String, online: Boolean): String = buildString {
    append(resources.getString(R.string.assistant_settings_move_text,
        resources.getString(R.string.assistant_settings_move_stays), resources.getString(R.string.assistant_settings_move_fresh, to, from)))
    if (!online) append("\n\n").append(resources.getString(R.string.assistant_settings_move_offline, to))
}

/**
 * Text the state hands the page: a string resource and its arguments, resolved in the page's language. An argument
 * may itself be a [SettingsText] (a fallback name such as "the original machine").
 */
internal class SettingsText(@StringRes val id: Int, vararg val args: Any) {
    fun resolve(resources: Resources): String =
        resources.getString(id, *args.map { if (it is SettingsText) it.resolve(resources) else it }.toTypedArray())

    override fun equals(other: Any?): Boolean = other is SettingsText && other.id == id && other.args.contentEquals(args)
    override fun hashCode(): Int = 31 * id + args.contentHashCode()

    companion object {
        /** A name the user or the hub gave: shown as it is in every language. */
        fun plain(text: String) = SettingsText(R.string.assistant_settings_plain, text)
    }
}

internal data class SettingsNotice(val text: SettingsText, val error: Boolean)

internal data class MemoryState(
    val entries: List<MemoryEntry>? = null,
    val index: MemoryFile? = null,
    val loading: Boolean = false,
    val failed: Boolean = false,
)

/**
 * The skills on the assistant's machine (Skills, like iOS): null until read. [failed] when they could not be read,
 * with the hub's own reason when it gave one ([reason], shown as it is).
 */
internal data class SkillsState(
    val list: AssistantSkills? = null,
    val loading: Boolean = false,
    val failed: Boolean = false,
    val reason: String? = null,
) {
    /** The page shows the empty state once it has an answer (or a failure) and no skill to list. */
    val empty: Boolean get() = !loading && (failed || list != null) && list?.skills.isNullOrEmpty()
}

internal data class AssistantSettingsUiState(
    val config: AssistantConfig? = null,
    val loading: Boolean = false,
    val loadFailed: Boolean = false,
    val busy: Boolean = false,
    val notice: SettingsNotice? = null,
    val move: MachineMove? = null,
    val memory: MemoryState = MemoryState(),
    val skills: SkillsState = SkillsState(),
    val followPhone: Boolean = true
) {
    val permissionSupported: Boolean get() = !loading && !loadFailed &&
        config?.permissionDefaults?.supports(config.manager) == true
}

/** Mutations run one at a time; every outcome lands in [state] (the page shows it, nothing is thrown). */
internal class AssistantSettingsState(
    private val api: AssistantSettingsApi,
    private val timezone: TimezoneChoice,
    /**
     * After a move, before [onMoved]: the hub has no main conversation now, so the outbox must forget the one it bound
     * on the old machine ([persona.core.contracts.api.MainConversationSender.mainChanged]); otherwise the next message
     * to the main conversation goes to that one, an ordinary conversation now.
     */
    private val mainChanged: suspend () -> Unit = {},
    /** After a move: the conversation list now comes from the other machine. */
    private val onMoved: suspend () -> Unit = {},
) {
    private val mutable = MutableStateFlow(AssistantSettingsUiState(followPhone = timezone.followPhone))
    val state = mutable.asStateFlow()
    val phoneZone: String get() = timezone.phoneZone

    private var configRevision = 0L

    private fun set(change: AssistantSettingsUiState.() -> AssistantSettingsUiState) = mutable.update(change)

    fun clearNotice() = set { copy(notice = null) }

    suspend fun load() {
        val revision = ++configRevision
        set { copy(loading = true, loadFailed = false) }
        try {
            val config = api.config()
            currentCoroutineContext().ensureActive()
            if (revision != configRevision) return
            set { copy(config = config) }
            // Like iOS: opening the page brings the hub's zone along with the phone's.
            quietly {
                val synced = followPhoneZone(api, timezone, config)
                currentCoroutineContext().ensureActive()
                if (revision == configRevision) set { copy(config = synced) }
            }
        } catch (error: CancellationException) { throw error }
        catch (_: Exception) { if (revision == configRevision) set { copy(loadFailed = true) } }
        finally { if (revision == configRevision) set { copy(loading = false) } }
    }

    fun requestMove(to: Machine, machines: List<Machine>) {
        val config = state.value.config ?: return
        if (state.value.busy || to.id == config.machineId) return
        val from = machines.firstOrNull { it.id == config.machineId }?.displayName ?: config.machineId.ifBlank { null }
        set { copy(move = MachineMove(from?.let(SettingsText::plain) ?: SettingsText(R.string.assistant_settings_move_from_unknown), to)) }
    }

    fun cancelMove() = set { copy(move = null) }

    suspend fun confirmMove() {
        val move = state.value.move ?: return
        set { copy(move = null) }
        val moved = perform(SettingsText(R.string.assistant_settings_move_failed, move.fromName)) {
            // The main conversation is a transcript on the old machine; the next message starts a new one here.
            val config = api.updateConfig(AssistantConfigUpdate(machineId = move.to.id, main = AssistantConfigUpdate.CLEAR_MAIN))
            set { copy(config = config, memory = MemoryState()) }
            done(SettingsText(R.string.assistant_settings_move_done, move.to.displayName))
        }
        if (!moved) return
        quietly { mainChanged() }
        quietly { onMoved() }
        loadMemory()
    }

    /** A default for future conversations; never writes the current conversation's mode. */
    suspend fun setDefaultPermission(mode: String) {
        val config = state.value.config ?: return
        val owner = config.manager?.takeIf(String::isNotBlank) ?: return
        if (!state.value.permissionSupported ||
            (config.defaultPermissionMode ?: "default") == mode) return
        require(mode in persona.core.contracts.api.AssistantPermissionDefaults.MODES)
        val revision = configRevision
        fun verify(value: AssistantConfig) {
            check(value.manager == owner) { "Assistant identity changed" }
            check(value.permissionDefaults?.supports(owner) == true) { "Assistant permission support changed" }
            check(value.defaultPermissionMode == mode || (mode == "default" && value.defaultPermissionMode.isNullOrBlank())) {
                "The hub did not save the permission default"
            }
        }
        perform(SettingsText(R.string.assistant_settings_permission_failed)) {
            val updated = api.updateConfig(AssistantConfigUpdate(defaultPermissionMode = mode))
            currentCoroutineContext().ensureActive()
            if (revision != configRevision) return@perform
            verify(updated)
            val persisted = api.config()
            currentCoroutineContext().ensureActive()
            if (revision != configRevision) return@perform
            verify(persisted)
            set { copy(config = persisted) }
        }
    }

    suspend fun setFollowPhone(follow: Boolean) {
        if (state.value.busy || follow == state.value.followPhone) return
        timezone.followPhone = follow
        set { copy(followPhone = follow) }
        if (follow) perform(SettingsText(R.string.assistant_settings_zone_sync_failed)) {
            val synced = followPhoneZone(api, timezone, state.value.config)
            set { copy(config = synced) }
        }
    }

    suspend fun chooseZone(zone: String) {
        if (state.value.followPhone || zone.isBlank() || zone == state.value.config?.timezone) return
        perform(SettingsText(R.string.assistant_settings_zone_failed)) {
            val updated = api.updateConfig(AssistantConfigUpdate(timezone = zone))
            set { copy(config = updated) }
            done(SettingsText(R.string.assistant_settings_zone_done, zone))
        }
    }

    /** "Notify only in main chat": moves at once; a failure puts it back and says so. */
    suspend fun setMainTasksQuiet(on: Boolean) {
        val config = state.value.config ?: return
        if (config.mainTasksQuiet == null || config.mainTasksQuiet == on) return
        set { copy(config = config.copy(mainTasksQuiet = on)) }
        val ok = perform(SettingsText(R.string.assistant_settings_save_failed)) {
            val updated = api.updateConfig(AssistantConfigUpdate(mainTasksQuiet = on))
            set { copy(config = updated) }
        }
        if (!ok) set { copy(config = this.config?.copy(mainTasksQuiet = !on)) }
    }

    /** "Fold background rounds" (#4314): moves at once; a failure puts it back and says so. */
    suspend fun setMainBackgroundFolded(on: Boolean) {
        val config = state.value.config ?: return
        if (config.mainBackgroundFolded == null || config.mainBackgroundFolded == on) return
        set { copy(config = config.copy(mainBackgroundFolded = on)) }
        val ok = perform(SettingsText(R.string.assistant_settings_save_failed)) {
            val updated = api.updateConfig(AssistantConfigUpdate(mainBackgroundFolded = on))
            set { copy(config = updated) }
        }
        if (!ok) set { copy(config = this.config?.copy(mainBackgroundFolded = !on)) }
    }

    /**
     * "The main chat supervises its own tasks" (#4927): off, the background supervisor follows them up. Moves at once;
     * a failure puts it back and says so.
     */
    suspend fun setMainSupervisesOwnTasks(on: Boolean) {
        val config = state.value.config ?: return
        if (config.mainSupervisesOwnTasks == null || config.mainSupervisesOwnTasks == on) return
        set { copy(config = config.copy(mainSupervisesOwnTasks = on)) }
        val ok = perform(SettingsText(R.string.assistant_settings_save_failed)) {
            val updated = api.updateConfig(AssistantConfigUpdate(mainSupervisesOwnTasks = on))
            set { copy(config = updated) }
        }
        if (!ok) set { copy(config = this.config?.copy(mainSupervisesOwnTasks = !on)) }
    }

    suspend fun saveNotes(text: String): Boolean = perform(SettingsText(R.string.assistant_settings_save_failed)) {
        val updated = api.updateConfig(AssistantConfigUpdate(notes = text))
        set { copy(config = updated) }
        done(SettingsText(R.string.assistant_settings_notes_saved))
    }

    suspend fun loadMemory() {
        set { copy(memory = memory.copy(loading = true, failed = false)) }
        try {
            val (entries, index) = memoryEntries(api.memory())
            set { copy(memory = MemoryState(entries, index)) }
        } catch (error: CancellationException) { throw error }
        catch (_: Exception) { set { copy(memory = memory.copy(loading = false, failed = true)) } }
    }

    /** A failed reload keeps the list it had, like iOS; the reason shows only while there is nothing to list. */
    suspend fun loadSkills() {
        set { copy(skills = skills.copy(loading = true)) }
        try {
            val list = api.skills()
            set { copy(skills = SkillsState(list)) }
        } catch (error: CancellationException) { throw error }
        catch (error: Exception) {
            val reason = (error as? AssistantRefused)?.message?.trim()?.ifEmpty { null }
            set { copy(skills = skills.copy(loading = false, failed = true, reason = reason)) }
        }
    }

    suspend fun saveMemory(name: String, content: String): Boolean {
        val saved = perform(SettingsText(R.string.assistant_settings_save_failed)) {
            api.saveMemory(name, content)
            // Show the edit right away; the reload below picks up anything else that changed meanwhile.
            set {
                val files = memory.entries.orEmpty().map { it.file } + listOfNotNull(memory.index)
                val (entries, index) = memoryEntries(files.map { if (it.name == name) it.copy(content = content) else it })
                copy(memory = memory.copy(entries = entries, index = index))
            }
            done(SettingsText(R.string.assistant_settings_memory_saved))
        }
        if (saved) loadMemory()
        return saved
    }

    suspend fun deleteMemory(name: String): Boolean {
        val deleted = perform(SettingsText(R.string.assistant_settings_delete_failed)) {
            api.deleteMemory(name)
            set { copy(memory = memory.copy(entries = memory.entries?.filterNot { it.file.name == name })) }
            done(SettingsText(R.string.assistant_settings_memory_deleted))
        }
        if (deleted) loadMemory()
        return deleted
    }

    private fun done(text: SettingsText) = set { copy(notice = SettingsNotice(text, error = false)) }

    private suspend fun perform(failure: SettingsText, block: suspend () -> Unit): Boolean {
        if (state.value.busy) return false
        set { copy(busy = true, notice = null) }
        return try { block(); true }
        catch (error: CancellationException) { throw error }
        catch (_: Exception) { set { copy(notice = SettingsNotice(failure, error = true)) }; false }
        finally { set { copy(busy = false) } }
    }

    private suspend fun quietly(block: suspend () -> Unit) {
        try { block() } catch (error: CancellationException) { throw error } catch (_: Exception) { }
    }
}

internal const val MEMORY_INDEX = "MEMORY.md"

/** One memory file: its front matter (`---` … `---`: name, description, type) and the text after it. */
internal data class MemoryEntry(
    val file: MemoryFile,
    val title: String,
    val description: String?,
    val type: String?,
    val body: String,
) {
    /** The kind of memory in the page's language; a kind the app does not know is shown as written. */
    fun typeLabel(resources: Resources): String? = when (type) {
        null -> null
        "user" -> resources.getString(R.string.assistant_settings_memory_type_user)
        "feedback" -> resources.getString(R.string.assistant_settings_memory_type_feedback)
        "project" -> resources.getString(R.string.assistant_settings_memory_type_project)
        "reference" -> resources.getString(R.string.assistant_settings_memory_type_reference)
        else -> type
    }
    val updated: Instant? get() = parseTime(file.updatedAt)
}

/** The memory files newest first, titled from the index where it has a line for them, and the index itself. */
internal fun memoryEntries(files: List<MemoryFile>): Pair<List<MemoryEntry>, MemoryFile?> {
    val index = files.firstOrNull { it.name == MEMORY_INDEX }
    val titles = index?.let { memoryIndexLines(it.content) }.orEmpty()
    val entries = files.filter { it.name != MEMORY_INDEX }.map { parseMemory(it, titles[it.name]) }
        .sortedWith(compareByDescending<MemoryEntry> { it.updated ?: Instant.EPOCH }.thenBy { it.file.name })
    return entries to index
}

/** The index's lines `- [title](file.md) — description`, by file. */
internal fun memoryIndexLines(content: String): Map<String, Pair<String, String?>> = buildMap {
    for (line in content.lines()) {
        val open = line.indexOf('[').takeIf { it >= 0 } ?: continue
        val close = line.indexOf("](", open).takeIf { it > open } ?: continue
        val paren = line.indexOf(')', close + 2).takeIf { it > close } ?: continue
        val title = line.substring(open + 1, close).trim()
        val file = line.substring(close + 2, paren).trim()
        val hook = line.substring(paren + 1).trimStart(' ', '—', '–', '-', ':').trim()
        if (title.isNotEmpty() && file.isNotEmpty()) put(file, title to hook.ifEmpty { null })
    }
}

internal fun parseMemory(file: MemoryFile, indexed: Pair<String, String?>? = null): MemoryEntry {
    val lines = file.content.lines()
    val end = if (lines.firstOrNull()?.trim() == "---") lines.drop(1).indexOfFirst { it.trim() == "---" } + 1 else 0
    val fields = mutableMapOf<String, String>()
    if (end > 0) for (line in lines.subList(1, end)) {
        val colon = line.indexOf(':').takeIf { it > 0 } ?: continue
        // `type` sits under `metadata:` (indented) in newer files and at the top level in older ones.
        val key = line.substring(0, colon).trim()
        var value = line.substring(colon + 1).trim()
        if (value.length >= 2 && (value.first() == '"' || value.first() == '\'') && value.last() == value.first())
            value = value.substring(1, value.length - 1)
        if (value.isNotEmpty() && key in setOf("name", "description", "type")) fields.putIfAbsent(key, value)
    }
    val body = (if (end > 0) lines.drop(end + 1) else lines).joinToString("\n").trim()
    return MemoryEntry(
        file, title = indexed?.first ?: fields["name"] ?: file.name.removeSuffix(".md"),
        description = fields["description"] ?: indexed?.second, type = fields["type"], body = body,
    )
}

/** `~/.persona/assistant` rather than the full path, when it is under the machine's home. */
internal fun homeRelative(path: String, home: String?): String {
    val root = home?.trimEnd('/')?.takeIf { it.isNotEmpty() } ?: return path
    return when {
        path == root -> "~"
        path.startsWith("$root/") -> "~" + path.removePrefix(root)
        else -> path
    }
}

/** "UTC+8", "UTC+5:30", "UTC" for a zone right now; null for an id Java does not know. */
internal fun zoneOffset(zone: String, at: Instant = Instant.now()): String? = runCatching {
    val seconds = ZoneId.of(zone).rules.getOffset(at).totalSeconds
    if (seconds == 0) return@runCatching "UTC"
    val sign = if (seconds < 0) "-" else "+"
    val hours = Math.abs(seconds) / 3600
    val minutes = Math.abs(seconds) % 3600 / 60
    "UTC$sign$hours" + if (minutes != 0) String.format(Locale.ROOT, ":%02d", minutes) else ""
}.getOrNull()

/** The zone's name in [locale] ("China Standard Time" in English, its own name in Chinese), when the system has one. */
internal fun zoneName(zone: String, locale: Locale): String? = runCatching {
    TimeZone.getTimeZone(ZoneId.of(zone)).getDisplayName(locale).takeIf { it.isNotBlank() && !it.startsWith("GMT") }
}.getOrNull()

/** Region zones (Asia/Shanghai …) by offset, then name: what the picker lists. */
internal fun pickableZones(at: Instant = Instant.now()): List<String> = ZoneId.getAvailableZoneIds()
    .filter { '/' in it && !it.startsWith("Etc/") && !it.startsWith("SystemV/") && it.first().isUpperCase() }
    .sortedWith(compareBy<String> { runCatching { ZoneId.of(it).rules.getOffset(at).totalSeconds }.getOrDefault(0) }.thenBy { it })

internal fun zoneMatches(zone: String, query: String, locale: Locale): Boolean {
    val q = query.trim()
    return q.isEmpty() || zone.contains(q, ignoreCase = true) || zone.replace('_', ' ').contains(q, ignoreCase = true) ||
        zoneName(zone, locale)?.contains(q, ignoreCase = true) == true || zoneOffset(zone)?.contains(q, ignoreCase = true) == true
}
