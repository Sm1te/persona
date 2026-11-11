import BeautifulUI
import MarkdownUI
import PersonaKit
import SwiftUI

extension MarkdownUI.Theme {
    /// Reading-first markdown: system body text, roomy lists, quiet code blocks.
    /// The base size is the body level's (`Theme.Level.body`: 17 on the iPhone, 13 on the Mac, where a fixed 17 put
    /// replies and profiles a size above the user's bubble, #779); MarkdownUI scales it with Dynamic Type (a
    /// ScaledMetric relative to .body), so a reply and the user's bubble are one size at every setting. Headings follow
    /// `Theme.Font`: the first is the title level (1.3 em), the second and third the heading level (body size, semibold),
    /// told apart by the space above them, the rest a step smaller in a quieter ink (`minorHeading`); code blocks are the
    /// code level. Paragraphs have the body's line height (1.45: 0.26 em over SF's own 1.19). On the Mac, where
    /// ScaledMetric does not follow Small / Standard / Large, the base size is times `Theme.Font.scale` (#833) and the
    /// theme is made again when it changes (once on the iPhone).
    @MainActor static var persona: MarkdownUI.Theme { personaAtScale.value }
    @MainActor private static let personaAtScale = PerTextScale { makePersona() }

    @MainActor private static func makePersona() -> MarkdownUI.Theme {
        MarkdownUI.Theme()
            .text { FontSize(Theme.Level.body.size * Theme.Font.scale) }
            .code {
                #if os(macOS)
                FontFamilyVariant(.monospaced)
                FontSize(.em(ReplyMetrics.inlineCode))
                BackgroundColor(Theme.codeBackground)
                #else
                // In the body's color on light gray, tappable to copy (#2984, `CodeCopyMarkdown`): applied after a
                // link's style, so a copy link keeps it; a path that opens is linked as plain words and stays blue.
                // Monospaced as the code blocks are, which holds because the blocks give their runs their fonts (`runFonts`).
                FontFamilyVariant(.monospaced)
                FontSize(.em(ReplyMetrics.inlineCode))
                ForegroundColor(Theme.primaryText)
                BackgroundColor(Theme.inlineCodeBackground)
                #endif
            }
            .strong { FontWeight(.semibold) }
            .link { ForegroundColor(Theme.accent) }
            .heading1 { config in
                config.label.runFonts().markdownMargin(top: ReplyMetrics.heading1.top, bottom: ReplyMetrics.heading1.bottom)
                    .markdownTextStyle { FontWeight(.bold); FontSize(.em(ReplyMetrics.heading1.size)) }
            }
            .heading2 { config in
                config.label.runFonts().markdownMargin(top: ReplyMetrics.heading2.top, bottom: ReplyMetrics.heading2.bottom)
                    .markdownTextStyle { FontWeight(.semibold); FontSize(.em(1)) }
            }
            .heading3 { config in
                config.label.runFonts().markdownMargin(top: ReplyMetrics.heading3.top, bottom: ReplyMetrics.heading3.bottom)
                    .markdownTextStyle { FontWeight(.semibold); FontSize(.em(1)) }
            }
            .heading4 { config in minorHeading(config) }
            .heading5 { config in minorHeading(config) }
            .heading6 { config in minorHeading(config) }
            .paragraph { config in
                config.label.runFonts().fixedSize(horizontal: false, vertical: true).relativeLineSpacing(.em(ReplyMetrics.lineSpacing))
                    .markdownMargin(top: 0, bottom: ReplyMetrics.paragraphBottom)
            }
            .listItem { config in config.label.markdownMargin(top: .em(ReplyMetrics.itemTop)) }
            .codeBlock { config in
                // A card the assistant drew (persona-view, or the older persona-card, #886), drawn with the extensions'
                // components (docs/extensions/extensions.md「Assistant cards: `persona-view` and `persona-card`」); one the app cannot read stays a code block.
                if AssistantCardBlock.handles(config.language), let view = AssistantCardBlock.parse(language: config.language, config.content) {
                    AssistantViewCard(view: view).markdownMargin(top: ReplyMetrics.codeTop, bottom: ReplyMetrics.codeBottom)
                } else if let words = AssistantCardBlock.words(language: config.language, config.content) {
                    // A persona-card of a type this app does not know yet, or one missing what its type needs: its numbers in words, not its JSON.
                    AssistantCardWords(title: words.title, lines: words.lines)
                        .markdownMargin(top: ReplyMetrics.codeTop, bottom: ReplyMetrics.codeBottom)
                } else if let kind = RenderedFence.kind(config.language) {
                    // A formula or a diagram (docs/apple/ios.md「Math and diagrams」); the code block when it cannot be drawn.
                    RenderedBlock(kind: kind, source: config.content.trimmingCharacters(in: .whitespacesAndNewlines)) { plainCodeBlock(config) }
                        .markdownMargin(top: ReplyMetrics.codeTop, bottom: ReplyMetrics.codeBottom)
                } else {
                    // The Mac is frozen (docs/apple/mac.md): its code blocks keep their hover Copy.
                    #if os(macOS)
                    plainCodeBlock(config).markdownMargin(top: ReplyMetrics.codeTop, bottom: ReplyMetrics.codeBottom)
                    #else
                    ReplyCodeBlock(config: config).markdownMargin(top: ReplyMetrics.codeTop, bottom: ReplyMetrics.codeBottom)
                    #endif
                }
            }
            .blockquote { config in
                #if os(macOS)
                config.label
                    .padding(.leading, Theme.Space.m)
                    .overlay(alignment: .leading) { Rectangle().fill(Theme.accent.opacity(0.5)).frame(width: Theme.Size.bar) }
                    .markdownTextStyle { ForegroundColor(.secondary) }
                #else
                ReplyQuote(config: config)
                #endif
            }
            .table { config in FittedTable(config: config).oneBlockInLift() } // fits the width it gets; scrolls only when it cannot
            .tableCell { config in FittedTableCell(config: config).runFonts() }
    }
}

/// A block's words each keep their own font (#2984). The style's font design set on the root (RootView's `fontDesign`)
/// replaces every run's font with the system one of that design, a monospaced or named one included, so inline code was
/// drawn in the body font. Under a block that draws words the design is cleared and given to its runs as their family
/// instead (read from the style: SwiftUI has no public environment value for it), and inline code's monospaced variant
/// holds. The Mac is frozen: it keeps the root's design.
struct RunFonts: ViewModifier {
    func body(content: Content) -> some View {
        #if os(macOS)
        content
        #else
        content.markdownTextStyle { FontFamily(.system(StyleSpec.current.fontDesign)) }.fontDesign(nil)
        #endif
    }
}

extension View {
    /// The block's runs keep their fonts under the style's font design (`RunFonts`).
    func runFonts() -> some View { modifier(RunFonts()) }
}

extension MarkdownUI.Theme {
    /// A fenced code block: monospaced, scrolling sideways, on the code background.
    @MainActor static func plainCodeBlock(_ config: CodeBlockConfiguration) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            config.label
                .relativeLineSpacing(.em(ReplyMetrics.codeLineSpacing))
                .markdownTextStyle { FontFamilyVariant(.monospaced); FontSize(.em(Theme.Level.code.size / Theme.Level.body.size)) }
                .padding(Theme.Space.m)
        }
        .background(Theme.codeBackground, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
        .macHoverActions {
            MacHoverButton(title: String(localized: "Copy Code [Message Views fa2611]"), symbol: Theme.Icon.copy) { MacHover.copy(config.content, said: String(localized: "Code copied")) }
        }
    }

    /// H4–H6 (#3863): MarkdownUI's own were the bare text, so `#### Notes` read as the next paragraph's first line. A step
    /// below the third level (the secondary level's size), semibold in the quieter ink, more space above than below.
    /// Its label is built as heading1–3's are: a modifier theirs gains (the style's run fonts, say) belongs here too.
    @MainActor static func minorHeading(_ config: BlockConfiguration) -> some View {
        config.label.runFonts().markdownMargin(top: ReplyMetrics.heading4.top, bottom: ReplyMetrics.heading4.bottom)
            .markdownTextStyle {
                FontWeight(.semibold)
                FontSize(.em(ReplyMetrics.heading4.size))
                ForegroundColor(Theme.Reply.secondaryInk)
            }
    }
}

/// The fences drawn as pictures: ```math (also latex, tex, katex) and ```mermaid.
nonisolated enum RenderedFence {
    static func kind(_ language: String?) -> MarkdownRenderer.Kind? {
        switch language?.lowercased() {
        case MathMarkdown.fence, "latex", "tex", "katex": .displayMath
        case "mermaid": .mermaid
        default: nil
        }
    }
}

/// Offers its content at most `max` points of width (the whole `max` when the parent leaves width open), so text wraps
/// there and narrower content keeps its own size.
struct MaxWidthLayout: Layout {
    var max: CGFloat

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        guard let s = subviews.first else { return .zero }
        return s.sizeThatFits(.init(width: min(proposal.width ?? max, max), height: nil))
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: .init(width: bounds.width, height: bounds.height))
    }
}

extension EnvironmentValues {
    /// Opens a path mentioned in the conversation on the session's machine (set by ChatView).
    @Entry var openFile: ((String) -> Void)? = nil
}

extension EnvironmentValues {
    /// Every image in the conversation, in order: the viewer pages through all of them (set by ChatView).
    @Entry var chatImages: [ImagePayload] = []
    /// Puts an (edited) image into this chat's composer as an attachment for the next message (set by ChatView).
    @Entry var attachImage: ((UIImage) -> Void)? = nil
    /// Loads an image file on the session's machine (photos sent from the phone are saved there; set by ChatView).
    @Entry var loadImage: (@Sendable (String) async -> ImagePayload?)? = nil
    /// Fetches the whole text of an item the page cut (`Item.fullSize`), by item id (set by ChatView).
    @Entry var loadFullText: ((String) async throws -> String)? = nil
    /// Opens the full-screen image viewer from the chat page (set by ChatView). A viewer presented by the row it was
    /// tapped in went away with the row: the lazy list drops rows it moves away from, and it moves to every new step
    /// while you are at the newest end (2026-10-31: a screenshot in a working agent's steps closed itself).
    @Entry var viewImages: ((ImageViewing) -> Void)? = nil
    /// The chat page's namespace for the viewer's zoom from its thumbnail.
    @Entry var imageZoom: Namespace.ID? = nil
    /// Which tool calls are open (set by ChatView): like the work lines, kept by the page so a row the lazy list
    /// dropped and made again comes back the way it was.
    @Entry var openTools: OpenTools? = nil
}

/// The viewer to show: the images to page through, where to start, and the thumbnail it zooms from.
struct ImageViewing: Identifiable {
    let id = UUID()
    var images: [ImagePayload]
    var index: Int
    /// The conversation's images (the viewer's 「对话里的图片」), as they were when it opened.
    var conversation: [ImagePayload]
    var source: String
    /// A turn's images it looked at (`ViewedImagesRow`), with their names and files: shown instead of `images`.
    var viewed: [ViewerImage]? = nil
}

/// Tool calls opened or closed by the user, by call id; the rest follow their own default.
@Observable final class OpenTools {
    private var state: [String: Bool] = [:]
    func isOpen(_ id: String) -> Bool? { state[id] }
    func set(_ id: String, _ open: Bool) { state[id] = open }
}

struct UserBubble: View, Equatable {
    @Environment(\.rowMemoScope) private var memoScope
    var item: Item
    /// The extension that opened the conversation with this message (#3758): 「来自 飞书 /agent」 under it. Set by the chat
    /// page on the conversation's first message only.
    var external: AutoConversationSource? = nil
    /// Only the message itself: the chat page re-evaluates its body on every hub push, and without this every row on
    /// screen (and in the lazy list's buffer) ran its body again too (about 45 a push on a long Persona conversation).
    nonisolated static func == (a: Self, b: Self) -> Bool { a.item == b.item && a.external == b.external }
    @Environment(\.openFile) private var openFile
    @Environment(\.loadImage) private var loadImage

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("UserBubble")
        #endif
        let d = RowMemo.user(RowMemo.key(memoScope, item.id)) { _ in UserRowText(item.text ?? "") } // once per message
        let parsed = (refs: d.refs, rest: d.text)
        let shown = d.shown
        let images = d.images
        // What the user picked to go with it opens the bubble as quotes; the hub's own context folds above it.
        let quotes = ContextRef.quotes(parsed.refs) ?? []
        VStack(alignment: .trailing, spacing: Theme.Space.sm) {
            if quotes.isEmpty, !parsed.refs.isEmpty { ContextGroup(refs: parsed.refs, key: item.id) }
            if !images.isEmpty, let loadImage {
                AttachedImages(paths: images, load: loadImage)
            }
            if !d.attached.isEmpty { AttachedFileCards(files: d.attached) }
            if !shown.isEmpty || !quotes.isEmpty {
                HStack {
                    Spacer(minLength: 48)
                    UserBubbleBody(quotes: quotes, text: d.words, paths: d.pathRefs, comments: d.comments, lead: d.lead)
                        .messageLongPress(id: item.id) { UserBubbleBody(quotes: quotes, text: d.words, paths: d.pathRefs, comments: d.comments, lead: d.lead) }
                }
            }
            if let openFile { FileChips(paths: d.chips, open: openFile) }
            // Came in from a channel (WeChat, the Feishu bot…): 「来自 微信」 under it; an extension opened the conversation
            // with it: 「来自 飞书 /agent」.
            if ChannelSource.possible(item.clientMsgID) {
                ChannelSourceLine(clientMsgID: item.clientMsgID)
            } else if let external {
                ExternalSourceLine(source: external)
            }
            if d.cardSource != nil {
                Text("From a card").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                    .accessibilityIdentifier("assistant-card-source")
            }
        }
        .lookUpFiles(d.asked)
        .frame(maxWidth: .infinity, alignment: .trailing)
        .messageActionBar(id: item.id, text: shown, isUser: true, date: item.ts)
    }
}

extension UserBubble {
    /// `<attached_images>` (current), and the plain-text notes older builds appended (own last line / old wording).
    nonisolated private static let notePatterns = [#"\s*<attached_images>[\s\S]*?</attached_images>\s*"#,
                                       #"(?:\A|\n)\s*相关图片：[^\n]*\z"#, #"\s*（我从手机发来了图片，请用 Read 查看：[^）]*）\s*\z"#] // i18n-ignore

    /// What was typed: the note the phone appends for attached photos becomes thumbnails above the bubble instead.
    nonisolated private static let displayMemo = TextMemo<String>()
    nonisolated private static let pathsMemo = TextMemo<[String]>()

    nonisolated static func display(_ text: String) -> String { displayMemo(text, computeDisplay) }

    nonisolated private static func computeDisplay(_ text: String) -> String {
        var t = AttachedFile.parse(text).rest // the videos sent with it are cards
        for pattern in notePatterns {
            while let r = t.range(of: pattern, options: .regularExpression) { t.replaceSubrange(r, with: "\n") }
        }
        // Claude's placeholder for an image it attached; the image itself shows as a thumbnail.
        t = t.replacingOccurrences(of: #"\[Image #\d+\]\s*"#, with: "", options: .regularExpression)
        return t.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// The photo paths listed in that note.
    nonisolated static func imagePaths(_ text: String) -> [String] { pathsMemo(text, computeImagePaths) }

    nonisolated private static func computeImagePaths(_ text: String) -> [String] {
        for pattern in notePatterns {
            guard let r = text.range(of: pattern, options: .regularExpression) else { continue }
            var note = String(text[r])
            for token in ["<attached_images>", "</attached_images>", "相关图片：", "（我从手机发来了图片，请用 Read 查看：", "）"] { // i18n-ignore
                note = note.replacingOccurrences(of: token, with: "\n")
            }
            return note.components(separatedBy: CharacterSet(charactersIn: "\n、"))
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { $0.hasPrefix("/") || $0.hasPrefix("~") }
        }
        return []
    }
}

/// A message another Claude session sent this one: on the left like the agent's side, in a quiet card that names
/// the sender — it was not typed by you. Its words are Markdown like an answer (agents write Markdown, and the hub
/// turns a teammate's JSON status notes into it); a single line break stays one, as in the message it was.
struct PeerMessage: View {
    var item: Item
    var from: String
    @Environment(\.rowMemoScope) private var memoScope
    @Environment(\.subagentsByCall) private var agents

    var body: some View {
        card
            .padding(.trailing, Theme.Space.xxl)
            .chatPinned(pinKey)
            .messageLongPress(id: item.id) { card }
            .messageActionBar(id: item.id, text: item.text ?? "", isUser: false, date: item.ts, from: from)
    }

    /// The message in its card (also what a long press lifts).
    var card: some View {
        let d = RowMemo.assistant(RowMemo.key(memoScope, item.id)) { _ in AssistantRowText(item.text ?? "") } // once per message
        return VStack(alignment: .leading, spacing: Theme.Space.sm) {
            Label("From \(from)", systemImage: "arrow.left.arrow.right.circle")
                .font(Theme.Font.labelStrong)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            if Self.isHandback(from: from, agents: agents.values) {
                // A subagent's report that came as a message: one line until opened, as in its card.
                FoldedReport(id: pinKey, text: item.text ?? "", font: Theme.Font.secondary) { words(d) }
            } else {
                // A long one: its first lines until opened, as many as an exchange shows (#2010).
                LineFold(lines: ChatLayoutSettings.defaultLines, id: pinKey) { words(d) }
            }
        }
        .padding(Theme.Space.m)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
    }

    private var pinKey: String { "peer-" + item.id }

    private func words(_ d: AssistantRowText) -> some View {
        Markdown(d.markdown)
            .markdownSoftBreakMode(.lineBreak)
            .markdownTheme(.persona)
            .environment(\.replyKey, RowMemo.key(memoScope, item.id)) // its quotes' folds, the lifted copy's too
            .selectableInLift(item.text ?? "", as: .reply(quiet: false)) // #603
            .frame(maxWidth: .infinity, alignment: .leading)
            #if os(macOS)
            .textSelection(.enabled) // drag to select and ⌘C, as on the web (#2542)
            #endif
    }
}

/// A message just sent from this phone, before the transcript has it: drawn like a sent bubble right away, with a
/// quiet clock while the request runs, the clock and "Sends when connected" while it waits for the network (Outbox.swift),
/// and a red "Resend" when it failed. A long press gives an action bar like a sent message's, with Copy, Select Text and
/// Share (#6427): one that never went out can be copied rather than typed again. One only on this phone (failed or
/// waiting) ends the bar in Delete / Don't Send, as its mark's menu does (#4626).
struct PendingBubble: View {
    var msg: PendingMessage
    var retry: () -> Void
    var discard: () -> Void
    @Environment(\.viewImages) private var viewImages
    @Environment(\.chatImages) private var chatImages

    var body: some View {
        let parsed = ContextRef.parse(msg.text)
        let shown = AttachedFile.parse(parsed.rest).rest
        VStack(alignment: .trailing, spacing: Theme.Space.sm) {
            let files = AttachedFile.parse(msg.text).files
            if !files.isEmpty { AttachedFileCards(files: files) }
            if !msg.images.isEmpty {
                HStack(spacing: Theme.Space.sm) {
                    Spacer(minLength: 48)
                    ForEach(Array(msg.images.enumerated()), id: \.offset) { i, p in
                        // The same viewer as a picture the transcript has (`SentPhotos`), while it is being sent.
                        Button {
                            viewImages?(ImageViewing(images: msg.images, index: i, conversation: chatImages, source: p.identity))
                        } label: {
                            Theme.Fill.chip
                                .scaledFrame(width: msg.images.count == 1 ? Theme.Size.box120 : Theme.Size.box76, height: msg.images.count == 1 ? Theme.Size.box120 : Theme.Size.box76)
                                .overlay { ChatThumbnail(payload: p) }
                                .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                        }
                        .buttonStyle(.plain)
                        .disabled(viewImages == nil)
                        .accessibilityLabel("Image \(i + 1), tap to view full size")
                    }
                }
            }
            HStack(alignment: .bottom, spacing: Theme.Space.s) {
                Spacer(minLength: 40)
                switch msg.state {
                case .sending: Image(systemName: Theme.Icon.time).font(Theme.Font.label).foregroundStyle(.tertiary)
                case .sent: EmptyView()
                case .waiting:
                    Menu {
                        Button(action: retry) { Label("Retry Now [Message Views f40f4f]", systemImage: Theme.Icon.retry) }
                        Button(role: .destructive, action: discard) { Label("Don't Send", systemImage: Theme.Icon.delete) }
                    } label: {
                        ThemedIcon(Theme.Icon.time).font(Theme.Font.caption).foregroundStyle(.secondary)
                            .scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                            .contentShape(.rect)
                    }
                    .accessibilityLabel("Waiting for network, will send once connected")
                case .failed:
                    Menu {
                        Button(action: retry) { Label("Resend", systemImage: Theme.Icon.retry) }
                        Button(role: .destructive, action: discard) { Label("Delete", systemImage: Theme.Icon.delete) }
                    } label: {
                        ThemedIcon(Theme.Icon.errorFilled).font(Theme.Font.body).foregroundStyle(Theme.danger)
                    }
                    .accessibilityLabel("Failed to send. Resend or delete.")
                    // The menu's items as actions too: VoiceOver's Actions rotor, and the Runtime CLI (`activate`
                    // `action:`), which cannot touch a menu's items.
                    .accessibilityAction(named: Text("Resend"), retry)
                    .accessibilityAction(named: Text("Delete"), discard)
                }
                let split = MessageComments.inBubble(shown)
                UserBubbleBody(quotes: ContextRef.quotes(parsed.refs) ?? [], text: split?.rest ?? shown, comments: split?.comments ?? [], lead: split?.quote)
                    .opacity(msg.state == .failed || msg.state == .waiting ? 0.6 : 1)
                    .messageLongPress(id: msg.id.uuidString) {
                        UserBubbleBody(quotes: ContextRef.quotes(parsed.refs) ?? [], text: split?.rest ?? shown, comments: split?.comments ?? [], lead: split?.quote)
                    }
            }
            if msg.state == .waiting {
                Text("Sends when connected").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        // The bar's last item on one only on this phone (#4626): what its mark's menu says, doing the same.
        .messageActionBar(id: msg.id.uuidString, text: shown, isUser: true, date: msg.createdAt, pending: true,
                          delete: .of(msg, run: discard))
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }
}

/// Photos that came with one of your messages (from the transcript): square thumbnails on your side, tap to view.
struct SentPhotos: View {
    @Environment(\.chatImages) private var chatImages
    @Environment(\.attachImage) private var attachImage
    @Environment(\.viewImages) private var viewImages
    var images: [ImagePayload]
    @State private var viewer: Int?

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            Spacer(minLength: 48)
            ForEach(Array(images.enumerated()), id: \.offset) { i, p in
                let side: CGFloat = images.count == 1 ? 140 : 84
                // A photo that did not load: the tap asks the hub again instead of opening an empty viewer (#559).
                let again = ImageRetries.shared.canRetry(p)
                Button {
                    if ImageRetries.shared.retry(p) { return }
                    if let viewImages {
                        viewImages(ImageViewing(images: images, index: i, conversation: chatImages, source: p.identity))
                    } else { viewer = i }
                } label: {
                    Theme.Fill.chip
                        .frame(width: side, height: side)
                        .overlay { ChatThumbnail(payload: p) }
                        .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).stroke(Color.primary.opacity(0.1), lineWidth: 0.5))
                }
                .buttonStyle(.plain)
                .accessibilityLabel(again ? Text("Image didn't load. Tap to retry") : Text("Image \(i + 1), tap to view full size"))
                .accessibilityIdentifier("sent-photo-\(i)")
            }
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .fullScreenCover(isPresented: Binding(get: { viewer != nil }, set: { if !$0 { viewer = nil } })) {
            if let i = viewer { ImageViewer(images: images, index: min(i, images.count - 1), conversation: chatImages, attach: attachImage) }
        }
    }
}

/// Photos sent with a message: a row of square thumbnails above the text; tap one to view them full screen.
struct AttachedImages: View {
    @Environment(\.chatImages) private var chatImages
    @Environment(\.attachImage) private var attachImage
    @Environment(\.viewImages) private var viewImages
    var paths: [String]
    var load: @Sendable (String) async -> ImagePayload?
    @State private var loaded: [String: ImagePayload] = [:]
    @State private var viewer: Int?

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            Spacer(minLength: 48)
            ForEach(Array(paths.enumerated()), id: \.element) { i, path in
                Button { open(i) } label: { thumb(path) }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Image \(i + 1) [Composer Parts 0d2e3a]")
            }
        }
        .task(id: paths) {
            // Cache hits show immediately; each miss is an independent request.
            var missing: [String] = []
            for path in paths where loaded[path] == nil {
                if let cached = AttachedImageCache.get(path) {
                    loaded[path] = cached
                } else if !missing.contains(path) {
                    missing.append(path) // one request per path
                }
            }
            await withTaskGroup(of: (String, ImagePayload?).self) { group in
                for path in missing {
                    group.addTask { (path, await load(path)) }
                }
                for await (path, payload) in group {
                    if let payload { // nil stays a spinner
                        AttachedImageCache.put(path, payload)
                        loaded[path] = payload
                    }
                }
            }
        }
        .fullScreenCover(isPresented: Binding(get: { viewer != nil }, set: { if !$0 { viewer = nil } })) {
            let images = paths.compactMap { loaded[$0] }
            if let i = viewer, !images.isEmpty { ImageViewer(images: images, index: min(i, images.count - 1), conversation: chatImages, attach: attachImage) }
        }
    }

    private func open(_ i: Int) {
        guard loaded[paths[i]] != nil else { return }
        guard let viewImages else { return viewer = i }
        let images = paths.compactMap { loaded[$0] }
        let at = paths.prefix(i).filter { loaded[$0] != nil }.count
        viewImages(ImageViewing(images: images, index: at, conversation: chatImages, source: paths[i]))
    }

    private func thumb(_ path: String) -> some View {
        let side: CGFloat = paths.count == 1 ? 120 : 76
        return Theme.Fill.chip
            .frame(width: side, height: side)
            .overlay { // the square sets the size; the photo fills and is cropped to it
                if let p = loaded[path] {
                    ChatThumbnail(payload: p)
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).stroke(Color.primary.opacity(0.1), lineWidth: 0.5))
            .contentShape(.rect)
    }
}

/// Decoded thumbnails survive scrolling a lazy list (rows are rebuilt when they come back on screen).
enum AttachedImageCache {
    private static var payloads: [String: ImagePayload] = [:]

    static func get(_ path: String) -> ImagePayload? { payloads[path] }
    static func put(_ path: String, _ p: ImagePayload) { payloads[path] = p }
}

struct AssistantText: View, Equatable {
    var item: Item
    /// The words between an agent's steps (Liquid Glass's work timeline): smaller and gray, part of the process.
    var quiet = false
    /// Like `UserBubble`: a hub push re-evaluating the page does not re-run every answer's body (Markdown is the
    /// heaviest row there is).
    nonisolated static func == (a: Self, b: Self) -> Bool { a.item == b.item && a.quiet == b.quiet }
    @Environment(\.openFile) private var openFile
    @Environment(\.rowMemoScope) private var memoScope
    /// The new conversation layout (#1666, `ChatLayout`): Persona's answer in its pale gray box.
    @Environment(\.chatLayout) private var layout

    /// In the new layout's box (not the words between an agent's steps).
    private var boxed: Bool { layout.on && !quiet }

    /// The answer's words, in their box when there is one (also what a long press lifts).
    private func answer(_ d: AssistantRowText) -> some View {
        AssistantRichMarkdown(text: item.text ?? "", itemID: item.id, memo: d, memoKey: RowMemo.key(memoScope, item.id))
            // Inside the theme (applied first), or the theme's text style wins (docs/lessons/ios.md).
            .modifier(QuietMarkdown(on: quiet))
            .markdownTheme(.persona)
            .environment(\.replyKey, RowMemo.key(memoScope, item.id)) // its quotes' folds, the lifted copy's too
            .selectableInLift(item.text ?? "", as: .reply(quiet: quiet)) // #603
            .frame(maxWidth: .infinity, alignment: .leading)
            .chatBoxed(Theme.ChatBox.persona, on: boxed)
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantText")
        #endif
        let d = RowMemo.assistant(RowMemo.key(memoScope, item.id)) { _ in AssistantRowText(item.text ?? "") } // once per message
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            answer(d)
                .task(id: item.id) { await Self.preloadLinks(item) }
                #if os(macOS)
                // Drag to select and ⌘C, as on the web (#2542); iOS keeps the long-press menu's Select.
                .textSelection(.enabled)
                #endif
                .contentShape(.rect)
                // A boxed answer is lifted in its own box, a bare one on a card.
                .messageLongPress(id: item.id, card: !boxed) { answer(d) }
            if let openFile {
                // Documents as cards with their first page (#1543), the rest as chips.
                FileChips(paths: d.chips, open: openFile)
                DocumentCards(paths: d.files.filter(DocumentCards.isDocument), open: openFile)
            }
            PortChips(text: item.text ?? "")
        }
        // A single line break is one, as in the terminal and Claude's web chat (#2088): not a space.
        .markdownSoftBreakMode(.lineBreak)
        .lookUpFiles(d.asked)
        .messageActionBar(id: item.id, text: item.text ?? "", isUser: false, date: item.ts)
        #if os(macOS)
        // Under an answer, while the pointer is over it: Copy, and Quote where the chat quotes (#2113). Not under an
        // agent's words between its steps (`quiet`).
        .modifier(AnswerHoverActions(on: !quiet, id: item.id, text: item.text ?? ""))
        #endif
    }

    /// Links in a fresh answer open instantly: their pages load in the background. Only for recent answers — every
    /// old one scrolling past used to create web views on the main thread, and those were the long freezes.
    private static func preloadLinks(_ item: Item) async {
        guard let ts = item.ts, Date.now.timeIntervalSince(ts) < 15 * 60, let text = item.text else { return }
        // Not a machine's localhost (it is opened through the hub, docs/operations/ports.md).
        let links = await Task.detached(priority: .utility) { WebLinks.find(in: text).filter { PortLinks.link($0) == nil } }.value
        if !links.isEmpty, !Task.isCancelled { WebViewPool.shared.preload(links) }
    }
}

/// Smaller, gray Markdown text (an agent's words between its steps).
private struct QuietMarkdown: ViewModifier {
    var on: Bool
    func body(content: Content) -> some View {
        if on {
            content.markdownTextStyle { FontSize(.em(0.88)); ForegroundColor(.secondary) }
        } else {
            content
        }
    }
}

#if os(macOS)
private struct AnswerHoverActions: ViewModifier {
    @Environment(MessageMenu.self) private var menu: MessageMenu?
    var on: Bool
    var id: String
    var text: String

    func body(content: Content) -> some View {
        if on {
            content.macHoverFooter {
                MacHoverButton(title: String(localized: "Copy [Readiness Views d37380]"), symbol: Theme.Icon.copy) { MacHover.copy(text) }
                if let quote = menu?.quote {
                    MacHoverButton(title: String(localized: "Quote"), symbol: Theme.Icon.quote) { quote(text, id, false, nil) }
                }
            }
        } else {
            content
        }
    }
}
#endif

struct CopyButton: View {
    var text: String
    var body: some View {
        Button { UIPasteboard.general.string = text; Haptics.success() } label: { Label("Copy [Readiness Views d37380]", systemImage: Theme.Icon.copy) }
        ShareLink(item: text) { Label("Share", systemImage: Theme.Icon.share) }
    }
}

/// A thought. In a work card (`compact`) it is a row like the tools' around it — "✦ Thinking [its first line]", open the
/// whole text under a guide rule (the reference's Thinking row); elsewhere BeautifulUI's thinking trace.
struct ThinkingRow: View {
    var item: Item
    var compact = false
    @State private var open = false
    @Environment(\.chatPin) private var pin

    private var pinKey: String { "think-" + item.id }
    @State private var unfold = Unfolding()

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ThinkingRow")
        #endif
        if !AppStyle.current.usesBeautifulUI { plain } else if compact { chipRow } else { trace }
    }

    /// Persona' own: the memory mark, the first line, a chevron; opened, the text beside a rule.
    private var plain: some View {
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            Button {
                toggleInPlace(pin, pinKey, animation: Theme.Motion.standard, unfold: $unfold, closing: open) { open.toggle() }
            } label: {
                HStack(spacing: Theme.Space.sm) {
                    ThemedIcon(Theme.Icon.memory).font(Theme.Font.label)
                    Text(open ? String(localized: "Thinking [Message Views a62acb]") : ToolPresentation.firstLine(item.text ?? ""))
                        .font(Theme.Font.label)
                        .lineLimit(1)
                    Spacer(minLength: 0)
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.label).rotationEffect(.degrees(open ? 90 : 0))
                }
                .foregroundStyle(.secondary)
                .padding(.horizontal, compact ? 10 : 0)
                .padding(.vertical, compact ? 4 : 2)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .chatPinned(pinKey)
            if open {
                Text(item.text ?? "")
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.leading, Theme.Space.m)
                    .overlay(alignment: .leading) { Rectangle().fill(Color.secondary.opacity(0.3)).frame(width: Theme.Size.rule) }
                    .padding(.horizontal, compact ? 10 : 0)
                    .unfolding(unfold)
            }
        }
    }

    private var chipRow: some View {
        let text = item.text ?? ""
        return VStack(alignment: .leading, spacing: 0) {
            ToolChipRow(BeautifulUI.ToolCall(id: item.id, .think, label: String(localized: "Thinking [Message Views 8cf010]"), chip: ToolPresentation.firstLine(text), monospacedChip: false),
                        isOpen: open) {
                Haptics.tap()
                toggleInPlace(pin, pinKey, animation: Theme.Motion.state, unfold: $unfold, closing: open) { open.toggle() }
            }
            .padding(ToolCallRow.chipInsets(grouped: true))
            .chatPinned(pinKey)
            if open {
                Text(text)
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
                    .padding(.leading, Theme.Space.m)
                    .overlay(alignment: .leading) { Rectangle().fill(Theme.cardStroke).frame(width: Theme.Size.line) }
                    .padding(.horizontal, WorkCardSlice.inset)
                    .padding(.bottom, Theme.Space.m)
                    .unfolding(unfold)
                    .transition(.opacity)
            }
        }
    }

    private var trace: some View {
        let text = item.text ?? ""
        return ThinkingState(
            .reasoning,
            isWorking: false,
            doneTitle: open ? String(localized: "Thinking [Message Views a62acb]") : ToolPresentation.firstLine(text),
            rows: [BeautifulUI.ThinkingRow(id: item.id, text)],
            isExpanded: Binding(get: { open }, set: { v in toggleInPlace(pin, pinKey, animation: Theme.Motion.standard) { open = v } }),
            maxWidth: .infinity
        )
        .chatPinned(pinKey) // the trace's header is its top
        .textSelection(.enabled)
    }
}

/// Where Claude Code compacted the context (`Item.compact`): a hairline across the chat, 「以上内容已压缩」 in the middle with
/// where it came from (Manual / Auto / While idle) and the size before it, when known, as the web and Android write it (#1762).
/// A tap shows the notes that came with it and when.
struct CompactDivider: View {
    var item: Item
    @State private var open = false
    /// Where the notes end on screen, to bring them into view once open (`ChatPin.show`).
    @State private var notesBottom: CGFloat = 0
    @Environment(\.chatPin) private var pin

    private var title: String { (item.compact ?? Compaction()).dividerTitle }

    private var notes: [String] {
        var n = item.compact?.notes ?? [item.text ?? ""]
        if let ts = item.ts {
            let at = ts.formatted(date: .abbreviated, time: .shortened)
            n.append(String(localized: "Compacted \(at)"))
        }
        return n.filter { !$0.isEmpty }
    }

    private var rule: some View {
        let beautiful = AppStyle.current.usesBeautifulUI
        return Rectangle().fill(beautiful ? Theme.cardStroke : Color(uiColor: .separator))
            .frame(height: beautiful ? Theme.Size.line : Theme.Size.hairline)
            .frame(maxWidth: .infinity)
    }

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            Button {
                let opening = !open
                toggleInPlace(pin, "compact-" + item.id, animation: Theme.Motion.state) { open.toggle() }
                // The divider as the last row: the notes open under it, past the composer on a short screen.
                guard opening, let pin else { return }
                Task { @MainActor in
                    try? await Task.sleep(for: .seconds(Theme.Motion.revealTime))
                    if open { pin.show("compact-" + item.id, bottom: notesBottom) }
                }
            } label: {
                HStack(spacing: Theme.Space.s) {
                    rule
                    HStack(spacing: Theme.Space.xs) {
                        Text(title).lineLimit(1)
                        ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).font(Theme.Font.caption)
                    }
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .fixedSize()
                    rule
                }
                .padding(.vertical, Theme.Space.s)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .chatPinned("compact-" + item.id)
            .accessibilityLabel(open ? String(localized: "\(title), hide notes") : String(localized: "\(title), show notes"))
            .accessibilityIdentifier("compact-divider")
            if open {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    ForEach(Array(notes.enumerated()), id: \.offset) { _, n in
                        Text(n).frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                .font(Theme.Font.label)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.leading)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.s)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .onGeometryChange(for: CGFloat.self, of: { $0.frame(in: .global).maxY }) { notesBottom = $0 }
            }
        }
        .contextMenu { CopyButton(text: notes.joined(separator: "\n")) }
    }
}

struct NoticeRow: View {
    var item: Item
    /// Kept by the chat (`OpenTools`) so a row built again stays as it was (#5602); on its own without one.
    @Environment(\.openTools) private var openTools
    @State private var expandedHere = false
    private var expanded: Bool { openTools?.isOpen("notice-" + item.id) ?? expandedHere }
    @Environment(\.chatPin) private var pin

    var body: some View {
        if item.compact != nil { CompactDivider(item: item) }
        else if item.wake == true { wake } else if item.task != nil { TaskNoteRow(item: item) } else if Self.isShort(item.text ?? "") { plain } else { long }
    }

    /// One short line reads as a centered capsule ("Interrupted", "Context compacted"); anything longer would be a block of centered
    /// gray text, screens tall (a local command's output): it is a left-aligned box of a few lines instead.
    static func isShort(_ s: String) -> Bool { !s.contains("\n") && s.count <= 60 }

    private var long: some View {
        Button {
            toggleInPlace(pin, "notice-" + item.id, animation: Theme.Motion.state) {
                if let openTools { openTools.set("notice-" + item.id, !expanded) } else { expandedHere.toggle() }
            }
        } label: {
            HStack(alignment: .top, spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.info).font(Theme.Font.label).foregroundStyle(.tertiary)
                Text(item.text ?? "")
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.leading)
                    .lineLimit(expanded ? nil : 4)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.s)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .chatPinned("notice-" + item.id)
        .contextMenu { CopyButton(text: item.text ?? "") }
    }

    private var plain: some View {
        Text(item.text ?? "")
            .font(Theme.Font.label)
            .foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.sm)
            .background(Theme.Fill.well, in: .capsule)
            .frame(maxWidth: .infinity)
    }

    /// A background task woke the agent: opens the work that follows, like a message would.
    private var wake: some View {
        let w = Self.wakeSummary(item)
        return HStack(spacing: Theme.Space.sm) {
            ThemedIcon(w.failed ? Theme.Icon.errorFilled : Theme.Icon.notification)
                .foregroundStyle(w.failed ? Theme.danger : Theme.accent)
            Text(w.text).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
        }
        .font(Theme.Font.label)
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.sm)
        .background(Theme.Fill.well, in: .capsule)
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// `Background command "Release" completed (exit code 0)` → "Background command done · Release".
    private static let wakeMemo = TextMemo<(text: String, failed: Bool)>(limit: 500)

    static func wakeSummary(_ s: String) -> (text: String, failed: Bool) { wakeMemo(s, computeWakeSummary) }

    /// The hub's one line when it read the notification (`Item.task`), else worked out from an older hub's summary.
    static func wakeSummary(_ item: Item) -> (text: String, failed: Bool) {
        if let n = item.task { return (item.text ?? n.title, n.failed) }
        return wakeSummary(item.text ?? "")
    }

    private static func computeWakeSummary(_ s: String) -> (text: String, failed: Bool) {
        guard let m = s.firstMatch(of: /^(.*?)\s*"(.+)"\s*(.*)$/) else { return (s, false) }
        let kind = switch String(m.1) {
        case "Background command": String(localized: "Background command")
        case "Agent": String(localized: "Subagent [Message Views e6e12c]")
        case "Monitor": String(localized: "Monitor [Message Views fdc390]")
        default: String(localized: "Background Task")
        }
        let rest = m.3.lowercased()
        let code = rest.firstMatch(of: /exit code (\d+)/).flatMap { Int($0.1) }
        let failed = rest.contains("fail") || rest.contains("error") || (code ?? 0) != 0
        let what = String(m.2)
        let text = if failed {
            String(localized: "\(kind) failed · \(what)")
        } else if rest.contains("kill") || rest.contains("stop") {
            String(localized: "\(kind) stopped · \(what)")
        } else if rest.contains("complet") {
            String(localized: "\(kind) done · \(what)")
        } else {
            String(localized: "\(kind) updated · \(what)")
        }
        return (text, failed)
    }
}

/// Claude Code's "while you were away" recap: like a background task's report, one short line ("While You Were Away · 10:02 ›");
/// the recap itself opens in a drawer. Several in a row are one line ("While You Were Away · 3 times · 10:02 ›"), the newest first
/// in the drawer.
struct RecapCard: View {
    /// Oldest first, as they came.
    var items: [Item]
    @State private var open = false

    init(item: Item) { items = [item] }
    init(items: [Item]) { self.items = items }

    private var newest: Item { items[items.count - 1] }

    var body: some View {
        Button {
            Haptics.tap()
            open = true
        } label: {
            HStack(spacing: Theme.Space.sm) {
                ThemedIcon("arrow.uturn.backward.circle.fill").foregroundStyle(Theme.accent)
                Text("While You Were Away").lineLimit(1)
                if items.count > 1 { Text("· \(items.count)×").lineLimit(1) }
                if let ts = newest.ts, ts.isMeaningful { Text("· " + Fmt.relative(ts)).lineLimit(1) }
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong)
            }
            .font(Theme.Font.caption)
            .foregroundStyle(.secondary)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.sm)
            .background(Theme.Fill.well, in: .capsule)
            .contentShape(.capsule)
        }
        .buttonStyle(.plain)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contextMenu { CopyButton(text: newest.text ?? "") }
        .accessibilityLabel(items.count > 1 ? String(localized: "\(items.count) recaps while you were away, tap to view") : String(localized: "Recap while you were away, tap to view"))
        .sheet(isPresented: $open) { RecapDrawer(items: items) }
        #if DEBUG
        .task { if UserDefaults.standard.bool(forKey: "showRecapDrawer") { try? await Task.sleep(for: .seconds(3)); open = true } }
        #endif
    }
}

/// The newest recap in full, as the chat's last line when you come back to it (in the flow, #1360): a tap closes it
/// back to the one-line `RecapCard`.
struct RecapStrip: View {
    var item: Item
    var close: () -> Void
    private var recap: String { item.text ?? "" }

    var body: some View {
        Button {
            Haptics.tap()
            close()
        } label: {
            HStack(alignment: .top, spacing: Theme.Space.m) {
                Capsule().fill(Theme.accent).frame(width: Theme.Size.bar)
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    HStack(spacing: Theme.Space.sm) {
                        ThemedIcon("arrow.uturn.backward.circle.fill").foregroundStyle(Theme.accent)
                        Text("While You Were Away").font(Theme.Font.captionStrong).lineLimit(1)
                        if let ts = item.ts, ts.isMeaningful {
                            Text("· " + Fmt.relative(ts)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        }
                        Spacer(minLength: 4)
                        ThemedIcon(Theme.Icon.close).font(Theme.Font.labelHeavy).foregroundStyle(.tertiary)
                    }
                    Text(Fmt.plain(item.text ?? ""))
                        .font(Theme.Font.secondary)
                        .foregroundStyle(.primary)
                        .lineLimit(8)
                        .multilineTextAlignment(.leading)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .fixedSize(horizontal: false, vertical: true) // as tall as the text: the bar would take all the room
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.m)
            .background(Theme.accent.opacity(0.08), in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .contentShape(.rect(cornerRadius: Theme.Radius.card))
        }
        .buttonStyle(.plain)
        .contextMenu { CopyButton(text: item.text ?? "") }
        .accessibilityLabel(String(localized: "Recap while you were away: \(recap). Tap to close"))
        .accessibilityIdentifier("chat.recapStrip")
    }
}

/// The recap in full, in the same kind of drawer as a background task's report.
struct RecapDrawer: View {
    @Environment(\.dismiss) private var dismiss
    /// Oldest first, as they came; shown newest first (it is the one that says where things are now).
    var items: [Item]

    /// When a recap was written; the newest of several says so.
    private static func written(_ ts: Date, latest: Bool) -> String {
        let when = Fmt.relative(ts)
        return latest ? String(localized: "Latest · Written \(when)") : String(localized: "Written \(when)")
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: Theme.Space.l) {
                    ForEach(Array(items.reversed().enumerated()), id: \.element.id) { n, item in
                        if n > 0 { Divider() }
                        VStack(alignment: .leading, spacing: Theme.Space.m) {
                            if let ts = item.ts, ts.isMeaningful {
                                Text(Self.written(ts, latest: n == 0 && items.count > 1))
                                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                            }
                            Markdown(Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply(item.text ?? "")))))
                                .markdownTheme(.persona)
                                .markdownSoftBreakMode(.lineBreak)
                                .textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                        }
                    }
                }
                .padding(Theme.Space.l)
            }
            .background(Theme.pageBackground)
            .navigationTitle(items.count > 1 ? String(localized: "While You Were Away · \(items.count)") : String(localized: "While You Were Away"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() } } }
        }
        .presentationDetents([.medium, .large])
        .presentationDragIndicator(.visible)
    }
}

/// The assistant's answer while it is still being generated, with a caret after its words (`StreamTail`).
struct StreamingText: View {
    var text: String

    /// A delta comes every 80 ms with the whole text so far. Parsing and laying out all of it each time cost a long
    /// reply 10–20 ms a delta on the simulator: the blocks already finished are their own Markdown views that stay as
    /// they are (`StreamChunks`), and only the block being written is parsed and laid out again.
    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("StreamingText")
        #endif
        let parts = StreamChunks.split(AssistantCardText.replacingCards(text))
        VStack(alignment: .leading, spacing: 0) {
            ForEach(Array(parts.done.enumerated()), id: \.offset) { i, chunk in
                StreamChunk(text: chunk).equatable()
                    .padding(.top, i == 0 ? 0 : StreamChunks.gap(after: parts.done[i - 1], before: chunk))
            }
            Markdown(StreamTail.content(parts.tail))
                .markdownTheme(.persona)
                .padding(.top, parts.done.last.map { StreamChunks.gap(after: $0, before: parts.tail) } ?? 0)
        }
        .markdownSoftBreakMode(.lineBreak) // as the finished reply (`AssistantText`)
        .environment(\.replyStreaming, true) // its quotes fold once it is done
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// The block of a streaming reply still being written, as it is drawn (#2916): made whole for the moment
/// (`StreamRepair`: no `**` or half a link showing raw and then snapping), with the caret only where it is prose (inside a
/// code fence, a table or a formula it was drawn as code, a cell or TeX), and an unfinished ```mermaid or ```math fence as
/// plain code until it closes (a half-written diagram was sent to the renderer at every draw: a spinner, or "Couldn't draw
/// this"). Only this block changes; the finished ones (`StreamChunk`) are drawn as before.
nonisolated enum StreamTail {
    /// Its own small memo: a tail is a new text at every delta, and in `MarkdownMemo` they filled it every half minute
    /// and wiped the finished replies' entries with them.
    private static let memo = TextMemo<MarkdownContent>(limit: 32)

    static func content(_ tail: String) -> MarkdownContent {
        memo(tail) { tail in
            var shown = StreamRepair.repair(StreamRepair.plainOpenFence(tail) { RenderedFence.kind($0) != nil })
            if StreamRepair.end(of: tail) == .text { shown += " ▍" }
            return MarkdownContent(Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply(shown)))))
        }
    }
}

/// A finished block of a streaming reply: the same text draws the same, so later deltas skip it.
private struct StreamChunk: View, Equatable {
    var text: String

    var body: some View {
        // A finished block: its paths are written whole, so they are asked about and linked (the block still being
        // written is not: its last word may be half a path).
        let paths = MarkdownMemo.paths(text)
        LinkedMarkdown(plain: MarkdownMemo.content(text), paths: paths, key: text)
            .markdownTheme(.persona)
            .lookUpFiles(paths.paths)
    }
}

nonisolated enum WebLinks {
    private static let memo = TextMemo<[URL]>()

    /// The web links in a message, in order (memoized: rows ask on every appearance). Same rule as the links drawn
    /// in the message (`Links`).
    static func find(in text: String) -> [URL] { memo(text) { Links.find(in: $0) } }
}


/// Parsed Markdown per text: MarkdownUI parses (cmark) in `Markdown.init`, on every redraw of the row.
nonisolated enum MarkdownMemo {
    private static let memo = TextMemo<MarkdownContent>(limit: 128)
    static func content(_ text: String) -> MarkdownContent { memo(text) { MarkdownContent(Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply($0))))) } }

    private static let pathsMemo = TextMemo<PathMarkdown>(limit: 128)
    /// Where the paths are in the same Markdown (`content`).
    static func paths(_ text: String) -> PathMarkdown { pathsMemo(text) { PathMarkdown(Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply($0))))) } }
}

/// Markdown with the paths that are there on the conversation's machine as links (docs/apple/ios.md「File viewer」), the code
/// spans that are a whole path too; tapping one opens it in the file viewer at its line (`PathLinkURL`, the chat's
/// `openURL`). Until the machine answers, and outside a chat, the plain Markdown: the same words in the same place, only
/// their color changes when the answer comes (a link is the text's size, so the row does not grow).
struct LinkedMarkdown: View {
    @Environment(AppModel.self) private var model: AppModel?
    @Environment(\.fileScope) private var scope
    var plain: MarkdownContent
    var paths: PathMarkdown
    /// The message's memo key: with the paths linked, the key of the linked Markdown.
    var key: String

    private static let memo = TextMemo<MarkdownContent>(limit: 512)

    /// The paths of a message that are there to open, which it links: none outside a chat. Only a message with paths
    /// reads the presence (and is drawn again when an answer comes). Also how its words are laid out to select them
    /// in place (`SelectableInLift`): a path written as code that is linked is drawn as words.
    static func linkable(_ paths: PathMarkdown, model: AppModel?, scope: FileScope?) -> [String] {
        guard !paths.paths.isEmpty, let model, let scope else { return [] }
        return paths.paths.filter { model.filePresence.linkable($0, in: scope) }
    }

    var body: some View { Markdown(content) }

    private var content: MarkdownContent {
        // In a chat on the iPhone and iPad a tap on inline code copies it (#2984): its spans are links the chat's
        // `openURL` answers (`CodeCopyMarkdown`), the ones linked to a file first.
        #if os(iOS)
        let copies = scope != nil
        #else
        let copies = false
        #endif
        let paths = paths
        let there = Self.linkable(paths, model: model, scope: scope)
        guard !there.isEmpty else {
            return copies ? Self.memo(key + "\u{1}") { _ in MarkdownContent(CodeCopyMarkdown.linked(paths.source)) } : plain
        }
        return Self.memo(key + "\u{0}" + there.joined(separator: "\n")) { _ in
            let linked = paths.linked(plainCode: copies) { there.contains($0) }.markdown
            return MarkdownContent(copies ? CodeCopyMarkdown.linked(linked) : linked)
        }
    }
}

/// What a chat row derives from its message (parsed Markdown, paths, links, the shown text), computed once per
/// message and keyed by its session and its id (`key`): rows redraw constantly, and hashing a long text on every
/// redraw was itself measurable. An item never changes its text under the same id — within its session. Ids are not
/// unique across sessions: a runtime's are "<seq>#<block>" (DSH, pi), Codex lines without one are "line<seq>#<n>",
/// and two branches of one DSH session share theirs up to where they part — keyed by the id alone, a branch showed
/// the other branch's message in its place (#372).
nonisolated enum RowMemo {
    static let assistant = TextMemo<AssistantRowText>(limit: 20_000) // a very long session's worth of messages
    static let user = TextMemo<UserRowText>(limit: 20_000)

    /// The memo key of item `id` in the chat of `scope` (`scope(_:)`; "" outside a chat: the id alone).
    static func key(_ scope: String, _ id: String) -> String { MemoScope.key(scope, id) }

    /// A chat's scope: its machine and session.
    static func scope(_ key: SessionKey) -> String { MemoScope.of(machine: key.machineID, session: key.sessionID) }

    /// Fills both memos off the main thread for messages just loaded (of the chat of `scope`), so a row scrolling
    /// into view only lays out.
    static func prewarm(_ items: [Item], scope: String) {
        for it in items {
            if Task.isCancelled { return }
            if it.kind == .toolUse { ToolDiffMemo.prewarm(it) }
            if it.kind == .toolUse || it.kind == .toolResult { ToolTextMemo.prewarm(it, scope: scope) }
            guard it.kind == .text else { continue }
            guard let text = it.text, !text.isEmpty else { continue }
            if it.role == "assistant" { _ = assistant(key(scope, it.id)) { _ in AssistantRowText(text) } }
            if it.role == "user" { _ = user(key(scope, it.id)) { _ in UserRowText(text) } }
        }
    }
}

extension EnvironmentValues {
    /// The chat the rows below belong to (`RowMemo.scope`): what their per-message memos are keyed by, with the id.
    @Entry var rowMemoScope: String = ""
}

nonisolated struct AssistantRowText {
    let markdown: MarkdownContent
    /// Where its paths are, to link the ones that are there (`LinkedMarkdown`).
    let paths: PathMarkdown
    let files: [String]
    /// What to ask the machine about: the paths in the text and in its code blocks (those stay chips).
    let asked: [String]
    /// The chips under the answer: the files it names that are not linked in its words (in code blocks, past the
    /// limit) and its pictures, videos and sounds; documents aside (their cards).
    let chips: [String]
    init(_ text: String) {
        // Steps that go on at "5." under a line of text are a list (`ReplyBreaks`, #2088); bold next to CJK punctuation
        // closes (`CJKEmphasis`, #3863); links end where `Links` says, not at the next space; formulas as pictures.
        let source = Links.markdown(MathMarkdown.rewrite(CJKEmphasis.apply(ReplyBreaks.apply(text))))
        markdown = MarkdownContent(source)
        let paths = PathMarkdown(source)
        self.paths = paths
        let files = FilePaths.find(in: text)
        self.files = files
        asked = PathRefs.unique(paths.paths + files, limit: 40)
        let inline = Set(paths.paths)
        chips = files.filter { !FileThumbnails.isDocument($0) && (!inline.contains($0) || FilePaths.isMedia($0)) }
    }
}

nonisolated struct UserRowText {
    /// The paths in `shown`, linked where they are there (`PathText`).
    let pathRefs: [PathRef]
    let refs: [ContextRef]
    let text: String   // without the context tags
    let cardSource: AssistantActionSource.Parsed?
    let shown: String  // without the image notes and the files' tags (what Copy and Edit take)
    /// The bubble's words: `shown` without the comment blocks it led with, drawn as cards (`comments`, `lead`).
    let words: String
    let comments: [MessageComment]
    let lead: String?
    let images: [String]
    /// The files sent with it (videos), drawn as cards.
    let attached: [AttachedFile]
    let files: [String]
    /// The chips under the bubble: the files it names that are not linked in it, and its pictures, videos and sounds.
    let chips: [String]
    let asked: [String]
    init(_ raw: String) {
        let source = AssistantActionSource.parse(raw)
        cardSource = source
        let parsed = ContextRef.parse(source?.text ?? raw)
        refs = parsed.refs
        text = parsed.rest
        let shown = UserBubble.display(parsed.rest)
        self.shown = shown
        let split = MessageComments.inBubble(shown)
        let words = split?.rest ?? shown
        self.words = words
        comments = split?.comments ?? []
        lead = split?.quote
        let images = UserBubble.imagePaths(parsed.rest)
        self.images = images
        let sent = AttachedFile.parse(parsed.rest)
        attached = sent.files
        // The comments' files are on their cards: the chips are the ones the words name.
        let named = split == nil ? sent.rest : (MessageComments.split(sent.rest)?.rest ?? sent.rest)
        let files = FilePaths.find(in: named).filter { !images.contains($0) }
        self.files = files
        let found = PathRefs.find(in: words)
        pathRefs = found
        let inline = PathRefs.unique(found, limit: 40)
        let linked = Set(inline)
        chips = files.filter { !linked.contains($0) || FilePaths.isMedia($0) }
        asked = PathRefs.unique(inline + files, limit: 40)
    }
}
