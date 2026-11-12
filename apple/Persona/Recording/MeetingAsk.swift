#if os(iOS)
import Foundation
import Observation

/// Asking Persona during a meeting it attends (#2981; docs/assistant/meeting-minutes.md「Asking」). Only by a tap: there is
/// no wake phrase. A typed question, or none: then the question is what was just said in the room (the open segment is
/// cut at the tap, transcribed, and its lines sent before the question, `live_next`). The hub answers from meeting
/// material only. The answer shows on the recording, and in a meeting Persona takes part in it is said out loud
/// (`MeetingSpeaker`): at most once, only for a question asked in this run, only while the recording runs, only when the
/// answer came within a minute of the tap, and only while the role still speaks.
@MainActor @Observable
final class MeetingAsk {
    static let shared = MeetingAsk(deps: .live)

    /// One question as it goes to the hub (`POST /api/v1/recordings/{id}/ask`).
    nonisolated struct Question: Equatable, Sendable {
        /// The phone's id for it: a retry sends the same one, so the hub never answers twice.
        var id: UUID
        var recordingID: UUID
        /// When it was asked, in seconds on the recording's clock.
        var at: Double
        /// What was typed; nil for a tap that asks about what was just said.
        var text: String?
        /// The live log's length once the lines up to the tap went (a question about what was just said).
        var liveNext: Int?
    }

    /// The hub's answer (`Ask`).
    nonisolated struct Answer: Equatable, Sendable {
        /// The question the hub understood (for one about what was just said: what it heard).
        var question: String?
        var text: String
        /// BCP 47: the language it is written in.
        var lang: String?
        /// Say it aloud (the hub fixes it from the role when it was asked).
        var speak: Bool
        /// The earlier meetings and projects it drew on: their names and, for a meeting, when it was.
        var sources: [String]
        /// Which way the hub answered it (#3862): `room` (the meeting's room conversation) or `material` (meeting
        /// material, as #2981 meetings are answered); nil from a hub without the meeting conversation.
        var via: String? = nil
    }

    struct Exchange: Identifiable, Equatable {
        enum State: Equatable {
            case asking
            case answered(String)
            case failed(String)
        }

        var id = UUID()
        var recordingID: UUID
        var at: Double
        /// What was typed, or what the hub heard; nil until it said.
        var question: String?
        var typed: Bool
        var liveNext: Int?
        var state: State
        var sources: [String] = []
        /// When it was asked, for the one-minute rule on saying it aloud.
        var asked = Date.now
        /// How the hub answered it (`Answer.via`), once it did.
        var via: String? = nil
        /// The hub's error code of a failed one (`room_unavailable` sends it again as a new question).
        var failure: String? = nil

        var answer: String? { if case .answered(let text) = state { text } else { nil } }
    }

    struct Deps {
        var ask: (Question) async throws -> Answer
        /// Where the recording is now, in seconds (`RecordingCapture.timeline`).
        var now: () -> Double
        /// Before a question about what was just said: cuts the open segment, waits for its text and sends the live lines;
        /// the live log's length once the hub holds them (nil: it doesn't, the hub then takes what it has).
        var heard: (UUID) async -> Int?
        /// Waits until the hub stored the meeting's attendance (an upload), at most a few seconds; whether it did.
        var settled: (UUID) async -> Bool
        /// The recording still runs and Persona may speak in it now.
        var maySpeak: (UUID) -> Bool
        var speak: (_ text: String, _ exchange: UUID, _ recording: UUID, _ lang: String?, _ locale: String) -> Void
        var stopSpeaking: () -> Void
        var clock: () -> Date
        /// An older manager took this room meeting with Persona off: its last upload's `200` had no `room`
        /// (`RecordingSync.roomMissing`, #3862).
        var roomMissing: (UUID) -> Bool = { _ in false }
    }

    enum Failure: Error, Equatable {
        /// The hub can't answer during a meeting (an older hub or manager).
        case unsupported
        /// The hub's own reason (its `error` or `code`).
        case hub(String)
    }

    /// Longest typed question (the contract's limit, in characters).
    static let maxQuestion = 1000

    /// A typed question can go: it has something besides spaces and new lines (what `ask` trims away). The live bar's
    /// send button waits for this.
    nonisolated static func hasWords(_ text: String) -> Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    /// An answer later than this after the tap is only shown, never said.
    static let speakWithin: TimeInterval = 60

    private(set) var exchanges: [Exchange] = []
    @ObservationIgnored var deps: Deps

    init(deps: Deps) { self.deps = deps }

    func exchanges(for recording: UUID) -> [Exchange] { exchanges.filter { $0.recordingID == recording } }
    func latest(for recording: UUID) -> Exchange? { exchanges.last { $0.recordingID == recording } }
    var busy: Bool { exchanges.contains { $0.state == .asking } }

    /// Persona can be asked about `entry`'s meeting now: it attends, and the hub holds the meeting as one it attends.
    /// `roomMissing`: an older manager took the room meeting with Persona off (`RecordingSync.roomMissing`), so an ask
    /// would only force a whole upload and fail; it can be asked again once a `200` carries `room`. The one rule for the
    /// meeting page, its bar, the Live Activity and `ask` / `retry` (#3862).
    nonisolated static func canAsk(_ entry: RecordingEntry, roomMissing: Bool) -> Bool { entry.attended && !roomMissing }

    /// Asks about `entry`'s meeting: `text` typed, or nil to ask about what was just said. Nothing when Persona can't be
    /// asked about it (`canAsk`).
    @discardableResult
    func ask(_ entry: RecordingEntry, text: String? = nil) -> UUID? {
        guard Self.canAsk(entry, roomMissing: deps.roomMissing(entry.id)) else { return nil }
        let typed = text.map { String(String.UnicodeScalarView($0.trimmingCharacters(in: .whitespacesAndNewlines).unicodeScalars.prefix(Self.maxQuestion))) }
        if let typed, !Self.hasWords(typed) { return nil }
        let exchange = Exchange(recordingID: entry.id, at: max(0, deps.now()), question: typed, typed: typed != nil, state: .asking,
                                asked: deps.clock())
        exchanges.append(exchange)
        run(exchange.id, locale: entry.locale, prepare: typed == nil)
        return exchange.id
    }

    /// Asks a failed one again: the same id, the same moment. A room ask the hub failed with `room_unavailable` goes as a
    /// new question (a new id): the hub fails that id again at once, while a new one is answered from the meeting so far.
    func retry(_ id: UUID, entry: RecordingEntry) {
        guard Self.canAsk(entry, roomMissing: deps.roomMissing(entry.id)), let index = exchanges.firstIndex(where: { $0.id == id }), case .failed = exchanges[index].state else { return }
        var exchange = exchanges[index]
        if exchange.failure == Self.roomUnavailable { exchange.id = UUID() }
        exchange.state = .asking
        exchange.failure = nil
        exchanges[index] = exchange
        run(exchange.id, locale: entry.locale, prepare: false)
    }

    func stopSpeaking() { deps.stopSpeaking() }

    /// The recording ended: nothing more is said, and its questions go (the next meeting starts empty; the hub keeps
    /// them as the meeting's `asks`).
    func ended(_ recording: UUID) {
        deps.stopSpeaking()
        exchanges.removeAll { $0.recordingID == recording }
    }

    #if DEBUG
    /// Screenshots (`-showMeetingSample answered`): made-up exchanges, no hub.
    func showSample(_ sample: [Exchange]) {
        exchanges.removeAll { e in sample.contains { $0.recordingID == e.recordingID } }
        exchanges += sample
    }
    #endif

    /// The hub's code for a room ask asked again while the room is unavailable (#3862).
    private static let roomUnavailable = "room_unavailable"

    /// `renewed`: this is already the one automatic new question after `room_unavailable`, so it is not asked again.
    private func run(_ id: UUID, locale: String, prepare: Bool, renewed: Bool = false) {
        Task {
            guard let start = exchanges.first(where: { $0.id == id }) else { return }
            // The hub's copy of the attendance first: until it has it, no live line can go, so a question about what was
            // just said would reach the hub before the lines it is about.
            let settled = await deps.settled(start.recordingID)
            var liveNext = start.liveNext
            if prepare, settled {
                liveNext = await deps.heard(start.recordingID)
                if let index = exchanges.firstIndex(where: { $0.id == id }) { exchanges[index].liveNext = liveNext }
            }
            let state: Exchange.State
            var answer: Answer?
            var failure: String?
            // The meeting ended meanwhile (its questions went with it): nothing to ask any more.
            guard exchanges.contains(where: { $0.id == id }) else { return }
            if settled {
                do {
                    let got = try await deps.ask(Question(id: id, recordingID: start.recordingID, at: start.at,
                                                          text: start.typed ? start.question : nil, liveNext: liveNext))
                    answer = got
                    state = got.text.isEmpty ? .failed(Self.message(nil)) : .answered(got.text)
                } catch {
                    state = .failed(Self.message(error))
                    failure = Self.code(error)
                }
            } else if deps.roomMissing(start.recordingID) {
                // The hub answered the upload without `room` meanwhile: not the connection, an older manager.
                state = .failed(String(localized: "This hub can't take part in meetings right now. The recording goes on as usual."))
            } else {
                state = .failed(Self.message(nil))
            }
            guard let index = exchanges.firstIndex(where: { $0.id == id }) else { return }
            // A room ask asked again while the room is unavailable fails at once, and the hub answers the same question
            // asked anew from the meeting so far: once, on its own, keeping the moment and the tap (the speak rule).
            if failure == Self.roomUnavailable, !renewed {
                let fresh = UUID()
                exchanges[index].id = fresh
                run(fresh, locale: locale, prepare: false, renewed: true)
                return
            }
            exchanges[index].state = state
            exchanges[index].failure = failure
            if let answer {
                if let heard = answer.question, !heard.isEmpty { exchanges[index].question = heard }
                exchanges[index].sources = answer.sources
                exchanges[index].via = answer.via
                let e = exchanges[index]
                if case .answered(let text) = state, answer.speak, deps.maySpeak(e.recordingID),
                   deps.clock().timeIntervalSince(e.asked) <= Self.speakWithin {
                    deps.speak(text, id, e.recordingID, answer.lang, locale)
                }
            }
        }
    }

    /// What a failed question says: what happened, then the one next step.
    static func message(_ error: Error?) -> String {
        switch error {
        case let failure as Failure:
            switch failure {
            case .unsupported: return String(localized: "This hub can't answer during meetings yet. Update the hub, then try again.")
            case .hub(let code): return hubMessage(code)
            }
        case let api as APIError:
            if let code = api.code, api.status != 503 { return hubMessage(code) }
            return String(localized: "Couldn't get an answer. Check the connection and try again.")
        default:
            return String(localized: "Couldn't get an answer. Check the connection and try again.")
        }
    }

    /// The hub's error code of a failure (its `error` or `code`), for what to do next.
    private static func code(_ error: Error) -> String? {
        switch error {
        case let failure as Failure:
            if case .hub(let code) = failure, !code.isEmpty { return code }
            return nil
        case let api as APIError: return api.code
        default: return nil
        }
    }

    private static func hubMessage(_ code: String) -> String {
        switch code {
        case "model_unavailable": String(localized: "No model is set up to answer. Choose one in Settings › Model Settings, then try again.")
        case "model_timeout", "model_error": String(localized: "Persona couldn't answer in time. Try again.")
        case "interrupted": String(localized: "The hub restarted while answering. Try again.")
        case "nothing_heard": String(localized: "Persona didn't hear a question just before the tap. Tap right after the question, or type it.")
        case "ask_busy": String(localized: "Persona is still answering two questions. Ask again once one is answered.")
        case "asks_full": String(localized: "This meeting has reached 100 questions, the most it takes.")
        case "kb_read_only": String(localized: "Knowledge is read-only on the hub, so Persona can't answer now.")
        case "not_attending": String(localized: "The hub doesn't have Persona in this meeting yet. Try again in a moment.")
        case "meeting_ended": String(localized: "The meeting has ended. Ask about it after the meeting, in its conversation.")
        case "room_unavailable": String(localized: "Persona's meeting conversation isn't available. Ask again for an answer from the meeting so far.")
        case "no_answer": String(localized: "Persona didn't answer that. Ask again.")
        default: String(localized: "Couldn't get an answer. Check the connection and try again.")
        }
    }
}

extension MeetingAsk.Deps {
    /// The app's: the hub, the recording, the phone's voice.
    static var live: Self {
        Self(ask: { try await MeetingAskWire.ask($0) },
             now: { RecordingCapture.shared.timeline },
             heard: { id in
                 RecordingCapture.shared.flushSegment()
                 await MeetingAskWire.transcribed(id)
                 MeetingLive.shared.append(id)
                 return await MeetingLive.shared.sent(id)
             },
             settled: { id in await MeetingAskWire.settled(id) },
             maySpeak: { id in
                 RecordingCapture.shared.recordingID == id && RecordingLibrary.shared.entry(id).map { $0.attended && $0.attendance?.role.speaks == true } == true
             },
             speak: { text, exchange, recording, lang, locale in
                 MeetingSpeaker.shared.speak(text, id: exchange, recording: recording, lang: lang, locale: locale)
             },
             stopSpeaking: { MeetingSpeaker.shared.stop() },
             clock: { .now },
             roomMissing: { RecordingSync.shared.roomMissing.contains($0) })
    }
}

/// `POST /api/v1/recordings/{id}/ask` as the contract has it: the same request again (same id) while the answer is
/// still being written (`202`, a timeout, a server error, a dropped connection), until it is answered or failed.
@MainActor enum MeetingAskWire {
    nonisolated struct Body: Encodable, Sendable {
        var id: String
        var question: String?
        var at: Double
        var liveNext: Int?
        enum CodingKeys: String, CodingKey { case id, question, at, liveNext = "live_next" }
    }

    nonisolated struct Ask: Decodable, Equatable, Sendable {
        var id: String?
        var question: String?
        var state: String
        var answer: String?
        var lang: String?
        var speak: Bool?
        var sources: [Source]?
        var error: String?
        /// `room` or `material` (#3862); nil on #2981 meetings and while the ask waits for a room that is starting.
        var via: String?
        /// A heard ask in a room meeting: the lines it was asked about.
        var heard: [Heard]?
        /// `typed`, `spoken` or `heard`.
        var questionSource: String?
        /// When it was asked, in seconds on the recording's clock (omitted when not sent).
        var at: Double?

        enum CodingKeys: String, CodingKey {
            case id, question, state, answer, lang, speak, sources, error, via, heard, at
            case questionSource = "question_source"
        }
    }

    nonisolated struct Source: Decodable, Equatable, Sendable {
        var name: String?
        /// The hub's fixed-format time of a meeting: shown only from a hub without `start` / `end`.
        var when: String?
        /// A meeting's span, RFC 3339 (#4713): formatted as the recording page's header writes dates.
        var start: String?
        var end: String?
    }

    /// One line a heard ask was about.
    nonisolated struct Heard: Decodable, Equatable, Sendable {
        var start: Double
        var end: Double
        var text: String

        enum CodingKeys: String, CodingKey { case start, end, text }
    }

    private struct Reply: Decodable { var ask: Ask }

    /// How long one question may take in all: the hub gives the model 3 minutes.
    static let deadline: TimeInterval = 4 * 60

    static func body(_ q: MeetingAsk.Question) -> Body {
        Body(id: q.id.uuidString.lowercased(), question: q.text, at: q.at, liveNext: q.liveNext)
    }

    static func ask(_ q: MeetingAsk.Question) async throws -> MeetingAsk.Answer {
        let path = "/api/v1/recordings/\(q.recordingID.uuidString.lowercased())/ask"
        let started = Date.now
        var uploaded = false
        while true {
            guard let api = AppModel.shared.api else { throw URLError(.notConnectedToInternet) }
            do {
                let data = try await api.request("POST", path, body: body(q), timeout: 35)
                let ask = try JSON.decoder.decode(Reply.self, from: data).ask
                switch ask.state {
                case "answered": return answer(ask)
                case "failed": throw MeetingAsk.Failure.hub(ask.error ?? "")
                default: break // pending: the same request again in a moment
                }
            } catch let error as APIError {
                switch (error.status, error.code) {
                case (404, "recording_not_found") where !uploaded:
                    // The hub has no upload of this recording yet: it goes first, then the question again.
                    uploaded = true
                    RecordingSync.shared.reupload(q.recordingID)
                    guard await settled(q.recordingID) else { throw error }
                case (404, "recording_not_found"):
                    throw error
                case (404, _):
                    // An older manager has no such route: this meeting stops attending.
                    RecordingLibrary.shared.change(q.recordingID) { $0.attendance?.stopped = .unsupported }
                    throw MeetingAsk.Failure.unsupported
                case (503, _), (500..., _), (408, _):
                    break
                default:
                    throw error
                }
            } catch let failure as MeetingAsk.Failure {
                throw failure
            } catch {
                if error.isCancellation { throw error }
                // A timeout or a dropped connection: the same id again.
            }
            guard Date.now.timeIntervalSince(started) < deadline else { throw MeetingAsk.Failure.hub("model_timeout") }
            try await Task.sleep(for: .seconds(3))
        }
    }

    static func answer(_ ask: Ask) -> MeetingAsk.Answer {
        MeetingAsk.Answer(question: ask.question, text: ask.answer ?? "", lang: ask.lang, speak: ask.speak ?? false,
                          sources: (ask.sources ?? []).compactMap { s in
                              guard let name = s.name, !name.isEmpty else { return nil }
                              return RecordingFormat.span(start: s.start, end: s.end, fallback: s.when).map { "\(name) · \($0)" } ?? name
                          }, via: ask.via)
    }

    /// Waits (10 s at most) until the hub has stored the meeting's current attendance.
    static func settled(_ id: UUID) async -> Bool {
        func done() -> Bool { RecordingLibrary.shared.entry(id).map { RecordingSync.shared.attendanceSettled($0) } ?? false }
        if done() { return true }
        RecordingSync.shared.reupload(id)
        for _ in 0..<50 {
            try? await Task.sleep(for: .milliseconds(200))
            if done() { return true }
        }
        return false
    }

    /// Waits (12 s at most) until every segment of the recording so far has its text.
    static func transcribed(_ id: UUID) async {
        for _ in 0..<60 {
            guard let entry = RecordingLibrary.shared.entry(id), entry.segments.contains(where: { $0.sentences == nil }) else { return }
            try? await Task.sleep(for: .milliseconds(200))
        }
    }
}

nonisolated extension MeetingAskWire.Heard {
    /// Leniently: a line of another shape reads as an empty one rather than losing the ask (and its answer) it came in.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        start = (try? c.decodeIfPresent(Double.self, forKey: .start)) ?? 0
        end = (try? c.decodeIfPresent(Double.self, forKey: .end)) ?? 0
        text = (try? c.decodeIfPresent(String.self, forKey: .text)) ?? ""
    }
}
#endif
