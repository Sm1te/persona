import Foundation
import PersonaKit

/// What a chat last went through, in its structure only (#3783): the batches that came in (how many, which lines, what
/// the store did with them), when the streamed text started and was cleared, and how the end of the chat was drawn
/// after each change (its last lines by kind and line number, held back or not). Never a word of a message. A reply
/// that showed while it streamed and was gone until the chat was opened again could not be told apart afterwards —
/// filed into a summary, held back, or dropped; Copy Diagnostics (the assistant's menu) copies this.
/// Kept on disk (a few chats, the newest lines of each), so it outlives the app being closed.
@MainActor
final class ChatTrace {
    static let shared = ChatTrace()
    static let limit = 240
    private static let chats = 4
    private static let file = "chat-trace"

    private var trace: [String: [String]]
    private var order: [String]
    private let saver = Debouncer(.seconds(2))
    private let clock: () -> Date

    init(load: Bool = true, clock: @escaping () -> Date = { .now }) {
        let saved = load ? StateCache.load(Self.file, as: Saved.self) : nil
        trace = saved?.trace ?? [:]
        order = saved?.order ?? []
        self.clock = clock
    }

    nonisolated private struct Saved: Codable, Sendable {
        var trace: [String: [String]]
        var order: [String]
    }

    /// Adds a line for `key`'s chat (one the same as the last is not repeated).
    func note(_ key: SessionKey, _ line: String) {
        let path = key.path
        var lines = trace[path] ?? []
        if let last = lines.last, last.hasSuffix(" " + line) { return }
        lines.append(Self.stamp(clock()) + " " + line)
        if lines.count > Self.limit { lines.removeFirst(lines.count - Self.limit) }
        trace[path] = lines
        order.removeAll { $0 == path }
        order.append(path)
        while order.count > Self.chats { trace[order.removeFirst()] = nil }
        let saved = Saved(trace: trace, order: order)
        saver.run { StateCache.save(Self.file, saved) }
    }

    /// The trace of `key`'s chat, oldest first.
    func lines(_ key: SessionKey) -> [String] { trace[key.path] ?? [] }

    /// What Copy Diagnostics copies.
    func text(_ key: SessionKey) -> String {
        let lines = lines(key)
        return (["Persona 对话诊断（只有结构，没有内容）", "行 \(lines.count)"] + lines).joined(separator: "\n") // i18n-ignore: diagnostics for a bug report
    }

    private static func stamp(_ date: Date) -> String {
        date.formatted(.dateTime.hour(.twoDigits(amPM: .omitted)).minute(.twoDigits).second(.twoDigits).secondFraction(.fractional(3)))
    }

    // MARK: What a line says

    /// A batch the store took in: how many items, their lines, the span it said it covers and what it left to do.
    static func batch(_ items: [Item], span: ItemSpan, backlog: Bool, gap: ChatStore.Gap, resets: Int, covered: Int) -> String {
        let seqs = items.map(\.seq)
        let range = seqs.min().map { lo in "\(lo)-\(seqs.max() ?? lo)" } ?? "-"
        let kinds = Dictionary(grouping: items, by: { kindCode($0) }).map { "\($0.key)\($0.value.count)" }.sorted().joined(separator: ",")
        return "items n=\(items.count) seq=\(range) [\(kinds)] span=\(span.from.map(String.init) ?? "end")..\(span.to.map(String.init) ?? "?")"
            + (backlog ? " backlog" : "") + " gap=\(gap) covered=\(covered) resets=\(resets)"
    }

    /// The end of the chat as drawn: its last lines, newest last, with what the view had then.
    static func screen(_ lines: [ChatLine], live: Int, streaming: Bool, running: Bool, frozen: Bool, withheld: Int) -> String {
        "lines \(lines.count) …" + lines.suffix(5).map { " " + code($0) }.joined()
            + " | live=\(live) streaming=\(streaming ? 1 : 0) running=\(running ? 1 : 0) frozen=\(frozen ? 1 : 0) withheld=\(withheld)"
    }

    /// One line of the chat, by kind and transcript line: `a17` an answer, `u10` a message, `work(3,live)`.
    static func code(_ line: ChatLine) -> String {
        switch line.content {
        case .supervision(_, let trace, let live, let source): "\(source == nil ? "supervision" : "background")(\(trace.count)\(live ? ",live" : ""))"
        case .supervisionInput(let item): "supervision-input\(item.seq)"
        case .row(let r):
            switch r {
            case .user(let i): "u\(i.seq)"
            case .assistant(let i): "a\(i.seq)"
            case .thinking(let i): "th\(i.seq)"
            case .tools(let steps): "tools\(steps.count)"
            case .notice(let i): (i.wake == true ? "wake" : "n") + "\(i.seq)"
            case .image(let i): "img\(i.seq)"
            case .recap(let i): "recap\(i.seq)"
            }
        case .work(_, let items, let live, _, _): "work(\(items.count)\(live ? ",live" : ""))"
        case .userImages(_, let items): "photos\(items.count)"
        case .recaps(_, let items): "recaps\(items.count)"
        case .workStep: "step"
        case .agentOut: "agent"
        case .agents(_, let calls, _, _): "agents\(calls.count)"
        case .toolRun(_, let calls, _, _): "run\(calls.count)"
        case .workEnd: "end"
        case .answerPart(let item, _, let index, _): "a\(item.seq).\(index)"
        case .apiError(let item, _, let count): "err\(item.seq)x\(count)"
        case .feed(let f): "feed:\(f.kind.rawValue)"
        case .quick: "quick"
        case .day: "day"
        case .viewed(_, let images): "viewed\(images.count)"
        }
    }

    private static func seqOf(_ row: ChatRow) -> String {
        switch row {
        case .user(let i), .assistant(let i), .thinking(let i), .notice(let i), .image(let i), .recap(let i): "\(i.seq)"
        case .tools: "tools"
        }
    }

    private static func kindCode(_ item: Item) -> String {
        switch item.kind {
        case .text: item.role == "user" ? "u" : "a"
        case .notice: item.wake == true ? "wake" : "n"
        default: item.kind.rawValue
        }
    }
}
