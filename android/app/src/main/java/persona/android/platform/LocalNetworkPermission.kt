package persona.android.platform

import persona.android.R
import androidx.compose.ui.res.stringResource
import android.app.Application
import android.content.Intent
import android.content.pm.PackageManager
import android.net.ConnectivityManager
import android.net.IpPrefix
import android.net.Uri
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import persona.android.di.ConnectionRuntime
import persona.android.di.WiredPairingService
import persona.core.contracts.api.*
import persona.core.hub.HubPairingClient
import persona.core.hub.LocalAddressPolicy
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import okhttp3.HttpUrl.Companion.toHttpUrl
import java.net.InetAddress
import java.security.MessageDigest
import javax.inject.Inject
import javax.inject.Singleton

internal const val LOCAL_NETWORK = "android.permission.ACCESS_LOCAL_NETWORK"
internal fun needsLocalNetworkPermission(sdk: Int, target: Int) = sdk >= 37 && target >= 37

@Singleton
class LocalNetworkPermission @Inject constructor(private val application: Application) {
    internal val request = MutableStateFlow<CompletableDeferred<Boolean>?>(null)
    private val lock = Mutex()
    private val preferences = application.getSharedPreferences("platform-entry", 0)
    internal val requestedBefore get() = preferences.getBoolean("local-network-requested", false)
    internal fun recordRequest() { preferences.edit().putBoolean("local-network-requested", true).apply() }
    internal fun granted() = !needsLocalNetworkPermission(Build.VERSION.SDK_INT, application.applicationInfo.targetSdkVersion) ||
        application.checkSelfPermission(LOCAL_NETWORK) == PackageManager.PERMISSION_GRANTED

    internal suspend fun missingFor(pairing: PairingRequest): Boolean = missingForAddresses(HubPairingClient.addresses(pairing))

    private suspend fun missingForAddresses(addresses: List<String>): Boolean {
        if (granted()) return false
        return withContext(Dispatchers.IO) {
            val manager = application.getSystemService(ConnectivityManager::class.java)
            val properties = manager.activeNetwork?.let(manager::getLinkProperties)
            // granted() is always true below 37; the SDK check spells out IpPrefix(InetAddress, Int), public from 33.
            val prefixes = if (Build.VERSION.SDK_INT < 33) emptyList()
                else properties?.linkAddresses.orEmpty().map { IpPrefix(it.address, it.prefixLength) }
            addresses.any { endpoint ->
                val host = endpoint.toHttpUrl().host
                host.endsWith(".local", ignoreCase = true) || runCatching { InetAddress.getAllByName(host).toList() }
                    .getOrDefault(emptyList()).any { address ->
                        !address.isLoopbackAddress && (LocalAddressPolicy().allows(address) || prefixes.any { it.contains(address) })
                    }
            }
        }
    }

    internal suspend fun ensureAllowed(pairing: PairingRequest) = ensure { missingFor(pairing) }

    internal suspend fun ensureAllowedAddress(address: String) = ensure { missingForAddresses(listOf(address)) }

    private suspend fun ensure(missing: suspend () -> Boolean) = lock.withLock {
        if (!missing()) return@withLock
        val reply = CompletableDeferred<Boolean>()
        request.value = reply
        try {
            if (!reply.await() || !granted()) throw PairingException(PairingFailure.LocalPermission)
        } finally { request.compareAndSet(reply, null) }
    }
}

@Singleton
class PlatformPairing @Inject constructor(
    private val application: Application,
    private val delegate: WiredPairingService,
    private val runtime: ConnectionRuntime,
    private val permission: LocalNetworkPermission,
    private val passwordBrowser: PasswordBrowserLogin,
) : PairingService by delegate {
    private val preferences = application.getSharedPreferences("platform-entry", 0)
    internal fun readinessPending(): Boolean = runtime.activeCredentials()?.deviceToken?.let {
        preferences.getBoolean(readinessKey(it), false)
    } ?: false
    internal fun completeReadiness() {
        runtime.activeCredentials()?.deviceToken?.let { preferences.edit().remove(readinessKey(it)).apply() }
    }
    override suspend fun pair(request: PairingRequest) = pair(request) { }
    override suspend fun passwordLogin(address: String, progress: (PairingProgress) -> Unit) {
        permission.ensureAllowedAddress(normalizedPasswordAddress(application, address).toString())
        pair(passwordBrowser.login(address), progress)
    }
    override suspend fun pair(request: PairingRequest, progress: (PairingProgress) -> Unit) {
        val parsed = request.parsedPairingLink() ?: request
        permission.ensureAllowed(parsed)
        try {
            delegate.pair(parsed, progress)
            runtime.activeCredentials()?.deviceToken?.let {
                withContext(Dispatchers.IO) { check(preferences.edit().putBoolean(readinessKey(it), true).commit()) }
            }
        } catch (error: PairingException) {
            if (error.reason == PairingFailure.Timeout && permission.missingFor(parsed)) throw PairingException(PairingFailure.LocalPermission)
            throw error
        }
    }
    /** The same flow, adding a hub instead of replacing the pairing (settings › hub › "Add hub"). */
    val adding: PairingService = object : PairingService by this {
        private val none = kotlinx.coroutines.flow.MutableStateFlow(false)
        // The pairing page warns that a new pairing replaces the current one while paired; adding replaces nothing.
        override val isPaired: kotlinx.coroutines.flow.StateFlow<Boolean> = none
        override suspend fun passwordLogin(address: String, progress: (PairingProgress) -> Unit) {
            permission.ensureAllowedAddress(normalizedPasswordAddress(application, address).toString())
            pair(passwordBrowser.login(address), progress)
        }
        override suspend fun pair(request: PairingRequest) = pair(request) { }
        override suspend fun pair(request: PairingRequest, progress: (PairingProgress) -> Unit) {
            val parsed = request.parsedPairingLink() ?: request
            permission.ensureAllowed(parsed)
            try { delegate.addHub(parsed, progress) }
            catch (error: PairingException) {
                if (error.reason == PairingFailure.Timeout && permission.missingFor(parsed)) throw PairingException(PairingFailure.LocalPermission)
                throw error
            }
        }
    }
    private fun readinessKey(token: String) = "readiness-" + MessageDigest.getInstance("SHA-256")
        .digest(token.toByteArray()).joinToString("") { "%02x".format(it) }
}

@Composable
internal fun LocalNetworkPermissionDialog(permission: LocalNetworkPermission, connection: HubConnection) {
    val pending by permission.request.collectAsState()
    val context = LocalContext.current
    val state by connection.state.collectAsState()
    var dismissed by remember(state) { mutableStateOf(false) }
    var openedSettings by remember { mutableStateOf(false) }
    var wasGranted by remember { mutableStateOf(permission.granted()) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        pending?.complete(granted)
        if (granted) connection.reconnect() else dismissed = true
    }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        val granted = permission.granted()
        if (granted && !wasGranted) connection.reconnect()
        wasGranted = granted
        if (openedSettings) {
            openedSettings = false
            pending?.complete(granted)
            if (!granted) dismissed = true
        }
    }
    if (pending != null || state == ConnectionState.NeedsLocalNetworkPermission && !permission.granted() && !dismissed) AlertDialog(
        onDismissRequest = { pending?.complete(false); dismissed = true },
        title = { Text(stringResource(R.string.app_local_network_title)) },
        text = { Text(stringResource(if (permission.requestedBefore) R.string.app_local_network_denied
            else R.string.app_local_network_ask)) },
        confirmButton = { TextButton(onClick = {
            if (permission.requestedBefore) {
                try {
                    openedSettings = true
                    context.startActivity(Intent(Settings.ACTION_APPLICATION_DETAILS_SETTINGS, Uri.parse("package:${context.packageName}")))
                } catch (_: Exception) { openedSettings = false; pending?.complete(false) }
            } else {
                permission.recordRequest()
                launcher.launch(LOCAL_NETWORK)
            }
        }) { Text(stringResource(if (permission.requestedBefore) R.string.app_local_network_open_settings else R.string.app_local_network_allow), maxLines = 1) } },
        dismissButton = { TextButton(onClick = { pending?.complete(false); dismissed = true }) { Text(stringResource(R.string.app_local_network_not_now), maxLines = 1) } },
    )
}
