import MarkdownUI
import PersonaKit
import SwiftUI

/// A long finished answer's parts (#4970, `AnswerParts`), worked out once per text: the chat's lines ask on every
/// rebuild, and the rows' memos are prewarmed with them off the main thread (`RowMemo.prewarm`).
nonisolated enum AnswerPartMemo {
    private static let memo = TextMemo<[AnswerParts.Part]>(limit: 64)

    /// Below this many bytes an answer is never cut (`AnswerParts.minimumBytes` and `minimumLines` decide above it);
    /// it saves hashing every answer's text.
    static let floor = 2_000

    /// DEBUG `-showAnswerPartLines <n>`: parts of about n lines (and n × 125 bytes), to compare sizes in one build.
    private static let target: (lines: Int, bytes: Int) = {
        #if DEBUG
        let n = UserDefaults.standard.integer(forKey: "showAnswerPartLines")
        if n > 0 { return (n, n * 125) }
        #endif
        return (AnswerParts.targetLines, AnswerParts.targetBytes)
    }()

    /// DEBUG `-showAnswerWhole YES`: every answer one row, as before parts, to measure against in the same build.
    private static let whole: Bool = {
        #if DEBUG
        UserDefaults.standard.bool(forKey: "showAnswerWhole")
        #else
        false
        #endif
    }()

    static func parts(_ text: String) -> [AnswerParts.Part] {
        guard !whole, text.utf8.count >= floor else { return [] }
        return memo(text) { AnswerParts.split($0, lines: target.lines, bytes: target.bytes) }
    }

    /// The row memo key of part `index` of the message whose key is `message` (`RowMemo.key`).
    static func key(_ message: String, _ index: Int) -> String { message + "\u{1}part\(index)" }
}

extension ChatLines {
    /// The chat's long finished answers as their parts (`AnswerParts`), each a line of its own; everything else as it is.
    static func splitAnswers(_ lines: [ChatLine]) -> [ChatLine] {
        var out: [ChatLine]?
        for (n, line) in lines.enumerated() {
            guard case .row(.assistant(let item)) = line.content else {
                out?.append(line)
                continue
            }
            let parts = AnswerPartMemo.parts(item.text ?? "")
            guard parts.count > 1 else {
                out?.append(line)
                continue
            }
            if out == nil { out = Array(lines[..<n]) }
            for (index, part) in parts.enumerated() {
                out?.append(.answerPart(item: item, part: part, index: index, count: parts.count))
            }
        }
        return out ?? lines
    }
}

/// A part of a long finished answer (#4970), a row of the chat list: drawn as `AssistantText` draws a whole answer,
/// one part at a time, `AnswerParts.Part.gap` under the part before it, as MarkdownUI would space those two blocks.
/// A long press on any part lifts that part with the whole answer's actions (Copy, Quote and Share take all of it);
/// the files and ports the answer names sit under its last part.
struct AssistantTextPart: View, Equatable {
    var item: Item
    var part: AnswerParts.Part
    var index: Int
    var count: Int

    nonisolated static func == (a: Self, b: Self) -> Bool {
        a.item == b.item && a.part == b.part && a.index == b.index && a.count == b.count
    }

    @Environment(\.openFile) private var openFile
    @Environment(\.rowMemoScope) private var memoScope

    private var last: Bool { index == count - 1 }

    private func words(_ text: AssistantRowText, key: String, message: String) -> some View {
        LinkedMarkdown(plain: text.markdown, paths: text.paths, key: key)
            .markdownTheme(.persona)
            .environment(\.replyKey, message) // its quotes' folds, the lifted copy's too (as `AssistantText`)
            .selectableInLift(part.text, as: .reply(quiet: false))
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantTextPart")
        #endif
        let message = RowMemo.key(memoScope, item.id)
        let key = AnswerPartMemo.key(message, index)
        let text = RowMemo.assistant(key) { _ in AssistantRowText(part.text) } // prewarmed with the message
        let whole = last ? RowMemo.assistant(message) { _ in AssistantRowText(item.text ?? "") } : nil
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            words(text, key: key, message: message)
                .contentShape(.rect)
                .messageLongPress(id: item.id, card: true) { words(text, key: key, message: message) }
            if let whole {
                if let openFile {
                    // Documents as cards with their first page (#1543), the rest as chips (`AssistantText`).
                    FileChips(paths: whole.chips, open: openFile)
                    DocumentCards(paths: whole.files.filter(DocumentCards.isDocument), open: openFile)
                }
                PortChips(text: item.text ?? "")
            }
        }
        .markdownSoftBreakMode(.lineBreak)
        .lookUpFiles(whole.map { PathRefs.unique(text.asked + $0.chips, limit: 40) } ?? text.asked)
        .task(id: item.id) { if last { await AssistantText.preloadLinks(item) } }
        .messageActionBar(id: item.id, text: item.text ?? "", isUser: false, date: item.ts)
        // The list puts its own spacing between rows; the part sits where the answer's next block would.
        .padding(.top, index == 0 ? 0 : part.gap - Theme.Space.l)
    }
}
