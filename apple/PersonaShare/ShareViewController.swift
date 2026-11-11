import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// Share › Persona (#4586, #1460): text, a link or pictures from another app go to the conversation or agent picked in
/// the sheet (Persona's main conversation unless a recent one was used within a day), or start a new chat, with a note
/// typed here. The extension talks to the hub itself (the app may not be running): it reads the pairing the app keeps in
/// the keychain, never writing it, and sends like the composer does. The rules are `ShareTargets` (Shared).
final class ShareViewController: UIViewController {
    override func viewDidLoad() {
        super.viewDidLoad()
        let model = ShareModel(items: (extensionContext?.inputItems as? [NSExtensionItem]) ?? []) { [weak self] sent in
            if sent {
                self?.extensionContext?.completeRequest(returningItems: nil)
            } else {
                self?.extensionContext?.cancelRequest(withError: CocoaError(.userCancelled))
            }
        }
        let host = UIHostingController(rootView: ShareView(model: model))
        addChild(host)
        host.view.frame = view.bounds
        host.view.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(host.view)
        host.didMove(toParent: self)
        Task { await model.load() }
    }
}

@MainActor @Observable final class ShareModel {
    enum Phase: Equatable { case loading, ready, sending, failed(String) }

    /// A file to upload: a copy in the extension's own temporary folder (the host app's goes away with its callback).
    struct SharedFile: Identifiable, Equatable {
        let id = UUID()
        var url: URL
        var name: String
        var size: Int64
    }

    var note = ""
    private(set) var text = ""
    private(set) var images: [UIImage] = []
    private(set) var files: [SharedFile] = []
    /// Files over `ShareTargets.fileLimit`, left out (each named on the sheet).
    private(set) var tooLarge: [String] = []
    /// Bytes of the files uploaded so far while sending.
    private(set) var sentBytes: Int64 = 0
    private(set) var phase = Phase.loading
    /// What can be sent to, once the hub answered; nil while loading and when it did not (`targetsFailed`).
    private(set) var listed: ShareTargets.Listed?
    /// The ones shared to most recently that are still listed (at most `ShareTargets.first`).
    private(set) var recent: [ShareTarget] = []
    private(set) var targetsFailed: String?
    var selected = ShareTarget.newChat
    private var api: APIClient?
    /// Where Persona's conversations and a new chat run: their files go there.
    private var assistantMachine: String?
    /// Files already on a computer (by file, with the computer they went to): a retry does not send them again.
    private var uploaded: [UUID: (machine: String, path: String)] = [:]
    private let store = ShareTargetStore()
    /// The same on a retry: a hub that took the message and did not answer types it once.
    private let clientMsgID = UUID().uuidString
    private let staging = FileManager.default.temporaryDirectory.appending(path: "share/" + UUID().uuidString)
    private let items: [NSExtensionItem]
    private let finish: (Bool) -> Void

    /// What Persona's composer sends at most (ChatView `add`); more pictures go as files.
    static let maxImages = 4

    init(items: [NSExtensionItem], finish: @escaping (Bool) -> Void) {
        self.items = items
        self.finish = finish
    }

    var canSend: Bool { phase == .ready && listed != nil && hasContent }
    var hasContent: Bool { !(message.isEmpty && images.isEmpty && files.isEmpty) }
    var totalBytes: Int64 { files.reduce(0) { $0 + $1.size } }

    /// The note, then what was shared (a page's title and its link, or the text).
    var message: String {
        [note.trimmingCharacters(in: .whitespacesAndNewlines), text].filter { !$0.isEmpty }.joined(separator: "\n\n")
    }

    /// The sheet's own rows: the main conversation, the recent ones, the one picked from the full list, a new chat.
    var quick: [ShareTarget] {
        guard let listed else { return [] }
        var out = (listed.main.map { [$0] } ?? []) + recent
        if selected.kind != .newChat, !out.contains(where: { $0.id == selected.id }) { out.append(selected) }
        return out + [.newChat]
    }

    func load() async {
        async let targets: Void = loadTargets()
        await loadItems()
        await targets
        phase = .ready
    }

    func loadTargets() async {
        targetsFailed = nil
        do {
            let api = try await ShareTargets.client()
            async let assistant = try? api.assistant()
            async let machines = try? api.machines()
            let sessions = try await api.sessions()
            let state = await assistant
            let listed = ShareTargets.listed(assistant: state, sessions: sessions, machines: await machines ?? [])
            let stored = store.read()
            self.api = api
            assistantMachine = state?.machineID
            self.listed = listed
            recent = ShareTargets.recentFirst(listed, recent: stored)
            selected = ShareTargets.preselected(listed, recent: stored, now: .now)
        } catch {
            targetsFailed = ShareTargets.sentence(error)
        }
    }

    private func loadItems() async {
        var parts: [String] = []
        for item in items {
            if let title = item.attributedContentText?.string.trimmingCharacters(in: .whitespacesAndNewlines), !title.isEmpty {
                parts.append(title)
            }
            for provider in item.attachments ?? [] {
                if images.count < Self.maxImages, let img = await provider.image() {
                    images.append(img)
                } else if let type = provider.fileType {
                    await add(try? await provider.staged(type, into: staging))
                } else if provider.hasItemConformingToTypeIdentifier(UTType.url.identifier), let url = try? await provider.url() {
                    if url.isFileURL {
                        await add(try? await NSItemProvider.staged(url, into: staging))
                    } else if !parts.contains(url.absoluteString) {
                        parts.append(url.absoluteString)
                    }
                } else if provider.hasItemConformingToTypeIdentifier(UTType.plainText.identifier) {
                    if let s = try? await provider.text(), !s.isEmpty, !parts.contains(s) { parts.append(s) }
                }
            }
        }
        text = parts.joined(separator: "\n")
    }

    private func add(_ copy: URL?) async {
        guard let copy, files.count < ShareTargets.maxFiles else { return }
        let size = Int64((try? copy.resourceValues(forKeys: [.fileSizeKey]).fileSize) ?? 0)
        if size > ShareTargets.fileLimit {
            tooLarge.append(copy.lastPathComponent)
            try? FileManager.default.removeItem(at: copy)
        } else {
            files.append(SharedFile(url: copy, name: copy.lastPathComponent, size: size))
        }
    }

    func cancel() { done(false) }

    private func done(_ sent: Bool) {
        try? FileManager.default.removeItem(at: staging)
        finish(sent)
    }

    func send() async {
        guard let api, phase == .ready || isFailed, listed != nil, hasContent else { return }
        phase = .sending
        let target = selected
        var paths: [String] = []
        if !files.isEmpty {
            guard let machine = ShareTargets.machine(for: target, assistantMachine: assistantMachine) else {
                phase = .failed(ShareTargets.sentence(URLError(.cannotConnectToHost)))
                return
            }
            sentBytes = 0
            for f in files {
                if let up = uploaded[f.id], up.machine == machine {
                    paths.append(up.path)
                    sentBytes += f.size
                    continue
                }
                let before = sentBytes
                do {
                    let out = try await api.upload(file: f.url, name: f.name, machine: machine) { [weak self] sent in
                        Task { @MainActor in self?.sentBytes = max(self?.sentBytes ?? 0, before + sent) }
                    }
                    uploaded[f.id] = (machine, out.path)
                    paths.append(out.path)
                    sentBytes = before + f.size
                } catch {
                    phase = .failed(ShareTargets.fileSentence(error, name: f.name))
                    return
                }
            }
        }
        let payload = images.compactMap(ShareModel.jpeg)
        let text = ShareTargets.message(message, paths: paths)
        let body = text.isEmpty ? String(localized: "Please look at the image [Assistant View 5ad878]") : text
        do {
            let went = try await ShareTargets.send(api, to: target, text: body, images: payload, clientMsgID: clientMsgID)
            store.remember(target, started: went)
            done(true)
        } catch {
            phase = .failed(ShareTargets.sentence(error))
        }
    }

    var isFailed: Bool { if case .failed = phase { true } else { false } }

    /// As the composer sends a picture: at most 1600 px a side, JPEG.
    private static func jpeg(_ img: UIImage) -> ImagePayload? {
        let side = max(img.size.width, img.size.height)
        let scale = side > 1600 ? 1600 / side : 1
        let size = CGSize(width: (img.size.width * scale).rounded(), height: (img.size.height * scale).rounded())
        let format = UIGraphicsImageRendererFormat.default()
        format.scale = 1
        let scaled = UIGraphicsImageRenderer(size: size, format: format).image { _ in img.draw(in: CGRect(origin: .zero, size: size)) }
        return scaled.jpegData(compressionQuality: 0.75).map { ImagePayload(mime: "image/jpeg", data: $0.base64EncodedString()) }
    }
}

struct ShareView: View {
    @Bindable var model: ShareModel

    var body: some View {
        NavigationStack {
            Form {
                // Not focused when the sheet opens: the keyboard would hide where it goes (#1460), and a recent target
                // may be picked already.
                Section {
                    TextField(String(localized: "Add a note (optional)"), text: $model.note, axis: .vertical)
                        .lineLimit(2...6)
                }
                targets
                if !model.text.isEmpty || !model.images.isEmpty || !model.files.isEmpty || !model.tooLarge.isEmpty {
                    Section("Shared") {
                        if !model.text.isEmpty {
                            Text(model.text).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(6)
                        }
                        if !model.images.isEmpty {
                            Label("\(model.images.count) images", systemImage: Theme.Icon.photos)
                        }
                        ForEach(model.files) { f in
                            LabeledContent {
                                Text(f.size.storageText).font(Theme.Font.label).foregroundStyle(.secondary)
                            } label: {
                                Label(f.name, systemImage: Theme.Icon.file).lineLimit(1).truncationMode(.middle)
                            }
                        }
                        ForEach(model.tooLarge, id: \.self) { name in
                            Label(String(localized: "“\(name)” is over 200 MB and can't be sent"), systemImage: Theme.Icon.error)
                                .foregroundStyle(.secondary)
                        }
                        if model.phase == .sending, model.totalBytes > 0 {
                            ProgressView(value: Double(min(model.sentBytes, model.totalBytes)), total: Double(model.totalBytes))
                        }
                    }
                }
                if case .failed(let why) = model.phase {
                    Section { Label(why, systemImage: Theme.Icon.error) }
                }
            }
            .navigationTitle("Send to Persona")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { model.cancel() } }
                ToolbarItem(placement: .confirmationAction) {
                    if model.phase == .sending {
                        ProgressView()
                    } else {
                        Button("Send") { Task { await model.send() } }.disabled(!model.canSend && !(model.isFailed && model.listed != nil && model.hasContent))
                    }
                }
            }
        }
    }

    /// Send To: the sheet's own rows, and the whole list to search.
    @ViewBuilder private var targets: some View {
        Section("Send To [Share Sheet]") {
            if let why = model.targetsFailed {
                Label(why, systemImage: Theme.Icon.error)
                Button("Try Again") { Task { await model.loadTargets() } }
            } else if model.listed == nil {
                ProgressView().frame(maxWidth: .infinity)
            } else {
                ForEach(model.quick) { t in
                    ShareTargetRow(target: t, selected: t.id == model.selected.id) { model.selected = t }
                }
                NavigationLink {
                    ShareTargetPicker(model: model)
                } label: {
                    Label("All Chats and Agents", systemImage: Theme.Icon.search).lineLimit(1)
                }
                .accessibilityIdentifier("share-all-targets")
            }
        }
    }
}

/// Every target, searchable: the recent ones, Persona's conversations, the agents. A tap picks it and comes back.
struct ShareTargetPicker: View {
    @Bindable var model: ShareModel
    @State private var query = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let listed = model.listed ?? ShareTargets.Listed()
        let searching = !query.trimmingCharacters(in: .whitespaces).isEmpty
        let recentIDs = Set(model.recent.map(\.id))
        let persona = (listed.main.map { [$0] } ?? []) + [.newChat] + listed.conversations.filter { searching || !recentIDs.contains($0.id) }
        let agents = listed.agents.filter { searching || !recentIDs.contains($0.id) }
        let shownPersona = ShareTargets.matching(persona, query)
        let shownAgents = ShareTargets.matching(agents, query)
        List {
            if !searching && !model.recent.isEmpty {
                Section("Recent") { rows(model.recent) }
            }
            if !shownPersona.isEmpty {
                Section { rows(shownPersona) } header: { Text(verbatim: "Persona") }
            }
            if !shownAgents.isEmpty {
                Section("Agents") { rows(shownAgents) }
            }
        }
        .overlay {
            if searching && shownPersona.isEmpty && shownAgents.isEmpty { ContentUnavailableView.search(text: query) }
        }
        .searchable(text: $query, placement: Self.searchPlacement, prompt: Text("Search Chats and Agents"))
        .navigationTitle("Send To [Share Sheet]")
        .navigationBarTitleDisplayMode(.inline)
    }

    /// Always shown on the phone (not hidden until the list is pulled down); visionOS places it itself.
    private static var searchPlacement: SearchFieldPlacement {
        #if os(iOS)
        .navigationBarDrawer(displayMode: .always)
        #else
        .automatic
        #endif
    }

    private func rows(_ targets: [ShareTarget]) -> some View {
        ForEach(targets) { t in
            ShareTargetRow(target: t, selected: t.id == model.selected.id) {
                model.selected = t
                dismiss()
            }
        }
    }
}

/// One target, as the Agents list names it: what it is, its title, and the machine and state (an agent) or when it
/// last spoke (a conversation); a check on the picked one.
struct ShareTargetRow: View {
    var target: ShareTarget
    var selected: Bool
    var pick: () -> Void

    var body: some View {
        Button(action: pick) {
            HStack(spacing: Theme.Space.m) {
                mark.font(Theme.Font.body).frame(minWidth: Theme.Space.xl)
                VStack(alignment: .leading, spacing: Theme.Space.lines) {
                    Text(title).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(1)
                    if let detail { Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1) }
                }
                Spacer(minLength: Theme.Space.s)
                if selected {
                    Image(systemName: Theme.Icon.done).font(Theme.Font.bodyStrong).foregroundStyle(.tint)
                        .accessibilityHidden(true)
                }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityIdentifier("share-target-\(target.id)")
    }

    @ViewBuilder private var mark: some View {
        switch target.kind {
        case .main: Image(systemName: Theme.Icon.persona).foregroundStyle(.tint)
        case .newChat: Image(systemName: Theme.Icon.compose).foregroundStyle(.tint)
        case .conversation: Image(systemName: Theme.Icon.conversation).foregroundStyle(.secondary)
        case .agent: Image(systemName: Theme.Icon.dot).font(Theme.Font.label).foregroundStyle(target.state.color)
        }
    }

    private var title: String {
        target.kind == .main ? String(localized: "Persona (Main Chat)") : target.title
    }

    private var detail: String? {
        switch target.kind {
        case .main, .newChat: nil
        case .conversation: Fmt.relative(target.date).nilIfEmpty
        case .agent: [target.machine, target.state.label].filter { !$0.isEmpty }.joined(separator: " · ")
        }
    }
}

private extension String {
    var nilIfEmpty: String? { isEmpty ? nil : self }
}

private extension NSItemProvider {
    /// The type to upload this item as, when it is a file: a movie, a document or archive, a file's text (it has a
    /// name with an extension), a picture past the ones that go as pictures. Nil for a web link or selected text.
    var fileType: UTType? {
        let types = registeredContentTypes
        let named = suggestedName.map { !($0 as NSString).pathExtension.isEmpty } ?? false
        // Asked only once the pictures are full (`ShareModel.maxImages`): the rest go as files.
        if let image = types.first(where: { $0.conforms(to: .image) }) { return image }
        if let movie = types.first(where: { $0.conforms(to: .audiovisualContent) }) { return movie }
        return types.first { $0.conforms(to: .data) && !$0.conforms(to: .url) && (named || types.contains(.fileURL) || !$0.conforms(to: .text)) }
    }

    /// A copy of the item as `type`, in `dir`, under its own name.
    func staged(_ type: UTType, into dir: URL) async throws -> URL {
        let name = suggestedName.map { n in
            (n as NSString).pathExtension.isEmpty ? n + (type.preferredFilenameExtension.map { "." + $0 } ?? "") : n
        }
        return try await withCheckedThrowingContinuation { c in
            _ = loadFileRepresentation(for: type, openInPlace: false) { url, _, error in
                guard let url else { return c.resume(throwing: error ?? CocoaError(.fileReadUnknown)) }
                do { c.resume(returning: try Self.copy(url, as: name ?? url.lastPathComponent, into: dir)) } catch { c.resume(throwing: error) }
            }
        }
    }

    /// A copy of a file shared by its URL (another app's file), in `dir`.
    static func staged(_ url: URL, into dir: URL) async throws -> URL {
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        return try copy(url, as: url.lastPathComponent, into: dir)
    }

    /// Each copy in a folder of its own: two files may have the same name.
    nonisolated private static func copy(_ url: URL, as name: String, into dir: URL) throws -> URL {
        let folder = dir.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let dest = folder.appending(path: name)
        try FileManager.default.copyItem(at: url, to: dest)
        return dest
    }

    /// The item as a picture, when it is one: its own image type's bytes (Photos gives JPEG or HEIC; an app's UIImage
    /// comes as PNG), else the object itself. Asking for the abstract `public.image` gets nothing from most providers.
    func image() async -> UIImage? {
        guard let type = registeredContentTypes.first(where: { $0.conforms(to: .image) }) else { return nil }
        if let data = try? await data(for: type), let img = UIImage(data: data) { return img }
        let item = try? await loadItem(forTypeIdentifier: type.identifier)
        if let img = item as? UIImage { return img }
        if let url = item as? URL, let data = try? Data(contentsOf: url) { return UIImage(data: data) }
        return (item as? Data).flatMap(UIImage.init(data:))
    }

    func data(for type: UTType) async throws -> Data {
        try await withCheckedThrowingContinuation { c in
            _ = loadDataRepresentation(for: type) { data, error in
                if let data { c.resume(returning: data) } else { c.resume(throwing: error ?? CocoaError(.fileReadUnknown)) }
            }
        }
    }

    func url() async throws -> URL {
        let item = try await loadItem(forTypeIdentifier: UTType.url.identifier)
        if let url = item as? URL { return url }
        if let data = item as? Data, let url = URL(dataRepresentation: data, relativeTo: nil) { return url }
        throw CocoaError(.fileReadUnknown)
    }

    func text() async throws -> String {
        let item = try await loadItem(forTypeIdentifier: UTType.plainText.identifier)
        if let s = item as? String { return s }
        if let data = item as? Data, let s = String(data: data, encoding: .utf8) { return s }
        throw CocoaError(.fileReadUnknown)
    }
}
