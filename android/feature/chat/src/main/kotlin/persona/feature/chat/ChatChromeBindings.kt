package persona.feature.chat

import persona.core.contracts.SessionKey
import persona.core.contracts.api.ChatActions
import persona.core.contracts.api.HubConnection
import persona.core.contracts.api.SessionFiles
import persona.core.domain.sort.SessionRenamer
import dagger.hilt.EntryPoint
import dagger.hilt.InstallIn
import dagger.hilt.components.SingletonComponent

/** The app supplies hub-scoped services and navigation without introducing feature-to-app dependencies. */
data class ChatChromeBindings(
    val covered: Boolean = false,
    val requestId: String? = null,
    val onFiles: (() -> Unit)? = null,
    val onGit: (() -> Unit)? = null,
    val gitLabel: String = "Git",
    val actions: ChatActions? = null,
    val connection: HubConnection? = null,
    /** Worktree status and removal for Close Agent / Delete Chat (#4532), by the group's own machine ids; null: none offered. */
    val worktrees: (() -> persona.core.contracts.api.AgentCreationService?)? = null,
    val onOpenSession: ((SessionKey) -> Unit)? = null,
    /**
     * The cards other agents wait on you with, in the Persona home's order, and how one opens: its chat with that card
     * first. With both, an agent's chat that has just answered its last card offers the next one (#4325).
     */
    val waiting: List<WaitingRequest> = emptyList(),
    val onOpenRequest: ((SessionKey, requestId: String) -> Unit)? = null,
    /** Opens an assistant conversation (a branch of the Persona conversation, web `forkAt`); null: assistant branches are not offered. */
    val onOpenAssistant: ((SessionKey) -> Unit)? = null,
    val onConnectionDetails: (() -> Unit)? = null,
    val renamer: SessionRenamer? = null,
    /** Read-only access to the session's machine: the photos sent with messages (`<attached_images>`) load from it. */
    val files: SessionFiles? = null,
    /** Opens the session's machine's ports on the phone (docs/operations/ports.md); null: port links open as plain pages. */
    val ports: persona.core.contracts.api.PortOpener? = null,
    /**
     * Opens a path from a message or a tool card in the session's files pane, scrolled to its line (`a.kt:12`) when
     * it names one; null: paths are not offered.
     */
    val onOpenPath: ((path: String, line: Int?) -> Unit)? = null,
    /** "Hand to Persona" on a message: hands the given text to the main Persona conversation; null: not offered. */
    val onHandToPersona: ((String) -> Unit)? = null,
    /** The assistant conversation's tasks (docs/assistant/unified-entry.md); null for an agent's chat. */
    val tasks: persona.core.domain.tasks.TasksStore? = null,
    val onOpenTodo: ((String) -> Unit)? = null,
    val assistantManagement: persona.core.contracts.api.AssistantManagement? = null,
    val inboxTrays: persona.core.contracts.api.AssistantInboxTrays? = null,
    val inboxScope: String? = null,
    val agentShares: (() -> persona.core.contracts.api.AgentShares)? = null,
    /** Opens what fixes a turn's model error ([TurnErrorFix]): the model's set-up, or the [launcher]'s provider; null: no button. */
    val onFixModel: ((action: persona.core.designsystem.ReadinessActionId, launcher: String?, manager: String?) -> Unit)? = null,
)

/** Where a branch made by "Branch to new chat" opens: an assistant's branch is an assistant conversation, an agent's an agent chat. */
internal fun ChatChromeBindings.forkOpener(style: ChatRenderStyle): ((SessionKey) -> Unit)? =
    if (style == ChatRenderStyle.Assistant) onOpenAssistant else onOpenSession

/** The page of [key]'s conversation; [assistant] opens it as an assistant conversation whatever the session list says yet. */
fun conversationRoute(key: SessionKey, assistant: Boolean = false) = persona.core.navigation.ChatRoute(
    key.machineId, key.sessionId, kind = if (assistant) persona.core.navigation.ConversationKind.Assistant else null)

@EntryPoint
@InstallIn(SingletonComponent::class)
interface ChatRenameServices {
    fun sessionRenamer(): SessionRenamer
}
