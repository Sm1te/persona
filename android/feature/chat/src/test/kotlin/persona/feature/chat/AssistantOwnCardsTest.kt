package persona.feature.chat

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.Interaction
import persona.core.contracts.SessionKey
import persona.core.designsystem.DecisionKind

/** A Persona conversation's own approval card is in the panel above its composer (#4883), says it waits for the user's
 *  approval, and leaves once answered; an Agent's chat is as it was. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistantOwnCardsTest {
    private val res get() = ApplicationProvider.getApplicationContext<Context>().resources
    private val chat = SessionKey("hub", "c1")
    private val bash = Interaction("ix_bash", machineId = "hub", sessionId = "c1", kind = "permission", toolName = "Bash")
    private val taskCard = Interaction("ix_task", machineId = "mac", sessionId = "t1", kind = "permission", toolName = "Edit")

    @Test fun theConversationsOwnCardIsInTheChatWithTheTrayClosed() {
        val panel = panelRequests(assistant = true, requests = listOf(bash, taskCard), key = chat, trayReview = null, shown = listOf(bash, taskCard))
        assertEquals(listOf(bash), panel) // a task's card stays its task's row in Needs you
        assertFalse(composerShown(panel, collapsed = false))
        assertTrue(composerShown(panel, collapsed = true))
    }

    @Test fun answeredItLeavesAndTheComposerComesBack() {
        val panel = panelRequests(assistant = true, requests = emptyList(), key = chat, trayReview = null, shown = emptyList())
        assertTrue(panel.isEmpty())
        assertTrue(composerShown(panel, collapsed = false))
    }

    @Test fun aCardOpenedFromTheTrayTakesThePanel() {
        assertEquals(listOf(taskCard), panelRequests(true, listOf(bash, taskCard), chat, "ix_task", listOf(bash, taskCard)))
    }

    @Test fun anAgentsChatIsAsItWas() {
        val requests = listOf(bash, taskCard)
        assertEquals(requests, panelRequests(false, requests, chat, null, requests))
    }

    @Config(qualifiers = "zh")
    @Test fun itSaysItWaitsForYourApprovalInChinese() {
        assertEquals("在等你批准", res.getString(DecisionKind.Command.labelRes))
        assertEquals("在等你批准", res.getString(DecisionKind.Tool.labelRes))
    }

    @Config(qualifiers = "en")
    @Test fun itSaysItWaitsForYourApprovalInEnglish() {
        assertEquals("Waiting for your approval", res.getString(DecisionKind.Command.labelRes))
    }

    @Config(qualifiers = "ja")
    @Test fun itSaysItWaitsForYourApprovalInJapanese() {
        assertEquals("承認を待っています", res.getString(DecisionKind.Command.labelRes))
    }
}
