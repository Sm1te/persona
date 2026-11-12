import Testing
import UIKit
@testable import Persona

/// A lifted message's words selected in place (#603, `ReplyText`): a reply reads as the chat shows it, without the
/// Markdown marks, and is laid out with the chat's spacing, so a part copied from it is clean; your own message,
/// formulas and code stay exactly as written.
@MainActor
struct ReplyTextTests {
    private let look = ReplyText.Look(.reply(quiet: false), traits: UITraitCollection(preferredContentSizeCategory: .large))

    private func reply(_ markdown: String) -> NSAttributedString { ReplyText.attributed(markdown, look: look, width: 360) }
    private func shown(_ markdown: String) -> String { reply(markdown).string }

    private func style(_ s: NSAttributedString, at word: String) -> NSParagraphStyle? {
        s.attribute(.paragraphStyle, at: (s.string as NSString).range(of: word).location, effectiveRange: nil) as? NSParagraphStyle
    }

    @Test func inlineMarksGoTheirLookStays() {
        let s = reply("今天有 **3 件事**，跑 `swift test`，看 [PR](https://example.com/pr/42)。")
        #expect(s.string == "今天有 3 件事，跑 swift test，看 PR。")
        // Semibold, as the chat's strong text.
        let bold = s.attribute(.font, at: 4, effectiveRange: nil) as? UIFont
        let traits = bold?.fontDescriptor.object(forKey: .traits) as? [UIFontDescriptor.TraitKey: Any]
        #expect((traits?[.weight] as? CGFloat ?? 0) >= UIFont.Weight.semibold.rawValue)
        let code = (s.string as NSString).range(of: "swift test")
        #expect(s.attribute(.backgroundColor, at: code.location, effectiveRange: nil) != nil)
        // A link keeps its color and is not followed: a tap on the words is about the selection.
        let link = (s.string as NSString).range(of: "PR")
        #expect(s.attribute(.link, at: link.location, effectiveRange: nil) == nil)
        #expect(s.attribute(.foregroundColor, at: link.location, effectiveRange: nil) as? UIColor == look.link)
    }

    /// Outside a fence `a*b*c` would be emphasis (`abc`); inside it is code, kept as written, monospaced, on the code
    /// background (`ReplyBlock`).
    @Test func fencedCodeLosesItsFenceKeepsItsLines() {
        #expect(shown("a*b*c") == "abc")
        let s = reply("新的写法：\n\n```swift\nlet x = a*b*c\n  __init__()\n```\n\n完了")
        #expect(s.string == "新的写法：\n\nlet x = a*b*c\n  __init__()\n\n完了")
        let at = (s.string as NSString).range(of: "a*b*c").location
        let font = s.attribute(.font, at: at, effectiveRange: nil) as? UIFont
        #expect(font?.fontDescriptor.symbolicTraits.contains(.traitMonoSpace) == true)
        let block = s.attribute(.replyBlock, at: at, effectiveRange: nil) as? ReplyBlock
        #expect(block?.kind == .code)
        // Both lines are one block: one background behind them.
        let next = (s.string as NSString).range(of: "__init__").location
        #expect(s.attribute(.replyBlock, at: next, effectiveRange: nil) as? ReplyBlock === block)
    }

    /// A shorter or other fence inside a fence is a line of it: ````md holding ```swift … ``` closes on ```` only.
    @Test func aFenceClosesOnlyOnItsOwnKind() {
        #expect(shown("看：\n````md\n```swift\nlet a = 1\n```\n还在里面\n````\n外面") == "看：\n```swift\nlet a = 1\n```\n还在里面\n外面")
        #expect(shown("~~~\n```\n~~~~\n后面") == "```\n后面")
        // An info string after a closing-looking fence: still inside.
        #expect(shown("```\na\n```js\nb\n```") == "a\n```js\nb")
    }

    @Test func formulasStayAsWritten() {
        #expect(shown("面积 $x*y*z$，还有 \\(a*b*c\\) 和 *重点*") == "面积 $x*y*z$，还有 \\(a*b*c\\) 和 重点")
        #expect(shown("$$\nE = m*c*c\n$$\n**完**") == "$$\nE = m*c*c\n$$\n完")
        #expect(shown("一行的 $$a*b*c$$ 公式") == "一行的 $$a*b*c$$ 公式")
        #expect(shown("跑 `echo $HOME*` 看看") == "跑 echo $HOME* 看看") // `$` in a code span is code
    }

    @Test func indentedCodeIsCode() {
        let s = reply("例子：\n\n    let x = a*b*c\n    __init__()\n\n完")
        #expect(s.string == "例子：\n\nlet x = a*b*c\n__init__()\n\n完")
        #expect((s.attribute(.replyBlock, at: (s.string as NSString).range(of: "let x").location, effectiveRange: nil) as? ReplyBlock)?.kind == .code)
        // In a list, four spaces in is the item's next level, not code.
        #expect(shown("- 一\n\n    - 二") == "•\t一\n\n◦\t二")
    }

    @Test func headingsListsQuotesAndRules() {
        #expect(shown("## 结论\n- 一\n  * 二\n1. **四**") == "结论\n•\t一\n◦\t二\n1.\t四")
        // A rule keeps its marks, drawn as a line (`ReplyBlock`), not as text.
        let rule = reply("上面\n\n---\n\n下面")
        #expect(rule.string == "上面\n\n---\n\n下面")
        let at = (rule.string as NSString).range(of: "---").location
        #expect((rule.attribute(.replyBlock, at: at, effectiveRange: nil) as? ReplyBlock)?.kind == .rule)
        #expect(rule.attribute(.foregroundColor, at: at, effectiveRange: nil) as? UIColor == .clear)
        let quote = reply("> 旧缓存要等一天\n> 才过期")
        #expect(quote.string == "旧缓存要等一天\n才过期")
        #expect((quote.attribute(.replyBlock, at: 0, effectiveRange: nil) as? ReplyBlock)?.kind == .quote)
        // The first heading level is larger.
        let h1 = reply("# 大\n## 小")
        let big = (h1.attribute(.font, at: 0, effectiveRange: nil) as? UIFont)?.pointSize ?? 0
        #expect(big > look.size * 1.2)
    }

    /// H4–H6 as the chat draws them (`MarkdownUI.Theme.minorHeading`, #3863): a step smaller than the text, semibold, in
    /// the quieter ink, with more space above than below; the third level stays at the text's size in its color.
    @Test func lowerHeadingsAreSmallerAndQuieter() {
        let s = reply("正文\n\n#### 补充说明\n\n更多\n\n###### 末级\n\n### 三级")
        for heading in ["补充说明", "末级"] {
            let at = (s.string as NSString).range(of: heading).location
            let font = s.attribute(.font, at: at, effectiveRange: nil) as? UIFont
            #expect(abs((font?.pointSize ?? 0) - look.size * ReplyMetrics.heading4.size) < 0.01, "\(heading)")
            let traits = font?.fontDescriptor.object(forKey: .traits) as? [UIFontDescriptor.TraitKey: Any]
            #expect((traits?[.weight] as? CGFloat ?? 0) >= UIFont.Weight.semibold.rawValue, "\(heading)")
            #expect(s.attribute(.foregroundColor, at: at, effectiveRange: nil) as? UIColor === look.secondaryInk, "\(heading)")
        }
        #expect(ReplyMetrics.heading4.size < 1 && ReplyMetrics.heading4.top > ReplyMetrics.heading4.bottom)
        let lines = look.size * ReplyMetrics.lineSpacing
        #expect(style(s, at: "正文")?.paragraphSpacing == ReplyMetrics.heading4.top - lines)
        #expect(style(s, at: "补充说明")?.paragraphSpacing == ReplyMetrics.heading4.bottom)
        let three = (s.string as NSString).range(of: "三级").location
        #expect((s.attribute(.font, at: three, effectiveRange: nil) as? UIFont)?.pointSize == look.size)
        #expect(s.attribute(.foregroundColor, at: three, effectiveRange: nil) as? UIColor === look.color)
    }

    /// A bold label the Chinese way (a full-width colon, no space after it) is bold here as in the chat (`CJKEmphasis`):
    /// no asterisks left.
    @Test func cjkBoldLabelsAreBold() {
        let s = reply("**注意：**后文，这是**「强调」**的文字")
        #expect(s.string == "注意：后文，这是「强调」的文字")
        let bold = s.attribute(.font, at: 0, effectiveRange: nil) as? UIFont
        let traits = bold?.fontDescriptor.object(forKey: .traits) as? [UIFontDescriptor.TraitKey: Any]
        #expect((traits?[.weight] as? CGFloat ?? 0) >= UIFont.Weight.semibold.rawValue)
    }

    /// Spacing as in the chat: the lines of a paragraph are only line-spaced; between blocks the larger margin; a list's
    /// items 0.2 em apart.
    @Test func spacingFollowsTheChat() {
        let s = reply("一行\n两行\n\n下一段\n- 甲\n- 乙")
        let lines = look.size * ReplyMetrics.lineSpacing
        #expect(style(s, at: "一行")?.paragraphSpacing == 0)
        #expect(style(s, at: "两行")?.paragraphSpacing == ReplyMetrics.paragraphBottom - lines)
        #expect(style(s, at: "甲")?.paragraphSpacing == look.size * ReplyMetrics.itemTop)
        #expect(style(s, at: "两行")?.lineSpacing == lines)
        // The blank line takes no room but stays a line break, so copied paragraphs stay apart.
        let blank = (s.string as NSString).range(of: "两行").location + 3
        #expect((s.attribute(.paragraphStyle, at: blank, effectiveRange: nil) as? NSParagraphStyle)?.maximumLineHeight ?? 1 < 0.1)
    }

    /// A table is drawn as the chat draws it, and copies as its Markdown.
    @Test func aTableCopiesAsItsMarkdown() {
        let table = "| 页面 | 风险 |\n| --- | --- |\n| 首页 | 低 |"
        let s = reply("看表：\n\n" + table + "\n\n完")
        let at = (s.string as NSString).range(of: "\u{FFFC}").location
        #expect(at != NSNotFound)
        #expect(s.attribute(.attachment, at: at, effectiveRange: nil) is NSTextAttachment)
        #expect(ReplyText.copyText(s) == "看表：\n\n" + table + "\n\n完")
    }

    /// A table is the chat's table as a picture: its words are drawn, not an empty box, and it fits the width.
    @Test func aTableIsDrawn() throws {
        let image = try #require(ReplyTable.picture("| 页面 | 风险 |\n| --- | --- |\n| 首页 | 低 |", width: 360, traits: look.traits))
        #expect(image.size.width > 60 && image.size.width < 200 && image.size.height > 40, "\(image.size)") // its own width
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"], let png = image.pngData() {
            try? png.write(to: URL(fileURLWithPath: dir).appending(path: "reply-table.png"))
        }
        #expect(try ink(image) > 200)
        // And in the words: the attachment carries it.
        let s = reply("看表：\n\n| 页面 | 风险 |\n| --- | --- |\n| 首页 | 低 |")
        let at = (s.string as NSString).range(of: "\u{FFFC}").location
        let attached = try #require((s.attribute(.attachment, at: at, effectiveRange: nil) as? NSTextAttachment)?.image)
        #expect(try ink(attached) > 200)
    }

    /// Selected to their ends, lines are highlighted across the whole width, a table's and the last line's too (the
    /// system's stopped at the table and at the last word); the end handle stays after the last word; a word inside a
    /// line is highlighted just where it is.
    @Test func theHighlightIsEvenAcrossLines() throws {
        let v = LiftUITextView()
        v.frame = CGRect(x: 0, y: 0, width: 320, height: 1000)
        v.show("看表：\n\n| 页面 | 风险 |\n| --- | --- |\n| 首页 | 低 |\n\n做完再说。", .reply(quiet: false))
        v.layoutIfNeeded()
        let all = try #require(v.textRange(from: v.beginningOfDocument, to: v.endOfDocument))
        let rects = v.selectionRects(for: all)
        for y in Set(rects.map(\.rect.minY)) {
            let reach = rects.filter { $0.rect.minY == y }.map(\.rect.maxX).max() ?? 0
            #expect(abs(reach - 320) < 0.5, "the line at \(y) stops at \(reach)")
        }
        let starts = rects.filter(\.containsStart).count, ends = rects.filter(\.containsEnd)
        #expect(starts == 1)
        let end = try #require(ends.first)
        #expect(end.rect.maxX > 20 && end.rect.maxX < 160, "the end handle after 做完再说。: \(end.rect)")
        let r = (v.text as NSString).range(of: "看表")
        let from = try #require(v.position(from: v.beginningOfDocument, offset: r.location))
        let to = try #require(v.position(from: from, offset: r.length))
        let word = try #require(v.textRange(from: from, to: to))
        let one = v.selectionRects(for: word)
        #expect(one.count == 1 && (one.first?.rect.minX ?? 1) < 0.5 && (one.first?.rect.maxX ?? 320) < 160, "\(one.map(\.rect))")
    }

    /// Pixels with (nearly) full alpha: the words, not the faint rules and rows.
    private func ink(_ image: UIImage) throws -> Int {
        let cg = try #require(image.cgImage)
        let w = cg.width, h = cg.height
        var px = [UInt8](repeating: 0, count: w * h * 4)
        let ctx = try #require(CGContext(data: &px, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                         space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: w, height: h))
        return stride(from: 3, to: px.count, by: 4).filter { px[$0] > 200 }.count
    }

    @Test func plainTextIsUnchanged() {
        let text = "PR #42 看完了：逻辑没问题。\n#不是标题\n2 * 3 = 6"
        #expect(shown(text) == text)
    }

    /// Your own message is shown as its bubble shows it: no Markdown at all.
    @Test func yourOwnMessageIsPlain() {
        let text = "Python 里 __init__.py 的 2*3*4 是不是写错了？\n# 不是标题\n- 也不是列表\n```\n照原样\n```"
        let mine = ReplyText.Look(.plain(.primary), traits: UITraitCollection(preferredContentSizeCategory: .large))
        #expect(ReplyText.plain(text, look: mine).string == text)
        #expect(shown(text) != text) // as a reply it would lose marks
    }

    /// Sizes in em are whole points, as MarkdownUI draws them (`round(size × scale)`, #3188): inline code at 17 is 15,
    /// the first heading 22, an agent's quiet words 15.
    @Test func emSizesAreWholePointsAsTheChatDraws() {
        #expect(ReplyMetrics.points(ReplyMetrics.inlineCode, of: 17) == 15)
        #expect(ReplyMetrics.points(ReplyMetrics.heading1.size, of: 17) == 22)
        let s = reply("以 `cb3b` 开头\n\n# 标题")
        let code = (s.string as NSString).range(of: "cb3b").location, title = (s.string as NSString).range(of: "标题").location
        #expect((s.attribute(.font, at: code, effectiveRange: nil) as? UIFont)?.pointSize == ReplyMetrics.points(ReplyMetrics.inlineCode, of: look.size))
        #expect((s.attribute(.font, at: title, effectiveRange: nil) as? UIFont)?.pointSize == ReplyMetrics.points(ReplyMetrics.heading1.size, of: look.size))
        let quiet = ReplyText.Look(.reply(quiet: true), traits: UITraitCollection(preferredContentSizeCategory: .large))
        #expect(quiet.size == ReplyMetrics.points(ReplyMetrics.quiet, of: look.size))
    }

    /// The fonts follow the text size.
    @Test func fontsFollowTheTextSize() {
        func size(_ s: NSAttributedString, _ at: Int) -> CGFloat { (s.attribute(.font, at: at, effectiveRange: nil) as? UIFont)?.pointSize ?? 0 }
        let small = ReplyText.Look(.reply(quiet: false), traits: UITraitCollection(preferredContentSizeCategory: .small))
        let large = ReplyText.Look(.reply(quiet: false), traits: UITraitCollection(preferredContentSizeCategory: .accessibilityExtraLarge))
        let md = "正文\n```\ncode\n```"
        let a = ReplyText.attributed(md, look: small, width: 360), b = ReplyText.attributed(md, look: large, width: 360)
        #expect(size(b, 0) > size(a, 0))
        #expect(size(b, b.length - 1) > size(a, a.length - 1))
        // An agent's words between its steps are smaller.
        let quiet = ReplyText.Look(.reply(quiet: true), traits: UITraitCollection(preferredContentSizeCategory: .large))
        #expect(quiet.size < look.size)
    }
}
