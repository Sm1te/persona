import PersonaKit
import SwiftUI

// The unified entry's pieces in the assistant's conversation (docs/assistant/unified-entry.md): the plan card, the task card,
// the lines the hub adds (answered, receipt, routed), the dock above the composer, the questions of the tasks' agents,
// and the route chip with its @ list. They read `TasksStore` and `AppModel`, so a card follows its task live.

/// A task's mark: the agent state dot it borrows (needs you breathes), a check once done.
struct TaskMark: View {
    var state: TaskState
    var size: CGFloat = Theme.Size.dotL

    var body: some View {
        if state == .failed {
            ThemedIcon(Theme.Icon.errorFilled)
                .font(Theme.Font.labelStrong)
                .foregroundStyle(Theme.danger)
                .accessibilityLabel("Didn't Start")
        } else if state == .done || state == .unread {
            ThemedIcon(Theme.Icon.successFilled)
                .font(Theme.Font.labelStrong)
                .foregroundStyle(state == .done ? Color.secondary : Theme.positive)
                .accessibilityLabel(state == .done ? StateName.done.text : StateName.unread.text)
        } else {
            StatusDot(state: state.agentState, size: size)
        }
    }
}

extension TaskState {
    var label: String {
        switch self {
        case .needsYou: StateName.needsYou.text
        case .working: StateName.working.text
        case .unread: StateName.unread.text
        case .waiting: StateName.taskWaiting.text
        case .followup: StateName.followUp.text
        case .notStarted: String(localized: "Starting [Task Views d14c3a]")
        case .done: StateName.done.text
        case .failed: String(localized: "Didn't Start")
        }
    }

    /// The color of the task's state label: its agent state's, red when it never started.
    var labelColor: Color { self == .failed ? Theme.danger : agentState.tone.text }
}

// MARK: - Removing a task

/// Remove for a task that does not run, like the Agents page's Close for an agent: a task whose agents ran asks first (their
/// chats stay on the Agents page), one whose agent never started goes at once.
struct TaskRemoval: ViewModifier {
    @Binding var pending: TodoItem?

    func body(content: Content) -> some View {
        content.confirmationDialog(pending.map { String(localized: "Remove “\($0.title)”?") } ?? "",
                                   isPresented: Binding(get: { pending != nil }, set: { if !$0 { pending = nil } }),
                                   titleVisibility: .visible, presenting: pending) { todo in
            Button("Remove [Task Sheets 6135d4]", role: .destructive) { Task { await TasksStore.shared.remove(todo) } }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("The task is removed from this chat. Its agent and chat history stay on the Agents tab.")
        }
    }
}

extension View {
    func confirmTaskRemoval(_ pending: Binding<TodoItem?>) -> some View { modifier(TaskRemoval(pending: pending)) }
}

extension TasksStore {
    /// Removes a task at once when no agent of it ever ran, else asks first (through `pending`).
    func askRemove(_ todo: TodoItem, pending: Binding<TodoItem?>) {
        if todo.agents.isEmpty { Task { await remove(todo) } } else { pending.wrappedValue = todo }
    }
}

/// The Remove Task of a task's menus (long press on its card, line or pill), when nothing of it runs.
struct TaskRemoveMenuItem: View {
    var todo: TodoItem
    var state: TaskState
    var pending: Binding<TodoItem?>

    var body: some View {
        if state.isRemovable {
            Button(role: .destructive) { TasksStore.shared.askRemove(todo, pending: pending) } label: {
                Label("Remove Task", systemImage: Theme.Icon.delete)
            }
        }
    }
}

/// "12 min", "1 h 5 min" since a date.
enum TaskClock {
    /// How often a running step's time is drawn again: it shows minutes.
    static let tick: TimeInterval = 30

    static func since(_ date: Date?, now: Date = .now) -> String? {
        guard let date, date.isMeaningful else { return nil }
        let m = max(0, Int(now.timeIntervalSince(date) / 60))
        if m < 1 { return String(localized: "Just started") }
        if m < 60 { return String(localized: "\(m) min [Task Views 135d12]") }
        let h = m / 60
        return m % 60 == 0 ? String(localized: "\(h) hr") : String(localized: "\(h) hr \(m % 60) min")
    }
}

/// Where a task runs, as the user names it: the machine's name, the project, the agent.
@MainActor
enum TaskPlace {
    static func machineName(_ id: String) -> String {
        let model = AppModel.shared
        return model.machine(model.hubRef(id))?.name ?? id
    }

    static func line(_ todo: TodoItem, lead: Session?) -> String {
        var parts: [String] = []
        if let a = todo.activeAgents.first ?? todo.agents.first {
            parts.append(machineName(a.machineID))
            if let cwd = a.cwd, !cwd.isEmpty { parts.append((cwd as NSString).lastPathComponent) }
        } else if let planned = todo.planID.flatMap({ TasksStore.shared.plans[$0] })?.tasks.first(where: { $0.todoID == todo.id }) {
            // No agent (yet, or ever): where the plan meant it to run.
            parts.append(machineName(planned.machine))
            if !planned.project.isEmpty { parts.append(planned.project) }
        }
        if let lead { parts.append(lead.agentLabel) }
        return parts.joined(separator: " · ")
    }
}

// MARK: - Plan card

struct PlanCard: View {
    var plan: TaskPlan
    @State private var remember = false
    @State private var editing = false
    /// Each task's ultracode as set on the card (#12), by index; the assistant's where it is not.
    @State private var ultraChoice: [Int: Bool] = [:]
    private var store: TasksStore { .shared }

    var body: some View {
        switch plan.state {
        case .open: open
        case .started: settled(symbol: Theme.Icon.done, text: String(localized: "Started \(plan.tasks.count) tasks"), failed: plan.tasks.filter { $0.error != nil })
        case .dismissed, .unknown: settled(symbol: Theme.Icon.close, text: String(localized: "Not started · \(plan.tasks.count) tasks"), failed: [])
        }
    }

    private var open: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Label(plan.tasks.count > 1 ? String(localized: "Split into \(plan.tasks.count) tasks, started like this") : String(localized: "Start like this"), systemImage: Theme.Icon.taskPlan)
                .font(Theme.Font.captionStrong)
                .foregroundStyle(.secondary)
            let offered = Ultracode.planOffered
            let ultracode = Ultracode.choices(plan, card: ultraChoice)
            ForEach(Array(plan.tasks.enumerated()), id: \.offset) { n, task in
                if n > 0 { Divider() }
                PlanTaskRow(number: n + 1, task: task, numbered: plan.tasks.count > 1,
                            ultra: offered ? .init(on: ultracode[n], blocked: Ultracode.blocked(machine: task.machine)) : nil,
                            onUltra: { ultraChoice[n] = $0 })
            }
            .disabled(store.busyPlans.contains(plan.id))
            Button {
                withAnimation(Theme.Motion.state) { remember.toggle() }
            } label: {
                Label("Start These Directly", systemImage: remember ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                    .font(Theme.Font.caption)
                    .foregroundStyle(remember ? Theme.accent : .secondary)
                    .lineLimit(1)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            HStack(spacing: Theme.Space.s) {
                Button {
                    let blocked = Ultracode.blockedMachines(plan.tasks)
                    let edits = Ultracode.cardEdits(plan, choices: Ultracode.choices(plan, card: ultraChoice), blocked: { blocked.contains($0) })
                    Task { await store.start(plan, edits: edits, remember: remember) }
                } label: {
                    Text(plan.tasks.count > 1 ? String(localized: "Start \(plan.tasks.count) Tasks") : String(localized: "Start")).lineLimit(1)
                        .frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                Button { editing = true } label: { Text("Edit [Task Views 928324]").lineLimit(1) }
                    .themedButton()
                Button {
                    Task { await store.dismiss(plan) }
                } label: { Text("Don't Start").lineLimit(1) }
                    .themedButton()
            }
            .disabled(store.busyPlans.contains(plan.id))
        }
        .card(padding: Theme.Space.l)
        .sheet(isPresented: $editing) {
            PlanEditSheet(plan: plan, ultracode: Ultracode.choices(plan, card: ultraChoice)) { edits in
                editing = false
                let blocked = Ultracode.blockedMachines(edits)
                let sent = Ultracode.editsToSend(plan, edits: edits, blocked: { blocked.contains($0) })
                Task { await store.start(plan, edits: sent, remember: remember) }
            }
        }
    }

    private func settled(symbol: String, text: String, failed: [PlanTask]) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Label(text, systemImage: symbol)
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            ForEach(failed, id: \.title) { t in
                // No reason from the hub says so, as the task's status line does, rather than ending on the colon (#4138).
                let reason = t.error?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                Label("“\(t.title)” didn't start: \(reason.isEmpty ? String(localized: "unknown reason") : reason)", systemImage: Theme.Icon.error)
                    .font(Theme.Font.caption)
                    .foregroundStyle(Theme.danger)
                    .lineLimit(2)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// One task of an open plan: its title, where it would run (each a chip), and why there.
struct PlanTaskRow: View {
    struct Ultra { var on: Bool; var blocked: String? }

    var number: Int
    var task: PlanTask
    var numbered: Bool
    /// The task's ultracode on the card (#12): nil where the hub has none, and the row is drawn as before.
    var ultra: Ultra? = nil
    var onUltra: (Bool) -> Void = { _ in }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                if numbered {
                    Text("\(number)")
                        .font(Theme.Font.labelHeavy)
                        .foregroundStyle(Theme.accent)
                        .scaledFrame(width: Theme.Size.badge, height: Theme.Size.badge)
                        .background(Theme.accent.opacity(0.14), in: .circle)
                }
                Text(task.title).font(Theme.Font.bodyStrong)
            }
            FlowLayoutRow(spacing: Theme.Space.xs) {
                Chip(text: TaskPlace.machineName(task.machine), symbol: Theme.Icon.laptop)
                Chip(text: task.project, symbol: Theme.Icon.folder)
                Chip(text: task.model.flatMap { $0.isEmpty ? nil : $0 } ?? task.launcher.flatMap { $0.isEmpty ? nil : $0 } ?? String(localized: "Default Model"),
                     symbol: Theme.Icon.cpu)
                if task.isReadOnly {
                    Chip(text: String(localized: "Read-only"), symbol: Theme.Icon.view, look: .tint(Theme.accent))
                } else if let p = task.permission, !p.isEmpty {
                    Chip(text: Self.permissionLabel(p))
                }
                if let ultra {
                    UltracodeChip(on: ultra.on, blocked: ultra.blocked, change: onUltra)
                }
            }
            // Asked for on a computer that can't: it starts without.
            if let ultra, ultra.on, let reason = ultra.blocked {
                Text("Starts without ultracode: \(reason)")
                    .font(Theme.Font.caption)
                    .foregroundStyle(Theme.cautionText)
            }
            if let why = task.why, !why.isEmpty {
                Text(why).font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        }
    }

    /// The modes a planned task can start with (web PLAN_PERMISSIONS, Android PlanPermissions); Auto too, which the hub
    /// may settle an unset one to from Claude Code's default mode (#3026).
    static let permissions = ["default", "acceptEdits", "plan", "auto", "bypassPermissions"]

    static func permissionLabel(_ mode: String) -> String {
        switch mode {
        case "acceptEdits": String(localized: "Can edit files")
        case "plan": String(localized: "Plan first")
        case "auto": String(localized: "Auto [Plan Card Permission]", comment: "A planned task's permission mode: the agent judges the risk itself")
        case "bypassPermissions": String(localized: "Allow Everything")
        default: String(localized: "Ask every step")
        }
    }
}

/// Edit: the plan's fields the user can change before it starts.
struct PlanEditSheet: View {
    var plan: TaskPlan
    /// Each task's ultracode as the card shows it (#12): where Edit starts.
    var ultracode: [Bool] = []
    var start: ([PlanTask]) -> Void
    @State private var tasks: [PlanTask] = []
    /// What the sheet opened with: anything else is unsaved.
    @State private var opened: [PlanTask] = []
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                ForEach(tasks.indices, id: \.self) { i in
                    Section(tasks.count > 1 ? String(localized: "Task \(i + 1)") : String(localized: "Tasks")) {
                        TextField(String(localized: "Title"), text: $tasks[i].title)
                        Picker("Computers", selection: $tasks[i].machine) {
                            ForEach(AppModel.shared.hubMachines, id: \.id) { m in Text(m.name).tag(m.id) }
                            if !AppModel.shared.hubMachines.contains(where: { $0.id == tasks[i].machine }) {
                                Text(tasks[i].machine).tag(tasks[i].machine)
                            }
                        }
                        TextField(String(localized: "Folder [Task Views 52daa7]"), text: $tasks[i].cwd)
                            .font(Theme.Font.code)
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        TextField(String(localized: "Model (blank for default)"), text: Binding(get: { tasks[i].model ?? "" }, set: { tasks[i].model = $0 }))
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                        Picker("Permissions", selection: Binding(get: { tasks[i].permission ?? "default" }, set: { tasks[i].permission = $0 })) {
                            ForEach(PlanTaskRow.permissions, id: \.self) { Text(PlanTaskRow.permissionLabel($0)).tag($0) }
                        }
                        if Ultracode.planOffered {
                            PlanUltracodeRow(on: Binding(get: { tasks[i].ultracode == true }, set: { tasks[i].ultracode = $0 }),
                                             blocked: Ultracode.blocked(machine: tasks[i].machine))
                        }
                    }
                    Section("Message for the Agent") {
                        TextField(String(localized: "First Message"), text: $tasks[i].prompt, axis: .vertical).lineLimit(3...8)
                    }
                }
            }
            .navigationTitle("Edit [Task Views 928324]")
            .navigationBarTitleDisplayMode(.inline)
            .sheetCancel(unsaved: !tasks.isEmpty && tasks != opened) { dismiss() }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Start") { start(tasks) }.bold() }
            }
        }
        #if os(macOS)
        .macSheet()
        #endif
        .onAppear {
            guard tasks.isEmpty else { return }
            tasks = Ultracode.editsOf(plan, choices: ultracode)
            opened = tasks
        }
    }
}

// MARK: - Task card

/// A task's full card: its mark, title, time, where it runs, what its agent says now.
struct TaskCard: View {
    var todoID: String
    var conversation: String
    @State private var removing: TodoItem?
    /// The conversation's link handler: web links open in the in-app link sheet (ChatView).
    @Environment(\.openURL) private var openURL
    private var store: TasksStore { .shared }

    var body: some View {
        if let todo = store.todos[todoID] {
            let state = store.state(todo)
            let lead = store.leadSession(todo)
            let ultracode = store.ultracode(todo)
            if let ask = todo.openAsk {
                BriefAcceptRow(row: ask.row(for: todo), group: AppModel.shared.hub?.group ?? "")
                    .card(padding: Theme.Space.l)
            } else {
                Button {
                    store.detailTask = todo.id
                } label: {
                    VStack(alignment: .leading, spacing: Theme.Space.s) {
                        HStack(spacing: Theme.Space.s) {
                            TaskMark(state: state).scaledFrame(width: Theme.Size.markM)
                            Text(todo.title).font(Theme.Font.bodyStrong).lineLimit(1)
                            if ultracode { UltracodeMark(font: Theme.Font.labelStrong).transition(.opacity) }
                            Spacer(minLength: Theme.Space.s)
                            if let t = TaskClock.since(todo.createdAt), state != .done, state != .failed {
                                Text(t).font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1).fixedSize()
                            }
                        }
                        .animation(UltracodeMark.motion, value: ultracode)
                        Text(TaskPlace.line(todo, lead: lead))
                            .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        TaskNowLine(todo: todo, state: state, lead: lead)
                        // What Persona sent its agent (#1542): the hand-off sent with this card is drawn only here.
                        TaskSentList(todoID: todo.id, conversation: conversation)
                        if state == .done, todo.supervision != nil {
                            ReviewChip(todo: todo)
                        }
                        // The PR its work is in (`pr_url`, #2768), as the Feishu bot's task report links it (#5033).
                        if let pr = todo.prLink {
                            Button {
                                openURL(pr)
                            } label: {
                                Label(todo.prLabel ?? String(localized: "View PR"), systemImage: Theme.Icon.pullRequest).lineLimit(1)
                            }
                            .font(Theme.Font.captionStrong)
                            .foregroundStyle(Theme.accent)
                            .buttonStyle(.plain)
                        }
                        if state != .done {
                            HStack(spacing: Theme.Space.s) {
                                Text(state.label).font(Theme.Font.captionMedium).foregroundStyle(state.labelColor).lineLimit(1).fixedSize()
                                ReviewChip(todo: todo)
                                Spacer(minLength: Theme.Space.l)
                                if state == .failed {
                                    // Nothing to say a word to: the way out is on the card.
                                    Button {
                                        store.askRemove(todo, pending: $removing)
                                    } label: {
                                        Label("Remove [Task Sheets 6135d4]", systemImage: Theme.Icon.delete).lineLimit(1)
                                    }
                                    .font(Theme.Font.captionStrong)
                                    .foregroundStyle(Theme.danger)
                                    .buttonStyle(.plain)
                                } else {
                                    Button {
                                        store.routes[conversation] = todo.id
                                    } label: {
                                        Label("Message [Task Views 7e7db6]", systemImage: Theme.Icon.routeToTask).lineLimit(1)
                                    }
                                    .font(Theme.Font.captionStrong)
                                    .foregroundStyle(Theme.accent)
                                    .buttonStyle(.plain)
                                }
                            }
                        }
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .card(padding: Theme.Space.l)
                .overlay {
                    if state == .needsYou {
                        RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
                            .stroke(state.agentState.color, lineWidth: Theme.Size.lineMedium)
                    }
                }
                .menuPreviewShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous))
                .contextMenu { TaskRemoveMenuItem(todo: todo, state: state, pending: $removing) }
                .confirmTaskRemoval($removing)
                .animation(Theme.Motion.state, value: state)
            }
        }
    }
}

/// What the task's agent is doing or said last, in a well: waiting for the user in its tone; while it only runs tools,
/// the step it is on and how long it has been at it (`TaskNow`).
struct TaskNowLine: View {
    var todo: TodoItem
    var state: TaskState
    var lead: Session?

    private var store: TasksStore { .shared }

    var body: some View {
        let now = TaskNow(lead: lead, working: state == .working)
        let text: String = {
            if state == .needsYou { return TaskAsk.waitingLine(store.interactions(todo)) }
            if state == .failed { return String(localized: "Didn't start: \(store.startFailure(todo) ?? String(localized: "unknown reason")) [Tasks Store 38bbb7]") }
            if let now { return now.text }
            if let e = todo.agents.first?.startError, !e.isEmpty { return String(localized: "Didn't start: \(e) [Tasks Store 38bbb7]") }
            return state == .notStarted ? String(localized: "Starting the agent") : String(localized: "No activity yet")
        }()
        let tint: Color? = state == .needsYou ? state.agentState.tone.text : state == .failed ? Theme.danger : nil
        let since: Date? = if state != .needsYou, state != .failed, case .step(_, let at)? = now { at } else { nil }
        let isStep: Bool = if state != .needsYou, state != .failed, case .step? = now { true } else { false }
        let subagents = (state == .working || state == .needsYou) ? runningSubagents(lead) : []
        let subagentsLine = subagentsSummaryLine(subagents)
        // What the agent says starts at the well's edge; only a question for the user keeps its mark.
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                if state == .needsYou {
                    ThemedIcon(Theme.Icon.askFirst)
                        .font(Theme.Font.label)
                        .foregroundStyle(state.agentState.tone.text)
                }
                Text(text)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(tint ?? (isStep ? .secondary : .primary))
                    .lineLimit(isStep ? 1 : 2)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if let since {
                    // How long the step has gone on: a long test run or CI wait reads as one, not as a stuck card.
                    VisibleTimelineView(interval: TaskClock.tick) { now in
                        if let t = TaskClock.since(since, now: now) {
                            Text(t).font(Theme.Font.caption).foregroundStyle(.tertiary).lineLimit(1).fixedSize()
                        }
                    }
                }
            }
            if let subagentsLine {
                Text(subagentsLine)
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            // What its agent last told Persona (report_to_supervisor, #5112): while the task is open.
            if state != .done, state != .failed, let reported = todo.supervision?.progressLine {
                TaskProgressLine(line: reported)
            }
            // A workflow its agent runs in the background (#2935): the task is not done while it does.
            if state != .failed, let runs = lead?.runningWorkflows, !runs.isEmpty {
                WorkflowLine(runs: runs)
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
    }
}

/// A task of another day, folded: one line; a tap opens its detail (its Agent's chat, as the card does).
struct TaskLine: View {
    var todoID: String
    @State private var removing: TodoItem?
    private var store: TasksStore { .shared }

    var body: some View {
        if let todo = store.todos[todoID] {
            let state = store.state(todo)
            let ultracode = store.ultracode(todo)
            Button { store.detailTask = todo.id } label: {
                HStack(spacing: Theme.Space.s) {
                    TaskMark(state: state).scaledFrame(width: Theme.Size.markM)
                    Text(todo.title).font(Theme.Font.secondaryMedium).lineLimit(1)
                    if ultracode { UltracodeMark(font: Theme.Font.label).transition(.opacity) }
                    Spacer(minLength: Theme.Space.s)
                    Text(state.label).font(Theme.Font.caption).foregroundStyle(state.labelColor).lineLimit(1).fixedSize()
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                }
                .animation(UltracodeMark.motion, value: ultracode)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.s)
                .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .menuPreviewShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .contextMenu { TaskRemoveMenuItem(todo: todo, state: state, pending: $removing) }
            .confirmTaskRemoval($removing)
        }
    }
}

// MARK: - The hub's lines

/// One line the hub added to the conversation (docs/protocol/protocol.md「Tasks and the chat feed」).
struct FeedLine: View {
    var item: FeedItem
    var group: String = ""
    @Environment(\.chatLayout) private var layout
    private var store: TasksStore { .shared }

    var body: some View {
        switch item.kind {
        case .plan, .task:
            if let tasks = DispatchedTasks.entries(for: item, plans: store.plans,
                                                  titleOf: { store.todos[$0]?.title }, fallback: String(localized: "Tasks")) {
                DispatchedTaskLinks(tasks: tasks)
            } else if let id = item.ref, let plan = store.plans[id] ?? item.plan {
                PlanCard(plan: plan)
            }
        case .today:
            if let body = item.today { TodayCard(item: item, body: body) }
        case .brief:
            // A routine's facts (#2320); a body this build can't read leaves the card out, never the feed around it.
            if let brief = item.brief { BriefCard(brief: brief, group: group) }
        case .message:
            if let id = item.ref, !store.laterMessages.contains(id) { MessageCard(item: item, itemID: id) }
        case .answered: AnsweredLine(item: item, taskTitle: taskTitle)
        case .receipt: ReceiptCard(item: item)
        case .routed:
            // Persona's own message to the task's agent is never the user's bubble (#1542). The new conversation layout
            // (#1666): "Persona → @task" folded in Persona's box, the user's own "Me → @task" in the user's box.
            if item.sentByPersona {
                if layout.on { RoutedExchange(item: item, taskTitle: taskTitle, layout: layout) } else { AgentSentLine(item: item, taskTitle: taskTitle) }
            } else if layout.on {
                UserRoutedExchange(item: item, taskTitle: taskTitle)
            } else {
                routed
            }
        case .review: ReviewFeedLine(item: item, taskTitle: taskTitle)
        case .report:
            if layout.on { ReportExchange(item: item, taskTitle: taskTitle, layout: layout) } else { ReportFeedLine(item: item, taskTitle: taskTitle) }
        case .view:
            // Until the shared interactive renderer is connected, show the current card's plain-text fallback.
            if let id = item.ref,
               let view = AssistantViewsStore.shared.view(id, conversation: item.conversation, group: group) ?? item.assistantView,
               view.id == id, view.conversation == item.conversation {
                let location = AssistantCardLocation(key: id, scope: AssistantViewScope(group: group, conversation: item.conversation))
                InteractiveAssistantViewCard(view: view.view, version: view.version,
                    registered: AppModel.shared.features(group).contains("assistant_views"),
                    location: location, deps: .live(location))
            }
        case .notice:
            // A line the hub wrote (a routine running again once the computer and model are ready, #4305).
            if let text = item.noticeText {
                Text(text)
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: .infinity)
                    .padding(.horizontal, Theme.Space.xl)
                    .accessibilityIdentifier("feed.notice")
            }
        case .unknown: EmptyView()
        }
    }

    private var taskTitle: String { item.ref.flatMap { store.todos[$0]?.title } ?? String(localized: "Tasks") }

    /// What the user said straight to the task (its card's Message): the user's bubble under where it went.
    private var routed: some View {
        VStack(alignment: .trailing, spacing: Theme.Space.xs) {
            // Persona stopped the agent's turn and then told it (agent_interrupt with then, #5112): from a hub before #1542,
            // which did not say it was Persona's.
            Label(item.interrupted ? String(localized: "Stopped “\(taskTitle)” and told it") : String(localized: "Message “\(taskTitle)”"),
                  systemImage: item.interrupted ? Theme.Icon.interrupt : Theme.Icon.routeToTask)
                .font(Theme.Font.captionMedium)
                .foregroundStyle(Theme.accent)
                .lineLimit(1)
            Text(item.text ?? "")
                .font(Theme.Font.body)
                .foregroundStyle(Theme.userBubbleText)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.s)
                .background(Theme.userBubbleFill, in: .rect(cornerRadius: Theme.Radius.bubble, style: .continuous))
        }
        .frame(maxWidth: .infinity, alignment: .trailing)
        .padding(.leading, Theme.Space.xxl)
    }
}

/// A card of a task's agent the user dealt with, kept as a line「✓ 任务 · 回答」. Tap it for every question with its
/// answer in full (#1231), selectable, and again to fold it; the task's name opens the task.
struct AnsweredLine: View {
    var item: FeedItem
    var taskTitle: String
    @State private var open = false
    @State private var unfold = Unfolding()
    /// Where the answers end on screen, to bring them into view once open (`ChatPin.show`).
    @State private var bottom: CGFloat = 0
    @Environment(\.chatPin) private var pin
    private var store: TasksStore { .shared }
    private var pinKey: String { "answered-" + item.id }

    var body: some View {
        let items = item.answeredItems
        VStack(alignment: .leading, spacing: 0) {
            header(expandable: !items.isEmpty)
            if open {
                VStack(alignment: .leading, spacing: Theme.Space.m) {
                    ForEach(Array(items.enumerated()), id: \.offset) { _, it in
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            if !it.question.isEmpty {
                                Text(it.question).font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                            }
                            if !it.answer.isEmpty {
                                Text(it.answer).font(Theme.Font.secondary).foregroundStyle(.primary)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                        .accessibilityElement(children: .combine)
                    }
                }
                .padding(.horizontal, Theme.Space.m)
                .padding(.bottom, Theme.Space.m)
                .accessibilityIdentifier("answered-detail")
                .unfolding(unfold)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .frame(maxWidth: open ? .infinity : nil, alignment: .leading)
        .onGeometryChange(for: CGFloat.self, of: { $0.frame(in: .global).maxY }) { bottom = $0 }
        .background(Theme.Fill.chip, in: open ? AnyShape(.rect(cornerRadius: Theme.Radius.card, style: .continuous)) : AnyShape(.capsule))
        .frame(maxWidth: .infinity, alignment: .leading)
        .chatPinned(pinKey)
    }

    private func header(expandable: Bool) -> some View {
        HStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.done).font(Theme.Font.labelStrong)
            HStack(spacing: Theme.Space.xs) {
                if let id = item.ref, store.todos[id] != nil {
                    Button { store.detailTask = id } label: { Text(taskTitle).lineLimit(1) }
                        .buttonStyle(.plain)
                } else {
                    Text(taskTitle).lineLimit(1)
                }
                Text("· \(item.answer ?? String(localized: "Answered"))").lineLimit(1)
            }
            if expandable {
                ThemedIcon(Theme.Icon.disclosure)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(.tertiary)
                    .rotationEffect(.degrees(open ? 90 : 0))
                    .accessibilityHidden(true)
            }
        }
        .font(Theme.Font.caption)
        .foregroundStyle(.secondary)
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.sm)
        .contentShape(.rect)
        .onTapGesture { if expandable { toggle() } }
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("answered-line")
        .accessibilityValue(expandable ? (open ? String(localized: "Expanded") : String(localized: "Collapsed [Assistant Sidebar e64447]")) : "")
        .accessibilityAction(named: open ? String(localized: "Collapse") : String(localized: "Expand")) { if expandable { toggle() } }
        .accessibilityAction(named: "Open Task") { if let id = item.ref, store.todos[id] != nil { store.detailTask = id } }
    }

    private func toggle() {
        Haptics.tap()
        let opening = !open
        toggleInPlace(pin, pinKey, animation: Theme.Motion.state, unfold: $unfold, closing: open) { open.toggle() }
        #if os(iOS) || os(visionOS)
        // The line as the chat's last row: the answers open under it, past the composer.
        guard opening, let pin else { return }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(Theme.Motion.revealTime))
            if open { pin.show(pinKey, bottom: bottom) }
        }
        #endif
    }
}

/// A message about one of the conversation's tasks, with the reply the AI twin drafted from the task's progress
/// (docs/assistant/unified-entry.md「Message cards link to tasks」): who said what, the task (live), the draft and its actions.
/// A per-card dismissal leaves the work open; handled messages keep their reply as plain text without Send.
struct MessageCard: View {
    var item: FeedItem
    var itemID: String
    @State private var editing = false
    @State private var text = ""
    private var store: TasksStore { .shared }

    var body: some View {
        let card = store.messageCard(item)
        let state = store.messageState(card, itemID: itemID)
        let filed = state == .filed || state == .dismissed
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(item.source == "mail" ? Theme.Icon.mail : Theme.Icon.groupChat)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(Theme.loop)
                Text([item.sender, item.chat].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · "))
                    .font(Theme.Font.captionStrong).foregroundStyle(.secondary).lineLimit(1)
            }
            Text(item.text ?? "")
                .font(Theme.Font.body)
                .lineLimit(4)
                .padding(.leading, Theme.Space.m)
                .overlay(alignment: .leading) { Capsule().fill(Theme.loop).frame(width: Theme.Size.bar) }
            if let id = item.todoID, let todo = store.todos[id] {
                let state = store.state(todo)
                Button { store.detailTask = id } label: {
                    HStack(spacing: Theme.Space.s) {
                        TaskMark(state: state).scaledFrame(width: Theme.Size.markM)
                        Text(todo.title).font(Theme.Font.captionStrong).lineLimit(1)
                        Text(TaskNow(lead: store.leadSession(todo), working: state == .working)?.text ?? state.label)
                            .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: 0)
                        ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.labelStrong).foregroundStyle(.tertiary)
                    }
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.s)
                    .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Label(heading(state), systemImage: icon(state))
                    .font(Theme.Font.captionStrong)
                    .foregroundStyle(tint(state))
                    .lineLimit(1)
                if editing && state == .open {
                    TextField(String(localized: "Reply"), text: $text, axis: .vertical)
                        .font(Theme.Font.body)
                        .lineLimit(2...8)
                } else {
                    let draft = text.isEmpty ? (card.draft ?? "") : text
                    // Sent: what the hub says went out, which may have been edited on another device.
                    Text(state == .sent ? card.sent ?? draft : draft)
                        .font(Theme.Font.body)
                        .foregroundStyle(filed ? .secondary : .primary)
                }
            }
            .padding(Theme.Space.m)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(filed ? Theme.Fill.well : tint(state).opacity(0.12), in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .accessibilityIdentifier("messageCard.reply")
            if filed {
                Text("Already dealt with, so this reply wasn't sent.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                    .accessibilityIdentifier("messageCard.filed")
            }
            if state == .open {
                VStack(spacing: Theme.Space.s) {
                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: Theme.Space.s) { sendButton(card); editButton(card) }
                        VStack(spacing: Theme.Space.s) { sendButton(card); editButton(card) }
                    }
                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: Theme.Space.s) { laterButton(card); dismissButton(card) }
                        VStack(spacing: Theme.Space.s) { laterButton(card); dismissButton(card) }
                    }
                }
                .disabled(store.busyMessages.contains(card.id))
            }
        }
        .card(padding: Theme.Space.l)
        .accessibilityElement(children: .contain)
        .animation(Theme.Motion.state, value: state)
    }

    private func sendButton(_ card: FeedItem) -> some View {
        Button {
            Task {
                await store.sendMessage(card, itemID: itemID, text: text.isEmpty ? (card.draft ?? "") : text)
                editing = false
            }
        } label: {
            Text("Send").lineLimit(1).fixedSize(horizontal: true, vertical: false).frame(maxWidth: .infinity)
        }
        .draftDecisionButton(prominent: true)
        .accessibilityIdentifier("messageCard.send")
    }

    private func editButton(_ card: FeedItem) -> some View {
        Button {
            if text.isEmpty { text = card.draft ?? "" }
            withAnimation(Theme.Motion.state) { editing.toggle() }
        } label: {
            Text(editing ? String(localized: "Done [Task Views 9b8ee8]") : String(localized: "Edit [Task Views 928324]"))
                .lineLimit(1).fixedSize(horizontal: true, vertical: false)
        }
        .draftDecisionButton()
        .accessibilityIdentifier("messageCard.edit")
    }

    private func laterButton(_ card: FeedItem) -> some View {
        Button { withAnimation(Theme.Motion.state) { store.later(card.id) } } label: {
            Text("Later [Task Views 9d2ca2]").lineLimit(1).fixedSize(horizontal: true, vertical: false).frame(maxWidth: .infinity)
        }
        .draftDecisionButton()
        .accessibilityIdentifier("messageCard.later")
    }

    private func dismissButton(_ card: FeedItem) -> some View {
        Button { Task { await store.dismissMessage(card) } } label: {
            Text("Do not handle [Task Draft]").lineLimit(1).fixedSize(horizontal: true, vertical: false).frame(maxWidth: .infinity)
        }
        .draftDecisionButton()
        .accessibilityIdentifier("messageCard.dismiss")
    }

    private func heading(_ state: TasksStore.MessageState) -> String {
        switch state {
        case .open: String(localized: "Reply drafted from the task's progress")
        case .sent: String(localized: "Replied as you")
        case .filed: String(localized: "Handled elsewhere, draft not sent")
        case .dismissed: String(localized: "Draft dismissed, not sent")
        }
    }

    private func icon(_ state: TasksStore.MessageState) -> String {
        switch state {
        case .open: Theme.Icon.draftReply
        case .sent: Theme.Icon.done
        case .filed, .dismissed: Theme.Icon.handledElsewhere
        }
    }

    private func tint(_ state: TasksStore.MessageState) -> Color {
        switch state {
        case .open: Theme.accent
        case .sent: Theme.positive
        case .filed, .dismissed: .secondary
        }
    }
}

/// A task finished (or its agent did not start): what it did, its PR, and what to do next.
struct ReceiptCard: View {
    var item: FeedItem
    @Environment(\.openURL) private var openURL
    private var store: TasksStore { .shared }

    /// What a receipt says (#2362; the web's `receiptState` and `OutcomeReceipt`, #975). A receipt ends a stretch of work,
    /// not the task: a done one reads "done" only once its to-do is, and says the task stopped unfinished while the to-do
    /// is open. One whose to-do is gone from the list stands on its receipt. A turn the model service cut off is its own
    /// state: `interrupted`, or a `done` from an older hub whose summary is the API error.
    nonisolated enum Outcome: Equatable {
        case done, stopped, failed
        case interrupted(reason: String?)
    }

    nonisolated static func outcome(_ item: FeedItem, todo: TodoItem?) -> Outcome {
        if item.failed { return .failed }
        if item.turnInterrupted { return .interrupted(reason: APIErrorInfo.parse(item.receiptError ?? item.summary)?.reason) }
        if let apiError = APIErrorInfo.parse(item.summary) { return .interrupted(reason: apiError.reason) }
        if let todo, todo.doneAt == nil { return .stopped }
        return .done
    }

    /// The task's name on its receipt (#3036): its to-do's title now, so a rename (the user's, or the model's a moment
    /// after the task was handed out) shows on every receipt, as on the card and lines beside it; the receipt's own title
    /// (the name when it was written) once the to-do is gone from the list.
    nonisolated static func title(_ item: FeedItem, todo: TodoItem?) -> String {
        if let now = todo?.title.trimmingCharacters(in: .whitespacesAndNewlines), !now.isEmpty { return now }
        return item.title ?? String(localized: "Tasks")
    }

    var body: some View {
        switch item.interruptLook {
        case .hidden: EmptyView() // drawn inside its batch's card (#3834)
        case .batch(let batch): InterruptBatchCard(batch: batch)
        case .resolved(let resolution):
            InterruptHandledLine(todoID: item.ref, title: Self.title(item, todo: item.ref.flatMap { store.todos[$0] }), resolution: resolution)
        case .pending, nil: card
        }
    }

    @ViewBuilder private var card: some View {
        let todo = item.ref.flatMap { store.todos[$0] }
        let outcome = Self.outcome(item, todo: todo)
        let title = Self.title(item, todo: todo)
        let tint: Color = switch outcome {
        case .failed, .interrupted: Theme.danger
        case .stopped: .secondary
        case .done: Theme.positive
        }
        HStack(alignment: .top, spacing: Theme.Space.m) {
            ThemedIcon(Self.icon(outcome))
                .font(Theme.Font.heading)
                .foregroundStyle(tint)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text(Self.heading(outcome, title: title))
                    .font(Theme.Font.bodyStrong)
                if let s = Self.detail(outcome, summary: item.summary) {
                    // The Agent's last words as Markdown, not raw `**` (#2363).
                    Text(ExchangeText.linked(s)).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(4)
                }
                HStack(spacing: Theme.Space.s) {
                    if let pr = item.pr, let url = URL(string: pr), let label = item.prLabel {
                        Button { openURL(url) } label: { Label(label, systemImage: Theme.Icon.pullRequest).lineLimit(1) }
                            .themedButton()
                    }
                    if let id = item.ref, todo != nil, outcome != .failed {
                        Button { store.detailTask = id } label: { Text("View [Task Views d87059]").lineLimit(1) }
                            .themedButton()
                    }
                }
                .font(Theme.Font.captionStrong)
                .padding(.top, Theme.Space.xs)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(Theme.Space.l)
        .background(outcome == .stopped ? Theme.Fill.well : tint.opacity(0.12),
                    in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityIdentifier("receipt.\(Self.outcomeKey(outcome))")
    }

    nonisolated static func icon(_ outcome: Outcome) -> String {
        switch outcome {
        case .failed, .interrupted: Theme.Icon.errorFilled
        case .stopped: Theme.Icon.time
        case .done: Theme.Icon.successFilled
        }
    }

    nonisolated static func heading(_ outcome: Outcome, title: String) -> String {
        switch outcome {
        case .failed: String(localized: "\(title) didn't start")
        case .interrupted: String(localized: "Task “\(title)” was interrupted")
        case .stopped: String(localized: "\(title) stopped, not done yet")
        case .done: String(localized: "\(title) is done [Task Views cd37f3]")
        }
    }

    /// The line under the heading: the agent's last words, or for an interrupted turn the reason in words rather than
    /// the raw API error.
    nonisolated static func detail(_ outcome: Outcome, summary: String?) -> String? {
        if case .interrupted(let reason) = outcome {
            if let reason, !reason.isEmpty { return String(localized: "Interrupted by a model service error: \(reason)") }
            return String(localized: "Interrupted by a model service error")
        }
        guard let s = summary, !s.isEmpty else { return nil }
        return s
    }

    nonisolated static func outcomeKey(_ outcome: Outcome) -> String {
        switch outcome {
        case .done: "done"
        case .stopped: "stopped"
        case .failed: "failed"
        case .interrupted: "interrupted"
        }
    }
}

// MARK: - Interruptions after the fact (#3834)

/// The words for how the hub says an interrupted task was dealt with.
nonisolated enum InterruptWords {
    static func resolution(_ r: InterruptResolution) -> String {
        switch r {
        case .pending: String(localized: "Pending [interrupt resolution]")
        case .cancelled: String(localized: "Cancelled [interrupt resolution]")
        case .rerouted: String(localized: "Dispatched again [interrupt resolution]")
        case .resumed: String(localized: "Resumed [interrupt resolution]")
        case .accepted: String(localized: "Accepted [interrupt resolution]")
        case .done: String(localized: "Done [interrupt resolution]")
        }
    }

    /// What cut a batch off (the hub's `cause`); one this build doesn't know reads as a model service error.
    static func cause(_ c: String) -> String {
        switch c {
        case "rate_limited": String(localized: "Model service rate limit")
        case "auth": String(localized: "Sign-in or key refused")
        case "model_route": String(localized: "Unknown model [interrupt cause]")
        case "no_output": String(localized: "Turns with no output")
        default: String(localized: "Model service error")
        }
    }

    static func title(_ b: InterruptBatch) -> String {
        let what = cause(b.cause)
        if let machine = b.machine, !machine.isEmpty {
            return String(localized: "\(what) on \(machine): \(b.total) tasks interrupted")
        }
        return String(localized: "\(what): \(b.total) tasks interrupted")
    }

    static func counts(_ b: InterruptBatch) -> String {
        b.pending > 0 ? String(localized: "→ \(b.resolved) handled / \(b.pending) pending") : String(localized: "→ all handled")
    }
}

/// An interrupted task that was dealt with since: a grey line with its result; it opens the task.
struct InterruptHandledLine: View {
    var todoID: String?
    var title: String
    var resolution: InterruptResolution
    private var store: TasksStore { .shared }

    var body: some View {
        let known = todoID.map { store.todos[$0] != nil } ?? false
        Button { if let todoID { store.detailTask = todoID } } label: {
            Label(String(localized: "“\(title)” was interrupted · \(InterruptWords.resolution(resolution))"), systemImage: Theme.Icon.infoFilled)
                .lineLimit(1)
                .font(Theme.Font.secondary)
                .foregroundStyle(.secondary)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .buttonStyle(.plain)
        .disabled(!known)
        .accessibilityIdentifier("receipt.interrupt.handled")
    }
}

/// Several tasks one outage cut off: one card with its cause and how many are handled and still pending, that opens
/// to each task and its result. Red only while one is pending.
struct InterruptBatchCard: View {
    var batch: InterruptBatch
    @State private var open = false
    private var store: TasksStore { .shared }

    var body: some View {
        let pending = batch.pending > 0
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Button { withAnimation(Theme.Motion.reveal) { open.toggle() } } label: {
                HStack(alignment: .top, spacing: Theme.Space.m) {
                    ThemedIcon(Theme.Icon.errorFilled)
                        .font(Theme.Font.heading)
                        .foregroundStyle(pending ? Theme.danger : .secondary)
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        Text(InterruptWords.title(batch))
                            .font(Theme.Font.bodyStrong)
                            .foregroundStyle(pending ? Color.primary : Color.secondary)
                        Text(InterruptWords.counts(batch))
                            .font(Theme.Font.secondary)
                            .foregroundStyle(.secondary)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    ThemedIcon(open ? Theme.Icon.expand : Theme.Icon.disclosure)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.tertiary)
                }
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            if open {
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    ForEach(batch.tasks, id: \.receipt) { task in
                        let todo = store.todos[task.todoID]
                        Button { store.detailTask = task.todoID } label: {
                            HStack(spacing: Theme.Space.s) {
                                Text(todo?.title ?? task.title).lineLimit(1)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                Text(InterruptWords.resolution(task.resolved)).lineLimit(1)
                                    .foregroundStyle(task.resolved == .pending ? Theme.danger : .secondary)
                            }
                            .font(Theme.Font.secondary)
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .disabled(todo == nil)
                    }
                }
                .transition(.opacity)
            }
        }
        .padding(Theme.Space.l)
        .background(pending ? Theme.danger.opacity(0.12) : Theme.Fill.well,
                    in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityIdentifier(pending ? "receipt.interrupt.batch.pending" : "receipt.interrupt.batch.handled")
    }
}

// MARK: - Above the composer

/// What the conversation's tasks are asking, at the bottom where the user is: the oldest question under its task's
/// name, answered where it is; the next comes up when it is done (「还有 N 件」 counts them).
struct TaskQuestions: View {
    var conversation: String
    private var store: TasksStore { .shared }

    var body: some View {
        let asks = store.tasks(of: conversation)
            .flatMap { todo in store.interactions(todo).map { (todo: todo, ix: $0) } }
            .sorted { ($0.ix.createdAt ?? .distantPast) < ($1.ix.createdAt ?? .distantPast) }
        if let first = asks.first {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                HStack(spacing: Theme.Space.s) {
                    Label(first.todo.title, systemImage: Theme.Icon.askFirst).lineLimit(1)
                    Spacer(minLength: Theme.Space.s)
                    if asks.count > 1 { Text("\(asks.count - 1) more").lineLimit(1).fixedSize() }
                }
                .font(Theme.Font.captionStrong)
                .foregroundStyle(AgentState.needsYou.tone.text)
                .padding(.horizontal, Theme.Space.xs)
                InteractionPanel(interaction: first.ix)
            }
            .id(first.ix.id)
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }
}

/// The capsule the row above the assistant's composer draws an entry in: the action bar's Needs You / In Progress (#3393)
/// and its trays' All, the look the dock's All had (#1094), so the bar is exactly as low as the dock was:
/// caption medium (13 pt at the default text size; it follows Dynamic Type), 12 pt sideways and 6 pt up and down
/// (`Theme.Space.m` / `.sm`), a capsule, `Theme.Fill.chip` behind secondary words: 28 pt tall at the default text size.
/// `.selected` (its tray is open) is the filter chip's on look in the same size (`FilterChip`: the accent behind white,
/// semibold); `.tone` is the dock's waiting pill (`DockPillLabel`: the state's tint behind its words, semibold).
struct DockCapsule: ViewModifier {
    enum Look: Equatable {
        case plain, selected
        case waiting(AgentState)
    }

    var look: Look = .plain

    func body(content: Content) -> some View {
        content
            .font(look == .plain ? Theme.Font.captionMedium : Theme.Font.captionStrong)
            .foregroundStyle(words)
            .lineLimit(1)
            .padding(.horizontal, Theme.Space.m)
            .padding(.vertical, Theme.Space.sm)
            .background(fill, in: .capsule)
    }

    private var words: AnyShapeStyle {
        switch look {
        case .plain: AnyShapeStyle(.secondary)
        case .selected: AnyShapeStyle(Color.white)
        case .waiting(let state): AnyShapeStyle(state.tone.text)
        }
    }

    private var fill: AnyShapeStyle {
        switch look {
        case .plain: AnyShapeStyle(Theme.Fill.chip)
        case .selected: AnyShapeStyle(Theme.accent)
        case .waiting(let state): AnyShapeStyle(state.tone.tint)
        }
    }
}

extension View {
    /// An entry of the row above the assistant's composer (`DockCapsule`).
    func dockCapsule(_ look: DockCapsule.Look = .plain) -> some View { modifier(DockCapsule(look: look)) }
}

/// A dock pill's face, the same for a task and an agent: its marks, the title (which gives way), and Needs You tinted with
/// the state's tone while it waits for the user.
struct DockPillLabel<Marks: View>: View {
    var title: String
    var state: AgentState
    var waiting: Bool
    var maxWidth: CGFloat = Theme.Size.box200
    @ViewBuilder var marks: Marks

    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            marks
            Text(title).lineLimit(1)
            if waiting { Text(StateName.needsYou.text).lineLimit(1).fixedSize() } // the title gives way, the ask does not
        }
        .font(Theme.Font.captionStrong)
        .foregroundStyle(waiting ? state.tone.text : .primary)
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.sm)
        .background(waiting ? state.tone.tint : Theme.Fill.chip, in: .capsule)
        // Not `scaledFrame(maxWidth:)`: the dock's ScrollView offers no width, so a long title took its full width
        // and its capsule spilled over the pills beside it (#3010).
        .scaledWidthCap(maxWidth)
    }
}

/// Message “xx” above the composer while the next message goes straight to a task; × sends it to Persona again.
struct RouteChip: View {
    var conversation: String
    private var store: TasksStore { .shared }

    var body: some View {
        if let id = store.routes[conversation], let todo = store.todos[id] {
            HStack(spacing: Theme.Space.xs) {
                Label("Message “\(todo.title)”", systemImage: Theme.Icon.routeToTask).lineLimit(1)
                Button {
                    withAnimation(Theme.Motion.state) { store.routes[conversation] = nil }
                } label: {
                    ThemedIcon(Theme.Icon.close).font(Theme.Font.labelStrong)
                        .scaledFrame(width: Theme.Size.badge, height: Theme.Size.badge)
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Hand to Persona")
            }
            .font(Theme.Font.captionStrong)
            .foregroundStyle(Theme.accent)
            .padding(.leading, Theme.Space.m)
            .padding(.trailing, Theme.Space.xs)
            .padding(.vertical, Theme.Space.xs)
            .background(Theme.accent.opacity(0.14), in: .capsule)
            .frame(maxWidth: .infinity, alignment: .leading)
            .transition(.move(edge: .bottom).combined(with: .opacity))
        }
    }
}

/// The @ list: the conversation's tasks; picking one routes the next message to it.
struct TaskMentionList: View {
    var conversation: String
    var pick: (TodoItem) -> Void
    private var store: TasksStore { .shared }

    var body: some View {
        let tasks = store.tasks(of: conversation).filter { store.state($0) != .done && store.state($0) != .failed }
        VStack(alignment: .leading, spacing: 0) {
            Text(tasks.isEmpty ? String(localized: "No tasks in this chat yet") : String(localized: "Message a Task"))
                .font(Theme.Font.sectionHeader)
                .foregroundStyle(.secondary)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.s)
            ForEach(tasks) { todo in
                Button { pick(todo) } label: {
                    HStack(spacing: Theme.Space.s) {
                        TaskMark(state: store.state(todo)).scaledFrame(width: Theme.Size.markM)
                        Text(todo.title).font(Theme.Font.body).lineLimit(1)
                        Spacer(minLength: Theme.Space.s)
                        Text(TaskPlace.line(todo, lead: nil)).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.s)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
        }
        .padding(.vertical, Theme.Space.xs)
        .surface(in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous))
        .transition(.move(edge: .bottom).combined(with: .opacity))
    }

    /// The @ the draft ends with ("@", "@login"), if any.
    static func mention(in draft: String) -> Range<String.Index>? {
        guard let at = draft.lastIndex(of: "@") else { return nil }
        let rest = draft[draft.index(after: at)...]
        guard !rest.contains(where: \.isWhitespace) else { return nil }
        if at > draft.startIndex, !draft[draft.index(before: at)].isWhitespace { return nil } // an address, not a mention
        return at..<draft.endIndex
    }
}

extension View {
    /// The shape a long press lifts for the context menu's preview (iOS). The Mac's right-click menu has no preview,
    /// and `ContentShapeKinds.contextMenuPreview` does not exist there (#1083).
    @ViewBuilder func menuPreviewShape(_ shape: some Shape) -> some View {
        #if os(iOS) || os(visionOS)
        contentShape(.contextMenuPreview, shape)
        #else
        self
        #endif
    }
}
