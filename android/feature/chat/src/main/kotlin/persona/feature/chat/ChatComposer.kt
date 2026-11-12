package persona.feature.chat

import android.Manifest
import android.content.ActivityNotFoundException
import android.graphics.Bitmap
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.focus.onFocusChanged
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalResources
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextOverflow
import androidx.core.content.FileProvider
import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatComposerService
import persona.core.contracts.api.ComposerOperationException
import persona.core.contracts.api.DraftUploadState
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.feature.chat.media.MediaShare
import java.io.File
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** How long an upload waits for the hub connection to be ready again before it fails (a setting, not measured). */
internal const val UPLOAD_READY_WAIT_MILLIS = 15_000L

@Composable
fun ChatComposer(
    session: SessionKey,
    state: ChatComposerState,
    working: Boolean,
    sending: Boolean,
    canClear: Boolean,
    service: ChatComposerService?,
    onSend: () -> Unit,
    onCommand: (String) -> Unit,
    onNotice: (String) -> Unit,
    // Content queued outside the text field (selected page context) also makes the draft sendable.
    extraContent: Boolean = false,
    enabled: Boolean = true,
    placeholder: String = stringResource(R.string.chat_composer_placeholder),
    leading: (@Composable RowScope.() -> Unit)? = null,
    accessory: (@Composable ColumnScope.() -> Unit)? = null,
    modeHint: String? = null,
    onOverlay: (Boolean) -> Unit = {},
    visible: Boolean = true,
    /** Who /clear restarts, for its confirmation ("Claude will start a new session…"). */
    agentName: String = "Claude",
    /** False for a session whose source does not let the phone interrupt it: no stop while it works. */
    canInterrupt: Boolean = true,
    /** The @ menu's files and sessions (iOS PromptItems); null: no @ button and no list while typing @. */
    mentions: ComposerMentions? = null,
    /** A session picked in the @ menu, as the context that goes with the next message. */
    onMention: (ChatLinkContext) -> Unit = {},
    /** /model in the "/" list, which opens the model choices instead of typing (Claude in a terminal pane). */
    onModel: (() -> Unit)? = null,
    /** /loop in the "/" list, with a line under the box saying what it will do (Claude Code only, web `hasLoop`). */
    loop: Boolean = false,
    /**
     * What has to happen before anything can be sent ("Connect a model first", "Add a computer first": docs/assistant/onboarding.md 4.4): the send button says
     * it and [onBlocked] opens that step. The box still takes typing and keeps the draft; nothing is queued.
     */
    blockedLabel: String? = null,
    onBlocked: () -> Unit = {},
) {
    val context = LocalContext.current
    val resources = LocalResources.current
    val photosOnly = stringResource(R.string.chat_composer_photos_only)
    SideEffect { state.photosOnlyText = photosOnly }
    val hasContent = state.hasContent || extraContent
    val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }
    var confirmClear by remember { mutableStateOf(false) }
    var cameraDenied by remember { mutableStateOf(false) }
    val focusRequester = remember { androidx.compose.ui.focus.FocusRequester() }
    val keyboard = LocalSoftwareKeyboardController.current
    // A photo in the draft being marked up (iOS editingIndex); [editOpening] while it is read for the editor.
    var editing by remember { mutableStateOf<ComposerPhotoEdit?>(null) }
    var editOpening by remember { mutableStateOf(false) }
    // The draft full screen (#5688): offered once the field has grown to its line limit and scrolls.
    var expandedEditor by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(menu, confirmClear, cameraDenied, editing, expandedEditor) {
        onOverlay(menu || confirmClear || cameraDenied || editing != null || expandedEditor)
    }
    DisposableEffect(Unit) { onDispose { onOverlay(false) } }
    var cameraUri by rememberSaveable { mutableStateOf<String?>(null) }
    var interruptPending by rememberSaveable { mutableStateOf(false) }
    LaunchedEffect(working) { if (!working) interruptPending = false }
    // A photo taken here waits in cache/chat-captures until it leaves this draft: removed, or sent and cleared. Only the
    // draft's own captures go; the folder is never swept (another conversation's draft may still hold one).
    LaunchedEffect(state) {
        var before = state.attachments to state.photoEdits.values.map { it.sourceUri }
        snapshotFlow { state.attachments to state.photoEdits.filterKeys { id -> state.attachments.any { it.id == id } }.values.map { it.sourceUri } }.collect { now ->
            val gone = leftCaptures(before.first, now.first, context.packageName, before.second, now.second)
            before = now
            if (gone.isNotEmpty()) withContext(Dispatchers.IO) { gone.forEach { deleteCapture(context, it) } }
        }
    }

    fun select(uris: List<Uri>, image: Boolean, capture: Boolean = false) {
        scope.launch {
            val allowed = if (image) (4 - state.attachments.count { it.image }).coerceAtLeast(0) else uris.size
            if (uris.size > allowed) onNotice(resources.getString(R.string.chat_composer_photo_limit))
            if (capture) withContext(Dispatchers.IO) { uris.drop(allowed).forEach { deleteCapture(context, it.toString()) } }
            for (uri in uris.take(allowed)) {
                try {
                    val attachment = withContext(Dispatchers.IO) { selectAttachment(context, uri, image) }
                    state.beginEdit()
                    state.attachments += attachment
                    attachment.error?.let(onNotice)
                } catch (e: CancellationException) { throw e }
                catch (_: Exception) {
                    if (capture) withContext(Dispatchers.IO) { deleteCapture(context, uri.toString()) }
                    onNotice(resources.getString(R.string.chat_composer_attachment_unreadable))
                }
            }
        }
    }
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickMultipleVisualMedia(4)) { select(it, true) }
    val files = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { select(it, false) }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { success ->
        cameraUri?.let { value ->
            if (success) select(listOf(Uri.parse(value)), true, capture = true)
            else runCatching { context.contentResolver.delete(Uri.parse(value), null, null) }
        }
        cameraUri = null
    }
    fun openCamera() {
        val folder = File(context.cacheDir, "chat-captures").apply { mkdirs() }
        val file = File.createTempFile("camera-", ".jpg", folder)
        val uri = FileProvider.getUriForFile(context, "${context.packageName}.chat.files", file)
        cameraUri = uri.toString()
        try { camera.launch(uri) }
        catch (error: Exception) {
            file.delete(); cameraUri = null
            onNotice(resources.getString(if (error is ActivityNotFoundException) R.string.chat_composer_no_camera_app else R.string.chat_composer_camera_failed))
        }
    }
    // The app declares CAMERA (pairing scans codes), so Android starts another app's camera for it only once granted.
    val cameraPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (granted) openCamera() else cameraDenied = true
    }
    fun takePhoto() {
        menu = false
        if (cameraAllowed(context)) openCamera()
        else try { cameraPermission.launch(Manifest.permission.CAMERA) } catch (_: Exception) { cameraDenied = true }
    }
    fun launchPicker(action: () -> Unit) {
        menu = false
        try { action() } catch (_: Exception) { onNotice(resources.getString(R.string.chat_composer_picker_failed)) }
    }

    // A tap on a photo that is on the computer: the editor opens on it, and "Done" puts the marked-up copy in its place.
    fun editPhoto(photo: ComposerAttachment) {
        if (editing != null || editOpening || sending) return
        editOpening = true
        scope.launch {
            try {
                // An edited photo opens from its source with its edits, still undoable; its source gone, it starts over on itself.
                val prior = state.photoEdits[photo.id]
                val source = prior?.let { runCatching { withContext(Dispatchers.IO) { decodePhoto(context, Uri.parse(it.sourceUri)) } }.getOrNull() }
                editing = if (prior != null && source != null) ComposerPhotoEdit(photo, source, prior.sourceUri, prior)
                    else ComposerPhotoEdit(photo, withContext(Dispatchers.IO) { decodePhoto(context, Uri.parse(photo.uri)) })
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { onNotice(resources.getString(R.string.chat_attach_edit_unreadable)) }
            finally { editOpening = false }
        }
    }
    // "Done": the editor spins while the new photo is written (JPEG like a picked photo) and uploaded, then closes. Only an
    // upload that worked replaces the original; the old path leaves the message with it.
    fun finishEdit(edit: ComposerPhotoEdit, result: Bitmap, done: ImageEditState) {
        if (result === edit.source && edit.prior == null) { editing = null; return } // nothing done to a photo never edited
        scope.launch {
            var edited: ComposerAttachment? = null
            var kept = false
            try {
                edited = MediaShare.writeEditedPhoto(context, result)
                val carried = PhotoEdits(edit.sourceUri, done).takeUnless { done.edits.isEmpty }
                val outcome = state.replaceWithEdited(edit.original, edited, carried) { photo ->
                    uploadAttachment(context, checkNotNull(service), session, photo)
                }
                kept = outcome == PhotoEditOutcome.Replaced
                if (outcome is PhotoEditOutcome.Failed) onNotice(resources.getString(R.string.chat_attach_edit_failed))
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { onNotice(resources.getString(R.string.chat_attach_edit_failed)) }
            finally {
                // A copy that did not make it into the draft is not left in cache/chat-captures.
                if (!kept) edited?.let { withContext(NonCancellable + Dispatchers.IO) { deleteCapture(context, it.uri) } }
                if (editing === edit) editing = null
            }
        }
    }

    state.attachments.forEach { attachment ->
        key(attachment.id) {
            LaunchedEffect(attachment.id, attachment.error, service) {
                if (attachment.uploadState == DraftUploadState.Invalid) return@LaunchedEffect
                if (!withContext(Dispatchers.IO) { attachmentReadable(context, attachment) }) {
                    val reason = resources.getString(R.string.chat_composer_attachment_gone)
                    state.attachments = state.attachments.map {
                        if (it.id == attachment.id) it.copy(uploadState = DraftUploadState.Invalid, error = reason) else it
                    }
                    onNotice(reason)
                    return@LaunchedEffect
                }
                if (attachment.path != null) return@LaunchedEffect
                if (attachment.error != null || service == null) return@LaunchedEffect
                try {
                    state.attachments = state.attachments.map {
                        if (it.id == attachment.id) it.copy(uploadState = DraftUploadState.Uploading) else it
                    }
                    val path = uploadAttachment(context, service, session, attachment)
                    if (state.attachments.any { it.id == attachment.id }) {
                        state.attachments = state.attachments.map {
                            if (it.id == attachment.id) it.copy(path = path, uploadState = DraftUploadState.Uploaded) else it
                        }
                    }
                } catch (e: CancellationException) { throw e }
                catch (e: Exception) {
                    val reason = (e as? ComposerOperationException)?.message ?: resources.getString(R.string.chat_composer_upload_failed)
                    state.attachments = state.attachments.map {
                        if (it.id == attachment.id) it.copy(error = reason, uploadState = DraftUploadState.Failed) else it
                    }
                }
            }
        }
    }

    LaunchedEffect(visible) { if (!visible) { menu = false; confirmClear = false; cameraDenied = false; editing = null; expandedEditor = false } }
    editing?.let { edit ->
        var done by remember(edit) { mutableStateOf(ImageEditState()) }
        key(edit) { ImageEditor(edit.source, onCancel = { if (editing === edit) editing = null }, onDone = { finishEdit(edit, it, done) },
            start = edit.prior?.state ?: ImageEditState(), onEdited = { done = it }) }
    }
    // Voice input: what the phone's recognizer heard goes at the end of the draft, to edit before sending.
    val dictation = rememberDictation(onHeard = { state.appendDictation(it) }, onError = onNotice)
    if (!visible) return
    LaunchedEffect(state.focusRequest) { if (state.focusRequest > 0) focusRequester.requestFocus() }
    // A read-only session (enabled = false) offers nothing to type with: no +, no mic, no "/" list, no send.
    val dictate = dictation?.takeIf { enabled }
    val suggestions = if (enabled) SlashCommand.suggestions(state.value.text, canClear && !working, model = onModel != null, loop = loop) else emptyList()
    val hint = if (enabled && loop) loopHint(state.value.text, resources) else null
    // @: the word being typed just before the caret, while nothing is selected and no "/" list is open.
    val selection = state.value.selection
    val mention = if (enabled && mentions != null && suggestions.isEmpty() && selection.collapsed) mentionAt(state.value.text, selection.start) else null
    val mentionRows = remember(mentions, mention?.query) { if (mentions != null && mention != null) mentionRows(mentions, mention.query) else emptyList() }
    val usedFile = stringResource(R.string.chat_mention_used_file)
    val mentionItems = mentionRows.map { row ->
        when (row) {
            is MentionRow.File -> {
                val relative = row.insert.removePrefix("@").trimEnd()
                ComposerSuggestion(relative.substringAfterLast('/'), relative.substringBeforeLast('/', "").ifEmpty { usedFile }, key = row.key, flexible = true)
            }
            is MentionRow.Quote -> ComposerSuggestion(row.session.displayTitle,
                stringResource(R.string.chat_mention_session_detail, sessionMentionDetail(row.session)), key = row.key, flexible = true)
        }
    }
    fun pickMention(picked: ComposerSuggestion) {
        val token = mention ?: return
        val row = mentionRows.firstOrNull { it.key == picked.key } ?: return
        state.beginEdit()
        when (row) {
            is MentionRow.File -> state.value = replaceMention(state.value, token, row.insert)
            // A session goes with the message as a quote, not as text (web pickMention).
            is MentionRow.Quote -> { onMention(sessionQuote(row.session, resources)); state.value = replaceMention(state.value, token, "") }
        }
        state.focusRequest++
    }
    val extensionPrompts = if (enabled) LocalExtensionPrompts.current else emptyList()
    val quick = ExtensionPromptText.matching(extensionPrompts, state.value.text)
    // Picking a quick message puts its words in the box to send; /clear empties the box and asks first.
    fun pick(command: SlashCommand) {
        state.beginEdit()
        when {
            command.clear -> { state.value = TextFieldValue(); confirmClear = true }
            command.model -> { state.value = TextFieldValue(); onModel?.invoke() }
            else -> state.value = TextFieldValue(command.text, TextRange(command.text.length))
        }
    }

    // The field grows with the draft up to growLines (from the height above the keyboard), then scrolls inside and
    // shows Expand at its top right; the text keeps clear of the button only while it shows.
    val growLines = rememberComposerGrowLines()
    var lineCount by remember { mutableIntStateOf(1) }
    val offersExpand = enabled && composerOffersExpand(lineCount, growLines)
    LaunchedEffect(enabled) { if (!enabled) expandedEditor = false }
    val canSend = enabled && state.ready && !sending
    val homeAssistant = LocalHomeChrome.current?.let { it.items.firstOrNull()?.key == it.selected } == true
    val fullToolbar = !homeAssistant || state.focused || hasContent
    // Expand once on focus, not when the first character arrives. ComposerFrame keeps the field in the same row across
    // this change, so moving the controls cannot dispose the field and cancel the tap or the IME.
    val expanded = state.focused || hasContent
    val composerPlaceholder = if (homeAssistant) stringResource(R.string.chat_composer_ask_persona, LocalPersonaName.current) else placeholder

    ComposerFrame(
        // Blank space around the field (its padding, beside the buttons) types too: one tap, not a hunt for the line.
        onBlankTap = { if (enabled) { focusRequester.requestFocus(); keyboard?.show() } },
        expanded = expanded,
        leading = {
            JIconButton(PhIcons.Plus, stringResource(R.string.chat_composer_add), { menu = true }, enabled = !sending && enabled)
            if (expanded) {
                if (mentions != null) JIconButton(PhIcons.At, stringResource(R.string.chat_mention), {
                    state.beginEdit(); state.value = typeMention(state.value); state.focusRequest++
                }, enabled = enabled)
                leading?.invoke(this)
            }
        },
        trailing = {
            // The mic stays (iOS ShellComposerBar): more can be dictated after what is typed. Send beside it once there is
            // something to send, else a stop while the agent works; nothing else while the box is empty.
            dictate?.let { JIconButton(PhIcons.Microphone, stringResource(R.string.chat_composer_dictate), it, enabled = !sending) }
            when {
                blockedLabel != null && enabled -> BlockedSendButton(blockedLabel, onBlocked)
                hasContent -> SendButton(onClick = onSend, enabled = canSend, loading = sending)
                working && canInterrupt -> {
                    SendButton(onClick = {
                        if (service != null) {
                            interruptPending = true
                            scope.launch {
                                try { service.interrupt(session); onNotice(resources.getString(R.string.chat_composer_interrupt_requested)) }
                                catch (e: CancellationException) { throw e }
                                catch (_: Exception) { onNotice(resources.getString(R.string.chat_composer_interrupt_unknown)) }
                            }
                        }
                    }, enabled = !sending && !interruptPending && service != null, kind = SendKind.Stop)
                }
                dictate == null -> SendButton(onClick = onSend, enabled = false, loading = sending)
            }
        },
        accessory = {
            if (state.oldVersion) Text(stringResource(R.string.chat_composer_old_version), style = PersonaTheme.type.caption,
                color = PersonaTheme.palette.cautionText)
            ComposerAttachmentPreviews(state, service, sending, enabled, onEdit = ::editPhoto)
            if (fullToolbar) accessory?.invoke(this)
            if (fullToolbar) modeHint?.let { JChip(it, icon = PhIcons.SlidersHorizontal) }
            if (working && hasContent && enabled) Text(stringResource(R.string.chat_composer_will_queue), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            // A draft that is one "/" word: the quick messages it can be, right above the field.
            ComposerSuggestions(quick.map { ComposerSuggestion(it.title, it.extensionName, key = "${it.extension}/${it.id}", flexible = true) },
                onPick = { picked -> quick.firstOrNull { "${it.extension}/${it.id}" == picked.key }?.let { state.fillQuickPrompt(it) } },
                modifier = Modifier.padding(vertical = Tokens.Space.xs))
            ComposerSuggestions(suggestions.map { ComposerSuggestion(it.word, it.detail, destructive = it.clear) },
                onPick = { picked -> suggestions.firstOrNull { it.word == picked.word }?.let(::pick) },
                modifier = Modifier.padding(vertical = Tokens.Space.xs))
            // The @ menu: files this chat used, then other sessions; a word that matches nothing says so.
            ComposerSuggestions(mentionItems, onPick = ::pickMention, modifier = Modifier.padding(vertical = Tokens.Space.xs))
            if (mention != null && mentionItems.isEmpty()) Text(stringResource(R.string.chat_mention_no_match),
                Modifier.padding(vertical = Tokens.Space.xs), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
            hint?.let { Text(it.text, style = PersonaTheme.type.caption, color = if (it.missing) PersonaTheme.palette.cautionText else PersonaTheme.palette.ink2) }
        },
    ) {
        BasicTextField(state.value, { state.beginEdit(); state.value = it },
            modifier = Modifier.fillMaxWidth().then(if (offersExpand) Modifier.padding(end = ComposerExpandInset) else Modifier)
                .focusRequester(focusRequester).onFocusChanged { state.focused = it.isFocused },
            maxLines = growLines, onTextLayout = { lineCount = it.lineCount },
            enabled = enabled, textStyle = PersonaTheme.type.body.copy(color = PersonaTheme.palette.ink),
            cursorBrush = SolidColor(PersonaTheme.palette.ink),
            decorationBox = { field ->
                Box {
                    if (state.value.text.isEmpty()) Text(if (working && enabled && !homeAssistant) stringResource(R.string.chat_composer_placeholder_working) else composerPlaceholder,
                        style = PersonaTheme.type.body, color = PersonaTheme.palette.ink3, maxLines = 2, overflow = TextOverflow.Ellipsis)
                    field()
                }
            })
        if (offersExpand) ComposerExpandButton(expanded = false, onClick = { expandedEditor = true }, modifier = Modifier.align(Alignment.TopEnd))
    }
    // Same draft and caret as the field; collapsing puts the caret back in the field. Sending closes it.
    if (expandedEditor) ExpandedComposer(state.value, { state.beginEdit(); state.value = it }, composerPlaceholder,
        onCollapse = { expandedEditor = false; state.focusRequest++ }, enabled = enabled) {
        SendButton(onClick = { expandedEditor = false; onSend() }, enabled = canSend && hasContent && blockedLabel == null, loading = sending)
    }
    if (menu) ChatActionsSheet(onDismiss = { menu = false }, title = stringResource(R.string.chat_composer_menu_title), actions = buildList {
        add(JAction(stringResource(R.string.chat_composer_photos), enabled = service != null && state.attachments.count { it.image } < 4) {
            launchPicker { photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }
        })
        add(JAction(stringResource(R.string.chat_composer_take_photo), enabled = service != null && state.attachments.count { it.image } < 4) { takePhoto() })
        add(JAction(stringResource(R.string.chat_composer_files), enabled = service != null) { launchPicker { files.launch(arrayOf("*/*")) } })
        // Quick messages go out at once; /clear asks first, and only while nothing is running or being cleared.
        extensionPrompts.forEach { prompt -> add(JAction(prompt.title) { menu = false; state.fillQuickPrompt(prompt) }) }
        SlashCommand.all(resources).forEach { command -> add(JAction(command.title) { menu = false; onCommand(command.text) }) }
        if (canClear && !working) add(JAction(SlashCommand.clearCommand(resources).title, destructive = true) { menu = false; confirmClear = true })
    })
    if (confirmClear) JDialog(onDismiss = { confirmClear = false }, title = stringResource(R.string.chat_composer_clear_title),
        text = stringResource(R.string.chat_composer_clear_text, agentName),
        confirm = stringResource(R.string.chat_composer_clear) to { confirmClear = false; onCommand(SlashCommand.CLEAR) }, destructive = true)
    if (cameraDenied) JDialog(onDismiss = { cameraDenied = false }, title = stringResource(R.string.chat_composer_camera_title),
        text = stringResource(R.string.chat_composer_camera_text, appLabel(context)),
        confirm = stringResource(R.string.chat_composer_open_settings) to { cameraDenied = false; openAppSettings(context) })
}
