import MarkdownUI
import PersonaKit
import Photos
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// An image the viewer shows: its bytes, a picture file on a machine (its thumbnail, as big as the node draws one), or
/// a Feishu image (a hub path or a web URL, through `LarkMedia`). `title` / `detail` are the two lines of the top bar
/// (a name; where it came from, its size); Copy Path / Open in Files when it is a file.
struct ViewerImage: Identifiable, Hashable {
    var payload: ImagePayload?
    var file: FileRef?
    var url: String?
    var title: String?
    var detail: String?
    var id: String { payload?.identity ?? file.map { "file:" + $0.id } ?? url.map { "url:" + $0 } ?? "" }

    /// Width × height in pixels when known before decoding.
    var pixelSize: String? {
        guard let w = payload?.width, let h = payload?.height else { return nil }
        return "\(w)×\(h)"
    }
}

/// The one full-screen image viewer of the app (docs/apple/ios.md「Information architecture」), as Photos shows a picture: a round back
/// button, the title in a capsule and a ⋯ menu on top; a strip of thumbnails when there are several, then Share · the
/// main actions in a capsule (Save, Markup, Info) · Copy at the bottom. The page's own color behind it; a tap hides the
/// bars and the backdrop turns black. Pinch / double-tap zoom, swipe between images, pull down to close (the picture
/// follows the finger and shrinks, the backdrop fades). Every place that opens a picture large opens this.
struct ImageViewer: View {
    var images: [ViewerImage]
    @State var index: Int
    /// Where an edited image goes (the chat's composer); nil hides Markup.
    var attach: ((UIImage) -> Void)? = nil
    @Environment(\.dismiss) private var dismiss
    @Environment(\.colorScheme) private var colorScheme
    @State private var openedFile: FileRef?
    @State private var decoded: [Int: UIImage] = [:]
    /// Pages whose last load gave no picture: they say so, with Retry, instead of spinning on (#182). Not loaded again
    /// until Retry.
    @State private var failed: Set<Int> = []
    @State private var chrome = true
    /// How far a pull-down has gone, 0…1: the backdrop and the bars fade with it.
    @State private var pull: CGFloat = 0
    @State private var saving = false
    @State private var editing: UIImage?
    @State private var showInfo = false
    /// The pictures being decoded; not observed (nothing draws from it).
    @State private var loading = Loading()

    /// Opens at `tapped` within the whole conversation when it is there, else within `local`.
    init(images local: [ImagePayload], index: Int, conversation: [ImagePayload] = [], attach: ((UIImage) -> Void)? = nil) {
        let tapped = local.indices.contains(index) ? local[index] : nil
        if let tapped, let at = conversation.firstIndex(where: { $0.identity == tapped.identity }) {
            images = conversation.map { ViewerImage(payload: $0) }
            _index = State(initialValue: at)
        } else {
            images = local.map { ViewerImage(payload: $0) }
            _index = State(initialValue: local.indices.contains(index) ? index : 0)
        }
        self.attach = attach
    }

    /// These images (a turn's, `ViewedImagesRow`), with their titles and files.
    init(items: [ViewerImage], index: Int, attach: ((UIImage) -> Void)? = nil) {
        images = items
        _index = State(initialValue: items.indices.contains(index) ? index : 0)
        self.attach = attach
    }

    /// Feishu images by URL (messages, cards, Moments, documents).
    init(urls: [String], index: Int) {
        self.init(items: urls.map { ViewerImage(url: $0) }, index: index)
    }

    private var current: ViewerImage? { images.indices.contains(index) ? images[index] : nil }
    private var currentImage: UIImage? { decoded[index] }
    /// The bars and the page's own backdrop: on a page that did not load they stay, whatever a tap did, so its words
    /// read and Close is there (a failed page has no picture to pull down).
    private var showsChrome: Bool { chrome || failed.contains(index) }
    /// The bars: shown, and not while the picture is being pulled away.
    private var barsOpacity: Double { showsChrome ? Double(max(0, 1 - pull * 3)) : 0 }

    var body: some View {
        ZStack {
            (showsChrome ? Theme.Viewer.canvas : Theme.Viewer.immersive)
                .opacity(Double(1 - pull))
                .ignoresSafeArea()
                .animation(Theme.Motion.state, value: showsChrome)
            pager
            chromeBars
                .opacity(barsOpacity)
                .allowsHitTesting(showsChrome && pull == 0)
                .animation(Theme.Motion.state, value: showsChrome)
        }
        .sheet(item: $openedFile) { ref in FileViewer(ref: ref) }
        .fullScreenCover(item: Binding(get: { editing.map(EditTarget.init) }, set: { if $0 == nil { editing = nil } })) { t in
            ImageEditor(EditedImage(source: t.image), doneTitle: String(localized: "Add to Message")) { edited in
                attach?(edited.result)
                Haptics.success()
                AppModel.shared.toast = Toast(String(localized: "Added to the message. Add a few words and send."), style: .success)
                dismiss()
            }
        }
        // The viewer's own pull-down closes it (the system's zoom-transition drag would fight the zoomed picture's pan).
        .interactiveDismissDisabled()
        #if os(iOS) || os(visionOS)
        .statusBarHidden(!showsChrome)
        .persistentSystemOverlays(showsChrome ? .automatic : .hidden)
        #endif
        #if DEBUG
        // Screenshots: `-showViewerChromeHidden YES` opens with the bars hidden, `-showViewerInfo YES` with Info open.
        .task {
            if UserDefaults.standard.bool(forKey: "showViewerChromeHidden") { try? await Task.sleep(for: .seconds(1)); chrome = false }
            if UserDefaults.standard.bool(forKey: "showViewerInfo") { try? await Task.sleep(for: .seconds(1)); showInfo = true }
        }
        #endif
    }

    // MARK: Pages

    @ViewBuilder private var pager: some View {
        #if os(iOS) || os(visionOS)
        // UIKit pages the pictures: a page-style TabView stopped between two pages now and then (#5678, ImagePager.swift).
        ImagePagerView(count: images.count, images: decoded, index: $index,
                       onTap: toggleChrome,
                       onPull: { pull = $0 },
                       onPullEnd: { close in
                           if close { dismiss() } else { withAnimation(Theme.Motion.state) { pull = 0 } }
                       },
                       onNeed: { i in Task { await load(i) } },
                       failed: failed,
                       // On the black backdrop (a neighbour seen mid-swipe while the bars are hidden) its words go light.
                       failure: { i in AnyView(notLoaded(i).environment(\.colorScheme, showsChrome ? colorScheme : .dark)) })
            .ignoresSafeArea()
            // The one it opens on and the ones either side, before a swipe brings them on.
            .task(id: index) {
                for i in [index, index + 1, index - 1] { await load(i) }
            }
        #else
        // No page-style TabView on the Mac: one image at a time, ← / → and the pager's arrows move (ImagePager).
        ImagePager(count: images.count, index: $index) { i in page(i) }
        #endif
    }

    #if os(macOS)
    private func page(_ i: Int) -> some View {
        Group {
            if let img = decoded[i] {
                ZoomableImage(image: img, onTap: toggleChrome)
            } else if failed.contains(i) {
                notLoaded(i)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .task(id: i) { await load(i) }
    }
    #endif

    /// Decodes picture `i` once (a page asking again while it loads waits for nothing).
    private func load(_ i: Int) async {
        guard images.indices.contains(i), decoded[i] == nil, !failed.contains(i), !loading.ids.contains(i) else { return }
        loading.ids.insert(i)
        defer { loading.ids.remove(i) }
        await decode(i)
        #if DEBUG // `-openEditor YES`: straight into the editor (screenshots)
        if i == index, attach != nil, UserDefaults.standard.bool(forKey: "openEditor") { editing = decoded[i] }
        #endif
    }

    /// A page whose picture did not come: said so, with Retry when loading again can bring it. One fetched from the hub
    /// or the web can; bytes already here that do not decode, or a file's thumbnail (`FileThumbnails` keeps its
    /// failures for the session), would only fail again. The bars stay while it is the page shown (`showsChrome`).
    private func notLoaded(_ i: Int) -> some View {
        let item = images[i]
        let again = item.payload.map { $0.ref != nil } ?? (item.url != nil)
        let retry: (title: String, run: () -> Void)? = again
            ? (title: String(localized: "Retry [Components b8784c]"), run: { self.retry(i) }) : nil
        let message = again ? String(localized: "Couldn't get this image. Tap Retry to try again.")
            : item.payload == nil && item.file != nil ? String(localized: "Couldn't get a preview of this image. Use Open in Files in the ⋯ menu.")
            : String(localized: "This image's data can't be opened")
        return EmptyStateView(symbol: Theme.Icon.error, title: String(localized: "Image didn't load"), message: message, action: retry,
                              tint: Theme.caution)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityIdentifier("viewer-not-loaded-\(i)")
    }

    /// Retry on a page that did not load: its load runs again.
    private func retry(_ i: Int) {
        failed.remove(i)
        Task { await load(i) }
    }

    private func toggleChrome() {
        #if os(macOS)
        // A click keeps the bars on the Mac (they hold the only close button besides Esc).
        #else
        chrome.toggle()
        #endif
    }

    // MARK: Bars

    private var chromeBars: some View {
        VStack(spacing: 0) {
            topBar
            Spacer(minLength: 0)
            VStack(spacing: Theme.Space.m) {
                if images.count > 1 { ViewerThumbStrip(images: images, index: $index) }
                bottomBar
            }
        }
        .padding(.vertical, Theme.Space.s)
        .dynamicTypeSize(...DynamicTypeSize.xxxLarge) // the bars must fit an iPhone SE's width
    }

    private var topBar: some View {
        ViewerTopBar {
            Button { dismiss() } label: { ViewerIcon(symbol: Theme.Icon.back) }
                .viewerRound()
                .accessibilityLabel("Close")
                .accessibilityIdentifier("viewer-close")
                .keyboardShortcut(.cancelAction)
        } center: {
            titleCapsule
        } trailing: {
            moreMenu
        }
    }

    private var titleCapsule: some View {
        let title = current?.title ?? String(localized: "images")
        let detail = detailLine
        return VStack(spacing: 0) {
            Text(title).font(Theme.Font.secondaryStrong).lineLimit(1).truncationMode(.middle)
            if let detail {
                Text(detail).font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
            }
        }
        .padding(.horizontal, Theme.Space.l)
        .scaledFrame(minHeight: Theme.Size.tapTarget)
        .chromeSurface(in: Capsule())
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("viewer-title")
    }

    /// 「9 / 20 · Studio」「480×300」: the position when there are several, and what the item says or its size.
    private var detailLine: String? {
        var parts: [String] = []
        if images.count > 1 { parts.append("\(index + 1) / \(images.count)") }
        if let d = current?.detail { parts.append(d) } else if let s = current?.pixelSize ?? currentImage.map(Self.pixels) { parts.append(s) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    private var moreMenu: some View {
        Menu {
            if let img = currentImage {
                Button { copy(img) } label: { Label("Copy Image", systemImage: Theme.Icon.copy) }
                if attach != nil {
                    Button { editing = img } label: { Label("Mark Up and Add to Message", systemImage: Theme.Icon.markup) }
                }
            }
            if let file = current?.file {
                Button {
                    UIPasteboard.general.string = file.path
                    Haptics.success()
                    AppModel.shared.toast = Toast(String(localized: "Path copied"), style: .success)
                } label: { Label("Copy Path", systemImage: Theme.Icon.copy) }
                Button { openedFile = file } label: { Label("Open in Files", systemImage: Theme.Icon.folder) }
            }
            Button { showInfo = true } label: { Label("Messages [Image Viewer e70286]", systemImage: Theme.Icon.info) }
        } label: { ViewerIcon(symbol: Theme.Icon.more) }
            .labelOnlyMenu()
            .chromeSurface(in: Circle())
            .accessibilityLabel("More")
            .accessibilityIdentifier("viewer-more")
    }

    private var bottomBar: some View {
        ViewerBottomBar {
            shareButton
        } actions: {
            Button { Task { await save() } } label: { ViewerIcon(symbol: Theme.Icon.save) }
                .disabled(saving || currentImage == nil)
                .accessibilityLabel(saving ? String(localized: "Saving [Image Viewer a1a172]") : String(localized: "Save"))
            if attach != nil {
                Button { editing = currentImage } label: { ViewerIcon(symbol: Theme.Icon.markup) }
                    .disabled(currentImage == nil)
                    .accessibilityLabel("Markup")
            }
            Button { showInfo = true } label: { ViewerIcon(symbol: Theme.Icon.info) }
                .accessibilityLabel("Messages [Image Viewer e70286]")
                .popover(isPresented: $showInfo) { info.presentationCompactAdaptation(.popover) }
        } trailing: {
            Button { if let img = currentImage { copy(img) } } label: { ViewerIcon(symbol: Theme.Icon.copy) }
                .disabled(currentImage == nil)
                .accessibilityLabel("Copy [Readiness Views d37380]")
        }
    }

    @ViewBuilder private var shareButton: some View {
        if let img = currentImage {
            ShareLink(item: Image(uiImage: img), preview: SharePreview(current?.title ?? String(localized: "images"), image: Image(uiImage: img))) {
                ViewerIcon(symbol: Theme.Icon.share)
            }
            .accessibilityLabel("Share")
        } else {
            ViewerIcon(symbol: Theme.Icon.share, enabled: false)
                .accessibilityLabel("Share")
                .accessibilityAddTraits(.isButton)
        }
    }

    /// Info: the picture's name, size and where it is.
    private var info: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(current?.title ?? String(localized: "images")).font(Theme.Font.heading).lineLimit(2)
            if let s = current?.pixelSize ?? currentImage.map(Self.pixels) { infoRow(String(localized: "Size [Image Viewer 246df7]"), s) }
            if let bytes = current?.payload?.size { infoRow(String(localized: "Size [Image Viewer 50db74]"), Int64(bytes).storageText) }
            if let mime = current?.payload?.mime, !mime.isEmpty { infoRow(String(localized: "Format"), mime) }
            if let d = current?.detail { infoRow(String(localized: "Source"), d) }
            if let f = current?.file { infoRow(String(localized: "Path"), f.path) }
        }
        .padding(Theme.Space.l)
        .scaledFrame(minWidth: Theme.Size.box240, alignment: .leading)
        .accessibilityIdentifier("viewer-info")
    }

    private func infoRow(_ key: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
            Text(key).font(Theme.Font.caption).foregroundStyle(.secondary)
            Spacer(minLength: Theme.Space.m)
            Text(value).font(Theme.Font.caption).multilineTextAlignment(.trailing).lineLimit(3).textSelection(.enabled)
        }
    }

    // MARK: Actions

    static func pixels(_ img: UIImage) -> String {
        #if canImport(UIKit)
        "\(Int(img.size.width * img.scale))×\(Int(img.size.height * img.scale))"
        #else
        "\(Int(img.size.width))×\(Int(img.size.height))"
        #endif
    }

    private func copy(_ img: UIImage) {
        UIPasteboard.general.image = img
        Haptics.success()
        AppModel.shared.toast = Toast(String(localized: "Image copied"), style: .success)
    }

    private func decode(_ i: Int) async {
        guard decoded[i] == nil, images.indices.contains(i) else { return }
        #if DEBUG // `-showViewerDecodeDelay`: legacy stress; `-showViewerDecodeDuringPan`: an event-driven gate.
        let delay = UserDefaults.standard.double(forKey: "showViewerDecodeDelay")
        if delay > 0 { try? await Task.sleep(for: .seconds(delay)) }
        #if os(iOS) || os(visionOS)
        await ImagePagingProbe.shared.beforeDecode(i)
        #endif
        #endif
        let image = await ViewerLoader.image(images[i], side: nil)
        #if DEBUG && (os(iOS) || os(visionOS))
        ImagePagingProbe.shared.decoded(i, image: image)
        #endif
        decoded[i] = image
        if image == nil, !Task.isCancelled { failed.insert(i) }
    }

    private func save() async {
        guard let img = currentImage else { return }
        saving = true
        defer { saving = false }
        let status = await PHPhotoLibrary.requestAuthorization(for: .addOnly)
        guard status == .authorized || status == .limited else {
            Haptics.error()
            AppModel.shared.toast = Toast(String(localized: "No access to Photos. In Settings › Persona › Photos, allow “Add Photos Only”."), style: .error)
            return
        }
        do {
            // `@Sendable`: Photos runs the block on its own queue, where main-actor code is a crash (BackgroundCallbackTests).
            try await PHPhotoLibrary.shared().performChanges { @Sendable in PHAssetChangeRequest.creationRequestForAsset(from: img) }
            Haptics.success()
            AppModel.shared.toast = Toast(String(localized: "Saved to Photos"), style: .success)
        } catch {
            Haptics.error()
            if !error.isCancellation { AppModel.shared.toast = Toast(String(localized: "Couldn't save: \(error.localizedDescription) [Image Viewer fcc6eb]"), style: .error) }
        }
    }
}

/// Loads what a `ViewerImage` points at. `side`: the long edge to decode to (a thumbnail), nil for the full picture.
enum ViewerLoader {
    @MainActor static func image(_ item: ViewerImage, side: CGFloat?) async -> UIImage? {
        if let p = item.payload { return await ChatImageLoader.image(p, side: side) }
        if let url = item.url {
            if let side { return await LarkMedia.shared.image(url, size: url.hasPrefix("/") ? larkThumbEdge(points: side / 3) : nil, maxPixel: side) }
            return await LarkMedia.shared.image(url, maxPixel: 4096)
        }
        if let f = item.file, AppModel.shared.features(f.group).contains("file_thumb"), let api = AppModel.shared.api(f.group) {
            // Only the file is known: the biggest thumbnail the node draws (1024 px); Open in Files has the original.
            return await FileThumbnails.shared.thumbnail(f, size: 0, modified: nil, side: min(side ?? 1024, 1024), scale: 1, api: api)
        }
        return nil
    }
}

/// A web picture in Markdown (`![](https://…)` in an answer, a message, a note): drawn through `LarkMedia`'s cache, a tap
/// opens it in the viewer. Pages with their own pictures set their own provider (a file's relative images, Feishu
/// documents); anything not on the web is left to MarkdownUI's default.
struct WebMarkdownImages: ImageProvider {
    func makeImage(url: URL?) -> some View { WebMarkdownImage(url: url) }
}

private struct WebMarkdownImage: View {
    var url: URL?
    @State private var open = false
    @Namespace private var zoom

    var body: some View {
        if let url, let scheme = url.scheme?.lowercased(), scheme == "https" || scheme == "http" {
            Button { open = true } label: {
                LarkRemoteImage(url: url.absoluteString, maxPixel: 1600, contentMode: .fit)
                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.box60, alignment: .leading)
                    .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
            }
            .buttonStyle(.plain)
            .matchedTransitionSource(id: "markdown-image", in: zoom)
            .accessibilityLabel("Image, tap to enlarge")
            .fullScreenCover(isPresented: $open) {
                ImageViewer(urls: [url.absoluteString], index: 0).navigationTransition(.zoom(sourceID: "markdown-image", in: zoom))
            }
        } else {
            DefaultImageProvider.default.makeImage(url: url)
        }
    }
}

/// The thumbnails under a picture when there are several, as Photos has them: the current one wider, a tap moves to
/// it. Centered when they fit, otherwise a strip that scrolls sideways and follows the swipes.
private struct ViewerThumbStrip: View {
    var images: [ViewerImage]
    @Binding var index: Int

    var body: some View {
        ViewThatFits(in: .horizontal) {
            thumbs.padding(.horizontal, Theme.Space.page)
            ScrollViewReader { proxy in
                ScrollView(.horizontal) { thumbs }
                    .scrollIndicators(.hidden)
                    .contentMargins(.horizontal, Theme.Space.page, for: .scrollContent)
                    .onAppear { proxy.scrollTo(index, anchor: .center) }
                    .onChange(of: index) { _, i in withAnimation(Theme.Motion.state) { proxy.scrollTo(i, anchor: .center) } }
            }
        }
        .accessibilityIdentifier("viewer-thumbs")
    }

    private var thumbs: some View {
        HStack(spacing: Theme.Space.xxs) {
            ForEach(images.indices, id: \.self) { i in
                Button {
                    Haptics.tap()
                    withAnimation(Theme.Motion.state) { index = i }
                } label: {
                    ViewerThumb(item: images[i])
                        .scaledFrame(width: i == index ? Theme.Size.avatarXL : Theme.Size.wellS, height: Theme.Size.wellXL)
                        .clipped()
                        .padding(.horizontal, i == index ? Theme.Space.xs : 0)
                }
                .buttonStyle(.plain)
                .id(i)
                .accessibilityLabel("Image \(i + 1) [Image Viewer 73d641]")
                .accessibilityAddTraits(i == index ? .isSelected : [])
            }
        }
        .animation(Theme.Motion.state, value: index)
    }
}

private struct ViewerThumb: View {
    var item: ViewerImage
    @State private var picture: UIImage?

    var body: some View {
        Theme.Fill.chip
            .overlay {
                if let picture { Image(uiImage: picture).resizable().scaledToFill() }
            }
            .task(id: item.id) { picture = await ViewerLoader.image(item, side: 120) }
    }
}

private final class Loading {
    var ids: Set<Int> = []
}

private struct EditTarget: Identifiable {
    var image: UIImage
    var id: ObjectIdentifier { ObjectIdentifier(image) }
}
