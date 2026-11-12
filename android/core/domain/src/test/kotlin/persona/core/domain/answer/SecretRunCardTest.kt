package persona.core.domain.answer

import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Resolution
import persona.core.contracts.SecretRunRequest
import persona.core.domain.zh
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * The assistant's password card (`secret_run`, #1137, #1581): its own form's Run sends exactly the typed password, a
 * Decline sends nothing typed, and no other answer or entry gets out.
 */
class SecretRunCardTest {
    private val commands = listOf(
        "sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist",
        "sudo -S -k -p '' sh -c \"echo  two  spaces > /tmp/x\"",
    )
    private val password = """[{"key":"password","label":"Password","secret":true}]"""
    private fun card(input: String, fields: String = password, kind: String = "permission", tool: String = "secret_run") =
        JsonCodec.decodeFromString<Interaction>(
            """{"id":"ix","machine_id":"hub","session_id":"chat","kind":"$kind","tool_name":"$tool","tool_input":$input,"fields":$fields}""",
        )
    private val input = """{"machine":"macmini","machine_name":"Mac mini","purpose":"Install the launch daemon for the backup job",
        "commands":["sudo -S -k -p '' launchctl bootstrap system /Library/LaunchDaemons/com.example.backup.plist",
                    "sudo -S -k -p '' sh -c \"echo  two  spaces > /tmp/x\""],"delivery":"stdin"}"""
    private val typed = "correct horse battery staple"

    @Test fun runSendsExactlyTheTypedPassword() {
        for (request in listOf(card(input), card(input, fields = "[]"))) {
            val result = AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(password = typed))
            assertEquals(Resolution(allow = true, values = mapOf(SecretRunRequest.PASSWORD to typed)), result.resolution)
        }
        // Spaces and symbols are part of a password: sent as typed, never trimmed.
        val padded = " p@ss word "
        assertEquals(mapOf("password" to padded),
            AnswerBuilder.build(card(input), AnswerAction.Allow, AnswerDraft(password = padded)).resolution?.values)
        assertEquals("""{"allow":true,"values":{"password":"$typed"}}""",
            JsonCodec.encodeToString(Resolution.serializer(), Resolution(allow = true, values = mapOf("password" to typed))))
    }

    @Test fun declineIsBareAndCarriesNothingTyped() {
        assertEquals(Resolution(allow = false), AnswerBuilder.build(card(input), AnswerAction.Deny).resolution)
        assertFalse(AnswerBuilder.build(card(input), AnswerAction.Deny, AnswerDraft(message = "typed by mistake")).allowed)
        assertFalse(AnswerBuilder.build(card(input), AnswerAction.Deny, AnswerDraft(password = typed)).allowed)
    }

    /** Every other shape stays blocked: Always, Dismiss, an Allow without the password or with anything else. */
    @Test fun everyOtherShapeIsBlocked() {
        val request = card(input)
        val blocked = listOf(
            AnswerAction.Allow to AnswerDraft(),
            AnswerAction.Allow to AnswerDraft(password = ""),
            // Only spaces: the hub would answer the card with refused and run nothing.
            AnswerAction.Allow to AnswerDraft(password = "   "),
            AnswerAction.Allow to AnswerDraft(password = typed).copy(message = "why"),
            AnswerAction.Allow to AnswerDraft(password = typed).copy(feedback = "why"),
            AnswerAction.Allow to AnswerDraft(password = typed).copy(text = typed),
            AnswerAction.Always to AnswerDraft(),
            AnswerAction.Dismiss to AnswerDraft(),
        ) + (AnswerAction.entries - AnswerAction.Allow - AnswerAction.Deny).map { it to AnswerDraft(password = typed) }
        for ((action, draft) in blocked) assertFalse("${action.wire} $draft", AnswerBuilder.build(request, action, draft).allowed)
        // Not while it is being sent, not once it is over.
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(password = typed), AnswerContext(sending = true)).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(password = typed), AnswerContext(state = "unknown")).allowed)
    }

    /** Notifications, the lock screen, the watch and shortcuts never allow it, even if a value reached them. */
    @Test fun quickEntriesOnlyDecline() {
        for (request in listOf(card(input), card(input, fields = "[]"))) {
            for (entry in AnswerEntry.entries - AnswerEntry.Conversation) {
                val context = AnswerContext(entry = entry)
                for (action in AnswerAction.entries - AnswerAction.Deny) {
                    assertFalse("${entry.wire} ${action.wire}", AnswerBuilder.build(request, action, context = context).allowed)
                    assertFalse("${entry.wire} ${action.wire} typed",
                        AnswerBuilder.build(request, action, AnswerDraft(password = typed), context).allowed)
                }
                assertFalse(entry.wire, AnswerBuilder.build(request, AnswerAction.Deny, AnswerDraft(message = "typed by mistake"), context).allowed)
            }
            // A locked phone's conversation entry does not run it either.
            assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(password = typed), AnswerContext(locked = true)).allowed)
        }
        assertTrue(card(input).needsFieldValues)
    }

    /** A typed value goes only to a password card: any other card carrying one is blocked. */
    @Test fun valuesNeverGoToAnotherCard() {
        val bash = card("""{"command":"ls"}""", fields = "[]", tool = "Bash")
        assertEquals(Resolution(allow = true), AnswerBuilder.build(bash, AnswerAction.Allow).resolution)
        assertFalse(AnswerBuilder.build(bash, AnswerAction.Allow, AnswerDraft(password = typed)).allowed)
        assertFalse(AnswerBuilder.build(bash, AnswerAction.Deny, AnswerDraft(password = typed)).allowed)
        // Another card with a password field takes it in its fields form's values, not as a password card's (#4641).
        val mail = card("""{"account":"me@example.com"}""", tool = "mail_add")
        assertFalse(AnswerBuilder.build(mail, AnswerAction.Allow, AnswerDraft(password = typed)).allowed)
    }

    @Test fun readsTheComputerPurposeAndCommandsExactlyAsSent() {
        assertEquals(SecretRunRequest("Mac mini", "Install the launch daemon for the backup job", commands), card(input).secretRun)
        val sparse = card("""{"machine":"macmini","commands":["true", 1, null, "  ", "false"]}""").secretRun
        assertEquals(SecretRunRequest("macmini", "", listOf("true", "false")), sparse)
        // Another tool, or a card of another kind, is no password card.
        assertNull(card(input, tool = "Bash").secretRun)
        assertNull(card(input, kind = "question").secretRun)
        assertFalse(card(input, kind = "question").isSecretRun)
    }

    @Test fun headlineNamesTheComputerInsteadOfTheRawTool() {
        assertEquals("用你的密码在 Mac mini 上运行", card(input).headline.zh())
        assertEquals("用你的密码在 macmini 上运行", card("""{"machine":"macmini"}""").headline.zh())
    }
}
