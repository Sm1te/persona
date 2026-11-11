#if os(iOS)
import Foundation

/// Who can attend a meeting (#2981, docs/assistant/meeting-minutes.md「What the phone does」): the main chat, Persona's
/// other open chats, and the Agents that take messages now; only those the hub would let attend ("Sessions that never
/// attend": a work computer's, another manager's, the health space's, a chat whose replies leave the apps, one that
/// never asks before it acts). The session picked last starts the next meeting again while it can still attend, unless
/// it was the main chat: the main chat attends only when picked for that meeting (#3862), else Persona stays out until
/// the owner picks.
@MainActor enum MeetingAttendees {
    struct Candidate: Identifiable, Hashable {
        var key: SessionKey
        var title: String
        /// "Main Chat", the computer and folder of an Agent, or nothing.
        var detail: String
        var main: Bool
        /// An assistant conversation (the main chat or another), not an Agent.
        var persona: Bool
        var id: String { key.path }
    }

    // MARK: The hub

    /// The hub offers attending: it lists `meeting_attendance` (from the last `hello` the app saw, kept for a start
    /// without the app in front).
    static func offered(_ model: AppModel) -> Bool {
        let offered = model.hubFeatures.isEmpty ? UserDefaults.standard.bool(forKey: MeetingAttendanceDefaults.offeredKey)
            : model.hubFeatures.contains(MeetingAttendanceDefaults.feature)
        if !model.hubFeatures.isEmpty { MeetingAttendanceDefaults.remember(offered: offered, role: hubDefault(model)) }
        return offered
    }

    /// The hub's `meeting_attendance_default`, when the assistant settings are in.
    static func hubDefault(_ model: AppModel) -> MeetingAttendance? {
        model.assistantConfig?.meetingAttendanceDefault.flatMap(MeetingAttendance.init(rawValue:))
    }

    /// The role a meeting starts with when nothing is picked: the hub's default while it offers attending, else off.
    static func defaultRole(_ model: AppModel) -> MeetingAttendance {
        guard offered(model) else { return .off }
        return hubDefault(model) ?? MeetingAttendanceDefaults.cachedRole()
    }

    /// The hub takes the meeting conversation (#3862): `meeting_attendance` and `meeting_conversation` (from the last
    /// `hello`, kept for a start without the app in front, as `offered`).
    static func conversation(_ model: AppModel) -> Bool {
        guard offered(model) else { return false }
        let features = model.hubFeatures
        guard !features.isEmpty else { return UserDefaults.standard.bool(forKey: MeetingAttendanceDefaults.conversationKey) }
        let conversation = features.contains(MeetingAttendanceDefaults.conversationFeature)
        MeetingAttendanceDefaults.remember(conversation: conversation,
                                           photos: conversation && features.contains(MeetingAttendanceDefaults.photosFeature))
        return conversation
    }

    /// …and photos in the meeting (phase 3), which come only with the conversation.
    static func photos(_ model: AppModel) -> Bool {
        guard conversation(model) else { return false }
        let features = model.hubFeatures
        guard !features.isEmpty else { return UserDefaults.standard.bool(forKey: MeetingAttendanceDefaults.photosKey) }
        return features.contains(MeetingAttendanceDefaults.photosFeature)
    }

    // MARK: Sessions

    /// The main chat first, Persona's other open chats, then the Agents, latest first; each one that may attend.
    static func candidates(_ model: AppModel, limit: Int = 30) -> [Candidate] {
        var out: [Candidate] = []
        // A hub with the meeting conversation keeps meetings out of the main chat (#3862): a meeting begun before it had
        // one (a #2981 meeting) can still pick another session, never the main chat.
        if !conversation(model), let main = mainCandidate(model) { out.append(main) }
        let chats = model.assistantConversations
            .filter { $0.key != model.assistantMain && $0.archived != true && mayAttend($0, model: model) }
            .sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
        out += chats.prefix(limit).map { Candidate(key: $0.key, title: $0.displayTitle, detail: "", main: false, persona: true) }
        let agents = InboxHandOffModel.agents(model.allSessions, limit: .max) { model.machine($0)?.online == true }
            .filter { mayAttend($0, model: model) }
        out += agents.prefix(limit).map { agent($0, model: model) }
        return out
    }

    static func mainCandidate(_ model: AppModel) -> Candidate? {
        guard let key = model.assistantMain else { return nil }
        // The same test as any other chat, once the hub has listed it (its computer may be a work one).
        if let s = model.session(key), !mayAttend(s, model: model) { return nil }
        return Candidate(key: key, title: model.persona.name, detail: String(localized: "Main Chat"), main: true, persona: true)
    }

    static func agent(_ s: Session, model: AppModel) -> Candidate {
        let machine = model.machine(s.machineRef)?.title ?? s.machineID
        return Candidate(key: s.key, title: s.displayTitle, detail: "\(machine) · \(s.folderName)", main: false, persona: false)
    }

    /// The contract's "Sessions that never attend", from what every client sees: other people's words never reach a work
    /// computer, another manager, the health space, a chat whose replies leave the apps, or a session that never asks
    /// before it acts.
    static func mayAttend(_ s: Session, model: AppModel) -> Bool {
        // Every listed session carries its manager; only one other than the default's is out.
        if let roster = model.managerRoster {
            if roster.owner(s.manager) != roster.defaultID { return false }
        } else if let manager = s.manager, !manager.isEmpty, manager != ManagerRoster.assistant {
            return false
        }
        if s.permissionMode == "bypassPermissions" { return false }
        if model.machine(s.machineRef)?.trust == "work" { return false }
        if s.assistant {
            if s.isHealth { return false }
            if let origin = s.origin, ["lark_agent", "channel", "extension", "inbox"].contains(origin) { return false }
            if let opener = s.openedBy, !opener.isEmpty { return false }
        }
        return true
    }

    /// The session a meeting starts with when nobody is picked for it: the one picked last while it can still attend,
    /// never the main chat (#3862, the owner's rule after a real meeting: a meeting's words fill the chat they are fed to,
    /// so the main chat gets them only when picked for that meeting). A remembered main chat counts as no pick; nil then.
    static func preferred(_ model: AppModel, store: UserDefaults = .standard) -> Candidate? {
        guard let path = store.string(forKey: MeetingAttendanceDefaults.sessionKey), path != MeetingAttendanceDefaults.main,
              let key = SessionKey(path: path), key != model.assistantMain else { return nil }
        if let listed = candidates(model).first(where: { $0.key == key }) { return listed.main ? nil : listed }
        // Started before the hub listed anything (Siri, the Control, with the app not running): the one picked last, as
        // it was named then; the hub says if it can't take messages (`attendee_unavailable`).
        if model.allSessions.isEmpty, let title = store.string(forKey: MeetingAttendanceDefaults.sessionTitleKey) {
            return Candidate(key: key, title: title, detail: "", main: false, persona: false)
        }
        return nil
    }

    /// What a meeting recording starts with when nothing was picked for it (the drawer, Siri, the Control, the Action
    /// button): `role` (the default unless given), with the preferred session; off when there is none, so Persona stays
    /// out of the meeting until the owner picks who attends (never the main chat by default, #3862).
    static func defaultAttendance(_ model: AppModel, role: MeetingAttendance? = nil, store: UserDefaults = .standard) -> RecordingAttendance {
        let role = role ?? defaultRole(model)
        guard role.attends, let chosen = preferred(model, store: store) else { return RecordingAttendance(role: .off) }
        return choose(chosen, in: RecordingAttendance(role: role))
    }

    /// `attendance` with `candidate` chosen to attend: a session by name, or the main chat, which is sent by leaving
    /// `attending_session` out and so is marked as picked (`mainPicked`), the only way it ever attends.
    static func choose(_ candidate: Candidate, in attendance: RecordingAttendance) -> RecordingAttendance {
        var a = attendance
        a.session = candidate.main ? nil : candidate.key.path
        a.sessionTitle = candidate.title
        a.mainPicked = candidate.main
        return a
    }

    /// A non-room meeting's attendance once its role turns to `role` at `at` on the recording's clock (the page's picker
    /// and Turn on Persona, the activity's Turn Off), or nil when it would attend with nobody chosen to and no session
    /// picked before that isn't the main chat (`lastPicked`, the preferred one): it then stays as it is, and the page asks
    /// who attends.
    static func turned(_ attendance: RecordingAttendance?, to role: MeetingAttendance, at: Double, lastPicked: Candidate?) -> RecordingAttendance? {
        var a = attendance ?? RecordingAttendance(role: .off)
        if role.attends, !a.hasAttendee {
            guard let lastPicked, !lastPicked.main else { return nil }
            a = choose(lastPicked, in: a)
        }
        a.set(role, at: at)
        return a
    }

    /// What a meeting recording started with nothing picked begins with (`MeetingRecorder.startMeeting`): with the meeting
    /// conversation a room meeting in the default role, not private (the one-tap rule, #3862); else `defaultAttendance`.
    static func startAttendance(_ model: AppModel) -> RecordingAttendance {
        guard conversation(model) else { return defaultAttendance(model) }
        return .room(defaultRole(model), private: false, photos: photos(model))
    }

    /// Remembers the session picked (nil: the main chat, which the next meeting reads as no pick), for the next meeting.
    static func remember(_ key: SessionKey?, title: String?, store: UserDefaults = .standard) {
        store.set(key?.path ?? MeetingAttendanceDefaults.main, forKey: MeetingAttendanceDefaults.sessionKey)
        store.set(title, forKey: MeetingAttendanceDefaults.sessionTitleKey)
    }
}
#endif
