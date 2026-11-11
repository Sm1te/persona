import SwiftUI
import MarkdownUI

/// A relay or automated update in the existing single conversation. Only the first row of a source run names it;
/// no avatars, participant lists, or group-chat chrome. iOS and macOS use exactly the same provenance component.
struct SourceMessage: View, Equatable {
    var item: Item
    var source: MessageSource
    var showsSource = true
    /// A scheduled task's or a follow-up's prompt stays hidden: only its one-line notice shows (`ChatLines.hidesPrompt`).
    var hidesPrompt = false
    @State private var open = false
    @State private var fullText: String?
    @State private var loadError: String?
    @Environment(\.loadFullText) private var loadFullText
    @Environment(\.rowMemoScope) private var memoScope
    @Environment(\.chatPin) private var pin

    nonisolated static func == (a: Self, b: Self) -> Bool {
        a.item == b.item && a.source == b.source && a.showsSource == b.showsSource && a.hidesPrompt == b.hidesPrompt
    }

    private var pinKey: String { "source-" + item.id }
    private var rawText: String {
        if hidesPrompt { return Injected.of(item)?.line ?? "" }
        guard let fullText else { return MessageSource.body(item) }
        var whole = item
        whole.text = fullText
        whole.fullSize = nil
        whole.injected = nil
        return MessageSource.body(whole)
    }
    private var text: String { ContextRef.parse(rawText).rest }
    private var folds: Bool {
        guard !hidesPrompt else { return false }
        return item.fullSize != nil || text.count > Theme.MessageSource.foldCharacters || text.split(separator: "\n").count > Theme.MessageSource.previewLines
    }

    var body: some View {
        content
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.trailing, Theme.Space.xxl)
            .chatPinned(pinKey)
            .messageActionBar(id: item.id, text: text, isUser: false, date: item.ts, from: source.mention)
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("message-source-" + item.id)
            .task(id: open) {
                guard open, item.fullSize != nil, fullText == nil, let loadFullText else { return }
                do {
                    let loaded = try await loadFullText(item.id)
                    guard !Task.isCancelled, open else { return }
                    toggleInPlace(pin, pinKey, animation: Theme.Motion.state) { fullText = loaded; loadError = nil }
                } catch {
                    guard !Task.isCancelled, open else { return }
                    loadError = error.shown
                }
            }
    }

    private var content: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if showsSource {
                Text(verbatim: source.mention)
                    .font(Theme.Font.captionStrong)
                    .foregroundStyle(Theme.accent)
                    .lineLimit(1)
                    .truncationMode(.middle)
                    .accessibilityHint(source.origin ?? "")
                    .accessibilityIdentifier("message-source-label")
            }
            let refs = ContextRef.parse(rawText).refs
            if !refs.isEmpty { ContextGroup(refs: refs, key: pinKey) }
            if folds && !open {
                Text(text)
                    .font(source.automated ? Theme.Font.secondary : Theme.Font.body)
                    .foregroundStyle(.primary)
                    .lineLimit(Theme.MessageSource.previewLines)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityIdentifier("message-source-preview")
            } else if !hidesPrompt || !text.isEmpty {
                let d = RowMemo.assistant(RowMemo.key(memoScope, pinKey + (hidesPrompt ? "-line" : "") + (fullText == nil ? "" : "-full"))) { _ in AssistantRowText(text) }
                Markdown(d.markdown)
                    .markdownSoftBreakMode(.lineBreak)
                    .markdownTheme(.persona)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    #if os(macOS)
                    .textSelection(.enabled)
                    #endif
                    .accessibilityIdentifier("message-source-body")
            }
            if open, item.fullSize != nil, fullText == nil {
                if let loadError {
                    Text(loadError).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                } else if loadFullText != nil {
                    ProgressView().controlSize(.small)
                }
            }
            if folds {
                Button {
                    toggleInPlace(pin, pinKey, animation: Theme.Motion.state) { open.toggle() }
                } label: {
                    Text(open ? String(localized: "Collapse") : String(localized: "Expand"))
                        .font(Theme.Font.captionStrong)
                        .lineLimit(1)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Theme.accent)
                .accessibilityIdentifier("message-source-toggle-" + item.id)
            }
        }
        .padding(Theme.Space.m)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
    }
}
