import Foundation
import Testing
@testable import Persona

/// An upstream refusal and its fallback model (#2528, #4489): what is read (never an older hub's record line), where the
/// refusal's line goes, what the card above the composer says and offers, and a refused going back in words. The
/// tests run in zh-Hans.
@MainActor
struct ModelFallbackTests {
    private let refusal = #"{"subtype":"model_refusal_fallback","original_model":"claude-opus-5-5","fallback_model":"claude-sonnet-5-5","category":"cyber","scope":"session","explanation":"The upstream provider declined this request.","text":"Claude Code is unable to respond to this request with claude-opus-5-5.","raw":"{\"type\":\"system\",\"secret\":\"x\"}"}"#

    private func session(_ extra: String) throws -> Session {
        let json = #"{"machine_id":"m","session_id":"s","title":"t","cwd":"/w","status":"done","state":"idle","agent":"claude"\#(extra)}"#
        return try JSON.decoder.decode(Session.self, from: Data(json.utf8))
    }

    private func item(_ seq: Int, _ kind: ItemKind, role: String, text: String?) -> Item {
        Item(id: "\(seq)#0", seq: seq, ts: nil, role: role, kind: kind, text: text, toolUseID: nil, toolName: nil, toolInput: nil, isError: nil, image: nil)
    }

    @Test func aRefusalIsReadWithoutTheRecordLine() throws {
        let json = #"{"id":"n","seq":2,"role":"system","kind":"notice","text":"{\"type\":\"system\",\"secret\":\"x\"}","model_refusal":\#(refusal)}"#
        let it = try JSON.decoder.decode(Item.self, from: Data(json.utf8)).publicRefusal()
        let r = try #require(it.modelRefusal)
        #expect(r.isFallback && r.originalModel == "claude-opus-5-5" && r.scope == "session")
        #expect(it.text == "Claude Code is unable to respond to this request with claude-opus-5-5.")
        // What the cache keeps has no `raw`.
        let kept = String(decoding: try JSON.encoder.encode(it), as: UTF8.self)
        #expect(!kept.contains("secret") && !kept.contains("raw"))
        // A malformed refusal doesn't drop the item; with no words the text is the explanation, else the subtype.
        let odd = try JSON.decoder.decode(Item.self, from: Data(#"{"id":"o","seq":3,"role":"system","kind":"notice","text":"x","model_refusal":"nope"}"#.utf8))
        #expect(odd.modelRefusal?.isFallback == false)
        #expect(ModelRefusal(subtype: "model_refusal", explanation: "说明").publicText == "说明")
        #expect(ModelRefusal(subtype: "model_refusal", text: "  ").publicText == "model_refusal")
    }

    @Test func theRefusalIsALineAfterTheExchangeNotAWorkStep() {
        var notice = item(2, .notice, role: "system", text: "Claude Code is unable…")
        notice.modelRefusal = ModelRefusal(subtype: ModelRefusal.fallbackSubtype, originalModel: "claude-opus-5-5", fallbackModel: "claude-sonnet-5-5")
        let blocks = ChatStore.rebuild([item(1, .text, role: "user", text: "做一下"), notice,
                                        item(3, .text, role: "assistant", text: "API Error: 404 model not available")], settled: nil).blocks
        guard case .turn(let t)? = blocks.first else { Issue.record("no turn: \(blocks)"); return }
        #expect(t.trailing.map(\.id) == [ChatRow.notice(notice).id])
        #expect(!t.process.contains { $0.id == ChatRow.notice(notice).id })
    }

    @Test func theRefusalsFactsInOrder() {
        let r = ModelRefusal(subtype: ModelRefusal.fallbackSubtype, originalModel: "claude-opus-5-5", fallbackModel: "claude-sonnet-5-5",
                             category: "cyber", scope: "session", explanation: "上游拒绝了")
        #expect(ModelFallbackText.refusalFacts(r) == ["Claude Code 从 Opus 5.5 自动改用 Sonnet 5.5", "本会话之后的轮次可能继续用它",
                                                      "类别：cyber", "说明：上游拒绝了"])
        #expect(ModelFallbackText.refusalFacts(ModelRefusal(subtype: "model_refusal")) == ["记录里没有说改用了其他模型"])
        #expect(ModelFallbackText.words(r) == nil)
    }

    @Test func aTerminalSessionsCardHasOnlyTheModelStep() throws {
        let s = try session(#","model_fallback":\#(refusal),"turn_error":"API Error: 404","turn_error_code":"model_fallback_unavailable""#)
        #expect(s.turnErrorCode == ModelFallbackText.unavailableCode)
        let card = try #require(ModelFallbackText.card(s, headless: false))
        #expect(card.title == "上游拒绝后，这个会话改用了回退模型" && !card.offersRestore)
        #expect(card.lines == ["原请求模型：Opus 5.5", "回退模型：Sonnet 5.5", "之后的错误：API Error: 404",
                               "终端里的会话请在 Claude Code 里自己输入 /model 换模型；它可能同时改掉新会话的默认模型。"])
    }

    @Test func aPersonaConversationGoesBackAndThenOnlyWaits() throws {
        let s = try session(#","assistant":true,"model_fallback":\#(refusal)"#)
        let card = try #require(ModelFallbackText.card(s, headless: true))
        #expect(card.offersRestore && card.lines.last == "不会重发任何消息；同样的请求可能再次被拒绝。")
        let pending = try session(#","assistant":true,"model_fallback":\#(refusal),"model_restore":{"state":"next_start","to":"claude-opus-5-5","at":"2026-11-12T00:00:00Z"}"#)
        let waiting = try #require(ModelFallbackText.card(pending, headless: true))
        #expect(waiting.title == "下次启动将使用配置的模型" && !waiting.offersRestore)
        #expect(waiting.lines.contains("配置的模型：Opus 5.5"))
        #expect(waiting.lines.contains("要等这个会话的下一次回复，才知道实际用的是哪个模型。"))
        // No fallback model named, or none at all: no card.
        #expect(ModelFallbackText.card(try session(#","model_fallback":{"subtype":"model_refusal_fallback"}"#), headless: true) == nil)
        #expect(ModelFallbackText.card(try session(""), headless: true) == nil)
    }

    @Test func aRefusedGoingBackIsSaidInWords() {
        func refused(_ code: String) -> String {
            ModelFallbackText.restoreError(APIError(status: 409, message: "hub words", fields: ["code": code]))
        }
        #expect(refused("restore_busy") == "这个对话正在工作，没有关闭任何东西。等它停下再试。")
        #expect(refused("restore_nothing") == "这个会话已经没有在用回退模型了。")
        #expect(refused("node_outdated") == "这台电脑上的 personad 太旧，还不会回到配置的模型，需要更新。")
        #expect(refused("restore_gone") == "hub words")
    }
}
