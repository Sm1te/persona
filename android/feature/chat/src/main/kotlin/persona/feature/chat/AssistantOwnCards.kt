package persona.feature.chat

import persona.core.contracts.Interaction
import persona.core.contracts.SessionKey

/**
 * The cards the panel above the composer shows (#4883). An Agent's chat shows every card it and its tasks wait on. A
 * Persona conversation shows its own cards there too, as iOS and the web do: its first Bash once waited minutes for
 * approval while the card was only a row in the closed Needs you tray, and the owner thought the assistant had hung.
 * A card opened from the tray (a task's or an Agent's, [trayReview]) takes the panel while it is open.
 */
internal fun panelRequests(assistant: Boolean, requests: List<Interaction>, key: SessionKey, trayReview: String?,
    shown: List<Interaction>): List<Interaction> = when {
    !assistant -> requests
    trayReview != null -> shown.filter { it.id == trayReview }
    else -> requests.filter { it.sessionKey == key }
}

/** The composer gives the panel its place while a card is open, as in an Agent's chat; folded, it comes back. */
internal fun composerShown(panel: List<Interaction>, collapsed: Boolean): Boolean = panel.isEmpty() || collapsed
