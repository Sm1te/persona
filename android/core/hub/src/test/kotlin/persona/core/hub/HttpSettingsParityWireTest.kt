package persona.core.hub

import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.boolean
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.mockwebserver.MockResponse
import persona.core.hub.ProtocolCompatibilityMockServer as MockWebServer
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import persona.core.contracts.JsonCodec
import persona.core.contracts.api.AssistantConfigUpdate

/** The wire of #3048's settings: "Mute phone" (`mute_while_desktop`), "Blocked config directories", "Notify only in main chat" (`main_tasks_quiet`). */
class HttpSettingsParityWireTest {
    @Test fun muteWhileDesktopRoundTripsAndStaysOutOfThePutWhenUnknown() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpDeviceSettings(server.url("/").toString(), "example-token", "phone", OkHttpClient())
            for (value in listOf(true, false)) {
                server.enqueue(MockResponse().setBody("""{"input":true,"done":"long","mute_while_desktop":$value}"""))
                val read = api.preferences()
                assertEquals(value, read.muteWhileDesktop)
                server.takeRequest()
                server.enqueue(MockResponse().setBody("""{"input":true,"done":"long","mute_while_desktop":${!value}}"""))
                assertEquals(!value, api.savePreferences(read.copy(muteWhileDesktop = !value)).muteWhileDesktop)
                val body = JsonCodec.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
                assertEquals(!value, body.getValue("mute_while_desktop").jsonPrimitive.boolean)
            }
            // A hub without it: the phone does not know the setting, so it must not reset it (the hub's field has no omitempty).
            server.enqueue(MockResponse().setBody("""{"input":true,"done":"long"}"""))
            val old = api.preferences()
            assertNull(old.muteWhileDesktop)
            server.takeRequest()
            server.enqueue(MockResponse().setBody("""{"input":true,"done":"long"}"""))
            api.savePreferences(old)
            val body = JsonCodec.parseToJsonElement(server.takeRequest().body.readUtf8()).jsonObject
            assertFalse("mute_while_desktop" in body.keys)
        }
    }

    @Test fun claudeDirsAreReadAndBlockedPerMachine() = runBlocking {
        MockWebServer().use { server ->
            val http = OkHttpClient.Builder().retryOnConnectionFailure(false).build()
            val machines = HttpMachineManagement { path, method, body, _ ->
                http.newCall(Request.Builder().url(server.url(path)).method(method, body).build()).execute()
            }
            val state = """{"dirs":[{"path":"/home/me/.claude","default":true},{"path":"/home/me/.bot","blocked":true,"blocked_by":"~/.bot"}],
                "blocked":["~/.bot"],"home":"/home/me","live":true}"""
            server.enqueue(MockResponse().setBody(state))
            val read = machines.claudeDirs("desk/one")
            server.takeRequest().let {
                assertEquals("GET", it.method)
                assertEquals("/api/v1/machines/desk%2Fone/claude-dirs", it.path)
            }
            assertEquals(listOf("~/.bot"), read.blocked)
            assertTrue(read.dirs[1].blocked)

            server.enqueue(MockResponse().setBody(state.replace("[\"~/.bot\"]", "[\"~/.bot\",\"~/.automation/*\"]")))
            val saved = machines.setBlockedClaudeDirs("desk/one", listOf("~/.bot", "~/.automation/*"))
            server.takeRequest().let {
                assertEquals("PUT", it.method)
                assertEquals("/api/v1/machines/desk%2Fone/claude-dirs/blocked", it.path)
                assertEquals("""{"blocked":["~/.bot","~/.automation/*"]}""", it.body.readUtf8())
            }
            assertEquals(listOf("~/.bot", "~/.automation/*"), saved.blocked)
        }
    }

    @Test fun mainTasksQuietIsSentOnlyWhenSet() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient())
            val answer = """{"machine_id":"server","launcher":"claude","cwd":"","main_tasks_quiet":true}"""
            repeat(2) { server.enqueue(MockResponse().setBody(answer)) }
            assertEquals(true, api.updateConfig(AssistantConfigUpdate(mainTasksQuiet = true)).mainTasksQuiet)
            assertEquals("""{"main_tasks_quiet":true}""", server.takeRequest().body.readUtf8())
            api.updateConfig(AssistantConfigUpdate(timezone = "Europe/Paris"))
            assertEquals("""{"timezone":"Europe/Paris"}""", server.takeRequest().body.readUtf8())
        }
    }

    /** #4314: "Fold background rounds" goes out alone when set; an older hub that omits it reads as on. */
    @Test fun mainBackgroundFoldedIsSentOnlyWhenSetAndOnWhenMissing() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","launcher":"claude","cwd":"","main_background_folded":false}"""))
            val saved = api.updateConfig(AssistantConfigUpdate(mainBackgroundFolded = false))
            assertEquals("""{"main_background_folded":false}""", server.takeRequest().body.readUtf8())
            assertEquals(false, saved.mainBackgroundFolded)
            assertEquals(false, saved.backgroundFolded)
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","launcher":"claude","cwd":""}"""))
            val old = api.config()
            assertEquals(null, old.mainBackgroundFolded)
            assertEquals(true, old.backgroundFolded)
        }
    }

    /**
     * #5084: "The main chat supervises its own tasks" goes out alone when set; the background supervisor's conversation
     * is read, never written; an older hub sends none of it.
     */
    @Test fun mainSupervisesOwnTasksIsSentOnlyWhenSetAndTheBackgroundSupervisorIsReadOnly() = runBlocking {
        MockWebServer().use { server ->
            val api = HttpAssistantManagement(server.url("/").toString(), "test-token", OkHttpClient())
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","launcher":"claude","cwd":"","main_supervises_own_tasks":true,
                "background_supervisor":"bg-1","background_supervisor_machine_id":"server","background_supervisor_at":"2026-11-12T08:00:00Z"}"""))
            val saved = api.updateConfig(AssistantConfigUpdate(mainSupervisesOwnTasks = true))
            assertEquals("""{"main_supervises_own_tasks":true}""", server.takeRequest().body.readUtf8())
            assertEquals(true, saved.mainSupervisesOwnTasks)
            assertEquals("bg-1", saved.backgroundSupervisor)
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","launcher":"claude","cwd":"","main_supervises_own_tasks":false}"""))
            val fresh = api.config()
            assertEquals(false, fresh.mainSupervisesOwnTasks)
            assertEquals("not made until a task needs it", null, fresh.backgroundSupervisor)
            server.enqueue(MockResponse().setBody("""{"machine_id":"server","launcher":"claude","cwd":""}"""))
            val old = api.config()
            assertEquals(null, old.mainSupervisesOwnTasks)
            assertEquals(null, old.backgroundSupervisor)
        }
    }
}
