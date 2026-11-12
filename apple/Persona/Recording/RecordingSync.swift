import Foundation
import Observation
#if canImport(UIKit)
import UIKit
#endif

/// The hub's side of a recording (docs/protocol/protocol.md「Recording sync」, #3235): what `PUT /api/v1/recordings/{id}` takes.
/// Text only: the audio never leaves the device.
nonisolated struct RecordingUpload: Encodable, Sendable {
    struct Bookmark: Encodable, Sendable { var at: Double; var label = "" }
    struct Segment: Encodable, Sendable { var start: Double; var end: Double; var text: String }
    /// The calendar event the device matched (docs/assistant/meeting-minutes.md「Field contract」): a hint for the hub, which
    /// prefers its own `meetings` / `events`. No event id: the hub can't see this calendar.
    struct CalendarHint: Encodable, Sendable {
        var title: String?
        var attendees: [String]?
        var location: String?
    }
    var revision: Int64
    var startedAt: Date
    var endedAt: Date?
    var duration: Double
    var language: String
    var device: String
    var title: String
    var bookmarks: [Bookmark]
    var segments: [Segment]
    var complete: Bool
    /// "meeting" (the hub writes it minutes) or "note".
    var kind: String
    /// Only when the device matched a calendar event.
    var calendar: CalendarHint?
    /// A session the API names (`{machine_id, session_id}`).
    struct SessionRef: Codable, Equatable, Sendable {
        var machineID: String
        var sessionID: String
        enum CodingKeys: String, CodingKey { case machineID = "machine_id", sessionID = "session_id" }
    }
    /// Persona's part in the meeting (docs/assistant/meeting-minutes.md「Attendance fields」): only on a phone meeting
    /// recording Persona was asked to attend, so every other upload is exactly as before. `off` once turned off.
    var attendance: String?
    /// The session that attends; left out for the main chat.
    var attendingSession: SessionRef?
    /// A room meeting's role (#3862, docs/assistant/meeting-minutes.md「Upload fields」): the meeting's own room attends.
    struct RoomUpload: Encodable, Sendable { var attendance: String }
    /// On every upload of a room meeting, whatever the role (`off` included), in place of `attendance` and
    /// `attending_session`: an older manager after a rollback reads only those, so it sees a meeting that is off.
    var room: RoomUpload?
    /// A room meeting's private mark as it started (the hub takes it only while it has none).
    var privateMark: Bool?

    enum CodingKeys: String, CodingKey {
        case revision, duration, language, device, title, bookmarks, segments, complete, kind, calendar, attendance, room
        case startedAt = "started_at", endedAt = "ended_at", attendingSession = "attending_session", privateMark = "private"
    }

    init(_ entry: RecordingEntry) {
        revision = entry.revision
        startedAt = entry.createdAt
        endedAt = entry.active ? nil : entry.createdAt.addingTimeInterval(entry.duration)
        duration = entry.duration
        language = entry.locale
        device = entry.source.rawValue
        title = entry.title
        bookmarks = entry.marks.map { Bookmark(at: $0) }
        segments = entry.sentences.compactMap { s in
            let text = s.text.trimmingCharacters(in: .whitespacesAndNewlines)
            return text.isEmpty ? nil : Segment(start: s.start, end: s.end, text: text)
        }
        complete = entry.transcribedAll
        kind = entry.isMeeting ? "meeting" : "note"
        if let meeting = entry.meeting, meeting.eventID != nil || meeting.title != nil {
            calendar = CalendarHint(title: meeting.title, attendees: meeting.attendees.isEmpty ? nil : meeting.attendees,
                                    location: meeting.location)
        }
        if let a = Self.attendance(entry) {
            if a.room {
                room = RoomUpload(attendance: a.role.rawValue)
                privateMark = a.privateMark ?? false
            } else {
                // Never an attending role without an explicit target (#3862, the owner's rule after a real meeting): the hub
                // reads `attending_session` left out as the main chat, so with no session named and the main chat not
                // picked for this meeting the role goes as off (`sentRole`), and the meeting's words never fill the main
                // chat by default.
                let role = a.sentRole
                attendance = role.rawValue
                if role.attends, let key = a.key { attendingSession = SessionRef(machineID: key.machineID, sessionID: key.sessionID) }
            }
        }
    }

    /// The attendance an upload of `entry` carries: a phone meeting's, once Persona was asked to attend it.
    static func attendance(_ entry: RecordingEntry) -> RecordingAttendance? {
        guard entry.isMeeting, entry.source == .phone else { return nil }
        return entry.attendance
    }

    /// What the hub must have stored before live lines and questions go (`role|session`; `room|role` for a room meeting).
    /// The role as sent (`sentRole`): a meeting sent as off settles as off, so nothing waits on a role the hub never got.
    static func attendanceMark(_ entry: RecordingEntry) -> String? {
        attendance(entry).map { a in
            let role = a.sentRole
            return a.room ? "room|\(role.rawValue)" : "\(role.rawValue)|\(role.attends ? a.session ?? "" : "")"
        }
    }
}

/// Keeps the knowledge base's copy of each recording's transcript in step with this device: uploads every new revision
/// (partial transcripts too), deletes what is deleted here, retries with backoff while the hub is away, and drops
/// recordings (audio included) once they are older than the hub's retention (`days`, 14 by default).
/// What was sent is kept in `sync.json` beside the recordings, so a relaunch only sends what changed.
@Observable
final class RecordingSync {
    static let shared = RecordingSync()

    nonisolated struct Store: Codable, Equatable, Sendable {
        /// The revision the hub has (stored, or newer than ours: stale).
        var synced: [UUID: Int64] = [:]
        /// Revisions the hub refused (400): not sent again until the recording changes.
        var rejected: [UUID: Int64] = [:]
        /// Deleted here, the hub not told yet.
        var deletes: Set<UUID> = []
        /// The hub's retention in days (`GET /api/v1/recordings`).
        var days = RecordingSync.defaultDays
        /// `days` came from the hub at least once: until then nothing here is dropped for its age (the hub may keep
        /// recordings longer than the default).
        var daysKnown = false
    }

    enum Status: Equatable {
        /// Nothing to upload yet (no text).
        case waiting
        case pending
        case uploading
        case synced
        case rejected
        case expired
    }

    nonisolated static let defaultDays = 14

    private(set) var store: Store
    private(set) var uploading: UUID?
    /// The attendance each recording's last stored upload carried, as the hub said it took it (`attendanceMark`): live
    /// lines and questions wait for it (docs/assistant/meeting-minutes.md「Live sentences」).
    private(set) var attendanceStored: [UUID: String] = [:]
    /// Why the hub stored a recording's attendance as off (`attendance_problem`), when it said.
    private(set) var attendanceProblem: [UUID: String] = [:]
    /// Room meetings (#3862) whose last upload `200` had no `room`: an older manager after a rollback, which stored the
    /// meeting with Persona off. Not for good: the uploads keep carrying `room`, and no live line or question goes until
    /// a `200` has it again.
    private(set) var roomMissing: Set<UUID> = []
    /// Recordings the hub has stored an upload with `ended_at` of in this run: until then it answers the follow-up
    /// `409 meeting_running`.
    private(set) var endedStored: Set<UUID> = []
    /// Called after an upload of an attending recording was stored: its live lines can go.
    @ObservationIgnored var onAttendanceStored: ((UUID) -> Void)?
    /// The `room` (and `private`, when there) of every upload `200` of a room meeting (`MeetingConversation.start`).
    @ObservationIgnored var onRoom: ((UUID, MeetingRoom, Bool?) -> Void)?
    /// Every save of a recording, after this sync's own reaction to it (the live log of a meeting Persona attends).
    @ObservationIgnored var onChange: ((UUID) -> Void)?
    /// Uploaded first, without the save debounce: a recording whose attendance the hub doesn't have yet.
    @ObservationIgnored private var urgent: Set<UUID> = []
    /// Configuration refusals need a foreground/manual retry, not a timer or another transcript save.
    private(set) var automaticRetryPaused = false
    private var lastProblem: String?
    /// Pending deletes are durable and visible even after a relaunch; a local delete is not a remote acknowledgement.
    var problem: String? {
        guard !store.deletes.isEmpty else { return lastProblem }
        return [lastProblem, String(localized: "Pending deletions have not been removed from Knowledge yet.")].compactMap { $0 }.joined(separator: " ")
    }

    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private let api: () -> APIClient?
    @ObservationIgnored private let file: URL
    @ObservationIgnored private let now: () -> Date
    @ObservationIgnored private var worker: Task<Void, Never>?
    @ObservationIgnored private var again = false
    /// `worker` is only waiting out a backoff: a new reason to sync cuts it short.
    @ObservationIgnored private var backingOff = false
    /// `worker` is only waiting out the save debounce.
    @ObservationIgnored private var debouncing = false
    @ObservationIgnored private var failures = 0
    @ObservationIgnored private var lastReconcile: Date?
    @ObservationIgnored private var started = false

    init(library: RecordingLibrary = .shared, api: @escaping () -> APIClient? = { AppModel.shared.api },
         now: @escaping () -> Date = { .now }) {
        self.library = library; self.api = api; self.now = now
        file = library.root.appendingPathComponent("sync.json")
        store = (try? Data(contentsOf: file)).flatMap { try? JSONDecoder().decode(Store.self, from: $0) } ?? Store()
    }

    /// From `PhoneRecordingReceiver.start`: every save is a reason to sync, and so is coming back to the foreground.
    func start() {
        guard !started else { return }
        started = true
        library.didChange = { [weak self] id in
            self?.kick()
            self?.onChange?(id)
        }
        #if canImport(UIKit) && !os(watchOS)
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in RecordingSync.shared.kick(reconcile: true) }
        }
        #endif
        purgeExpired()
        kick(reconcile: true)
    }

    func status(_ entry: RecordingEntry) -> Status {
        if isExpired(entry) { return .expired }
        if uploading == entry.id { return .uploading }
        if let have = store.synced[entry.id], have >= entry.revision { return .synced }
        if store.rejected[entry.id] == entry.revision { return .rejected }
        return Self.uploadable(entry) ? .pending : .waiting
    }

    /// Text worth sending: at least one sentence (an empty transcript is noise in the knowledge base). A meeting Persona
    /// attends goes before it has any, so the hub knows the role (the contract's first exception).
    nonisolated static func uploadable(_ entry: RecordingEntry) -> Bool {
        if RecordingUpload.attendance(entry) != nil { return true }
        return entry.sentences.contains { !$0.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    }

    /// The revision of `id` the hub last stored (ms, the entry's `revision` when it went), or nil when none was. The meeting
    /// page counts the hub's 12 hours from it: a meeting whose `ended_at` never landed ends then (#3862).
    func storedRevision(_ id: UUID) -> Int64? { store.synced[id] }

    /// The hub has stored the attendance `entry` carries now.
    func attendanceSettled(_ entry: RecordingEntry) -> Bool {
        guard let mark = RecordingUpload.attendanceMark(entry) else { return false }
        return attendanceStored[entry.id] == mark
    }

    /// The attendance of `id` changed (started, turned on or off, another session): it goes now, ahead of the others.
    func attendanceChanged(_ id: UUID) {
        urgent.insert(id)
        kick(now: true)
    }

    /// The hub doesn't have this recording with its attendance (`recording_not_found`, `not_attending`): it goes again now,
    /// even if this phone thinks it was stored.
    func reupload(_ id: UUID) {
        store.synced[id] = nil
        attendanceStored[id] = nil
        attendanceChanged(id)
    }

    func isExpired(_ entry: RecordingEntry) -> Bool {
        store.daysKnown && !entry.active && entry.createdAt < now().addingTimeInterval(-Double(store.days) * 86_400)
    }

    /// Deleted by the user: gone here at once (audio and text), and from the knowledge base when the hub is reachable.
    func delete(_ id: UUID) throws {
        try library.remove(id)
        store.deletes.insert(id)
        forget(id)
        save()
        kick()
    }

    /// Runs a pass soon (several saves in a row become one upload). `reconcile`: also ask the hub what it has, at most
    /// every ten minutes.
    func kick(reconcile: Bool = false, now immediately: Bool = false) {
        if reconcile, automaticRetryPaused { automaticRetryPaused = false; lastReconcile = nil }
        guard !automaticRetryPaused else { return }
        if reconcile, lastReconcile.map({ now().timeIntervalSince($0) > 600 }) ?? true { lastReconcile = nil }
        if backingOff { worker?.cancel(); worker = nil; backingOff = false }
        // Waiting out the save debounce: now is now (a pass already under way takes the urgent ones next).
        if immediately, debouncing { worker?.cancel(); worker = nil; debouncing = false }
        guard worker == nil else { again = true; return }
        debouncing = !immediately
        worker = Task { [weak self] in
            if !immediately { try? await Task.sleep(for: .seconds(1)) }
            guard !Task.isCancelled else { return }
            self?.debouncing = false
            await self?.runPasses()
        }
    }

    /// One pass now, for tests (no debounce, no retry timer).
    func syncNow(retryPaused: Bool = false) async {
        if retryPaused { automaticRetryPaused = false; lastReconcile = nil }
        await pass()
    }

    private func runPasses() async {
        repeat {
            again = false
            await pass()
        } while again && !automaticRetryPaused && !Task.isCancelled
        worker = nil
        guard needsWork, !automaticRetryPaused, api()?.config.isDemo == false else { failures = 0; return }
        // The hub was away or refused for now: try again later, longer each time (5 s … 10 min).
        failures += 1
        let delay = min(600, 5 * pow(2, Double(min(failures, 8) - 1)))
        backingOff = true
        worker = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled, let self else { return }
            self.backingOff = false
            self.worker = nil
            self.kick()
        }
    }

    private var needsWork: Bool {
        !store.deletes.isEmpty || library.entries.contains { status($0) == .pending }
    }

    private func pass() async {
        guard !automaticRetryPaused else { return }
        purgeExpired()
        guard let api = api(), !api.config.isDemo else {
            lastProblem = api == nil ? nil : String(localized: "Demo mode doesn't sync recordings")
            return
        }
        if lastReconcile == nil {
            guard await reconcile(api) else { return }
            lastReconcile = now()
        }
        // A meeting whose attendance the hub doesn't have yet goes first, also between the others: its live lines and
        // questions wait for it.
        guard await uploadUrgent(api) else { return }
        for id in store.deletes.sorted(by: { $0.uuidString < $1.uuidString }) {
            guard await sendDelete(id, api) else { return }
        }
        for entry in library.entries where status(entry) == .pending {
            guard await uploadUrgent(api) else { return }
            guard let latest = library.entry(entry.id), status(latest) == .pending else { continue }
            guard await upload(latest, api) else { return }
        }
        lastProblem = nil
    }

    /// `GET /api/v1/recordings`: the hub's retention, what it already has, and what was deleted elsewhere.
    private func reconcile(_ api: APIClient) async -> Bool {
        struct Have: Decodable { var id: String; var revision: Int64 }
        struct Answer: Decodable { var days: Int?; var recordings: [Have]?; var deleted: [String]? }
        do {
            let answer: Answer = try await api.get("/api/v1/recordings")
            if let days = answer.days, (1...365).contains(days) { store.days = days; store.daysKnown = true }
            let have = Dictionary((answer.recordings ?? []).compactMap { r in UUID(uuidString: r.id).map { ($0, r.revision) } },
                                  uniquingKeysWith: max)
            // The hub's word replaces ours: one it lost (a new hub) goes again, one it has newer is not resent.
            store.synced = have.filter { library.entry($0.key) != nil }
            for id in (answer.deleted ?? []).compactMap(UUID.init(uuidString:)) where library.entry(id) != nil {
                deletedOnHub(id)
            }
            save()
            purgeExpired()
            return true
        } catch {
            note(error)
            return false
        }
    }

    private func uploadUrgent(_ api: APIClient) async -> Bool {
        for id in urgent.sorted(by: { $0.uuidString < $1.uuidString }) {
            urgent.remove(id)
            guard let entry = library.entry(id), status(entry) == .pending else { continue }
            guard await upload(entry, api) else { urgent.insert(id); return false }
        }
        return true
    }

    private func upload(_ entry: RecordingEntry, _ api: APIClient) async -> Bool {
        struct Answer: Decodable {
            var revision: Int64?; var status: String?; var attendance: String?; var attendanceProblem: String?
            enum CodingKeys: String, CodingKey { case revision, status, attendance, attendanceProblem = "attendance_problem" }
        }
        /// A room meeting's part of the answer (#3862), read on its own so the rest reads exactly as before; a field of
        /// another shape is left out.
        struct RoomAnswer: Decodable {
            var room: MeetingRoom?; var privateMark: Bool?
            enum CodingKeys: String, CodingKey { case room, privateMark = "private" }
            init(from decoder: Decoder) throws {
                let c = try decoder.container(keyedBy: CodingKeys.self)
                room = try? c.decodeIfPresent(MeetingRoom.self, forKey: .room)
                privateMark = try? c.decodeIfPresent(Bool.self, forKey: .privateMark)
            }
        }
        uploading = entry.id
        defer { uploading = nil }
        var body = RecordingUpload(entry)
        #if os(iOS)
        // Persona is saying an answer in this meeting right now: what the microphone hears of it isn't the meeting's.
        if let open = MeetingSpeaker.shared.openSpan(for: entry.id) {
            body.segments.removeAll { open.holds(RecordingSentence(start: $0.start, end: $0.end, text: $0.text)) }
        }
        #endif
        do {
            let data = try await api.request("PUT", "/api/v1/recordings/\(entry.id.uuidString.lowercased())", body: body, timeout: 60)
            let answer = try? JSON.decoder.decode(Answer.self, from: data)
            // Deleted while it was on its way: the delete that follows removes it there too.
            guard library.entry(entry.id) != nil else { return true }
            switch answer?.status {
            case "expired":
                if let latest = library.entry(entry.id), isExpired(latest) { purge(latest.id) }
                store.synced[entry.id] = body.revision // the hub will not keep it; nothing more to send
            default: // stored, stale (it has a newer one)
                store.synced[entry.id] = max(body.revision, answer?.revision ?? 0)
            }
            store.rejected[entry.id] = nil
            save()
            if body.endedAt != nil, !endedStored.contains(entry.id) { endedStored.insert(entry.id) }
            if body.room != nil {
                let room = try? JSON.decoder.decode(RoomAnswer.self, from: data)
                roomAnswered(entry, sent: body, answer?.attendance, problem: answer?.attendanceProblem, room: room?.room,
                             privateMark: room?.privateMark)
            } else if body.attendance != nil {
                attendanceAnswered(entry, sent: body, answer?.attendance, problem: answer?.attendanceProblem)
            }
            return true
        } catch let error as APIError where error.status == 410 {
            deletedOnHub(entry.id)
            save()
            return true
        } catch let error as APIError where error.status == 400 || error.status == 413 {
            store.rejected[entry.id] = body.revision
            save()
            lastProblem = String(localized: "Knowledge didn't accept a recording: \(error.message)")
            return true
        } catch {
            note(error)
            return false
        }
    }

    /// What the hub said about the attendance an upload carried. A `200` without `attendance` is an older manager: it took
    /// the transcript and nothing else, so the phone stops attending this meeting (the contract's rule).
    private func attendanceAnswered(_ entry: RecordingEntry, sent body: RecordingUpload, _ took: String?, problem: String?) {
        guard let took else {
            if body.attendance != MeetingAttendance.off.rawValue {
                library.change(entry.id) { e in
                    guard var a = e.attendance else { return }
                    a.set(.off, at: e.duration)
                    a.stopped = .unsupported
                    e.attendance = a
                }
            }
            attendanceStored[entry.id] = nil
            return
        }
        attendanceProblem[entry.id] = problem
        guard let mark = RecordingUpload.attendanceMark(entry) else { return }
        // Stored as asked, or as off (a problem): either way what the hub holds now.
        attendanceStored[entry.id] = took == body.attendance ? mark : "\(took)|"
        if took == body.attendance, took != MeetingAttendance.off.rawValue { onAttendanceStored?(entry.id) }
    }

    /// What the hub said about a room meeting's upload (#3862). A `200` without `room` is an older manager after a
    /// rollback: unlike #2981's rule nothing stops for good; the meeting just waits (no live lines, no questions) until a
    /// `200` carries `room` again.
    private func roomAnswered(_ entry: RecordingEntry, sent body: RecordingUpload, _ took: String?, problem: String?,
                              room: MeetingRoom?, privateMark: Bool?) {
        guard let room else {
            if !roomMissing.contains(entry.id) { roomMissing.insert(entry.id) }
            attendanceStored[entry.id] = nil
            return
        }
        if roomMissing.contains(entry.id) { roomMissing.remove(entry.id) }
        attendanceProblem[entry.id] = problem
        onRoom?(entry.id, room, privateMark)
        guard let mark = RecordingUpload.attendanceMark(entry), let sent = body.room?.attendance else { return }
        // Stored as asked, or as another role (a problem): either way what the hub holds now.
        attendanceStored[entry.id] = took == sent ? mark : "room|\(took ?? "")"
        if took == sent, sent != MeetingAttendance.off.rawValue { onAttendanceStored?(entry.id) }
    }

    private func sendDelete(_ id: UUID, _ api: APIClient) async -> Bool {
        do {
            _ = try await api.request("DELETE", "/api/v1/recordings/\(id.uuidString.lowercased())")
        } catch let error as APIError where error.status == 410 {
            // The hub confirms it was already deleted. An unsupported 404 does not confirm a remote deletion.
        } catch {
            note(error)
            return false
        }
        store.deletes.remove(id)
        save()
        return true
    }

    /// Deleted in the knowledge base (the web, another device): the text there is the truth, so it goes here too.
    private func deletedOnHub(_ id: UUID) {
        RecordingTranscription.shared.cancel(id)
        try? library.remove(id)
        forget(id)
    }

    /// Past the retention: the hub drops it, and the audio here goes with it.
    func purgeExpired() {
        let old = library.entries.filter(isExpired)
        guard !old.isEmpty else { return }
        for entry in old { purge(entry.id) }
        save()
    }

    private func purge(_ id: UUID) {
        RecordingTranscription.shared.cancel(id)
        try? library.remove(id)
        forget(id)
    }

    private func forget(_ id: UUID) {
        store.synced[id] = nil
        store.rejected[id] = nil
        attendanceStored[id] = nil
        attendanceProblem[id] = nil
        if roomMissing.contains(id) { roomMissing.remove(id) }
        if endedStored.contains(id) { endedStored.remove(id) }
    }

    private func note(_ error: Error) {
        if error.isCancellation { return }
        if let e = error as? APIError {
            automaticRetryPaused = e.code == "kb_disabled" || e.code == "kb_read_only"
                || (400..<500).contains(e.status) && e.status != 408 && e.status != 429
            if e.businessMessage != nil {
                lastProblem = e.localizedDescription
                // Use the shared classification, including a request id, without raw internal diagnostics.
            } else if e.status == 404 {
                lastProblem = String(localized: "Recording sync isn't supported by this hub. Update the hub, then reopen the app to retry.")
            } else if e.status == 401 || e.status == 403 {
                lastProblem = String(localized: "Recording sync needs access. Check this device's permissions and pairing, then reopen the app to retry.")
            } else {
                lastProblem = String(localized: "Recording sync couldn't finish. Try again after checking the hub's diagnostics.")
            }
        } else if error.isTimeout {
            lastProblem = String(localized: "The request timed out. Try again later.")
        } else if error.isOffline {
            lastProblem = String(localized: "Can't reach the hub. Recording text will sync automatically later.")
        } else {
            lastProblem = String(localized: "Recording sync couldn't finish. Try again after checking the hub's diagnostics.")
        }
    }

    private func save() {
        do { try JSONEncoder().encode(store).write(to: file, options: .atomic) } catch { lastProblem = String(localized: "Sync record wasn't saved") }
    }
}
