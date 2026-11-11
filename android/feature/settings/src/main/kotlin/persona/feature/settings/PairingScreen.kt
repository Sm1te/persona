package persona.feature.settings

import persona.core.designsystem.text
import android.Manifest
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.PackageManager
import android.content.res.Resources
import androidx.annotation.StringRes
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import persona.core.contracts.api.*
import persona.core.domain.engines.Engines
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull

private enum class PairingPage(@StringRes val title: Int?) {
    Welcome(null),
    Password(R.string.settings_password_login_title),
    Connect(R.string.settings_pairing_title),
    Manual(R.string.settings_pairing_manual),
    Confirm(R.string.settings_pairing_confirm),
    Connecting(R.string.settings_pairing_connecting_title),
    Failed(R.string.settings_pairing_failed_title),
}

@Composable
fun PairingScreen(
    pairing: PairingService,
    sessions: SessionsRepository,
    initialLink: String? = null,
    onReady: () -> Unit,
    onDiagnostics: () -> Unit,
    diagnostics: SettingsDiagnostics? = null,
    onCredentialsSaved: () -> Unit = {},
    onBack: (() -> Unit)? = null,
    footer: @Composable () -> Unit = {},
    /** Saves the engine of the hub's machine, the step between pairing and the checklist (docs/integrations/engines.md). */
    machines: MachineManagement? = null,
    /** Start at the connect step instead of the welcome page (adding another hub). */
    skipWelcome: Boolean = false,
    /** Where the last attempt is kept for "Connection diagnostics", which has nothing else to show before a pairing succeeds. */
    attempts: PairingAttempts? = null,
    /** The main conversation's steps on the checklist that follows a pairing (docs/assistant/onboarding.md 4.5). */
    mainReadiness: AssistantReadinessSource? = null,
) {
    val context = LocalContext.current
    val resources = LocalResources.current
    val scope = rememberCoroutineScope()
    val paired by pairing.isPaired.collectAsState()
    val records = remember(context) { ReadinessRecords(context) }
    // The first-run checklist comes back only on the root page (no link to confirm): Settings › Pairing and Add hub
    // (skipWelcome) are for connecting, so they start at the connect choices even while that checklist is unfinished.
    // A pairing made on any page is still followed by it (nextStep).
    var readiness by rememberSaveable {
        mutableStateOf(paired && !skipWelcome && initialLink == null && needsOnboardingReadiness(context))
    }
    // The machine whose engine must be chosen first; kept on disk so a restarted app asks again rather than skipping.
    var engineStep by rememberSaveable { mutableStateOf(if (paired && machines != null) records.enginePending() else null) }
    var welcome by rememberSaveable { mutableStateOf(initialLink == null && !skipWelcome) }
    var otherWays by rememberSaveable { mutableStateOf(false) }
    var credentialsSaved by remember { mutableStateOf(false) }
    var lastRequest by remember { mutableStateOf<PairingRequest?>(null) }
    var awaitingSettings by remember { mutableStateOf(false) }
    var permissionDenied by rememberSaveable { mutableStateOf(false) }
    // Credentials intentionally stay out of saved-instance state and preferences.
    var address by remember { mutableStateOf("") }
    var token by remember { mutableStateOf("") }
    var metadata by remember { mutableStateOf<PairingRequest?>(null) }
    // A Cloudflare Access service token (docs/operations/network.md "Behind Cloudflare Access"), kept out of saved state too.
    var accessId by remember { mutableStateOf("") }
    var accessSecret by remember { mutableStateOf("") }
    var accessOpen by remember { mutableStateOf(false) }
    var failure by remember { mutableStateOf<PairingFailure?>(null) }
    /** The address a failure names ([PairingException.address], #2622). */
    var failureAddress by remember { mutableStateOf<String?>(null) }
    var running by remember { mutableStateOf(false) }
    var progress by remember { mutableStateOf(listOf<PairingProgress>()) }
    var confirm by remember { mutableStateOf<PairingRequest?>(null) }
    var manual by remember { mutableStateOf(false) }
    var passwordEntry by remember { mutableStateOf(true) }
    var loginJob by remember { mutableStateOf<kotlinx.coroutines.Job?>(null) }
    var info by remember { mutableStateOf(false) }
    var notice by remember { mutableStateOf<Int?>(null) } // a string resource
    var attempt by remember { mutableStateOf<PairingAttempt?>(null) }
    // Why the manual form's address can't be used (a string resource), shown under the field instead of a failure page.
    var addressError by remember { mutableStateOf<Int?>(null) }
    // Connecting failures in a row for the same input, so "Try again" visibly did something.
    var tries by remember { mutableIntStateOf(0) }
    // The details on the confirm page came from the manual form (Next), not a scanned, pasted or opened link: a wrong or
    // expired code there is most likely a typo, so the failure page offers editing first.
    var typed by remember { mutableStateOf(false) }
    val code = remember { FocusRequester() }
    fun recordAttempt(request: PairingRequest?, error: Throwable?) {
        attempt = pairingAttempt(request, error, progress.lastOrNull()?.stage,
            progress.lastOrNull { it.candidates.isNotEmpty() }?.candidates.orEmpty())
        attempts?.record(attempt)
    }
    fun accept(link: String) {
        passwordEntry = false
        manual = false; confirm = null; progress = emptyList()
        welcome = false; readiness = false; credentialsSaved = false; lastRequest = null; tries = 0; addressError = null; typed = false
        // A token typed for another hub must never go to this one.
        accessId = ""; accessSecret = ""; accessOpen = false
        try {
            val request = PairingInput.parse(link)
            metadata = request; address = request.url; token = request.token; failure = null
            confirm = request; notice = null
        } catch (error: Exception) {
            failure = pairingFailure(error)
            recordAttempt(null, error)
        }
    }
    LaunchedEffect(initialLink) { if (!readiness) initialLink?.let(::accept) }
    // After the credentials: the hub's machine's engine when the hub reports engines, else the checklist. The list
    // may reach the shared repository a moment after the refresh (or come with the first push): wait a little.
    suspend fun nextStep() {
        val listed: List<persona.core.contracts.Machine> = if (machines == null) emptyList()
            else withTimeoutOrNull(3_000) { sessions.machines.first { it.isNotEmpty() } } ?: sessions.machines.value
        val engine = Engines.pairingStep(listed)
        records.enginePending(engine?.id)
        records.finishing(false)
        if (engine != null) engineStep = engine.id else readiness = true
    }
    // Re-pairing from Settings › Pairing replaces the shell that page sits in, and the root's pairing page comes up at
    // "Welcome to Persona" instead: it takes over the steps the other page did not get to.
    LaunchedEffect(paired) {
        if (!paired || running || credentialsSaved || engineStep != null || readiness || !records.finishing()) return@LaunchedEffect
        welcome = false; confirm = null; manual = false; failure = null; notice = null
        running = true
        progress = PairingStage.entries.map { PairingProgress(it) }
        try {
            records.pending(true)
            nextStep()
        } finally { running = false }
    }
    fun connect(request: PairingRequest?, passwordAddress: String? = null) {
        if (running) return
        if (passwordAddress != null) typed = false
        lastRequest = request
        running = true
        loginJob = scope.launch {
            failure = null; progress = emptyList()
            fun record(item: PairingProgress) {
                val stamped = item.copy(timestamp = item.timestamp ?: pairingStepTime(java.time.Instant.now(), LocalizedFormats.locale(context)))
                progress = progress.filterNot { it.stage == item.stage } + stamped
            }
            try {
                record(PairingProgress(PairingStage.Parse))
                if (request != null && PairingLocalNetwork.needsPermission(context, request)) {
                    failure = PairingFailure.LocalPermission
                    recordAttempt(request, PairingException(PairingFailure.LocalPermission))
                    return@launch
                }
                if (!credentialsSaved) {
                    // Saving replaces the pairing, and the shell this page may sit in goes with it.
                    records.finishing(true)
                    if (passwordAddress != null) pairing.passwordLogin(passwordAddress) { record(it) }
                    else pairing.pair(requireNotNull(request)) { record(it) }
                    credentialsSaved = true
                    recordAttempt(request, null)
                    records.pending(true)
                    // The link's pairing code is spent; the host must not offer it for confirmation again.
                    onCredentialsSaved()
                }
                record(PairingProgress(PairingStage.LoadMachines))
                try { sessions.refresh() }
                catch (error: CancellationException) { throw error }
                catch (error: Exception) {
                    // The pairing is saved and the shell refreshes on its own; a slow first list is not a failed pairing.
                    android.util.Log.w("PersonaPairing", "machine list after pairing: " + error.javaClass.name)
                    notice = R.string.settings_pairing_notice_list_later
                }
                record(PairingProgress(PairingStage.Complete))
                token = ""; metadata = null; lastRequest = null; manual = false
                nextStep()
            } catch (error: CancellationException) {
                if (!credentialsSaved) records.finishing(false)
                progress = emptyList()
                throw error
            }
            catch (error: Exception) {
                if (!credentialsSaved) records.finishing(false)
                if (passwordAddress != null) notice = R.string.settings_password_login_failed
                // Exception messages may contain pairing links or credentials: log only types and frames.
                android.util.Log.w("PersonaPairing", generateSequence(error as Throwable) { it.cause }.take(4)
                    .joinToString(" <- ") { e -> e.javaClass.name + " @ " + e.stackTrace.take(3).joinToString(", ") })
                failure = pairingFailure(error)
                failureAddress = pairingFailureAddress(error)
                tries++
                if (failure == PairingFailure.Timeout && request != null && PairingLocalNetwork.needsPermission(context, request)) {
                    failure = PairingFailure.LocalPermission
                }
                // A saved pairing that only missed the machine list is not a failed pairing.
                if (!credentialsSaved) recordAttempt(request, error)
            }
            finally { running = false }
        }
    }
    val localPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) lastRequest?.let { connect(it) }
        else { permissionDenied = true; failure = PairingFailure.LocalPermission; notice = R.string.settings_pairing_notice_local_denied }
    }
    fun authorize() {
        if (!PairingLocalNetwork.missing(context)) lastRequest?.let { connect(it) }
        else if (permissionDenied) {
            awaitingSettings = PhoneReadiness.open(context, listOf(appDetails))
        } else localPermission.launch(PairingLocalNetwork.Permission)
    }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        if (awaitingSettings) {
            awaitingSettings = false
            if (!PairingLocalNetwork.missing(context)) { notice = null; lastRequest?.let { connect(it) } }
        }
    }
    val scanner = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let(::accept)
        if (result.originalIntent?.getBooleanExtra(PairingScanActivity.ManualInput, false) == true) {
            credentialsSaved = false; lastRequest = null
            confirm = null; failure = null; progress = emptyList(); manual = true
        }
    }
    fun scan() { scanner.launch(ScanOptions().setCaptureActivity(PairingScanActivity::class.java).setOrientationLocked(false).setDesiredBarcodeFormats(ScanOptions.QR_CODE).setPrompt("").setBeepEnabled(false)) }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) scan() else { credentialsSaved = false; lastRequest = null; confirm = null; failure = null; progress = emptyList(); manual = true; notice = R.string.settings_pairing_notice_camera_denied }
    }
    fun requestScan() {
        if (context.checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) scan()
        else camera.launch(Manifest.permission.CAMERA)
    }
    fun paste() {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        val value = clipboard.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
        if (value.isNullOrBlank()) notice = R.string.settings_pairing_notice_clipboard_empty else accept(value)
    }
    fun edit() {
        passwordEntry = false
        credentialsSaved = false; lastRequest = null; confirm = null; failure = null; progress = emptyList(); manual = true; tries = 0; addressError = null
        // The address may change to another hub: its token starts empty.
        accessId = ""; accessSecret = ""; accessOpen = false
    }
    fun next() {
        if (address.isBlank() || token.isBlank()) return
        tries = 0
        try { confirm = PairingInput.manual(address, token, metadata); addressError = null; typed = true }
        catch (error: Exception) {
            addressError = if (pairingFailure(error) == PairingFailure.TlsRequired) R.string.settings_pairing_failure_tls_required
                else R.string.settings_pairing_manual_invalid
            recordAttempt(PairingRequest(address.trim(), token), error)
        }
    }
    val attemptFailure = failure
    val confirmedRequest = confirm
    val page = when {
        welcome -> PairingPage.Welcome
        attemptFailure != null -> PairingPage.Failed
        running || progress.isNotEmpty() -> PairingPage.Connecting
        confirmedRequest != null -> PairingPage.Confirm
        manual -> PairingPage.Manual
        passwordEntry -> PairingPage.Password
        else -> PairingPage.Connect
    }
    val later = onBack ?: onReady
    fun returnFromAttempt() {
        confirm = null
        failure = null
        progress = emptyList()
        notice = null
        manual = typed
    }
    val back: (() -> Unit)? = when {
        engineStep != null -> null
        readiness -> later
        page == PairingPage.Connecting -> null
        page == PairingPage.Confirm || page == PairingPage.Failed -> ::returnFromAttempt
        page == PairingPage.Manual -> ({ manual = false })
        page == PairingPage.Welcome -> null
        onBack != null -> onBack
        !skipWelcome && initialLink == null -> ({ welcome = true; notice = null })
        else -> null
    }
    // Connecting cannot be dismissed through Back. The engine step owns Back once credentials are saved.
    BackHandler(engineStep == null && (page == PairingPage.Connecting || back != null)) { back?.invoke() }
    val machineToChoose = engineStep
    if (machineToChoose != null) {
        EngineSelectionScreen(machineToChoose, sessions, machines, pairing = true, onDone = {
            records.enginePending(null); engineStep = null; readiness = true
        })
    } else if (readiness) {
        ReadinessScreen(sessions, diagnostics, notificationsOnly = false,
            onDiagnostics = onDiagnostics, onDone = { records.pending(false); onReady() }, onLater = later, onBack = null,
            readiness = mainReadiness, footer = footer)
    } else PairingListPage(page, onBack = back, state = key(page) { rememberLazyListState() }) {
        when (page) {
            PairingPage.Welcome -> {
                item {
                    Column(Modifier.padding(vertical = Tokens.Space.l), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        Text(stringResource(R.string.settings_pairing_welcome_title), style = PersonaTheme.type.largeTitle,
                            color = PersonaTheme.palette.ink, modifier = Modifier.semantics { heading() })
                        Text(stringResource(R.string.settings_pairing_welcome_body), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2)
                    }
                }
                item { JGroupItem(GroupPosition.of(0, 3)) { JRow("Agents", leadingIcon = PhIcons.Robot, subtitle = stringResource(R.string.settings_pairing_welcome_agents), subtitleLines = 3) } }
                item { JGroupItem(GroupPosition.of(1, 3)) { JRow(stringResource(R.string.settings_pairing_welcome_inbox_title), leadingIcon = PhIcons.Tray, subtitle = stringResource(R.string.settings_pairing_welcome_inbox), subtitleLines = 3) } }
                item { JGroupItem(GroupPosition.of(2, 3)) { JRow(stringResource(R.string.settings_pairing_welcome_assistant_title), leadingIcon = PhIcons.Sparkle, subtitle = stringResource(R.string.settings_pairing_welcome_assistant), subtitleLines = 3) } }
                item { SettingsCaption(stringResource(R.string.settings_pairing_welcome_push), Modifier.padding(vertical = Tokens.Space.m)) }
                item { JButton(stringResource(R.string.settings_pairing_welcome_connect), { welcome = false }, Modifier.fillMaxWidth()) }
            }
            PairingPage.Failed -> {
                val reason = requireNotNull(attemptFailure)
                val recovery = reason.recovery()
                // A typed code that is wrong or expired: fix it in the form first (it keeps what was typed); scanning is
                // the second way. Certificate and https problems, and anything from a link, still lead with scanning.
                val retype = typed && (reason == PairingFailure.InvalidCode || reason == PairingFailure.Expired)
                pairingInstructions(R.string.settings_pairing_failed_body)
                item { JNotice(pairingExplanation(reason, failureAddress), status = Status.NeedsYou) }
                if (tries >= 2) item {
                    SettingsCaption(pluralStringResource(R.plurals.settings_pairing_tried_again, tries, tries), Modifier.padding(top = Tokens.Space.s))
                }
                // Only the step: the error chain is for "Diagnostics", one tap away.
                attempt?.takeIf { it.failed }?.step?.let { step ->
                    item { SettingsCaption(stringResource(R.string.settings_pairing_stopped_at, stringResource(step.labelRes())), Modifier.padding(top = Tokens.Space.s)) }
                }
                item {
                    if (retype) PairingActions {
                        JButton(stringResource(R.string.settings_pairing_edit_address_code), ::edit, Modifier.fillMaxWidth())
                        JButton(stringResource(R.string.settings_pairing_diagnostics), onDiagnostics, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                        JButton(stringResource(R.string.settings_pairing_rescan), ::requestScan, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, icon = PhIcons.Scan)
                    } else PairingActions {
                        JButton(stringResource(if (credentialsSaved && recovery == PairingRecovery.Retry) R.string.settings_pairing_reload_machines else recovery.label), {
                            when (recovery) {
                                PairingRecovery.Permission -> authorize()
                                PairingRecovery.Scan -> requestScan()
                                PairingRecovery.Retry, PairingRecovery.Update -> lastRequest?.let { connect(it) } ?: edit()
                                PairingRecovery.Access -> lastRequest?.let { confirm = it; failure = null; progress = emptyList(); accessOpen = true } ?: edit()
                            }
                        }, Modifier.fillMaxWidth())
                        JButton(stringResource(R.string.settings_pairing_diagnostics), onDiagnostics, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                        if (recovery != PairingRecovery.Scan) JButton(stringResource(R.string.settings_pairing_rescan), ::requestScan, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary, icon = PhIcons.Scan)
                        JButton(stringResource(R.string.settings_pairing_edit_address_code), ::edit, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                    }
                }
                if (progress.isNotEmpty()) pairingProgress(progress, running, failed = true, resources)
            }
            PairingPage.Connecting -> {
                pairingInstructions(R.string.settings_pairing_connecting_body)
                pairingProgress(progress, running, failed = false, resources)
                if (running && passwordEntry) item { JTextButton(stringResource(R.string.settings_password_login_cancel), { loginJob?.cancel() }) }
            }
            PairingPage.Confirm -> {
                val request = requireNotNull(confirmedRequest)
                pairingInstructions(R.string.settings_pairing_confirm_body)
                pairingDetails(request, resources)
                item {
                    JTextButton(stringResource(R.string.settings_access_title), { accessOpen = !accessOpen }, Modifier.padding(top = Tokens.Space.s))
                }
                if (accessOpen) item {
                    JCard {
                        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                            HubAccessFields(accessId, { accessId = it }, accessSecret, { accessSecret = it })
                            SettingsCaption(stringResource(R.string.settings_access_pairing_caption))
                        }
                    }
                }
                if (paired) item { JNotice(stringResource(R.string.settings_pairing_replace_body), title = stringResource(R.string.settings_pairing_replace_title), status = Status.NeedsYou) }
                item {
                    PairingActions {
                        JButton(stringResource(R.string.settings_pairing_confirm), {
                            confirm = null; connect(request.copy(access = if (accessOpen) HubAccess.of(accessId, accessSecret) else null))
                        }, Modifier.fillMaxWidth())
                        JButton(stringResource(R.string.settings_pairing_edit_input), ::edit, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                    }
                }
            }
            PairingPage.Manual -> {
                pairingInstructions(R.string.settings_pairing_manual_body)
                item {
                    JCard {
                        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                            JTextField(address, { value ->
                                address = value; addressError = null; metadata = PairingInput.editedAddress(metadata, value)
                                if (PairingInput.isLink(value)) accept(value)
                            }, label = stringResource(R.string.settings_pairing_address_label), modifier = Modifier.fillMaxWidth(),
                                supportingText = addressError?.let { stringResource(it) }, isError = addressError != null,
                                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri, imeAction = ImeAction.Next),
                                keyboardActions = KeyboardActions(onNext = { code.requestFocus() }), singleLine = true)
                            JTextField(token, { token = it; metadata = metadata?.copy(token = it) }, label = stringResource(R.string.settings_pairing_code_label),
                                visualTransformation = PasswordVisualTransformation(),
                                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Go),
                                keyboardActions = KeyboardActions(onGo = { next() }),
                                modifier = Modifier.fillMaxWidth().focusRequester(code), singleLine = true)
                            if (address.startsWith("http://")) SettingsCaption(stringResource(R.string.settings_pairing_http_warning))
                        }
                    }
                }
                item {
                    PairingActions {
                        JButton(stringResource(R.string.settings_pairing_next), ::next, Modifier.fillMaxWidth(), enabled = address.isNotBlank() && token.isNotBlank())
                        JButton(stringResource(R.string.settings_pairing_paste), ::paste, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                        JTextButton(stringResource(R.string.settings_pairing_back_to_scan), { manual = false })
                    }
                }
            }
            PairingPage.Password -> {
                pairingInstructions(R.string.settings_password_login_body)
                item { JCard {
                    JTextField(address, { address = it }, label = stringResource(R.string.settings_password_login_address), modifier = Modifier.fillMaxWidth(),
                        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri), singleLine = true)
                } }
                item { PairingActions {
                    JButton(stringResource(R.string.settings_password_login_connect), { connect(null, address) }, Modifier.fillMaxWidth(), enabled = address.isNotBlank())
                    JButton(stringResource(R.string.settings_password_login_pairing), { passwordEntry = false }, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                    SettingsCaption(stringResource(R.string.settings_password_login_note))
                } }
            }
            PairingPage.Connect -> {
                pairingInstructions(R.string.settings_pairing_connect_body)
                item {
                    PairingActions {
                        JButton(stringResource(R.string.settings_password_login_title), { passwordEntry = true }, Modifier.fillMaxWidth(), kind = JButtonKind.Secondary)
                        JButton(stringResource(R.string.settings_pairing_scan), ::requestScan, Modifier.fillMaxWidth(), icon = PhIcons.Scan)
                        Row(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                            JButton(stringResource(R.string.settings_pairing_paste), ::paste, Modifier.weight(1f), kind = JButtonKind.Secondary)
                            JButton(stringResource(R.string.settings_pairing_manual), { manual = true }, Modifier.weight(1f), kind = JButtonKind.Secondary)
                        }
                        SettingsCaption(stringResource(R.string.settings_pairing_connect_push))
                    }
                }
                // The command line is for people who installed the hub themselves (docs/assistant/onboarding.md 4.5): folded away.
                item {
                    JTextButton(stringResource(R.string.settings_pairing_other_ways), { otherWays = !otherWays },
                        Modifier.padding(top = Tokens.Space.m))
                }
                if (otherWays) item {
                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        SettingsCaption(stringResource(R.string.settings_pairing_other_ways_command))
                        JCodeBlock(PairCommand, small = true)
                    }
                }
            }
        }
        notice?.let { id -> item { JNotice(stringResource(id), Modifier.padding(top = Tokens.Space.m)) } }
        item { JTextButton(stringResource(R.string.settings_pairing_saved_title), { info = true }, Modifier.padding(top = Tokens.Space.m)) }
        item { footer() }
    }
    if (info) JDialog(onDismiss = { info = false }, title = stringResource(R.string.settings_pairing_saved_title),
        text = stringResource(R.string.settings_pairing_saved_body),
        confirm = stringResource(R.string.settings_pairing_got_it) to { info = false }, dismiss = null)
}

@Composable
private fun PairingActions(content: @Composable ColumnScope.() -> Unit) {
    Column(Modifier.fillMaxWidth().padding(top = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m), content = content)
}

// The glass bar already insets its title by 10 dp. Its empty leading slot adds the remaining 6 dp.
internal val pairingTitleInset: @Composable () -> Unit = {
    if (PersonaTheme.style == AppStyle.Glass) Spacer(Modifier.width(Tokens.Space.sm))
}

@Composable
private fun PairingListPage(page: PairingPage, onBack: (() -> Unit)?, state: LazyListState, content: LazyListScope.() -> Unit) {
    val title = page.title
    if (title != null) {
        CompositionLocalProvider(
            LocalRootBack provides null,
            LocalRootLeading provides pairingTitleInset.takeIf { page == PairingPage.Connecting },
            LocalHomeChrome provides null,
        ) {
            JListPage(stringResource(title), onBack = onBack, state = state, largeTitle = false, content = content)
        }
    } else {
        // The welcome page has no floating controls. Reserve the status inset without masking the welcome heading.
        JPage(title = "", topBar = { Spacer(Modifier.statusBarsPadding()) }, edges = false) { insets ->
            val pad = pagePadding
            LazyColumn(
                Modifier.fillMaxSize(), state = state,
                contentPadding = PaddingValues(start = pad, end = pad, top = insets.calculateTopPadding() + Tokens.Space.xs,
                    bottom = insets.calculateBottomPadding() + Tokens.Space.section),
                content = content,
            )
        }
    }
}

private fun LazyListScope.pairingInstructions(@StringRes description: Int) {
    item("instructions") {
        Text(stringResource(description), modifier = Modifier.padding(vertical = Tokens.Space.l),
            style = PersonaTheme.type.body, color = PersonaTheme.palette.ink2)
    }
}

private fun LazyListScope.pairingDetails(request: PairingRequest, resources: Resources) {
    val missing = resources.getString(R.string.settings_pairing_detail_missing)
    val details = listOf(resources.getString(R.string.settings_pairing_detail_address) to request.url,
        resources.getString(R.string.settings_pairing_detail_transport) to resources.getString(
            if (request.url.startsWith("https://")) R.string.settings_pairing_detail_https else R.string.settings_pairing_detail_http),
        resources.getString(R.string.settings_pairing_detail_identity) to (request.hub ?: missing),
        resources.getString(R.string.settings_pairing_detail_fingerprint) to (request.fp ?: missing)) +
        listOfNotNull(request.tls?.let { resources.getString(R.string.settings_pairing_detail_tls_port) to it.toString() })
    itemsIndexed(details) { index, (label, value) ->
        JGroupItem(GroupPosition.of(index, details.size)) { PairingDetail(label, value) }
    }
    if (request.url.startsWith("http://")) item {
        JNotice(stringResource(if (request.fp != null) R.string.settings_pairing_fingerprint_probe
            else R.string.settings_pairing_http_warning), Modifier.padding(top = Tokens.Space.m))
    }
    if (request.url.startsWith("https://") && request.fp == null) item {
        JNotice(stringResource(R.string.settings_pairing_https_no_fingerprint), Modifier.padding(top = Tokens.Space.m))
    }
    item { JSectionHeader(stringResource(R.string.settings_pairing_candidates)) }
    val addresses = (listOf(request.url) + request.alt).distinct()
    itemsIndexed(addresses) { index, url ->
        JGroupItem(GroupPosition.of(index, addresses.size)) { PairingDetail(if (index == 0) stringResource(R.string.settings_pairing_primary) else stringResource(R.string.settings_pairing_alternate, index), url) }
    }
    item { SettingsCaption(stringResource(R.string.settings_pairing_identity_note), Modifier.padding(vertical = Tokens.Space.s)) }
}

@Composable
private fun PairingDetail(label: String, value: String) {
    Column(Modifier.fillMaxWidth().padding(Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
        SettingsCaption(label)
        SelectionContainer { Text(value, style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink) }
    }
}

/** When a pairing step was reported, in the app's language: 14:30:05 · 2:30:05 PM. */
internal fun pairingStepTime(at: java.time.Instant, locale: java.util.Locale, zone: java.time.ZoneId = java.time.ZoneId.systemDefault()): String =
    LocalizedFormats.format(at, LocalizedFormats.TIME_SECONDS, locale, zone)

/** The step names of [pairingSteps], as string resources. */
@StringRes
internal fun PairingStage.title(): Int = when (this) {
    PairingStage.Parse -> R.string.settings_pairing_stage_parse
    PairingStage.Probe -> R.string.settings_pairing_stage_probe
    PairingStage.VerifyIdentity -> R.string.settings_pairing_stage_verify
    PairingStage.ExchangeCredentials -> R.string.settings_pairing_stage_exchange
    PairingStage.LoadMachines -> R.string.settings_pairing_stage_machines
    PairingStage.Complete -> R.string.settings_pairing_stage_complete
}

/** [title] names each stage (its [PairingStage.title] resource in the app). */
internal fun pairingSteps(progress: List<PairingProgress>, running: Boolean, failed: Boolean, title: (PairingStage) -> String): List<ProgressStep> {
    val current = progress.lastOrNull()?.stage
    return PairingStage.entries.map { stage ->
        val reported = progress.any { it.stage == stage }
        val state = when {
            !reported -> StepState.Pending
            stage == current && failed -> StepState.Failed
            stage == current && running -> StepState.Active
            stage == PairingStage.Complete && current != PairingStage.Complete -> StepState.Pending
            else -> StepState.Complete
        }
        ProgressStep(title(stage), state)
    }
}

private fun LazyListScope.pairingProgress(progress: List<PairingProgress>, running: Boolean, failed: Boolean, resources: Resources) {
    val steps = pairingSteps(progress, running, failed) { resources.getString(it.title()) }
    item { JCard { StepProgress(steps) } }
    PairingStage.entries.forEachIndexed { index, stage ->
        val reported = progress.firstOrNull { it.stage == stage }
        val step = steps[index]
        item {
            JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.m)) {
                JRow(step.title, subtitle = step.state.label, trailing = {
                    Text(reported?.timestamp ?: "—", style = PersonaTheme.type.micro, color = PersonaTheme.palette.ink3)
                })
            }
        }
        itemsIndexed(reported?.candidates.orEmpty()) { candidateIndex, candidate ->
            JGroupItem(GroupPosition.of(candidateIndex, reported?.candidates.orEmpty().size)) {
                PairingDetail(candidate.kind, candidate.url)
                SettingsCaption(candidate.result.text(), Modifier.padding(horizontal = Tokens.Space.m).padding(bottom = Tokens.Space.m))
            }
        }
    }
}

/** The hub's own command for a new pairing code, under "Other ways" only. */
private const val PairCommand = "personad hub pair"
