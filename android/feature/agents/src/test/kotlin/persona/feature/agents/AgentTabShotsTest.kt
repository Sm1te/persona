package persona.feature.agents

import android.graphics.Bitmap
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
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
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
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
import java.time.Instant

/**
 * A herdr tab of three agents (#2320; the mock hub's MOCK_TAB_PANES): the launcher working, a Codex pane it split off
 * waiting on a dialog, a Claude pane done; beside it a tab of one agent and a tab of two peers. The Agents list ("By machine")
 * and the space page in every style, light and dark; PNGs in `build/tab-shots/`.
 */
@RunWith(AndroidJUnit4::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
@Config(sdk = [35], qualifiers = "zh-w400dp-h900dp-xhdpi")
class AgentTabShotsTest {
    @get:Rule val rule = createComposeRule()

    private fun out(): File = File(System.getProperty("persona.shots") ?: "build/tab-shots").also { it.mkdirs() }

    private val now = Instant.parse("2026-11-05T10:00:00Z")
    private fun pane(id: String, title: String, state: String, slot: Int, place: Int?, words: String, minutes: Long,
                     agent: String = "claude", name: String = "$slot", space: String = "app", spaceId: String = "w1") =
        AgentSession(Session(machineId = "server", sessionId = id, title = title, cwd = "/home/me/code/$space", state = state,
            agent = agent, spaceKind = "herdr", spaceId = spaceId, spaceName = space, spaceIndex = 1, slotId = "t$slot",
            slotName = name, slotIndex = slot, paneId = "$spaceId:p$id", paneIndex = place, lastText = words,
            updatedAt = now.minusSeconds(minutes * 60).toString()), stateSince = now.minusSeconds(minutes * 60).toString())

    private val rows = listOf(
        pane("2E", "审一下登录重构的改动", "needs_you", 1, 1, "有两处会吞掉错误，要我直接改吗？", 1, agent = "codex"),
        pane("W", "重构登录流程，把 token 刷新挪到中间件里", "working", 1, 0, "开了两个窗格分头审改动、补测试，我在合中间件。", 12),
        pane("31", "补刷新失败的测试", "unread", 1, 2, "补了 6 个用例，都过了。", 3),
        pane("3A", "升级依赖", "working", 2, 0, "在跑全量测试。", 4, name = "deps"),
        pane("40", "对比两种缓存方案", "unread", 3, 1, "方案 A 写好了基准。", 8, name = "cache"),
        pane("41", "缓存方案 B 的基准", "idle", 3, 2, "方案 B 慢 12%。", 30, name = "cache"),
    )
    private val machines = listOf(Machine("server", "工作站", online = true, icon = "server"))

    private fun capture(name: String) {
        val bitmap = rule.onNodeWithTag("shot").captureToImage().asAndroidBitmap()
        val background = bitmap.getPixel(1, 1)
        var drawn = 0
        for (y in 0 until bitmap.height step 7) for (x in 0 until bitmap.width step 7) if (bitmap.getPixel(x, y) != background) drawn++
        assertTrue("$name must draw", drawn > 100)
        File(out(), "$name.png").outputStream().use { assertTrue(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
    }

    @Test fun drawsTheListWithATabOfThreeAgentsInEveryStyleAndAppearance() {
        val list = buildAgentList(rows, machines, settings = AgentListSettings(grouping = AgentGrouping.MachineSpace))
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        var folded by mutableStateOf(emptySet<String>())
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Column(Modifier.testTag("shot").width(400.dp).background(PersonaTheme.palette.background).padding(Tokens.Space.l)) {
                    agentTreeEntries(list, "", tabs = true, foldedTabs = folded).forEach { entry ->
                        when (entry) {
                            is TreeMachineEntry -> AgentTreeHeader(entry.machine.machine.displayName, entry.machine.folded, {}, first = entry.first,
                                icon = entry.machine.machine.deviceIcon, online = entry.machine.machine.online,
                                detail = "${entry.machine.spaceCount} 个空间，${entry.machine.sessionCount} 个会话", counts = entry.machine.headingCounts)
                            is TreeGroupEntry -> AgentSpaceHeader(entry.group.title.text(), entry.group.folded, {}, depth = entry.depth, first = entry.first,
                                quiet = entry.quiet, sessions = entry.group.rows.size, counts = entry.group.headingCounts)
                            is TreeTabEntry -> AgentTabEntryRow(entry, now, compact = false, onOpen = {}, onToggle = {})
                            is TreeRowEntry -> SlotRow(entry.row, "工作站", AgentRowMetadata(), now, false, false, false, depth = entry.depth,
                                position = GroupPosition(entry.first, entry.last), located = entry.located, onOpen = {}, menuItems = { emptyList() },
                                launcher = entry.launcher, branch = entry.branch)
                            is TreeChildrenEntry -> error("Fixture has no Codex subtasks")
                            is TreeIdleEntry -> AgentIdleRow(entry.hidden, {}, depth = entry.depth)
                            is TreeShowAllEntry -> AgentShowAllRow(entry.count, entry.expanded, {}, depth = entry.depth)
                        }
                    }
                }
            }
        }
        val tab = agentTreeEntries(list, "", tabs = true).filterIsInstance<TreeTabEntry>().first().tab
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark; folded = emptySet() }
            rule.onAllNodesWithText("发起者")[0].assertIsDisplayed()
            rule.onNodeWithContentDescription("3 个 Agent").assertIsDisplayed()
            capture("list-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}")
            rule.runOnIdle { folded = setOf(tab.foldKey) }
            rule.onNodeWithContentDescription("3 个 Agent").assertIsDisplayed()
            capture("list-folded-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}")
        }
    }

    @Test fun drawsTheSpacePageInEveryStyleAndAppearance() {
        val space = agentSpaces(rows).single()
        var style by mutableStateOf(AppStyle.Glass)
        var dark by mutableStateOf(false)
        rule.setContent {
            PersonaTheme(style = style, darkTheme = dark, reduceMotion = true) {
                Box(Modifier.testTag("shot").size(400.dp, 900.dp).background(PersonaTheme.palette.background)) {
                    SpaceScreen(space, machines, emptyList(), AgentsPreferencesSnapshot(), busy = false, canStart = true,
                        toast = JToastState(), now = now, onDismiss = {}, onOpen = {}, onNewTab = {},
                        spaceMenu = {}, sessionMenu = { emptyList() })
                }
            }
        }
        for (nextStyle in AppStyle.entries) for (nextDark in listOf(false, true)) {
            rule.runOnIdle { style = nextStyle; dark = nextDark }
            rule.onAllNodesWithText("发起者")[0].assertIsDisplayed()
            capture("space-${nextStyle.name.lowercase()}-${if (nextDark) "dark" else "light"}")
        }
    }
}
