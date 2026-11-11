package persona.feature.chat

import kotlinx.coroutines.runBlocking
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.api.DraftUploadState
import java.io.IOException

class ComposerPhotoEditTest {
    private fun photo(id: String, path: String? = "/tmp/$id.jpg") = ComposerAttachment(id, "$id.jpg",
        "content://example.com/$id", true, path, size = 10, uploadState = if (path != null) DraftUploadState.Uploaded else DraftUploadState.Pending)
    private val file = ComposerAttachment("notes", "notes.txt", "content://example.com/notes", false, "/tmp/notes.txt",
        uploadState = DraftUploadState.Uploaded)
    private val edited = ComposerAttachment("edited", "edited-1.jpg", "content://example.com/edited", true, size = 20)

    @Test fun anEditReplacesTheOriginalInPlaceAndTheMessageCarriesOnlyTheNewPath() = runBlocking {
        val state = ChatComposerState(ChatComposerDraft("看这里", listOf(photo("a"), file, photo("b"), photo("c"))))
        val uploaded = mutableListOf<ComposerAttachment>()
        val outcome = state.replaceWithEdited(state.attachments[2], edited) { uploaded += it; "/tmp/edited-b.jpg" }
        assertEquals(PhotoEditOutcome.Replaced, outcome)
        assertEquals(listOf(edited), uploaded)
        assertEquals(listOf("a", "notes", "edited", "c"), state.attachments.map { it.id })
        val now = state.attachments[2]
        assertEquals("/tmp/edited-b.jpg", now.path)
        assertEquals(DraftUploadState.Uploaded, now.uploadState)
        assertEquals("content://example.com/edited", now.uri)
        assertTrue(state.ready)
        val message = state.message()
        assertTrue(message.contains("/tmp/a.jpg\n/tmp/edited-b.jpg\n/tmp/c.jpg"))
        assertFalse(message.contains("/tmp/b.jpg"))
    }

    @Test fun aFailedEditUploadKeepsTheOriginal() = runBlocking {
        val before = listOf(photo("a"), photo("b"))
        val state = ChatComposerState(ChatComposerDraft("", before))
        val outcome = state.replaceWithEdited(before[0], edited) { throw IOException("offline") }
        assertTrue(outcome is PhotoEditOutcome.Failed)
        assertEquals(before, state.attachments)
    }

    @Test fun anOriginalRemovedDuringTheUploadIsNotBroughtBack() = runBlocking {
        val state = ChatComposerState(ChatComposerDraft("", listOf(photo("a"), photo("b"))))
        val original = state.attachments[0]
        val outcome = state.replaceWithEdited(original, edited) {
            state.attachments = state.attachments.filterNot { it.id == original.id }
            "/tmp/edited-a.jpg"
        }
        assertEquals(PhotoEditOutcome.Gone, outcome)
        assertEquals(listOf("b"), state.attachments.map { it.id })
    }

    @Test fun onlyPhotosOnTheComputerWithALocalCopyOpenTheEditor() {
        assertTrue(photo("a").editable)
        assertFalse(photo("a", path = null).editable) // still uploading
        assertFalse(photo("a", path = null).copy(error = "上传失败", uploadState = DraftUploadState.Failed).editable)
        assertFalse(photo("a").copy(uploadState = DraftUploadState.Invalid).editable)
        assertFalse(photo("a").copy(uri = "").editable) // restored from a sent message
        assertFalse(file.editable)
    }

    @Test fun replaceEditedLeavesAListWithoutTheOriginalAlone() {
        val list = listOf(photo("a"), file)
        assertEquals(list, replaceEdited(list, "gone", edited))
        assertEquals(listOf(edited, file), replaceEdited(list, "a", edited))
    }

    /** #4992 (iOS `anEditedPhotoKeepsItsEditsUntilItGoes`): the edits follow the photo they made, and go with it. */
    @Test fun anEditedPhotoKeepsItsEditsUntilItGoes() = runBlocking {
        val state = ChatComposerState(ChatComposerDraft("", listOf(photo("a"), photo("b"))))
        val turned = persona.core.designsystem.ImageEditState(persona.core.designsystem.ImageEdits(turns = 1))
        val kept = PhotoEdits("content://example.com/b", turned)
        state.replaceWithEdited(state.attachments[1], edited, kept) { "/tmp/edited-b.jpg" }
        assertEquals("the edit opens again as it was left, from the same source", kept, state.photoEdits["edited"])
        // Edited again: the newer photo carries them on; the one it replaced keeps nothing.
        val again = edited.copy(id = "edited-2", uri = "content://example.com/edited-2")
        val more = kept.copy(state = turned.copy(edits = turned.edits.turnLeft()))
        state.replaceWithEdited(state.attachments[1], again, more) { "/tmp/edited-b2.jpg" }
        assertEquals(setOf("edited-2"), state.photoEdits.keys)
        assertEquals("content://example.com/b", state.photoEdits["edited-2"]?.sourceUri)
        // Every edit undone: the photo goes back to its source and keeps nothing.
        state.replaceWithEdited(state.attachments[1], edited.copy(id = "plain"), null) { "/tmp/plain.jpg" }
        assertTrue(state.photoEdits.isEmpty())
        state.replaceWithEdited(state.attachments[1], again, more) { "/tmp/edited-b3.jpg" }
        state.attachments = state.attachments.filter { it.id != "edited-2" }
        assertTrue("the photo gone, its edits go too", state.photoEdits.isEmpty())
    }

    /** #4992: an edited camera photo's source stays in cache/chat-captures while the photo made from it is in the draft. */
    @Test fun anEditedCapturesSourceStaysUntilItsPhotoGoes() {
        val app = "persona.test"
        val capture = "content://$app.chat.files/chat-captures/shot.jpg"
        val shot = photo("shot").copy(uri = capture)
        val markedUp = edited.copy(uri = "content://$app.chat.files/chat-captures/edited.jpg")
        assertEquals("replaced by its edit, the source is kept", emptyList<String>(),
            leftCaptures(listOf(shot), listOf(markedUp), app, keptNow = listOf(capture)))
        assertEquals("the edit sent: the edit and its source go", listOf(markedUp.uri, capture),
            leftCaptures(listOf(markedUp), emptyList(), app, keptBefore = listOf(capture)))
    }
}
