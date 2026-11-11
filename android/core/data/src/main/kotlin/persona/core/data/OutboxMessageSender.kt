package persona.core.data

import persona.core.contracts.HubScope
import persona.core.contracts.Item
import persona.core.contracts.MessageEcho
import persona.core.contracts.SessionKey
import persona.core.contracts.api.MessageEvidence
import persona.core.contracts.api.MessageRequest
import persona.core.contracts.api.MessageSender
import persona.core.contracts.api.MessageTransport
import persona.core.contracts.api.MessageTransportResult
import persona.core.contracts.api.OutboxMessage
import persona.core.contracts.api.DraftMessageSender
import persona.core.contracts.api.DraftSnapshot
import persona.core.contracts.api.MainConversationResolver
import persona.core.contracts.api.MainConversationSender
import persona.core.contracts.api.PendingMainConversation
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerDelivery
import persona.core.contracts.api.DeliveryState
import persona.core.contracts.api.hubCodedFailure
import persona.core.domain.SetupFailureCodes
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull

class OutboxMessageSender(
    private val hubScope: HubScope,
    private val store: OutboxStore,
    private var transport: MessageTransport,
    parentScope: CoroutineScope,
    private val reviewTimeoutMillis: Long = 30_000,
    private val reconciliationTimeoutMillis: Long = 3_000,
    /** How long a delivered message waits for its copy in the transcript before its bubble goes anyway (iOS, web). */
    private val sentGraceMillis: Long = 60_000,
    // Last: callers pass the clock as a trailing lambda.
    private val now: () -> Long = System::currentTimeMillis,
) : MessageSender, DraftMessageSender, MainConversationSender {
    private val job = SupervisorJob(parentScope.coroutineContext[Job])
    private val scope = CoroutineScope(parentScope.coroutineContext + job)
    private val wake = Channel<Unit>(Channel.CONFLATED)
    private val lifecycle = Mutex()
    private val processing = Mutex()
    private val initialized = CompletableDeferred<Unit>()
    private val reconcilers = mutableListOf<OutboxReconciler>()
    /**
     * Messages that came back waiting while the link was up, each with the code it fails with ([stall]). Memory only,
     * under its own lock; the link going down starts every run again. Declared before `init`: its readiness collector
     * clears them.
     */
    private val stalls = mutableMapOf<String, Stall>()
    /** The one retry of the stalled messages, due in [StallRetryMillis]. */
    private var stallRetry: Job? = null
    @Volatile private var mainResolver: MainConversationResolver? = null
    /** Transcript items that already showed a message (observeTranscript): the same item never shows a second. */
    private val claimed = mutableSetOf<Pair<SessionKey, String>>()
    /** Whether a session is at work (connectActivity): its turn may write a delivered message only when it ends. */
    @Volatile private var busy: (SessionKey) -> Boolean = { false }

    override val outbox = store.entries.map { entries ->
        entries.filter { it.state !in hidden }.map {
            OutboxMessage(it.clientMessageId, it.session, it.text, it.state.delivery, it.error, it.mainTarget)
        }
    }.stateIn(scope, SharingStarted.Eagerly, emptyList())

    init {
        require(reviewTimeoutMillis > 0 && reconciliationTimeoutMillis > 0)
        scope.launch {
            store.recoverInterrupted()
            initialized.complete(Unit)
            wake.trySend(Unit)
            for (ignored in wake) processing.withLock { drain() }
        }
        scope.launch {
            transport.readyToSend.collect { if (it) wake.trySend(Unit) else clearStalls() }
        }
        scope.launch {
            val deadlines = mutableMapOf<String, Job>()
            store.entries.collect { entries ->
                val unresolved = entries.filter { it.state in reviewable }.map { it.clientMessageId }.toSet()
                deadlines.keys.toList().filter { it !in unresolved }.forEach { deadlines.remove(it)?.cancel() }
                for (id in unresolved) if (id !in deadlines) {
                    deadlines[id] = scope.launch {
                        delay(reviewTimeoutMillis)
                        store.transition(id, reviewable, OutboxState.NeedsConfirmation)
                        wake.trySend(Unit)
                    }
                }
            }
        }
        scope.launch {
            // A delivered message whose copy never shows (a /compact becomes a notice, Codex writes its queue when the
            // turn ends, other words) would wait for ever: after the grace, and once its session is not at work (for
            // up to 15 more minutes), its bubble goes, as on iOS (AppModel.settleSend) and the web.
            val waits = mutableMapOf<String, Job>()
            store.entries.collect { entries ->
                val sent = entries.filter { it.state == OutboxState.Sent }.associateBy { it.clientMessageId }
                waits.keys.toList().filter { it !in sent }.forEach { waits.remove(it)?.cancel() }
                for ((id, entry) in sent) if (id !in waits) {
                    waits[id] = scope.launch {
                        delay(sentGraceMillis)
                        var checks = 0
                        while (checks++ < 90 && busy(entry.session)) delay(10_000)
                        store.transition(id, setOf(OutboxState.Sent), OutboxState.Shown)
                    }
                }
            }
        }
    }

    /**
     * Sessions holding a delivered message not yet seen in their transcript. Only Sent: it settles within the grace and
     * busy wait, while undecided states can last until the user acts and would keep their chats syncing.
     */
    val awaitingTranscript: Flow<Set<SessionKey>> = store.entries
        .map { entries -> entries.filter { it.state == OutboxState.Sent }.mapTo(mutableSetOf()) { it.session } }
        .distinctUntilChanged()

    /** Tells the outbox which sessions are at work (the pairing's session list). */
    fun connectActivity(busy: (SessionKey) -> Boolean) { this.busy = busy }

    override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        require(text.isNotBlank() && clientMessageId.isNotBlank())
        require(session.machineId.isNotBlank() && session.sessionId.isNotBlank())
        store.enqueue(OutboxEntry(clientMessageId, session, text, clearDraft, createdAt = now()))
        wake.trySend(Unit)
        Unit
    }

    /** Not sent yet, or refused (by the computer, or confirmed as never delivered): nothing reached the agent. */
    override suspend fun cancel(clientMessageId: String): Boolean =
        change(clientMessageId, dispatchable + OutboxState.Refused, OutboxState.Cancelled)

    override fun restore(message: OutboxMessage) {
        val to = when (message.state) {
            DeliveryState.Queued -> OutboxState.Queued
            DeliveryState.WaitingForNetwork -> OutboxState.WaitingForNetwork
            DeliveryState.Refused, DeliveryState.Rejected -> OutboxState.Refused
            else -> return
        }
        // The sender's own scope: the toast's Undo can come after the chat page that showed it has gone.
        scope.launch { if (job.isActive) change(message.clientMessageId, setOf(OutboxState.Cancelled), to, message.error) }
    }

    override suspend fun discard(clientMessageId: String): Boolean =
        change(clientMessageId, undecided, OutboxState.Cancelled)

    override suspend fun retry(clientMessageId: String): Boolean = change(
        clientMessageId, setOf(OutboxState.Refused, OutboxState.NeedsConfirmation), OutboxState.Queued)

    override suspend fun confirm(clientMessageId: String, delivered: Boolean): Boolean = change(
        clientMessageId, undecided,
        if (delivered) OutboxState.Sent else OutboxState.Refused)

    private suspend fun change(id: String, from: Set<OutboxState>, to: OutboxState, error: String? = null): Boolean = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        store.transition(id, from, to, error).also { if (it) wake.trySend(Unit) }
    }

    /** Looks at the queue again: messages were written into the store directly (GroupMerge.kt). */
    internal fun nudge() { wake.trySend(Unit) }

    /** Called before the transport releases sending after connection catch-up. */
    suspend fun onReady() {
        initialized.await()
        processing.withLock {
            reconcilers.forEach { it.onReady() }
            reconcile()
            // The main conversation may have moved while this pairing was away (another machine, another device).
            checkMain()
        }
    }

    /** Register a scope-owned queue without changing public message dispatch. */
    suspend fun connectReconciler(reconciler: OutboxReconciler) = processing.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        reconcilers += reconciler
    }

    /** Install the resolver owned by this pairing before exposing the main sender to features. */
    suspend fun connectMainConversation(resolver: MainConversationResolver) = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        check(mainResolver == null || mainResolver === resolver) { "Main resolver already connected" }
        mainResolver = resolver
        wake.trySend(Unit)
        Unit
    }

    /** Install one composer decorator without replacing the original transport's readiness gate. */
    suspend fun connectComposer(factory: (MessageTransport) -> ChatComposerService): ChatComposerService = processing.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        val composer = factory(transport)
        require(composer is MessageTransport)
        transport = composer
        composer
    }

    /**
     * Manual refusal retries must update the durable outbox as well as the composer overlay. [opId] is the operation
     * id the retry goes under (a new one: the refused one would be answered from its first try).
     */
    suspend fun recordComposerDelivery(id: String, delivery: ComposerDelivery, opId: String? = null) = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        val state = OutboxState.entries.first { it.delivery == delivery.state }
        val from = if (delivery.state == DeliveryState.Sending) setOf(OutboxState.Refused) else setOf(OutboxState.Sending)
        // Refused again: nothing was delivered under that id either, so a later retry takes another.
        val next = when (state) {
            OutboxState.Refused -> newOperationId()
            OutboxState.Sending -> opId
            else -> null
        }
        check(if (next != null) store.transition(id, from, state, delivery.refusal?.code, next)
            else store.transition(id, from, state, delivery.refusal?.code)) { "Message state changed" }
    }

    /** Attachments need the same durably bound target before uploading machine-local files. */
    suspend fun prepareMainConversation(): SessionKey? = processing.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        if (transport.readyToSend.value) (resolveMain() as? MainTarget.Found)?.session else null
    }

    override suspend fun sendDraft(session: SessionKey, draft: DraftSnapshot, clientMessageId: String) = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        require(draft.scope == hubScope && draft.text.isNotBlank() && clientMessageId.isNotBlank())
        require(session.machineId.isNotBlank() && session.sessionId.isNotBlank())
        store.enqueueDraft(OutboxEntry(clientMessageId, session, draft.text, createdAt = now()), draft)
        wake.trySend(Unit)
        Unit
    }

    override suspend fun sendToMain(text: String, clientMessageId: String, draft: DraftSnapshot?) = lifecycle.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        require(text.isNotBlank() && clientMessageId.isNotBlank())
        require(draft == null || draft.scope == hubScope)
        store.enqueueDraft(OutboxEntry(clientMessageId, PendingMainConversation, text,
            createdAt = now(), mainTarget = true), draft)
        wake.trySend(Unit)
        Unit
    }

    // Under the processing lock: a drain that already read the old binding finishes before it goes.
    override suspend fun mainReplaced(previous: SessionKey) = processing.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        store.forgetMainConversation(previous)
        wake.trySend(Unit)
        Unit
    }

    override suspend fun mainChanged() = processing.withLock {
        check(job.isActive) { "Pairing scope is closed" }
        checkMain()
        Unit
    }

    /**
     * The hub decides which conversation is the main one: a binding it no longer names (moved with the assistant to
     * another machine, replaced from another device) goes, and the next message resolves the main conversation again.
     * Kept when the hub cannot be asked, and while a creation is claimed (no binding yet).
     */
    private suspend fun checkMain() {
        val bound = store.mainConversation() ?: return
        val resolver = mainResolver ?: return
        // Boxed: a lookup that timed out (null) is not the hub saying there is none.
        val current = try {
            withTimeoutOrNull(reconciliationTimeoutMillis) { Result.success(resolver.findMainConversation()) } ?: return
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (_: Exception) {
            return
        }
        if (current.getOrNull() == bound) return
        store.forgetMainConversation(bound)
        wake.trySend(Unit)
    }

    /**
     * The session's transcript holds [items]: authoritative (from the hub), never an optimistic local echo. A message
     * sent from here that is among them is shown, and its bubble goes. An item the hub tagged with a client message id
     * shows that message only, whatever its kind (a /compact becomes a notice). Untagged, Claude Code keeps its own id
     * for the message, so it is found by its words ([MessageEcho]) unless the runtime kept the client message id. One
     * transcript item shows one message (the oldest it can be), so a second identical message stays until its own copy
     * arrives.
     */
    suspend fun observeTranscript(session: SessionKey, items: List<Item>) {
        // Not the `outbox` flow: it lags the store, and an echo that beats it would be passed over.
        if (items.none(MessageEcho::candidate)) return
        lifecycle.withLock {
            check(job.isActive) { "Pairing scope is closed" }
            val pending = store.entries.first().filter { it.session == session && it.state in reconcilable }
                .sortedBy { it.createdAt }.toMutableList()
            for (item in items) {
                if (pending.isEmpty()) break
                if (!MessageEcho.candidate(item) || (session to item.id) in claimed) continue
                val entry = pending.firstOrNull { MessageEcho.carries(item, it.clientMessageId) ||
                    it.opId?.let { op -> MessageEcho.carries(item, op) } == true }
                    ?: pending.firstOrNull { MessageEcho.arrived(it.text, it.createdAt, item) } ?: continue
                pending.remove(entry)
                if (store.transition(entry.clientMessageId, reconcilable, OutboxState.Shown)) {
                    claimed += session to item.id
                    wake.trySend(Unit)
                }
            }
        }
    }

    suspend fun close() = lifecycle.withLock { job.cancelAndJoin() }

    private suspend fun reconcile() {
        // Bound the whole catch-up so unresolved entries cannot hold the sending gate forever.
        withTimeoutOrNull(reconciliationTimeoutMillis) {
            val entries = store.entries.first()
            for (entry in entries.filter { it.state in reconcilable }) {
                if (entry.state in uncertain) store.transition(entry.clientMessageId, uncertain, OutboxState.Verifying)
                val evidence = try {
                    transport.reconcile(request(entry), entry.createdAt,
                        entries.count { it.session == entry.session && it.text == entry.text } == 1)
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { null }
                when (evidence) {
                    MessageEvidence.Shown -> store.transition(entry.clientMessageId, reconcilable, OutboxState.Shown)
                    is MessageEvidence.Receipt -> {
                        // A late success can settle manual review; an unknown receipt must keep that choice visible.
                        val from = if (evidence.result == MessageTransportResult.Accepted)
                            uncertain + OutboxState.NeedsConfirmation else uncertain
                        settle(entry.clientMessageId, from, evidence.result)
                    }
                    null -> store.transition(entry.clientMessageId, setOf(OutboxState.Verifying), OutboxState.Unknown)
                }
            }
        }
        // Timeout or an interrupted lookup must remain visible and eligible for manual review.
        store.entries.first().filter { it.state == OutboxState.Verifying }.forEach {
            store.transition(it.clientMessageId, setOf(OutboxState.Verifying), OutboxState.Unknown)
        }
    }

    private suspend fun drain() {
        if (!transport.readyToSend.value) {
            store.entries.first().filter { it.state == OutboxState.Queued }.forEach {
                store.transition(it.clientMessageId, setOf(OutboxState.Queued), OutboxState.WaitingForNetwork)
            }
            return
        }
        reconcile()
        for (queued in store.entries.first()) {
            var entry = queued
            if (!transport.readyToSend.value) return
            val allowed = if (transport.supportsClientOps) dispatchable + OutboxState.Unknown else dispatchable
            if (entry.mainTarget && entry.session == PendingMainConversation) {
                if (entry.state !in dispatchable) continue
                val target = when (val main = resolveMain()) {
                    is MainTarget.Found -> main.session
                    // The hub said why it cannot start (no computer, no Claude Code…): nothing was started, so the
                    // message is not queued behind it (docs/assistant/onboarding.md 4.4); the user fixes the step and retries.
                    is MainTarget.Refused -> {
                        store.transition(entry.clientMessageId, dispatchable, OutboxState.Refused, main.code)
                        unstall(entry.clientMessageId)
                        continue
                    }
                    is MainTarget.NotYet -> {
                        if (store.transition(entry.clientMessageId, dispatchable, OutboxState.WaitingForNetwork,
                                "main_not_ready")) stall(entry.clientMessageId, main.code)
                        return
                    }
                }
                entry = entry.copy(session = target)
            }
            if (entry.state !in allowed) continue
            if (!store.transition(entry.clientMessageId, allowed, OutboxState.Sending)) continue
            var result = attempt(entry)
            // One immediate retry is safe only when the hub durably deduplicates this exact id.
            if (result == MessageTransportResult.NoReceipt && transport.supportsClientOps && transport.readyToSend.value) {
                store.transition(entry.clientMessageId, setOf(OutboxState.Sending), OutboxState.Unknown)
                if (!store.transition(entry.clientMessageId, setOf(OutboxState.Unknown), OutboxState.Sending)) continue
                result = attempt(entry)
            }
            if (settle(entry.clientMessageId, setOf(OutboxState.Sending), result) == OutboxState.WaitingForNetwork) return
        }
    }

    /**
     * Records [result] for the message's current operation id. When the hub said nothing was delivered, that id is
     * spent (the hub answers it again from this try: 202 unknown, the stored 409), so the next attempt takes a new one,
     * as on iOS. Without an answer the id stays: the hub sends a repeated id once.
     */
    private suspend fun settle(id: String, from: Set<OutboxState>, result: MessageTransportResult): OutboxState {
        val state = classify(result)
        val error = (result as? MessageTransportResult.HubError)?.let { it.code ?: it.error }
        val moved = if ((result as? MessageTransportResult.HubError)?.delivered == "none") {
            store.transition(id, from, state, error, newOperationId())
        } else store.transition(id, from, state, error)
        if (moved && state == OutboxState.WaitingForNetwork) stall(id, error) else unstall(id)
        return state
    }

    /**
     * [id] came back waiting. Waiting for the link is what the outbox is for; while the link is up it means the hub
     * cannot pass it on yet (the assistant restarting, a 5xx without a code, the main conversation not started), and
     * nothing else would wake the queue: as on iOS (`OutboxWait`), it goes again every [StallRetryMillis] and after
     * [StallLimitMillis] it is refused with [code], so the user sees why and can retry.
     */
    private fun stall(id: String, code: String?) {
        if (!transport.readyToSend.value) return
        synchronized(stalls) {
            val stall = stalls[id]
            if (stall != null) stall.code = code
            else stalls[id] = Stall(scope.launch { delay(StallLimitMillis); giveUp(id) }, code)
            if (stallRetry?.isActive != true) stallRetry = scope.launch { delay(StallRetryMillis); wake.trySend(Unit) }
        }
    }

    private fun unstall(id: String) = synchronized(stalls) { stalls.remove(id)?.giveUp?.cancel() }

    private fun clearStalls() = synchronized(stalls) {
        stalls.values.forEach { it.giveUp.cancel() }
        stalls.clear()
        stallRetry?.cancel()
        stallRetry = null
    }

    private suspend fun giveUp(id: String) {
        processing.withLock {
            val stall = synchronized(stalls) { stalls.remove(id) } ?: return
            // The messages queued behind it go now.
            if (store.transition(id, setOf(OutboxState.WaitingForNetwork), OutboxState.Refused, stall.code)) wake.trySend(Unit)
        }
    }

    private class Stall(val giveUp: Job, var code: String?)

    private fun request(entry: OutboxEntry) =
        MessageRequest(hubScope, entry.session, entry.text, entry.clientMessageId, entry.clearDraft, opId = entry.operationId)

    private sealed interface MainTarget {
        data class Found(val session: SessionKey) : MainTarget
        /** The hub refused to start it with a setup code: nothing was created. */
        data class Refused(val code: String) : MainTarget
        /** Not started yet; [code] is why, if the hub said (the computer asleep, the assistant restarting). */
        data class NotYet(val code: String = "main_not_ready") : MainTarget
    }

    private suspend fun resolveMain(): MainTarget {
        store.mainConversation()?.let { return MainTarget.Found(it) }
        val resolver = mainResolver ?: return MainTarget.NotYet()
        return try {
            val target = resolver.findMainConversation() ?: if (store.claimMainCreation()) {
                resolver.createMainConversation()
            } else null
            if (target != null) store.bindMainConversation(target)
            target?.let { MainTarget.Found(it) } ?: MainTarget.NotYet()
        } catch (cancelled: CancellationException) {
            throw cancelled
        } catch (error: Exception) {
            // A 409 with a setup code is the hub refusing before it creates anything: the claim goes back, so the
            // next try (after the user adds a computer…) starts it. Any other failure may have created it remotely:
            // only look up the main pointer on retry.
            val coded = hubCodedFailure(error)
            val refusal = coded?.takeIf { it.status == 409 }?.code?.takeIf { it in SetupFailureCodes }
            if (refusal != null) {
                store.releaseMainCreation()
                MainTarget.Refused(refusal)
            } else {
                // The computer asleep or the assistant restarting: the hub answered and started nothing, so the next
                // try (still a lookup first) may start it once they are back; the message waits meanwhile ([stall]).
                val later = coded?.code?.takeIf { it in RetryLaterCodes }
                if (later != null) store.releaseMainCreation()
                later?.let { MainTarget.NotYet(it) } ?: MainTarget.NotYet()
            }
        }
    }

    private suspend fun attempt(entry: OutboxEntry): MessageTransportResult = try {
        withTimeoutOrNull(reviewTimeoutMillis) { transport.send(request(entry)) } ?: MessageTransportResult.NoReceipt
    } catch (cancelled: CancellationException) {
        withContext(NonCancellable) {
            store.transition(entry.clientMessageId, setOf(OutboxState.Sending), OutboxState.Unknown)
        }
        throw cancelled
    } catch (_: Exception) {
        // The transport must positively identify pre-send failures with NotSent.
        MessageTransportResult.NoReceipt
    }

    companion object {
        /** Refusals of the start that come back by themselves (docs/assistant/onboarding.md 4.6): nothing was created. */
        private val RetryLaterCodes = setOf("node_unreachable", "assistant_offline")
        private val uncertain = setOf(OutboxState.Sending, OutboxState.Unknown, OutboxState.Verifying)
        private val reviewable = setOf(OutboxState.Unknown, OutboxState.Verifying)
        /** Delivery unknown, with or without the review deadline passed: the user decides (confirm or discard). */
        private val undecided = reviewable + OutboxState.NeedsConfirmation
        private val reconcilable = uncertain + setOf(OutboxState.Sent, OutboxState.NeedsConfirmation)
        private val dispatchable = setOf(OutboxState.Queued, OutboxState.WaitingForNetwork)
        private val hidden = setOf(OutboxState.Cancelled, OutboxState.Shown)
        private val refusals = setOf("input_draft", "dialog_open", "op_id_reused", "invalid", "stale_session")
        /** A message that keeps coming back waiting while the link is up goes again this often (iOS retryOutboxSoon). */
        private const val StallRetryMillis = 10_000L
        /** …and is refused after this long, about six tries (iOS OutboxWait.connectedLimit). */
        private const val StallLimitMillis = 60_000L

        /** The hub takes UUIDs only as operation ids. */
        fun newOperationId(): String = java.util.UUID.randomUUID().toString()

        fun classify(result: MessageTransportResult): OutboxState = when (result) {
            MessageTransportResult.Accepted -> OutboxState.Sent
            MessageTransportResult.NotSent -> OutboxState.WaitingForNetwork
            MessageTransportResult.NoReceipt -> OutboxState.Unknown
            is MessageTransportResult.HubError -> when {
                result.delivered == "unknown" -> OutboxState.Verifying
                result.delivered == "yes" -> OutboxState.Sent
                result.code in refusals || result.code in SetupFailureCodes -> OutboxState.Refused
                // The computer asleep: it would wait for a link that is already up; refused at once with Retry (iOS).
                result.code == "node_unreachable" && result.delivered == "none" -> OutboxState.Refused
                result.delivered == "none" && result.retryable -> OutboxState.WaitingForNetwork
                result.delivered == "none" -> OutboxState.Refused
                else -> OutboxState.Unknown
            }
        }
    }
}
