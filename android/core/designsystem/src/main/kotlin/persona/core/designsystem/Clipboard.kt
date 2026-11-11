package persona.core.designsystem

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.os.Build
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources

/**
 * Upper bound for text placed directly on the system clipboard.
 *
 * Android binder transactions are limited to ~1 MB for the process. Above that, setPrimaryClip
 * throws a RuntimeException wrapping TransactionTooLargeException. A 256K-character bound
 * leaves ample headroom under the IPC limit across all multi-byte encodings while holding any
 * realistic snippet.
 */
const val MAX_CLIPBOARD_TEXT_CHARS = 256 * 1024

/**
 * Checks whether [text] length is within [MAX_CLIPBOARD_TEXT_CHARS] safe threshold.
 */
fun isClipboardTextUnderLimit(text: String): Boolean = text.length <= MAX_CLIPBOARD_TEXT_CHARS

/**
 * Places [text] on the system clipboard if its length is within [MAX_CLIPBOARD_TEXT_CHARS],
 * notifying [onNotice] or [feedback] of success or refusal.
 *
 * If [text] exceeds [MAX_CLIPBOARD_TEXT_CHARS], copying is refused and [tooLargeNotice] (or the
 * localized `designsystem_copy_too_large`) is shown to direct the user to Share instead.
 *
 * Any runtime failure from the system clipboard service is caught, preventing
 * [android.os.TransactionTooLargeException] crashes.
 *
 * @return true if the clip was successfully placed on the clipboard.
 */
fun copyTextSafely(
    context: Context,
    text: String,
    label: String = "Persona",
    feedback: AppFeedback? = null,
    onNotice: ((String) -> Unit)? = null,
    successNotice: String? = null,
    tooLargeNotice: String? = null,
): Boolean {
    val notify = onNotice ?: { msg -> feedback?.notice(msg) }
    if (text.length > MAX_CLIPBOARD_TEXT_CHARS) {
        val notice = tooLargeNotice ?: context.getString(R.string.designsystem_copy_too_large)
        notify(notice)
        return false
    }
    return try {
        val clipboard = context.getSystemService(Context.CLIPBOARD_SERVICE) as? ClipboardManager
        if (clipboard != null) {
            clipboard.setPrimaryClip(ClipData.newPlainText(label, text))
            if (successNotice != null) {
                notify(successNotice)
            }
            true
        } else {
            false
        }
    } catch (_: Exception) {
        val notice = tooLargeNotice ?: context.getString(R.string.designsystem_copy_too_large)
        notify(notice)
        false
    }
}

/**
 * Copying as iOS does it (#4209, `DESIGN_SYSTEM.md` section 4.3): the text goes on the clipboard through [copyTextSafely]
 * and a success haptic plays; a refusal (too large, or the clipboard failed) plays the error haptic with its notice. Every
 * copy action in the app goes through this, so they all feel the same. `val copy = rememberCopy(); … copy(text, null)`.
 *
 * The notice: a caller's own [notice] (what was copied, or more) always shows; an empty one shows none, for a screen that
 * says it itself. With null, the plain "Copied" shows only before Android 13, which confirms a copy on its own.
 */
@Composable
fun rememberCopy(): (text: String, notice: String?) -> Boolean {
    val context = LocalContext.current
    val feedback = LocalAppFeedback.current
    val haptics = rememberHaptics()
    val resources = LocalResources.current
    return remember(context, resources, feedback, haptics) {
        { text, notice ->
            val plain = if (Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU) resources.getString(R.string.designsystem_copied) else null
            var said: String? = null
            val copied = copyTextSafely(context, text, onNotice = { said = it },
                successNotice = notice?.takeIf { it.isNotEmpty() } ?: if (notice == null) plain else null)
            val style = if (copied) FeedbackStyle.Success else FeedbackStyle.Error
            // A shown toast plays its style's haptic; without one, the haptic plays here.
            val shown = said?.let { feedback?.notice(it, style) } == true
            if (!shown) haptics(style.haptic ?: Haptic.Tap)
            copied
        }
    }
}
