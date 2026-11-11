import BeautifulUI
import PersonaKit
import SwiftUI

// Quotes, as Telegram's replies (#986): what goes with the next message sits in the composer's box over the field — a
// rule, who or what is quoted, a line or two of it, × — and the sent bubble opens with the same block, which leads
// back to what it quotes. Every quote is a `ContextRef` (`<persona-context>` before the message); the hub's own
// context of a Feishu /agent request (instructions, documents) is not a quote and stays folded (`ContextGroup`).

nonisolated extension ContextRef {
    /// Quote on a message of this chat: who wrote it (「我」 for the user's own, as the agent reads it), its id, and the
    /// part quoted (`selectionPart`, or nil for the whole message).
    static func quote(_ text: String, from: String, msg: String, part: String? = nil) -> ContextRef {
        ContextRef(kind: "quote", title: from, url: "", text: text, msg: msg, part: part)
    }

    /// `part` of a quote of words picked in a message (Select Text › Quote, #4549).
    static let selectionPart = "selection"

    /// `refs` with `ref` added, or nil when it is there already: a message goes over the box once as a whole and once
    /// per picked part (a message without an id every time).
    static func adding(_ ref: ContextRef, to refs: [ContextRef]) -> [ContextRef]? {
        let sameKind = ref.kind == "quote" || ref.kind == ForwardMessage.kind
        let there = sameKind && !ref.msg.isEmpty && refs.contains {
            $0.kind == ref.kind && $0.msg == ref.msg && $0.part == ref.part && (ref.part == nil || $0.text == ref.text)
        }
        return there ? nil : refs + [ref]
    }

    /// What the user picked to go with a message (a message, a page's text, a session, a recording…), shown as a quote.
    var isQuote: Bool { ["quote", "context", "edit", ForwardMessage.kind].contains(kind) }

    var isForward: Bool { kind == ForwardMessage.kind }

    /// A message's contexts as quotes, or nil when the hub put them there (a Feishu /agent request): those fold.
    static func quotes(_ refs: [ContextRef]) -> [ContextRef]? {
        refs.isEmpty || !refs.allSatisfy(\.isQuote) ? nil : refs
    }

    /// An @-picked session as context (not a forward, whose link names its message's session too).
    var isSession: Bool {
        guard kind != ForwardMessage.kind, let components = URLComponents(string: url) else { return false }
        return HubNaming.accepts(scheme: components.scheme) && components.host == "session" && components.path.hasPrefix("/")
    }

    #if DEBUG
    /// `-showSampleQuotes YES`: what Add as Context on a page and @ on a session put over the box.
    static let samples = [
        ContextRef(kind: "context", title: "WidgetKit 时间线", url: "https://example.com/docs/widgetkit/timeline", // i18n-ignore
                   text: "时间线里的每一项都有一个日期，系统按这个日期换上新的内容；刷新策略决定时间线用完以后什么时候再要一条。"), // i18n-ignore
        ContextRef(kind: "context", title: "照片同步支持 HEIC", url: "persona://session/studio/a1f3c2d4-0000-4000-8000-000000000005", // i18n-ignore
                   text: "另一个会话：照片同步支持 HEIC（Claude）\n目录：~/code/photo-sync\n它最近说：计划写好了，等你批准后开始改。"), // i18n-ignore
    ]
    #endif

    /// The page quoted as a whole: the text is the marker the link sheet puts there, not the user's words.
    private var isWholePage: Bool { text == "（整篇）" } // i18n-ignore: marker

    /// The quote's first line: whom or what it quotes.
    var quoteHeading: String {
        switch kind {
        case "quote":
            let who = title.isEmpty ? String(localized: "Messages [Quote Views 4da199]") : title
            if part == Self.selectionPart { return String(localized: "Quoting selected text · \(who)") }
            let mine = title == "我" // i18n-ignore: the sender name Lark gives the user's own messages
            if mine { return String(localized: "Quoting your message") }
            return String(localized: "Quoting \(who)")
        case "edit": return String(localized: "Text to change · \(label)")
        case ForwardMessage.kind: return ForwardMessage.heading(self)
        default:
            if isSession { return String(localized: "Quoting session \(label)") }
            if !url.isEmpty { return isWholePage ? String(localized: "Quoting \(label)") : String(localized: "Quoting selected text · \(label)") }
            return String(localized: "Quoting \(label)")
        }
    }

    /// A line or two of what is quoted, its line breaks as spaces.
    var quoteSummary: String {
        if isWholePage { return host }
        var flat = text.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        if isSession, flat.count > 1 { flat.removeFirst() } // 「另一个会话：…」: the heading names it already
        let joined = flat.joined(separator: " ")
        return joined.isEmpty ? String(localized: "No content") : String(joined.prefix(240))
    }

    /// A forward's words in its card: its own lines, blank ones dropped.
    var forwardSummary: String {
        let lines = text.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        return lines.isEmpty ? String(localized: "No content") : String(lines.joined(separator: "\n").prefix(1200))
    }
}

extension Theme {
    /// Quotes' colors (`QuoteBar`, `QuoteBlock`).
    enum Quote {
        /// The block's fill under its rule and words: a tint of the rule's color.
        static let fillOpacity = 0.12
        /// The quoted words on a colored bubble: its words' color, a little quieter.
        static let summaryOnColor = 0.82
        /// The band left on the message a quote led back to, as it starts to fade.
        static let flashOpacity = 0.18

        /// The rule and the heading in a sent bubble: on a colored bubble (white words) its words' color, the accent
        /// on a pale one.
        static var tint: Color {
            StyleSpec.current.deepensBubble ? Theme.userBubbleText : Theme.accent
        }

        static var summary: Color {
            StyleSpec.current.deepensBubble ? Theme.userBubbleText.opacity(summaryOnColor) : .primary
        }
    }
}

/// The rule, the heading and the summary: one quote's words, in a bar over the box or in a bubble.
private struct QuoteLines: View {
    var heading: String
    /// A forward's arrow before the heading (#3807).
    var icon: String? = nil
    /// A forward's computer and time, under the heading.
    var meta: String? = nil
    var summary: String
    var lines: Int
    /// A forward's route over the box: where it comes from and where it goes.
    var note: String? = nil
    var tint: Color
    var ink: Color
    /// The rule is drawn by the block's own shape (BeautifulUI's card), not beside the words.
    var rule = true

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            if rule { Capsule().fill(tint).frame(width: Theme.Size.bar) }
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(spacing: Theme.Space.xs) {
                    if let icon { ThemedIcon(icon).font(Theme.Font.captionStrong).foregroundStyle(tint).accessibilityHidden(true) }
                    Text(heading).font(Theme.Font.captionStrong).foregroundStyle(tint).lineLimit(1).truncationMode(.tail)
                }
                if let meta { Text(meta).font(Theme.Font.label).foregroundStyle(ink).lineLimit(1) }
                Text(summary).font(Theme.Font.caption).foregroundStyle(ink).lineLimit(lines).truncationMode(.tail)
                if let note {
                    // Two lines: a computer's and an Agent's names are both in it, and a cut in the middle hides one.
                    Text(note).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2).truncationMode(.tail)
                        .accessibilityIdentifier("composer.forwardRoute")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .fixedSize(horizontal: false, vertical: true)
    }
}

/// What goes with the next message, over the composer's field inside its box: a bar for each quote, stacked; × drops
/// one. One quote shows two lines of itself, several one line each.
struct QuoteBar: View {
    @Binding var refs: [ContextRef]
    /// Feishu's pages draw their own blue; elsewhere the style's accent.
    var tint: Color?

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ForEach(refs) { r in
                QuoteBarRow(heading: r.quoteHeading, summary: r.quoteSummary, lines: refs.count == 1 ? 2 : 1, tint: tint,
                            icon: r.isForward ? Theme.Icon.forwardMessage : nil, note: r.route,
                            removeLabel: r.isForward ? String(localized: "Remove Forward") : String(localized: "Remove Quote")) {
                    withAnimation(Theme.Motion.state) { refs.removeAll { $0.id == r.id } }
                }
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("composer.quotes")
    }
}

/// One quote over the box: its words and ×. Liquid glass and the other styles draw Telegram's bar (a rule and the
/// words on the box itself); BeautifulUI a quiet inset card with the rule along its edge.
struct QuoteBarRow: View {
    var heading: String
    var summary: String
    var lines = 2
    var tint: Color?
    var icon: String? = nil
    /// A forward's route (`ContextRef.route`), under its words.
    var note: String? = nil
    /// What × says to VoiceOver (Feishu's reply: Cancel Reply).
    var removeLabel = String(localized: "Remove Quote")
    var remove: () -> Void
    @Environment(\.buiTheme) private var theme

    var body: some View {
        let bui = AppStyle.current.usesBeautifulUI
        let tint = tint ?? (bui ? theme.colors.accent : Theme.accent)
        HStack(spacing: Theme.Space.xs) {
            QuoteLines(heading: heading, icon: icon, summary: summary, lines: lines, note: note, tint: tint,
                       ink: bui ? theme.colors.ink2 : .secondary, rule: !bui)
                .padding(.vertical, bui ? Theme.Space.s : Theme.Space.xxs)
                .padding(.leading, bui ? Theme.Space.m : 0)
            Button(action: remove) {
                ThemedIcon(Theme.Icon.close).font(Theme.Font.labelStrong)
                    .foregroundStyle(bui ? theme.colors.ink3 : Color.secondary)
                    .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(removeLabel)
            .accessibilityIdentifier(removeLabel)
        }
        .background {
            if bui {
                RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).fill(theme.colors.inset)
                    .overlay(alignment: .leading) { Rectangle().fill(tint).frame(width: Theme.Size.bar) }
                    .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
            }
        }
        .accessibilityElement(children: .contain)
    }
}

extension EnvironmentValues {
    /// A quote's block in a sent bubble was tapped: the chat brings back what it quotes (the message, highlighted) and
    /// says whether it could; when not, the block opens to the whole text.
    @Entry var openQuote: ((ContextRef) -> Bool)? = nil
}

/// A quote at the top of a sent bubble: the same words as over the box, on a tint of the bubble. A tap leads back to
/// the message it quotes (or opens the page it is from); with nothing to go back to, it opens to the whole text.
struct QuoteBlock: View {
    var ref: ContextRef
    @Environment(\.openQuote) private var openQuote
    @Environment(\.openLink) private var openLink
    @State private var open = false

    /// A forward shows this many lines of the original until it is opened.
    static let forwardLines = 4

    var body: some View {
        if ref.isForward {
            QuoteCard(heading: ref.quoteHeading, icon: Theme.Icon.forwardMessage, meta: ForwardMessage.meta(ref),
                      summary: open ? ref.text : ref.forwardSummary, lines: open ? .max : Self.forwardLines,
                      tint: Theme.Quote.tint, ink: Theme.Quote.summary, action: tap)
                .accessibilityHint(String(localized: "Opens the original message"))
                .accessibilityIdentifier("bubble.forward")
        } else {
            QuoteCard(heading: ref.quoteHeading, summary: open ? ref.text : ref.quoteSummary, lines: open ? .max : 2,
                      tint: Theme.Quote.tint, ink: Theme.Quote.summary, action: tap)
                .accessibilityHint(ref.msg.isEmpty ? "" : String(localized: "Jumps to the original message"))
        }
    }

    private func tap() {
        Haptics.tap()
        if openQuote?(ref) == true { return }
        if !ref.isSession, let u = URL(string: ref.url), u.scheme?.hasPrefix("http") == true, let openLink {
            openLink(u)
            return
        }
        withAnimation(Theme.Motion.state) { open.toggle() }
    }
}

/// A quote's block in a bubble, as a button: liquid glass and the other styles draw Telegram's (a rule beside the
/// words on a tint), BeautifulUI a card with the rule along its edge.
struct QuoteCard: View {
    var heading: String
    var icon: String? = nil
    var meta: String? = nil
    var summary: String
    var lines = 2
    var tint: Color
    var ink: Color
    var action: () -> Void

    var body: some View {
        let bui = AppStyle.current.usesBeautifulUI
        let shape = RoundedRectangle(cornerRadius: bui ? Theme.Radius.control : Theme.Radius.badge, style: .continuous)
        Button(action: action) {
            QuoteLines(heading: heading, icon: icon, meta: meta, summary: summary, lines: lines, tint: tint, ink: ink, rule: !bui)
                .padding(.vertical, Theme.Space.sm)
                .padding(.leading, bui ? Theme.Space.m : Theme.Space.s)
                .padding(.trailing, Theme.Space.m)
                .background {
                    shape.fill(tint.opacity(Theme.Quote.fillOpacity))
                        .overlay(alignment: .leading) {
                            if bui { Rectangle().fill(tint).frame(width: Theme.Size.bar) }
                        }
                        .clipShape(shape)
                }
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel([heading, meta, summary].compactMap { $0 }.joined(separator: ": "))
        .accessibilityIdentifier("bubble.quote")
    }
}

/// The band a quote's tap leaves on the message it led back to, fading on its own.
struct QuoteFlash: View {
    @State private var on = true

    var body: some View {
        Rectangle().fill(Theme.accent.opacity(on ? Theme.Quote.flashOpacity : 0))
            .allowsHitTesting(false)
            .task {
                try? await Task.sleep(for: .milliseconds(700))
                withAnimation(Theme.Motion.layout) { on = false }
            }
    }
}

/// A column as wide as its widest part at its own size (at most what it is offered), every part laid out at that
/// width: a bubble with a quote is as wide as the longer of the two, the quote's block across all of it.
struct WidestColumn: Layout {
    var spacing: CGFloat = 0

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = width(proposal, subviews)
        let heights = subviews.map { $0.sizeThatFits(ProposedViewSize(width: width, height: nil)).height }
        return CGSize(width: width, height: heights.reduce(0, +) + spacing * CGFloat(max(subviews.count - 1, 0)))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        let offer = ProposedViewSize(width: bounds.width, height: nil)
        for s in subviews {
            let h = s.sizeThatFits(offer).height
            s.place(at: CGPoint(x: bounds.minX, y: y), proposal: ProposedViewSize(width: bounds.width, height: h))
            y += h + spacing
        }
    }

    private func width(_ proposal: ProposedViewSize, _ subviews: Subviews) -> CGFloat {
        let ideal = subviews.map { $0.sizeThatFits(.unspecified).width }.max() ?? 0
        guard let offered = proposal.width else { return ideal }
        return min(ideal, offered)
    }
}

/// The words of the user's bubble under the quotes it opens with (none: the plain bubble as it always was).
struct UserBubbleBody: View {
    var quotes: [ContextRef]
    var text: String
    /// The paths in `text`: linked where they are there on the machine (`PathText`).
    var paths: [PathRef] = []
    /// The comment blocks the message led with (`MessageComments.inBubble`), and the plain quote before them.
    var comments: [MessageComment] = []
    var lead: String?
    /// The new conversation layout (#1666, `ChatLayout`): the user's box is pale blue under the primary text color.
    @Environment(\.chatLayout) private var layout

    var body: some View {
        if quotes.isEmpty, comments.isEmpty, lead == nil {
            words.padding(.horizontal, Theme.Space.l).padding(.vertical, Theme.Space.m)
                .background(fill, in: .rect(cornerRadius: Theme.Radius.bubble, style: .continuous))
        } else {
            quoted
        }
    }

    private var fill: AnyShapeStyle { layout.on ? AnyShapeStyle(Theme.ChatBox.user) : Theme.userBubbleFill }

    private var words: some View {
        PathText(text: text, refs: paths, link: layout.on ? Theme.accent : Theme.userBubbleLink)
            .font(Theme.Font.body).lineHeight(.body).foregroundStyle(layout.on ? Color.primary : Theme.userBubbleText)
            .selectableInLift(text, as: .plain(layout.on ? Color.primary : Theme.userBubbleText)) // #603
            #if os(macOS)
            .textSelection(.enabled) // drag to select and ⌘C, as on the web (#2542)
            #endif
    }

    private var quoted: some View {
        WidestColumn {
            if !quotes.isEmpty || !comments.isEmpty || lead != nil {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    ForEach(quotes) { QuoteBlock(ref: $0) }
                    if let lead { QuoteBlock(ref: .quote(lead, from: "", msg: "")) }
                    ForEach(Array(comments.enumerated()), id: \.offset) { i, c in CommentQuote(comment: c, number: i + 1) }
                }
                .padding(.horizontal, Theme.Space.s)
                .padding(.top, Theme.Space.s)
                .padding(.bottom, text.isEmpty ? Theme.Space.s : 0)
            }
            if !text.isEmpty {
                words
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, Theme.Space.l)
                    .padding(.vertical, Theme.Space.m)
            }
        }
        .background(fill, in: .rect(cornerRadius: Theme.Radius.bubble, style: .continuous))
    }
}

/// Which line to light up after a quote's tap; a new mark each tap, so tapping again lights it again.
struct QuoteFlashMark: Equatable {
    var line: String
    var id = UUID()
}

/// Plain text (a message you sent) with the paths that are there on the conversation's machine as underlined links
/// (docs/apple/ios.md「File viewer」); a tap opens the file viewer at the path's line (`PathLinkURL`, the chat's `openURL`). Until
/// the machine answers, and outside a chat, the text as it is: the same words in the same place.
struct PathText: View {
    @Environment(AppModel.self) private var model: AppModel?
    @Environment(\.fileScope) private var scope
    var text: String
    var refs: [PathRef]
    var link: Color

    var body: some View {
        if let linked {
            Text(linked).tint(link)
        } else {
            Text(text)
        }
    }

    private var linked: AttributedString? {
        // Only a message with paths reads the presence (and is drawn again when an answer comes).
        guard !refs.isEmpty, let model, let scope else { return nil }
        let picked = PathRefs.pick(refs) { model.filePresence.linkable($0, in: scope) }
        return picked.isEmpty ? nil : Self.attributed(text, picked, link: link)
    }

    /// `text` with `refs` (in order, none overlapping) as links.
    static func attributed(_ text: String, _ refs: [PathRef], link: Color) -> AttributedString {
        let ns = text as NSString
        var out = AttributedString()
        var at = 0
        for r in refs where r.range.location >= at {
            guard let url = PathLinkURL.make(r.path, line: r.line) else { continue }
            out += AttributedString(ns.substring(with: NSRange(location: at, length: r.range.location - at)))
            var a = AttributedString(ns.substring(with: r.range))
            a.link = url
            a.foregroundColor = link
            a.underlineStyle = .single
            out += a
            at = NSMaxRange(r.range)
        }
        out += AttributedString(ns.substring(from: at))
        return out
    }
}
