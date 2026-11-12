import Foundation
import Testing
@testable import Persona

/// The meeting conversation on the phone (#3862 phase 1; contract docs/assistant/meeting-minutes.md「Meeting conversation」,
/// the phone's side docs/apple/recording.md「The meeting conversation」): what a room meeting's upload carries and what its
/// `200` means, the hub's state of the meeting (`GET …/live`, `meeting_live`), the private mark, Just me, the follow-up,
/// asks the room can't take, and the words for each refusal. The hub is a `FakeHub`; nothing reaches a network.
@MainActor struct MeetingConversationTests {
    // MARK: Fixtures

    private func meeting(_ attendance: RecordingAttendance?, active: Bool = true, lines: [RecordingSentence] = []) -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .phone, title: "Weekly",
                               locale: "zh-CN", active: active)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = attendance
        if !lines.isEmpty { e.segments = [RecordingSegment(index: 0, start: 0, duration: 60, sentences: lines)] }
        return e
    }

    private func room(_ role: MeetingAttendance, private mark: Bool = false) -> RecordingAttendance {
        RecordingAttendance.room(role, private: mark, photos: false)
    }

    private func json(_ e: RecordingEntry) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: JSON.encoder.encode(RecordingUpload(e))) as? [String: Any] ?? [:]
    }

    private func body(_ request: LocalRequest?) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: request?.body ?? Data("{}".utf8)) as? [String: Any] ?? [:]
    }

    private func decodeRoom(_ text: String) throws -> MeetingRoom {
        try JSON.decoder.decode(MeetingRoom.self, from: Data(text.utf8))
    }

    private func conversation(_ hub: FakeHub) -> MeetingConversation {
        let client = hub.client
        return MeetingConversation(deps: MeetingConversation.Deps(api: { client }, group: { "" }, now: { .now }, wait: { _ in }))
    }

    private func path(_ id: UUID, _ route: String) -> String { "/api/v1/recordings/\(id.uuidString.lowercased())/\(route)" }

    /// Waits (5 s at most) until `done`: the model's own task got as far as the case needs.
    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async {
        for _ in 0..<500 {
            if done() { return }
            try? await Task.sleep(for: .milliseconds(10))
        }
        #expect(done(), "timed out waiting: \(what)", sourceLocation: sourceLocation)
    }

    /// Counts a fake hub route's calls, so one can answer differently the first time.
    private final class Calls {
        var count = 0
        func next() -> Int { count += 1; return count }
    }

    // MARK: The upload

    /// A room meeting's role travels inside `room`, whatever it is (off too), with its private mark beside it; never the
    /// attending session's top-level fields, which an older manager would read.
    @Test func aRoomMeetingUploadsItsRoleInsideRoom() throws {
        for role in MeetingAttendance.allCases {
            let body = try json(meeting(room(role, private: role == .listener)))
            #expect((body["room"] as? [String: String]) == ["attendance": role.rawValue], "\(role)")
            #expect(body["private"] as? Bool == (role == .listener), "\(role)")
            #expect(body["attendance"] == nil && body["attending_session"] == nil, "\(role)")
        }
        var unmarked = room(.participant)
        unmarked.privateMark = nil
        #expect(try json(meeting(unmarked))["private"] as? Bool == false, "no choice kept on the phone goes as not private")
    }

    /// Meeting content never reaches the main chat through a room meeting (the owner's rule after a real meeting): its
    /// upload names no attending session and no top-level role for any role, even with a session or the main chat's pick
    /// set on it (nothing makes either for a room meeting; this pins the shape), so neither this hub nor an older manager
    /// after a rollback feeds its lines to a chat. Only the room hears them; only Just me reaches the main chat.
    @Test func aRoomMeetingNeverNamesAnAttendingSession() throws {
        for role in MeetingAttendance.allCases {
            var a = room(role)
            a.session = "server/other"
            a.mainPicked = true
            let e = meeting(a)
            let body = try json(e)
            #expect(body["attendance"] == nil && body["attending_session"] == nil, "\(role)")
            #expect((body["room"] as? [String: String]) == ["attendance": role.rawValue], "\(role)")
            #expect(RecordingUpload.attendanceMark(e) == "room|\(role.rawValue)", "\(role)")
        }
    }

    /// A meeting started before the hub listed the conversation keeps uploading as #3746 did (the main chat picked for it).
    @Test func aMeetingStartedWithoutTheConversationUploadsAsBefore() throws {
        let e = meeting(RecordingAttendance(role: .listener, mainPicked: true))
        let body = try json(e)
        #expect(body["attendance"] as? String == "listener")
        #expect(body["room"] == nil && body["private"] == nil)
        #expect(RecordingUpload.attendanceMark(e) == "listener|")
        #expect(!e.roomMeeting)
    }

    /// What live lines and asks wait for names the room; only a phone meeting started as one is a room meeting.
    @Test func theMarkAndTheFlagSayRoom() {
        let e = meeting(room(.participant))
        #expect(e.roomMeeting)
        #expect(e.attended, "a room meeting Persona takes part in is asked like any attended one")
        #expect(RecordingUpload.attendanceMark(e) == "room|participant")
        #expect(RecordingUpload.attendanceMark(meeting(room(.off))) == "room|off")
        var watch = e
        watch.source = .watch
        #expect(!watch.roomMeeting)
        var note = e
        note.meeting = nil
        #expect(!note.roomMeeting)
    }

    /// A meeting keeps what it started with: the room, the photos and the private choice are kept with the recording, and a
    /// copy from before them reads as a #3746 meeting.
    @Test func aRoomMeetingStaysOne() throws {
        let a = RecordingAttendance.room(.listener, private: true, photos: true, at: 5)
        #expect(a.room && a.photos && a.privateMark == true && a.role == .listener)
        let back = try JSONDecoder().decode(RecordingAttendance.self, from: JSONEncoder().encode(a))
        #expect(back == a)
        let old = try JSONDecoder().decode(RecordingAttendance.self, from: Data(#"{"role":"listener"}"#.utf8))
        #expect(!old.room && !old.photos && old.privateMark == nil)
    }

    /// Whether the hub lists the two is kept for a start without the app in front (Siri, the Control, the Action button).
    @Test func theHubsFlagsAreKept() {
        let store = UserDefaults(suiteName: "meeting-conversation-\(UUID())")!
        #expect(MeetingAttendanceDefaults.conversationFeature == "meeting_conversation")
        #expect(MeetingAttendanceDefaults.photosFeature == "meeting_photos")
        MeetingAttendanceDefaults.remember(conversation: true, photos: false, store)
        #expect(store.bool(forKey: MeetingAttendanceDefaults.conversationKey))
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.photosKey))
        MeetingAttendanceDefaults.remember(conversation: false, photos: true, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.conversationKey))
        #expect(store.bool(forKey: MeetingAttendanceDefaults.photosKey))

        // Every hub hello keeps them (`AppModel.handle`), not only the Recordings page: a hub gone back to a manager without
        // the conversation leaves a cold one-tap start a #3746 meeting.
        MeetingAttendanceDefaults.remember(conversation: true, photos: true, store)
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance"], role: .listener, store)
        #expect(store.bool(forKey: MeetingAttendanceDefaults.offeredKey))
        #expect(MeetingAttendanceDefaults.cachedRole(store) == .listener)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.conversationKey))
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.photosKey))
        MeetingAttendanceDefaults.remember(hello: ["meeting_attendance", "meeting_conversation"], role: nil, store)
        #expect(store.bool(forKey: MeetingAttendanceDefaults.conversationKey))
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.photosKey))
        MeetingAttendanceDefaults.remember(hello: ["meeting_conversation", "meeting_photos"], role: nil, store)
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.offeredKey))
        #expect(!store.bool(forKey: MeetingAttendanceDefaults.conversationKey), "only beside meeting_attendance")
    }

    /// One tap (the drawer, Siri, the Control) on a hub with the conversation starts a room meeting with the default role,
    /// not private; on a hub without it, a #3746 meeting. Reads the last hello's flags as a start without the app does
    /// (no hub in this model), so the user defaults it reads are set for the case and put back after.
    @Test func aOneTapStartIsARoomMeetingThatIsNotPrivate() throws {
        let store = UserDefaults.standard
        let keys = [MeetingAttendanceDefaults.offeredKey, MeetingAttendanceDefaults.roleKey,
                    MeetingAttendanceDefaults.conversationKey, MeetingAttendanceDefaults.photosKey]
        let before = keys.map { store.object(forKey: $0) }
        defer { for (key, value) in zip(keys, before) { store.set(value, forKey: key) } }
        store.set(true, forKey: MeetingAttendanceDefaults.offeredKey)
        store.set(MeetingAttendance.listener.rawValue, forKey: MeetingAttendanceDefaults.roleKey)
        MeetingAttendanceDefaults.remember(conversation: true, photos: false, store)
        let model = AppModel()
        guard model.hubFeatures.isEmpty else { return } // a hello already in: the flags are the hub's, not this case's
        let start = MeetingAttendees.startAttendance(model)
        #expect(start.room)
        #expect(start.role == MeetingAttendees.defaultRole(model))
        #expect(start.privateMark == false)
        #expect(!start.photos)
        #expect(start.session == nil && !start.mainPicked, "the room attends, no chat")
        let body = try json(meeting(start))
        #expect(body["attendance"] == nil && body["attending_session"] == nil, "nothing an older manager would feed a chat")
        MeetingAttendanceDefaults.remember(conversation: false, photos: false, store)
        #expect(!MeetingAttendees.startAttendance(model).room, "without the conversation: as #3746 starts it")
    }

    // MARK: The upload's 200

    /// A recording hub's side of the sync: its list (empty), and every upload answered by `answer` (the body sent).
    private func recordingHub(_ e: RecordingEntry, answer: @escaping @MainActor ([String: Any]) -> String) -> FakeHub {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/recordings", json: #"{"days":14,"recordings":[],"deleted":[]}"#)
        hub.on("PUT", "/api/v1/recordings/\(e.id.uuidString.lowercased())") { req in
            let body = (try? JSONSerialization.jsonObject(with: req.body ?? Data())) as? [String: Any] ?? [:]
            return Data(answer(body).utf8)
        }
        return hub
    }

    private final class Heard {
        struct Room { var id: UUID; var room: MeetingRoom; var privateMark: Bool? }
        var rooms: [Room] = []
        var stored: [UUID] = []
        var withRoom = false
    }

    /// A `200` without `room` is an older manager after a rollback: the meeting waits (nothing is settled, so no live line
    /// or ask goes), but it is not stopped for good. The next `200` with `room` settles it, hands the room on and lets the
    /// live lines go.
    @Test func anAnswerWithoutRoomPausesTheMeetingUntilOneWithRoom() async throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "meeting-conversation-\(UUID())")
        let lib = RecordingLibrary(root: root)
        let e = meeting(room(.participant), lines: [RecordingSentence(start: 1, end: 4, text: "line")])
        try lib.save(e)
        let heard = Heard()
        let hub = recordingHub(e) { body in
            let revision = (body["revision"] as? NSNumber)?.int64Value ?? 0
            let room = heard.withRoom ? #","room":{"state":"starting"},"private":false"# : ""
            return #"{"id":"x","revision":\#(revision),"status":"stored","attendance":"participant"\#(room)}"#
        }
        let client = hub.client
        let sync = RecordingSync(library: lib, api: { client })
        sync.onRoom = { id, room, mark in heard.rooms.append(Heard.Room(id: id, room: room, privateMark: mark)) }
        sync.onAttendanceStored = { heard.stored.append($0) }

        await sync.syncNow()
        let put = "/api/v1/recordings/\(e.id.uuidString.lowercased())"
        try #require(hub.sent("PUT", put).count == 1)
        #expect(sync.roomMissing.contains(e.id))
        #expect(lib.entry(e.id)?.attendance?.stopped == nil, "not stopped for good: a later 200 may carry room again")
        #expect(lib.entry(e.id)?.attendance?.role == .participant)
        let waiting = try #require(lib.entry(e.id))
        #expect(!sync.attendanceSettled(waiting))
        #expect(heard.rooms.isEmpty && heard.stored.isEmpty)

        heard.withRoom = true
        lib.change(e.id) { $0.title = "Weekly, later" }
        await sync.syncNow()
        try #require(hub.sent("PUT", put).count == 2)
        #expect(try body(hub.sent("PUT", put).last)["room"] as? [String: String] == ["attendance": "participant"], "room on every upload")
        #expect(!sync.roomMissing.contains(e.id))
        let settled = try #require(lib.entry(e.id))
        #expect(sync.attendanceSettled(settled))
        #expect(heard.rooms.map(\.id) == [e.id])
        #expect(heard.rooms.first?.room.known == .starting)
        #expect(heard.rooms.first?.privateMark == false)
        #expect(heard.stored == [e.id], "the live lines can go")
    }

    /// While an older manager holds the room meeting with Persona off, the Live Activity says Persona isn't there (no
    /// role, no answer, so no Ask or Turn Off), as the page does.
    @Test func theActivityShowsAMeetingTheHubTookOffAsNotAttended() {
        let e = meeting(room(.listener))
        let shown = RecordingActivity.content(entry: e, recording: true, interrupted: false, elapsed: 10, previous: nil, now: .now, answer: "x")
        #expect(shown.persona == "listener")
        let missing = RecordingActivity.content(entry: e, recording: true, interrupted: false, elapsed: 10, previous: nil, now: .now,
                                                answer: "x", roomMissing: true)
        #expect(missing.persona == nil && missing.answer == nil)
    }

    // MARK: The room's state

    /// The room's state is an open set: one this app doesn't know reads as starting. Its conversation has ids from the
    /// start on, never with none or deleted.
    @Test func aRoomStateTheAppDoesntKnowReadsAsStarting() throws {
        #expect(try decodeRoom(#"{"state":"photos_pending"}"#).known == .starting)
        let ready = try decodeRoom(#"{"state":"ready","machine_id":"server","session_id":"room-1"}"#)
        #expect(ready.known == .ready)
        #expect(ready.key(group: "") == SessionKey(machineID: "server", sessionID: "room-1"))
        #expect(ready.key(group: "work") == SessionKey(group: "work", machineID: "server", sessionID: "room-1"))
        let unavailable = try decodeRoom(#"{"state":"unavailable","problem":"machine_offline"}"#)
        #expect(unavailable.known == .unavailable && unavailable.problem == "machine_offline")
        #expect(unavailable.key(group: "") == nil, "no ids yet")
        #expect(try decodeRoom(#"{"state":"ended","machine_id":"server","session_id":"room-1"}"#).key(group: "") != nil,
                "an ended room's thread can still be read")
        #expect(try decodeRoom(#"{"state":"deleted","machine_id":"server","session_id":"room-1"}"#).key(group: "") == nil)
        #expect(try decodeRoom(#"{"state":"none"}"#).key(group: "") == nil)
    }

    /// Answers on separate requests arrive in any order: an upload's or a live POST's late answer never takes the room back
    /// to an earlier state (ready and unavailable may replace each other), and a missing private mark or follow-up says
    /// nothing.
    @Test func aLateAnswerNeverTakesTheRoomBack() throws {
        let c = conversation(FakeHub())
        let id = UUID(), other = UUID()
        let ready = try decodeRoom(#"{"state":"ready","machine_id":"server","session_id":"room-1"}"#)
        c.uploaded(id, room: ready, privateMark: false)
        c.applyLive(id, room: try decodeRoom(#"{"state":"ended","machine_id":"server","session_id":"room-1"}"#), privateMark: nil, followup: nil)
        c.uploaded(id, room: ready, privateMark: nil)
        #expect(c.meeting(id).room?.known == .ended)
        #expect(c.meeting(id).privateMark == false)
        #expect(c.roomKey(id) == SessionKey(machineID: "server", sessionID: "room-1"))

        c.uploaded(other, room: ready, privateMark: nil)
        c.applyLive(other, room: try decodeRoom(#"{"state":"unavailable","problem":"start_failed","machine_id":"server","session_id":"room-1"}"#),
                    privateMark: true, followup: RecordingUpload.SessionRef(machineID: "server", sessionID: "f1"))
        #expect(c.meeting(other).room?.known == .unavailable)
        #expect(c.roomKey(other) != nil, "the thread so far stays while the room is unavailable")
        #expect(c.meeting(other).privateMark == true)
        #expect(c.followupKey(other) == SessionKey(machineID: "server", sessionID: "f1"))
        c.uploaded(other, room: ready, privateMark: nil)
        #expect(c.meeting(other).room?.known == .ready)
    }

    /// `GET …/live` reads everything once; `?parts=` replaces only the parts asked for (one asked for and absent is not
    /// set), and the revisions only go up.
    @Test func aReadOfSomePartsReplacesOnlyThose() async throws {
        let hub = FakeHub(), id = UUID()
        hub.on("GET", path(id, "live")) { req in
            if req.query["parts"] == nil {
                return Data("""
                    {"next":2,"attendance":"participant","fed":2,"private":false,
                     "room":{"state":"ready","machine_id":"server","session_id":"room-1"},
                     "followup":{"machine_id":"server","session_id":"f1"},
                     "asks":[{"id":"A1","question":"Q","question_source":"spoken","state":"answered","answer":"A","via":"room","at":12}],
                     "revs":{"asks":4,"room":2,"private":1,"followup":1}}
                    """.utf8)
            }
            return Data(#"{"private":true,"revs":{"private":3}}"#.utf8)
        }
        let c = conversation(hub)
        await c.refresh(id)
        var m = c.meeting(id)
        #expect(m.loaded && !m.missing)
        #expect(m.room?.known == .ready)
        #expect(c.roomKey(id) == SessionKey(machineID: "server", sessionID: "room-1"))
        #expect(c.followupKey(id) == SessionKey(machineID: "server", sessionID: "f1"))
        #expect(m.privateMark == false)
        #expect(m.asks.map(\.questionSource) == ["spoken"])
        #expect(m.revs == ["asks": 4, "room": 2, "private": 1, "followup": 1])

        await c.refresh(id, parts: ["private", "followup"])
        #expect(hub.sent("GET", path(id, "live")).last?.query["parts"] == "private,followup")
        m = c.meeting(id)
        #expect(m.privateMark == true)
        #expect(m.followup == nil, "asked for and absent: not set")
        #expect(m.room?.known == .ready, "not asked for: kept")
        #expect(m.asks.count == 1)
        #expect(m.revs["private"] == 3 && m.revs["room"] == 2)
    }

    /// `404` / `410`: the hub has no such recording; the page falls back to what it has.
    @Test func aRecordingTheHubDoesntHaveIsMissing() async {
        let hub = FakeHub(), id = UUID()
        hub.on("GET", path(id, "live")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) }
        let c = conversation(hub)
        await c.refresh(id)
        #expect(c.meeting(id).missing)
        #expect(c.roomKey(id) == nil)
    }

    @Test func newerPartsAreThoseWithAHigherNumberOrNone() {
        let known: [String: Int64] = ["room": 3, "asks": 12, "private": 1]
        #expect(MeetingConversation.newerParts(changed: ["room", "asks"], revs: ["room": 3, "asks": 13], known: known) == ["asks"])
        #expect(MeetingConversation.newerParts(changed: ["followup", "private"], revs: ["followup": 1], known: known) == ["followup", "private"])
        #expect(MeetingConversation.newerParts(changed: ["room", "room"], revs: [:], known: known) == ["room"])
        #expect(MeetingConversation.newerParts(changed: [], revs: ["room": 9], known: known).isEmpty)
    }

    /// A watched page reads everything once; a `meeting_live` event reads only the newer parts the phone shows in phase 1
    /// (later phases' `lines` alone reads nothing); a reconnect reads everything again; nobody watching, nothing is read.
    @Test func anEventReadsOnlyTheNewerPartsThePhoneShows() async throws {
        let hub = FakeHub(), id = UUID(), owner = UUID()
        let live = path(id, "live")
        hub.on("GET", live) { req in
            if req.query["parts"] == nil {
                return Data(#"{"room":{"state":"ready","machine_id":"server","session_id":"room-1"},"private":false,"asks":[],"revs":{"room":2,"private":1,"asks":0}}"#.utf8)
            }
            return Data(#"{"private":true,"revs":{"private":2}}"#.utf8)
        }
        let c = conversation(hub)
        c.watch(id, by: owner)
        await until("the first read") { c.meeting(id).loaded }
        #expect(hub.sent("GET", live).count == 1)

        c.event(recordingID: id.uuidString.lowercased(), changed: ["lines", "room"], revs: ["lines": 40, "room": 2])
        try await Task.sleep(for: .milliseconds(100))
        #expect(hub.sent("GET", live).count == 1, "nothing newer the phone reads")

        c.event(recordingID: id.uuidString.lowercased(), changed: ["private"], revs: ["private": 2])
        await until("the private mark read") { hub.sent("GET", live).count == 2 }
        #expect(hub.sent("GET", live).last?.query["parts"] == "private")
        await until("the mark applied") { c.meeting(id).privateMark == true }

        c.reconnected()
        await until("the read after a reconnect") { hub.sent("GET", live).count == 3 }
        #expect(hub.sent("GET", live).last?.query["parts"] == nil)

        c.unwatch(id, by: owner)
        try await Task.sleep(for: .milliseconds(50))
        let read = hub.sent("GET", live).count
        c.event(recordingID: id.uuidString.lowercased(), changed: ["private"], revs: ["private": 9])
        try await Task.sleep(for: .milliseconds(100))
        #expect(hub.sent("GET", live).count == read, "a recording nobody watches and this phone isn't recording")
    }

    /// The follow-up waits while the hub would still answer `409 meeting_running`: the recording runs, or its end isn't
    /// stored by the hub yet.
    @Test func aMeetingRunsUntilTheHubHasItsEnd() {
        let c = conversation(FakeHub())
        let live = meeting(room(.listener))
        #expect(c.running(live))
        var stopped = live
        stopped.active = false
        #expect(c.running(stopped), "its ended_at not uploaded yet")
    }

    /// A library with one stopped room meeting, and a sync whose record (`sync.json`) says what the hub stored of it:
    /// `stored` hours before now (nil: nothing), and whether its latest revision was refused.
    private func stoppedMeeting(stored hoursAgo: Double?, refused: Bool = false, synced: Bool = false)
        throws -> (RecordingEntry, RecordingLibrary, RecordingSync) {
        let root = FileManager.default.temporaryDirectory.appending(path: "meeting-running-\(UUID())")
        let lib = RecordingLibrary(root: root)
        let made = meeting(room(.listener), active: false, lines: [RecordingSentence(start: 1, end: 4, text: "line")])
        try lib.save(made)
        let e = try #require(lib.entry(made.id))
        var store = RecordingSync.Store()
        if let hoursAgo { store.synced[e.id] = synced ? e.revision : Int64((Date.now.timeIntervalSince1970 - hoursAgo * 3600) * 1000) }
        if refused { store.rejected[e.id] = e.revision }
        try JSONEncoder().encode(store).write(to: root.appendingPathComponent("sync.json"))
        return (e, lib, RecordingSync(library: lib, api: { nil }))
    }

    private func conversation(_ hub: FakeHub, _ lib: RecordingLibrary, _ sync: RecordingSync) -> MeetingConversation {
        let client = hub.client
        return MeetingConversation(deps: MeetingConversation.Deps(api: { client }, group: { "" }, now: { .now }, wait: { _ in }),
                                   library: lib, sync: sync)
    }

    /// The hub's whole read of a recording whose room is `none` (Persona off from the start): nothing of the room to wait
    /// for, so what the follow-up waits for is the upload alone.
    private func readWithoutARoom(_ e: RecordingEntry, _ lib: RecordingLibrary, _ sync: RecordingSync) async -> MeetingConversation {
        let hub = FakeHub()
        hub.on("GET", path(e.id, "live"), json: #"{"room":{"state":"none"},"asks":[]}"#)
        let c = conversation(hub, lib, sync)
        await c.refresh(e.id)
        return c
    }

    /// Only an upload on its way keeps the follow-up waiting, and only within the hub's 12 hours of the last upload it
    /// stored: a refused end doesn't wait for good, it says why instead, until those 12 hours are over.
    @Test func onlyAnUploadOnItsWayKeepsTheFollowUpWaiting() async throws {
        let (pending, lib, sync) = try stoppedMeeting(stored: 1)
        #expect(sync.status(pending) == .pending)
        let c = await readWithoutARoom(pending, lib, sync)
        #expect(c.running(pending), "the upload with ended_at is on its way")
        #expect(c.followupHold(pending) == .finishing)

        let (stale, staleLib, staleSync) = try stoppedMeeting(stored: 13)
        let old = await readWithoutARoom(stale, staleLib, staleSync)
        #expect(!old.running(stale), "the hub counts it as ended 12 hours after its last stored upload")
        #expect(old.followupHold(stale) == nil)

        let (refused, refusedLib, refusedSync) = try stoppedMeeting(stored: 1, refused: true)
        #expect(refusedSync.status(refused) == .rejected)
        let r = await readWithoutARoom(refused, refusedLib, refusedSync)
        #expect(!r.running(refused), "a refused upload isn't on its way: nothing to wait for")
        #expect(r.followupHold(refused) == .endRefused, "the hub has no end yet: the page says why instead of 'in a moment'")

        let (refusedLong, longLib, longSync) = try stoppedMeeting(stored: 13, refused: true)
        #expect(await readWithoutARoom(refusedLong, longLib, longSync).followupHold(refusedLong) == nil,
                "past the 12 hours the hub has ended it")
    }

    /// The hub's word goes first: a room it reports `ended` (or `deleted`) has passed the meeting's end, whatever is still
    /// on its way. A full read without a room has none to wait for; before any read a room meeting waits for it.
    @Test func theHubsRoomSaysWhenTheMeetingEnded() async throws {
        let (pending, lib, sync) = try stoppedMeeting(stored: 1)
        let hub = FakeHub()
        hub.on("GET", path(pending.id, "live"), json: #"{"room":{"state":"ended","machine_id":"server","session_id":"room-1"}}"#)
        let c = conversation(hub, lib, sync)
        #expect(c.running(pending))
        await c.refresh(pending.id)
        #expect(!c.running(pending), "ended: the hub has the meeting's end")
        #expect(c.followupHold(pending) == nil)

        let (stored, storedLib, storedSync) = try stoppedMeeting(stored: 0, synced: true)
        #expect(storedSync.status(stored) == .synced)
        let empty = FakeHub()
        empty.on("GET", path(stored.id, "live"), json: #"{"asks":[]}"#)
        let d = conversation(empty, storedLib, storedSync)
        #expect(d.running(stored), "a room meeting whose room isn't read yet waits for it")
        await d.refresh(stored.id)
        #expect(d.meeting(stored.id).loaded)
        #expect(!d.running(stored), "read whole and no room: nothing to wait for (an older manager, a lost meeting.json)")
    }

    /// An older manager answered the upload without `room`: no follow-up to wait for, and none to write to (it has no
    /// such route); the page says so.
    @Test func anOlderManagerHasNoFollowUp() async throws {
        let root = FileManager.default.temporaryDirectory.appending(path: "meeting-older-\(UUID())")
        let lib = RecordingLibrary(root: root)
        let e = meeting(room(.participant), active: false, lines: [RecordingSentence(start: 1, end: 4, text: "line")])
        try lib.save(e)
        let hub = recordingHub(e) { body in
            let revision = (body["revision"] as? NSNumber)?.int64Value ?? 0
            return #"{"id":"x","revision":\#(revision),"status":"stored","attendance":"off"}"#
        }
        let client = hub.client
        let sync = RecordingSync(library: lib, api: { client })
        await sync.syncNow()
        let stopped = try #require(lib.entry(e.id))
        #expect(sync.roomMissing.contains(e.id))
        let c = conversation(hub, lib, sync)
        #expect(!c.running(stopped))
        #expect(c.followupHold(stopped) == .noFollowup)
    }

    // MARK: The private mark

    /// The start's choice stands in for the mark only until the hub has said anything: once a read (whole, or of
    /// `private`) or a change came back, an absent mark is the hub's, and counts as private.
    @Test func theStartsChoiceStandsInOnlyUntilTheHubSays() async throws {
        let hub = FakeHub()
        let e = meeting(room(.listener, private: false), active: false)
        hub.on("GET", path(e.id, "live")) { req in
            if req.query["parts"] == nil { return Data(#"{"room":{"state":"none"}}"#.utf8) }
            return Data(#"{"private":false,"revs":{"private":2}}"#.utf8)
        }
        let c = conversation(hub)
        #expect(!MeetingPage.isPrivate(e, c.meeting(e.id)), "nothing read yet: the start's choice")
        await c.refresh(e.id)
        #expect(c.meeting(e.id).privateRead && c.meeting(e.id).privateMark == nil)
        #expect(MeetingPage.isPrivate(e, c.meeting(e.id)), "the hub has no mark: private, whatever the start said")
        await c.refresh(e.id, parts: ["private"])
        #expect(!MeetingPage.isPrivate(e, c.meeting(e.id)))
        #expect(hub.sent("GET", path(e.id, "live")).count == 2)

        let gone = FakeHub()
        gone.on("GET", path(e.id, "live")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) }
        let missing = conversation(gone)
        await missing.refresh(e.id)
        #expect(!missing.meeting(e.id).privateRead, "a recording the hub doesn't have said nothing about its mark")
        #expect(!MeetingPage.isPrivate(e, missing.meeting(e.id)))
        missing.uploaded(e.id, room: try decodeRoom(#"{"state":"none"}"#), privateMark: nil)
        #expect(!missing.meeting(e.id).privateRead, "an upload's 200 without private isn't a read of it")
    }

    @Test func thePrivateSwitchStoresWhatTheHubSays() async throws {
        let hub = FakeHub(), id = UUID()
        hub.on("PUT", path(id, "private")) { req in
            let mark = ((try? JSONSerialization.jsonObject(with: req.body ?? Data())) as? [String: Any])?["private"] as? Bool == true
            return Data(#"{"private":\#(mark)}"#.utf8)
        }
        let c = conversation(hub)
        #expect(try await c.setPrivate(id, true))
        #expect(c.meeting(id).privateMark == true)
        #expect(try body(hub.sent("PUT", path(id, "private")).first)["private"] as? Bool == true)
        #expect(try await c.setPrivate(id, false) == false)
        #expect(c.meeting(id).privateMark == false)
        #expect(c.meeting(id).privateRead)

        hub.on("PUT", path(id, "private")) { _ in throw APIError(status: 403, message: "read only", fields: ["code": "kb_read_only"]) }
        await #expect(throws: APIError.self) { try await c.setPrivate(id, true) }
        #expect(c.meeting(id).privateMark == false, "a refusal changes nothing")
    }

    // MARK: Just me

    private func justMeAnswer(_ req: LocalRequest) throws -> Data {
        let id = try body(req)["id"] as? String ?? ""
        return Data(#"{"conversation":{"machine_id":"server","session_id":"main-1"},"client_msg_id":"just-me-\#(id)"}"#.utf8)
    }

    private func sending(_ c: MeetingConversation, _ recording: UUID) -> Bool { c.justMe(for: recording).last?.state == .sending }

    private func sent(_ c: MeetingConversation, _ recording: UUID) -> Bool {
        if case .sent? = c.justMe(for: recording).last?.state { true } else { false }
    }

    /// The owner's private question goes to its own route under its own id (lowercase), the text trimmed, no images when
    /// there are none; the answer names the main chat. It never becomes one of the room's asks.
    @Test func justMeGoesToItsOwnRouteUnderItsID() async throws {
        let hub = FakeHub()
        let e = meeting(room(.listener))
        hub.on("POST", path(e.id, "just-me")) { req in try self.justMeAnswer(req) }
        let c = conversation(hub)
        #expect(c.sendJustMe(e, text: "  \n ") == nil, "nothing to ask")
        let id = try #require(c.sendJustMe(e, text: "  Who owns the export cap?  "))
        #expect(c.justMe(for: e.id).map(\.text) == ["Who owns the export cap?"])
        await until("the answer") { !sending(c, e.id) }
        let sentBody = try body(hub.sent("POST", path(e.id, "just-me")).first)
        #expect(sentBody["id"] as? String == id.uuidString.lowercased())
        #expect(sentBody["text"] as? String == "Who owns the export cap?")
        #expect(sentBody["images"] == nil)
        #expect(c.justMe(for: e.id).last?.state == .sent(conversation: SessionKey(machineID: "server", sessionID: "main-1"),
                                                          clientMsgID: "just-me-\(id.uuidString.lowercased())"))
        #expect(MeetingAsk.shared.exchanges(for: e.id).isEmpty, "Just me is never one of the room's asks")
    }

    /// Words over the contract's 8000 characters never go, and are never cut: the bar keeps Send off and says why. The
    /// limit counts what is left once trimmed, as the hub counts it (Unicode scalars).
    @Test func wordsOverTheLimitAreRefusedNotCut() async throws {
        let hub = FakeHub()
        let e = meeting(room(.listener))
        hub.on("POST", path(e.id, "just-me")) { req in try self.justMeAnswer(req) }
        let c = conversation(hub)
        let most = String(repeating: "字", count: MeetingConversation.maxText)
        #expect(!MeetingConversation.tooLong("  \n" + most + "\n  "))
        #expect(MeetingConversation.tooLong(most + "a"))
        #expect(!MeetingConversation.tooLong(String(repeating: "e\u{301}", count: MeetingConversation.maxText / 2)),
                "two scalars a character: 8000 in all")
        #expect(c.sendJustMe(e, text: most + "a") == nil)
        #expect(c.justMe(for: e.id).isEmpty)
        _ = try #require(c.sendJustMe(e, text: " " + most + " "))
        await until("sent") { sent(c, e.id) }
        #expect(try body(hub.sent("POST", path(e.id, "just-me")).first)["text"] as? String == most, "trimmed, whole")

        let id = UUID()
        #expect(c.followupLimited(id))
        #expect(c.sendFollowup(id, text: most + "a") == nil, "…/followup takes 8000 at most")
        c.applyLive(id, room: nil, privateMark: nil, followup: RecordingUpload.SessionRef(machineID: "server", sessionID: "f1"))
        #expect(!c.followupLimited(id), "the follow-up's own messages have no such limit")
        hub.on("POST", "/api/v1/sessions/server/f1/messages", json: #"{"ok":true}"#)
        #expect(c.sendFollowup(id, text: most + "a") != nil)
    }

    /// Any answer the hub gave ends that id (the hub replays it under the same one): no second try on its own, and Retry
    /// sends the same words under a new id.
    @Test func aJustMeTheHubAnsweredGoesAgainUnderANewID() async throws {
        let hub = FakeHub(), calls = Calls()
        let e = meeting(room(.listener))
        hub.on("POST", path(e.id, "just-me")) { req in
            if calls.next() == 1 { throw APIError(status: 502, message: "bad gateway") }
            return try self.justMeAnswer(req)
        }
        let c = conversation(hub)
        _ = try #require(c.sendJustMe(e, text: "Who owns it?"))
        await until("the refusal") { !sending(c, e.id) }
        let failed = try #require(c.justMe(for: e.id).last)
        guard case .failed = failed.state else { Issue.record("not failed: \(failed.state)"); return }
        #expect(hub.sent("POST", path(e.id, "just-me")).count == 1, "the hub keeps that answer under its id")
        c.retryJustMe(failed.id)
        await until("the retry") { sent(c, e.id) }
        let ids = try hub.sent("POST", path(e.id, "just-me")).map { try body($0)["id"] as? String }
        #expect(ids.count == 2 && ids[0] != ids[1])
        #expect(c.justMe(for: e.id).map(\.text) == ["Who owns it?"], "the same question, not a second one")
    }

    /// No answer at all (the connection dropped): the hub may have it, so the same id goes again.
    @Test func aJustMeWithNoAnswerKeepsItsID() async throws {
        let hub = FakeHub(), calls = Calls()
        let e = meeting(room(.listener))
        hub.on("POST", path(e.id, "just-me")) { req in
            if calls.next() == 1 { throw URLError(.networkConnectionLost) }
            return try self.justMeAnswer(req)
        }
        let c = conversation(hub)
        _ = try #require(c.sendJustMe(e, text: "Who owns it?"))
        await until("the first try") { !sending(c, e.id) }
        if let last = c.justMe(for: e.id).last, case .failed = last.state { c.retryJustMe(last.id) }
        await until("sent") { sent(c, e.id) }
        let ids = try hub.sent("POST", path(e.id, "just-me")).map { try body($0)["id"] as? String }
        #expect(ids.count == 2 && Set(ids).count == 1)
    }

    // MARK: The follow-up

    private func made(_ req: LocalRequest) throws -> Data {
        let id = try body(req)["id"] as? String ?? ""
        return Data(#"{"followup":{"machine_id":"server","session_id":"f1"},"created":true,"client_msg_id":"followup-\#(id)"}"#.utf8)
    }

    /// The first message makes the follow-up (`…/followup`, its id lowercase); the next go to its messages under their own
    /// op id. Each send keeps the route it started on, and the ids its message comes back with.
    @Test func theFirstFollowUpMakesItAndTheRestGoToItsMessages() async throws {
        let hub = FakeHub(), id = UUID()
        hub.on("POST", path(id, "followup")) { req in try self.made(req) }
        hub.on("POST", "/api/v1/sessions/server/f1/messages", json: #"{"ok":true}"#)
        let c = conversation(hub)
        #expect(c.sendFollowup(id, text: "   ") == nil)
        let first = try #require(c.sendFollowup(id, text: "  Draft the email to the vendor. "))
        await until("made") { c.followups(for: id).first?.state == .sent }
        let sentBody = try body(hub.sent("POST", path(id, "followup")).first)
        #expect(sentBody["id"] as? String == first.uuidString.lowercased())
        #expect(sentBody["text"] as? String == "Draft the email to the vendor.")
        #expect(c.meeting(id).followup == RecordingUpload.SessionRef(machineID: "server", sessionID: "f1"))
        #expect(c.followupKey(id) == SessionKey(machineID: "server", sessionID: "f1"))
        let madeSend = try #require(c.followups(for: id).first { $0.id == first })
        #expect(madeSend.route == .followup)
        #expect(MeetingConversation.clientMsgIDs(for: madeSend).map { $0.lowercased() }.contains("followup-\(first.uuidString.lowercased())"))

        let second = try #require(c.sendFollowup(id, text: "Shorter, please."))
        await until("sent to the follow-up") { c.followups(for: id).first { $0.id == second }?.state == .sent }
        #expect(hub.sent("POST", path(id, "followup")).count == 1, "made once")
        let message = try #require(hub.sent("POST", "/api/v1/sessions/server/f1/messages").first)
        #expect((try body(message)["client_msg_id"] as? String)?.lowercased() == second.uuidString.lowercased())
        #expect(message.headers.values.contains(second.uuidString.lowercased()), "its op id")
        let later = try #require(c.followups(for: id).first { $0.id == second })
        #expect(later.route == .messages(SessionKey(machineID: "server", sessionID: "f1")))
        #expect(MeetingConversation.clientMsgIDs(for: later).map { $0.lowercased() }.contains(second.uuidString.lowercased()))

        c.delivered(first)
        c.delivered(second)
        #expect(c.followups(for: id).isEmpty, "the thread has them")
    }

    /// `409 meeting_running` ends that id (the hub keeps the refusal under it): its words show, and Retry sends the same
    /// words under a new id, the same way.
    @Test func aRefusedFollowUpGoesAgainUnderANewID() async throws {
        let hub = FakeHub(), id = UUID(), calls = Calls()
        let running = APIError(status: 409, message: "running", fields: ["code": "meeting_running"])
        hub.on("POST", path(id, "followup")) { req in
            if calls.next() == 1 { throw running }
            return try self.made(req)
        }
        let c = conversation(hub)
        _ = try #require(c.sendFollowup(id, text: "Draft it."))
        await until("the refusal") { c.followups(for: id).first?.state != .sending }
        let failed = try #require(c.followups(for: id).first)
        #expect(failed.state == .failed(MeetingConversation.message(running)))
        c.retryFollowup(failed.id)
        await until("the retry") { c.followups(for: id).first?.state == .sent }
        let ids = try hub.sent("POST", path(id, "followup")).map { try body($0)["id"] as? String }
        #expect(ids.count == 2 && ids[0] != ids[1])
        #expect(c.followups(for: id).count == 1)
        #expect(c.followups(for: id).first?.route == .followup)
    }

    /// A server error the hub reruns under the same id: the same id goes again.
    @Test func aFollowUpTheHubMayHaveKeepsItsID() async throws {
        let hub = FakeHub(), id = UUID(), calls = Calls()
        hub.on("POST", path(id, "followup")) { req in
            if calls.next() == 1 { throw APIError(status: 500, message: "boom") }
            return try self.made(req)
        }
        let c = conversation(hub)
        _ = try #require(c.sendFollowup(id, text: "Draft it."))
        await until("the first try") { c.followups(for: id).first?.state != .sending }
        if let last = c.followups(for: id).first, case .failed = last.state { c.retryFollowup(last.id) }
        await until("sent") { c.followups(for: id).first?.state == .sent }
        let ids = try hub.sent("POST", path(id, "followup")).map { try body($0)["id"] as? String }
        #expect(ids.count == 2 && Set(ids).count == 1)
    }

    // MARK: Asks the room can't take

    /// A fake hub for asks: each question takes the next answer.
    private final class Asked {
        var questions: [MeetingAsk.Question] = []
        var answers: [Result<MeetingAsk.Answer, Error>]
        init(_ answers: [Result<MeetingAsk.Answer, Error>]) { self.answers = answers }
        func next() throws -> MeetingAsk.Answer {
            guard !answers.isEmpty else { throw MeetingAsk.Failure.hub("model_error") }
            return try answers.removeFirst().get()
        }
    }

    private func asker(_ asked: Asked, settled: @escaping (UUID) -> Bool = { _ in true },
                       roomMissing: @escaping (UUID) -> Bool = { _ in false }) -> MeetingAsk {
        MeetingAsk(deps: MeetingAsk.Deps(
            ask: { q in asked.questions.append(q); return try asked.next() },
            now: { 312 },
            heard: { _ in 7 },
            settled: { settled($0) },
            maySpeak: { _ in false },
            speak: { _, _, _, _, _ in },
            stopSpeaking: {},
            clock: { Date(timeIntervalSince1970: 1_800_000_000) },
            roomMissing: roomMissing))
    }

    /// The switch a case turns while its asker runs.
    private final class Flag { var on = false }

    /// While an older manager holds the room meeting with Persona off (`roomMissing`), nothing is asked: no exchange, so
    /// no whole upload forced and no 10 s wait; Retry does nothing either. One that went before says why it failed, not
    /// that the connection did.
    @Test func noAskGoesWhileTheHubHasNoRoom() async throws {
        let e = meeting(room(.participant))
        #expect(MeetingAsk.canAsk(e, roomMissing: false))
        #expect(!MeetingAsk.canAsk(e, roomMissing: true))
        #expect(!MeetingAsk.canAsk(meeting(room(.off)), roomMissing: false))

        let missing = Flag()
        let asked = Asked([.failure(MeetingAsk.Failure.hub("model_error"))])
        let ask = asker(asked, roomMissing: { _ in missing.on })
        _ = try #require(ask.ask(e, text: "Who owns it?"))
        await until("failed") { !ask.busy }
        let failed = try #require(ask.latest(for: e.id))
        missing.on = true
        #expect(ask.ask(e, text: "And the test?") == nil)
        #expect(ask.ask(e) == nil, "nor what was just said")
        ask.retry(failed.id, entry: e)
        #expect(!ask.busy)
        #expect(asked.questions.count == 1)
        #expect(ask.exchanges(for: e.id).count == 1)

        let late = Flag()
        let unsettled = asker(Asked([]), settled: { _ in late.on = true; return false }, roomMissing: { _ in late.on })
        _ = try #require(unsettled.ask(e, text: "Who owns it?"))
        await until("failed") { !unsettled.busy }
        #expect(unsettled.latest(for: e.id)?.state == .failed(MeetingPage.roomProblem(nil, missing: true) ?? ""),
                "the page's words for it, not the connection's")
    }

    private var unavailable: MeetingAsk.Failure { .hub("room_unavailable") }

    /// The answer the hub gives from the meeting so far (`via: material`).
    private var fromMaterial: MeetingAsk.Answer {
        var answer = MeetingAsk.Answer(question: nil, text: "From the meeting so far: Bo owns it.", lang: "en", speak: false, sources: [])
        answer.via = "material"
        return answer
    }

    /// The hub fails a room ask asked again while the room is unavailable at once (`room_unavailable`): the phone asks it
    /// once more under a new id, at the same moment, which the hub answers from the meeting so far.
    @Test func anAskTheRoomCantTakeGoesAgainUnderANewID() async throws {
        let asked = Asked([.failure(unavailable), .success(fromMaterial)])
        let ask = asker(asked)
        let e = meeting(room(.listener))
        let first = try #require(ask.ask(e, text: "Who owns the test?"))
        await until("answered") { !ask.busy }
        try #require(asked.questions.count == 2)
        #expect(asked.questions[0].id == first)
        #expect(asked.questions[1].id != first, "the hub fails that id at once")
        #expect(asked.questions[1].at == asked.questions[0].at)
        #expect(asked.questions[1].text == "Who owns the test?")
        let done = try #require(ask.latest(for: e.id))
        #expect(done.id == asked.questions[1].id)
        #expect(done.answer == "From the meeting so far: Bo owns it.")
        #expect(done.via == "material")
        #expect(ask.exchanges(for: e.id).count == 1)
    }

    /// Still unavailable the second time: it says so (with its code kept), and Retry asks under a new id again.
    @Test func aRoomThatStaysUnavailableSaysSo() async throws {
        let asked = Asked([.failure(unavailable), .failure(unavailable), .success(fromMaterial)])
        let ask = asker(asked)
        let e = meeting(room(.listener))
        _ = try #require(ask.ask(e, text: "Who owns the test?"))
        await until("failed") { !ask.busy }
        #expect(asked.questions.count == 2, "once more on its own, not again and again")
        let failed = try #require(ask.latest(for: e.id))
        #expect(failed.state == .failed(MeetingAsk.message(unavailable)))
        #expect(failed.failure == "room_unavailable")
        ask.retry(failed.id, entry: e)
        await until("answered") { !ask.busy }
        #expect(asked.questions.count == 3)
        #expect(Set(asked.questions.map(\.id)).count == 3)
        #expect(ask.latest(for: e.id)?.answer == "From the meeting so far: Bo owns it.")
    }

    /// An ask's wire says how it was answered: `via`, the lines heard, how it was asked and when.
    @Test func anAskSaysHowItWasAnswered() throws {
        let ask = try JSON.decoder.decode(MeetingAskWire.Ask.self, from: Data("""
            {"id":"AB","question":"","question_source":"heard","at":741.5,"state":"answered","answer":"A","via":"room",
             "heard":[{"start":741,"end":748,"text":"Who owns the long test?"}],"asked_at":"2026-11-10T06:30:00Z"}
            """.utf8))
        #expect(ask.via == "room")
        #expect(ask.questionSource == "heard")
        #expect(ask.at == 741.5)
        #expect(ask.heard?.map(\.text) == ["Who owns the long test?"])
        let info = MeetingThread.AskInfo(id: ask.id?.lowercased() ?? "", spoken: ask.questionSource == "spoken", at: ask.at)
        #expect(info == MeetingThread.AskInfo(id: "ab", spoken: false, at: 741.5))
    }

    // MARK: Words

    /// Each new reason reads as its own sentence; the refusals of Just me, the follow-up and the private switch too.
    @Test func everyNewReasonHasItsWords() {
        let generic = MeetingAsk.message(nil)
        let asks = ["meeting_ended", "room_unavailable", "no_answer"].map { MeetingAsk.message(MeetingAsk.Failure.hub($0)) }
        #expect(!asks.contains(generic))
        #expect(Set(asks).count == asks.count)

        func refused(_ status: Int, _ code: String? = nil) -> APIError {
            APIError(status: status, message: "x", fields: code.map { ["code": $0] } ?? [:])
        }
        let fallback = MeetingConversation.message(refused(400))
        let said = [refused(409, "meeting_running"), refused(409, "no_material"), refused(409, "meeting_ended"), refused(409, "not_meeting"),
                    refused(403, "kb_read_only"), refused(413, "too_large"), refused(503)].map { MeetingConversation.message($0) }
            + [MeetingConversation.message(URLError(.notConnectedToInternet))]
        for (i, text) in said.enumerated() { #expect(text != fallback, "\(i)") }
        #expect(Set(said).count == said.count, "each its own")
        #expect(MeetingConversation.message(URLError(.timedOut)) == MeetingConversation.message(URLError(.notConnectedToInternet)))
        #expect(MeetingConversation.message(refused(409, "op_id_reused")) == fallback, "Retry sends it again")

        // The private switch: nothing was sent and there is no Retry, so its own fallback; the reasons the hub names keep
        // their words.
        let privateFallback = MeetingConversation.privateMessage(refused(500))
        #expect(privateFallback != fallback)
        #expect(MeetingConversation.privateMessage(refused(404, "recording_not_found")) == privateFallback)
        #expect(MeetingConversation.privateMessage(refused(409, "op_id_reused")) == privateFallback)
        for error: Error in [refused(403, "kb_read_only"), refused(409, "not_meeting"), refused(503), URLError(.notConnectedToInternet)] {
            #expect(MeetingConversation.privateMessage(error) == MeetingConversation.message(error))
        }
    }

    // MARK: The event

    /// `meeting_live` carries no content: the recording, the parts that changed and their numbers.
    @Test func theMeetingLiveEventIsDecoded() throws {
        let data = Data(#"{"type":"meeting_live","recording_id":"00000000-0000-0000-0000-000000000002","changed":["room","asks"],"revs":{"room":3,"asks":12}}"#.utf8)
        guard case .meetingLive(let id, let changed, let revs) = try #require(ServerEvent.decode(data)) else {
            Issue.record("not meeting_live"); return
        }
        #expect(id == "00000000-0000-0000-0000-000000000002")
        #expect(changed == ["room", "asks"])
        #expect(revs == ["room": 3, "asks": 12])
        #expect(ServerEvent.decode(Data(#"{"type":"meeting_live","changed":["room"],"revs":{"room":1}}"#.utf8)) == nil,
                "no recording: nothing to read")
    }
}
