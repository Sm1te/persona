package persona.core.designsystem

import persona.core.contracts.ProtocolCompatibility

import androidx.compose.ui.res.stringResource
import androidx.annotation.StringRes
import android.graphics.Paint
import android.os.PowerManager
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.withFrameMillis
import androidx.compose.runtime.compositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.DrawScope
import androidx.compose.ui.graphics.drawscope.clipPath
import androidx.compose.ui.graphics.drawscope.rotate
import androidx.compose.ui.graphics.drawscope.translate
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.drawIntoCanvas
import androidx.compose.ui.graphics.nativeCanvas
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import persona.core.contracts.api.PersonaInfo
import kotlinx.coroutines.delay
import kotlin.math.abs
import kotlin.math.cos
import kotlin.math.max
import kotlin.math.sin

/** What the persona's face shows (docs/apple/persona.md "States"); the wire values are persona.json's. */
enum class PersonaState(val wire: String, @StringRes val labelRes: Int) {
    Idle("idle", R.string.designsystem_persona_idle), Thinking("thinking", R.string.designsystem_persona_thinking),
    Working("working", R.string.designsystem_persona_working), NeedsYou("needs_you", R.string.designsystem_persona_needs_you),
    Offline("offline", R.string.designsystem_persona_offline);

    val label: String @Composable get() = stringResource(labelRes)

    /** The states whose face moves every frame (idle only blinks now and then). */
    val moves: Boolean get() = this == Thinking || this == Working || this == NeedsYou
}

/** The app-drawn default; hub and extension packages provide other looks through [PersonaStore]. */
enum class PersonaDesign(val id: String, @StringRes val titleRes: Int, @StringRes val summaryRes: Int) {
    Mochi("persona", R.string.designsystem_face_mochi, R.string.designsystem_face_mochi_summary);

    val title: String @Composable get() = stringResource(titleRes)
    val summary: String @Composable get() = stringResource(summaryRes)

    companion object {
        val Default = Mochi
        fun fromId(id: String?): PersonaDesign = entries.firstOrNull { it.id == ProtocolCompatibility.canonicalBuiltin(id) } ?: Default
    }
}

/** The chosen built-in face; [PersonaFace] draws it unless told otherwise. */
val LocalPersonaDesign = compositionLocalOf { PersonaDesign.Default }

/** The default dumpling, or a downloaded package, animated for the current state. */
@Composable
fun PersonaFace(
    state: PersonaState, modifier: Modifier = Modifier, size: Dp = 40.dp,
    design: PersonaDesign = LocalPersonaDesign.current, pack: PersonaInfo? = LocalPersonaPack.current,
) {
    val store = LocalPersonaStore.current
    if (pack != null && store != null) {
        // Keyed on the package only: the last state's frames stay up while the next state's load, instead of the built-in.
        val frames = remember(pack.id, pack.rev) { mutableStateOf<PersonaFrames?>(null) }
        LaunchedEffect(pack.id, pack.rev, state) { store.frames(pack, state)?.let { frames.value = it } }
        frames.value?.let { PackFace(it, modifier.size(size)); return }
    }
    val palette = PersonaTheme.palette
    val context = LocalContext.current
    // Held still with Reduce Motion and in Battery Saver, like iOS (Low Power Mode) and the glass fallback.
    val still = PersonaTheme.reduceMotion || context.getSystemService(PowerManager::class.java)?.isPowerSaveMode == true
    val ring = palette.needsYou.dot
    val moving = state.moves && !still

    // Seconds for the moving states, about 15 frames a second like iOS (nothing runs while idle or still).
    var time by remember { mutableFloatStateOf(0f) }
    LaunchedEffect(moving) {
        if (!moving) return@LaunchedEffect
        val start = withFrameMillis { it }
        while (true) {
            time = (withFrameMillis { it } - start) / 1000f
            delay(FRAME_MS)
        }
    }
    // The idle blink: the eyes close to 12 % and open again every 3.6 s.
    val blink = remember { Animatable(1f) }
    LaunchedEffect(state, still) {
        blink.snapTo(1f)
        if (state != PersonaState.Idle || still) return@LaunchedEffect
        while (true) {
            delay(BLINK_EVERY_MS)
            blink.animateTo(0.12f, tween(BLINK_MS))
            blink.animateTo(1f, tween(BLINK_MS))
        }
    }

    val paint = remember { Paint(Paint.ANTI_ALIAS_FLAG) }
    val dark = PersonaTheme.darkTheme
    Box(modifier.size(size).clearAndSetSemantics {}, contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val t = if (moving) time else 0f
            when (design) {
                PersonaDesign.Mochi -> mochi(state, dark, t, moving, blink.value, size < Mochi.COMPACT_BELOW, paint)
            }
            if (state == PersonaState.NeedsYou) needsYouRing(ring, t, moving)
        }
        if (state == PersonaState.Thinking) {
            ThoughtBubble(size, Mochi.cheek, if (moving) time else 0f, moving,
                Modifier.align(Alignment.Center).offset(x = size * 0.25f, y = -size * 0.29f))
        }
    }
}

/**
 * A hub package's dot: its frames in turn at their own delays (frame 0 alone with Reduce Motion or in Battery
 * Saver: the package makes frame 0 tell the state on its own). The state colours are drawn into the frames.
 */
@Composable
private fun PackFace(frames: PersonaFrames, modifier: Modifier) {
    val context = LocalContext.current
    val still = PersonaTheme.reduceMotion || context.getSystemService(PowerManager::class.java)?.isPowerSaveMode == true
    var index by remember(frames) { mutableIntStateOf(0) }
    LaunchedEffect(frames, still) {
        index = 0
        if (still || frames.bitmaps.size < 2) return@LaunchedEffect
        while (true) {
            delay(frames.delaysMs[index].coerceAtLeast(20).toLong())
            index = (index + 1) % frames.bitmaps.size
        }
    }
    val bitmaps = remember(frames) { frames.bitmaps.map { it.asImageBitmap() } }
    Image(bitmaps[index], contentDescription = null, modifier.clearAndSetSemantics {})
}

/**
 * Draws Mochi at time t (iOS `PersonaFace.mochi`, the same geometry traced from the 1024 px icon): the disc, the body's
 * soft shadow, the outlined body, the cheek, the gloss and the motion lines (not when [compact]), and the eyes.
 * Thinking hides the lines (the bubble is there) and lifts the eyes; working scans the eyes, bobs, lights the lines in turn
 * and runs an orange arc around the rim; waiting for you rocks the body (a wave); offline is gray with closed eyes.
 */
private fun DrawScope.mochi(state: PersonaState, dark: Boolean, t: Float, moving: Boolean, open: Float, compact: Boolean, paint: Paint) {
    val s = this.size.minDimension
    val offline = state == PersonaState.Offline
    val ink = if (offline) Mochi.sleepInk else Mochi.ink
    drawCircle(Brush.verticalGradient(if (dark) Mochi.discDark else Mochi.discLight, startY = 0f, endY = s))
    val circle = Path().apply { addOval(androidx.compose.ui.geometry.Rect(0f, 0f, s, s)) }
    clipPath(circle) {
        if (state == PersonaState.Working) {
            val w = max(RING_MIN.toPx(), s * RING_SHARE)
            val turn = if (moving) (t / 1.6f % 1f) * 360f else 300f
            drawArc(Mochi.cheek, turn - 90f, 0.28f * 360f, false, Offset(w / 2, w / 2), Size(s - w, s - w), style = Stroke(w, cap = StrokeCap.Round))
        }
        val rock = if (state == PersonaState.NeedsYou && moving) sin(t * 7f) * 5f else 0f
        val bob = if (state == PersonaState.Working && moving) -abs(sin(t * 5f)) * s * 0.02f else 0f
        translate(top = bob) {
            rotate(rock, Mochi.p(500f, 780f) * s) {
                val lw = max(Mochi.STROKE_MIN.toPx(), s * Mochi.STROKE)
                // The soft shadow under the body.
                paint.clearShadowLayer()
                paint.style = Paint.Style.FILL
                paint.color = Color.Black.copy(alpha = if (dark) 0.45f else 0.14f).toArgb()
                paint.maskFilter = android.graphics.BlurMaskFilter(s * 0.02f, android.graphics.BlurMaskFilter.Blur.NORMAL)
                val c = Mochi.p(490f, 800f) * s
                drawIntoCanvas { it.nativeCanvas.drawOval(c.x - 0.3f * s, c.y - 0.022f * s, c.x + 0.3f * s, c.y + 0.022f * s, paint) }
                paint.maskFilter = null
                val body = Mochi.body(s)
                drawPath(body, Brush.linearGradient(if (offline) Mochi.sleepBody else Mochi.bodyColors,
                    start = Offset(0.4f * s, 0.3f * s), end = Offset(0.5f * s, 0.82f * s)))
                drawPath(body, if (offline) Mochi.sleepInk else if (dark) Mochi.outlineDark else Mochi.ink,
                    style = Stroke(lw, cap = StrokeCap.Round, join = StrokeJoin.Round))
                val k = if (compact) Mochi.COMPACT_FEATURES else 1f
                val cheek = Mochi.p(312f, 556f) * s
                val cr = Mochi.CHEEK_RADIUS * k * s
                drawOval(if (offline) Mochi.sleepCheek else Mochi.cheek, Offset(cheek.x - cr, cheek.y - cr * 0.9f), Size(cr * 2, cr * 1.8f))
                if (!compact) {
                    drawPath(Mochi.gloss(s), Color.White, style = Stroke(lw * 0.6f, cap = StrokeCap.Round))
                    val lit = when (state) {
                        PersonaState.Idle, PersonaState.NeedsYou -> floatArrayOf(1f, 1f, 1f)
                        PersonaState.Working -> {
                            val on = if (moving) (t * 4f).toInt() % 3 else 0
                            FloatArray(3) { if (it == on) 1f else 0.35f }
                        }
                        else -> FloatArray(0)
                    }
                    val motion = if (dark) Mochi.motionDark else Mochi.ink
                    lit.forEachIndexed { i, a ->
                        val (from, to) = Mochi.lines[i]
                        drawLine(motion.copy(alpha = a), from * s, to * s, lw * 0.85f, StrokeCap.Round)
                    }
                }
                val er = Mochi.EYE_RADIUS * k * s
                if (offline) {
                    for (e in Mochi.eyes) {
                        val c2 = e * s
                        val lid = Path().apply { moveTo(c2.x - er, c2.y); quadraticTo(c2.x, c2.y + er * 0.7f, c2.x + er, c2.y) }
                        drawPath(lid, ink, style = Stroke(lw * 0.8f, cap = StrokeCap.Round))
                    }
                } else {
                    val look = when (state) {
                        PersonaState.Thinking -> Offset(s * 0.022f, -s * 0.03f)
                        PersonaState.Working -> Offset(s * (if (moving) sin(t * 1.8f) * 0.028f else -0.024f), s * 0.012f)
                        else -> Offset.Zero
                    }
                    val eh = er * (if (state == PersonaState.Idle) open else 1f)
                    for (e in Mochi.eyes) {
                        val c2 = e * s + look
                        drawOval(ink, Offset(c2.x - er, c2.y - eh), Size(er * 2, eh * 2))
                    }
                }
            }
        }
    }
}

/** Mochi's geometry in shares of the face's side and its colours (iOS `Mochi` / `MochiInk`): icon pixels into the disc. */
internal object Mochi {
    private const val SCALE = 1.16f
    /** Below this the motion lines and the gloss go and the eyes and cheek grow, so the face still reads. */
    val COMPACT_BELOW = 36.dp
    const val COMPACT_FEATURES = 1.3f
    const val EYE_RADIUS = 29f / 1024f * SCALE
    const val CHEEK_RADIUS = 37f / 1024f * SCALE
    const val STROKE = 30f / 1024f * SCALE
    val STROKE_MIN = 1.3.dp

    val cheek = Color(0xFFF2703C)
    val ink = Color(0xFF1D1816)
    val outlineDark = Color(0xFF0E0B0A)
    val motionDark = Color(0xFFD6D1CD)
    val discLight = listOf(Color(0xFFF4EADF), Color(0xFFE7D7C4))
    val discDark = listOf(Color(0xFF332A26), Color(0xFF1D1816))
    val bodyColors = listOf(Color.White, Color(0xFFE9E3DE))
    val sleepBody = listOf(Color(0xFFD9D4D0), Color(0xFFBDB6B1))
    val sleepInk = Color(0xFF6E6763)
    val sleepCheek = Color(0xFFA59D98)

    fun p(x: Float, y: Float) = Offset(0.5f + (x - 545f) / 1024f * SCALE, 0.5f + (y - 548f) / 1024f * SCALE)

    val eyes = listOf(p(397f, 497f), p(553f, 480f))
    val lines = listOf(p(792f, 352f) to p(818f, 298f), p(838f, 384f) to p(884f, 340f), p(864f, 428f) to p(924f, 408f))

    fun body(s: Float): Path {
        fun q(x: Float, y: Float) = p(x, y) * s
        fun Path.c(c1: Offset, c2: Offset, to: Offset) = cubicTo(c1.x, c1.y, c2.x, c2.y, to.x, to.y)
        return Path().apply {
            val start = q(262f, 778f)
            moveTo(start.x, start.y)
            c(q(420f, 792f), q(600f, 790f), q(702f, 772f))
            c(q(742f, 764f), q(752f, 700f), q(760f, 640f))
            c(q(800f, 598f), q(828f, 562f), q(826f, 502f))
            c(q(825f, 446f), q(795f, 418f), q(764f, 428f))
            c(q(736f, 440f), q(722f, 468f), q(712f, 494f))
            c(q(690f, 440f), q(650f, 350f), q(560f, 304f))
            c(q(470f, 260f), q(330f, 288f), q(254f, 382f))
            c(q(184f, 462f), q(158f, 582f), q(168f, 672f))
            c(q(176f, 742f), q(206f, 772f), start)
            close()
        }
    }

    /** The icon's highlight on the top left of the head. */
    fun gloss(s: Float): Path = Path().apply {
        val a = p(312f, 380f) * s; val c = p(352f, 346f) * s; val b = p(412f, 334f) * s
        moveTo(a.x, a.y); quadraticTo(c.x, c.y, b.x, b.y)
        val d = p(288f, 414f) * s; val e = p(290f, 411f) * s
        moveTo(d.x, d.y); lineTo(e.x, e.y)
    }
}

/** "Needs you": a ring inside the edge, breathing while moving. */
private fun DrawScope.needsYouRing(ring: Color, t: Float, moving: Boolean) {
    val s = this.size.minDimension
    val w = max(RING_MIN.toPx(), s * RING_SHARE)
    val pulse = if (moving) 0.55f + 0.45f * cos(t * 3f) else 1f
    drawCircle(ring.copy(alpha = ring.alpha * pulse), radius = (s - w) / 2, style = Stroke(w))
}

/** A small thought bubble whose three dots light up in turn (all lit when still). */
@Composable
private fun ThoughtBubble(size: Dp, accent: Color, t: Float, moving: Boolean, modifier: Modifier) {
    val lit = if (moving) (t * 2.5f).toInt() % 4 else 3
    Row(
        modifier.shadow(size * 0.02f, CircleShape).clip(RoundedCornerShape(50)).background(Color.White)
            .padding(horizontal = size * 0.04f, vertical = size * 0.04f),
        horizontalArrangement = Arrangement.spacedBy(size * 0.025f), verticalAlignment = Alignment.CenterVertically,
    ) {
        repeat(3) { i ->
            Box(Modifier.size(size * 0.05f).clip(CircleShape).background(if (i == lit || lit == 3) accent else Color(0xFFB3B3B3)))
        }
    }
}

private const val FRAME_MS = 66L
private const val BLINK_EVERY_MS = 3600L
private const val BLINK_MS = 110
/** "Needs you"'s ring: this share of the face's side, never thinner than [RING_MIN] (iOS `ringShare` / `ringMin`). */
private const val RING_SHARE = 0.07f
private val RING_MIN = 1.5.dp

/**
 * The persona's face as a top-bar button (iOS `ShellPersonaButton`): as big as the bar's round buttons, read out as
 * [contentDescription] with the persona's state.
 */
@Composable
fun JPersonaButton(state: PersonaState, contentDescription: String, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val stateText = stringResource(R.string.designsystem_persona_button_state, state.label)
    val haptics = rememberHaptics()
    Box(
        modifier.size(48.dp).clip(CircleShape)
            .clickable(role = Role.Button, onClick = haptics.tapped(onClick))
            .semantics { this.contentDescription = contentDescription; stateDescription = stateText },
        contentAlignment = Alignment.Center,
    ) { PersonaFace(state, size = if (LocalHomeChrome.current != null && PersonaTheme.style == AppStyle.Glass) HomeTokens.homeFaceSize else 40.dp) }
}
