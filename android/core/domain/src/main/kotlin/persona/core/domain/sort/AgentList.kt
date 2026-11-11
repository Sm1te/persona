package persona.core.domain.sort

import persona.core.contracts.CoreText.Key
import persona.core.contracts.CoreText
import persona.core.contracts.AgentState
import persona.core.contracts.Machine
import persona.core.contracts.Todo
import persona.core.contracts.state

/** Kept IDs retain membership, not a previous row position. Reselecting a filter replaces kept. */
data class AgentListSettings(
    val grouping: AgentGrouping = AgentGrouping.Repo,
    val quick: AgentQuickFilter = AgentQuickFilter.All,
    val machineFilter: String = "",
    val query: String = "",
    val queryAncestors: Set<String> = emptySet(),
    val includeCodexHistory: Boolean = false,
    val kept: Set<String> = emptySet(),
    val folded: Set<String> = emptySet(),
    val expandedIdle: Set<String> = emptySet(),
    val expandedQuiet: Set<String> = emptySet(),
    /** "By state" (default) or "Recently updated" (iOS `AgentSort`, kept on this phone). */
    val sort: AgentSort = AgentSort.State,
    /** botmux's sessions hidden from this list (web #2786, kept on this phone); "Lark group" shows them again. */
    val hideBotmux: Boolean = false,
)

/**
 * How the list orders its agents (iOS `AgentSort`): the state first, or the latest update only. Under "By state" the
 * sections stay the states; only the rows' order inside them is by time either way.
 */
enum class AgentSort {
    State, Updated;
    companion object {
        fun fromPreference(value: String?): AgentSort = entries.firstOrNull { it.name == value } ?: State
    }
}

enum class AgentGrouping {
    Repo, MachineSpace, State, Todo,
    // Retained for callers of the original list projection; stored preferences migrate to MachineSpace.
    Space, Machine;

    companion object {
        /** A phone that never chose a grouping gets "By repository" (#2252, as iOS #2108); any stored choice keeps its own. */
        fun fromPreference(value: String?): AgentGrouping = when (value) {
            null, Repo.name -> Repo
            State.name -> State
            Todo.name -> Todo
            else -> MachineSpace
        }
        val selectable = listOf(Repo, MachineSpace, State, Todo)
    }
}

/** A group's place in "By repository": a repository, or one of its working trees (the main checkout or another). */
enum class RepoLevel { Repository, MainCheckout, Worktree }
enum class AgentQuickFilter(val state: AgentState?, val source: AgentSource? = null) {
    All(null), NeedsYou(AgentState.NeedsYou), Unread(AgentState.Unread), Working(AgentState.Working),
    /**
     * Working, but all it still runs in the background only waits (#6045, iOS `AgentFilter.backgroundWaiting`): a filter of
     * its own, and not Working. The hub has no state for it: a paged list asks for working rows ([state]) and keeps these.
     */
    BackgroundWaiting(AgentState.Working),
    /** Where the agent was opened (#2526): a botmux session (its Feishu group) or a herdr pane. */
    Feishu(null, AgentSource.Feishu), Terminal(null, AgentSource.Terminal);
    fun matches(session: AgentSession) = when {
        source != null -> session.session.agentSource == source
        this == BackgroundWaiting -> session.waitsInBackground
        this == Working -> session.state == AgentState.Working && !session.waitsInBackground
        else -> state == null || session.state == state
    }
    /** "Lark group" matches botmux sessions only, so it and a hidden botmux cannot both hold (web `botmuxAndFilter`). */
    val showsBotmux: Boolean get() = this == Feishu

    companion object {
        /**
         * "All", and the filters with agents right now; the chosen one stays while it empties, so it can be left (iOS
         * `QuickFilterBar`, web `shownFilters`). When the rest still do not fit a row, the bar scrolls sideways.
         */
        fun shown(selected: AgentQuickFilter, counts: Map<AgentQuickFilter, Int>): List<AgentQuickFilter> =
            entries.filter { it == All || it == selected || (counts[it] ?: 0) > 0 }
    }
}

data class AgentListGroup(
    val id: String,
    val title: CoreText,
    val rows: List<AgentSession>,
    val visibleRows: List<AgentSession>,
    val folded: Boolean = false,
    val parent: String? = null,
    val stateCounts: Map<AgentState, Int>? = null,
    val spoken: CoreText? = null,
    /** "By repository": the repository or working tree this group is. */
    val repo: RepoLevel? = null,
    /** The group this one sits in: a working tree's repository, whose own [rows] are then empty (its trees hold them). */
    val under: String? = null,
    /** Server membership for this exact group; null means its rows/counts describe the loaded window. */
    val totalCount: Int? = null,
)

data class AgentListResult(
    val counts: Map<AgentQuickFilter, Int>,
    val groups: List<AgentListGroup>,
    val unreadShown: List<String>,
    val quietTitle: CoreText? = null,
    val machines: List<AgentMachineGroup> = emptyList(),
    val children: Map<String, List<AgentSession>> = emptyMap(),
)

data class AgentMachineGroup(
    val machine: Machine,
    val groups: List<String>,
    val sessionCount: Int,
    val spaceCount: Int,
    val stateCounts: Map<AgentState, Int>,
    val folded: Boolean,
    /** "By repository": how many repositories it has. */
    val repoCount: Int? = null,
    /** How many of [stateCounts]'s working only wait in the background (#6045): its heading counts them apart. */
    val waitingInBackground: Int = 0,
) {
    val id: String get() = "machine/${machine.id}"
}

/**
 * State First's order, the index into By Status's sections ([AgentStateSections], iOS `AgentListOrder.rank`): Needs You →
 * Unread → Awaiting Reply → Working → Waiting in the background ([AgentSession.waitsInBackground]) → Waiting → Muted → Seen.
 * Muted agents that are working, finished-unread or waiting rank as Muted; a muted one that only waits for the user's
 * reply is just Seen ([AgentSession.awaitsReply]); one that needs you stays first.
 */
fun agentRank(session: AgentSession, mutes: AgentPreferences = AgentPreferences()): Int = when {
    session.state == AgentState.NeedsYou -> 0
    (session.state == AgentState.Working || session.state == AgentState.Unread || session.isWaiting) && mutes.mutes(session) -> 6
    session.state == AgentState.Unread -> 1
    session.awaitsReply(mutes) -> 2
    session.state == AgentState.Working -> if (session.waitsInBackground) 4 else 3
    session.isWaiting -> 5
    else -> 7
}

/** By Status's sections in rank order ([agentRank]): each one's key (its group is `state/<key>`) and title (iOS `stateTitles`). */
val AgentStateSections: List<Pair<String, CoreText.Key>> = listOf("needs" to CoreText.Key.StateNeedsYou, "unread" to CoreText.Key.StateUnread,
    "awaiting" to CoreText.Key.StateAwaitingReply, "working" to CoreText.Key.StateWorking, "background" to CoreText.Key.StateBackgroundWaiting,
    "waiting" to CoreText.Key.StateWaiting, "muted" to CoreText.Key.StateMuted, "idle" to CoreText.Key.StateIdle)

/** By Status's Seen, the long tail: it starts folded, and its id is kept in the folds once it is opened (iOS `foldedByDefault`). */
const val StateSeenGroup = "state/idle"
const val StateSeenOpened = "!state/idle"

/**
 * The list's own filter before the quick filter and the botmux switch: not the assistant, the machine, the search ([AgentQuery]:
 * its words in the session's fields, the branch among them, #4497, and its conditions, #4338 A10). [pins] and
 * [machineNames] are what `is:pinned` and `machine:` read; [now] dates `since:`.
 */
fun AgentListSettings.admits(s: persona.core.contracts.Session, pins: AgentPreferences = AgentPreferences(),
                             machineNames: Map<String, String> = emptyMap(), now: java.time.Instant = java.time.Instant.now()): Boolean =
    !s.assistant && (machineFilter.isEmpty() || machineFilter == s.machineId) &&
        (query.isEmpty() || s.key.path in queryAncestors ||
            AgentQuery.parse(query).admits(s, s.key.path in pins.sessions, machineNames[s.machineId], now))

/**
 * Whether the list of running sessions draws [row], its search, quick filter and botmux switch aside: pinned (under
 * Pinned), live, or a Codex thread of the past while those are included. [buildFlatAgentList] draws these rows, and a
 * subtask among them needs one of them above it to hang under ([codexTaskForest]).
 */
fun AgentListSettings.draws(row: AgentSession, pins: AgentPreferences): Boolean =
    row.id in pins.sessions || row.isLive || includeCodexHistory && row.session.isCodexTask

fun buildAgentList(
    sessions: List<AgentSession>, machines: List<Machine> = emptyList(),
    pins: AgentPreferences = AgentPreferences(), mutes: AgentPreferences = AgentPreferences(),
    settings: AgentListSettings = AgentListSettings(), todos: List<Todo> = emptyList(),
): AgentListResult {
    val tree = codexTaskForest(sessions, runs = { settings.draws(it, pins) })
    return buildFlatAgentList(tree.roots, machines, pins, mutes, tree.searchSettings(settings, pins, machines.associate { it.id to it.name }), todos)
        .copy(children = tree.children)
}

/**
 * The list of [sessions] as they come, no subtask hung under another here: the roots of the [codexTaskForest] its caller
 * built for the rows it draws, whose subtasks the caller hangs under them ([AgentListResult.children]).
 */
fun buildFlatAgentList(
    sessions: List<AgentSession>,
    machines: List<Machine> = emptyList(),
    pins: AgentPreferences = AgentPreferences(),
    mutes: AgentPreferences = AgentPreferences(),
    settings: AgentListSettings = AgentListSettings(),
    todos: List<Todo> = emptyList(),
): AgentListResult {
    val machineNames = machines.associate { it.id to it.name }
    val base = sessions.filter { settings.admits(it.session, pins, machineNames) && !(settings.hideBotmux && it.session.isBotmux) }
    val counts = AgentQuickFilter.entries.filter { it != AgentQuickFilter.All }
        .associateWith { filter -> base.count { !it.isNestedSubtask && filter.matches(it) } }
    val filtered = base.filter { settings.quick.matches(it) || it.id in settings.kept }
    val pinned = filtered.filter { it.id in pins.sessions }.sortedBy { pins.sessions.indexOf(it.id) }
    val live = filtered.filter { it.id !in pins.sessions && settings.draws(it, pins) }
    val quick = settings.quick != AgentQuickFilter.All
    // Under a quick filter, or sorted by "Recently updated", rows go by time only (iOS `AgentListOrder.byRank`).
    val byRank = !quick && settings.sort == AgentSort.State
    val latest = settings.sort == AgentSort.Updated
    val comparator = compareBy<AgentSession> { if (byRank) agentRank(it, mutes) else 0 }
        .thenByDescending { it.sortDate }.thenBy { it.session.sessionId }.thenBy { it.id }
    fun ordered(rows: List<AgentSession>) = rows.sortedWith(comparator)
    fun stays(row: AgentSession) = row.stays(mutes)
    fun showIdle(key: String) = key in settings.expandedIdle || settings.query.isNotEmpty()
    fun group(id: String, title: CoreText, rows: List<AgentSession>, visible: List<AgentSession>): AgentListGroup {
        val folded = id in settings.folded
        return AgentListGroup(id, title, rows, if (folded) emptyList() else visible, folded)
    }
    val result = mutableListOf<AgentListGroup>()
    if (pinned.isNotEmpty()) result += if (settings.grouping == AgentGrouping.MachineSpace || settings.grouping == AgentGrouping.Repo)
        group("pinned", CoreText.of(Key.AgentsPinned), pinned, pinned) else AgentListGroup("pinned", CoreText.of(Key.AgentsPinned), pinned, pinned)
    var quietTitle: CoreText? = null
    fun stateCounts(rows: List<AgentSession>) = AgentState.entries.associateWith { state -> rows.count { !it.isNestedSubtask && it.state == state } }
    when (settings.grouping) {
        AgentGrouping.Repo -> {
            // Computer → repository → working tree → sessions, from each cwd (web `buildRepoMachineTree`, iOS `RepoTree`).
            // A repository with nothing going on is quiet: folded, after the busy ones, like a quiet space; a quiet working
            // tree is folded too but keeps its place (main checkout first, then by name, as on iOS).
            fun quiet(members: List<AgentSession>) = !quick && members.none(::stays)
            fun section(id: String, title: CoreText, members: List<AgentSession>, level: RepoLevel, under: String?, machineFolded: Boolean): AgentListGroup {
                val isQuiet = quiet(members)
                val rows = ordered(members)
                val folded = if (isQuiet) id !in settings.expandedQuiet && settings.query.isEmpty() else id in settings.folded
                val visible = when {
                    folded || machineFolded -> emptyList()
                    isQuiet || quick || showIdle(id) -> rows
                    else -> rows.filter(::stays)
                }
                return AgentListGroup(id, title, rows, visible, folded, if (isQuiet) "quiet" else null, stateCounts(rows), repo = level, under = under)
            }
            val known = machines + live.map { it.session.machineId }.distinct()
                .filter { id -> machines.none { it.id == id } }.map { Machine(it, "") }
            val machineGroups = known.sortedBy { !it.online }.mapNotNull { machine ->
                val members = live.filter { it.session.machineId == machine.id }
                if (members.isEmpty()) return@mapNotNull null
                val machineFolded = "machine/${machine.id}" in settings.folded
                // "Recently updated": the busy repositories by their latest agent (iOS `RepoTree` under `AgentSort.updated`).
                val repos = agentRepos(members).let { all ->
                    if (latest) all.sortedWith(compareBy<AgentRepo> { quiet(it.members) }
                        .thenByDescending { repo -> repo.members.mapNotNull { it.sortDate }.maxOrNull() }.thenBy { it.id })
                    else all.sortedBy { quiet(it.members) }
                }
                val groups = repos.flatMap { repo ->
                    // A quiet repository opens to all its sessions in one list, as iOS's "Idle repositories" does. A repository with only
                    // its main checkout is drawn as one level but keeps the checkout's id, so a session opened under a quick
                    // filter stays in the same group when a second working tree appears or goes (retainOpenedRows).
                    if (repo.flat) listOf(section(repo.worktrees[0].id, repoTitle(repo.name), repo.members, RepoLevel.Repository, null, machineFolded))
                    else if (quiet(repo.members)) listOf(section(repo.id, repoTitle(repo.name), repo.members, RepoLevel.Repository, null, machineFolded))
                    else listOf(AgentListGroup(repo.id, repoTitle(repo.name), emptyList(), emptyList(), repo.id in settings.folded,
                        stateCounts = stateCounts(repo.members), repo = RepoLevel.Repository)) +
                        repo.worktrees.map { tree ->
                            section(tree.id, repoTitle(tree.name), tree.members, if (tree.main) RepoLevel.MainCheckout else RepoLevel.Worktree, repo.id, machineFolded)
                        }
                }
                result += groups
                AgentMachineGroup(machine, groups.map { it.id }, members.count { !it.isNestedSubtask }, agentSpaces(members).size, stateCounts(members),
                    machineFolded, repoCount = repos.size, waitingInBackground = members.count { !it.isNestedSubtask && it.waitsInBackground })
            }
            return AgentListResult(counts, result, filtered.filter { it.state == AgentState.Unread }.map { it.id },
                machines = machineGroups)
        }
        AgentGrouping.MachineSpace -> {
            // Keep each session in a single leaf group; machine headers only reference those leaves.
            val treeRows = filtered.filter { it.isLive || settings.includeCodexHistory && it.session.isCodexTask }
            val known = machines + treeRows.map { it.session.machineId }.distinct()
                .filter { id -> machines.none { it.id == id } }.map { Machine(it, "") }
            val machineGroups = known.sortedBy { !it.online }.mapNotNull { machine ->
                val members = treeRows.filter { it.session.machineId == machine.id }
                if (members.isEmpty()) return@mapNotNull null
                val nested = buildFlatAgentList(members, pins = pins, mutes = mutes,
                    settings = settings.copy(grouping = AgentGrouping.Space))
                val machineFolded = "machine/${machine.id}" in settings.folded
                result += nested.groups.filterNot { it.id == "pinned" }.map { group ->
                    val folded = group.folded || group.id in settings.folded
                    group.copy(folded = folded, visibleRows = if (machineFolded || folded) emptyList() else group.visibleRows)
                }
                val spaces = agentSpaces(members)
                AgentMachineGroup(machine, nested.groups.filterNot { it.id == "pinned" }.map { it.id }, members.count { !it.isNestedSubtask }, spaces.size,
                    AgentState.entries.associateWith { state -> members.count { !it.isNestedSubtask && it.state == state } },
                    machineFolded, waitingInBackground = members.count { !it.isNestedSubtask && it.waitsInBackground })
            }
            return AgentListResult(counts, result, filtered.filter { it.state == AgentState.Unread }.map { it.id },
                machines = machineGroups)
        }
        AgentGrouping.State -> {
            // A section per rank, each folded from its header; Seen starts folded and its header is its one fold, with no
            // "N more idle" row inside (iOS `stateSections`). A search opens them all.
            val byRank = live.groupBy { agentRank(it, mutes) }
            AgentStateSections.forEachIndexed { rank, (key, title) ->
                val rows = ordered(byRank[rank].orEmpty())
                val id = "state/$key"
                val folded = settings.query.isEmpty() &&
                    (id in settings.folded || id == StateSeenGroup && StateSeenOpened !in settings.folded)
                if (rows.isNotEmpty()) result += AgentListGroup(id, CoreText.of(title), rows, if (folded) emptyList() else rows, folded)
            }
        }
        AgentGrouping.Todo -> {
            // As iOS's By To-do (`TodosLogic.buildTodoAgentGroups`, #4338 A5): a section per open to-do with agents on it, in
            // the to-dos' order; then "Opened by hand" (open) and "No to-do", one list each, as the web's todos.ts (#2608).
            val sessionMap = sessions.associateBy { it.session.key }
            val uncompletedTodos = todos.filter { !it.isDone }.sortedWith(
                compareBy<Todo> { it.state { k -> sessionMap[k]?.session }.rank }
                    .thenByDescending { it.created ?: java.time.Instant.EPOCH }
                    .thenBy { it.id }
            )
            fun counts(rows: List<AgentSession>) = AgentState.entries.associateWith { state -> rows.count { !it.isNestedSubtask && it.state == state } }
            fun section(id: String, title: CoreText, members: List<AgentSession>, folded: Boolean) {
                val rows = ordered(members)
                result += AgentListGroup(id, title, rows, if (folded) emptyList() else rows, folded, stateCounts = counts(rows))
            }
            // Each agent sits under one to-do only, as on the web (`assignedSessionKeys`): a to-do's agents list can still hold
            // an agent the session's own `todo` (pushed with the session, so newer) already moved to another, and one row twice
            // would repeat its list key. Unlike the web, which takes the first to-do listing it, the session's own open to-do
            // wins here, so a moved agent shows under its new to-do at once; else the first to-do listing it.
            val openIds = uncompletedTodos.map { it.id }.toSet()
            val home = live.associate { row ->
                val own = row.session.todo?.id?.takeIf { it.isNotEmpty() && it in openIds }
                row.session.key to (own ?: uncompletedTodos.firstOrNull { todo -> todo.currentAgents.any { it.key == row.session.key } }?.id)
            }
            val claimed = home.filterValues { it != null }.keys
            val linked = uncompletedTodos.mapNotNull { todo ->
                val matched = live.filter { home[it.session.key] == todo.id }
                if (matched.isEmpty()) null else todo to matched
            }
            linked.forEach { (todo, rows) -> section("todo/${todo.id}", CoreText.plain(todo.title), rows, "todo/${todo.id}" in settings.folded) }
            val unlinked = live.filter { it.session.key !in claimed }
            // Opened by hand in herdr and on no to-do: a section of its own before "No to-do", open.
            val manual = unlinked.filter { it.session.isManualHerdr }
            if (manual.isNotEmpty()) section(TodoManualGroup, CoreText.of(Key.AgentsManual), manual, TodoManualGroup in settings.folded)
            val rest = unlinked.filterNot { it.session.isManualHerdr }
            if (rest.isNotEmpty()) {
                // Folded under the sections above unless something in it needs you; with none above it is the whole list, so
                // open (iOS `TodoAgentGroup.folded`). The user's own fold wins either way ([TodoNoneOpened] once they opened
                // it), and a search opens it.
                val byDefault = (linked.isNotEmpty() || manual.isNotEmpty()) && rest.none { it.state == AgentState.NeedsYou }
                val folded = settings.query.isEmpty() &&
                    (TodoNoneGroup in settings.folded || (byDefault && TodoNoneOpened !in settings.expandedIdle))
                section(TodoNoneGroup, CoreText.of(Key.AgentsNoTodo), rest, folded)
            }
        }
        AgentGrouping.Machine -> (if (latest) machines.sortedByDescending { m ->
            live.filter { it.session.machineId == m.id }.mapNotNull { it.sortDate }.maxOrNull() } else machines).forEach { machine ->
            val rows = ordered(live.filter { it.session.machineId == machine.id })
            if (rows.isNotEmpty()) result += group("machine/${machine.id}", CoreText.plain(machine.name), rows,
                if (quick || showIdle("machine-${machine.id}")) rows else rows.filter(::stays))
        }
        AgentGrouping.Space -> {
            val spaces = agentSpaces(live)
            fun pinnedSpace(space: AgentSpace) = space.preferenceKey in pins.spaces
            fun mutedSpace(space: AgentSpace) = space.preferenceKey in mutes.spaces
            fun leading(space: AgentSpace) = pinnedSpace(space) && !mutedSpace(space)
            val active = spaces.filter(::leading) + if (quick) {
                spaces.filterNot(::leading).sortedWith(compareByDescending<AgentSpace> { it.updatedAt }.thenBy { it.id })
            } else {
                // "Recently updated": the busy spaces by their latest agent, not by state (muted ones still after the rest).
                val busy = if (latest) spaces.sortedWith(compareByDescending<AgentSpace> { space -> space.members.mapNotNull { it.sortDate }.maxOrNull() }
                    .thenBy { it.id }) else spaces
                busy.filter { !it.quiet(mutes) && !pinnedSpace(it) && !mutedSpace(it) } +
                    busy.filter { !it.quiet(mutes) && mutedSpace(it) }
            }
            val quiet = if (quick) emptyList() else spaces.filter { it.quiet(mutes) && !leading(it) }
            if (quiet.isNotEmpty()) quietTitle = CoreText.of(Key.AgentsQuietSpaces)
            fun spaceGroup(space: AgentSpace, isQuiet: Boolean): AgentListGroup {
                val rows = if (isQuiet) space.members else ordered(space.members)
                val folded = if (isQuiet) space.id !in settings.expandedQuiet && settings.query.isEmpty()
                    else space.id in settings.folded
                val visible = when {
                    folded -> emptyList()
                    isQuiet || quick || showIdle(space.id) -> rows
                    else -> rows.filter(::stays)
                }
                val states = listOf(AgentState.NeedsYou, AgentState.Working, AgentState.Unread, AgentState.Idle, AgentState.Unknown)
                val stateCounts = states.associateWith { state -> rows.count { !it.isNestedSubtask && it.state == state } }
                val labels = listOf(AgentState.NeedsYou to Key.CountNeedsYou, AgentState.Working to Key.CountWorking, AgentState.Unread to Key.CountUnread)
                val spoken = labels.mapNotNull { (state, label) -> stateCounts.getValue(state).takeIf { it > 0 }?.let { CoreText.of(label, it) } } +
                    (stateCounts.getValue(AgentState.Idle) + stateCounts.getValue(AgentState.Unknown))
                        .takeIf { it > 0 }?.let { listOf(CoreText.of(Key.CountIdle, it)) }.orEmpty()
                return AgentListGroup(space.id, CoreText.plain(space.name), rows, visible, folded,
                    if (isQuiet) "quiet" else null, stateCounts, CoreText.joined(CoreText.of(Key.ListSeparator), spoken))
            }
            result += active.map { spaceGroup(it, false) }
            result += quiet.map { spaceGroup(it, true) }
        }
    }
    return AgentListResult(counts, result, filtered.filter { it.state == AgentState.Unread }.map { it.id }, quietTitle)
}

/** A repository's or working tree's heading: its name, "Other" for sessions without a cwd ([UnnamedRepo]). */
private fun repoTitle(name: String): CoreText = if (name == UnnamedRepo) CoreText.of(Key.RepoOther) else CoreText.plain(name)

/** "By to-do"'s sections that are not a to-do: agents opened by hand, then those on none. */
/**
 * A long run of a section's rows (iOS `AgentRowCap`): the first [ROWS], then "Show All N"; a run only a few over the cap
 * shows them all (no "Show All 22").
 */
object AgentRowCap {
    const val ROWS = 20
    fun capped(count: Int, cap: Int = ROWS): Int? = if (count <= cap + 5) null else cap
    /** In `expandedIdle` while the section [groupId] shows all its rows. */
    fun key(groupId: String) = "all:$groupId"
}

const val TodoManualGroup = "todo/manual"
const val TodoNoneGroup = "todo/none"
/** In `expandedIdle` once the user opened "No to-do", which is folded by default under to-dos. */
const val TodoNoneOpened = "todo-none"

/** Opened by hand in herdr: not started by Persona (nor spawned through it), web `isManualHerdr`. */
val persona.core.contracts.Session.isManualHerdr: Boolean get() = spaceKind == "herdr" && !startedByPersona
