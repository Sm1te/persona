package persona.core.designsystem

import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import persona.core.designsystem.icons.PhIcons

internal fun sidebarActionFill(palette: PersonaPalette): Color =
    if (palette.dark && palette.accent == Palettes.GlassDark.accent) Palettes.GlassLight.accent else palette.accent

@Composable
fun JSidebarNewChat(text: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val palette = PersonaTheme.palette
    val compact = LocalHomeDrawer.current && PersonaTheme.style == AppStyle.Glass
    val fill = if (compact) sidebarActionFill(palette) else palette.accent
    CompositionLocalProvider(LocalPalette provides if (compact) palette.copy(action = fill, onAction = palette.onAccentFill(fill)) else palette) {
        JButton(text, onClick, modifier, icon = PhIcons.NotePencil)
    }
}

/** Compact iPhone history is one title and a state mark, not a second conversation preview. */
@Composable
fun JSidebarHistoryRow(
    title: String,
    status: Status,
    selected: Boolean,
    onClick: () -> Unit,
    onMenu: () -> Unit,
    menuLabel: String,
    /** The line under the title (a leader conversation's open tasks, #4331); null: one line only. */
    hint: String? = null,
    titleAccessory: (@Composable () -> Unit)? = null,
) {
    JRow(title, onClick = onClick, onLongClick = onMenu, selected = selected, chevron = false, subtitle = hint,
        titleAccessory = titleAccessory, trailing = {
            if (status == Status.NeedsYou || status == Status.Working || status == Status.Unread) JStatusDot(status)
            JIconButton(PhIcons.DotsThree, menuLabel, onMenu)
        })
}
