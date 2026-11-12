import SwiftUI
#if canImport(UIKit)
import UIKit
#endif
import WebKit

// The link card's tools for a machine's dev server (#150, docs/web/preview.md "iPhone"): under a forwarded page, back /
// forward, where the page is on that computer, a screenshot for the message and picking an element to comment on. The
// page itself is the pool's web view, as for any link (LinkSheet.swift).

#if os(iOS) || os(visionOS)
/// What the page's web view says about where it is (KVO), for the bar.
@MainActor @Observable
final class WebNav {
    private(set) var canGoBack = false
    private(set) var canGoForward = false
    private(set) var url: URL?
    private(set) var isLoading = false
    /// The page's view is there (`view` itself is not observed).
    private(set) var attached = false
    @ObservationIgnored private(set) weak var view: SelectableWebView?
    @ObservationIgnored private var tokens: [NSKeyValueObservation] = []

    /// Called from `makeUIView`: what it observes changes on the next turn, never during the view update.
    func attach(_ v: SelectableWebView) {
        guard view !== v else { return }
        view = v
        tokens = [
            v.observe(\.canGoBack, options: [.initial, .new]) { [weak self] _, change in
                let b = change.newValue ?? false
                Task { @MainActor in self?.canGoBack = b }
            },
            v.observe(\.canGoForward, options: [.initial, .new]) { [weak self] _, change in
                let b = change.newValue ?? false
                Task { @MainActor in self?.canGoForward = b }
            },
            v.observe(\.url, options: [.initial, .new]) { [weak self] _, change in
                let u = change.newValue ?? nil
                Task { @MainActor in self?.url = u }
            },
            v.observe(\.isLoading, options: [.initial, .new]) { [weak self] _, change in
                let b = change.newValue ?? false
                Task { @MainActor in self?.isLoading = b }
            },
        ]
        Task { @MainActor [weak self] in self?.attached = true }
    }
}
#endif

#if os(iOS)
/// Picking an element in the page: the node's own `preview_pick.js` (the app bundles a copy, kept equal by
/// scripts/test/test_preview_pick_copy.py) runs on the element tapped, which is outlined and not followed. Each pick comes
/// back through the script handler `personaPreviewPick`, added while picking and removed after: the pool's web views share
/// one user content controller, so a handler left there would answer for every page.
@MainActor
final class ElementPicker {
    static let handler = "personaPreviewPick"
    private weak var view: WKWebView?
    private var onPick: ((PickedElement) -> Void)?
    /// Each start and stop moves it on: a start that waited for the script and was stopped (or started again) meanwhile
    /// does nothing.
    private var turn = 0

    /// The page's own world: the pick function reads what a dev build left on the element (React's fibers, Vue's and
    /// Svelte's markers), which an isolated world cannot see.
    func start(in v: WKWebView, color: String, onPick: @escaping (PickedElement) -> Void) async {
        stop()
        let mine = turn
        let pick = await Self.pickFunction.value
        guard mine == turn else { return }
        install(in: v, script: Self.script(color: color, pick: pick), onPick: onPick)
    }

    /// The handler, then the script. The script's result is `undefined`, which the async `evaluateJavaScript` does not take.
    private func install(in v: WKWebView, script: String, onPick: @escaping (PickedElement) -> Void) {
        self.onPick = onPick
        let c = v.configuration.userContentController
        c.removeScriptMessageHandler(forName: Self.handler)
        c.add(WeakPickHandler(self), name: Self.handler)
        view = v
        v.evaluateJavaScript(script)
    }

    /// Ends picking: the page's listeners and outline go, and the handler.
    func stop() {
        turn += 1
        guard let v = view else { return }
        v.evaluateJavaScript("window.__personaPick && window.__personaPick.off()")
        v.configuration.userContentController.removeScriptMessageHandler(forName: Self.handler)
        view = nil
        onPick = nil
    }

    fileprivate func received(_ message: WKScriptMessage) {
        guard message.name == Self.handler, message.frameInfo.isMainFrame, message.webView === view,
              let text = message.body as? String,
              let el = try? JSONDecoder().decode(PickedElement.self, from: Data(text.utf8)) else { return }
        onPick?(el)
    }

    /// The bundled pick function, read off the main actor (docs/apple/performance.md P1). The link card asks for it as it
    /// appears (`PreviewPage`), so it has been read by the time Pick is tapped.
    static let pickFunction = Task.detached(priority: .utility) { () -> String in
        let file = Bundle.main.url(forResource: "preview_pick", withExtension: "js")
        return file.flatMap { try? String(contentsOf: $0, encoding: .utf8) } ?? "function () { return null; }" // perf-ok: in its own Task.detached
    }

    /// A tap (a touch that did not scroll) picks the element under it, a pointer's click too; the page's own handlers
    /// never see either. The picked element keeps an outline and a label with its size until picking ends; another tap
    /// picks another.
    static func script(color: String, pick: String) -> String {
        #"""
        (function () {
          const pick = (\#(pick));
          if (window.__personaPick) window.__personaPick.off();
          const color = \#(jsString(color));
          const box = document.createElement("div");
          const tag = document.createElement("div");
          box.style.cssText = "position:fixed;z-index:2147483647;pointer-events:none;box-sizing:border-box;border:2px solid " + color + ";border-radius:4px;display:none";
          tag.style.cssText = "position:fixed;z-index:2147483647;pointer-events:none;font:600 11px ui-monospace,Menlo,monospace;color:#fff;background:" + color + ";padding:2px 6px;border-radius:4px;white-space:nowrap;display:none";
          document.documentElement.appendChild(box);
          document.documentElement.appendChild(tag);
          let el = null, start = null, moved = false;
          const name = (e) => {
            try { const r = pick.call(e); return r && r.selector ? r.selector.split(" > ").pop() : e.localName; } catch (_) { return e.localName; }
          };
          const place = () => {
            if (!el || !el.isConnected) { box.style.display = "none"; tag.style.display = "none"; return; }
            const r = el.getBoundingClientRect();
            box.style.display = "block";
            box.style.left = r.left + "px"; box.style.top = r.top + "px";
            box.style.width = r.width + "px"; box.style.height = r.height + "px";
            tag.textContent = name(el) + " · " + Math.round(r.width) + "×" + Math.round(r.height);
            tag.style.display = "block";
            const w = tag.offsetWidth, h = tag.offsetHeight;
            tag.style.left = Math.max(4, Math.min(r.left, innerWidth - w - 4)) + "px";
            tag.style.top = (r.top - h - 4 >= 0 ? r.top - h - 4 : Math.min(r.bottom + 4, innerHeight - h - 4)) + "px";
          };
          const show = (t) => { el = t && t.nodeType === 1 ? t : t && t.parentElement; place(); };
          const choose = (t) => {
            show(t);
            if (!el) return;
            let r = null;
            try { r = pick.call(el); } catch (_) {}
            if (!r) return;
            r.url = location.href;
            r.title = document.title;
            const h = window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.personaPreviewPick;
            if (h) h.postMessage(JSON.stringify(r));
          };
          const swallow = (e) => { e.preventDefault(); e.stopPropagation(); e.stopImmediatePropagation(); };
          const on = [
            ["touchstart", (e) => { const t = e.touches[0]; start = { x: t.clientX, y: t.clientY }; moved = false; }, { capture: true, passive: true }],
            ["touchmove", (e) => { const t = e.touches[0]; if (start && Math.hypot(t.clientX - start.x, t.clientY - start.y) > 10) moved = true; }, { capture: true, passive: true }],
            ["touchend", (e) => { const tap = start && !moved; start = null; if (tap) { swallow(e); choose(e.target); } }, { capture: true, passive: false }],
            ["click", (e) => { swallow(e); choose(e.target); }, { capture: true, passive: false }],
            ["pointermove", (e) => { if (e.pointerType === "mouse") show(e.target); }, { capture: true, passive: true }],
            ["pointerdown", swallow, { capture: true, passive: false }],
            ["pointerup", swallow, { capture: true, passive: false }],
            ["mousedown", swallow, { capture: true, passive: false }],
            ["mouseup", swallow, { capture: true, passive: false }],
            ["dblclick", swallow, { capture: true, passive: false }],
            ["scroll", place, { capture: true, passive: true }],
            ["resize", place, { capture: true, passive: true }],
          ];
          for (const [type, f, o] of on) window.addEventListener(type, f, o);
          window.__personaPick = {
            off() {
              for (const [type, f, o] of on) window.removeEventListener(type, f, o);
              box.remove();
              tag.remove();
              delete window.__personaPick;
            },
          };
        })();
        """#
    }

    private static func jsString(_ s: String) -> String {
        (try? String(decoding: JSONEncoder().encode(s), as: UTF8.self)) ?? "\"\""
    }

    /// A color as CSS, in the page's appearance.
    static func css(_ color: Color, in v: UIView) -> String {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        UIColor(color).resolvedColor(with: v.traitCollection).getRed(&r, green: &g, blue: &b, alpha: &a)
        return "rgba(\(Int(r * 255)),\(Int(g * 255)),\(Int(b * 255)),\(a))"
    }
}

/// Holds the picker weakly for the content controller, which keeps its handlers alive.
@MainActor
private final class WeakPickHandler: NSObject, WKScriptMessageHandler {
    weak var picker: ElementPicker?
    init(_ picker: ElementPicker) { self.picker = picker }

    func userContentController(_ controller: WKUserContentController, didReceive message: WKScriptMessage) {
        picker?.received(message)
    }
}

/// Pictures of the page for the message box.
enum PageShot {
    /// The web's cap for a whole page.
    static let maxPixels: CGFloat = 12_000

    /// What is in view.
    @MainActor static func visible(_ v: WKWebView) async throws -> UIImage {
        try await v.takeSnapshot(configuration: nil)
    }

    /// The whole page, drawn from its PDF (what is off screen too), at most `maxPixels` high.
    @MainActor static func whole(_ v: WKWebView) async throws -> UIImage {
        let data = try await v.pdf(configuration: WKPDFConfiguration())
        guard let provider = CGDataProvider(data: data as CFData), let doc = CGPDFDocument(provider), doc.numberOfPages > 0 else {
            throw URLError(.cannotDecodeContentData)
        }
        let pages = (1...doc.numberOfPages).compactMap { doc.page(at: $0) }
        let boxes = pages.map { $0.getBoxRect(.mediaBox) }
        let size = CGSize(width: boxes.map(\.width).max() ?? 0, height: boxes.map(\.height).reduce(0, +))
        guard size.width > 0, size.height > 0 else { throw URLError(.cannotDecodeContentData) }
        let format = UIGraphicsImageRendererFormat()
        format.scale = min(v.traitCollection.displayScale, maxPixels / size.height)
        format.opaque = true
        return UIGraphicsImageRenderer(size: size, format: format).image { ctx in
            UIColor.white.setFill()
            ctx.fill(CGRect(origin: .zero, size: size))
            var top: CGFloat = 0
            for (page, box) in zip(pages, boxes) {
                ctx.cgContext.saveGState()
                ctx.cgContext.translateBy(x: 0, y: top + box.height)
                ctx.cgContext.scaleBy(x: 1, y: -1)
                ctx.cgContext.drawPDFPage(page)
                ctx.cgContext.restoreGState()
                top += box.height
            }
        }
    }
}

/// A forwarded page in the link card with its bar: the pool's web view, the bar under it, and while an element is picked,
/// the line to write about it instead of the bar.
struct PreviewPage: View {
    @Environment(AppModel.self) private var model
    var url: URL
    var key: SessionKey
    @Binding var title: String
    var onSelect: (String, String) -> Void
    @State private var nav = WebNav()
    @State private var picker = ElementPicker()
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.dynamicTypeSize) private var typeSize
    @State private var picking = false
    /// DEBUG: picking starts once the page has loaded.
    @State private var pickWhenLoaded = PreviewPage.startsPicking
    @State private var picked: PickedElement?
    @State private var note = ""
    @State private var shooting = false
    @FocusState private var writing: Bool

    /// DEBUG `-showPreviewPicking YES`: the card opens with picking on (screenshots).
    static var startsPicking: Bool {
        #if DEBUG
        UserDefaults.standard.bool(forKey: "showPreviewPicking")
        #else
        false
        #endif
    }

    /// The computer's port the page is forwarded from.
    private var machinePort: Int? { PortForwarder.shared.key(for: nav.url ?? url)?.port }

    var body: some View {
        VStack(spacing: Theme.Space.flush) {
            PooledWebView(url: url, title: $title, nav: nav, onPick: onSelect)
                .overlay(alignment: .top) {
                    if picking && picked == nil { banner.transition(reduceMotion ? AnyTransition.opacity : AnyTransition.move(edge: .top).combined(with: .opacity)) }
                }
            Divider()
            if let picked { notePanel(picked) } else { bar }
        }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: picking)
        .animation(reduceMotion ? nil : Theme.Motion.state, value: picked)
        // A new document drops the page's listeners: picking ends with it.
        .onChange(of: nav.isLoading) { _, loading in
            if loading {
                if picking { stopPicking() }
            } else if pickWhenLoaded {
                pickWhenLoaded = false
                startPicking()
            }
        }
        // The pick script is read off the main actor now, so it is there when Pick is tapped.
        .task { _ = await ElementPicker.pickFunction.value }
        .onDisappear { picker.stop() }
    }

    // MARK: The bar

    private var bar: some View {
        Group {
            if typeSize.isAccessibilitySize {
                // The address on a line of its own: beside the four buttons it came down to "…" (#5114).
                VStack(spacing: Theme.Space.xs) {
                    address
                    HStack(spacing: Theme.Space.xs) {
                        moveButtons
                        Spacer(minLength: Theme.Space.flush)
                        pageButtons
                    }
                }
            } else {
                HStack(spacing: Theme.Space.xs) {
                    moveButtons
                    address
                    pageButtons
                }
            }
        }
        .font(Theme.Font.title)
        .padding(.horizontal, Theme.Space.s)
        .padding(.vertical, Theme.Space.xs)
        .background(Theme.pageBackground)
    }

    /// Back and Forward.
    @ViewBuilder private var moveButtons: some View {
        barButton(Theme.Icon.back, String(localized: "Back"), id: "preview-back", enabled: nav.canGoBack) { nav.view?.goBack() }
        barButton(Theme.Icon.forward, String(localized: "Forward"), id: "preview-forward", enabled: nav.canGoForward) { nav.view?.goForward() }
    }

    /// Screenshot and Pick.
    @ViewBuilder private var pageButtons: some View {
        Menu {
            Section("Adds a picture to your message") {
                Button { shoot(whole: false) } label: { Label("Visible Area", systemImage: Theme.Icon.pageScreenshot) }
                Button { shoot(whole: true) } label: { Label("Whole Page", systemImage: Theme.Icon.document) }
            }
        } label: {
            Group {
                if shooting { ProgressView() } else { ThemedIcon(Theme.Icon.pageScreenshot) }
            }
            .tapTargetFrame()
        }
        .disabled(shooting || !nav.attached)
        .accessibilityLabel("Screenshot")
        .accessibilityIdentifier("preview-shot")
        barButton(Theme.Icon.pickElement, String(localized: "Pick an element to comment on"), id: "preview-pick", enabled: nav.attached) {
            startPicking()
        }
    }

    private func barButton(_ icon: String, _ label: String, id: String, enabled: Bool, run: @escaping () -> Void) -> some View {
        Button { Haptics.tap(); run() } label: { ThemedIcon(icon).tapTargetFrame() }
            .disabled(!enabled)
            .accessibilityLabel(label)
            .accessibilityIdentifier(id)
    }

    /// `:5173/settings`: the page's path on the computer's port.
    private var address: some View {
        let path = PreviewLogic.address(nav.url ?? url, localPort: url.port)
        return HStack(spacing: Theme.Space.flush) {
            if let machinePort { Text(verbatim: ":\(machinePort)").foregroundStyle(.secondary) }
            Text(path).foregroundStyle(Color.primary)
        }
        .font(Theme.Font.code)
        .lineLimit(1)
        .truncationMode(.middle)
        .padding(.horizontal, Theme.Space.m)
        .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS, alignment: .leading)
        .background(Theme.Fill.well, in: .capsule)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Address")
        .accessibilityValue((machinePort.map { ":\($0)" } ?? "") + path)
        .accessibilityIdentifier("preview-address")
        .contextMenu {
            Button {
                UIPasteboard.general.string = PreviewLogic.machinePage((nav.url ?? url).absoluteString, machinePort: machinePort)
                Haptics.success()
            } label: { Label("Copy Link", systemImage: Theme.Icon.link) }
        }
    }

    private func shoot(whole: Bool) {
        guard let v = nav.view else { return }
        shooting = true
        Task {
            defer { shooting = false }
            do {
                let image: UIImage
                if whole { image = try await PageShot.whole(v) } else { image = try await PageShot.visible(v) }
                attach(image)
            } catch {
                shotFailed()
            }
        }
    }

    private func shotFailed() {
        Haptics.error()
        model.toast = Toast(String(localized: "Couldn't take the screenshot. Try again."), style: .error)
    }

    /// The screenshot goes to the message box, like a photo attached there.
    private func attach(_ image: UIImage) {
        model.composerInbox[key, default: []].append(image)
        Haptics.success()
        model.toast = Toast(String(localized: "Screenshot added to your message"), style: .success)
    }

    // MARK: Picking

    /// What picking does, with Cancel. At the accessibility sizes the words take the icon's room and up to four lines, in a
    /// rounded box: two lines cut them off, and a capsule's ends cut into lines that tall (#5114).
    @ViewBuilder private var banner: some View {
        let large = typeSize.isAccessibilitySize
        let row = HStack(spacing: Theme.Space.m) {
            if !large { ThemedIcon(Theme.Icon.pickElement).foregroundStyle(Theme.accent).accessibilityHidden(true) }
            Text("Tap an element to comment on it").font(Theme.Font.secondary).lineLimit(large ? 4 : 2)
            Spacer(minLength: Theme.Space.s)
            Button("Cancel") { stopPicking() }
                .font(Theme.Font.secondaryMedium).lineLimit(1)
                .accessibilityIdentifier("preview-pick-cancel")
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
        Group {
            if large { row.surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous)) } else { row.surface(in: .capsule) }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.top, Theme.Space.s)
        .accessibilityIdentifier("preview-pick-banner")
    }

    private func startPicking() {
        guard let v = nav.view else { return }
        picked = nil
        note = ""
        picking = true
        let color = ElementPicker.css(Theme.accent, in: v)
        Task {
            await picker.start(in: v, color: color) { el in
                picked = el
                writing = true
            }
        }
    }

    private func stopPicking() {
        picker.stop()
        picking = false
        picked = nil
        note = ""
        writing = false
    }

    /// The element picked: what it is, where its code is, and the line to write about it. At the accessibility sizes the
    /// element and its page take a line each, the buttons stack with Add to Message first, and the panel scrolls: it can be
    /// taller than the room above the keyboard (#5114).
    private func notePanel(_ el: PickedElement) -> some View {
        Group {
            if typeSize.isAccessibilitySize {
                ScrollView { noteForm(el, large: true) }
                    .scrollBounceBehavior(.basedOnSize)
                    .accessibilityIdentifier("preview-note-panel")
            } else {
                noteForm(el, large: false)
            }
        }
        .background(Theme.pageBackground)
        .transition(reduceMotion ? AnyTransition.opacity : AnyTransition.move(edge: .bottom).combined(with: .opacity))
    }

    private func noteForm(_ el: PickedElement, large: Bool) -> some View {
        let path = PreviewLogic.address(URL(string: el.url), localPort: URL(string: el.url)?.port)
        let source = el.source.map { PreviewLogic.relativeSource($0.file, cwd: model.session(key)?.cwd ?? "") + (($0.line ?? 0) > 0 ? ":\($0.line!)" : "") }
        let origin = [el.component.flatMap { $0.isEmpty ? nil : $0 }, source.map { el.component?.isEmpty == false ? "(\($0))" : $0 }]
            .compactMap { $0 }.joined(separator: " ")
        let icon = ThemedIcon(Theme.Icon.pickElement).foregroundStyle(Theme.accent).accessibilityHidden(true)
        let what = Text(el.label).font(Theme.Font.code).lineLimit(large ? 2 : 1)
        return VStack(alignment: .leading, spacing: Theme.Space.s) {
            Group {
                if large {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        HStack(spacing: Theme.Space.s) { icon; what }
                        Text(verbatim: path).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(2).truncationMode(.middle)
                    }
                } else {
                    HStack(spacing: Theme.Space.s) {
                        icon
                        what
                        Text(verbatim: "· \(path)").font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("preview-picked")
            if !origin.isEmpty {
                Text(origin).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(large ? 3 : 1).truncationMode(.middle)
            }
            TextField(String(localized: "What should change?"), text: $note, axis: .vertical)
                .lineLimit(1...4)
                .font(Theme.Font.body)
                .focused($writing)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.sm)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .accessibilityIdentifier("preview-note")
            if large {
                VStack(spacing: Theme.Space.m) {
                    Button { add(el) } label: { Text("Add to Message").lineLimit(1).frame(maxWidth: .infinity) }
                        .themedButton(prominent: true)
                        .accessibilityIdentifier("preview-note-add")
                    Button { stopPicking() } label: { Text("Cancel").lineLimit(1).frame(maxWidth: .infinity) }
                        .themedButton()
                        .accessibilityIdentifier("preview-note-cancel")
                }
            } else {
                HStack(spacing: Theme.Space.m) {
                    Spacer(minLength: Theme.Space.flush)
                    Button { stopPicking() } label: { Text("Cancel").lineLimit(1) }
                        .themedButton()
                        .accessibilityIdentifier("preview-note-cancel")
                    Button { add(el) } label: { Text("Add to Message").lineLimit(1) }
                        .themedButton(prominent: true)
                        .accessibilityIdentifier("preview-note-add")
                }
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .padding(.vertical, Theme.Space.m)
    }

    private func add(_ el: PickedElement) {
        let page = PreviewLogic.machinePage(el.url, machinePort: machinePort)
        let comment = PreviewLogic.comment(el, page: page, cwd: model.session(key)?.cwd ?? "", note: note)
        model.composerComments[key, default: []].append(comment)
        Haptics.success()
        model.toast = Toast(String(localized: "Comment added to your message"), style: .success)
        stopPicking()
    }
}
#endif
