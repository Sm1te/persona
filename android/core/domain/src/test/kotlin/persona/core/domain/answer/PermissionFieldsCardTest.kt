package persona.core.domain.answer

import persona.core.contracts.Interaction
import persona.core.contracts.JsonCodec
import persona.core.contracts.Resolution
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * A permission card that declares fields (#4641, docs/protocol/protocol.md "Permission cards that need filling in"): its
 * form's Fill In & Allow sends the declared values once the required ones are filled, Deny sends none, and no Always.
 */
class PermissionFieldsCardTest {
    private val fields = """[{"key":"api_key","label":"API key","secret":true},{"key":"region","label":"Region","placeholder":"us-east-1"},
        {"key":"note","label":"Note","optional":true}]"""
    private fun card(fields: String = this.fields, alwaysHonored: Boolean = true) = JsonCodec.decodeFromString<Interaction>(
        """{"id":"ix","machine_id":"m","session_id":"s","kind":"permission","tool_name":"mail_add","summary":"Add the account",
            "always_honored":$alwaysHonored,"fields":$fields}""")
    private val filled = mapOf("api_key" to "sk-123", "region" to "eu-west-1")

    @Test fun fillInAndAllowSendsTheDeclaredValuesOnceTheRequiredOnesAreFilled() {
        val request = card()
        assertTrue(request.hasFieldsForm)
        assertTrue(request.needsFieldValues)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = mapOf("api_key" to "sk-123"))).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled + ("region" to "   "))).allowed)
        assertEquals(Resolution(allow = true, values = filled), AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled)).resolution)
        // An optional one goes when filled; an empty one is left out.
        assertEquals(Resolution(allow = true, values = filled + ("note" to "for work")),
            AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled + ("note" to "for work"))).resolution)
        assertEquals(Resolution(allow = true, values = filled),
            AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled + ("note" to ""))).resolution)
        // A key the card does not declare: nothing goes.
        assertFalse(AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled + ("other" to "x"))).allowed)
    }

    @Test fun denySendsNoValuesAndThereIsNoAlways() {
        val request = card()
        assertEquals(Resolution(allow = false), AnswerBuilder.build(request, AnswerAction.Deny).resolution)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Deny, AnswerDraft(values = filled)).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Always).allowed)
        assertFalse(AnswerBuilder.build(request, AnswerAction.Always, AnswerDraft(values = filled)).allowed)
    }

    @Test fun onlyTheConversationFillsThemIn() {
        val request = card()
        for (entry in AnswerEntry.entries.filter { it != AnswerEntry.Conversation }) {
            assertFalse("$entry", AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(values = filled), AnswerContext(entry = entry)).allowed)
            assertFalse("$entry", AnswerBuilder.build(request, AnswerAction.Allow, AnswerDraft(), AnswerContext(entry = entry)).allowed)
        }
        // Only optional fields: a quick Allow without values still goes, as before.
        val optional = card("""[{"key":"note","label":"Note","optional":true}]""")
        assertTrue(optional.hasFieldsForm)
        assertFalse(optional.needsFieldValues)
        assertEquals(Resolution(allow = true), AnswerBuilder.build(optional, AnswerAction.Allow, AnswerDraft(), AnswerContext(entry = AnswerEntry.Notification)).resolution)
    }

    @Test fun valuesNeverGoToACardWithoutFields() {
        val plain = card("[]")
        assertFalse(plain.hasFieldsForm)
        assertEquals(Resolution(allow = true), AnswerBuilder.build(plain, AnswerAction.Allow).resolution)
        assertFalse(AnswerBuilder.build(plain, AnswerAction.Allow, AnswerDraft(values = filled)).allowed)
        assertFalse(AnswerBuilder.build(plain, AnswerAction.Deny, AnswerDraft(values = filled)).allowed)
    }
}
