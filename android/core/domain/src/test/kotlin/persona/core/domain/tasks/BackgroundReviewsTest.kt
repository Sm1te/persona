package persona.core.domain.tasks

import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.put
import org.junit.Assert.*
import org.junit.Test
import persona.core.contracts.*
import persona.core.domain.lines.ChatLine

/** #5084: a review the background supervisor wrote is one line in the conversation, not a record behind the trays. */
class BackgroundReviewsTest {
    private fun review(id: String, by: String? = "background", verdict: String = "accepted") =
        FeedItem(id, "main", "2026-11-12T10:00:00Z", "review", "t", buildJsonObject {
            put("title", "Fix the login"); put("verdict", verdict); by?.let { put("by", it) }
        })
    private val todos = listOf(Todo("t", "Fix the login", origin = "main", supervision = Supervision(supervisor = "main")))
    private fun feedIds(lines: List<ChatLine>) =
        lines.filterIsInstance<ChatLine.Extra>().mapNotNull { (it.payload as? AssistantExtra.Feed)?.item?.id }

    @Test fun theHubMarksTheBackgroundSupervisorsReviews() {
        assertTrue(AssistantTaskCards.isBackgroundSupervisorReview(review("a")))
        assertTrue(AssistantTaskCards.isBackgroundSupervisorReview(review("b", verdict = "rework")))
        // the conversation's own review, another author, and a hub that does not say who wrote it
        assertFalse(AssistantTaskCards.isBackgroundSupervisorReview(review("c", by = "")))
        assertFalse(AssistantTaskCards.isBackgroundSupervisorReview(review("d", by = "judge")))
        assertFalse(AssistantTaskCards.isBackgroundSupervisorReview(review("e", by = null)))
        // only a review: another entry with the same field is nothing new
        assertFalse(AssistantTaskCards.isBackgroundSupervisorReview(review("f").copy(kind = "receipt")))
        assertFalse(AssistantTaskCards.isBackgroundSupervisorReview(FeedItem("g", "main", "2026-11-12T10:00:00Z", "review", "t")))
    }

    @Test fun itsReviewIsALineOfTheConversationAndLeavesTheRecord() {
        val feed = listOf(review("own", by = null), review("background"))
        assertEquals(listOf("background"), feedIds(AssistantTaskCards.lines(emptyList(), feed, emptyMap(), todos)))
        assertEquals(listOf("own"), AssistantTaskCards.backgroundFeed(feed).map { it.id })
        // the task being gone from the list changes nothing: the entry says which task it was
        assertEquals(listOf("background"), feedIds(AssistantTaskCards.lines(emptyList(), feed, emptyMap(), emptyList())))
    }

    @Test fun aHubWithoutTheMarkKeepsEveryReviewBehindTheTrays() {
        val feed = listOf(review("one", by = null), review("two", by = null, verdict = "rework"))
        assertEquals(emptyList<String>(), feedIds(AssistantTaskCards.lines(emptyList(), feed, emptyMap(), todos)))
        assertEquals(listOf("one", "two"), AssistantTaskCards.backgroundFeed(feed).map { it.id })
    }

    @Test fun itsReviewAlreadyAmongTheLinesStaysInTheForeground() {
        val shown = listOf<ChatLine>(ChatLine.Extra("feed-new", AssistantExtra.Feed(review("new"))))
        assertEquals(shown, AssistantConversation.project(shown, emptyList()).foreground)
        val own = listOf<ChatLine>(ChatLine.Extra("feed-own", AssistantExtra.Feed(review("own", by = null))))
        assertEquals(own, AssistantConversation.project(own, emptyList()).background)
    }
}
