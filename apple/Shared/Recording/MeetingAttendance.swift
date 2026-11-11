import Foundation

/// Persona's part in a meeting being recorded (#2981; the contract is docs/assistant/meeting-minutes.md「Attending a
/// meeting」, the phone's side docs/apple/recording.md「Persona in meetings」): off, listening in, or taking part. Chosen
/// each time a meeting recording starts, pre-selected from the hub's `meeting_attendance_default`. Offered only by a hub
/// that lists the `meeting_attendance` feature; only a phone's meeting recording has one (notes and watch recordings never).
nonisolated enum MeetingAttendance: String, Codable, CaseIterable, Sendable {
    /// Today's behaviour: the hub writes minutes after the meeting, and nothing reaches it while the meeting goes on.
    case off
    /// Persona follows the meeting and answers on screen only. It never speaks.
    case listener
    /// Persona also says its answers out loud, through a connected headset or the loudspeaker.
    case participant

    var attends: Bool { self != .off }
    var speaks: Bool { self == .participant }

    /// The segmented choice's word.
    var title: String {
        switch self {
        case .off: String(localized: "Off")
        case .listener: String(localized: "Listen [Meeting Role]")
        case .participant: String(localized: "Take Part [Meeting Role]")
        }
    }

    /// One plain sentence on what the choice does.
    var detail: String {
        switch self {
        case .off: String(localized: "Persona stays out of the meeting. The minutes come after it ends.")
        case .listener: String(localized: "Persona follows the meeting. Ask it from this phone; it answers on screen and never speaks.")
        case .participant: String(localized: "Persona follows the meeting and answers out loud when you tap Ask Persona.")
        }
    }
}

/// The attendance of one meeting recording, kept with the recording on this phone: the role, the session that attends,
/// the phone's live log of what it sent the hub, and the stretches Persona spoke in.
nonisolated struct RecordingAttendance: Codable, Hashable, Sendable {
    var role: MeetingAttendance
    /// The attending session's `SessionKey.path`; nil is the main chat only when `mainPicked`, else nobody chosen yet.
    var session: String?
    /// Its name when it was chosen.
    var sessionTitle: String?
    /// The stretches of the recording's clock the role was listener or participant: only a sentence that ends inside
    /// one enters the live log (the contract: what is said while attending is off is never sent). The last one is open
    /// (`end` nil) while attending.
    var periods: [RecordingPeriod] = []
    /// The phone's append-only live log (`POST /api/v1/recordings/{id}/live`): a sentence's position is its index here,
    /// never renumbered. A re-transcription or a dropped echo line changes nothing already in it.
    var live: [RecordingSentence] = []
    /// The segments whose sentences were already looked at for the log, so a segment transcribed again adds nothing.
    var liveSegments: [Int] = []
    /// Where on the recording Persona said an answer out loud: the microphone may have heard it, so a sentence at least
    /// half inside one is Persona's, not the meeting's (left out of the transcript, the upload and the live log).
    var spoken: [RecordingSpan] = []
    /// Why the phone stopped attending on its own, said on the recording: the hub can't follow meetings (an older
    /// manager), or it takes no more live lines for this one.
    var stopped: StopReason?
    /// A room meeting (#3862): started while the hub listed `meeting_conversation`. Its uploads carry `room` and `private`
    /// (never the top-level `attendance` / `attending_session`); it stays one for life.
    var room: Bool = false
    /// Started while the hub listed `meeting_photos` (phase 3): the page offers the camera and the library.
    var photos: Bool = false
    /// The start sheet's Private meeting choice, sent as the upload's `private` (the hub takes it only while it has no mark).
    var privateMark: Bool? = nil
    /// The owner picked the main chat to attend this meeting (#3862): the only way it ever attends one. A meeting's words
    /// fill the chat they are fed to, so the main chat never gets them by default; no session named without this pick
    /// means nobody was chosen, and the meeting goes as off (`sentRole`).
    var mainPicked: Bool = false

    nonisolated enum StopReason: String, Codable, Sendable {
        case unsupported
        case liveFull = "live_full"
    }

    init(role: MeetingAttendance, session: String? = nil, sessionTitle: String? = nil, mainPicked: Bool = false, at: Double = 0) {
        self.role = .off; self.session = session; self.sessionTitle = sessionTitle; self.mainPicked = mainPicked
        set(role, at: at)
    }

    /// A room meeting's attendance as it starts: no attending session (the meeting's own room attends), whatever the role.
    static func room(_ role: MeetingAttendance, private privateMark: Bool, photos: Bool, at: Double = 0) -> RecordingAttendance {
        var a = RecordingAttendance(role: role, at: at)
        a.room = true
        a.photos = photos
        a.privateMark = privateMark
        return a
    }

    enum CodingKeys: String, CodingKey {
        case role, session, sessionTitle, periods, live, liveSegments, spoken, stopped, room, photos, privateMark, mainPicked
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        // A role a later app wrote and this one doesn't know reads as off: nothing attends.
        role = (try? c.decode(MeetingAttendance.self, forKey: .role)) ?? .off
        session = try c.decodeIfPresent(String.self, forKey: .session)
        sessionTitle = try c.decodeIfPresent(String.self, forKey: .sessionTitle)
        periods = try c.decodeIfPresent([RecordingPeriod].self, forKey: .periods) ?? []
        live = try c.decodeIfPresent([RecordingSentence].self, forKey: .live) ?? []
        liveSegments = try c.decodeIfPresent([Int].self, forKey: .liveSegments) ?? []
        spoken = try c.decodeIfPresent([RecordingSpan].self, forKey: .spoken) ?? []
        stopped = try? c.decodeIfPresent(StopReason.self, forKey: .stopped)
        // Written by #3862 apps; an older file has none, and is no room meeting.
        room = (try? c.decodeIfPresent(Bool.self, forKey: .room)) ?? false
        photos = (try? c.decodeIfPresent(Bool.self, forKey: .photos)) ?? false
        privateMark = try? c.decodeIfPresent(Bool.self, forKey: .privateMark)
        // Written since #3862; a file from before has none: its main chat was never picked, so it goes as off.
        mainPicked = (try? c.decodeIfPresent(Bool.self, forKey: .mainPicked)) ?? false
    }

    /// The attending session, nil for the main chat (when `mainPicked`) or nobody.
    var key: SessionKey? { session.flatMap(SessionKey.init(path:)) }

    /// Who attends is known: the meeting's own room, a session named (one the API can name: a malformed path such as ""
    /// would go as no `attending_session`, which the hub reads as the main chat), or the main chat picked for this meeting.
    var hasAttendee: Bool { room || key != nil || mainPicked }

    /// The role the hub is told and the phone acts on (the upload, its stored mark, live lines, asks): `role`, except an
    /// attending one with nobody chosen to attend, which goes as off. The structural half of "never the main chat by
    /// default" (#3862): `attending_session` left out means the main chat to the hub, so an attending role without a
    /// target is never sent, whatever built the attendance.
    var sentRole: MeetingAttendance { role.attends && !hasAttendee ? .off : role }

    /// Sets the role at `at` (seconds on the recording's clock), opening or closing the attending period.
    mutating func set(_ next: MeetingAttendance, at: Double) {
        let open = periods.last.map { $0.end == nil } ?? false
        if next.attends, !open { periods.append(RecordingPeriod(start: max(0, at))) }
        if !next.attends, open, let last = periods.indices.last { periods[last].end = max(at, periods[last].start) }
        role = next
        if next.attends { stopped = nil }
    }

    /// The sentence ended while Persona attended.
    func attended(_ line: RecordingSentence) -> Bool {
        periods.contains { $0.holds(line.end) }
    }

    /// Persona's own voice: at least half of the line is inside a stretch it spoke in (`open`: one still going).
    func spokenBy(_ line: RecordingSentence, open: RecordingSpan? = nil) -> Bool {
        spoken.contains { $0.holds(line) } || (open?.holds(line) ?? false)
    }
}

/// A stretch of a recording, in seconds on its clock (`RecordingSentence.start`).
nonisolated struct RecordingSpan: Codable, Hashable, Sendable {
    var start: Double
    var end: Double

    /// At least half of the line was said inside this stretch (the contract's rule for Persona's own voice).
    func holds(_ line: RecordingSentence) -> Bool {
        let length = line.end - line.start
        guard length > 0 else { return line.start >= start && line.start <= end }
        let inside = min(line.end, end) - max(line.start, start)
        return inside / length >= 0.5
    }
}

/// A stretch with an open end: attending since `start`, until `end` (nil while it goes on).
nonisolated struct RecordingPeriod: Codable, Hashable, Sendable {
    var start: Double
    var end: Double?

    func holds(_ time: Double) -> Bool { time >= start && end.map { time <= $0 } ?? true }
}

/// The hub's side of attending (docs/assistant/meeting-minutes.md「Attendance fields」).
nonisolated enum MeetingAttendanceDefaults {
    /// Listed in `hello.features` by a hub whose manager takes everything about attending: only then the phone offers a role.
    static let feature = "meeting_attendance"
    /// The last the phone heard from the hub, for a start without the app in front (Siri, the Control, the Action
    /// button): whether it offers attending, and its default role. Rarely changes, so a user default.
    static let offeredKey = "recording.meeting.offered"
    static let roleKey = "recording.meeting.defaultRole"
    /// The session picked last (`SessionKey.path`, `main` for the main chat): the next meeting starts with it again while
    /// it can still attend.
    static let sessionKey = "recording.meeting.attendee"
    /// Its name then, for a start before the hub has listed it.
    static let sessionTitleKey = "recording.meeting.attendeeTitle"
    static let main = "main"

    /// The role a meeting starts with when nothing is picked: the hub's default while it offers attending, else off.
    static func cachedRole(_ store: UserDefaults = .standard) -> MeetingAttendance {
        guard store.bool(forKey: offeredKey) else { return .off }
        return store.string(forKey: roleKey).flatMap(MeetingAttendance.init(rawValue:)) ?? .off
    }

    /// Keeps what the hub said: whether it lists the feature, and `meeting_attendance_default` when known.
    static func remember(offered: Bool, role: MeetingAttendance?, _ store: UserDefaults = .standard) {
        if store.object(forKey: offeredKey) == nil || store.bool(forKey: offeredKey) != offered { store.set(offered, forKey: offeredKey) }
        if let role, store.string(forKey: roleKey) != role.rawValue { store.set(role.rawValue, forKey: roleKey) }
    }

    /// Listed by a hub whose manager has the meeting conversation (#3862, docs/assistant/meeting-minutes.md「Flags and
    /// compatibility」): a meeting started then is a room meeting.
    static let conversationFeature = "meeting_conversation"
    /// …and photos in the meeting (phase 3); only ever with `meeting_conversation`.
    static let photosFeature = "meeting_photos"
    /// Whether the hub listed the two, for a start without the app in front, as `offeredKey`.
    static let conversationKey = "recording.meeting.conversation"
    static let photosKey = "recording.meeting.photos"

    /// Keeps whether the hub lists the two (a start without the app in front reads them).
    static func remember(conversation: Bool, photos: Bool, _ store: UserDefaults = .standard) {
        if store.object(forKey: conversationKey) == nil || store.bool(forKey: conversationKey) != conversation { store.set(conversation, forKey: conversationKey) }
        if store.object(forKey: photosKey) == nil || store.bool(forKey: photosKey) != photos { store.set(photos, forKey: photosKey) }
    }

    /// Keeps every meeting flag of a hub's `hello` as it arrives (`features`, not empty: an empty list says nothing), so a
    /// start without the app in front (Siri, a Control, the Action button) goes by the last hub it heard, not by the last
    /// one whose Recordings page was open: a stale `meeting_conversation` would start a room meeting an older manager
    /// takes as off. `role`: the hub's default role, when its settings are in.
    static func remember(hello features: Set<String>, role: MeetingAttendance?, _ store: UserDefaults = .standard) {
        let offered = features.contains(feature)
        remember(offered: offered, role: role, store)
        let conversation = offered && features.contains(conversationFeature)
        remember(conversation: conversation, photos: conversation && features.contains(photosFeature), store)
    }
}

/// The room conversation's state as the hub reports it (`room` on the upload's 200, `GET/POST …/live`, `detail.meta`;
/// docs/assistant/meeting-minutes.md「Fields on `GET …/live` and `detail.meta`」).
nonisolated struct MeetingRoom: Codable, Hashable, Sendable {
    nonisolated enum State: String, Sendable { case none, starting, ready, unavailable, ended, deleted }
    var state: String
    var machineID: String?
    var sessionID: String?
    /// Why it is `unavailable` (an open set: `runtime_unsupported`, `node_too_old`, `machine_offline`, `start_failed`).
    var problem: String?

    enum CodingKeys: String, CodingKey { case state, machineID = "machine_id", sessionID = "session_id", problem }

    /// An open set: a value the app doesn't know reads as `starting`.
    var known: State { State(rawValue: state) ?? .starting }

    /// The room's conversation once it has ids (never with `none` / `deleted`).
    func key(group: String) -> SessionKey? {
        guard known != State.none, known != State.deleted, let machineID, !machineID.isEmpty, let sessionID, !sessionID.isEmpty else { return nil }
        return SessionKey(group: group, machineID: machineID, sessionID: sessionID)
    }
}

nonisolated extension MeetingRoom {
    /// Leniently: a field of another shape is left out rather than losing the answer it came in (a missing state is one
    /// the app doesn't know).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        state = (try? c.decodeIfPresent(String.self, forKey: .state)) ?? State.starting.rawValue
        machineID = try? c.decodeIfPresent(String.self, forKey: .machineID)
        sessionID = try? c.decodeIfPresent(String.self, forKey: .sessionID)
        problem = try? c.decodeIfPresent(String.self, forKey: .problem)
    }
}
