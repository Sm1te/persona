import SwiftUI

/// The Persona tab's top bar (docs/apple/ios.md「Persona top bar and sidebar」), floating over the conversation with a fog under it
/// instead of a solid bar: Chat History (the sidebar) on the left, Persona's face and name in the middle, New Chat on the right.
/// The face opens a menu: Knowledge, Details (the profile), the copy items (the working directory, a block of facts to
/// paste to another agent, the session id), the launcher, the session info card, Interrupt. While the hub is out of reach
/// the name pill shows the spinner and Waiting for Network / Connecting… / Updating….
struct AssistantTopBar: View {
    @Environment(AppModel.self) private var model
    /// In the shell (docs/apple/ios.md「New shell」) the middle is the Assistant | Agents switch; its Assistant opens this bar's menu.
    @Environment(\.shell) private var shell
    /// The system's bars down the side (iPhone Duo): the shell's bar is the side rail there.
    @Environment(\.shellRailEdge) private var railEdge
    /// The conversation on screen; nil on the new-conversation page.
    var key: SessionKey?
    /// The sidebar is a column beside the conversation (a wide display): the button shows or hides it.
    var sidebarIsColumn = false
    /// The column is out. On a phone's tabs (iPhone Duo open) its own button folds it, at its top, so this one is only
    /// there to bring it back (#2623).
    var sidebarShown = false
    /// A phone's tabs (iPhone Duo open): the Assistant | Agents switch in the face's place (#2623).
    @Environment(\.pageTabSwitch) private var pageTabSwitch
    var openSidebar: () -> Void
    var newConversation: () -> Void
    var openKnowledge: () -> Void
    var openProfile: () -> Void
    @State private var showInfo = false
    @State private var showLink = false
    @State private var launchers: [Launcher] = []
    /// Each launcher's /model list, fetched when the bar appears (the menu's model submenus).
    @State private var modelOptions: [String: [AgentModelOption]] = [:]
    /// A copy's confirmation, in the name pill for a moment (a toast would sit right on the face).
    @State private var hint: String?
    /// The launcher whose "Other Model…" is open (#1644): a model ID typed by hand for the next message.
    @State private var typingFor: String?
    #if os(macOS)
    @Environment(\.macTitleRowLeading) private var titleRowLeading
    #endif

    /// The bar's height below the status bar: what the conversation leaves free at its top so nothing hides under it.
    /// On the Mac, the title bar's row (`macBar`).
    #if os(macOS)
    static let height: CGFloat = Theme.Size.macToolbarHeight
    #else
    static let height: CGFloat = 70
    #endif
    /// How far below the bar the fog fades out.
    static let fade: CGFloat = 44
    /// How far below the safe area's top the fog stays solid: just past the round buttons and the switch, so a line of
    /// text is hidden under the controls and starts fading right below them.
    static let fogSolid: CGFloat = height - Theme.Space.l
    /// What the page leaves free at its top for the bar: little while it is the side rail (iPhone Duo, `ShellSideRail`),
    /// where the column beside the content holds it.
    static func room(_ railEdge: HorizontalEdge?) -> CGFloat {
        #if os(iOS) || os(visionOS)
        return railEdge == nil ? height : Theme.Space.s
        #else
        return height
        #endif
    }
    /// How far below the status bar the round buttons start. The sidebar's header beside the bar in the split starts
    /// there too, with the same `circle` (AssistantSidebar `besideBar`), so the two rows line up.
    static let topInset = Theme.Space.xxs

    private var session: Session? { key.flatMap { model.session($0) } }
    private var machineID: String { model.assistantMachineID(key) }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("AssistantTopBar")
        #endif
        bar
            // Session Info is a sheet, like the Agents chat's (SessionInfoSheet).
            .sheet(isPresented: Binding(get: { showInfo && key != nil }, set: { showInfo = $0 })) {
                if let key {
                    #if os(macOS)
                    // A window sheet: SessionInfoSheet has no stack or Done of its own on the Mac (it is the chat's inspector there).
                    NavigationStack {
                        SessionInfoSheet(key: key, session: session, assistant: true)
                            .navigationTitle("Session Info")
                            .macSheetDone { showInfo = false }
                    }
                    .macSheet()
                    #else
                    SessionInfoSheet(key: key, session: session, assistant: true)
                        .presentationDetents([.medium, .large])
                        .presentationDragIndicator(.visible)
                    #endif
                }
            }
            .onChange(of: key) { showInfo = false }
            // "Other Model…" (#1644): an ID typed by hand for the next message; the IDs typed before can be forgotten
            // there, so the submenus are read again when it closes.
            .sheet(isPresented: Binding(get: { typingFor != nil && key != nil }, set: { if !$0 { typingFor = nil } }),
                   onDismiss: { Task { await loadModelOptions() } }) {
                if let l = typingFor {
                    TypedModelSheet(machine: model.hubRef(machineID), launcher: l, initial: typedPick(l),
                                    note: String(localized: "This chat uses it from the next message.")) { id in pick(l, model: id) }
                }
            }
            #if DEBUG
            .task { // `-showInfo YES`: Session Info open, for screenshots (the Agents chat reads the same flag)
                if UserDefaults.standard.bool(forKey: "showInfo") { try? await Task.sleep(for: .seconds(2)); showInfo = true }
            }
            #endif
        .task(id: model.hubRef(machineID)) {
            launchers = ((try? await model.launchers(model.hubRef(machineID))) ?? []).filter(\.runsAssistant)
            await loadModelOptions()
        }
    }

    @ViewBuilder private var bar: some View {
        #if os(macOS)
        if shell != nil { shellBar } else if AppStyle.current.usesBeautifulUI { classicBar } else { macBar }
        #else
        if shell != nil { shellBar } else { classicBar }
        #endif
    }

    #if os(macOS)
    /// The Mac's (#2113): the title bar's row of the conversation's column, as the Agents chat's (`macTitleRow`):
    /// Chat History, the name (its menu: Knowledge, Details, the copies, the launcher, Interrupt), and New Chat (⇧⌘N) at the end,
    /// instead of the phone's face over a pill in the middle.
    private var macBar: some View {
        let link = model.connection.shown
        return HStack(spacing: Theme.Space.s) {
            MacTitleRowButton(String(localized: "Chat History [Shell Components 60636b]"), symbol: sidebarIsColumn ? Theme.Icon.sidebar : Theme.Icon.sidebarMenu, action: openSidebar)
                .accessibilityIdentifier("assistant-sidebar-button")
            Menu { menuItems(link) } label: {
                HStack(spacing: Theme.Space.xs) {
                    VStack(alignment: .leading, spacing: 0) {
                        Text(name).font(Theme.Font.heading).foregroundStyle(Theme.macLabel).lineLimit(1)
                        Group {
                            if let hint {
                                Label(hint, systemImage: Theme.Icon.successFilled).foregroundStyle(Theme.positive)
                            } else if link != .online {
                                ConnectionLabel(phase: link, compact: true)
                            }
                        }
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                    }
                    ThemedIcon(Theme.Icon.expand).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                }
                .contentShape(.rect)
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .fixedSize()
            .help("Knowledge, details, copy directory and chat info")
            .accessibilityLabel(link != .online ? String(localized: "\(name), \(ConnectionLabel.text(link, hubRestarting: model.hubUpdate?.restarting == true))") : name)
            .accessibilityIdentifier("persona-avatar")
            .connectionDetails($showLink)
            .layoutPriority(-1)
            Spacer(minLength: Theme.Space.s)
            MacTitleRowButton(String(localized: "New Chat (⇧⌘N)"), symbol: Theme.Icon.compose, action: newConversation)
                .disabled(key == nil)
                .accessibilityIdentifier("assistant-new")
        }
        .padding(.horizontal, Theme.Space.l)
        // In the title bar's row, past the traffic lights when the bar is at the window's edge (#830).
        .padding(.leading, max(0, titleRowLeading - Theme.Space.l))
        .frame(height: Self.height)
        .frame(maxWidth: .infinity)
        .background(alignment: .top) { BarFog(solid: Self.height - Theme.Space.sm, fade: Self.fade) }
        .animation(Theme.Motion.standard, value: hint)
        .task(id: hint) { await clearHint() }
    }
    #endif

    /// ChatGPT's top bar: the persona's face (the drawer; a mark while the inbox has something for the user), the
    /// switch, New Chat.
    @ViewBuilder private var shellBar: some View {
        #if os(iOS) || os(visionOS)
        if let railEdge { shellRail(railEdge) } else { shellRow }
        #else
        shellRow
        #endif
    }

    #if os(iOS) || os(visionOS)
    /// The shell's bar as the side rail (iPhone Duo): the face and New Chat, then the switch at the foot of the column.
    private func shellRail(_ edge: HorizontalEdge) -> some View {
        @Bindable var model = model
        let link = model.connection.shown
        return ShellSideRail(edge: edge) {
            ShellPersonaButton(id: "assistant-sidebar-button", action: openSidebar)
            ShellCircleButton(symbol: Theme.Icon.compose, label: String(localized: "New Chat [Quick Actions ea4b29]"), id: "assistant-new", action: newConversation)
                .disabled(key == nil)
                .opacity(key == nil ? 0.4 : 1)
        } switcher: {
            ShellSwitch(tab: $model.selectedTab, axis: .vertical) { menuItems(link) }
        }
        .shellTopBarScaled()
        .connectionDetails($showLink)
        .modifier(ShellBarAtRest(page: 0)) // ShellMovingBar stands in while the strip slides
    }
    #endif

    private var shellRow: some View {
        @Bindable var model = model
        let link = model.connection.shown
        return HStack(alignment: .top) {
            ShellPersonaButton(id: "assistant-sidebar-button", action: openSidebar)
            Spacer(minLength: Theme.Space.s)
            ShellSwitch(tab: $model.selectedTab) { menuItems(link) }
            Spacer(minLength: Theme.Space.s)
            ShellCircleButton(symbol: Theme.Icon.compose, label: String(localized: "New Chat [Quick Actions ea4b29]"), id: "assistant-new", action: newConversation)
                .disabled(key == nil)
                .opacity(key == nil ? 0.4 : 1)
        }
        .shellTopBarScaled()
        .padding(.horizontal, Theme.Space.l)
        .padding(.top, Theme.Space.xxs)
        .frame(height: Self.height, alignment: .top)
        .background(alignment: .top) { BarFog(solid: Self.fogSolid, fade: Self.fade) }
        .connectionDetails($showLink)
        .modifier(ShellBarAtRest(page: 0)) // ShellMovingBar stands in while the strip slides
    }

    private var classicBar: some View {
        // On a phone's tabs the column's own button folds it, at its top: this one only brings it back, and is not there
        // while the column is out. Left out, not faded: a glass button made see-through (or slid away) while the split
        // moved its columns left the page taking no touches at all on iPhone Duo open (2026-11-10).
        let sidebarButtonShown = !(pageTabSwitch && sidebarIsColumn && sidebarShown)
        return HStack(alignment: .top) {
            if sidebarButtonShown {
                GlassEffectContainer {
                    Self.circle(sidebarIsColumn ? Theme.Icon.sidebar : Theme.Icon.sidebarMenu, label: String(localized: "Chat History [Shell Components 60636b]"), id: "assistant-sidebar-button") {
                        Haptics.tap()
                        openSidebar()
                    }
                }
                .transition(.identity)
            }
            Spacer(minLength: 8)
            if !pageTabSwitch { face }
            Spacer(minLength: 8)
            GlassEffectContainer {
                Self.circle(Theme.Icon.compose, label: String(localized: "New Chat [Quick Actions ea4b29]"), id: "assistant-new") {
                    Haptics.tap()
                    newConversation()
                }
                .disabled(key == nil)
            }
        }
        .padding(.horizontal, Theme.Space.l)
        #if os(macOS)
        // In the title bar's row, past the traffic lights when the bar is at the window's edge (#830); the fog under
        // it stays the whole width.
        .padding(.leading, max(0, titleRowLeading - Theme.Space.l))
        #endif
        .padding(.top, Self.topInset)
        #if os(iOS)
        // Clear of the round buttons on both sides, whichever are there: over New Chat it took its taps in a narrow column.
        .overlay(alignment: .top) {
            if pageTabSwitch { pageSwitch.padding(.top, Self.topInset).padding(.horizontal, Self.sideRoom) }
        }
        #endif
        .frame(height: Self.height, alignment: .top)
        .background(alignment: .top) {
            // Solid down to the bar's bottom edge, fading out below it (fading under the pill let the messages show
            // through the name).
            BarFog(solid: Self.fogSolid, fade: Self.fade)
        }
    }

    #if os(iOS)
    /// What the switch leaves free on each side of the bar: a round button and the bar's margin.
    private static let sideRoom: CGFloat = Theme.Size.tapTarget + Theme.Space.l + Theme.Space.s

    /// ChatGPT's Chat | Work at the top of the page, in the face's place, centered whatever the buttons beside it
    /// (#2623): tapping Assistant while it is selected opens the face's menu, as in the shell. Beside it, what the face's
    /// pill said while the hub is out of reach (under it, several managers' switcher covered it); a copy's confirmation
    /// is a toast here (`copy`).
    private var pageSwitch: some View {
        @Bindable var model = model
        let link = model.connection.shown
        return HStack(spacing: Theme.Space.s) {
            ViewThatFits(in: .horizontal) {
                ShellSwitch(tab: $model.selectedTab) { menuItems(link) }
                    .dynamicTypeSize(...Theme.Shell.topBarLargestTypeSize)
                ShellSwitch(tab: $model.selectedTab) { menuItems(link) }
                    .dynamicTypeSize(...Theme.Shell.compactSwitchLargestTypeSize)
            }
            if link != .online { ShellLinkPill(phase: link).transition(.opacity) }
        }
        .animation(Theme.Motion.standard, value: link)
        .connectionDetails($showLink)
    }
    #endif

    /// The bar's round icon button; also the sidebar header's search button when the sidebar is the split's column.
    static func circle(_ symbol: String, label: String, id: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ThemedIcon(symbol)
                .font(Theme.Font.heading)
                .dynamicTypeSize(...DynamicTypeSize.xxLarge)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .surface(interactive: true, in: .circle)
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
    }

    /// The face over the name pill; both are the menu's button. A copy's confirmation covers the pill for a moment
    /// (beside the menu, not on it: anything attached to a Menu becomes part of its button and its label).
    private var face: some View {
        ZStack(alignment: .bottom) {
            menu
            if let hint { hintPill(hint) }
        }
        .animation(Theme.Motion.standard, value: hint)
        .task(id: hint) { await clearHint() }
    }

    /// A copy's confirmation, in a pill of its own.
    private func hintPill(_ hint: String) -> some View {
        HStack(spacing: Theme.Space.xs) {
            ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.positive)
            Text(hint).lineLimit(1)
        }
        .font(Theme.Font.labelStrong)
        .padding(.horizontal, Theme.Space.m)
        .scaledFrame(height: Theme.Size.icon)
        .fixedSize()
        .background(.regularMaterial, in: .capsule)
        .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5))
        .allowsHitTesting(false)
        .transition(.opacity)
    }

    /// A multi-line closure in the modifier chain slows the release build's type-check (docs/lessons/ios.md): a method instead.
    private func clearHint() async {
        guard hint != nil else { return }
        try? await Task.sleep(for: .seconds(3))
        hint = nil
    }

    private var menu: some View {
        let link = model.connection.shown
        return Menu { menuItems(link) } label: {
            VStack(spacing: -7) {
                TopBarDot(key: key, machineID: machineID)
                    .shadow(color: Theme.persona.opacity(0.3), radius: 8, y: 3)
                ZStack {
                    if link != .online {
                        ConnectionLabel(phase: link, compact: true).transition(.opacity)
                    } else {
                        Text(name).font(Theme.Font.labelStrong).lineLimit(1).transition(.opacity)
                    }
                }
                .padding(.horizontal, Theme.Space.m)
                .scaledFrame(height: Theme.Size.icon)
                .scaledFrame(maxWidth: Theme.Size.box200)
                .fixedSize() // as wide as the name, at most 200
                // Not the glass `surface`: inside a Menu's label the glass spreads over the whole label (face included).
                .background(.regularMaterial, in: .capsule)
                .overlay(Capsule().strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5))
                .animation(Theme.Motion.standard, value: link)
                .opacity(hint == nil ? 1 : 0)
            }
            .contentShape(.rect)
        }
        .menuStyle(.button)
        .buttonStyle(.plain)
        .accessibilityLabel(link != .online ? String(localized: "\(name), \(ConnectionLabel.text(link, hubRestarting: model.hubUpdate?.restarting == true))") : name)
        .accessibilityHint("Knowledge, details, copy directory and chat info")
        .accessibilityIdentifier("persona-avatar")
        .connectionDetails($showLink)
    }

    /// The pill's name: the assistant's on the main conversation and the new-conversation page, a side conversation's
    /// title.
    private var name: String {
        let own = ownName
        guard let key, key != model.assistantMain else { return own }
        let t = session?.title ?? ""
        return t.isEmpty || t == "Claude Code" ? own : t
    }

    /// Whose page it is: the current assistant's name on a hub with several (the face stays the persona's, picked in
    /// Appearance), the persona's with one; another assistant's conversation opened from elsewhere names its own.
    private var ownName: String {
        if let key, let other = model.otherAssistant(of: key) { return other.name }
        return model.assistantManager?.name ?? model.persona.name
    }

    /// The conversation's launcher (the next message uses it) or, on the new-conversation page, the default one.
    /// The model picked for the next message here, nil: the launcher's default.
    private var pickedModel: String? { key.flatMap { model.assistantModels[$0] } }

    private func modelID(_ m: AgentModelOption) -> String? { m.isDefault == true ? nil : m.id }

    /// One model of a launcher's submenu, checked when it is the next message's.
    private func modelButton(_ launcherName: String, _ m: AgentModelOption) -> some View {
        let chosen = launcherName == launcher && pickedModel == modelID(m)
        return Button { pick(launcherName, model: m.isDefault == true ? nil : m.id) } label: {
            if chosen { Label(m.title, systemImage: Theme.Icon.done) } else { Text(m.title) }
        }
    }

    /// What the launcher menu is built from (see `.id(launcherMenuID)`): the pick, whether the hub keeps a typed ID, and
    /// every launcher's models.
    private var launcherMenuID: [String] {
        [launcher, pickedModel ?? "", model.hubFeatures.contains(ModelID.feature) ? ModelID.feature : ""]
            + launchers.flatMap { l in [l.name] + (modelOptions[l.name] ?? []).map(\.id) }
    }

    /// The next message's model when it is one typed for `launcherName` that its list doesn't have: the field starts with it.
    private func typedPick(_ launcherName: String) -> String {
        guard launcherName == launcher, let p = pickedModel, !(modelOptions[launcherName] ?? []).contains(where: { $0.id == p }) else { return "" }
        return p
    }

    /// The launcher's agent takes a model ID typed by hand (#1644): Claude Code or Codex.
    static func takesTypedModel(_ l: Launcher) -> Bool {
        guard l.baseURL != "(traex)" else { return false }
        return LauncherModels.takesTypedModel(agent: l.agent.flatMap { $0.isEmpty ? nil : $0 } ?? "claude")
    }

    /// The launcher's submenu ends with "Other Model…": its agent takes a typed ID and the hub keeps one for the next
    /// message (`typed_models`); an older hub dropped an unlisted pick without a word.
    static func offersTypedModel(_ l: Launcher, features: Set<String>) -> Bool {
        features.contains(ModelID.feature) && takesTypedModel(l)
    }

    private func pick(_ launcher: String, model picked: String?) {
        guard let key else { return }
        model.assistantLaunchers[key] = launcher
        model.assistantModels[key] = picked
        Haptics.tap()
        let what = picked == nil ? launcher : "\(launcher) · \(picked ?? "")"
        model.toast = Toast(String(localized: "Using \(what) from the next message"), style: .success)
    }

    /// What the conversation runs with no pick of its own: Main Chat the launcher in Settings › Assistant, another its own.
    private var defaultLauncher: String {
        if let key, key != model.assistantMain, let l = session?.launcher, !l.isEmpty { return l }
        return model.assistantConfig?.launcher ?? "claude"
    }

    private func pickDefault() {
        guard let key else { return }
        model.assistantLaunchers[key] = nil
        model.assistantModels[key] = nil
        Haptics.tap()
        model.toast = Toast(String(localized: "Using the default, \(defaultLauncher), from the next message"), style: .success)
    }

    /// Each launcher's /model list; a failure just leaves the submenu without entries.
    private func loadModelOptions() async {
        let machine = model.hubRef(machineID)
        guard let api = model.api(machine.group), !launchers.isEmpty else { return }
        await withTaskGroup(of: (String, [AgentModelOption]?).self) { group in
            for l in launchers {
                group.addTask { (l.name, try? await api.launcherModels(machine: machine.id, launcher: l.name)) }
            }
            // An empty answer counts too (the last typed ID forgotten); a failed one keeps what was there.
            for await (name, options) in group {
                if let options { modelOptions[name] = options }
            }
        }
    }

    private var launcher: String { AssistantCopyText.launcher(key, in: model) }

    private var cwd: String { AssistantCopyText.cwd(key, in: model) }

    @ViewBuilder private func menuItems(_ link: ConnectionStatus.Phase) -> some View {
        if model.isMultiHub, let current = model.hub?.group {
            // Several groups of hubs: each has its own Persona (docs/protocol/multi-hub.md「Terminology」); pick whose.
            Section("hub") {
                Picker("hub", selection: Binding(get: { current }, set: { model.selectHub($0) })) {
                    ForEach(model.groupIDs, id: \.self) { g in
                        Label(model.groupName(g), systemImage: Theme.Icon.hubGroup).tag(g)
                    }
                }
            }
        }
        Section(menuTitle) {
            // The Agent SDK could not run here (#2455): it says so, and why.
            if session?.runtimeFallback?.isEmpty == false,
               let r = AssistantRuntime.status(runtime: session?.runtime, fallback: session?.runtimeFallback) {
                Button { showInfo = true } label: { Label(r, systemImage: Theme.Icon.error) }
                    .accessibilityIdentifier("assistant-runtime-fallback")
            }
            if link != .online {
                Button { showLink = true } label: { Label("Connection Details", systemImage: Theme.Icon.offline) }
            }
            Button(action: openKnowledge) { Label("Knowledge", systemImage: Theme.Icon.knowledge) }
            Button(action: openProfile) { Label("Details [Assistant Top Bar 1932da]", systemImage: Theme.Icon.person) }
            ScreenMenu() // a Mac's live screen, whether or not the model is operating it (#1343)
            if let main = model.assistantMain, key != main {
                Button {
                    withAnimation(Theme.Motion.standard) { model.assistantCurrent = main }
                    Haptics.tap()
                } label: { Label("Back to Main Chat", systemImage: Theme.Icon.home) }
            }
        }
        Section {
            Button { copy(cwd, String(localized: "Working Directory Copied")) } label: { Label("Copy Directory", systemImage: Theme.Icon.folder) }
            Button { copy(infoText, String(localized: "Chat Info Copied")) } label: { Label("Copy Info", systemImage: Theme.Icon.copy) }
            if let key {
                Button { copy(key.sessionID, String(localized: "Session ID Copied")) } label: { Label(String(localized: "Copy Session ID [Assistant Sidebar f24fb3]"), systemImage: Theme.Icon.id) }
                // What the chat last went through, structure only (ChatTrace): to paste into a report.
                Button { copy(ChatTrace.shared.text(key), String(localized: "Diagnostics copied")) } label: { Label(String(localized: "Copy Diagnostics"), systemImage: Theme.Icon.diagnostics) }
            }
        }
        if model.canCompact(key) || model.canStartNewMain(key) {
            Section { MainConversationMenu(key: key) } // Compact context, New Main Chat… (#1762)
        }
        if let key {
            Section {
                Menu {
                    Section("From the Next Message, Use") {
                        // The conversation's own launcher (for Main Chat the one in Settings › Assistant): no pick of this chat's.
                        Button { pickDefault() } label: {
                            if model.assistantLaunchers[key] == nil, pickedModel == nil { Label("Default: \(defaultLauncher)", systemImage: Theme.Icon.done) }
                            else { Text("Default: \(defaultLauncher)") }
                        }
                        ForEach(launchers) { l in
                            Menu {
                                Button { pick(l.name, model: nil) } label: {
                                    if l.name == launcher, pickedModel == nil { Label("Default Model", systemImage: Theme.Icon.done) } else { Text("Default Model") }
                                }
                                ForEach((modelOptions[l.name] ?? []).filter { $0.custom != true }) { m in
                                    modelButton(l.name, m)
                                }
                                let typed = (modelOptions[l.name] ?? []).filter { $0.custom == true }
                                if !typed.isEmpty {
                                    Section("Typed Before") { ForEach(typed) { m in modelButton(l.name, m) } }
                                }
                                if Self.offersTypedModel(l, features: model.hubFeatures) {
                                    let picked = l.name == launcher ? pickedModel : nil
                                    let other = picked.flatMap { p in (modelOptions[l.name] ?? []).contains { $0.id == p } ? nil : p }
                                    Section {
                                        Button { typingFor = l.name } label: {
                                            // A typed pick that isn't listed (yet) is checked here, by its ID.
                                            if let other { Label { Text("Other Model…"); Text(other) } icon: { ThemedIcon(Theme.Icon.done) } }
                                            else { Label("Other Model…", systemImage: Theme.Icon.keyboard) }
                                        }
                                        .accessibilityIdentifier("assistant-model-other-\(l.name)")
                                    }
                                }
                            } label: {
                                let title = [l.name, Fmt.model(l.model)].compactMap { $0 }.joined(separator: " · ")
                                if l.name == launcher, model.assistantLaunchers[key] != nil {
                                    Label { Text(title); Text("Just Once") } icon: { ThemedIcon(Theme.Icon.done) }
                                } else if l.name == launcher { Label(title, systemImage: Theme.Icon.done) } else { Text(title) }
                            }
                            // A nested menu keeps the items it was first built with: the list read after the bar
                            // came up, an ID typed or forgotten since, or another pick builds it again.
                            .id([launcher, pickedModel ?? ""] + (modelOptions[l.name] ?? []).map(\.id))
                        }
                    }
                } label: { Label("Launcher: \(launcher)", systemImage: Theme.Icon.model) }
                // This one too keeps the items it was first built with (its launchers' submenus among them): built
                // again when a list comes in or changes, or a pick does.
                .id(launcherMenuID)
                Button { showInfo = true } label: {
                    Label("Session Info", systemImage: Theme.Icon.info)
                }
                if session?.status.isActive == true {
                    Button { Task { await model.interrupt(key) } } label: { Label("Interrupt [Composer Buttons f9d193]", systemImage: Theme.Icon.interrupt) }
                }
            }
        }
    }

    /// The menu's heading: which conversation, and what it runs on.
    private var menuTitle: String {
        let title = baseMenuTitle
        guard model.isMultiHub, let g = model.hub?.group else { return title }
        return title + " · " + model.groupName(g)
    }

    private var baseMenuTitle: String {
        guard let key else { return String(localized: "New chat · \(launcher)") }

        let title = name == ownName && key == model.assistantMain ? String(localized: "Main Chat") : name
        return [title, launcher, Fmt.model(pickedModel ?? session?.model)].compactMap { $0 }.joined(separator: " · ")
    }

    private var infoText: String { AssistantCopyText.info(key, in: model) }

    private func copy(_ text: String, _ done: String) {
        UIPasteboard.general.string = text
        Haptics.success()
        // The face's pill is not there on a phone's tabs (the switch has its place): a toast says it, as the main
        // conversation's row does.
        if pageTabSwitch { model.toast = Toast(done, style: .success) } else { hint = done }
        AccessibilityNotification.Announcement(done).post()
    }
}

/// What Copy Info puts on the clipboard: enough for another agent (or the user at a terminal) to find this conversation.
/// The face's menu copies it, and on iPhone Duo open the main conversation's row (#2623).
nonisolated enum AssistantCopyText {
    /// The conversation's launcher (the next message uses it) or, with none (the new-conversation page), the default one.
    @MainActor static func launcher(_ key: SessionKey?, in model: AppModel) -> String {
        // The main conversation follows Settings › Assistant (the hub sends it there), side ones keep their own.
        if let key, key == model.assistantMain, let l = model.assistantLaunchers[key] ?? model.assistantConfig?.launcher { return l }
        if let key { return model.assistantLaunchers[key] ?? model.session(key)?.launcher ?? model.assistantConfig?.launcher ?? "claude" }
        return model.assistantConfig?.launcher ?? "claude"
    }

    /// Where the conversation runs.
    @MainActor static func cwd(_ key: SessionKey?, in model: AppModel) -> String {
        if let c = key.flatMap({ model.session($0) })?.cwd, !c.isEmpty { return c }
        if let c = model.assistantConfig?.cwd, !c.isEmpty { return c }
        return "~/.persona/assistant"
    }

    /// Copy Info's text for the conversation.
    @MainActor static func info(_ key: SessionKey?, in model: AppModel) -> String {
        let session = key.flatMap { model.session($0) }
        let machineID = model.assistantMachineID(key)
        return info(title: session.map { $0.title.isEmpty || $0.title == "Claude Code" ? nil : $0.title } ?? nil,
                    isMain: key != nil && key == model.assistantMain, sessionID: key?.sessionID,
                    machine: model.machine(model.hubRef(machineID))?.name, machineID: machineID, cwd: cwd(key, in: model),
                    launcher: launcher(key, in: model), model: Fmt.model(session?.model))
    }

    static func info(title: String?, isMain: Bool, sessionID: String?, machine: String?, machineID: String, cwd: String,
                     launcher: String, model: String?) -> String {
        var lines: [String] = []
        let name = [isMain ? String(localized: "Main Chat") : nil, title.map { String(localized: "「\($0)」", comment: "A chat title in quotes") }]
            .compactMap { $0 }.joined(separator: " ")
        lines.append(sessionID == nil ? String(localized: "Persona assistant (Claude on the hub)")
                     : name.isEmpty ? String(localized: "Persona assistant chat") : String(localized: "Persona assistant chat \(name)"))
        if let sessionID { lines.append(String(localized: "Session ID: \(sessionID)")) }
        let place = machine.map { $0 == machineID ? $0 : String(localized: "\($0) (\(machineID))") } ?? machineID
        lines.append(String(localized: "Machine: \(place)"))
        lines.append(String(localized: "Working directory: \(cwd)"))
        lines.append(model.map { String(localized: "Launcher: \(launcher) · Model: \($0)") } ?? String(localized: "Launcher: \(launcher)"))
        if let sessionID { lines.append(String(localized: "To continue on that machine: cd \(cwd) && claude --resume \(sessionID)")) }
        return lines.joined(separator: "\n")
    }
}

/// The face in the bar, in its own view: what it shows reads the machines, the session and its last item, all of which
/// every hub push replaces; read in the bar itself, each push rebuilt the bar and its menu (an open menu with it).
private struct TopBarDot: View {
    @Environment(AppModel.self) private var model
    @Environment(\.tabIsActive) private var inFront
    let key: SessionKey?
    let machineID: String

    var body: some View {
        PersonaDot(size: Theme.Size.avatarL, state: model.personaState(key, machineID: machineID, inFront: inFront))
    }
}

extension AppModel {
    /// The machine Persona's conversation `key` runs on; on the new-conversation page (nil) the assistant's.
    func assistantMachineID(_ key: SessionKey?) -> String {
        key?.machineID ?? assistantConfig?.machineID ?? hubMachineID
    }

    /// What the persona's face shows for Persona's conversation `key` (docs/apple/persona.md「States」): Offline when the hub or
    /// Persona's machine is out of reach, Waiting for you when the conversation waits on the user, Thinking / Working while a turn
    /// runs (thinking until it writes or calls a tool), else Idle. Read only by the face's own view (`TopBarDot`,
    /// `ShellPersonaButton`): every hub push changes what it reads. `inFront`: the page's `tabIsActive`; behind another
    /// section (the Mac keeps the page, `MacPageStack`) the machine is read as it is now, not followed push by push.
    func personaState(_ key: SessionKey?, machineID: String, inFront: Bool = true) -> PersonaState {
        #if DEBUG
        // `-showPersonaState idle|thinking|working|needs_you|offline`: hold the face in one state (screenshots).
        if let s = UserDefaults.standard.string(forKey: "showPersonaState").flatMap(PersonaState.init(rawValue:)) { return s }
        #endif
        // Updating… is the hub answering again: not Offline.
        let link = connection.shown
        if link == .waitingForNetwork || link == .connecting || link == .rejected || machine(hubRef(machineID), inFront: inFront)?.online == false { return .offline }
        guard let key, let session = session(key) else { return .idle }
        if session.state == .needsYou || !interactions(for: key).isEmpty { return .needsYou }
        guard session.state == .working else { return .idle }
        let last = chats[key]?.items.last
        return last == nil || last?.role == "user" || last?.kind == .thinking ? .thinking : .working
    }
}

/// `TopFog` behind a floating bar, as its background: from the top of the screen (under the status bar) down to `solid`
/// below the safe area's top, then fading out over `fade`, and across the whole width. On iPhone Duo's outer screen
/// the status bar stands at the side: a fog only as wide as the page stopped beside it, a hard edge (#283).
struct BarFog: View {
    var solid: CGFloat
    var fade: CGFloat

    var body: some View {
        GeometryReader { g in
            let inset = g.safeAreaInsets
            let solidHeight = inset.top + solid
            TopFog(solidHeight: solidHeight, fadeHeight: fade)
                .frame(width: g.size.width + inset.leading + inset.trailing, height: solidHeight + fade)
                .offset(x: -inset.leading, y: -inset.top)
        }
    }
}

/// The fog under a floating bar: the page's own color, blurred, solid for `solidHeight` and then fading out over
/// `fadeHeight` (the content scrolls under it and shows through only below the bar). Both layers fade along an eased
/// curve, so the end of the solid part leaves no line across the text; the blur fades out over the first half only, so
/// a line half under the fog dims rather than shows a smeared copy of itself (#2909).
struct TopFog: View {
    var solidHeight: CGFloat
    var fadeHeight: CGFloat

    var body: some View {
        let total = max(solidHeight + fadeHeight, 1)
        let solid = solidHeight / total
        let fade = fadeHeight / total
        ZStack {
            Rectangle().fill(.regularMaterial)
                .mask(Self.easedMask(from: solid, over: fade / 2))
            Theme.pageBackground.opacity(0.82)
                .mask(Self.easedMask(from: solid, over: fade))
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    /// Opaque down to `start`, then out to clear over `length` (unit points) along the eased curve the chat's footer fog
    /// shares (`EasedFade`).
    private static func easedMask(from start: CGFloat, over length: CGFloat) -> LinearGradient {
        LinearGradient(stops: EasedFade.stops(.black, from: start, over: length), startPoint: .top, endPoint: .bottom)
    }
}
