import Foundation
import Testing
@testable import Persona

/// A work line's time (#5119): while the turn runs it counts from the turn's start to now; once it is over it stops at
/// the exchange's latest record, its answer included. So the time carries on from what the running line showed and
/// never goes back as the answer comes in (it read 8m 17s, then 8m 12s: it stopped at the last tool step).
@MainActor
struct WorkLineTimeTests {
    private let t0 = Date(timeIntervalSince1970: 1_782_000_000)

    private func item(_ seq: Int, _ kind: ItemKind, at: TimeInterval, role: String = "assistant", text: String? = nil,
                      tool: String? = nil, id: String? = nil) -> Item {
        Item(id: "\(seq)#0", seq: seq, ts: t0.addingTimeInterval(at), role: role, kind: kind, text: text, toolUseID: id,
             toolName: tool, toolInput: nil, isError: nil, image: nil)
    }

    /// The work line's start and end, built as the chat builds it (`live`: the turn is running).
    private func span(_ items: [Item], live: Bool) -> (start: Date?, end: Date?, live: Bool)? {
        let blocks = ChatStore.rebuild(items, settled: nil).blocks
        var turn: String?
        if live, case .turn(let t) = blocks.last { turn = t.id }
        for line in ChatLines(openWork: []).build(blocks, live: turn) {
            if case .work(_, _, let isLive, let start, let end) = line.content { return (start, end, isLive) }
        }
        return nil
    }

    private func seconds(_ s: (start: Date?, end: Date?, live: Bool)?, now: Date) -> Int? {
        guard let s, let start = s.start else { return nil }
        return Int((s.live ? now : s.end ?? now).timeIntervalSince(start))
    }

    /// The mock's `tool_turn` flow (#3005): a call, its result at 8m 12s, a pause, the answer at 8m 17s.
    private var turn: [Item] {
        [item(1, .text, at: 0, role: "user", text: "Find the PR that closed it"),
         item(2, .toolUse, at: 2, tool: "Bash", id: "a"),
         item(3, .toolResult, at: 492, role: "user", text: "ok", id: "a")]
    }

    @Test func theTimeNeverGoesBackWhenTheAnswerComesIn() throws {
        let answer = item(4, .text, at: 497, text: "It was closed by the merge.")
        var shown: [Int] = []
        // Running: each tick until the answer is in, then the tick it lands on while the agent is still at work.
        for tick in stride(from: 490.0, through: 497, by: 1) {
            shown.append(try #require(seconds(span(turn, live: true), now: t0.addingTimeInterval(tick))))
        }
        shown.append(try #require(seconds(span(turn + [answer], live: true), now: t0.addingTimeInterval(497))))
        // Finished.
        let done = try #require(span(turn + [answer], live: false))
        #expect(!done.live)
        #expect(done.end == answer.ts)
        let finished = try #require(seconds(done, now: .distantFuture))
        #expect(finished == 497)
        #expect(shown.allSatisfy { $0 <= finished }, "\(shown) then \(finished)")
        #expect(shown == shown.sorted())
        // The same words as the running line's last tick.
        #expect(WorkLine.duration(from: done.start, to: try #require(done.end))
                == WorkLine.duration(from: done.start, to: t0.addingTimeInterval(497)))
    }

    /// Stopped before it answered: the interrupt is where the turn ended.
    @Test func anInterruptedTurnEndsAtItsInterrupt() throws {
        let stop = item(4, .notice, at: 495, role: "system", text: "已中断") // i18n-ignore: the hub's notice
        let done = try #require(span(turn + [stop], live: false))
        #expect(done.end == stop.ts)
    }

    /// Parallel calls that finish out of order, and nothing said after them: the one that finished last, as the web
    /// counts it (not the last call in the list).
    @Test func parallelCallsEndAtTheLatestResult() throws {
        let items = [item(1, .text, at: 0, role: "user", text: "Read both"),
                     item(2, .toolUse, at: 1, tool: "Read", id: "a"), item(3, .toolUse, at: 1, tool: "Read", id: "b"),
                     item(4, .toolResult, at: 40, role: "user", text: "ok", id: "a"),
                     item(5, .toolResult, at: 12, role: "user", text: "ok", id: "b")]
        let done = try #require(span(items, live: false))
        #expect(seconds(done, now: .distantFuture) == 40)
    }

    /// Compacted while idle an hour after the answer: the compaction is not part of the turn's time.
    @Test func anIdleCompactionAfterTheAnswerDoesNotCount() throws {
        let notes = [ChatStore.compactedText, ChatStore.continuedText,
                     "Compacted while idle, before the prompt cache expired · turn off with \"idleCompaction\": false in settings.json"]
        let idle = notes.enumerated().map { n, text in item(5 + n, .notice, at: 3600, role: "system", text: text) }
        let done = try #require(span(turn + [item(4, .text, at: 497, text: "Done.")] + idle, live: false))
        #expect(seconds(done, now: .distantFuture) == 497)
    }
}
