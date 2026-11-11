package persona.android.platform

import android.app.Application
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.Base64
import persona.core.contracts.api.PairingRequest
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.*
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import persona.android.R
import java.security.MessageDigest
import java.security.SecureRandom
import java.security.cert.X509Certificate
import javax.inject.Inject
import javax.inject.Singleton

/** RFC 7636 PKCE binds the browser's one-time authorization code to the app that started the login. */
@Singleton
class PasswordBrowserLogin @Inject constructor(private val application: Application) {
    private val http = OkHttpClient.Builder().followRedirects(false).followSslRedirects(false).build()

    suspend fun login(address: String): PairingRequest {
        val base = normalizedPasswordAddress(application, address)
        val state = randomValue()
        val verifier = randomValue()
        val challenge = encode(MessageDigest.getInstance("SHA-256").digest(verifier.toByteArray(Charsets.US_ASCII)))
        val callback = CompletableDeferred<Uri>()
        synchronized(lock) {
            check(pending == null) { application.getString(R.string.app_password_login_in_progress) }
            pending = state to callback
        }
        try {
            val stateRequest = Request.Builder().url("${base}api/v1/auth/password/state").build()
            request(stateRequest) { response ->
                check(response.isSuccessful) { application.getString(R.string.app_password_login_unavailable) }
                val settings = Json.parseToJsonElement(response.body!!.string()).jsonObject
                check(settings["enabled"]?.jsonPrimitive?.booleanOrNull == true) { application.getString(R.string.app_password_login_disabled) }
            }
            val device = "${android.os.Build.MANUFACTURER} ${android.os.Build.MODEL}".take(70)
            val page = Uri.parse("${base}login/device").buildUpon().fragment(
                Uri.Builder().appendQueryParameter("state", state).appendQueryParameter("challenge", challenge)
                    .appendQueryParameter("kind", "phone").appendQueryParameter("name", device).build().encodedQuery
            ).build()
            withContext(Dispatchers.Main) {
                application.startActivity(Intent(Intent.ACTION_VIEW, page).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            }
            val result = withTimeout(5 * 60 * 1000L) { callback.await() }
            val code = requireNotNull(result.getQueryParameter("code"))
            require(code.matches(Regex("[A-Za-z0-9_-]{43}")))
            val body = buildJsonObject { put("code", code); put("verifier", verifier) }.toString()
            val exchange = Request.Builder().url("${base}api/v1/auth/password/exchange")
                .post(body.toRequestBody("application/json".toMediaType())).build()
            return request(exchange) { response ->
                check(response.isSuccessful) { application.getString(R.string.app_password_login_expired) }
                val data = Json.parseToJsonElement(response.body!!.string()).jsonObject
                val certificate = response.handshake?.peerCertificates?.firstOrNull() as? X509Certificate
                    ?: error(application.getString(R.string.app_password_login_certificate))
                val fingerprint = encode(MessageDigest.getInstance("SHA-256").digest(certificate.publicKey.encoded))
                val token = data.getValue("token").jsonPrimitive.content
                val deviceId = data.getValue("id").jsonPrimitive.content
                val hubId = data.getValue("hub_id").jsonPrimitive.content
                check(token.startsWith("jd_") && deviceId.startsWith("dev_"))
                PairingRequest(base.toString(), token, hub = hubId, fp = fingerprint, deviceId = deviceId)
            }
        } finally {
            synchronized(lock) { if (pending?.first == state) pending = null }
            callback.cancel()
        }
    }

    private suspend fun <T> request(request: Request, read: (okhttp3.Response) -> T): T =
        withContext(Dispatchers.IO) { http.newCall(request).execute().use(read) }

    companion object {
        private val lock = Any()
        private var pending: Pair<String, CompletableDeferred<Uri>>? = null
        fun complete(uri: Uri): Boolean = synchronized(lock) {
            val current = pending ?: return false
            if (uri.scheme != "persona" || uri.host != "password-login" ||
                uri.getQueryParameters("state").size != 1 || uri.getQueryParameters("code").size != 1 ||
                uri.getQueryParameter("state") != current.first) return false
            current.second.complete(uri)
        }
        private fun encode(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.URL_SAFE or Base64.NO_PADDING or Base64.NO_WRAP)
        private fun randomValue() = encode(ByteArray(32).also { SecureRandom().nextBytes(it) })
    }
}

internal fun normalizedPasswordAddress(context: Context, address: String): okhttp3.HttpUrl {
    val text = address.trim().let { if ("://" in it) it else "https://$it" }
    val url = text.toHttpUrl()
    require(url.isHttps && url.username.isEmpty() && url.password.isEmpty() && url.query == null && url.fragment == null && url.encodedPath == "/") {
        context.getString(R.string.app_password_login_invalid_address)
    }
    return url
}
