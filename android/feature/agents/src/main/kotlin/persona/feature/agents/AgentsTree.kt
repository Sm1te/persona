package persona.feature.agents

import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import persona.core.contracts.AgentState
import persona.core.contracts.Machine
import persona.core.contracts.TodoState
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcon
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.sort.*

// The Agents tree (computer → space → session, or computer → repository → worktree → session under "By repository") as lazy list lines. The lines are drawn by the design system's Agents components
// (AgentTreeHeader, AgentSpaceHeader, AgentRow, AgentIdleRow, MachineChip; docs/android/android-design.md 2.4).

/** One line of the flattened tree, in list order. [key] is the lazy item key. */
internal sealed interface AgentTreeEntry { val key: String }

/** A computer. Hidden when the list is already filtered to one computer (the title and chip say which). */
internal data class TreeMachineEntry(val machine: AgentMachineGroup, val first: Boolean) : AgentTreeEntry {
    override val key: String get() = "machine-header:${machine.id}"
}

/**
 * A group heading. [space] groups are herdr workspaces of a computer (or its repositories and their working trees
 * under "By repository"); the others ("Pinned", states, history projects) are top-level sections. [depth] 1 sits under a computer
 * heading, a working tree one step further in. Quiet spaces (every agent finished and seen)
 * come after the active ones of their computer, folded by default and drawn weaker.
 */
internal data class TreeGroupEntry(val group: AgentListGroup, val space: Boolean, val depth: Int, val first: Boolean) : AgentTreeEntry {
    val quiet: Boolean get() = group.parent == QUIET
    override val key: String get() = "group:${group.id}"
}

/**
 * A session. [located] rows sit outside the computer tree and show where they run. A row under a [TreeTabEntry] says
 * whether it is its tab's [launcher] (the one that started it) or a [branch] the launcher split off, drawn one step further in.
 * A [nested] row is a subtask drawn under its parent's [TreeChildrenEntry].
 */
internal data class TreeRowEntry(
    val row: AgentSession, val depth: Int, val first: Boolean, val last: Boolean, val located: Boolean,
    val launcher: Boolean = false, val branch: Boolean = false, val nested: Boolean = false,
    /** Under a By Status section that names its state, so the row leaves the word out (iOS `inStateSection`); Muted mixes states. */
    val inStateSection: Boolean = false,
) : AgentTreeEntry {
    override val key: String get() = "session:${row.id}"
}

/**
 * A herdr tab holding several agents of the space (#2320, docs/protocol/agent-states.md "Several panes in one tab"): its row
 * speaks for the most urgent of them and says how many there are; its agents follow it in pane order unless [folded].
 * Open by default (the point is to see which agents belong together); folded by hand it stays folded ([AgentTab.foldKey]).
 */
internal data class TreeTabEntry(val tab: AgentTab, val depth: Int, val first: Boolean, val last: Boolean, val folded: Boolean) : AgentTreeEntry {
    override val key: String get() = "tab:${tab.key}"
}

/**
 * Whether Collapse All is done: every section folded. It leaves the computer headings as they are (#4338 A11), so they
 * say nothing about it.
 */
internal val AgentListResult.allCollapsed: Boolean get() = groups.isNotEmpty() && groups.all { it.folded }

/** A section's run of rows past [AgentRowCap]: "Show All [count]", or "Collapse" once they all show ([expanded]). */
internal data class TreeShowAllEntry(val group: AgentListGroup, val count: Int, val depth: Int, val expanded: Boolean) : AgentTreeEntry {
    override val key: String get() = "all:${group.id}"
}

/** Finished sessions of an active space that are tucked away until asked for. */
internal data class TreeIdleEntry(val group: AgentListGroup, val hidden: Int, val depth: Int) : AgentTreeEntry {
    override val key: String get() = "idle:${group.id}"
}

internal data class TreeChildrenEntry(val parent: AgentSession, val count: Int, val depth: Int, val expanded: Boolean) : AgentTreeEntry {
    override val key: String get() = "children:${parent.id}"
}

private const val QUIET = "quiet"

/**
 * Flattens the list into tree lines: "Pinned" first, then every computer with its spaces, then any other groups. With
 * [tabs] ("By machine", as the web's sidebar) a space's agents that share a herdr tab are one [TreeTabEntry] with its agents
 * under it, listed where its most urgent agent would be; a tab is shown whole once one of its agents is, and the
 * tabs in [foldedTabs] ([AgentTab.foldKey]) are folded to their row. [mutes] pick a tab's lead as the list ranks them.
 */
internal fun agentTreeEntries(
    list: AgentListResult, machineFilter: String, tabs: Boolean = false, foldedTabs: Set<String> = emptySet(),
    mutes: AgentPreferences = AgentPreferences(),
    expandedTasks: Set<String> = emptySet(),
    /**
     * The sections showing all their rows ([AgentRowCap.key]); null: no section is capped (a search, a quick filter or a
     * paged list shows every row it leaves, as iOS).
     */
    showAll: Set<String>? = null,
): List<AgentTreeEntry> {
    val entries = mutableListOf<AgentTreeEntry>()
    fun group(group: AgentListGroup, space: Boolean, depth: Int, first: Boolean) {
        entries += TreeGroupEntry(group, space, depth, first)
        if (group.folded) return
        val byTab = if (tabs && space) agentTabs(group.rows, mutes) else emptyMap()
        // each line is a row of its own or a tab of several, in the group's order
        val lines = mutableListOf<Any>()
        val placed = mutableSetOf<String>()
        val cap = showAll?.let { AgentRowCap.capped(group.visibleRows.size) }
        val open = cap != null && AgentRowCap.key(group.id) in showAll.orEmpty()
        val drawn = if (cap != null && !open) group.visibleRows.take(cap) else group.visibleRows
        drawn.forEach { row ->
            val tab = row.session.tabKey?.let(byTab::get)
            if (tab == null) lines += row else if (placed.add(tab.key)) lines += tab
        }
        val inStateSection = group.id.startsWith("state/") && group.id != "state/muted"
        var shown = 0
        lines.forEachIndexed { index, line ->
            val firstLine = index == 0
            val lastLine = index == lines.lastIndex
            when (line) {
                is AgentSession -> { shown++; entries += TreeRowEntry(line, depth, firstLine, lastLine, located = !space, inStateSection = inStateSection) }
                is AgentTab -> {
                    val folded = line.foldKey in foldedTabs
                    shown += line.members.size
                    entries += TreeTabEntry(line, depth, firstLine, lastLine && folded, folded)
                    if (!folded) line.members.forEachIndexed { at, row ->
                        val branch = line.isBranch(row)
                        entries += TreeRowEntry(row, depth + if (branch) 2 else 1, first = false, last = lastLine && at == line.members.lastIndex,
                            located = false, launcher = row.id == line.launcher?.id, branch = branch)
                    }
                }
            }
        }
        if (cap != null) entries += TreeShowAllEntry(group, group.visibleRows.size, depth, expanded = open)
        val hidden = group.rows.size - shown - (group.visibleRows.size - drawn.size)
        if (hidden > 0) entries += TreeIdleEntry(group, hidden, depth)
    }
    val byId = list.groups.associateBy { it.id }
    val treeIds = list.machines.flatMap { it.groups }.toSet()
    list.groups.filter { it.id == "pinned" }.forEach { group(it, space = false, depth = 0, first = entries.isEmpty()) }
    // Computer headings only while the list holds more than one computer, as iOS's `byHost` (#4338 A7): one computer's
    // name over its own list says nothing.
    val headed = machineFilter.isEmpty() && list.machines.size > 1
    list.machines.forEach { machine ->
        // A computer whose sessions are all pinned has nothing left to hold here; "Pinned" says where they run.
        if (machine.groups.none(byId::containsKey)) return@forEach
        if (headed) entries += TreeMachineEntry(machine, first = entries.isEmpty())
        if (headed && machine.folded) return@forEach
        machine.groups.mapNotNull(byId::get).forEachIndexed { index, it ->
            // A working tree sits in its repository, gone while the repository is folded.
            if (it.under != null && byId[it.under]?.folded != false) return@forEachIndexed
            group(it, space = true, depth = (if (headed) 1 else 0) + (if (it.under != null) 1 else 0), first = index == 0)
        }
    }
    // Sections outside the computer tree; one [AgentListGroup.under] another ("By to-do"'s todos under their project, #3156)
    // sits one step in and is gone while that one is folded.
    list.groups.filter { it.id != "pinned" && it.id !in treeIds }.forEach {
        if (it.under != null && byId[it.under]?.folded != false) return@forEach
        group(it, space = false, depth = if (it.under != null) 1 else 0, first = entries.isEmpty())
    }
    val nested = mutableListOf<AgentTreeEntry>()
    fun append(entry: AgentTreeEntry) {
        nested += entry
        if (entry !is TreeRowEntry) return
        val children = list.children[entry.row.id].orEmpty()
        if (children.isEmpty()) return
        val expanded = entry.row.id in expandedTasks
        nested += TreeChildrenEntry(entry.row, children.size, entry.depth + 1, expanded)
        if (expanded) children.forEachIndexed { index, child ->
            append(TreeRowEntry(child, entry.depth + 1, false, index == children.lastIndex, entry.located, branch = true, nested = true))
        }
    }
    entries.forEach(::append)
    return nested
}

/** A computer in the filter strip: how many of its sessions the list would show. */
@Immutable
internal data class MachineCount(val machine: Machine, val count: Int)

/** Every computer, online ones first (the tree's order), with the count the list would show for it. */
internal fun machineCounts(machines: List<Machine>, rows: List<AgentSession>): List<MachineCount> {
    val counts = rows.filterNot { it.isNestedSubtask }.groupingBy { it.session.machineId }.eachCount()
    return machines.sortedBy { !it.online }.map { MachineCount(it, counts[it.id] ?: 0) }
}

internal val Machine.deviceIcon: PhIcon get() = when (icon) {
    "server" -> PhIcons.HardDrives
    "desktop" -> PhIcons.Desktop
    else -> PhIcons.Laptop
}

// ------------------------------------------------------------------ filter strip

/** "All machines" and every computer, side by side; tapping the selected computer goes back to all of them. */
@Composable
internal fun MachineFilterRow(machines: List<MachineCount>, total: Int, selected: String, onSelect: (String) -> Unit, modifier: Modifier = Modifier) {
    Row(
        modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(vertical = Tokens.Space.xs),
        horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalAlignment = Alignment.CenterVertically,
    ) {
        MachineChip(stringResource(R.string.agents_chip_all_machines), total, selected.isEmpty(), onClick = { onSelect("") })
        machines.forEach { (machine, count) ->
            val on = machine.id == selected
            MachineChip(machine.displayName, count, on, onClick = { onSelect(if (on) "" else machine.id) }, icon = machine.deviceIcon, online = machine.online)
        }
    }
}

/**
 * A computer heading's counts, as iOS's `StateCounts` (#4338 A7): needing you, working, unread, then idle; the agents that
 * only wait in the background are out of Working and drawn apart ([AgentMachineGroup.waitingInBackground], a clock).
 */
internal val AgentMachineGroup.headingCounts: List<Pair<Status, Int>>
    get() = listOf(
        Status.NeedsYou to (stateCounts[AgentState.NeedsYou] ?: 0),
        Status.Working to ((stateCounts[AgentState.Working] ?: 0) - waitingInBackground).coerceAtLeast(0),
        Status.Unread to (stateCounts[AgentState.Unread] ?: 0),
        Status.Idle to (stateCounts[AgentState.Idle] ?: 0) + (stateCounts[AgentState.Unknown] ?: 0),
    )

/**
 * A By Status section's one count, in its state's color (iOS `SectionHeader` with the section's tone): Awaiting reply
 * takes Unread's, Waiting in the background Working's, Muted and Seen the idle one. Null for every other group.
 */
internal val AgentListGroup.stateSectionCounts: List<Pair<Status, Int>>?
    get() {
        val status = when (id.takeIf { it.startsWith("state/") }?.removePrefix("state/")) {
            "needs" -> Status.NeedsYou
            "unread", "awaiting" -> Status.Unread
            "working", "background" -> Status.Working
            "waiting" -> Status.Waiting
            "muted", "idle" -> Status.Idle
            else -> return null
        }
        return listOf(status to (totalCount ?: rows.count { !it.isNestedSubtask }))
    }

/** "By to-do"'s "Opened by hand" and "No to-do": one count of their agents, as iOS's sections without a to-do. */
internal val AgentListGroup.todoLooseCounts: List<Pair<Status, Int>>?
    get() = if (id == TodoManualGroup || id == TodoNoneGroup) listOf(Status.Idle to rows.count { !it.isNestedSubtask }) else null

/** A to-do's count in its state's color (iOS `todoTone`): needing you, unread, working, else plain. */
internal val TodoState?.countTone: Status
    get() = when (this) {
        TodoState.NeedsYou -> Status.NeedsYou
        TodoState.Unread -> Status.Unread
        TodoState.Working -> Status.Working
        else -> Status.Idle
    }

internal val AgentListGroup.headingCounts: List<Pair<Status, Int>>
    get() = listOf(AgentState.NeedsYou, AgentState.Working, AgentState.Unread).map { state ->
        // A repository with working trees holds no rows itself (its trees do): it counts from stateCounts.
        state.designStatus to if (totalCount != null || rows.isEmpty()) stateCounts?.get(state) ?: 0
            else rows.count { !it.isNestedSubtask && it.state == state }
    }
