package persona.android.capsule

import java.lang.reflect.Proxy
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.emptyPreferences
import persona.feature.agents.AgentsPreferences
import persona.feature.agents.listSession
import persona.core.domain.sort.AgentPreferences
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Before
import org.junit.Test
import persona.android.ui.ShellSession
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.feature.chat.ChatAppBindings
import persona.feature.chat.ChatWiringSource
import persona.platform.capsule.CapsuleState
import persona.platform.capsule.CapsuleCompletion
import persona.platform.capsule.CapsuleNoticeKind
import kotlinx.serialization.json.JsonObject

/** Regression cases from #2124: one surface, the original session, and an explicit destination. */
@OptIn(ExperimentalCoroutinesApi::class)
class CapsuleSessionsTest {
    @Before fun chinese() = persona.android.ZhText.install()

    private fun agent(machine: String, id: String, state: String = "working", quiet: Boolean = false) =
        Session(machine, id, title = "$machine $id", state = state, quiet = quiet)

    @Test fun unreadMeansTheSameUnseenCompletionAsAgents() {
        val unseen = agent("home", "unseen", "unread").copy(updatedAt = "2026-11-05T08:00:00Z", rev = 3)
        val seen = unseen.copy(sessionId = "seen")
        val muted = unseen.copy(sessionId = "muted")
        val quiet = unseen.copy(sessionId = "quiet", quiet = true)
        val assistant = unseen.copy(sessionId = "assistant", assistant = true)
        val preferences = persona.feature.agents.AgentsPreferencesSnapshot(
            mutes = AgentPreferences(sessions = listOf(muted.key.path)), seen = mapOf(seen.key.path to "${seen.updatedAt}|${seen.rev}"))
        val rows = capsuleUnreadSessions(listOf(unseen, seen, muted, quiet, assistant,
            agent("home", "working"), agent("home", "waiting", "needs_you"), agent("home", "read", "idle")), preferences)
        assertEquals(listOf(unseen), rows)
        assertEquals(listOf(seen.copy(rev = 4)), capsuleUnreadSessions(listOf(seen.copy(rev = 4)), preferences))
    }

    /** An agent another one created (#4494): a runtime's, its creator named by session id, or a Codex thread's subagent. */
    private fun subtask(id: String, parent: String, state: String, codex: Boolean = false) = agent("home", id, state).let {
        if (codex) it.copy(agent = "codex", sourceKind = "codex_app_server", sourceThreadId = id, parentThreadId = parent, isSubagent = true)
        else it.copy(agent = "claude", sourceKind = "runtime", parentSessionId = parent, isSubagent = true)
    }
    private fun codexThread(id: String, state: String) = agent("home", id, state).copy(agent = "codex", sourceKind = "codex_app_server", sourceThreadId = id)

    @Test fun unreadLeavesOutTheSubtasksTheAgentsListFoldsAndCountsWhatItCounts() {
        val preferences = persona.feature.agents.AgentsPreferencesSnapshot()
        val lead = agent("home", "lead", "unread").copy(agent = "claude", sourceKind = "runtime")
        val sessions = listOf(lead, subtask("review", "lead", "unread"), codexThread("codex", "unread"), subtask("probe", "codex", "unread", codex = true),
            // Its creator is not listed: a row of its own in Agents, not counted there either.
            subtask("orphan", "gone", "unread"), subtask("asks", "lead", "needs_you"), subtask("busy", "lead", "working"))
        assertEquals(listOf(lead.sessionId, "codex"), capsuleUnreadSessions(sessions, preferences).map { it.sessionId })
        val agents = persona.core.domain.sort.buildAgentList(sessions.map { it.listSession(preferences) })
        assertEquals(agents.counts[persona.core.domain.sort.AgentQuickFilter.Unread], capsuleUnreadSessions(sessions, preferences).size)
        // Without subtasks the list is as it was.
        val plain = sessions.map { it.copy(isSubagent = false, parentSessionId = null, parentThreadId = null) }
        assertEquals(listOf("lead", "review", "codex", "probe", "orphan"), capsuleUnreadSessions(plain, preferences).map { it.sessionId })
    }

    @Test fun aSubtaskIsNeitherUnreadNorCountedNorActivityOfItsOwn() = runTest {
        val lead = agent("home", "lead", "unread").copy(agent = "claude", sourceKind = "runtime")
        val codex = codexThread("codex", "unread")
        val fixture = Fixture(backgroundScope, listOf(subtask("review", "lead", "unread"), lead, subtask("probe", "codex", "unread", codex = true), codex))
        runCurrent()
        var content = fixture.source.content.value
        assertEquals(listOf(lead, codex).map { capsuleId(it.key) }, content.unreadEntries.map { it.id })
        assertEquals(2, content.badgeCount)
        assertEquals(2, content.sections.first().count)
        assertEquals(setOf(lead, codex).map { capsuleId(it.key) }.toSet(), content.noticeTargetIds)
        // Both parents read: the subtasks are read under them in Agents, so nothing is left here, nor keeps the capsule up.
        fixture.sessions.value = fixture.sessions.value.map { if (it.isSubagent) it else it.copy(state = "idle") }
        runCurrent()
        content = fixture.source.content.value
        assertEquals(0, content.badgeCount)
        assertEquals(0, content.sections.first().count)
        assertFalse(content.hasActivity)
        // Working under a working parent, the parent is what goes on; with its creator stopped, a working one is itself.
        fixture.sessions.value = listOf(subtask("review", "lead", "working"), lead.copy(state = "working"))
        runCurrent()
        assertEquals(capsuleId(lead.key), fixture.source.content.value.headline?.id)
        fixture.sessions.value = listOf(subtask("review", "lead", "working"), lead.copy(state = "idle"))
        runCurrent()
        assertTrue(fixture.source.content.value.hasActivity)
        assertEquals(capsuleId(SessionKey("home", "review")), fixture.source.content.value.headline?.id)
    }

    @Test fun aSubtaskFinishingIsNoCompletionNotice() = runTest {
        val lead = agent("home", "lead").copy(agent = "claude", sourceKind = "runtime", rev = 1)
        val review = subtask("review", "lead", "working").copy(rev = 1)
        val fixture = Fixture(backgroundScope, listOf(lead, review), fresh = listOf(lead, review))
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(lead, review.copy(state = "unread", rev = 2))))
        runCurrent()
        assertTrue(notices.isEmpty())
        fixture.connection.events.emit(SessionsEvent("home", listOf(lead.copy(state = "unread", rev = 2), review.copy(state = "unread", rev = 2))))
        runCurrent()
        assertEquals(listOf(capsuleId(lead.key)), notices.map { it.targetId })
    }

    @Test fun theThreeEntrancesHaveIndependentCountsAndUnreadInitiallyShowsAList() = runTest {
        val done = agent("home", "done", "unread")
        val fixture = Fixture(backgroundScope, listOf(done, agent("home", "working")))
        fixture.inbox.value = persona.feature.inbox.InboxSnapshot(records = listOf(inboxRecord("mail:1")))
        runCurrent()
        val content = fixture.source.content.value
        assertEquals(listOf("未读", "主对话", "收件箱"), content.sections.map { it.title })
        assertEquals(listOf(1, 0, 1), content.sections.map { it.count })
        assertEquals(UnreadSection, content.sectionId)
        assertTrue(content.listing)
        assertNull(content.selectedId)
        assertEquals(listOf(capsuleId(done.key)), content.entries.map { it.id })
        assertTrue(fixture.observed.isEmpty())
        assertTrue(content.hasActivity)
        assertEquals(listOf(capsuleId(done.key)), content.unreadEntries.map { it.id })
        assertEquals(1, content.badgeCount)
    }

    @Test fun withoutUnreadTheInitialEntranceIsMainAndWorkIsNotAnUnreadCount() = runTest {
        val fixture = Fixture(backgroundScope, listOf(agent("home", "working"), agent("home", "waiting", "needs_you")))
        runCurrent()
        assertEquals(MainSection, fixture.source.content.value.sectionId)
        assertEquals(0, fixture.source.content.value.sections.first().count)
        assertEquals(0, fixture.source.content.value.badgeCount)
        assertTrue(fixture.source.content.value.hasActivity)
        fixture.source.selectSection(InboxSection)
        fixture.sessions.value += agent("home", "later", "unread")
        runCurrent()
        fixture.source.shown(true)
        runCurrent()
        assertEquals(MainSection, fixture.source.content.value.sectionId)
        assertFalse(fixture.source.content.value.listing)
    }

    @Test fun quietAndMutedWorkDoesNotMakeTheGlobalCapsuleActive() = runTest {
        val quiet = agent("home", "quiet", quiet = true)
        val muted = agent("home", "muted")
        val fixture = Fixture(backgroundScope, listOf(quiet, muted))
        fixture.hubPreferences.value = HubPreferencesSnapshot(mutes = HubPreferenceSet(sessions = listOf(muted.key.path)))
        runCurrent()
        assertFalse(fixture.source.content.value.hasActivity)
        assertEquals(0, fixture.source.content.value.badgeCount)
    }

    @Test fun aSeenVersionDoesNotKeepTheGlobalCapsuleActive() = runTest {
        val done = agent("home", "done", "unread").copy(updatedAt = "2026-11-05T08:00:00Z", rev = 3)
        val fixture = Fixture(backgroundScope, listOf(done))
        fixture.hubPreferences.value = HubPreferencesSnapshot(seen = mapOf(done.key.path to "${done.updatedAt}|${done.rev}"))
        runCurrent()
        assertFalse(fixture.source.content.value.hasActivity)
        assertEquals(0, fixture.source.content.value.badgeCount)
        assertEquals(0, fixture.source.content.value.sections.first().count)
        fixture.sessions.value = listOf(done.copy(rev = 4))
        runCurrent()
        assertTrue(fixture.source.content.value.hasActivity)
        assertEquals(1, fixture.source.content.value.badgeCount)
    }

    @Test fun selectingAnotherHubsUnreadObservesAndSendsOnlyItsQualifiedKey() = runTest {
        val primary = agent("machine", "same", "unread")
        val secondary = agent("machine~g0a1b2c3d", "same", "unread")
        assertNotEquals(capsuleId(primary.key), capsuleId(secondary.key))
        val fixture = Fixture(backgroundScope, listOf(primary, secondary))
        runCurrent()
        fixture.source.shown(true)
        fixture.source.select(capsuleId(secondary.key))
        runCurrent()
        assertEquals(secondary.key, fixture.observed.last())
        fixture.source.send(fixture.source.content.value.selectedId, "Continue the selected task")
        assertEquals(listOf(secondary.key to "Continue the selected task"), fixture.sent)
        assertTrue(fixture.mainSent.isEmpty())
        fixture.source.open(capsuleId(secondary.key))
        assertEquals(secondary.key to false, fixture.opened.single())
    }

    @Test fun mainDoesNotFollowAnUrgentTaskAndUsesTheExistingMainSession() = runTest {
        val urgent = agent("home", "urgent", "needs_you")
        val main = agent("home", "main", "idle").copy(assistant = true)
        val fixture = Fixture(backgroundScope, listOf(urgent), AssistantSnapshot(main.key, listOf(main)))
        runCurrent()
        fixture.source.selectSection(MainSection)
        runCurrent()
        assertEquals(MainEntry, fixture.source.content.value.selectedId)
        fixture.source.send(MainEntry, "Talk to Persona")
        assertEquals(listOf(main.key to "Talk to Persona"), fixture.sent)
        fixture.source.open(MainEntry)
        assertEquals(main.key to true, fixture.opened.single())
    }

    @Test fun anEmptyMainUsesTheOriginalMainSenderAndKeepsItsDraftIdentityWhenCreated() = runTest {
        val fixture = Fixture(backgroundScope, listOf(agent("home", "urgent", "needs_you")))
        fixture.bindings.value = fixture.binding()
        runCurrent()
        assertEquals(MainEntry, fixture.source.content.value.selectedId)
        fixture.source.send(MainEntry, "Start Persona")
        assertEquals(listOf("Start Persona"), fixture.mainSent)
        val created = agent("home", "created-main").copy(assistant = true)
        fixture.assistant.value = AssistantSnapshot(created.key, listOf(created))
        runCurrent()
        assertEquals(MainEntry, fixture.source.content.value.selectedId)
        fixture.source.send(MainEntry, "Continue Persona")
        assertEquals(listOf(created.key to "Continue Persona"), fixture.sent)
    }

    @Test fun selectedConversationSurvivesReadStateChangesUntilReturningToTheList() = runTest {
        val done = agent("home", "done", "unread")
        val fixture = Fixture(backgroundScope, listOf(done))
        runCurrent()
        fixture.source.shown(true)
        fixture.source.select(capsuleId(done.key))
        runCurrent()
        fixture.sessions.value = listOf(done.copy(state = "idle"), agent("home", "urgent", "needs_you"))
        runCurrent()
        assertEquals(capsuleId(done.key), fixture.source.content.value.selectedId)
        assertEquals(0, fixture.source.content.value.sections.first().count)
        fixture.source.send(capsuleId(done.key), "The draft still belongs here")
        assertEquals(listOf(done.key to "The draft still belongs here"), fixture.sent)
        fixture.source.select(null)
        runCurrent()
        assertTrue(fixture.source.content.value.listing)
        assertTrue(fixture.source.content.value.entries.isEmpty())
    }

    @Test fun inboxUsesTheActualActionRowsIncludingMergedRequestsInsteadOfActionCount() {
        val primary = agent("home", "waiting", "needs_you")
        val other = agent("home~g0a1b2c3d", "waiting", "needs_you")
        val snapshot = persona.feature.inbox.InboxSnapshot(
            records = listOf(inboxRecord("mail:action"), inboxRecord("mail:fyi", "fyi"), inboxRecord("mail:done", done = true)),
            interactions = listOf(inboxRequest("ask-a", primary), inboxRequest("ask-b~g0a1b2c3d", other)),
            sessions = listOf(primary, other).map(::personaAgent),
        )
        val inbox = capsuleInbox(snapshot)
        assertEquals(3, inbox.entries.size)
        assertEquals(setOf("inbox:item:mail:action", "inbox:request:ask-a", "inbox:request:ask-b~g0a1b2c3d"), inbox.targets.keys)
        assertEquals(CapsuleInboxTarget.Request(other.key, "ask-b~g0a1b2c3d"), inbox.targets["inbox:request:ask-b~g0a1b2c3d"])
    }

    @Test fun requestsFromTheSameConversationKeepTheirDistinctHeadlines() {
        val session = agent("home", "waiting", "needs_you")
        val question = inboxRequest("question", session).copy(title = "采用哪个实现方案？", summary = null)
        val plan = inboxRequest("plan", session).copy(title = "计划等你批准", summary = "")
        val permission = inboxRequest("permission", session).copy(title = "请求使用 Bash", summary = "运行测试")
        val rows = capsuleInbox(persona.feature.inbox.InboxSnapshot(interactions = listOf(question, plan, permission),
            sessions = listOf(personaAgent(session)))).entries.associateBy { it.id }
        assertEquals(1, rows.values.map { it.title }.distinct().size)
        assertEquals("采用哪个实现方案？", rows.getValue("inbox:request:question").summary)
        assertEquals("计划等你批准", rows.getValue("inbox:request:plan").summary)
        assertEquals("请求使用 Bash · 运行测试", rows.getValue("inbox:request:permission").summary)
        assertEquals(3, rows.values.map { it.summary }.distinct().size)
    }

    @Test fun inboxItemsOpenTheExistingRawItemRouteAndRequestsOpenTheirQualifiedSession() = runTest {
        val other = agent("home~g0a1b2c3d", "waiting", "needs_you")
        val fixture = Fixture(backgroundScope, listOf(other))
        fixture.bindings.value = fixture.binding()
        fixture.inbox.value = persona.feature.inbox.InboxSnapshot(records = listOf(inboxRecord("lark:a:b")),
            interactions = listOf(inboxRequest("ask~g0a1b2c3d", other)), sessions = listOf(personaAgent(other)))
        runCurrent()
        fixture.source.selectSection(InboxSection)
        runCurrent()
        fixture.source.select("inbox:item:lark:a:b")
        assertEquals(listOf("lark:a:b"), fixture.openedInbox)
        fixture.source.select("inbox:request:ask~g0a1b2c3d")
        assertEquals(listOf(other.key to false), fixture.opened)
        assertNull(fixture.source.content.value.selectedId)
    }

    @Test fun aRequestOutsideAnySessionOpensItsCardInsteadOfABlankConversation() = runTest {
        val fixture = Fixture(backgroundScope)
        fixture.bindings.value = fixture.binding()
        fixture.inbox.value = persona.feature.inbox.InboxSnapshot(
            interactions = listOf(inboxRequest("script", Session("home", "")).copy(title = "Deploy?")))
        runCurrent()
        fixture.source.selectSection(InboxSection)
        runCurrent()
        fixture.source.select("inbox:request:script")
        assertEquals(emptyList<Pair<SessionKey?, Boolean>>(), fixture.opened)
        assertEquals(listOf(persona.core.navigation.RequestRef("home", "script")), fixture.openedCards)
    }

    @Test fun anOldInboxCannotOpenTheSameRawIdInANewPairing() = runTest {
        val fixture = Fixture(backgroundScope)
        fixture.bindings.value = fixture.binding()
        fixture.inbox.value = persona.feature.inbox.InboxSnapshot(records = listOf(inboxRecord("mail:1")))
        runCurrent()
        fixture.source.selectSection(InboxSection)
        runCurrent()
        fixture.bindings.value = fixture.binding(HubScope("other", "pairing"))
        assertTrue(runCatching { fixture.source.select("inbox:item:mail:1") }.isFailure)
        assertTrue(fixture.openedInbox.isEmpty())
    }

    @Test fun changingTasksNeverShowsThePreviousTasksReplyUnderTheNewTitle() = runTest {
        val first = agent("home", "first", "unread")
        val second = agent("home~g0a1b2c3d", "second", "unread")
        val fixture = Fixture(backgroundScope, listOf(first, second))
        runCurrent()
        fixture.source.shown(true)
        fixture.source.select(capsuleId(first.key))
        runCurrent()
        fixture.messages.getValue(first.key).value = ChatSnapshot(loading = false,
            items = listOf(Item("private-first", role = "assistant", kind = "text", text = "First task reply")))
        runCurrent()
        val wrongReplies = mutableListOf<String>()
        val collector = backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) {
            fixture.source.content.collect { content ->
                if (content.selectedId == capsuleId(second.key)) wrongReplies += content.messages.map { it.id }
            }
        }
        fixture.source.select(capsuleId(second.key))
        runCurrent()
        assertFalse(wrongReplies.contains("private-first"))
        assertTrue(fixture.source.content.value.messages.isEmpty())
        collector.cancel()
    }

    @Test fun aLateMainBindingEnablesSendingWithoutAnUnrelatedPush() = runTest {
        val fixture = Fixture(backgroundScope)
        runCurrent()
        assertFalse(fixture.source.content.value.canSend)
        fixture.bindings.value = fixture.binding()
        runCurrent()
        assertTrue(fixture.source.content.value.canSend)
        fixture.bindings.value = fixture.binding(HubScope("different", "pairing"))
        runCurrent()
        assertFalse(fixture.source.content.value.canSend)
        assertTrue(runCatching { fixture.source.send(MainEntry, "Wrong pairing") }.isFailure)
        assertTrue(fixture.mainSent.isEmpty())
    }

    @Test fun readOnlyAndRemovedSessionsRejectSendsWithoutTouchingTheOutbox() = runTest {
        val readOnly = agent("home", "history", "unread").copy(sourceKind = "codex_history", capabilities = SessionCapabilities(read = true))
        val fixture = Fixture(backgroundScope, listOf(readOnly))
        runCurrent()
        fixture.source.select(capsuleId(readOnly.key))
        runCurrent()
        assertFalse(fixture.source.content.value.canSend)
        assertTrue(runCatching { fixture.source.send(capsuleId(readOnly.key), "Do something") }.isFailure)
        fixture.sessions.value = emptyList()
        runCurrent()
        assertTrue(runCatching { fixture.source.send(capsuleId(readOnly.key), "Stale task") }.isFailure)
        assertTrue(fixture.sent.isEmpty())
    }

    @Test fun cancellingThePairingScopePreventsOldCapsuleActionsFromSending() = runTest {
        val scope = CoroutineScope(backgroundScope.coroutineContext + SupervisorJob())
        val task = agent("home", "task", "unread")
        val fixture = Fixture(scope, listOf(task))
        runCurrent()
        fixture.source.select(capsuleId(task.key))
        runCurrent()
        scope.cancel()
        runCurrent()
        assertTrue(runCatching { fixture.source.send(capsuleId(task.key), "Old pairing") }.isFailure)
        assertTrue(fixture.sent.isEmpty())
    }

    @Test fun replyHistoryUsesOnlyTextAndDoesNotDuplicateAcknowledgedOutboxMessages() = runTest {
        val task = agent("home", "task", "unread")
        val fixture = Fixture(backgroundScope, listOf(task))
        runCurrent()
        fixture.source.select(capsuleId(task.key))
        fixture.source.shown(true)
        runCurrent()
        fixture.messages.getValue(task.key).value = ChatSnapshot(loading = false, items = listOf(
            Item("user", role = "user", kind = "text", text = "Question", clientMsgId = "accepted"),
            Item("thought", role = "assistant", kind = "thinking", text = "Private reasoning"),
            Item("answer", role = "assistant", kind = "text", text = "Answer"),
        ))
        fixture.outbox.value = listOf(
            OutboxMessage("accepted", task.key, "Question", DeliveryState.Sending),
            OutboxMessage("pending", task.key, "Follow-up", DeliveryState.WaitingForNetwork),
            OutboxMessage("other", SessionKey("another", "task"), "Other task", DeliveryState.Sending),
        )
        runCurrent()
        assertEquals(listOf("user", "answer", "pending"), fixture.source.content.value.messages.map { it.id })
        assertTrue(fixture.source.content.value.messages.last().text.contains("等待网络"))
    }

    /** A refused message says it was not sent and to open the chat, never the hub's code (#2821). */
    @Test fun aRefusedMessageSaysNotSentWithoutTheHubsCode() = runTest {
        val task = agent("home", "task", "unread")
        val fixture = Fixture(backgroundScope, listOf(task))
        runCurrent()
        fixture.source.select(capsuleId(task.key))
        fixture.source.shown(true)
        fixture.outbox.value = listOf(
            OutboxMessage("asleep", task.key, "Synthetic one", DeliveryState.Refused, error = "node_unreachable"),
            OutboxMessage("stale", task.key, "Synthetic two", DeliveryState.Rejected, error = "stale_session"),
        )
        runCurrent()
        val shown = fixture.source.content.value.messages.associate { it.id to it.text }
        assertEquals("Synthetic one\n发送未成功，请打开会话", shown["asleep"])
        assertEquals("Synthetic two\n发送未成功，请打开会话", shown["stale"])
    }

    @Test fun completionUsesRawEdgesEvenWhenTheRepositoryStateNeverPublishesWorking() = runTest {
        val task = agent("home", "task", "unread").copy(rev = 1)
        val manual = agent("home", "manual", "idle")
        val fixture = Fixture(backgroundScope, listOf(task, manual))
        fixture.bindings.value = fixture.binding()
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.source.shown(true)
        fixture.source.select(capsuleId(manual.key))
        runCurrent()
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task)))
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(state = "working"))))
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(state = "needs_you"))))
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(rev = 2))))
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(rev = 3))))
        runCurrent()
        assertEquals(listOf(completionId(task.copy(rev = 2))), notices.map { it.id })
        assertEquals(task.displayTitle, notices.single().title)
        assertEquals(capsuleId(manual.key), fixture.source.content.value.selectedId)
        fixture.source.openCompletion(notices.single())
        runCurrent()
        assertEquals(UnreadSection, fixture.source.content.value.sectionId)
        assertEquals(capsuleId(task.key), fixture.source.content.value.selectedId)
        assertEquals(task.key, fixture.observed.last())
        assertTrue(fixture.opened.isEmpty())
        fixture.source.send(capsuleId(task.key), "Reply to the completed task")
        assertEquals(listOf(task.key to "Reply to the completed task"), fixture.sent)
        val late = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { late += it } }
        runCurrent()
        assertTrue(late.isEmpty())
    }

    @Test fun enablingWithFreshWorkingAuthorityRecognizesTheVeryFirstLiveCompletion() = runTest {
        val task = agent("home", "task").copy(rev = 1)
        val fixture = Fixture(backgroundScope, listOf(task), fresh = listOf(task))
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "unread", rev = 2))))
        runCurrent()
        assertEquals(listOf(completionId(task.copy(state = "unread", rev = 2))), notices.map { it.id })
        fixture.connection.state.value = ConnectionState.WaitingForNetwork
        runCurrent()
        fixture.connection.state.value = ConnectionState.Online
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "unread", rev = 2))))
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "working", rev = 3))))
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "unread", rev = 4))))
        runCurrent()
        assertEquals(listOf(2L, 4L).map { completionId(task.copy(state = "unread", rev = it)) }, notices.map { it.id })
    }

    @Test fun restoredWorkingCacheDoesNotClaimTheFirstUnreadAsANewCompletion() = runTest {
        val task = agent("home", "task")
        val fixture = Fixture(backgroundScope, listOf(task))
        fixture.bindings.value = fixture.binding()
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "unread", rev = 2))))
        runCurrent()
        assertTrue(notices.isEmpty())
    }

    @Test fun reconnectAndUpdatingDiscardArmedWorkAndDoNotReplayOldCompletions() = runTest {
        val task = agent("home", "task").copy(rev = 1)
        val fixture = Fixture(backgroundScope, listOf(task))
        fixture.bindings.value = fixture.binding()
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task)))
        for (offline in listOf(ConnectionState.WaitingForNetwork, ConnectionState.Updating)) {
            fixture.connection.state.value = offline
            runCurrent()
            fixture.connection.state.value = ConnectionState.Online
            runCurrent()
            fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(state = "unread", rev = 2))))
            assertTrue(notices.isEmpty())
            fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task)))
        }
        fixture.connection.events.emit(SessionsEvent(task.machineId, listOf(task.copy(state = "unread", rev = 3))))
        runCurrent()
        assertEquals(1, notices.size)
    }

    @Test fun completionFiltersMuteQuietAndInterruptedAtTheEdgeWithoutUnmuteReplay() = runTest {
        val muted = agent("home", "muted")
        val quiet = agent("home", "quiet", quiet = true)
        val interrupted = agent("home", "interrupted")
        val fixture = Fixture(backgroundScope, listOf(muted, quiet, interrupted))
        fixture.bindings.value = fixture.binding()
        fixture.hubPreferences.value = HubPreferencesSnapshot(mutes = HubPreferenceSet(sessions = listOf(muted.key.path)))
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(muted, quiet, interrupted)))
        fixture.connection.events.emit(SessionsEvent("home", listOf(muted.copy(state = "unread", rev = 2),
            quiet.copy(state = "unread", rev = 2), interrupted.copy(state = "unread", rev = 2, turnError = "Model stopped"))))
        fixture.hubPreferences.value = HubPreferencesSnapshot()
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(muted, quiet.copy(quiet = false), interrupted)))
        fixture.connection.events.emit(SessionsEvent("home", listOf(muted.copy(state = "unread", rev = 2),
            quiet.copy(state = "unread", rev = 2, quiet = false), interrupted.copy(state = "unread", rev = 2))))
        runCurrent()
        assertTrue(notices.isEmpty())
    }

    @Test fun completionRoutesTheCapturedMainInAnotherHubWithoutChangingTheManualConversation() = runTest {
        val selected = agent("machine", "same", "unread")
        val main = agent("machine~g0a1b2c3d", "same").copy(assistant = true)
        val fixture = Fixture(backgroundScope, listOf(selected), AssistantSnapshot(main.key, listOf(main)))
        fixture.connection.state.value = ConnectionState.WaitingForNetwork
        fixture.bindings.value = fixture.binding()
        val secondary = TestConnection()
        fixture.byMachine[main.machineId] = MutableStateFlow(fixture.binding(HubScope("other", "pairing"), secondary))
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        fixture.source.shown(true)
        fixture.source.select(capsuleId(selected.key))
        runCurrent()
        val before = fixture.source.content.value
        fixture.connection.events.emit(SessionsEvent(main.machineId, listOf(main)))
        fixture.connection.events.emit(SessionsEvent(main.machineId, listOf(main.copy(state = "unread", rev = 2))))
        runCurrent()
        assertEquals(before.sectionId, fixture.source.content.value.sectionId)
        assertEquals(before.selectedId, fixture.source.content.value.selectedId)
        fixture.source.send(before.selectedId, "Keep the manual draft target")
        assertEquals(listOf(selected.key to "Keep the manual draft target"), fixture.sent)
        fixture.source.openCompletion(notices.single())
        runCurrent()
        assertEquals(MainSection, fixture.source.content.value.sectionId)
        assertEquals(MainEntry, fixture.source.content.value.selectedId)
        assertEquals(main.key, fixture.observed.last())
        assertTrue(fixture.opened.isEmpty())
        fixture.source.send(MainEntry, "Reply to the completed main")
        assertEquals(listOf(selected.key to "Keep the manual draft target", main.key to "Reply to the completed main"), fixture.sent)
        fixture.assistant.value = AssistantSnapshot()
        runCurrent()
        assertTrue(runCatching { fixture.source.openCompletion(notices.single()) }.isFailure)
    }

    @Test fun cancelledScopeRejectsACapturedCompletionClick() = runTest {
        val task = agent("home", "task")
        val owner = CoroutineScope(SupervisorJob() + UnconfinedTestDispatcher(testScheduler))
        try {
            val fixture = Fixture(owner, listOf(task))
            fixture.bindings.value = fixture.binding()
            val notices = mutableListOf<CapsuleCompletion>()
            backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
            runCurrent()
            fixture.connection.events.emit(SessionsEvent("home", listOf(task)))
            fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(state = "unread", rev = 2))))
            runCurrent()
            owner.cancel()
            assertTrue(runCatching { fixture.source.openCompletion(notices.single()) }.exceptionOrNull() is kotlinx.coroutines.CancellationException)
            assertTrue(fixture.opened.isEmpty())
        } finally { owner.cancel() }
    }

    @Test fun oneTapUsesTheCurrentAppConversationAndKeepsItAfterOtherRepliesArrive() = runTest {
        val active = agent("home", "current", "idle")
        val unread = agent("home", "newest", "unread")
        val fixture = Fixture(backgroundScope, listOf(active, unread), current = active.key)
        runCurrent()
        fixture.source.shown(true)
        runCurrent()
        assertEquals(capsuleId(active.key), fixture.source.content.value.selectedId)
        fixture.sessions.value += agent("home", "later", "unread")
        fixture.source.shown(false)
        fixture.source.shown(true)
        runCurrent()
        assertEquals(capsuleId(active.key), fixture.source.content.value.selectedId)
        val fallback = Fixture(backgroundScope, listOf(unread))
        runCurrent()
        fallback.source.shown(true)
        runCurrent()
        assertEquals(capsuleId(unread.key), fallback.source.content.value.selectedId)
        assertFalse(fallback.source.content.value.listing)
    }

    @Test fun tappingAfterCollapsingEitherListRestoresTheLastConversationAndItsSendTarget() = runTest {
        val task = agent("home", "task", "unread")
        val fixture = Fixture(backgroundScope, listOf(task))
        runCurrent()
        fixture.source.shown(true)
        runCurrent()
        for (section in listOf(UnreadSection, InboxSection)) {
            fixture.source.selectSection(section)
            runCurrent()
            assertTrue(fixture.source.content.value.listing)
            fixture.source.shown(false)
            fixture.source.shown(true)
            runCurrent()
            assertFalse(fixture.source.content.value.listing)
            assertEquals(capsuleId(task.key), fixture.source.content.value.selectedId)
            assertTrue(fixture.source.content.value.canSend)
            fixture.source.send(capsuleId(task.key), "Preserve the draft target after $section")
        }
        assertEquals(listOf(task.key, task.key), fixture.sent.map { it.first })
    }

    @Test fun onlyTheVerifiedReplyActuallyViewedInTheSelectedConversationIsAcknowledged() = runTest {
        val first = agent("home", "first", "unread").copy(rev = 1)
        val other = agent("home~g0a1b2c3d", "second", "unread").copy(rev = 2)
        val fixture = Fixture(backgroundScope, listOf(first, other))
        fixture.bindings.value = fixture.binding()
        runCurrent()
        fixture.source.viewed(capsuleId(first.key), "reply", "${capsuleId(first.key)}||1")
        fixture.source.shown(true)
        runCurrent()
        fixture.source.select(capsuleId(other.key))
        runCurrent()
        fixture.messages.getValue(other.key).value = ChatSnapshot(loading = false, verified = false,
            items = listOf(Item("reply", role = "assistant", kind = "text", text = "Verified answer")))
        runCurrent()
        fixture.source.viewed(capsuleId(other.key), "reply", "${capsuleId(other.key)}||2")
        runCurrent()
        assertTrue(fixture.read.isEmpty())
        fixture.messages.getValue(other.key).value = fixture.messages.getValue(other.key).value.copy(verified = true)
        runCurrent()
        fixture.source.viewed(capsuleId(first.key), "reply", requireNotNull(fixture.source.content.value.readVersion))
        fixture.source.viewed(capsuleId(other.key), "not-visible", requireNotNull(fixture.source.content.value.readVersion))
        runCurrent()
        assertTrue(fixture.read.isEmpty())
        assertTrue(fixture.source.viewed(capsuleId(other.key), "reply", requireNotNull(fixture.source.content.value.readVersion)))
        runCurrent()
        assertEquals(listOf(other.key to "|2"), fixture.read)
        assertEquals(1, fixture.source.content.value.badgeCount)
        assertEquals(capsuleId(other.key), fixture.source.content.value.selectedId)
        assertFalse(fixture.source.content.value.noticeTargetIds.orEmpty().contains(capsuleId(other.key)))
        assertTrue(fixture.hubPreferences.value.seen[first.key.path] == null)
        fixture.source.select(null)
        runCurrent()
        fixture.source.viewed(capsuleId(first.key), "reply", "${capsuleId(first.key)}||1")
        runCurrent()
        assertEquals(1, fixture.read.size)
    }

    @Test fun aNewerRevisionCannotBeAcknowledgedByAnOlderDisplayedReply() = runTest {
        val task = agent("home", "task", "unread").copy(rev = 1)
        val fixture = Fixture(backgroundScope, listOf(task))
        fixture.bindings.value = fixture.binding()
        runCurrent()
        fixture.source.shown(true)
        runCurrent()
        fixture.messages.getValue(task.key).value = ChatSnapshot(loading = false,
            items = listOf(Item("old", role = "assistant", kind = "text", text = "Old reply")))
        runCurrent()
        val oldVersion = requireNotNull(fixture.source.content.value.readVersion)
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(rev = 2))))
        fixture.sessions.value = listOf(task.copy(rev = 2))
        runCurrent()
        assertNull(fixture.source.content.value.readVersion)
        assertFalse(fixture.source.viewed(capsuleId(task.key), "old", oldVersion))
        assertTrue(fixture.read.isEmpty())
        fixture.messages.getValue(task.key).value = ChatSnapshot(loading = false,
            items = listOf(Item("new", role = "assistant", kind = "text", text = "New reply")))
        runCurrent()
        val newVersion = requireNotNull(fixture.source.content.value.readVersion)
        assertNotEquals(oldVersion, newVersion)
        assertTrue(fixture.source.viewed(capsuleId(task.key), "new", newVersion))
        assertEquals(listOf(task.key to "|2"), fixture.read)
    }

    @Test fun theMainConversationsUnreadReplyCountsWithoutEnteringTheAgentsList() = runTest {
        val main = agent("home", "main", "unread").copy(assistant = true, rev = 3)
        val fixture = Fixture(backgroundScope, assistantState = AssistantSnapshot(main.key, listOf(main)))
        runCurrent()
        assertEquals(1, fixture.source.content.value.badgeCount)
        assertEquals(0, fixture.source.content.value.sections.first().count)
        fixture.hubPreferences.value = HubPreferencesSnapshot(seen = mapOf(main.key.path to "|3"))
        runCurrent()
        assertEquals(0, fixture.source.content.value.badgeCount)
    }

    @Test fun anIncomingUnreadReplyWithoutObservedWorkProducesANewMessageAndIgnoresRevisionOnlyChanges() = runTest {
        val task = agent("home", "task", "idle").copy(lastText = "Before", updatedAt = "2026-11-05T08:00:00Z", rev = 1)
        val fixture = Fixture(backgroundScope, listOf(task), fresh = listOf(task))
        val notices = mutableListOf<CapsuleCompletion>()
        backgroundScope.launch(UnconfinedTestDispatcher(testScheduler)) { fixture.source.completions.collect { notices += it } }
        runCurrent()
        val reply = task.copy(state = "unread", lastText = "New answer", updatedAt = "2026-11-05T09:00:00Z", rev = 2)
        fixture.connection.events.emit(SessionsEvent("home", listOf(reply)))
        fixture.connection.events.emit(SessionsEvent("home", listOf(reply.copy(rev = 3))))
        runCurrent()
        assertEquals(listOf(CapsuleNoticeKind.NewMessage), notices.map { it.kind })
        assertEquals(notices.single(), fixture.source.content.value.pendingCompletion)
        fixture.source.noticeShown(notices.single().id)
        runCurrent()
        assertNull(fixture.source.content.value.pendingCompletion)
    }

    @Test fun anUnshownNoticeSurvivesSourceRecreationAndShownOrReadRepliesDoNotReplay() = runTest {
        val task = agent("home", "task", "idle").copy(lastText = "Before", updatedAt = "2026-11-05T08:00:00Z", rev = 1)
        val memory = CapsuleNoticeMemory()
        val fixture = Fixture(backgroundScope, listOf(task), fresh = listOf(task), memory = memory)
        runCurrent()
        val reply = task.copy(state = "unread", lastText = "Arrived while locked", updatedAt = "2026-11-05T09:00:00Z", rev = 2)
        fixture.connection.events.emit(SessionsEvent("home", listOf(reply)))
        runCurrent()
        val pending = requireNotNull(memory.pending)
        val restored = Fixture(backgroundScope, listOf(reply), fresh = listOf(reply), memory = memory)
        runCurrent()
        assertEquals(pending.id, restored.source.content.value.pendingCompletion?.id)
        restored.source.noticeShown(pending.id)
        val shown = Fixture(backgroundScope, listOf(reply), fresh = listOf(reply), memory = memory)
        runCurrent()
        assertNull(shown.source.content.value.pendingCompletion)
        assertNull(memory.pending)
        shown.hubPreferences.value = HubPreferencesSnapshot(seen = mapOf(reply.key.path to "${reply.updatedAt}|${reply.rev}"))
        runCurrent()
        assertTrue(shown.source.content.value.noticeTargetIds.orEmpty().isEmpty())
    }

    @Test fun aReplyMissedWhileTheProcessWasGoneIsComparedBeforeTheFreshSeedIsStored() = runTest {
        val before = agent("home", "task", "idle").copy(lastText = "Before", updatedAt = "2026-11-05T08:00:00Z", rev = 1)
        val memory = CapsuleNoticeMemory()
        Fixture(backgroundScope, listOf(before), fresh = listOf(before), memory = memory)
        runCurrent()
        val reply = before.copy(state = "unread", lastText = "Arrived without a running process", updatedAt = "2026-11-05T09:00:00Z", rev = 2)
        val restored = Fixture(backgroundScope, listOf(reply), fresh = listOf(reply), memory = memory)
        runCurrent()
        assertEquals(CapsuleNoticeKind.NewMessage, restored.source.content.value.pendingCompletion?.kind)
        assertEquals(capsuleId(reply.key), restored.source.content.value.pendingCompletion?.targetId)
    }

    @Test fun initialHistoryIsABaselineAndManualConversationConsumesRatherThanDefersNotices() = runTest {
        val task = agent("home", "task", "unread").copy(lastText = "Historical answer", updatedAt = "2026-11-05T08:00:00Z", rev = 1)
        val memory = CapsuleNoticeMemory()
        val fixture = Fixture(backgroundScope, listOf(task), fresh = listOf(task), memory = memory)
        runCurrent()
        assertNull(fixture.source.content.value.pendingCompletion)
        fixture.source.shown(true)
        runCurrent()
        fixture.connection.events.emit(SessionsEvent("home", listOf(task.copy(lastText = "During manual chat", updatedAt = "2026-11-05T09:00:00Z", rev = 2))))
        runCurrent()
        assertNull(memory.pending)
        fixture.source.shown(false)
        runCurrent()
        assertNull(fixture.source.content.value.pendingCompletion)
    }

    private fun personaAgent(session: Session) = persona.core.domain.sort.AgentSession(session)
    private fun inboxRecord(id: String, tier: String = "action", done: Boolean = false) = persona.feature.inbox.InboxRecord(
        persona.core.domain.inbox.InboxItem(id, title = "Item $id", preview = "Needs an answer", tier = tier,
            doneAt = if (done) "2026-11-05T08:00:00Z" else null))
    private fun inboxRequest(id: String, session: Session) = persona.core.domain.inbox.InboxInteraction(id, session.machineId, session.sessionId,
        sessionTitle = session.displayTitle, summary = "Waiting for an answer")

    /** The capsule over fake repositories, its flows unchanged; [CapsuleSubtasksShotsTest] draws it too. */
    internal class Fixture(
        val scope: CoroutineScope,
        initial: List<Session> = emptyList(),
        assistantState: AssistantSnapshot = AssistantSnapshot(),
        fresh: List<Session> = emptyList(),
        memory: CapsuleNoticeMemory = CapsuleNoticeMemory(),
        current: SessionKey? = null,
    ) {
        val sessions = MutableStateFlow(initial)
        val assistant = MutableStateFlow(assistantState)
        val outbox = MutableStateFlow<List<OutboxMessage>>(emptyList())
        val sent = mutableListOf<Pair<SessionKey, String>>()
        val mainSent = mutableListOf<String>()
        val observed = mutableListOf<SessionKey>()
        val opened = mutableListOf<Pair<SessionKey?, Boolean>>()
        val openedInbox = mutableListOf<String?>()
        val read = mutableListOf<Pair<SessionKey, String>>()
        val openedCards = mutableListOf<persona.core.navigation.RequestRef>()
        val inbox = MutableStateFlow(persona.feature.inbox.InboxSnapshot())
        val hubPreferences = MutableStateFlow(HubPreferencesSnapshot())
        val messages = mutableMapOf<SessionKey, MutableStateFlow<ChatSnapshot>>()
        val connection = TestConnection()
        private val hubScope = HubScope("hub", "pairing")
        val bindings = MutableStateFlow<ChatAppBindings?>(null)
        val byMachine = mutableMapOf<String, MutableStateFlow<ChatAppBindings?>>()
        val wiring = object : ChatWiringSource {
            override val current = bindings
            override fun bindingsFor(session: SessionKey) = byMachine[session.machineId] ?: bindings
        }
        private val sender = object : MessageSender {
            override val outbox = this@Fixture.outbox
            override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) {
                assertTrue(clientMessageId.isNotBlank())
                sent += session to text
            }
        }
        private val mainSender = object : MainConversationSender {
            override val outbox = this@Fixture.outbox
            override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) {
                assertTrue(clientMessageId.isNotBlank())
                mainSent += text
            }
        }
        init { if (fresh.isNotEmpty()) bindings.value = binding() }
        private val shell = ShellSession(
            identity = hubScope.path,
            connection = connection,
            sessions = object : SessionsRepository, VerifiedSessionsRepository {
                override val machines = MutableStateFlow<List<Machine>>(emptyList())
                override val sessions = this@Fixture.sessions
                override suspend fun refresh() = Unit
                override fun verifiedSessions() = fresh.groupBy { it.machineId }
            },
            chats = object : ChatRepository {
                override fun observe(session: SessionKey) = messages.getOrPut(session) {
                    MutableStateFlow(ChatSnapshot(loading = false))
                }.also { observed += session }
                override suspend fun loadOlder(session: SessionKey) = Unit
                override suspend fun expandFold(session: SessionKey, fromSeq: Int, toSeq: Int) = emptyList<Item>()
            },
            interactions = object : InteractionCenter {
                override val pending = MutableStateFlow<List<Interaction>>(emptyList())
                override suspend fun refresh() = Unit
                override suspend fun resolve(id: String, resolution: Resolution) = error("Capsule must use the existing interaction UI")
            },
            sender = sender,
            assistant = object : AssistantConversations {
                override val assistant = this@Fixture.assistant
                override val pinned = MutableStateFlow<Set<SessionKey>>(emptySet())
                override suspend fun refresh() = Unit
                override suspend fun startConversation() = error("Use the original main sender")
            },
            inbox = object : persona.feature.inbox.InboxRepository by unused() {
                override val snapshot = this@Fixture.inbox
            },
            preferences = AgentsPreferences(object : DataStore<Preferences> {
                override val data = MutableStateFlow(emptyPreferences())
                override suspend fun updateData(transform: suspend (Preferences) -> Preferences): Preferences =
                    transform(data.value).also { data.value = it }
            }, object : HubPreferences {
                override val preferences = this@Fixture.hubPreferences
                override suspend fun refresh() = Unit
                override suspend fun togglePin(path: String, space: Boolean) = Unit
                override suspend fun toggleMute(path: String, space: Boolean) = Unit
                override suspend fun markSeen(session: SessionKey, version: String) {
                    read += session to version
                    hubPreferences.value = hubPreferences.value.copy(seen = hubPreferences.value.seen + (session.path to version))
                }
            }),
        )
        val source = CapsuleSessions(shell, wiring, scope, openInbox = { openedInbox += it }, noticeMemory = memory,
            currentConversation = { current }, openConversation = { key, assistant -> opened += key to assistant },
            openCard = { openedCards += it })
        fun binding(key: HubScope = hubScope, connection: HubConnection = this.connection) = ChatAppBindings(
            key, scope, unused(), connection, unused(), unused(), unused(), unused(), mainSender,
        )
    }

    internal class TestConnection : HubConnection {
        override val state = MutableStateFlow(ConnectionState.Online)
        override val events = MutableSharedFlow<ServerEvent>()
        override fun reconnect() = Unit
        override fun send(message: JsonObject) = true
    }

    companion object {
        /** Unused system boundaries fail on any accidental access; the capsule and its flow logic run unchanged. */
        private inline fun <reified T> unused(): T = Proxy.newProxyInstance(T::class.java.classLoader, arrayOf(T::class.java)) { proxy, method, args ->
            when (method.name) {
                "toString" -> "Unused ${T::class.java.simpleName}"
                "hashCode" -> System.identityHashCode(proxy)
                "equals" -> proxy === args?.firstOrNull()
                else -> error("Unexpected ${T::class.java.simpleName}.${method.name}")
            }
        } as T
    }
}
