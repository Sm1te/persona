#if os(iOS)
import Foundation
import Observation

/// The meeting conversation (#3862 phase 1) of the meetings this phone records: the room's state, the private mark and the
/// follow-up as the hub reports them (`GET …/live`, the upload's `200`, `meeting_live`), Just me questions, follow-up
/// messages and the private switch (docs/assistant/meeting-minutes.md「Phase 1: the meeting conversation」). The room's and
/// the follow-up's items come through the app's chat machinery (`AppModel.subscribe`), keyed by `roomKey` / `followupKey`.
@MainActor @Observable
final class MeetingConversation {
    static let shared = MeetingConversation(deps: .live)

    /// What the hub says about one meeting's conversation.
    nonisolated struct Meeting: Equatable, Sendable {
        var room: MeetingRoom?
        /// Absent until chosen (counts as private for material).
        var privateMark: Bool?
        /// The hub said what the mark is (a read of `private`, a change, an answer carrying it): from then on an absent mark
        /// is the hub's word (private), and the start's choice no longer stands in for it.
        var privateRead = false
        var followup: RecordingUpload.SessionRef?
        /// `asks` of `GET …/live`, oldest first (the thread uses them to tell spoken/typed and to show asks answered from material).
        var asks: [MeetingAskWire.Ask] = []
        /// The revision of each part read so far (`meeting_live` reads only newer ones); `photos`: the newest one a
        /// `GET …/photos` that began after it was known has applied (`MeetingPhotos`), so a failed read leaves it behind
        /// and the next event or `…/live` read tries again.
        var revs: [String: Int64] = [:]
        /// A full read came back at least once.
        var loaded = false
        /// `404` / `410`: the hub has no such recording (yet, or any more).
        var missing = false
    }

    /// One Just me question (`POST …/just-me`), on this phone only: never in `MeetingAsk`, the Live Activity, the voice
    /// or the room.
    nonisolated struct JustMe: Identifiable, Equatable, Sendable {
        nonisolated enum State: Equatable, Sendable { case sending, sent(conversation: SessionKey, clientMsgID: String), failed(String) }
        /// The request's `id` (a retry sends the same one while the hub may still answer it, else a new one).
        var id: UUID
        var recordingID: UUID
        var text: String
        var images: [ImagePayload] = []
        var at: Date
        var state: State
    }

    /// One follow-up message on its way (`…/followup`, then the session's messages).
    nonisolated struct FollowupSend: Identifiable, Equatable, Sendable {
        /// `sent` stays until the follow-up's transcript holds it (`delivered`).
        nonisolated enum State: Equatable, Sendable { case sending, sent, failed(String) }
        /// Where it goes, fixed at its first attempt: a retry never moves a message the hub may already have taken
        /// through `…/followup` (as `followup-<id>`) to the session's messages (as `<id>`), so it never lands twice.
        nonisolated enum Route: Equatable, Sendable { case followup, messages(SessionKey) }
        var id: UUID
        var recordingID: UUID
        var text: String
        var images: [ImagePayload] = []
        var state: State
        var route: Route
    }

    struct Deps {
        var api: () -> APIClient?
        /// The hub group the meeting's conversations are keyed in.
        var group: () -> String
        var now: () -> Date
        var wait: (Duration) async -> Void
        /// The hub has no upload of the recording (`404 recording_not_found`): it goes again now; whether the hub stored
        /// it within a few seconds (false: the request isn't made again).
        var reupload: (UUID) async -> Bool = { _ in false }
        /// `GET …/photos` of a meeting that takes photos (`MeetingPhotos.refresh`); whether its list was applied.
        var readPhotos: (UUID) async -> Bool = { await MeetingPhotos.shared.refresh($0) }
    }

    /// Longest Just me or `…/followup` text (the contract's limit, in characters).
    nonisolated static let maxText = 8000
    /// The parts of `GET …/live` phase 1 reads; a later phase's (`lines`, `topics`, …) are left to it. `photos` (phase 3)
    /// is read only for a meeting that takes photos (`partsRead(for:)`), and never as a part: `GET …/photos` holds them.
    static let readParts = ["room", "asks", "private", "followup"]
    /// How long after its end a room meeting recorded here still follows `meeting_live` unwatched (the hub's 12 hours).
    static let followsEnded: TimeInterval = 12 * 3600

    private(set) var meetings: [UUID: Meeting] = [:]
    private(set) var justMe: [JustMe] = []
    private(set) var followups: [FollowupSend] = []
    /// Recordings whose page is on screen, by owner (the live page, the page after Stop and the recording detail come and
    /// go in their own order): `meeting_live` and reconnects read them again while any owner is left.
    private(set) var watched: [UUID: Set<UUID>] = [:]
    @ObservationIgnored var deps: Deps
    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private let sync: RecordingSync
    /// Recordings with a read in flight, and what to read once it is back.
    @ObservationIgnored private var reading: Set<UUID> = []
    @ObservationIgnored private var readAgain: [UUID: Wanted] = [:]
    /// Request ids the hub answered for good (a replay would return the same failure): a retry goes under a new one.
    @ObservationIgnored private var spent: Set<UUID> = []
    @ObservationIgnored private var started = false

    private enum Wanted: Equatable {
        case all
        case parts([String])
    }

    init(deps: Deps, library: RecordingLibrary = .shared, sync: RecordingSync = .shared) {
        self.deps = deps
        self.library = library
        self.sync = sync
    }

    /// From `MeetingRecorder.start`: hears the room of every upload `200` (`RecordingSync.onRoom`).
    func start() {
        guard !started else { return }
        started = true
        sync.onRoom = { [weak self] id, room, privateMark in self?.uploaded(id, room: room, privateMark: privateMark) }
    }

    func meeting(_ id: UUID) -> Meeting { meetings[id] ?? Meeting() }

    /// The room's conversation, once it has ids (any state but `none` / `deleted`).
    func roomKey(_ id: UUID) -> SessionKey? { meetings[id]?.room?.key(group: deps.group()) }

    func followupKey(_ id: UUID) -> SessionKey? {
        guard let ref = meetings[id]?.followup, !ref.machineID.isEmpty, !ref.sessionID.isEmpty else { return nil }
        return SessionKey(group: deps.group(), machineID: ref.machineID, sessionID: ref.sessionID)
    }

    // MARK: What the hub reports

    /// The hub's answer to an upload of a room meeting.
    func uploaded(_ id: UUID, room: MeetingRoom, privateMark: Bool?) {
        applyLive(id, room: room, privateMark: privateMark, followup: nil)
    }

    /// What a `POST …/live` 200 says.
    func applyLive(_ id: UUID, room: MeetingRoom?, privateMark: Bool?, followup: RecordingUpload.SessionRef?) {
        var m = meetings[id] ?? Meeting()
        // Answers to separate requests arrive in any order: a late one never takes the room back to an earlier state (a
        // read of `…/live` is the hub's word and always replaces it).
        if let room, m.room.map({ Self.rank(room.known) >= Self.rank($0.known) }) ?? true { m.room = room }
        if let privateMark {
            m.privateMark = privateMark
            m.privateRead = true
        }
        if let followup { m.followup = followup }
        m.missing = false
        if meetings[id] != m { meetings[id] = m }
    }

    /// `GET /api/v1/recordings/{id}/live` (all parts, or `?parts=a,b`): room, private, followup, asks, revs. One read per
    /// recording at a time; what is asked meanwhile is read once it is back.
    func refresh(_ id: UUID, parts: [String]? = nil) async {
        let wanted: Wanted
        if let parts { wanted = .parts(Self.unique(parts)) } else { wanted = .all }
        guard !reading.contains(id) else {
            readAgain[id] = Self.merge(readAgain[id], wanted)
            return
        }
        reading.insert(id)
        var next: Wanted? = wanted
        while let current = next {
            await read(id, current)
            next = readAgain.removeValue(forKey: id)
        }
        reading.remove(id)
    }

    /// The page is on screen (inserts `owner` and reads everything again, its photos too) / gone.
    func watch(_ id: UUID, by owner: UUID) {
        if watched[id]?.contains(owner) != true { watched[id, default: []].insert(owner) }
        Task { await refresh(id) }
        if takesPhotos(id) { photosChanged(id, rev: nil) }
    }

    func unwatch(_ id: UUID, by owner: UUID) {
        guard var owners = watched[id], owners.contains(owner) else { return }
        owners.remove(owner)
        watched[id] = owners.isEmpty ? nil : owners
    }

    /// A `meeting_live` event: reads the parts newer than those read so far, of those the phone reads (only for watched
    /// recordings or room meetings recorded here, running or ended within the last 12 hours). Newer `photos` read the
    /// photos (`GET …/photos`), not `…/live`, whose `photos` part is only a revision.
    func event(recordingID: String, changed: [String], revs: [String: Int64]) {
        guard let id = UUID(uuidString: recordingID), follows(id) else { return }
        let wanted = partsRead(for: id)
        let parts = Self.newerParts(changed: changed, revs: revs, known: meetings[id]?.revs ?? [:]).filter { wanted.contains($0) }
        if parts.contains("photos") { photosChanged(id, rev: revs["photos"]) }
        let live = parts.filter { $0 != "photos" }
        guard !live.isEmpty else { return }
        Task { await refresh(id, parts: live) }
    }

    /// The socket came back (`hello`): every watched meeting is read again, its photos too (its events while it was down
    /// are gone).
    func reconnected() {
        for (id, owners) in watched where !owners.isEmpty {
            Task { await refresh(id) }
            if takesPhotos(id) { photosChanged(id, rev: nil) }
        }
    }

    /// The parts of a `meeting_live` event the phone follows for `id`: phase 1's, and `photos` for a meeting that started
    /// with them (`RecordingAttendance.photos`).
    func partsRead(for id: UUID) -> [String] { takesPhotos(id) ? Self.readParts + ["photos"] : Self.readParts }

    /// The meeting started while the hub took photos: the page shows them, and the hub's changes to them are read.
    private func takesPhotos(_ id: UUID) -> Bool { library.entry(id)?.attendance?.photos == true }

    /// The photos moved at the hub (added, noted, cited, deleted), as of `rev` when it is known: `GET …/photos` reads
    /// them, and only once that list applied is `rev` kept, so the same event isn't read twice. A read that fails
    /// (offline, a timeout, the hub switching) keeps the revision before it: the next event, or the `…/live` read after a
    /// reconnect, still sees a newer one and reads again.
    private func photosChanged(_ id: UUID, rev: Int64?) {
        let read = deps.readPhotos
        Task {
            guard await read(id), let rev else { return }
            var m = meetings[id] ?? Meeting()
            m.revs["photos"] = max(rev, m.revs["photos"] ?? rev)
            if meetings[id] != m { meetings[id] = m }
        }
    }

    // MARK: The private mark

    /// `PUT /api/v1/recordings/{id}/private`; returns the stored value; throws APIError.
    @discardableResult
    func setPrivate(_ id: UUID, _ value: Bool) async throws -> Bool {
        guard let api = deps.api() else { throw URLError(.notConnectedToInternet) }
        let path = Self.path(id, "private")
        let data = try await afterUpload(id) { try await api.request("PUT", path, body: PrivateMark(privateMark: value)) }
        let stored = (try? JSON.decoder.decode(PrivateMark.self, from: data))?.privateMark ?? value
        var m = meetings[id] ?? Meeting()
        m.privateMark = stored
        m.privateRead = true
        if meetings[id] != m { meetings[id] = m }
        return stored
    }

    // MARK: Just me

    /// `POST …/just-me`: the owner's private question to the main chat; nil when there are no words or images, or more
    /// words than the hub takes (`tooLong`: the bar says so before, nothing is cut).
    @discardableResult
    func sendJustMe(_ entry: RecordingEntry, text: String, images: [ImagePayload] = []) -> UUID? {
        guard !Self.tooLong(text) else { return nil }
        let words = Self.trimmed(text)
        guard !words.isEmpty || !images.isEmpty else { return nil }
        let send = JustMe(id: UUID(), recordingID: entry.id, text: words, images: images, at: deps.now(), state: .sending)
        justMe.append(send)
        Task { await deliverJustMe(send.id) }
        return send.id
    }

    /// A failed one again: the same id while the hub may still answer it, else (a definite answer) a new one.
    func retryJustMe(_ id: UUID) {
        guard let index = justMe.firstIndex(where: { $0.id == id }), case .failed = justMe[index].state else { return }
        var send = justMe[index]
        if spent.remove(id) != nil { send.id = UUID() }
        send.state = .sending
        justMe[index] = send
        Task { await deliverJustMe(send.id) }
    }

    func justMe(for recording: UUID) -> [JustMe] { justMe.filter { $0.recordingID == recording } }

    // MARK: The follow-up

    /// Writes into the meeting's follow-up: `POST …/followup` until it exists, then the session's messages. Nil when there
    /// were no words or images, or more words than `…/followup` takes on its way there (`followupLimited`).
    @discardableResult
    func sendFollowup(_ recording: UUID, text: String, images: [ImagePayload] = []) -> UUID? {
        let route = followupKey(recording).map { FollowupSend.Route.messages($0) } ?? FollowupSend.Route.followup
        if route == .followup, Self.tooLong(text) { return nil }
        let words = Self.trimmed(text)
        guard !words.isEmpty || !images.isEmpty else { return nil }
        let send = FollowupSend(id: UUID(), recordingID: recording, text: words, images: images, state: .sending, route: route)
        followups.append(send)
        Task { await deliverFollowup(send.id) }
        return send.id
    }

    /// A failed one again, the same way: the same id while the hub may still answer it, else (a definite answer) a new one.
    func retryFollowup(_ id: UUID) {
        guard let index = followups.firstIndex(where: { $0.id == id }), case .failed = followups[index].state else { return }
        var send = followups[index]
        if spent.remove(id) != nil { send.id = UUID() }
        send.state = .sending
        followups[index] = send
        Task { await deliverFollowup(send.id) }
    }

    func followups(for recording: UUID) -> [FollowupSend] { followups.filter { $0.recordingID == recording } }

    /// Drops a sent follow-up once its message is in the follow-up's transcript (the thread calls it).
    func delivered(_ id: UUID) {
        guard followups.contains(where: { $0.id == id }) else { return }
        followups.removeAll { $0.id == id }
    }

    /// The `client_msg_id`s the follow-up's transcript holds a send's message under (compare case-insensitively):
    /// `followup-<id>` through `…/followup`, the op id through the session's messages.
    nonisolated static func clientMsgIDs(for send: FollowupSend) -> [String] {
        switch send.route {
        case .followup: ["followup-" + send.id.uuidString.lowercased()]
        case .messages: [send.id.uuidString.lowercased()]
        }
    }

    /// The follow-up's first message goes through `…/followup`, which takes at most `maxText` characters; later ones go to
    /// the session's messages, which have no such limit.
    func followupLimited(_ recording: UUID) -> Bool { followupKey(recording) == nil }

    /// Whether the hub would still answer the follow-up `409 meeting_running` while things here go their way: the
    /// recording runs; an upload on its way may carry the `ended_at` the hub doesn't have yet (within the hub's 12 hours
    /// of the last upload it stored); the room is starting, ready or unavailable; or a room meeting's room isn't read yet.
    /// The hub's own word goes first: a room `ended` or `deleted` has passed the meeting's end, the 12 hours included.
    /// An older manager that answered without `room` (`roomMissing`) and a full read without one have no room to wait
    /// for. A refused upload or a paused sync isn't on its way: `followupHold` says so instead of waiting for it.
    func running(_ entry: RecordingEntry) -> Bool {
        if entry.active { return true }
        let m = meeting(entry.id)
        if Self.hubEnded(m) || sync.roomMissing.contains(entry.id) { return false }
        if !sync.endedStored.contains(entry.id), !sync.automaticRetryPaused,
           [.pending, .uploading].contains(sync.status(entry)), recentlyStored(entry.id) { return true }
        if let room = m.room {
            switch room.known {
            case .starting, .ready, .unavailable: return true
            case .none, .ended, .deleted: return false
            }
        }
        return entry.roomMeeting && !m.missing && !m.loaded
    }

    /// Why the after page's composer can't write to the follow-up now; nil when it can.
    enum FollowupHold: Equatable {
        /// The hub would answer `409 meeting_running` until what is on its way lands (`running`).
        case finishing
        /// The hub stored no upload with `ended_at` and none is on its way (refused, or the sync paused on a refusal):
        /// it counts the meeting as running until 12 hours after the last upload it stored.
        case endRefused
        /// The last upload's `200` had no `room`: an older manager, which has no follow-up either.
        case noFollowup
    }

    func followupHold(_ entry: RecordingEntry) -> FollowupHold? {
        if sync.roomMissing.contains(entry.id) { return .noFollowup }
        if running(entry) { return .finishing }
        guard !entry.active, !sync.endedStored.contains(entry.id), !Self.hubEnded(meeting(entry.id)) else { return nil }
        let status = sync.status(entry)
        let held = status == .rejected || (sync.automaticRetryPaused && [.pending, .uploading].contains(status))
        return held && recentlyStored(entry.id) ? .endRefused : nil
    }

    /// The hub has passed the meeting's end: its room is `ended`, or `deleted` (trimmed or deleted counts as ended).
    private static func hubEnded(_ m: Meeting) -> Bool {
        guard let room = m.room else { return false }
        return room.known == .ended || room.known == .deleted
    }

    /// The hub's last stored upload of `id` is under 12 hours old, or it has none: a hub without `ended_at` still counts
    /// the meeting as running. The revision is the entry's time when it went, a little before the hub's own: the phone
    /// may open a moment early, and then the hub's `409 meeting_running` says so.
    private func recentlyStored(_ id: UUID) -> Bool {
        guard let stored = sync.storedRevision(id) else { return true }
        return deps.now() < Date(timeIntervalSince1970: Double(stored) / 1000).addingTimeInterval(Self.followsEnded)
    }

    // MARK: Pure

    /// The parts of a `meeting_live` event newer than those read (a part with no number is read). Pure.
    nonisolated static func newerParts(changed: [String], revs: [String: Int64], known: [String: Int64]) -> [String] {
        var out: [String] = []
        for part in changed where !out.contains(part) {
            guard let rev = revs[part], let have = known[part] else {
                out.append(part)
                continue
            }
            if rev > have { out.append(part) }
        }
        return out
    }

    /// What a failed Just me or follow-up says, in plain words (a private change: `privateMessage`).
    static func message(_ error: Error) -> String {
        if let api = error as? APIError {
            switch api.code {
            case "meeting_running": return String(localized: "Persona is still finishing the meeting. Try again in a moment.")
            case "no_material": return String(localized: "This meeting has no transcript or minutes to talk about yet.")
            case "meeting_ended": return String(localized: "The meeting has ended. Ask about it after the meeting, in its conversation.")
            case "not_meeting": return String(localized: "This recording isn't a meeting.")
            case "kb_read_only": return String(localized: "Knowledge is read-only on the hub, so this can't change now.")
            case "op_id_reused": return String(localized: "Couldn't send. Retry.")
            case "too_large": return String(localized: "That's too large to send. Try fewer or smaller photos.")
            default: break
            }
            if api.status == 413 { return String(localized: "That's too large to send. Try fewer or smaller photos.") }
            if api.status == 503 { return String(localized: "The hub isn't ready. Try again in a moment.") }
        }
        if error.isOffline || error.isTimeout { return String(localized: "Couldn't reach the hub. Check the connection and retry.") }
        return String(localized: "Couldn't send. Retry.")
    }

    /// What a failed change of the private mark says: the reasons the hub names as `message` has them, else that the
    /// switch didn't change (nothing was sent, and the switch itself is the way to try again).
    static func privateMessage(_ error: Error) -> String {
        if let api = error as? APIError, ["kb_read_only", "not_meeting"].contains(api.code) || api.status == 503 { return message(error) }
        if error.isOffline || error.isTimeout { return message(error) }
        return String(localized: "Couldn't change Private Meeting. Try again.")
    }

    // MARK: Reading

    private func read(_ id: UUID, _ wanted: Wanted) async {
        guard let api = deps.api() else { return }
        var query: [String: String] = [:]
        if case .parts(let parts) = wanted {
            guard !parts.isEmpty else { return }
            query["parts"] = parts.joined(separator: ",")
        }
        let data: Data
        do {
            data = try await api.request("GET", Self.path(id, "live"), query: query)
        } catch let error as APIError where error.status == 404 || error.status == 410 {
            var m = meetings[id] ?? Meeting()
            m.missing = true
            if meetings[id] != m { meetings[id] = m }
            return
        } catch {
            return // offline, a timeout, the hub away: what was read before stands; an event or a reconnect reads again
        }
        guard let answer = try? JSON.decoder.decode(LiveRead.self, from: data) else { return }
        var m = meetings[id] ?? Meeting()
        switch wanted {
        case .all:
            m.room = answer.room
            m.privateMark = answer.privateMark
            m.privateRead = true
            m.followup = answer.followup
            m.asks = answer.asks ?? []
            m.loaded = true
        case .parts(let parts):
            // A part asked for and absent is one the hub has no value for (`private` not chosen, no follow-up yet).
            if parts.contains("room") { m.room = answer.room }
            if parts.contains("private") {
                m.privateMark = answer.privateMark
                m.privateRead = true
            }
            if parts.contains("followup") { m.followup = answer.followup }
            if parts.contains("asks") { m.asks = answer.asks ?? [] }
        }
        m.missing = false
        // The photos' revision moves only with a read of the photos that applied: a `…/live` read answered just after a
        // photo changed would otherwise take the revision of a change `GET …/photos` never saw, and the event for it
        // would read nothing. One newer than the last read (an event lost while the socket was down, or a read that
        // failed) reads them again, and is kept once that read applied.
        if let rev = answer.revs?["photos"], rev > (m.revs["photos"] ?? -1), takesPhotos(id) { photosChanged(id, rev: rev) }
        for (part, rev) in answer.revs ?? [:] where part != "photos" { m.revs[part] = max(rev, m.revs[part] ?? rev) }
        if meetings[id] != m { meetings[id] = m }
    }

    /// A watched recording, or a room meeting recorded here that runs or ended in the last 12 hours.
    private func follows(_ id: UUID) -> Bool {
        if watched[id]?.isEmpty == false { return true }
        guard let entry = library.entry(id), entry.roomMeeting else { return false }
        return entry.active || entry.createdAt.addingTimeInterval(entry.duration) > deps.now().addingTimeInterval(-Self.followsEnded)
    }

    /// The room's states in the order they come: none, starting, ready or unavailable (either way round), ended, deleted.
    private static func rank(_ state: MeetingRoom.State) -> Int {
        switch state {
        case .none: 0
        case .starting: 1
        case .ready, .unavailable: 2
        case .ended: 3
        case .deleted: 4
        }
    }

    private static func merge(_ queued: Wanted?, _ wanted: Wanted) -> Wanted {
        switch (queued, wanted) {
        case (nil, _): wanted
        case (.all?, _), (_, .all): .all
        case (.parts(let a)?, .parts(let b)): .parts(unique(a + b))
        }
    }

    private static func unique(_ parts: [String]) -> [String] {
        var out: [String] = []
        for part in parts where !part.isEmpty && !out.contains(part) { out.append(part) }
        return out
    }

    // MARK: Sending

    /// What to do after a failed send.
    private enum Next {
        /// The first request with this id is still running at the hub (`202 pending`): the same one again in a moment.
        case pending
        /// No answer (a timeout, a lost connection) or one the hub runs again: the same request again, a few times.
        case again
        /// `404 recording_not_found`: the upload goes first, then the request once more.
        case reupload
        /// Failed; `spend`: the hub answered that id for good, so a retry goes under a new one.
        case stop(spend: Bool)
    }

    /// The same request again at most this often, for a pending one (2 minutes in all) and for the others.
    private static let pendingTries = 40
    private static let againTries = 3

    /// Just me's receipt replays every answer it recorded for an id (failures included), so only no answer at all, the
    /// assistant being away (`503 assistant_offline`, which keeps no receipt) and a pending one keep the id.
    private static func justMeNext(_ error: Error) -> Next {
        guard let api = error as? APIError else { return error is DecodingError ? .stop(spend: true) : .again }
        if api.code == OpReceipt.pendingCode { return .pending }
        if api.status == 503, api.code == "assistant_offline" { return .again }
        if api.status == 404, api.code == "recording_not_found" { return .reupload }
        return .stop(spend: true)
    }

    /// The follow-up's receipt keeps 400 and 409 answers for good; any other answer (a 5xx, a 404, an unknown receipt)
    /// the hub runs again for the same id.
    private static func followupNext(_ error: Error) -> Next {
        guard let api = error as? APIError else { return .again } // no answer, or a 2xx without the follow-up
        if api.code == OpReceipt.pendingCode { return .pending }
        if [400, 409, 413].contains(api.status) { return .stop(spend: true) }
        if api.status == 404, api.code == "recording_not_found" { return .reupload }
        if [401, 403, 410].contains(api.status) { return .stop(spend: false) }
        return .again
    }

    private func deliverJustMe(_ first: UUID) async {
        var id = first
        var pending = 0, again = 0, reuploaded = false
        while let send = justMe.first(where: { $0.id == id }), send.state == .sending {
            do {
                let reply = try await postJustMe(send)
                let key = SessionKey(group: deps.group(), machineID: reply.conversation.machineID, sessionID: reply.conversation.sessionID)
                let clientMsgID = reply.clientMsgID ?? "just-me-" + id.uuidString.lowercased()
                editJustMe(id) { $0.state = .sent(conversation: key, clientMsgID: clientMsgID) }
                return
            } catch {
                switch Self.justMeNext(error) {
                case .pending where pending < Self.pendingTries:
                    pending += 1
                    await deps.wait(.seconds(3))
                case .again where again < Self.againTries:
                    again += 1
                    await deps.wait(.seconds(2))
                case .reupload where !reuploaded:
                    // The hub replays this id's 404 for good: once uploaded, the question goes under a new one.
                    reuploaded = true
                    guard await deps.reupload(send.recordingID) else { return failJustMe(id, error, spend: true) }
                    let fresh = UUID()
                    editJustMe(id) { $0.id = fresh }
                    id = fresh
                case .pending, .again:
                    return failJustMe(id, error, spend: false)
                case .reupload, .stop:
                    return failJustMe(id, error, spend: true)
                }
            }
        }
    }

    private func postJustMe(_ send: JustMe) async throws -> JustMeReply {
        guard let api = deps.api() else { throw URLError(.notConnectedToInternet) }
        // It may start a main chat: as long as a message send may take.
        let data = try await api.request("POST", Self.path(send.recordingID, "just-me"),
                                          body: Self.wire(send.id, send.text, send.images), timeout: 100)
        try APIClient.checkReceipt(data, status: nil)
        return try JSON.decoder.decode(JustMeReply.self, from: data)
    }

    private func failJustMe(_ id: UUID, _ error: Error, spend: Bool) {
        if spend { spent.insert(id) }
        editJustMe(id) { $0.state = .failed(Self.message(error)) }
    }

    private func editJustMe(_ id: UUID, _ edit: (inout JustMe) -> Void) {
        guard let index = justMe.firstIndex(where: { $0.id == id }) else { return }
        var send = justMe[index]
        edit(&send)
        justMe[index] = send
    }

    private func deliverFollowup(_ id: UUID) async {
        var pending = 0, again = 0, reuploaded = false
        while let send = followups.first(where: { $0.id == id }), send.state == .sending {
            do {
                try await postFollowup(send)
                editFollowup(id) { $0.state = .sent }
                return
            } catch {
                switch Self.followupNext(error) {
                case .pending where pending < Self.pendingTries:
                    pending += 1
                    await deps.wait(.seconds(3))
                case .again where again < Self.againTries:
                    again += 1
                    await deps.wait(.seconds(2))
                case .reupload where !reuploaded:
                    // The hub runs a 404 again for the same id: once uploaded, the same request.
                    reuploaded = true
                    guard await deps.reupload(send.recordingID) else { return failFollowup(id, error, spend: false) }
                case .pending, .again, .reupload:
                    return failFollowup(id, error, spend: false)
                case .stop(let spend):
                    return failFollowup(id, error, spend: spend)
                }
            }
        }
    }

    private func postFollowup(_ send: FollowupSend) async throws {
        guard let api = deps.api() else { throw URLError(.notConnectedToInternet) }
        switch send.route {
        case .followup:
            // The first call starts the follow-up conversation: as long as a message send may take.
            let data = try await api.request("POST", Self.path(send.recordingID, "followup"),
                                              body: Self.wire(send.id, send.text, send.images), timeout: 100)
            try APIClient.checkReceipt(data, status: nil)
            // A 2xx without the follow-up is no answer yet: the same id again (the hub replays what it made).
            guard let ref = try JSON.decoder.decode(FollowupReply.self, from: data).followup,
                  !ref.machineID.isEmpty, !ref.sessionID.isEmpty else { throw URLError(.badServerResponse) }
            var m = meetings[send.recordingID] ?? Meeting()
            m.followup = ref
            if meetings[send.recordingID] != m { meetings[send.recordingID] = m }
        case .messages(let key):
            try await api.send(key, text: send.text, images: send.images, clientMsgID: send.id.uuidString, opID: send.id)
        }
    }

    private func failFollowup(_ id: UUID, _ error: Error, spend: Bool) {
        if spend { spent.insert(id) }
        editFollowup(id) { $0.state = .failed(Self.message(error)) }
    }

    private func editFollowup(_ id: UUID, _ edit: (inout FollowupSend) -> Void) {
        guard let index = followups.firstIndex(where: { $0.id == id }) else { return }
        var send = followups[index]
        edit(&send)
        followups[index] = send
    }

    /// `404 recording_not_found`: the recording isn't on the hub yet (a switch turned right at the start); the upload
    /// goes first, then `call` once more.
    private func afterUpload<T>(_ id: UUID, _ call: () async throws -> T) async throws -> T {
        do {
            return try await call()
        } catch let error as APIError where error.status == 404 && error.code == "recording_not_found" {
            guard await deps.reupload(id) else { throw error }
            return try await call()
        }
    }

    // MARK: Wire

    private static func path(_ id: UUID, _ route: String) -> String { "/api/v1/recordings/\(id.uuidString.lowercased())/\(route)" }

    /// The words as they go: trimmed, never cut (`tooLong` keeps an over-long text from going at all).
    private nonisolated static func trimmed(_ text: String) -> String { text.trimmingCharacters(in: .whitespacesAndNewlines) }

    /// More than `maxText` characters once trimmed (Unicode scalars, as the hub counts): the hub would refuse it, and the
    /// bar keeps Send off with a caption rather than drop the end of the owner's words.
    nonisolated static func tooLong(_ text: String) -> Bool {
        // A scalar takes at least one UTF-8 byte: a text of fewer bytes is short enough without counting (each keystroke).
        guard text.utf8.count > maxText else { return false }
        return trimmed(text).unicodeScalars.count > maxText
    }

    private static func wire(_ id: UUID, _ text: String, _ images: [ImagePayload]) -> WireMessage {
        WireMessage(id: id.uuidString.lowercased(), text: text.isEmpty ? nil : text,
                    images: images.isEmpty ? nil : images.map { WireMessage.Image(mime: $0.mime, data: $0.data) })
    }

    /// The body of `…/just-me` and `…/followup`: the receipt's digest covers all of it, so a repeat is built the same.
    private nonisolated struct WireMessage: Encodable, Sendable {
        /// Only what the routes take (an `ImagePayload` may carry the size and other fields of a lazy one).
        nonisolated struct Image: Encodable, Sendable {
            var mime: String
            var data: String
        }
        var id: String
        var text: String?
        var images: [Image]?
    }

    private nonisolated struct JustMeReply: Decodable, Sendable {
        var conversation: RecordingUpload.SessionRef
        var clientMsgID: String?
        enum CodingKeys: String, CodingKey { case conversation, clientMsgID = "client_msg_id" }
    }

    private nonisolated struct FollowupReply: Decodable, Sendable {
        var followup: RecordingUpload.SessionRef?
        var created: Bool?
        var clientMsgID: String?
        enum CodingKeys: String, CodingKey { case followup, created, clientMsgID = "client_msg_id" }
    }

    /// `PUT …/private`'s body and answer.
    private nonisolated struct PrivateMark: Codable, Sendable {
        var privateMark: Bool?
        enum CodingKeys: String, CodingKey { case privateMark = "private" }
    }

    /// `GET …/live`'s phase-1 parts, each read on its own: a field of another shape is left out, not the whole answer.
    private nonisolated struct LiveRead: Decodable, Sendable {
        var room: MeetingRoom?
        var privateMark: Bool?
        var followup: RecordingUpload.SessionRef?
        var asks: [MeetingAskWire.Ask]?
        var revs: [String: Int64]?

        enum CodingKeys: String, CodingKey { case room, privateMark = "private", followup, asks, revs }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            room = try? c.decodeIfPresent(MeetingRoom.self, forKey: .room)
            privateMark = try? c.decodeIfPresent(Bool.self, forKey: .privateMark)
            followup = try? c.decodeIfPresent(RecordingUpload.SessionRef.self, forKey: .followup)
            asks = (try? c.decodeIfPresent([Lenient<MeetingAskWire.Ask>].self, forKey: .asks))?.compactMap(\.value)
            revs = try? c.decodeIfPresent([String: Int64].self, forKey: .revs)
        }
    }

    /// One element of a list, or nil when it has another shape (the others still read).
    private nonisolated struct Lenient<Value: Decodable>: Decodable {
        var value: Value?
        init(from decoder: Decoder) throws { value = try? Value(from: decoder) }
    }
}

extension MeetingConversation.Deps {
    /// The app's: recordings sync with the hub-level group's hub (`AppModel.api`).
    static var live: Self {
        Self(api: { AppModel.shared.api },
             group: { AppModel.shared.hub?.group ?? "" },
             now: { .now },
             wait: { try? await Task.sleep(for: $0) },
             reupload: { id in
                 RecordingSync.shared.reupload(id)
                 // As `MeetingAskWire.settled` waits: 10 s at most for the hub to store the latest revision.
                 for _ in 0..<50 {
                     try? await Task.sleep(for: .milliseconds(200))
                     if let entry = RecordingLibrary.shared.entry(id), RecordingSync.shared.status(entry) == .synced { return true }
                 }
                 return false
             })
    }
}
#endif
