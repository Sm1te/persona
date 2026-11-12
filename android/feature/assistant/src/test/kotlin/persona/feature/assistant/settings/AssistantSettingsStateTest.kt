package persona.feature.assistant.settings

import persona.core.contracts.Machine
import persona.core.contracts.api.AssistantConfig
import persona.core.contracts.api.AssistantConfigUpdate
import persona.core.contracts.api.AgentSkill
import persona.core.contracts.api.AssistantRefused
import persona.core.contracts.api.AssistantSkills
import persona.core.contracts.api.MemoryFile
import kotlinx.coroutines.test.runTest
import org.junit.Assert.*
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.annotation.Config
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.robolectric.RuntimeEnvironment
import java.time.Instant
import java.util.Locale

/** Runs with Robolectric so the page's text resolves from the Chinese resources (the default test language). */
@RunWith(AndroidJUnit4::class)
@Config(sdk = [35])
class AssistantSettingsStateTest {
    private val res get() = RuntimeEnvironment.getApplication().resources
    private val SettingsNotice.shown get() = text.resolve(res)
    private val MemoryEntry.typeLabel get() = typeLabel(res)

    private class FakeApi : AssistantSettingsApi {
        var config = AssistantConfig(machineId = "server", launcher = "claude", cwd = "/home/me/.persona/assistant",
            timezone = "Asia/Shanghai", notesEffective = "from hub.json", main = "main-id")
        val updates = mutableListOf<AssistantConfigUpdate>()
        var memory = listOf(
            MemoryFile("MEMORY.md", "- [早报格式](morning.md) — 一屏以内\n- [周报](weekly.md) — 先给草稿", "2026-10-31T08:00:00Z"),
            MemoryFile("morning.md", "---\nname: morning-format\ndescription: 早报一屏以内\nmetadata:\n  type: feedback\n---\n\n早报一屏以内，结论先行。",
                "2026-10-30T08:00:00Z"),
            MemoryFile("weekly.md", "---\nname: weekly\ntype: \"user\"\n---\n周报先给草稿。", "2026-10-31T09:00:00Z"),
        )
        val saved = mutableListOf<Pair<String, String>>()
        val deleted = mutableListOf<String>()
        var fail = false
        var skillsAnswer: () -> AssistantSkills = { AssistantSkills("server", "服务器", listOf(AgentSkill("lark-gateway", "飞书", "builtin"))) }

        override suspend fun config(): AssistantConfig { check(!fail) { "hub unreachable" }; return config }
        override suspend fun updateConfig(update: AssistantConfigUpdate): AssistantConfig {
            check(!fail) { "hub refused" }
            updates += update
            config = config.copy(
                machineId = update.machineId ?: config.machineId,
                launcher = update.launcher ?: config.launcher,
                // as the hub (#1855): a zone without timezone_picked is a device's sync, kept out while one was picked
                timezone = update.timezone?.takeUnless { update.timezonePicked == null && config.timezonePicked == true } ?: config.timezone,
                timezonePicked = update.timezonePicked ?: config.timezonePicked,
                notes = update.notes ?: config.notes,
                notesEffective = update.notes ?: config.notesEffective,
                main = if (update.main == AssistantConfigUpdate.CLEAR_MAIN) null else update.main ?: config.main,
                mainTasksQuiet = update.mainTasksQuiet ?: config.mainTasksQuiet,
                mainBackgroundFolded = update.mainBackgroundFolded ?: config.mainBackgroundFolded,
                mainSupervisesOwnTasks = update.mainSupervisesOwnTasks ?: config.mainSupervisesOwnTasks,
                defaultPermissionMode = update.defaultPermissionMode ?: config.defaultPermissionMode,
            )
            return config
        }
        override suspend fun memory(): List<MemoryFile> { check(!fail) { "assistant machine is offline" }; return memory }
        override suspend fun saveMemory(name: String, content: String) {
            check(!fail) { "write failed" }
            saved += name to content
            memory = memory.map { if (it.name == name) it.copy(content = content) else it }
        }
        override suspend fun deleteMemory(name: String) {
            check(!fail) { "delete failed" }
            deleted += name
            memory = memory.filterNot { it.name == name }
        }
        override suspend fun skills(): AssistantSkills = skillsAnswer()
    }

    private class Zone(override val phoneZone: String = "Asia/Shanghai", override var followPhone: Boolean = true) : TimezoneChoice

    private val server = Machine("server", name = "服务器", kind = "local", online = true, home = "/home/me")
    private val laptop = Machine("laptop", name = "笔记本", kind = "ssh", online = true, home = "/Users/me")
    private val offline = Machine("lab", name = "实验室", kind = "ssh", online = false)

    @Test fun futureDefaultsStayIndependentAndKeepExistingConversations() = runTest {
        val a = FakeApi().apply { config = config.copy(manager = "assistant", defaultPermissionMode = "default", permissionDefaults = persona.core.contracts.api.AssistantPermissionDefaults(1, "assistant", persona.core.contracts.api.AssistantPermissionDefaults.MODES)) }
        val b = FakeApi().apply { config = config.copy(manager = "work", defaultPermissionMode = "plan", permissionDefaults = persona.core.contracts.api.AssistantPermissionDefaults(1, "work", persona.core.contracts.api.AssistantPermissionDefaults.MODES)) }
        val first = AssistantSettingsState(a, Zone())
        val second = AssistantSettingsState(b, Zone())
        first.load(); second.load()
        first.setDefaultPermission("acceptEdits")
        second.setDefaultPermission("bypassPermissions")
        val restartedA = AssistantSettingsState(a, Zone()).also { it.load() }
        val restartedB = AssistantSettingsState(b, Zone()).also { it.load() }
        assertEquals("acceptEdits", restartedA.state.value.config?.defaultPermissionMode)
        assertEquals("bypassPermissions", restartedB.state.value.config?.defaultPermissionMode)
        assertEquals("main-id", a.config.main)
        assertEquals("main-id", b.config.main)
        assertEquals(AssistantConfigUpdate(defaultPermissionMode = "acceptEdits"), a.updates.single())
        assertEquals(AssistantConfigUpdate(defaultPermissionMode = "bypassPermissions"), b.updates.single())
        second.setDefaultPermission("default")
        assertEquals("default", second.state.value.config?.defaultPermissionMode)
    }

    @Test fun oldHubsNeverPretendToSaveDefaults() = runTest {
        val api = FakeApi()
        val model = AssistantSettingsState(api, Zone())
        model.load()
        model.setDefaultPermission("bypassPermissions")
        assertTrue(api.updates.isEmpty())
    }

    @Test fun quietTasksSwitchSavesAndComesBackOnFailure() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.load()
        // A hub without the setting: nothing to switch.
        state.setMainTasksQuiet(false)
        assertTrue(api.updates.isEmpty())
        api.config = api.config.copy(mainTasksQuiet = true)
        state.load()
        state.setMainTasksQuiet(false)
        assertEquals(false, api.updates.single().mainTasksQuiet)
        assertEquals(false, state.state.value.config?.mainTasksQuiet)
        api.fail = true
        state.setMainTasksQuiet(true)
        assertEquals(false, state.state.value.config?.mainTasksQuiet)
        assertTrue(state.state.value.notice?.error == true)
    }

    @Test fun backgroundFoldSwitchSavesAndComesBackOnFailure() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.load()
        // A hub without the setting: nothing to switch (the chat folds as if it were on).
        state.setMainBackgroundFolded(false)
        assertTrue(api.updates.isEmpty())
        assertTrue(state.state.value.config?.backgroundFolded == true)
        api.config = api.config.copy(mainBackgroundFolded = true)
        state.load()
        state.setMainBackgroundFolded(false)
        assertEquals(false, api.updates.single().mainBackgroundFolded)
        assertEquals(null, api.updates.single().mainTasksQuiet)
        assertEquals(false, state.state.value.config?.mainBackgroundFolded)
        api.fail = true
        state.setMainBackgroundFolded(true)
        assertEquals(false, state.state.value.config?.mainBackgroundFolded)
        assertTrue(state.state.value.notice?.error == true)
    }

    /** #5084: "The main chat supervises its own tasks", the background supervisor's off switch. */
    @Test fun superviseOwnTasksSwitchSavesAndComesBackOnFailure() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.load()
        // A hub without the background supervisor: nothing to switch.
        state.setMainSupervisesOwnTasks(true)
        assertTrue(api.updates.isEmpty())
        assertNull(state.state.value.config?.mainSupervisesOwnTasks)
        api.config = api.config.copy(mainSupervisesOwnTasks = false, backgroundSupervisor = "bg")
        state.load()
        state.setMainSupervisesOwnTasks(false)
        assertTrue("already off: nothing is sent", api.updates.isEmpty())
        state.setMainSupervisesOwnTasks(true)
        assertEquals(true, api.updates.single().mainSupervisesOwnTasks)
        assertEquals(null, api.updates.single().mainBackgroundFolded)
        assertEquals(true, state.state.value.config?.mainSupervisesOwnTasks)
        assertEquals("the conversation it made stays the hub's to say", "bg", state.state.value.config?.backgroundSupervisor)
        api.fail = true
        state.setMainSupervisesOwnTasks(false)
        assertEquals(true, state.state.value.config?.mainSupervisesOwnTasks)
        assertTrue(state.state.value.notice?.error == true)
    }

    @Test fun readsWhereItRuns() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.load()
        assertEquals("server", state.state.value.config?.machineId)
        assertTrue("the phone and the hub already agree on the zone", api.updates.isEmpty())
    }

    @Test fun movingWarnsThatMemoryAndConversationsStayAndStartsAFreshMainConversation() = runTest {
        val api = FakeApi()
        var moved = 0
        val state = AssistantSettingsState(api, Zone()) { moved++ }
        state.load()
        state.requestMove(server, listOf(server, laptop))
        assertNull("the machine it already runs on is not a move", state.state.value.move)

        state.requestMove(laptop, listOf(server, laptop))
        val move = requireNotNull(state.state.value.move)
        assertEquals("换到「笔记本」上运行？", move.title(res))
        assertTrue(move.text(res).startsWith("记忆和会话记录在原来那台机器上，不会跟过去。"))
        assertTrue(move.text(res).contains("「服务器」"))
        assertFalse(move.text(res).contains("离线"))
        assertTrue("nothing is written before the user confirms", api.updates.isEmpty())

        state.cancelMove()
        assertNull(state.state.value.move)
        assertTrue(api.updates.isEmpty())

        state.requestMove(laptop, listOf(server, laptop))
        state.confirmMove()
        // The main conversation is a transcript on the old machine, so it is cleared with the move.
        assertEquals(listOf(AssistantConfigUpdate(machineId = "laptop", main = AssistantConfigUpdate.CLEAR_MAIN)), api.updates)
        with(state.state.value) {
            assertEquals("laptop", config?.machineId)
            assertNull(config?.main)
            assertNull(this.move)
            assertNotNull("memory is read again from the new machine", memory.entries)
            assertFalse(notice!!.error)
        }
        assertEquals(1, moved)
    }

    /** The outbox bound the old machine's main conversation: a move tells it, before the list is read again. */
    @Test fun aMoveTellsTheOutboxTheMainConversationChangedBeforeRefreshing() = runTest {
        val api = FakeApi()
        val calls = mutableListOf<String>()
        val state = AssistantSettingsState(api, Zone(), mainChanged = { calls += "main changed" }) { calls += "moved" }
        state.load()
        state.requestMove(laptop, listOf(server, laptop))
        state.confirmMove()
        assertEquals(listOf("main changed", "moved"), calls)

        // A failing outbox does not undo the move or keep the list from being read again.
        val failing = AssistantSettingsState(FakeApi(), Zone(), mainChanged = { error("pairing closed") }) { calls += "moved again" }
        failing.load()
        failing.requestMove(laptop, listOf(server, laptop))
        failing.confirmMove()
        assertEquals("laptop", failing.state.value.config?.machineId)
        assertEquals("moved again", calls.last())
    }

    @Test fun movingToAnOfflineMachineSaysSoAndAFailedMoveChangesNothing() = runTest {
        val api = FakeApi()
        var moved = 0
        var mainChanged = 0
        val state = AssistantSettingsState(api, Zone(), mainChanged = { mainChanged++ }) { moved++ }
        state.load()
        state.requestMove(offline, listOf(server, offline))
        assertTrue(state.state.value.move!!.text(res).contains("「实验室」现在离线"))

        api.fail = true
        state.confirmMove()
        assertEquals("server", state.state.value.config?.machineId)
        assertEquals("main-id", state.state.value.config?.main)
        assertEquals("没换成，Persona 还在「服务器」上运行，请重试。", state.state.value.notice?.shown)
        assertEquals(0, moved)
        assertEquals("the main conversation did not change", 0, mainChanged)
    }

    @Test fun theHubZoneFollowsThePhoneUnlessTheUserPicksOne() = runTest {
        val api = FakeApi()
        val zone = Zone(phoneZone = "Europe/Paris")
        val state = AssistantSettingsState(api, zone)
        state.load()
        assertEquals(listOf(AssistantConfigUpdate(timezone = "Europe/Paris")), api.updates)
        assertEquals("Europe/Paris", state.state.value.config?.timezone)

        state.chooseZone("Asia/Tokyo")
        assertEquals("picking a zone needs following the phone turned off first", 1, api.updates.size)
        state.setFollowPhone(false)
        assertFalse(zone.followPhone)
        assertEquals("turning following off keeps the hub's zone, now picked",
            AssistantConfigUpdate(timezone = "Europe/Paris", timezonePicked = true), api.updates.last())
        state.chooseZone("Asia/Tokyo")
        assertEquals(AssistantConfigUpdate(timezone = "Asia/Tokyo", timezonePicked = true), api.updates.last())
        assertEquals(true, api.config.timezonePicked)

        // A phone that no longer follows leaves the hub alone when the page opens again.
        val reopened = AssistantSettingsState(api, zone)
        reopened.load()
        assertEquals(3, api.updates.size)
        assertEquals("Asia/Tokyo", reopened.state.value.config?.timezone)
        assertFalse(reopened.state.value.followPhone)

        reopened.setFollowPhone(true)
        assertTrue(zone.followPhone)
        assertEquals(AssistantConfigUpdate(timezone = "Europe/Paris", timezonePicked = false), api.updates.last())
        assertEquals(false, api.config.timezonePicked)
    }

    /** #1855: a zone picked on another device stays, here and on the Persona tab, and this phone's switch shows it. */
    @Test fun aZonePickedOnAnotherDeviceStays() = runTest {
        val api = FakeApi().apply { config = config.copy(timezone = "Europe/Berlin", timezonePicked = true) }
        val zone = Zone(phoneZone = "America/New_York") // this phone still followed its own zone
        val state = AssistantSettingsState(api, zone)
        state.load()
        assertTrue("the picked zone is left alone", api.updates.isEmpty())
        assertFalse("the switch goes by the hub", state.state.value.followPhone)
        assertFalse("and the phone keeps that", zone.followPhone)
        assertEquals("Europe/Berlin", followPhoneZone(api, Zone(phoneZone = "America/New_York")).timezone)
        assertTrue(api.updates.isEmpty())
        // An app from before (a zone without timezone_picked) does not move it either.
        api.updateConfig(AssistantConfigUpdate(timezone = "America/New_York"))
        assertEquals("Europe/Berlin", api.config.timezone)
    }

    /** #1855: a zone picked on this phone before the hub kept the choice is told to the hub once the page opens. */
    @Test fun aZonePickedBeforeTheHubKeptItGoesToTheHub() = runTest {
        val api = FakeApi().apply { config = config.copy(timezone = "Europe/Berlin") }
        AssistantSettingsState(api, Zone(phoneZone = "America/New_York", followPhone = false)).load()
        assertEquals(listOf(AssistantConfigUpdate(timezone = "Europe/Berlin", timezonePicked = true)), api.updates)
        assertEquals(true, api.config.timezonePicked)
    }

    /** Match phone the hub could not be told about goes back, with the reason. */
    @Test fun matchPhoneGoesBackWhenTheHubCannotBeTold() = runTest {
        val api = FakeApi()
        val zone = Zone()
        val state = AssistantSettingsState(api, zone).also { it.load() }
        api.fail = true
        state.setFollowPhone(false)
        assertTrue(state.state.value.followPhone)
        assertTrue(zone.followPhone)
        assertEquals(true, state.state.value.notice?.error)
    }

    @Test fun phoneZoneSyncOnlySendsWhatDiffers() = runTest {
        assertEquals("Europe/Paris", zoneToSync(Zone("Europe/Paris"), "Asia/Shanghai"))
        assertEquals("Europe/Paris", zoneToSync(Zone("Europe/Paris"), null))
        assertNull(zoneToSync(Zone("Asia/Shanghai"), "Asia/Shanghai"))
        assertNull(zoneToSync(Zone("Europe/Paris", followPhone = false), "Asia/Shanghai"))
        val api = FakeApi()
        assertEquals("Asia/Shanghai", followPhoneZone(api, Zone()).timezone)
        assertTrue(api.updates.isEmpty())
        assertEquals("Europe/Paris", followPhoneZone(api, Zone("Europe/Paris")).timezone)
        assertEquals(listOf(AssistantConfigUpdate(timezone = "Europe/Paris")), api.updates)
    }

    @Test fun notesSaveWhatTheUserWroteEvenEmpty() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.load()
        assertEquals("never saved in the app: shows what hub.json tells the assistant", "from hub.json", state.state.value.config!!.notesShown())
        assertTrue(state.saveNotes("代码在 ~/code 下"))
        assertEquals("代码在 ~/code 下", state.state.value.config!!.notesShown())
        assertTrue(state.saveNotes(""))
        assertEquals(AssistantConfigUpdate(notes = ""), api.updates.last())
        assertEquals("", state.state.value.config!!.notesShown())

        api.fail = true
        assertFalse(state.saveNotes("lost?"))
        assertTrue(state.state.value.notice!!.error)
        assertEquals("", state.state.value.config!!.notes)
    }

    @Test fun memoryIsReadEditedAndDeleted() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        state.loadMemory()
        val memory = state.state.value.memory
        // Newest first, titled from the index, the index itself kept apart.
        assertEquals(listOf("weekly.md", "morning.md"), memory.entries?.map { it.file.name })
        assertEquals(listOf("周报", "早报格式"), memory.entries?.map { it.title })
        assertEquals("MEMORY.md", memory.index?.name)
        val morning = memory.entries!!.last()
        assertEquals("早报一屏以内", morning.description)
        assertEquals("反馈", morning.typeLabel)
        assertEquals("早报一屏以内，结论先行。", morning.body)
        val weekly = memory.entries!!.first()
        assertEquals("from the index line when the front matter has none", "先给草稿", weekly.description)
        assertEquals("用户", weekly.typeLabel)

        val edited = morning.file.content.replace("结论先行", "先说结论")
        assertTrue(state.saveMemory("morning.md", edited))
        assertEquals(listOf("morning.md" to edited), api.saved)
        assertEquals("早报一屏以内，先说结论。", state.state.value.memory.entries!!.first { it.file.name == "morning.md" }.body)
        assertEquals("记忆已更新", state.state.value.notice?.shown)
        assertFalse(state.state.value.notice!!.error)

        api.fail = true
        assertFalse(state.saveMemory("morning.md", "won't land"))
        assertEquals("没保存上，修改的内容还在，请重试。", state.state.value.notice?.shown)
        assertFalse(state.deleteMemory("weekly.md"))
        assertEquals(2, state.state.value.memory.entries?.size)

        api.fail = false
        assertTrue(state.deleteMemory("weekly.md"))
        assertEquals(listOf("weekly.md"), api.deleted)
        assertEquals(listOf("morning.md"), state.state.value.memory.entries?.map { it.file.name })
        assertEquals("已删除这条记忆", state.state.value.notice?.shown)
        assertFalse(state.state.value.notice!!.error)
    }

    @Test fun unreadableMemoryIsReportedNotThrown() = runTest {
        val api = FakeApi()
        api.fail = true
        val state = AssistantSettingsState(api, Zone())
        state.loadMemory()
        assertTrue(state.state.value.memory.failed)
        assertNull(state.state.value.memory.entries)
        state.load()
        assertTrue(state.state.value.loadFailed)
        assertNull(state.state.value.config)
    }

    @Test fun memoryFilesParseWithAndWithoutFrontMatter() {
        val plain = parseMemory(MemoryFile("plain-note.md", "只有正文\n第二行"))
        assertEquals("plain-note", plain.title)
        assertNull(plain.description)
        assertNull(plain.typeLabel)
        assertEquals("只有正文\n第二行", plain.body)
        val unterminated = parseMemory(MemoryFile("open.md", "---\nname: open\n正文"))
        assertEquals("open", unterminated.title)
        assertEquals("---\nname: open\n正文", unterminated.body)
        val quoted = parseMemory(MemoryFile("q.md", "---\nname: 'quoted name'\ndescription: \"说明\"\ntype: project\n---\n\n"))
        assertEquals("quoted name", quoted.title)
        assertEquals("说明", quoted.description)
        assertEquals("项目", quoted.typeLabel)
        assertEquals("", quoted.body)
        assertEquals(mapOf("a.md" to ("标题 A" to "说明"), "b.md" to ("标题 B" to null)),
            memoryIndexLines("# Memory\n- [标题 A](a.md) — 说明\n- [标题 B](b.md)\n不是索引的一行"))
    }

    @Test fun pathsAndZonesReadWell() {
        assertEquals("~/.persona/assistant", homeRelative("/home/me/.persona/assistant", "/home/me"))
        assertEquals("~/.persona/assistant", homeRelative("/home/me/.persona/assistant", "/home/me/"))
        assertEquals("/srv/assistant", homeRelative("/srv/assistant", "/home/me"))
        assertEquals("/home/user/x", homeRelative("/home/user/x", "/home/me"))
        assertEquals("", homeRelative("", null))
        val winter = Instant.parse("2026-01-15T00:00:00Z")
        assertEquals("UTC+8", zoneOffset("Asia/Shanghai", winter))
        assertEquals("UTC+5:30", zoneOffset("Asia/Kolkata", winter))
        assertEquals("UTC-5", zoneOffset("America/New_York", winter))
        assertEquals("UTC", zoneOffset("Europe/London", winter))
        assertNull(zoneOffset("Not/AZone", winter))
        val zones = pickableZones(winter)
        assertTrue("Asia/Shanghai" in zones)
        assertFalse(zones.any { it.startsWith("Etc/") })
        assertTrue(zones.indexOf("America/New_York") < zones.indexOf("Asia/Shanghai"))
        assertTrue(zoneMatches("America/New_York", "new york", Locale.ENGLISH))
        assertTrue(zoneMatches("Asia/Shanghai", "shanghai", Locale.ENGLISH))
        assertFalse(zoneMatches("Asia/Shanghai", "Paris", Locale.ENGLISH))
    }

    @Test fun skillsListTheMachinesAndAnEmptyListIsTheEmptyState() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        assertFalse("nothing is shown as empty before the hub answers", state.state.value.skills.empty)
        state.loadSkills()
        with(state.state.value.skills) {
            assertEquals(listOf("lark-gateway"), list?.skills?.map { it.name })
            assertTrue(list!!.skills.single().builtin)
            assertEquals("服务器", list?.machine)
            assertFalse(empty)
        }
        api.skillsAnswer = { AssistantSkills("server", "服务器", emptyList()) }
        state.loadSkills()
        with(state.state.value.skills) {
            assertTrue(empty)
            assertFalse(failed)
            assertNull(reason)
        }
    }

    @Test fun skillsThatCannotBeReadShowTheHubsReasonAndKeepWhatWasListed() = runTest {
        val api = FakeApi()
        val state = AssistantSettingsState(api, Zone())
        api.skillsAnswer = { throw AssistantRefused("conflict", "还没有选助手所在的机器") }
        state.loadSkills()
        with(state.state.value.skills) {
            assertTrue(failed)
            assertTrue(empty)
            assertEquals("还没有选助手所在的机器", reason)
        }
        // Without the hub's words (a dropped connection) the page says it in its own.
        api.skillsAnswer = { error("socket closed") }
        state.loadSkills()
        assertNull(state.state.value.skills.reason)

        api.skillsAnswer = { AssistantSkills("server", null, listOf(AgentSkill("weekly-report"))) }
        state.loadSkills()
        api.skillsAnswer = { error("socket closed") }
        state.loadSkills()
        with(state.state.value.skills) {
            assertEquals(listOf("weekly-report"), list?.skills?.map { it.name })
            assertFalse("a failed reload keeps the list", empty)
            assertFalse(loading)
        }
    }
}
