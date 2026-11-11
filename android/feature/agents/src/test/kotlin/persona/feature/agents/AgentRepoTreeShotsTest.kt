package persona.feature.agents

import persona.core.designsystem.text
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
import androidx.compose.ui.test.assertIsDisplayed
import androidx.compose.ui.test.captureToImage
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.unit.dp
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import org.robolectric.annotation.GraphicsMode
import persona.core.contracts.Machine
import persona.core.contracts.Session
import persona.core.designsystem.*
import persona.core.domain.sort.*
import java.io.File

/** "By repository": computer → repository → working tree → sessions, drawn with the Agents components in every style and appearance. */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h800dp-xhdpi")
class AgentRepoTreeShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/repo-tree-shots").also { it.mkdirs() }

    private fun at(id: String, title: String, cwd: String, state: String, machine: String = "mac") =
        AgentSession(Session(machineId = machine, sessionId = id, title = title, cwd = cwd, state = state, updatedAt = "2026-01-01T00:00:00Z"))

    private val list = buildAgentList(listOf(
        at("a", "修登录页", "/code/app", "working"), at("b", "看日志", "/code/app", "idle"),
        at("c", "改接口", "/code/app/.worktrees/fix-api", "needs_you"), at("d", "旧分支", "/code/app/.claude/worktrees/old", "idle"),
        at("e", "写周报", "/code/notes", "idle"), at("f", "跑测试", "/srv/api", "unread", machine = "box"),
    ), listOf(Machine("mac", "MacBook", online = true), Machine("box", "Server", online = true, icon = "server")),
        settings = AgentListSettings(grouping = AgentGrouping.Repo))

    @Test fun drawsTheRepositoryTreeInEveryStyleAndAppearance() {
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    agentTreeEntries(list, "").forEach { entry ->
                        when (entry) {
                            is TreeMachineEntry -> AgentTreeHeader(entry.machine.machine.displayName, entry.machine.folded, {}, first = entry.first,
                                icon = entry.machine.machine.deviceIcon, online = entry.machine.machine.online,
                                detail = "${entry.machine.repoCount} 个仓库，${entry.machine.sessionCount} 个会话", counts = entry.machine.headingCounts)
                            is TreeGroupEntry -> AgentSpaceHeader(entry.group.title.text(), entry.group.folded, {}, depth = entry.depth, first = entry.first,
                                quiet = entry.quiet, sessions = entry.group.rows.size, counts = entry.group.headingCounts, icon = entry.group.repo?.icon,
                                kind = entry.group.repo?.kind ?: "空间")
                            is TreeRowEntry -> AgentRow(entry.row.session.displayTitle, entry.row.state.designStatus, {}, depth = entry.depth,
                                position = GroupPosition(entry.first, entry.last), time = "刚刚",
                                statusText = entry.row.state.takeIf { it != persona.core.contracts.AgentState.Idle }?.label)
                            is TreeChildrenEntry -> error("Fixture has no Codex subtasks")
                            is TreeIdleEntry -> AgentIdleRow(entry.hidden, {}, depth = entry.depth)
                            is TreeShowAllEntry -> AgentShowAllRow(entry.count, entry.expanded, {}, depth = entry.depth)
                            is TreeTabEntry -> error("按仓库 lists one agent a row")
                        }
                    }
                }
            }
        }
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark }
            rule.onNodeWithText("notes").assertIsDisplayed()
            rule.onNodeWithText("fix-api").assertIsDisplayed()
            rule.onNodeWithText("2 个仓库，5 个会话").assertIsDisplayed()
            rule.onNodeWithContentDescription("主工作树").assertIsDisplayed()
            rule.onNodeWithContentDescription("展开工作树").assertIsDisplayed()
            val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
            val background = bitmap.getPixel(1, 1)
            var drawn = 0
            for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) drawn++
            assertTrue("the tree must draw", drawn > 100)
            File(out(), "repo-tree-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}.png").outputStream().use {
                assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it))
            }
        }
    }
}
