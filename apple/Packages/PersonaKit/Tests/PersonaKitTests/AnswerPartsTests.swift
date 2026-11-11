import CoreGraphics
import Testing
@testable import PersonaKit

/// A long finished answer cut into rows (`AnswerParts`, #4970): cut only between plain blocks, never inside a fence, a
/// list, display math or an HTML comment, with the gap MarkdownUI's reply theme puts between the two blocks.
struct AnswerPartsTests {
    /// About `n` lines of paragraphs, each a block of its own.
    private func paragraphs(_ n: Int, word: String = "段落") -> [String] {
        (0..<n).map { "\(word) \($0)：迁移方案需要先确认现有数据的边界，再决定哪些字段保留。" }
    }

    private func joined(_ blocks: [String]) -> String { blocks.joined(separator: "\n\n") }

    @Test func aShortAnswerStaysWhole() {
        #expect(AnswerParts.split(joined(paragraphs(20))).isEmpty)
        #expect(AnswerParts.split("").isEmpty)
    }

    @Test func aLongAnswerIsCutIntoPartsThatMakeItUpAgain() {
        let text = joined(paragraphs(150))
        let parts = AnswerParts.split(text)
        #expect(parts.count > 4)
        #expect(parts.map(\.text).joined(separator: "\n\n") == text)
        #expect(parts.first?.gap == 0)
        // Paragraph after paragraph: the paragraph's bottom margin (10), the next one's top (0).
        #expect(parts.dropFirst().allSatisfy { $0.gap == 10 })
        // Each part but the last holds at least the target.
        for part in parts.dropLast() {
            let lines = part.text.components(separatedBy: "\n").count
            #expect(lines >= AnswerParts.targetLines || part.text.utf8.count >= AnswerParts.targetBytes)
        }
    }

    @Test func gapsFollowTheThemeMargins() {
        typealias K = AnswerParts.Kind
        #expect(max(K.paragraph.bottom, K.heading(2).top) == 14)
        #expect(max(K.code.bottom, K.paragraph.top) == 12)
        #expect(max(K.table.bottom, K.heading(1).top) == 16)
        #expect(max(K.heading(3).bottom, K.paragraph.top) == 4)
        #expect(max(K.paragraph.bottom, K.heading(4).top) == 12)
        // Before a heading: cut there, and the gap is the larger of the paragraph's bottom and the heading's top.
        var blocks: [String] = []
        for n in 0..<12 { blocks += ["## 第 \(n) 部分"] + paragraphs(8) }
        let parts = AnswerParts.split(joined(blocks))
        #expect(parts.count > 1)
        for part in parts.dropFirst() {
            let first = part.text.components(separatedBy: "\n")[0]
            #expect(part.gap == (first.hasPrefix("## ") ? 14 : first.hasPrefix("段落") ? 10 : -1))
        }
    }

    @Test func neverCutsInsideACodeFence() {
        let code = "```text\n" + (0..<60).map { "line \($0)\n" }.joined(separator: "\n") + "```"
        let text = joined(paragraphs(30) + [code] + paragraphs(30))
        let parts = AnswerParts.split(text)
        #expect(parts.count > 1)
        #expect(parts.contains { $0.text.contains(code) })
        #expect(parts.map(\.text).joined(separator: "\n\n") == text)
        // After the code block: its bottom margin.
        if let after = parts.firstIndex(where: { $0.text.hasPrefix(paragraphs(30)[0]) == false && $0.text.hasPrefix("段落") }),
           parts[after - 1].text.hasSuffix("```") {
            #expect(parts[after].gap == 12)
        }
    }

    @Test func neverCutsALooseListOrNextToOne() {
        let list = (0..<80).map { "- 第 \($0) 项：先在测试环境跑一遍" }.joined(separator: "\n\n")
        let text = joined(paragraphs(40) + [list] + paragraphs(40))
        let parts = AnswerParts.split(text)
        #expect(parts.count > 1)
        #expect(parts.contains { $0.text.contains(list) })
        // The list is not the first or last thing of a part with anything else next to it: no cut touches it.
        for part in parts {
            let lines = part.text.components(separatedBy: "\n").filter { !$0.isEmpty }
            #expect(!(lines.first?.hasPrefix("- ") ?? false))
            #expect(!(lines.last?.hasPrefix("- ") ?? false))
        }
    }

    @Test func neverCutsNextToAQuoteOrAnIndentedContinuation() {
        let text = joined(paragraphs(40) + ["> 引用", "1. 第一项", "   接着第一项", "后面一段"] + paragraphs(40))
        for part in AnswerParts.split(text) {
            #expect(!part.text.hasPrefix(">"))
            #expect(!part.text.hasPrefix("   "))
            #expect(!part.text.hasPrefix("1. "))
            #expect(!part.text.hasSuffix("> 引用"))
            #expect(!part.text.hasSuffix("第一项"))
        }
    }

    @Test func neverCutsInsideDisplayMathOrAnHTMLComment() {
        let math = "$$\n" + paragraphs(30).joined(separator: "\n\n") + "\n$$"
        let comment = "<!--\n" + paragraphs(30).joined(separator: "\n\n") + "\n-->"
        let bracket = "\\[\n" + paragraphs(30).joined(separator: "\n\n") + "\n\\]"
        for block in [math, comment, bracket] {
            let parts = AnswerParts.split(joined(paragraphs(40) + [block] + paragraphs(40, word: "后文")))
            #expect(parts.contains { $0.text.contains(block) })
        }
    }

    @Test func tablesAreCutAroundWhole() {
        let table = "| 列 1 | 列 2 |\n|---|---|\n" + (0..<40).map { "| a\($0) | b\($0) |" }.joined(separator: "\n")
        let text = joined(paragraphs(30) + [table, "## 下一节"] + paragraphs(30))
        let parts = AnswerParts.split(text)
        #expect(parts.contains { $0.text.contains(table) })
        if let n = parts.firstIndex(where: { $0.text.hasPrefix("## 下一节") }), parts[n - 1].text.hasSuffix("| a39 | b39 |") {
            #expect(parts[n].gap == 14) // max(table bottom 12, heading 2 top 14)
        }
        #expect(AnswerParts.kind(ArraySlice(table.components(separatedBy: "\n"))) == .table)
        // Not a table: the delimiter row has another number of cells.
        #expect(AnswerParts.kind(["| a | b |", "|---|", "| c | d |"]) == nil)
    }

    @Test func linkDefinitionsGoWithEveryPart() {
        let text = joined(paragraphs(150) + ["看 [文档][doc]。", "[doc]: https://example.com/doc"])
        let parts = AnswerParts.split(text)
        #expect(parts.count > 1)
        #expect(parts.allSatisfy { $0.text.hasSuffix("\n\n[doc]: https://example.com/doc") })
        // A definition over two lines: the answer stays whole rather than lose it.
        #expect(AnswerParts.split(joined(paragraphs(150) + ["[doc]:\n  https://example.com/doc"])).isEmpty)
    }

    @Test func plainBlocksAreTold() {
        #expect(AnswerParts.kind(["## 标题"]) == .heading(2))
        #expect(AnswerParts.kind(["#### 备注"]) == .heading(4))
        #expect(AnswerParts.kind(["#标签"]) == .paragraph)
        #expect(AnswerParts.kind(["```swift", "let a = 1", "```"]) == .code)
        #expect(AnswerParts.kind(["```swift", "let a = 1", "```", "后面一句"]) == nil)
        #expect(AnswerParts.kind(["一段", "接着一行"]) == .paragraph)
        #expect(AnswerParts.kind(["标题", "---"]) == nil)       // setext heading
        #expect(AnswerParts.kind(["---"]) == nil)               // thematic break
        #expect(AnswerParts.kind(["一段", "- 一项"]) == nil)      // a list under it
        #expect(AnswerParts.kind(["价格是 $5"]) == nil)           // math, maybe
        #expect(AnswerParts.kind(["![图](a.png)"]) == nil)
        #expect(AnswerParts.kind(["  缩进"]) == nil)
    }
}
