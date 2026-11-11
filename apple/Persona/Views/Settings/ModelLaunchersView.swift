import SwiftUI

/// Pushes Settings › Model (value-based, like the other settings pages).
struct ModelLaunchersRoute: Hashable {}
/// One launcher's page.
struct ModelLauncherRoute: Hashable { var name: String }

/// Settings › Model (docs/integrations/launchers.md): every launcher the machines can start — added here (Persona keeps the gateway, model
/// and key), found in a machine's rc file (~/.zshrc, ~/.bashrc) or on its PATH (synced each time this opens), or the built-in
/// claude of each machine — the hidden ones, and how each machine's list came in (offline, timed out, failed).
struct ModelLaunchersView: View {
    @Environment(AppModel.self) private var model
    @State private var launchers: [LauncherView]?
    /// How each machine's list came in; nil from an older hub.
    @State private var sync: [LauncherSync]?
    @State private var error: String?
    /// The provider sheet, at its first step (Add) or at Import from ~/.zshrc; nil: closed.
    @State private var adding: ProviderWizardStep?
    @State private var query = ""
    /// Hidden starts folded (2026-11-01): they are hidden for a reason. A search shows them anyway.
    @State private var showHidden = false
    /// A Persona launcher swiped to Delete, waiting for the confirmation.
    @State private var pendingDelete: LauncherView?
    #if DEBUG
    /// `-openProviderComputers <state>`: a made-up provider's Computers page (`ProviderComputersDemo`).
    @State private var computersDemo: LauncherView?
    #endif

    /// Name, model, gateway host, or a machine's name.
    private func matches(_ l: LauncherView) -> Bool {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return true }
        let machines = (l.machines ?? []).map { model.machine(model.hubRef($0))?.name ?? $0 }
        return ([l.name, l.model ?? "", l.smallModel ?? "", l.baseURL ?? ""] + machines).contains { $0.localizedCaseInsensitiveContains(q) }
    }

    var body: some View {
        SettingsForm {
            if let all = launchers {
                let launchers = all.filter(matches)
                let managed = launchers.filter(\.managed)
                let builtin = launchers.filter(\.builtin)
                let zshrc = launchers.filter { $0.source == "zshrc" && $0.hidden != true }
                // a wrapper script on PATH or a runtime's launcher (agy, omp): a computer's, but not from an rc file
                let onPath = launchers.filter { !$0.managed && !$0.builtin && $0.source != "zshrc" && $0.hidden != true }
                let hidden = launchers.filter { !$0.managed && !$0.builtin && $0.hidden == true }
                let byFile = Dictionary(grouping: zshrc, by: \.rcFileTitle)
                let files = byFile.keys.sorted()
                if !query.isEmpty, launchers.isEmpty {
                    ContentUnavailableView.search(text: query).listRowBackground(Color.clear)
                }
                managedSections(managed)
                ForEach(files, id: \.self) { file in
                    let group = byFile[file] ?? []
                    Section {
                        ForEach(group) { row($0) }
                    } header: { header(String(localized: "From \(file)"), group.count) } footer: {
                        if file == files.last {
                            Text("Functions and aliases in each computer's login shell config (~/.zshrc, ~/.bashrc) that call claude or codex. They sync each time you open this page, and new ones show up automatically.")
                        }
                    }
                }
                if !onPath.isEmpty {
                    Section {
                        ForEach(onPath) { row($0) }
                    } header: { header(String(localized: "From PATH"), onPath.count) } footer: {
                        Text("Wrapper scripts on your computers' PATH that start claude, and the runtimes' own launchers. They sync each time you open this page.")
                    }
                }
                ForEach(builtin) { l in
                    Section {
                        ForEach(l.machines ?? [], id: \.self) { builtinRow(l, machine: $0) }
                    } header: { header(String(localized: "Built-in"), l.machines?.count ?? 0) } footer: {
                        Text("The plain claude command on each computer: “Claude Code default” in Model Configuration.")
                    }
                }
                if !hidden.isEmpty {
                    let open = showHidden || !query.isEmpty
                    Section {
                        if open { ForEach(hidden) { row($0) } }
                    } header: {
                        Button {
                            withAnimation(Theme.Motion.standard) { showHidden.toggle() }
                        } label: {
                            HStack(spacing: Theme.Space.xs) {
                                header(String(localized: "Hidden"), hidden.count)
                                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong)
                                    .rotationEffect(.degrees(open ? 90 : 0))
                                Spacer(minLength: 0)
                            }
                            .foregroundStyle(Color.secondary)
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .disabled(!query.isEmpty)
                        .accessibilityLabel(open ? String(localized: "\(hidden.count) hidden providers, expanded") : String(localized: "\(hidden.count) hidden providers, collapsed"))
                        .accessibilityAddTraits(.isButton)
                    } footer: {
                        if open { Text("They're only hidden in Persona; nothing on the computer is changed. Open one to unhide it.") }
                    }
                }
                if let sync, !sync.isEmpty, query.isEmpty {
                    Section {
                        ForEach(sync) { syncRow($0) }
                    } header: { header(String(localized: "Computers"), sync.count) } footer: {
                        if sync.contains(where: { !$0.ok }) {
                            Text("A computer that didn't connect or answer in time shows its last synced list, marked “Not updated”.")
                        }
                    }
                }
            } else if let error {
                Section { LoadProblem(text: error) }
            } else {
                Section { ProgressView().frame(maxWidth: .infinity).padding(.vertical, Theme.Space.xl) }.listRowBackground(Color.clear)
            }
        }
        .detailTitle(String(localized: "Model Providers"))
        .navigationBarTitleDisplayMode(.inline)
        .pageSearchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: String(localized: "Name, model, gateway, or computer"))
        #if os(iOS) || os(visionOS)
        .pageToolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button { adding = .pick } label: { Label("Add Provider", systemImage: Theme.Icon.add) }
                    Button { adding = .importZshrc } label: { Label("Import from ~/.zshrc", systemImage: Theme.Icon.terminal) }
                } label: { ThemedIcon(Theme.Icon.add) } primaryAction: { adding = .pick }
                .accessibilityLabel("Add Provider")
                .accessibilityIdentifier("launchers.add")
            }
        }
        #endif
        .sheet(item: $adding) { step in
            // Saving moves `launchersRevision`, which reloads this list below.
            if debugOldEditor { LauncherEditor(existing: nil) {} } else { ProviderWizard(start: step) }
        }
        #if os(macOS) && DEBUG
        .onAppear { if UserDefaults.standard.bool(forKey: "macLauncherEditor") { adding = .pick } } // `-macLauncherEditor YES`: Add Launcher
        #endif
        #if DEBUG
        // `-openLauncherEditor YES` opens the form Other Connection Types leads to, `-openLauncherEditor preset` on From Extension;
        // `-openProviderWizard pick|connect|models|import` the provider sheet at that step; `-openProviderComputers
        // all|selected|none|legacy|keys|api` a made-up provider's Computers page (`new` goes with `-openProviderWizard models`);
        // `via` an API connection sending through a computer, at Connect.
        .onAppear {
            if UserDefaults.standard.string(forKey: "openLauncherEditor") != nil { adding = .pick }
            if let s = UserDefaults.standard.string(forKey: "openProviderWizard") { adding = ProviderWizardStep(rawValue: s) ?? .pick }
        }
        .task {
            guard let s = ProviderComputersDemo.state, s != "new", UserDefaults.standard.string(forKey: "openProviderWizard") == nil else { return }
            // The made-up provider runs on the hub's computers: wait (3 s at most) for their list.
            var waited = 0
            while model.hubMachines.isEmpty, waited < 30 {
                try? await Task.sleep(for: .milliseconds(100))
                waited += 1
            }
            computersDemo = ProviderComputersDemo.launcher(s, machines: model.hubMachines)
        }
        .sheet(item: $computersDemo) { l in ProviderWizard(existing: l, start: ProviderComputersDemo.state == "via" ? .connect : .models, showComputers: ProviderComputersDemo.state != "via") }
        #endif
        .confirmationDialog("Delete Launcher “\(pendingDelete?.name ?? "")”?", isPresented: Binding(get: { pendingDelete != nil }, set: { if !$0 { pendingDelete = nil } }),
                            titleVisibility: .visible, presenting: pendingDelete) { l in
            Button("Delete", role: .destructive) { Task { await delete(l) } }
        } message: { _ in Text("The key stored on the hub is deleted too. This can't be undone.") }
        // Again whenever a launcher changes anywhere, its own page included (hide, restore, delete): this page stays
        // built under the one pushed on it, so without this it showed the old list when you came back (#1433).
        .task(id: model.launchersRevision) { await load() }
        .refreshable { await load() }
        #if DEBUG
        .onAppear { if let q = UserDefaults.standard.string(forKey: "showModelSearch") { query = q } } // `-showModelSearch glm`
        #endif
    }

    /// Managed by Persona: one section, or (#2333) one per harness when there are several kinds, the last with the add
    /// buttons and the footer.
    @ViewBuilder private func managedSections(_ managed: [LauncherView]) -> some View {
        let harnesses = ProviderScopeForm.harnessGroups(managed)
        if harnesses.count > 1 {
            ForEach(harnesses) { g in
                Section {
                    ForEach(g.items) { row($0) }
                    #if os(macOS)
                    if g.id == harnesses.last?.id { macAddButtons }
                    #endif
                } header: { header(String(localized: "Managed by Persona · \(g.title)"), g.items.count) } footer: {
                    if g.id == harnesses.last?.id { managedFooter }
                }
            }
        } else if query.isEmpty || !managed.isEmpty { Section {
            if managed.isEmpty {
                #if os(macOS)
                Text("No providers yet. Use “Add Provider…” below to set up the connection type, gateway, key, and default model, then pick one for each use in “Model Configuration”.")
                    .font(Theme.Font.label).foregroundStyle(.secondary)
                #else
                Text("No providers yet. Tap + at the top right to set up the connection type, gateway, key, and default model, then pick one for each use in “Model Configuration”.")
                    .font(Theme.Font.label).foregroundStyle(.secondary)
                #endif
            }
            ForEach(managed) { row($0) }
            #if os(macOS)
            macAddButtons
            #endif
        } header: { header(String(localized: "Managed by Persona"), managed.count) } footer: {
            if !managed.isEmpty { managedFooter }
        } }
    }

    #if os(macOS)
    /// In the group it adds to, as a Mac settings pane has its add button (#1223): in the window's toolbar the search field
    /// took the trailing end and the + went to the window's middle, over the settings list.
    private var macAddButtons: some View {
        HStack {
            Spacer(minLength: 0)
            Button("Import from ~/.zshrc…") { adding = .importZshrc }.lineLimit(1).formButton()
            Button("Add Provider…") { adding = .pick }.lineLimit(1).formButton()
        }
    }
    #endif

    private var managedFooter: some View {
        Text("Keys are stored encrypted on the hub and only handed to the computer at launch. They're never written to a file.")
    }

    /// `-openLauncherEditor` (SecretFieldTests): straight to the older form.
    private var debugOldEditor: Bool {
        #if DEBUG
        UserDefaults.standard.string(forKey: "openLauncherEditor") != nil
        #else
        false
        #endif
    }

    private func header(_ title: String, _ count: Int) -> some View {
        HStack(spacing: Theme.Space.xs) {
            Text(title)
            Text("\(count)").monospacedDigit().foregroundStyle(Color.secondary)
        }
    }

    private func name(_ machine: String) -> String {
        model.machine(model.hubRef(machine))?.name ?? sync?.first { $0.machine == machine }?.name ?? machine
    }

    /// Built-in claude · <machine>: the version there; marked when that machine did not answer now.
    private func builtinRow(_ l: LauncherView, machine: String) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.sm) {
                Text("Built-in claude · \(name(machine))").font(Theme.Font.heading).lineLimit(1)
                if l.stale?.contains(machine) == true { Chip(text: String(localized: "Not updated"), look: .tint(Theme.caution)) }
            }
            HStack(spacing: Theme.Space.sm) {
                Chip(text: String(localized: "Default Model"))
                if let v = l.versions?[machine] {
                    Text(verbatim: "Claude Code \(v)").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }

    /// One machine: whether its list came in now, and from which file.
    private func syncRow(_ s: LauncherSync) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            ThemedIcon(s.ok ? Theme.Icon.successFilled : s.state == "offline" ? Theme.Icon.offline : Theme.Icon.errorFilled)
                .foregroundStyle(s.ok ? Theme.positive : s.state == "error" ? Theme.danger : Theme.caution)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(s.name ?? name(s.machine)).font(Theme.Font.body).lineLimit(1)
                Text(s.summary()).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(3)
            }
        }
        .accessibilityElement(children: .combine)
    }

    private func row(_ l: LauncherView) -> some View {
        NavigationLink(value: ModelLauncherRoute(name: l.name)) {
            LauncherRow(launcher: l, machines: machineNames(l), stale: (l.stale ?? []).map(name))
        }
        // Swipe left to hide a ~/.zshrc launcher or bring a hidden one back (2026-11-01 user request). No
        // confirmation: it only changes what Persona lists and is undone the same way. A Persona one is deleted, key
        // and all, so that one asks first.
        #if os(macOS)
        // A Mac form has no swipe: the same actions in the row's menu (#1221).
        .contextMenu {
            if l.managed {
                Button("Delete…", role: .destructive) { pendingDelete = l }
            } else if l.hidden == true {
                Button("Unhide [Lark Chat View e0534b]") { Task { await setHidden(l, false) } }
            } else {
                Button("Hide") { Task { await setHidden(l, true) } }
            }
        }
        #endif
        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
            if l.managed {
                Button { pendingDelete = l } label: { Label("Delete", systemImage: Theme.Icon.delete) }.tint(Theme.danger)
            } else {
                if l.hidden == true {
                    Button { Task { await setHidden(l, false) } } label: { Label("Unhide [Lark Chat View e0534b]", systemImage: Theme.Icon.view) }
                        .tint(Theme.accent)
                } else {
                    Button { Task { await setHidden(l, true) } } label: { Label("Hide", systemImage: Theme.Icon.hide) }
                        .tint(Theme.caution)
                }
            }
        }
    }

    /// Takes the row out at once, then asks the hub; a failure puts it back and says why.
    private func delete(_ l: LauncherView) async {
        guard let api = model.api, let i = launchers?.firstIndex(where: { $0.name == l.name }) else { return }
        _ = withAnimation(Theme.Motion.standard) { launchers?.remove(at: i) }
        do {
            try await api.deleteLauncher(l.name)
            model.launchersChanged()
            Haptics.success()
        } catch {
            withAnimation(Theme.Motion.standard) { launchers?.insert(l, at: min(i, launchers?.count ?? 0)) }
            model.show(error)
        }
    }

    /// Moves the row at once, then asks the hub; a failure puts it back and says why.
    private func setHidden(_ l: LauncherView, _ hide: Bool) async {
        guard let api = model.api, let i = launchers?.firstIndex(where: { $0.name == l.name }) else { return }
        withAnimation(Theme.Motion.standard) { launchers?[i].hidden = hide }
        do {
            if hide { try await api.deleteLauncher(l.name) } else { try await api.restoreLauncher(l.name) }
            model.launchersChanged()
            Haptics.success()
        } catch {
            withAnimation(Theme.Motion.standard) { launchers?[i].hidden = l.hidden }
            model.show(error)
        }
    }

    private func machineNames(_ l: LauncherView) -> String? {
        guard let ids = l.machines, !ids.isEmpty else { return nil }
        return ids.map { model.machine(model.hubRef($0))?.name ?? $0 }.joined(separator: String(localized: "、", comment: "Separator between computer names in a list"))
    }

    private func load() async {
        guard let api = model.api else { return }
        do {
            (launchers, sync) = try await api.launcherList()
            error = nil
        } catch {
            self.error = error.shown
        }
    }
}

/// A launcher in the list: its name, the model, where it goes, which machines have it.
struct LauncherRow: View {
    var launcher: LauncherView
    var machines: String?
    /// The machines it is listed for from their last list (offline or not answering now).
    var stale: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.sm) {
                // A display name (「智谱 GLM」, docs/integrations/launchers.md「Built-in presets」) is a name, not an id; without one, the id as before.
                if launcher.title != launcher.name {
                    Text(launcher.title).font(Theme.Font.heading).lineLimit(1)
                } else {
                    Text(launcher.name).font(Theme.Font.heading.monospaced()).lineLimit(1)
                }
                if launcher.managed { Chip(text: "Persona", look: .tint(Theme.accent)) }
                if !stale.isEmpty { Chip(text: String(localized: "Not updated"), look: .tint(Theme.caution)) }
            }
            HStack(spacing: Theme.Space.sm) {
                if let m = Fmt.model(launcher.model) ?? launcher.model, !m.isEmpty { Chip(text: m) }
                if let host = launcher.baseURL.flatMap({ URL(string: $0)?.host() }) {
                    Text(host).font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1)
                }
            }
            if let machines {
                Label(machines, systemImage: Theme.Icon.machines).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            if !stale.isEmpty {
                Text("\(stale.joined(separator: String(localized: "、", comment: "Separator between computer names in a list"))) didn't connect; showing the last synced list.")
                    .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }
}

/// One launcher: its settings; a Persona one can be changed or deleted, a ~/.zshrc one shows its function (keys
/// masked) on each machine and can be hidden or brought back.
struct ModelLauncherDetailView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var name: String
    @State private var launcher: LauncherView?
    @State private var error: String?
    @State private var editing = false
    @State private var confirmDelete = false
    @State private var showDefinition = false
    /// Test Availability: per machine, running (nil) or done.
    @State private var checks: [String: LauncherCheck?] = [:]
    /// What plugins draw on this page (docs/extensions/extensions.md「Declarative UI」), and the link one of them opens.
    @State private var pluginSections: ExtensionLauncherModel?
    @State private var pluginLink: LinkTarget?

    /// Machines whose launcher sets the same, with that detail.
    private struct DetailGroup { var machines: [String]; var names: String; var detail: LauncherDetail }

    private func detailGroups(_ l: LauncherView) -> [DetailGroup] {
        var out: [DetailGroup] = []
        for (id, d) in (l.details ?? [:]).sorted(by: { $0.key < $1.key }) {
            let name = model.machine(model.hubRef(id))?.name ?? id
            if let i = out.firstIndex(where: { $0.detail == d }) {
                out[i].machines.append(id)
                out[i].names += String(localized: "、", comment: "Separator between computer names in a list") + name
            } else {
                out.append(DetailGroup(machines: [id], names: name, detail: d))
            }
        }
        return out
    }

    private func groups(_ l: LauncherView) -> Int { detailGroups(l).count }

    var body: some View {
        SettingsForm {
            if let l = launcher {
                Section {
                    LabeledContent("Source", value: l.managed ? String(localized: "Managed by Persona") : l.builtin ? String(localized: "Built-in claude command")
                                   : l.pathScript ? String(localized: "Script on PATH") : String(localized: "Function in \(l.rcFileTitle)"))
                    if l.kind == "api" { LabeledContent("Connection Type", value: String(localized: "Direct API · \(l.api == "responses" ? "Responses" : "Messages")")) }
                    // A ~/.zshrc launcher's gateway and models are in the sections it was probed into below.
                    if l.managed || (l.details ?? [:]).isEmpty {
                        if let u = l.baseURL, !u.isEmpty { mono("Base URL", u) }
                        if let m = l.model, !m.isEmpty { mono("Model [Assistant View c98e11]", m) }
                    }
                    if let s = l.smallModel, !s.isEmpty { mono("Small Model", s) }
                    if let s = l.subagentModel, !s.isEmpty { mono("Subagent Model", s) }
                    if let c = l.customModel, !c.isEmpty {
                        mono("Custom Model", c)
                        if let n = l.customModelName, !n.isEmpty { LabeledContent("Custom Model Name", value: n) }
                        if let d = l.customModelDescription, !d.isEmpty { LabeledContent("Custom Model Description", value: d) }
                    }
                    if let o = l.origin, o.hasPrefix("zshrc:") { mono("Imported From", "~/.zshrc · " + o.dropFirst("zshrc:".count)) }
                    if let ms = l.models, ms.count > 1 { mono("Available Models", ms.map(\.id).joined(separator: String(localized: "、", comment: "Separator between computer names in a list"))) }
                    if l.hasHeaders == true { LabeledContent("Headers", value: String(localized: "Set (hidden)")) }
                    if l.managed { LabeledContent("API Key", value: l.hasKey == true ? String(localized: "Set (hidden)") : String(localized: "Not set")) }
                    if l.managed, l.api != "responses" { LabeledContent("Authentication [Model Launchers View d1ff8f]", value: l.bearerAuth ? "Bearer" : "x-api-key") }
                    if let a = l.args, !a.isEmpty { mono("Extra Arguments", a.joined(separator: " ")) }
                    if let ids = l.machines, !ids.isEmpty {
                        LabeledContent("Computers", value: ids.map { model.machine(model.hubRef($0))?.name ?? $0 }.joined(separator: String(localized: "、", comment: "Separator between computer names in a list")))
                    }
                    // A Persona provider's device scope (#2333): where it runs, how its key travels, and which computers
                    // have a key of their own (never the keys).
                    if l.managed, l.share != nil {
                        LabeledContent("Computers", value: ProviderScopeForm.summary(l, name: { model.machine(model.hubRef($0))?.name ?? $0 }))
                            .accessibilityIdentifier("launcher.scope")
                        if let keyed = l.keyMachines, !keyed.isEmpty {
                            LabeledContent("Per-Computer Keys", value: String(localized: "\(keyed.count) computers"))
                                .accessibilityIdentifier("launcher.machineKeys")
                        }
                    }
                }
                if l.managed {
                    Section {
                        Text(l.kind == "api" ? String(localized: "The hub calls this API directly; no agent is launched on a computer. Pick it for each use in “Model Configuration”.") : String(localized: "Runs as `personad launch \(l.name)` on any computer: it gets these settings from the local node, then starts claude."))
                            .font(Theme.Font.label).foregroundStyle(.secondary)
                    }
                }
                // How much is left comes before trying it: it is why the page is opened more often.
                if l.kind != "api" {
                    LauncherQuotaSection(name: l.name)
                    availability(l)
                }
                if let pluginSections { ExtensionLauncherSections(page: pluginSections, link: $pluginLink) }
                // What the launcher really sets, once for the machines where it is the same.
                ForEach(detailGroups(l), id: \.machines) { g in
                    LauncherDetailSections(detail: g.detail, place: groups(l) > 1 ? g.names : nil)
                }
                if let defs = l.definitions, !defs.isEmpty {
                    Section {
                        DisclosureGroup(isExpanded: $showDefinition) {
                            ForEach(defs.sorted(by: { $0.key < $1.key }), id: \.key) { id, def in
                                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                                    if defs.count > 1 { Text(model.machine(model.hubRef(id))?.name ?? id).font(Theme.Font.labelStrong).foregroundStyle(.secondary) }
                                    Text(def).font(Theme.Font.code).textSelection(.enabled)
                                }
                            }
                        } label: { Label(l.pathScript ? String(localized: "Script Source") : String(localized: "Function Source"), systemImage: Theme.Icon.agents).lineLimit(1) }
                    } footer: { Text("Values that look like keys or tokens are replaced with •••.") }
                }
                Section {
                    if l.hidden == true {
                        Button { Task { await restore() } } label: { Label("Unhide [Model Launchers View 38f0f1]", systemImage: Theme.Icon.view).lineLimit(1) }
                    } else {
                        // The confirmation comes up right where the button is (2026-10-31 user request), not as a sheet.
                        Button(role: .destructive) { confirmDelete = true } label: {
                            Label(l.managed ? String(localized: "Delete Launcher") : String(localized: "Hide in Persona"), systemImage: l.managed ? Theme.Icon.delete : Theme.Icon.hide).lineLimit(1)
                        }
                        .popover(isPresented: $confirmDelete, arrowEdge: .top) {
                            Button(role: .destructive) {
                                confirmDelete = false
                                Task { await delete() }
                            } label: {
                                Text(l.managed ? String(localized: "Confirm Delete") : String(localized: "Confirm Hide")).font(Theme.Font.heading).lineLimit(1)
                                    .padding(.horizontal, Theme.Space.l).padding(.vertical, Theme.Space.m)
                            }
                            .foregroundStyle(Theme.danger)
                            .presentationCompactAdaptation(.popover)
                        }
                    }
                }
            } else if let error {
                Section { LoadProblem(text: error) }
            } else {
                Section { ProgressView().frame(maxWidth: .infinity).padding(.vertical, Theme.Space.xl) }.listRowBackground(Color.clear)
            }
        }
        .detailTitle(name)
        .navigationBarTitleDisplayMode(.inline)
        .pageToolbar {
            if launcher?.managed == true {
                ToolbarItem(placement: .topBarTrailing) { Button("Edit [Message Menu 051836]") { editing = true }.lineLimit(1) }
            }
        }
        .sheet(isPresented: $editing) {
            // A Responses API keeps the older form; a Claude Code provider (or a Messages one) the wizard, at Connect.
            if launcher?.kind == "api" && launcher?.api == "responses" {
                LauncherEditor(existing: launcher) { await load() }
            } else {
                ProviderWizard(existing: launcher) { await load() }
            }
        }
        .pluginLinks($pluginLink)
        .extensionPushes(pluginSections.map { Array($0.models.values) } ?? [])
        .task(id: model.hubFeatures.hasExtensionFeature("manager")) {
            guard model.hubFeatures.hasExtensionFeature("manager") else { return }
            let sections = pluginSections ?? ExtensionLauncherModel(launcher: name)
            pluginSections = sections
            await sections.load()
        }
        .task {
            await load()
            #if DEBUG
            if UserDefaults.standard.bool(forKey: "showDeleteConfirm") { try? await Task.sleep(for: .seconds(1)); confirmDelete = true }
            if UserDefaults.standard.bool(forKey: "showLauncherCheck"), let l = launcher { await check(testMachines(l)) }
            #endif
        }
    }

    private func mono(_ label: LocalizedStringResource, _ value: String) -> some View {
        LabeledContent {
            Text(value).font(Theme.Font.body.monospaced()).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
        } label: {
            Text(label)
        }
    }

    /// The machines to test on: a ~/.zshrc launcher's own (online ones), a Persona one on the hub's machine.
    private func testMachines(_ l: LauncherView) -> [String] {
        if l.managed { return [""] } // the hub picks (the assistant's machine)
        return (l.machines ?? []).filter { model.machine(model.hubRef($0))?.online != false }
    }

    @ViewBuilder private func availability(_ l: LauncherView) -> some View {
        let machines = testMachines(l)
        let running = checks.values.contains { $0 == nil }
        Section {
            ForEach(machines, id: \.self) { id in
                if let entry = checks[id] {
                    LauncherCheckRow(machine: id.isEmpty ? nil : (model.machine(model.hubRef(id))?.name ?? id), check: entry)
                }
            }
            Button { Task { await check(machines) } } label: {
                HStack {
                    Label(checks.isEmpty ? String(localized: "Test Availability") : String(localized: "Test Again"), systemImage: Theme.Icon.tryOut).lineLimit(1)
                    if running { Spacer(); ProgressView() }
                }
            }
            .disabled(running || machines.isEmpty)
        } header: { Text("Availability") } footer: {
            Text(machines.isEmpty ? String(localized: "None of the computers with this launcher are online.") : String(localized: "Sends a real one-word request (a few tokens) to see whether it answers, how long it takes, and which model replies."))
        }
    }

    private func check(_ machines: [String]) async {
        guard let api = model.api else { return }
        for id in machines { checks[id] = .some(nil) }
        await withTaskGroup(of: (String, LauncherCheck).self) { group in
            for id in machines {
                group.addTask {
                    do { return (id, try await api.checkLauncher(name, machine: id.isEmpty ? nil : id)) }
                    catch { return (id, LauncherCheck.failed(error)) }
                }
            }
            for await (id, c) in group { checks[id] = .some(c) }
        }
        let allOK = checks.values.allSatisfy { $0?.ok == true }
        if allOK { Haptics.success() } else { Haptics.error() }
    }

    private func load() async {
        guard let api = model.api else { return }
        do {
            launcher = try await api.launcher(name)
            error = nil
        } catch {
            self.error = error.shown
        }
    }

    private func delete() async {
        guard let api = model.api else { return }
        do {
            try await api.deleteLauncher(name)
            model.launchersChanged()
            Haptics.success()
            dismiss()
        } catch { model.show(error) }
    }

    private func restore() async {
        guard let api = model.api else { return }
        do {
            try await api.restoreLauncher(name)
            model.launchersChanged()
            Haptics.success()
            await load()
        } catch { model.show(error) }
    }
}

/// Add a launcher, or change one Persona keeps.
struct LauncherEditor: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var existing: LauncherView?
    var saved: () async -> Void
    @State private var name = ""
    @State private var baseURL = ""
    @State private var apiKey = ""
    @State private var modelName = ""
    @State private var smallModel = ""
    @State private var subagentModel = ""
    @State private var customModel = ""
    @State private var customModelName = ""
    @State private var customModelDescription = ""
    @State private var args = ""
    @State private var kind = "launcher"
    @State private var protocolName = "messages"
    @State private var auth = "api_key"
    @State private var saving = false
    @State private var error: String?
    /// The fields as the sheet opened: Cancel asks first once they differ (Mac).
    @State private var opened: [String]?
    /// From Extension: the providers the hub's plugins offer (only when adding, and only when there are some).
    @State private var presets = LauncherPresetModel()
    @State private var fromExtension = false
    /// Where it runs and when its key travels (#2333): only on a hub that takes scopes (a stored provider it returned one for).
    @State private var scope = ProviderScopeForm()

    private var isNew: Bool { existing == nil }
    private var usesPreset: Bool { isNew && fromExtension && !presets.presets.isEmpty }
    private var scopeShown: Bool { existing.map { $0.share != nil } ?? model.hubFeatures.contains(ProviderScopeForm.feature) }
    /// The Responses API always takes the key as Bearer: nothing to choose.
    private var usesResponses: Bool { kind == "api" && protocolName == "responses" }
    /// What was typed in either form (a preset's model is filled in as it loads, so it does not count).
    private var fields: [String] {
        [name, baseURL, apiKey, modelName, smallModel, subagentModel, customModel, customModelName, customModelDescription, args, kind,
         protocolName, auth, presets.name, presets.apiKey]
    }

    var body: some View {
        NavigationStack {
            Form {
                if isNew, !presets.presets.isEmpty {
                    Section {
                        Picker("Source", selection: $fromExtension) {
                            Text("Manual [Model Launchers View e98ba2]").tag(false)
                            Text("From Extension").tag(true)
                        }
                        .pickerStyle(.segmented)
                        .labelsHidden()
                        .accessibilityIdentifier("launcher.source")
                    }
                }
                if usesPreset {
                    LauncherPresetSections(model: presets)
                } else {
                    manualSections
                }
            }
            .navigationTitle(isNew ? String(localized: "Add Provider") : String(localized: "Edit \(existing?.name ?? "")"))
            .navigationBarTitleDisplayMode(.inline)
            .sheetCancel(unsaved: opened.map { $0 != fields } ?? false) { dismiss() }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { Task { usesPreset ? await savePreset() : await save() } }.lineLimit(1)
                        .disabled(usesPreset ? !presets.canSave
                                  : saving || name.isEmpty || baseURL.isEmpty || modelName.isEmpty || (isNew && apiKey.isEmpty)
                                    || (scopeShown && scope.problem != nil))
                }
            }
            .task {
                guard isNew else { return }
                await presets.load()
                #if DEBUG
                if UserDefaults.standard.string(forKey: "openLauncherEditor") == "preset" { fromExtension = true }
                #endif
            }
            .onAppear {
                defer { if opened == nil { opened = fields } }
                guard let e = existing else { return }
                name = e.name
                baseURL = e.baseURL ?? ""
                modelName = e.model ?? ""
                smallModel = e.smallModel ?? ""
                subagentModel = e.subagentModel ?? ""
                customModel = e.customModel ?? ""
                customModelName = e.customModelName ?? ""
                customModelDescription = e.customModelDescription ?? ""
                args = (e.args ?? []).joined(separator: " ")
                kind = e.kind ?? "launcher"
                protocolName = e.api ?? "messages"
                auth = e.bearerAuth ? "bearer" : "api_key"
                if let s = ProviderScopeForm(view: e) { scope = s }
            }
        }
        #if os(macOS)
        .macSheet()
        #endif
    }

    /// Manual: the name, the kind, the gateway and key, the models, the flags.
    @ViewBuilder private var manualSections: some View {
        Section {
            TextField(String(localized: "Name, e.g. jv_strong"), text: $name)
                .font(Theme.Font.body.monospaced())
                .textInputAutocapitalization(.never).autocorrectionDisabled()
                .disabled(!isNew)
        } header: { Text("Name [Add Computer View 677331]") } footer: { Text("Letters, digits, _, and -. This is the name you pick the provider by.") }
        Section {
            Picker("Connection Type", selection: $kind) {
                Text("Claude Code Launcher").tag("launcher")
                Text("Direct API").tag("api")
            }
            if kind == "api" {
                Picker("API Protocol", selection: $protocolName) {
                    Text("Messages").tag("messages")
                    Text("Responses").tag("responses")
                }
            }
        } footer: {
            Text(kind == "api" ? String(localized: "The hub calls the model directly. Available for uses like triage, translation, and knowledge processing.") : String(localized: "Launches Claude Code on your computer. Available for uses like chats and agents."))
        }
        Section {
            TextField("https://…", text: $baseURL)
                .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
            SecretField(isNew || existing?.hasKey != true ? "API Key" : String(localized: "API Key (leave empty to keep)"), filled: String(localized: "Filled in; tap Save [Extension Market Views 94348c]"),
                        text: $apiKey, identifier: "launcher.editor.key")
            if !usesResponses {
                Picker("Authentication [Model Launchers View d1ff8f]", selection: $auth) {
                    Text("x-api-key").tag("api_key")
                    Text("Bearer").tag("bearer")
                }
            }
        } header: { Text("Gateway") } footer: {
            Text(usesResponses ? String(localized: "Enter an address compatible with the selected protocol. The key is stored encrypted on the hub and never sent back to clients.")
                : String(localized: "Enter an address compatible with the selected protocol. The key is stored encrypted on the hub and never sent back to clients. Choose Bearer if the gateway only accepts Authorization: Bearer (ANTHROPIC_AUTH_TOKEN)."))
        }
        Section {
            TextField(String(localized: "Default Model"), text: $modelName).textInputAutocapitalization(.never).autocorrectionDisabled()
            if kind == "launcher" {
                TextField(String(localized: "Small model (optional, defaults to the above)"), text: $smallModel).textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField(String(localized: "Subagent model (optional, defaults to the above)"), text: $subagentModel)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
            }
        } header: { Text("Default Model") } footer: { Text("Choices for each use are managed in “Model Configuration”. Uses without their own choice get the default here. The subagent model is used for multi-agent runs such as workflows.") }
        if kind == "launcher" {
            Section {
                TextField(String(localized: "Custom model (optional)"), text: $customModel)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                TextField(String(localized: "Display name (optional)"), text: $customModelName)
                TextField(String(localized: "Description (optional)"), text: $customModelDescription)
            } header: { Text("Custom Model Entry (Optional)") } footer: { Text("Adds an extra option to /model. The gateway decides which model IDs are valid.") }
        }
        if kind == "launcher" {
            Section {
                TextField(String(localized: "e.g. --dangerously-skip-permissions"), text: $args)
                    .font(Theme.Font.body.monospaced()).textInputAutocapitalization(.never).autocorrectionDisabled()
            } header: { Text("Extra Arguments (Optional)") } footer: { Text("Added after claude, separated by spaces. Each must start with -.") }
        }
        if scopeShown {
            Section {
                ProviderComputersRow(scope: $scope, old: existing, kind: kind, agent: existing?.agent)
            } footer: { Text("Which computers can run it and get its key.") }
        }
        if let error {
            Section { LoadProblem(text: error) }
        }
    }

    private func savePreset() async {
        if await presets.save() {
            model.launchersChanged()
            Haptics.success()
            await saved()
            dismiss()
        } else {
            Haptics.error()
        }
    }

    private func save() async {
        guard let api = model.api else { return }
        saving = true
        defer { saving = false }
        var input = LauncherInput(name: name.trimmingCharacters(in: .whitespaces), baseURL: baseURL.trimmingCharacters(in: .whitespaces),
                                  model: modelName.trimmingCharacters(in: .whitespaces),
                                  smallModel: kind == "api" ? "" : smallModel.trimmingCharacters(in: .whitespaces),
                                  subagentModel: kind == "api" ? "" : subagentModel.trimmingCharacters(in: .whitespaces),
                                  customModel: kind == "api" ? "" : customModel.trimmingCharacters(in: .whitespaces),
                                  customModelName: kind == "api" ? "" : customModelName.trimmingCharacters(in: .whitespaces),
                                  customModelDescription: kind == "api" ? "" : customModelDescription.trimmingCharacters(in: .whitespaces),
                                  args: kind == "api" ? [] : args.split(whereSeparator: \.isWhitespace).map(String.init),
                                  apiKey: apiKey.isEmpty ? nil : apiKey, kind: kind,
                                  api: kind == "api" ? protocolName : "messages", auth: usesResponses ? "api_key" : auth)
        if scopeShown { scope.apply(to: &input, old: existing, machines: model.hubMachines, kind: kind) }
        do {
            try await api.saveLauncher(input, isNew: isNew)
            model.launchersChanged()
            Haptics.success()
            await saved()
            dismiss()
        } catch {
            self.error = error.shown
            Haptics.error()
        }
    }
}

/// A launcher's settings as it really runs them: agent and gateway, each model tier, context and effort, flags,
/// and every other setting it makes (secrets as •••).
struct LauncherDetailSections: View {
    var detail: LauncherDetail
    /// The machines it is about, when they differ.
    var place: String?

    var body: some View {
        if let e = detail.error {
            Section { Label(e, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(.secondary) } header: { title("Runtime") }
        } else {
            Section {
                LabeledContent("Agent", value: detail.agentName)
                if let u = detail.baseURL, !u.isEmpty { mono("Gateway", u) }
                if let a = detail.auth { mono("Key", String(localized: "Set · \(a)")) }
            } header: { title("Runtime") }
            if let models = detail.models, !models.isEmpty {
                Section {
                    ForEach(models, id: \.self) { m in
                        LabeledContent {
                            Text(m.model).font(Theme.Font.body.monospaced()).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
                        } label: {
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text(m.role)
                                Text(m.env).font(Theme.Font.code).foregroundStyle(.tertiary)
                            }
                        }
                    }
                } header: { title("Model [Assistant View c98e11]") }
            }
            if detail.contextWindow != nil || detail.compactWindow != nil || detail.effort != nil || detail.timeoutMS != nil {
                Section {
                    if let w = detail.contextWindow, w > 0 { LabeledContent("Context Window", value: String(localized: "\(LauncherDetail.tokens(w)) tokens")) }
                    if let c = detail.compactWindow, c > 0 { LabeledContent("Auto-Compact Window", value: String(localized: "\(LauncherDetail.tokens(c)) tokens")) }
                    if let e = detail.effort, !e.isEmpty { LabeledContent("Thinking Effort", value: e) }
                    if let t = detail.timeoutMS, t > 0 { LabeledContent("Request Timeout", value: t >= 60_000 ? String(localized: "\(t / 60_000) min [Task Views 135d12]") : String(localized: "\(t / 1000) s")) }
                } header: { title("Context") }
            }
            if let args = detail.args, !args.isEmpty {
                Section {
                    Text(args.joined(separator: " ")).font(Theme.Font.code).textSelection(.enabled)
                } header: { title("Launch Arguments") }
            }
            if let env = detail.env, !env.isEmpty {
                Section {
                    ForEach(env, id: \.key) { e in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(e.key).font(Theme.Font.code).foregroundStyle(.secondary)
                            Text(e.value).font(Theme.Font.body.monospaced()).lineLimit(3).textSelection(.enabled)
                        }
                    }
                } header: { title("Other Settings (\(env.count))") }
            }
        }
    }

    private func title(_ resource: LocalizedStringResource) -> Text {
        let s = String(localized: resource)
        return Text(place.map { "\(s) · \($0)" } ?? s)
    }

    private func mono(_ label: LocalizedStringResource, _ value: String) -> some View {
        LabeledContent {
            Text(value).font(Theme.Font.body.monospaced()).lineLimit(2).truncationMode(.middle).textSelection(.enabled)
        } label: {
            Text(label)
        }
    }
}

/// One machine's test: ✓ with how long and which model, ✗ with why; a spinner while it runs.
struct LauncherCheckRow: View {
    var machine: String?
    var check: LauncherCheck?

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            Group {
                if let c = check {
                    ThemedIcon(c.ok ? Theme.Icon.successFilled : Theme.Icon.errorFilled).foregroundStyle(c.ok ? Theme.positive : Theme.danger)
                } else {
                    ProgressView().controlSize(.small)
                }
            }
            .frame(width: Theme.Size.iconS)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(spacing: Theme.Space.sm) {
                    Text(machine ?? String(localized: "Hub Machine")).font(Theme.Font.heading).lineLimit(1)
                    if let c = check { Text(Self.seconds(c.millis)).font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary) }
                }
                if let c = check {
                    if c.ok {
                        Text([c.model, c.answer.map { String(localized: "Answered “\($0)”") }].compactMap { $0 }.joined(separator: " · "))
                            .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                    } else if let e = c.error {
                        Text(e).font(Theme.Font.code).foregroundStyle(Theme.danger).lineLimit(6).textSelection(.enabled)
                    }
                } else {
                    Text("Requesting…").font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }

    static func seconds(_ ms: Int) -> String { ms >= 10_000 ? String(localized: "\(ms / 1000) s") : String(localized: "\((Double(ms) / 1000).formatted(.number.precision(.fractionLength(1)))) s") }
}
