package persona.android.platform

import android.app.Application
import android.content.Context
import android.content.res.Configuration
import androidx.test.core.app.ApplicationProvider
import persona.android.R
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertThrows
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import java.util.Locale

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [35], application = Application::class)
class PasswordBrowserLoginTest {
    private val app: Application = ApplicationProvider.getApplicationContext()

    private fun localized(tag: String): Context = app.createConfigurationContext(
        Configuration(app.resources.configuration).apply { setLocale(Locale.forLanguageTag(tag)) },
    )

    @Test fun addressValidationKeepsItsRulesAndFollowsTheAppLanguage() {
        assertEquals("https://hub.example.com/", normalizedPasswordAddress(localized("en"), " hub.example.com ").toString())
        assertEquals("https://hub.example.com:8443/", normalizedPasswordAddress(localized("zh-Hans"), "https://hub.example.com:8443").toString())
        assertEquals("https://127.0.0.1:8443/", normalizedPasswordAddress(localized("ja"), "https://127.0.0.1:8443").toString())

        val english = localized("en")
        val chinese = localized("zh-Hans")
        val japanese = localized("ja")
        val expected = english.getString(R.string.app_password_login_invalid_address)
        val invalid = listOf(
            "http://hub.example.com",
            "https://user@hub.example.com",
            "https://user:password@hub.example.com",
            "https://hub.example.com/path",
            "https://hub.example.com?query=1",
            "https://hub.example.com#fragment",
        )
        for (address in invalid) {
            val error = assertThrows(address, IllegalArgumentException::class.java) {
                normalizedPasswordAddress(english, address)
            }
            assertEquals(address, expected, error.message)
        }
        val chineseError = assertThrows(IllegalArgumentException::class.java) {
            normalizedPasswordAddress(chinese, "http://hub.example.com")
        }
        val japaneseError = assertThrows(IllegalArgumentException::class.java) {
            normalizedPasswordAddress(japanese, "http://hub.example.com")
        }
        assertEquals(chinese.getString(R.string.app_password_login_invalid_address), chineseError.message)
        assertEquals(japanese.getString(R.string.app_password_login_invalid_address), japaneseError.message)
        assertNotEquals(expected, chineseError.message)
        assertNotEquals(expected, japaneseError.message)
        assertNotEquals(chineseError.message, japaneseError.message)
    }

    @Test fun allSixErrorsHaveDistinctTranslations() {
        val keys = listOf(
            R.string.app_password_login_in_progress,
            R.string.app_password_login_unavailable,
            R.string.app_password_login_disabled,
            R.string.app_password_login_expired,
            R.string.app_password_login_certificate,
            R.string.app_password_login_invalid_address,
        )
        val english = localized("en")
        val chinese = localized("zh-Hans")
        val japanese = localized("ja")
        for (key in keys) {
            val source = english.getString(key)
            val simplified = chinese.getString(key)
            val japan = japanese.getString(key)
            assertNotEquals(english.resources.getResourceEntryName(key), source, simplified)
            assertNotEquals(english.resources.getResourceEntryName(key), source, japan)
            assertNotEquals(english.resources.getResourceEntryName(key), simplified, japan)
        }
    }
}
