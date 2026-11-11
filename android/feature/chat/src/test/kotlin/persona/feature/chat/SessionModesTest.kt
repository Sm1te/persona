package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The old App's SessionModesTest and ModelPickerTest (81dc6928), as iOS names the modes. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class SessionModesTest {
    private val res = ApplicationProvider.getApplicationContext<Context>().resources
    private fun claude(mode: String?) = Session(sessionId = "s", agent = "claude", permissionMode = mode)

    @Test fun runningSessionsCannotPickBypassButShowItWhenOn() {
        assertEquals(listOf("default", "acceptEdits", "plan", "auto"), SessionModes.switchable(claude("default")).map { it.id })
        assertEquals(listOf("default", "acceptEdits", "plan", "auto"), SessionModes.switchable(claude(null)).map { it.id })
        assertEquals("bypassPermissions", SessionModes.switchable(claude("bypassPermissions")).last().id)
        assertEquals("全部放行", SessionModes.switchable(claude("bypassPermissions")).last().title(res))
        // A newer Claude Code's mode is listed by its raw name, so the current one still shows as picked.
        assertEquals("future", SessionModes.switchable(claude("future")).last().title(res))
        assertEquals("手动确认", SessionModes.modeLabel("default", res))
        assertEquals("很高", SessionModes.effortLabel("xhigh", res))
        assertEquals("future", SessionModes.modeLabel("future", res))
        assertNull(SessionModes.modeLabel(null, res))
    }

    @Test fun aCodexThreadOffersItsOwnLevels() {
        val codex = Session(sessionId = "s", agent = "codex", effort = "high", efforts = listOf("low", "high", "ultra", "turbo"))
        assertEquals(listOf("low", "high", "ultra", "turbo"), SessionModes.effortOptions(codex).map { it.id })
        assertEquals(listOf("低", "高", "极高", "turbo"), SessionModes.effortOptions(codex).map { it.title(res) })
        assertEquals("极低", SessionModes.effortLabel("minimal", res))
        // Claude Code's sessions keep Claude's levels.
        assertEquals(SessionModes.effortLevels, SessionModes.effortOptions(Session(sessionId = "s")))
    }

    @Test fun modelAliasesAsIos() {
        val alias = SessionModes::claudeModelId
        assertEquals("opus", alias("claude-opus-5-5[1m]"))
        assertEquals("sonnet", alias("us.anthropic.claude-sonnet-5-5"))
        assertEquals("haiku", alias("claude-haiku-5-5"))
        assertEquals("default", alias(null))
        assertEquals("default", alias("deepseek/v4.1"))
        assertEquals(listOf("default", "opus", "sonnet", "haiku"), SessionModes.claudeModels.map { it.id })
    }

    @Test fun theGptHintKeepsTheModesAndEveryVersionVariant() {
        val session = Session(sessionId = "s", model = "gpt-6.1-sol", permissionMode = "default", effort = "high")
        assertEquals("6.1 Sol", chatModeHint(session, res))
        assertEquals("6.1", chatModeHint(session.copy(model = "gpt-6.1"), res))
        assertEquals("4o Mini", chatModeHint(session.copy(model = "gpt-4o-mini"), res))
        assertEquals("6.1 Codex Max", chatModeHint(session.copy(model = "gpt-6.1-codex-max"), res))
        assertEquals("自动改文件，思考很高，6.1 Sol", chatModeHint(session.copy(permissionMode = "acceptEdits", effort = "xhigh"), res))
        // The current model does not grant a session permission or effort controls.
        assertEquals("6.1 Sol", chatModeHint(session.copy(permissionMode = "acceptEdits", effort = "xhigh"), res, includeModes = false))
        assertEquals("gpt-6.1-sol", session.model)
    }

    @Test fun nonGptAndUnknownModelsDoNotChangeTheExistingHint() {
        val session = Session(sessionId = "s", permissionMode = "acceptEdits", effort = "xhigh")
        listOf(null, "", " \t", "unknown", "claude-opus-5-5", "deepseek/v4.1", "gateway/gpt-6.1", "gpt-", "gpt-6.1--sol").forEach { model ->
            assertEquals(model, "自动改文件，思考很高", chatModeHint(session.copy(model = model), res))
            assertNull(chatModeHint(session.copy(model = model), res, includeModes = false))
            assertNull(chatModeHint(session.copy(model = model, permissionMode = "default", effort = "high"), res))
        }
        assertNull(chatModeHint(null, res))
    }

    @Test fun ultracodeShowsOnlyForTheUsersRunningClaudeOnAHubThatHasIt() {
        val claude = Session(sessionId = "s", status = "idle")
        val has = setOf(Ultracode.FEATURE)
        assertTrue(Ultracode.offered(claude, has))
        assertFalse(Ultracode.offered(claude, emptySet()))
        assertFalse(Ultracode.offered(claude.copy(assistant = true), has))
        assertFalse(Ultracode.offered(claude.copy(agent = "codex"), has))
        assertFalse(Ultracode.offered(claude.copy(status = "offline"), has))
        assertFalse(Ultracode.offered(claude.copy(sourceKind = "claude_cli"), has))
        assertFalse(Ultracode.offered(null, has))
        // The hint names it while it is on (unknown is not on); not on an offline row, which a resume starts off.
        assertEquals("ultracode", chatModeHint(claude.copy(ultracode = true), res))
        assertNull(chatModeHint(claude.copy(ultracode = null), res))
        assertNull(chatModeHint(claude.copy(ultracode = true, status = "offline"), res))
        assertNull(chatModeHint(claude.copy(ultracode = true), res, includeModes = false))
    }

    @Test fun theUltracodeSwitchStandsWhereItWasPutUntilTheNodeAnswers() {
        val off = Session(sessionId = "s", status = "idle", ultracode = false)
        assertFalse(Ultracode.shownOn(off, null))
        assertTrue(Ultracode.shownOn(off, Ultracode.Ask(true)))
        // Queued for the next turn: the queue flag holds it on, and says so.
        val queued = off.copy(ultracodePending = true)
        val answered = Ultracode.Ask(true, sending = false, answered = queued)
        assertTrue(Ultracode.shownOn(queued, answered))
        assertFalse(Ultracode.spent(queued, answered))
        assertEquals("下一轮生效", Ultracode.line(queued, answered, res))
        assertEquals("有一次切换在排队，这一轮结束后生效", Ultracode.line(queued, null, res))
        assertEquals("下一轮生效，等这一轮结束", Ultracode.line(queued.copy(ultracodeNote = "等这一轮结束"), answered, res))
        // The request came back before the push: still where it was put, until the session moves.
        val back = Ultracode.Ask(true, sending = false, answered = off)
        assertTrue(Ultracode.shownOn(off, back))
        val on = off.copy(ultracode = true)
        assertTrue(Ultracode.spent(on, back))
        assertTrue(Ultracode.shownOn(on, null))
        // The node's reason wins over what was asked, and is the line.
        val refused = off.copy(ultracodeNote = "模型不支持")
        assertFalse(Ultracode.shownOn(refused, Ultracode.Ask(true, sending = false, answered = refused)))
        assertEquals("模型不支持", Ultracode.line(refused, null, res))
        assertEquals("每个任务都编排多 Agent workflow · 很费额度 · 只对本会话", Ultracode.line(off, null, res))
    }

    @Test fun theHintNamesWhatIsNotTheDefault() {
        val session = Session(sessionId = "s", permissionMode = "default", effort = "high")
        assertNull(chatModeHint(session, res))
        assertEquals("自动改文件，思考很高", chatModeHint(session.copy(permissionMode = "acceptEdits", effort = "xhigh"), res))
        assertEquals("全部放行", chatModeHint(session.copy(permissionMode = "bypassPermissions"), res))
        assertEquals("future", chatModeHint(session.copy(permissionMode = "future"), res))
    }

    /** #582: the after-resume notice shows while it was on before a resume and nothing turned it back on. */
    @Test fun theAfterResumeNoticeShowsUntilItIsOnAgain() {
        val lost = persona.core.contracts.Session(machineId = "m", sessionId = "s", agent = "claude", status = "idle",
            ultracode = false, ultracodeLostAt = "2026-11-11T12:00:00Z")
        val features = setOf(Ultracode.FEATURE)
        org.junit.Assert.assertTrue(Ultracode.lostShown(lost, features))
        org.junit.Assert.assertFalse(Ultracode.lostShown(lost.copy(ultracode = true), features))
        org.junit.Assert.assertFalse(Ultracode.lostShown(lost.copy(ultracodeLostAt = null), features))
        org.junit.Assert.assertFalse(Ultracode.lostShown(lost, emptySet()))
        org.junit.Assert.assertFalse(Ultracode.lostShown(lost.copy(assistant = true), features))
    }

    /** #582: the quota under the switch (iOS `quotaLine`, `quotaAge`, `quotaWarnShown`). */
    @Test fun theQuotaLineSaysEachWindowAndWarnsOnlyUnderASwitchThatCanBeOn() {
        val now = java.time.Instant.parse("2026-11-11T12:00:00Z")
        val q = persona.core.domain.usage.UltracodeQuota("Claude", listOf(
            persona.core.domain.usage.UltracodeQuota.Window(persona.core.domain.usage.UltracodeQuota.Kind.FiveHours, 86.6),
            persona.core.domain.usage.UltracodeQuota.Window(persona.core.domain.usage.UltracodeQuota.Kind.Week, 41.0)), warn = true, stale = false, sampledAt = null)
        assertEquals("Claude 额度：5 小时 86% · 每周 41%", quotaLine(q, res))
        assertNull("current numbers: no age", quotaAge(q, now, res))
        assertEquals("已过期 · 2 小时前", quotaAge(q.copy(stale = true, sampledAt = now.minusSeconds(7200)), now, res))
        val off = Session(sessionId = "s", status = "idle", ultracode = false)
        assertTrue("off and can be turned on", Ultracode.quotaWarnShown(off, null, enabled = true))
        assertTrue("on", Ultracode.quotaWarnShown(off.copy(ultracode = true), null, enabled = false))
        assertFalse("can't be changed", Ultracode.quotaWarnShown(off, null, enabled = false))
        val turningOff = off.copy(ultracode = true, ultracodePending = true)
        assertFalse("turning it off waits", Ultracode.quotaWarnShown(turningOff, Ultracode.Ask(false, sending = false, answered = turningOff), enabled = true))
        assertFalse("under the node's reason", Ultracode.quotaWarnShown(off.copy(ultracodeNote = "模型不支持"), null, enabled = true))
    }
}
