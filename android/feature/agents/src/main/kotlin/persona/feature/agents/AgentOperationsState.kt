package persona.feature.agents

import persona.core.contracts.Session
import persona.core.contracts.SessionKey
import persona.core.contracts.api.*
import persona.core.domain.sort.AgentPreferences
import persona.core.domain.sort.toggleMute
import persona.core.domain.sort.togglePin
import persona.core.domain.sort.SessionRename
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock

internal data class AgentSessionEdit(
    val title: String? = null, val tab: String? = null, val closed: Boolean = false,
    /** "Delete chat": hidden until the hub's list no longer has it. */
    val deleted: Boolean = false,
) {
    fun matches(session: Session?) = when {
        deleted -> session == null
        closed -> session == null || !session.isLive
        else -> session != null && (title == null || session.title == title) && (tab == null || session.slotName == tab)
    }
    /** Leaves the list (closed or deleted) once it succeeds: the chat open on it goes too. */
    val removes: Boolean get() = closed || deleted
}
internal data class AgentSpaceEdit(val machine: String, val workspace: String, val oldName: String, val name: String)
internal data class AgentOperationsSnapshot(
    val pins: AgentOperationPreferences? = null,
    val mutes: AgentOperationPreferences? = null,
    val sessions: Map<SessionKey, AgentSessionEdit> = emptyMap(),
    val spaces: Map<String, AgentSpaceEdit> = emptyMap(),
    val uncertain: Set<String> = emptySet(),
    val busy: Boolean = false,
) {
    fun project(source: List<Session>): List<Session> = source.mapNotNull { session ->
        val edit = sessions[session.key]
        if (edit?.removes == true) null else {
            val space = spaces.values.firstOrNull { it.machine == session.machineId && it.workspace == session.operationWorkspaceId }
            session.copy(title = edit?.title ?: session.title, slotName = edit?.tab ?: session.slotName,
                spaceName = space?.name ?: session.spaceName)
        }
    }
}

/** The last hub pins and mutes of a pairing scope, shown until the hub answers. */
internal interface AgentPreferencesCache {
    suspend fun read(scope: String): Pair<AgentPreferences, AgentPreferences>?
    suspend fun write(scope: String, pins: AgentPreferences, mutes: AgentPreferences)
}

/** Serialize preference replacements and roll back only the temporary operation snapshot. */
internal class AgentOperationsState(private val api: AgentOperations, private val cache: AgentPreferencesCache? = null) {
    private val mutex = Mutex()
    private val mutable = MutableStateFlow(AgentOperationsSnapshot())
    val state = mutable.asStateFlow()

    suspend fun refresh() = mutex.withLock {
        if (mutable.value.pins == null) cached { read(api.scope) }?.let { (pins, mutes) ->
            mutable.value = mutable.value.copy(pins = pins.operation(), mutes = mutes.operation())
        }
        val pins = api.pins()
        val mutes = api.mutes()
        mutable.value = mutable.value.copy(pins = pins, mutes = mutes)
        cached { write(api.scope, pins.domain(), mutes.domain()) }
    }

    /** The cache only speeds up the first paint; failing to use it must not block the hub. */
    private suspend fun <T> cached(block: suspend AgentPreferencesCache.() -> T): T? = try {
        cache?.block()
    } catch (e: CancellationException) { throw e } catch (_: Exception) { null }

    suspend fun preference(pin: Boolean, space: Boolean, key: String, spaceKey: String? = null) = mutex.withLock {
        // Fetch before replacing the document so unrelated remote entries survive.
        val current = if (pin) api.pins() else api.mutes()
        val next = when {
            space -> current.domain().togglePin(key, space = true)
            pin -> current.domain().togglePin(key)
            else -> current.domain().toggleMute(key, spaceKey)
        }.operation()
        val before = if (pin) mutable.value.copy(pins = current) else mutable.value.copy(mutes = current)
        mutable.value = if (pin) before.copy(pins = next, busy = true) else before.copy(mutes = next, busy = true)
        try {
            if (pin) api.savePins(next) else api.saveMutes(next)
        } catch (e: Exception) {
            mutable.value = before
            throw e
        } finally { mutable.value = mutable.value.copy(busy = false) }
    }

    suspend fun session(session: Session, requested: AgentSessionEdit) = mutex.withLock {
        val rename = SessionRename.changes(session, requested.title, requested.tab)
        if (!requested.removes && !rename.changed) return@withLock
        val edit = if (requested.removes) requested else AgentSessionEdit(rename.title, rename.tabName)
        val key = session.key
        val operation = "${if (edit.deleted) "delete" else if (edit.closed) "close" else "rename"}:${key.path}"
        check(operation !in mutable.value.uncertain) { "Verify the previous operation first" }
        val before = mutable.value
        val previous = before.sessions[key]
        val projected = if (edit.removes) edit else edit.copy(title = edit.title ?: previous?.title, tab = edit.tab ?: previous?.tab)
        mutable.value = before.copy(sessions = before.sessions + (key to projected), busy = true)
        try {
            when {
                edit.deleted -> api.deleteSession(key)
                edit.closed -> api.closeSession(key)
                else -> api.renameSession(key, edit.title, edit.tab)
            }
        } catch (e: Exception) {
            val uncertain = e is CancellationException || (e as? AgentOperationFailure)?.uncertain == true
            mutable.value = before.copy(uncertain = if (uncertain) before.uncertain + operation else before.uncertain)
            if (uncertain) expectedSessions[operation] = key to edit
            throw e
        } finally { mutable.value = mutable.value.copy(busy = false) }
    }

    suspend fun renameSpace(session: Session, requestedName: String) = mutex.withLock {
        val name = requestedName.trim()
        if (name.isEmpty() || name == session.spaceName) return@withLock
        val workspace = requireNotNull(session.operationWorkspaceId)
        val oldName = requireNotNull(session.spaceName)
        val key = "${session.machineId}/$workspace"
        val operation = "space:$key"
        check(operation !in mutable.value.uncertain) { "Verify the previous operation first" }
        val pins = api.pins()
        val mutes = api.mutes()
        val before = mutable.value.copy(pins = pins, mutes = mutes)
        val edit = AgentSpaceEdit(session.machineId, workspace, oldName, name)
        fun AgentOperationPreferences.renamed() = copy(spaces = spaces.map {
            if (it == "${session.machineId}/$oldName") "${session.machineId}/$name" else it
        }.distinct())
        mutable.value = before.copy(pins = pins.renamed(), mutes = mutes.renamed(), spaces = before.spaces + (key to edit), busy = true)
        try { api.renameSpace(session.machineId, workspace, name) }
        catch (e: Exception) {
            val uncertain = e is CancellationException || (e as? AgentOperationFailure)?.uncertain == true
            mutable.value = before.copy(uncertain = if (uncertain) before.uncertain + operation else before.uncertain)
            if (uncertain) expectedSpaces[operation] = edit
            throw e
        } finally { mutable.value = mutable.value.copy(busy = false) }
    }

    /** Not a projected edit: the list shows the source's attached flag once the hub has it. */
    suspend fun attach(session: Session) = mutex.withLock { api.attachSession(session.key) }
    suspend fun interrupt(session: Session) = mutex.withLock { api.interruptSession(session.key) }

    private val expectedSessions = mutableMapOf<String, Pair<SessionKey, AgentSessionEdit>>()
    private val expectedSpaces = mutableMapOf<String, AgentSpaceEdit>()

    /** Only authoritative snapshots can resolve an ambiguous one-shot write. */
    suspend fun reconcile(source: List<Session>, complete: Boolean = true): List<SessionKey> = mutex.withLock {
        fun observed(key: SessionKey, edit: AgentSessionEdit): Boolean {
            val row = source.firstOrNull { it.key == key }
            return (complete || row != null) && edit.matches(row)
        }
        val resolved = expectedSessions.filter { (_, value) -> observed(value.first, value.second) }
        val resolvedSpaces = expectedSpaces.filter { (_, edit) -> source.any {
            it.machineId == edit.machine && it.operationWorkspaceId == edit.workspace && it.spaceName == edit.name
        } }
        val resolvedKeys = resolved.keys + resolvedSpaces.keys
        expectedSessions.keys.removeAll(resolvedKeys)
        expectedSpaces.keys.removeAll(resolvedKeys)
        mutable.value = mutable.value.copy(
            sessions = mutable.value.sessions.filter { (key, edit) -> !observed(key, edit) },
            spaces = mutable.value.spaces.filter { (_, edit) -> source.none {
                it.machineId == edit.machine && it.operationWorkspaceId == edit.workspace && it.spaceName == edit.name
            } },
            uncertain = mutable.value.uncertain - resolvedKeys,
        )
        resolved.values.filter { it.second.removes }.map { it.first }
    }
}

internal val Session.operationWorkspaceId: String? get() = workspaceId?.takeIf { it.isNotBlank() }
    ?: spaceId?.takeIf { it.isNotBlank() && it != "-" && spaceKind != "directory" }
internal fun AgentOperationPreferences.domain() = AgentPreferences(spaces, sessions, allowed)
internal fun AgentPreferences.operation() = AgentOperationPreferences(spaces, sessions, allowed)
internal fun Session.handoffPrompt(res: android.content.res.Resources) =
    res.getString(R.string.agents_handoff_prompt, machineId, sessionId, displayTitle, cwd, lastText.orEmpty())
/** An instruction to the agent that names its tools and the reply it should give. */
internal const val STOP_LOOP_PROMPT = "停止当前的 /loop：调用 ScheduleWakeup(stop: true) 结束循环（如果用 CronCreate 建过定时任务，用 CronDelete 删掉），不要再安排下一次唤醒，回复一句「循环已停止」即可。" // i18n-ignore: an instruction to the agent naming its tools
