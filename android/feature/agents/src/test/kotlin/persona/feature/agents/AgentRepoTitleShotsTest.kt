package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asAndroidBitmap
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.SemanticsActions
import androidx.compose.ui.semantics.getOrNull
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.text.TextLayoutResult
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.AgentState
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.designsystem.*
import persona.core.domain.sort.*
import java.io.File

/**
 * "By repository" with Paseo workspaces (#4497): each linked worktree is headed by its workspace's title, the main
 * checkout still by its branch, in every style and appearance; a title too long for its row ends in an ellipsis on its
 * one line. Written as PNGs to `build/repo-title-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "en-w400dp-h800dp-xhdpi")
class AgentRepoTitleShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File("build/repo-title-shots").also { it.mkdirs() }

    private val repo = "/home/me/code/app"
    private val longTitle = "Move the nightly export to the new storage bucket and retire the old job"

    /** An agent Paseo placed: the node's git place and branch, its workspace's title in slot_name, no slot_id. */
    private fun paseo(id: String, title: String, tree: String, branch: String, workspace: String, state: String) =
        AgentSession(Session(machineId = "laptop", sessionId = id, title = title, cwd = tree, state = state, updatedAt = "2026-01-01T00:00:00Z",
            repoRoot = repo, worktree = tree, branch = branch, spaceId = "project-app", spaceName = "app", slotName = workspace))

    private val list = buildAgentList(listOf(
        paseo("triage", "Sort the new bug reports", repo, "main", "Triage", "working"),
        paseo("login", "Add a remember-me option", "/home/me/.paseo/worktrees/abc123/brave-fox", "fix/login", "Fix the login flow", "needs_you"),
        paseo("login-tests", "Cover the login form with tests", "/home/me/.paseo/worktrees/abc123/brave-fox", "fix/login", "Fix the login flow", "working"),
        paseo("importer", "Profile the CSV parser", "/home/me/.paseo/worktrees/abc123/calm-otter", "perf/importer", "Speed up the importer", "working"),
        paseo("export", "Plan the storage move", "/home/me/.paseo/worktrees/abc123/quiet-heron", "chore/export", longTitle, "unread"),
    ), listOf(Machine("laptop", "Laptop", online = true)), settings = AgentListSettings(grouping = AgentGrouping.Repo))

    private var style by mutableStateOf(AppStyle.Glass)
    private var dark by mutableStateOf(false)

    private fun show() = rule.setContent {
        PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
            Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                agentTreeEntries(list, "").forEach { entry ->
                    when (entry) {
                        is TreeMachineEntry -> AgentTreeHeader(entry.machine.machine.displayName, entry.machine.folded, {}, first = entry.first,
                            icon = entry.machine.machine.deviceIcon, online = entry.machine.machine.online,
                            detail = stringResource(R.string.agents_count_pair,
                                pluralStringResource(R.plurals.agents_count_repos, entry.machine.repoCount ?: 0, entry.machine.repoCount ?: 0),
                                pluralStringResource(R.plurals.agents_count_sessions, entry.machine.sessionCount, entry.machine.sessionCount)),
                            counts = entry.machine.headingCounts)
                        is TreeGroupEntry -> AgentSpaceHeader(entry.group.title.text(), entry.group.folded, {}, depth = entry.depth, first = entry.first,
                            quiet = entry.quiet, sessions = entry.group.rows.size, counts = entry.group.headingCounts, icon = entry.group.repo?.icon,
                            kind = entry.group.repo?.kind ?: stringResource(R.string.agents_kind_space))
                        is TreeRowEntry -> AgentRow(entry.row.session.displayTitle, entry.row.state.designStatus, {}, depth = entry.depth,
                            position = GroupPosition(entry.first, entry.last), time = "Just now",
                            statusText = entry.row.state.takeIf { it != AgentState.Idle }?.label)
                        is TreeChildrenEntry -> error("The fixture has no Codex subtasks")
                        is TreeIdleEntry -> AgentIdleRow(entry.hidden, {}, depth = entry.depth)
                        is TreeShowAllEntry -> AgentShowAllRow(entry.count, entry.expanded, {}, depth = entry.depth)
                        is TreeTabEntry -> error("By repository lists one agent a row")
                    }
                }
            }
        }
    }

    /** Every style and appearance: the titles head their trees, the long one on one line; one PNG each, named with [suffix]. */
    private fun captureEveryStyle(suffix: String) {
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark }
            rule.onNodeWithText("main").assertIsDisplayed()
            rule.onNodeWithText("Fix the login flow").assertIsDisplayed()
            rule.onNodeWithText("Speed up the importer").assertIsDisplayed()
            val layouts = mutableListOf<TextLayoutResult>()
            rule.onNodeWithText(longTitle).assertIsDisplayed().fetchSemanticsNode()
                .config.getOrNull(SemanticsActions.GetTextLayoutResult)?.action?.invoke(layouts)
            assertEquals("a long title stays on one line", 1, layouts.single().lineCount)
            assertTrue("and ends in an ellipsis", layouts.single().isLineEllipsized(0))
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            File(out(), "repo-title-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}$suffix.png").outputStream().use {
                assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }

    @Test fun headsEachPaseoWorktreeWithItsWorkspaceTitleInEveryStyleAndAppearance() {
        show()
        captureEveryStyle("")
    }

    /** At the largest text size (200 %) a title still heads its tree on one line. */
    @Test
    @Config(sdk = [35], qualifiers = "en-w400dp-h2400dp-xhdpi", fontScale = 2f)
    fun headsEachPaseoWorktreeWithItsWorkspaceTitleAtTheLargestTextSize() {
        show()
        captureEveryStyle("-large")
    }
}
