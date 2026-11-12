import BeautifulUI
import SwiftUI

/// Persona's profile, opened from its avatar on the Persona tab (and from Settings › Assistant): who it is and where it runs, what
/// it will do on its own next, and everything that shapes it — its scheduled tasks, the history of runs, what it
/// remembers, what it is told about the user, and its launcher.
struct AssistantProfileView: View {
    @Environment(AppModel.self) private var model
    @State private var tasks: [APIClient.ScheduledTask]?
    @State private var runs: [APIClient.ScheduleRun]?
    @State private var memoryCount: Int?
    @State private var placeCount: Int?
    #if DEBUG
    @State private var debugPage: String?
    #endif

    private var machineID: String { model.assistantConfig?.machineID ?? model.hubMachineID }
    private var assistantCwd: String {
        if let cwd = model.assistantConfig?.cwd, !cwd.isEmpty { return cwd }
        return "~/.persona/assistant"
    }
    private var machineName: String { model.machine(model.hubRef(machineID))?.name ?? machineID }

    /// The machines the hub connects to, by name ("Server, Laptop…"), from the hub's own list.
    private var machinesLine: String {
        let names = model.machines.map(\.name)
        let list = names.joined(separator: String(localized: "、", comment: "Separator between machine names"))
        return names.isEmpty ? String(localized: "Computers connected to the hub (SSH)") : String(localized: "\(list) (SSH)")
    }

    var body: some View {
        Group { if AppStyle.current.usesBeautifulUI { cards } else { list } }
        .pageTitle("Persona")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
        .refreshable { await load() }
        #if DEBUG
        .onAppear {
            if debugPage == nil, let p = UserDefaults.standard.string(forKey: "openAssistantPage"),
               !["profile", "assistant", "avatars"].contains(p) { debugPage = p }
        }
        .navigationDestination(item: $debugPage) { p in
            switch p {
            case "routines", "routine", "newRoutine", "followups", "followup", "intervalTask", "cronTask", "bypassTask": AssistantRoutinesView()
            case "runs": AssistantRunsView()
            case "memory", "memoryFile", "memoryEdit": AssistantMemoryView()
            case "places", "place", "newPlace": SavedPlacesView()
            default: AssistantNotesView()
            }
        }
        #endif
    }

    private var list: some View {
        List {
            Section { header }
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
            if !upcoming.isEmpty {
                Section {
                    ForEach(upcoming) { u in AssistantUpcomingRow(item: u) }
                } header: {
                    Text("Up Next")
                }
            }
            Section {
                NavigationLink { AssistantRoutinesView() } label: {
                    row("Scheduled Tasks", "alarm", detail: tasks.map { t in
                        let on = t.filter(\.enabled).count
                        return on == 0 ? String(localized: "Off [Assistant Profile View 3cffa9]") : String(localized: "\(on) on [Assistant Profile View cfee38]")
                    })
                }
                NavigationLink { AssistantRunsView() } label: {
                    row("Run History", Theme.Icon.recent, detail: runs.flatMap(\.first).map { AssistantRunText.statusWord($0) + " · " + Fmt.relative($0.at) })
                }
                NavigationLink { TaskTrustView() } label: { row("Auto-Start Tasks", Theme.Icon.taskTrust, detail: nil) }
            } header: {
                Text("On Its Own")
            } footer: {
                Text("Starts work on schedule, follows up on what it promised you, and notifies your phone when it's done. Say “Give me a morning briefing every day at 9” or “Remind me in an hour” in a chat to add one here.")
            }
            Section {
                NavigationLink { AssistantNotesView() } label: {
                    row("About Me", "person.text.rectangle", detail: nil)
                }
                NavigationLink { AssistantMemoryView() } label: {
                    row("Memory [Assistant Profile View 7a6335]", "brain", detail: memoryCount.flatMap { $0 > 0 ? String(localized: "\($0) items [Assistant Profile View 29b8db]", comment: "Memory count") : nil })
                }
                NavigationLink { SavedPlacesView() } label: {
                    row("Saved Places", Theme.Icon.place, detail: placesDetail)
                }
            } header: {
                Text("Knowing You")
            } footer: {
                Text("“About Me” is what you tell Persona; “Memory” is what it notes down on its own in chats. You can edit both.")
            }
            if let quiet = quietTasks {
                Section {
                    Toggle(isOn: quiet) {
                        Label(Self.quietTitle, systemImage: Theme.Icon.notifications).lineLimit(1)
                    }
                    .accessibilityIdentifier("assistant-main-tasks-quiet")
                } header: {
                    Text("Task Notifications")
                } footer: {
                    Text(Self.quietFooter)
                }
            }
            if let folded = backgroundFolded {
                Section {
                    Toggle(isOn: folded) {
                        Label(Self.foldTitle, systemImage: Theme.Icon.collapseAll).lineLimit(1)
                    }
                    .accessibilityIdentifier("assistant-main-background-folded")
                } footer: {
                    Text(Self.foldFooter)
                }
            }
            if let own = ownTasks {
                Section {
                    Toggle(isOn: own) {
                        Label(Self.ownTasksTitle, systemImage: Theme.Icon.supervised).lineLimit(1).minimumScaleFactor(0.8)
                    }
                    .accessibilityIdentifier("assistant-main-supervises-own-tasks")
                } footer: {
                    Text(Self.ownTasksFooter)
                }
            }
            Section {
                NavigationLink { SkillsPage() } label: { row("Skills", Theme.Icon.skills, detail: nil) }
                LabeledContent("Runs On", value: machineName)
                LabeledContent("Working Directory", value: assistantCwd)
            } header: {
                Text("Settings")
            } footer: {
                Text("Each chat runs Claude in the background in this directory on \(machineName), outside herdr. Choose the model in Settings › Models › Main Chat; change it for a single chat from that chat's menu.")
            }
            Section {
                Label(machinesLine, systemImage: Theme.Icon.machines)
                Label("Agents on your computers: see status, read chats, send messages, start new ones", systemImage: "rectangle.stack.badge.person.crop")
                Label("Lark: messages, calendar, docs, meeting notes, tasks, mail", systemImage: "building.2")
                Label("iPhone: contacts, calendar, reminders, location, health, photos", systemImage: Theme.Icon.phone)
                Label("All the mail accounts you've added", systemImage: Theme.Icon.mail)
                Label(Self.reachApprovalLine, systemImage: Theme.Icon.notifications)
            } header: {
                Text("Has Access To")
            } footer: {
                Text(Self.reachApprovalFooter)
            }
        }
        .themedPage()
    }

    /// The BeautifulUI style: its list cards, a heading over each and a note under it.
    private var cards: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Theme.Space.xl) {
                header
                if !upcoming.isEmpty {
                    BUIListSection(String(localized: "Up Next")) {
                        ForEach(upcoming) { u in AssistantUpcomingRow(item: u) }
                    }
                }
                BUIListSection(String(localized: "On Its Own"), footer: String(localized: "Starts work on schedule, follows up on what it promised you, and notifies your phone when it's done. Say “Give me a morning briefing every day at 9” or “Remind me in an hour” in a chat to add one here.")) {
                    NavigationLink { AssistantRoutinesView() } label: {
                        buiRow("Scheduled Tasks", Theme.Icon.schedule, detail: tasks.map { t in
                            let on = t.filter(\.enabled).count
                            return on == 0 ? String(localized: "Off [Assistant Profile View 3cffa9]") : String(localized: "\(on) on [Assistant Profile View cfee38]")
                        })
                    }
                    NavigationLink { AssistantRunsView() } label: {
                        buiRow("Run History", Theme.Icon.recent, detail: runs.flatMap(\.first).map { AssistantRunText.statusWord($0) + " · " + Fmt.relative($0.at) })
                    }
                    NavigationLink { TaskTrustView() } label: { buiRow("Auto-Start Tasks", Theme.Icon.taskTrust, detail: nil) }
                }
                BUIListSection(String(localized: "Knowing You"), footer: String(localized: "“About Me” is what you tell Persona; “Memory” is what it notes down on its own in chats. You can edit both.")) {
                    NavigationLink { AssistantNotesView() } label: {
                        buiRow("About Me", "person.text.rectangle", detail: nil)
                    }
                    NavigationLink { AssistantMemoryView() } label: {
                        buiRow("Memory [Assistant Profile View 7a6335]", "brain", detail: memoryCount.flatMap { $0 > 0 ? String(localized: "\($0) items [Assistant Profile View 29b8db]", comment: "Memory count") : nil })
                    }
                    NavigationLink { SavedPlacesView() } label: {
                        buiRow("Saved Places", Theme.Icon.place, detail: placesDetail)
                    }
                }
                if let quiet = quietTasks {
                    BUIListSection(String(localized: "Task Notifications"), footer: Self.quietFooter) {
                        BUIListRow(Self.quietTitle) { IconTile(Theme.Icon.notifications, tint: Theme.accent) } trailing: {
                            Toggle(Self.quietTitle, isOn: quiet).labelsHidden()
                                .accessibilityIdentifier("assistant-main-tasks-quiet")
                        }
                    }
                }
                if let folded = backgroundFolded {
                    BUIListSection(footer: Self.foldFooter) {
                        BUIListRow(Self.foldTitle) { IconTile(Theme.Icon.collapseAll, tint: Theme.accent) } trailing: {
                            Toggle(Self.foldTitle, isOn: folded).labelsHidden()
                                .accessibilityIdentifier("assistant-main-background-folded")
                        }
                    }
                }
                if let own = ownTasks {
                    BUIListSection(footer: Self.ownTasksFooter) {
                        BUIListRow(Self.ownTasksTitle) { IconTile(Theme.Icon.supervised, tint: Theme.accent) } trailing: {
                            Toggle(Self.ownTasksTitle, isOn: own).labelsHidden()
                                .accessibilityIdentifier("assistant-main-supervises-own-tasks")
                        }
                    }
                }
                BUIListSection(String(localized: "Settings"), footer: String(localized: "Each chat runs Claude in the background in this directory on \(machineName), outside herdr. Choose the model in Settings › Models › Main Chat; change it for a single chat from that chat's menu.")) {
                    NavigationLink { SkillsPage() } label: { buiRow("Skills", Theme.Icon.skills, detail: nil) }
                    BUIListRow(String(localized: "Runs On"), value: machineName)
                    BUIListRow(String(localized: "Working Directory"), value: assistantCwd)
                }
                BUIListSection(String(localized: "Has Access To"), footer: Self.reachApprovalFooter) {
                    buiReach(machinesLine, Theme.Icon.machines)
                    buiReach(String(localized: "Agents on your computers: see status, read chats, send messages, start new ones"), "rectangle.stack.badge.person.crop")
                    buiReach(String(localized: "Lark: messages, calendar, docs, meeting notes, tasks, mail"), "building.2")
                    buiReach(String(localized: "iPhone: contacts, calendar, reminders, location, health, photos"), Theme.Icon.phone)
                    buiReach(String(localized: "All the mail accounts you've added"), Theme.Icon.mail)
                    buiReach(Self.reachApprovalLine, Theme.Icon.notifications)
                }
            }
            .padding(.horizontal, Theme.Space.l)
            .padding(.bottom, Theme.Space.xxl)
        }
        .themedGroupedPage()
    }

    private var header: some View {
        VStack(spacing: Theme.Space.m) {
            PersonaAvatar(size: 88, working: working)
                .shadow(color: Theme.persona.opacity(0.3), radius: 16, y: 6)
            Text(model.persona.name).font(Theme.Font.display).fontDesign(.rounded)
            Text("Your personal assistant · on \(machineName)")
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            HStack(spacing: Theme.Space.s) {
                let online = model.machine(model.hubRef(machineID))?.online ?? false
                Chip(text: online ? String(localized: "Online") : String(localized: "Offline [Machine Entity be1b4f]"), symbol: online ? Theme.Icon.successFilled : "moon.zzz", look: online ? .tint(Theme.positive) : .neutral)
                if working { Chip(text: StateName.working.text, symbol: Theme.Icon.more, look: .tint(Theme.accent)) }
                if let next = upcoming.first {
                    Chip(text: String(localized: "Next \(AssistantScheduleText.moment(next.at)) [Assistant Profile View 6f07f2]"), symbol: Theme.Icon.schedule)
                }
            }
        }
        .frame(maxWidth: .infinity)
        // Room above for the face's glow (radius 16): the list row clips what sticks out, as a hard square edge.
        .modifier(HeaderRoom())
    }

    /// The header's room: in the BeautifulUI style on the token grid, in every other style what it always had.
    private struct HeaderRoom: ViewModifier {
        @ViewBuilder func body(content: Content) -> some View {
            if AppStyle.current.usesBeautifulUI {
                content.padding(.top, Theme.Space.xl)
            } else {
                content.padding(.top, 28).padding(.bottom, Theme.Space.m)
            }
        }
    }

    /// Any assistant conversation running a turn right now.
    private var working: Bool {
        (model.sessionsByMachine[model.hubRef(machineID)] ?? []).contains { $0.assistant && $0.state == .working }
    }

    /// Tasks sent from the main chat notify only in the main chat (#1731); nil (no switch) from a hub without the setting.
    private var quietTasks: Binding<Bool>? {
        guard let on = model.assistantConfig?.mainTasksQuiet else { return nil }
        return Binding(get: { on }, set: { v in Task { await model.setMainTasksQuiet(v) } })
    }
    private static let quietTitle = String(localized: "Notify Only in Main Chat")
    private static let quietFooter = String(localized: "Tasks started from the main chat stay out of the Dynamic Island, notifications, and badges. Persona tells you in the main chat when they finish, need your answer, or fail. Only affects tasks started from now on.")

    /// The main conversation folds its background rounds (docs/apple/ios.md "Background rounds"); nil (no switch) from a
    /// hub without the setting.
    private var backgroundFolded: Binding<Bool>? {
        guard let on = model.assistantConfig?.mainBackgroundFolded else { return nil }
        return Binding(get: { on }, set: { v in Task { await model.setMainBackgroundFolded(v) } })
    }
    private static let foldTitle = String(localized: "Fold background rounds")
    private static let foldFooter = String(localized: "Rounds that another conversation, a scheduled task or a follow-up started in the main chat show as one line you can open. Rounds that ask or tell you something stay shown.")

    /// The main conversation follows up its own tasks instead of the background supervisor (#5083,
    /// docs/assistant/unified-entry.md "Background supervisor"); nil (no switch) from a hub without the setting.
    private var ownTasks: Binding<Bool>? {
        guard let on = model.assistantConfig?.mainSupervisesOwnTasks else { return nil }
        return Binding(get: { on }, set: { v in Task { await model.setMainSupervisesOwnTasks(v) } })
    }
    private static let ownTasksTitle = String(localized: "Main Chat Supervises Its Own Tasks")
    private static let ownTasksFooter = String(localized: "While this is off, the background supervisor follows up the tasks Persona hands out, so the main chat answers you first.")

    // Where Persona asks before acting on your behalf: the Mac app asks in the app, iOS asks on the phone (#4068).
    // The push itself still goes to the iPhone, so "notify you" stays in both.
    #if os(macOS)
    private static let reachApprovalLine = String(localized: "Ask you and notify you in the app")
    private static let reachApprovalFooter = String(localized: "Anything that acts on your behalf, like sending Lark messages, sending email, or creating events, asks you in the app first.")
    #else
    private static let reachApprovalLine = String(localized: "Ask you and notify you on your phone")
    private static let reachApprovalFooter = String(localized: "Anything that acts on your behalf, like sending Lark messages, sending email, or creating events, asks you on your phone first.")
    #endif

    private var upcoming: [AssistantUpcoming] {
        AssistantUpcoming.merge(tasks ?? []).prefix(3).map { $0 }
    }

    private func row(_ title: LocalizedStringResource, _ symbol: String, detail: String?) -> some View {
        LabeledContent {
            if let detail { Text(detail).lineLimit(1) }
        } label: {
            Label(String(localized: title), systemImage: symbol).lineLimit(1)
        }
    }

    /// The BeautifulUI style's row that opens a page: its mark, its name, what is there now.
    private func buiRow(_ title: LocalizedStringResource, _ symbol: String, detail: String?) -> some View {
        BUIListRow(String(localized: title), value: detail, accessory: .disclosure) { IconTile(symbol, tint: Theme.accent) }
    }

    /// The BeautifulUI style's row for what Persona can reach.
    private func buiReach(_ text: String, _ symbol: String) -> some View {
        BUIListRow(text) { IconTile(symbol, tint: Theme.Hue.gray) }
    }

    /// Reloaded each time the page shows (coming back from a sub-page included).
    private func load() async {
        guard let api = model.api else { return }
        async let r = try? api.scheduledTasks()
        async let h = try? api.scheduleRuns()
        async let m = try? api.assistantMemory()
        async let p = try? api.savedPlaces()
        // The switch shows once the hub says it has the setting (#1731): a hub updated since the app read it, too.
        let held = model.assistantConfig
        if held?.mainTasksQuiet == nil || held?.mainBackgroundFolded == nil || held?.mainSupervisesOwnTasks == nil { await model.refreshAssistant() }
        tasks = await r
        runs = await h
        if let files = await m { memoryCount = files.filter { $0.name != AssistantMemoryEntry.index }.count }
        placeCount = (await p)?.count
    }

    /// Saved Places' row detail: how many there are, nothing when none.
    private var placesDetail: String? {
        placeCount.flatMap { $0 > 0 ? String(localized: "\($0) places", comment: "Saved places count") : nil }
    }
}

/// One thing Persona will do on its own: a task's next run.
struct AssistantUpcoming: Identifiable, Hashable {
    var id: String
    var at: Date
    var title: String
    var routine: Bool
    var detail: String
    var key: SessionKey?

    /// Soonest first; what is switched off is not coming up.
    static func merge(_ tasks: [APIClient.ScheduledTask]) -> [AssistantUpcoming] {
        var out: [AssistantUpcoming] = []
        for t in tasks where t.enabled {
            guard let next = t.nextRun ?? t.at else { continue }
            if t.repeats == .once {
                out.append(.init(id: "t-" + t.id, at: next, title: AssistantScheduleText.title(t), routine: false,
                                 detail: t.sessionID == nil ? String(localized: "Follow-up · new chat") : String(localized: "Follow-up · same chat"),
                                 key: t.sessionID.map { SessionKey(machineID: t.machineID ?? "", sessionID: $0) }))
            } else {
                out.append(.init(id: "t-" + t.id, at: next, title: AssistantScheduleText.title(t), routine: true,
                                 detail: AssistantScheduleText.sentence(t)))
            }
        }
        return out.sorted { $0.at < $1.at }
    }
}

/// A timeline row like Reminders': the time big on the left, what happens on the right.
struct AssistantUpcomingRow: View {
    var item: AssistantUpcoming

    var body: some View {
        if AppStyle.current.usesBeautifulUI { beautiful } else { plain }
    }

    /// The BeautifulUI style: its list row, the kind's mark, what and when, the time on the right.
    private var beautiful: some View {
        BUIListRow(item.title, detail: "\(item.detail) · \(AssistantScheduleText.until(item.at))",
                   value: "\(AssistantScheduleText.day(item.at)) \(AssistantScheduleText.hm(item.at))") {
            IconTile(item.routine ? Theme.Icon.recurring : Theme.Icon.followOn, tint: Theme.accent)
        }
        .accessibilityElement(children: .combine)
    }

    private var plain: some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.l) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(AssistantScheduleText.hm(item.at))
                    .font(Theme.Font.heading.monospacedDigit())
                Text(AssistantScheduleText.day(item.at)).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
            }
            .scaledFrame(width: Theme.Size.box58, alignment: .leading)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.sm) {
                    ThemedIcon(item.routine ? Theme.Icon.recurring : Theme.Icon.followOn)
                        .font(Theme.Font.labelHeavy)
                        .foregroundStyle(Theme.accent)
                    Text(item.title).font(Theme.Font.heading).lineLimit(1)
                }
                Text("\(item.detail) · \(AssistantScheduleText.until(item.at))")
                    .font(Theme.Font.label)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }
}

// MARK: - Run History

enum AssistantRunText {
    static func statusWord(_ r: APIClient.ScheduleRun) -> String {
        switch r.status {
        case "ok": String(localized: "Ran")
        case "skipped": String(localized: "Skipped")
        default: String(localized: "Failed")
        }
    }

    static func symbol(_ r: APIClient.ScheduleRun) -> (String, Color) {
        switch r.status {
        case "ok": (Theme.Icon.successFilled, Theme.positive)
        case "skipped": ("forward.end.circle.fill", .secondary)
        default: (Theme.Icon.errorFilled, Theme.danger)
        }
    }

    /// "On time" / "21 s late" / "Run manually" / the error.
    static func detail(_ r: APIClient.ScheduleRun) -> String {
        if r.manual == true { return String(localized: "Run manually") }
        if r.status != "ok" { return r.error ?? statusWord(r) }
        let late = r.at.timeIntervalSince(r.due)
        if late < 5 { return String(localized: "On time") }
        if late < 120 { return String(localized: "\(Int(late)) seconds late") }
        if late < 7200 { return String(localized: "\(Int(late / 60)) minutes late") }
        return String(localized: "\(Int(late / 3600)) hours late")
    }
}

/// Every run of a routine or follow-up, newest first; `ref` limits it to one of them. A run opens its conversation.
struct AssistantRunsView: View {
    @Environment(AppModel.self) private var model
    var ref: String?
    @State private var runs: [APIClient.ScheduleRun] = []
    @State private var loaded = false
    @State private var error: String?

    var body: some View {
        List {
            ForEach(days, id: \.0) { day, items in
                Section(day) {
                    ForEach(items) { r in AssistantRunRow(run: r) }
                }
            }
        }
        .themedPage()
        .overlay {
            if !loaded {
                ProgressView()
            } else if let error, runs.isEmpty {
                ContentUnavailableView("Can't Load Run History", systemImage: Theme.Icon.error, description: Text(error))
            } else if runs.isEmpty {
                ContentUnavailableView("No Runs Yet", systemImage: Theme.Icon.recent,
                                       description: Text("Every run of a scheduled task or follow-up, including failed and skipped ones, is listed here."))
            }
        }
        .pageTitle(String(localized: "Run History"))
        .task { await load() }
        .refreshable { await load() }
    }

    private var days: [(String, [APIClient.ScheduleRun])] {
        var out: [(String, [APIClient.ScheduleRun])] = []
        for r in runs {
            let d = AssistantScheduleText.day(r.at)
            if out.last?.0 == d { out[out.count - 1].1.append(r) } else { out.append((d, [r])) }
        }
        return out
    }

    private func load() async {
        guard let api = model.api else { return }
        do {
            runs = try await api.scheduleRuns(ref: ref).sorted { $0.at > $1.at } // one section per day
            error = nil
        } catch {
            self.error = error.shown ?? self.error
        }
        loaded = true
    }
}

struct AssistantRunRow: View {
    @Environment(AppModel.self) private var model
    var run: APIClient.ScheduleRun
    /// Inside one routine's page the title is the page's.
    var showTitle = true

    var body: some View {
        if AppStyle.current.usesBeautifulUI { beautiful } else { plain }
    }

    /// BeautifulUI's task row: the green check, the red cross, or an empty ring for a skipped run; what ran and how
    /// (On time / 21 s late / the error) under it; the chevron when it has a conversation to open.
    @ViewBuilder private var beautiful: some View {
        let row = TaskRow(TaskItem(id: run.id, title: showTitle ? run.title : AssistantScheduleText.moment(run.at),
                                   subtitle: showTitle ? "\(AssistantScheduleText.hm(run.at)) · \(AssistantRunText.detail(run))" : AssistantRunText.detail(run),
                                   status: run.status == "ok" ? .completed : .failed),
                          shape: .plain, accessory: run.key != nil ? .disclosure : .none, subtitleLines: 2) {
            if run.status == "skipped" {
                Circle().strokeBorder(Theme.cardStroke, lineWidth: Theme.Size.rule).frame(width: Theme.Size.badge, height: Theme.Size.badge)
            } else {
                TaskBadge(run.status == "ok" ? .completed : .failed)
            }
        } trailing: {
            EmptyView()
        } detail: {
            EmptyView()
        }
        if let key = run.key {
            Button {
                model.selectedTab = .assistant
                model.assistantCurrent = model.inHub(key)
            } label: { row }
            .buttonStyle(.plain)
            .accessibilityHint("Opens this run's chat")
        } else {
            row
        }
    }

    @ViewBuilder private var plain: some View {
        let (symbol, tint) = AssistantRunText.symbol(run)
        let content = HStack(spacing: Theme.Space.m) {
            ThemedIcon(symbol).font(Theme.Font.title).foregroundStyle(tint)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(showTitle ? run.title : AssistantScheduleText.moment(run.at))
                    .font(Theme.Font.heading).foregroundStyle(Color.primary).lineLimit(1)
                Text(showTitle ? "\(AssistantScheduleText.hm(run.at)) · \(AssistantRunText.detail(run))"
                     : AssistantRunText.detail(run))
                    .font(Theme.Font.label)
                    .foregroundStyle(run.status == "failed" ? Theme.danger : Color.secondary)
                    .lineLimit(2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if run.key != nil {
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.captionStrong).foregroundStyle(Color(uiColor: .tertiaryLabel))
            }
        }
        if let key = run.key {
            Button {
                model.selectedTab = .assistant
                model.assistantCurrent = model.inHub(key)
            } label: { content }
            .accessibilityHint("Opens this run's chat")
        } else {
            content
        }
    }
}
