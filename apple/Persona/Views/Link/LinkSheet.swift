import BeautifulUI
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif
import WebKit

// Links open inside the app, Telegram-style (CLAUDE.md): a sheet that rises to 60%, drags up to full screen and down
// to close, with the chat behind it still usable. Feishu docs open in the native reader and company-feed links as the
// post while the hub has them (FeishuLinks.route, LarkFeatures); any other page in a
// WKWebView taken from a pool that preloads the links of the latest answers. Text selected on a page can go to the
// agent as an edit request (Edit with AI) or ride along with the next message (Add as Context), both as <persona-context>.

/// A piece of a page the user picked, carried in a message as
/// `<persona-context kind="context|edit" title="…" url="…">text</persona-context>`, the text through `ContextText.inBlock`
/// so it cannot close the tag (#3831).
nonisolated struct ContextRef: Identifiable, Hashable, Sendable, Codable {
    var id = UUID()
    var kind: String // "context" | "edit"
    var title: String
    var url: String
    var text: String
    /// A quoted message's id in its chat (`kind` "quote"), so its block in the sent bubble leads back to it.
    var msg = ""
    /// Which part of the message is quoted (`part="selection"`: words picked in Select Text, #4549); nil for all of it.
    /// Readers that do not know it read the block as a quote of the message.
    var part: String? = nil
    /// The block's other attributes as written: a forward's metadata (`ForwardMessage`, docs/protocol/protocol.md
    /// "Forwarded messages"), a workspace's machine and folder. Kept so a block read back writes the same attributes.
    var extra: [String: String]? = nil
    /// The composer's line under a forward over the box: where it comes from and where it goes. Shown, never sent.
    var route: String? = nil

    var host: String { URL(string: url)?.host ?? url }

    /// The extra attributes in the order they are written: a forward's metadata first, then any others by name.
    static let extraOrder = ["role", "author", "chat", "project", "machine", "time"]

    var xml: String {
        func esc(_ s: String) -> String {
            s.replacingOccurrences(of: "&", with: "&amp;").replacingOccurrences(of: "\"", with: "&quot;")
                .replacingOccurrences(of: "<", with: "&lt;").replacingOccurrences(of: ">", with: "&gt;")
        }
        let msgAttr = msg.isEmpty ? "" : " msg=\"\(esc(msg))\""
        let partAttr = part.map { " part=\"\(esc($0))\"" } ?? ""
        let extras = extra ?? [:]
        let names = Self.extraOrder.filter { extras[$0] != nil } + extras.keys.filter { !Self.extraOrder.contains($0) }.sorted()
        let extraAttrs = names.compactMap { name in extras[name].map { " \(name)=\"\(esc($0))\"" } }.joined()
        return "<persona-context kind=\"\(kind)\" title=\"\(esc(title))\" url=\"\(esc(url))\"\(msgAttr)\(partAttr)\(extraAttrs)>\n\(ContextText.inBlock(text))\n</persona-context>"
    }

    /// One `<persona-context>` block, its attributes in any order and any of them missing (a Lark /agent request's context has no url).
    private static let tagRe = try! NSRegularExpression(pattern: "<" + HubNaming.contextPattern + #"\b([^>]*)>\s*([\s\S]*?)\s*</"# + HubNaming.contextPattern + #">\s*"#)
    private static let attrRe = try! NSRegularExpression(pattern: #"(\w+)="([^"]*)""#)

    /// The attributes that have fields of their own; the rest go to `extra`.
    private static let ownAttributes: Set<String> = ["kind", "title", "url", "msg", "part"]

    /// The contexts in a message and the message without them.
    private static let memo = TextMemo<(refs: [ContextRef], rest: String)>()

    static func parse(_ text: String) -> (refs: [ContextRef], rest: String) { memo(text, scan) }

    private static func unescape(_ s: String) -> String {
        s.replacingOccurrences(of: "&quot;", with: "\"").replacingOccurrences(of: "&lt;", with: "<")
            .replacingOccurrences(of: "&gt;", with: ">").replacingOccurrences(of: "&amp;", with: "&")
    }

    private static func scan(_ text: String) -> (refs: [ContextRef], rest: String) {
        let ns = text as NSString
        var refs: [ContextRef] = []
        for m in tagRe.matches(in: text, range: NSRange(location: 0, length: ns.length)) {
            let attrs = ns.substring(with: m.range(at: 1)) as NSString
            var a: [String: String] = [:]
            for am in attrRe.matches(in: attrs as String, range: NSRange(location: 0, length: attrs.length)) {
                a[attrs.substring(with: am.range(at: 1))] = unescape(attrs.substring(with: am.range(at: 2)))
            }
            // Provenance and protected-user sentinels are transport metadata, never selectable context cards.
            guard a["kind"] != "message-source" else { continue }
            let extra = a.filter { !Self.ownAttributes.contains($0.key) }
            refs.append(ContextRef(kind: a["kind"] ?? "context", title: a["title"] ?? "", url: a["url"] ?? "",
                                   text: ns.substring(with: m.range(at: 2)), msg: a["msg"] ?? "", part: a["part"],
                                   extra: extra.isEmpty ? nil : extra))
        }
        let rest = tagRe.stringByReplacingMatches(in: text, range: NSRange(location: 0, length: ns.length), withTemplate: "")
        return (refs, rest.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    /// What the row of a message's contexts says, folded: the one context's title, or how many.
    static func summary(_ refs: [ContextRef]) -> String {
        if refs.count == 1, let r = refs.first { return r.kind == "edit" ? String(localized: "To Edit · \(r.label)") : String(localized: "Context · \(r.label)") }
        return String(localized: "Context · \(refs.count) items")
    }

    var label: String { title.isEmpty ? (url.isEmpty ? String(localized: "Quote") : host) : title }

    var icon: String {
        switch kind {
        case "instructions": Theme.Icon.info
        case "doc": Theme.Icon.document
        case "edit": Theme.Icon.ai
        case ForwardMessage.kind: Theme.Icon.forwardMessage
        default: Theme.Icon.quote
        }
    }
}

struct LinkTarget: Identifiable, Hashable {
    var url: URL
    /// A machine's port forwarded to the phone (docs/operations/ports.md): what to call it ("machine name :5173"); the url is the
    /// phone's loopback, which only this app serves.
    var forwarded: String? = nil
    /// The manager its native view reads through (the chat's or inbox item's it was tapped in, docs/assistant/managers.md
    /// "Clients"); nil: the default's, nothing named.
    var manager: String? = nil
    var id: String { manager.map { $0 + "\u{0}" + url.absoluteString } ?? url.absoluteString }
}

extension EnvironmentValues {
    /// Opens a link in the in-app sheet (set by ChatView).
    @Entry var openLink: ((URL) -> Void)? = nil
    /// Inside the link sheet: hands picked text to the agent (text, "edit" | "context"). Native document views
    /// (the Feishu reader) show Edit with AI / Add as Context on long press when it is set.
    @Entry var docPick: ((String, String) -> Void)? = nil
}

/// The sheet's content: header (title, host, actions) over the page.
struct LinkSheet: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let target: LinkTarget
    var key: SessionKey? = nil
    /// Opens another of the session's dev servers in the card (ChatView): a forwarded page's subtitle lists them (#150).
    var choosePreview: ((PreviewOption) -> Void)? = nil
    @State private var title = ""
    @State private var choosing = false
    @State private var chosen: PreviewOption?
    @State private var editing: ContextRef?
    @State private var instruction = ""
    /// Opens full screen (2026-10-28: half a screen was too little to read); pull down to 0.6 to see the chat again.
    @State private var detent: PresentationDetent = .large

    /// Feishu links with a native view (documents, company-feed posts), while the hub has them.
    private var feishuRoute: FeishuRoute? { FeishuLinks.route(target.url, lark: model.larkFeatures, manager: target.manager) }
    private var isFeishuDoc: Bool { if case .doc = feishuRoute { true } else { false } }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if let feishuRoute {
                NavigationStack {
                    FeishuRouteView(route: feishuRoute)
                        .toolbar(.hidden, for: .navigationBar)
                        .feishuRoutes()
                        .environment(\.docPick) { text, kind in pick(text, kind: kind) }
                }
            } else {
                #if os(iOS)
                if let key, target.forwarded != nil {
                    // A machine's dev server from a chat: the preview's bar under it (#150).
                    PreviewPage(url: target.url, key: key, title: $title) { selected, kind in pick(selected, kind: kind) }
                } else {
                    page
                }
                #else
                page
                #endif
            }
        }
        .presentationDetents([.fraction(0.6), .large], selection: $detent)
        .presentationDragIndicator(.visible)
        .presentationBackground(Theme.pageBackground) // pages with a clear background let the chat show through
        .presentationBackgroundInteraction(.enabled(upThrough: .fraction(0.6))) // the chat stays usable behind
        .presentationContentInteraction(.resizes) // a swipe up grows the sheet before it scrolls the page
        #if os(iOS)
        .sheet(isPresented: $choosing, onDismiss: {
            if let o = chosen { chosen = nil; choosePreview?(o) }
        }) {
            if let key, let cwd = model.session(key)?.cwd {
                PreviewChooser(key: key, cwd: cwd) { o in chosen = o; choosing = false }
            }
        }
        #endif
        .alert("Have an Agent Edit This", isPresented: Binding(get: { editing != nil }, set: { if !$0 { editing = nil } })) {
            TextField(String(localized: "How should it change?"), text: $instruction)
            Button("Cancel", role: .cancel) { instruction = "" }
            Button("Send") { sendEdit() }
        } message: {
            Text(editing.map { String($0.text.prefix(120)) } ?? "")
        }
    }

    private var page: some View {
        PooledWebView(url: target.url, title: $title) { selected, kind in pick(selected, kind: kind) }
            .ignoresSafeArea(.container, edges: .bottom) // full screen: the page runs under the home indicator
    }

    /// A forwarded page whose chat's hub has previews: its subtitle lists the session's other servers.
    private var canChoose: Bool {
        #if os(iOS)
        guard let key, target.forwarded != nil, choosePreview != nil else { return false }
        return model.features(key.group).contains(PreviewLogic.feature)
        #else
        false
        #endif
    }

    private var header: some View {
        HStack(spacing: Theme.Space.m) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title.isEmpty ? (isFeishuDoc ? String(localized: "Lark Doc") : feishuRoute != nil ? String(localized: "Moments") : (target.forwarded != nil ? nil : target.url.host) ?? String(localized: "Web Pages")) : title)
                    .font(Theme.Font.secondaryStrong).lineLimit(1)
                if canChoose {
                    Button { Haptics.tap(); choosing = true } label: {
                        HStack(spacing: Theme.Space.xxs) {
                            Text(target.forwarded ?? "").lineLimit(1)
                            ThemedIcon(Theme.Icon.expand).font(Theme.Font.labelStrong)
                        }
                        .font(Theme.Font.label)
                        .foregroundStyle(Theme.accent)
                    }
                    .buttonStyle(.plain)
                    .accessibilityHint(String(localized: "Lists this session's dev servers"))
                    .accessibilityIdentifier("link-servers")
                } else {
                    Text(target.forwarded ?? target.url.host ?? target.url.absoluteString).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
            }
            Spacer(minLength: 6)
            if isFeishuDoc, key != nil {
                // The native reader has no custom selection menu: the whole doc can still go along.
                Button { pick("", kind: "context") } label: { ThemedIcon(Theme.Icon.addContext) }
                    .accessibilityLabel("Add this document as context")
            }
            if feishuRoute == nil {
                Button { Haptics.tap(); WebViewPool.shared.reload(target.url) } label: { ThemedIcon(Theme.Icon.reload) }
                    .accessibilityLabel("Refresh")
                    .accessibilityIdentifier("link-reload")
            }
            Menu {
                Button { UIPasteboard.general.url = target.url; Haptics.success() } label: { Label("Copy Link", systemImage: Theme.Icon.link) }
                if target.forwarded == nil { browserItems } // the phone's loopback means nothing to another app
            } label: { ThemedIcon(Theme.Icon.moreMenu) }
            Button { dismiss() } label: { ThemedIcon(Theme.Icon.clear).symbolRenderingMode(.hierarchical) }
                .accessibilityLabel("Close")
                .accessibilityIdentifier("link-close")
        }
        .font(Theme.Font.title)
        .padding(.horizontal, Theme.Space.l)
        .padding(.top, Theme.Space.l)
        .padding(.bottom, Theme.Space.s)
    }

    @ViewBuilder private var browserItems: some View {
        #if os(iOS) || os(visionOS)
        Button { UIApplication.shared.open(target.url) } label: { Label("Open in Safari", systemImage: Theme.Icon.safari) }
        #else
        Button { UIApplication.shared.open(target.url) } label: { Label("Open in Browser", systemImage: Theme.Icon.safari) }
        #endif
        ShareLink(item: target.url) { Label("Share", systemImage: Theme.Icon.share) }
    }

    private func pick(_ text: String, kind: String) {
        guard let key else {
            model.toast = Toast(String(localized: "Open the link from a chat to hand it to an agent."), style: .info)
            return
        }
        // A forwarded port's host is the phone's loopback: name it as the header does ("machine :5173", #3378).
        let ref = ContextRef(kind: kind, title: title.isEmpty ? (target.forwarded ?? target.url.host ?? "") : title, url: target.url.absoluteString,
                             text: text.isEmpty ? "（整篇）" : text) // i18n-ignore: sentinel in the context XML, compared in QuoteViews
        if kind == "edit" {
            instruction = ""
            editing = ref
        } else {
            model.composerContexts[key, default: []].append(ref)
            Haptics.success()
            model.toast = Toast(String(localized: "Added as context. It'll be sent with your next message."), style: .success)
        }
    }

    private func sendEdit() {
        guard let ref = editing, let key else { return }
        let ask = instruction.trimmingCharacters(in: .whitespacesAndNewlines)
        model.sendNow(key, text: ref.xml + "\n\n" + (ask.isEmpty ? "按上面选中的内容修改。" : "按要求修改上面选中的内容：" + ask)) // i18n-ignore
        instruction = ""
        editing = nil
    }
}

// MARK: - Web views

/// WKWebViews kept warm: the links of the latest answers load in the background, so opening one shows a finished
/// page. All share the default (on-disk) website data store, so assets and logins carry over.
/// On iPhone, iPad and Vision Pro a view serves one opening of its link: once a sheet has shown it, the pool lets it go
/// (`release`, from `PooledWebView`), so a later tap on the link starts on the link's own page with no back/forward
/// history, not where the last opening was left (#4074). The Mac's panel (frozen, apple/Mac/Link) still reuses its views:
/// its Open in Separate Window moves the view on screen, page and history, into the window.
@MainActor
final class WebViewPool {
    static let shared = WebViewPool()
    private var views: [String: SelectableWebView] = [:]
    private var order: [String] = []
    private let limit = 6
    private let config: WKWebViewConfiguration = {
        let c = WKWebViewConfiguration()
        c.websiteDataStore = .default()
        #if os(iOS) || os(visionOS)
        c.allowsInlineMediaPlayback = true
        #endif
        return c
    }()

    /// A view for this url, already loading (or loaded) if it was preloaded. A machine's dev server (the phone's
    /// loopback) loads again when its view is taken again: its page changes as the agent works, and the old view
    /// still showed the page as it was last closed (2026-11-05, #1934).
    func view(for url: URL) -> SelectableWebView {
        let key = url.absoluteString
        if let v = views[key] {
            touch(key)
            if Self.isLoopback(url), !v.isLoading { v.load(Self.request(url)) }
            return v
        }
        let v = SelectableWebView(frame: .zero, configuration: config)
        #if os(iOS) || os(visionOS)
        v.scrollView.showsVerticalScrollIndicator = false
        v.scrollView.showsHorizontalScrollIndicator = false
        #endif
        v.allowsBackForwardNavigationGestures = true
        v.load(Self.request(url))
        views[key] = v
        touch(key)
        return v
    }

    /// The view for this url if the pool has one (nothing is loaded).
    func existing(for url: URL) -> SelectableWebView? { views[url.absoluteString] }

    /// The sheet that showed `v` for `url` has closed. The pool drops `v`, with the page the user browsed to and its
    /// back/forward history, and starts a fresh view on the link, so tapping it again opens the link's own page at once
    /// (#4074). A machine's dev server is asked only when its page is opened, as before. A view the pool no longer
    /// holds (pushed out by newer links while it was shown) changes nothing.
    func release(_ v: WKWebView, for url: URL) {
        let key = url.absoluteString
        guard views[key] === v else { return }
        views[key] = nil
        order.removeAll { $0 == key }
        if !Self.isLoopback(url) { _ = view(for: url) }
    }

    /// The sheet's Refresh: the page again from the server, not from the cache. A view that never got a page (its first
    /// load failed: `url` is nil) loads it afresh.
    func reload(_ url: URL) {
        guard let v = views[url.absoluteString] else { return }
        if v.url == nil { v.load(Self.request(url)) } else { v.reloadFromOrigin() }
    }

    /// A page seen before paints from disk at once; a machine's dev server (the phone's loopback) is always asked.
    private static func request(_ url: URL) -> URLRequest {
        var req = URLRequest(url: url)
        req.cachePolicy = isLoopback(url) ? .reloadIgnoringLocalCacheData : .returnCacheDataElseLoad
        return req
    }

    private static func isLoopback(_ url: URL) -> Bool { ["localhost", "127.0.0.1"].contains(url.host() ?? "") }

    /// Starts loading links that may be opened soon (not the ones a native Feishu view opens).
    func preload(_ urls: [URL]) {
        let lark = AppModel.shared.larkFeatures
        for u in urls.prefix(3) where views[u.absoluteString] == nil && FeishuLinks.route(u, lark: lark) == nil { _ = view(for: u) }
    }

    private func touch(_ key: String) {
        order.removeAll { $0 == key }
        order.append(key)
        while order.count > limit {
            let old = order.removeFirst()
            views[old]?.removeFromSuperview()
            views[old] = nil
        }
    }
}

#if os(iOS) || os(visionOS)
/// A WKWebView whose selection menu has Edit with AI and Add as Context.
final class SelectableWebView: WKWebView {
    var onPick: ((String, String) -> Void)?

    override func buildMenu(with builder: any UIMenuBuilder) {
        super.buildMenu(with: builder)
        let actions = UIMenu(options: .displayInline, children: [
            UIAction(title: String(localized: "Edit with AI"), image: UIImage(systemName: Theme.Icon.ai)) { [weak self] _ in self?.pickSelection("edit") },
            UIAction(title: String(localized: "Add as Context"), image: UIImage(systemName: Theme.Icon.addContext)) { [weak self] _ in self?.pickSelection("context") },
        ])
        builder.insertChild(actions, atStartOfMenu: .root)
    }

    private func pickSelection(_ kind: String) {
        evaluateJavaScript("window.getSelection().toString()") { [weak self] result, _ in
            let text = (result as? String ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { return }
            self?.onPick?(text, kind)
        }
    }
}

struct PooledWebView: UIViewRepresentable {
    var url: URL
    @Binding var title: String
    /// Told where the page is (the preview's bar, #150).
    var nav: WebNav? = nil
    var onPick: (String, String) -> Void

    func makeUIView(context: Context) -> SelectableWebView {
        let v = WebViewPool.shared.view(for: url)
        v.removeFromSuperview()
        v.onPick = onPick
        context.coordinator.url = url
        context.coordinator.observe(v) { title = $0 }
        nav?.attach(v)
        return v
    }

    func updateUIView(_ v: SelectableWebView, context: Context) { v.onPick = onPick }

    /// The sheet has closed: this opening's page and history go with it, and the next tap on the link starts fresh (#4074).
    static func dismantleUIView(_ v: SelectableWebView, coordinator: Coordinator) {
        if let url = coordinator.url { WebViewPool.shared.release(v, for: url) }
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    @MainActor
    final class Coordinator {
        /// The link this view was taken for: the pool's key, which the page the user browsed to no longer is.
        var url: URL?
        private var token: NSKeyValueObservation?
        private var onTitle: ((String) -> Void)?
        func observe(_ v: WKWebView, title: @escaping (String) -> Void) {
            onTitle = title
            if let t = v.title, !t.isEmpty { title(t) }
            token = v.observe(\.title, options: [.new]) { [weak self] v, _ in
                let t = v.title ?? ""
                Task { @MainActor in if !t.isEmpty { self?.onTitle?(t) } }
            }
        }
    }
}
#endif

// MARK: - In messages

/// The contexts a message carries (picked page excerpts, everything a Feishu /agent request hands its agent): one
/// folded row by the bubble, the raw tags never show. Open, a list of titles; a title opens to its text. One context
/// opens straight to its text.
struct ContextGroup: View {
    var refs: [ContextRef]
    /// The message's id: the row stays put in the chat when it opens or closes (`ChatPin`).
    var key: String
    @Environment(\.chatPin) private var pin
    @State private var open = ContextGroup.shownOpen
    @State private var unfold = Unfolding()
    private var pinKey: String { "contexts-\(key)" }

    /// DEBUG `-showContexts YES`: groups and their first context start open (screenshots).
    static var shownOpen: Bool {
        #if DEBUG
        UserDefaults.standard.bool(forKey: "showContexts")
        #else
        false
        #endif
    }

    var body: some View {
        VStack(alignment: .trailing, spacing: Theme.Space.xs) {
            Button {
                Haptics.tap()
                toggleInPlace(pin, pinKey, animation: Theme.Motion.state, unfold: $unfold, closing: open) { open.toggle() }
            } label: {
                HStack(spacing: Theme.Space.sm) {
                    ThemedIcon(refs.count == 1 ? refs[0].icon : Theme.Icon.contexts).font(Theme.Font.label)
                    Text(ContextRef.summary(refs)).font(Theme.Font.labelMedium).lineLimit(1).truncationMode(.middle)
                    ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).font(Theme.Font.label)
                }
                .foregroundStyle(.secondary)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.sm)
                .background(Theme.Fill.well, in: .capsule)
                .contentShape(.capsule)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(ContextRef.summary(refs))
            .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]"))

            if open {
                VStack(alignment: .leading, spacing: 0) {
                    if refs.count == 1 {
                        ContextBody(ref: refs[0])
                    } else {
                        ForEach(Array(refs.enumerated()), id: \.offset) { n, r in
                            if n > 0 { Divider() }
                            ContextRow(ref: r, key: "\(pinKey)-\(n)", open: n == 0 && ContextGroup.shownOpen)
                        }
                    }
                }
                .scaledFrame(maxWidth: Theme.Size.box380, alignment: .leading)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .unfolding(unfold)
            }
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .chatPinned(pinKey)
    }
}

/// One context in an open group: its title; a tap shows its text.
private struct ContextRow: View {
    var ref: ContextRef
    var key: String
    @Environment(\.chatPin) private var pin
    @State var open = false
    @State private var unfold = Unfolding()

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Button {
                Haptics.tap()
                toggleInPlace(pin, key, animation: Theme.Motion.state, unfold: $unfold, closing: open) { open.toggle() }
            } label: {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(ref.icon).font(Theme.Font.label).foregroundStyle(.secondary)
                        .scaledFrame(width: Theme.Size.markM)
                    Text(ref.label).font(Theme.Font.secondary).foregroundStyle(Color.primary).lineLimit(1).truncationMode(.tail)
                    Spacer(minLength: Theme.Space.s)
                    if ref.text.isEmpty { Text("Empty").font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1) }
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.label).foregroundStyle(.tertiary)
                        .rotationEffect(.degrees(open ? 90 : 0))
                }
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.s)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed [Readiness Card ee6046]"))
            if open { ContextBody(ref: ref).unfolding(unfold) }
        }
        .chatPinned(key)
    }
}

/// A context's text (scrolls inside a fixed height when long) and its page, when it has one.
private struct ContextBody: View {
    var ref: ContextRef
    @Environment(\.openLink) private var openLink

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if ref.text.isEmpty {
                Text("No content").font(Theme.Font.caption).foregroundStyle(.tertiary)
            } else if ref.text.count > 1200 {
                ScrollView { text }.scaledFrame(height: Theme.Size.box300)
            } else {
                text
            }
            if let u = URL(string: ref.url), !ref.url.isEmpty {
                Button { openLink?(u) } label: {
                    Label("Open \(ref.host) [Git Hub Thread View eb8679]", systemImage: Theme.Icon.open).font(Theme.Font.labelMedium).lineLimit(1)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Theme.accent)
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.bottom, Theme.Space.m)
        .padding(.top, Theme.Space.xs)
    }

    private var text: some View {
        Text(ref.text).font(Theme.Font.caption).foregroundStyle(.secondary).textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - Picking text in native views

/// What a long press on a paragraph offers inside the link sheet (SwiftUI's own selection menu cannot take items).
struct DocPickMenu: View {
    var text: String
    var pick: (String, String) -> Void
    @Binding var selecting: Bool

    var body: some View {
        Button { pick(text, "edit") } label: { Label("Edit This with AI", systemImage: Theme.Icon.ai) }
        Button { pick(text, "context") } label: { Label("Add This as Context", systemImage: Theme.Icon.addContext) }
        Button { selecting = true } label: { Label("Select Text…", systemImage: Theme.Icon.select) }
        Button { UIPasteboard.general.string = text; Haptics.success() } label: { Label("Copy [Readiness Views d37380]", systemImage: Theme.Icon.copy) }
    }
}

/// What a selection's menu offers in `PickTextView`: a title, its icon, and the kind `pick` gets with the text.
struct PickAction: Hashable, Sendable {
    var title: String
    var icon: String
    var kind: String

    /// The chat's link sheet: Edit with AI and Add as Context.
    static let ai = [PickAction(title: String(localized: "Edit with AI"), icon: Theme.Icon.ai, kind: "edit"),
                     PickAction(title: String(localized: "Add as Context"), icon: Theme.Icon.addContext, kind: "context")]
}

/// The paragraph in a text view where any span can be selected; its selection menu has `actions` (Edit with AI and
/// Add as Context by default).
struct PickTextSheet: View {
    @Environment(\.dismiss) private var dismiss
    var text: String
    var actions = PickAction.ai
    var pick: (String, String) -> Void

    var body: some View {
        NavigationStack {
            PickTextView(text: text, actions: actions) { t, kind in
                dismiss()
                pick(t, kind)
            }
            .navigationTitle("Select Text [Link Sheet 409a5a]")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() } } }
        }
        .presentationDetents([.medium, .large])
    }
}

#if os(iOS) || os(visionOS)
struct PickTextView: UIViewRepresentable {
    var text: String
    var actions = PickAction.ai
    var pick: (String, String) -> Void

    func makeUIView(context: Context) -> UITextView {
        let v = UITextView()
        v.isEditable = false
        v.isSelectable = true
        v.font = .preferredFont(forTextStyle: .body)
        v.adjustsFontForContentSizeCategory = true
        v.textContainerInset = UIEdgeInsets(top: 16, left: 12, bottom: 24, right: 12)
        v.backgroundColor = .clear
        v.delegate = context.coordinator
        v.text = text
        return v
    }

    func updateUIView(_ v: UITextView, context: Context) {
        context.coordinator.pick = pick
        context.coordinator.actions = actions
    }

    func makeCoordinator() -> Coordinator { Coordinator(pick: pick, actions: actions) }

    final class Coordinator: NSObject, UITextViewDelegate {
        var pick: (String, String) -> Void
        var actions: [PickAction]
        init(pick: @escaping (String, String) -> Void, actions: [PickAction]) {
            self.pick = pick
            self.actions = actions
        }

        func textView(_ textView: UITextView, editMenuForTextIn range: NSRange, suggestedActions: [UIMenuElement]) -> UIMenu? {
            let selected = (textView.text as NSString).substring(with: range).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !selected.isEmpty else { return UIMenu(children: suggestedActions) }
            let ours = UIMenu(options: .displayInline, children: actions.map { a in
                UIAction(title: a.title, image: UIImage(systemName: a.icon)) { [weak self] _ in self?.pick(selected, a.kind) }
            })
            return UIMenu(children: [ours] + suggestedActions)
        }
    }
}
#endif
