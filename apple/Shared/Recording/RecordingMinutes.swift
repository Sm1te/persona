import Foundation

/// The hub's minutes of a meeting recording (docs/assistant/meeting-minutes.md「Field contract」, #2710): when and where, who
/// was there, the points discussed, the decisions, the action items (who, what, by when) and the open questions. Each item
/// carries `at`, seconds from the recording's start (the transcript's clock), so a tap lands on that line. Decoded from
/// `GET /api/v1/kb/recordings/{id}`'s `detail.minutes`; sections the hub leaves out are empty here.
nonisolated struct RecordingMinutes: Codable, Hashable, Sendable {
    /// A point, decision, action item or open question. An action item's text is its `what`.
    nonisolated struct Item: Codable, Hashable, Sendable {
        var text: String
        /// Seconds into the recording where it was said.
        var at: Double?
        /// Who said it (a point) or does it (an action item), only when the transcript or the calendar says so.
        var who: String?
        /// An action item's deadline, as said ("Friday").
        var by: String?
        /// An action item's name within one `version` of the minutes ("a1"): what confirming it as a to-do sends.
        var id: String?
        /// An action item's deadline as a date (`YYYY-MM-DD`), when the one said resolves to a single day.
        var dueDate: String?
        /// The to-do an action item became once the user confirmed it (#4062); absent or empty while it is a suggestion.
        var todoID: String?

        init(text: String, at: Double? = nil, who: String? = nil, by: String? = nil, id: String? = nil, dueDate: String? = nil,
             todoID: String? = nil) {
            self.text = text; self.at = at; self.who = who; self.by = by; self.id = id; self.dueDate = dueDate; self.todoID = todoID
        }

        /// An action item already confirmed as a to-do.
        var added: Bool { todoID?.isEmpty == false }

        enum CodingKeys: String, CodingKey {
            case text, what, at, who, by, id
            case dueDate = "due_date", todoID = "todo_id"
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            text = try c.decodeIfPresent(String.self, forKey: .text) ?? c.decodeIfPresent(String.self, forKey: .what) ?? ""
            at = try? c.decodeIfPresent(Double.self, forKey: .at)
            who = try c.decodeIfPresent(String.self, forKey: .who)
            by = try c.decodeIfPresent(String.self, forKey: .by)
            id = try c.decodeIfPresent(String.self, forKey: .id)
            dueDate = try c.decodeIfPresent(String.self, forKey: .dueDate)
            todoID = try c.decodeIfPresent(String.self, forKey: .todoID)
        }

        func encode(to encoder: Encoder) throws {
            var c = encoder.container(keyedBy: CodingKeys.self)
            try c.encode(text, forKey: .text)
            try c.encodeIfPresent(at, forKey: .at)
            try c.encodeIfPresent(who, forKey: .who)
            try c.encodeIfPresent(by, forKey: .by)
            try c.encodeIfPresent(id, forKey: .id)
            try c.encodeIfPresent(dueDate, forKey: .dueDate)
            try c.encodeIfPresent(todoID, forKey: .todoID)
        }
    }

    /// Goes up each time the hub writes them again; sent back when confirming action items as to-dos (#4062).
    var version: Int?
    var title: String?
    /// "2026-11-10 14:00–15:10", as the hub writes it: shown only when `start` / `end` are missing (a hub from before them).
    var when: String?
    /// The recording's span (#4713): what the app formats in its own date style (`whenLine`).
    var start: Date?
    var end: Date?
    var place: String?
    var attendees: [String] = []
    var points: [Item] = []
    var decisions: [Item] = []
    var actions: [Item] = []
    var openQuestions: [Item] = []

    init(title: String? = nil, when: String? = nil, start: Date? = nil, end: Date? = nil, place: String? = nil, attendees: [String] = [],
         points: [Item] = [], decisions: [Item] = [], actions: [Item] = [], openQuestions: [Item] = []) {
        self.title = title; self.when = when; self.start = start; self.end = end; self.place = place; self.attendees = attendees
        self.points = points; self.decisions = decisions; self.actions = actions; self.openQuestions = openQuestions
    }

    enum CodingKeys: String, CodingKey {
        case version, title, when, start, end, attendees, points, decisions, actions
        case place = "where", openQuestions = "open"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        version = try? c.decodeIfPresent(Int.self, forKey: .version)
        title = try c.decodeIfPresent(String.self, forKey: .title)
        when = try c.decodeIfPresent(String.self, forKey: .when)
        // RFC 3339 with the user's offset, whole seconds ("2026-11-10T14:00:00+08:00"); unreadable reads as absent.
        start = (try? c.decodeIfPresent(String.self, forKey: .start)).flatMap { $0 }.flatMap(JSON.parseDate)
        end = (try? c.decodeIfPresent(String.self, forKey: .end)).flatMap { $0 }.flatMap(JSON.parseDate)
        place = try c.decodeIfPresent(String.self, forKey: .place)
        attendees = (try? c.decodeIfPresent([String].self, forKey: .attendees)) ?? []
        points = try c.decodeIfPresent([Item].self, forKey: .points) ?? []
        decisions = try c.decodeIfPresent([Item].self, forKey: .decisions) ?? []
        actions = try c.decodeIfPresent([Item].self, forKey: .actions) ?? []
        openQuestions = try c.decodeIfPresent([Item].self, forKey: .openQuestions) ?? []
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(version, forKey: .version)
        try c.encodeIfPresent(title, forKey: .title)
        try c.encodeIfPresent(when, forKey: .when)
        try c.encodeIfPresent(start?.formatted(.iso8601), forKey: .start)
        try c.encodeIfPresent(end?.formatted(.iso8601), forKey: .end)
        try c.encodeIfPresent(place, forKey: .place)
        try c.encode(attendees, forKey: .attendees)
        try c.encode(points, forKey: .points)
        try c.encode(decisions, forKey: .decisions)
        try c.encode(actions, forKey: .actions)
        try c.encode(openQuestions, forKey: .openQuestions)
    }

    var isEmpty: Bool { points.isEmpty && decisions.isEmpty && actions.isEmpty && openQuestions.isEmpty }

    /// When the meeting was, as the recording page's header writes dates (#4875); the hub's `when` from a hub without
    /// `start` / `end`.
    var whenLine: String? {
        if let start, let end { return RecordingFormat.span(start, end) }
        return when?.isEmpty == false ? when : nil
    }

    /// The transcript line said at `seconds`: the last one that started by then (a second's grace: a point is often
    /// stamped as its sentence begins), else the first.
    static func sentenceIndex(at seconds: Double, in sentences: [RecordingSentence]) -> Int? {
        guard !sentences.isEmpty, seconds.isFinite else { return nil }
        return sentences.lastIndex { $0.start <= seconds + 1 } ?? 0
    }

    /// An item's line under its text: who, by when (as said, and the date it resolves to).
    static func byline(_ item: Item) -> String? {
        let due = item.dueDate.flatMap { $0.isEmpty ? nil : String(localized: "Due \($0)") }
        let parts = [item.who, item.by, due].compactMap { $0?.isEmpty == false ? $0 : nil }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    /// What Copy and Share send: Markdown, headed by the title (the recording's name without one), then when and where.
    func text(title fallback: String) -> String {
        var out = ["# " + ((title?.isEmpty == false ? title : nil) ?? fallback)]
        let line = [whenLine, place].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ")
        if !line.isEmpty { out.append(line) }
        if !attendees.isEmpty { out.append(String(localized: "Attendees: \(ListFormatter.localizedString(byJoining: attendees))")) }
        func section(_ heading: String, _ items: [Item]) {
            guard !items.isEmpty else { return }
            out += ["", "## " + heading]
            for item in items {
                var row = "- " + item.text
                if let byline = Self.byline(item) { row += " (" + byline + ")" }
                if let at = item.at { row += " [" + RecordingFormat.clock(at) + "]" }
                out.append(row)
            }
        }
        section(String(localized: "Key Points"), points)
        section(String(localized: "Decisions"), decisions)
        section(String(localized: "Action Items"), actions)
        section(String(localized: "Open Questions"), openQuestions)
        return out.joined(separator: "\n")
    }
}
