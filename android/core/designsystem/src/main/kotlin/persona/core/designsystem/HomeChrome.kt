package persona.core.designsystem

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusProperties
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

class HomeChrome(
    val items: List<NavItem>,
    val selected: String,
    val onSelect: (String) -> Unit,
    val leading: @Composable () -> Unit,
    /** Where the home bar ends, in pixels from the page's top: what floats under it starts there (the link pill). */
    val barHeight: androidx.compose.runtime.MutableIntState? = null,
)

val LocalHomeChrome = staticCompositionLocalOf<HomeChrome?> { null }
val LocalHomeDrawer = staticCompositionLocalOf { false }

/** The sidebar stays behind a full-size page that moves aside; opening it never changes navigation. */
@Composable
fun JShellDrawer(
    opened: Boolean,
    onDismiss: () -> Unit,
    modifier: Modifier = Modifier,
    drawer: @Composable () -> Unit,
    content: @Composable () -> Unit,
) {
    val progress by animateFloatAsState(if (opened) 1f else 0f,
        tween(if (PersonaTheme.reduceMotion) Tokens.Motion.reduced else PersonaTheme.motion.standard), label = "home-drawer")
    val palette = PersonaTheme.palette
    val density = LocalDensity.current
    val shape = RoundedCornerShape(HomeTokens.drawerPageRadius)
    val close = stringResource(R.string.designsystem_close)
    BoxWithConstraints(modifier.fillMaxSize().background(sidebarBackground())) {
        val drawerWidth = (maxWidth * HomeTokens.drawerFraction).coerceAtMost(HomeTokens.drawerMaximum)
        if (opened || progress > 0f) {
            Box(Modifier.width(drawerWidth).fillMaxHeight().statusBarsPadding().navigationBarsPadding()) {
                androidx.compose.runtime.CompositionLocalProvider(LocalHomeDrawer provides (PersonaTheme.style == AppStyle.Glass)) { drawer() }
            }
        }
        Box(Modifier.fillMaxSize().graphicsLayer {
            translationX = with(density) { drawerWidth.toPx() } * progress
            this.shape = shape
            clip = progress > 0f
            shadowElevation = with(density) { HomeTokens.drawerShadow.toPx() } * progress
            ambientShadowColor = Color.Black.copy(alpha = HomeTokens.drawerShadowAlpha)
            spotShadowColor = Color.Black.copy(alpha = HomeTokens.drawerShadowAlpha)
        }.then(if (progress > 0f) Modifier.border(Tokens.Space.xxs / 2, palette.line, shape) else Modifier)) {
            Box(Modifier.fillMaxSize().focusProperties { canFocus = !opened }
                .then(if (opened) Modifier.clearAndSetSemantics {} else Modifier)) { content() }
            if (opened || progress > 0f) Box(Modifier.fillMaxSize().background(palette.scrim.copy(alpha = palette.scrim.alpha * progress))
                .clickable(enabled = opened, role = Role.Button, onClick = onDismiss).semantics { contentDescription = close })
        }
    }
}

/**
 * The same sidebar as a column beside the page, the way an open iPhone Duo and an iPad show it (a regular window):
 * [width] wide, the page from [pageStart] on (past a separating fold). [shown] slides it out and back; nothing about it
 * is modal. Without [sidebar] (a phone, a folded foldable) the page fills the box exactly as before, and it stays the
 * same composition either way, so folding or unfolding keeps everything the page holds.
 */
@Composable
fun JSidebarColumn(
    sidebar: (@Composable () -> Unit)?,
    width: Dp,
    pageStart: Dp,
    shown: Boolean,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    val progress by animateFloatAsState(if (shown) 1f else 0f,
        tween(if (PersonaTheme.reduceMotion) Tokens.Motion.reduced else PersonaTheme.motion.standard), label = "sidebar-column")
    val palette = PersonaTheme.palette
    val visible = sidebar != null && progress > 0f
    Box(modifier.fillMaxSize()) {
        if (visible) Box(Modifier.width(width).fillMaxHeight()
            .graphicsLayer { translationX = -(1f - progress) * width.toPx() }
            .background(sidebarBackground())) {
            Box(Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding()) {
                androidx.compose.runtime.CompositionLocalProvider(LocalHomeDrawer provides (PersonaTheme.style == AppStyle.Glass)) { sidebar?.invoke() }
            }
            Box(Modifier.align(Alignment.TopEnd).width(Tokens.Space.xxs / 2).fillMaxHeight().background(palette.line))
        }
        Box(Modifier.fillMaxSize().padding(start = if (sidebar != null) pageStart * progress else 0.dp)) { content() }
    }
}

/** The sidebar's own surface, whether it is a drawer or a column. */
@Composable
private fun sidebarBackground(): Color = if (PersonaTheme.style == AppStyle.Glass) {
    if (PersonaTheme.darkTheme) HomeTokens.drawerDark else HomeTokens.drawerLight
} else PersonaTheme.palette.surfaceOpaque
