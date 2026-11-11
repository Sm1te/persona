import MarkdownUI
import PersonaKit
import SwiftUI

/// A reply's fenced code block as Telegram draws it (#2984): a title bar on top with the fence's language (none when
/// it names none) and Copy, which takes the whole block; the code under it scrolls sideways when it is wide, so a
/// sideways drag on it still goes to the code.
struct ReplyCodeBlock: View {
    var config: CodeBlockConfiguration

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ReplyCodeBlock")
        #endif
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                if let language = config.language?.trimmingCharacters(in: .whitespaces), !language.isEmpty {
                    Text(verbatim: language).font(Theme.Font.labelMedium).foregroundStyle(.secondary).lineLimit(1)
                }
                Spacer(minLength: Theme.Space.s)
                Button { CodeCopy.copy(config.content, said: String(localized: "Code copied")) } label: {
                    HStack(spacing: Theme.Space.xs) {
                        ThemedIcon(Theme.Icon.copy)
                        Text("Copy [Message Menu 63d90d]").lineLimit(1)
                    }
                    .font(Theme.Font.label)
                    .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .foregroundStyle(.secondary)
                .accessibilityLabel(String(localized: "Copy Code [Message Views fa2611]"))
                .accessibilityIdentifier("code-block-copy")
            }
            .padding(.horizontal, Theme.Space.m)
            // The bar is a label's height; Copy's 44 pt reach past its edges (nothing is clipped, it is drawn over the code).
            .padding(.vertical, -Theme.Space.sm)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.codeBar, in: UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.control,
                                                                  topTrailingRadius: Theme.Radius.control, style: .continuous))
            .oneBlockInLift() // not the message's words: lifted, the text view over them has no lines for it (#3188)
            .zIndex(1)
            ScrollView(.horizontal, showsIndicators: false) {
                config.label
                    .relativeLineSpacing(.em(0.2))
                    .markdownTextStyle { FontFamilyVariant(.monospaced); FontSize(.em(Theme.Level.code.size / Theme.Level.body.size)) }
                    // The style's font design (RootView's `fontDesign`) won over the monospaced variant: the code was
                    // drawn in the body's font.
                    .fontDesign(.monospaced)
                    .padding(Theme.Space.m)
            }
            .background(Theme.codeBackground, in: UnevenRoundedRectangle(bottomLeadingRadius: Theme.Radius.control,
                                                                         bottomTrailingRadius: Theme.Radius.control, style: .continuous))
        }
    }
}

/// A reply's quote as Telegram draws it (#2984): the accent bar on the left of a faint accent card, a quote mark at its
/// top right. A long one is folded to a few lines, fading out: a tap on it opens it, its chevron folds it again. A
/// quote still being written is never folded (`replyStreaming`): it would fold and unfold as its words came.
/// Whether it is open and how tall its words are live outside the view (`QuoteFold`, by its message and its words), so
/// the copy a long press lifts shows it as the chat does from its first frame (#3188).
struct ReplyQuote: View {
    var config: BlockConfiguration
    @Environment(\.replyStreaming) private var streaming
    @Environment(\.replyKey) private var replyKey
    @ScaledMetric(relativeTo: .body) private var fold = Theme.Size.quoteFold
    /// The words' room on the right, clear of the quote mark and the chevron: they are drawn in the label's size, so it
    /// grows with them (at the largest text sizes a fixed room let the mark sit on the first line).
    @ScaledMetric(relativeTo: .caption) private var markRoom = Theme.Space.xl
    /// Its fold where it has no message (`replyKey`): its own.
    @State private var own = QuoteFold()
    /// Its words' height as this view measured it (it redraws when it changes); until then the fold's.
    @State private var height: CGFloat = 0

    var body: some View {
        let state = replyKey.isEmpty ? own : QuoteFold.of(replyKey, config.content.renderMarkdown())
        // Long enough to fold: more than a line past what a folded one shows.
        let folds = !streaming && (height > 0 ? height : state.height) > fold + Theme.Space.xl
        quote(state, folds: folds, folded: folds && !state.open)
    }

    private func quote(_ state: QuoteFold, folds: Bool, folded: Bool) -> some View {
        config.label
            .markdownTextStyle { ForegroundColor(.secondary) }
            .padding(.leading, Theme.Space.m)
            .padding(.trailing, markRoom)
            .padding(.vertical, Theme.Space.s)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: {
                height = $0
                state.height = $0
            }
            .frame(maxHeight: folded ? fold : nil, alignment: .top)
            .clipped()
            .mask {
                if folded {
                    VStack(spacing: 0) {
                        Color.black
                        LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom).frame(height: Theme.Space.xl)
                    }
                } else {
                    Color.black
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.leading, Theme.Size.bar)
            .background(Theme.quoteBackground)
            .overlay(alignment: .leading) { Theme.accent.frame(width: Theme.Size.bar) }
            .clipShape(.rect(cornerRadius: Theme.Radius.badge, style: .continuous))
            // Its lines past the fold are laid out but hidden: no lines of the words to select (`FoldedQuotes`).
            .anchorPreference(key: FoldedQuotes.self, value: .bounds) { folded ? [$0] : [] }
            .overlay(alignment: .topTrailing) {
                ThemedIcon(Theme.Icon.quoteMark).font(Theme.Font.label).foregroundStyle(Theme.accent).padding(Theme.Space.s)
                    .accessibilityHidden(true)
            }
            .overlay {
                if folded { // the folded quote is one button: it opens
                    Button { withAnimation(Theme.Motion.layout) { state.open = true } } label: { Color.clear.contentShape(.rect) }
                        .buttonStyle(.plain)
                        .accessibilityLabel(String(localized: "Expand"))
                }
            }
            .overlay(alignment: .bottomTrailing) {
                if folds {
                    Button { withAnimation(Theme.Motion.layout) { state.open.toggle() } } label: {
                        ThemedIcon(state.open ? Theme.Icon.collapse : Theme.Icon.expand)
                            .font(Theme.Font.label)
                            .foregroundStyle(Theme.accent)
                            .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                            .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(state.open ? String(localized: "Collapse") : String(localized: "Expand"))
                    .accessibilityIdentifier("quote-fold")
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("reply-quote")
    }
}

/// Copying code from a reply: a code block's Copy, a tap on inline code (`CodeCopyURL`, the chat's `openURL`).
enum CodeCopy {
    @MainActor static func copy(_ code: String, said: String) {
        var code = code
        while code.hasSuffix("\n") { code.removeLast() }
        UIPasteboard.general.string = code
        Haptics.success()
        AppModel.shared.toast = Toast(said, style: .success)
    }
}

/// A reply quote's fold (`ReplyQuote`): open or not, and its words' height as last measured. Kept by the quote's message
/// (`replyKey`, `RowMemo.key`: ids repeat across chats) and its words, so the chat's row and the copy a long press
/// lifts (drawn anew) show it the same, folded or open from the lift's first frame (#3188). MarkdownUI tells a block
/// nothing of where it is in its message: two quotes of one message with the same words open and close together.
@Observable final class QuoteFold {
    var open = false
    /// What a quote drawn anew starts from (its own measure redraws it): no view redraws when this changes.
    @ObservationIgnored var height: CGFloat = 0

    private static var all: [String: QuoteFold] = [:]

    static func of(_ message: String, _ words: String) -> QuoteFold {
        let key = message + "\u{0}" + words
        if let fold = all[key] { return fold }
        if all.count >= 2000 { all.removeAll(keepingCapacity: true) } // a long session's worth; then start over
        let fold = QuoteFold()
        all[key] = fold
        return fold
    }
}

extension EnvironmentValues {
    /// The reply under it is still being written (`StreamingText`): its quotes do not fold yet.
    @Entry var replyStreaming = false
    /// The message of the reply under it (`RowMemo.key`): what its quotes' folds are kept by (`QuoteFold`).
    @Entry var replyKey = ""
}
