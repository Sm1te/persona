package persona.feature.knowledge

import androidx.test.ext.junit.runners.AndroidJUnit4
import java.time.OffsetDateTime
import java.time.ZoneId
import java.util.Locale
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.api.RecordingMinutes
import persona.core.designsystem.LocalizedFormats

/** #4877: the minutes' time from `start` / `end`, in the recording header's date style; `when` only from an older hub. */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class KnowledgeMinutesTimeTest {
    private val zone = ZoneId.of("Asia/Shanghai")
    private val locales = listOf(Locale.US, Locale.SIMPLIFIED_CHINESE, Locale.JAPANESE)
    private fun minutes(start: String?, end: String?) = RecordingMinutes(time = "2026-11-10 14:00–14:32", start = start, end = end)
    private fun format(at: String, skeleton: String, locale: Locale) =
        LocalizedFormats.format(OffsetDateTime.parse(at).toInstant(), skeleton, locale, zone)

    @Test fun theSameDayShowsTheEndsTimeOnly() {
        val m = minutes("2026-11-10T14:00:00+08:00", "2026-11-10T14:32:10+08:00")
        assertEquals("2026年11月10日 14:00–14:32", minutesTime(m, Locale.SIMPLIFIED_CHINESE, zone))
        for (locale in locales) assertEquals(
            format("2026-11-10T14:00:00+08:00", LocalizedFormats.DATE_TIME, locale) + "–" + format("2026-11-10T14:32:10+08:00", LocalizedFormats.TIME, locale),
            minutesTime(m, locale, zone))
    }

    @Test fun twoDaysShowBothDates() {
        val m = minutes("2026-11-10T23:30:00+08:00", "2026-11-11T00:15:00+08:00")
        assertEquals("2026年11月10日 23:30 – 2026年11月11日 00:15", minutesTime(m, Locale.SIMPLIFIED_CHINESE, zone))
        for (locale in locales) assertEquals(
            format("2026-11-10T23:30:00+08:00", LocalizedFormats.DATE_TIME, locale) + " – " + format("2026-11-11T00:15:00+08:00", LocalizedFormats.DATE_TIME, locale),
            minutesTime(m, locale, zone))
    }

    @Test fun anOlderHubsWhenIsShownAsIs() {
        assertEquals("2026-11-10 14:00–14:32", minutesTime(minutes(null, null), Locale.US, zone))
        assertEquals("2026-11-10 14:00–14:32", minutesTime(minutes("2026-11-10T14:00:00+08:00", null), Locale.US, zone))
        assertEquals("2026-11-10 14:00–14:32", minutesTime(minutes("yesterday", "2026-11-10T14:32:00+08:00"), Locale.US, zone))
        assertEquals(null, minutesTime(RecordingMinutes(), Locale.US, zone))
    }
}
