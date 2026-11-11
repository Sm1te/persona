package persona.core.contracts.api

import persona.core.contracts.SessionKey
import kotlinx.coroutines.flow.StateFlow
import kotlinx.serialization.json.JsonObject

/** An item of a bulk "Done": its id and the `version` it was listed at (null: as it is now). */
data class InboxDoneTarget(val id: String, val version: String? = null)

/**
 * What a bulk request does (`POST /api/v1/inbox/bulk`): done (a section's Mark all as seen / Clear all), dismiss (Clean up all, recorded as
 * done_by=dismissed), reopen (Undo).
 */
enum class InboxBulkAction(val wire: String) { Done("done"), Dismiss("dismiss"), Reopen("reopen") }

/** The suggestion acted on is no longer the current one (`409 suggestion_stale`); [item] is the item as it is now. */
class InboxSuggestionStale(val item: JsonObject?) : IllegalStateException("suggestion_stale")

/** One immutable pairing owns a transport and all its requests, including media. */
interface InboxAssistantTransport {
    val scope: String
    suspend fun inbox(): JsonObject
    suspend fun larkConversations(cursor: String? = null): JsonObject = throw UnsupportedOperationException("Conversation metadata unavailable")
    suspend fun larkConversationsForAccount(account: Int?, cursor: String? = null): JsonObject {
        require(account == null) { "Account-specific conversation metadata unavailable" }
        return larkConversations(cursor)
    }
    suspend fun setDone(itemId: String, done: Boolean): JsonObject
    /**
     * Many items done (or back) in one request (`POST /api/v1/inbox/done`): each with the version it was listed at,
     * so one that took new messages since stays open. Answers `{changed: [items], unchanged: [ids], skipped: [{id, reason}]}`.
     */
    suspend fun setDone(items: List<InboxDoneTarget>, done: Boolean): JsonObject =
        throw UnsupportedOperationException("Bulk done unavailable")
    /**
     * [action] on many items in one request (`POST /api/v1/inbox/bulk`), each with the version it was listed at.
     * Answers what [setDone] does, plus `ids` (the changed ones) and `undo`. Without it, all go the old way ("Dismiss" as "Done").
     */
    suspend fun bulk(items: List<InboxDoneTarget>, action: InboxBulkAction): JsonObject =
        setDone(items, action != InboxBulkAction.Reopen)
    /**
     * One page of [manager]'s done history (`GET /api/v1/inbox/done`, #2200; null: the default manager): newest done
     * first, from after the entry [before] / [beforeId] (both null: the newest), answering `{items, next?: {before, before_id}}`.
     * A cursor is one manager's, so each is asked on its own. Old hubs: 404.
     */
    suspend fun doneHistory(manager: String?, before: String?, beforeId: String?, limit: Int = 50): JsonObject =
        throw UnsupportedOperationException("Done history unavailable")
    /**
     * [manager]'s done items per day (`GET /api/v1/inbox/done/days?tz=&sources=all`, #2967) in time zone [tz] (an IANA
     * name), answering `{tz, days: [{day, count}]}`, newest first. Old hubs: 404; a zone the hub doesn't know: 400.
     */
    suspend fun doneDays(manager: String?, tz: String): JsonObject = throw UnsupportedOperationException("Done days unavailable")
    suspend fun reply(itemId: String?, feedId: String?, text: String, clientId: String): JsonObject
    /** Hands an item to its manager's main chat (`POST /api/v1/inbox/{id}/handoff`), answering `{item, session}`. */
    suspend fun handOff(itemId: String, text: String, clientId: String): JsonObject =
        throw UnsupportedOperationException("Inbox hand-off route unavailable")
    /**
     * Acts on an item's suggestion (`POST /api/v1/inbox/{id}/suggestion`, inbox phase 1): [action] is confirm, reject or
     * instruct, [version] the suggestion's as shown, [text] the edited text (confirm) or the words (instruct). Answers
     * the item; throws [InboxSuggestionStale] with the current item when the hub has a newer suggestion (409).
     */
    suspend fun suggestion(itemId: String, version: Long, action: String, text: String?, clientId: String): JsonObject =
        throw UnsupportedOperationException("Inbox suggestions unavailable")
    suspend fun replyMail(itemId: String, text: String, clientId: String, target: MailReplyTarget): JsonObject =
        throw UnsupportedOperationException("Checked mail replies unavailable")
    suspend fun messages(feedId: String, around: Long? = null, before: String? = null, after: String? = null): JsonObject
    /** One Feishu message (`GET /api/v1/lark/messages/{id}`, `{account?, message}`): the outbox confirms delivery with it. */
    suspend fun larkMessage(messageId: String): JsonObject = throw UnsupportedOperationException("Message lookup unavailable")
    suspend fun assistant(): JsonObject
    suspend fun createConversation(main: Boolean): JsonObject
    /** A conversation that runs on [launcher] (POST /api/v1/assistant/conversations `launcher`), not the default one. */
    suspend fun createConversation(main: Boolean, launcher: String): JsonObject =
        throw UnsupportedOperationException("Picking a launcher unavailable")
    /**
     * The chats of the assistant's [space] (`GET /api/v1/assistant?space=`, docs/assistant/health.md), answering
     * `{machine_id, conversations}` as [assistant] does. A space is the default manager's: no manager is named.
     */
    suspend fun assistantIn(space: String): JsonObject = throw UnsupportedOperationException("Assistant spaces unavailable")
    /**
     * A new chat in the assistant's [space] (`POST /api/v1/assistant/conversations` with `space`, never `main`: a space
     * has no main chat), answering `{session}`.
     */
    suspend fun createConversationIn(space: String): JsonObject = throw UnsupportedOperationException("Assistant spaces unavailable")
    /**
     * [assistant] of one manager (null: the default) whatever the assistant page shows meanwhile (docs/assistant/managers.md
     * step 4): what a message was written for is where it goes.
     */
    suspend fun assistantOf(manager: String?): JsonObject = assistant()
    /**
     * The directions of one manager (null: the default), `GET /api/v1/assistant/leaders`: `{leaders: [{id, name, ended?}]}`
     * (#3519, #3695). A transport without it answers none, and the drawer shows the leaders' titles.
     */
    suspend fun assistantLeadersOf(manager: String?): JsonObject = JsonObject(emptyMap())
    /**
     * The direction tree of the default manager (`GET /api/v1/assistant/directions/tree`, #4352): `{directions: [{id,
     * name, secretary?, counts: {working, needs_you, stuck, review}}]}`, the current directions in the leaders' order.
     * The hub answers it for the default manager alone. A transport without it says so (as an older hub's 404 does), and
     * the drawer keeps the flat direction rows.
     */
    suspend fun directionsTreeOf(manager: String?): JsonObject =
        throw UnsupportedOperationException("Direction tree unavailable")
    /** [createConversation] for one manager (null: the default), as [assistantOf]. */
    suspend fun createConversationOf(manager: String?, main: Boolean, launcher: String? = null): JsonObject =
        if (launcher == null) createConversation(main) else createConversation(main, launcher)
    suspend fun pins(): JsonObject
    suspend fun mutes(): JsonObject
    suspend fun media(ref: String): ByteArray
    /**
     * A person's, chat's or bot's picture: like [media] for one on the hub, while a bot's is a full https URL on
     * Feishu's CDN, which the hub transport reads directly without the pairing's credentials.
     */
    suspend fun avatar(ref: String): ByteArray = media(ref)
    suspend fun readDocument(url: String): JsonObject = throw UnsupportedOperationException("Document reader unavailable")
    suspend fun documentMedia(ref: String): ByteArray = media(ref)
    /**
     * What the reader shows around a document (`/api/v1/lark/raw/documents/<path>?url=…`): [path] is `reader/info`
     * (owner, counts), `header` (who edited it last, when) or `ai-summary` (Feishu's cached summary).
     */
    suspend fun documentExtra(path: String, url: String): JsonObject = throw UnsupportedOperationException("Document details unavailable")
    /** A document's comment threads (`GET /api/v1/lark/raw/documents/comments?url=`, `{threads:[…]}`). */
    suspend fun documentComments(url: String): JsonObject = throw UnsupportedOperationException("Document comments unavailable")
    /**
     * The user's own write on a document's comments (#1290; the plugin's `lark_doc_comments`): [thread] null posts a
     * new comment (`POST /api/v1/lark/documents/comments`), else a reply to that thread (`…/{thread}/replies`).
     * [body] is `{url, content, mentions, quote?, block_id?, client_id}`; answers `{ok, threads}`.
     */
    suspend fun writeDocumentComment(thread: String?, body: JsonObject): JsonObject =
        throw UnsupportedOperationException("Document comments unavailable")
    /** Resolves a thread or reopens it (`POST /api/v1/lark/documents/comments/{thread}/resolve {url, resolved}`). */
    suspend fun resolveDocumentComment(thread: String, url: String, resolved: Boolean): JsonObject =
        throw UnsupportedOperationException("Document comments unavailable")
    /** People to @ in a document's comment, by name (`GET /api/v1/lark/documents/people?url=&q=`, `{members:[…]}`). */
    suspend fun documentPeople(url: String, query: String): JsonObject = throw UnsupportedOperationException("Document people unavailable")
    suspend fun momentsPost(postId: String): JsonObject = throw UnsupportedOperationException("Moments post unavailable")
    suspend fun momentsComments(postId: String): JsonObject = throw UnsupportedOperationException("Moments comments unavailable")
    suspend fun urlPreview(messageId: String, url: String): JsonObject = throw UnsupportedOperationException("URL preview unavailable")

    // Mute (docs/assistant/inbox.md "Muting"): an item's repository, chat or sender, in every source.
    /** `GET /api/v1/inbox/mutes`: `{mutes: [{id, source, source_name, tag, value, name, created_at}]}`. */
    suspend fun inboxMutes(): JsonObject = throw UnsupportedOperationException("Inbox mutes unavailable")
    /** `POST /api/v1/inbox/mutes` `{item_id, tag}`: `{mute, settled}`. */
    suspend fun muteInbox(itemId: String, tag: String): JsonObject = throw UnsupportedOperationException("Inbox mutes unavailable")
    /** `DELETE /api/v1/inbox/mutes/{id}`. */
    suspend fun unmuteInbox(id: String): Unit = throw UnsupportedOperationException("Inbox mutes unavailable")

    // Policies (docs/assistant/inbox.md "Policies"): the tier and AI twin of what an item has under one of its mute options.
    /** `GET /api/v1/inbox/policies`: `{policies: [{id, source, source_name, tag, value, name, tier?, twin?, created_at, updated_at}]}`. */
    suspend fun inboxPolicies(): JsonObject = throw UnsupportedOperationException("Inbox policies unavailable")
    /** `POST /api/v1/inbox/policies` `{item_id, tag, tier, twin}`: `{policy, applied}`; setting neither part removes it. */
    suspend fun setInboxPolicy(itemId: String, tag: String, tier: String, twin: String): JsonObject =
        throw UnsupportedOperationException("Inbox policies unavailable")
    /** `PATCH /api/v1/inbox/policies/{id}` `{tier, twin}`: `{policy, applied}`; setting neither part removes it. */
    suspend fun updateInboxPolicy(id: String, tier: String, twin: String): JsonObject =
        throw UnsupportedOperationException("Inbox policies unavailable")
    /** `DELETE /api/v1/inbox/policies/{id}`. */
    suspend fun removeInboxPolicy(id: String): Unit = throw UnsupportedOperationException("Inbox policies unavailable")

    // A Feishu chat's pinned messages (iOS `LarkChatPins`): reading the list marks the pins read upstream.
    /** `GET /api/v1/lark/conversations/{feed}/pins?limit=50&account_id=&cursor=`. */
    suspend fun larkChatPins(feedId: String, accountId: String?, cursor: String?): JsonObject =
        throw UnsupportedOperationException("Chat pins unavailable")
    /** `GET /api/v1/lark/conversations/{feed}/pin-messages/{message}?account_id=&topic=`: where a pin's message is. */
    suspend fun larkPinTarget(feedId: String, messageId: String, accountId: String, topic: Boolean): JsonObject =
        throw UnsupportedOperationException("Chat pins unavailable")
    /** `POST /api/v1/lark/conversations/{feed}/pins` `{account_id, message_id, pin_id, remove}`: `{account_id, pinned}`. */
    suspend fun larkChangePin(feedId: String, messageId: String, pinId: String?, accountId: String): JsonObject =
        throw UnsupportedOperationException("Chat pins unavailable")
    /** `POST /api/v1/lark/conversations/{feed}/read-states` `{message_ids}`: receipts of my messages. */
    suspend fun larkReadStates(feedId: String, messageIds: List<String>): JsonObject =
        throw UnsupportedOperationException("Read receipts unavailable")
}

interface InboxAssistantConnection {
    val current: StateFlow<InboxAssistantTransport?>
}

interface AssistantConversations : AssistantRepository {
    /**
     * Manager [manager]'s main conversation, made if it has none (docs/assistant/managers.md step 4): a message written while it
     * was on the page goes there even if the page moved on since. Fails rather than answer another manager's.
     */
    suspend fun mainConversationOf(manager: String): SessionKey = throw UnsupportedOperationException("Managers unavailable")
    val pinned: StateFlow<Set<SessionKey>>
    suspend fun startConversation(): SessionKey
    /** A new conversation that runs on [launcher], for it alone (iOS StarterComposer's model menu): the default stays. */
    suspend fun startConversation(launcher: String): SessionKey = throw UnsupportedOperationException("Picking a launcher unavailable")
    /**
     * The chats of the assistant's [space] ([AssistantSpace]), read from the hub each time: they are never in [assistant]'s
     * list. Each carries [space] even from a hub that leaves it out of this list.
     */
    suspend fun spaceConversations(space: String): List<persona.core.contracts.Session> =
        throw UnsupportedOperationException("Assistant spaces unavailable")
    /** A new chat in the assistant's [space], never its main one: what the space's new-chat page sends first starts it. */
    suspend fun startConversationIn(space: String): SessionKey = throw UnsupportedOperationException("Assistant spaces unavailable")
}
