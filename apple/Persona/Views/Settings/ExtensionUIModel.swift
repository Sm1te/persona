import Foundation
import Observation

// A plugin's declarative UI (docs/extensions/extensions.md「Declarative UI」): the plugin sends a View made of a few fixed kinds of items, the
// app draws them with its own design system (ExtensionViewRenderer) and sends the buttons back. Nothing of the plugin
// runs here; its tones become the app's role colors, it gives no colors, sizes or icons.

// The View, its components, actions and places are the schema's (apple/Shared/Generated/ExtensionWire.swift,
// schema/extension/view.schema.json); what follows is how the app reads them.

nonisolated extension ExtensionUIPlace: Identifiable {
    /// The extension that draws it: `extension` under /api/v1/extensions, `plugin` under the old path (#1844).
    var owner: String { self.extension?.isEmpty == false ? self.extension! : (plugin ?? "") }
    /// The extension's name (its entries say whose they are).
    var ownerName: String? { extensionName?.isEmpty == false ? extensionName : pluginName }

    /// The slots this app draws (anything else from a newer hub is ignored). `extension_page` is UI version 3's name for
    /// `plugin_page` (docs/extensions/extensions.md「UI version 3」, compatibility).
    static let slots: Set<String> = ["plugin_page", "extension_page", "launcher_section", "nav_page", "overview_card",
                                     "machine_section", "inbox_item"]

    /// An extension's own page in Settings › Extensions › it.
    var isExtensionPage: Bool { placement.isEmpty || placement == "plugin_page" || placement == "extension_page" }
}

nonisolated extension ExtensionUIContext {
    /// The UI version a whole page of the app draws (`ExtensionPageView`): 4 adds the code-hosting parts (header,
    /// breadcrumb, file_tree, code, diff, comment_thread, composer, tabs, the split layout) and actions' own values
    /// (#2961). Cards, widgets, the watch and the extensions keep `version` (Shared, 3): they draw none of them.
    static let pageVersion = 4

    /// This context for a whole page: the page's UI version.
    func forPage() -> Self {
        var c = self
        if c.size == nil || c.size == "full" { c.uiVersion = Self.pageVersion }
        return c
    }
}

nonisolated extension ExtensionChoice: Identifiable {
    var id: String { value }
    var label: String { title?.isEmpty == false ? title! : value }
}

nonisolated extension ExtensionComponent {
    /// The name a field or a segmented sends its value under.
    var valueName: String? {
        switch self {
        case .field(let f): f.name.isEmpty ? nil : f.name
        case .segmented(let s): s.name.isEmpty ? nil : s.name
        default: nil
        }
    }

    /// The component's `key` (unique on the page): what keeps its place when the page is drawn again.
    var key: String? {
        switch self {
        case .text(let c): c.key
        case .metric(let c): c.key
        case .progress(let c): c.key
        case .row(let c): c.key
        case .button(let c): c.key
        case .link(let c): c.key
        case .field(let c): c.key
        case .divider(let c): c.key
        case .chart(let c): c.key
        case .statGrid(let c): c.key
        case .segmented(let c): c.key
        case .markdown(let c): c.key
        case .table(let c): c.key
        case .empty(let c): c.key
        case .loading(let c): c.key
        case .html(let c): c.key
        case .header(let c): c.key
        case .breadcrumb(let c): c.key
        case .fileTree(let c): c.key
        case .code(let c): c.key
        case .diff(let c): c.key
        case .commentThread(let c): c.key
        case .composer(let c): c.key
        case .image(let c): c.key
        case .grid(let c): c.key
        case .carousel(let c): c.key
        case .unknown: nil
        }
    }
}

/// `persona://inbox/<item id>/source` (the id path-escaped): an inbox_item page asking to open its item's own source
/// page, what opening the item showed before the slot (a Feishu chat at the item's first message). The only `open_url`
/// that is not https, and only for the item the page is drawn for (docs/extensions/extensions.md「Inbox items」).
nonisolated enum InboxSourceLink {
    /// The item `url` opens the source of, nil when it is no such link.
    static func item(_ url: URL) -> String? {
        guard HubNaming.accepts(scheme: url.scheme),
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false), c.host?.lowercased() == "inbox" else { return nil }
        let parts = c.percentEncodedPath.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 3, parts[0].isEmpty, parts[2] == "source",
              let id = String(parts[1]).removingPercentEncoding, !id.isEmpty else { return nil }
        return id
    }
}

/// `persona://settings/extensions/<extension id>` (the id path-escaped, #5906): an extension's page asking to open its
/// own settings, Settings › Extensions › it. The hub lets an extension open only its own (docs/extensions/extensions.md).
nonisolated enum ExtensionSettingsLink {
    /// The extension whose settings `url` opens, nil when it is no such link.
    static func extensionID(_ url: URL) -> String? {
        guard HubNaming.accepts(scheme: url.scheme),
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false), c.host?.lowercased() == "settings" else { return nil }
        let parts = c.percentEncodedPath.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 3, parts[0].isEmpty, parts[1] == "extensions",
              let id = String(parts[2]).removingPercentEncoding, !id.isEmpty else { return nil }
        return id
    }
}

extension APIClient {
    /// What enabled extensions draw at a placement (a launcher's section: only the extensions whose gateway it is).
    /// No placement: every slot's but launchers' sections (docs/extensions/extensions.md「UI slots」). Placements this app does not
    /// draw are left out.
    func pluginUI(placement: String?, launcher: String? = nil, machine: String? = nil) async throws -> [ExtensionUIPlace] {
        let query = ["placement": placement, "launcher": launcher, "machine": machine].compactMapValues { $0?.isEmpty == false ? $0 : nil }
        let list: ExtensionUIPlaces = try await get(extensionPath("/-/ui"), query: query)
        return list.ui.filter { $0.placement.isEmpty || ExtensionUIPlace.slots.contains($0.placement) }
    }

    /// Draws (no action) or uses a control (an action, with the fields' values it sends).
    func pluginUI(_ ext: String, _ ui: String, context: ExtensionUIContext, state: String?, action: String? = nil,
                  values: [String: String]? = nil, manager: String? = nil) async throws -> ExtensionActionResult {
        let body = ExtensionUIRequest(context: context, state: state, action: action, values: values)
        let query = manager.map { ["manager": $0] } ?? [:]
        // The source subscription pages (#2041) are the manager's own, drawn and pressed as an extension page.
        let path = ext == SubscriptionsUI.id ? SubscriptionsUI.path : extensionPath("/\(ext)/-/ui/\(ui)")
        return try JSON.decoder.decode(ExtensionActionResult.self, from: try await request("POST", path, query: query, body: body))
    }
}

/// The extension models alive now, so that `ui.invalidate` reaches the ones drawing that extension (docs/extensions/extensions.md
/// 「ui.invalidate」). A model on screen draws again at once, one that is not when it is next shown (`ExtensionRefresh`).
@MainActor final class ExtensionUIRegistry {
    static let shared = ExtensionUIRegistry()
    private var models: [WeakModel] = []

    private struct WeakModel { weak var model: ExtensionViewModel? }

    func add(_ model: ExtensionViewModel) {
        models.removeAll { $0.model == nil }
        models.append(WeakModel(model: model))
    }

    /// The extension's data changed: `ui` the one of its interfaces, nil all of them.
    func invalidate(extension ext: String, ui: String?, group: String = "") {
        models.removeAll { $0.model == nil }
        for case let m? in models.map(\.model)
            where m.plugin == ext && (ui == nil || ui == m.ui) && m.group == group {
            m.invalidate()
        }
    }
}

/// One extension view on a page: drawing it, the fields' values as typed, using its controls (buttons, rows, search,
/// filter, load more), the page it asks to push or to pop back from.
@Observable final class ExtensionViewModel {
    struct Deps {
        var api: () -> APIClient?
        var connection: () -> LiveConnection? = { nil }
        static var live: Deps {
            let group = AppModel.shared.hub?.group ?? ""
            return .init(api: { AppModel.shared.api(group) }, connection: { AppModel.shared.socket(group) })
        }
    }

    /// What using a control came to, for the page to show: a word (a toast), a link to open in the app, or why it failed.
    struct Outcome: Equatable {
        var toast: String?
        var url: URL?
        var error: String?
        var lines: ExtensionUIStreamFileLines?
        var cancelled = false
    }

    private(set) var fileLines: [String: ExtensionUIStreamFileLines] = [:]
    @ObservationIgnored private(set) var stream: ExtensionPageStream?
    private(set) var confirmation: ExtensionPageStream.Frozen?
    @ObservationIgnored private var confirmReply: CheckedContinuation<Outcome, Never>?
    var managesConfirmation: Bool { stream?.supported == true }
    var fieldsEnabled: Bool { true } // Local drafts remain editable during snapshot recovery.
    func isEnabled(_ action: ExtensionAction) -> Bool { running == nil && (stream == nil || stream?.supported == false || stream?.ready == true) }

    func setVisible(_ visible: Bool) {
        if !visible { cancelConfirmation(); clearSecrets() }
        stream?.setVisible(visible)
    }
    func cancelConfirmation() {
        confirmation = nil; confirmReply?.resume(returning: Outcome(cancelled: true)); confirmReply = nil
    }
    func approveConfirmation() {
        guard let frozen = confirmation, let reply = confirmReply else { return }
        confirmation = nil; confirmReply = nil
        Task { reply.resume(returning: await performFrozen(frozen, confirmed: true)) }
    }
    private func clearSecrets() {
        for case .field(let field) in components where field.kind == "secret" { values[field.name] = "" }
    }
    private func performFrozen(_ frozen: ExtensionPageStream.Frozen, confirmed: Bool = false) async -> Outcome {
        guard let stream else { return Outcome() }
        do {
            if let push = frozen.action.push {
                guard stream.isCurrent(frozen) else {
                    throw ExtensionStreamJSON.Failure.version
                }
                if depth < Self.maxDepth { pushed = push }
                return Outcome()
            }
            let result = try await stream.perform(frozen, confirmed: confirmed)
            guard let result else { return Outcome() }
            guard stream.canPresent(frozen) else { return Outcome(cancelled: true) }
            if let lines = result.lines, lines.path == frozen.chosen["path"],
               lines.side == nil || lines.side == frozen.chosen["side"],
               components.contains(where: { $0.key == frozen.interaction.key }) {
                if fileLines.count < 64 || fileLines[frozen.interaction.key] != nil { fileLines[frozen.interaction.key] = lines }
            }
            if let push = result.push, depth < Self.maxDepth {
                pushed = ExtensionPush(ui: push.ui, state: push.state, title: push.title)
            }
            if result.pop == true { popped = true }
            let url = result.openUrl.flatMap(URL.init(string:)).flatMap { Self.opens($0, item: context.item, owner: plugin) ? $0 : nil }
            return Outcome(toast: result.toast, url: url, lines: result.lines)
        } catch {
            return stream.visible ? Outcome(error: String(localized: "Couldn't Load This Page")) : Outcome(cancelled: true)
        }
    }

    let plugin: String
    let ui: String
    /// Where it is drawn: the launcher or machine, the size, and the app's time zone, language, platform and UI version.
    let context: ExtensionUIContext
    /// How deep in its navigation stack the page is (the first is 1): no push past `maxDepth`.
    let depth: Int
    private(set) var view: ExtensionView?
    private(set) var loading = false
    private(set) var error: String?
    /// The hub's status when the last draw failed (403, 404, 502…); nil when it did not fail or never got an answer.
    private(set) var failedStatus: Int?
    /// The extension says this is not its business (a launcher whose gateway is not its own: 404 `unsupported`): the
    /// section is not drawn at all, and that is no error.
    private(set) var unsupported = false
    /// The control being used (its spinner), by action id.
    private(set) var running: String?
    /// The fields' and segmented's values by name (a toggle "true" / "false"); a secret starts empty, whatever the
    /// extension sends.
    var values: [String: String] = [:]
    /// What is typed in the page's search field.
    var query = ""
    /// The search the extension last answered or was last asked (the View's `search.value`): what is typed and not yet
    /// asked is not this, and a View drawn meanwhile leaves it alone.
    private(set) var searchedQuery = ""
    /// The page the extension asks to show on top of this one (`push`), shown by the page that hosts this view.
    var pushed: ExtensionPush?
    /// The extension asked to go back (`pop: true`): the page that hosts this view closes itself.
    var popped = false
    /// `ui.invalidate` came for it: drawn again at once while on screen, else when it is next shown.
    private(set) var stale = false
    /// Every invalidation bumps the version, so a second event arriving while a request is merged is not swallowed by the first refresh.
    private(set) var invalidationVersion = 0
    /// A refresh held back while busy is retried once the current operation ends; starting a request does not change the task key.
    private(set) var completedOperations = 0
    var group: String { deps.api()?.group ?? "" }

    func invalidate() {
        invalidationVersion += 1
        stale = true
    }
    /// It pushed a page by value (`splitPush`): drawn again when it is shown again.
    @ObservationIgnored var returning = false
    /// A section's `more` being loaded, by its action id: not asked again until it answers.
    private(set) var loadingMore: String?
    @ObservationIgnored private let deps: Deps
    /// The state the page starts with (a pushed page's `push.state`).
    @ObservationIgnored private let initialState: String?
    @ObservationIgnored private let initialManager: String?
    var selectedManager: String? { stream?.manager ?? initialManager }

    /// When the view on screen was last drawn (its `refresh_after` counts from here).
    private(set) var drawnAt: Date?

    static let maxDepth = 8

    init(plugin: String, ui: String, context: ExtensionUIContext = .here(), state: String? = nil, depth: Int = 1,
         deps: Deps = .live, manager: String? = nil) {
        self.plugin = plugin
        self.initialManager = manager
        self.ui = ui
        self.context = context
        self.initialState = state
        self.depth = depth
        self.deps = deps
        ExtensionUIRegistry.shared.add(self)
        #if os(iOS) || os(macOS)
        // The source subscription pages (#2041) are the manager's, drawn over HTTP only.
        if context.uiVersion == ExtensionUIContext.pageVersion, plugin != SubscriptionsUI.id, let connection = deps.connection() {
            let stream = ExtensionPageStream(connection: connection, owner: plugin, contribution: ui,
                                             context: context, state: state ?? "", manager: manager)
            self.stream = stream
            stream.onView = { [weak self] view in
                guard let self else { return }
                let old = self.components
                let next = view.sections.flatMap(\.items)
                self.fileLines = self.fileLines.filter { key, _ in
                    let previous = old.first { $0.key == key }
                    let current = next.first { $0.key == key }
                    return previous != nil && previous == current
                }
                self.clearSecrets()
                self.show(view, keeping: self.values)
            }
            stream.onFailure = { [weak self] _ in self?.error = String(localized: "Couldn't Load This Page") }
            stream.onLegacy = { [weak self] in Task { await self?.load() } }
            stream.resolveHostConfirmation = { [weak self] id in
                guard let self, let api = self.deps.api() else { throw ExtensionStreamJSON.Failure.unsupported }
                _ = try await api.resolve(id, Resolution(allow: true))
            }
        }
        #endif
    }

    deinit {
        if let stream { Task { @MainActor in stream.close() } }
    }

    func load() async {
        if let stream {
            if stream.tree != nil { stream.refresh(); return }
            // Negotiate only with this connection's hello; a declaration is not support.
            for _ in 0..<30 where stream.supported == nil {
                try? await Task.sleep(for: .milliseconds(100))
                if Task.isCancelled { return }
            }
            if stream.supported == true { stream.refresh(); return }
        }
        guard !loading else { return }
        loading = true
        defer { loading = false; completedOperations += 1 }
        do {
            let r = try await call(nil, values: nil)
            failedStatus = nil
            show(r.view)
            if r.view == nil { error = String(localized: "The extension returned nothing") }
        } catch let e as APIError where Self.isUnsupported(e) || (context.machine != nil && e.status == 404) {
            // A machine's section the hub says is not there (404: no such machine, or not the extension's business)
            // is not drawn at all.
            unsupported = true
            error = nil
            failedStatus = e.status
        } catch {
            self.error = error.localizedDescription
            failedStatus = (error as? APIError)?.status
        }
    }

    /// Capture on the native control's tap, before scheduling a Task or showing confirmation.
    func prepared(_ action: ExtensionAction, with chosen: [String: String] = [:],
                  displayed: ExtensionDisplayedRevision? = nil) -> @MainActor () async -> Outcome {
        guard let stream, stream.supported != false else {
            return { [weak self] in
                guard let self else { return Outcome(cancelled: true) }
                guard self.stream == nil || self.stream?.supported == false else { return Outcome(error: String(localized: "Couldn't Load This Page")) }
                return await self.perform(action, with: chosen)
            }
        }
        do {
            let sent = (action.submit == true ? submitted : [:]).merging(chosen) { _, c in c }
                .merging(action.values ?? [:]) { _, own in own }
            let secretNames = Set(components.compactMap { item -> String? in
                if case .field(let field) = item, field.kind == "secret" { return field.name }; return nil
            })
            let frozen = try stream.freeze(action, values: sent.filter { !secretNames.contains($0.key) }, displayed: displayed)
            return { [weak self] in
                guard let self else { return Outcome(cancelled: true) }
                guard self.running == nil, stream.isCurrent(frozen) else { return Outcome(error: String(localized: "Couldn't Load This Page")) }
                self.running = action.id
                defer { self.running = nil; self.completedOperations += 1 }
                if action.confirm?.isEmpty == false {
                    return await withCheckedContinuation { reply in self.confirmation = frozen; self.confirmReply = reply }
                }
                return await self.performFrozen(frozen)
            }
        } catch { return { Outcome(error: String(localized: "Couldn't Load This Page")) } }
    }

    func preparedMore(_ more: ExtensionMore, displayed: ExtensionDisplayedRevision) -> @MainActor () async -> Outcome {
        var chosen: [String: String] = [:]
        if view?.search != nil { chosen["q"] = query }
        if let filter = view?.filter { chosen[filter.name] = values[filter.name] ?? filter.value }
        let execute = prepared(more.action, with: chosen, displayed: displayed)
        return { [weak self] in
            guard let self, self.loadingMore == nil else { return Outcome(cancelled: true) }
            self.loadingMore = more.action.id
            defer { self.loadingMore = nil }
            return await execute()
        }
    }

    /// Uses a button or row (asked first by the page when it has `confirm`). A `push` opens its page without asking the
    /// extension; otherwise a new view replaces this one, and nothing in the answer draws it again.
    func perform(_ action: ExtensionAction, with chosen: [String: String] = [:]) async -> Outcome {
        if let push = action.push, stream == nil || stream?.supported == false {
            if depth < Self.maxDepth { pushed = push }
            return Outcome()
        }
        guard running == nil else { return Outcome() }
        running = action.id
        defer { running = nil; completedOperations += 1 }
        if let stream, stream.supported != false {
            do {
                let sent = (action.submit == true ? submitted : [:]).merging(chosen) { _, c in c }
                    .merging(action.values ?? [:]) { _, own in own }
                let safe = sent.filter { key, _ in !components.contains { item in
                    if case .field(let field) = item { return field.name == key && field.kind == "secret" }; return false
                } }
                let frozen = try stream.freeze(action, values: safe)
                if action.confirm?.isEmpty == false {
                    return await withCheckedContinuation { reply in confirmation = frozen; confirmReply = reply }
                }
                return await performFrozen(frozen)
            } catch { return Outcome(error: String(localized: "Couldn't Load This Page")) }
        }
        do {
            // The page's fields (submit), then the component's own values (a path, a line, what was written), then the
            // action's own (UI version 4): the later wins (docs/extensions/extensions.md「UI version 4: code hosting」).
            let sent = (action.submit == true ? submitted : [:]).merging(chosen) { _, c in c }
                .merging(action.values ?? [:]) { _, own in own }
            let r = try await call(action.id, values: sent.isEmpty ? nil : sent)
            return await take(r) { running = nil }
        } catch let e as APIError where Self.isUnsupported(e) {
            unsupported = true
            return Outcome()
        } catch {
            return Outcome(error: error.localizedDescription)
        }
    }

    /// An answer to a control: its view, its page to push or the way back, its word and link.
    /// `searched`: the answer to the search, which need not say again what was searched for (its `q` is in its state).
    /// `done`: ends the control's busy state (`running`, `loadingMore`) just before the answer is drawn. A view drawn while
    /// it is still busy keeps its new controls disabled after the state clears (#1937); an answer with nothing in it is
    /// drawn again first, still busy, so nothing else starts meanwhile.
    private func take(_ r: ExtensionActionResult, searched: Bool = false, done: () -> Void = {}) async -> Outcome {
        let again = r.view == nil && r.toast == nil && r.openUrl == nil && r.push == nil && r.pop != true
            ? try? await call(nil, values: nil) : nil
        done()
        if let v = r.view { show(v, keeping: [:], searched: searched) } else if let v = again?.view { show(v, keeping: values) }
        if let p = r.push, depth < Self.maxDepth { pushed = p }
        if r.pop == true { popped = true }
        let url = r.openUrl.flatMap(URL.init(string:)).flatMap { Self.opens($0, item: context.item, owner: plugin) ? $0 : nil }
        return Outcome(toast: r.toast?.isEmpty == false ? r.toast : nil, url: url)
    }

    /// A `segmented` picked: shown picked at once, and its action sent with `{name: value}` (and the page's fields when it
    /// submits); the extension answers with the view for it. Picking the one already picked does nothing.
    func choose(_ name: String, _ value: String, action: ExtensionAction) async -> Outcome {
        guard values[name] != value else { return Outcome() }
        let before = values[name]
        values[name] = value
        let o = await perform(action, with: [name: value])
        if o.error != nil || o.cancelled { values[name] = before }
        return o
    }

    /// The page's tabs (UI version 4): picked at once, its action sent with `{name: value}` and the search.
    func pickTab(_ value: String) async -> Outcome {
        guard let t = view?.tabs, values[t.name] != value else { return Outcome() }
        let before = values[t.name]
        values[t.name] = value
        var sent = [t.name: value]
        if view?.search != nil { sent["q"] = query }
        let o = await perform(t.action, with: sent)
        if o.error != nil { values[t.name] = before }
        return o
    }

    func searched(_ query: String, outcome: Outcome) {
        if outcome.error == nil && !outcome.cancelled { searchedQuery = query }
    }

    /// The page's search: its action with `{q}` (and the filter's value), once the typing pauses (the page waits). A
    /// newer search makes an older one's answer moot: only the last one's is shown.
    func search() async -> Outcome {
        guard let s = view?.search else { return Outcome() }
        if stream?.supported == true {
            searchedQuery = query
            var sent = ["q": query]
            if let filter = view?.filter { sent[filter.name] = values[filter.name] ?? filter.value }
            return await perform(s.action, with: sent)
        }
        searches += 1
        let mine = searches
        defer { completedOperations += 1 }
        searchedQuery = query
        var sent = ["q": query]
        if let f = view?.filter { sent[f.name] = values[f.name] ?? f.value }
        do {
            let r = try await call(s.action.id, values: sent)
            guard mine == searches else { return Outcome() }
            return await take(r, searched: true)
        } catch {
            return mine == searches ? Outcome(error: error.localizedDescription) : Outcome()
        }
    }

    @ObservationIgnored private var searches = 0

    /// The page's filter picked: shown picked at once, its action sent with `{name: value}` and what is in the search
    /// field.
    func filter(_ value: String) async -> Outcome {
        guard let f = view?.filter, values[f.name] != value else { return Outcome() }
        let before = values[f.name]
        values[f.name] = value
        var sent = [f.name: value]
        if view?.search != nil { sent["q"] = query }
        let o = await perform(f.action, with: sent)
        if o.error != nil { values[f.name] = before }
        return o
    }

    /// A section's `more` (scrolled to its end, or tapped): asked once until it answers; the extension answers with the
    /// whole new view.
    func more(_ m: ExtensionMore) async -> Outcome {
        guard loadingMore == nil, running == nil else { return Outcome() }
        if stream?.supported == true {
            var sent: [String: String] = [:]
            if view?.search != nil { sent["q"] = query }
            if let filter = view?.filter { sent[filter.name] = values[filter.name] ?? filter.value }
            return await perform(m.action, with: sent)
        }
        loadingMore = m.action.id
        defer { loadingMore = nil; completedOperations += 1 }
        do {
            var sent: [String: String] = [:]
            if view?.search != nil { sent["q"] = query }
            if let f = view?.filter { sent[f.name] = values[f.name] ?? f.value }
            let r = try await call(m.action.id, values: sent.isEmpty ? nil : sent)
            return await take(r) { loadingMore = nil }
        } catch {
            return Outcome(error: error.localizedDescription)
        }
    }

    // MARK: Controls (UI version 6, docs/extensions/extensions.md "UI version 6", "Controls")

    /// What a control shows while the user's value is asked about or on its way, by the control's name; the
    /// extension's value (`ExtensionRowControl.wire`) when it has none here.
    private(set) var controlValues: [String: String] = [:]
    /// Controls whose change has been on its way for over a second: a spinner shows beside them.
    private(set) var slowControls: Set<String> = []
    /// The controls whose change is on its way, each with the latest value it was changed to meanwhile (sent next).
    @ObservationIgnored private var controlFlights: [String: ControlFlight] = [:]
    private struct ControlFlight { var queued: (control: ExtensionRowControl, value: String)? }
    var sendsControls: Bool { true }

    func preview(_ name: String, _ value: String?) {
        if let value { controlValues[name] = value } else if controlFlights[name] == nil { controlValues[name] = nil }
    }

    /// A control changed (after its confirm): the new value shows at once and goes with the control's action as
    /// `{name: value}`; one request a control at a time, a change made meanwhile goes after it (only the latest). The
    /// answer's view or `items` say what the control is now, an answer with neither draws the page again; a failure
    /// puts back the extension's last value and says why. A redraw meanwhile keeps the user's value.
    func change(_ control: ExtensionRowControl, to value: String) async -> Outcome {
        let name = control.name
        guard !name.isEmpty, !control.action.id.isEmpty, control.disabled != true else { return Outcome() }
        controlValues[name] = value
        if controlFlights[name] != nil {
            controlFlights[name]?.queued = (control, value)
            return Outcome()
        }
        controlFlights[name] = ControlFlight()
        let slow = Task { [weak self] in
            try? await Task.sleep(for: .seconds(1))
            if !Task.isCancelled { self?.slowControls.insert(name) }
        }
        var next: (control: ExtensionRowControl, value: String)? = (control, value)
        var outcome = Outcome()
        var sent = value
        defer {
            slow.cancel()
            slowControls.remove(name)
            controlFlights[name] = nil
            // The answer's value from now on, unless the user is already at another (a stepper's taps not yet sent).
            if controlValues[name] == sent || outcome.error != nil || outcome.cancelled { controlValues[name] = nil }
            completedOperations += 1
        }
        while let n = next {
            sent = n.value
            outcome = await sendControl(n.control, n.value)
            next = outcome.error == nil && !outcome.cancelled ? controlFlights[name]?.queued : nil
            controlFlights[name]?.queued = nil
        }
        return outcome
    }

    private func sendControl(_ control: ExtensionRowControl, _ value: String) async -> Outcome {
        // A native stream page sends it as its other actions (frozen against the tree on screen).
        if let stream, stream.supported != false { return await perform(control.action, with: [control.name: value]) }
        do {
            // The page's fields (submit), the action's own values, then the control's: the control's wins.
            let sent = (control.action.submit == true ? submitted : [:])
                .merging(control.action.values ?? [:]) { _, own in own }
                .merging([control.name: value]) { _, mine in mine }
            let r = try await call(control.action.id, values: sent)
            if let v = r.view {
                show(v, keeping: values)
            } else if let items = r.items, !items.isEmpty {
                replace(items)
            } else if r.push == nil, r.pop != true, let v = try? await call(nil, values: nil).view {
                show(v, keeping: values)
            }
            if let p = r.push, depth < Self.maxDepth { pushed = p }
            if r.pop == true { popped = true }
            let url = r.openUrl.flatMap(URL.init(string:)).flatMap { Self.opens($0, item: context.item, owner: plugin) ? $0 : nil }
            return Outcome(toast: r.toast?.isEmpty == false ? r.toast : nil, url: url)
        } catch let e as APIError where Self.isUnsupported(e) {
            unsupported = true
            return Outcome()
        } catch {
            return Outcome(error: error.localizedDescription)
        }
    }

    /// An answer's `items` (UI version 5) in place of the components with the same key: the rest is not drawn again.
    private func replace(_ items: [ExtensionComponent]) {
        guard var v = view else { return }
        let byKey = Dictionary(items.compactMap { c in c.key.map { ($0, c) } }) { _, last in last }
        for i in v.sections.indices {
            v.sections[i].items = v.sections[i].items.map { c in c.key.flatMap { byKey[$0] } ?? c }
        }
        ExtensionMotion.redraw(from: view, to: v) { view = v }
    }

    /// The fields' values as the extension gets them: every field of the view, by name.
    var submitted: [String: String] {
        var out: [String: String] = [:]
        for item in components {
            if case .field(let f) = item, !f.name.isEmpty { out[f.name] = values[f.name] ?? "" }
            if case .composer(let c) = item, !c.name.isEmpty { out[c.name] = values[c.name] ?? "" }
        }
        if let review = view?.review, !review.name.isEmpty { out[review.name] = values[review.name] ?? "" }
        return out
    }

    private var components: [ExtensionComponent] { (view?.sections ?? []).flatMap(\.items) }

    /// When the view asks to be drawn again (`refresh_after`), nil: it does not.
    var refreshDue: Date? {
        guard let s = view?.refreshAfter, stream?.supported != true, s > 0, let at = drawnAt else { return nil }
        return at.addingTimeInterval(TimeInterval(max(s, Self.refreshFloor)))
    }

    /// The hub keeps refresh_after at 30 s or more; so does the app, against a hub that does not.
    static let refreshFloor = 30

    /// Draws it again because its `refresh_after` is up, `ui.invalidate` came or the page on top of it closed: what the
    /// user is typing in its fields stays, a failure keeps the view that is there (the next one tries again).
    func refresh() async {
        if stream?.supported == true || stream?.tree != nil { stream?.refresh(); stale = false; return }
        guard !loading, running == nil, loadingMore == nil else { return }
        let version = invalidationVersion
        loading = true
        defer { loading = false; completedOperations += 1 }
        let result = try? await call(nil, values: nil)
        guard !Task.isCancelled else { return }
        // Invalidation marks are handled only after the request ends, so changing the SwiftUI task key does not cancel our own network request.
        if version == invalidationVersion { stale = false }
        guard let v = result?.view else {
            drawnAt = .now
            return
        }
        show(v, keeping: values)
    }

    private func show(_ v: ExtensionView?, keeping typed: [String: String] = [:], searched: Bool = false) {
        guard let v else { return }
        ExtensionMotion.redraw(from: view, to: v) { view = v }
        fileLines = fileLines.filter { key, lines in v.sections.flatMap(\.items).contains { item in
            guard item.key == key else { return false }
            switch item { case .code(let code): return code.path == lines.path; case .diff(let diff): return diff.path == lines.path; default: return false }
        } }
        drawnAt = .now
        error = nil
        unsupported = false
        var next: [String: String] = [:]
        for item in v.sections.flatMap(\.items) {
            switch item {
            case .field(let f) where !f.name.isEmpty:
                next[f.name] = typed[f.name] ?? (f.kind == "secret" ? "" : (f.value ?? (f.kind == "toggle" ? "false" : "")))
            // What the extension says is picked (its answer to a pick, or its own state).
            case .segmented(let s) where !s.name.isEmpty: next[s.name] = s.value
            // A composer keeps what is being typed in it; what the extension puts there, otherwise.
            case .composer(let c) where !c.name.isEmpty: next[c.name] = typed[c.name] ?? c.value ?? ""
            default: break
            }
        }
        if let review = v.review, !review.name.isEmpty { next[review.name] = typed[review.name] ?? "" }
        if let f = v.filter, !f.name.isEmpty { next[f.name] = f.value }
        if let t = v.tabs, !t.name.isEmpty { next[t.name] = t.value }
        values = next
        // The search field says what the View was drawn for (none: nothing), unless something else is being typed in it
        // or this is the search's own answer.
        if !searched, query == searchedQuery {
            query = v.search?.value ?? ""
            searchedQuery = query
        }
    }

    private func call(_ action: String?, values: [String: String]?) async throws -> ExtensionActionResult {
        if let stream, stream.hasMountedNative || (action != nil && stream.supported != false) { throw ExtensionStreamJSON.Failure.version }
        guard let api = deps.api() else { throw APIError(status: 0, message: String(localized: "Not connected to the hub yet")) }
        let generation = stream?.tree?.version
        let result = try await api.pluginUI(plugin, ui, context: context, state: view?.state ?? initialState, action: action, values: values, manager: selectedManager)
        guard stream?.supported != true, generation == stream?.tree?.version else { throw ExtensionStreamJSON.Failure.version }
        return result
    }

    static func isUnsupported(_ e: APIError) -> Bool { e.status == 404 && e.code == "unsupported" }

    /// What a control's `open_url` may open: an https page; the settings of the extension it is from (`owner`,
    /// `ExtensionSettingsLink`), never another's; on an inbox item's page (`item`), also that item's own source
    /// (`InboxSourceLink`), never another item's.
    static func opens(_ url: URL, item: String?, owner: String? = nil) -> Bool {
        if url.scheme?.lowercased() == "https" { return url.host()?.isEmpty == false && url.user == nil && url.password == nil }
        if let settings = ExtensionSettingsLink.extensionID(url) { return owner != nil && settings == owner }
        guard let item, let source = InboxSourceLink.item(url) else { return false }
        return source == item
    }
}

/// The sections a launcher's page shows from plugins (placement launcher_section).
@Observable final class ExtensionLauncherModel {
    let launcher: String
    private(set) var places: [ExtensionUIPlace] = []
    private(set) var models: [String: ExtensionViewModel] = [:]
    @ObservationIgnored private let deps: ExtensionViewModel.Deps

    init(launcher: String, deps: ExtensionViewModel.Deps = .live) {
        self.launcher = launcher
        self.deps = deps
    }

    func load() async {
        guard let api = deps.api(), let list = try? await api.pluginUI(placement: "launcher_section", launcher: launcher) else { return }
        places = list
        for p in list where models[key(p)] == nil {
            models[key(p)] = ExtensionViewModel(plugin: p.owner, ui: p.id, context: .here(launcher: launcher), deps: deps)
        }
        // Each draws on its own: a slow plugin does not hold the others' sections.
        let pending = list.compactMap { models[key($0)] }
        await withTaskGroup(of: Void.self) { group in
            for m in pending { group.addTask { await m.load() } }
        }
    }

    struct Entry: Identifiable {
        var place: ExtensionUIPlace
        var model: ExtensionViewModel
        var id: String { place.owner + "/" + place.id }
    }

    /// The sections to draw: a plugin that said 404 unsupported is left out.
    var shown: [Entry] {
        places.compactMap { p in models[key(p)].flatMap { $0.unsupported ? nil : Entry(place: p, model: $0) } }
    }

    private func key(_ p: ExtensionUIPlace) -> String { p.owner + "/" + p.id }
}
