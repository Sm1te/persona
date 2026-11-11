package persona.feature.chat

import android.graphics.Bitmap
import persona.core.designsystem.ImageEditState
import persona.core.contracts.api.DraftUploadState
import kotlinx.coroutines.CancellationException

/**
 * Whether a tap on this attachment opens the image editor (iOS ComposerImages, web AttachmentTray): a photo still on
 * the phone and already on the computer. One uploading or failed keeps its spinner / "Retry" until it has gone up; one
 * restored from a sent message has no local copy to draw on.
 */
internal val ComposerAttachment.editable: Boolean
    get() = image && uri.isNotEmpty() && path != null && error == null && uploadState != DraftUploadState.Invalid

/**
 * An edited photo's way back into the editor (#4992, iOS `EditedImage`): its untouched source and the edits with their
 * history, so a tap brings back its crop, turn and marks, all still undoable. Kept beside the attachment, dropped with it.
 */
internal data class PhotoEdits(val sourceUri: String, val state: ImageEditState)

/**
 * One photo open in the composer's editor: the attachment it replaces, the picture the editor works on ([sourceUri]'s,
 * the photo before any edit) and the edits it carries on from ([prior]; null for a photo never edited).
 */
internal class ComposerPhotoEdit(val original: ComposerAttachment, val source: Bitmap, val sourceUri: String = original.uri, val prior: PhotoEdits? = null)

/** How putting a marked-up photo in the draft ended. */
internal sealed interface PhotoEditOutcome {
    /** The edited photo is on the computer and took the original's place. */
    data object Replaced : PhotoEditOutcome
    /** The original left the draft (removed, or sent) while the edit went up: nothing to replace. */
    data object Gone : PhotoEditOutcome
    /** The upload failed; the draft keeps the original. */
    data class Failed(val error: Exception) : PhotoEditOutcome
}

/** [attachments] with the one of id [originalId] swapped for [edited] in the same place; unchanged without it. */
internal fun replaceEdited(attachments: List<ComposerAttachment>, originalId: String, edited: ComposerAttachment): List<ComposerAttachment> =
    attachments.map { if (it.id == originalId) edited else it }

/**
 * Puts [edited] (the marked-up copy of [original], a local JPEG) in [original]'s place: [upload] sends it to the
 * computer first, and only then does it replace the original, keeping the order, so the message carries the new path
 * and not the old one (web Composer `onDone`). A failed upload leaves the draft as it was.
 */
internal suspend fun ChatComposerState.replaceWithEdited(
    original: ComposerAttachment,
    edited: ComposerAttachment,
    /** What the edited photo keeps to be edited again; null: nothing (all its edits were undone). */
    kept: PhotoEdits? = null,
    upload: suspend (ComposerAttachment) -> String,
): PhotoEditOutcome {
    val path = try {
        upload(edited)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        return PhotoEditOutcome.Failed(e)
    }
    // Read again after the upload: the draft may have changed while it went up.
    if (attachments.none { it.id == original.id }) return PhotoEditOutcome.Gone
    attachments = replaceEdited(attachments, original.id, edited.copy(path = path, error = null, uploadState = DraftUploadState.Uploaded))
    photoEdits = photoEdits.filterKeys { id -> id != original.id && attachments.any { it.id == id } } +
        listOfNotNull(kept?.let { edited.id to it })
    return PhotoEditOutcome.Replaced
}
