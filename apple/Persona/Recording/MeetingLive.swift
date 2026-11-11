#if os(iOS)
import Foundation
import Observation

/// The live sentences of a meeting Persona attends (#2981, docs/assistant/meeting-minutes.md「Live sentences」): every
/// finished sentence that ended while Persona attended, and isn't Persona's own voice, goes into the recording's
/// append-only live log (`RecordingAttendance.live`), and from there to `POST /api/v1/recordings/{id}/live`, from the
/// position the hub holds (`next`). Nothing goes before the hub has stored an upload with the meeting's current
/// attendance (`RecordingSync.attendanceSettled`). The live log only feeds the attending session and the answers; the
/// transcript stays the upload's.
@MainActor @Observable
final class MeetingLive {
    static let shared = MeetingLive(deps: .live)

    /// What the hub said about one meeting's live lines.
    struct State: Equatable {
        /// How many positions the hub holds: the next request starts there. nil until it said.
        var next: Int?
        /// How many it has given the attending session.
        var fed: Int?
        /// The hub's `problem` (an open set), for the page.
        var problem: String?
    }

    struct Answer: Decodable, Equatable {
        var next: Int
        var fed: Int?
        var problem: String?
        /// A room meeting's room, the private mark and the follow-up as the hub has them now (#3862,
        /// docs/assistant/meeting-minutes.md「Fields on `GET …/live` and `detail.meta`」).
        var room: MeetingRoom? = nil
        var privateMark: Bool? = nil
        var followup: RecordingUpload.SessionRef? = nil

        enum CodingKeys: String, CodingKey { case next, fed, problem, room, privateMark = "private", followup }
    }

    struct Deps {
        var post: (_ recording: UUID, _ from: Int, _ segments: [RecordingSentence]) async throws -> Answer
        /// The hub has the meeting's current attendance (`RecordingSync.attendanceSettled`).
        var settled: (RecordingEntry) -> Bool
        /// The hub needs the upload first, even if this phone thinks it was stored (`RecordingSync.reupload`).
        var upload: (UUID) -> Void
        /// The stretch Persona is saying now (`MeetingSpeaker.openSpan`).
        var openSpan: (UUID) -> RecordingSpan?
        var wait: (Duration) async -> Void
        /// Every `200` of `…/live`: the meeting conversation keeps what it says of the room (`MeetingConversation.applyLive`).
        var report: (UUID, Answer) -> Void = { _, _ in }
    }

    /// Lines in one request at most (the contract's limit).
    static let batch = 200

    private(set) var states: [UUID: State] = [:]
    @ObservationIgnored var deps: Deps
    @ObservationIgnored private let library: RecordingLibrary
    @ObservationIgnored private var sending: Set<UUID> = []
    @ObservationIgnored private var again: Set<UUID> = []
    @ObservationIgnored private var failures: [UUID: Int] = [:]
    @ObservationIgnored private var started = false

    init(deps: Deps, library: RecordingLibrary = .shared) {
        self.deps = deps
        self.library = library
    }

    /// From `MeetingRecorder.start`: every save of a recording may bring new lines; an upload the hub took lets them go.
    func start() {
        guard !started else { return }
        started = true
        RecordingSync.shared.onChange = { [weak self] id in self?.changed(id) }
        RecordingSync.shared.onAttendanceStored = { [weak self] id in self?.changed(id) }
    }

    func state(_ id: UUID) -> State { states[id] ?? State() }

    /// A recording changed: its new lines join the log, and what the hub doesn't hold goes.
    func changed(_ id: UUID) {
        append(id)
        guard let entry = library.entry(id), entry.attendance != nil else { return }
        Task { await send(id) }
    }

    /// Adds the sentences of segments transcribed since last time: those that ended while Persona attended and are not
    /// its own voice. Positions already given never move. Returns whether any were added.
    @discardableResult
    func append(_ id: UUID) -> Bool {
        guard let entry = library.entry(id), entry.isMeeting, entry.source == .phone, let attendance = entry.attendance else { return false }
        let fresh = entry.segments.sorted { $0.index < $1.index }
            .filter { $0.sentences != nil && !attendance.liveSegments.contains($0.index) }
        guard !fresh.isEmpty else { return false }
        let open = deps.openSpan(id)
        var lines: [RecordingSentence] = []
        for segment in fresh {
            for line in segment.sentences ?? [] where !line.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                if attendance.attended(line), !attendance.spokenBy(line, open: open) { lines.append(line) }
            }
        }
        library.change(id) { e in
            e.attendance?.live += lines
            e.attendance?.liveSegments += fresh.map(\.index)
        }
        return !lines.isEmpty
    }

    /// Sends what the hub doesn't hold yet, until it holds all of the log or something stops it. One at a time per meeting.
    func send(_ id: UUID) async {
        guard !sending.contains(id) else { again.insert(id); return }
        sending.insert(id)
        defer { sending.remove(id) }
        repeat {
            again.remove(id)
            await sendAll(id)
        } while again.contains(id)
    }

    /// The log's length once the hub holds every line in it (for a question about what was just said: the hub waits for
    /// that many); nil when the lines can't go now. Waits (10 s at most) for a send already under way, then sends the rest.
    func sent(_ id: UUID) async -> Int? {
        for _ in 0..<50 where sending.contains(id) { await deps.wait(.milliseconds(200)) }
        await send(id)
        guard let entry = library.entry(id), let count = entry.attendance?.live.count, let next = states[id]?.next else { return nil }
        return next >= count ? count : nil
    }

    private func sendAll(_ id: UUID) async {
        while let entry = library.entry(id), let attendance = entry.attendance, attendance.stopped == nil {
            // Off for now (or sent as off: nobody chosen to attend, #3862): the hub refuses lines (`not_attending`); they
            // wait in the log until it is on again.
            guard attendance.sentRole.attends else { return }
            guard deps.settled(entry) else { return } // the upload that names the attendance goes first; it calls back
            let log = attendance.live
            let from = min(states[id]?.next ?? 0, log.count)
            // The hub said all the log is in: nothing to send (an empty request only asks where it is, once).
            if let next = states[id]?.next, next >= log.count { return }
            let chunk = Array(log[from..<min(log.count, from + Self.batch)])
            do {
                let answer = try await deps.post(id, from, chunk)
                failures[id] = nil
                deps.report(id, answer)
                var state = states[id] ?? State()
                state.next = answer.next
                state.fed = answer.fed
                state.problem = answer.problem
                states[id] = state
                if answer.problem == RecordingAttendance.StopReason.liveFull.rawValue { stop(id, .liveFull); return }
                // All in, or the hub took nothing more (never loop on a log it won't take).
                if answer.next >= log.count || answer.next <= from { return }
            } catch let error as APIError {
                guard await handle(error, id) else { return }
            } catch {
                guard await backOff(id) else { return }
            }
        }
    }

    /// What the contract says to do about each refusal; whether to try again now.
    private func handle(_ error: APIError, _ id: UUID) async -> Bool {
        switch (error.status, error.code) {
        case (409, "live_gap"):
            // The hub holds fewer than the phone thought: ask where it is, then go on from there.
            states[id]?.next = nil
            return true
        case (404, "recording_not_found"), (409, "not_attending"):
            // No upload with this attendance yet: it goes first, and calls back once stored.
            deps.upload(id)
            return false
        case (404, _):
            // An older manager has no such route: this meeting stops attending; recording sync goes on as before.
            stop(id, .unsupported)
            return false
        case (410, _):
            return false
        case (413, "live_full"):
            stop(id, .liveFull)
            return false
        case (503, _), (408, _), (429, _), (500..., _):
            return await backOff(id)
        default:
            // A read-only knowledge base or a request the hub refuses: the next change tries again.
            states[id, default: State()].problem = error.code
            return false
        }
    }

    /// The hub was away: again in a while, longer each time (2 s … 60 s); false once the meeting no longer attends.
    private func backOff(_ id: UUID) async -> Bool {
        let n = (failures[id] ?? 0) + 1
        failures[id] = n
        await deps.wait(.seconds(min(60, 2 * Int(pow(2, Double(min(n, 6) - 1))))))
        return library.entry(id)?.attendance?.stopped == nil
    }

    /// The phone stops attending this meeting on its own (an older manager; no room for more live lines).
    private func stop(_ id: UUID, _ reason: RecordingAttendance.StopReason) {
        library.change(id) { e in e.attendance?.stopped = reason }
    }
}

extension MeetingLive.Deps {
    static var live: Self {
        Self(post: { recording, from, segments in
                struct Body: Encodable { var from: Int; var segments: [RecordingUpload.Segment] }
                guard let api = AppModel.shared.api else { throw URLError(.notConnectedToInternet) }
                let body = Body(from: from, segments: segments.map { RecordingUpload.Segment(start: $0.start, end: $0.end, text: $0.text) })
                let data = try await api.request("POST", "/api/v1/recordings/\(recording.uuidString.lowercased())/live", body: body, timeout: 30)
                return try JSON.decoder.decode(MeetingLive.Answer.self, from: data)
             },
             settled: { RecordingSync.shared.attendanceSettled($0) },
             upload: { RecordingSync.shared.reupload($0) },
             openSpan: { MeetingSpeaker.shared.openSpan(for: $0) },
             wait: { try? await Task.sleep(for: $0) },
             report: { id, answer in
                 MeetingConversation.shared.applyLive(id, room: answer.room, privateMark: answer.privateMark, followup: answer.followup)
             })
    }
}
#endif
