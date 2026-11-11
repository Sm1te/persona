package persona.core.designsystem

import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.hapticfeedback.HapticFeedback
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalHapticFeedback

/**
 * The feedback iOS plays through `Haptics` (AppModel.swift): a light [Tap] on a press that opens or undoes something,
 * [Selection] as something dragged reaches a new place (a to-do on the calendar over the next day), [Success] when an
 * answer, approval or "Done" went through, [Attention] when what the user acted on changed or must wait (the
 * terminal's dialog moved on, the hub is out of reach), [Error] when an operation failed.
 */
enum class Haptic { Tap, Selection, Success, Attention, Error }

/** Android's closest feedback type for each; the system's touch-feedback setting still decides whether it plays. */
fun Haptic.feedbackType(): HapticFeedbackType = when (this) {
    Haptic.Tap -> HapticFeedbackType.ContextClick
    Haptic.Selection -> HapticFeedbackType.SegmentTick
    Haptic.Success -> HapticFeedbackType.Confirm
    Haptic.Attention -> HapticFeedbackType.GestureThresholdActivate
    Haptic.Error -> HapticFeedbackType.Reject
}

fun HapticFeedback.play(haptic: Haptic) = performHapticFeedback(haptic.feedbackType())

/** Plays a [Haptic] from a composable's callbacks: `val haptics = rememberHaptics(); … haptics(Haptic.Success)`. */
@Composable
fun rememberHaptics(): (Haptic) -> Unit {
    val feedback = LocalHapticFeedback.current
    return remember(feedback) { { haptic: Haptic -> feedback.play(haptic) } }
}

/** [action] after a light [Haptic.Tap], as iOS's buttons that call `Haptics.tap()` before their action (DESIGN_SYSTEM.md 4.3). */
fun ((Haptic) -> Unit).tapped(action: () -> Unit): () -> Unit = { this(Haptic.Tap); action() }

/** The same [JAction], felt when pressed ([tapped]). */
fun ((Haptic) -> Unit).tapped(action: JAction): JAction = action.copy(onClick = tapped(action.onClick))
