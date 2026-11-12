package persona.feature.settings

import android.content.Context
import android.content.res.Resources
import androidx.test.core.app.ApplicationProvider
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.AddMailAccountRequest
import persona.core.contracts.api.HubConnector
import persona.core.contracts.api.LarkStatus
import persona.core.contracts.api.MailAccount
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

// Robolectric: the words come from the app's resources, in Simplified Chinese (robolectric.properties).
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class MailAccountFormTest {
    private val res: Resources get() = ApplicationProvider.getApplicationContext<Context>().resources

    @Test fun imapRequestOnlyCarriesTypedServers() {
        val f = MailAccountForm(MailAccountForm.Kind.IMAP, "me@example.com", "pw", "", "993", "smtp.example.com", "587")
        val r = f.request()
        assertEquals("imap", r.provider)
        assertEquals("password", r.auth)
        assertEquals("me@example.com", r.name)
        assertNull(r.imapHost)
        assertNull(r.imapPort)
        assertEquals("smtp.example.com", r.smtpHost)
        assertEquals(587, r.smtpPort)
        assertEquals("starttls", r.smtpSecurity)
        val json = JsonCodec.encodeToString(AddMailAccountRequest.serializer(), r)
        assertFalse(json, json.contains("imap_host"))
        assertTrue(json, json.contains("\"smtp_security\":\"starttls\""))
    }

    /** #5276 (the web's #4879): the security follows the port as sent, so a zero-padded 587 is still STARTTLS. */
    @Test fun smtpSecurityFollowsTheParsedPort() {
        fun sent(port: String) = MailAccountForm(MailAccountForm.Kind.IMAP, "me@example.com", "pw", "", "993", "smtp.example.com", port)
            .request().let { it.smtpPort to it.smtpSecurity }
        assertEquals(587 to "starttls", sent("0587"))
        assertEquals(587 to "starttls", sent("00587"))
        assertEquals(465 to "tls", sent("0465"))
        assertEquals(2525 to "tls", sent("2525"))
    }

    @Test fun feishuAndGmailFallback() {
        assertEquals("feishu", MailAccountForm(MailAccountForm.Kind.FEISHU, "me@example.com", "pw").request().provider)
        val g = MailAccountForm(MailAccountForm.Kind.GMAIL, "me@example.com", "abcd efgh")
        assertFalse(g.canConnect(gmailPasswordAllowed = false))
        assertTrue(g.canConnect(gmailPasswordAllowed = true))
        val r = g.request()
        assertEquals("gmail", r.provider)
        assertEquals("imap.gmail.com" to 993, r.imapHost to r.imapPort)
        assertEquals(Triple("smtp.gmail.com", 465, "tls"), Triple(r.smtpHost, r.smtpPort, r.smtpSecurity))
        assertFalse(MailAccountForm(MailAccountForm.Kind.IMAP, "nope", "pw").canConnect(false))
    }

    @Test fun googleRedirectParsing() {
        assertEquals(GoogleSignIn.Result.Code("c1", "s1"), GoogleSignIn.parse("c1", "s1", null))
        assertEquals(GoogleSignIn.Result.Cancelled, GoogleSignIn.parse(null, "s1", "access_denied"))
        assertEquals(GoogleSignIn.Result.Failed("server_error", null), GoogleSignIn.parse(null, null, "server_error"))
        assertEquals(GoogleSignIn.Result.Cancelled, GoogleSignIn.parse(null, null, null))
    }

    @Test fun decodesAccounts() {
        val a = JsonCodec.decodeFromString(
            MailAccount.serializer(),
            """{"id":"a1","name":"x","email":"me@example.com","provider":"gmail","auth":"oauth2","username":"","imap_host":"imap.gmail.com","imap_port":993,"smtp_host":"","smtp_port":0,"smtp_security":"","status":"ok","error":"","extra":1}""",
        )
        assertEquals("Gmail · Google 账号登录", a.detail.shown)
        val bad = a.copy(status = "error", error = "Google authorization expired or was revoked")
        assertEquals("Google authorization expired or was revoked", bad.detail.shown)
        assertEquals("飞书邮箱", a.copy(auth = "password", provider = "feishu").detail.shown)
        assertEquals("Outlook · Microsoft 账号登录", a.copy(auth = "oauth2", provider = "outlook").detail.shown)
    }

    @Test fun connectorText() {
        assertEquals("", ConnectorText.mail(res, null))
        assertEquals("未添加", ConnectorText.mail(res, emptyList()))
        val ok = MailAccount(id = "a", email = "me@example.com", status = "ok")
        assertEquals("2 个账户", ConnectorText.mail(res, listOf(ok, ok.copy(id = "b"))))
        assertEquals("2 个 · 有错误", ConnectorText.mail(res, listOf(ok, ok.copy(id = "b", status = "error"))))
        assertEquals("", ConnectorText.lark(res, null))
        assertEquals("未配置", ConnectorText.lark(res, LarkStatus(configured = false)))
        assertEquals("连不上", ConnectorText.lark(res, LarkStatus(configured = true, connected = false)))
        assertEquals("已连接", ConnectorText.lark(res, LarkStatus(configured = true, connected = true)))
        assertEquals("我", ConnectorText.lark(res, LarkStatus(configured = true, connected = true, name = "我")))
        assertEquals("未打开", ConnectorText.device(res, 0))
        assertEquals("3 项已打开", ConnectorText.device(res, 3))
        val st = JsonCodec.decodeFromString(LarkStatus.serializer(), """{"configured":true,"connected":true,"name":"我","last_event_at":1700000000}""")
        assertEquals(1700000000L, st.lastEventAt)
    }

    @Test fun hubConnectorText() {
        val github = HubConnector(id = "github", name = "GitHub", status = "ok", account = "myname")
        assertEquals("已连接", ConnectorText.state(res, github))
        assertEquals("myname", ConnectorText.summary(res, github))
        val broken = github.copy(status = "error", error = "token expired")
        assertEquals("出错了", ConnectorText.state(res, broken))
        assertEquals("出错了", ConnectorText.summary(res, broken))
        val unconfigured = github.copy(status = "not_configured")
        assertEquals("未配置", ConnectorText.state(res, unconfigured))
        assertEquals("未配置", ConnectorText.summary(res, unconfigured))
    }

    /** QQ / NetEase addresses get where to make their authorization code (#5123); other addresses and kinds get none. */
    @Test fun qqAndNetEaseAddressesGetTheCodeGuide() {
        val f = MailAccountForm(MailAccountForm.Kind.IMAP, email = "me@QQ.com")
        assertEquals(CodeGuide(CodeGuide.Provider.QQ, "https://mail.qq.com"), f.codeGuide)
        assertTrue(res.getString(f.codeGuide!!.provider.steps).contains("生成授权码"))
        assertEquals(CodeGuide(CodeGuide.Provider.NETEASE, "https://mail.126.com"), f.copy(email = "me@126.com").codeGuide)
        assertEquals("https://vip.163.com", f.copy(email = "me@vip.163.com").codeGuide?.webMail)
        assertEquals("https://www.188.com", f.copy(email = "me@188.com").codeGuide?.webMail)
        assertNull(f.copy(email = "me@example.com").codeGuide)
        assertNull(f.copy(email = "me").codeGuide)
        assertNull(f.copy(kind = MailAccountForm.Kind.FEISHU).codeGuide)
        assertNull(f.copy(imapHost = "imap.example.com").codeGuide)
    }
}
