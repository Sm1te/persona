package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Session
import persona.core.contracts.SessionCapabilities
import persona.core.contracts.SessionInfo
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.ZoneId

/** The old App's SessionInfoTest (81dc6928): tokens, rows and what can be copied. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class ChatSessionInfoTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val zone = ZoneId.of("Asia/Shanghai")
    private fun rows(session: Session, info: SessionInfo) = sessionInfoSections(res, session, info, zone).associate { section ->
        section.title to section.rows.associate { it.label to it.value }
    }

    // Token counts use the language's compact unit, as the compaction divider does (#4137).
    @Test fun tokensCountInTenThousandsInChinese() {
        assertEquals("950", formatTokens(950, res))
        assertEquals("9500", formatTokens(9_500, res))
        assertEquals("1.2 万", formatTokens(12_345, res))
        assertEquals("120 万", formatTokens(1_200_000, res))
    }

    @Config(qualifiers = "ja")
    @Test fun tokensCountInTenThousandsInJapanese() {
        assertEquals("950", formatTokens(950, res))
        assertEquals("4.8万", formatTokens(48_200, res))
        assertEquals("120万", formatTokens(1_200_000, res))
    }

    @Config(qualifiers = "en")
    @Test fun tokensCountInThousandsAndMillionsInEnglish() {
        assertEquals("950", formatTokens(950, res))
        assertEquals("12.3K", formatTokens(12_345, res))
        assertEquals("500K", formatTokens(500_000, res))
        assertEquals("999.9K", formatTokens(999_949, res))
        assertEquals("1M", formatTokens(999_950, res))
        assertEquals("1.2M", formatTokens(1_200_000, res))
    }

    @Test fun claudesSessionShowsWhatItRunsHowFarAndWhere() {
        val session = Session(machineId = "m", sessionId = "s1", cwd = "/home/me/code/shop", launcher = "claude", model = "opus",
            permissionMode = "acceptEdits", effort = "xhigh")
        val info = SessionInfo(sessionId = "s1", cwd = "/home/me/code/shop", model = "claude-opus-5", version = "2.1.320",
            contextTokens = 48_200, outputTokens = 12_800, turns = 6, compactions = 0, gitBranch = "main",
            startedAt = "2026-11-01T02:00:00Z")
        val all = rows(session, info)
        assertEquals("claude-opus-5", all.getValue("运行")["模型"])
        assertEquals("2.1.320", all.getValue("运行")["Claude Code"])
        assertEquals("自动改文件", all.getValue("运行")["权限"])
        assertEquals("很高", all.getValue("运行")["思考强度"])
        assertEquals("4.8 万 tokens", all.getValue("用量")["上下文"])
        assertFalse("压缩" in all.getValue("用量"))
        assertEquals("6 轮", all.getValue("用量")["对话"])
        assertEquals("11月1日 10:00", all.getValue("用量")["开始"])
        assertEquals("main", all.getValue("位置")["分支"])
        assertEquals("cd -- '/home/me/code/shop' && claude --resume 's1'", all.getValue("拷贝")["恢复命令"])
        assertEquals("s1", all.getValue("拷贝")["session id"])
        // Unknown counts and fields read as nothing rather than inventing them.
        val sparse = rows(session.copy(launcher = null), SessionInfo(sessionId = "s1", compactions = 2))
        assertEquals("2 次", sparse.getValue("用量")["压缩"])
        assertNull(sparse.getValue("运行")["启动器"])
        assertFalse("开始" in sparse.getValue("用量"))
    }

    @Test fun aCodexThreadResumesByItsThreadAndARuntimeNotAtAll() {
        val codex = Session(machineId = "m", sessionId = "c0de-opaque", cwd = "/srv/deploy", agent = "codex",
            sourceKind = "codex_app_server", sourceThreadId = "019a-thread",
            capabilities = SessionCapabilities(read = true, send = true, terminal = true))
        val all = rows(codex, SessionInfo(sessionId = "c0de-opaque", version = "0.153.0"))
        assertEquals("0.153.0", all.getValue("运行")["Codex"])
        assertEquals("cd -- '/srv/deploy' && codex resume '019a-thread'", all.getValue("拷贝")["恢复命令"])
        val runtime = Session(machineId = "m", sessionId = "session-1", agent = "dsh", agentLabelHint = "DSH", sourceKind = "runtime",
            capabilities = SessionCapabilities(read = true, send = true))
        val copied = rows(runtime, SessionInfo(sessionId = "session-1")).getValue("拷贝")
        assertFalse("恢复命令" in copied)
        assertTrue("session id" in copied)
    }

    @Test fun readOnlySourcesAndBlockedApprovalsSayWhy() {
        val history = Session(sessionId = "h", sourceKind = "codex_history", readOnlyReason = "仅查看：外部进程正在使用此会话",
            capabilities = SessionCapabilities(read = true))
        assertEquals("仅查看：外部进程正在使用此会话", readOnlyNote(res, history))
        assertEquals("此会话仅可查看", readOnlyNote(res, history.copy(sourceKind = "codex_history", readOnlyReason = null,
            capabilities = SessionCapabilities(read = true, send = true))))
        val live = history.copy(sourceKind = "codex_app_server", capabilities = SessionCapabilities(read = true, send = true, attach = true))
        assertTrue("实时接入" in approvalBlockedReason(res, live))
        assertFalse("实时接入" in approvalBlockedReason(res, history))
    }

    /** #582 (iOS `infoShown` / `stateWord`): the ultracode row says on, off or unknown, for the user's running Claude Code on a hub that has it. */
    @Test fun ultracodeRowSaysOnOffOrUnknown() {
        val claude = Session(machineId = "m", sessionId = "s1", agent = "claude", status = "idle")
        fun row(s: Session, features: Set<String> = setOf("ultracode")) = sessionInfoSections(res, s, SessionInfo(sessionId = "s1"), zone, features = features)
            .flatMap { it.rows }.firstOrNull { it.label == "ultracode" }?.value
        assertEquals("开", row(claude.copy(ultracode = true)))
        assertEquals("关", row(claude.copy(ultracode = false)))
        assertEquals("未知", row(claude))
        assertEquals(null, row(claude, emptySet()))
        assertEquals("not a Persona conversation", null, row(claude.copy(assistant = true)))
        assertEquals("not Codex", null, row(claude.copy(agent = "codex")))
        assertEquals("not offline", null, row(claude.copy(state = "offline")))
    }
}
