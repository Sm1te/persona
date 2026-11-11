import SwiftUI

/// Settings › Model Providers › a provider › Computers (#2333, docs/integrations/launchers.md "Device scope and on-demand
/// delivery"), laid out like `ProviderProxyPage`: where it runs (All Computers, Selected Computers, Hub Only), whether each
/// computer keeps a copy of the key, whether it also runs where Claude Code or Codex is signed in already, and keys for single
/// computers. The form is `ProviderScopeForm`; the editor that opened it holds it and sends it.
struct ProviderComputersPage: View {
    @Environment(AppModel.self) private var model
    @Binding var scope: ProviderScopeForm
    /// The stored provider (its `key_machines`, `legacy_scope`); nil for a new one.
    var old: LauncherView?
    /// launcher | api
    var kind: String
    /// The harness of a launcher: claude ("" or nil), codex, agy, omp.
    var agent: String?
    /// A new provider saved with `assign`: the hub's own-service default is then use.
    var assign = false
    /// Per-Computer Keys, open.
    @State private var keysOpen = false

    init(scope: Binding<ProviderScopeForm>, old: LauncherView?, kind: String, agent: String? = nil, assign: Bool = false) {
        _scope = scope
        self.old = old
        self.kind = kind
        self.agent = agent
        self.assign = assign
        #if DEBUG
        // `-openProviderComputers keys`: Per-Computer Keys open, for screenshots.
        _keysOpen = State(initialValue: UserDefaults.standard.string(forKey: "openProviderComputers") == "keys")
        #endif
    }

    private var machines: [Machine] { model.hubMachines }
    private func machine(_ id: String) -> Machine? { machines.first { $0.id == id } }
    private func name(_ id: String) -> String { machine(id)?.name ?? id }
    private var stored: [String] { old?.keyMachines ?? [] }
    private var ownDetected: Bool { ProviderScopeForm.ownServiceDetected(kind: kind, agent: agent) }

    var body: some View {
        Form {
            if old?.legacyScope == true {
                Section {
                    Label {
                        Text("Saved before computer scopes existed: it still runs on every computer, work ones included, and each keeps a copy of the key. Changing anything here and saving makes the scope what you set.")
                            .font(Theme.Font.label).foregroundStyle(.secondary)
                    } icon: {
                        ThemedIcon(Theme.Icon.info).foregroundStyle(Theme.accent)
                    }
                    .accessibilityIdentifier("computers.legacy")
                }
            }
            shareSection
            if scope.share == .selected { selectedSection }
            if scope.share == .all { workSection }
            if scope.share != .none { deliverySections }
            keysSection
        }
        .navigationTitle(String(localized: "Computers"))
        .navigationBarTitleDisplayMode(.inline)
    }

    // MARK: Where it runs

    private var shareSection: some View {
        Section {
            ForEach(ProviderScopeForm.Share.allCases) { share in
                Button { scope.share = share } label: {
                    HStack {
                        Text(share.title).foregroundStyle(Color.primary).lineLimit(1)
                        Spacer()
                        if scope.share == share {
                            ThemedIcon(Theme.Icon.done).font(Theme.Font.label).foregroundStyle(Theme.accent)
                        }
                    }
                    .contentShape(.rect)
                }
                .accessibilityIdentifier("computers.share.\(share.rawValue)")
                .accessibilityAddTraits(scope.share == share ? .isSelected : [])
            }
        } header: {
            Text("Where It Runs")
        } footer: {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                switch scope.share {
                case .all: Text("Every computer you've confirmed as yours. Work computers only when you add them.")
                case .selected: Text("Only the computers ticked below. The others don't see it at all.")
                case .none: Text("No computer gets the key. It stays on the hub, which still uses it for direct API calls like triage and translation.")
                }
                if kind == "api", scope.share != .none {
                    Text("The hub calls a direct API itself; the computers here can get the key too.")
                }
            }
        }
    }

    @ViewBuilder private var selectedSection: some View {
        let ids = ProviderScopeForm.choosable(machines, chosen: scope.machines)
        Section {
            if ids.isEmpty {
                Text("No computers are connected to the hub yet.").font(Theme.Font.label).foregroundStyle(.secondary)
            }
            ForEach(ids, id: \.self) { id in machineRow(id, work: false) }
        } header: {
            Text("Computers")
        } footer: {
            if let problem = scope.problem { Text(problem).foregroundStyle(Theme.danger) }
        }
    }

    @ViewBuilder private var workSection: some View {
        let ids = ProviderScopeForm.addableWork(machines, added: scope.work)
        if !ids.isEmpty {
            Section {
                ForEach(ids, id: \.self) { id in machineRow(id, work: true) }
            } header: {
                Text("Work Computers")
            } footer: {
                Text("The ones you tick get it too.")
            }
        }
    }

    /// A computer to tick: its name, and whether it is offline or still waiting for you to confirm it.
    private func machineRow(_ id: String, work: Bool) -> some View {
        let on = work ? scope.work.contains(id) : scope.machines.contains(id)
        let m = machine(id)
        return CheckRow(on: on, title: name(id), id: "computers.machine.\(id)") {
            scope.toggle(id, work: work)
            Haptics.tap()
        } content: {
            Text(name(id)).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(1)
            if m?.isPending == true {
                Text("Waiting for You to Confirm").font(Theme.Font.label).foregroundStyle(Theme.cautionText).lineLimit(1)
            } else if m?.online != true {
                Text("Offline [Provider Wizard View 431d7b]").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }

    // MARK: When the key travels

    @ViewBuilder private var deliverySections: some View {
        Section {
            Toggle(isOn: Binding(get: { scope.delivery == .push }, set: { scope.delivery = $0 ? .push : .onDemand })) {
                Text("Keep a Copy on Each Computer").lineLimit(2)
            }
            .accessibilityIdentifier("computers.push")
        } footer: {
            Text("Each computer fetches the key as soon as it connects, so it still starts while the hub can't be reached. Off: a computer fetches it the first time it needs it.")
        }
        if ownDetected {
            Section {
                Toggle(isOn: Binding(get: { scope.ownService(assign: assign, kind: kind) == .use },
                                     set: { scope.ownService = $0 ? .use : .skip })) {
                    Text("Use Even Where Already Signed In").lineLimit(2)
                }
                .accessibilityIdentifier("computers.ownService")
            } footer: {
                Text("Off: it's skipped on a computer where Claude Code or Codex already has its own sign-in, API key or launcher in the shell's startup file.")
            }
        }
    }

    // MARK: Keys for single computers

    @ViewBuilder private var keysSection: some View {
        let rows = scope.keyRows(machines, stored: stored)
        if !rows.isEmpty {
            Section {
                DisclosureGroup(isExpanded: $keysOpen) {
                    ForEach(rows, id: \.self) { id in keyRow(id) }
                } label: {
                    HStack {
                        Text("Per-Computer Keys").lineLimit(1)
                        Spacer()
                        if !keysOpen { Text(keysBadge).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1) }
                    }
                    // On the label only: on the group it would replace every key row's own identifier.
                    .accessibilityIdentifier("computers.keys")
                }
            } footer: {
                Text("A key here replaces the provider's key on that computer only. Leave it empty to keep what's there. Keys are stored encrypted on the hub and never sent back.")
            }
        }
    }

    /// Folded: how many computers have a key of their own (stored or typed), else that it is optional.
    private var keysBadge: String {
        let n = stored.filter { !scope.remove.contains($0) }.count + scope.typedKeys
        return n > 0 ? String(localized: "\(n) set") : String(localized: "Optional")
    }

    @ViewBuilder private func keyRow(_ id: String) -> some View {
        let has = stored.contains(id)
        let removed = scope.remove.contains(id)
        let typed = !(scope.keys[id] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(name(id)).font(Theme.Font.labelStrong).foregroundStyle(.secondary).lineLimit(1)
            SecretField(has && !removed ? String(localized: "Type a key to replace it") : String(localized: "Uses the provider's key"),
                        text: Binding(get: { scope.keys[id] ?? "" }, set: { scope.keys[id] = $0 }),
                        identifier: "computers.key.\(id)")
            if has, !typed {
                HStack(spacing: Theme.Space.s) {
                    if removed {
                        Text("Removed When You Save").font(Theme.Font.label).foregroundStyle(Theme.cautionText).lineLimit(1)
                        Spacer(minLength: Theme.Space.s)
                        Button { scope.remove.removeAll { $0 == id } } label: {
                            Label("Undo", systemImage: Theme.Icon.undo).font(Theme.Font.label).lineLimit(1)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("computers.key.\(id).undo")
                    } else {
                        Text("Has Its Own Key").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: Theme.Space.s)
                        Button(role: .destructive) { scope.remove.append(id) } label: {
                            Text("Remove Key").font(Theme.Font.label).lineLimit(1)
                        }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("computers.key.\(id).remove")
                    }
                }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
    }
}

/// The Computers row the editors show: what the scope comes to in a few words, its page behind it.
struct ProviderComputersRow: View {
    @Environment(AppModel.self) private var model
    @Binding var scope: ProviderScopeForm
    var old: LauncherView?
    var kind: String
    var agent: String? = nil
    var assign = false

    var body: some View {
        NavigationLink {
            ProviderComputersPage(scope: $scope, old: old, kind: kind, agent: agent, assign: assign)
        } label: {
            LabeledContent(String(localized: "Computers")) {
                Text(scope.rowSummary(legacy: old?.legacyScope == true && scope == ProviderScopeForm(view: old),
                                      name: { id in model.hubMachines.first { $0.id == id }?.name ?? id }))
                .foregroundStyle(scope.problem == nil ? Color.secondary : Theme.danger)
            }
            .lineLimit(1)
        }
        .accessibilityIdentifier("wizard.computers")
    }
}

#if DEBUG
/// `-openProviderComputers all|selected|none|legacy|keys|api`: a made-up provider of that scope with its Computers page open
/// (screenshots), on the hub's own computers: `selected` / `keys` on the first two, the first with a key of its own.
enum ProviderComputersDemo {
    static var state: String? { UserDefaults.standard.string(forKey: "openProviderComputers") }

    static func launcher(_ state: String, machines: [Machine]) -> LauncherView {
        let ids = machines.filter { !$0.isPending }.prefix(2).map(\.id)
        var l = LauncherView(name: "demo_scope", source: "persona", baseURL: "https://gateway.example.com", model: "glm-5", hasKey: true,
                             kind: state == "api" || state == "via" ? "api" : "launcher", api: "messages", auth: "bearer")
        l.share = "all"
        l.delivery = "on_demand"
        l.ownService = "skip"
        switch state {
        case "selected", "keys":
            l.share = "selected"
            l.scopeMachines = Array(ids)
            l.delivery = "push"
            l.keyMachines = Array(ids.prefix(1))
        case "none": l.share = "none"
        case "legacy":
            l.delivery = "push"
            l.ownService = "use"
            l.legacyScope = true
        case "api": l.ownService = "use"
        case "via": // Send Through on the first computer, the wizard at Connect (#4775)
            l.ownService = "use"
            l.viaMachine = ids.first
        default: break
        }
        return l
    }
}
#endif
