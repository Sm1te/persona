package persona.core.domain.sort

import persona.core.domain.zhAny

import persona.core.domain.zh
import persona.core.contracts.CoreText
import persona.core.contracts.JsonCodec
import persona.core.contracts.Session
import persona.core.contracts.SessionCapabilities
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/** The old App's SessionSourcesTest (81dc6928), rewritten for the domain rules. */
class SessionSourceTest {
    private fun session(json: String) = JsonCodec.decodeFromString<Session>(json)

    @Test fun legacyAndMissingMetadataDoNotInventASource() {
        val old = session("""{"session_id":"aabbccdd","status":"working"}""")
        assertTrue(old.canRead && old.canSend && old.canInterrupt && old.canApprove && old.canTerminal)
        assertFalse(old.canAttach)
        assertNull(old.source)
        assertNull(old.sourceSubtitle)
        val missing = old.copy(sourceKind = "codex_app_server")
        assertFalse(missing.canRead || missing.canSend || missing.canInterrupt || missing.canApprove || missing.canTerminal || missing.canAttach)
    }

    @Test fun historyIsReadOnlyAndResumesByItsThread() {
        val history = session(
            """{"machine_id":"m","session_id":"opaque","cwd":"/tmp/project","source_kind":"codex_history","source_thread_id":"thread",""" +
                """"read_only_reason":"外部进程正在使用此会话","capabilities":{"read":true}}""",
        )
        assertTrue(history.canRead)
        assertFalse(history.canSend || history.canInterrupt || history.canApprove || history.canTerminal || history.canAttach)
        assertEquals("Codex 历史 · 仅查看：外部进程正在使用此会话", (history.sourceSubtitle?.zh()).zhAny())
        assertEquals(SessionSourceInfo(CoreText.of(CoreText.Key.SourceCodexHistory), readOnlyReason = CoreText.plain("外部进程正在使用此会话")), history.source)
        assertEquals("Codex 历史 · 仅查看：外部进程正在使用此会话", (history.readOnlyNotice.zh()).zhAny())
        assertEquals("Codex", (history.agentLabel).zhAny())
        // A reason the node already starts with the Chinese "Read-only: " prefix is not prefixed twice.
        assertEquals("Codex 历史 · 仅查看：未发现可共享的 app-server 入口",
            (history.copy(readOnlyReason = "仅查看：未发现可共享的 app-server 入口").sourceSubtitle).zhAny())
        // Only a session in a terminal can be resumed there.
        assertNull(history.resumeCommand())
        val terminal = history.copy(capabilities = SessionCapabilities(read = true, terminal = true))
        assertEquals("cd -- '/tmp/project' && codex resume 'thread'", (terminal.resumeCommand()).zhAny())
    }

    @Test fun liveMenusRequireExplicitCapabilities() {
        val live = session(
            """{"session_id":"opaque","source_kind":"codex_app_server","status":"working","agent_name":"j-3f2a",""" +
                """"capabilities":{"read":true,"send":true,"attach":true},"attached":false}""",
        )
        assertTrue(live.canSend && live.canAttach)
        assertFalse(live.canTerminal || live.canInterrupt || live.canApprove)
        assertFalse(live.copy(attached = true).canAttach)
        assertTrue(live.copy(capabilities = live.capabilities!!.copy(interrupt = true)).canInterrupt)
        assertEquals("Codex 实时 · j-3f2a", (live.sourceSubtitle).zhAny())
        assertEquals("Claude 后台 · 仅查看：此来源尚未允许发送", (live.copy(sourceKind = "claude_bg", agentName = null, capabilities = SessionCapabilities(read = true)).sourceSubtitle).zhAny())
        // Claude outside Persona's terminals: named for where it runs; sends when the node says it can.
        val desktop = live.copy(sourceKind = "claude_desktop", agentName = null, capabilities = SessionCapabilities(read = true, send = true))
        assertEquals("Claude 桌面版", desktop.sourceSubtitle.zhAny())
        assertTrue(desktop.canSend)
        assertFalse(desktop.canInterrupt || desktop.canTerminal)
        assertEquals("终端里的 Claude · 仅查看：此来源尚未允许发送", (desktop.copy(sourceKind = "claude_cli", capabilities = SessionCapabilities(read = true)).sourceSubtitle).zhAny())
        // Codex is not Claude: no modes, no /model or /clear even when it can send.
        assertFalse(live.canChangeModes || live.canTypeClaudeCommands)
    }

    @Test fun runtimeSessionsAreNamedByTheirRuntime() {
        val dsh = session(
            """{"session_id":"session-1","agent":"dsh","agent_label":"DSH","source_kind":"runtime","agent_name":"dsh-web",""" +
                """"capabilities":{"read":true,"approve":true}}""",
        )
        assertEquals("DSH", (dsh.agentLabel).zhAny())
        assertEquals("DSH · dsh-web · 仅查看：此来源尚未允许发送", (dsh.sourceSubtitle).zhAny())
        assertTrue(dsh.canApprove)
        assertFalse(dsh.canTerminal || dsh.canAttach)
        val writable = dsh.copy(capabilities = SessionCapabilities(read = true, send = true))
        assertEquals("DSH · dsh-web", (writable.sourceSubtitle).zhAny())
        assertEquals(READ_ONLY, writable.readOnlyNotice)
        // An identity conflict does not name the agent it cannot tell apart.
        val conflict = writable.copy(sourceKind = "identity_conflict")
        assertEquals("身份冲突", (conflict.sourceSubtitle).zhAny())
    }

    @Test fun claudeOnlyControlsArePositive() {
        val claude = session("""{"session_id":"s","status":"idle","pane_id":"w1:p1"}""")
        assertTrue(claude.canChangeModes && claude.canTypeClaudeCommands)
        assertFalse(claude.copy(agent = "traex").canChangeModes)
        assertFalse(claude.copy(agent = "dsh").canChangeModes)
        assertFalse(claude.copy(status = "offline").canChangeModes)
        // Persona's conversations change modes but have no pane to type /model or /clear into.
        val persona = claude.copy(assistant = true)
        assertTrue(persona.canChangeModes)
        assertFalse(persona.canTypeClaudeCommands)
        assertFalse(claude.copy(paneId = null).canTypeClaudeCommands)
    }

    @Test fun onlyClaudeInAPaneSteersItsQueue() {
        val claude = session("""{"session_id":"s","status":"working","pane_id":"w1:p1"}""")
        assertTrue(claude.steersQueue)
        assertTrue(claude.copy(agent = "claude").steersQueue)
        // Send now is Claude Code's own key in its terminal: no pane, another agent, Persona or a source can't take it.
        assertFalse(claude.copy(paneId = null).steersQueue)
        assertFalse(claude.copy(paneId = " ").steersQueue)
        assertFalse(claude.copy(agent = "codex").steersQueue)
        assertFalse(claude.copy(agent = "traex").steersQueue)
        assertFalse(claude.copy(assistant = true).steersQueue)
        assertFalse(claude.copy(sourceKind = "claude_cli", capabilities = SessionCapabilities(read = true, send = true)).steersQueue)
    }

    @Test fun modeCapabilityHidesUnsupportedClaude() {
        val desktop = session(
            """{"session_id":"desktop","status":"working","agent":"claude","source_kind":"claude_desktop",""" +
                """"capabilities":{"read":true,"send":true,"modes":false}}""",
        )
        assertTrue(desktop.canSend)
        assertFalse(desktop.canChangeModes)

        // An older hub omits modes: keep the old agent-based behavior rather than treating absence as a refusal.
        val oldDesktop = session(
            """{"session_id":"old-desktop","status":"working","agent":"claude","source_kind":"claude_desktop",""" +
                """"capabilities":{"read":true,"send":true}}""",
        )
        assertTrue(oldDesktop.canChangeModes)
    }

    @Test fun modeCapabilityShowsSupportedRuntime() {
        val orca = session(
            """{"session_id":"orca","status":"working","agent":"claude","source_kind":"runtime",""" +
                """"capabilities":{"read":true,"send":true,"modes":true}}""",
        )
        assertTrue(orca.canChangeModes)
        val agy = orca.copy(sessionId = "agy", agent = "agy")
        assertTrue(agy.canChangeModes)
        assertFalse(agy.copy(status = "offline").canChangeModes)
        assertFalse(agy.copy(capabilities = agy.capabilities!!.copy(send = false)).canChangeModes)

        val oldAgy = session(
            """{"session_id":"old-agy","status":"working","agent":"agy","source_kind":"runtime",""" +
                """"capabilities":{"read":true,"send":true}}""",
        )
        assertFalse(oldAgy.canChangeModes)
    }

    @Test fun codexOnItsAppServerChangesItsEffortOnly() {
        val live = session(
            """{"session_id":"opaque","source_kind":"codex_app_server","agent":"codex","status":"idle","effort":"high",""" +
                """"efforts":["low","high","ultra"],"capabilities":{"read":true,"send":true}}""",
        )
        assertEquals(listOf("low", "high", "ultra"), (live.efforts).zhAny())
        assertTrue(live.canChangeEffortOnly)
        assertFalse(live.canChangeModes || live.canTypeClaudeCommands)
        // A herdr pane joined on its app-server: no source, the levels the hub lists say so.
        assertTrue(session("""{"session_id":"s","agent":"codex","status":"idle","efforts":["medium"]}""").canChangeEffortOnly)
        // Without levels (the terminal route, a compatible CLI), without sending, offline, or a Claude session: not this way.
        assertFalse(live.copy(efforts = emptyList()).canChangeEffortOnly)
        assertFalse(live.copy(capabilities = SessionCapabilities(read = true)).canChangeEffortOnly)
        assertFalse(live.copy(status = "offline").canChangeEffortOnly)
        assertFalse(session("""{"session_id":"s","status":"idle","efforts":["low"]}""").canChangeEffortOnly)
        assertTrue(session("""{"session_id":"s","status":"idle"}""").efforts.isEmpty())
    }

    @Test fun resumeCommandsQuoteWhatTheyCarry() {
        val claude = session("""{"session_id":"s'1","cwd":"/tmp/my dir"}""")
        assertEquals("cd -- '/tmp/my dir' && claude --resume 's'\"'\"'1'", (claude.resumeCommand()).zhAny())
        assertEquals("traex resume 'abc'", (claude.copy(agent = "traex", cwd = "").resumeCommand(id = "abc")).zhAny())
        assertEquals("cd -- '/x' && codex resume 's'\"'\"'1'", (claude.copy(agent = "codex").resumeCommand(cwd = "/x")).zhAny())
    }
}
