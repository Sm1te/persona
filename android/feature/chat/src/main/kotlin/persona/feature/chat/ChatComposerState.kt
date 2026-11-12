package persona.feature.chat

import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.listSaver
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import persona.core.contracts.api.*
import persona.core.contracts.JsonCodec
import kotlinx.serialization.encodeToString

data class ComposerAttachment(
    val id: String,
    val name: String,
    val uri: String,
    val image: Boolean,
    val path: String? = null,
    val error: String? = null,
    val size: Long? = null,
    val uploadState: DraftUploadState = DraftUploadState.Pending,
)

data class ChatComposerDraft(
    val text: String = "", val attachments: List<ComposerAttachment> = emptyList(),
    val sessionVersion: DraftSessionVersion? = null,
)

private fun DraftAttachment.composerAttachment() = ComposerAttachment(
    id, name, uri, type == DraftAttachmentType.Image, uploadedPath, error, size, uploadState)

internal fun DraftSnapshot.composerDraft() = ChatComposerDraft(text, attachments.map { it.composerAttachment() }, sessionVersion)

internal fun ChatComposerDraft.storedAttachments() = attachments.map {
    DraftAttachment(it.id, if (it.image) DraftAttachmentType.Image else DraftAttachmentType.File,
        it.uri, it.name, it.size, when {
            it.uploadState == DraftUploadState.Invalid -> DraftUploadState.Invalid
            it.path != null -> DraftUploadState.Uploaded
            it.error != null -> DraftUploadState.Failed
            else -> it.uploadState
        }, it.path, it.error)
}

@Stable
class ChatComposerState(initial: ChatComposerDraft = ChatComposerDraft()) {
    var focused by mutableStateOf(false)
    var focusRequest by mutableIntStateOf(0)
    /** What [message] says for photos sent without words; [ChatComposer] keeps it in the app's language. */
    var photosOnlyText: String = ""
    var value by mutableStateOf(TextFieldValue(initial.text, TextRange(initial.text.length)))
    private var attachmentList by mutableStateOf(initial.attachments)
    /** Edited photos' sources, edits and history by attachment id (#4992): kept in memory beside the draft, never sent. */
    internal var photoEdits by mutableStateOf<Map<String, PhotoEdits>>(emptyMap())
    /** The draft's attachments; a photo that leaves it takes its kept edits along. */
    var attachments: List<ComposerAttachment>
        get() = attachmentList
        set(value) {
            attachmentList = value
            if (photoEdits.keys.any { id -> value.none { it.id == id } }) photoEdits = photoEdits.filterKeys { id -> value.any { it.id == id } }
        }
    var sessionVersion by mutableStateOf(initial.sessionVersion)
    var currentSessionVersion by mutableStateOf<DraftSessionVersion?>(null)
    val oldVersion get() = hasContent && (currentSessionVersion?.replaced == true || sessionVersion?.let { origin ->
        currentSessionVersion?.let(origin::differsFrom)
    } == true)
    val draft get() = ChatComposerDraft(value.text, attachments, sessionVersion)
    val ready get() = attachments.all { it.path != null && it.uploadState != DraftUploadState.Invalid }
    val hasContent get() = value.text.isNotBlank() || attachments.isNotEmpty()

    internal fun beginEdit() {
        if (!hasContent) sessionVersion = currentSessionVersion
    }

    fun insert(text: String, atSelection: Boolean) {
        beginEdit()
        val start = if (atSelection) value.selection.min.coerceIn(0, value.text.length) else value.text.length
        val end = if (atSelection) value.selection.max.coerceIn(start, value.text.length) else start
        val prefix = value.text.take(start)
        val suffix = value.text.drop(end)
        val addition = (if (prefix.isNotEmpty() && !prefix.last().isWhitespace()) " " else "") + text +
            (if (suffix.isNotEmpty() && !suffix.first().isWhitespace()) " " else "")
        value = TextFieldValue(prefix + addition + suffix, TextRange(start + addition.length))
    }

    fun message(): String {
        check(ready)
        val images = attachments.filter { it.image }.mapNotNull { it.path }
        val files = attachments.filterNot { it.image }.mapNotNull { it.path }.map(::quoteComposerPath)
        // Photos alone still say something to the agent (iOS and the old app send "Please look at the images"), in the app's language.
        val words = if (value.text.isBlank() && files.isEmpty() && images.isNotEmpty()) photosOnlyText else value.text
        val text = (listOf(words) + files).filter(String::isNotBlank).joinToString(" ")
        return text + if (images.isEmpty()) "" else "\n\n<attached_images>\n${images.joinToString("\n")}\n</attached_images>"
    }

    fun clearIfUnchanged(sent: ChatComposerDraft) {
        if (draft == sent) { value = TextFieldValue(); attachments = emptyList(); sessionVersion = currentSessionVersion }
    }

    /** Fills an empty composer with a stored draft: the composer is drawn before the draft is read (#4045). */
    internal fun load(draft: ChatComposerDraft) {
        value = TextFieldValue(draft.text, TextRange(draft.text.length))
        attachments = draft.attachments
        sessionVersion = draft.sessionVersion
    }

    fun editMessage(text: String) {
        beginEdit()
        val paths = composerImagePaths(text)
        val plain = text.replace(composerImagePlaceholders, "").replace(composerImageTags, "").trimEnd()
        value = TextFieldValue(plain, TextRange(plain.length))
        attachments = paths.mapIndexed { index, path ->
            ComposerAttachment("restored-$index-$path", path.substringAfterLast('/'), "", true, path,
                uploadState = DraftUploadState.Uploaded)
        }
    }

    internal fun restoreMessage(text: String) {
        if (!hasContent) { editMessage(text); return }
        val restored = ChatComposerState().apply { editMessage(text) }
        val combined = listOf(value.text, restored.value.text).filter(String::isNotBlank).joinToString("\n\n")
        value = TextFieldValue(combined, TextRange(combined.length))
        attachments = attachments + restored.attachments.filter { item -> attachments.none { it.id == item.id } }
    }

    /**
     * A queued message pulled back (the queue strip's "Pull back to edit"): the draft if empty, else after it on a new line (iOS); photos it
     * carried come back as attachments rather than as their tag.
     */
    internal fun restoreQueued(text: String) {
        beginEdit()
        val restored = ChatComposerState().apply { editMessage(text) }
        val words = restored.value.text.let {
            if (restored.attachments.isNotEmpty() && (it.trim() in composerPhotoStandIns || it.trim() == photosOnlyText)) "" else it
        }
        val combined = when {
            value.text.isEmpty() -> words
            words.isEmpty() -> value.text
            else -> value.text + "\n" + words
        }
        value = TextFieldValue(combined, TextRange(combined.length))
        attachments = attachments + restored.attachments.filter { item -> attachments.none { it.id == item.id } }
    }

    companion object {
        val Saver = listSaver<ChatComposerState, String>(
            save = { state -> listOf(state.value.text, JsonCodec.encodeToString(state.draft.storedAttachments()),
                state.sessionVersion?.let { JsonCodec.encodeToString(it) }.orEmpty()) },
            restore = { values ->
                if (values.size == 3) ChatComposerState(ChatComposerDraft(values[0],
                    JsonCodec.decodeFromString<List<DraftAttachment>>(values[1]).map { it.composerAttachment() },
                    values[2].takeIf(String::isNotEmpty)?.let { JsonCodec.decodeFromString<DraftSessionVersion>(it) }))
                else ChatComposerState(ChatComposerDraft(values.first(), values.drop(1).chunked(6).map {
                    ComposerAttachment(it[0], it[1], it[2], it[3].toBoolean(), it[4].ifEmpty { null }, it[5].ifEmpty { null })
                }))
            },
        )
    }
}

@Composable
fun rememberChatComposerState(initial: ChatComposerDraft = ChatComposerDraft()): ChatComposerState =
    rememberSaveable(saver = ChatComposerState.Saver) { ChatComposerState(initial) }

internal fun quoteComposerPath(path: String): String =
    if (path.any { it.isWhitespace() || it in "'\"\\\$`;&|<>()[]{}*?!" }) "'${path.replace("'", "'\\''")}'" else path

private val composerImageTags = Regex("<attached_images>([\\s\\S]*?)</attached_images>")
private val composerImagePlaceholders = Regex("\\A(?:\\[Image #\\d+]\\s*)+")
// Photo-only words sent by current and older clients: wire data, not interface copy.
private val composerPhotoStandIns = setOf(
    "Please look at the image", "Please look at the images", "Please look at the photos", "See the images",
    "请看图片", // i18n-ignore
    "画像を見てください", // i18n-ignore
)

private fun composerImagePaths(text: String): List<String> =
    composerImageTags.findAll(text).flatMap { it.groupValues[1].lineSequence() }
        .map { it.trim().removePrefix("path:").trim() }
        .filter { it.startsWith('/') || it.startsWith("~/") }.distinct().toList()

/** [text] with its photos as one [photo] mark ("[Image]"). */
internal fun composerMessagePreview(text: String, photo: String): String {
    val clean = text.replace(composerImagePlaceholders, "")
    val plain = clean.replace(composerImageTags, "").trim()
    return if (composerImagePaths(clean).isNotEmpty() && plain in composerPhotoStandIns) photo
        else clean.replace(composerImageTags, photo).trim()
}

@Composable
internal fun composerMessagePreview(text: String): String =
    composerMessagePreview(text, stringResource(R.string.chat_composer_photo_placeholder))
