package persona.feature.inbox

import org.junit.Assert.assertEquals
import org.junit.Test

/** A topic card's reply preview (#576 G134, iOS `LarkTopicReplies`): the newest two replies, and how many are earlier. */
class LarkTopicCardTest {
    private fun m(id: String, system: Boolean = false) = LarkMessage(id, "u", "U", parts = listOf(LarkPart.Text(id)), system = system)

    @Test fun theCardShowsTheNewestTwoRepliesNotTheTopic() {
        val page = listOf(m("topic"), m("r1"), m("r2"), m("joined", system = true), m("r3"))
        assertEquals(listOf("r2", "r3"), LarkTopicReplies.newest("topic", page).map { it.id })
        assertEquals("the topic alone: no replies", emptyList<String>(), LarkTopicReplies.newest("topic", listOf(m("topic"))).map { it.id })
    }

    @Test fun earlierCountsWhatTheCardDoesNotShow() {
        assertEquals(3L, LarkTopicReplies.earlier(5, shown = 2))
        assertEquals("one reply shown of one", 0L, LarkTopicReplies.earlier(1, shown = 1))
        assertEquals("not read yet: the lines they will take", 4L, LarkTopicReplies.earlier(6, shown = 0))
        assertEquals(0L, LarkTopicReplies.earlier(0, shown = 0))
    }
}
