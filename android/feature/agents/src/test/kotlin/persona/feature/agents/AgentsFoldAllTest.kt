package persona.feature.agents

import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import androidx.lifecycle.viewModelScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.*
import kotlinx.serialization.json.JsonObject
import org.junit.After
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.domain.sort.*

@OptIn(ExperimentalCoroutinesApi::class)
class AgentsFoldAllTest {
    private lateinit var model: AgentsViewModel
    private val preferences = AgentsPreferences(object : DataStore<Preferences> {
        override val data = MutableStateFlow<Preferences>(emptyPreferences())
        override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
            transform(data.value).also { data.value = it }
    }, object : HubPreferences {
        override val preferences = MutableStateFlow(HubPreferencesSnapshot())
        override suspend fun refresh() = Unit
        override suspend fun togglePin(path: String, space: Boolean) = Unit
        override suspend fun toggleMute(path: String, space: Boolean) = Unit
        override suspend fun markSeen(session: SessionKey, version: String) = Unit
    })
    private val sessions = listOf("shown", "hidden").flatMap { machine ->
        (0..1).map { pane -> Session(machine, "pane-$pane", state = "working", spaceId = "space", spaceName = "project",
            slotId = "tab", slotIndex = 1, paneId = "space:p$pane", paneIndex = pane) }
    }

    @Before fun setMain() { Dispatchers.setMain(StandardTestDispatcher()) }
    @After fun tearDown() {
        if (::model.isInitialized) model.viewModelScope.cancel()
        Dispatchers.resetMain()
    }

    private suspend fun TestScope.startModel(extra: List<Session> = emptyList()) {
        preferences.grouping(AgentGrouping.MachineSpace)
        model = AgentsViewModel(object : SessionsRepository {
            override val sessions = MutableStateFlow(this@AgentsFoldAllTest.sessions + extra)
            override val machines = MutableStateFlow(listOf(Machine("shown", online = true), Machine("hidden", online = true)))
            override suspend fun refresh() = Unit
        }, object : InteractionCenter {
            override val pending = MutableStateFlow<List<Interaction>>(emptyList())
            override suspend fun refresh() = Unit
            override suspend fun resolve(id: String, resolution: Resolution) = Unit
        }, object : HubConnection {
            override val state = MutableStateFlow(ConnectionState.Online)
            override val events = MutableSharedFlow<ServerEvent>()
            override fun reconnect() = Unit
            override fun send(message: JsonObject) = true
        }, object : MessageSender {
            override val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = Unit
        }, preferences)
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { model.page.collect {} }
        runCurrent()
    }

    // "Expand all" opens a tab group folded by hand, as it opens every space (#2792); "Collapse all" leaves tab groups alone
    @Test fun expandAllOpensATabGroupFoldedByHand() = runTest {
        startModel()
        model.machine("shown")
        runCurrent()
        val tab = agentTabs(model.page.value.list.groups.single().rows).values.single()
        model.foldTab(tab)
        runCurrent()
        assertEquals(setOf(tab.foldKey), preferences.snapshots.first().foldedTabs)
        model.foldAll(true)
        runCurrent()
        assertEquals(setOf(tab.foldKey), preferences.snapshots.first().foldedTabs)
        model.foldAll(false)
        runCurrent()
        assertTrue(preferences.snapshots.first().foldedTabs.isEmpty())
        val page = model.page.value
        val tree = agentTreeEntries(page.list, page.settings.machineFilter, tabs = true, foldedTabs = page.preferences.foldedTabs)
        assertFalse(tree.filterIsInstance<TreeTabEntry>().single().folded)
    }

    // #4338 A6, as iOS: folds stay through a search, a range change and a grouping change, and come back after a relaunch.
    @Test fun foldsAreKeptThroughSearchRangeGroupingAndRelaunch() = runTest {
        startModel()
        val id = model.page.value.list.groups.first().id
        model.fold(model.page.value.list.groups.first())
        runCurrent()
        assertEquals(setOf(id), model.page.value.settings.folded)
        assertEquals(setOf(id), preferences.snapshots.first().folds[AgentGrouping.MachineSpace])
        // A search opens every fold while it runs and gives the kept ones back when it clears.
        model.query("pane")
        runCurrent()
        assertTrue(model.page.value.settings.folded.isEmpty())
        model.query("")
        runCurrent()
        assertEquals(setOf(id), model.page.value.settings.folded)
        model.range(AgentRange.All)
        runCurrent()
        assertEquals(setOf(id), model.page.value.settings.folded)
        // Another grouping has folds of its own; coming back brings these back.
        model.grouping(AgentGrouping.State)
        runCurrent()
        assertFalse(id in model.page.value.settings.folded)
        model.grouping(AgentGrouping.MachineSpace)
        runCurrent()
        assertEquals(setOf(id), model.page.value.settings.folded)
        // A new model, as after a relaunch, reads them back.
        model.viewModelScope.cancel()
        startModel()
        assertEquals(setOf(id), model.page.value.settings.folded)
    }

    // #4338 A5, as iOS: By Status's Seen starts folded and opens from its header; opening it is kept, "Expand all" opens it,
    // and rows under a section that names their state leave the word out (Muted mixes states, so its rows keep it).
    @Test fun byStatusSeenOpensFromItsHeaderAndRowsLeaveTheirStateOut() = runTest {
        startModel(listOf(Session("shown", "seen", state = "idle", title = "Seen one")))
        model.grouping(AgentGrouping.State)
        runCurrent()
        fun seen() = model.page.value.list.groups.single { it.id == StateSeenGroup }
        assertTrue(seen().folded)
        model.fold(seen())
        runCurrent()
        assertFalse(seen().folded)
        assertEquals(setOf(StateSeenOpened), preferences.snapshots.first().folds[AgentGrouping.State])
        model.fold(seen())
        runCurrent()
        assertTrue(seen().folded)
        assertTrue(preferences.snapshots.first().folds[AgentGrouping.State].orEmpty().isEmpty())
        model.foldAll(true)
        runCurrent()
        model.foldAll(false)
        runCurrent()
        assertFalse(seen().folded)
        val rows = agentTreeEntries(model.page.value.list, "").filterIsInstance<TreeRowEntry>()
        assertTrue(rows.isNotEmpty())
        assertTrue(rows.all { it.inStateSection })
    }

    // #4338 A11, as iOS's Collapse All: the sections fold, the computer headings stay as they are.
    @Test fun collapseAllLeavesTheComputerHeadingsAsTheyAre() = runTest {
        startModel()
        model.foldAll(true)
        runCurrent()
        assertTrue(model.page.value.list.machines.isNotEmpty())
        assertTrue(model.page.value.list.machines.none { it.folded })
        assertTrue(model.page.value.list.groups.all { it.folded })
        model.foldAll(false)
        runCurrent()
        val hidden = model.page.value.list.machines.single { it.machine.id == "hidden" }
        model.fold(hidden)
        runCurrent()
        model.foldAll(true)
        runCurrent()
        assertTrue(model.page.value.list.machines.single { it.machine.id == "hidden" }.folded)
        assertFalse(model.page.value.list.machines.single { it.machine.id == "shown" }.folded)
    }
}
