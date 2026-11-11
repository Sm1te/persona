package persona.core.data

import persona.core.contracts.HubScope
import persona.core.contracts.Item
import persona.core.contracts.SessionKey
import persona.core.contracts.api.MessageRequest
import persona.core.contracts.api.MessageEvidence
import persona.core.contracts.api.MessageTransport
import persona.core.contracts.api.MessageTransportResult
import persona.core.contracts.api.MainConversationResolver
import persona.core.contracts.api.PendingMainConversation
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerDelivery
import persona.core.contracts.api.DeliveryState
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.awaitCancellation
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

@OptIn(ExperimentalCoroutinesApi::class)
class OutboxMessageSenderTest {
    private val scope = HubScope("test-hub", "generation-a")
    private val session = SessionKey("machine", "session")

    @Test fun composerUsesOriginalReadinessAndPersistsManualRefusalRetry() = runTest {
        val store = MemoryOutboxStore()
        val original = FakeTransport(online = false)
        val sender = OutboxMessageSender(scope, store, original, backgroundScope)
        var requests = 0
        sender.connectComposer { transport ->
            object : MessageTransport by transport, ChatComposerService {
                override val deliveries = MutableStateFlow(emptyMap<String, ComposerDelivery>())
                override suspend fun send(request: MessageRequest): MessageTransportResult {
                    requests++
                    return MessageTransportResult.HubError("input_draft", "none", retryable = false)
                }
                override suspend fun upload(session: SessionKey, name: String, file: java.io.File): String = error("Unused")
                override suspend fun interrupt(session: SessionKey) = Unit
                override suspend fun retryRefused(clientMessageId: String, clearDraft: Boolean): ComposerDelivery {
                    sender.recordComposerDelivery(clientMessageId, ComposerDelivery(DeliveryState.Sending))
                    return ComposerDelivery(DeliveryState.Sent).also { sender.recordComposerDelivery(clientMessageId, it) }
                }
            }
        }.also { composer ->
            sender.send(session, "保留原消息", "retry")
            runCurrent()
            assertEquals(0, requests)
            original.readyToSend.value = true
            runCurrent()
            assertEquals(1, requests)
            assertEquals(OutboxState.Refused, store.state("retry"))
            composer.retryRefused("retry", true)
            assertEquals(OutboxState.Sent, store.state("retry"))
            assertEquals("保留原消息", store.entries.value.single().text)
            assertTrue(original.requests.isEmpty())
        }
        sender.close()
    }

    @Test fun offlineFirstMessagesSurviveRestartAndBindToOneMainConversation() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = false)
        var creates = 0
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = null
            override suspend fun createMainConversation(): SessionKey { creates++; return session }
        }
        val first = OutboxMessageSender(scope, store, transport, backgroundScope)
        first.connectMainConversation(resolver)
        first.sendToMain("first", "main-a")
        first.sendToMain("shared context", "main-b")
        assertEquals(listOf("first", "shared context"), store.entries.value.map { it.text })
        assertTrue(store.entries.value.all { it.session == PendingMainConversation })
        runCurrent()
        assertEquals(0, creates)
        assertTrue(transport.requests.isEmpty())
        first.close()

        val restarted = OutboxMessageSender(scope, store, transport, backgroundScope)
        restarted.connectMainConversation(resolver)
        runCurrent()
        transport.answers += MessageTransportResult.NotSent
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(1, creates)
        assertTrue(store.entries.value.all { it.session == session })
        assertEquals(OutboxState.WaitingForNetwork, store.state("main-a"))
        transport.readyToSend.value = false
        runCurrent()
        transport.answers += MessageTransportResult.Accepted
        transport.answers += MessageTransportResult.Accepted
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(1, creates)
        assertTrue(store.entries.value.all { it.state == OutboxState.Sent })
        assertEquals(listOf("main-a", "main-a", "main-b"), transport.requests.map { it.clientMessageId })
        assertTrue(transport.requests.all { it.session == session })
        restarted.close()
    }

    /** A start the hub refuses for a missing step (409 no_machine): the message is refused, not queued, and a retry starts it. */
    @Test fun refusedMainStartWaitsForTheUserAndARetryStartsIt() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        var creates = 0
        var computer = false
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = null
            override suspend fun createMainConversation(): SessionKey {
                creates++
                if (!computer) throw Refusal(409, "no_machine")
                return session
            }
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.connectMainConversation(resolver)
        sender.sendToMain("你好", "main-a")
        runCurrent()
        assertEquals(1, creates)
        assertEquals(OutboxState.Refused, store.state("main-a"))
        assertEquals("no_machine", store.entries.value.single().error)
        assertTrue(transport.requests.isEmpty())
        // Not tried again by itself: nothing comes later as a surprise answer.
        transport.readyToSend.value = false; runCurrent(); transport.readyToSend.value = true; runCurrent()
        assertEquals(1, creates)

        computer = true
        transport.answers += MessageTransportResult.Accepted
        assertTrue(sender.retry("main-a"))
        runCurrent()
        assertEquals(2, creates)
        assertEquals(OutboxState.Sent, store.state("main-a"))
        assertEquals(session, transport.requests.single().session)
        sender.close()
    }

    /** Any other failure of the start may have created it on the hub: it is looked up, never created twice. */
    @Test fun aStartFailureWithoutASetupCodeKeepsTheClaim() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        var creates = 0
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = null
            override suspend fun createMainConversation(): SessionKey { creates++; throw Refusal(502, "internal") }
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.connectMainConversation(resolver)
        sender.sendToMain("你好", "main-a")
        runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("main-a"))
        transport.readyToSend.value = false; runCurrent(); transport.readyToSend.value = true; runCurrent()
        assertEquals(1, creates)
        sender.close()
    }

    /** The computer asleep (502 node_unreachable) or the assistant restarting (503): it is started once they are back. */
    @Test fun aStartRefusedWhileOfflineIsStartedLater() = runTest {
        for ((status, code) in listOf(502 to "node_unreachable", 503 to "assistant_offline")) {
            val store = MemoryOutboxStore()
            val transport = FakeTransport(online = false)
            transport.answers += MessageTransportResult.Accepted
            var creates = 0
            var looks = 0
            val resolver = object : MainConversationResolver {
                override suspend fun findMainConversation(): SessionKey? { looks++; return null }
                override suspend fun createMainConversation(): SessionKey {
                    creates++
                    if (creates == 1) throw Refusal(status, code)
                    return SessionKey("machine", "main")
                }
            }
            val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
            sender.connectMainConversation(resolver)
            sender.sendToMain("你好", "main-$code")
            runCurrent()
            transport.readyToSend.value = true; runCurrent()
            assertEquals(code, 1, creates)
            assertEquals(OutboxState.WaitingForNetwork, store.state("main-$code"))
            transport.readyToSend.value = false; runCurrent(); transport.readyToSend.value = true; runCurrent()
            assertEquals(code, 2, creates)
            assertEquals(code, 2, looks)
            assertEquals(1, transport.requests.size)
            sender.close()
        }
    }

    private class Refusal(override val status: Int?, override val code: String?) :
        java.io.IOException(code), persona.core.contracts.api.HubCodedFailure

    @Test fun lostCreateReceiptNeverCreatesAgainAndRetainsInputUntilMainIsFound() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        var creates = 0
        var remoteMain: SessionKey? = null
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = remoteMain
            override suspend fun createMainConversation(): SessionKey {
                creates++
                throw java.io.IOException("Receipt lost")
            }
        }
        val first = OutboxMessageSender(scope, store, transport, backgroundScope)
        first.connectMainConversation(resolver)
        first.sendToMain("keep this input", "main-a")
        runCurrent()
        assertEquals(1, creates)
        assertEquals("keep this input", store.entries.value.single().text)
        assertTrue(transport.requests.isEmpty())
        first.close()

        val restarted = OutboxMessageSender(scope, store, transport, backgroundScope)
        restarted.connectMainConversation(resolver)
        runCurrent()
        assertEquals(1, creates)
        assertTrue(transport.requests.isEmpty())
        transport.readyToSend.value = false
        runCurrent()
        remoteMain = session
        transport.answers += MessageTransportResult.Accepted
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(1, creates)
        assertEquals(session, transport.requests.single().session)
        assertEquals("keep this input", transport.requests.single().text)
        assertEquals(OutboxState.Sent, store.state("main-a"))
        restarted.close()
    }

    /** "New main chat": once the old main conversation is put away, the next message starts a new one, not the archived one. */
    @Test fun aReplacedMainConversationIsForgottenAndTheNextMessageStartsANewOne() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val replacement = SessionKey("machine", "next-main")
        var remoteMain: SessionKey? = session
        var creates = 0
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = remoteMain
            override suspend fun createMainConversation(): SessionKey { creates++; return replacement.also { remoteMain = it } }
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.connectMainConversation(resolver)
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("to the old one", "old")
        runCurrent()
        assertEquals(session, transport.requests.single().session)

        remoteMain = null // the hub put it away
        sender.mainReplaced(SessionKey("machine", "someone-else")) // not the bound one: nothing changes
        assertEquals(session, store.mainConversation())
        sender.mainReplaced(session)
        assertEquals(null, store.mainConversation())
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("to the new one", "new")
        runCurrent()
        assertEquals(1, creates)
        assertEquals(replacement, transport.requests.last().session)
        assertEquals(OutboxState.Sent, store.state("new"))
        sender.close()
    }

    /**
     * Settings › Assistant "Change machine" clears the hub's main conversation (`PUT {"machine_id","main":"-"}`); a binding kept from before
     * (the app restarted, another device moved it) must not send the next main message to the old machine's
     * conversation, which is an ordinary one now.
     */
    @Test fun aMainConversationTheHubNoLongerHasIsDroppedOnCatchUp() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val old = SessionKey("old-machine", "old-main")
        val moved = SessionKey("new-machine", "new-main")
        var remoteMain: SessionKey? = old
        var creates = 0
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = remoteMain
            override suspend fun createMainConversation(): SessionKey { creates++; return moved.also { remoteMain = it } }
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.connectMainConversation(resolver)
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("before the move", "before")
        runCurrent()
        assertEquals(old, transport.requests.single().session)

        remoteMain = null // moved to new-machine; the hub has no main conversation until the next message
        sender.onReady()
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("after the move", "after")
        runCurrent()
        assertEquals(1, creates)
        assertEquals(moved, transport.requests.last().session)
        assertEquals(moved, store.mainConversation())
        sender.close()
    }

    /** mainChanged asks the hub: the binding stays while the hub names it or cannot be asked, and goes once it names another. */
    @Test fun mainChangedKeepsTheBindingUnlessTheHubNamesAnotherOne() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val other = SessionKey("machine", "other-main")
        var remoteMain: SessionKey? = session
        var failing = false
        var hanging = false
        var creates = 0
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? {
                if (hanging) awaitCancellation()
                if (failing) throw java.io.IOException("hub unreachable")
                return remoteMain
            }
            override suspend fun createMainConversation(): SessionKey { creates++; error("Unused") }
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.mainChanged() // nothing bound, no resolver: nothing to do
        sender.connectMainConversation(resolver)
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("first", "first")
        runCurrent()
        assertEquals(session, store.mainConversation())

        sender.mainChanged()
        assertEquals("the hub still names it", session, store.mainConversation())
        failing = true
        sender.mainChanged()
        assertEquals("the hub could not be asked", session, store.mainConversation())
        failing = false
        hanging = true
        sender.mainChanged() // times out after reconciliationTimeoutMillis
        assertEquals("a lookup that timed out is not the hub saying there is none", session, store.mainConversation())
        hanging = false

        remoteMain = other // another device replaced it
        sender.mainChanged()
        assertEquals(null, store.mainConversation())
        transport.answers += MessageTransportResult.Accepted
        sender.sendToMain("second", "second")
        runCurrent()
        assertEquals(0, creates)
        assertEquals(other, transport.requests.last().session)
        assertEquals(other, store.mainConversation())
        sender.close()
    }

    @Test fun durableQueueWaitsForNetworkAndDistinguishesRefusalFromUncertainDelivery() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = false)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope) { 100 }
        sender.send(session, "hello", "message-a", null)
        // send() returns only after persistence, without waiting for the network.
        assertEquals(OutboxState.Queued, store.state("message-a"))
        runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("message-a"))
        assertTrue(transport.requests.isEmpty())

        transport.answers += MessageTransportResult.NotSent
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("message-a"))
        transport.readyToSend.value = false
        runCurrent()
        transport.answers += MessageTransportResult.HubError("input_draft", "none")
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(OutboxState.Refused, store.state("message-a"))

        transport.answers += MessageTransportResult.HubError(delivered = "unknown")
        sender.send(session, "next", "message-b", null)
        runCurrent()
        assertEquals(OutboxState.Verifying, store.state("message-b"))
        sender.observeTranscript(session, listOf(Item("message-b", 1, role = "user", kind = "text", text = "next")))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("message-b"))
        assertEquals(OutboxState.Unknown, OutboxMessageSender.classify(MessageTransportResult.HubError()))
        sender.close()
    }

    @Test fun catchUpChecksAllUncertainStatesBeforeSendingAndUsesTheSameOperationId() = runTest {
        val store = MemoryOutboxStore()
        listOf(OutboxState.Sending, OutboxState.Unknown, OutboxState.Verifying).forEachIndexed { index, state ->
            store.enqueue(OutboxEntry("uncertain-$index", session, "text-$index", state = state, createdAt = 100))
        }
        store.enqueue(OutboxEntry("next", session, "next", createdAt = 101))
        val transport = FakeTransport(online = false, supportsClientOps = true)
        transport.evidence["uncertain-0"] = MessageEvidence.Receipt(MessageTransportResult.Accepted)
        transport.evidence["uncertain-1"] = MessageEvidence.Shown
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        runCurrent()
        sender.onReady()
        assertEquals(listOf("uncertain-0", "uncertain-1", "uncertain-2"), transport.lookups)
        assertTrue(transport.requests.isEmpty())
        assertEquals(OutboxState.Sent, store.state("uncertain-0"))
        assertEquals(OutboxState.Shown, store.state("uncertain-1"))
        transport.answers += MessageTransportResult.Accepted
        transport.answers += MessageTransportResult.Accepted
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(listOf("uncertain-2", "next"), transport.requests.map { it.clientMessageId })
        assertEquals("uncertain-2", transport.requests.first().headers["X-Persona-Op-Id"])
        sender.close()
    }

    @Test fun legacyUnknownDoesNotBlockLaterMessagesAndTimesOutToManualDisposition() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = false)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope,
            reviewTimeoutMillis = 100, reconciliationTimeoutMillis = 10)
        sender.send(session, "cancel", "cancel")
        assertTrue(sender.cancel("cancel"))
        sender.send(session, "unknown", "unknown")
        sender.send(session, "later", "later")
        transport.answers += MessageTransportResult.NoReceipt
        transport.answers += MessageTransportResult.Accepted
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(listOf("unknown", "later"), transport.requests.map { it.clientMessageId })
        assertTrue(!sender.cancel("unknown"))
        assertTrue(!sender.retry("unknown"))
        // A hung reconciliation is bounded and cannot hide the manual review deadline.
        transport.hangLookup = true
        transport.readyToSend.value = false
        runCurrent()
        transport.readyToSend.value = true
        runCurrent()
        advanceTimeBy(111)
        runCurrent()
        assertEquals(OutboxState.NeedsConfirmation, store.state("unknown"))
        assertEquals(2, transport.requests.size)
        assertTrue(sender.confirm("unknown", delivered = false))
        assertEquals(OutboxState.Refused, store.state("unknown"))
        transport.hangLookup = false
        transport.answers += MessageTransportResult.NoReceipt
        assertTrue(sender.retry("unknown"))
        runCurrent()
        assertTrue(sender.confirm("unknown", delivered = true))
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("unknown"))
        assertTrue(sender.outbox.value.none { it.clientMessageId == "cancel" })
        sender.close()
    }

    @Test fun messagesOfUnknownDeliveryCanBeDiscardedAndAreNeverRetried() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope,
            reviewTimeoutMillis = 100, reconciliationTimeoutMillis = 10)
        transport.answers += MessageTransportResult.NoReceipt
        transport.answers += MessageTransportResult.Accepted
        sender.send(session, "unknown", "unknown")
        sender.send(session, "sent", "sent")
        runCurrent()
        assertTrue(store.state("unknown") in setOf(OutboxState.Unknown, OutboxState.Verifying))
        assertTrue(!sender.discard("sent"))
        assertTrue(sender.discard("unknown"))
        assertEquals(OutboxState.Cancelled, store.state("unknown"))
        advanceTimeBy(111)
        runCurrent()
        // Discarded: no review deadline brings it back, and it is never sent again.
        assertEquals(OutboxState.Cancelled, store.state("unknown"))
        assertTrue(sender.outbox.value.none { it.clientMessageId == "unknown" })
        assertEquals(listOf("unknown", "sent"), transport.requests.map { it.clientMessageId })
        sender.close()
    }

    @Test fun refusedMessagesCanBeDiscardedButSentOnesCannot() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        transport.answers += MessageTransportResult.HubError(code = "input_draft", delivered = "none")
        transport.answers += MessageTransportResult.Accepted
        sender.send(session, "refused", "refused")
        runCurrent()
        assertEquals(OutboxState.Refused, store.state("refused"))
        sender.send(session, "sent", "sent")
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("sent"))
        // Nothing of a refused message reached the agent, so deleting it is safe; a sent one stays to be confirmed.
        assertTrue(sender.cancel("refused"))
        assertTrue(!sender.cancel("sent"))
        assertEquals(OutboxState.Cancelled, store.state("refused"))
        runCurrent()
        assertTrue(sender.outbox.value.none { it.clientMessageId == "refused" })
        assertEquals(listOf("refused", "sent"), transport.requests.map { it.clientMessageId })
        sender.close()
    }

    @Test fun undoPutsADeletedMessageBackAsItWas() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        transport.answers += MessageTransportResult.HubError(code = "input_draft", delivered = "none")
        sender.send(session, "refused", "refused")
        runCurrent()
        val refused = sender.outbox.value.single { it.clientMessageId == "refused" }
        assertEquals("input_draft", refused.error)
        assertTrue(sender.cancel("refused"))
        runCurrent()
        assertTrue(sender.outbox.value.none { it.clientMessageId == "refused" })
        // Back as it was, its refusal too, and nothing sent for it.
        sender.restore(refused)
        runCurrent()
        assertEquals(refused, sender.outbox.value.single { it.clientMessageId == "refused" })
        // A second Undo, or one for a message no longer deleted, changes nothing.
        sender.restore(refused.copy(state = DeliveryState.Queued))
        runCurrent()
        assertEquals(OutboxState.Refused, store.state("refused"))
        assertEquals(listOf("refused"), transport.requests.map { it.clientMessageId })

        // One still waiting comes back waiting and goes once the link is up.
        transport.readyToSend.value = false
        sender.send(session, "waiting", "waiting")
        runCurrent()
        val waiting = sender.outbox.value.single { it.clientMessageId == "waiting" }
        assertEquals(DeliveryState.WaitingForNetwork, waiting.state)
        assertTrue(sender.cancel("waiting"))
        sender.restore(waiting)
        runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("waiting"))
        transport.answers += MessageTransportResult.Accepted
        transport.readyToSend.value = true
        runCurrent()
        assertEquals(listOf("refused", "waiting"), transport.requests.map { it.clientMessageId })
        sender.close()
    }

    @Test fun interruptedAndUnknownRequestsOnlyRetryWithDurableHubDeduplication() = runTest {
        val store = MemoryOutboxStore()
        store.enqueue(OutboxEntry("message-a", session, "hello", state = OutboxState.Sending, createdAt = 100))
        val legacy = FakeTransport(online = true)
        val first = OutboxMessageSender(scope, store, legacy, backgroundScope)
        runCurrent()
        assertEquals(OutboxState.Unknown, store.state("message-a"))
        assertTrue(legacy.requests.isEmpty())
        first.close()

        val modern = FakeTransport(online = true, supportsClientOps = true)
        modern.answers += MessageTransportResult.NoReceipt
        modern.answers += MessageTransportResult.Accepted
        val second = OutboxMessageSender(scope, store, modern, backgroundScope)
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("message-a"))
        assertEquals(2, modern.requests.size)
        modern.requests.forEach {
            assertEquals("message-a", it.headers["X-Persona-Op-Id"])
            assertEquals(scope, it.scope)
        }
        assertTrue(store.transitions.contains(OutboxState.Unknown to OutboxState.Sending))
        second.close()
    }

    @Test fun transcriptCopyWithTheAgentsOwnIdShowsTheMessage() = runTest {
        // Claude Code keeps its own uuid for the user's message: the client message id never comes back (#895).
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true, supportsClientOps = true)
        val sentAt = java.time.Instant.parse("2026-11-03T09:00:00Z")
        var clock = sentAt.toEpochMilli()
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope, now = { clock })
        fun user(id: String, text: String, at: java.time.Instant) =
            Item(id, 1, at.toString(), role = "user", kind = "text", text = text)

        transport.answers += MessageTransportResult.Accepted
        sender.send(session, "可以删的都删掉吧", "first")
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("first"))
        // An older message with the same words is not this one.
        sender.observeTranscript(session, listOf(user("old-uuid", "可以删的都删掉吧", sentAt.minusSeconds(3600))))
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("first"))
        // The computer's clock is a little behind and Claude wrapped the text differently.
        sender.observeTranscript(session, listOf(user("claude-uuid", "可以删的都删掉吧\n", sentAt.minusSeconds(30))))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("first"))

        // Two identical messages: one transcript item shows one of them, seen again it does not show the other.
        clock += 60_000
        transport.answers += MessageTransportResult.Accepted
        transport.answers += MessageTransportResult.Accepted
        sender.send(session, "继续", "again-1")
        sender.send(session, "继续", "again-2")
        runCurrent()
        val again = user("claude-again-1", "继续", sentAt.plusSeconds(61))
        sender.observeTranscript(session, listOf(again))
        sender.observeTranscript(session, listOf(again))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("again-1"))
        assertEquals(OutboxState.Sent, store.state("again-2"))
        sender.observeTranscript(session, listOf(again, user("claude-again-2", "继续", sentAt.plusSeconds(62))))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("again-2"))

        // A message still waiting for the network is not taken for the same words typed on the computer.
        transport.readyToSend.value = false
        runCurrent()
        sender.send(session, "看看状态", "offline")
        runCurrent()
        sender.observeTranscript(session, listOf(user("typed-there", "看看状态", sentAt.plusSeconds(70))))
        runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("offline"))
        sender.close()
    }

    @Test fun anItemTaggedWithTheClientMessageIdShowsThatMessageOnly() = runTest {
        // Hubs since #968 tag every item a sent message became: it decides, whatever the words or the kind.
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true, supportsClientOps = true)
        val sentAt = java.time.Instant.parse("2026-11-03T09:00:00Z")
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope, now = { sentAt.toEpochMilli() })
        fun user(id: String, text: String, tag: String?) =
            Item(id, 1, sentAt.plusSeconds(5).toString(), role = "user", kind = "text", text = text, clientMsgId = tag)

        repeat(4) { transport.answers += MessageTransportResult.Accepted }
        sender.send(session, "/compact", "compact")
        sender.send(session, "继续", "again-1")
        sender.send(session, "继续", "again-2")
        sender.send(session, "看看状态", "status")
        runCurrent()
        // A /compact became a notice: no user row ever comes, the tagged notice shows it.
        sender.observeTranscript(session, listOf(
            Item("notice-1", 2, role = "system", kind = "notice", text = "压缩上下文", clientMsgId = "compact")))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("compact"))

        // The copy tagged for the second "continue" shows that one, not the older one with the same words.
        sender.observeTranscript(session, listOf(user("claude-uuid-2", "继续", "again-2")))
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("again-1"))
        assertEquals(OutboxState.Shown, store.state("again-2"))
        // Other words (the runtime rewrote it), still the tag decides; an image of an already shown message settles nothing.
        sender.observeTranscript(session, listOf(
            Item("image-1", 3, role = "user", kind = "image", clientMsgId = "again-2"),
            user("claude-uuid-1", "[Image #1] 继续下一步", "again-1")))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("again-1"))
        assertEquals(OutboxState.Sent, store.state("status"))

        // Untagged (an older hub): words still find it.
        sender.observeTranscript(session, listOf(user("claude-uuid-3", "看看状态", null)))
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("status"))
        sender.close()
    }

    @Test fun aDeliveredMessageWhoseCopyNeverShowsGoesOnceItsSessionRests() = runTest {
        // A /compact becomes a notice, Codex writes its queue at the end of the turn: no copy ever comes (#950).
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true, supportsClientOps = true)
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope, sentGraceMillis = 60_000)
        var working = true
        sender.connectActivity { it == session && working }
        transport.answers += MessageTransportResult.Accepted
        sender.send(session, "/compact", "compact")
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("compact"))
        advanceTimeBy(59_000)
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("compact"))
        advanceTimeBy(30_000)
        runCurrent()
        assertEquals(OutboxState.Sent, store.state("compact")) // the session still works on it
        working = false
        advanceTimeBy(10_001)
        runCurrent()
        assertEquals(OutboxState.Shown, store.state("compact"))
        sender.close()
    }

    @Test fun messageTheHubDidNotDeliverGoesAgainUnderANewOperationId() = runTest {
        val store = MemoryOutboxStore()
        val hub = ReplayingHub()
        val sender = OutboxMessageSender(scope, store, hub, backgroundScope)
        // The computer is offline for the first try (503, delivered none), back for the next.
        hub.answers += MessageTransportResult.HubError("assistant_offline", "none", retryable = true)
        hub.answers += MessageTransportResult.Accepted
        sender.send(session, "电脑离线时写的", "offline")
        runCurrent()
        hub.readyToSend.value = false
        runCurrent()
        hub.readyToSend.value = true
        runCurrent()
        val ops = hub.requests.map { it.headers.getValue("X-Persona-Op-Id") }
        assertEquals(2, ops.size)
        assertEquals("offline", ops[0])
        assertTrue(ops[1] != ops[0])
        assertEquals(OutboxState.Sent, store.state("offline"))
        sender.observeTranscript(session, listOf(Item("echo", 1, role = "user", kind = "text", text = "电脑离线时写的",
            clientMsgId = ops[1])))
        assertEquals(OutboxState.Shown, store.state("offline"))
        sender.close()
    }

    /** The computer asleep (502 node_unreachable, delivered none): refused at once with its code, and Retry sends it anew. */
    @Test fun aMessageToASleepingComputerIsRefusedAndRetriedByHand() = runTest {
        val store = MemoryOutboxStore()
        val hub = ReplayingHub()
        val sender = OutboxMessageSender(scope, store, hub, backgroundScope)
        hub.answers += MessageTransportResult.HubError("node_unreachable", "none", retryable = true)
        sender.send(session, "电脑睡着时写的", "asleep")
        runCurrent()
        advanceTimeBy(60_000); runCurrent()
        assertEquals(OutboxState.Refused, store.state("asleep"))
        assertEquals("node_unreachable", store.entries.value.single().error)
        assertEquals(1, hub.requests.size)
        hub.answers += MessageTransportResult.Accepted
        assertTrue(sender.retry("asleep"))
        runCurrent()
        val ops = hub.requests.map { it.headers.getValue("X-Persona-Op-Id") }
        assertEquals(2, ops.size)
        assertTrue(ops[1] != ops[0])
        assertEquals(OutboxState.Sent, store.state("asleep"))
        sender.close()
    }

    /** Waiting while the link is up (503 assistant_offline): tried again every 10 s, refused after 60 s (iOS OutboxWait). */
    @Test fun aMessageThatKeepsWaitingWhileTheLinkIsUpGoesAgainThenIsRefused() = runTest {
        val restarting = MessageTransportResult.HubError("assistant_offline", "none", retryable = true)
        data class Case(val label: String, val answers: List<MessageTransportResult>, val after: Long,
            val state: OutboxState, val sends: Int, val error: String?)
        for (c in listOf(
            Case("back within 10 s", listOf(restarting, MessageTransportResult.Accepted), 10_000, OutboxState.Sent, 2, null),
            Case("still restarting after 60 s", List(6) { restarting }, 60_000, OutboxState.Refused, 6, "assistant_offline"),
        )) {
            val store = MemoryOutboxStore()
            val transport = FakeTransport(online = true)
            val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
            runCurrent() // the start's own looks at the queue
            transport.answers += c.answers
            sender.send(session, "助手重启时写的", "restart")
            runCurrent()
            assertEquals(c.label, OutboxState.WaitingForNetwork, store.state("restart"))
            advanceTimeBy(c.after); runCurrent()
            assertEquals(c.label, c.state, store.state("restart"))
            assertEquals(c.label, c.sends, transport.requests.size)
            assertEquals(c.label, c.error, store.entries.value.single().error)
            sender.close()
        }
    }

    /** A main conversation that cannot start gets a minute on a link that is up; the link dropping starts it again. */
    @Test fun aMainConversationThatCannotStartIsRefusedWithTheHubsReason() = runTest {
        val store = MemoryOutboxStore()
        val transport = FakeTransport(online = true)
        val resolver = object : MainConversationResolver {
            override suspend fun findMainConversation(): SessionKey? = null
            override suspend fun createMainConversation(): SessionKey = throw Refusal(503, "assistant_offline")
        }
        val sender = OutboxMessageSender(scope, store, transport, backgroundScope)
        sender.connectMainConversation(resolver)
        sender.sendToMain("你好", "main-a")
        runCurrent()
        advanceTimeBy(50_000); runCurrent()
        transport.readyToSend.value = false; runCurrent(); transport.readyToSend.value = true; runCurrent()
        advanceTimeBy(50_000); runCurrent()
        assertEquals(OutboxState.WaitingForNetwork, store.state("main-a"))
        advanceTimeBy(10_000); runCurrent()
        assertEquals(OutboxState.Refused, store.state("main-a"))
        assertEquals("assistant_offline", store.entries.value.single().error)
        assertTrue(transport.requests.isEmpty())
        sender.close()
    }

    @Test fun refusedMessageRetriedByHandGoesUnderANewOperationId() = runTest {
        val store = MemoryOutboxStore()
        val hub = ReplayingHub()
        val sender = OutboxMessageSender(scope, store, hub, backgroundScope)
        hub.answers += MessageTransportResult.HubError("input_draft", "none")
        sender.send(session, "电脑上有草稿", "draft")
        runCurrent()
        assertEquals(OutboxState.Refused, store.state("draft"))
        hub.answers += MessageTransportResult.Accepted
        assertTrue(sender.retry("draft"))
        runCurrent()
        val ops = hub.requests.map { it.headers.getValue("X-Persona-Op-Id") }
        assertEquals(2, ops.size)
        assertTrue(ops[1] != ops[0])
        assertEquals(OutboxState.Sent, store.state("draft"))
        // Still the same message under its first id for the pages.
        assertEquals("draft", sender.outbox.value.single().clientMessageId)
        sender.close()
    }

    /** Like the hub's client ops: a repeated operation id gets the first answer again (a 503 replays as unknown). */
    private class ReplayingHub : MessageTransport {
        override val readyToSend = MutableStateFlow(true)
        override val supportsClientOps = true
        val answers = ArrayDeque<MessageTransportResult>()
        val requests = mutableListOf<MessageRequest>()
        private val first = mutableMapOf<String, MessageTransportResult>()
        override suspend fun send(request: MessageRequest): MessageTransportResult {
            requests += request
            val op = request.headers.getValue("X-Persona-Op-Id")
            val stored = first[op] ?: return answers.removeFirst().also { first[op] = it }
            return if (stored is MessageTransportResult.HubError && stored.retryable) MessageTransportResult.NoReceipt else stored
        }
    }

    private class FakeTransport(online: Boolean, override val supportsClientOps: Boolean = false) : MessageTransport {
        override val readyToSend = MutableStateFlow(online)
        val answers = ArrayDeque<MessageTransportResult>()
        val requests = mutableListOf<MessageRequest>()
        val lookups = mutableListOf<String>()
        val evidence = mutableMapOf<String, MessageEvidence>()
        var hangLookup = false
        override suspend fun reconcile(request: MessageRequest, createdAt: Long, allowTextMatch: Boolean): MessageEvidence? {
            lookups += request.clientMessageId
            if (hangLookup) awaitCancellation()
            return evidence[request.clientMessageId]
        }
        override suspend fun send(request: MessageRequest): MessageTransportResult {
            requests += request
            return answers.removeFirst()
        }
    }
}
