import PersonaKit
import PhotosUI
import SwiftUI

/// Start a session inside an existing space (a new herdr tab next to the other agents).
nonisolated struct SpacePreset {
    /// The machine id, or "group|id" for another group of hubs (`MachineRef.path`).
    var machineID: String
    var cwd: String
    var workspaceID: String
    var spaceName: String
    /// Workspace IDs belong to their host; nil keeps directory-only and legacy starts on the machine's default.
    var host: String? = nil

    static func destination(preset: Self?, workspace: HostWorkspace?) -> Self? {
        preset ?? workspace?.preset
    }

    /// The same directory can switch between joining a host workspace and offering an independent Git worktree.
    static func repositoryLookupKey(machineID: String, cwd: String, online: Bool, destination: Self?) -> [String] {
        [machineID, cwd, String(online)] + (destination.map { ["workspace", $0.workspaceID, $0.host ?? ""] } ?? ["directory"])
    }
}

/// Pick a machine, a project directory and a launcher (model), optionally with a first message.
struct NewChatView: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var initialPrompt = ""
    var preset: SpacePreset?
    var todoID: String? = nil
    /// A todo handed to a new Agent (TodoHandoffSheet): the todo's context block goes ahead of the first message, the
    /// session is named after the todo, and only the todo's hub's machines are offered.
    var contextBlock: String? = nil
    /// A message forwarded to a new Agent (#3807): its card over the first message, which carries it as `contextBlock`,
    /// and the pictures it had, attached.
    var forwarded: ContextRef? = nil
    var forwardedImages: [UIImage] = []
    var nameHint: String? = nil
    var onlyGroup: String? = nil
    var onStarted: (Session) -> Void

    @AppStorage("newChat.machine") private var machineID = ""
    @State private var cwd = ""
    @State private var launcher = ""
    @State private var unifiedModels = false
    @State private var defaultLauncher = ""
    /// Model Settings › New Agent's model: what runs with no pick when its launcher does.
    @State private var configuredModel = ""
    @State private var mode = ""   // "" = whatever the launcher uses
    @State private var effort = ""
    /// Claude Code's ultracode for this start (#1307): off each time the sheet opens and never saved, unlike the effort
    /// (no choice carries over, the server's rule); only an explicit on is sent.
    @State private var ultracode = false
    @State private var prompt = ""
    @State private var tabName = ""
    @State private var launchers: [Launcher] = []
    /// The models of the launcher that will run (`runLauncher`) and the IDs typed for it before (#1644). `pickedModel`
    /// "": the default; an ID the list doesn't have (typed) stays picked when the list comes.
    @State private var modelList = LauncherModelsModel()
    @State private var pickedModel = ""
    /// "Enter Model ID…" while the list couldn't be read.
    @State private var typingModel = false
    @State private var recent: [RecentDir] = []
    @State private var loading = false
    /// Machine whose saved defaults were applied; `.task` re-runs when a pushed picker pops, which must not clobber picks.
    @State private var defaultsFor = ""
    /// Machine the directories, launchers and picks on screen belong to (cleared for another one); "" before the first.
    @State private var shownFor = ""
    /// Group whose New Agent model (`unifiedModels`, `defaultLauncher`, `configuredModel`) is loaded; nil: none yet.
    /// Each group's hub has its own model configuration.
    @State private var purposeGroup: String?
    @State private var starting = false
    @State private var error: String?
    @State private var workspaceCatalog = WorkspaceCatalog()
    @State private var pickedWorkspace: HostWorkspace?
    @State private var childSource: HostWorkspace?
    /// The picked folder's repository, as its computer says (#6115); nil: not in one, or it cannot say.
    @State private var repo: WorktreeStatus?
    /// Start the agent in a new worktree of `repo`; off each time the sheet opens.
    @State private var inWorktree = false
    /// The worktree's branch as typed; "": the one the computer picks.
    @State private var worktreeBranch = ""
    /// Photos and files for the first message (#2321), and the pickers that add them.
    @State private var attachments = NewAgentAttachments()
    /// The forwarded pictures are attached once, not again each time the form appears.
    @State private var forwardedAttached = false
    @State private var photoPicks: [PhotosPickerItem] = []
    @State private var showPhotos = false
    @State private var showCamera = false
    @State private var showFiles = false
    @FocusState private var promptFocused: Bool
    /// Bumped by a card (#2134): First Message is scrolled to and focused.
    @State private var promptRequests = 0
    private static let promptRow = "newChat.firstMessage"
    /// The recent setup last tapped (#2134): marked while the form still holds what it filled in.
    @State private var pickedRecent: RecentSetup?
    /// A tapped card whose machine's launchers and directories are not in yet: filled in with them (`loadMachine`).
    @State private var pendingFill: RecentSetupCard.Fill?
    /// A model to pick once `loadModels` moves to its launcher's list, which starts each launcher over from the default.
    @State private var restoredModel: RestoredModel?
    #if DEBUG
    @State private var debugLaunchers = false
    @State private var debugAttached = false
    #endif

    /// `machineID` is what is remembered: the machine id, "group|id" for another group of hubs (`MachineRef.path`).
    private var ref: MachineRef { MachineRef(path: machineID) }
    private var machine: Machine? { model.machine(ref) }
    private var targetPreset: SpacePreset? { SpacePreset.destination(preset: preset, workspace: pickedWorkspace) }
    private var workspaceChoices: [HostWorkspace] {
        workspaceCatalog.workspaces.filter { $0.machineRef == ref && $0.source == "orca" }
    }

    /// The launcher that will run: the one picked, or Model Settings'.
    private var runLauncher: String { launcher.isEmpty && unifiedModels ? defaultLauncher : launcher }
    /// 「默认：jv_strong · glm-5」: what runs with no pick, as the machine's launchers say (#2455), else Model Settings'.
    private var defaultLabel: String {
        model.launcherDefaults[ref]?.label
            ?? LauncherDefault(provider: defaultLauncher.isEmpty ? nil : "launcher." + defaultLauncher, model: configuredModel).label
            ?? String(localized: "Use Model Configuration")
    }

    /// Why the launcher that will run is grayed out on this computer (#1643); nil when it is not.
    private var runBlock: LauncherBlock? { launchers.first(where: { $0.name == runLauncher })?.block }

    /// The launcher runs Codex: it starts with its first message (Codex makes the thread then) and the start waits for it.
    private var codex: Bool { launchers.first(where: { $0.name == runLauncher })?.isCodex == true }

    /// A Codex this computer opens only with its first message (an older computer); a newer one opens it waiting for
    /// one, sent later here or typed in its terminal (docs/integrations/codex.md「Starting without a first message」).
    private var codexNeedsFirst: Bool { launchers.first(where: { $0.name == runLauncher })?.needsFirstMessage == true }

    /// The list for `runLauncher`; nil: none to pick (not loaded, an older hub).
    private var models: LauncherModels? { modelList.list }
    private var modelsLoading: Bool { modelList.loading }
    private var modelsError: String? { modelList.error }

    private var modelChoice: LauncherModelChoice? {
        models.map { LauncherModelChoice(list: $0, picked: pickedModel, configured: unifiedModels && runLauncher == defaultLauncher ? configuredModel : "") }
    }

    /// The list couldn't be read, but the launcher runs Claude Code or Codex: a model ID can still be typed (#1644).
    private var typesWithoutList: Bool { modelsError != nil && LauncherModels.takesTypedModel(agent: startAgent) }

    /// The `model` the start sends: the pick (from the list or typed), or an ID typed while the list couldn't be read.
    private var sentModel: String? {
        if let c = modelChoice { return c.sent }
        return typesWithoutList && ModelID.isValid(pickedModel) ? pickedModel : nil
    }

    /// The efforts the model that will run takes (all of them when that is not known).
    private var effortOptions: [SessionModes.Option] {
        guard let efforts = modelChoice?.efforts else { return SessionModes.startEffortLevels }
        return SessionModes.startEffortLevels.filter { efforts.contains($0.id) }
    }

    /// What the launcher that will run starts: its model list's agent, else the launcher's; Claude Code by default.
    private var startAgent: String {
        if let a = models?.agent, !a.isEmpty { return a }
        guard let l = launchers.first(where: { $0.name == runLauncher }) else { return "claude" }
        if l.baseURL == "(traex)" { return "traex" }
        return l.agent.flatMap { $0.isEmpty ? nil : $0 } ?? "claude"
    }

    /// The ultracode row (#1307): Claude Code whose effort can be set here, on a hub that has the switch.
    private var ultracodeShown: Bool {
        !codex && !effortOptions.isEmpty && Ultracode.startOffered(features: model.features(ref.group), agent: startAgent)
    }

    /// Why this computer can't start with it (its Claude Code is known to be too old); nil when it can.
    private var ultracodeBlocked: String? { ultracodeShown ? Ultracode.startBlocked(claudeVersion: machine?.claudeVersion) : nil }

    private var promptHint: String {
        if codexNeedsFirst { return String(localized: "What should Codex do? (Required: Codex starts a session only after the first message)") }
        if codex { return String(localized: "What should Codex do? (Optional: you can also send it later, here or in its terminal)") }
        return String(localized: "What should Claude do? (Optional)")
    }

    /// Nothing missing for Start Chat, and the machine's own launchers and directories are in.
    private var ready: Bool {
        machine?.online == true && defaultsFor == machineID && !cwd.isEmpty && (!launcher.isEmpty || unifiedModels)
            && !codexNeedsMessage && attachmentsHold == nil
    }

    private var codexNeedsMessage: Bool {
        codexNeedsFirst && contextBlock == nil && prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    /// What the attachments hold the start back for (a Codex launcher, uploads still going); nil: nothing.
    private var attachmentsHold: String? {
        FirstMessage.hold(codex: codex, attached: !attachments.isEmpty, uploading: attachments.uploading)
    }

    /// Why Start Chat is greyed out, the first thing missing in `ready`'s order; nil when it is ready (or still loading).
    private var notReadyReason: String? {
        guard !ready, !loading, !starting else { return nil }
        // A space's sheet hides the machine and directory rows: say what is wrong, not what to pick.
        if machine == nil {
            if preset != nil { return String(localized: "Can't find the computer this space is on") }
            return machinesShown.isEmpty ? String(localized: "No computers available yet") : String(localized: "Choose a computer first")
        }
        if machine?.online != true {
            if preset != nil || !machinesShown.contains(where: \.online) { return String(localized: "This computer is offline") }
            return String(localized: "This computer is offline. Choose one that's online.")
        }
        // Until this machine's launchers and directories are in, cwd and launcher are not its own yet.
        guard defaultsFor == machineID else { return nil }
        if cwd.isEmpty { return String(localized: "Choose a directory first") }
        if launcher.isEmpty && !unifiedModels { return launchers.isEmpty ? String(localized: "This computer has no launchers") : String(localized: "Choose a launcher first") }
        if codexNeedsMessage { return String(localized: "Codex needs a first message") }
        return attachmentsHold
    }

    private var startingLabel: String {
        let name = machine?.name ?? ""
        return codex ? String(localized: "Starting Codex on \(name)…") : String(localized: "Starting on \(name)…")
    }

    @ViewBuilder private var promptField: some View {
        #if os(macOS)
        // A grouped Mac form shows a titled field as label + trailing value: the prompt took the right half.
        TextField(promptHint, text: $prompt, prompt: Text(promptHint), axis: .vertical)
            .labelsHidden()
            .lineLimit(3...10)
        #else
        TextField(promptHint, text: $prompt, axis: .vertical)
            .lineLimit(3...10)
        #endif
    }

    var body: some View {
        NavigationStack {
            // Only to bring First Message on screen for a card (#2134); the form inside keeps its indentation.
            ScrollViewReader { proxy in
            Form {
                if let preset {
                    Section {
                        Label(preset.spaceName, systemImage: Theme.Icon.spaceFilled)
                        Text(Fmt.path(preset.cwd, home: machine?.home)).font(Theme.Font.code).foregroundStyle(.secondary)
                    } header: { Text("Join Space") } footer: {
                        Text(String(localized: "workspace.joinHelp", defaultValue: "Opens a new tab in the selected workspace."))
                    }
                } else {
                if !recentCards.isEmpty {
                    Section {
                        ForEach(Array(recentCards.enumerated()), id: \.element.card.setup) { i, c in
                            RecentSetupRow(card: c.card, machine: c.machine, picked: showsPicked(c.card.setup),
                                           pick: { pickRecent(c.card) },
                                           remove: { recents.remove(c.card.setup) },
                                           clear: { recents.clear() })
                                .accessibilityIdentifier("newChat.recent.\(i)")
                        }
                    } header: { Text("Recent") }
                }
                Section {
                    ScrollView(.horizontal, showsIndicators: false) {
                        HStack(spacing: Theme.Space.m) {
                            ForEach(machinesShown, id: \.ref) { m in machineCard(m) }
                        }
                        .padding(.vertical, Theme.Space.xs)
                    }
                    .listRowInsets(EdgeInsets(top: Theme.Space.s, leading: Theme.Space.page, bottom: Theme.Space.s, trailing: Theme.Space.page))
                } header: { Text("Machines") }

                if !workspaceChoices.isEmpty {
                    Section {
                        NavigationLink {
                            WorkspacePicker(workspaces: workspaceChoices, selectedID: pickedWorkspace?.sourceID, allowDirectory: true) { picked in
                                pickedWorkspace = picked
                                if let picked { cwd = picked.cwd }
                            }
                        } label: {
                            LabeledContent(String(localized: "workspace.target", defaultValue: "Worktree"),
                                           value: pickedWorkspace?.title ?? String(localized: "workspace.useDirectory", defaultValue: "Choose by Folder"))
                        }
                        .accessibilityIdentifier("newChat.workspace")
                        if let pickedWorkspace {
                            Button { childSource = pickedWorkspace } label: {
                                Label(String(localized: "workspace.newChild", defaultValue: "New Child Worktree…"), systemImage: Theme.Icon.newWorktree)
                            }
                            .accessibilityIdentifier("newChat.newChild")
                        }
                    }
                }
                if pickedWorkspace == nil {
                Section {
                    NavigationLink {
                        DirectoryBrowser(ref: ref, start: cwd.isEmpty ? "" : cwd) { picked in cwd = picked }
                    } label: {
                        HStack {
                            ThemedIcon(Theme.Icon.folderFilled).foregroundStyle(Theme.accent)
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text(cwd.isEmpty ? String(localized: "Choose Directory") : (cwd as NSString).lastPathComponent).font(Theme.Font.heading)
                                if !cwd.isEmpty {
                                    Text(Fmt.path(cwd, home: machine?.home)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).truncationMode(.head)
                                }
                            }
                        }
                    }
                    .accessibilityIdentifier("newChat.directory")
                    if !recent.isEmpty {
                        ForEach(recent.prefix(6)) { r in
                            Button {
                                cwd = r.path
                                Haptics.tap()
                            } label: {
                                HStack {
                                    ThemedIcon(cwd == r.path ? Theme.Icon.successFilled : Theme.Icon.recent)
                                        .foregroundStyle(cwd == r.path ? Theme.accent : .secondary)
                                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                        Text(r.name).foregroundStyle(Color.primary)
                                        Text(Fmt.path((r.path as NSString).deletingLastPathComponent, home: machine?.home))
                                            .font(Theme.Font.label).foregroundStyle(Color.secondary).lineLimit(1).truncationMode(.head)
                                    }
                                    Spacer()
                                    Text(Fmt.relative(r.lastUsed)).font(Theme.Font.label).foregroundStyle(Color.secondary)
                                }
                            }
                        }
                    }
                } header: { Text("Folder [Task Views 52daa7]") }
                if let repo, !repo.path.isEmpty {
                    NewAgentWorktreeSection(repo: repo, on: $inWorktree, branch: $worktreeBranch,
                                            suggested: AgentWorktree.branch(nameHint: worktreeNameHint), home: machine?.home)
                }
                }
                }

                Section {
                    NavigationLink {
                        launcherPicker
                    } label: {
                        HStack {
                            ThemedIcon(Theme.Icon.model).foregroundStyle(Theme.accent)
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                HStack(spacing: Theme.Space.sm) {
                                    Text(launcher.isEmpty ? (unifiedModels ? defaultLabel : String(localized: "Choose Launcher")) : launcher).font(Theme.Font.heading.monospaced()).lineLimit(1)
                                    // A pick over the default is for this start only.
                                    if unifiedModels, !launcher.isEmpty { Chip(text: String(localized: "Just Once")) }
                                }
                                if let l = launchers.first(where: { $0.name == launcher }), let m = l.model, !m.isEmpty {
                                    Text(m).font(Theme.Font.label).foregroundStyle(.secondary)
                                }
                                // Picked anyway (Try Anyway), or what Default runs: still said, the start is not held back.
                                if let runBlock { LauncherBlockLine(block: runBlock) }
                            }
                            Spacer()
                            if loading { ProgressView().controlSize(.small) }
                        }
                    }
                    .accessibilityIdentifier("newChat.launcher")
                    modelRow
                } header: { Text(models?.models.isEmpty == false || modelChoice?.canPick == true ? String(localized: "Launcher & Model (This Time Only)") : String(localized: "Launcher (This Time Only)")) } footer: {
                    let about = unifiedModels ? String(localized: "Uses Model Configuration › New Agent by default. A choice here applies to this session only and doesn't change the global default.")
                        : String(localized: "Claude Code / Codex launchers from this machine. It runs in a new tab in the terminal engine.")
                    Text(modelChoice?.canPick == true ? String(localized: "\(about) Models are the same as what /model shows in the terminal.") : about)
                }

                if !codex { // Claude Code's modes; a Codex keeps its own
                    Section {
                        Picker(selection: $mode) {
                            Text("Use Launcher's").tag("")
                            ForEach(SessionModes.permissionModes) { o in Label(o.title, systemImage: o.symbol).tag(o.id) }
                        } label: { Label { Text("Permission Mode") } icon: { ThemedIcon(Theme.Icon.permissions).foregroundStyle(Theme.accent) } }
                        if !effortOptions.isEmpty {
                            Picker(selection: $effort) {
                                Text("Use Launcher's").tag("")
                                ForEach(effortOptions) { o in Label(o.title, systemImage: o.symbol).tag(o.id) }
                            } label: { Label { Text("Thinking Effort") } icon: { ThemedIcon(Theme.Icon.effort).foregroundStyle(Theme.accent) } }
                        }
                        if ultracodeShown {
                            UltracodeStartRow(on: $ultracode, blocked: ultracodeBlocked, group: ref.group)
                        }
                    } header: { Text("Permissions & Thinking") } footer: {
                        Text(SessionModes.mode(mode)?.detail
                             ?? (effortOptions.isEmpty ? String(localized: "This model's thinking effort can't be changed. You can change the permission mode anytime in the chat.")
                                                    : String(localized: "You can change the permission mode and thinking effort anytime in the chat.")))
                    }
                    .pickerStyle(.menu)
                    .tint(Theme.accent)
                }

                Section {
                    if let forwarded {
                        QuoteCard(heading: forwarded.quoteHeading, icon: Theme.Icon.forwardMessage, meta: ForwardMessage.meta(forwarded),
                                  summary: forwarded.forwardSummary, lines: QuoteBlock.forwardLines, tint: Theme.accent, ink: .secondary) {}
                            .accessibilityIdentifier("newChat.forward")
                    }
                    promptField
                        .focused($promptFocused)
                        .accessibilityIdentifier("newChat.prompt")
                        .id(Self.promptRow)
                    // A Codex launcher takes no attachments: no menu, only those picked before to take out (#2321).
                    NewAgentAttachmentRows(attachments: attachments, adds: !codex, photos: { showPhotos = true },
                                           camera: CameraPicker.isAvailable ? { showCamera = true } : nil,
                                           files: { showFiles = true })
                } header: { Text("First Message") } footer: {
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        if codex && !attachments.isEmpty {
                            Text(FirstMessage.codexRefusal).accessibilityIdentifier("newChat.codexNote")
                        }
                        if let note = attachments.note {
                            Text(note).accessibilityIdentifier("newChat.attachNote")
                        }
                    }
                }

                Section {
                    TextField(String(localized: "Leave empty to name it from the first message"), text: $tabName)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.done)
                        .accessibilityIdentifier("newChat.tabName")
                } header: { Text("Tab Name (Optional)") } footer: {
                    Text(String(localized: "workspace.tabNameHelp", defaultValue: "Optional name for the new session tab. If empty, it is named from the first message."))
                }

            }
            .themedPage()
            .navigationTitle("New Chat [Quick Actions ea4b29]")
            .navigationBarTitleDisplayMode(.inline)
            .pageToolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        attachments.cancelUploads()
                        dismiss()
                    }
                }
                #if os(macOS)
                // A Mac sheet's primary button sits beside Cancel and answers ⏎ (docs/apple/mac.md「Mapping from iOS」).
                ToolbarItem(placement: .confirmationAction) {
                    Button(starting ? startingLabel : String(localized: "Start Chat")) { Task { await start() } }
                        .lineLimit(1)
                        .disabled(starting || loading || !ready)
                }
                #endif
            }
            .safeAreaInset(edge: .bottom) {
                VStack(spacing: Theme.Space.s) {
                // Next to the button, not as the form's last section: below First Message and under this button it went
                // unseen, the button just turned back to Start Chat (and a retry started a second agent).
                if let error {
                    Label(error, systemImage: Theme.Icon.errorFilled)
                        .font(Theme.Font.caption).foregroundStyle(Theme.danger).lineLimit(4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(Theme.Space.m)
                        .background(.regularMaterial, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                        .accessibilityIdentifier("newChat.error")
                } else if let notReadyReason {
                    // A grey button alone left the user checking every row for what was missing. On the error's card:
                    // the form scrolls under this inset, and bare text mixed with the form's (#2677).
                    Label(notReadyReason, systemImage: Theme.Icon.info)
                        .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(Theme.Space.m)
                        .background(.regularMaterial, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                        .accessibilityIdentifier("newChat.notReady")
                }
                #if os(iOS) || os(visionOS)
                Button {
                    Task { await start() }
                } label: {
                    HStack(spacing: Theme.Space.s) {
                        if starting { ProgressView().tint(.white) }
                        Text(starting ? startingLabel : String(localized: "Start Chat")).font(Theme.Font.heading)
                            .lineLimit(1).minimumScaleFactor(0.8)
                    }
                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton(prominent: true)
                .controlSize(.large)
                .disabled(starting || loading || !ready)
                #endif
                }
                .padding(.horizontal, Theme.Space.xl)
                .padding(.bottom, Theme.Space.s)
            }
            .task(id: machineID) { await loadMachine() }
            .task(id: machineID) { await workspaceCatalog.refresh([ref.group]) { model.api($0) } }
            // A provider added from a grayed-out launcher's help (Add Provider): this computer's list again.
            .onChange(of: model.launchersRevision) { _, _ in Task { await loadMachine() } }
            #if DEBUG
            // `-openLauncherPicker YES`: the launcher list open once this computer's launchers are in, for screenshots.
            .navigationDestination(isPresented: $debugLaunchers) { launcherPicker }
            .onChange(of: defaultsFor) { _, now in
                if !now.isEmpty, UserDefaults.standard.bool(forKey: "openLauncherPicker") { debugLaunchers = true }
            }
            #endif
            // The Claude quota under ultracode's row (#1307).
            .task(id: ultracodeShown) { if ultracodeShown { await UsageWindowsStore.shared.loadIfOld(model) } }
            .task(id: "\(machineID)|\(runLauncher)|\(machine?.online == true)|\(defaultsFor)") { await loadModels() }
            .task(id: SpacePreset.repositoryLookupKey(machineID: machineID, cwd: cwd, online: machine?.online == true,
                                                      destination: targetPreset)) { await loadRepo() }
            .sheet(isPresented: $typingModel) {
                TypedModelSheet(initial: pickedModel, note: String(localized: "Used for this start only. Once it runs, it's listed under Typed Before.")) { id in
                    pickedModel = id
                }
            }
            .onChange(of: effortOptions.map(\.id)) { _, ids in
                if !effort.isEmpty, !ids.contains(effort) { effort = "" } // the picked model does not take it
            }
            .onAppear {
                if prompt.isEmpty { prompt = initialPrompt }
                if !forwardedAttached {
                    forwardedAttached = true
                    for img in forwardedImages.prefix(MessagePhotos.limit) {
                        if let p = MessagePhotos.payload(img) { attachments.images.append(p) }
                    }
                }
                if let preset { machineID = preset.machineID; cwd = preset.cwd } else { pickDefaultMachine() }
            }
            .onChange(of: model.machines) { _, _ in if preset == nil { pickDefaultMachine() } }
            // A file is on the computer it was uploaded to (#2321): another one picked takes the files out.
            .onChange(of: machineID) { _, now in
                attachments.computerChanged(to: now) { model.machine(MachineRef(path: $0))?.name }
            }
            .photosPicker(isPresented: $showPhotos, selection: $photoPicks, maxSelectionCount: MessagePhotos.limit,
                          matching: .images, preferredItemEncoding: .current)
            .onChange(of: photoPicks) { _, items in
                guard !items.isEmpty else { return }
                Task {
                    await attachments.load(items, model: model)
                    photoPicks = []
                }
            }
            .fileUploadPicker(isPresented: $showFiles) { urls in attachments.upload(urls, to: ref, model: model) }
            .fullScreenCover(isPresented: $showCamera) {
                CameraPicker { img in attachments.add(img, model: model) }.ignoresSafeArea()
            }
            #if DEBUG && (os(iOS) || os(visionOS))
            // `-openNewAgentAttachments photo,build.log`: once this computer's launchers are in, the made-up photo
            // attached and a made-up 3 MB file uploading for every other name, for screenshots and UI tests (#2321).
            .onChange(of: defaultsFor) { _, now in
                guard !now.isEmpty, !debugAttached, let list = UserDefaults.standard.string(forKey: "openNewAgentAttachments") else { return }
                debugAttached = true
                for name in list.split(separator: ",").map(String.init) {
                    if name == "photo" {
                        attachments.add(ChatView.debugPhoto, model: model)
                        continue
                    }
                    let url = FileManager.default.temporaryDirectory.appending(path: name)
                    try? Data(count: 3 << 20).write(to: url)
                    attachments.upload([url], to: ref, model: model)
                }
            }
            // `-showNewAgentPrompt YES`: once this computer's launchers are in, First Message scrolled into view and
            // focused, for screenshots of its hint (#4961).
            .onChange(of: defaultsFor) { _, now in
                if !now.isEmpty, UserDefaults.standard.bool(forKey: "showNewAgentPrompt") { promptRequests += 1 }
            }
            #endif
            .interactiveDismissDisabled(starting)
            // A list makes only the rows on screen: First Message is scrolled into view before it can take the focus.
            .onChange(of: promptRequests) { _, _ in
                withAnimation(Theme.Motion.layout) { proxy.scrollTo(Self.promptRow, anchor: .center) }
                Task {
                    try? await Task.sleep(for: .milliseconds(350))
                    promptFocused = true
                }
            }
            }
        }
        .sheet(item: $childSource) { source in
            NewWorktreeSheet(group: SpaceGroup(id: source.key, machineID: source.machineID, spaceID: source.sourceID, group: source.group,
                                              name: source.title, index: source.index ?? 0, sessions: []), workspace: source) { made in
                pickedWorkspace = HostWorkspace(machineID: source.machineID, source: source.source, sourceID: made.workspaceID,
                                                title: made.branch, cwd: made.path, parentSourceID: made.parentWorkspaceID, group: source.group)
                cwd = made.path
                childSource = nil
            }
        }
        #if os(macOS)
        // On the stack, not its root: with the minimum size on the form, pushing Launcher / Folder shrank a sheet opened
        // from Agents' New Session to the pushed list's own height, zero (#303). Also gives the grouped form to sheets
        // that miss macLook's (Knowledge › Start Agent).
        .macSheet()
        #endif
    }

    private var launcherPicker: some View {
        LauncherPicker(launchers: launchers, selection: $launcher, allowDefault: unifiedModels, defaultLabel: defaultLabel,
                       defaultBlock: unifiedModels ? launchers.first(where: { $0.name == defaultLauncher })?.block : nil,
                       // The provider wizard saves to the hub-level group: on another group's computer it would land elsewhere.
                       addsProvider: ref.group == (model.hub?.group ?? ""))
    }

    /// The machines offered: this hub's when a todo is handed over (`onlyGroup`), else all of them.
    private var machinesShown: [Machine] { Self.offered(model.machines, onlyGroup: onlyGroup) }

    /// Not one waiting to be confirmed (the hub refuses it, 409 machine_pending): it is not the user's yet.
    static func offered(_ machines: [Machine], onlyGroup: String?) -> [Machine] {
        machines.filter { m in !m.isPending && (onlyGroup.map { m.group == $0 } ?? true) }
    }

    /// The remembered machine belongs to another hub than the one the todo is on.
    private var machineOutsideGroup: Bool { onlyGroup.map { machine?.group != $0 } ?? false }

    /// The setups that started an Agent here before (#2134), one store for the app.
    private var recents: RecentSetupsStore { .shared }

    /// The Recent section: setups on the machines offered here (this hub's for a todo), at most three.
    private var recentCards: [(card: RecentSetupCard, machine: Machine)] {
        let shown = machinesShown
        return recents.cards(shown.map { .init(id: $0.ref.path, online: $0.online) }).compactMap { c in
            shown.first { $0.ref.path == c.setup.machine }.map { (RecentSetupCard(setup: c.setup, online: c.online), $0) }
        }
    }

    /// A card's setup in every field and the first message focused; it never starts (Start stays the user's).
    private func pickRecent(_ card: RecentSetupCard) {
        guard let fill = card.fill, model.machine(MachineRef(path: fill.machine))?.online == true else { return }
        Haptics.tap()
        pickedRecent = card.setup
        if fill.machine == machineID, defaultsFor == machineID {
            apply(fill)
        } else {
            // Another machine (or this one still loading): filled in once its launchers and directories are in.
            pendingFill = fill
            machineID = fill.machine
        }
        promptRequests += 1
    }

    /// A card's choices, on its machine with its launchers in: a launcher it no longer has is replaced as a saved one
    /// would be, and the model goes only with the launcher it was picked for.
    private func apply(_ fill: RecentSetupCard.Fill) {
        pendingFill = nil
        pickedWorkspace = nil
        cwd = fill.cwd
        launcher = fill.launcher
        if let m = fill.mode { mode = m }
        if let e = fill.effort { effort = e }
        if !launchers.contains(where: { $0.name == launcher }), !(unifiedModels && launcher.isEmpty) {
            launcher = unifiedModels ? "" : LauncherChoices.initial(saved: nil, in: launchers)
        }
        restore(model: launcher == fill.launcher ? fill.model : "")
    }

    /// Picks a model for the launcher that will run: now when its list is the one on screen, else once `loadModels`
    /// moves to it (moving to another launcher's list starts over from the default).
    private func restore(model id: String) {
        let key = LauncherModelsModel.key(ref, runLauncher)
        if modelList.key == key {
            pickedModel = id
            restoredModel = nil
        } else {
            restoredModel = RestoredModel(key: key, id: id)
        }
    }

    /// The card tapped last, while every field still holds what it filled in.
    private func showsPicked(_ s: RecentSetup) -> Bool {
        guard pickedRecent == s, pendingFill == nil, machineID == s.machine, cwd == s.cwd, launcher == s.launcher,
              (restoredModel?.id ?? pickedModel) == s.model else { return false }
        return s.agentType != "claude" || (mode == s.mode && effort == s.effort)
    }

    /// The setup a start records (#2134): the fields as they are, the model the start sends ("" for the default).
    private func recentSetup(agent: String) -> RecentSetup {
        let claude = agent == "claude"
        return RecentSetup(machine: machineID, cwd: cwd, launcher: launcher, model: sentModel ?? "", agentType: agent,
                           mode: claude ? mode : "", effort: claude ? effort : "", at: "")
    }

    private func record(_ setup: RecentSetup) {
        var s = setup
        s.at = RecentSetups.at(.now)
        recents.record(s)
    }

    /// Remember the last machine, but fall back to the first online one (machines may arrive after the sheet opens).
    private func pickDefaultMachine() {
        if machine == nil || machine?.online == false || machine?.isPending == true || machineOutsideGroup,
           let m = machinesShown.first(where: \.online) ?? machinesShown.first {
            machineID = m.ref.path
        }
    }

    private func machineCard(_ m: Machine) -> some View {
        let selected = m.ref.path == machineID
        return Button {
            Haptics.tap()
            pendingFill = nil
            machineID = m.ref.path
        } label: {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                HStack {
                    ThemedIcon(m.symbol).font(Theme.Font.title)
                    Spacer()
                    Circle().fill(m.online ? Theme.positive : Color.secondary.opacity(0.4)).scaledFrame(width: Theme.Size.dot, height: Theme.Size.dot)
                }
                Text(m.name).font(Theme.Font.secondaryStrong).lineLimit(1)
                // Several groups of hubs: which group the machine is in, where the host name was.
                Text(m.online ? (m.groupLabel ?? m.hostname ?? String(localized: "Online")) : String(localized: "Offline [Machine Entity be1b4f]")).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            .scaledFrame(width: Theme.Size.box120, alignment: .leading)
            .padding(Theme.Space.m)
            .background(selected ? Theme.accent.opacity(0.14) : Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).stroke(selected ? Theme.accent : .clear, lineWidth: 1.5))
            .opacity(m.online ? 1 : 0.55)
        }
        .buttonStyle(.plain)
        .disabled(!m.online)
        .accessibilityIdentifier("newChat.machine.\(m.ref.path)")
    }

    /// The model purpose a new Agent inherits.
    /// Nil when the hub is older and has no model configuration (404);
    /// every other error propagates to the caller.
    private func purposeForNewChat(_ api: APIClient) async throws -> ModelPurpose? {
        do {
            return try await api.modelPurpose("agent.new").purpose
        } catch let e as APIError where e.status == 404 {
            return nil
        }
    }

    private func loadMachine() async {
        let id = machineID
        // Another machine: the last one's directories and launchers go now, not when this one's hub answers. Until then
        // they were offered under this machine's card, and stayed there when its hub could not be reached.
        if shownFor != id {
            if !shownFor.isEmpty { forgetLastMachine() }
            shownFor = id
        }
        guard !id.isEmpty, let api = model.api(ref.group), machine?.online == true else { loading = false; return }
        let group = ref.group
        // Still the load the sheet is waiting for: not one for a machine picked before (its task is cancelled, but an
        // answer can be in already), nor one cut short by a pushed page (it runs again when the page pops).
        func current() -> Bool { !Task.isCancelled && machineID == id }
        loading = true
        defer { if current() { loading = false } }
        error = nil
        do {
            // The three requests are independent; start all of them together,
            // then apply the purpose result before launchers and dirs finish.
            async let p: ModelPurpose? = purposeForNewChat(api)
            // Read again each time, not the app's cached list: a launcher's health here (#1643) changes behind it (a
            // start that fails blocks it, one that works clears it), and the web's dialog reads it fresh too.
            async let l = model.launchersForStart(ref)
            async let d = api.dirs(machine: ref.id, path: "")
            let purpose = try await p
            guard current() else { return }
            purposeGroup = group
            if let purpose {
                defaultLauncher = purpose.effective.provider.map { provider in
                    provider.hasPrefix("launcher.")
                        ? String(provider.dropFirst("launcher.".count)) : provider
                } ?? ""
                configuredModel = purpose.effective.model ?? ""
                unifiedModels = true
            } else {
                // 404: an older hub without model configuration.
                unifiedModels = false
            }
            let (ls, dirs) = try await (l, d)
            guard current() else { return }
            launchers = ls
            recent = dirs.recent ?? []
            let firstLoad = defaultsFor != machineID
            if firstLoad {
                defaultsFor = machineID
                let saved = UserDefaults.standard.string(forKey: "launcher.\(machineID)")
                mode = UserDefaults.standard.string(forKey: "mode.\(machineID)") ?? ""
                effort = UserDefaults.standard.string(forKey: "effort.\(machineID)") ?? ""
                if unifiedModels { launcher = ""; effort = "" }
                else { launcher = LauncherChoices.initial(saved: saved, in: ls) }
                if let preset = targetPreset { cwd = preset.cwd } else {
                    cwd = UserDefaults.standard.string(forKey: "cwd.\(machineID)") ?? recent.first?.path ?? ""
                }
            }
            if !ls.contains(where: { $0.name == launcher }), !(unifiedModels && launcher.isEmpty) {
                launcher = unifiedModels ? "" : LauncherChoices.initial(saved: nil, in: ls)
            }
            // A card tapped for this machine wins over its saved defaults (#2134); without one, the model is the one its
            // latest recent setup with this launcher started (none: the default).
            if let fill = pendingFill, fill.machine == machineID {
                apply(fill)
            } else if firstLoad {
                restore(model: recents.model(machine: machineID, launcher: launcher))
            }
        } catch {
            guard current(), !error.isCancellation else { return }
            // No answer at all (out of reach, timed out): said, not left as an empty sheet with a grey button.
            self.error = error.shown ?? String(localized: "Can't reach this computer's hub. Try again later.")
        }
    }

    /// What belonged to the machine picked before: its directories, launchers, the picks made from them and their
    /// saved defaults (applied again for the next machine), and its hub's New Agent model when the new machine is in
    /// another group of hubs.
    private func forgetLastMachine() {
        pickedWorkspace = nil
        recent = []
        launchers = []
        launcher = ""
        mode = ""
        effort = ""
        restoredModel = nil
        defaultsFor = ""
        loading = false
        error = nil
        if preset == nil { cwd = "" }
        if purposeGroup != ref.group {
            unifiedModels = false
            defaultLauncher = ""
            configuredModel = ""
            purposeGroup = nil
        }
    }

    /// New Agent's Model row: loading, the pick (into the list), why it cannot be picked, or what went wrong (and then
    /// "Enter Model ID…" where one can be typed anyway).
    @ViewBuilder private var modelRow: some View {
        if let modelsError {
            HStack(spacing: Theme.Space.m) {
                ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.cautionText)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text("Couldn't Load Models").font(Theme.Font.heading).lineLimit(1)
                    Text(modelsError).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(3)
                }
                Spacer(minLength: Theme.Space.s)
                Button { Task { await loadModels(refresh: true) } } label: { ThemedIcon(Theme.Icon.retry) }
                    .buttonStyle(.borderless)
                    .accessibilityLabel("Retry [Components b8784c]")
            }
            .accessibilityIdentifier("newChat.model.error")
            if typesWithoutList {
                HStack(spacing: Theme.Space.m) {
                    Button { typingModel = true } label: {
                        modelLabel(title: pickedModel.isEmpty ? String(localized: "Enter Model ID…") : SessionModes.gptModelName(pickedModel) ?? pickedModel,
                                   detail: pickedModel.isEmpty ? String(localized: "Without the list, a model can still be used by its ID") : String(localized: "Typed model ID"),
                                   trailing: pickedModel.isEmpty ? Theme.Icon.keyboard : nil)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("newChat.model.enter")
                    // A typed pick can be dropped again: the start goes with the launcher's default.
                    if !pickedModel.isEmpty {
                        Button { pickedModel = "" } label: { ThemedIcon(Theme.Icon.clear).foregroundStyle(.secondary) }
                            .buttonStyle(.borderless)
                            .accessibilityLabel("Use Default Model")
                            .accessibilityIdentifier("newChat.model.clearTyped")
                    }
                }
            }
        } else if let c = modelChoice, c.canPick {
            NavigationLink {
                LauncherModelPicker(choice: c, picked: $pickedModel, refresh: { await loadModels(refresh: true) }, offersTyping: true,
                                    forget: { id in await modelList.forget(id, machine: ref, launcher: runLauncher) },
                                    forgetError: modelList.forgetError,
                                    defaultFooter: c.configured.isEmpty ? String(localized: "No model chosen: runs with the launcher's own settings.")
                                        : String(localized: "No model chosen: runs with the launcher's own settings and Model Configuration."))
            } label: {
                modelLabel(title: c.selected?.displayName ?? c.typed.map { SessionModes.gptModelName($0) ?? $0 } ?? String(localized: "Default Model"),
                           detail: c.detail, trailing: nil)
            }
            .accessibilityIdentifier("newChat.model")
        } else if let c = modelChoice, c.showsLocked, let locked = c.list.locked {
            modelLabel(title: String(localized: "Default Model"), detail: locked, trailing: Theme.Icon.locked)
                .accessibilityElement(children: .combine) // one row, read with why, like the pickable one
                .accessibilityIdentifier("newChat.model.locked")
        } else if modelsLoading {
            modelLabel(title: String(localized: "Model [Assistant View c98e11]"), detail: String(localized: "Loading models for \(runLauncher)…"), trailing: nil)
                .accessibilityIdentifier("newChat.model.loading")
        }
    }

    private func modelLabel(title: String, detail: String?, trailing: String?) -> some View {
        HStack {
            ThemedIcon(Theme.Icon.ai).foregroundStyle(Theme.accent)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(title).font(Theme.Font.heading).lineLimit(1)
                if let detail {
                    Text(detail).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2).truncationMode(.middle)
                }
            }
            Spacer()
            if modelsLoading { ProgressView().controlSize(.small) }
            if let trailing { ThemedIcon(trailing).foregroundStyle(.secondary) }
        }
    }

    /// The model list of the launcher that will run. Again only when that launcher (or machine) changed, or refresh:
    /// `.task` runs again when a pushed list pops. A typed pick stays when the list comes (it isn't in it, #1644); another
    /// launcher or machine starts over from the default.
    private func loadModels(refresh: Bool = false) async {
        let key = LauncherModelsModel.key(ref, runLauncher)
        if !refresh, modelList.key == key, modelList.list != nil || modelList.loading { return }
        if modelList.key != key { pickedModel = "" }
        // A recent setup's model, or the machine's (#2134): picked before the list comes, so the efforts offered are its.
        if let r = restoredModel, r.key == key {
            pickedModel = r.id
            restoredModel = nil
        }
        modelList.select(ref, launcher: runLauncher)
        // Only once this machine's launchers are in: before that `launcher` is still the last machine's.
        guard defaultsFor == machineID, launchers.contains(where: { $0.name == runLauncher }), machine?.online == true else { return }
        restoredModel = nil // one left was for a launcher no longer picked
        await modelList.load(ref, launcher: runLauncher, refresh: refresh)
    }

    /// What names the worktree's branch: the todo's name, else the first message, else the folder.
    private var worktreeNameHint: String {
        AgentWorktree.nameHint(prompt: nameHint ?? prompt, cwd: cwd)
    }

    /// Asks the computer whether the folder is in a Git repository, a moment after it stops changing.
    private func loadRepo() async {
        repo = nil
        let dir = cwd
        guard targetPreset == nil, !dir.isEmpty, machine?.online == true, let api = model.api(ref.group) else { return }
        try? await Task.sleep(for: .milliseconds(400))
        guard !Task.isCancelled else { return }
        let st = await api.worktreeStatus(machine: ref.id, cwd: dir)
        guard !Task.isCancelled, targetPreset == nil, dir == cwd else { return }
        repo = st
    }

    private func start() async {
        guard let api = model.api(ref.group) else {
            error = String(localized: "Can't reach this computer's hub. Try again later.")
            return
        }
        starting = true
        error = nil
        defer { starting = false }
        let setup = recentSetup(agent: codex ? "codex" : startAgent)
        // In a worktree of its own (#6115): made first; a failure says why, and nothing starts.
        var dir = cwd
        if inWorktree, targetPreset == nil, repo?.path.isEmpty == false {
            do {
                dir = try await api.addAgentWorktree(machine: ref.id, cwd: cwd, nameHint: worktreeNameHint, branch: worktreeBranch).path
            } catch {
                self.error = String(localized: "Couldn't create the worktree: \(error.shown ?? error.localizedDescription) [Agent Worktree]")
                Haptics.error()
                return
            }
        }
        do {
            let p = prompt.trimmingCharacters(in: .whitespacesAndNewlines)
            let name = tabName.trimmingCharacters(in: .whitespacesAndNewlines)
            // The first message: a todo's context block (when a todo is handed over), then what was typed.
            let outgoing = contextBlock.map { p.isEmpty ? $0 : "\($0)\n\n\(p)" } ?? p
            if codex {
                // Codex picks its thread's id once it has the first message: it goes with the start, which answers
                // with that thread (one session, already working on it). With none, the start answers with Codex
                // waiting for it (Session.awaitingFirstMessage).
                let s = try await api.start(.init(machineID: ref.id, cwd: dir, launcher: launcher, prompt: contextBlock == nil ? prompt : outgoing,
                                                  workspaceID: targetPreset?.workspaceID, host: targetPreset?.host, model: sentModel, tabName: name.isEmpty ? nil : name,
                                                  nameHint: nameHint ?? (p.isEmpty ? nil : p), todoID: todoID))
                UserDefaults.standard.set(launcher, forKey: "launcher.\(machineID)")
                UserDefaults.standard.set(cwd, forKey: "cwd.\(machineID)")
                record(setup)
                Haptics.success()
                model.adopt(s)
                onStarted(s)
                return
            }
            // The hub answers at once and starts claude in the background; the first message goes out as an ordinary
            // message (on screen right away, typed in once claude is ready).
            let s = try await api.start(.init(machineID: ref.id, cwd: dir, launcher: launcher, prompt: nil,
                                              workspaceID: targetPreset?.workspaceID, host: targetPreset?.host, permissionMode: mode.isEmpty ? nil : mode,
                                              effort: effort.isEmpty ? nil : effort, model: sentModel, tabName: name.isEmpty ? nil : name,
                                              async: true, nameHint: nameHint ?? (p.isEmpty ? nil : p), todoID: todoID,
                                              ultracode: sendsUltracode ? true : nil))
            if !unifiedModels {
                UserDefaults.standard.set(launcher, forKey: "launcher.\(machineID)")
                UserDefaults.standard.set(effort, forKey: "effort.\(machineID)")
            }
            UserDefaults.standard.set(mode, forKey: "mode.\(machineID)")
            UserDefaults.standard.set(cwd, forKey: "cwd.\(machineID)")
            record(setup)
            Haptics.success()
            model.adopt(s)
            // The photos and the uploaded files' paths go with it (#2321).
            let photos = attachments.images.map(\.1)
            let first = FirstMessage.text(prompt: prompt, contextBlock: contextBlock, paths: attachments.paths, photos: photos.count)
            if !first.isEmpty { model.sendNow(s.key, text: first, images: photos) }
            onStarted(s)
        } catch {
            if sendsUltracode, Ultracode.refused(error) {
                // The hub won't start it with ultracode here (an old node, a CLI or engine that can't take it): asking
                // again would get the same answer, so the switch goes off and Start Chat starts it without.
                ultracode = false
                self.error = String(localized: "Couldn't start with ultracode: \(Ultracode.errorText(error)). Retry starts without it")
            } else {
                self.error = error.shown
            }
            Haptics.error()
        }
    }

    /// The start asks for ultracode: offered, not blocked, switched on.
    private var sendsUltracode: Bool { ultracodeShown && ultracodeBlocked == nil && ultracode }

    /// A model to pick for one launcher's list ("path|launcher", `LauncherModelsModel.key`).
    private struct RestoredModel: Equatable {
        var key: String
        var id: String
    }
}

/// New Agent's launchers. One grayed out on this computer (`Launcher.block`, #1643) stays listed with why; choosing it
/// shows how to fix it instead of picking it, and the help's Try Anyway picks it after all.
struct LauncherPicker: View {
    var launchers: [Launcher]
    @Binding var selection: String
    var allowDefault = false
    /// The first row's words: 「默认：…」 (what runs with no pick).
    var defaultLabel = String(localized: "Use Model Configuration")
    /// Why the launcher Default runs is grayed out on this computer; nil when it is not. Default stays pickable: it is
    /// Model Settings' choice, said under it.
    var defaultBlock: LauncherBlock? = nil
    /// The help offers Add Provider: the computer is in the hub group the provider wizard saves to.
    var addsProvider = true
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    /// The grayed-out launcher whose help is open.
    @State private var help: Launcher?
    /// Try Anyway in the help: picked once the help has closed, then the list closes too.
    @State private var pickAfterHelp: String?

    var body: some View {
        List {
            if allowDefault {
                Button {
                    selection = ""
                    dismiss()
                } label: {
                    HStack {
                        if let defaultBlock {
                            // Why under the words, in the Label's title column: lined up with the title and the separator.
                            Label {
                                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                                    Text(defaultLabel).lineLimit(1)
                                    LauncherBlockLine(block: defaultBlock)
                                }
                            } icon: {
                                ThemedIcon(Theme.Icon.model)
                            }
                        } else {
                            Label(defaultLabel, systemImage: Theme.Icon.model).lineLimit(1)
                        }
                        Spacer()
                        if selection.isEmpty { ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent).fontWeight(.semibold) }
                    }
                }
                .accessibilityIdentifier("launcherPicker.default")
            }
            ForEach(LauncherChoices.shown(launchers, query: query)) { l in
                if let block = l.block {
                    blockedRow(l, block)
                } else {
                    Button {
                        selection = l.name
                        Haptics.tap()
                        dismiss()
                    } label: {
                        HStack {
                            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                                Text(l.name).font(Theme.Font.heading.monospaced()).foregroundStyle(.primary)
                                chips(l, look: .neutral)
                            }
                            Spacer()
                            if l.name == selection { ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent).fontWeight(.semibold) }
                        }
                    }
                }
            }
        }
        .pageSearchable(text: $query, prompt: String(localized: "Function name or model"))
        .themedPage()
        .navigationTitle("Launcher")
        .sheet(item: $help, onDismiss: {
            guard let name = pickAfterHelp else { return }
            pickAfterHelp = nil
            selection = name
            Haptics.tap()
            dismiss()
        }) { l in
            LauncherBlockedHelp(launcher: l, block: l.block ?? LauncherBlock(kind: ""), addsProvider: addsProvider) {
                pickAfterHelp = l.name
                help = nil
            }
        }
        #if DEBUG
        .task { // `-showLauncherHelp <name>`: that launcher's help open, for screenshots
            if let name = UserDefaults.standard.string(forKey: "showLauncherHelp"), let l = launchers.first(where: { $0.name == name && $0.block != nil }) {
                try? await Task.sleep(for: .seconds(1))
                help = l
            }
        }
        #endif
    }

    /// Grayed out: the name dimmed, why under it, and a tap opens how to fix it.
    private func blockedRow(_ l: Launcher, _ block: LauncherBlock) -> some View {
        Button {
            help = l
            Haptics.tap()
        } label: {
            HStack {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    // Gray, not the row tint's lighter shade: it reads as unavailable, like a disabled row.
                    Text(l.name).font(Theme.Font.heading.monospaced()).foregroundStyle(Color.secondary)
                    chips(l, look: .outline)
                    LauncherBlockLine(block: block)
                }
                Spacer()
                if l.name == selection { ThemedIcon(Theme.Icon.done).foregroundStyle(Color.secondary).fontWeight(.semibold) }
                ThemedIcon(Theme.Icon.info).foregroundStyle(Color.secondary)
            }
        }
        .accessibilityHint(Text("Shows how to fix it"))
        .accessibilityIdentifier("launcherPicker.blocked.\(l.name)")
    }

    @ViewBuilder private func chips(_ l: Launcher, look: Chip.Look) -> some View {
        HStack(spacing: Theme.Space.sm) {
            if allowDefault, l.name == selection { Chip(text: String(localized: "Just Once"), look: .tint(Theme.accent)) }
            if l.isCodex { Chip(text: "Codex", look: look) }
            if let m = l.model, !m.isEmpty { Chip(text: m, look: look) }
            if let host = l.baseURL.flatMap({ URL(string: $0)?.host() }) {
                Text(host).font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1)
            }
        }
    }
}

/// Why a launcher is grayed out, in one line under it (`LauncherBlock.reason`).
struct LauncherBlockLine: View {
    var block: LauncherBlock

    var body: some View {
        // Not a Label: in a list row its icon takes a column of its own, far from the words.
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.xs) {
            ThemedIcon(Theme.Icon.error)
            Text(block.reason).lineLimit(2)
        }
        .font(Theme.Font.label)
        .foregroundStyle(Theme.cautionText)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("launcher.blockReason")
    }
}

struct DirectoryBrowser: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    var ref: MachineRef
    var start: String
    var onPick: (String) -> Void
    @State private var list: DirList?
    @State private var path = ""
    @State private var draft = ""
    @State private var error: String?
    @State private var loading = false
    @State private var attempt = 0
    @FocusState private var editing: Bool

    var body: some View {
        List {
            if let list {
                Section {
                    HStack(spacing: Theme.Space.s) {
                        TextField(String(localized: "Enter a path"), text: $draft)
                            .font(Theme.Font.code)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                            .keyboardType(.asciiCapable)
                            .submitLabel(.go)
                            .focused($editing)
                            .onSubmit { go() }
                            .accessibilityIdentifier("dirBrowser.path")
                        if loading {
                            ProgressView()
                        } else if editing, DirectoryPath.resolve(draft, from: list.path) != list.path {
                            Button { go() } label: { ThemedIcon(Theme.Icon.enter) }
                                .accessibilityLabel("Go [New Chat View fa9607]")
                        }
                    }
                    Button {
                        onPick(list.path)
                        dismiss()
                    } label: {
                        Label("Use “\((list.path as NSString).lastPathComponent)”", systemImage: Theme.Icon.successFilled)
                            .font(Theme.Font.heading)
                            .lineLimit(1)
                    }
                } footer: { Text("Edit the path above and press Return to go there; ~ is your home folder") }

                if let error {
                    Section {
                        Label(error, systemImage: Theme.Icon.error)
                            .foregroundStyle(Theme.danger)
                            .accessibilityIdentifier("dirBrowser.error")
                    }
                }

                Section {
                    if let parent = list.parent, !parent.isEmpty {
                        Button { Task { await load(parent) } } label: { Label("Parent Folder", systemImage: Theme.Icon.parentFolder) }
                    }
                    ForEach(list.entries) { e in
                        Button { Task { await load(e.path) } } label: {
                            HStack {
                                ThemedIcon(e.isGit ? Theme.Icon.gitRepository : Theme.Icon.folder).foregroundStyle(e.isGit ? Theme.accent : .secondary)
                                Text(e.name).foregroundStyle(.primary)
                                Spacer()
                                if e.isGit { Chip(text: "git") }
                            }
                        }
                    }
                }
            } else if let error {
                Text(error).foregroundStyle(Theme.danger)
                // Nothing listed yet: the page has no row to tap and no path to edit, only this (#4315).
                if loading {
                    ProgressView().frame(maxWidth: .infinity)
                } else {
                    Button("Retry [Components b8784c]") { Task { await load(start) } }.lineLimit(1)
                        .accessibilityIdentifier("dirBrowser.retry")
                }
            } else {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
        .themedPage()
        .navigationTitle((path as NSString).lastPathComponent.isEmpty ? String(localized: "Folder [Task Views 52daa7]") : (path as NSString).lastPathComponent)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load(start) }
    }

    private func go() {
        guard let target = DirectoryPath.resolve(draft, from: list?.path ?? "") else { return }
        editing = false
        Task { await load(target) }
    }

    /// The last tap wins; a directory the hub can't list keeps the current one on screen and says why. A start that
    /// can't be listed (a saved directory gone or unreadable) falls back to the home directory, the reason above it.
    private func load(_ p: String) async {
        // No hub for this machine: said, not left as a spinner (#4315).
        guard let api = model.api(ref.group) else { error = String(localized: "Not connected to the hub [App Model 93d955]"); return }
        attempt += 1
        let mine = attempt
        loading = true
        defer { if mine == attempt { loading = false } }
        do {
            let l = try await api.dirs(machine: ref.id, path: p)
            guard mine == attempt else { return }
            withAnimation(Theme.Motion.standard) {
                list = l
                path = l.path
                draft = l.path
                error = nil
            }
        } catch {
            guard mine == attempt else { return }
            // The hub never answered (out of reach, timed out, cut off): there is no reason from it to show, and the home
            // folder would fail the same way. It showed only "Can't open" with an empty path when the hub was out of reach.
            let unreachable = !(error is APIError) && error.isQuiet
            let reason = unreachable ? String(localized: "Can't reach this computer's hub. Try again later.")
                : DirectoryPath.problem((error as? APIError)?.message ?? error.shown ?? "", path: p)
            if list == nil, !p.isEmpty, !unreachable {
                await load("")
                self.error = reason
                return
            }
            withAnimation(Theme.Motion.standard) {
                self.error = reason
                if let list { draft = list.path }
            }
        }
    }
}
