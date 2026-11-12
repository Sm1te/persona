import Foundation
import SwiftUI
import Testing
@testable import Persona

/// Supervised tasks (#3124, docs/assistant/unified-entry.md ("Supervision")) and their agents' reports (#5112): `supervision` decodes
/// leniently, the chip, the checklist and the lines say what the web says (web/src/lib/supervision.test.ts). Tests run
/// in Chinese (the scheme pins zh-Hans).
struct SupervisionTests {
    private func todo(_ json: String) throws -> TodoItem { try JSON.decoder.decode(TodoItem.self, from: Data(json.utf8)) }

    private func sup(_ s: TaskSupervision = .init(supervisor: "main", doneWhen: ["PR 合并到 master", "截图贴进 PR"]),
                     done: Bool = false, doneBy: String? = nil) -> TodoItem {
        TodoItem(id: "todo_1", title: "部署 hub", doneAt: done ? Date() : nil, doneBy: doneBy, supervision: s)
    }

    // MARK: Decoding

    @Test func decodesAFullSupervision() throws {
        let t = try todo(#"""
        {"id":"todo_1","title":"部署 hub","agents":[],"supervision":{"supervisor":"main","done_when":["PR 合并到 master","截图贴进 PR"],
         "expect_minutes":30,"state":"rework","round":1,"updated_at":"2026-11-05T08:00:00Z",
         "reviews":[{"at":"2026-11-05T08:30:00Z","verdict":"rework","round":1,
                     "checks":[{"criterion":"PR 合并到 master","met":true},{"criterion":"截图贴进 PR","met":false,"evidence":"只有浅色"}],
                     "note":"补深色截图"}],
         "progress":{"kind":"progress","step":3,"steps":5,"text":"在跑迁移","at":"2026-11-05T08:40:00Z"}}}
        """#)
        let s = try #require(t.supervision)
        #expect(s.supervisor == "main" && s.doneWhen.count == 2 && s.expectMinutes == 30 && s.state == "rework" && s.round == 1)
        #expect(s.reviews.count == 1 && s.reviews[0].checks.count == 2 && s.reviews[0].note == "补深色截图" && s.reviews[0].round == 1)
        #expect(s.progress == TaskSupervision.Progress(kind: "progress", step: 3, steps: 5, text: "在跑迁移", at: s.progress?.at))
        #expect(s.updatedAt != nil && s.progress?.at != nil)
    }

    @Test func aTodoWithoutSupervisionHasNone() throws {
        let t = try todo(#"{"id":"todo_2","title":"手动的","agents":[]}"#)
        #expect(t.supervision == nil && t.reviewBadge == nil && t.reviewState == nil)
        let null = try todo(#"{"id":"todo_3","title":"x","agents":[],"supervision":null}"#)
        #expect(null.supervision == nil)
    }

    /// Fields left out take their empty value: only the supervisor, or not even that.
    @Test func aPartialSupervisionDecodes() throws {
        let t = try todo(#"{"id":"todo_1","title":"x","agents":[],"supervision":{"supervisor":"main"}}"#)
        let s = try #require(t.supervision)
        #expect(s.doneWhen.isEmpty && s.reviews.isEmpty && s.state == "" && s.round == 0 && s.expectMinutes == nil && s.progress == nil)
        #expect(t.reviewBadge?.text == "督办中")
        let empty = try todo(#"{"id":"todo_1","title":"x","agents":[],"supervision":{}}"#)
        #expect(empty.supervision?.supervisor == "")
    }

    /// A malformed supervision is as if there were none; the todo itself still decodes.
    @Test func aMalformedSupervisionIsDropped() throws {
        for bad in [#""yes""#, "[1,2]", #"{"done_when":"PR 合并"}"#, #"{"state":5}"#] {
            let t = try todo(#"{"id":"todo_1","title":"部署 hub","agents":[],"supervision":"# + bad + "}")
            #expect(t.title == "部署 hub" && t.supervision == nil, "supervision \(bad)")
        }
    }

    /// A broken progress or reviews list is left out, the rest of the supervision stays.
    @Test func aMalformedProgressOrReviewIsLeftOut() throws {
        let t = try todo(#"""
        {"id":"todo_1","title":"x","agents":[],"supervision":{"supervisor":"main","done_when":["A"],
         "reviews":"none","progress":["not","an","object"],"expect_minutes":"soon","round":"two"}}
        """#)
        let s = try #require(t.supervision)
        #expect(s.doneWhen == ["A"] && s.reviews.isEmpty && s.progress == nil && s.expectMinutes == nil && s.round == 0)
        let partial = try todo(#"{"id":"todo_1","title":"x","agents":[],"supervision":{"progress":{"text":"在跑迁移","step":"3"}}}"#)
        #expect(partial.supervision?.progress == TaskSupervision.Progress(kind: "", text: "在跑迁移"))
    }

    @Test func aSupervisionSurvivesTheCache() throws {
        let t = sup(.init(supervisor: "main", doneWhen: ["A"], state: "pending_review",
                          progress: .init(kind: "question", text: "要不要停写？")))
        let back = try JSON.decoder.decode(TodoItem.self, from: JSON.encoder.encode(t))
        #expect(back.supervision == t.supervision)
    }

    // MARK: Feed

    @Test func feedKindsReviewAndReport() throws {
        let res = try JSON.decoder.decode(APIClient.FeedResponse.self, from: Data(#"""
        {"items":[
          {"id":"f1","conversation":"main","at":"2026-11-05T09:00:00Z","kind":"review","ref":"todo_1",
           "body":{"title":"部署 hub","verdict":"rework","round":2,"note":"补深色截图","checks":[]}},
          {"id":"f2","conversation":"main","at":"2026-11-05T09:01:00Z","kind":"report","ref":"todo_1",
           "body":{"kind":"question","text":"A 先灰度\n还是 B 全量？","evidence":["https://github.com/o/r/pull/902"," ",""],"step":2,"steps":5}},
          {"id":"f3","conversation":"main","at":"2026-11-05T09:02:00Z","kind":"routed","ref":"todo_1","body":{"text":"先修测试","interrupted":true}},
          {"id":"f4","conversation":"main","at":"2026-11-05T09:03:00Z","kind":"routed","ref":"todo_1","body":{"text":"先修测试"}},
          {"id":"f5","conversation":"main","at":"2026-11-05T09:04:00Z","kind":"something_newer","ref":"todo_1"}]}
        """#.utf8))
        #expect(res.items.map(\.kind) == [.review, .report, .routed, .routed, .unknown])
        let review = res.items[0]
        #expect(review.verdict == "rework" && review.round == 2 && review.note == "补深色截图" && review.title == "部署 hub")
        #expect(review.reportKind == nil)
        let report = res.items[1]
        #expect(report.reportKind == "question" && report.text == "A 先灰度\n还是 B 全量？" && report.step == 2 && report.steps == 5)
        #expect(report.evidence == ["https://github.com/o/r/pull/902"])
        #expect(res.items[2].interrupted && !res.items[3].interrupted && !review.interrupted)
    }

    @Test func reportBodyAndEvidenceRemainSeparate() throws {
        let report = try JSON.decoder.decode(FeedItem.self, from: Data(#"""
        {"id":"report","conversation":"main","at":"2026-11-05T09:01:00Z","kind":"report","ref":"todo_1",
         "body":{"kind":"progress","text":"Summary unchanged.\nSee https://example.com/body-link",
                 "evidence":["https://example.com/pull/1","https://example.com/commit/abc","https://example.com/issues/1#comment",
                             "https://example.com/actions/runs/1","/tmp/report-check.xcresult"]}}
        """#.utf8))
        let body = "Summary unchanged.\nSee https://example.com/body-link"
        #expect(report.text == body)
        #expect(ReviewText.reportLine(kind: report.reportKind ?? "", text: report.text ?? "") == body)
        #expect(report.evidence.count == 5)
        #expect(report.evidence.last == "/tmp/report-check.xcresult")
    }

    /// Evidence must not change the rendered card's size or pixels, in either appearance or report state.
    @MainActor @Test func reportCardRenderingIgnoresEvidence() throws {
        func render(_ report: FeedItem, _ appearance: ColorScheme) throws -> (width: Int, height: Int, pixels: Data) {
            let renderer = ImageRenderer(content: ReportFeedLine(item: report, taskTitle: "Example task")
                .frame(width: 480)
                .environment(\.colorScheme, appearance))
            let image = try #require(renderer.cgImage)
            let pixels = try #require(image.dataProvider?.data)
            return (image.width, image.height, pixels as Data)
        }
        for kind in ["progress", "done", "blocked", "question"] {
            let body: [String: JSONValue] = ["kind": .string(kind), "text": .string("Summary unchanged.\nhttps://example.com/body-link")]
            let plain = FeedItem(id: "render-report", conversation: "main", at: .distantPast, kind: .report, body: .object(body))
            var withEvidence = plain
            withEvidence.body = .object(body.merging(["evidence": .array([
                .string("https://example.com/pull/1"), .string("https://example.com/commit/abc"),
                .string("https://example.com/issues/1#comment"), .string("https://example.com/actions/runs/1"),
                .string("/tmp/report-check.xcresult"),
            ])]) { _, added in added })
            for appearance in [ColorScheme.light, .dark] {
                let expected = try render(plain, appearance)
                let actual = try render(withEvidence, appearance)
                #expect(actual.width == expected.width && actual.height == expected.height)
                #expect(actual.pixels.count == expected.pixels.count)
                // On macOS two renders of the same card can differ by a step in a few channels.
                let off = zip(actual.pixels, expected.pixels).map { abs(Int($0) - Int($1)) }.max() ?? 0
                #expect(off <= 2, "\(kind), \(appearance): a channel differs by \(off)")
            }
        }
    }

    // MARK: The chip

    @Test func badgeTextsAndTones() {
        func badge(_ state: String, round: Int = 0, done: Bool = false, checks: [TaskSupervision.Check] = []) -> ReviewBadge? {
            sup(.init(supervisor: "main", doneWhen: ["PR 合并到 master", "截图贴进 PR"], state: state, round: round,
                      reviews: checks.isEmpty ? [] : [.init(verdict: "rework", checks: checks)]), done: done).reviewBadge
        }
        #expect(badge("pending_review")?.text == "待验收 · 0/2")
        #expect(badge("pending_review")?.tone == .caution)
        let rework = badge("rework", round: 1, checks: [.init(criterion: "PR 合并到 master", met: true)])
        #expect(rework?.text == "返工第 1 轮 · 1/2" && rework?.tone == .caution)
        #expect(badge("needs_user")?.label == "等你决定" && badge("needs_user")?.tone == .danger)
        #expect(badge("accepted", done: true)?.label == "已验收" && badge("accepted", done: true)?.tone == .positive)
        #expect(badge("cancelled", done: true)?.label == "已取消" && badge("cancelled", done: true)?.tone == .neutral)
        #expect(badge("")?.label == "督办中" && badge("")?.tone == .neutral)
        #expect(badge("a_state_from_later")?.label == "督办中")
        // No criteria: no count.
        #expect(sup(.init(supervisor: "main", state: "needs_user")).reviewBadge?.text == "等你决定")
        #expect(TodoItem(id: "t", title: "x").reviewBadge == nil)
    }

    /// An accepted or cancelled task the user reopened goes on again.
    @Test func reopenedAcceptedCountsAsInProgress() {
        let accepted = TaskSupervision(supervisor: "main", doneWhen: ["A"], state: "accepted")
        #expect(sup(accepted, done: true, doneBy: "review").reviewState == .accepted)
        let reopened = sup(accepted, done: false)
        #expect(reopened.reviewState == .ongoing && reopened.reviewBadge?.label == "督办中")
        let cancelled = TaskSupervision(supervisor: "main", state: "cancelled")
        #expect(sup(cancelled, done: false).reviewState == .ongoing)
    }

    @Test func statusLabelOfAReviewedTask() {
        let accepted = sup(.init(supervisor: "main", state: "accepted"), done: true, doneBy: "review")
        #expect(accepted.statusLabel(.done) == "已验收" && accepted.reviewDone)
        let cancelled = sup(.init(supervisor: "main", state: "cancelled"), done: true, doneBy: "review")
        #expect(cancelled.statusLabel(.done) == "已取消")
        let auto = TodoItem(id: "t", title: "x", doneAt: Date(), doneBy: "auto", doneReason: "PR 已合并")
        #expect(auto.statusLabel(.done) == "自动完成" && auto.closedReason == "PR 已合并")
        let byHand = TodoItem(id: "t", title: "x", doneAt: Date(), doneBy: nil, doneReason: "x")
        #expect(byHand.statusLabel(.done) == "已完成" && byHand.closedReason == nil)
        var reviewed = cancelled
        reviewed.doneReason = "  不需要了 "
        #expect(reviewed.closedReason == "不需要了")
        // Open: the state's own label, the chip says the rest.
        #expect(sup(.init(supervisor: "main", state: "rework")).statusLabel(.working) == "工作中")
    }

    // MARK: The checklist

    /// Each criterion takes the verdict of the latest review that judged it; criteria judged but not given follow.
    @Test func checklistUsesTheLatestReviewPerCriterion() {
        let s = TaskSupervision(supervisor: "main", doneWhen: ["PR 合并到 master", "截图贴进 PR", "部署"], reviews: [
            .init(verdict: "rework", checks: [.init(criterion: "PR 合并到 master", met: false, evidence: "还没合"),
                                               .init(criterion: "截图贴进 PR", met: false, evidence: "没截图"),
                                               .init(criterion: "顺手改了文档", met: true)]),
            .init(verdict: "rework", round: 2, checks: [.init(criterion: "PR 合并到 master", met: true, evidence: " "),
                                                         .init(criterion: "顺手改了文档", met: false, evidence: "又删了")]),
        ])
        #expect(s.checklist == [
            ReviewCheckItem(criterion: "PR 合并到 master", mark: .met, evidence: nil),
            ReviewCheckItem(criterion: "截图贴进 PR", mark: .missing, evidence: "没截图"),
            ReviewCheckItem(criterion: "部署", mark: .open, evidence: nil),
            ReviewCheckItem(criterion: "顺手改了文档", mark: .missing, evidence: "又删了"),
        ])
        #expect(s.checkCount == "1/4")
        #expect(TaskSupervision(supervisor: "main").checklist.isEmpty && TaskSupervision(supervisor: "main").checkCount == "")
        #expect(s.latestReview?.round == 2)
    }

    // MARK: Lines

    @Test func progressAndReportLines() {
        func line(_ p: TaskSupervision.Progress?) -> String? { TaskSupervision(supervisor: "main", progress: p).progressLine }
        #expect(line(nil) == nil)
        #expect(line(.init(kind: "progress", text: "")) == nil)
        #expect(line(.init(kind: "progress", step: 3, steps: 5, text: "在跑迁移")) == "第 3/5 步 · 在跑迁移")
        #expect(line(.init(kind: "progress", step: 2, text: "在跑迁移")) == "第 2 步 · 在跑迁移")
        #expect(line(.init(kind: "progress", step: 0, steps: 5, text: "在跑迁移")) == "在跑迁移")
        #expect(line(.init(kind: "progress", text: "在跑迁移")) == "在跑迁移")
        #expect(line(.init(kind: "question", step: 2, steps: 5, text: "要不要停写？")) == "问 Persona：要不要停写？")
        #expect(ReviewText.reportLine(kind: "blocked", text: "等 CI") == "卡住了：等 CI")
        #expect(ReviewText.reportLine(kind: "done", text: "PR 已合并") == "说做完了：PR 已合并")
        #expect(ReviewText.reportWantsAttention("question") && ReviewText.reportWantsAttention("blocked") && !ReviewText.reportWantsAttention("done"))
    }

    @Test func reviewVerdictLabels() {
        #expect(ReviewText.verdict("accepted", round: nil) == "已验收")
        #expect(ReviewText.verdict("rework", round: 2) == "返工第 2 轮")
        #expect(ReviewText.verdict("rework", round: nil) == "返工第 1 轮")
        #expect(ReviewText.verdict("needs_user", round: nil) == "等你决定")
        #expect(ReviewText.verdict("cancelled", round: nil) == "已取消")
        #expect(ReviewText.verdict("", round: nil) == "验收")
    }
}

/// Synthetic transcript contract for #2092, matching web/src/lib/turns.supervision.test.ts. No real chat or UI needed.
struct ChatSupervisionTests {
    // i18n-ignore: the exact header, marker and level tag are protocol data, not user-facing copy.
    private static let raw = "📋 督办：你派出去的任务有 2 个要你处理\n<persona-supervision level=\"triage\" />\n这是 Persona 的督办消息\nCheck the fictional release task."

    private func input(_ seq: Int, text: String? = nil, queued: Bool = false) -> Item {
        Item(id: "u\(seq)#0", seq: seq, role: "user", kind: .text, text: text ?? Self.raw, queued: queued)
    }

    private func call(_ seq: Int, _ name: String, input: JSONValue? = nil) -> Item {
        Item(id: "t\(seq)#0", seq: seq, role: "assistant", kind: .toolUse, toolUseID: "call\(seq)", toolName: name, toolInput: input)
    }

    private func result(_ seq: Int, _ use: Item, text: String = "ok", error: Bool = false) -> Item {
        Item(id: "r\(seq)#0", seq: seq, role: "user", kind: .toolResult, text: text, toolUseID: use.toolUseID, isError: error)
    }

    private func visible(_ items: [Item], live: Bool = false) -> Bool {
        let results = Dictionary(uniqueKeysWithValues: items.filter { $0.kind == .toolResult && $0.toolUseID != nil }.map { ($0.toolUseID!, $0) })
        return Supervision.visible(items, results: results, live: live)
    }

    private func turns(_ items: [Item]) -> [Turn] {
        ChatBlock.build(ChatStore.buildRows(items)).compactMap { if case .turn(let t) = $0 { t } else { nil } }
    }

    @Test func recognizesLegacyTextAndExactLevelsWithoutChangingRawInput() throws {
        let original = "<persona-context kind=\"context\">Fictional snapshot</persona-context>\n" + Self.raw
        let parsed = try #require(Injected.of(input(1, text: original)))
        #expect(parsed.kind == "system" && parsed.isSupervision && parsed.level == "triage")
        #expect(input(1, text: original).text == original)
        #expect(Injected.parse(Self.raw.replacingOccurrences(of: "level=\"triage\"", with: "level=\"user\""))?.level == "user")
        #expect(Injected.parse(Self.raw.replacingOccurrences(of: "<persona-supervision level=\"triage\" />\n", with: ""))?.level == "triage")
        #expect(Injected.parse(Self.raw.replacingOccurrences(of: "level=\"triage\"", with: "level=\"other\""))?.level == "triage")
        #expect(Injected.parse(String(Self.raw.split(separator: "\n")[0])) == nil)
        #expect(Injected.parse("A real request\n<persona-supervision level=\"user\" />") == nil)
        #expect(Injected.parse("A real request\n" + Self.raw) == nil)
    }

    @Test func decodesLevelsAndDefaultsLegacyMarksThroughOf() throws {
        let legacy = try JSON.decoder.decode(Item.self, from: Data(#"{"id":"u1#0","seq":1,"role":"user","kind":"text","injected":{"kind":"system","state":"supervise","line":"Check tasks"}}"#.utf8))
        #expect(legacy.injected?.level == nil)
        #expect(Injected.of(legacy)?.level == "triage")
        #expect(!visible([legacy]))
        var marked = legacy
        marked.text = Self.raw.replacingOccurrences(of: "level=\"triage\"", with: "level=\"user\"")
        #expect(Injected.of(marked)?.level == "user")
        marked.injected?.level = "triage" // An explicit hub level wins over text.
        #expect(Injected.of(marked)?.level == "triage")
        marked.injected?.level = "user"
        let decoded = try JSON.decoder.decode(Item.self, from: JSON.encoder.encode(marked))
        #expect(decoded.injected?.level == "user" && visible([decoded]))
    }

    @Test func realUsersAndPeersAreNeverRecognizedFromTheirWords() {
        var user = input(1)
        user.clientMsgID = "fictional-message"
        #expect(Injected.of(user) == nil)
        user.clientMsgID = nil
        user.from = "fictional-peer"
        #expect(Injected.of(user) == nil)
        user.from = nil
        user.role = "assistant"
        #expect(Injected.of(user) == nil)
        #expect(!PendingMessage.arrived(PendingMessage(text: Self.raw, images: [], createdAt: nil), as: input(1)))
    }

    @Test func canonicalToolNamespacesMatchTheWebExactly() {
        for bare in ["notify_user", "ask_user", "supervision_finish", "task_review"] {
            for prefix in ["", "mcp__persona__", "mcp__plugin_persona__", "mcp__\(LegacyNames.product)__", "mcp__plugin_\(LegacyNames.product)__"] {
                #expect(Supervision.tool(prefix + bare) == bare)
            }
            for prefix in ["mcp__fictional__", "mcp__persona_other__", "mcp__plugin_persona_other__", "mcp__persona__extra__"] {
                #expect(Supervision.tool(prefix + bare) == nil)
            }
        }
        for name in [nil, "", "mcp__persona", "mcp__persona__", "notify_user_extra", "AskUserQuestion", "task_done"] {
            #expect(Supervision.tool(name) == nil)
        }
    }

    @Test func notificationsAndQuestionsNeedSuccessfulResults() {
        for name in ["notify_user", "ask_user", "mcp__persona__notify_user", "mcp__plugin_persona__ask_user"] {
            let use = call(2, name)
            #expect(visible([input(1), use, result(3, use)]))
            #expect(!visible([input(1), use, result(3, use, text: #"{"visibility":"user"}"#, error: true)], live: true))
        }
        let unrelated = call(2, "mcp__fictional__notify_user")
        #expect(!visible([input(1), unrelated, result(3, unrelated)]))
    }

    @Test func onlyLivePendingQuestionsPromoteBeforeTheirResult() {
        for name in ["ask_user", "mcp__persona__ask_user"] {
            #expect(visible([input(1), call(2, name)], live: true))
            #expect(!visible([input(1), call(2, name)]))
        }
        for name in ["notify_user", "supervision_finish", "task_review", "mcp__fictional__ask_user"] {
            #expect(!visible([input(1), call(2, name, input: .object(["visibility": .string("user"), "verdict": .string("accepted")]))], live: true))
        }
    }

    @Test func finishUsesTheSuccessfulReceiptNotRequestedVisibility() {
        let use = call(2, "mcp__persona__supervision_finish", input: .object(["visibility": .string("user")]))
        for text in [#"{"visibility":"silent"}"#, "not JSON", "null", #"{"level":"user"}"#, #"[{"visibility":"user"}]"#, #"{"visibility":true}"#] {
            #expect(!visible([input(1), use, result(3, use, text: text)]), "receipt: \(text)")
        }
        #expect(visible([input(1), use, result(3, use, text: #"{"visibility":"user"}"#)]))
        #expect(!visible([input(1), use, result(3, use, text: #"{"visibility":"user"}"#, error: true)]))
    }

    @Test func reviewsPromoteOnlyAcceptedOrNeedsUserWithSuccessfulResults() {
        for verdict in ["accepted", "needs_user", "rework", "cancelled", "pending_review"] {
            let use = call(2, "task_review", input: .object(["verdict": .string(verdict)]))
            #expect(visible([input(1), use, result(3, use)]) == (verdict == "accepted" || verdict == "needs_user"))
            #expect(!visible([input(1), use, result(3, use, error: true)]))
        }
    }

    @Test func promotionCannotBeHiddenByALaterSilentFinish() {
        let notify = call(2, "notify_user"), finish = call(4, "supervision_finish")
        #expect(visible([input(1), notify, result(3, notify), finish, result(5, finish, text: #"{"visibility":"silent"}"#)]))
        var retained = call(2, "Read")
        retained.supervisionUser = true
        #expect(visible([input(1), retained, finish, result(5, finish, text: #"{"visibility":"silent"}"#)]))
    }

    @Test func resultIndexAssociatesLateReceiptsWithTheirOriginalRound() {
        let use = call(2, "notify_user"), late = result(5, use)
        #expect(Supervision.visible([input(1), use], results: [use.toolUseID!: late], live: false))
        #expect(!Supervision.visible([input(4, text: "Next real request"), late], results: [use.toolUseID!: late], live: false))
    }

    @Test func foldAndClientPromotionRoundTripWithTheirDistinctKeys() throws {
        let json = #"{"id":"fold:t2#0","seq":2,"role":"assistant","kind":"fold","fold":{"items":4,"tools":2,"thoughts":0,"through_seq":5,"through_id":"r5#0","supervision_user":true}}"#
        var folded = try JSON.decoder.decode(Item.self, from: Data(json.utf8))
        #expect(folded.fold?.supervisionUser == true && visible([input(1), folded]))
        folded.supervisionUser = true
        let data = try JSON.encoder.encode(folded)
        let value = try JSONDecoder().decode(JSONValue.self, from: data)
        #expect(value["supervisionUser"] == .bool(true))
        #expect(value["supervision_user"] == nil)
        #expect(value["fold"]?["supervision_user"] == .bool(true))
        #expect(try JSON.decoder.decode(Item.self, from: data) == folded)
        folded.fold?.supervisionUser = nil
        folded.supervisionUser = nil
        #expect(!visible([input(1), folded]))
        #expect(ChatCache.folder == "chats-v17")
    }

    @Test func queuedSupervisionAndTheNextGenuineQueuedInputAreSeparateTurns() {
        let items = [input(1, text: "First real request"), call(2, "Read"), input(3, queued: true), call(4, "Read"),
                     input(5, text: "Next real request", queued: true), call(6, "Read"), input(7, text: "One more detail", queued: true)]
        let built = turns(items)
        #expect(built.map(\.id) == ["u1#0", "u3#0", "u5#0"])
        #expect(built.map { $0.users.map(\.id) } == [["u1#0"], ["u3#0"], ["u5#0", "u7#0"]])
        #expect(built[1].ordered.allSatisfy { $0.id != "u5#0" })
    }

    @Test func sameEntryTextAndImagesStayInTheSupervisionRound() {
        let photo = Item(id: "u3#0", seq: 3, role: "user", kind: .image, image: ImagePayload(mime: "image/png", data: ""))
        var supervise = input(3, queued: true)
        supervise.id = "u3#1"
        var extra = input(3, text: "Extra fictional detail")
        extra.id = "u3#2"
        let items = [input(1, text: "First real request"), call(2, "Read"), photo, supervise, extra, call(4, "Read"),
                     input(5, text: "Next real request", queued: true)]
        let built = turns(items)
        #expect(built.map { $0.users.map(\.id) } == [["u1#0"], ["u3#1", "u3#0", "u3#2"], ["u5#0"]])
        #expect(built[1].users.compactMap(\.userInput).map(\.text).contains(Self.raw))
        #expect(!built[1].process.contains { $0.isUserInput })
    }

    @Test func imageOnlyQueuedInputAfterSupervisionOpensANewTurn() {
        let first = Item(id: "u4#0", seq: 4, role: "user", kind: .image, image: ImagePayload(mime: "image/png", data: ""), queued: true)
        var second = first
        second.id = "u4#1"
        let built = turns([input(1), call(2, "Read"), first, second, call(5, "Read")])
        #expect(built.map { $0.users.map(\.id) } == [["u1#0"], ["u4#0", "u4#1"]])
        #expect(built[1].process.allSatisfy { !$0.isUserInput })
    }

    @Test func recapContinuationsDoNotAbsorbTheNextGenuineQueuedInput() {
        let recap = Item(id: "recap#0", seq: 3, role: "system", kind: .recap, text: "Fictional recap")
        let built = turns([input(1), call(2, "Read"), recap, call(4, "Read"), input(5, text: "Next real request", queued: true)])
        #expect(built.map { $0.users.map(\.id) } == [["u1#0"], [], ["u5#0"]])
        #expect(built[1].ordered.map(\.id) == ["tools-t4#0"])
    }
}
