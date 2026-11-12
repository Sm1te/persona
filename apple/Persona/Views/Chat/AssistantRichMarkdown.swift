import Foundation
import MarkdownUI
import PersonaKit
import SwiftUI

/// Splits only genuine assistant fences. All other Markdown, including code examples, keeps its normal renderer.
struct AssistantRichMarkdown: View {
    var text: String
    var itemID: String
    var memo: AssistantRowText
    var memoKey: String
    @Environment(\.assistantCardConversation) private var conversation
    @Environment(AppModel.self) private var app

    private struct Part: Identifiable {
        var id: String
        var markdown: String?
        var block: AssistantViewBlocks.Block?
    }
    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantRichMarkdown")
        #endif
        let parts = pieces
        if conversation == nil || parts.isEmpty {
            LinkedMarkdown(plain: memo.markdown, paths: memo.paths, key: memoKey)
        } else {
            VStack(alignment: .leading, spacing: Theme.Space.sm) {
                ForEach(parts) { part in
                    if let block = part.block, let conversation {
                        card(block, conversation: conversation)
                    } else if let markdown = part.markdown, !markdown.isEmpty {
                        let source = Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply(markdown))))
                        LinkedMarkdown(plain: MarkdownContent(source), paths: PathMarkdown(source), key: memoKey + "/" + part.id)
                    }
                }
            }
        }
    }
    private var pieces: [Part] {
        let blocks = AssistantViewBlocks.parse(text)
        guard !blocks.isEmpty else { return [] }
        var parts: [Part] = [], position = text.startIndex
        for block in blocks {
            if position < block.range.lowerBound {
                parts.append(Part(id: "text-\(block.ordinal)", markdown: String(text[position..<block.range.lowerBound])))
            }
            parts.append(Part(id: "view-\(block.ordinal)", block: block))
            position = block.range.upperBound
        }
        if position < text.endIndex { parts.append(Part(id: "tail", markdown: String(text[position...]))) }
        return parts
    }
    @ViewBuilder private func card(_ block: AssistantViewBlocks.Block, conversation: SessionKey) -> some View {
        let location = AssistantCardLocation(key: itemID + "/" + String(block.ordinal),
            scope: AssistantViewScope(group: conversation.group, conversation: conversation.sessionID))
        if block.closed, block.ordinal < AssistantViewBlocks.maxBlocks,
           block.content.utf8.count <= AssistantViewBlocks.maxContentBytes,
           let view = Self.decode(block.content) {
            InteractiveAssistantViewCard(view: view,
                registered: app.features(conversation.group).contains("assistant_views") && AssistantViewsStore.shared.isRegistered(location.key, scope: location.scope),
                location: location, deps: .live(location))
        } else {
            Text(AssistantCardText.replacingCards(String(text[block.range])))
                .font(Theme.Font.body).textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
                .card(padding: Theme.Space.m)
                .accessibilityIdentifier("assistant-view-fallback")
        }
    }
    static func decode(_ text: String) -> ExtensionView? {
        guard let data = text.data(using: .utf8), data.count <= AssistantViewBlocks.maxContentBytes,
              let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let fallback = json["fallback"] as? String, !fallback.isEmpty, fallback.count <= 2000 else { return nil }
        let view: ExtensionView
        if json["sections"] != nil {
            guard let decoded = try? JSON.decoder.decode(ExtensionView.self, from: data) else { return nil }
            view = decoded
        } else {
            guard json["type"] is String, let component = try? JSON.decoder.decode(ExtensionComponent.self, from: data) else { return nil }
            view = ExtensionView(sections: [ExtensionSection(items: [component])], fallback: fallback)
        }
        return supports(view) ? view : nil
    }
    /// Knowing a generated wire type does not mean this UI version can draw it. HTML has its own verified host.
    static func supports(_ view: ExtensionView) -> Bool {
        guard (view.pages?.count ?? 0) <= 8 else { return false }
        for page in [view] + Array((view.pages ?? [:]).values) {
            guard page.sections.flatMap(\.items).count <= 300 else { return false }
            for item in page.sections.flatMap(\.items) {
                switch item {
                case .unknown: return false
                case .field(let field) where field.kind == "secret": return false
                case .html: continue
                default:
                    guard (ExtensionComponent.since[item.type] ?? Int.max) <= ExtensionUIContext.version else { return false }
                }
            }
        }
        return true
    }
}

extension AssistantViewModel.Deps {
    /// Uses only the protocol's generated inputs and restores snapshots from this card's own hub group.
    static func live(_ location: AssistantCardLocation) -> Self {
        .init(send: { submission in
            guard let api = AppModel.shared.api(location.scope.group) else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            if let text = submission.htmlText {
                let input = ExtensionAssistantHTMLActionInput(kind: "reply", componentKey: submission.action, page: submission.page,
                    expectedVersion: submission.version, text: text, clientMsgId: submission.clientMsgID, confirmed: submission.confirmed)
                return try await api.assistantHTMLReply(conversation: location.scope.conversation, key: location.key, input: input)
            }
            let input = ExtensionViewActionInput(action: submission.action, page: submission.page,
                values: submission.values.isEmpty ? nil : submission.values, clientMsgId: submission.clientMsgID,
                confirmed: submission.confirmed, expectedVersion: submission.version)
            return try await api.assistantViewAction(conversation: location.scope.conversation, key: location.key, input: input)
        }, recover: {
            let store = AssistantViewsStore.shared, epoch = AssistantViewsStore.shared.epoch
            guard let api = AppModel.shared.api(location.scope.group), let feed = try? await api.feed(conversation: location.scope.conversation),
                  store.epoch == epoch else { return }
            store.receive(feed.views ?? [:], conversation: location.scope.conversation, group: location.scope.group)
            store.receiveActions(feed.viewActions ?? [:], conversation: location.scope.conversation, group: location.scope.group)
            store.receiveBlocks(feed.viewBlocks ?? [], conversation: location.scope.conversation, group: location.scope.group)
        }, htmlOpen: { input in
            guard let api = AppModel.shared.api(location.scope.group) else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
            return try await api.assistantHTMLOpen(conversation: location.scope.conversation, key: location.key, input: input)
        })
    }
}
