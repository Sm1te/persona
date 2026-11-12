#if os(iOS)
import Foundation

/// A recorded meeting's one thread (#3862, docs/assistant/meeting-minutes.md "Meeting conversation (#3862)"): the room
/// conversation's items, oldest first, and the follow-up's, read apart into what the meeting page shows. The room's
/// first message is the meeting header, its batches carry message source `meeting` and collapse to "N lines from the
/// meeting", a heard question shows as "What was just said (m:ss)", a photo the owner added (phase 3) is one entry where
/// the room got it, and the follow-up's first message holds the meeting's material in a context block before the owner's
/// words.
///
/// A port of the web's `web/src/lib/meeting-thread.ts`, so both apps tell the same items apart the same way. Pure: items
/// in, entries out, no model and no views, so it runs off the main actor and is tested with fixtures shaped like the
/// mock hub's. Every text-derived step is memoized by its text (`TextMemo`, `ContextRef.parse`): the page reads the whole
/// thread again whenever an item arrives, and a long meeting's batches are large.
nonisolated enum MeetingThread {
    /// One live line of a batch or a heard question: its clock in the meeting (`m:ss`) and its words.
    nonisolated struct Line: Hashable, Sendable {
        var clock: String
        var text: String
    }

    /// How an owner's question came: typed or spoken on the phone (an ask's attempt), or written into the conversation
    /// some other way (the follow-up, an app that doesn't know rooms).
    nonisolated enum Via: String, Sendable {
        case typed, spoken, owner
    }

    nonisolated enum Entry: Identifiable, Hashable, Sendable {
        /// The room's first message (the meeting header): Persona joined.
        case joined(id: String, at: Date?)
        /// A batch of live lines.
        case lines(id: String, at: Date?, lines: [Line])
        /// The end batch: the meeting ended (after the batch's last lines, when it had any).
        case ended(id: String, at: Date?)
        /// A question heard in the room: the lines before the tap; `ask` the ask it is an attempt of, lowercase.
        case heard(id: String, at: Date?, clock: String, lines: [Line], ask: String?)
        /// The owner's own message: an ask typed or spoken on the phone (`ask`, lowercase), or written in the follow-up.
        case question(id: String, at: Date?, text: String, via: Via, ask: String?)
        /// Persona's reply: the assistant's texts with nothing shown between them, joined.
        case answer(id: String, at: Date?, text: String)
        /// A photo the owner sent (lazy from history pages: its `ref` loads it).
        case image(id: String, at: Date?, image: ImagePayload?)
        /// A context block of the owner's message; the follow-up's first (`seed`) is the meeting's material.
        case material(id: String, at: Date?, title: String, text: String, seed: Bool)
        /// A meeting photo the hub forwarded to the room (phase 3): its image and its line from the meeting share the
        /// `client_msg_id` `photo-<id>` and show as this one entry, at the first of them. `photoID` is the photo's own id,
        /// lowercase: the page draws it from `GET …/photos`, or as removed once that no longer lists it.
        case photo(id: String, at: Date?, photoID: String)

        /// The item's id (unique within its conversation); an end batch's `ended` adds `#end`, a material `#c<n>`.
        var id: String {
            switch self {
            case .joined(let i, _): i
            case .lines(let i, _, _): i
            case .ended(let i, _): i
            case .heard(let i, _, _, _, _): i
            case .question(let i, _, _, _, _): i
            case .answer(let i, _, _): i
            case .image(let i, _, _): i
            case .material(let i, _, _, _, _): i
            case .photo(let i, _, _): i
            }
        }

        /// When its item was written (the hub's `ts`).
        var at: Date? {
            switch self {
            case .joined(_, let t): t
            case .lines(_, let t, _): t
            case .ended(_, let t): t
            case .heard(_, let t, _, _, _): t
            case .question(_, let t, _, _, _): t
            case .answer(_, let t, _): t
            case .image(_, let t, _): t
            case .material(_, let t, _, _, _): t
            case .photo(_, let t, _): t
            }
        }
    }

    /// What `ask`'s `question_source` is per ask id (lowercase), and an ask's `at` (seconds) for a heard question with no clock.
    nonisolated struct AskInfo: Hashable, Sendable {
        var id: String
        var spoken: Bool
        var at: Double?
    }

    // MARK: - The room

    /// The room's items as thread entries. `recordingID` names the header (`meeting-<recording id>`); `asks` tell a spoken
    /// question from a typed one and give a heard question's time when its block has none. `ended`: the meeting is over,
    /// so its last line from the meeting with words after its blocks (or none) is the end batch ("The meeting ended").
    /// Any other such line (a withdrawn photo's "Photo N was withdrawn…", phase 3) is the hub talking to the room and
    /// shows nothing.
    static func room(_ items: [Item], recordingID: UUID, asks: [AskInfo] = [], ended: Bool = true) -> [Entry] {
        let header = "meeting-" + recordingID.uuidString.lowercased()
        let byID = Dictionary(asks.map { ($0.id.lowercased(), $0) }, uniquingKeysWith: { _, last in last })
        // Only the meeting's last line can say it ended: a photo's lines and a withdrawal never do.
        let last = ended ? items.lastIndex { item in
            item.role == "user" && item.kind == .text && photoID(item.clientMsgID) == nil && !withdrawn(item.clientMsgID)
                && (sourceKind(item).map { $0 == "meeting" } ?? raws(item.text ?? "").meeting)
        } : nil
        var photos: Set<String> = []
        var out: [Entry] = []
        out.reserveCapacity(items.count)
        for (index, item) in items.enumerated() {
            // A forwarded photo: its image and its line about it, one entry at the first of them (neither an image, a
            // batch nor a question).
            if item.role == "user", let photo = photoID(item.clientMsgID) {
                if photos.insert(photo).inserted { out.append(.photo(id: item.id, at: item.ts, photoID: photo)) }
                continue
            }
            guard item.role == "user", item.kind == .text else {
                pushReply(&out, item)
                continue
            }
            let raw = item.text ?? ""
            let whole = raws(raw) // once per text: the same scan says whether it is from the meeting and an end batch
            let sourced = sourceKind(item).map { $0 == "meeting" } ?? whole.meeting
            let ask = askID(item.clientMsgID)
            let parsed = ContextRef.parse(raw)
            let block = parsed.refs.first { $0.kind == "context" }
            let headerBlock = sourced && (block.map({ analyzed($0.text).header }) ?? false)
            if item.clientMsgID?.lowercased() == header || headerBlock {
                out.append(.joined(id: item.id, at: item.ts))
            } else if sourced && (ask != nil || matches(heardTitle, block?.title ?? "")) {
                // The block's title carries the clock of the tap; a hub that leaves it out has the ask's `at`.
                let tapped = firstGroup(titleClock, block?.title ?? "")
                    ?? ask.flatMap { byID[$0]?.at }.map { clock($0) }
                    ?? ""
                out.append(.heard(id: item.id, at: item.ts, clock: tapped, lines: lines(block?.text ?? ""), ask: ask))
            } else if sourced {
                // A batch (prompts/meeting/feed.md): its lines in the block. One that holds no line (only questions answered
                // from the meeting's material, which show among the asks) is left out; the end batch says the meeting ended
                // after its block, or alone when nothing was new.
                let found = lines(block?.text ?? "")
                if !found.isEmpty { out.append(.lines(id: item.id, at: item.ts, lines: found)) }
                if whole.endBatch, index == last { out.append(.ended(id: item.id + "#end", at: item.ts)) }
            } else if sourceKind(item) == nil, item.injected == nil {
                // The owner's own: an ask (typed, or spoken from a Talk span), or a message an app sent into the room.
                let words = UserBubble.display(parsed.rest)
                guard !words.isEmpty else { continue }
                let via: Via = ask.map { byID[$0]?.spoken == true ? Via.spoken : Via.typed } ?? .owner
                out.append(.question(id: item.id, at: item.ts, text: words, via: via, ask: ask))
            }
        }
        return out
    }

    // MARK: - The follow-up

    /// The follow-up's items as thread entries: the first message's context block is the meeting's material (shown
    /// folded), then the owner's words; later context blocks are the owner's own picks.
    static func followup(_ items: [Item]) -> [Entry] {
        var out: [Entry] = []
        out.reserveCapacity(items.count)
        var seeded = false
        for item in items {
            guard item.role == "user", item.kind == .text else {
                pushReply(&out, item)
                continue
            }
            // Persona's own lines (a task's notice, a scheduled run, a line from the meeting), not the owner's.
            if sourceKind(item) != nil || item.injected != nil || fromMeeting(item) { continue }
            let parsed = ContextRef.parse(item.text ?? "")
            for (index, ref) in parsed.refs.filter({ $0.kind == "context" }).enumerated() {
                out.append(.material(id: item.id + "#c\(index)", at: item.ts, title: ref.title, text: ref.text, seed: !seeded))
                seeded = true
            }
            seeded = true
            let words = UserBubble.display(parsed.rest)
            if !words.isEmpty { out.append(.question(id: item.id, at: item.ts, text: words, via: .owner, ask: nil)) }
        }
        return out
    }

    // MARK: - Just me and sent messages

    /// The answers to a Just me question in the main chat: the assistant's texts after the user item whose
    /// `clientMsgID` is `clientMsgID` (any case: the hub's `just-me-<id>`), up to the next user text, joined as the room's
    /// replies are. The question's own items (its photos, a queued copy) share its id and are passed over.
    static func justMeReplies(_ items: [Item], after clientMsgID: String) -> [Entry] {
        let wanted = clientMsgID.lowercased()
        guard !wanted.isEmpty,
              let start = items.firstIndex(where: { $0.clientMsgID?.lowercased() == wanted }) else { return [] }
        var out: [Entry] = []
        for item in items[items.index(after: start)...] {
            if item.clientMsgID?.lowercased() == wanted { continue }
            if item.role == "user", item.kind == .text { break }
            if item.role == "assistant" { pushReply(&out, item) }
        }
        return out
    }

    /// A Just me question's answer in the main chat, as the meeting page shows it.
    nonisolated struct JustMeAnswer: Equatable, Sendable {
        /// The assistant's texts of its turn, joined (`justMeReplies`); nil before the first.
        var text: String?
        /// When its message was written (the hub's `ts`): a card raised before it belongs to another turn.
        var at: Date?
        /// Its turn is the chat's newest: no user text came after it, the owner's or one the hub put in (a task's notice, a
        /// scheduled run). Only then is the chat's live text this answer being written, and its cards this turn's.
        var latest: Bool
    }

    /// The answer to the Just me message whose `clientMsgID` is `clientMsgID`, up to the end of its turn: the next user
    /// text of any kind, injected ones included, ends it. Nil while the message isn't in the items (queued behind another
    /// turn, or not loaded): then nothing of the chat is its answer yet.
    static func justMeAnswer(_ items: [Item], after clientMsgID: String) -> JustMeAnswer? {
        let wanted = clientMsgID.lowercased()
        guard !wanted.isEmpty,
              let start = items.firstIndex(where: { $0.clientMsgID?.lowercased() == wanted }) else { return nil }
        let texts = justMeReplies(items, after: clientMsgID).compactMap { entry -> String? in
            if case .answer(_, _, let text) = entry { return text }
            return nil
        }
        let later = items[items.index(after: start)...].contains { item in
            item.role == "user" && item.kind == .text && item.clientMsgID?.lowercased() != wanted
        }
        return JustMeAnswer(text: texts.isEmpty ? nil : texts.joined(separator: "\n\n"), at: items[start].ts, latest: !later)
    }

    // MARK: - Asks answered apart from the room

    /// One of the hub's answered asks (`GET …/live` `asks`) the room's thread doesn't hold: answered from the meeting's
    /// material beside a room, or any answered one of a meeting whose room never came up.
    nonisolated struct AskPair: Identifiable, Hashable, Sendable {
        var id: String
        /// The ask's id, lowercase (empty when the hub gave none).
        var ask: String
        var question: String?
        var answer: String
    }

    /// A row of the room's part of the thread: one of its entries, or an ask answered apart from it.
    nonisolated enum RoomRow: Identifiable, Hashable, Sendable {
        case entry(Entry)
        case ask(AskPair)

        var id: String {
            switch self {
            case .entry(let entry): entry.id
            case .ask(let pair): pair.id
            }
        }
    }

    /// The answered asks as pairs, oldest first: only those answered from the meeting's material (`materialOnly`, beside
    /// a room thread), or all of them.
    static func pairs(_ asks: [MeetingAskWire.Ask], materialOnly: Bool) -> [AskPair] {
        placedPairs(asks, materialOnly: materialOnly).map(\.pair)
    }

    /// The room's entries with the asks answered from the material where they were asked, so the one thread stays oldest
    /// first: each before the first question or heard question of an ask the hub lists after it (`asks`, oldest first),
    /// and any left before the meeting's end, never after it. Ordered by the asks' order, not by clocks: the phone's and
    /// the hub's may differ, and a batch's words say nothing a phone should parse.
    static func merged(_ entries: [Entry], asks: [MeetingAskWire.Ask]) -> [RoomRow] {
        var waiting = placedPairs(asks, materialOnly: true)[...]
        guard !waiting.isEmpty else { return entries.map { .entry($0) } }
        var place: [String: Int] = [:]
        for (index, ask) in asks.enumerated() {
            if let id = ask.id?.lowercased(), !id.isEmpty, place[id] == nil { place[id] = index }
        }
        var out: [RoomRow] = []
        out.reserveCapacity(entries.count + waiting.count)
        /// The pairs asked before the ask at `limit` (all of them: nil).
        func flush(before limit: Int?) {
            while let next = waiting.first, limit.map({ next.place < $0 }) ?? true {
                out.append(.ask(next.pair))
                waiting.removeFirst()
            }
        }
        for entry in entries {
            switch entry {
            case .question(_, _, _, _, let ask?), .heard(_, _, _, _, let ask?):
                if let at = place[ask] { flush(before: at) }
            case .ended:
                flush(before: nil)
            default:
                break
            }
            out.append(.entry(entry))
        }
        flush(before: nil)
        return out
    }

    /// The pairs with their place in `asks`.
    private static func placedPairs(_ asks: [MeetingAskWire.Ask], materialOnly: Bool) -> [(place: Int, pair: AskPair)] {
        asks.enumerated().compactMap { index, ask in
            guard ask.state == "answered", let answer = ask.answer, !answer.isEmpty else { return nil }
            if materialOnly, ask.via != "material" { return nil }
            let id = ask.id?.lowercased() ?? ""
            return (place: index, pair: AskPair(id: "ask-" + (id.isEmpty ? String(index) : id), ask: id, question: ask.question, answer: answer))
        }
    }

    /// Whether a message sent with one of `clientMsgIDs` is in the transcript (any case: the hub lowercases ids). The
    /// newest items are looked at first, where a message just sent lands.
    static func delivered(_ items: [Item], clientMsgIDs: [String]) -> Bool {
        let wanted = Set(clientMsgIDs.map { $0.lowercased() }.filter { !$0.isEmpty })
        guard !wanted.isEmpty else { return false }
        return items.reversed().contains { item in item.clientMsgID.map { wanted.contains($0.lowercased()) } ?? false }
    }

    // MARK: - Reading items

    /// Whether a user item came from the meeting (message source `meeting`): `source.kind == "meeting"`, else the leading
    /// message-source block's JSON `kind`, which the manager persists and which the header and a heard question carry
    /// without a `source` (they have a `client_msg_id`, so `MessageSource.of` doesn't infer it from the text).
    static func fromMeeting(_ item: Item) -> Bool {
        if let kind = sourceKind(item) { return kind == "meeting" }
        return raws(item.text ?? "").meeting
    }

    /// The `[m:ss] text` lines of a context block, in order; anything else in it (a note of lines left out) is not one.
    static func lines(_ text: String) -> [Line] { analyzed(text).lines }

    /// Seconds from the start as the hub's meeting clock: `m:ss`, `h:mm:ss`.
    static func clock(_ seconds: Double) -> String {
        // Past what a recording can last (or not a number) the clock stops instead of trapping on the conversion.
        let s = seconds.isFinite ? Int(min(max(0, seconds), Double(Int32.max)).rounded(.down)) : 0
        let hours = s / 3600, minutes = s % 3600 / 60, rest = s % 60
        return hours > 0 ? "\(hours):\(pad(minutes)):\(pad(rest))" : "\(minutes):\(pad(rest))"
    }

    /// The ask id (lowercase) an attempt message belongs to: `ask-<uuid>-<k>` ("Asking the room").
    static func askID(_ clientMsgID: String?) -> String? {
        guard let clientMsgID, !clientMsgID.isEmpty else { return nil }
        return firstGroup(askMessage, clientMsgID.lowercased())
    }

    /// The photo id (lowercase) a room message is about: `photo-<uuid>`, the forwarded photo's image and its line. A
    /// withdrawal (`photo-withdrawn-<uuid>`) is not one.
    static func photoID(_ clientMsgID: String?) -> String? {
        guard let clientMsgID, !clientMsgID.isEmpty else { return nil }
        return firstGroup(photoMessage, clientMsgID.lowercased())
    }

    /// The photos the room thread holds, in its order (lowercase ids): the page draws the others after it.
    static func photoIDs(_ entries: [Entry]) -> [String] {
        entries.compactMap { entry in
            if case .photo(_, _, let photo) = entry { return photo }
            return nil
        }
    }

    /// The asks the room thread holds an attempt of (lowercase ids): those the page doesn't draw again from `GET …/live`.
    static func askIDs(_ entries: [Entry]) -> Set<String> {
        var ids = Set<String>()
        for entry in entries {
            switch entry {
            case .heard(_, _, _, _, let ask?), .question(_, _, _, _, let ask?): ids.insert(ask)
            default: break
            }
        }
        return ids
    }

    // MARK: - Private

    /// A reply's text, joined to the one before it when nothing shown came between (tool calls, thinking and folds show
    /// nothing); a photo the owner sent.
    private static func pushReply(_ out: inout [Entry], _ item: Item) {
        if item.role == "assistant", item.kind == .text {
            let text = (item.text ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { return }
            if case .answer(let id, let at, let before)? = out.last {
                out[out.count - 1] = .answer(id: id, at: at, text: before + "\n\n" + text)
            } else {
                out.append(.answer(id: item.id, at: item.ts, text: text))
            }
        } else if item.role == "user", item.kind == .image, item.toolUseID == nil {
            out.append(.image(id: item.id, at: item.ts, image: item.image))
        }
    }

    /// The hub's line telling the room a deleted photo must not be used again (`photo-withdrawn-<uuid>`).
    private static func withdrawn(_ clientMsgID: String?) -> Bool {
        clientMsgID?.lowercased().hasPrefix(withdrawnPrefix) == true
    }

    /// The item's `source.kind` when it has one (a string, not empty): it decides over anything in the text.
    private static func sourceKind(_ item: Item) -> String? {
        guard case .string(let kind)? = item.source?["kind"], !kind.isEmpty else { return nil }
        return kind
    }

    private static func pad(_ n: Int) -> String { n < 10 ? "0\(n)" : "\(n)" }

    /// What a user item's whole text says: its leading message-source block names the meeting, and it is an end batch
    /// (words after its last block, or no block at all).
    private nonisolated struct Raw: Sendable {
        var meeting: Bool
        var endBatch: Bool
    }

    /// What a context block's text says: the meeting header's (it says when it started and the role), and its live lines.
    private nonisolated struct Block: Sendable {
        var header: Bool
        var lines: [Line]
    }

    private static let rawMemo = TextMemo<Raw>(limit: 1024)
    private static let blockMemo = TextMemo<Block>(limit: 1024)

    private static func raws(_ text: String) -> Raw { rawMemo(text, scanRaw) }
    private static func analyzed(_ text: String) -> Block { blockMemo(text, scanBlock) }

    /// A message's first block, either generation's tag name (`HubNaming`); its attributes and its body.
    private static let leadingBlock = try! NSRegularExpression(
        pattern: #"^\s*<"# + HubNaming.contextPattern + #"\b([^>]*)>([\s\S]*?)</"# + HubNaming.contextPattern + ">")
    private static let sourceAttribute = try! NSRegularExpression(pattern: #"\bkind="message-source""#)
    private static let blockEnd = try! NSRegularExpression(pattern: "</" + HubNaming.contextPattern + ">")
    private static let liveLine = try! NSRegularExpression(pattern: #"^\[([0-9]{1,2}(?::[0-9]{2}){1,2})\]\s?(.*)$"#)
    private static let askMessage = try! NSRegularExpression(pattern: #"^ask-([0-9a-f-]{36})-[0-9]+$"#)
    private static let photoMessage = try! NSRegularExpression(pattern: #"^photo-([0-9a-f-]{36})$"#)
    private static let withdrawnPrefix = "photo-withdrawn-"
    // The hub's own words (prompts/meeting), matched as the web matches them; they are never shown.
    private static let heardTitle = try! NSRegularExpression(pattern: #"^What was just said\b"#)
    private static let titleClock = try! NSRegularExpression(pattern: #"\(([0-9:]+)\)\s*$"#)
    private static let startedLine = try! NSRegularExpression(pattern: "^Started: ", options: .anchorsMatchLines)
    private static let roleLine = try! NSRegularExpression(pattern: "^Role: ", options: .anchorsMatchLines)

    private static func scanRaw(_ text: String) -> Raw {
        let ns = text as NSString
        let whole = NSRange(location: 0, length: ns.length)
        var meeting = false
        if let lead = leadingBlock.firstMatch(in: text, range: whole),
           matches(sourceAttribute, ns.substring(with: lead.range(at: 1))) {
            let json = ns.substring(with: lead.range(at: 2)).trimmingCharacters(in: .whitespacesAndNewlines)
            if case .string("meeting")? = (try? JSONDecoder().decode(JSONValue.self, from: Data(json.utf8)))?["kind"] {
                meeting = true
            }
        }
        var endBatch = true
        if let last = blockEnd.matches(in: text, range: whole).last {
            let after = ns.substring(from: NSMaxRange(last.range))
            endBatch = !after.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
        return Raw(meeting: meeting, endBatch: endBatch)
    }

    private static func scanBlock(_ text: String) -> Block {
        var found: [Line] = []
        for raw in text.split(whereSeparator: \.isNewline) {
            let line = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            let ns = line as NSString
            guard let m = liveLine.firstMatch(in: line, range: NSRange(location: 0, length: ns.length)) else { continue }
            let words = ns.substring(with: m.range(at: 2)).trimmingCharacters(in: .whitespacesAndNewlines)
            if !words.isEmpty { found.append(Line(clock: ns.substring(with: m.range(at: 1)), text: words)) }
        }
        return Block(header: matches(startedLine, text) && matches(roleLine, text), lines: found)
    }

    private static func matches(_ re: NSRegularExpression, _ text: String) -> Bool {
        re.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil
    }

    /// The first capture group of `re`'s first match in `text`.
    private static func firstGroup(_ re: NSRegularExpression, _ text: String) -> String? {
        guard let m = re.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)),
              let r = Range(m.range(at: 1), in: text) else { return nil }
        return String(text[r])
    }
}

nonisolated extension MeetingThread.AskInfo {
    /// An ask of `GET …/live`: its id lowercased, spoken by its `question_source`, and its `at`.
    init(_ ask: MeetingAskWire.Ask) {
        self.init(id: ask.id?.lowercased() ?? "", spoken: ask.questionSource == "spoken", at: ask.at)
    }
}
#endif
