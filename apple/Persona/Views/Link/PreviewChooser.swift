#if os(iOS)
import SwiftUI

/// A session's dev servers to preview (#150, docs/web/preview.md "iPhone"): opened from the chat's ⋯ › Preview and from
/// the link card's subtitle. The project's `.claude/launch.json` entries come first, then the ports the session's Agents
/// listen on; a running one opens in the link card (`open`), a stopped one with a command offers Start…, which shows the
/// command and folder and asks first, then waits for its port and opens it.
struct PreviewChooser: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @Environment(\.dynamicTypeSize) private var typeSize
    let key: SessionKey
    var open: (PreviewOption) -> Void
    @State private var preview: PreviewModel
    @State private var confirming: PreviewOption?
    @State private var startTask: Task<Void, Never>?
    @State private var otherPort = false
    @State private var otherText = ""
    /// Opens tall, as the list it is: a drag down to half shows the chat again.
    @State private var detent: PresentationDetent = .large

    init(key: SessionKey, cwd: String, open: @escaping (PreviewOption) -> Void) {
        self.key = key
        self.open = open
        _preview = State(initialValue: PreviewModel(machine: key.machineID, cwd: cwd,
                                                    deps: .init(api: { AppModel.shared.api(key.group) })))
    }

    private var session: Session? { model.session(key) }
    private var machine: Machine? { model.machine(key.machineRef) }
    private var machineName: String { machine?.name ?? key.machineID }
    private var ports: [ListeningPort] { session?.ports ?? [] }
    private var options: [PreviewOption] { preview.options(ports: ports) }
    private var canStart: Bool { session?.workspaceID?.isEmpty == false }

    var body: some View {
        NavigationStack {
            content
                .navigationTitle(String(localized: "Preview"))
                .navigationSubtitle(machineName + " · " + Fmt.path(preview.cwd, home: machine?.home))
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button(role: .close) { dismiss() }
                            .accessibilityIdentifier("preview-close")
                    }
                }
        }
        .presentationDetents([.medium, .large], selection: $detent)
        .presentationDragIndicator(.visible)
        .task { await preview.load() }
        .onDisappear {
            startTask?.cancel()
            preview.cancelStart()
        }
        .onChange(of: preview.starting != nil) { _, started in if started { confirming = nil } }
        .sheet(item: $confirming) { o in
            PreviewStartSheet(option: o, machine: machineName, folder: PreviewLogic.folder(o.cwd, session: preview.cwd, home: machine?.home),
                              busy: preview.sending) { start(o) }
        }
        .alert("Open a Port", isPresented: $otherPort) {
            TextField(String(localized: "Port"), text: $otherText)
                .keyboardType(.numberPad)
                .accessibilityIdentifier("preview-other-port")
            Button("Cancel", role: .cancel) { otherText = "" }
            Button("Open") { openOther() }
        } message: {
            Text("A port on \(machineName), such as 3000.")
        }
    }

    @ViewBuilder private var content: some View {
        switch preview.phase {
        case .loading:
            VStack(spacing: Theme.Space.m) {
                ProgressView()
                Text("Looking for dev servers…").font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.pageBackground)
        case .old:
            EmptyStateView(symbol: Theme.Icon.info, title: String(localized: "Update This Computer to Preview"),
                           message: String(localized: "Persona on \(machineName) is too old for previews. Update it, then open Preview again."))
                .padding(Theme.Space.page)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Theme.pageBackground)
        case .failed(let error):
            EmptyStateView(symbol: Theme.Icon.error, title: String(localized: "Couldn't Read the Dev Servers"), message: error,
                           action: (title: String(localized: "Retry"), run: { Task { await preview.load() } }))
                .padding(Theme.Space.page)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .background(Theme.pageBackground)
        case .choose:
            list
        }
    }

    private var list: some View {
        let all = options
        let launch = all.filter { $0.from == .launch }
        let agents = all.filter { $0.from == .agent }
        return List {
            if all.isEmpty {
                Section { emptyState }
            }
            if !launch.isEmpty {
                Section {
                    ForEach(launch) { row($0) }
                } header: {
                    Text("Dev servers · .claude/launch.json")
                }
            }
            Section {
                ForEach(agents) { row($0) }
                Button { otherText = ""; otherPort = true } label: {
                    Label { Text("Another Port…").lineLimit(1) } icon: { IconTile(Theme.Icon.add, tint: Theme.accent) }
                }
                .accessibilityIdentifier("preview-other")
            } header: {
                if !agents.isEmpty { Text("Ports agents opened") }
            } footer: {
                problems
            }
        }
        .listStyle(.insetGrouped)
        .refreshable { await preview.load() }
    }

    @ViewBuilder private var emptyState: some View {
        let error = preview.servers?.error
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text(error == nil ? String(localized: "No Dev Servers Yet") : String(localized: "Can't Read .claude/launch.json"))
                .font(Theme.Font.heading)
            Text(error ?? String(localized: "Servers the agent starts show up here. To list your own, add .claude/launch.json to the project:"))
                .font(Theme.Font.secondary).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            if error == nil {
                Text(PreviewLogic.launchExample)
                    .font(Theme.Font.code).foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(Theme.Space.m)
                    .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                    .textSelection(.enabled)
                Button {
                    UIPasteboard.general.string = PreviewLogic.launchExample
                    Haptics.success()
                    model.toast = Toast(String(localized: "Example copied"), style: .success)
                } label: {
                    Label("Copy Example", systemImage: Theme.Icon.copy).lineLimit(1)
                }
                .themedButton()
            }
        }
        .padding(.vertical, Theme.Space.s)
    }

    /// One server: a running one opens on a tap; a stopped one with a command has Start…; one being started says what
    /// it waits for and can be cancelled; one with a url and no command is started by hand. At the accessibility sizes
    /// the row's end goes on a line under the name and port, which get more lines: beside them Start… was cut to its
    /// icon and the words to "…" (#5114).
    @ViewBuilder private func row(_ o: PreviewOption) -> some View {
        let starting = preview.starting?.option.id == o.id
        let large = typeSize.isAccessibilitySize
        let label = HStack(alignment: large ? .top : .center, spacing: Theme.Space.m) {
            IconTile(o.from == .agent ? Theme.Icon.agents : o.command == nil && !o.listening ? Theme.Icon.document : o.listening ? Theme.Icon.servedPage : Theme.Icon.server,
                     tint: o.from == .agent ? Theme.Hue.purple : o.listening ? Theme.Hue.green : Theme.Hue.gray)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(o.name).font(Theme.Font.body).foregroundStyle(Color.primary).lineLimit(large ? 2 : 1)
                Text(detail(o, starting: starting)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(large ? 4 : starting ? 2 : 1)
                if large { trailing(o, starting: starting) }
            }
            // the separator starts under the name, not under a Start… button's words
            .alignmentGuide(.listRowSeparatorLeading) { $0[.leading] }
            if !large {
                Spacer(minLength: Theme.Space.s)
                trailing(o, starting: starting)
            }
        }
        if o.listening {
            Button { Haptics.tap(); open(o) } label: { label.contentShape(.rect) }
                .buttonStyle(.plain)
                .accessibilityIdentifier("preview-server-\(o.name)")
                .accessibilityHint(String(localized: "Opens this page in the app"))
        } else {
            label.accessibilityElement(children: .contain).accessibilityIdentifier("preview-server-\(o.name)")
        }
    }

    /// `:5173 · Acme Notes`, `:8080 · Not running`, `:4000/guide · Not running`, `:6006 · node`; while it starts, what it
    /// waits for.
    private func detail(_ o: PreviewOption, starting: Bool) -> String {
        if starting { return String(localized: "Starting \(o.name)… waiting for something to answer on port \(String(o.port)).") }
        let at = ":\(o.port)" + (o.path == "/" ? "" : o.path)
        let what = o.listening ? (o.from == .agent ? o.process : o.title.flatMap { $0 == o.name ? nil : $0 }) : StateName.notRunning.text
        return what.map { "\(at) · \($0)" } ?? at
    }

    @ViewBuilder private func trailing(_ o: PreviewOption, starting: Bool) -> some View {
        if starting {
            HStack(spacing: Theme.Space.s) {
                ProgressView()
                Button("Cancel") { startTask?.cancel(); preview.cancelStart() }
                    .font(Theme.Font.secondaryMedium).lineLimit(1)
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("preview-start-cancel")
            }
        } else if o.listening {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(Theme.Icon.dot).font(Theme.Font.label).imageScale(.small).foregroundStyle(Theme.positive)
                    .accessibilityHidden(true)
                Text("Running").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.label).foregroundStyle(.tertiary).accessibilityHidden(true)
            }
        } else if o.command != nil && canStart {
            Button { confirming = o } label: {
                Label("Start…", systemImage: Theme.Icon.startServer).font(Theme.Font.secondaryMedium).lineLimit(1)
            }
            .themedButton()
            .disabled(preview.starting != nil)
            .accessibilityIdentifier("preview-start-\(o.name)")
        } else if o.command == nil {
            Text("Start it yourself").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
        }
    }

    @ViewBuilder private var problems: some View {
        let bad = preview.problems
        if !bad.isEmpty {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text("Entries in launch.json that can't be used").font(Theme.Font.captionStrong)
                ForEach(Array(bad.enumerated()), id: \.offset) { _, s in
                    Text(verbatim: "\(s.name.isEmpty ? String(localized: "(no name)") : s.name): \(Self.problem(s.problem ?? ""))")
                        .lineLimit(2)
                }
            }
            .font(Theme.Font.caption)
            .padding(.top, Theme.Space.s)
        }
    }

    static func problem(_ code: String) -> String {
        switch code {
        case "no_name": String(localized: "needs a name")
        case "no_port": String(localized: "needs a port")
        case "not_local": String(localized: "its url is another computer: a preview shows this one")
        case "no_run": String(localized: "needs runtimeExecutable or a local url")
        case "bad_env": String(localized: "an env name isn't valid")
        default: code
        }
    }

    private func start(_ o: PreviewOption) {
        guard let s = session, let workspace = s.workspaceID else { return }
        let kind = s.spaceKind ?? s.hostKind ?? ""
        startTask?.cancel()
        startTask = Task {
            let result = await preview.start(o, workspace: workspace, kind: kind) { [model, key] in model.session(key)?.ports ?? [] }
            switch result {
            case .ready(let now):
                Haptics.success()
                open(now)
            case .failed(let error):
                Haptics.error()
                model.toast = Toast(String(localized: "Couldn't start \(o.name): \(error)"), style: .error)
            case .slow(let o):
                model.toast = Toast(String(localized: "\(o.name) isn't answering on :\(String(o.port)) yet. Check its terminal tab."), style: .error)
            case .stopped:
                break
            }
        }
    }

    private func openOther() {
        let digits = otherText.filter(\.isNumber)
        otherText = ""
        guard let port = Int(digits), (1...65535).contains(port) else { return }
        open(PreviewOption(name: ":\(port)", port: port, path: "/", listening: true, from: .agent))
    }
}

/// Start…'s question: what runs where, before anything does (docs/web/preview.md "Servers"). The confirmation sheets'
/// layout (`DeleteAgentSheet`): title, what happens, the command in a well, the folder, Start over Cancel.
private struct PreviewStartSheet: View {
    @Environment(\.dismiss) private var dismiss
    var option: PreviewOption
    var machine: String
    var folder: String?
    var busy: Bool
    var start: () -> Void
    @State private var height: CGFloat = Theme.Size.box300

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Start \(option.name)?").font(Theme.Font.title)
            Text("This runs the command from the project's .claude/launch.json on \(machine), in a new terminal tab. Ctrl-C there stops it.")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            Text(PreviewLogic.commandLine(option))
                .font(Theme.Font.code)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(Theme.Space.m)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .textSelection(.enabled)
                .accessibilityIdentifier("preview-start-command")
            if let folder {
                Text("In \(folder)").font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(2).truncationMode(.head)
            }
            VStack(spacing: Theme.Space.m) {
                Button { Haptics.tap(); start() } label: {
                    Group {
                        if busy { ProgressView().tint(Theme.onSolid) } else { Text("Start [Preview]").lineLimit(1) }
                    }
                    .font(Theme.Font.heading)
                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton(prominent: true)
                .keyboardShortcut(.defaultAction)
                .disabled(busy)
                .accessibilityIdentifier("preview-start-confirm")
                Button { dismiss() } label: {
                    Text("Cancel").lineLimit(1).font(Theme.Font.heading).scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                }
                .themedButton()
                .keyboardShortcut(.cancelAction)
                .disabled(busy)
            }
            .controlSize(.large)
            .padding(.top, Theme.Space.s)
        }
        .padding(.horizontal, Theme.Space.xl)
        .padding(.top, Theme.Space.xxl)
        .padding(.bottom, Theme.Space.m)
        .fixedSize(horizontal: false, vertical: true)
        .onGeometryChange(for: CGFloat.self, of: { $0.size.height }) { height = $0 }
        .presentationDetents([.height(height)])
        .presentationDragIndicator(.visible)
        .interactiveDismissDisabled(busy)
    }
}
#endif
