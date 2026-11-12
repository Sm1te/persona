package persona.feature.settings

import androidx.annotation.StringRes
import androidx.compose.runtime.saveable.Saver
import persona.core.contracts.api.AddMailAccountRequest

/** The "Add mail account" form (iOS `AddMailAccountView` state) and what it sends. Pure: unit-tested in MailAccountFormTest. */
data class MailAccountForm(
    val kind: Kind = Kind.GMAIL,
    val email: String = "",
    val password: String = "",
    val imapHost: String = "",
    val imapPort: String = "993",
    val smtpHost: String = "",
    val smtpPort: String = "465",
) {
    enum class Kind(@StringRes val title: Int) {
        GMAIL(R.string.settings_src_mail_kind_gmail), OUTLOOK(R.string.settings_src_mail_kind_outlook),
        FEISHU(R.string.settings_src_mail_kind_feishu), IMAP(R.string.settings_src_mail_kind_imap),
    }

    /** "Connect" is possible: address and password typed (Gmail only through the app-password fallback; Outlook signs in with Microsoft). */
    fun canConnect(gmailPasswordAllowed: Boolean): Boolean =
        kind != Kind.OUTLOOK && email.contains('@') && password.isNotEmpty() && (kind != Kind.GMAIL || gmailPasswordAllowed)

    /** The POST /api/v1/mail/accounts body (iOS `save`); servers only when typed (else the domain preset). */
    fun request(): AddMailAccountRequest {
        val base = AddMailAccountRequest(name = email, email = email, auth = "password", password = password)
        return when (kind) {
            // Google's servers spelled out: a Google Workspace address has its own domain, which no preset knows.
            Kind.GMAIL -> base.copy(provider = "gmail", imapHost = "imap.gmail.com", imapPort = 993,
                smtpHost = "smtp.gmail.com", smtpPort = 465, smtpSecurity = "tls")
            Kind.OUTLOOK -> base.copy(provider = "outlook")
            Kind.FEISHU -> base.copy(provider = "feishu")
            Kind.IMAP -> {
                var r = base.copy(provider = "imap")
                if (imapHost.isNotEmpty()) r = r.copy(imapHost = imapHost, imapPort = imapPort.toIntOrNull())
                if (smtpHost.isNotEmpty()) {
                    // The security follows the port as sent: a zero-padded 587 is still STARTTLS (#5276, the web's #4879).
                    val port = smtpPort.toIntOrNull()
                    r = r.copy(smtpHost = smtpHost, smtpPort = port, smtpSecurity = if (port == 587) "starttls" else "tls")
                }
                r
            }
        }
    }

    /** Where a QQ / NetEase address typed under Other IMAP makes its authorization code; null for any other, and once servers are typed (iOS `codeGuide`). */
    val codeGuide: CodeGuide?
        get() = if (kind != Kind.IMAP || imapHost.isNotEmpty() || smtpHost.isNotEmpty()) null else CodeGuide.forDomain(email.substringAfterLast('@', "").trim().lowercase())

    companion object {
        /** Keeps the form across rotation / process death — without the password (it is not written to disk). */
        val Saver: Saver<MailAccountForm, Any> = Saver(
            save = { f -> arrayListOf(f.kind.name, f.email, f.imapHost, f.imapPort, f.smtpHost, f.smtpPort) },
            restore = { v ->
                @Suppress("UNCHECKED_CAST") val l = v as List<String>
                MailAccountForm(Kind.valueOf(l[0]), l[1], "", l[2], l[3], l[4], l[5])
            },
        )
    }
}

/**
 * A mail provider whose clients sign in with an authorization code made in its web mail, not the account's password. The hub says the
 * same steps for the same domains (server/internal/mail/preset.go, Guide; iOS CodeGuide): change them together.
 */
data class CodeGuide(val provider: Provider, val webMail: String) {
    enum class Provider(@StringRes val steps: Int, @StringRes val open: Int) {
        QQ(R.string.settings_src_mail_qq_code_steps, R.string.settings_src_mail_qq_open_web),
        NETEASE(R.string.settings_src_mail_netease_code_steps, R.string.settings_src_mail_netease_open_web),
    }

    companion object {
        fun forDomain(domain: String): CodeGuide? = when (domain) {
            "qq.com", "foxmail.com", "vip.qq.com" -> CodeGuide(Provider.QQ, "https://mail.qq.com")
            "163.com", "126.com", "yeah.net" -> CodeGuide(Provider.NETEASE, "https://mail.$domain")
            "vip.163.com", "vip.126.com" -> CodeGuide(Provider.NETEASE, "https://$domain")
            "188.com" -> CodeGuide(Provider.NETEASE, "https://www.188.com")
            else -> null
        }
    }
}
