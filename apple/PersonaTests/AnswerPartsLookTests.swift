#if os(iOS)
import MarkdownUI
import PersonaKit
import SwiftUI
import Testing
@testable import Persona

/// A long answer drawn as parts (#4970, `AnswerParts`, `AssistantTextPart`) looks exactly as it did whole: the parts
/// stacked with the chat list's spacing, each `Part.gap` under the one before, draw the same pixels as one `Markdown`
/// of the whole answer. MarkdownUI spaces its blocks from margins it learns after a first layout (state set from a
/// preference), so the views are hosted in a window and drawn once their height has settled, not with `ImageRenderer`.
@MainActor
struct AnswerPartsLookTests {
    /// Every kind of block a report has, cut points between all the plain ones.
    static let report: String = {
        var blocks = ["# 数据层迁移方案", "> 第 3 版：补了回滚和灰度两节。"]
        for n in 1...4 {
            blocks += ["## \(n). 第 \(n) 部分",
                       "迁移方案需要先确认现有数据的边界，再决定哪些字段保留。The adapter keeps the old wire format readable.",
                       "### \(n).1 细节",
                       "- **要点 1**：先在测试环境跑一遍\n- **要点 2**：再按 5% / 25% / 100% 放量",
                       "#### 备注",
                       "每一档观察 24 小时，`field_\(n)` 的数字和压测记录一致才放下一档。",
                       "| 列 1 | 列 2 | 列 3 |\n|---|---|---|\n| `field_\(n)_a` | 说明文字 | 说明文字，说明文字 |\n| `field_\(n)_b` | 说明 | 说明 |",
                       "1. 第一步\n\n2. 第二步，松散列表\n\n3. 第三步",
                       "```text\nstep_\(n) -> check_\(n) -> commit  # keep the old reader until every client moved\nline two\n```",
                       "最后一段：每一步都要能单独回滚，不依赖下一步。"]
        }
        return blocks.joined(separator: "\n\n")
    }()

    private static let width: CGFloat = 390

    private struct Drawn {
        let height: CGFloat
        let pixels: [UInt8]
    }

    /// The view at the chat's width on white, drawn once its height stops changing.
    private func draw(_ view: some View) async throws -> Drawn {
        let box = HeightBox()
        let content = view
            .frame(width: Self.width, alignment: .topLeading)
            .fixedSize(horizontal: false, vertical: true)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { box.height = $0 }
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .background(Color.white)
            .environment(\.colorScheme, .light)
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        let window = scene.map { UIWindow(windowScene: $0) } ?? UIWindow()
        window.frame = CGRect(x: 0, y: 0, width: Self.width, height: 6000)
        window.windowLevel = .alert + 1
        window.rootViewController = UIHostingController(rootView: content)
        window.makeKeyAndVisible()
        defer { window.isHidden = true }
        var last: CGFloat = -1, steady = 0
        for _ in 0..<60 where steady < 5 {
            try await Task.sleep(for: .milliseconds(50))
            window.layoutIfNeeded()
            steady = box.height == last && box.height > 0 ? steady + 1 : 0
            last = box.height
        }
        let height = box.height
        let format = UIGraphicsImageRendererFormat()
        format.scale = 2
        let size = CGSize(width: Self.width, height: ceil(height))
        let image = UIGraphicsImageRenderer(size: size, format: format).image { _ in
            window.drawHierarchy(in: window.bounds, afterScreenUpdates: true)
        }
        let cg = try #require(image.cgImage)
        var px = [UInt8](repeating: 0, count: cg.width * cg.height * 4)
        let ctx = try #require(CGContext(data: &px, width: cg.width, height: cg.height, bitsPerComponent: 8, bytesPerRow: cg.width * 4,
                                         space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        return Drawn(height: height, pixels: px)
    }

    private func markdown(_ text: String) -> some View {
        Markdown(MarkdownMemo.content(text))
            .markdownTheme(.persona)
            .markdownSoftBreakMode(.lineBreak)
            .frame(maxWidth: .infinity, alignment: .leading)
    }

    @Test func partsDrawTheAnswerAsItWasWhole() async throws {
        // Small parts, so every kind of cut is in it.
        let parts = AnswerParts.split(Self.report, lines: 6, bytes: 400)
        try #require(parts.count >= 6)
        let text = parts.map(\.text).joined(separator: "\n\n")
        #expect(text == Self.report)
        // The parts as the chat's lazy list stacks them (`chatRows`: spacing `Theme.Space.l`, `AssistantTextPart`).
        let split = VStack(alignment: .leading, spacing: Theme.Space.l) {
            ForEach(Array(parts.enumerated()), id: \.offset) { n, part in
                markdown(part.text).padding(.top, n == 0 ? 0 : part.gap - Theme.Space.l)
            }
        }
        let whole = try await draw(markdown(text))
        let drawn = try await draw(split)
        #expect(whole.height > 1000)
        // Rows and the whole document may round a fraction of a point differently; a wrong gap moves everything under it
        // by whole points, which shows as far more than this.
        #expect(abs(drawn.height - whole.height) <= 1, "parts \(drawn.height) pt, whole \(whole.height) pt")
        let compared = min(whole.pixels.count, drawn.pixels.count)
        let differing = zip(whole.pixels.prefix(compared), drawn.pixels.prefix(compared)).reduce(0) { $0 + ($1.0 == $1.1 ? 0 : 1) }
        #expect(Double(differing) <= Double(compared) * 0.002, "\(differing) of \(compared) pixel bytes differ")
    }
}

/// The chat's lines with long answers as parts (`ChatLines.splitAnswers`): the first part keeps the answer's id (a
/// jump, a quote or a saved place lands on its start), every part holds the answer (its long press, its zIndex), and
/// short answers and other lines stay as they were.
@MainActor
struct AnswerPartLinesTests {
    private func item(_ seq: Int, _ text: String, role: String = "assistant") -> Item {
        Item(id: "\(seq)#0", seq: seq, ts: nil, role: role, kind: .text, text: text, toolUseID: nil, toolName: nil, toolInput: nil,
             isError: nil, image: nil)
    }

    @Test func aLongAnswerBecomesItsPartsInPlace() throws {
        let long = item(2, AnswerPartsLookTests.report + "\n\n" + Array(repeating: "补充一段，说明为什么这样拆。", count: 300).joined(separator: "\n\n"))
        let lines: [ChatLine] = [.row(.user(item(1, "整理方案", role: "user"))), .row(.assistant(long)), .row(.assistant(item(3, "好的。")))]
        let parts = AnswerPartMemo.parts(long.text ?? "")
        try #require(parts.count > 1)
        let split = ChatLines.splitAnswers(lines)
        #expect(split.count == lines.count - 1 + parts.count)
        #expect(split.map(\.id) == ["1#0", "2#0"] + (1..<parts.count).map { "2#0/part-\($0)" } + ["3#0"])
        #expect(split.dropFirst().dropLast().allSatisfy { $0.holds("2#0") })
        #expect(!split[0].holds("2#0"))
    }

    @Test func shortAnswersStayAsTheyAre() {
        let short = "好的，第 6 部分的表核对过了。"
        let lines: [ChatLine] = [.row(.user(item(1, "你好", role: "user"))), .row(.assistant(item(2, short)))]
        #expect(AnswerPartMemo.parts(short).isEmpty)
        #expect(ChatLines.splitAnswers(lines).map(\.id) == ["1#0", "2#0"])
    }
}

/// The drawn height, written by the view's geometry callback without redrawing anything.
private final class HeightBox {
    var height: CGFloat = 0
}
#endif
