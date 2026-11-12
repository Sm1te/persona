package persona.platform.shortcuts

import persona.core.contracts.clipped
import android.content.Context
import android.content.Intent
import android.util.Log
import androidx.core.content.pm.ShortcutInfoCompat
import androidx.core.content.pm.ShortcutManagerCompat
import androidx.core.graphics.drawable.IconCompat
import persona.core.contracts.Session

/**
 * The launcher shortcuts (long-press the Persona icon; iOS App Intents `PersonaShortcuts`): "Ask Persona", "Approve pending",
 * "New to-do" (iOS AddTodoIntent) and the two most recently active agents. Published at runtime: a static shortcut's intent would have to spell out the
 * application id, which is personal configuration (docs/operations/config.md).
 */
internal object ShortcutPublisher {
    private const val TAG = "PersonaShortcuts"
    private const val ASK = "ask"
    private const val APPROVE = "approve"
    private const val NEW_TODO = "new-todo"
    private const val SESSION = "session:"

    /** The fixed shortcuts; pushDynamicShortcut keeps the recent agents' ones. */
    fun publishFixed(context: Context) {
        val approve = Intent(context, ApproveFirstActivity::class.java).setAction(ApproveFirstActivity.ACTION_APPROVE)
        runCatching {
            ShortcutManagerCompat.pushDynamicShortcut(context, shortcut(context, ASK, context.getString(R.string.shortcuts_ask_label), context.getString(R.string.shortcuts_ask_label),
                SurfaceLinks.ask(context), R.drawable.ic_shortcut_ask, 0))
            ShortcutManagerCompat.pushDynamicShortcut(context, shortcut(context, APPROVE, context.getString(R.string.shortcuts_approve_label), context.getString(R.string.shortcuts_approve_title),
                approve, R.drawable.ic_shortcut_approve, 1))
            ShortcutManagerCompat.pushDynamicShortcut(context, shortcut(context, NEW_TODO, context.getString(R.string.shortcuts_new_todo_label),
                context.getString(R.string.shortcuts_new_todo_title), SurfaceLinks.newTodo(context), R.drawable.ic_shortcut_new_todo, 2))
        }.onFailure { Log.w(TAG, "Fixed shortcuts not published: ${it.javaClass.simpleName}") }
    }

    /** Replaces the recent agents' shortcuts with [recent] (none when unpaired); ones no longer among them go. */
    fun publishRecent(context: Context, recent: List<Session>) {
        val shortcuts = recent.mapIndexed { index, session ->
            shortcut(context, SESSION + session.key.path, shortLabel(session), longLabel(context, session),
                SurfaceLinks.conversation(context, session.key, session.assistant), R.drawable.ic_shortcut_agent, 10 + index)
        }
        runCatching {
            shortcuts.forEach { ShortcutManagerCompat.pushDynamicShortcut(context, it) }
            val keep = shortcuts.map { it.id }.toSet()
            val stale = ShortcutManagerCompat.getDynamicShortcuts(context).map { it.id }.filter { it.startsWith(SESSION) && it !in keep }
            if (stale.isNotEmpty()) ShortcutManagerCompat.removeDynamicShortcuts(context, stale)
        }.onFailure { Log.w(TAG, "Recent shortcuts not published: ${it.javaClass.simpleName}") }
    }

    internal fun shortLabel(session: Session) = session.displayTitle.clipped(24)
    internal fun longLabel(context: Context, session: Session) = context.getString(R.string.shortcuts_open_session, session.displayTitle).clipped(48)

    private fun shortcut(context: Context, id: String, short: String, long: String, intent: Intent, icon: Int, rank: Int) =
        ShortcutInfoCompat.Builder(context, id)
            .setShortLabel(short)
            .setLongLabel(long)
            .setIcon(IconCompat.createWithResource(context, icon))
            .setIntent(intent)
            .setRank(rank)
            .build()
}
