package persona.android.di

import persona.android.AppText
import persona.android.R
import android.app.Application
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.IpPrefix
import android.net.LinkProperties
import android.net.Network
import android.net.NetworkCapabilities
import android.os.Build
import android.util.Log
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner
import persona.android.PersonaApp
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.data.*
import persona.core.domain.Pairing
import persona.core.hub.*
import persona.core.hub.tunnel.HubTunnel
import persona.core.hub.tunnel.HubWays
import persona.core.hub.tunnel.SshIdentity
import java.net.InetAddress
import java.security.MessageDigest
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.json.*
import persona.feature.settings.detectedPhoneSystem
import okhttp3.HttpUrl.Companion.toHttpUrl

/**
 * Owns platform hooks and every hub group's connection (docs/android/android.md "Multiple hubs"). The single-hub API the app was
 * written against stays as it was and stands for the selected group: [scopedData], [state], [connectionFor],
 * [activeCredentials], [endpoints], [pair] (replaces everything) and [unpair] (forgets everything). [events] and
 * [messages] cover every group, with the ids of all but the primary group suffixed (GroupKeys). [hubGroups], [addHub]
 * and the group operations are the multi-hub API.
 */
@Singleton
@OptIn(ExperimentalCoroutinesApi::class)
class ConnectionRuntime @Inject constructor(application: Application) : HubConnection, ConnectionDiagnosticsSource,
    DefaultLifecycleObserver, OpenWindowReconciliationOwner, GroupRuntime.Listener {
    private val app = application as PersonaApp
    private val scope = app.scope
    private val version = app.packageManager.getPackageInfo(app.packageName, 0).versionName.orEmpty()
    private val os by lazy {
        val system = detectedPhoneSystem(app)
        buildJsonObject { put("name", system.name); put("version", system.version); put("sdk", Build.VERSION.SDK_INT) }
    }
    private val heartbeatPolicy by lazy {
        val configuration = app.getSharedPreferences("connection-heartbeat", Application.MODE_PRIVATE)
        fun seconds(key: String, fallback: Int) = configuration.getInt(key, fallback).takeIf { it > 0 } ?: fallback
        HeartbeatPolicy(
            foregroundSeconds = seconds("foreground_seconds", 20),
            backgroundWifiSeconds = seconds("background_wifi_seconds", 240),
            backgroundCellularSeconds = seconds("background_cellular_seconds", 240),
            backgroundOtherSeconds = seconds("background_other_seconds", 240),
            pongTimeoutSeconds = seconds("pong_timeout_seconds", 15),
            backgroundJitterFraction = configuration.getFloat("background_jitter_fraction", 0f)
                .toDouble().takeIf { it.isFinite() }?.coerceIn(0.0, 1.0) ?: 0.0,
        )
    }
    private val reconciliationTimeoutMillis by lazy {
        // Preserve the existing timeout policy; this is not a measured sync duration.
        app.getSharedPreferences("connection-heartbeat", Application.MODE_PRIVATE)
            .getLong("reconciliation_timeout_ms", 7500L).takeIf { it > 0 } ?: 7500L
    }
    private val settings by lazy {
        GroupConnectionSettings(version, JsonPrimitive(app.packageManager.getPackageInfo(app.packageName, 0).longVersionCode),
            os, heartbeatPolicy, reconciliationTimeoutMillis)
    }
    private val manager = app.getSystemService(ConnectivityManager::class.java)
    private val gate = Mutex()

    private val registry = MutableStateFlow(GroupsDocument())
    private val mutableGroups = MutableStateFlow<List<GroupRuntime>>(emptyList())
    /** Every open group, in the registry's order. */
    internal val groups = mutableGroups.asStateFlow()
    private val mutableSelected = MutableStateFlow<GroupRuntime?>(null)
    /** The group the hub-level pages show. */
    internal val selected = mutableSelected.asStateFlow()

    private val current = MutableStateFlow<ScopedHubData?>(null)
    /** The selected group's data. */
    val scopedData = current.asStateFlow()
    override val openWindowReconciliation get() = current.value?.openWindowReconciliation
    private val paired = MutableStateFlow(false)
    val pairedState = paired.asStateFlow()
    private val mutableState = MutableStateFlow(ConnectionState.WaitingForNetwork)
    /** The selected group's connection. */
    override val state = mutableState.asStateFlow()
    private val mutableDiagnostics = MutableStateFlow(ConnectionDiagnostics())
    override val diagnostics = mutableDiagnostics.asStateFlow()
    override suspend fun readDiagnostics(): ConnectionDiagnostics {
        val group = selected.value ?: return diagnostics.value
        val connection = group.connectionOrNull ?: return diagnostics.value
        val result = connection.readDiagnostics()
        if (selected.value === group) mutableDiagnostics.value = result
        return diagnostics.value
    }
    private val mutableEvents = MutableSharedFlow<ServerEvent>()
    /** Every group's events, its machine ids as the merged lists show them. */
    override val events = mutableEvents.asSharedFlow()
    private val groupDesktops = GroupDesktops(scope, selected.map { it?.id }.stateIn(scope, SharingStarted.Eagerly, null))
    /** The desktop apps (the Mac app) signed in to the selected group's hub now. */
    val desktops: StateFlow<List<HubDesktop>> = groupDesktops.selected
    override val lastHello: Hello? get() = selected.value?.hello?.value
    private var started = false
    private var startup: Job? = null
    /** Live cadence: the app is in front, or the floating capsule is usable on an unlocked screen. */
    @Volatile private var foreground = false
    @Volatile private var appForeground = false
    @Volatile private var capsuleAttention = false
    /** The last group went away because no hub accepts this phone any more. */
    @Volatile private var lastRevoked = false

    /** Waits until nothing built on a group's data uses it any more (GroupStacks sets this); then it may close. */
    internal var beforeClose: suspend (GroupRuntime) -> Unit = {}

    private class Opening(val group: GroupRuntime, val members: List<HubMember>, val tunnels: List<HubTunnel>)
    /** Group being opened, by storage path: HubDataStore.openGroup calls the transport factory from inside. */
    private val opening = ConcurrentHashMap<String, Opening>()

    val dataStore = HubDataStore(app, scope) { storage, credentials ->
        val open = opening[storage.path] ?: throw IllegalStateException("No group is being opened")
        open.group.createTransport(storage, credentials, open.members, open.tunnels)
    }

    private val identityLock = Mutex()
    @Volatile private var identity: SshIdentity? = null
    /** Every group's tunnels (docs/operations/network.md "Connecting to hub through SSH"). */
    private val ways = HubWays(identity = ::sshIdentity)
    private class PairedHubTunnels(val pairing: String, val hubAddress: String, val routes: PairedTunnels)
    /** Each paired hub's tunnel settings and address, by hub id: read once, then kept as they are saved. */
    private val pairedTunnels = MutableStateFlow<Map<String, PairedHubTunnels>>(emptyMap())

    init {
        scope.launch {
            groups.collectLatest { list ->
                groupDesktops.retain(list.map { it.id }.toSet())
                list.forEach { group -> launch { group.state.collect { groupDesktops.onState(group.id, it) } } }
            }
        }
        scope.launch {
            selected.collectLatest { group ->
                if (group == null) {
                    mutableDiagnostics.value = ConnectionDiagnostics()
                    mutableState.value = if (lastRevoked) ConnectionState.Revoked else ConnectionState.WaitingForNetwork
                    return@collectLatest
                }
                launch { group.diagnostics.collect { mutableDiagnostics.value = it } }
                group.state.collect { mutableState.value = it }
            }
        }
    }

    /** Manual candidates and route choices of the selected group's own hub (its first paired hub). */
    val endpoints: ConnectionEndpoints = object : ConnectionEndpoints {
        private val settings = selected.flatMapLatest { it?.endpointSettings ?: flowOf(null) }
            .stateIn(scope, SharingStarted.Eagerly, null)
        override val state = settings.flatMapLatest { it?.state ?: flowOf(ConnectionEndpointsState()) }
            .stateIn(scope, SharingStarted.Eagerly, ConnectionEndpointsState())
        override fun check(address: String, replacing: String?) =
            settings.value?.check(address, replacing) ?: EndpointCheck.Invalid(persona.core.contracts.CoreText.of(persona.core.contracts.CoreText.Key.PairHubFirst))
        override suspend fun add(address: String) = controller().add(address)
        override suspend fun replace(url: String, address: String) = controller().replace(url, address)
        override suspend fun remove(url: String) = controller().remove(url)
        override suspend fun setEnabled(url: String, enabled: Boolean) = controller().setEnabled(url, enabled)
        override suspend fun reorder(urls: List<String>) = controller().reorder(urls)
        override suspend fun resetOrder() = controller().resetOrder()
        override suspend fun test(url: String) = controller().test(url)
        private fun controller() = settings.value ?: throw EndpointEditException(persona.core.contracts.CoreText.of(persona.core.contracts.CoreText.Key.PairHubFirst))
    }

    /** Every group's outbox in one list; a message goes to the group its session belongs to. */
    val messages: MessageSender = object : MessageSender {
        override val outbox = groups.flatMapLatest { list ->
            val flows = list.mapNotNull { group -> group.data?.messages?.outbox?.map { entries ->
                val names = GroupNames(group.namespace)
                entries.map(names::outbox)
            } }
            if (flows.isEmpty()) flowOf(emptyList()) else combine(flows) { lists -> lists.flatMap { it } }
        }.stateIn(scope, SharingStarted.Eagerly, emptyList())
        override suspend fun send(session: SessionKey, text: String, clientMessageId: String, clearDraft: String?) {
            gate.withLock {
                val group = groupOf(session.machineId) ?: throw IllegalStateException(AppText.get(R.string.app_error_pair_first))
                checkNotNull(group.data) { AppText.get(R.string.app_error_pair_first) }.messages.send(GroupKeys.rawKey(session), text, clientMessageId, clearDraft)
            }
        }
        override suspend fun cancel(clientMessageId: String) = gate.withLock {
            owner(clientMessageId)?.messages?.cancel(clientMessageId) ?: false
        }
        override suspend fun discard(clientMessageId: String) = gate.withLock {
            owner(clientMessageId)?.messages?.discard(clientMessageId) ?: false
        }
        // By its session: a cancelled message is in no group's outbox any more.
        override fun restore(message: OutboxMessage) {
            groupOf(message.session.machineId)?.data?.messages?.restore(message.copy(session = GroupKeys.rawKey(message.session)))
        }
        override suspend fun retry(clientMessageId: String) = gate.withLock {
            owner(clientMessageId)?.messages?.retry(clientMessageId) ?: false
        }
        override suspend fun confirm(clientMessageId: String, delivered: Boolean) = gate.withLock {
            owner(clientMessageId)?.messages?.confirm(clientMessageId, delivered) ?: false
        }
        private fun owner(clientMessageId: String) = groups.value.mapNotNull { it.data }
            .firstOrNull { data -> data.messages.outbox.value.any { it.clientMessageId == clientMessageId } }
            ?: current.value
    }

    /** Whether the group owning this namespaced machine currently has a desktop app connected. */
    fun desktopOnline(machineId: String): Boolean? = groupOf(machineId)?.let { groupDesktops.online(it.id) }

    /** The group a merged-list machine id belongs to (by its suffix). */
    internal fun groupOf(machineId: String): GroupRuntime? {
        val namespace = GroupKeys.namespaceOf(machineId)
        return groups.value.firstOrNull { it.namespace == namespace }
    }

    fun start() {
        if (started) return
        started = true
        ProcessLifecycleOwner.get().lifecycle.addObserver(this)
        appForeground = ProcessLifecycleOwner.get().lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)
        foreground = appForeground || capsuleAttention
        manager.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = refreshNetwork()
            override fun onLost(network: Network) = refreshNetwork()
            override fun onCapabilitiesChanged(network: Network, capabilities: NetworkCapabilities) = refreshNetwork()
            override fun onLinkPropertiesChanged(network: Network, properties: LinkProperties) = refreshNetwork()
        })
        try {
            // Receivers query pairing synchronously after Application.onCreate returns.
            val restored = runBlocking(Dispatchers.IO) {
                clearUnversionedPairingData(app)
                gate.withLock { restoreLocked() }
            }
            if (!restored) startup = scope.launch {
                try {
                    gate.withLock { app.pairings.load()?.let { pairLegacyLocked(it) } }
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) {
                    failClosed()
                    Log.w("ConnectionRuntime", "Cannot migrate pairing; stored data retained")
                }
            }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) {
            failClosed()
            Log.w("ConnectionRuntime", "Cannot restore pairing; stored data retained")
        }
    }

    private fun failClosed() {
        for (group in mutableGroups.value) group.close()
        mutableGroups.value = emptyList()
        mutableSelected.value = null
        current.value = null
        paired.value = false
        app.showHub(null)
        mutableState.value = ConnectionState.Revoked
        scope.launch { runCatching { dataStore.publish(null) } }
    }

    /** Opens every group the registry lists (the first read migrates a single pairing); false when there is none. */
    private suspend fun restoreLocked(): Boolean {
        val document = dataStore.registry()
        dataStore.sweep()
        applyLocked(document)
        return document.groups.isNotEmpty()
    }

    suspend fun pair(pairing: Pairing) = gate.withLock { pairLegacyLocked(pairing) }

    /**
     * Replaces every pairing with [credentials] (already verified and encrypted by the pairing flow); [access] is the
     * Cloudflare Access service token the pairing was made with.
     */
    suspend fun pair(credentials: HubCredentials, access: HubAccess? = null) = gate.withLock {
        cleanupLocked()
        addLocked(credentials, access = access)
        app.pairings.clear()
    }

    /** Adds a hub next to the existing ones: into the group its mesh puts it in, else as a new group. */
    suspend fun addHub(credentials: HubCredentials, access: HubAccess? = null): String? = gate.withLock {
        addLocked(credentials, access = access).also { app.pairings.clear() }
    }

    private suspend fun pairLegacyLocked(pairing: Pairing) {
        val candidate = Endpoint(pairing.url)
        val identity = HubHttpClient(pairing.hub, pairing.fp, version).use { it.health(candidate) }
        // Old hubs lack an identity; isolate their local-only data without trusting a new pin.
        val hubId = identity.hubId ?: "legacy-" + MessageDigest.getInstance("SHA-256")
            .digest(pairing.url.toByteArray()).joinToString("") { "%02x".format(it) }
        cleanupLocked()
        addLocked(HubCredentials(pairing.url, hubId, pairing.fp, pairing.alt, pairing.token), identity.hubName)
        app.pairings.clear()
    }

    private suspend fun addLocked(credentials: HubCredentials, hubName: String? = null, access: HubAccess? = null): String? {
        require(credentials.hubAddress.isNotBlank() && credentials.deviceToken.isNotBlank())
        val stored = dataStore.storeHub(credentials, access)
        val change = dataStore.change { it.addPairedHub(StoredScope.of(stored), hubName, System.currentTimeMillis()) }
        lastRevoked = false
        applyLocked(change.document, change.erase)
        return change.group
    }

    /** Forgets every pairing and group. */
    suspend fun unpair() = gate.withLock {
        cleanupLocked()
    }

    private suspend fun cleanupLocked(revoked: Boolean = false) = withContext(NonCancellable) {
        lastRevoked = revoked
        closeAllLocked()
        persona.platform.notify.Notify.disconnect(app)
        ways.retain(emptySet())
        pairedTunnels.value = emptyMap()
        dataStore.eraseAll()
        registry.value = GroupsDocument()
        dataStore.publish(null)
        app.pairings.clear()
        withContext(Dispatchers.IO) { clearUnversionedPairingData(app, disconnect = true) }
        if (revoked) mutableState.value = ConnectionState.Revoked
    }

    private suspend fun closeAllLocked() {
        val open = mutableGroups.value
        mutableGroups.value = emptyList()
        current.value = null
        paired.value = false
        mutableSelected.value = null
        app.showHub(null)
        for (group in open) beforeClose(group)
        for (group in open) closeLocked(group)
        mutableDiagnostics.value = ConnectionDiagnostics()
        mutableState.value = ConnectionState.WaitingForNetwork
    }

    private suspend fun closeLocked(group: GroupRuntime) {
        group.close()
        group.data?.let { runCatching { dataStore.closeGroup(it) } }
    }

    /**
     * Makes the open groups match [document]: closes the groups that are gone or whose data scope or own hub changed,
     * erases what [erase] lists once nothing uses it, opens the new ones and passes the others their new hubs.
     */
    private suspend fun applyLocked(document: GroupsDocument, erase: List<ScopeErasure> = emptyList()) {
        registry.value = document
        val open = mutableGroups.value
        val (kept, leaving) = open.partition { group ->
            val entry = document.group(group.id)
            entry != null && entry.storage == group.group.storage && entry.home.pairing == group.group.home.pairing && group.active
        }
        if (leaving.isNotEmpty()) {
            // Pages stop using a leaving group's data before it closes (an open chat of a group that merged away).
            mutableGroups.value = kept
            if (mutableSelected.value in leaving) {
                mutableSelected.value = null
                current.value = null
                app.showHub(null)
            }
            leaving.forEach { beforeClose(it) }
            leaving.forEach { closeLocked(it) }
        }
        if (erase.isNotEmpty()) {
            mutableGroups.value = kept
            try { dataStore.erase(erase) } catch (_: Exception) { Log.w("ConnectionRuntime", "Erasure deferred until restart") }
        }
        val pairedHubs = document.groups.flatMap { group -> group.hubs.map { it.hubId } }.toSet()
        pairedTunnels.update { it.filterKeys(pairedHubs::contains) }
        ways.retain(pairedHubs)
        val next = document.groups.mapNotNull { entry ->
            val namespace = document.namespace(entry.id)
            val existing = kept.firstOrNull { it.id == entry.id }
            if (existing != null) {
                existing.update(entry, namespace, members(entry), tunnels(entry))
                existing
            } else try {
                openLocked(entry, namespace)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) {
                Log.w("ConnectionRuntime", "Cannot open a hub group; its data is retained")
                null
            }
        }
        mutableGroups.value = next
        paired.value = next.isNotEmpty()
        publishSelectionLocked()
        // A group that went away takes its entries (and buttons no hub answers now) out of the ongoing notification.
        // Emitted outside the gate: emit waits for every collector.
        fleets.retainFleets(next.map { it.id }, mutableSelected.value?.id)?.let { scope.launch { mutableEvents.emit(FleetEvent(it)) } }
        refreshNetwork()
        moveUnsentLocked()
    }

    /**
     * A group that merged into another left its data as a pending move: its unsent messages (same client_msg_id, so
     * the hub deduplicates them) and drafts go into the group it joined, then its storage is erased. Nothing is
     * dropped when that fails: the move stays in the registry and is tried again on the next change or start.
     */
    private suspend fun moveUnsentLocked() {
        for (move in registry.value.moves) {
            val target = mutableGroups.value.firstOrNull { it.id == move.into }?.data ?: continue
            try {
                val unsent = dataStore.unsentAt(move.storage.scope)
                if (!unsent.isEmpty) target.adopt(unsent)
                val change = dataStore.change { it.completeMove(move.storage) }
                registry.value = change.document
                dataStore.erase(change.erase)
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { Log.w("ConnectionRuntime", "Cannot move a merged group's messages yet; kept for a retry") }
        }
    }

    private suspend fun openLocked(entry: StoredGroup, namespace: String?): GroupRuntime {
        val group = GroupRuntime(entry, namespace, app, settings, this, ways)
        opening[entry.storage.path] = Opening(group, members(entry), tunnels(entry))
        try {
            val data = dataStore.openGroup(entry.storage.scope, entry.home.pairing.scope)
            group.activate(data)
        } catch (failure: Exception) {
            group.close()
            throw failure
        } finally {
            opening.remove(entry.storage.path)
        }
        group.setForeground(foreground)
        return group
    }

    /** The group's hubs other than its own: the other paired ones with their credentials, then the mesh's. */
    private suspend fun members(entry: StoredGroup): List<HubMember> {
        val center = entry.centerHubId
        val paired = entry.hubs.drop(1).mapNotNull { hub ->
            try {
                dataStore.credentials(hub.pairing.scope)?.let { pairedMember(it, dataStore.endpoints(hub.pairing.scope), hub.hubId == center) }
            } catch (cancelled: CancellationException) { throw cancelled }
            catch (_: Exception) { null }
        }
        return paired + entry.mesh.filter { it.endpoints.isNotEmpty() }.map { meshMember(it, it.hubId == center || it.center == true) }
    }

    /** The tunnels of the group's paired hubs, its own hub's first; a hub whose settings cannot be read has none. */
    private suspend fun tunnels(entry: StoredGroup): List<HubTunnel> = entry.hubs.flatMap { hub ->
        val saved = pairedTunnels.value[hub.hubId]?.takeIf { it.pairing == hub.pairing.path } ?: try {
            val address = dataStore.credentials(hub.pairing.scope)?.hubAddress ?: return@flatMap emptyList()
            PairedHubTunnels(hub.pairing.path, address, dataStore.tunnels(hub.pairing.scope))
                .also { loaded -> pairedTunnels.update { it + (hub.hubId to loaded) } }
        } catch (cancelled: CancellationException) { throw cancelled }
        catch (_: Exception) { return@flatMap emptyList() }
        HubTunnel.of(hub.hubId, saved.hubAddress, saved.routes.ssh, saved.routes.proxy)
    }

    private suspend fun sshIdentity(): SshIdentity = identity ?: identityLock.withLock {
        identity ?: run {
            val seed = try { dataStore.deviceSshKey(SshIdentity::generateSeed) }
                catch (cancelled: CancellationException) { throw cancelled }
                catch (failure: Exception) { throw TunnelException(TunnelProblem.KeyUnavailable, failure) }
            SshIdentity(seed, SshIdentity.comment(app.deviceName)).also { identity = it }
        }
    }

    private suspend fun publishSelectionLocked() {
        val document = registry.value
        val open = mutableGroups.value
        val selection = open.firstOrNull { it.id == document.selected } ?: open.firstOrNull()
        val data = selection?.data
        // HubDataStore.restore() callers (shell, platform services) read the data once the hub handle changes.
        dataStore.publish(data)
        if (mutableSelected.value !== selection) mutableSelected.value = selection
        if (current.value !== data) current.value = data
        app.showHub(selection?.hub)
    }

    /** Waits until start()'s restore or legacy migration is done. */
    suspend fun settled() {
        startup?.join()
        gate.withLock { }
    }

    /** The selected group's own hub's credentials; null while unpaired. */
    fun activeCredentials(): HubCredentials? = current.value?.credentials

    /** The connection of the group [data] belongs to (any group, not only the selected one). */
    fun connectionFor(data: ScopedHubData): OkHttpHubConnection {
        val group = groups.value.firstOrNull { it.data === data } ?: throw CancellationException("Pairing changed")
        if (data.coroutineScope.coroutineContext[Job]?.isActive != true) throw CancellationException("Pairing changed")
        return group.connectionOrNull ?: throw CancellationException("Pairing changed")
    }

    // GroupRuntime.Listener

    /** The latest fleet of each group, merged for the ongoing notification. */
    private val fleets = ConcurrentHashMap<String, FleetState>()

    /**
     * GET /api/v1/fleet of every connected group (the hub pushes `fleet` only when it changes), translated and merged
     * like the pushed ones; a group whose fetch fails keeps its last fleet. The groups are fetched at once so one
     * stalled hub can't use up the caller's timeout before the others are in.
     */
    suspend fun fetchFleet(): FleetState {
        val open = groups.value.ifEmpty { throw CancellationException("Not paired") }
        val store = Mutex()
        var merged: FleetState? = null
        coroutineScope {
            open.mapNotNull { group ->
                val connection = group.connectionOrNull?.takeIf { group.state.value == ConnectionState.Online } ?: return@mapNotNull null
                async {
                    try {
                        val fleet = connection.request("/api/v1/fleet").use { response ->
                            JsonCodec.decodeFromString(FleetResponse.serializer(), response.body!!.string()).fleet
                        }
                        // One at a time, so the last merge has every fetched group.
                        store.withLock {
                            merged = fleets.putFleet(group.id, GroupNames(group.namespace), fleet, groups.value.map { it.id }, selected.value?.id)
                        }
                    } catch (cancelled: CancellationException) { throw cancelled }
                    catch (failure: Exception) { Log.w("ConnectionRuntime", "Fleet fetch failed: ${failure.javaClass.simpleName}") }
                }
            }.awaitAll()
        }
        return merged ?: throw IllegalStateException("No group's fleet")
    }

    override suspend fun onEvent(group: GroupRuntime, event: ServerEvent) {
        if (event is FleetEvent) {
            val merged = fleets.putFleet(group.id, GroupNames(group.namespace), event.fleet, groups.value.map { it.id }, selected.value?.id)
            mutableEvents.emit(FleetEvent(merged))
            return
        }
        if (event is DesktopsEvent) groupDesktops.onDesktops(group.id, event.desktops)
        mutableEvents.emit(GroupNames(group.namespace).event(event))
    }

    override fun onHello(group: GroupRuntime, hello: Hello) {
        val from = hello.hubId ?: return
        scope.launch {
            gate.withLock {
                if (group !in mutableGroups.value) return@withLock
                try {
                    // Most hellos teach nothing new (every reconnect sends one): skip the write then.
                    val now = System.currentTimeMillis()
                    if (registry.value.learn(from, hello.hubName, hello.mesh?.hubs, now).document == registry.value) return@withLock
                    val change = dataStore.change { it.learn(from, hello.hubName, hello.mesh?.hubs, now) }
                    applyLocked(change.document, change.erase)
                } catch (cancelled: CancellationException) { throw cancelled }
                catch (_: Exception) { Log.w("ConnectionRuntime", "Cannot record the hub mesh") }
            }
        }
    }

    override fun onRevoked(group: GroupRuntime) {
        scope.launch {
            gate.withLock {
                if (group !in mutableGroups.value) return@withLock
                try { removeGroupLocked(group.id, revoked = true) }
                catch (_: Exception) { Log.w("ConnectionRuntime", "Pairing cleanup deferred until restart") }
            }
        }
    }

    override fun onMemberRevoked(group: GroupRuntime, hubId: String, ownToken: Boolean) {
        // A hub that only refused the borrowed group credential stays paired elsewhere; the connection skips it.
        if (!ownToken) return
        scope.launch {
            gate.withLock {
                if (group !in mutableGroups.value) return@withLock
                try {
                    val change = dataStore.change { it.removeHub(group.id, hubId) }
                    if (change.document.groups.isEmpty()) removeGroupLocked(group.id, revoked = true)
                    else applyLocked(change.document, change.erase)
                } catch (_: Exception) { Log.w("ConnectionRuntime", "Pairing cleanup deferred until restart") }
            }
        }
    }

    private suspend fun removeGroupLocked(groupId: String, revoked: Boolean = false): Unit = withContext(NonCancellable) {
        val change = dataStore.change { it.removeGroup(groupId) }
        if (change.document.groups.isEmpty()) {
            // The last group: exactly what disconnecting the only pairing always did.
            cleanupLocked(revoked)
        } else {
            applyLocked(change.document, change.erase)
            try { withContext(Dispatchers.IO) { clearOpenedHubFiles(app) } } catch (_: Exception) { Log.w("ConnectionRuntime", "Cannot clear opened hub files") }
        }
    }

    /** The phone's hub groups for settings and the merged lists. */
    val hubGroups: HubGroups = object : HubGroups {
        private val live = this@ConnectionRuntime.groups.flatMapLatest { list ->
            if (list.isEmpty()) flowOf(emptyMap())
            else combine(list.map { group -> combine(group.state, group.hello) { state, hello -> group.id to (state to hello) } }) { it.toMap() }
        }
        override val groups: StateFlow<List<HubGroupInfo>> = combine(registry, live) { document, states ->
            document.groups.map { entry ->
                val (state, hello) = states[entry.id] ?: (ConnectionState.WaitingForNetwork to null)
                val online = hello?.mesh?.hubs?.associate { it.hubId to it.online }.orEmpty()
                val hubs = entry.hubs.map { hub ->
                    HubMemberInfo(hub.hubId, hub.name ?: shortHubName(hub.hubId), paired = true,
                        connected = hello?.hubId == hub.hubId && state in setOf(ConnectionState.Online, ConnectionState.Updating),
                        online = online[hub.hubId])
                } + entry.mesh.map { hub ->
                    HubMemberInfo(hub.hubId, hub.name ?: shortHubName(hub.hubId), paired = false,
                        connected = hello?.hubId == hub.hubId && state in setOf(ConnectionState.Online, ConnectionState.Updating),
                        online = online[hub.hubId] ?: hub.online)
                }
                HubGroupInfo(entry.id, entry.name ?: hubs.firstOrNull()?.name ?: shortHubName(entry.id),
                    document.namespace(entry.id), selected = entry.id == (document.selected ?: document.groups.firstOrNull()?.id),
                    state = state, hubs = hubs)
            }
        }.stateIn(scope, SharingStarted.Eagerly, emptyList())

        override suspend fun select(groupId: String) = gate.withLock {
            if (registry.value.group(groupId) == null) return@withLock
            registry.value = dataStore.updateRegistry { it.copy(selected = groupId) }
            publishSelectionLocked()
        }

        override suspend fun rename(groupId: String, name: String) = gate.withLock {
            registry.value = dataStore.updateRegistry { document ->
                document.copy(groups = document.groups.map { if (it.id == groupId) it.copy(name = name.trim().ifEmpty { null }) else it })
            }
        }

        override suspend fun removeGroup(groupId: String) = gate.withLock { removeGroupLocked(groupId) }

        override suspend fun removeHub(groupId: String, hubId: String) = gate.withLock {
            val group = registry.value.group(groupId) ?: return@withLock
            if (group.hubs.size <= 1 && group.hubs.any { it.hubId == hubId }) return@withLock removeGroupLocked(groupId)
            withContext(NonCancellable) {
                val change = dataStore.change { it.removeHub(groupId, hubId) }
                applyLocked(change.document, change.erase)
            }
        }
    }

    /** Settings › Hubs › SSH Tunnel and Local Proxy of every paired hub. */
    val hubTunnels: HubTunnelSettings = object : HubTunnelSettings {
        private val live = this@ConnectionRuntime.groups.flatMapLatest { list ->
            if (list.isEmpty()) flowOf(emptyMap())
            else combine(list.map { group -> combine(group.state, group.tunnel) { state, tunnel -> group.id to (state to tunnel) } }) { it.toMap() }
        }
        override val hubs: StateFlow<List<HubTunnelInfo>> = combine(registry, pairedTunnels, ways.states, live) { document, saved, states, groups ->
            document.groups.flatMap { entry ->
                val (state, tunnel) = groups[entry.id] ?: (ConnectionState.WaitingForNetwork to null)
                // A hub too old to say its id: its connection cannot tell which tunnel is its own.
                entry.hubs.filterNot { it.hubId.startsWith("legacy-") }.mapNotNull { hub ->
                    val paired = saved[hub.hubId]?.takeIf { it.pairing == hub.pairing.path } ?: return@mapNotNull null
                    HubTunnelInfo(hub.hubId, hub.name ?: shortHubName(hub.hubId), paired.hubAddress, paired.routes.ssh, paired.routes.proxy,
                        states[hub.hubId to TunnelKind.Ssh], states[hub.hubId to TunnelKind.Proxy],
                        routed = tunnel?.takeIf { it.hubId == hub.hubId }?.kind,
                        online = state in setOf(ConnectionState.Online, ConnectionState.Updating), access = paired.routes.access)
                }
            }
        }.stateIn(scope, SharingStarted.Eagerly, emptyList())

        override suspend fun deviceKey() = sshIdentity().deviceKey
        override suspend fun verifySsh(hubId: String, route: SshRoute) = ways.verifySsh(route)
        override suspend fun saveSsh(hubId: String, route: SshRoute?) = saveTunnels(hubId, TunnelKind.Ssh) { it.copy(ssh = route) }
        override suspend fun verifyProxy(hubId: String, route: LocalProxyRoute) {
            val address = pairedTunnels.value[hubId]?.hubAddress ?: throw IllegalStateException("Not a paired hub")
            ways.verifyProxy(route, address, hubId)
        }
        override suspend fun saveProxy(hubId: String, route: LocalProxyRoute?) = saveTunnels(hubId, TunnelKind.Proxy) { it.copy(proxy = route) }
        override suspend fun saveAccess(hubId: String, access: HubAccess?) = saveTunnels(hubId, null) { it.copy(access = access) }
    }

    /** A paired hub's Cloudflare Access service token, as its connection's requests send it (read per request). */
    override fun access(hubId: String): HubAccess? = pairedTunnels.value[hubId]?.routes?.access

    /**
     * A tunnel that waited for new settings (a refused key, a changed server key) is dialled again once they are saved;
     * [kind] null is the Cloudflare Access token, which only needs a connection that is not up to try again.
     */
    private suspend fun saveTunnels(hubId: String, kind: TunnelKind?, change: (PairedTunnels) -> PairedTunnels): Unit = gate.withLock {
        val entry = registry.value.groups.firstOrNull { group -> group.hubs.any { it.hubId == hubId } }
            ?: throw IllegalStateException("Not a paired hub")
        val hub = entry.hubs.first { it.hubId == hubId }
        val address = pairedTunnels.value[hubId]?.takeIf { it.pairing == hub.pairing.path }?.hubAddress
            ?: dataStore.credentials(hub.pairing.scope)?.hubAddress ?: throw IllegalStateException("Not a paired hub")
        val saved = dataStore.updateTunnels(hub.pairing.scope, change)
        pairedTunnels.update { it + (hubId to PairedHubTunnels(hub.pairing.path, address, saved)) }
        kind?.let { ways.unblock(hubId, it) }
        val group = mutableGroups.value.firstOrNull { it.id == entry.id } ?: return@withLock
        val online = group.state.value in setOf(ConnectionState.Online, ConnectionState.Updating)
        group.updateTunnels(tunnels(entry))
        if (!online) group.reconnect()
    }

    override fun reconnect() {
        if (state.value == ConnectionState.Revoked && groups.value.isEmpty()) return
        refreshNetwork()
        groups.value.forEach { it.reconnect() }
    }

    override val groupLinks get() = groups.value.map { group ->
        GroupLink(group.state, reconnect = { refreshNetwork(); group.reconnect() }, wake = group::onWake)
    }

    override fun send(message: JsonObject): Boolean = selected.value?.connectionOrNull?.send(message) ?: false

    override fun onStart(owner: LifecycleOwner) {
        appForeground = true
        applyForeground(wake = true)
        refreshNetwork()
    }

    override fun onStop(owner: LifecycleOwner) {
        appForeground = false
        applyForeground(wake = false)
    }

    /**
     * The floating capsule shows hub content over other apps, so while it is usable (enabled, screen on and unlocked)
     * the connection keeps the foreground cadence: a socket that died during sleep or on a network change is found
     * within seconds of unlocking instead of after the background heartbeat (240s), which showed stale replies (#2124).
     */
    fun setCapsuleAttention(value: Boolean) {
        if (capsuleAttention == value) return
        capsuleAttention = value
        applyForeground(wake = value)
    }

    /** Validates every open connection now, as coming to the front does; a dead socket reconnects. */
    fun wake() { groups.value.forEach { it.onWake() } }

    private fun applyForeground(wake: Boolean) {
        foreground = appForeground || capsuleAttention
        groups.value.forEach { group ->
            group.setForeground(foreground)
            if (wake) group.onWake()
        }
    }

    private fun refreshNetwork() {
        val open = groups.value
        if (open.isEmpty()) return
        scope.launch(Dispatchers.IO) {
            val network = manager.activeNetwork
            val properties = network?.let(manager::getLinkProperties)
            val capabilities = network?.let(manager::getNetworkCapabilities)
            val transport = when {
                capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) == true -> HubNetworkTransport.Wifi
                capabilities?.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) == true -> HubNetworkTransport.Cellular
                else -> HubNetworkTransport.Other
            }
            val needsPermission = Build.VERSION.SDK_INT >= 37 && app.applicationInfo.targetSdkVersion >= 37
            val granted = !needsPermission || app.checkSelfPermission(LOCAL_NETWORK_PERMISSION) == PackageManager.PERMISSION_GRANTED
            for (group in open) {
                val connection = group.connectionOrNull ?: continue
                // granted is always true below 37; the SDK check spells out IpPrefix(InetAddress, Int), public from 33.
                val localHosts = if (granted || Build.VERSION.SDK_INT < 33) emptySet() else connection.allCandidates().mapNotNull { endpoint ->
                    val host = runCatching { endpoint.url.toHttpUrl().host }.getOrNull() ?: return@mapNotNull null
                    val addresses = runCatching { InetAddress.getAllByName(host).toList() }.getOrDefault(emptyList())
                    host.takeIf { addresses.any { address ->
                        properties?.linkAddresses.orEmpty().any { link -> IpPrefix(link.address, link.prefixLength).contains(address) }
                    } }
                }.toSet()
                if (group.connectionOrNull === connection && manager.activeNetwork == network) group.updateNetwork(HubNetwork(
                    network?.toString() ?: "offline", available = network != null, homeWifi = needsPermission,
                    localNetworkPermissionGranted = granted, localHosts = localHosts, transport = transport))
            }
        }
    }

    companion object {
        const val LOCAL_NETWORK_PERMISSION = "android.permission.ACCESS_LOCAL_NETWORK"
    }
}

/** A hub without a name yet (an older hub, or before its first hello): the start of its id. */
internal fun shortHubName(hubId: String) = "hub " + hubId.removePrefix("legacy-").take(6)
