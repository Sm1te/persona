import MarkdownUI
import PersonaKit
import SwiftUI

// The native document reader: Markdown export first (fast, faithful tables and lists); when the owner forbids export,
// the JSON node tree rendered natively. Around the text: title, where it lives, who edited it, read count, the cached
// AI summary, the outline, comments and Open in Lark. Base / whiteboard / file links get their own page.

/// What a document read produced.
enum FeishuDocsContent {
    case markdown(FeishuDocsMarkdownBody, title: String, copy: Bool)
    case tree(FeishuDocsView)
    case unsupported(FeishuDocsUnsupported)
}

struct FeishuDocsUnsupported {
    var kind: FeishuDocsKind
    var title: String
    var message: String
    /// A drive file that downloads into Quick Look.
    var file: (token: String, ext: String?)?
    /// A whiteboard's rendered picture.
    var image: String?
}

@MainActor @Observable
final class FeishuDocsReaderModel {
    var content: FeishuDocsContent?
    var error: String?
    var header: FeishuDocsHeader?
    var info: FeishuDocsReaderInfo?
    var summary: FeishuDocsAISummary?
    var loading = false

    /// `api`: the client of the manager the document is read through (`FeishuDocReader.manager`).
    func load(url: String, title: String?, api: APIClient?) async {
        guard let api else { error = String(localized: "Not connected to a hub yet"); return }
        loading = true
        defer { loading = false }
        async let header: FeishuDocsHeader? = try? api.feishuDocsGet("documents/header", query: ["url": url])
        async let info: FeishuDocsReaderInfo? = try? api.feishuDocsGet("documents/reader/info", query: ["url": url])
        async let summary: FeishuDocsAISummary? = try? api.feishuDocsGet("documents/ai-summary", query: ["url": url])
        do {
            content = try await Self.read(api, url: url, title: title)
            error = nil
        } catch {
            if let msg = error.feishuDocsMessage { self.error = msg }
        }
        self.header = await header
        self.info = await info
        self.summary = await summary
    }

    /// Markdown when the owner allows export, else the JSON tree; what neither can show becomes `.unsupported`.
    static func read(_ api: APIClient, url: String, title: String?) async throws -> FeishuDocsContent {
        let kind = FeishuDocsKind.from(url: url)
        if kind == .bitable {
            return .unsupported(.init(kind: .bitable, title: title ?? String(localized: "Base"), message: String(localized: "Bases can't be viewed in Persona yet."), file: nil, image: nil))
        }
        if kind != .file && kind != .whiteboard {
            do {
                let md: FeishuDocsMarkdownResponse = try await api.feishuDocsGet("documents/read", query: ["url": url, "format": "markdown"])
                return .markdown(FeishuDocsMarkdownExport.prepare(md.markdown, title: md.title), title: md.title, copy: md.authority?.copy ?? true)
            } catch let e as FeishuDocsFailure where e.code == "DOCUMENT_EXPORT_FORBIDDEN" || e.code == "DOCUMENT_FORMAT_UNSUPPORTED" || e.status == 422 {
                // Fall through to the tree.
            }
        }
        let view: FeishuDocsView
        do {
            view = try await api.feishuDocsGet("documents/read", query: ["url": url, "format": "json"])
        } catch let e as FeishuDocsFailure where e.code == "DOCUMENT_FORMAT_UNSUPPORTED" || e.status == 422 {
            return .unsupported(.init(kind: kind, title: title ?? kind.label, message: String(localized: "This kind of content can't be viewed in Persona yet."), file: nil, image: nil))
        }
        let name = view.title.isEmpty ? (title ?? kind.label) : view.title
        if view.isBase {
            return .unsupported(.init(kind: .bitable, title: name, message: String(localized: "Bases can't be viewed in Persona yet."), file: nil, image: nil))
        }
        if let o = view.object {
            switch o.kind {
            case "file":
                return .unsupported(.init(kind: .file, title: name, message: String(localized: "A file in Lark Drive. Download it to preview."), file: (o.token, o.extension), image: nil))
            case "whiteboard":
                return .unsupported(.init(kind: .whiteboard, title: name, message: o.empty == true ? String(localized: "This whiteboard is empty.") : String(localized: "Whiteboard"), file: nil, image: o.image))
            default:
                return .unsupported(.init(kind: kind, title: name, message: String(localized: "This kind of content can't be viewed in Persona yet."), file: nil, image: nil))
            }
        }
        if view.isSpreadsheet && view.content.children.isEmpty {
            return .unsupported(.init(kind: .sheet, title: name, message: String(localized: "The owner doesn't allow exporting this sheet. View it in Lark."), file: nil, image: nil))
        }
        return .tree(view)
    }
}

struct FeishuDocReader: View {
    var url: String
    var title: String?
    /// The manager it reads through (`FeishuRoute.doc`): every request and picture names it; nil: the default's.
    var manager: String? = nil

    @Environment(\.feishuOpen) private var open
    @Environment(AppModel.self) private var app
    @State private var model = FeishuDocsReaderModel()
    @State private var showTitle = false
    @State private var showOutline = false
    @State private var showComments = false
    /// The passage Comment on This is about: the comments open with the box on it (#1290).
    @State private var passage: FeishuDocsPassage?
    @State private var summaryExpanded = false
    @State private var gallery: FeishuDocsGallery?
    @State private var jump: String?

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: Theme.Space.m) {
                    switch model.content {
                    case .markdown(let body, _, _):
                        titleBlock
                        summaryCard
                        ForEach(body.chunks) { chunk in
                            if let table = chunk.table {
                                FeishuDocsMarkdownTableView(table: table)
                                    .padding(.vertical, Theme.Space.xs)
                                    .id(chunk.id)
                            } else {
                                Markdown(Links.markdown(chunk.text))
                                    // Inside the theme: an environment set closer to the view wins, and the theme replaces
                                    // the whole of it, paragraph style and all.
                                    .modifier(FeishuDocsMarkdownComments(comment: comment, copyable: copyAllowed))
                                    .markdownTheme(.feishuDocs)
                                    .markdownImageProvider(FeishuDocsImageProvider(manager: manager, open: { openImage($0, in: body.images) }))
                                    .modifier(FeishuDocsSelectable(enabled: copyAllowed && comment == nil))
                                    .id(chunk.id)
                            }
                        }
                        footer
                    case .tree(let view):
                        titleBlock
                        summaryCard
                        treeRows(view)
                        footer
                    case .unsupported(let u):
                        FeishuDocsUnsupportedView(info: u, url: url, manager: manager)
                    case nil:
                        if let e = model.error {
                            EmptyStateView(symbol: Theme.Icon.missingFile, title: String(localized: "Can't open this doc"), message: e,
                                           action: (String(localized: "Retry [Components b8784c]"), { Task { await model.load(url: url, title: title, api: api) } }))
                                .padding(.top, 60)
                            Button("Open in Lark [Feishu Doc Reader 76d0eb]") { FeishuDocsLinks.openInFeishu(url) }
                                .lineLimit(1).frame(maxWidth: .infinity)
                        } else {
                            FeishuDocsSkeleton(title: title)
                        }
                    }
                }
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.m)
            }
            .onScrollGeometryChange(for: Bool.self) { $0.contentOffset.y + $0.contentInsets.top > 90 } action: { _, v in
                withAnimation(.easeInOut(duration: 0.15)) { showTitle = v }
            }
            .onChange(of: jump) {
                guard let id = jump else { return }
                jump = nil
                withAnimation(Theme.Motion.standard) { proxy.scrollTo(id, anchor: .top) }
            }
        }
        .themedPage()
        .navigationTitle(showTitle ? docTitle : "")
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar { toolbar }
        .refreshable { await model.load(url: url, title: title, api: api) }
        .task {
            if model.content == nil { await model.load(url: url, title: title, api: api) }
            #if DEBUG
            // Simulator automation: `-openFeishuDocHeading <n>` scrolls to the n-th outline entry, `-showFeishuDocSheet outline|comments` opens a sheet.
            let n = UserDefaults.standard.integer(forKey: "openFeishuDocHeading")
            if n > 0, n <= outline.count {
                try? await Task.sleep(for: .milliseconds(500))
                jump = rowID(for: outline[n - 1])
            }
            switch UserDefaults.standard.string(forKey: "showFeishuDocSheet") {
            case "outline": showOutline = true
            case "comments": showComments = true
            default: break
            }
            #endif
        }
        .environment(\.openURL, OpenURLAction { link in handle(link) })
        .environment(\.feishuDocsContext, FeishuDocsRenderContext(images: images, selectable: copyAllowed, openImage: { openImage($0, in: images) }, manager: manager))
        .environment(\.feishuDocComment, comment)
        .sheet(isPresented: $showOutline) {
            FeishuDocsOutlineSheet(items: outline) { item in
                showOutline = false
                jump = rowID(for: item)
            }
            .presentationDetents([.medium, .large])
            #if os(macOS)
            .macSheet()
            #endif
        }
        .sheet(isPresented: $showComments) {
            NavigationStack { FeishuDocsCommentsView(url: url, title: docTitle, manager: manager) }
                .presentationDetents([.large])
                #if os(macOS)
                .macSheet()
                #endif
        }
        .sheet(item: $passage) { p in
            NavigationStack { FeishuDocsCommentsView(url: url, title: docTitle, passage: .passage(quote: p.quote, blockID: p.blockID), manager: manager) }
                .presentationDetents([.large])
                #if os(macOS)
                .macSheet()
                #endif
        }
        .fullScreenCover(item: $gallery) { g in ImageViewer(urls: g.urls.map(media), index: g.index) }
    }

    private var api: APIClient? { app.api?.manager(manager) }

    /// A picture's or file's URL as this reader's manager reads it.
    private func media(_ url: String) -> String { LarkMedia.url(url, manager: manager) }

    // MARK: Header

    private var docTitle: String {
        switch model.content {
        case .markdown(_, let t, _): t
        case .tree(let v): v.title.isEmpty ? (title ?? String(localized: "Document")) : v.title
        case .unsupported(let u): u.title
        case nil: title ?? String(localized: "Document")
        }
    }

    private var titleBlock: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if let crumbs = breadcrumb { Text(crumbs).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head) }
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                if let icon = model.header?.icon, icon.type == "emoji" { Text(icon.value).font(Theme.Font.display) }
                Text(docTitle).font(Theme.Font.display).fixedSize(horizontal: false, vertical: true)
                    .modifier(FeishuDocsSelectable(enabled: copyAllowed))
            }
            HStack(spacing: Theme.Space.s) {
                if let owner = model.info?.owner ?? treeOwner {
                    Button {
                        if let id = owner.id { open?(.profile(LarkPersonRef(id: id, name: owner.name, avatar: owner.avatarUrl), manager: manager)) }
                    } label: {
                        HStack(spacing: Theme.Space.sm) {
                            LarkAvatarImage(name: owner.name, url: owner.avatarUrl.map(media), size: 22)
                            Text(owner.name).font(Theme.Font.captionMedium).foregroundStyle(.primary).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .disabled(owner.id == nil)
                }
                if let edited = editedLine { Text("· " + edited).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1) }
                Spacer(minLength: 0)
            }
            if let stats = statsLine {
                Text(stats).font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1)
            }
        }
        .padding(.bottom, Theme.Space.xs)
    }

    private var breadcrumb: String? {
        guard let h = model.header else { return nil }
        let names = (h.path ?? []).map(\.name)
        if h.inMySpace == true { return ([String(localized: "My Space")] + names).joined(separator: " / ") }
        return names.isEmpty ? nil : names.joined(separator: " / ")
    }

    private var treeOwner: FeishuDocsPerson? { if case .tree(let v) = model.content { v.owner } else { nil } }

    private var editedLine: String? {
        let when = model.header?.editedAt.flatMap(JSON.parseDate) ?? model.info?.updatedAt.flatMap(JSON.parseDate)
        guard let when else { return nil }
        let t = FeishuTime.short(when)
        let owner = (model.info?.owner ?? treeOwner)?.name
        if let who = model.header?.editorName, !who.isEmpty, who != owner { return String(localized: "Updated by \(who) \(t)") }
        return String(localized: "Updated \(t)")
    }

    private var statsLine: String? {
        guard let i = model.info else { return nil }
        var parts: [String] = []
        if let v = i.views, v > 0 { parts.append(String(localized: "\(v) views")) }
        if let v = i.visitors, v > 0 { parts.append(String(localized: "\(v) visitors")) }
        if let l = i.likes, l > 0 { parts.append(String(localized: "\(l) likes")) }
        if let c = i.comments, c > 0 { parts.append(String(localized: "\(c) comments")) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    @ViewBuilder private var summaryCard: some View {
        if let s = model.summary, s.state == "ready", !s.summary.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                HStack(spacing: Theme.Space.sm) {
                    Image(systemName: Theme.Icon.ai).foregroundStyle(LarkColor.hue("purple"))
                    Text("AI Summary").font(Theme.Font.secondaryStrong)
                    Spacer()
                    Button(summaryExpanded ? String(localized: "Collapse") : String(localized: "Expand")) { withAnimation(Theme.Motion.standard) { summaryExpanded.toggle() } }
                        .font(Theme.Font.caption).lineLimit(1)
                }
                Markdown(Links.markdown(s.summary))
                    .markdownTheme(.feishuDocsSmall)
                    .frame(maxHeight: summaryExpanded ? nil : 96, alignment: .top)
                    .clipped()
                    .mask(alignment: .top) {
                        if summaryExpanded { Rectangle() } else {
                            LinearGradient(stops: [.init(color: .black, location: 0.7), .init(color: .clear, location: 1)], startPoint: .top, endPoint: .bottom)
                        }
                    }
                    .contentShape(.rect)
                    .onTapGesture { withAnimation(Theme.Motion.standard) { summaryExpanded.toggle() } }
            }
            .padding(Theme.Space.m)
            .background(LarkColor.hue("purple").opacity(0.07), in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(LarkColor.hue("purple").opacity(0.18), lineWidth: 0.5))
        }
    }

    private var footer: some View {
        VStack(spacing: Theme.Space.m) {
            Divider()
            HStack(spacing: Theme.Space.m) {
                if (model.info?.comments ?? 0) > 0 {
                    Button { showComments = true } label: {
                        Label("\(model.info?.comments ?? 0) comments", systemImage: Theme.Icon.conversation).lineLimit(1)
                    }
                    .themedButton()
                } else if app.larkFeatures.docComments {
                    Button { showComments = true } label: {
                        Label("Comment", systemImage: Theme.Icon.conversation).lineLimit(1)
                    }
                    .themedButton()
                }
                Button { FeishuDocsLinks.openInFeishu(url) } label: {
                    Label("Open in Lark [Feishu Doc Reader 76d0eb]", systemImage: Theme.Icon.openExternal).lineLimit(1)
                }
                .themedButton()
            }
            .font(Theme.Font.secondary)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, Theme.Space.xl)
        .padding(.bottom, Theme.Space.m)
    }

    // MARK: Tree

    @ViewBuilder private func treeRows(_ view: FeishuDocsView) -> some View {
        let blocks = view.content.children
        let numbers = FeishuDocsTree.orderedNumbers(blocks)
        ForEach(Array(blocks.enumerated()), id: \.offset) { i, node in
            FeishuDocsBlock(node: node, number: numbers[i])
                .id(FeishuDocsTree.rowKey(node, index: i))
        }
        if view.partial == true {
            Label("This doc is long. Only the beginning is shown.", systemImage: Theme.Icon.moreMenu).font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }

    // MARK: Outline, images, links

    private var outline: [FeishuDocsOutlineItem] {
        switch model.content {
        case .markdown(let b, _, _): b.outline
        case .tree(let v): v.outline.isEmpty ? FeishuDocsTree.outline(v.content) : v.outline
        default: []
        }
    }

    private func rowID(for item: FeishuDocsOutlineItem) -> String? {
        switch model.content {
        case .markdown: item.id
        case .tree(let v): FeishuDocsTree.rowID(for: item.id.replacingOccurrences(of: "doc-block-", with: ""), in: v.content)
            ?? FeishuDocsTree.rowID(for: item.id, in: v.content)
        default: nil
        }
    }

    private var images: [String] {
        switch model.content {
        case .markdown(let b, _, _): b.images
        case .tree(let v): FeishuDocsTree.images(v.content)
        default: []
        }
    }

    private var copyAllowed: Bool {
        switch model.content {
        case .markdown(_, _, let copy): copy
        case .tree(let v): v.authority?.copy ?? true
        default: true
        }
    }

    private func openImage(_ src: String, in list: [String]) {
        let all = list.isEmpty ? [src] : list
        gallery = FeishuDocsGallery(urls: all, index: all.firstIndex(of: src) ?? 0)
    }

    /// Mentions open the profile card, Feishu doc links the native reader, the rest the system.
    private func handle(_ link: URL) -> OpenURLAction.Result {
        if link.scheme == feishuDocsUserScheme, let id = link.host() {
            let name = URLComponents(url: link, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "name" }?.value ?? ""
            open?(.profile(LarkPersonRef(id: id, name: name), manager: manager))
            return .handled
        }
        if link.scheme == nil, link.path.hasPrefix("/messenger/") { return .discarded }
        if let open, FeishuLinks.isDoc(link) {
            open(.doc(url: link.absoluteString, title: nil, manager: manager))
            return .handled
        }
        #if os(macOS)
        return LinkPolicy.system(link) // NSWorkspace would run any scheme it names
        #else
        return .systemAction
        #endif
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        ToolbarItemGroup(placement: .topBarTrailing) {
            if !outline.isEmpty {
                Button { showOutline = true } label: { Label("Folder [Task Views 52daa7]", systemImage: "list.bullet.indent") }
            }
            if model.content != nil, case .unsupported = model.content {} else if model.content != nil {
                Button { showComments = true } label: {
                    Image(systemName: Theme.Icon.conversation)
                        .overlay(alignment: .topTrailing) {
                            if let n = model.info?.comments, let text = CountBadge.text(n) {
                                Text(text)
                                    .font(.system(size: 9, weight: .bold).monospacedDigit()) // type-scale: glyph (a count on an icon)
                                    .foregroundStyle(.white)
                                    .padding(.horizontal, Theme.Space.xs)
                                    .scaledFrame(minWidth: Theme.Size.markS, minHeight: Theme.Size.markS)
                                    .background(LarkColor.hue("red"), in: .capsule)
                                    .offset(x: 9, y: -7)
                            }
                        }
                }
                .accessibilityLabel("Comments")
            }
            Menu {
                FeishuDocsLinkMenu(url: url)
                Divider()
                Button { Task { await model.load(url: url, title: title, api: api) } } label: { Label("Refresh", systemImage: Theme.Icon.retry) }
            } label: { Image(systemName: Theme.Icon.more) }
            .accessibilityLabel("More")
        }
    }
}

struct FeishuDocsGallery: Identifiable {
    var urls: [String]
    var index: Int
    var id: String { urls.joined() + "#\(index)" }
}

/// Comment on This where the plugin can write comments (`LarkFeatures.docComments`); nil elsewhere.
extension FeishuDocReader {
    var comment: ((String, String?) -> Void)? {
        guard app.larkFeatures.docComments else { return nil }
        return { [$passage] quote, block in
            let q = quote.trimmingCharacters(in: .whitespacesAndNewlines)
            if !q.isEmpty { $passage.wrappedValue = FeishuDocsPassage(quote: q, blockID: block) }
        }
    }
}

/// A passage to comment on: its text and the block it is in, when the reader knows (the JSON tree's).
struct FeishuDocsPassage: Identifiable, Hashable {
    var quote: String
    var blockID: String?
    var id: String { (blockID ?? "") + "\n" + quote }
}

/// The Markdown's paragraphs (list items' too) commentable (`FeishuDocsCommentable`); the theme's paragraph as it is.
private struct FeishuDocsMarkdownComments: ViewModifier {
    var comment: ((String, String?) -> Void)?
    var copyable: Bool

    func body(content: Content) -> some View {
        if let comment {
            content.markdownBlockStyle(\.paragraph) { config in
                // MarkdownUI.Theme.persona's paragraph (the reader's theme keeps it), with the long press.
                config.label.fixedSize(horizontal: false, vertical: true).relativeLineSpacing(.em(0.26)).markdownMargin(top: 0, bottom: 10)
                    .modifier(FeishuDocsCommentable(text: config.content.renderPlainText(), blockID: nil, copyable: copyable, comment: comment))
            }
        } else {
            content
        }
    }
}

private struct FeishuDocsSelectable: ViewModifier {
    var enabled: Bool
    func body(content: Content) -> some View {
        if enabled { content.textSelection(.enabled) } else { content }
    }
}

/// Markdown images through the hub (as `manager` reads them), tappable into the gallery.
struct FeishuDocsImageProvider: ImageProvider {
    var manager: String? = nil
    var open: (String) -> Void

    func makeImage(url: URL?) -> some View {
        let src = url?.absoluteString ?? ""
        return Button { open(src) } label: {
            LarkRemoteImage(url: LarkMedia.url(src, manager: manager), maxPixel: 1600, contentMode: .fit)
                .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.box60)
                .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
        }
        .buttonStyle(.plain)
        .padding(.vertical, Theme.Space.xs)
    }
}

extension MarkdownUI.Theme {
    /// Long-form reading: the app's Markdown theme with document headings and Feishu-blue links.
    @MainActor static var feishuDocs: MarkdownUI.Theme {
        MarkdownUI.Theme.persona
            .link { ForegroundColor(LarkColor.blue) }
            .heading1 { config in
                config.label.markdownMargin(top: 20, bottom: 8).markdownTextStyle { FontWeight(.bold); FontSize(.em(1.45)) }
            }
            .heading2 { config in
                config.label.markdownMargin(top: 16, bottom: 6).markdownTextStyle { FontWeight(.bold); FontSize(.em(1.25)) }
            }
            .heading3 { config in
                config.label.markdownMargin(top: 12, bottom: 4).markdownTextStyle { FontWeight(.semibold); FontSize(.em(1.1)) }
            }
            .heading4 { config in
                config.label.markdownMargin(top: 10, bottom: 4).markdownTextStyle { FontWeight(.semibold) }
            }
    }

    @MainActor static var feishuDocsSmall: MarkdownUI.Theme {
        MarkdownUI.Theme.persona
            .text { FontSize(Theme.Level.secondary.size * Theme.Font.scale) }
            .paragraph { config in config.label.fixedSize(horizontal: false, vertical: true).relativeLineSpacing(.em(0.15)).markdownMargin(top: 0, bottom: 6) }
    }
}

/// Grey bars in the shape of a document while it loads.
struct FeishuDocsSkeleton: View {
    var title: String?

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.l) {
            if let title {
                Text(title).font(Theme.Font.display).fixedSize(horizontal: false, vertical: true)
            } else {
                bar(0.7, height: 26)
            }
            HStack(spacing: Theme.Space.s) { Circle().fill(Color.primary.opacity(0.08)).scaledFrame(width: Theme.Size.iconS, height: Theme.Size.iconS); bar(0.35, height: 12) }
            ForEach(0..<9, id: \.self) { i in bar([0.95, 0.88, 0.6, 0.92, 0.8, 0.45, 0.9, 0.85, 0.5][i], height: 14) }
            ProgressView().frame(maxWidth: .infinity).padding(.top, Theme.Space.s)
        }
        .redacted(reason: [])
    }

    private func bar(_ fraction: CGFloat, height: CGFloat) -> some View {
        GeometryReader { g in
            RoundedRectangle(cornerRadius: 4).fill(Color.primary.opacity(0.07)).frame(width: g.size.width * fraction)
        }
        .frame(height: height)
    }
}

/// Bases, whiteboards and files: what it is, a preview when there is one, and Open in Lark.
struct FeishuDocsUnsupportedView: View {
    var info: FeishuDocsUnsupported
    var url: String
    /// The manager the reader reads through: the preview and the download name it.
    var manager: String? = nil
    @State private var request: (url: String, name: String)?
    @State private var gallery: FeishuDocsGallery?

    var body: some View {
        VStack(spacing: Theme.Space.l) {
            if let image = info.image.map({ LarkMedia.url($0, manager: manager) }) {
                Button { gallery = FeishuDocsGallery(urls: [image], index: 0) } label: {
                    LarkRemoteImage(url: image, maxPixel: 2000, contentMode: .fit)
                        .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.box200)
                        .clipShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(LarkColor.cardStroke, lineWidth: 0.5))
                }
                .buttonStyle(.plain)
                Text(info.title).font(Theme.Font.heading).multilineTextAlignment(.center)
            } else {
                let file = info.kind == .file ? FeishuDocsFileKind.symbol(ext: info.file?.ext, title: info.title) : nil
                FeishuAppIcon(symbol: file?.symbol ?? info.kind.symbol, hue: file?.hue ?? info.kind.hue, size: 72)
                    .padding(.top, 48)
                Text(info.title).font(Theme.Font.title).multilineTextAlignment(.center)
                Text(info.message).font(Theme.Font.secondary).foregroundStyle(.secondary).multilineTextAlignment(.center)
            }
            VStack(spacing: Theme.Space.m) {
                if let f = info.file {
                    Button {
                        request = (LarkMedia.url("/api/v1/lark/raw/documents/drive/files/\(f.token)/download", manager: manager), info.title)
                    } label: {
                        Label("Preview File", systemImage: Theme.Icon.view).lineLimit(1).scaledFrame(minWidth: Theme.Size.box180)
                    }
                    .themedButton(prominent: true)
                }
                Button { FeishuDocsLinks.openInFeishu(url) } label: {
                    Label("Open in Lark [Feishu Doc Reader 76d0eb]", systemImage: Theme.Icon.openExternal).lineLimit(1).scaledFrame(minWidth: Theme.Size.box180)
                }
                .themedButton(prominent: info.file == nil)
            }
            .padding(.top, Theme.Space.s)
        }
        .frame(maxWidth: .infinity)
        .modifier(LarkFileOpener(request: $request))
        .fullScreenCover(item: $gallery) { g in ImageViewer(urls: g.urls, index: g.index) }
    }
}

/// The outline: headings indented by level; picking one scrolls the reader there.
struct FeishuDocsOutlineSheet: View {
    var items: [FeishuDocsOutlineItem]
    var pick: (FeishuDocsOutlineItem) -> Void
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let top = items.map(\.level).min() ?? 1
        NavigationStack {
            List(items) { item in
                Button { pick(item) } label: {
                    Text(item.title)
                        .font(item.level == top ? Theme.Font.heading : Theme.Font.secondary)
                        .foregroundStyle(item.level == top ? .primary : .secondary)
                        .lineLimit(2)
                        .padding(.leading, CGFloat(min(item.level - top, 4)) * 16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
            .listStyle(.plain)
            .navigationTitle("Folder [Task Views 52daa7]")
            .navigationBarTitleDisplayMode(.inline)
            .macSheetDone { dismiss() }
        }
    }
}

/// The reader's Open in Lark / Copy Link / Share menu items.
struct FeishuDocsLinkMenu: View {
    var url: String

    var body: some View {
        Button { FeishuDocsLinks.openInFeishu(url) } label: { Label("Open in Lark [Feishu Doc Reader 76d0eb]", systemImage: Theme.Icon.openExternal) }
        Button { UIPasteboard.general.string = url } label: { Label("Copy Link", systemImage: Theme.Icon.link) }
        if let u = URL(string: url) { ShareLink(item: u) { Label("Share", systemImage: Theme.Icon.share) } }
    }
}
