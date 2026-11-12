package persona.core.contracts.api

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable

/**
 * GET /api/v1/assistant without the conversations: which machine the assistant runs on, with which launcher, in
 * which directory, and what it is told about the user (docs/protocol/protocol.md, docs/assistant/assistant.md).
 */
@Serializable
data class AssistantConfig(
    @SerialName("machine_id") val machineId: String = "",
    val launcher: String = "",
    /** The assistant directory on that machine; empty while the machine has not reported its home. */
    val cwd: String = "",
    /** The IANA zone routines and the assistant's sense of "today" use; null until a phone has set one. */
    val timezone: String? = null,
    /**
     * The user picked [timezone] by hand (#1855): devices leave it until Match phone is turned on again (false). null from
     * a hub never told, or one from before the hub kept it.
     */
    @SerialName("timezone_picked") val timezonePicked: Boolean? = null,
    /** What the user wrote in the app; null while nothing was saved there and hub.json's notes apply. */
    val notes: String? = null,
    /** What the assistant is actually told: [notes], else hub.json's. */
    @SerialName("notes_effective") val notesEffective: String = "",
    /** The main conversation's session id; it lives on [machineId]. */
    val main: String? = null,
    /**
     * "Notify only in the main chat": tasks the main conversation sends out stay out of pushes and the badge (the main conversation
     * tells you instead); null from a hub without the setting.
     */
    @SerialName("main_tasks_quiet") val mainTasksQuiet: Boolean? = null,
    /**
     * "Fold background rounds" (#4314): the main conversation's rounds another conversation, a scheduled task or a
     * follow-up started show as one line; null from a hub without the setting (see [backgroundFolded]).
     */
    @SerialName("main_background_folded") val mainBackgroundFolded: Boolean? = null,
    /**
     * "The main chat supervises its own tasks" (#4927, docs/assistant/unified-entry.md "Background supervisor"): off by
     * default, when the background supervisor follows up the tasks no leader supervises; null from a hub without it.
     */
    @SerialName("main_supervises_own_tasks") val mainSupervisesOwnTasks: Boolean? = null,
    /**
     * The background supervisor's conversation (its session id, on the assistant's machine) once the hub has made it;
     * read-only. Null until then and from a hub without it.
     */
    @SerialName("background_supervisor") val backgroundSupervisor: String? = null,
    /** Stable owner returned by hubs supporting per-assistant settings. */
    val manager: String? = null,
    @SerialName("default_permission_mode") val defaultPermissionMode: String? = null,
    @SerialName("permission_defaults") val permissionDefaults: AssistantPermissionDefaults? = null,
) {
    /** Whether the main conversation folds its background rounds: on unless turned off, also on a hub that omits it. */
    val backgroundFolded: Boolean get() = mainBackgroundFolded ?: true
}

/**
 * The built-in background supervisor (#4927, docs/assistant/unified-entry.md "Background supervisor"): the conversation
 * that follows up the tasks no leader supervises, so the main conversation stays free for the user.
 */
object BackgroundSupervisor {
    /**
     * The model purpose it runs under (docs/assistant/model-config.md): it follows Main chat until given its own. The
     * hub's catalog does not list it; it is read and saved through its own id.
     */
    const val PURPOSE = "assistant.supervise"
}

/**
 * PUT /api/v1/assistant. Only the fields that are set go on the wire and change; the hub keeps the rest.
 * [notes] set to "" is sent (it clears what the user wrote, unlike leaving it null).
 */
@Serializable
data class AssistantConfigUpdate(
    @SerialName("machine_id") val machineId: String? = null,
    val launcher: String? = null,
    val timezone: String? = null,
    /** true: [timezone] (or the hub's, without one) was picked by hand; false: it follows the devices again (#1855). */
    @SerialName("timezone_picked") val timezonePicked: Boolean? = null,
    val notes: String? = null,
    /** "-" clears the main conversation: it stays on the machine it ran on and cannot continue elsewhere. */
    val main: String? = null,
    @SerialName("main_tasks_quiet") val mainTasksQuiet: Boolean? = null,
    @SerialName("main_background_folded") val mainBackgroundFolded: Boolean? = null,
    @SerialName("main_supervises_own_tasks") val mainSupervisesOwnTasks: Boolean? = null,
    @SerialName("default_permission_mode") val defaultPermissionMode: String? = null,
) {
    companion object {
        const val CLEAR_MAIN = "-"
    }
}

/** Manager-owned contract for scoped persistence and future-session inheritance; absent on older managers. */
@Serializable
data class AssistantPermissionDefaults(
    val version: Int = 0,
    val manager: String = "",
    val modes: List<String> = emptyList(),
) {
    fun supports(owner: String?): Boolean = !owner.isNullOrBlank() && manager == owner && version == 1 &&
        modes.containsAll(MODES)

    companion object {
        val MODES = listOf("default", "acceptEdits", "plan", "auto", "bypassPermissions")
    }
}
