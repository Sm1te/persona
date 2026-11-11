package persona.feature.settings

import android.provider.Settings
import android.Manifest
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LifecycleEventEffect
import persona.core.contracts.api.*
import persona.core.navigation.LocalDetailKey
import persona.core.navigation.SettingsPage
import persona.core.navigation.SettingsPageRoute
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

@Composable
internal fun Action(label: String, enabled: Boolean = true, onClick: () -> Unit) {
    JButton(label, onClick, kind = JButtonKind.Secondary, size = JButtonSize.Small, enabled = enabled)
}

@Composable
internal fun SettingsCaption(text: String, modifier: Modifier = Modifier) {
    Text(text, modifier, style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink3)
}

@Composable
fun SettingsScreen(
    services: SettingsServices,
    initialPage: String = "settings",
    onPhoneData: (() -> Unit)? = null,
    onDemo: (() -> Unit)? = null,
    onExitDemo: (() -> Unit)? = null,
    onOpen: ((SettingsPage) -> Unit)? = null,
    onBack: (() -> Unit)? = null,
    /** Opens a page by key (one AI use of "Model configuration"); without it such pages open inside this one. */
    onNavigate: ((androidx.navigation3.runtime.NavKey) -> Unit)? = null,
    onClose: (() -> Unit)? = null,
) {
    SettingsContent(services.bound(), initialPage, onPhoneData, onDemo, onExitDemo, onOpen, onBack, onNavigate, onClose)
}

@Composable
private fun SettingsContent(
    services: SettingsServices,
    initialPage: String,
    onPhoneData: (() -> Unit)?,
    onDemo: (() -> Unit)?,
    onExitDemo: (() -> Unit)?,
    onOpen: ((SettingsPage) -> Unit)?,
    onBack: (() -> Unit)?,
    onNavigate: ((androidx.navigation3.runtime.NavKey) -> Unit)?,
    onClose: (() -> Unit)?,
) {
    var page by rememberSaveable(initialPage) { mutableStateOf(settingsPageFromInitialPage(initialPage)?.toInitialPage() ?: initialPage) }
    var purposeManager by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var purpose by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var extension by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var extensionManager by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var connectorManager by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var channel by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var manager by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    /** `ssh:<hub id>` or `proxy:<hub id>`: a hub's tunnel page under Settings › Hubs. */
    var tunnelPage by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var pairingDiagnostics by rememberSaveable(initialPage) { mutableStateOf(false) }
    // Model providers's own steps when they open in place: one provider, adding one, importing from ~/.zshrc.
    var provider by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    var providerStep by rememberSaveable(initialPage) { mutableStateOf<String?>(null) }
    // The first group's pages read and write the current assistant's settings alone (docs/assistant/managers.md "Clients").
    val assistant = currentAssistant()
    val mine = services.forCurrentAssistant()
    // The assistant the first group's pages are of, for the pages they open (null: a hub without a roster, the default's).
    val owner = assistant.takeIf { it.roster != null }?.id
    // A switch while one of its pages is open here goes back to the list: no page shows one assistant's settings once
    // another is current (the shell closes such pages it opened itself).
    var shownFor by rememberSaveable(initialPage) { mutableStateOf(assistant.id) }
    LaunchedEffect(assistant.id) {
        if (shownFor == assistant.id) return@LaunchedEffect
        shownFor = assistant.id
        if (initialPage == "settings" && isAssistantSettingsPage(page)) {
            page = "settings"; purpose = null; purposeManager = null; extension = null; extensionManager = null
            connectorManager = null; channel = null; provider = null; providerStep = null
        }
    }
    val desktops = services.desktops?.collectAsState()?.value.orEmpty()
    val context = LocalContext.current
    // Read here: the settings list below is a LazyColumn builder, where composition locals cannot be read.
    val appStyleStore = LocalAppStyleStore.current
    val scope = rememberCoroutineScope()
    val connection by services.connection.state.collectAsState()
    val machines by services.sessions.machines.collectAsState()
    val paired by services.pairing.isPaired.collectAsState()
    val deviceController = remember(services) { DeviceSettingsState(services.deviceSettings, services.disconnect) }
    val deviceState by deviceController.state.collectAsState()
    // The signed-in Mac's row says "Mute phone" when it is on: read this device's preferences once a desktop shows.
    LaunchedEffect(desktops.isNotEmpty(), page) {
        if (desktops.isNotEmpty() && page == "settings" && paired && services.deviceSettings != null && deviceState.preferences == null) deviceController.loadPreferences()
    }
    var confirmDisconnect by remember { mutableStateOf(false) }
    val updateController = rememberAppUpdateState(services)
    val updateStatus by updateController.state.collectAsState()
    val update = updateStatus.latest
    val updateMessage = updateStatus.message
    val busy = updateStatus.checking
    val installing = updateStatus.installing
    fun checkUpdate() { scope.launch { updateController.check(manual = true) } }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) {
        if (!services.demo) scope.launch { updateController.check() }
    }
    val extensionService = mine.extensions.takeUnless { services.demo }
    val extensionsAvailable by remember(extensionService) { extensionService?.available ?: kotlinx.coroutines.flow.flowOf(false) }.collectAsState(false)
    // Each assistant's own: a switch never shows the last one's count, not even while the new one's is read (or fails).
    var extensionSummary by remember(assistant.id) { mutableStateOf<String?>(null) }
    val connectorsService = mine.connectors.takeUnless { services.demo }
    var mailAccounts by remember(assistant.id) { mutableStateOf<List<MailAccount>?>(null) }
    var larkStatus by remember(assistant.id) { mutableStateOf<LarkStatus?>(null) }
    val larkChats = LocalLarkFeatures.current.chats
    val translationPrefs = services.translation?.prefs?.collectAsState()?.value
    val translationValue = when {
        services.demo || translationPrefs == null -> null
        translationPrefs.enabled -> TranslationPrefs.languageName(translationPrefs.target)
        else -> stringResource(R.string.settings_off)
    }
    var hubConnectors by remember(assistant.id) { mutableStateOf<List<HubConnector>>(emptyList()) }
    val phoneOn = remember(page) {
        listOf(
            Manifest.permission.READ_CONTACTS,
            Manifest.permission.READ_CALENDAR,
            Manifest.permission.ACCESS_FINE_LOCATION,
        ).count {
            androidx.core.content.ContextCompat.checkSelfPermission(context, it) == android.content.pm.PackageManager.PERMISSION_GRANTED
        }
    }
    // The row's count, read again whenever the settings list is shown or the hub reconnects.
    LaunchedEffect(extensionService, extensionsAvailable, page, connection, assistant.id) {
        if (extensionService == null || !extensionsAvailable || page != "settings" || connection != ConnectionState.Online) return@LaunchedEffect
        extensionSummary = try { ExtensionText.listSummary(context.resources, extensionService.extensions().filter { assistant.owns(it.manager) }) }
        catch (error: CancellationException) { throw error } catch (_: Exception) { extensionSummary }
    }
    // Extensions' UI slots (docs/extensions/extensions.md "UI slots"): read again whenever the settings list shows.
    val slots = services.extensionSlots.takeUnless { services.demo }
    val slotsState = slots?.state?.collectAsState()?.value ?: ExtensionSlotsState()
    val slotCards = if (page == "settings" && onNavigate != null) rememberExtensionCards(slots) { onNavigate(it) } else emptyList()
    LaunchedEffect(slots, page, connection) {
        if (slots != null && page == "settings" && connection == ConnectionState.Online) slots.load()
    }
    val channelService = mine.channels.takeUnless { services.demo }
    val channelsAvailable by remember(channelService) { channelService?.available ?: kotlinx.coroutines.flow.flowOf(false) }.collectAsState(false)
    var channelList by remember(assistant.id) { mutableStateOf<List<HubChannel>?>(null) }
    // Like "Extensions"': read again whenever the settings list is shown or the hub reconnects.
    LaunchedEffect(channelService, channelsAvailable, page, connection, assistant.id) {
        if (channelService == null || !channelsAvailable || page != "settings" || connection != ConnectionState.Online) return@LaunchedEffect
        channelList = try { channelService.channels() }
        catch (error: CancellationException) { throw error } catch (_: Exception) { channelList }
    }
    // Settings › Hub version's row value (iOS `hubSummary`): read when the list shows or the hub reconnects, then each push.
    val hubUpdates = services.hubUpdates.takeUnless { services.demo }
    var hubUpdateStatus by remember(hubUpdates) { mutableStateOf<persona.core.contracts.api.HubUpdateStatus?>(null) }
    LaunchedEffect(hubUpdates, page, connection) {
        if (hubUpdates == null || page != "settings" || connection != ConnectionState.Online) return@LaunchedEffect
        hubUpdateStatus = try { hubUpdates.status() }
        catch (error: CancellationException) { throw error } catch (_: Exception) { hubUpdateStatus }
    }
    LaunchedEffect(hubUpdates) { hubUpdates?.events?.collect { hubUpdateStatus = it } }
    LaunchedEffect(connectorsService, page, connection, larkChats, assistant.id) {
        if (connectorsService == null || page != "connectors" || connection != ConnectionState.Online) return@LaunchedEffect
        try {
            mailAccounts = runCatching { connectorsService.mailAccounts() }.getOrNull()
            // Lark's status is the Lark extension's (`/api/v1/lark/status`): only asked while the hub has it.
            larkStatus = if (larkChats) runCatching { connectorsService.larkStatus() }.getOrNull() else null
            // The current assistant's alone: a merged list (a hub that answers every manager's) is filtered by each one's stamp.
            hubConnectors = runCatching { connectorsService.connectors(all = true) }.getOrDefault(emptyList()).filter { assistant.owns(it.manager) }
        } catch (_: Exception) {}
    }
    val back: () -> Unit = onBack ?: {
        page = when {
            page == "mail-add" -> "mail"
            page in setOf("sent", "connectors/sent", "phone", "mail", "feishu", "feishu-bot") || page.startsWith("connector/") -> "connectors"
            else -> "settings"
        }
    }
    BackHandler(page != "settings" && onBack == null, onBack = back)
    val detailKey = LocalDetailKey.current
    fun open(destination: SettingsPage) {
        if (onOpen != null) onOpen(destination) else page = destination.toInitialPage()
    }
    val deviceFeedback: @Composable () -> Unit = {
        deviceState.message?.let { JNotice(it.text(), status = Status.NeedsYou) }
        if (deviceState.needsCleanup) Action(stringResource(R.string.settings_retry_cleanup), enabled = !deviceState.busy) {
            scope.launch { deviceController.disconnect() }
        }
    }
    val connectorItems: LazyListScope.() -> Unit = {
        val connectorRows = connectorRows(hubConnectors, lark = larkChats)
        fun connectorPosition(row: ConnectorRow) = GroupPosition.of(connectorRows.indexOf(row), connectorRows.size)
        item {
            JGroupItem(connectorPosition(ConnectorRow.Phone)) {
                JRow(
                    selected = detailKey == SettingsPageRoute(SettingsPage.PhoneData),
                    title = stringResource(R.string.settings_diag_phone),
                    leadingIcon = PhIcons.DeviceMobile,
                    subtitle = stringResource(R.string.settings_row_phone_subtitle),
                    value = ConnectorText.device(phoneOn),
                    onClick = { if (onOpen == null && onPhoneData != null) onPhoneData() else open(SettingsPage.PhoneData) },
                )
            }
        }
        item {
            JGroupItem(connectorPosition(ConnectorRow.Mail)) {
                JRow(
                    selected = detailKey == SettingsPageRoute(SettingsPage.Mail),
                    title = stringResource(R.string.settings_row_mail),
                    leadingIcon = PhIcons.EnvelopeSimple,
                    subtitle = stringResource(R.string.settings_row_mail_subtitle),
                    value = ConnectorText.mail(mailAccounts),
                    onClick = { open(SettingsPage.Mail) },
                )
            }
        }
        if (ConnectorRow.Lark in connectorRows) item {
            JGroupItem(connectorPosition(ConnectorRow.Lark)) {
                JRow(
                    selected = detailKey == SettingsPageRoute(SettingsPage.Feishu),
                    title = stringResource(R.string.settings_row_lark),
                    leadingIcon = PhIcons.ChatCircleDots,
                    subtitle = stringResource(R.string.settings_row_lark_subtitle),
                    value = ConnectorText.lark(larkStatus),
                    onClick = { open(SettingsPage.Feishu) },
                )
            }
        }
        connectorRows.filterIsInstance<ConnectorRow.Hub>().forEach { row ->
            val c = row.connector
            item {
                JGroupItem(connectorPosition(row)) {
                    JRow(
                        title = connectorTitle(c, hubConnectors, LocalManagerRoster.current),
                        leadingIcon = PhIcons.PlugsConnected,
                        subtitle = listOfNotNull(c.account, c.via).joinToString(" · ").ifEmpty { ConnectorText.state(c) },
                        value = ConnectorText.summary(c),
                        onClick = {
                            onNavigate?.invoke(persona.core.navigation.ConnectorRoute(c.id, c.manager)) ?: run {
                                connectorManager = c.manager
                                page = "connector/${c.id}"
                            }
                        },
                    )
                }
            }
        }
        item {
            SettingsCaption(
                stringResource(R.string.settings_connectors_caption),
                Modifier.padding(horizontal = pagePadding, vertical = Tokens.Space.s),
            )
        }
        // The host-owned ledger entry follows every connector, as on the web (#2521).
        if (services.sent != null && !services.demo) item {
            JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.s)) {
                JRow(selected = detailKey == SettingsPageRoute(SettingsPage.Sent), title = stringResource(R.string.settings_sent_title),
                    leadingIcon = PhIcons.PaperPlaneTilt, subtitle = stringResource(R.string.settings_sent_subtitle), onClick = { open(SettingsPage.Sent) })
            }
        }
    }
    // The first group's pages while the current assistant is offline (several assistants): what it has can't be read or
    // changed now, so they say so, as its inbox and to-dos do, rather than look empty or failed (docs/assistant/managers.md).
    if (assistant.offline != null && isAssistantSettingsPage(page)) AssistantOfflinePage(page, assistant, back) else when (page) {
        "connectors" -> JListPage(title = stringResource(R.string.settings_section_connectors), onBack = back, content = connectorItems)
        "pair" -> PairingScreen(services.pairing, services.sessions, onReady = back,
            onDiagnostics = { pairingDiagnostics = true }, diagnostics = services.diagnostics, onBack = back,
            footer = { onDemo?.let { Action(stringResource(R.string.settings_try_demo), onClick = it) }; deviceFeedback() },
            machines = services.machines.takeUnless { services.demo }, attempts = services.pairingAttempts,
            mainReadiness = services.readiness.takeUnless { services.demo }, skipWelcome = true)
        "capsule" -> CapsuleSettingsPage(services.capsule, back)
        // The current assistant's computers alone: their Claude Code / Codex settings.
        "claude" -> ClaudeSettingsPage(services.sessions, services.machines.takeUnless { services.demo }, back, owns = { assistant.ownsOn(it.id, it.manager) })
        "codex" -> ClaudeSettingsPage(services.sessions, services.machines.takeUnless { services.demo }, back, AgentSettingsKind.Codex,
            owns = { assistant.ownsOn(it.id, it.manager) })
        "models" -> purpose?.let { id -> ModelPurposePage(services.models.takeUnless { services.demo }, id, onBack = { purpose = null },
                providers = services.providers.takeUnless { services.demo }, manager = purposeManager) }
            ?: ModelConfigurationPage(services.models.takeUnless { services.demo }, back,
                onPurpose = { id, owner -> onNavigate?.invoke(persona.core.navigation.ModelPurposeRoute(id, owner)) ?: run { purposeManager = owner; purpose = id } },
                providers = services.providers.takeUnless { services.demo })
        "plugins" -> extension?.let { id -> ExtensionPage(services.extensions.takeUnless { services.demo }, id, onBack = { extension = null },
                manager = extensionManager,
                machines = services.sessions.machines,
                launchers = services.providers.takeUnless { services.demo }?.let { p -> { p.launchers() } },
                onUpgrade = onNavigate?.let { navigate -> { market, ext -> navigate(persona.core.navigation.ExtensionCatalogRoute(market, ext, manager = extensionManager)) } }) }
            ?: ExtensionsPage(mine.extensions.takeUnless { services.demo }, back,
                onExtension = { id, manager -> onNavigate?.invoke(persona.core.navigation.ExtensionRoute(id, manager = manager)) ?: run { extension = id; extensionManager = manager } },
                onMarket = onNavigate?.let { navigate -> { navigate(persona.core.navigation.ExtensionMarketRoute) } })
        "channels" -> channel?.let { id -> ChannelPage(channelService, id, onBack = { channel = null }, onOpen = onNavigate) }
            ?: ChannelsPage(channelService, back,
                onChannel = { id -> onNavigate?.invoke(persona.core.navigation.ChannelRoute(id)) ?: run { channel = id } },
                // Installed for the current assistant, whose channels these are.
                onMarketExtension = onNavigate?.let { navigate -> { market, id -> navigate(persona.core.navigation.ExtensionCatalogRoute(market, id, manager = owner)) } })
        "ready", "notify" -> {
            val preferences: LazyListScope.() -> Unit =
                if (page == "notify") NotificationPreferences(deviceController, paired && services.deviceSettings != null,
                    desktops, onDesktops = { open(SettingsPage.Desktops) }) else ({})
            ReadinessScreen(services.sessions, services.diagnostics,
                notificationsOnly = page == "notify", showTitle = page == "notify",
                readiness = services.readiness.takeUnless { services.demo }, onNavigate = onNavigate,
                onDiagnostics = { open(SettingsPage.Diagnostics) }, onDone = back, onBack = back,
                header = preferences, footer = {
                    if (page == "notify") SettingsCaption(stringResource(R.string.settings_notify_system_footer), Modifier.padding(vertical = Tokens.Space.s))
                    deviceFeedback()
                })
        }
        "desktops" -> DesktopsPage(deviceController, services.desktops.takeUnless { services.demo }, paired && services.deviceSettings != null, back)
        "storage" -> StoragePage(back, services.clearCacheFiles, footer = deviceFeedback)
        "providers" -> {
            val providerService = mine.providers.takeUnless { services.demo }
            when {
                providerStep == "add" && providerService != null -> NewProviderPage(providerService, onBack = { providerStep = null }, onSaved = { providerStep = null },
                    readiness = services.readiness, machines = machines)
                providerStep == "import" && providerService != null -> ProviderImportPage(providerService, machines, onBack = { providerStep = null }, onDone = { providerStep = null })
                provider != null -> ProviderDetailPage(providerService, provider!!, machines, onBack = { provider = null })
                else -> ProvidersPage(providerService, machines, back,
                    onOpen = { name -> onNavigate?.invoke(persona.core.navigation.ModelProviderRoute(name)) ?: run { provider = name } },
                    onAdd = { providerStep = "add" }, onImport = { providerStep = "import" },
                    selected = { name -> detailKey == persona.core.navigation.ModelProviderRoute(name) })
            }
        }
        "devices" -> DevicesPage(deviceController, paired && services.deviceSettings != null, back, deviceFeedback,
            connect = services.deviceSettings.takeUnless { services.demo })
        "managers" -> manager?.let { id -> ManagerPage(services.managers.takeUnless { services.demo }, id, onBack = { manager = null },
            devices = services.deviceSettings.takeUnless { services.demo }, machines = machines) }
            ?: ManagersPage(services.managers.takeUnless { services.demo }, back,
                onManager = { id -> onNavigate?.invoke(persona.core.navigation.ManagerRoute(id)) ?: run { manager = id } })
        "sent", "connectors/sent" -> SentAsYouPage(mine.sent.takeUnless { services.demo }, back,
            // A chat opens by its ids in the hub's group, as every other page's link to one does.
            onChat = onNavigate?.let { navigate -> { machine, session -> navigate(persona.core.navigation.ChatRoute(machine, session)) } })
        "hub-update" -> HubUpdatePage(hubUpdates, machines, back, online = connection == ConnectionState.Online, onMachine = onNavigate)
        "hubs" -> tunnelPage?.takeIf { services.tunnels != null }?.let { opened ->
            if (opened.startsWith("access:")) HubAccessPage(services.tunnels!!, opened.substringAfter(':'), onBack = { tunnelPage = null })
            else HubTunnelPage(services.tunnels!!, opened.substringAfter(':'),
                if (opened.startsWith("ssh:")) TunnelKind.Ssh else TunnelKind.Proxy, onBack = { tunnelPage = null })
        } ?: HubsScreen(services.hubs ?: HubGroups.None, services.addHub?.let { { page = "hubs-add" } }, back, deviceFeedback,
            tunnels = services.tunnels, onTunnel = { hubId, kind -> tunnelPage = (if (kind == TunnelKind.Ssh) "ssh:" else "proxy:") + hubId },
            onAccess = { hubId -> tunnelPage = "access:$hubId" })
        // Adding a hub is the pairing page with an adding service; it returns to the hub list.
        "hubs-add" -> PairingScreen(services.addHub ?: services.pairing, services.sessions,
            onReady = { page = "hubs" }, onDiagnostics = { pairingDiagnostics = true }, diagnostics = services.diagnostics,
            onBack = { page = "hubs" }, skipWelcome = true, attempts = services.pairingAttempts)
        "diag" -> DiagnosticsScreen(services, onBack = back, footer = deviceFeedback)
        "style" -> JListPage(title = stringResource(R.string.settings_style_title), onBack = back) {
            item { AppearanceSettings() }
            item { deviceFeedback() }
        }
        "language" -> LanguagePage(onBack = back, footer = deviceFeedback)
        "persona" -> JListPage(title = stringResource(R.string.settings_avatar_title), onBack = back) {
            item { PersonaSettings(demo = services.demo) }
            item { deviceFeedback() }
        }
        "about" -> JListPage(title = stringResource(R.string.settings_about_persona), onBack = back) {
            item { AboutSettingsPage() }
            item { deviceFeedback() }
        }
        "phone" -> JListPage(title = stringResource(R.string.settings_phone_data_title), onBack = back) {
            item { JNotice(stringResource(R.string.settings_phone_data_notice)) }
            item { SettingsCaption(stringResource(R.string.settings_phone_data_caption), Modifier.padding(vertical = Tokens.Space.m)) }
            item { Action(stringResource(R.string.settings_phone_data_manage)) { PhoneReadiness.open(context, listOf(appDetails)) } }
            item { deviceFeedback() }
        }
        "translate" -> TranslationSettingsPage(services.translation.takeUnless { services.demo }, services.models.takeUnless { services.demo },
            services.connectors.takeUnless { services.demo }, onBack = back, pickingAtStart = TranslationPreview.take(),
            onModel = { onNavigate?.invoke(persona.core.navigation.ModelPurposeRoute("translate")) ?: run { purposeManager = null; purpose = "translate"; page = "models" } })
        "triage" -> TriageSettingsPage(mine.triage.takeUnless { services.demo }, mine.models.takeUnless { services.demo }, onBack = back,
            onModel = { onNavigate?.invoke(persona.core.navigation.ModelPurposeRoute("agent.triage", owner)) ?: run {
                purposeManager = owner; purpose = "agent.triage"; page = "models" } })
        "mail" -> MailAccountsPage(connectorsService, onBack = back, onAdd = { page = "mail-add" })
        "feishu-bot" -> FeishuBotPage(mine.feishuBot.takeUnless { services.demo }, back)
        "mail-add" -> AddMailAccountPage(connectorsService, onBack = { page = "mail" }, onAdded = { page = "mail" })
        "feishu" -> FeishuConnectorPage(connectorsService, onBack = back, onAgentModel = {
            // The model of the current assistant's agent there, as the page's connector is that one's.
            onNavigate?.invoke(persona.core.navigation.ModelPurposeRoute("lark.agent", owner)) ?: run { purposeManager = owner; purpose = "lark.agent"; page = "models" }
        })
        // "Messages" has its own page, as on iOS: "Hand to Persona", its steps on the Mac, "Conversations" (MessagesConnector.kt).
        else -> if (page == "connector/imessage") {
            MessagesConnectorPage(connectorsService, onBack = back)
        } else if (page.startsWith("connector/")) {
            HubConnectorPage(connectorsService, connectorId = page.removePrefix("connector/"), onBack = back, manager = connectorManager)
        } else JListPage(title = stringResource(R.string.settings_title), actions = {
            onClose?.let { close -> JIconButton(PhIcons.X, stringResource(persona.core.designsystem.R.string.designsystem_close), close, raised = true) }
        }) {
            if (services.demo) {
                item { JNotice(stringResource(R.string.settings_demo_notice), title = stringResource(R.string.settings_demo_mode)) }
                onExitDemo?.let { exit ->
                    item { JButton(stringResource(R.string.settings_demo_connect), exit, Modifier.fillMaxWidth().padding(top = Tokens.Space.m)) }
                }
            }
            // Three groups, in this order (docs/assistant/managers.md "Clients"): the current assistant's settings, this
            // device's, the hub's. A row of a group is one position in it (its corners), whichever rows the hub has.
            val sw = this
            fun group(rows: List<@Composable (GroupPosition) -> Unit>) = rows.forEachIndexed { index, row ->
                sw.item { row(GroupPosition.of(index, rows.size)) }
            }
            // A row drawn its own way (a switch, a count of its own): typed here so its content is composable.
            fun custom(content: @Composable (GroupPosition) -> Unit) = content
            fun row(destination: SettingsPage, title: Int, icon: persona.core.designsystem.icons.PhIcon, subtitle: Int? = null,
                value: (@Composable () -> String?)? = null): @Composable (GroupPosition) -> Unit = { position ->
                JGroupItem(position) {
                    JRow(selected = detailKey == SettingsPageRoute(destination), title = stringResource(title), leadingIcon = icon,
                        subtitle = subtitle?.let { stringResource(it) }, value = value?.invoke(), onClick = { open(destination) })
                }
            }
            // 1. The current assistant's settings: its head (monogram, "{name}'s settings", Switch to the sidebar's roster).
            item("assistant-head") {
                val switchLabel = stringResource(R.string.settings_group_assistant_switch)
                AssistantSwitcher(services.managers.takeUnless { services.demo }, LocalAssistantWaiting.current,
                    hubOnline = connection == ConnectionState.Online || connection == ConnectionState.Updating,
                    onManage = { open(SettingsPage.Managers) },
                    // No roster (an older hub, demo mode): no assistants to name or switch, so a plain heading and no footer.
                    fallback = { JAssistantSettingsHeader(stringResource(persona.core.designsystem.R.string.designsystem_brand_persona), null, false,
                        switchLabel, onSwitch = null, modifier = Modifier.testTag("settings-assistant-head")) },
                    head = { current, isDefault, _, toggle ->
                        JAssistantSettingsHeader(stringResource(R.string.settings_group_assistant, current.shownName), current, isDefault,
                            switchLabel, onSwitch = toggle)
                    })
            }
            // The extensions' cards and nav pages are the default assistant's (they open by key, so only with a navigation host).
            if (onNavigate != null && (assistant.roster == null || assistant.id == assistant.roster?.defaultManagerId)) extensionSlotItems(slotsState, slotCards,
                selected = { place -> detailKey == persona.core.navigation.ExtensionUiRoute(place.extension, place.id, place.title.ifBlank { place.id }) },
                onOpen = { place -> onNavigate(persona.core.navigation.ExtensionUiRoute(place.extension, place.id, place.title.ifBlank { place.id })) })
            group(listOfNotNull(
                row(SettingsPage.Providers, R.string.settings_providers_title, PhIcons.Cpu, R.string.settings_row_providers_subtitle),
                row(SettingsPage.ModelConfig, R.string.settings_row_models, PhIcons.Brain, R.string.settings_row_models_subtitle),
                // "Assistant profile" is a page of the Persona tab: only with a navigation host.
                if (onNavigate == null) null else custom { position ->
                    JGroupItem(position) {
                        JRow(selected = detailKey == persona.core.navigation.AssistantSettingsRoute, title = stringResource(R.string.settings_row_assistant),
                            leadingIcon = PhIcons.Sparkle, subtitle = stringResource(R.string.settings_row_assistant_subtitle),
                            onClick = { onNavigate.invoke(persona.core.navigation.AssistantSettingsRoute) })
                    }
                },
                row(SettingsPage.Triage, R.string.settings_triage_title, PhIcons.ListChecks, R.string.settings_row_triage_subtitle),
                custom { position ->
                    JGroupItem(position) {
                        JRow(selected = detailKey == SettingsPageRoute(SettingsPage.Connectors) || detailKey == SettingsPageRoute(SettingsPage.Sent),
                            title = stringResource(R.string.settings_section_connectors), leadingIcon = PhIcons.PlugsConnected,
                            subtitle = stringResource(R.string.settings_connectors_caption), onClick = { open(SettingsPage.Connectors) })
                    }
                },
                // Extension management and conversation channels are independent hub capabilities.
                if (!extensionsAvailable) null else row(SettingsPage.Extensions, R.string.settings_section_extensions, PhIcons.PuzzlePiece,
                    R.string.settings_row_extensions_subtitle) { extensionSummary },
                if (!channelsAvailable) null else custom { position ->
                    ChannelsSettingsRow(channelList, position, selected = detailKey == SettingsPageRoute(SettingsPage.Channels),
                        onClick = { open(SettingsPage.Channels) })
                },
                row(SettingsPage.ClaudeCode, R.string.settings_row_claude, PhIcons.TerminalWindow, R.string.settings_row_claude_subtitle),
                row(SettingsPage.Codex, R.string.settings_row_codex, PhIcons.Code, R.string.settings_row_codex_subtitle),
            ))
            // Its footer names the assistant its head does; without a roster there is none to name.
            val named = assistant.manager?.takeIf { services.managers != null && !services.demo && assistant.roster?.installed?.isNotEmpty() == true }
            if (named != null) item("assistant-footer") {
                SettingsCaption(stringResource(R.string.settings_group_assistant_footer, named.shownName),
                    Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
            }
            // 2. This device's.
            item("device-head") { JSectionHeader(stringResource(R.string.settings_group_device)) }
            val desktopRow = desktops.isNotEmpty() && !services.demo
            group(listOfNotNull(
                row(SettingsPage.Appearance, R.string.settings_style_title, PhIcons.SquaresFour) { PersonaTheme.style.label },
                custom { position ->
                    JGroupItem(position) { LanguageRow(selected = detailKey == SettingsPageRoute(SettingsPage.Language), onClick = { open(SettingsPage.Language) }) }
                },
                // Inbox chats open natively (left / right bubbles, reply composer) unless switched to the extension's page.
                appStyleStore?.let { store -> custom { position ->
                    JGroupItem(position) {
                        val nativeInboxChats by store.nativeInboxChats.collectAsState()
                        JSwitchRow(stringResource(R.string.settings_update_v7_native_title), nativeInboxChats, store::setNativeInboxChats,
                            subtitle = stringResource(R.string.settings_update_v7_native_subtitle))
                    }
                } },
                row(SettingsPage.Notifications, R.string.settings_ready_notifications, PhIcons.BellSimple, R.string.settings_row_notifications_subtitle),
                services.capsule?.let { capsule -> custom { position ->
                    JGroupItem(position) {
                        val mode by capsule.mode.collectAsState()
                        JRow(title = stringResource(R.string.settings_capsule_title), leadingIcon = PhIcons.ChatCircle, value = CapsuleSettingsText.label(mode),
                            subtitle = stringResource(R.string.settings_row_capsule_subtitle), selected = detailKey == SettingsPageRoute(SettingsPage.Capsule),
                            onClick = { open(SettingsPage.Capsule) })
                    }
                } },
                row(SettingsPage.Readiness, R.string.settings_row_background, PhIcons.BatteryCharging, R.string.settings_row_background_subtitle),
                // "Persona on Mac is signed in" while a desktop app is connected (iOS shows it over the Agents list).
                if (!desktopRow) null else custom { position ->
                    DesktopPresenceRow(desktops, deviceState.preferences?.muteWhileDesktop == true,
                        selected = detailKey == SettingsPageRoute(SettingsPage.Desktops), position, onClick = { open(SettingsPage.Desktops) })
                },
                row(SettingsPage.Translation, R.string.settings_translation_title, PhIcons.Translate, R.string.settings_row_translation_subtitle) { translationValue },
                custom { position ->
                    JGroupItem(position) { StorageRow(selected = detailKey == SettingsPageRoute(SettingsPage.Storage), onClick = { open(SettingsPage.Storage) }) }
                },
                row(SettingsPage.About, R.string.settings_section_about, PhIcons.Info, R.string.settings_row_about_subtitle),
                custom { position ->
                    JGroupItem(position) {
                        JRow(stringResource(R.string.settings_update_check), leadingIcon = PhIcons.ArrowClockwise, value = when {
                            busy -> stringResource(R.string.settings_update_checking)
                            installing -> stringResource(R.string.settings_update_installing)
                            update != null -> stringResource(R.string.settings_update_found)
                            services.demo -> stringResource(R.string.settings_demo_mode)
                            else -> updateMessage?.let { stringResource(it).trimEnd('。', '.') } ?: stringResource(R.string.settings_update_check_hint)
                        },
                            enabled = !services.demo && !busy && !installing, onClick = ::checkUpdate)
                    }
                },
            ))
            if (!services.demo) item { AppUpdateRecovery(updateController) }
            if (update != null || updateMessage != null) item {
                JCard(Modifier.padding(top = Tokens.Space.m)) {
                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                        update?.let { latest ->
                            Text(stringResource(R.string.settings_update_new_version, latest.versionName), style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
                            if (latest.releaseNotes.isNotBlank()) Text(latest.releaseNotes, style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
                            AppUpdateInstallButton(updateController)
                        }
                        updateMessage?.let { SettingsCaption(stringResource(it)) }
                    }
                }
            }
            // 3. The hub's: how this device reaches it, then its rows.
            item("hub-head") { JSectionHeader(stringResource(R.string.settings_group_hub)) }
            item {
                HubCard(name = stringResource(persona.core.designsystem.R.string.designsystem_brand_persona), connected = paired && connection == ConnectionState.Online,
                    statusText = if (paired) connection.label() else stringResource(R.string.settings_not_paired),
                    facts = if (paired) machines.count { it.online }.let { online ->
                        listOf(stringResource(R.string.settings_hub_computers_online) to pluralStringResource(R.plurals.settings_hub_computers_count, online, online))
                    } else emptyList(),
                    busy = connection in setOf(ConnectionState.Connecting, ConnectionState.Updating),
                    modifier = Modifier.testTag("settings-hub-card").padding(bottom = Tokens.Space.m), onClick = { open(SettingsPage.Diagnostics) })
            }
            if (paired) item {
                JGroupItem(GroupPosition.Single, Modifier.padding(bottom = Tokens.Space.m)) {
                    JRow {
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(Tokens.Space.xxs)) {
                            Text(stringResource(R.string.settings_hub_address), style = PersonaTheme.type.body, color = PersonaTheme.palette.ink)
                            SelectionContainer(Modifier.testTag("settings-hub-address-selection")) {
                                Text(services.hubAddress ?: stringResource(R.string.settings_not_provided), Modifier.fillMaxWidth(),
                                    style = PersonaTheme.type.callout, color = PersonaTheme.palette.ink2)
                            }
                        }
                    }
                }
            }
            // The hub list is the one row a single hub group adds to settings (docs/android/android.md "Multiple hubs").
            // Hub version (docs/operations/hub-update.md "In the app") follows it while there is a real hub (not demo mode).
            group(listOfNotNull(
                if (services.hubs == null) null else row(SettingsPage.Hubs, R.string.settings_row_hubs, PhIcons.HardDrives, R.string.settings_row_hubs_subtitle),
                if (hubUpdates == null) null else row(SettingsPage.HubUpdate, R.string.settings_row_hub_update, PhIcons.ArrowsClockwise,
                    R.string.settings_row_hub_update_subtitle) { HubUpdateText.summary(hubUpdateStatus) },
                // Assistants (docs/assistant/managers.md): the hub's roster, after its version.
                if (services.managers == null || services.demo) null else custom { position ->
                    JGroupItem(position) {
                        JRow(selected = detailKey == SettingsPageRoute(SettingsPage.Managers) || detailKey is persona.core.navigation.ManagerRoute,
                            title = stringResource(R.string.settings_managers_title), leadingIcon = PhIcons.UserCircle,
                            subtitle = stringResource(R.string.settings_managers_hint), onClick = { open(SettingsPage.Managers) })
                    }
                },
                row(SettingsPage.Pairing, R.string.settings_row_pairing, PhIcons.QrCode, R.string.settings_row_pairing_subtitle),
                // The avatar is the hub's (`/api/v1/looks`: every device shows the one picked, whichever assistant is current).
                row(SettingsPage.Persona, R.string.settings_avatar_title, PhIcons.Robot) {
                    if (LocalPersonaPack.current != null) LocalPersonaName.current else LocalPersonaDesign.current.title
                },
                row(SettingsPage.Devices, R.string.settings_devices_title, PhIcons.DeviceMobile, R.string.settings_row_devices_subtitle),
                row(SettingsPage.Diagnostics, R.string.settings_diag_title, PhIcons.Gauge, R.string.settings_row_diag_subtitle),
            ))
            item("hub-footer") {
                SettingsCaption(stringResource(R.string.settings_group_hub_footer), Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
            }
            if (paired) item {
                JButton(stringResource(R.string.settings_disconnect_forget), { confirmDisconnect = true },
                    Modifier.fillMaxWidth().padding(top = Tokens.Space.section), kind = JButtonKind.Destructive, enabled = !deviceState.busy)
            }
            item { deviceFeedback() }
        }
    }
    // Routed Back leaves a page in one step. Pairing handles its own steps before returning to Settings or Hubs.
    if (onBack != null) BackHandler(page != "settings" && page != "pair" && page != "hubs-add", onBack = onBack)
    // An AI use opened inside "Model configuration" (no navigation host) closes before the page does.
    BackHandler(page == "models" && purpose != null) { purpose = null }
    BackHandler(page == "plugins" && extension != null) { extension = null }
    BackHandler(page == "channels" && channel != null) { channel = null }
    BackHandler(page == "managers" && manager != null) { manager = null }
    BackHandler(page == "providers" && (provider != null || providerStep != null)) { if (providerStep != null) providerStep = null else provider = null }
    BackHandler(page == "hubs" && tunnelPage != null) { tunnelPage = null }
    if (pairingDiagnostics && (page == "pair" || page == "hubs-add")) {
        val close = { pairingDiagnostics = false }
        JFullScreen(onDismiss = close) { DiagnosticsScreen(services, onBack = close, footer = deviceFeedback) }
    }
    if (confirmDisconnect) JDialog(
        onDismiss = { confirmDisconnect = false }, title = stringResource(R.string.settings_disconnect_title),
        text = stringResource(R.string.settings_disconnect_text),
        destructive = true, confirmEnabled = !deviceState.busy,
        confirm = stringResource(R.string.settings_disconnect) to { confirmDisconnect = false; scope.launch { deviceController.disconnect() }; Unit },
    )
}

@Composable
fun ReadinessScreen(
    sessions: SessionsRepository,
    diagnostics: SettingsDiagnostics? = null,
    notificationsOnly: Boolean = false,
    showTitle: Boolean = true,
    onDiagnostics: () -> Unit,
    onDone: () -> Unit,
    onBack: (() -> Unit)? = onDone,
    header: LazyListScope.() -> Unit = {},
    footer: @Composable () -> Unit = {},
    /** The main conversation's steps before the phone's (docs/assistant/onboarding.md 4.5), on hubs that offer them. */
    readiness: AssistantReadinessSource? = null,
    /** Opens a step's page ("Model providers", "Machines"); null during the first checklist, whose buttons then open help only. */
    onNavigate: ((androidx.navigation3.runtime.NavKey) -> Unit)? = null,
    onLater: (() -> Unit)? = null,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val preferences = remember { context.getSharedPreferences("settings-readiness", android.content.Context.MODE_PRIVATE) }
    val readinessOn by remember(readiness) { readiness?.available ?: kotlinx.coroutines.flow.flowOf(false) }.collectAsState(false)
    var main by remember(readiness) { mutableStateOf<AssistantReadiness?>(null) }
    var probing by remember { mutableStateOf(false) }
    var mainHelp by remember { mutableStateOf<ReadinessHelp?>(null) }
    suspend fun loadMain(probe: Boolean = false) {
        val source = readiness ?: return
        if (probe) probing = true
        try { main = source.readiness(probe) }
        catch (error: CancellationException) { throw error }
        catch (_: Exception) { /* An older hub or a moment offline: the phone's items as before. */ }
        finally { if (probe) probing = false }
    }
    fun mainAction(action: ReadinessActionId, step: ReadinessStep) {
        action.help?.let { mainHelp = it; return }
        if (action == ReadinessActionId.TestAssistant) { scope.launch { loadMain(probe = true) }; return }
        val destination = action.settingsDestination(step.launcher ?: main?.launcher, step.machine)
        // The first checklist has no navigation: "Add a computer" says how instead.
        if (destination != null && onNavigate != null) onNavigate(destination)
        else mainHelp = if (action == ReadinessActionId.AddComputer) ReadinessHelp.AddComputer else ReadinessHelp.HowToConnectModel
    }
    val records = remember(context) { ReadinessRecords(context) }
    var system by remember { mutableStateOf<SystemKind?>(null) }
    var items by remember { mutableStateOf(emptyList<ReadyItem>()) }
    var revision by remember { mutableIntStateOf(0) }
    var testMessage by remember { mutableStateOf<Int?>(null) }
    val machines by sessions.machines.collectAsState()
    fun refresh() { scope.launch {
        val detected = system ?: withContext(Dispatchers.IO) { detectedPhoneSystem(context) }
        system = detected
        items = PhoneReadiness.items(context, detected)
        if (detected.name == PhoneReadiness.HyperOS) { delay(1_000); items = PhoneReadiness.items(context, detected) }
    } }
    val machinesSeen = machines.joinToString(",") { "${it.id}:${it.online}" }
    LaunchedEffect(readinessOn, machinesSeen) { if (readinessOn && !notificationsOnly) loadMain() }
    val notifications = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { refresh() }
    val localNetwork = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { refresh() }
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { refresh(); if (readinessOn && !notificationsOnly) scope.launch { loadMain() } }
    LaunchedEffect(Unit) {
        refresh()
        try { sessions.refresh() } catch (error: CancellationException) { throw error } catch (_: Exception) { }
    }
    fun finish(later: Boolean) {
        items.filter { !notificationsOnly || it.id == "notifications" || it.id.startsWith("channel") }.forEach { item ->
            val decision = when {
                item.satisfied == true -> "verified"
                item.satisfied == null && preferences.getBoolean(item.id, false) -> "confirmed"
                else -> "skipped"
            }
            records.record(item.id, decision)
        }
        if (!notificationsOnly) {
            listOf("machines", "lock-test", "cellular-test").forEach { id ->
                if (records.decision(id) == null) records.record(id, "skipped")
            }
            records.pending(false)
            // "Later" for the main conversation too: the hub stops offering the whole list (its card still shows).
            // onDone() leaves the page and cancels its scope, so the request starts now and is not cancelled with it.
            if (later && main?.ready == false && main?.dismissed == false) readiness?.let { source ->
                scope.launch(start = kotlinx.coroutines.CoroutineStart.UNDISPATCHED) {
                    withContext(kotlinx.coroutines.NonCancellable) { try { source.dismiss(true) } catch (_: Exception) { } }
                }
            }
        }
        onDone()
    }
    val title = stringResource(if (notificationsOnly) R.string.settings_ready_notifications else R.string.settings_ready_title)
    val checklist: LazyListScope.() -> Unit = {
        header()
        val mainSteps = main?.takeIf { readinessOn && !notificationsOnly }
        if (mainSteps != null) {
            item("main-header") { JSectionHeader(stringResource(R.string.settings_ready_main_header)) }
            item("main-steps") {
                MainConversationSteps(mainSteps, probing, ::mainAction,
                    // Without a page to go to, "Confirm computer" has nothing to open; "Connect a model" opens how to.
                    allowed = { onNavigate != null || it != ReadinessActionId.ConfirmComputer })
            }
            item("phone-header") { JSectionHeader(stringResource(R.string.settings_ready_phone_header)) }
        }
        if (notificationsOnly) item { JSectionHeader(stringResource(R.string.settings_ready_channels_header)) }
        item {
            Column(Modifier.padding(vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                SettingsCaption(system?.let { stringResource(R.string.settings_ready_system, PhoneReadiness.systemName(context, it.name), it.version) }
                    ?: stringResource(R.string.settings_ready_detecting))
                SettingsCaption(stringResource(R.string.settings_ready_about))
            }
        }
        items(items.filter { !notificationsOnly || it.id == "notifications" || it.id.startsWith("channel") }, key = { it.id }) { item ->
            val skipped = remember(item.id, revision) { records.skipped(item.id) }
            val confirmed = remember(item.id, revision) { preferences.getBoolean(item.id, false) }
            val status = when (item.satisfied) { true -> Status.Unread; false -> Status.NeedsYou; null -> Status.Unknown }
            JCard(Modifier.padding(bottom = Tokens.Space.m), status = status) {
                Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    Text(item.title, style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
                    StatusLabel(status, text = stringResource(when (item.satisfied) {
                        true -> R.string.settings_ready_satisfied
                        false -> R.string.settings_ready_needs_setup
                        null -> if (confirmed) R.string.settings_ready_confirmed else R.string.settings_ready_needs_confirm
                    }))
                    if (skipped && item.satisfied != true && !confirmed) SettingsCaption(stringResource(R.string.settings_ready_skipped_note))
                    SettingsCaption(item.detail)
                    FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        Action(stringResource(R.string.settings_ready_open)) {
                            when {
                                item.id == "notifications" && Build.VERSION.SDK_INT >= 33 &&
                                    !preferences.getBoolean("notification-requested", false) -> {
                                    preferences.edit().putBoolean("notification-requested", true).apply()
                                    notifications.launch(Manifest.permission.POST_NOTIFICATIONS)
                                }
                                item.id == "local-network" && PairingLocalNetwork.missing(context) &&
                                    !preferences.getBoolean("local-network-requested", false) -> {
                                    preferences.edit().putBoolean("local-network-requested", true).apply()
                                    localNetwork.launch(PairingLocalNetwork.Permission)
                                }
                                else -> if (!PhoneReadiness.open(context, item.targets)) {
                                    testMessage = R.string.settings_ready_open_failed
                                }
                            }
                        }
                        if (item.satisfied == null) Action(stringResource(if (confirmed) R.string.settings_ready_undo_confirm else R.string.settings_ready_mark_done)) {
                            preferences.edit().putBoolean(item.id, !confirmed).apply()
                            records.record(item.id, if (confirmed) "pending" else "confirmed"); revision++
                        }
                        if (item.satisfied != true && !confirmed) Action(stringResource(if (skipped) R.string.settings_ready_unskip else R.string.settings_ready_skip)) {
                            records.record(item.id, if (skipped) "pending" else "skipped"); revision++
                        }
                    }
                }
            }
        }
        if (!notificationsOnly) {
            // The hub's computer step says it for the beginner; the per-machine versions stay for older hubs.
            if (mainSteps == null) {
                item { JSectionHeader(stringResource(R.string.settings_section_computers), count = machines.size) }
                if (machines.isEmpty()) item { JEmptyState(stringResource(R.string.settings_ready_no_machines), stringResource(R.string.settings_ready_no_machines_detail)) }
                itemsIndexed(machines) { index, machine ->
                    JGroupItem(GroupPosition.of(index, machines.size)) {
                        val missing = stringResource(R.string.settings_not_provided)
                        JRow(machine.displayName, value = stringResource(if (machine.online) R.string.settings_online else R.string.settings_offline))
                        JRow(stringResource(R.string.settings_ready_row_persona_version), value = machine.nodeVersion ?: missing)
                        JRow(stringResource(R.string.settings_ready_row_terminal), value = machine.herdrVersion ?: missing)
                        JRow("Claude", value = machine.claudeVersion ?: missing)
                        if (machine.nodeMismatch == true) SettingsCaption(stringResource(R.string.settings_ready_node_mismatch), Modifier.padding(Tokens.Space.m))
                    }
                }
                item { Action(stringResource(R.string.settings_ready_skip_machines)) { records.record("machines", "skipped"); revision++; testMessage = R.string.settings_ready_skipped_machines } }
            }
            item { LockScreenTestSection(diagnostics, onCheckSettings = ::refresh) }
            item { Action(stringResource(R.string.settings_ready_skip_lock)) { records.record("lock-test", "skipped"); revision++; testMessage = R.string.settings_ready_skipped_lock } }
            item {
                JCard(Modifier.padding(vertical = Tokens.Space.m), status = Status.Unknown) {
                    Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                        Text(stringResource(R.string.settings_ready_cellular), style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink)
                        SettingsCaption(stringResource(R.string.settings_ready_cellular_about))
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                            Action(stringResource(R.string.settings_ready_cellular_works)) { records.record("cellular-test", "confirmed"); revision++; preferences.edit().putString("cellular-test", CellularTest.Confirmed).apply(); testMessage = R.string.settings_ready_cellular_recorded }
                            Action(stringResource(R.string.settings_ready_skip)) { records.record("cellular-test", "skipped"); revision++; testMessage = R.string.settings_ready_skipped_cellular }
                        }
                    }
                }
            }
            items(listOf("machines" to R.string.settings_ready_machines_check, "lock-test" to R.string.settings_lock_test_title, "cellular-test" to R.string.settings_ready_cellular_test)) { (id, title) ->
                val skipped = remember(id, revision) { records.skipped(id) }
                if (skipped) SettingsCaption(stringResource(R.string.settings_ready_skipped_item, stringResource(title)))
            }
            item { Action(stringResource(R.string.settings_diag_title), onClick = onDiagnostics) }
        }
        item {
            Column(Modifier.padding(vertical = Tokens.Space.m), verticalArrangement = Arrangement.spacedBy(Tokens.Space.m)) {
                testMessage?.let { JNotice(stringResource(it)) }
                SettingsCaption(stringResource(R.string.settings_ready_skip_note))
                FlowRow(horizontalArrangement = Arrangement.spacedBy(Tokens.Space.s), verticalArrangement = Arrangement.spacedBy(Tokens.Space.s)) {
                    Action(stringResource(R.string.settings_ready_skip_rest), enabled = items.isNotEmpty(), onClick = { finish(later = true) })
                    JButton(stringResource(R.string.settings_done), { finish(later = false) }, size = JButtonSize.Small, enabled = items.isNotEmpty())
                }
                footer()
            }
        }
    }
    if (onLater == null) {
        JListPage(title, onBack = onBack, largeTitle = showTitle && onBack == null,
            scrollUnderBottom = notificationsOnly, content = checklist)
    } else CompositionLocalProvider(
        LocalRootBack provides null, LocalRootLeading provides pairingTitleInset, LocalHomeChrome provides null,
    ) {
        JListPage(title, largeTitle = false, actions = { JTextButton(stringResource(R.string.settings_ready_set_up_later), onLater) },
            scrollUnderBottom = false, content = checklist)
    }
    mainHelp?.let { help ->
        ReadinessHelpDialog(help, main?.machine?.name, onDismiss = { mainHelp = null }, onAction = { action ->
            main?.steps?.firstOrNull { it.primaryAction() == action }?.let { mainAction(action, it) }
                ?: run { if (action == ReadinessActionId.ConnectModel) mainAction(action, ReadinessStep(ReadinessStep.MODEL)) }
        }, canConnectModel = onNavigate != null)
    }
}

/** Reuses the settings checklist for the first successful pairing. */
@Composable
fun OnboardingScreen(services: SettingsServices, onDone: () -> Unit) {
    OnboardingContent(services.bound(), onDone)
}

@Composable
private fun OnboardingContent(services: SettingsServices, onDone: () -> Unit) {
    val context = LocalContext.current
    val records = remember(context) { ReadinessRecords(context) }
    // The app stopped during the engine step after pairing: that step comes back before the checklist.
    var engine by rememberSaveable { mutableStateOf(records.enginePending()?.takeIf { services.machines != null }) }
    engine?.let { machine ->
        EngineSelectionScreen(machine, services.sessions, services.machines, pairing = true, onDone = {
            records.enginePending(null); engine = null
        })
        return
    }
    var diagnostics by rememberSaveable { mutableStateOf(false) }
    BackHandler(onBack = onDone)
    ReadinessScreen(services.sessions, services.diagnostics, notificationsOnly = false, showTitle = false,
        onDiagnostics = { diagnostics = true }, onDone = onDone, onBack = null, onLater = onDone,
        readiness = services.readiness.takeUnless { services.demo })
    if (diagnostics) {
        val close = { diagnostics = false }
        JFullScreen(onDismiss = close) { DiagnosticsScreen(services, onBack = close) }
    }
}

/** One of the first group's pages ([page]) while [assistant] is offline: its title and [JAssistantOffline]. */
@Composable
private fun AssistantOfflinePage(page: String, assistant: persona.core.contracts.api.CurrentAssistant, onBack: () -> Unit) {
    val title = when (settingsPageFromInitialPage(page)) {
        SettingsPage.Providers -> R.string.settings_providers_title
        SettingsPage.ModelConfig -> R.string.settings_row_models
        SettingsPage.Triage -> R.string.settings_triage_title
        SettingsPage.Extensions -> R.string.settings_section_extensions
        SettingsPage.Channels -> R.string.settings_src_channels_title
        SettingsPage.ClaudeCode -> R.string.settings_row_claude
        SettingsPage.Codex -> R.string.settings_row_codex
        SettingsPage.Mail -> R.string.settings_src_mail_title
        SettingsPage.Feishu -> R.string.settings_src_lark_title
        SettingsPage.FeishuBot -> R.string.settings_feishu_bot_title
        SettingsPage.Sent -> R.string.settings_sent_title
        else -> R.string.settings_section_connectors
    }
    JListPage(title = stringResource(title), onBack = onBack) { item("offline") { JAssistantOffline(assistant) } }
}
