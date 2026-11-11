import CoreGraphics
import Foundation

/// A long finished answer cut into parts, each drawn as its own row of the chat list (#4970). One `Markdown` view for
/// a whole 30,000-character report was one row many screens tall: MarkdownUI laid out every block of it at once, twice
/// (its block spacing arrives as state after the first pass), and every table's decoration ran again on each scrolled
/// frame. As parts, only what is near the screen is laid out and drawn.
///
/// The answer must look the same. MarkdownUI puts `max(bottom margin of a block, top margin of the next)` between two
/// blocks, and a container (a list, a quote) takes the largest margins of what is inside it, so its margins depend on its
/// contents. A cut is therefore made only between two plain blocks whose margins the reply theme fixes (a paragraph, an
/// ATX heading, a fenced code block, a table; `MarkdownUI.Theme.persona`, `ReplyMetrics`), and the part after it is
/// drawn that gap below the one before (`Part.gap`). Lists, quotes, setext headings, thematic breaks, HTML, display
/// math, link definitions and indented lines are never at a cut, so a list is never cut between its items.
public nonisolated enum AnswerParts {
    public struct Part: Equatable, Hashable, Sendable {
        /// Its Markdown, with the answer's link definitions after it when it has any (they draw nothing).
        public let text: String
        /// The space MarkdownUI's theme puts between the previous part's last block and this part's first; 0 for the first.
        public let gap: CGFloat
    }

    /// Answers smaller than this many UTF-8 bytes or lines stay one row.
    public static let minimumBytes = 6_000
    public static let minimumLines = 80
    /// A part ends at the first cut after it holds this many lines or bytes (about a screen of text).
    public static let targetLines = 24
    public static let targetBytes = 3_000

    /// The block kinds a cut can be next to, with the reply theme's margins.
    enum Kind: Equatable {
        case paragraph, heading(Int), code, table

        var top: CGFloat {
            switch self {
            case .paragraph: 0
            case .heading(1): 16
            case .heading(2): 14
            case .heading: 12 // the third level, and the fourth to sixth (Theme.Space.m)
            case .code, .table: 4
            }
        }

        var bottom: CGFloat {
            switch self {
            case .paragraph: 10
            case .heading(1): 8
            case .heading(2): 6
            case .heading: 4 // the third level, and the fourth to sixth (Theme.Space.xs)
            case .code, .table: 12
            }
        }
    }

    /// The parts of `text`, or nothing when it stays whole (short, or no place to cut). A part ends at the first cut
    /// after it holds `lines` lines or `bytes` bytes.
    public static func split(_ text: String, lines target: Int = targetLines, bytes targetSize: Int = targetBytes) -> [Part] {
        let lines = text.components(separatedBy: "\n")
        guard text.utf8.count >= minimumBytes || lines.count >= minimumLines else { return [] }
        let chunks = Self.chunks(lines)
        guard chunks.count > 1, let definitions = Self.definitions(lines, chunks) else { return [] }
        var parts: [Part] = []
        var start = 0, gap: CGFloat = 0, size = (lines: 0, bytes: 0)
        for (n, chunk) in chunks.enumerated() {
            size.lines += chunk.range.count
            size.bytes += chunk.bytes
            guard n + 1 < chunks.count, size.lines >= target || size.bytes >= targetSize,
                  let before = chunk.kind, let after = chunks[n + 1].kind, chunks[n + 1].cuttable else { continue }
            parts.append(Part(text: Self.text(lines, chunks[start].range.lowerBound..<chunk.range.upperBound) + definitions, gap: gap))
            gap = max(before.bottom, after.top)
            start = n + 1
            size = (0, 0)
        }
        parts.append(Part(text: Self.text(lines, chunks[start].range.lowerBound..<lines.count) + definitions, gap: gap))
        return parts.count > 1 ? parts : []
    }

    /// A run of lines between blank lines outside code fences.
    struct Chunk {
        var range: Range<Int>
        var bytes: Int
        /// What it is when it is one plain block a cut can be next to; nil otherwise.
        var kind: Kind?
        /// Whether a cut may be made before it: it starts a block of its own (not a list's continuation, not inside
        /// display math or an HTML comment that started earlier).
        var cuttable: Bool
    }

    static func chunks(_ lines: [String]) -> [Chunk] {
        var out: [Chunk] = []
        var fence: MarkdownFence?
        var start: Int?
        var inMath = false, inComment = false
        var mathAtStart = false, commentAtStart = false
        func close(_ end: Int) {
            guard let s = start else { return }
            let range = s..<end
            out.append(Chunk(range: range, bytes: lines[range].reduce(0) { $0 + $1.utf8.count + 1 },
                             kind: kind(lines[range]), cuttable: !mathAtStart && !commentAtStart))
            start = nil
        }
        for (i, line) in lines.enumerated() {
            let t = line.trimmingCharacters(in: .whitespaces)
            if let f = fence {
                if f.isClosed(by: t) { fence = nil }
                continue
            }
            if t.isEmpty {
                close(i)
                continue
            }
            if start == nil {
                start = i
                mathAtStart = inMath
                commentAtStart = inComment
            }
            if let f = MarkdownFence.opening(t) {
                fence = f
                continue
            }
            // Display math (`MathMarkdown`) and HTML comments can hold blank lines: never cut inside one.
            if line.contains("$$"), line.components(separatedBy: "$$").count.isMultiple(of: 2) { inMath.toggle() }
            if t == "\\[" { inMath = true } else if t == "\\]" { inMath = false }
            if let open = line.range(of: "<!--", options: .backwards) {
                inComment = line.range(of: "-->", range: open.upperBound..<line.endIndex) == nil
            } else if inComment, line.contains("-->") {
                inComment = false
            }
        }
        close(lines.count)
        return out
    }

    /// What a chunk is when it is one plain block whose margins the theme fixes, else nil.
    static func kind(_ chunk: ArraySlice<String>) -> Kind? {
        guard let first = chunk.first, !first.hasPrefix(" "), !first.hasPrefix("\t") else { return nil }
        if let f = MarkdownFence.opening(first) {
            // The whole chunk is the block: closed on its last line (or still open at the end of the answer).
            let body = chunk.dropFirst()
            guard let end = body.firstIndex(where: { f.isClosed(by: $0.trimmingCharacters(in: .whitespaces)) }) else {
                return .code
            }
            return end == chunk.index(before: chunk.endIndex) ? .code : nil
        }
        if let level = atxLevel(first) { return chunk.count == 1 ? .heading(level) : nil }
        if first.hasPrefix("|") {
            let rows = Array(chunk)
            guard rows.count >= 2, isDelimiterRow(rows[1]), cells(rows[0]) == cells(rows[1]),
                  rows.dropFirst(2).allSatisfy({ $0.hasPrefix("|") }) else { return nil }
            return .table
        }
        for (n, line) in chunk.enumerated() where startsOtherBlock(line, continuation: n > 0) { return nil }
        return .paragraph
    }

    /// The level of an ATX heading line ("## Title"), nil when it is not one.
    static func atxLevel(_ line: String) -> Int? {
        let hashes = line.prefix(while: { $0 == "#" }).count
        guard (1...6).contains(hashes) else { return nil }
        let rest = line.dropFirst(hashes)
        return rest.isEmpty || rest.first == " " || rest.first == "\t" ? hashes : nil
    }

    /// Whether a line of a would-be paragraph makes it something else: a list, a quote, HTML, a heading, a table, math,
    /// a picture or a formula, a thematic break, a link definition, or (after the first line) a setext underline.
    static func startsOtherBlock(_ line: String, continuation: Bool) -> Bool {
        let t = line.trimmingCharacters(in: .whitespaces)
        if t.hasPrefix(">") || t.hasPrefix("<") || t.hasPrefix("|") || line.contains("![") { return true }
        // Math becomes a fence or a picture (`MathMarkdown`): a line that is one formula is drawn as a block.
        if line.contains("$") || line.contains("\\(") || line.contains("\\[") { return true }
        if MarkdownFence.opening(t) != nil || atxLevel(t) != nil || isDelimiterRow(t) { return true }
        if Pattern.listItem.matches(t) || Pattern.thematicBreak.matches(t) || Pattern.definitionStart.matches(t) { return true }
        if continuation, Pattern.setextUnderline.matches(t) { return true }
        return false
    }

    /// Compiled once: a split runs the line checks on every line of an answer.
    enum Pattern {
        static let listItem = Pattern.make(#"^([-*+]|\d{1,9}[.)])(\s|$)"#)
        static let thematicBreak = Pattern.make(#"^([-*_])(\s*\1){2,}$"#)
        static let definitionStart = Pattern.make(#"^\[[^\]]+\]:"#)
        static let definition = Pattern.make(#"^\[[^\]]+\]:\s*\S+(\s+("[^"]*"|'[^']*'|\([^)]*\)))?$"#)
        static let setextUnderline = Pattern.make(#"^(=+|-+)$"#)
        static let delimiterRow = Pattern.make(#"^\|?\s*:?-+:?\s*(\|\s*:?-+:?\s*)*\|?$"#)

        private static func make(_ p: String) -> NSRegularExpression { try! NSRegularExpression(pattern: p) }
    }

    static func isDelimiterRow(_ line: String) -> Bool {
        line.contains("-") && Pattern.delimiterRow.matches(line.trimmingCharacters(in: .whitespaces))
            && (line.contains("|") || line.contains(":"))
    }

    /// The number of cells in a table row (pipes at its ends do not count; escaped pipes are text).
    static func cells(_ line: String) -> Int {
        var t = line.trimmingCharacters(in: .whitespaces).replacingOccurrences(of: "\\|", with: "")
        if t.hasPrefix("|") { t.removeFirst() }
        if t.hasSuffix("|") { t.removeLast() }
        return t.split(separator: "|", omittingEmptySubsequences: false).count
    }

    /// The answer's link definitions, to go after every part so references still resolve; "" when it has none, nil when
    /// one is not on a line of its own (the answer then stays whole).
    /// Only lines that start a chunk (and the ones right under them) are definitions: inside a paragraph "[x]: y" is text.
    static func definitions(_ lines: [String], _ chunks: [Chunk]) -> String? {
        var found: [String] = []
        for chunk in chunks {
            for line in lines[chunk.range] {
                let t = line.trimmingCharacters(in: .whitespaces)
                guard !line.hasPrefix("    "), Pattern.definitionStart.matches(t) else { break }
                guard Pattern.definition.matches(t) else {
                    return nil
                }
                found.append(t)
            }
        }
        return found.isEmpty ? "" : "\n\n" + found.joined(separator: "\n")
    }

    static func text(_ lines: [String], _ range: Range<Int>) -> String {
        var part = lines[range]
        while let last = part.last, last.trimmingCharacters(in: .whitespaces).isEmpty { part.removeLast() }
        return part.joined(separator: "\n")
    }
}

private extension NSRegularExpression {
    nonisolated func matches(_ s: String) -> Bool {
        firstMatch(in: s, range: NSRange(s.startIndex..., in: s)) != nil
    }
}
