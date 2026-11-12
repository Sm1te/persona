package persona.feature.chat

import androidx.compose.ui.platform.LocalResources
import android.content.res.Resources
import androidx.compose.foundation.layout.*
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.contracts.SessionInfo
import persona.core.contracts.parseTime
import persona.core.designsystem.*
import persona.core.designsystem.icons.PhIcons
import persona.core.domain.lines.tokenCount
import persona.core.domain.sort.resumeCommand
import java.time.ZoneId
import kotlinx.coroutines.CancellationException

/**
 * A token count in the language of [resources], as the compaction divider counts them ([tokenCount]): 950, 48.2K and
 * 1.2M in English, 4.8 万 in Chinese, 4.8万 in Japanese.
 */
internal fun formatTokens(count: Long, resources: Resources): String = tokenCount(count).resolve(resources)

/** One fact of "Session info"; a null value reads as —. [copy] names what tapping it copies. */
internal data class SessionInfoRow(val label: String, val value: String?, val copy: String? = null, val onClick: (() -> Unit)? = null)
internal data class SessionInfoSection(val title: String, val rows: List<SessionInfoRow>)

/**
 * "Session info" (iOS SessionInfoSheet), from GET …/info: what it runs, how far it has got, where its files are, and what to
 * copy. The resume command only for a session in a terminal (an agent runtime's is picked up in its own UI); a Codex
 * thread resumes by its own thread id.
 */
internal fun sessionInfoSections(
    res: Resources,
    session: Session,
    info: SessionInfo,
    zone: ZoneId = ZoneId.systemDefault(),
    onOpenTodo: ((String) -> Unit)? = null,
    features: Set<String> = emptySet(),
): List<SessionInfoSection> {
    fun String?.orSession(fallback: String?) = this?.takeIf(String::isNotBlank) ?: fallback?.takeIf(String::isNotBlank)
    val cwd = info.cwd.orSession(session.cwd)
    val id = info.sessionId.ifBlank { session.sessionId }
    val locale = res.configuration.locales[0]
    val directory = res.getString(R.string.chat_info_directory)
    val branch = res.getString(R.string.chat_info_branch)
    val resume = res.getString(R.string.chat_info_resume_command)
    return buildList {
        session.todo?.let { todo ->
            add(SessionInfoSection(res.getString(R.string.chat_info_todo_section), listOf(
                SessionInfoRow(res.getString(R.string.chat_info_todo), todo.title, copy = todo.id, onClick = onOpenTodo?.let { { it(todo.id) } })
            )))
        }
        add(SessionInfoSection(res.getString(R.string.chat_info_run_section), listOfNotNull(
            SessionInfoRow(res.getString(R.string.chat_info_model), info.model.orSession(session.model)),
            SessionInfoRow(res.getString(R.string.chat_info_launcher), info.launcher.orSession(session.launcher)),
            SessionInfoRow(if (session.isClaude) "Claude Code" else session.agentLabel, info.version.orSession(null)),
            SessionInfoRow(res.getString(R.string.chat_info_permission), SessionModes.modeLabel(info.permissionMode.orSession(session.permissionMode), res)),
            SessionInfoRow(res.getString(R.string.chat_info_effort), SessionModes.effortLabel(info.effort.orSession(session.effort), res)),
            // #582: on / off / unknown, as the node last saw it
            SessionInfoRow(Ultracode.NAME, Ultracode.infoWord(session, res)).takeIf { Ultracode.infoShown(session, features) },
        )))
        add(SessionInfoSection(res.getString(R.string.chat_info_usage_section), buildList {
            add(SessionInfoRow(res.getString(R.string.chat_info_context), res.getString(R.string.chat_info_tokens, formatTokens(info.contextTokens, res))))
            if (info.compactions > 0) add(SessionInfoRow(res.getString(R.string.chat_info_compactions),
                res.getQuantityString(R.plurals.chat_info_compaction_count, info.compactions, info.compactions)))
            add(SessionInfoRow(res.getString(R.string.chat_info_turns), res.getQuantityString(R.plurals.chat_info_turn_count, info.turns, info.turns)))
            add(SessionInfoRow(res.getString(R.string.chat_info_output), res.getString(R.string.chat_info_tokens, formatTokens(info.outputTokens, res))))
            parseTime(info.startedAt)?.let {
                add(SessionInfoRow(res.getString(R.string.chat_info_started),
                    LocalizedFormats.format(it, LocalizedFormats.MONTH_DAY_TIME, locale, zone)))
            }
        }))
        add(SessionInfoSection(res.getString(R.string.chat_info_place_section), listOfNotNull(
            SessionInfoRow(directory, cwd, copy = cwd?.let { directory }),
            info.gitBranch.orSession(null)?.let { SessionInfoRow(branch, it, copy = branch) },
        )))
        add(SessionInfoSection(res.getString(R.string.chat_info_copy_section), listOfNotNull(
            SessionInfoRow("session id", id, copy = "session id"),
            session.resumeCommand(cwd = cwd.orEmpty())?.let { SessionInfoRow(resume, it, copy = resume) },
        )))
    }
}

@Composable
internal fun ChatSessionInfoSheet(
    session: Session,
    machine: Machine?,
    state: ChatChromeState,
    onOpenTodo: ((String) -> Unit)? = null,
    onDismiss: () -> Unit,
) {
    val copyText = rememberCopy()
    val res = LocalResources.current
    var info by remember(session.key) { mutableStateOf<SessionInfo?>(null) }
    var error by remember(session.key) { mutableStateOf<String?>(null) }
    var attempt by remember(session.key) { mutableIntStateOf(0) }
    LaunchedEffect(session.key, attempt) {
        error = null
        try { info = state.info(session) }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) {
            error = res.getString(R.string.chat_info_unreadable, (e as? UnsupportedOperationException)?.message ?: operationFeedback(e).text.resolve(res))
        }
    }
    JSheet(onDismiss = onDismiss, title = stringResource(R.string.chat_info_title), scrollable = true) {
        Column(verticalArrangement = Arrangement.spacedBy(Tokens.Space.xs)) {
            Text(session.displayTitle, style = PersonaTheme.type.headline, color = PersonaTheme.palette.ink, maxLines = 2,
                overflow = TextOverflow.Ellipsis)
            val status = Status.entries.firstOrNull { it.name == session.agentState.name }
            Text(listOfNotNull(status?.localizedLabel, machine?.displayName ?: session.machineId, session.spaceName?.takeIf(String::isNotBlank))
                .joinToString(" / "), style = PersonaTheme.type.caption, color = PersonaTheme.palette.ink2, maxLines = 1,
                overflow = TextOverflow.Ellipsis)
        }
        val loaded = info
        when {
            loaded != null -> sessionInfoSections(res, session, loaded, onOpenTodo = onOpenTodo,
                features = persona.core.designsystem.LocalHubFeatures.current).filter { it.rows.isNotEmpty() }.forEach { section ->
                JSectionHeader(section.title)
                section.rows.forEachIndexed { index, row ->
                    JGroupItem(GroupPosition.of(index, section.rows.size)) {
                        val value = row.value ?: "—"
                        val copy = row.copy?.takeIf { row.value != null }
                        if (row.onClick != null) JRow(row.label, subtitle = value, subtitleLines = 4,
                            chevron = true, onClick = { onDismiss(); row.onClick.invoke() })
                        else if (copy != null) JRow(row.label, subtitle = value, subtitleLines = 4,
                            trailing = { JIcon(PhIcons.Copy, stringResource(R.string.chat_info_copy_item, copy), tint = PersonaTheme.palette.ink2) }, onClick = {
                                copyText(value, res.getString(R.string.chat_info_copied_item, copy))
                            })
                        else JRow(row.label, value = value)
                    }
                }
            }
            error != null -> JNotice(error.orEmpty(), Modifier.padding(top = Tokens.Space.m), status = Status.NeedsYou,
                icon = PhIcons.WarningCircle, actionLabel = stringResource(R.string.chat_info_retry), onAction = { attempt++ })
            else -> JLoading()
        }
    }
}
