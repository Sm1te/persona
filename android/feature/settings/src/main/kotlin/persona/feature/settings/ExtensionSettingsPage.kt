package persona.feature.settings

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.res.stringResource
import persona.core.contracts.api.*
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import java.net.URI

/** Lifecycle authority comes only from manager-owned installation metadata, never extension configuration. */
internal data class ExtensionSettingsCapabilities(val detail: ExtensionDetail) {
    private val extension get() = detail.extension
    private val external get() = detail.readOnly || extension.source in listOf("hub.json", "dev")
    private val installed get() = extension.source == "installed" && extension.attached == null && !external
    val editable get() = !external && extension.enabled != null
    val market: String? get() = detail.origin?.takeIf { it.startsWith("market:") }
        ?.removePrefix("market:")?.substringBefore('/')?.takeIf { it.isNotBlank() }
        ?: extension.upgrade?.market?.takeIf { it.isNotBlank() }
    val check get() = installed && market != null
    val automatic get() = installed && extension.autoUpgrade != null
    val uninstall get() = installed && detail.fromMarket
    fun reason(removing: Boolean): Int = when {
        extension.source == "builtin" -> if (removing) R.string.settings_extension_builtin_uninstall else R.string.settings_extension_builtin_updates
        external || extension.attached != null || extension.source == "installed" ->
            if (removing) R.string.settings_extension_external_uninstall else R.string.settings_extension_external_updates
        else -> R.string.settings_extension_unknown
    }
}

/** The one host-owned settings renderer. Extensions supply configuration only; host diagnostics follow identity. */
@Composable
internal fun ExtensionSettingsPage(
    detail: ExtensionDetail?, busy: Boolean, refreshing: Boolean, onBack: () -> Unit, onRefresh: () -> Unit,
    updates: ExtensionSettingsUpdates, onEnabled: (Boolean) -> Unit, onAutoUpgrade: (Boolean) -> Unit,
    onCheck: () -> Unit, onUpgrade: ((String, String) -> Unit)?, onUninstall: () -> Unit,
    configuration: LazyListScope.() -> Unit, information: LazyListScope.() -> Unit,
    /** A pack's members by their installed names (#4122). */
    memberNames: Map<String, String> = emptyMap(),
) {
    JListPage(title = detail?.extension?.name?.ifBlank { null } ?: stringResource(R.string.settings_ext_title),
        onBack = onBack, largeTitle = false, refreshing = refreshing, onRefresh = onRefresh) {
        if (detail == null) {
            configuration()
            return@JListPage
        }
        val capabilities = ExtensionSettingsCapabilities(detail)
        val extension = detail.extension
        item("enabled") {
            JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.m)) {
                JSwitchRow(stringResource(R.string.settings_ext_enabled), extension.enabled ?: false, onEnabled, enabled = !busy && capabilities.editable)
            }
        }
        item("enabled-note") { HostSettingsNote(stringResource(when {
            capabilities.editable -> R.string.settings_extension_enabled_note
            detail.readOnly -> R.string.settings_ext_read_only
            extension.source in listOf("dev", "hub.json") -> R.string.settings_extension_external_enabled
            else -> R.string.settings_extension_unknown
        })) }
        item("configuration-title") { JSectionHeader(stringResource(R.string.settings_extension_configuration)) }
        configuration()
        hostInformation(detail)
        installedExtensionModel(detail, memberNames)
        information()
        item("updates-title") { JSectionHeader(stringResource(R.string.settings_extension_updates)) }
        item("updates-check") {
            JGroupItem(GroupPosition.Single) {
                JRow(stringResource(R.string.settings_extension_check), leadingIcon = PhIcons.ArrowClockwise,
                    trailing = if (updates.checking) ({ JSpinner() }) else null, chevron = false,
                    enabled = capabilities.check && !busy && !updates.checking, onClick = onCheck)
            }
        }
        item("updates-check-note") { HostSettingsNote(stringResource(when {
            !capabilities.check -> capabilities.reason(false)
            updates.checked && updates.candidate?.upgradable == false -> R.string.settings_extension_current
            else -> R.string.settings_extension_check_hint
        })) }
        updates.error?.let { error -> item("updates-error") { JInlineStatus(error, tone = InlineTone.Failed) } }
        val upgrade = extension.upgrade ?: updates.candidate?.takeIf { it.upgradable }?.let {
            // A version under a new ID is never installed automatically: it waits for the owner's confirmation.
            val renamed = it.id != extension.id
            ExtensionUpgrade(id = extension.id, market = it.market, from = extension.version.orEmpty(), to = it.version,
                state = if (renamed) "needs_permission" else "available", renameTo = it.id.takeIf { renamed })
        }
        upgrade?.let { u ->
            item("updates-candidate") {
                val res = androidx.compose.ui.platform.LocalResources.current
                val action = ExtensionText.upgradeAction(res, u) ?: res.getString(R.string.settings_ext_upgrade_go)
                JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.s)) {
                    JRow(stringResource(R.string.settings_ext_new_version, u.to), subtitle = ExtensionText.upgradeDetail(res, u), subtitleLines = Int.MAX_VALUE,
                        value = action, enabled = !busy && !updates.checking && capabilities.check && onUpgrade != null,
                        onClick = { onUpgrade?.invoke(u.market, u.marketId) })
                }
            }
        }
        item("updates-auto") {
            JGroupItem(GroupPosition.Single, Modifier.padding(top = Tokens.Space.s)) {
                // While the assistant's automatic extension updates are off it stays at its own saved value, grayed (#4748, the
                // web's): that value applies again once they are on. Unknown (loading, an older hub) stays editable.
                JSwitchRow(stringResource(R.string.settings_ext_auto_upgrade), extension.autoUpgrade ?: false, onAutoUpgrade,
                    enabled = !busy && !updates.checking && capabilities.automatic && extension.autoUpgradePaused == null && updates.globalAuto != false)
            }
        }
        item("updates-auto-note") { HostSettingsNote(stringResource(when {
            !capabilities.automatic -> if (capabilities.check) R.string.settings_extension_unknown else capabilities.reason(false)
            extension.autoUpgradePaused == "market_removed" -> R.string.settings_extension_market_removed
            extension.autoUpgradePaused != null -> ExtensionText.upgradeFooter(extension)
            updates.globalAuto == false -> R.string.settings_extension_global_off
            updates.globalAuto == null -> R.string.settings_extension_global_unknown
            else -> R.string.settings_ext_upgrade_footer
        })) }
        extension.upgradeNote?.let { n -> item("updates-history") {
            val res = androidx.compose.ui.platform.LocalResources.current
            ExtensionText.upgradeNote(res, n)?.let { text -> JRow(text, subtitle = n.at?.let(CatalogText::time), subtitleLines = Int.MAX_VALUE) }
        } }
        detail.previousVersion?.takeIf { it.isNotBlank() }?.let { previous ->
            item("updates-previous") { HostInformationRow(stringResource(R.string.settings_ext_about_previous), previous) }
            item("updates-rollback") { JRow(stringResource(R.string.settings_extension_rollback), enabled = false, chevron = false, onClick = {}) }
            item("updates-rollback-note") { HostSettingsNote(stringResource(R.string.settings_extension_rollback_unavailable)) }
        }
        item("danger-title") { JSectionHeader(stringResource(R.string.settings_extension_danger)) }
        item("uninstall") {
            JGroupItem(GroupPosition.Single) {
                JRow(stringResource(R.string.settings_ext_uninstall), leadingIcon = PhIcons.Trash, destructive = true,
                    chevron = false, enabled = !busy && !updates.checking && capabilities.uninstall, onClick = onUninstall)
            }
        }
        item("uninstall-note") { HostSettingsNote(stringResource(if (capabilities.uninstall) R.string.settings_ext_market_installed_note else capabilities.reason(true))) }
    }
}

private fun LazyListScope.hostInformation(detail: ExtensionDetail) {
    item("information-title") { JSectionHeader(stringResource(R.string.settings_extension_information)) }
    item("information-identity") {
        val e = detail.extension
        val missing = stringResource(R.string.settings_extension_missing)
        val source = when {
            e.attached != null -> stringResource(R.string.settings_extension_attached)
            e.source == "dev" -> stringResource(R.string.settings_extension_development)
            e.source == "hub.json" -> stringResource(R.string.settings_extension_configured)
            else -> ExtensionText.source(androidx.compose.ui.platform.LocalResources.current, e.source) ?: missing
        }
        val provenance = when {
            e.attached != null -> stringResource(R.string.settings_extension_attached)
            detail.fromMarket -> detail.origin.orEmpty()
            e.source == "installed" && !detail.origin.isNullOrBlank() -> stringResource(R.string.settings_extension_local_source)
            else -> missing
        }
        JGroupItem(GroupPosition.Single) {
            Column {
                HostInformationRow(stringResource(R.string.settings_ext_about_version), e.version?.takeIf { it.isNotBlank() } ?: missing)
                HostInformationRow(stringResource(R.string.settings_extension_id), e.id)
                HostInformationRow(stringResource(R.string.settings_ext_about_source), source)
                // kind is a contribution point, not a declared runtime; the current detail endpoint supplies no runtime.
                HostInformationRow(stringResource(R.string.settings_extension_runtime), missing)
                HostInformationRow(stringResource(R.string.settings_extension_installed_from), provenance)
                detail.publisher?.takeIf { it.isNotBlank() }?.let { HostInformationRow(stringResource(R.string.settings_ext_about_publisher), it) }
                e.tools.orEmpty().forEach { tool -> HostInformationRow(stringResource(R.string.settings_ext_about_tools), listOf(tool.title ?: tool.name, tool.version).filter { it.isNotBlank() }.joinToString(" · ")) }
                val home = detail.homepage?.takeIf { value -> runCatching { URI(value).let { it.scheme == "https" && !it.host.isNullOrBlank() && it.userInfo == null } }.getOrDefault(false) }
                home?.let { url ->
                    val handler = LocalUriHandler.current
                    JRow(stringResource(R.string.settings_ext_about_homepage), leadingIcon = PhIcons.ArrowSquareOut, onClick = { runCatching { handler.openUri(url) } })
                }
            }
        }
    }
    detail.extension.warnings.orEmpty().forEachIndexed { index, warning ->
        item("information-warning-$index") { JInlineStatus(warning, Modifier.padding(top = Tokens.Space.s), tone = InlineTone.Caution) }
    }
}

@Composable
private fun HostInformationRow(label: String, value: String) = SelectionContainer { JRow(label, subtitle = value, subtitleLines = Int.MAX_VALUE, chevron = false) }

@Composable
private fun HostSettingsNote(text: String) = SettingsCaption(text, Modifier.padding(horizontal = rowPadding, vertical = Tokens.Space.s))
