import SwiftUI

/// Where the provider sheet starts.
enum ProviderWizardStep: String, Hashable, Identifiable {
    /// Choose Provider (adding).
    case pick
    /// Connect (editing starts here).
    case connect
    /// Choose Models.
    case models
    /// Import from ~/.zshrc.
    case importZshrc = "import"
    /// A preset's key (a hub with built-in presets).
    case key
    /// What adding a preset came to.
    case done
    /// "I already use Claude Code": try the main conversation once.
    case claude
    /// "Where to get a model": where each built-in provider gives out keys.
    case getModel = "get"

    var id: Self { self }
}

/// Settings › Model Providers' sheet to add or change a provider (#2455, after cindy): three steps in one form, the way back in
/// the bar's leading button (a Mac sheet does not push, #1170), and Import from ~/.zshrc as a step of its own. Other Connection Types
/// (Direct API, a plugin's provider) is the form it had before (`LauncherEditor`).
///
/// On a hub with built-in presets (#4197, docs/assistant/onboarding.md 4.3「三条路」) the first step lists them: a preset goes
/// Paste Key → Done, the custom compatible gateway the three steps; "I already use Claude Code" and "Where to get a
/// model" sit beside them, and the import / other connection types under Advanced.
struct ProviderWizard: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var saved: () async -> Void
    @State private var wizard: ProviderWizardModel
    @State private var imports: LauncherImportModel
    @State private var step: ProviderWizardStep
    private let start: ProviderWizardStep
    /// Other Connection Types: the older form in this sheet.
    @State private var other = false
    /// The model whose context window and thinking levels are open under its row.
    @State private var expanded: String?
    @State private var newModel = ""
    @State private var showAdvanced = false
    /// The first step's Advanced (import, other ways), open.
    @State private var showPickAdvanced = false
    /// The preset's More Options (name, display name, model), open.
    @State private var showPresetMore = false
    /// Where Back goes from the key step: the list, or "Where to get a model" it came from.
    @State private var keyFrom: ProviderWizardStep = .pick
    /// A signup page open in the app (「Key 从哪拿」, 「去开通」).
    @State private var link: LinkTarget?
    /// The preset whose Sign Up page is open: once it closes, its key is next.
    @State private var signingUp: String?
    /// The Computers page, pushed without its row (`showComputers`, DEBUG screenshots).
    @State private var computersOpen = false

    init(existing: LauncherView? = nil, start: ProviderWizardStep? = nil, saved: @escaping () async -> Void = {},
         wizard: ProviderWizardModel? = nil, imports: LauncherImportModel? = nil, showComputers: Bool = false) {
        self.saved = saved
        _computersOpen = State(initialValue: showComputers)
        let first = start ?? (existing == nil ? .pick : .connect)
        self.start = first
        _step = State(initialValue: first)
        _wizard = State(initialValue: wizard ?? ProviderWizardModel(existing: existing))
        _imports = State(initialValue: imports ?? LauncherImportModel())
    }

    var body: some View {
        if other {
            LauncherEditor(existing: nil, saved: saved)
        } else {
            NavigationStack {
                Form {
                    switch step {
                    case .pick: pickSections
                    case .connect: connectSections
                    case .models: modelSections
                    case .importZshrc: LauncherImportSections(imports: imports, machines: importMachines)
                    case .key: keySections
                    case .done: doneSections
                    case .claude: claudeSections
                    case .getModel: getModelSections
                    }
                }
                .pluginLinks($link)
                .navigationDestination(isPresented: $computersOpen) { computersPage }
                .onChange(of: link) { _, now in
                    // Back from a Sign Up page: its key is next.
                    guard now == nil, let id = signingUp else { return }
                    signingUp = nil
                    pickPreset(id, from: .getModel)
                }
                .navigationTitle(title)
                .navigationBarTitleDisplayMode(.inline)
                .toolbar { toolbar }
                // Cancel on the first step (a Mac asks first once something is typed); Back after it.
                .sheetCancel(unsaved: !wizard.name.isEmpty || !wizard.apiKey.isEmpty || !wizard.presetKey.isEmpty,
                             when: step == start) { dismiss() }
                .animation(Theme.Motion.standard, value: step)
                // Again once the hub's features are in (a sheet opened while connecting starts without them).
                .task(id: wizard.presetsFlow) {
                    // The toggle's default reads the readiness: ask for a fresh one alongside the presets.
                    if wizard.presetsFlow, model.hubFeatures.contains(AssistantReadiness.feature) {
                        Task { await model.readiness.refresh(api: model.api) }
                    }
                    await wizard.loadPresets()
                    #if DEBUG
                    await debugFill()
                    #endif
                }
            }
            #if os(macOS)
            .macSheet()
            #endif
        }
    }

    /// The Computers page of the provider being edited: the preset's kind on the key step, else the stored provider's.
    private var computersPage: some View {
        @Bindable var wizard = wizard
        let presetStep = step == .key
        return ProviderComputersPage(scope: $wizard.scope, old: presetStep ? nil : wizard.existing,
                                     kind: presetStep ? wizard.preset?.kind ?? "launcher" : wizard.existing?.kind ?? "launcher",
                                     agent: presetStep ? nil : wizard.existing?.agent, assign: presetStep && wizard.preset?.runsClaude != false && wizard.assign)
    }

    private var title: String {
        switch step {
        case .importZshrc: String(localized: "Import from ~/.zshrc")
        default: wizard.isNew ? String(localized: "Add Provider") : String(localized: "Edit \(wizard.existing?.name ?? "")")
        }
    }

    /// 「第 2 步，共 3 步 · 连接」 over each step of the wizard (not the import, not the two side paths).
    private func stepHeader(_ n: Int, _ what: String) -> some View {
        Text(wizard.isNew ? String(localized: "Step \(n) of 3 · \(what)") : what)
            .accessibilityIdentifier("wizard.step")
    }

    // MARK: Bar

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        if step != start && step != .done {
            ToolbarItem(placement: .cancellationAction) {
                Button("Back [Provider Wizard View da336f]") { back() }.lineLimit(1).accessibilityIdentifier("wizard.back")
            }
        }
        ToolbarItem(placement: .confirmationAction) {
            switch step {
            case .pick: EmptyView()
            case .connect:
                Button("Next [Inbox Line acfc4e]") { Task { await toModels() } }.lineLimit(1)
                    .disabled(!wizard.connectionReady || wizard.discovering)
                    .accessibilityIdentifier("wizard.next")
            case .models:
                Button { Task { await save() } } label: {
                    if wizard.saving { ProgressView() } else { Text("Save").lineLimit(1) }
                }
                .disabled(!wizard.canSave)
                .accessibilityIdentifier("wizard.save")
            case .key:
                Button { Task { await savePreset() } } label: {
                    if wizard.saving { ProgressView() } else { Text("Save").lineLimit(1) }
                }
                .disabled(!wizard.canSavePreset)
                .accessibilityIdentifier("wizard.save")
            case .done:
                Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1).accessibilityIdentifier("wizard.done")
            case .claude:
                if wizard.claudeTry?.ok == true {
                    Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1).accessibilityIdentifier("wizard.done")
                }
            case .getModel: EmptyView()
            case .importZshrc:
                switch imports.phase {
                case .preview, .importing:
                    Button("Import") { Task { await runImport() } }.lineLimit(1)
                        .disabled(!imports.canImport).accessibilityIdentifier("import.confirm")
                case .done:
                    Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1).accessibilityIdentifier("import.done")
                default: EmptyView()
                }
            }
        }
    }

    private func back() {
        switch step {
        case .models: step = .connect
        case .importZshrc where imports.phase == .preview: imports.restart()
        case .key: step = keyFrom
        default: step = .pick
        }
    }

    /// Next from Connect: a new provider reads the gateway's models first (once), so the next step has them.
    private func toModels() async {
        if !wizard.discoveredOnce && wizard.choices.isEmpty { await wizard.discover() }
        step = .models
    }

    private func save() async {
        if await finished(wizard.save()) { dismiss() }
    }

    /// Saves the preset's provider; its last screen says what now runs on it.
    private func savePreset() async {
        if await finished(wizard.savePreset()) { step = .done }
    }

    private func pickPreset(_ id: String, from: ProviderWizardStep) {
        if wizard.presetID != id { showPresetMore = false }
        wizard.presetID = id
        keyFrom = from
        step = .key
    }

    private func runImport() async {
        await imports.runImport()
        _ = await finished(imports.phase == .done)
    }

    /// After a save or an import: the lists read the launchers again and the page that opened this hears of it.
    private func finished(_ ok: Bool) async -> Bool {
        guard ok else { Haptics.error(); return false }
        model.launchersChanged()
        model.readinessChanged()
        Haptics.success()
        await saved()
        return true
    }

    // MARK: 1 Choose Provider

    @ViewBuilder private var pickSections: some View {
        if wizard.presetsFlow { presetPickSections } else { legacyPickSections }
    }

    /// An older hub's first step, as it was: Anthropic official (written into the app), a gateway, the import, other ways.
    @ViewBuilder private var legacyPickSections: some View {
        Section {
            pickRow(title: String(localized: "Anthropic (Official)"), detail: String(localized: "api.anthropic.com, with an API key from the Anthropic Console"), id: "wizard.kind.anthropic") {
                BrandMark(Theme.Brand.claude, side: Theme.Size.icon)
            } action: {
                wizard.kind = .anthropic
                step = .connect
            }
            pickRow(title: String(localized: "Custom Gateway"), detail: String(localized: "An Anthropic-compatible endpoint, like a Coding Plan or your company's proxy"), id: "wizard.kind.custom") {
                ThemedIcon(Theme.Icon.server).foregroundStyle(Theme.accent)
            } action: {
                wizard.kind = .custom
                step = .connect
            }
        } header: { stepHeader(1, String(localized: "Choose Provider")) } footer: {
            Text("Providers run as Claude Code on your computers, for both the main chat and Agents. Choose which one to use in Model Configuration.")
        }
        Section {
            pickRow(title: String(localized: "Import from ~/.zshrc"), detail: String(localized: "Turn the aliases that call claude on a computer into providers"), id: "wizard.kind.import") {
                ThemedIcon(Theme.Icon.terminal).foregroundStyle(Theme.accent)
            } action: { step = .importZshrc }
            pickRow(title: String(localized: "Other Connection Types"), detail: String(localized: "Direct API (Messages / Responses), providers from extensions"), id: "wizard.kind.other") {
                ThemedIcon(Theme.Icon.options).foregroundStyle(.secondary)
            } action: { other = true }
        }
    }

    /// No computer yet: nothing to read a ~/.zshrc on (the import would wait for one forever).
    private var noComputer: Bool { model.hubMachines.isEmpty }

    /// A hub with built-in presets: its providers in its order, the gateway typed by hand last; the two side paths;
    /// the import and the other ways under Advanced.
    @ViewBuilder private var presetPickSections: some View {
        Section {
            if !wizard.presetsLoaded {
                ProgressView().frame(maxWidth: .infinity).padding(.vertical, Theme.Space.l)
            }
            ForEach(wizard.presets) { p in
                pickRow(title: p.title, detail: presetDetail(p), id: "wizard.preset.\(p.id)",
                        badge: p.isBuiltin ? nil : String(localized: "Extensions")) {
                    ThemedIcon(p.isBuiltin ? Theme.Icon.ai : Theme.Icon.plugins).foregroundStyle(Theme.accent)
                } action: { pickPreset(p.id, from: .pick) }
            }
            pickRow(title: String(localized: "Custom compatible gateway"), detail: String(localized: "A relay, a company gateway or any Anthropic-compatible service; you enter the address and auth yourself"),
                    id: "wizard.kind.custom") {
                ThemedIcon(Theme.Icon.server).foregroundStyle(Theme.accent)
            } action: {
                wizard.kind = .custom
                step = .connect
            }
        } header: { stepHeader(1, String(localized: "Choose a provider")) } footer: {
            if let e = wizard.presetsError {
                Text("Couldn't read the hub's providers: \(e). You can use a custom compatible gateway for now.").foregroundStyle(Theme.cautionText)
            } else {
                Text("Pick one and paste its API key; the address and models are already filled in.")
            }
        }
        Section {
            pickRow(title: String(localized: "I already use Claude Code"), detail: String(localized: "No provider needed: try one message to see if the main conversation works"), id: "wizard.path.claude") {
                BrandMark(Theme.Brand.claude, side: Theme.Size.icon)
            } action: { step = .claude }
            pickRow(title: String(localized: "Where to get a model"), detail: String(localized: "No API key yet: see where each provider signs you up"), id: "wizard.path.get") {
                ThemedIcon(Theme.Icon.help).foregroundStyle(Theme.accent)
            } action: { step = .getModel }
            .disabled(wizard.signupPresets.isEmpty)
        }
        Section {
            DisclosureGroup(isExpanded: $showPickAdvanced) {
                pickRow(title: String(localized: "Import from ~/.zshrc"),
                        detail: noComputer ? String(localized: "No computer yet: add one first, then import from it") : String(localized: "Turn the aliases that call claude on a computer into providers"),
                        id: "wizard.kind.import") {
                    ThemedIcon(Theme.Icon.terminal).foregroundStyle(noComputer ? Color.secondary : Theme.accent)
                } action: { step = .importZshrc }
                .disabled(noComputer)
                pickRow(title: String(localized: "Other Connection Types"), detail: String(localized: "Direct API (Messages / Responses), providers from extensions"), id: "wizard.kind.other") {
                    ThemedIcon(Theme.Icon.options).foregroundStyle(.secondary)
                } action: { other = true }
            } label: { Text("Advanced").lineLimit(1) }
            .accessibilityIdentifier("wizard.pick.advanced")
        }
    }

    /// A built-in provider says who it is for; an extension's says its kind and host.
    private func presetDetail(_ p: LauncherPreset) -> String {
        if p.isBuiltin { return p.description.flatMap { $0.isEmpty ? nil : $0 } ?? p.host }
        return [p.description, LauncherPresetText.subtitle(p)].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: "\n")
    }

    // MARK: 2 Paste Key

    @ViewBuilder private var keySections: some View {
        @Bindable var wizard = wizard
        if let p = wizard.preset {
            Section {
                HStack(spacing: Theme.Space.m) {
                    ThemedIcon(p.isBuiltin ? Theme.Icon.ai : Theme.Icon.plugins).foregroundStyle(Theme.accent)
                        .scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(p.title).font(Theme.Font.heading).lineLimit(1)
                        Text(p.isBuiltin ? p.host : String(localized: "Provided by the extension “\(p.plugin)”"))
                            .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                .accessibilityElement(children: .combine)
                SecretField(String(localized: "Paste the API key"), filled: String(localized: "Filled in"), text: $wizard.presetKey, identifier: "wizard.preset.key")
                    .onChange(of: wizard.presetKey) { wizard.keyChanged() }
                if let url = p.signup {
                    Button { link = LinkTarget(url: url) } label: {
                        Label("Where to get a key", systemImage: Theme.Icon.open).lineLimit(1)
                    }
                    .accessibilityIdentifier("wizard.preset.signup")
                }
            } header: { stepHeader(2, String(localized: "Paste the key")) } footer: {
                Text([p.keyHint, String(localized: "The key is stored encrypted on the hub and never sent back to clients.")].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: "\n"))
            }
            Section {
                keyTestRow(p)
            } header: { Text("Test") } footer: {
                if p.testable { Text("Once pasted, it's tested once: a very short request that may cost a little.") }
            }
            if p.runsClaude {
                Section {
                    Toggle(isOn: Binding(get: { wizard.assign }, set: { wizard.assignChoice = $0 })) {
                        Text("Use it for the main conversation and new agents").lineLimit(2)
                    }
                    .accessibilityIdentifier("wizard.assign")
                } footer: { Text("Only changes what nobody has set; models you picked stay.") }
            } else {
                Section {
                    Text("The main chat and Agents can't run on it. Processing, Sorting and other uses without a picked model may now use it; change that under Models.")
                        .font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
            }
            if wizard.scopeShown {
                Section {
                    ProviderComputersRow(scope: $wizard.scope, old: nil, kind: p.kind, assign: p.runsClaude && wizard.assign)
                } footer: { Text("Which computers can run it and get its key.") }
            }
            Section {
                DisclosureGroup(isExpanded: $showPresetMore) { presetMoreFields(p) } label: {
                    HStack {
                        Text("More options").lineLimit(1)
                        Spacer()
                        if !showPresetMore { Text("Name, default model").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1) }
                    }
                }
                .accessibilityIdentifier("wizard.preset.more")
            }
            if let e = wizard.error {
                Section { LoadProblem(text: e) }
            }
        }
    }

    @ViewBuilder private func keyTestRow(_ p: LauncherPreset) -> some View {
        if !p.testable {
            Text("This API protocol's key can't be tested here; save it and use it.").font(Theme.Font.label).foregroundStyle(.secondary)
        } else if wizard.keyTesting {
            HStack(spacing: Theme.Space.s) {
                ProgressView()
                Text("Testing this key…").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
        } else if let t = wizard.keyTest {
            ProbeResultRow(result: t)
            if wizard.regionBlocked {
                // the region, not the key: testing again or another key will not help, another provider will (#4526)
                Button { step = .pick } label: {
                    Label("Use another provider", systemImage: Theme.Icon.back).lineLimit(1)
                }
                .accessibilityIdentifier("wizard.preset.other")
            } else if !t.ok {
                Button { Task { await wizard.testKey() } } label: {
                    Label("Test again", systemImage: Theme.Icon.reload).lineLimit(1)
                }
                .accessibilityIdentifier("wizard.preset.retest")
            }
        } else {
            Text(wizard.presetKey.isEmpty ? String(localized: "Tested automatically once you paste a key") : String(localized: "Testing once you stop typing…"))
                .font(Theme.Font.label).foregroundStyle(.secondary)
        }
    }

    @ViewBuilder private func presetMoreFields(_ p: LauncherPreset) -> some View {
        @Bindable var wizard = wizard
        TextField(String(localized: "Name (leave empty for an automatic one)"), text: $wizard.presetName)
            .font(Theme.Font.body.monospaced())
            .textInputAutocapitalization(.never).autocorrectionDisabled()
            .accessibilityIdentifier("wizard.preset.name")
        if let problem = wizard.presetNameProblem {
            Text(problem).font(Theme.Font.label).foregroundStyle(Theme.danger)
        }
        TextField(String(localized: "Display name; empty uses “\(p.title)”"), text: $wizard.presetDisplayName)
            .accessibilityIdentifier("wizard.preset.displayName")
        if p.choices.isEmpty {
            TextField(String(localized: "Default Model"), text: $wizard.presetModel).textInputAutocapitalization(.never).autocorrectionDisabled()
                .onChange(of: wizard.presetModel) { wizard.keyChanged() }
        } else {
            Picker("Default Model", selection: $wizard.presetModel) {
                ForEach(p.choices) { m in Text(m.label).tag(m.id) }
            }
            .lineLimit(1)
            .onChange(of: wizard.presetModel) { wizard.keyChanged() }
        }
    }

    // MARK: 3 Done

    @ViewBuilder private var doneSections: some View {
        let summary = wizard.addedSummary
        Section {
            VStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.successFilled).font(Theme.Font.display).foregroundStyle(Theme.positive)
                Text("\(wizard.addedTitle) is connected").font(Theme.Font.title).multilineTextAlignment(.center)
                if !summary.assigned.isEmpty {
                    Text(summary.assigned).font(Theme.Font.body).multilineTextAlignment(.center)
                }
                ForEach(summary.kept, id: \.self) { k in
                    Text(k).font(Theme.Font.secondary).foregroundStyle(.secondary).multilineTextAlignment(.center)
                }
                if let p = wizard.preset, !p.runsClaude {
                    Text("The main chat and Agents can't run on it. Processing, Sorting and other uses without a picked model may now use it; change that under Models.")
                        .font(Theme.Font.secondary).foregroundStyle(.secondary).multilineTextAlignment(.center)
                } else if summary.assigned.isEmpty {
                    Text("To use it for the main conversation or agents, pick it in Model Settings.").font(Theme.Font.secondary).foregroundStyle(.secondary)
                        .multilineTextAlignment(.center)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, Theme.Space.l)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("wizard.doneResult")
        } header: { stepHeader(3, String(localized: "Done [Extension System Presentation c0b3fb]")) }
    }

    // MARK: I already use Claude Code

    @ViewBuilder private var claudeSections: some View {
        Section {
            Button { Task { await wizard.tryClaude() } } label: {
                HStack {
                    Label(wizard.claudeTry == nil ? String(localized: "Try a message") : String(localized: "Try Again"), systemImage: Theme.Icon.tryOut).lineLimit(1)
                    if wizard.claudeTrying { Spacer(); ProgressView() }
                }
            }
            .disabled(wizard.claudeTrying)
            .accessibilityIdentifier("wizard.claude.try")
            if let t = wizard.claudeTry { claudeTryRow(t) }
        } header: { Text("I already use Claude Code") } footer: {
            Text("The main conversation uses the Claude Code on your computer by default; no provider needed. Trying sends a real, very small request that may cost a little.")
        }
        if wizard.claudeTry != nil && wizard.claudeTry?.ok != true {
            Section {
                Button { step = .pick } label: { Text("Use an API key instead").lineLimit(1) }
                    .accessibilityIdentifier("wizard.claude.useKey")
            } footer: { Text("If it isn't signed in or doesn't work, you can pick a provider and paste its API key.") }
        }
    }

    @ViewBuilder private func claudeTryRow(_ t: ClaudeTry) -> some View {
        switch t {
        case .answered(let r):
            if let p = r.probe {
                let machine = r.machine.flatMap { $0.name.isEmpty ? nil : $0.name }
                let ok = p.ok == true
                ClaudeTryResult(ok: ok,
                                title: ok ? (machine.map { String(localized: "Claude Code on \($0) works") } ?? String(localized: "Claude Code works"))
                                            : String(localized: "Tried a message, it didn't work"),
                                detail: ok ? [p.model, p.answer.map { String(localized: "Replied “\($0)”") }, p.millis.map { LauncherCheckRow.seconds($0) }]
                                                .compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · ")
                                             : (p.error ?? ""))
            } else {
                // The computer is not ready: no try ran; the step in the way says why.
                let step = r.steps.first { $0.state != "ok" && $0.state != "skipped" && $0.title?.isEmpty == false }
                ClaudeTryResult(ok: false, title: step?.title ?? String(localized: "Can't try right now"),
                                detail: step?.detail ?? String(localized: "The main conversation has no computer it can use yet."))
            }
        case .unsupported:
            ClaudeTryResult(ok: false, title: String(localized: "Can't try it here yet"), detail: String(localized: "Send a message in the main conversation to try it."))
        case .failed(let e):
            ClaudeTryResult(ok: false, title: String(localized: "Tried a message, it didn't work"), detail: e)
        }
    }

    // MARK: Where to get a model

    @ViewBuilder private var getModelSections: some View {
        Section {
            ForEach(wizard.signupPresets) { p in
                HStack(spacing: Theme.Space.m) {
                    ThemedIcon(Theme.Icon.ai).foregroundStyle(Theme.accent)
                        .scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text(p.title).font(Theme.Font.heading).lineLimit(1)
                        if let d = p.description, !d.isEmpty {
                            Text(d).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(3)
                        }
                    }
                    Spacer(minLength: Theme.Space.s)
                    Button("Sign up") {
                        guard let url = p.signup else { return }
                        signingUp = p.id
                        link = LinkTarget(url: url)
                    }
                    .lineLimit(1)
                    .themedButton()
                    .accessibilityIdentifier("wizard.get.\(p.id)")
                }
                .padding(.vertical, Theme.Space.xxs)
            }
        } header: { Text("Where to get a model") } footer: {
            Text("Sign up on the provider's site and get an API key, then come back and paste it.")
        }
    }

    private func pickRow(title: String, detail: String, id: String, badge: String? = nil, @ViewBuilder icon: () -> some View,
                         action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.m) {
                icon().scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    HStack(spacing: Theme.Space.sm) {
                        Text(title).font(Theme.Font.heading).foregroundStyle(.primary).lineLimit(1)
                        if let badge { Chip(text: badge) }
                    }
                    Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                }
                Spacer(minLength: Theme.Space.s)
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
            }
            .padding(.vertical, Theme.Space.xxs)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(id)
    }

    // MARK: 2 Connect

    @ViewBuilder private var connectSections: some View {
        @Bindable var wizard = wizard
        Section {
            TextField(String(localized: "Name, like jv_glm"), text: $wizard.name)
                .font(Theme.Font.body.monospaced())
                .textInputAutocapitalization(.never).autocorrectionDisabled()
                .disabled(!wizard.isNew)
                .accessibilityIdentifier("wizard.name")
        } header: { stepHeader(2, String(localized: "Connect [V N C Page a55741]")) } footer: {
            Text(wizard.nameProblem ?? String(localized: "Letters, digits, _, and -. You pick it by this name everywhere."))
                .foregroundStyle(wizard.nameProblem == nil ? Color.secondary : Theme.danger)
        }
        Section {
            if wizard.kind == .anthropic {
                LabeledContent("Address", value: "api.anthropic.com")
            } else {
                TextField("https://…", text: $wizard.baseURL)
                    .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    .accessibilityIdentifier("wizard.url")
                Picker("Authentication [Provider Wizard View 9f026b]", selection: $wizard.auth) {
                    Text("x-api-key").tag("api_key")
                    Text("Bearer").tag("bearer")
                }
                .lineLimit(1)
            }
            SecretField(wizard.hasStoredKey ? String(localized: "API Key (leave empty to keep)") : "API Key", filled: String(localized: "Filled in"), text: $wizard.apiKey,
                        identifier: "wizard.key")
        } header: { Text("Gateway") } footer: {
            Text(wizard.kind == .anthropic ? String(localized: "The key is stored encrypted on the hub, handed to the computer only at launch, and never sent back to apps.")
                 : String(localized: "Choose Bearer if the gateway only accepts Authorization: Bearer (ANTHROPIC_AUTH_TOKEN). The key is stored encrypted on the hub and never sent back to apps."))
        }
        if wizard.isAPI {
            Section {
                Picker("Send Through", selection: $wizard.viaMachine) {
                    Text("The Hub Itself").tag("")
                    ForEach(wizard.viaChoices, id: \.id) { m in Text(m.name).tag(m.id) }
                }
                .lineLimit(1)
                .accessibilityIdentifier("wizard.via")
            } footer: {
                Text("Which machine sends this connection’s requests. Choose one of your computers when the hub can’t reach the provider and that computer can; it must be online when a request goes out.")
            }
        }
        Section {
            Button { Task { await wizard.runTest() } } label: {
                HStack {
                    Label("Test Connection", systemImage: Theme.Icon.tryOut).lineLimit(1)
                    if wizard.testing { Spacer(); ProgressView() }
                }
            }
            .disabled(!wizard.canTest)
            .accessibilityIdentifier("wizard.test")
            if let t = wizard.test { ProbeResultRow(result: t) }
            Button { Task { await wizard.discover() } } label: {
                HStack {
                    Label("Get Model List", systemImage: Theme.Icon.reload).lineLimit(1)
                    Spacer()
                    if wizard.discovering { ProgressView() } else if wizard.discoveredOnce {
                        Text("\(wizard.discovered.count) [Chats View cb60bd]").font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
            }
            .disabled(!wizard.connectionReady || wizard.discovering)
            .accessibilityIdentifier("wizard.discover")
            if let e = wizard.discoverError {
                Text(e).font(Theme.Font.label).foregroundStyle(Theme.danger).lineLimit(4)
            }
        } header: { Text("Check") } footer: {
            Text("Testing the connection sends a real, very small request (\(wizard.testModel.isEmpty ? String(localized: "get the model list first") : wizard.testModel)), which may cost a little.")
        }
    }

    // MARK: 3 Choose Models

    @ViewBuilder private var modelSections: some View {
        @Bindable var wizard = wizard
        Section {
            if wizard.choices.isEmpty {
                Text(wizard.discoverError.map { String(localized: "Couldn't read the model list: \($0). You can add models below.") } ?? String(localized: "The gateway didn't provide a model list. Add models below."))
                    .font(Theme.Font.label).foregroundStyle(.secondary)
            }
            ForEach(wizard.choices) { m in modelRow(m) }
            HStack(spacing: Theme.Space.s) {
                TextField(String(localized: "Add a model ID"), text: $newModel)
                    .font(Theme.Font.body.monospaced())
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .onSubmit { addModel() }
                    .accessibilityIdentifier("wizard.newModel")
                Button("Add [Beautiful U I Bridge 7a8a11]") { addModel() }.lineLimit(1).buttonStyle(.borderless)
                    .disabled(newModel.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        } header: { stepHeader(3, String(localized: "Choose Models")) } footer: {
            Text("Check the models to use with this gateway. Models with a context window over 200K run as “[1m]”. Tap ⓘ to change the context window and thinking level.")
        }
        if !wizard.models.isEmpty {
            Section {
                Picker("Default Model", selection: $wizard.defaultModel) {
                    ForEach(wizard.models) { Text($0.title).tag($0.id) }
                }
                .lineLimit(1)
                .accessibilityIdentifier("wizard.default")
            } footer: { Text("Used when no model is chosen. Choose per use in Model Configuration.") }
        }
        Section {
            DisclosureGroup(isExpanded: $showAdvanced) { advancedFields } label: { Text("Advanced").lineLimit(1) }
                .accessibilityIdentifier("wizard.advanced")
        }
        if let e = wizard.error {
            Section { LoadProblem(text: e) }
        }
    }

    private func addModel() {
        if wizard.addModel(newModel) { newModel = "" }
    }

    @ViewBuilder private func modelRow(_ m: ProviderModel) -> some View {
        let picked = wizard.isPicked(m.id)
        HStack(spacing: Theme.Space.m) {
            CheckRow(on: picked, title: m.title, id: "wizard.model.\(m.id)") {
                wizard.toggle(m)
                Haptics.tap()
            } content: {
                HStack(spacing: Theme.Space.sm) {
                    Text(m.title).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(1)
                    if m.id == wizard.defaultModel { Chip(text: String(localized: "Default [Prompt Composer 844b8c]"), look: .tint(Theme.accent)) }
                }
                Text(ProviderModelText.detail(wizard.model(m.id) ?? m)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            if picked {
                Button {
                    withAnimation(Theme.Motion.standard) { expanded = expanded == m.id ? nil : m.id }
                } label: { ThemedIcon(Theme.Icon.info) }
                .buttonStyle(.borderless)
                .accessibilityLabel("Context and thinking level for \(m.title)")
            }
        }
        if picked, expanded == m.id { modelDetail(m.id) }
    }

    /// One picked model's context window and default thinking level, under its row.
    @ViewBuilder private func modelDetail(_ id: String) -> some View {
        let m = wizard.model(id) ?? ProviderModel(id: id)
        let levels = (m.efforts ?? []).isEmpty ? SessionModes.effortLevels.map(\.id) : (m.efforts ?? [])
        numberField(String(localized: "Context Window"), Binding(get: { m.contextWindow.map(String.init) ?? "" },
                                     set: { v in wizard.update(id) { $0.contextWindow = Int(v) } }), String(localized: "Unknown (200K assumed)"))
        Picker("Default Thinking Level", selection: Binding(get: { m.defaultEffort ?? "" },
                                            set: { v in wizard.update(id) { $0.defaultEffort = v.isEmpty ? nil : v } })) {
            Text("None [Provider Wizard View 03b6f8]").tag("")
            ForEach(levels, id: \.self) { level in
                Text(SessionModes.effortLevels.first { $0.id == level }?.title ?? level).tag(level)
            }
        }
        .lineLimit(1)
    }

    @ViewBuilder private var advancedFields: some View {
        @Bindable var wizard = wizard
        Group {
            tierField(String(localized: "Opus Tier"), $wizard.opusModel)
            tierField(String(localized: "Sonnet Tier"), $wizard.sonnetModel)
            tierField(String(localized: "Haiku Tier"), $wizard.haikuModel)
            tierField(String(localized: "Fable Tier"), $wizard.fableModel)
            tierField(String(localized: "Small Model"), $wizard.smallModel)
            tierField(String(localized: "Subagent Model"), $wizard.subagentModel)
            Toggle("Always Use for Subagents", isOn: $wizard.subagentForce).lineLimit(1)
            Picker("Default Thinking Level", selection: $wizard.effort) {
                Text("Model Default").tag("")
                ForEach(SessionModes.effortLevels) { Text($0.title).tag($0.id) }
            }
            .lineLimit(1)
        }
        Group {
            numberField(String(localized: "Auto-Compact Window"), $wizard.compactWindow, String(localized: "By model"))
            numberField(String(localized: "Max Context"), $wizard.maxContext, String(localized: "By model"))
            numberField(String(localized: "Request Timeout (ms)"), $wizard.timeoutMS, String(localized: "Default 3000000"))
            // As iOS Settings' Configure Proxy: one row saying the choice, its page behind it (#6238).
            NavigationLink {
                ProviderProxyPage(wizard: wizard)
            } label: {
                LabeledContent(String(localized: "Proxy")) {
                    Text(wizard.proxySummary).foregroundStyle(wizard.proxyProblem == nil ? Color.secondary : Theme.danger)
                }
                .lineLimit(1)
            }
            // Where it runs and when its key travels (#2333), the same kind of row; only on a hub that takes scopes.
            if wizard.scopeShown {
                ProviderComputersRow(scope: $wizard.scope, old: wizard.existing, kind: wizard.existing?.kind ?? "launcher",
                                     agent: wizard.existing?.agent)
            }
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                TextField(wizard.hasStoredHeaders ? String(localized: "Headers set (hidden; leave empty to keep)") : String(localized: "Headers, one Name: value per line"),
                          text: $wizard.headers, axis: .vertical)
                    .font(Theme.Font.body.monospaced())
                    .lineLimit(2...5)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .privacySensitive() // header values are credentials: redacted in snapshots and the app switcher
                    .accessibilityIdentifier("wizard.headers")
                if wizard.hasStoredHeaders {
                    Button("Clear Saved Headers", role: .destructive) { wizard.clearHeaders = true }
                        .lineLimit(1).buttonStyle(.borderless)
                } else if wizard.clearHeaders {
                    Text("Saved headers will be cleared when you save").font(Theme.Font.label).foregroundStyle(Theme.cautionText)
                }
            }
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                TextField(wizard.hasStoredEnv ? String(localized: "Environment set (hidden; leave empty to keep)") : String(localized: "Environment, one NAME=value per line"),
                          text: $wizard.env, axis: .vertical)
                    .font(Theme.Font.body.monospaced())
                    .lineLimit(2...5)
                    .textInputAutocapitalization(.never).autocorrectionDisabled()
                    .privacySensitive() // values can be credentials, like the headers
                    .accessibilityIdentifier("wizard.env")
                if wizard.hasStoredEnv {
                    Button("Clear Saved Environment", role: .destructive) { wizard.clearEnv = true }
                        .lineLimit(1).buttonStyle(.borderless)
                } else if wizard.clearEnv {
                    Text("Saved environment will be cleared when you save").font(Theme.Font.label).foregroundStyle(Theme.cautionText)
                } else {
                    Text("Set last, over the fields above and the computer's own. NAME= with nothing after it sets a variable empty; to go around a proxy, choose No Proxy under Proxy instead.")
                        .font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
        }
    }

    private func tierField(_ label: String, _ text: Binding<String>) -> some View {
        LabeledContent(label) {
            TextField(String(localized: "Same as default model"), text: text)
                .font(Theme.Font.body.monospaced()).multilineTextAlignment(.trailing)
                .textInputAutocapitalization(.never).autocorrectionDisabled()
        }
    }

    private func numberField(_ label: String, _ text: Binding<String>, _ hint: String) -> some View {
        LabeledContent(label) {
            TextField(hint, text: Binding(get: { text.wrappedValue }, set: { text.wrappedValue = $0.filter(\.isNumber) }))
                .keyboardType(.numberPad).multilineTextAlignment(.trailing).font(Theme.Font.body.monospacedDigit())
        }
    }

    // MARK: Import

    /// The machines to import from: the hub group's, online first.
    private var importMachines: [Machine] {
        model.hubMachines.sorted { ($0.online ? 0 : 1, $0.name) < ($1.online ? 0 : 1, $1.name) }
    }

    #if DEBUG
    /// `-openProviderWizard connect|models` fills the form in with made-up values (screenshots; `-openProviderComputers new`
    /// then opens its Computers page); `key` / `done` take the
    /// first built-in preset (`-wizardPreset <id>` another) with a made-up key (`-wizardKey <key>`: the mock hub
    /// refuses one with "bad" in it), `done` saves it; `claude` tries the main conversation.
    private func debugFill() async {
        guard let want = UserDefaults.standard.string(forKey: "openProviderWizard"), wizard.isNew, wizard.name.isEmpty else { return }
        if want == "claude" { await wizard.tryClaude(); return }
        if want == "key" || want == "done" {
            let d = UserDefaults.standard
            guard let p = wizard.presets.first(where: { $0.id == d.string(forKey: "wizardPreset") }) ?? wizard.presets.first(where: \.isBuiltin)
            else { return }
            pickPreset(p.id, from: .pick)
            wizard.presetKey = d.string(forKey: "wizardKey") ?? "sk-demo-0123456789"
            await wizard.testKey()
            if want == "done" { await savePreset() }
            return
        }
        guard want == "connect" || want == "models" else { return }
        wizard.kind = .custom
        wizard.name = "demo_gateway"
        wizard.baseURL = "https://gateway.example.com"
        wizard.apiKey = "sk-test-demo"
        if want == "models" { await toModels() }
        // `-openProviderComputers new` on top: the new provider's Computers page.
        if want == "models", ProviderComputersDemo.state == "new" { computersOpen = true }
    }
    #endif
}

/// A row that is ticked or not: the tick, what it is about, and selected / not selected for VoiceOver.
struct CheckRow<Content: View>: View {
    var on: Bool
    var title: String
    var id: String
    var action: () -> Void
    @ViewBuilder var content: Content

    var body: some View {
        Button(action: action) {
            HStack(alignment: .top, spacing: Theme.Space.m) {
                ThemedIcon(on ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                    .foregroundStyle(on ? Theme.accent : Color.secondary).font(Theme.Font.heading)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) { content }
                Spacer(minLength: 0)
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(on ? String(localized: "\(title), selected") : String(localized: "\(title), not selected"))
        .accessibilityIdentifier(id)
    }
}

/// How a provider's model reads under its name: 「glm-5 · 200K · 思考 low–high」.
enum ProviderModelText {
    static func detail(_ m: ProviderModel) -> String {
        var parts = [m.id]
        if let w = m.contextWindow, w > 0 { parts.append(LauncherDetail.tokens(w)) }
        if let e = m.efforts, !e.isEmpty {
            let range = e.count > 1 ? "\(e.first!)–\(e.last!)" : e[0]
            parts.append(String(localized: "Thinking \(range) [Chat View feba04]"))
        }
        return parts.joined(separator: " · ")
    }
}

/// Test Connection's answer: ✓ with how long and which model, ✗ with why.
struct ProbeResultRow: View {
    var result: LauncherProbeResult

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            ThemedIcon(result.ok ? Theme.Icon.successFilled : Theme.Icon.errorFilled)
                .foregroundStyle(result.ok ? Theme.positive : Theme.danger)
                .scaledFrame(width: Theme.Size.iconS)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(result.ok ? String(localized: "Connected [Provider Wizard View da0bfe]") : String(localized: "Connection Failed")).font(Theme.Font.heading).lineLimit(1)
                let detail = [result.model, result.ms.map { LauncherCheckRow.seconds($0) }].compactMap { $0 }.joined(separator: " · ")
                if result.ok, !detail.isEmpty {
                    Text(detail).font(Theme.Font.label.monospacedDigit()).foregroundStyle(.secondary).lineLimit(1)
                }
                if let e = result.error, !e.isEmpty {
                    Text(e).font(Theme.Font.code).foregroundStyle(Theme.danger).lineLimit(6).textSelection(.enabled)
                }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityIdentifier("wizard.testResult")
    }
}

/// "I already use Claude Code"'s answer: ✓ the computer's Claude Code answered (the model, its words, how long), ✗ why not.
struct ClaudeTryResult: View {
    var ok: Bool
    var title: String
    var detail: String

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.m) {
            ThemedIcon(ok ? Theme.Icon.successFilled : Theme.Icon.errorFilled)
                .foregroundStyle(ok ? Theme.positive : Theme.danger)
                .scaledFrame(width: Theme.Size.iconS)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title).font(Theme.Font.heading).lineLimit(2)
                if !detail.isEmpty {
                    Text(detail).font(ok ? Theme.Font.label : Theme.Font.code).foregroundStyle(ok ? Color.secondary : Theme.danger)
                        .lineLimit(6).textSelection(.enabled)
                }
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("wizard.claude.result")
    }
}

/// Import from ~/.zshrc's sections: the machine, what was found (to tick), and what came of it.
struct LauncherImportSections: View {
    var imports: LauncherImportModel
    var machines: [Machine]

    var body: some View {
        switch imports.phase {
        case .machine, .loading: machineSection
        case .preview, .importing: previewSections
        case .done: resultSection
        }
    }

    @ViewBuilder private var machineSection: some View {
        Section {
            if machines.isEmpty {
                Text("No computer yet. Add one first, then import from it.").font(Theme.Font.label).foregroundStyle(.secondary)
                    .accessibilityIdentifier("import.noComputer")
            }
            ForEach(machines) { m in
                Button {
                    imports.machine = m.id
                    Task { await imports.load() }
                } label: {
                    HStack(spacing: Theme.Space.m) {
                        ThemedIcon(Theme.Icon.machine).foregroundStyle(m.online ? Theme.accent : Color.secondary)
                        Text(m.name).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(1)
                        Spacer(minLength: Theme.Space.s)
                        if imports.phase == .loading, imports.machine == m.id { ProgressView() }
                        else if !m.online { Text("Offline [Provider Wizard View 431d7b]").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1) }
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .disabled(!m.online || imports.phase == .loading)
                .accessibilityIdentifier("import.machine.\(m.id)")
            }
        } header: { Text("Choose a Computer") } footer: {
            Text("Reads this computer's ~/.zshrc, finds lines like alias x=\\\"K=V … claude\\\", and turns them into providers by gateway and key. Keys only pass through the hub and are stored encrypted.")
        }
        if let e = imports.error {
            Section {
                Label(e, systemImage: Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.danger)
                if imports.outdated {
                    Text("Redeploy personad on this computer in Settings › Computers, then try again.").font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
            .accessibilityIdentifier("import.error")
        }
    }

    @ViewBuilder private var previewSections: some View {
        Section {
            if imports.providers.isEmpty {
                Text("This computer's ~/.zshrc has no aliases that call claude.").font(Theme.Font.label).foregroundStyle(.secondary)
            }
            ForEach(imports.providers) { p in
                CheckRow(on: imports.picked.contains(p.name), title: p.name, id: "import.provider.\(p.name)") {
                    imports.toggle(p.name)
                } content: {
                    HStack(spacing: Theme.Space.sm) {
                        Text(p.name).font(Theme.Font.heading.monospaced()).foregroundStyle(.primary).lineLimit(1)
                        if let e = p.existing { Chip(text: String(localized: "Merge into \(e)")) }
                    }
                    Text(ImportText.detail(p)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2)
                    if let a = p.aliases, !a.isEmpty {
                        Text("alias " + a.joined(separator: "、")).font(Theme.Font.code).foregroundStyle(.tertiary).lineLimit(1)
                    }
                }
            }
        } header: { Text(machineName) } footer: {
            Text("For a provider with the same gateway and key, only the models are merged; other settings stay the same.")
        }
        if let e = imports.error {
            Section { LoadProblem(text: e) }
        }
    }

    private var machineName: String {
        let name = machines.first { $0.id == imports.machine }?.name ?? imports.machine
        return String(localized: "Found \(imports.providers.count) providers on \(name)")
    }

    @ViewBuilder private var resultSection: some View {
        let created = imports.result?.created ?? []
        let updated = imports.result?.updated ?? []
        Section {
            ForEach(created, id: \.self) { n in
                Label { Text(n).font(Theme.Font.body.monospaced()) } icon: {
                    ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.positive)
                }
            }
            ForEach(updated, id: \.self) { n in
                Label { Text("\(n) (models merged)").font(Theme.Font.body.monospaced()) } icon: {
                    ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.accent)
                }
            }
            if created.isEmpty && updated.isEmpty {
                Text("No providers were imported.").font(Theme.Font.label).foregroundStyle(.secondary)
            }
        } header: { Text("\(created.count) Added, \(updated.count) Merged") } footer: {
            Text("Choose them for the main chat and Agents in Model Configuration.")
        }
        .accessibilityIdentifier("import.result")
    }
}

/// How an imported provider reads: its gateway, its models, whether it has a key.
enum ImportText {
    static func detail(_ p: LauncherImportPreview.Provider) -> String {
        var parts: [String] = []
        parts.append(p.baseURL.flatMap { URL(string: $0)?.host() } ?? String(localized: "Anthropic (Official)"))
        let models = (p.models ?? []).map(\.id)
        if !models.isEmpty { parts.append(models.count == 1 ? models[0] : String(localized: "\(models.count) models")) }
        else if let m = p.model, !m.isEmpty { parts.append(m) }
        if p.hasKey != true { parts.append(String(localized: "No key")) }
        return parts.joined(separator: " · ")
    }
}
