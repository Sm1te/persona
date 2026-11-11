package persona.feature.chat

import android.content.Context
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.android.EntryPointAccessors
import dagger.hilt.android.qualifiers.ApplicationContext
import dagger.hilt.components.SingletonComponent
import persona.core.contracts.*
import persona.core.contracts.api.*
import persona.core.designsystem.PersonaTheme
import persona.core.domain.answer.*
import javax.inject.Inject
import javax.inject.Singleton
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.JsonObject

@Singleton
class RequestDraftStore @Inject constructor(@ApplicationContext context: Context, chats: ChatRepository, interactions: InteractionCenter) {
    private val preferences = context.getSharedPreferences("request-drafts-v1", Context.MODE_PRIVATE)
    private val journal = RequestDraftJournal(preferences::getStringValue) { key, value ->
        preferences.edit().apply { if (value == null) remove(key) else putString(key, value) }.apply()
    }
    private val lifetime = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    internal val storage = (chats as? ChatLocalStorage)?.localStorage ?: MutableStateFlow(null)
    internal val version = MutableStateFlow(0L)
    private val openedUrls = mutableMapOf<Pair<String?, Interaction>, String>()
    init {
        lifetime.launch {
            var previousScope: String? = null
            var previous = emptyList<Interaction>()
            combine(storage, interactions.pending) { local, pending -> local?.scope to pending }.collect { (scope, pending) ->
                if (previousScope != null && previousScope != scope) {
                    preferences.edit().clear().apply()
                    openedUrls.clear()
                    version.value++
                } else if (scope != null) {
                    previous.filter { old -> pending.none { it.id == old.id && it.sessionKey == old.sessionKey } }
                        .forEach { journal.clear(scope, it) }
                }
                previousScope = scope
                previous = pending
                openedUrls.keys.removeAll { (_, request) -> pending.none { it == request } }
            }
        }
    }
    internal fun load(scope: String, request: Interaction) = journal.load(scope, request)
    internal fun openedUrl(scope: String?, request: Interaction) = openedUrls[scope to request]
    internal fun recordOpenedUrl(scope: String?, request: Interaction, url: String?) {
        if (url != null && url == ElicitationUrl.target(request.elicitation?.url)) {
            openedUrls[scope to request] = url
            version.value++
        }
    }
    internal fun clear(scope: String, request: Interaction) {
        journal.clear(scope, request)
        version.value++
    }
    internal fun save(scope: String, request: Interaction, draft: AnswerDraft) {
        journal.save(scope, request, draft)
        version.value++
    }
}

private fun android.content.SharedPreferences.getStringValue(key: String) = getString(key, null)

@EntryPoint
@InstallIn(SingletonComponent::class)
interface RequestEditorServices {
    fun requestDrafts(): RequestDraftStore
    fun chats(): ChatRepository
}

/** Both request inbox and conversation use the same form, journal and validation. */
@Composable
fun RequestEditor(
    request: Interaction, machine: Machine? = null, busy: Boolean = false, uncertain: Boolean = false,
    full: Boolean = true, online: Boolean = true, onTerminal: (() -> Unit)? = null, onExpand: () -> Unit = {},
    readOnly: Boolean = false,
    linkVisible: Boolean = false,
    /** Why this session's requests cannot be answered from the phone (its source does not approve). */
    blockedReason: String? = null,
    /** False for a session without a terminal (a Codex thread, an agent runtime's): no "handle in terminal" note. */
    terminalHelp: Boolean = true,
    onAnswer: (AnswerAction, AnswerDraft, Boolean) -> Unit,
) {
    val context = LocalContext.current.applicationContext
    val services = remember(context) { EntryPointAccessors.fromApplication(context, RequestEditorServices::class.java) }
    val store = remember(services) { services.requestDrafts() }
    val local by store.storage.collectAsStateWithLifecycle()
    val version by store.version.collectAsStateWithLifecycle()
    val storageScope = local?.scope
    var transient by remember(storageScope, request) { mutableStateOf(AnswerDraft()) }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, request, storageScope) {
        val observer = LifecycleEventObserver { _, event -> if (event == Lifecycle.Event.ON_STOP) transient = AnswerDraft() }
        lifecycle.addObserver(observer)
        onDispose { transient = AnswerDraft(); lifecycle.removeObserver(observer) }
    }
    val persisted = if (storageScope == null) transient else remember(storageScope, request, version) { store.load(storageScope, request) }
    // Sensitive fields are local to the visible editor and are never part of the journal.
    val draft = (if (request.secret == true) transient else persisted.copy(fields = JsonObject(persisted.fields + transient.fields)))
        .copy(openedElicitationUrl = store.openedUrl(storageScope, request))
    val update: (AnswerDraft) -> Unit = { next ->
        store.recordOpenedUrl(storageScope, request, next.openedElicitationUrl)
        transient = if (request.secret == true || storageScope == null) next else AnswerDraft(fields = JsonObject(
            next.fields.filterKeys { key -> ElicitationForm.fields(request.elicitation?.requestedSchema).any { it.key == key && it.secret } }))
        if (storageScope != null) store.save(storageScope, request, next)
    }
    // A password card's (#1137) only typed value is its password, a fields card's its values (#4641): never a draft.
    if (storageScope == null && request.secret != true && !request.isSecretRun && !request.hasFieldsForm) Text(stringResource(R.string.chat_request_draft_page_only), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2)
    val items by remember(services, request.sessionKey, request.kind) {
        if (request.kind == "screen") services.chats().observe(request.sessionKey) else flowOf(ChatSnapshot())
    }
        .collectAsStateWithLifecycle(ChatSnapshot())
    InteractionForm(request, machine, draft, update, busy || !online, uncertain, full,
        { action, submitted, read ->
            storageScope?.let { store.clear(it, request) }
            transient = AnswerDraft()
            onAnswer(action, submitted, read)
        }, onTerminal, onExpand, items.items, readOnly, linkVisible, blockedReason = blockedReason, showTerminalHelp = terminalHelp)
}
