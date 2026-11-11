import BeautifulUI
import MarkdownUI
import PersonaKit
import SwiftUI

/// How each Claude Code tool is named and summarized in the chat.
enum ToolPresentation {
    nonisolated static func baseName(_ name: String) -> String {
        if name.hasPrefix("mcp__") { return name.split(separator: "__").last.map(String.init) ?? name }
        return name
    }

    static func symbol(_ name: String) -> String {
        if ToolNaming.isOtherMCP(name) { return Theme.Icon.skills }
        let n = baseName(name)
        if n.hasPrefix("phone_") { return Theme.Icon.phone }
        if n.hasPrefix("mail_") { return Theme.Icon.mail }
        switch n {
        case "conversation_rename": return Theme.Icon.edit
        case "conversation_archive": return Theme.Icon.archive
        case "conversation_unarchive": return Theme.Icon.unarchive
        case "conversations_list", "conversation_read", "conversation_new", "conversation_send": return Theme.Icon.conversation
        case "computer_state", "computer_run": return Theme.Icon.machine
        case SecretRun.tool: return Theme.Icon.credentials
        case "browser_run", "browser_look": return Theme.Icon.web
        case "iphone_status", "iphone_look", "iphone_do": return Theme.Icon.phoneControl
        case "Bash", "BashOutput", "PowerShell": return Theme.Icon.terminal
        case "Read", "NotebookRead": return Theme.Icon.document
        case "Edit", "MultiEdit", "NotebookEdit": return Theme.Icon.editFile
        case "Write": return Theme.Icon.newFile
        case "Grep": return Theme.Icon.findText
        case "Glob", "LS": return Theme.Icon.findFiles
        case "WebFetch": return Theme.Icon.web
        case "WebSearch": return Theme.Icon.search
        case "Task", "Agent", "ListAgents", "SubagentHandback": return Theme.Icon.subagent
        case "TodoWrite", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet": return Theme.Icon.todo
        case "TaskStop", "KillShell", "KillBash": return Theme.Icon.stopLoop
        case "TaskOutput": return Theme.Icon.backgroundTask
        case "SendMessage": return Theme.Icon.agentChat
        case "ScheduleWakeup": return Theme.Icon.schedule
        case "CronCreate", "CronList", "CronDelete", "RemoteTrigger": return Theme.Icon.recurring
        case "LSP": return Theme.Icon.code
        case "EnterWorktree", "ExitWorktree": return Theme.Icon.gitBranch
        case "SendUserFile": return Theme.Icon.attachment
        case "Artifact": return Theme.Icon.share
        case "PushNotification": return Theme.Icon.notification
        case "ReportFindings": return Theme.Icon.triage
        case "SendFeedback": return Theme.Icon.compose
        case "EndConversation": return Theme.Icon.end
        case "AskUserQuestion", "ask_user": return Theme.Icon.question
        case "ExitPlanMode", "EnterPlanMode": return Theme.Icon.plan
        case "ToolSearch": return Theme.Icon.unknownTool
        case "Skill", "WaitForMcpServers", "ListMcpResourcesTool", "ReadMcpResourceTool": return Theme.Icon.skills
        case "Monitor": return Theme.Icon.monitor
        case "Workflow": return Theme.Icon.workflow
        default: return name.hasPrefix("mcp__") ? Theme.Icon.skills : Theme.Icon.unknownTool
        }
    }

    static func verb(_ name: String) -> String {
        if let own = PersonaToolNames.verb(name) { return own }
        // Another server's tool: "GitHub · Create issue", never its bare snake_case name (#2986).
        if let mcp = ToolNaming.mcpLabel(name) { return mcp }
        let n = baseName(name)
        if n.hasPrefix("phone_") { return "iPhone" }
        if n.hasPrefix("mail_") { return String(localized: "Mail [Assistant View 3d708e]") }
        return knownVerb(n) ?? ToolNaming.humanize(n)
    }

    /// The verb the tables give a tool's own name; nil for one they don't know.
    static func knownVerb(_ n: String) -> String? {
        switch n {
        // Persona managing its own conversations (docs/assistant/assistant.md「Managing its own conversations」).
        case "conversations_list": String(localized: "List Chats")
        case "conversation_read": String(localized: "Read Chat")
        case "conversation_rename": String(localized: "Rename Chat")
        case "conversation_archive": String(localized: "Archive Chat")
        case "conversation_unarchive": String(localized: "Unarchive")
        case "conversation_new": String(localized: "New Chat [Tool Views 3f0830]")
        case "conversation_send": String(localized: "Send to Chat")
        // Operating a Mac through Cua Driver (docs/extensions/computer-use.md).
        case "computer_state": String(localized: "View Computer")
        case "computer_run": String(localized: "Control Computer")
        case "browser_run": String(localized: "Use Browser")
        case "browser_look": String(localized: "View Browser")
        // Operating an iPhone through a Mac it is paired with (docs/extensions/iphone.md).
        case "iphone_status": String(localized: "Check iPhones")
        case "iphone_look": String(localized: "View iPhone")
        case "iphone_do": String(localized: "Operate iPhone")
        case "Bash", "PowerShell": String(localized: "Run")
        case "Read", "NotebookRead": String(localized: "Read [Tool Views 534cb3]")
        case "Edit", "MultiEdit", "NotebookEdit": String(localized: "Edit [Message Menu 051836]")
        case "Write": String(localized: "Write")
        case "Grep": String(localized: "Search")
        case "Glob", "LS": String(localized: "Find")
        case "WebFetch": String(localized: "Open Web Page")
        case "WebSearch": String(localized: "Web Search")
        case "Task", "Agent": String(localized: "Subtask")
        case "TodoWrite": String(localized: "Update To-dos")
        // Claude Code's task list (TodoWrite's successor) and its background tasks.
        case "TaskCreate": String(localized: "Add To-Do")
        case "TaskUpdate": String(localized: "Update To-Do")
        case "TaskList": String(localized: "List To-Dos")
        case "TaskGet": String(localized: "Read To-Do")
        case "TaskStop", "KillShell", "KillBash": String(localized: "Stop Task")
        case "TaskOutput", "BashOutput": String(localized: "Read Output")
        // Other agents and sessions (cross-session messaging, agent teams).
        case "SendMessage": String(localized: "Message Agent")
        case "ListAgents": String(localized: "List Agents")
        case "SubagentHandback": String(localized: "Hand Back Report")
        // Coming back later: a /loop's next round, prompts on a schedule here or in the cloud.
        case "ScheduleWakeup": String(localized: "Schedule Wake-Up")
        case "CronCreate": String(localized: "Schedule Task")
        case "CronList": String(localized: "List Scheduled Tasks")
        case "CronDelete": String(localized: "Delete Scheduled Task [Persona Tool Names 24bbfb]")
        case "RemoteTrigger": String(localized: "Cloud Scheduled Task")
        case "LSP": String(localized: "Look Up Code")
        case "EnterWorktree": String(localized: "Enter Worktree")
        case "ExitWorktree": String(localized: "Leave Worktree")
        // What reaches the user: a file, a published page, a notification, a review's findings.
        case "SendUserFile": String(localized: "Send File")
        case "Artifact": String(localized: "Publish Artifact")
        case "PushNotification": String(localized: "Notify You")
        case "ReportFindings": String(localized: "Report Findings")
        case "SendFeedback": String(localized: "Draft Feedback")
        case "EndConversation": String(localized: "End Conversation")
        case "AskUserQuestion", "ask_user": String(localized: "Question [Tool Views 387441]")
        case "ExitPlanMode": String(localized: "Submit Plan")
        case "EnterPlanMode": String(localized: "Enter Plan Mode")
        case "ToolSearch": String(localized: "Load Tools")
        case "WaitForMcpServers": String(localized: "Wait for MCP Servers")
        case "ListMcpResourcesTool": String(localized: "List MCP Resources")
        case "ReadMcpResourceTool": String(localized: "Read MCP Resource")
        case "Skill": String(localized: "Use Skill")
        case "Monitor": String(localized: "Monitor [Tool Views 22be42]")
        case "Workflow": String(localized: "Run Workflow [Tool Views]")
        default: nil
        }
    }

    /// The verb of this call: a few tools say what they did from their input (a to-do completed, a notebook cell
    /// added, a language server asked for references); the rest as `verb(_:)`. A run's chips group by `verb(_:)`.
    static func verb(_ call: ToolCall) -> String {
        let i = call.input
        let mode = { (key: String) in i[key]?.string ?? "" }
        switch ToolNaming.tableName(call.name) {
        case "TaskUpdate":
            switch mode("status") {
            case "in_progress": return String(localized: "Start To-Do")
            case "completed": return String(localized: "Complete To-Do")
            case "deleted": return String(localized: "Delete To-Do")
            default: break
            }
        case "NotebookEdit":
            switch mode("edit_mode") {
            case "insert": return String(localized: "Add Cell")
            case "delete": return String(localized: "Delete Cell")
            default: return String(localized: "Edit Cell")
            }
        case "LSP":
            switch mode("operation") {
            case "goToDefinition": return String(localized: "Go to Definition")
            case "goToImplementation": return String(localized: "Find Implementations")
            case "findReferences": return String(localized: "Find References")
            case "hover": return String(localized: "Inspect Symbol")
            case "documentSymbol": return String(localized: "List Symbols")
            case "workspaceSymbol": return String(localized: "Find Symbol")
            case "prepareCallHierarchy", "incomingCalls", "outgoingCalls": return String(localized: "Call Hierarchy")
            default: break
            }
        case "ScheduleWakeup":
            if i["stop"]?.bool == true { return String(localized: "Stop Loop") }
        case "ExitWorktree":
            if mode("action") == "remove" { return String(localized: "Remove Worktree") }
        case "Artifact":
            if mode("action") == "list" { return String(localized: "List Artifacts") }
        default: break
        }
        return verb(call.name)
    }

    /// Where a tool's subject gives way when it does not fit: a Mac's steps, a browser script, a plan (which step it is
    /// on) and words (a description, a title, a message, a command line) read from their start (the end goes); one word
    /// (a path, a pattern, an id) keeps its end, where the file name is (the middle goes).
    static func truncation(_ name: String, subject: String = "") -> Text.TruncationMode {
        if ComputerSteps.isComputerTool(name) || ["browser_run", "browser_look", "iphone_look", "iphone_do", "TodoWrite"].contains(baseName(name)) {
            return .tail
        }
        return subject.contains(where: \.isWhitespace) ? .tail : .middle
    }

    /// `machines`: who the computer tools' `machine` is (AppModel's when nil).
    static func subject(_ call: ToolCall, machines: [Machine]? = nil) -> String {
        let i = call.input
        if ToolNaming.isOtherMCP(call.name) { return ToolNaming.subject(i) }
        let n = baseName(call.name)
        func s(_ k: String) -> String? { i[k]?.string.flatMap { $0.isEmpty ? nil : $0 } }
        func mac() -> String? { s("machine").map { ComputerSteps.machineName($0, in: machines ?? AppModel.shared.machines) } }
        func file(_ k: String) -> String? { s(k).map(ToolNaming.lastPathComponent) }
        switch n {
        case "Bash", "PowerShell": return s("description") ?? firstLine(s("command") ?? "")
        case "Read", "Edit", "MultiEdit", "Write", "NotebookEdit", "NotebookRead":
            return ((s("file_path") ?? s("notebook_path") ?? "") as NSString).lastPathComponent
        case "Grep": return s("pattern") ?? ""
        case "Glob": return s("pattern") ?? ""
        case "WebFetch": return URL(string: s("url") ?? "")?.host() ?? s("url") ?? ""
        case "WebSearch": return s("query") ?? ""
        case "Task", "Agent": return s("description") ?? ""
        case "Monitor": return s("description") ?? s("command").map(firstLine) ?? i["ws"]?["url"]?.string ?? ""
        case SecretRun.tool: return SecretRunRequest(i)?.subject ?? ""
        // The node's name for its run: its `name`, the name in its script's meta, else its script file's (#3436).
        case "Workflow": return s("name") ?? workflowName(script: s("script")) ?? s("scriptPath").map(workflowScriptName) ?? ""
        case "Skill": return s("skill") ?? s("command") ?? ""
        case "conversations_list": return s("query") ?? ""
        case "conversation_rename": return [s("kind"), s("topic")].compactMap { $0 }.joined(separator: " | ")
        case "conversation_new", "conversation_send": return firstLine(s("text") ?? "")
        case "conversation_read", "conversation_archive", "conversation_unarchive": return String((s("id") ?? "").prefix(8))
        // "select:WebFetch,Monitor" loads those two by name.
        case "ToolSearch":
            let q = s("query") ?? ""
            return q.hasPrefix("select:") ? q.dropFirst(7).split(separator: ",").joined(separator: ", ") : q
        case "computer_state": return [mac(), s("app") ?? String(localized: "Entire screen")].compactMap { $0 }.joined(separator: " · ")
        case "browser_look": return mac() ?? ""
        case "browser_run": return [mac(), s("script").map { firstLine($0.trimmingCharacters(in: .whitespacesAndNewlines)) }].compactMap { $0 }.joined(separator: " · ")
        case "computer_run": return [mac(), ComputerSteps.describe(call.name, input: i)].compactMap { $0 }.joined(separator: " · ")
        case "iphone_status": return ""
        case "iphone_look": return s("phone") ?? s("query") ?? ""
        case "iphone_do": return [s("phone"), IPhoneSteps.describe(i)].compactMap { $0 }.joined(separator: " · ")
        // ask_user is the Persona assistant's own question tool (docs/assistant/assistant.md); it takes the same questions.
        case "AskUserQuestion", "ask_user": return i["questions"]?.array?.first?["question"]?.string ?? ""
        case "ExitPlanMode": return PlanText.title(s("plan") ?? "")
        case "TodoWrite": return PlanProgress.summary(ChatPlan(callID: call.id, todos: ChatPlan.todos(i["todos"]?.array ?? [])))
        // A task-list call names its to-do; an update without one, its number (the hub resolves them in folds).
        case "TaskCreate": return s("subject") ?? firstLine(s("description") ?? "")
        case "TaskUpdate": return s("subject") ?? s("activeForm") ?? s("taskId").map { "#" + $0 } ?? ""
        case "TaskGet": return s("taskId").map { "#" + $0 } ?? ""
        case "TaskList", "ListAgents", "CronList", "ExitWorktree": return ""
        case "TaskStop", "KillShell", "KillBash", "TaskOutput", "BashOutput":
            return s("task_id") ?? s("shell_id") ?? s("bash_id") ?? ""
        // Who it went to and what about: Claude's own one-line summary, else the message's first line.
        case "SendMessage":
            let what = s("summary") ?? (s("message") ?? s("content")).map(firstLine) ?? i["message"]?["type"]?.string.map(ToolNaming.humanize)
            return [s("to") ?? s("recipient"), what].compactMap { $0 }.joined(separator: " · ")
        case "ScheduleWakeup":
            if let why = s("reason") { return firstLine(why) }
            let start = Date(timeIntervalSinceReferenceDate: 0)
            return i["delaySeconds"]?.double.map { WorkLine.duration(from: start, to: start.addingTimeInterval($0)) } ?? ""
        case "CronCreate": return [s("cron"), s("prompt").map(firstLine)].compactMap { $0 }.joined(separator: " · ")
        case "CronDelete": return s("id") ?? ""
        case "RemoteTrigger": return [s("action").map(ToolNaming.humanize), s("trigger_id")].compactMap { $0 }.joined(separator: " · ")
        // Where it looked: the symbol searched for, else the file and line.
        case "LSP":
            if s("operation") == "workspaceSymbol", let q = s("query") { return q }
            return [file("filePath"), s("line")].compactMap { $0 }.joined(separator: ":")
        case "EnterWorktree": return s("name") ?? file("path") ?? ""
        case "SendUserFile":
            let listed = (i["files"] ?? i["file_paths"] ?? i["paths"])?.array ?? []
            let paths = listed.compactMap { $0.string ?? $0["path"]?.string ?? $0["file_path"]?.string } + [s("file_path"), s("path")].compactMap { $0 }
            if !paths.isEmpty { return paths.map(ToolNaming.lastPathComponent).joined(separator: ", ") }
            return s("caption").map(firstLine) ?? ToolNaming.subject(i)
        case "Artifact": return s("title") ?? file("file_path") ?? s("url") ?? ""
        case "PushNotification": return firstLine(s("message") ?? "")
        // One finding by what it says; several by how many.
        case "ReportFindings":
            let findings = i["findings"]?.array ?? []
            if findings.count == 1 { return firstLine(findings[0]["short_summary"]?.string ?? findings[0]["summary"]?.string ?? "") }
            return findings.isEmpty ? "" : String(localized: "\(findings.count) findings")
        case "WaitForMcpServers":
            let servers = (i["servers"] ?? i["server_names"])?.array?.compactMap(\.string) ?? []
            return servers.isEmpty ? s("server") ?? "" : servers.joined(separator: ", ")
        case "ListMcpResourcesTool": return s("server") ?? ""
        case "ReadMcpResourceTool": return s("uri") ?? s("server") ?? ""
        default:
            if n.hasPrefix("phone_") || n.hasPrefix("mail_") { return n.split(separator: "_").dropFirst().joined(separator: " ") }
            return ToolNaming.subject(i)
        }
    }

    /// The `name: '…'` a workflow script's meta gives it (the node reads it the same way, `wfNameRe`).
    static func workflowName(script: String?) -> String? {
        guard let script, let m = script.firstMatch(of: #/\bname:\s*['"`]([^'"`\n]+)['"`]/#) else { return nil }
        return String(m.1)
    }

    /// A workflow's name from its script file, as the node has it (`scriptName`): the file's name without ".js" and
    /// the run id Claude Code adds ("…/parity-audit-wf_c1d9daf8.js" is "parity-audit"), so the call's row says what
    /// the rows, the header and the notifications call the run.
    nonisolated static func workflowScriptName(_ path: String) -> String {
        var base = path.split(omittingEmptySubsequences: false, whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? path
        if base.hasSuffix(".js") { base.removeLast(3) }
        if let r = base.range(of: "-wf_"), r.lowerBound > base.startIndex { base = String(base[..<r.lowerBound]) }
        return base
    }

    static func firstLine(_ s: String) -> String {
        s.split(separator: "\n", maxSplits: 1, omittingEmptySubsequences: true).first.map(String.init) ?? s
    }
}

/// A run of tool calls (and thoughts), collapsed until the user opens it.
struct ToolGroupView: View {
    var steps: [ToolStep]
    /// Every step listed, no "N steps" fold (inside a line that was already tapped open).
    var flat = false
    @State private var expanded = false
    @Environment(\.chatPin) private var pin

    private var calls: [ToolCall] { steps.compactMap { if case .call(let c) = $0 { c } else { nil } } }
    private var pinKey: String { "group-" + (steps.first?.id ?? "") }
    @State private var unfold = Unfolding()

    var body: some View {
        let calls = calls
        VStack(alignment: .leading, spacing: Theme.Space.sm) {
            if !steps.isEmpty && !expanded && !flat {
                Button {
                    toggleInPlace(pin, pinKey, animation: Theme.Motion.standard, unfold: $unfold) { expanded = true }
                } label: {
                    HStack(spacing: Theme.Space.sm) {
                        ThemedIcon(Theme.Icon.space).font(Theme.Font.label)
                        Text("\(calls.count) steps").font(Theme.Font.labelMedium)
                        Text(summary(calls)).font(Theme.Font.label).foregroundStyle(.tertiary).lineLimit(1)
                        Spacer(minLength: 0)
                        ThemedIcon(Theme.Icon.expand).font(Theme.Font.labelStrong)
                    }
                    .foregroundStyle(.secondary)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.vertical, Theme.Space.s)
                    .background(Theme.Fill.well, in: .capsule)
                }
                .buttonStyle(.plain)
            } else {
                ForEach(steps) { step in
                    switch step {
                    case .call(let c): ToolCallRow(call: c, autoExpand: false)
                    case .thought(let t):
                        if t.kind == .notice { NoticeRow(item: t) } else { ThinkingRow(item: t, compact: true) }
                    case .folded(let f): FoldedWorkRow(fold: f)
                    }
                }
                .unfolding(unfold)
                if !steps.isEmpty && !flat {
                    Button("Collapse") { toggleInPlace(pin, pinKey, animation: Theme.Motion.standard, unfold: $unfold, closing: true) { expanded = false } }
                        .font(Theme.Font.labelMedium)
                        .padding(.leading, Theme.Space.m)
                }
            }
        }
        .chatPinned(pinKey) // its top: the "N steps" line closed, the first step open
    }

    private func summary(_ calls: [ToolCall]) -> String {
        var counts: [String: Int] = [:]
        var order: [String] = []
        let work = calls.filter(ToolNaming.speaks)
        for c in work.isEmpty ? calls : work {
            let v = ToolPresentation.verb(c.name)
            if counts[v] == nil { order.append(v) }
            counts[v, default: 0] += 1
        }
        return order.prefix(3).map { "\($0) \(counts[$0]!)" }.joined(separator: " · ")
    }
}

/// Two or more tool calls in a row of an open work line, as one line of chips (BeautifulUI's `ToolChipGroup`):
/// "Search hourly · Read 3 files · Edit Provider.swift +6 −3". Tapping opens a row per call under it (`ChatView.runLines`),
/// where each call opens to its input and output as before. One text layout per run, however long the run.
struct ToolRunRow: View, Equatable {
    nonisolated let calls: [ToolCall]
    nonisolated let open: Bool
    nonisolated let first: Bool
    var toggle: () -> Void

    nonisolated static func == (lhs: Self, rhs: Self) -> Bool {
        lhs.calls == rhs.calls && lhs.open == rhs.open && lhs.first == rhs.first
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ToolRunRow")
        #endif
        Button(action: toggle) {
            ToolChipGroup(ToolRunSummary.chips(calls), isOpen: open, isRunning: calls.contains(where: \.isRunning))
                .padding(.horizontal, WorkCardSlice.inset)
                .padding(.vertical, Theme.Space.s)
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background { WorkCardSlice(top: first, bottom: false) }
        .overlay(alignment: .top) { if !first { WorkCardSlice.separator } }
        .accessibilityHint(open ? String(localized: "Collapse \(calls.count) steps [Tool Views 05f314]") : String(localized: "Expand \(calls.count) steps"))
        .accessibilityIdentifier("tool-run")
    }
}

/// A run's chips: one per kind of tool, in the order first used. One call names its subject ("Read WeatherStore.swift"),
/// several count ("Read 3 files"); edits to one file carry its +N −N; a failed call turns its chip red.
enum ToolRunSummary {
    private enum Kind: Hashable { case read, search, edit, run, todo, other(String) }

    private static func kind(_ name: String) -> Kind {
        switch ToolNaming.tableName(name) {
        case "Read", "NotebookRead": .read
        case "Grep", "Glob", "LS", "WebSearch": .search
        case "Edit", "MultiEdit", "Write", "NotebookEdit": .edit
        case "Bash", "PowerShell": .run
        case "TodoWrite": .todo
        default: .other(ToolPresentation.verb(name))
        }
    }

    static func chips(_ calls: [ToolCall]) -> [ToolChipGroup.Chip] {
        var order: [Kind] = []
        var groups: [Kind: [ToolCall]] = [:]
        // Bookkeeping (loading tools, reading the to-do list) says nothing about the work: a chip only when it failed or
        // is all the run did. Opened, the run still lists every call.
        let work = calls.filter(ToolNaming.speaks)
        for c in work.isEmpty ? calls : work {
            let k = kind(c.name)
            if groups[k] == nil { order.append(k) }
            groups[k, default: []].append(c)
        }
        return order.map { k in
            var c = chip(k, groups[k]!)
            // A chip is one line of label and detail: its middle is the detail's only when the detail is the longer of
            // the two, or a middle cut takes the verb ("Co…o-Do #1").
            c.truncatesTail = ToolPresentation.truncation(groups[k]![0].name, subject: c.detail ?? "") == .tail
                || (c.detail ?? "").count <= c.label.count
            return c
        }
    }

    private static func chip(_ kind: Kind, _ calls: [ToolCall]) -> ToolChipGroup.Chip {
        let first = calls[0]
        let error = calls.contains(where: \.failed)
        let one = calls.count == 1
        let subject = ToolPresentation.subject(first)
        let ascii = subject.unicodeScalars.allSatisfy(\.isASCII)
        func named(_ label: String, many: String) -> ToolChipGroup.Chip {
            ToolChipGroup.Chip(symbol: Theme.Icon.styled(ToolPresentation.symbol(first.name)), label: label, detail: one ? subject : many,
                               monospacedDetail: one && ascii, isError: error)
        }
        switch kind {
        case .read: return named(String(localized: "Read [Tool Views 534cb3]"), many: String(localized: "\(calls.count) files"))
        case .search: return named(ToolPresentation.verb(first.name), many: String(localized: "\(calls.count) times"))
        case .run: return named(String(localized: "Run"), many: String(localized: "\(calls.count) commands"))
        case .todo:
            // The latest list is the state of the to-dos.
            return ToolChipGroup.Chip(symbol: Theme.Icon.todo, label: String(localized: "To-dos"), detail: ToolPresentation.subject(calls[calls.count - 1]),
                                      monospacedDetail: true, isError: error)
        case .other(let verb):
            // One call says what it did, as its row does ("Complete To-Do"); several, what they share.
            return ToolChipGroup.Chip(symbol: Theme.Icon.styled(ToolPresentation.symbol(first.name)), label: one ? ToolPresentation.verb(first) : verb,
                                      detail: one ? subject : String(localized: "\(calls.count) times"), monospacedDetail: one && ascii, isError: error)
        case .edit:
            var adds = 0, dels = 0
            var files: [String] = []
            for c in calls {
                let d = diff(c)
                adds += d.adds
                dels += d.dels
                let f = c.input["file_path"]?.string ?? c.input["notebook_path"]?.string ?? ""
                if !files.contains(f) { files.append(f) }
            }
            return ToolChipGroup.Chip(
                symbol: Theme.Icon.styled(ToolPresentation.symbol(first.name)),
                label: files.count == 1 ? ToolPresentation.verb(first.name) : String(localized: "Edit [Message Menu 051836]"),
                detail: files.count == 1 ? (files[0] as NSString).lastPathComponent : String(localized: "\(files.count) files"),
                monospacedDetail: files.count == 1,
                additions: adds, deletions: dels, isError: error
            )
        }
    }

    /// The same counts the open call's diff shows (`ToolDiffMemo`, warmed off the main thread with the rows).
    static func diff(_ c: ToolCall) -> (adds: Int, dels: Int) {
        let i = c.input
        switch ToolPresentation.baseName(c.name) {
        case "Edit":
            if let diff = i["diff"]?.string { return ToolDiffMemo.unifiedCounts(diff) }
            return ToolDiffMemo.editCounts(old: i["old_string"]?.string ?? "", new: i["new_string"]?.string ?? "")
        case "MultiEdit":
            return (i["edits"]?.array ?? []).reduce((0, 0)) { n, e in
                let d = ToolDiffMemo.editCounts(old: e["old_string"]?.string ?? "", new: e["new_string"]?.string ?? "")
                return (n.0 + d.adds, n.1 + d.dels)
            }
        case "Write":
            return ToolDiffMemo.editCounts(old: "", new: i["content"]?.string ?? "")
        case "NotebookEdit":
            // The call carries only the cell's new source: its lines count as added; a deleted cell's are unknown.
            return (i["edit_mode"]?.string == "delete" ? 0 : ToolNaming.lineCount(i["new_source"]?.string ?? ""), 0)
        default:
            return (0, 0)
        }
    }
}

struct ToolCallRow: View {
    @Environment(\.taskIndex) private var tasks
    var call: ToolCall
    var autoExpand: Bool
    /// A row of an open work line's card (`WorkCardSlice` draws the card): no gray tile of its own, 44 pt tall.
    var grouped = false
    var headerID = "chat.tool-call"
    @State private var expanded: Bool?
    @Environment(\.openTools) private var openTools
    @Environment(\.dynamicTypeSize) private var typeSize

    private var isOpen: Bool {
        openTools?.isOpen(call.id) ?? expanded ?? (autoExpand || (ResultBlock.debugFull && call.result?.fullSize != nil)
            || Self.debugDetails)
    }

    /// Screenshots (docs/apple/mac.md, docs/operations/testing.md): `-showToolDetails YES` opens every tool row (diffs, Bash output,
    /// an answered ask_user) without a tap.
    #if DEBUG
    static let debugDetails = UserDefaults.standard.bool(forKey: "showToolDetails")
    #else
    static let debugDetails = false
    #endif

    private func setOpen(_ open: Bool) {
        if let openTools { openTools.set(call.id, open) } else { expanded = open }
    }

    @Environment(\.chatPin) private var pin
    @Environment(\.openPlan) private var openPlan
    private var pinKey: String { "call-" + call.id }

    /// A tap on its head: a plan opens the plan sheet, any other call opens or folds its details.
    private func tapped() {
        Haptics.tap()
        if ToolPresentation.baseName(call.name) == "TodoWrite", let openPlan { return openPlan() }
        toggleInPlace(pin, pinKey, animation: Theme.Motion.state, unfold: $unfold, closing: isOpen) { setOpen(!isOpen) }
    }
    @State private var unfold = Unfolding()

    var body: some View {
        let base = ToolPresentation.baseName(call.name)
        VStack(alignment: .leading, spacing: 0) {
            Group { if AppStyle.current.usesBeautifulUI { chipHeader } else { header(base) } }
                .chatPinned(pinKey)

            if base == "TodoWrite", openPlan != nil {
                // The plan in one line: how far along, as a thin bar; its whole list in the plan sheet (#5602).
                let todos = ChatPlan.todos(call.input["todos"]?.array ?? [])
                PlanBar(done: todos.filter { $0.status == "completed" }.count, total: todos.count)
                    .padding(.leading, AppStyle.current.usesBeautifulUI ? pad : detailLeading)
                    .padding(.trailing, pad)
                    .padding(.bottom, Theme.Space.m)
            } else if base == "TodoWrite" {
                if AppStyle.current.usesBeautifulUI {
                    TodoList(todos: call.input["todos"]?.array ?? []).padding(.horizontal, pad).padding(.bottom, Theme.Space.m)
                } else {
                    TodoList(todos: call.input["todos"]?.array ?? []).padding(.leading, detailLeading).padding(.trailing, pad).padding(.bottom, Theme.Space.s)
                }
            } else if isOpen {
                ToolDetail(call: call)
                    .padding(.leading, detailLeading)
                    .padding(.trailing, pad)
                    .padding(.bottom, Theme.Space.m)
                    .unfolding(unfold)
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .background(grouped ? Color.clear : Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
    }

    private var pad: CGFloat { grouped ? WorkCardSlice.inset : 10 }
    /// On the work timeline what a tool shows keeps right of the rail, under its words.
    private var detailLeading: CGFloat { grouped && WorkCardSlice.timeline ? WorkCardSlice.textColumn : pad }
    private var onTimeline: Bool { grouped && WorkCardSlice.timeline }

    /// Around a chip row (`ToolChipRow`): in a work card it lines up with the card's other rows; alone, a little
    /// tighter. Thoughts and background notices in a card use the same (`ThinkingRow`, `TaskNoteRow`).
    static func chipInsets(grouped: Bool) -> EdgeInsets {
        grouped ? EdgeInsets(top: 5, leading: 9, bottom: 5, trailing: 9) : EdgeInsets(top: 2, leading: 7, bottom: 2, trailing: 7)
    }

    private var tint: Color { call.failed ? Theme.danger : call.isRunning ? Theme.busy : .secondary }

    /// The row's head: a round kind icon, the verb, the subject, a background task's badge and the chevron.
    private func header(_ base: String) -> some View {
        Button(action: tapped) {
            HStack(spacing: Theme.Space.m) {
                ZStack {
                    // On the timeline the icon sits on the rail: the page under its tint hides the line behind it.
                    if onTimeline { Circle().fill(Theme.pageBackground).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon) }
                    Circle().fill(tint.opacity(0.14)).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                    if call.isRunning {
                        ProgressView().controlSize(.mini).tint(tint)
                    } else {
                        ThemedIcon(ToolPresentation.symbol(call.name))
                            .font(Theme.Font.labelStrong).dynamicTypeSize(...DynamicTypeSize.xLarge)
                            .foregroundStyle(tint)
                    }
                }
                if typeSize.isAccessibilitySize {
                    // At the accessibility sizes the subject and a background task's badge each get a line under the
                    // verb (as `NameWithChips` stacks a row's chips): side by side, the badge, which keeps its width,
                    // left the verb nothing and ran off the screen, and the subject was cut to a sliver of a letter.
                    let subject = ToolPresentation.subject(call)
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        verbText.lineLimit(2)
                        if !subject.isEmpty { subjectText(subject) }
                        taskBadge
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                } else {
                    // One line: the verb first ("GitHub · Create issue" is long), the subject gives way.
                    verbText
                        .lineLimit(1)
                        .layoutPriority(1)
                    subjectText(ToolPresentation.subject(call))
                    Spacer(minLength: 4)
                    taskBadge
                }
                ToolOutcomeMark(call: call)
                ThemedIcon(Theme.Icon.disclosure)
                    .font(Theme.Font.labelStrong)
                    .foregroundStyle(.tertiary)
                    .rotationEffect(.degrees(isOpen ? 90 : 0))
            }
            .padding(.vertical, onTimeline ? Theme.Space.s : grouped ? 10 : 7)
            .padding(.horizontal, pad)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityIdentifier(headerID)
    }

    private var verbText: Text {
        Text(ToolPresentation.verb(call)).font(onTimeline ? Theme.Font.secondaryMedium : Theme.Font.secondaryStrong)
    }

    private func subjectText(_ subject: String) -> some View {
        Text(subject)
            .font(Theme.Font.secondary)
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .truncationMode(ToolPresentation.truncation(call.name, subject: subject))
    }

    /// The row's head: BeautifulUI's tool row — kind icon (a chevron once open), the verb, the subject in a chip. A call
    /// with nothing to name (Enter Plan Mode, List Agents) draws no empty box beside its verb.
    private var chipHeader: some View {
        let subject = ToolPresentation.subject(call)
        let stacked = typeSize.isAccessibilitySize
        return VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            ToolChipRow(chip(subject), isOpen: isOpen, isRunning: call.isRunning, chipStyle: subject.isEmpty ? .plain : .boxed,
                        action: tapped) {
                if !stacked { taskBadge }
                ToolOutcomeMark(call: call)
            }
            .accessibilityIdentifier(headerID)
            // At the accessibility sizes the badge, which keeps its width, left the verb nothing beside it: it gets a
            // line of its own under the verb, past the row's 16 pt icon and its 8 pt gap.
            if stacked { taskBadge.padding(.leading, Theme.Space.xl) }
        }
        .padding(Self.chipInsets(grouped: grouped))
    }

    /// A call that went to the background says how its task went (Background · Done).
    @ViewBuilder private var taskBadge: some View {
        if let id = call.use.toolUseID, tasks.started.contains(id) || tasks.notes[id] != nil,
           !SubagentCard.isAgent(call) || tasks.started.contains(id) || call.input["run_in_background"]?.bool == true {
            let b = TaskNoteText.badge(tasks.notes[id] ?? [])
            let text = Text(b.text).font(Theme.Font.label).foregroundStyle(b.failed ? Theme.danger : Color.secondary)
            if AppStyle.current.usesBeautifulUI {
                BUIPill(b.text, tone: b.failed ? .red : .neutral, truncates: typeSize.isAccessibilitySize)
            } else if typeSize.isAccessibilitySize {
                // On a line of its own it can still be wider than the screen: then it wraps, in a box rather than a pill.
                ViewThatFits(in: .horizontal) {
                    badgePill(text)
                    text.fixedSize(horizontal: false, vertical: true)
                        .padding(.horizontal, Theme.Space.sm).padding(.vertical, Theme.Space.xxs)
                        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                }
            } else {
                badgePill(text)
            }
        }
    }

    private func badgePill(_ text: some View) -> some View {
        text.lineLimit(1).fixedSize()
            .padding(.horizontal, Theme.Space.sm).padding(.vertical, Theme.Space.xxs)
            .background(Theme.Fill.well, in: .capsule)
    }

    private func chip(_ subject: String) -> BeautifulUI.ToolCall {
        BeautifulUI.ToolCall(
            id: call.id,
            .symbol(Theme.Icon.styled(ToolPresentation.symbol(call.name))),
            label: ToolPresentation.verb(call),
            chip: subject,
            // Paths, patterns and commands in the code face; a description someone wrote (often Chinese) in the text face.
            monospacedChip: subject.unicodeScalars.allSatisfy(\.isASCII)
        )
    }
}

struct ToolDetail: View {
    @Environment(\.rowMemoScope) private var memoScope
    var call: ToolCall
    @Environment(\.openFile) private var openFile

    var body: some View {
        if ComputerSteps.isComputerTool(call.name) {
            ComputerToolDetail(call: call, input: ToolTextMemo.input(call.use, scope: memoScope)) // in words (#1323)
        } else {
            detail
        }
    }

    @ViewBuilder private var detail: some View {
        let base = ToolNaming.tableName(call.name)
        let i = call.input
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            switch base {
            case "Bash", "PowerShell", "Monitor":
                CodeBlock(text: ToolNaming.commandLine(call.name, i) ?? "", maxLines: 12)
            case "Edit":
                // Codex: the patch as a unified diff; Claude: the old and new strings.
                let key = "diff-" + call.id
                let diff = i["diff"]?.string.map { DiffView(unified: $0, pinKey: key) }
                    ?? DiffView(old: i["old_string"]?.string ?? "", new: i["new_string"]?.string ?? "", pinKey: key)
                diffHeader(i["file_path"]?.string, [diff])
                diff
            case "MultiEdit":
                let diffs = (i["edits"]?.array ?? []).enumerated().map { n, e in
                    DiffView(old: e["old_string"]?.string ?? "", new: e["new_string"]?.string ?? "", pinKey: "diff-\(call.id)-\(n)")
                }
                diffHeader(i["file_path"]?.string, diffs)
                ForEach(Array(diffs.enumerated()), id: \.offset) { _, d in d }
            case "Write":
                let diff = DiffView(old: "", new: i["content"]?.string ?? "", pinKey: "diff-" + call.id)
                diffHeader(i["file_path"]?.string, [diff])
                diff
            case "Read", "NotebookRead":
                path(i["file_path"]?.string ?? i["notebook_path"]?.string)
            case "NotebookEdit":
                path(i["notebook_path"]?.string ?? i["file_path"]?.string)
                // Which cell, and what it holds now (a deleted cell has nothing new).
                let cell = [i["cell_id"]?.string, i["cell_type"]?.string].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
                if !cell.isEmpty { Text(cell).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(1) }
                if i["edit_mode"]?.string != "delete", let source = i["new_source"]?.string, !source.isEmpty {
                    CodeBlock(text: source, maxLines: 12)
                }
            case "ReportFindings":
                FindingsList(findings: i["findings"]?.array ?? [])
            case "ExitPlanMode":
                Markdown(MarkdownMemo.content(i["plan"]?.string ?? "")).markdownTheme(.persona)
                    #if os(macOS)
                    .textSelection(.enabled)
                    #endif
            case "AskUserQuestion":
                ForEach(Array((i["questions"]?.array ?? []).enumerated()), id: \.offset) { _, q in
                    Text(q["question"]?.string ?? "").font(Theme.Font.captionMedium)
                        #if os(macOS)
                        .textSelection(.enabled)
                        #endif
                }
            case "ask_user":
                AskUserDetail(call: call)
            case SecretRun.tool:
                SecretRunToolDetail(call: call)
            case "Task", "Agent":
                Text(i["prompt"]?.string ?? "").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(8)
            default:
                if i != .object([:]) { ToolArgumentsDetail(item: call.use, scope: memoScope) }
            }
            // An answered ask_user shows the picks under its questions (AskUserDetail), not the text the model got; a
            // password run its commands' outcomes (SecretRunToolDetail).
            if (base == "Agent" || base == "Task"), !call.results.isEmpty {
                let reports = call.results.filter { !SubagentDelivery.isReceipt($0) && !($0.text ?? "").isEmpty }
                ForEach(reports) { report in
                    ResultBlock(text: report.cutText, isError: report.isError ?? false, cut: report.fullSize.map { (report.id, $0) },
                                outcome: report.outcome)
                }
                if reports.isEmpty, let result = call.result {
                    ResultBlock(text: "", isError: result.isError ?? false, outcome: result.outcome)
                }
            } else if let result = call.result, (base != "Edit" && base != "ask_user" && base != SecretRun.tool) || result.isError == true {
                ResultBlock(text: result.cutText, isError: result.isError ?? false, cut: result.fullSize.map { (result.id, $0) },
                            outcome: result.outcome)
                if base == "Bash" || base == "PowerShell" { PortChips(text: result.cutText) } // a dev server saying where it listens
            } else if call.isRunning {
                HStack(spacing: Theme.Space.sm) {
                    ProgressView().controlSize(.mini)
                    Text("Running…").font(Theme.Font.label).foregroundStyle(.secondary)
                }
            }
            ForEach(Array(call.images.enumerated()), id: \.offset) { i, img in Base64Image(payload: img, gallery: call.images, index: i) }
        }
    }

    @ViewBuilder private func diffHeader(_ p: String?, _ diffs: [DiffView]) -> some View {
        if let p, !p.isEmpty {
            DiffFileHeader(path: p, adds: diffs.reduce(0) { $0 + $1.adds }, dels: diffs.reduce(0) { $0 + $1.dels })
        }
    }

    @ViewBuilder private func path(_ p: String?) -> some View {
        if let p, !p.isEmpty {
            if let openFile {
                Button { openFile(p) } label: {
                    HStack(spacing: Theme.Space.xs) {
                        ThemedIcon(FileViewer.symbol(for: p)).font(Theme.Font.label)
                        Text(p).lineLimit(2).truncationMode(.head)
                        ThemedIcon(Theme.Icon.openDocument).font(Theme.Font.label)
                    }
                    .font(Theme.Font.code)
                    .foregroundStyle(Theme.accent)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("View file \((p as NSString).lastPathComponent)")
            } else {
                Text(p).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(2).truncationMode(.head).textSelection(.enabled)
            }
        }
    }
}

/// The Raw Input switch under a tool's detail in words, showing or hiding the JSON the tool was called with.
struct RawInputToggle: View {
    @Binding var raw: Bool

    var body: some View {
        Button {
            withAnimation(Theme.Motion.state) { raw.toggle() }
        } label: {
            Label(raw ? String(localized: "Hide Raw Input") : String(localized: "Raw Input"), systemImage: raw ? Theme.Icon.collapse : Theme.Icon.expand)
                .font(Theme.Font.label).lineLimit(1)
        }
        .buttonStyle(.plain)
        .foregroundStyle(.secondary)
    }
}

/// A tool no table knows (another server's MCP tool, a new one), its input as a key/value list (`ToolNaming.arguments`)
/// rather than a JSON dump (#2986); the JSON itself under Raw Input, as a computer tool keeps it (`ComputerToolDetail`).
struct ToolArgumentsDetail: View {
    var item: Item
    var scope: String
    @State private var raw = false

    var body: some View {
        let arguments = ToolTextMemo.arguments(item, scope: scope)
        if arguments.isEmpty {
            CodeBlock(text: ToolTextMemo.input(item, scope: scope), maxLines: 14)
        } else {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Theme.Space.m, verticalSpacing: Theme.Space.xs) {
                    ForEach(arguments) { a in
                        GridRow {
                            // A cap, not a flexible frame: `scaledFrame(maxWidth:)` grows to its whole maximum whenever it
                            // is offered more, so the label column always took 120 pt and short labels ("Title") cut the
                            // values early. The cap makes the column as wide as its widest label, up to 120 pt.
                            Text(a.label).font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1)
                                .scaledWidthCap(Theme.Size.box120)
                            Text(a.value).font(a.code ? Theme.Font.code : Theme.Font.caption)
                                .lineLimit(a.lines)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .textSelection(.enabled)
                        }
                    }
                }
                .accessibilityIdentifier("tool-arguments")
                RawInputToggle(raw: $raw).accessibilityIdentifier("tool-raw")
                if raw { CodeBlock(text: ToolTextMemo.input(item, scope: scope), maxLines: 14) }
            }
        }
    }
}

/// Claude Code's ReportFindings: each finding's place (a tap opens its file) and what it says.
struct FindingsList: View {
    var findings: [JSONValue]
    @Environment(\.openFile) private var openFile

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            ForEach(Array(findings.enumerated()), id: \.offset) { _, f in
                let file = f["file"]?.string ?? ""
                let place = [ToolNaming.lastPathComponent(file), f["line"]?.string ?? ""].filter { !$0.isEmpty }.joined(separator: ":")
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    if let openFile, !file.isEmpty {
                        Button { openFile(file) } label: {
                            Text(place).font(Theme.Font.code).foregroundStyle(Theme.accent).lineLimit(1).truncationMode(.middle)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("View file \(ToolNaming.lastPathComponent(file))")
                    } else if !place.isEmpty {
                        Text(place).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                    }
                    Text(f["summary"]?.string ?? "").font(Theme.Font.caption).fixedSize(horizontal: false, vertical: true)
                }
            }
        }
    }
}

nonisolated enum ToolTextMemo {
    private static let inputs = TextMemo<String>(limit: 512)
    private static let lines = TextMemo<[String]>(limit: 256)
    private static let argumentRows = TextMemo<[ToolNaming.Argument]>(limit: 512)

    /// The call's input as shown, once per call — keyed by its chat and id, as `RowMemo` is (ids repeat across chats).
    static func input(_ item: Item, scope: String) -> String {
        inputs(RowMemo.key(scope, item.id)) { _ in (item.toolInput ?? .object([:])).pretty }
    }

    static func codeLines(_ text: String) -> [String] { lines(text) { $0.components(separatedBy: "\n") } }

    /// An unknown tool's input as its detail lists it (`ToolArgumentsDetail`), once per call, keyed as `input`.
    static func arguments(_ item: Item, scope: String) -> [ToolNaming.Argument] {
        argumentRows(RowMemo.key(scope, item.id)) { _ in ToolNaming.arguments(item.toolInput ?? .object([:])) }
    }

    static func prewarm(_ item: Item, scope: String) {
        if item.kind == .toolResult { _ = codeLines(item.cutText); return }
        let input = item.toolInput ?? .object([:])
        let name = item.toolName ?? ""
        switch ToolNaming.tableName(name) {
        case "Bash", "PowerShell", "Monitor": _ = codeLines(ToolNaming.commandLine(name, input) ?? "")
        case "NotebookEdit": _ = codeLines(input["new_source"]?.string ?? "")
        case "ExitPlanMode": _ = MarkdownMemo.content(input["plan"]?.string ?? "")
        case "Edit", "MultiEdit", "Write", "Read", "NotebookRead", "AskUserQuestion", "ask_user", "Task", "Agent", "ReportFindings",
             SecretRun.tool: break
        default:
            // A computer tool shows its steps in words and its JSON under Raw Input; any other its arguments.
            if ComputerSteps.isComputerTool(name) { _ = codeLines(self.input(item, scope: scope)) } else { _ = arguments(item, scope: scope) }
        }
    }
}

/// What the user picked in an ask_user card, read back from the tool's result: the hub answers the model with
/// "The user answered:\n- <question> → <answer>\n" per question (server/internal/assistant/tools.go), and with "The user did
/// not answer …" when the card was dismissed or expired. A multi-select answer joins its labels with ", ".
nonisolated enum AskUserAnswers {
    struct Question: Equatable, Sendable {
        var question: String
        var options: [String]
        var multiSelect: Bool
    }

    static func questions(_ input: JSONValue) -> [Question] {
        (input["questions"]?.array ?? []).map { q in
            Question(question: q["question"]?.string ?? "",
                     options: (q["options"]?.array ?? []).compactMap { $0["label"]?.string },
                     multiSelect: q["multiSelect"]?.bool ?? false)
        }
    }

    /// Each question's answer; empty when the result says nobody answered (or is not the hub's answer at all).
    static func parse(_ result: String, questions: [Question]) -> [String: String] {
        guard result.hasPrefix("The user answered:") else { return [:] }
        let marks = questions.compactMap { q -> (String, Range<String.Index>)? in
            result.range(of: "\n- \(q.question) → ").map { (q.question, $0) }
        }.sorted { $0.1.lowerBound < $1.1.lowerBound }
        var out: [String: String] = [:]
        for (n, (question, mark)) in marks.enumerated() {
            let end = n + 1 < marks.count ? marks[n + 1].1.lowerBound : result.endIndex
            let answer = result[mark.upperBound..<end].trimmingCharacters(in: .whitespacesAndNewlines)
            if !answer.isEmpty { out[question] = answer }
        }
        return out
    }

    /// Whether an option is among what was picked.
    static func picked(_ label: String, answer: String?, multiSelect: Bool) -> Bool {
        guard let answer else { return false }
        if answer == label { return true }
        return multiSelect && answer.components(separatedBy: ", ").contains(label)
    }

    /// An answer typed instead of picking an option.
    static func typed(_ q: Question, answer: String?) -> String? {
        guard let answer, !q.options.contains(where: { picked($0, answer: answer, multiSelect: q.multiSelect) }) else { return nil }
        return answer
    }
}

/// The assistant's ask_user once opened: each question with its options, what the user picked ticked. The card itself
/// is gone once answered, so this is where the conversation keeps what was asked and what came back.
struct AskUserDetail: View {
    var call: ToolCall

    var body: some View {
        let questions = AskUserAnswers.questions(call.input)
        let answers = call.result.map { AskUserAnswers.parse($0.cutText, questions: questions) } ?? [:]
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            ForEach(Array(questions.enumerated()), id: \.offset) { _, q in
                let answer = answers[q.question]
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text(q.question).font(Theme.Font.captionMedium)
                    ForEach(Array(q.options.enumerated()), id: \.offset) { _, label in
                        choice(label, picked: AskUserAnswers.picked(label, answer: answer, multiSelect: q.multiSelect))
                    }
                    if let typed = AskUserAnswers.typed(q, answer: answer) { choice(typed, picked: true) }
                }
            }
            if let result = call.result, result.isError != true, answers.isEmpty {
                Text("No answer").font(Theme.Font.label).foregroundStyle(.secondary)
            }
        }
        .accessibilityIdentifier("ask-user-detail")
    }

    private func choice(_ label: String, picked: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            ThemedIcon(picked ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                .font(Theme.Font.label)
                .foregroundStyle(picked ? Theme.accent : Color.secondary)
            Text(label).font(Theme.Font.caption).foregroundStyle(picked ? Color.primary : Color.secondary)
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(picked ? .isSelected : [])
    }
}

struct CodeBlock: View {
    var text: String
    var maxLines: Int = 20
    /// Clipped, the last `maxLines` lines show instead of the first (an output that ends with why it failed).
    var keepsEnd = false
    @State private var full = false

    var body: some View {
        let lines = ToolTextMemo.codeLines(text)
        let clipped = !full && lines.count > maxLines
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ScrollView(.horizontal, showsIndicators: false) {
                Text(clipped ? (keepsEnd ? lines.suffix(maxLines) : lines.prefix(maxLines)).joined(separator: "\n") : text)
                    .font(Theme.Font.code)
                    .textSelection(.enabled)
                    .padding(Theme.Space.m)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.codeBackground, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .macHoverActions {
                MacHoverButton(title: String(localized: "Copy [Readiness Views d37380]"), symbol: Theme.Icon.copy) { MacHover.copy(text) }
            }
            if clipped {
                Button("Show All \(lines.count) Lines [Tool Views 3aed77]") { withAnimation { full = true } }.font(Theme.Font.labelMedium)
            }
        }
    }
}

struct ResultBlock: View {
    var text: String
    var isError: Bool
    /// The page carries only the start of a long output: its item id and whole size.
    var cut: (id: String, size: Int)? = nil
    /// What the call came to, when the node said: the line over the output, and a cut output's end.
    var outcome: ToolOutcome? = nil
    @Environment(\.loadFullText) private var loadFullText
    @State private var showFull = false

    /// Simulator automation: `-showFullOutput card` opens the cut tool outputs, `sheet` also the first one's whole text.
    #if DEBUG
    static let debugFull = UserDefaults.standard.string(forKey: "showFullOutput") != nil
    static let debugSheet = UserDefaults.standard.string(forKey: "showFullOutput") == "sheet"
    @MainActor static var debugFullShown = false
    #else
    static let debugFull = false
    #endif

    var body: some View {
        let head = ToolOutcomeText.headline(outcome)
        if text.isEmpty && cut == nil {
            let failed = head?.failed ?? (head == nil && isError)
            Text(head?.text ?? (isError ? String(localized: "Failed") : String(localized: "Done, no output")))
                .font(Theme.Font.label).foregroundStyle(failed ? Theme.danger : .secondary)
        } else {
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                // Above the output: over it, the mark covered the end of the first line.
                if let head {
                    Label(head.text, systemImage: head.symbol).font(Theme.Font.labelStrong)
                        .foregroundStyle(head.failed ? Theme.danger : .secondary)
                        .lineLimit(1)
                } else if isError {
                    Label("Something Went Wrong", systemImage: Theme.Icon.errorFilled).font(Theme.Font.labelStrong).foregroundStyle(Theme.danger)
                        .lineLimit(1)
                }
                CodeBlock(text: text, maxLines: 10)
                // The page kept the output's start; why a command failed is usually at its end.
                if cut != nil, let tail = outcome?.tail, !tail.isEmpty {
                    Text("End of output").font(Theme.Font.labelMedium).foregroundStyle(.secondary)
                        .padding(.top, Theme.Space.xs)
                    CodeBlock(text: tail, maxLines: 10, keepsEnd: true)
                }
                if let cut {
                    Button { showFull = true } label: {
                        Label("Truncated. View full text (\(Int64(cut.size).storageText))",
                              systemImage: Theme.Icon.text)
                            .lineLimit(1)
                            .minimumScaleFactor(0.8)
                    }
                    .font(Theme.Font.labelMedium)
                    .sheet(isPresented: $showFull) { FullOutputSheet(itemID: cut.id, load: loadFullText) }
                    #if DEBUG
                    .task {
                        // Taken only by a card still there a second later: the first one drawn can be rebuilt away
                        // (a work line's steps arriving) and took the only turn without showing anything.
                        guard Self.debugSheet, !Self.debugFullShown else { return }
                        try? await Task.sleep(for: .seconds(1))
                        guard !Task.isCancelled, !Self.debugFullShown else { return }
                        Self.debugFullShown = true
                        showFull = true
                    }
                    #endif
                }
            }
        }
    }
}

/// A tool's whole output, fetched when opened (pages carry its first 4 KB). Monospaced, scrolls both ways.
struct FullOutputSheet: View {
    var itemID: String
    var load: ((String) async throws -> String)?
    @Environment(\.dismiss) private var dismiss
    @State private var text: String?
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Group {
                if let text {
                    MonoTextView(text: MonoText.plain(text, color: .label, size: 12))
                } else if let error {
                    ContentUnavailableView("Couldn't Load", systemImage: Theme.Icon.error, description: Text(error))
                } else {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                }
            }
            .background(Theme.codeBackground)
            .navigationTitle("Tool Output")
            .navigationBarTitleDisplayMode(.inline)
            .pageToolbar {
                #if os(macOS)
                // A Mac sheet has no navigation bar: only its bottom row of actions is drawn, and the leading item that
                // maps to `.navigation` never showed (#743). Copy All goes in that row, before Done.
                ToolbarItem(placement: .cancellationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1) }
                if let text {
                    ToolbarItem(placement: .primaryAction) {
                        Button { UIPasteboard.general.string = text } label: { Text("Copy All [Tool Views 1c5144]").lineLimit(1) }
                    }
                }
                #else
                ToolbarItem(placement: .confirmationAction) { Button("Done [Extension System Presentation c0b3fb]") { dismiss() }.lineLimit(1) }
                if let text {
                    ToolbarItem(placement: .topBarLeading) {
                        Button { UIPasteboard.general.string = text } label: { Label("Copy All [Tool Views 1c5144]", systemImage: Theme.Icon.copy) }
                    }
                }
                #endif
            }
            #if os(macOS)
            // Sized to its bars a Mac sheet was 470 × 113, the text squeezed to nothing (#508): a reader wants the room.
            .macLargeSheet(bars: true)
            #endif
        }
        .task {
            guard text == nil, let load else { return }
            do { text = try await load(itemID) } catch { if let msg = error.shown { self.error = msg } }
            #if DEBUG
            if ResultBlock.debugFull, let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first {
                // What View Full Text got, for checking against the transcript (`-showFullOutput sheet`).
                try? (text ?? "ERROR: \(error ?? "")").write(to: dir.appending(path: "full-output.txt"), atomically: true, encoding: .utf8)
                try? itemID.write(to: dir.appending(path: "full-output-id.txt"), atomically: true, encoding: .utf8)
            }
            #endif
        }
    }
}

/// The file a diff is in: name, folder, +N −N; a tap opens it where the page opens files (`openFile`). The chat's edits
/// (`ToolDetail`) and a file tool's permission card (`PermissionDiffView`) draw the same one.
struct DiffFileHeader: View {
    var path: String
    var adds: Int
    var dels: Int
    @Environment(\.openFile) private var openFile

    var body: some View {
        let header = HStack(spacing: Theme.Space.sm) {
            ThemedIcon(FileViewer.symbol(for: path)).font(Theme.Font.label).foregroundStyle(.secondary)
            Text((path as NSString).lastPathComponent).font(Theme.Font.captionStrong).foregroundStyle(.primary).lineLimit(1)
                .layoutPriority(1) // the folder gives way first: at the largest sizes the two cut each other to a letter
            Text((path as NSString).deletingLastPathComponent).font(Theme.Font.caption).foregroundStyle(.secondary)
                .lineLimit(1).truncationMode(.head)
            Spacer(minLength: 4)
            if adds > 0 { Text("+\(adds)").foregroundStyle(Theme.Diff.add) }
            if dels > 0 { Text("−\(dels)").foregroundStyle(Theme.Diff.del) }
            if openFile != nil { ThemedIcon(Theme.Icon.openDocument).font(Theme.Font.label).foregroundStyle(Theme.accent) }
        }
        .font(Theme.Font.captionStrong.monospacedDigit())
        if let openFile {
            Button { openFile(path) } label: { header.contentShape(.rect) }
                .buttonStyle(.plain)
                .accessibilityLabel("View file \((path as NSString).lastPathComponent), \(adds) added, \(dels) removed")
        } else {
            header
        }
    }
}

/// The chat's one diff: Edit / MultiEdit / Write (old and new text, compared line by line and word by word with
/// `LineDiff`, #2915) and Codex's patches (a unified diff), on the Git page's line model (`DiffText.Line`,
/// `DiffText.parse`; the Git page draws whole files with MonoTextView). Only the sign and the line's background are
/// colored, and the words that changed in a changed line; the code stays label. Long lines scroll sideways and a fade
/// on the right says there is more. Long diffs show their start (an edit's long stretches of unchanged lines folded)
/// and Show All.
nonisolated enum ToolDiffMemo {
    struct Content: Sendable {
        var lines: [DiffText.Line]
        /// What shows until Show All: an edit's long stretches of unchanged lines folded into one row each.
        var short: [DiffText.Line]
        var adds: Int
        var dels: Int
    }

    private enum Input: Hashable { case edit(String, String), unified(String) }
    nonisolated(unsafe) private static var values: [Input: Content] = [:]
    /// Every diff's +N −N, apart from `values` and many more of them: an open work line's chips and changed files
    /// (`ToolRunSummary.diff`) count all of its edits each time the chat's lines are rebuilt. Read from `values`, a
    /// turn with more edits than it holds emptied it on every pass and compared every edit again on the main thread.
    nonisolated(unsafe) private static var counts: [Input: (adds: Int, dels: Int)] = [:]
    private static let lock = NSLock()

    private static func cached(_ input: Input, make: () -> Content) -> Content {
        lock.lock()
        if let value = values[input] { lock.unlock(); return value }
        lock.unlock()
        let value = make()
        lock.lock()
        if values.count >= 64 { values.removeAll(keepingCapacity: true) }
        values[input] = value
        remember(input, (value.adds, value.dels))
        lock.unlock()
        return value
    }

    private static func counted(_ input: Input, make: () -> (adds: Int, dels: Int)) -> (adds: Int, dels: Int) {
        lock.lock()
        if let n = counts[input] { lock.unlock(); return n }
        lock.unlock()
        let n = make()
        lock.lock()
        remember(input, n)
        lock.unlock()
        return n
    }

    /// Under `lock`. A long session's worth (a few small values, and the texts the items hold anyway); then start over.
    private static func remember(_ input: Input, _ n: (adds: Int, dels: Int)) {
        if counts.count >= 4000 { counts.removeAll(keepingCapacity: true) }
        counts[input] = n
    }

    /// The +N −N `edit(old:new:)` shows, without its lines: the work line's chips and changed files.
    static func editCounts(old: String, new: String) -> (adds: Int, dels: Int) {
        counted(.edit(old, new)) { LineDiff.counts(old: old, new: new) }
    }

    /// The +N −N `unified(_:)` shows, without keeping its lines.
    static func unifiedCounts(_ text: String) -> (adds: Int, dels: Int) {
        counted(.unified(text)) { tally(DiffText.parse(text)) }
    }

    private static func tally(_ lines: [DiffText.Line]) -> (adds: Int, dels: Int) {
        var adds = 0, dels = 0
        for line in lines {
            if line.kind == .add { adds += 1 }
            if line.kind == .del { dels += 1 }
        }
        return (adds, dels)
    }

    static func edit(old: String, new: String) -> Content {
        cached(.edit(old, new)) {
            let diff = LineDiff(old: old, new: new)
            var lines: [DiffText.Line] = []
            lines.reserveCapacity(diff.lines.count)
            for (n, l) in diff.lines.enumerated() {
                let kind: DiffText.Line.Kind = switch l.kind {
                case .add: .add
                case .del: .del
                case .context: .context
                }
                lines.append(DiffText.Line(id: n, kind: kind, text: l.text, old: l.oldLine, new: l.newLine, changed: l.changed))
            }
            // A folded stretch takes the id of its first line, which it does not show.
            let short: [DiffText.Line] = diff.shown().map { shown in
                switch shown {
                case .line(let n): lines[n]
                case .folded(let r): DiffText.Line(id: r.lowerBound, kind: .hunk, text: String(localized: "… \(r.count) unchanged lines"))
                }
            }
            return Content(lines: lines, short: short, adds: diff.adds, dels: diff.dels)
        }
    }

    static func unified(_ text: String) -> Content {
        cached(.unified(text)) {
            let lines = DiffText.parse(text)
            let n = tally(lines)
            return Content(lines: lines, short: lines, adds: n.adds, dels: n.dels)
        }
    }

    static func prewarm(_ item: Item) {
        let input = item.toolInput ?? .object([:])
        switch ToolPresentation.baseName(item.toolName ?? "") {
        case "Edit":
            if let diff = input["diff"]?.string { _ = unified(diff) }
            else { _ = edit(old: input["old_string"]?.string ?? "", new: input["new_string"]?.string ?? "") }
        case "MultiEdit":
            for edit in input["edits"]?.array ?? [] {
                _ = self.edit(old: edit["old_string"]?.string ?? "", new: edit["new_string"]?.string ?? "")
            }
        case "Write": _ = edit(old: "", new: input["content"]?.string ?? "")
        default: break
        }
    }
}

struct DiffView: View {
    var lines: [DiffText.Line]
    /// Until Show All (`ToolDiffMemo.Content.short`), at most `maxLines` of it.
    var short: [DiffText.Line]
    /// The chat's tool rows show 40 rows until Show All; a permission card fewer, so its buttons stay in view.
    var maxLines = 40
    var adds: Int
    var dels: Int
    /// What the chat's pin knows it by (`chatPinned`): Show All keeps its top where it is on screen.
    var pinKey: String
    @Environment(\.chatPin) private var pin
    @State private var full = false
    @State private var moreRight = false

    /// Old and new text compared (`LineDiff`): unchanged lines as context, a stretch of more than a few of them folded
    /// into one row until Show All, removed lines before added ones, and in a line changed rather than rewritten, the
    /// words that changed. Claude's Edit carries no line numbers, so none are shown. Past `LineDiff`'s budget, all of
    /// old removed, then all of new added.
    init(old: String, new: String, pinKey: String) {
        self.init(ToolDiffMemo.edit(old: old, new: new), pinKey: pinKey)
    }

    init(unified diff: String, pinKey: String) {
        self.init(ToolDiffMemo.unified(diff), pinKey: pinKey)
    }

    init(_ content: ToolDiffMemo.Content, pinKey: String, maxLines: Int = 40) {
        lines = content.lines
        short = content.short
        adds = content.adds
        dels = content.dels
        self.pinKey = pinKey
        self.maxLines = maxLines
    }

    var body: some View {
        let shown = full ? lines : Array(short.prefix(maxLines))
        // Something is left out: lines past the cap, or a folded stretch (folding always takes away a row).
        let clipped = shown.count < lines.count
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ScrollView(.horizontal, showsIndicators: false) {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(shown) { line($0) }
                }
                .padding(.vertical, Theme.Space.sm)
            }
            .onScrollGeometryChange(for: Bool.self) { g in
                g.contentSize.width - g.contentOffset.x > g.containerSize.width + 1
            } action: { _, more in moreRight = more }
            .overlay(alignment: .trailing) {
                if moreRight {
                    LinearGradient(colors: [Theme.codeBackground.opacity(0), Theme.codeBackground], startPoint: .leading, endPoint: .trailing)
                        .scaledFrame(width: Theme.Size.wellXS)
                        .allowsHitTesting(false)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Theme.codeBackground)
            .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
            if clipped {
                // Held in place: the rows it adds would push the diff's top, and the call's head above it, up under the
                // top bar of the upside-down chat (docs/lessons/ios.md, "Every expander inside a chat row needs the pin").
                Button("Show All \(lines.count) Lines [Tool Views 3aed77]") {
                    toggleInPlace(pin, pinKey, animation: Theme.Motion.standard) { full = true }
                }
                .font(Theme.Font.labelMedium).lineLimit(1)
            }
        }
        .chatPinned(pinKey)
    }

    private func line(_ l: DiffText.Line) -> some View {
        let (sign, color, fill, emphasis): (String, Color, Color, Color) = switch l.kind {
        case .add: ("+", Theme.Diff.add, Theme.Diff.addFill, Theme.Diff.addEmphasis)
        case .del: ("−", Theme.Diff.del, Theme.Diff.delFill, Theme.Diff.delEmphasis)
        default: (" ", .secondary, .clear, .clear)
        }
        let quiet = l.kind == .hunk || l.kind == .info
        return HStack(spacing: Theme.Space.sm) {
            Text(sign).foregroundStyle(color).scaledFrame(width: Theme.Size.dotXL)
            Self.text(l, emphasis: emphasis).foregroundStyle(quiet ? Color.secondary : .primary)
                #if os(macOS)
                .textSelection(.enabled) // the text, not the +/− column, as on the web
                #endif
        }
        .font(Theme.Font.code)
        .padding(.horizontal, Theme.Space.s)
        .padding(.vertical, 1)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(fill)
    }

    /// The line's code, the words that changed (`DiffText.Line.changed`) on `emphasis` over the row's own fill.
    private static func text(_ l: DiffText.Line, emphasis: Color) -> Text {
        guard !l.changed.isEmpty else { return Text(l.text.isEmpty ? " " : l.text) }
        var out = AttributedString()
        for piece in LineDiff.pieces(l.text, changed: l.changed) {
            var run = AttributedString(piece.text)
            if piece.changed { run.backgroundColor = emphasis }
            out += run
        }
        return Text(out)
    }
}

/// TodoWrite's list as BeautifulUI's task rows in one card: a numbered ring for what is left, the ring turning on the
/// one in progress (its activeForm), a green check for what is done.
struct TodoList: View {
    var todos: [JSONValue]

    var body: some View {
        if AppStyle.current.usesBeautifulUI {
            TaskRows(Self.items(todos), style: .list, showsPills: false, maxWidth: nil)
        } else {
            checklist
        }
    }

    /// Persona' own: a check, a dotted ring or an empty one, the finished ones struck through.
    private var checklist: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ForEach(Array(todos.enumerated()), id: \.offset) { _, t in
                let status = t["status"]?.string ?? "pending"
                HStack(alignment: .top, spacing: Theme.Space.s) {
                    ThemedIcon(status == "completed" ? Theme.Icon.successFilled : status == "in_progress" ? "circle.dotted.circle" : Theme.Icon.unchecked)
                        .foregroundStyle(status == "completed" ? Theme.positive : status == "in_progress" ? Theme.busy : .secondary)
                    Text(status == "in_progress" ? (t["activeForm"]?.string ?? t["content"]?.string ?? "") : (t["content"]?.string ?? ""))
                        .font(Theme.Font.caption)
                        .strikethrough(status == "completed", color: .secondary)
                        .foregroundStyle(status == "completed" ? .secondary : .primary)
                }
            }
        }
    }

    static func items(_ todos: [JSONValue]) -> [TaskItem] {
        todos.enumerated().map { n, t in
            let status = t["status"]?.string ?? "pending"
            let content = t["content"]?.string ?? ""
            return switch status {
            case "completed": TaskItem(id: "\(n)", title: content, status: .completed)
            case "in_progress": TaskItem(id: "\(n)", title: t["activeForm"]?.string ?? content, status: .running(step: n + 1))
            default: TaskItem(id: "\(n)", title: content, status: .pending(step: n + 1))
            }
        }
    }
}

/// Where a plan (TodoWrite) is, in the one sentence the web says it too (#5901): "Step 3 of 4", the current step being the
/// first in progress, else the first still to do; "All 4 steps done" once every step is.
enum PlanProgress {
    static func text(_ todos: [JSONValue]) -> String {
        guard !todos.isEmpty else { return "" }
        let status = todos.map { $0["status"]?.string ?? "pending" }
        guard let at = status.firstIndex(of: "in_progress") ?? status.firstIndex(of: "pending") else {
            return String(localized: "All \(todos.count) steps done [Plan Progress]")
        }
        return String(localized: "Step \(at + 1) of \(todos.count) [Plan Progress]")
    }

    static func text(_ plan: ChatPlan) -> String { text(plan.todos.map { .object(["status": .string($0.status)]) }) }

    /// "Step 3 of 4 · Make both sizes' layouts", the step it is on after how far along; once all are done, that alone.
    static func summary(_ plan: ChatPlan) -> String {
        [text(plan), plan.allDone ? nil : plan.current?.content].compactMap { $0 }.joined(separator: " · ")
    }
}

/// Thumbnail of an image in a conversation; tap opens the full-screen viewer (paging through `gallery`).
struct Base64Image: View {
    @Environment(\.chatImages) private var chatImages
    @Environment(\.attachImage) private var attachImage
    @Environment(\.viewImages) private var viewImages
    @Environment(\.imageZoom) private var pageZoom
    var payload: ImagePayload
    var gallery: [ImagePayload] = []
    var index = 0
    /// The viewer's title for a picture alone (a file's name); a gallery's pictures go untitled.
    var title: String? = nil
    @State private var image: UIImage?
    @State private var showViewer = false
    /// The last load gave no picture (no hub, a failed download, bytes that do not decode).
    @State private var failed = false
    /// Bumped by a tap on the retry: loads again (the loader keeps no failures).
    @State private var attempt = 0
    init(payload: ImagePayload, gallery: [ImagePayload] = [], index: Int = 0, title: String? = nil) {
        self.payload = payload
        self.gallery = gallery
        self.index = index
        self.title = title
        _image = State(initialValue: ChatImageLoader.cached(payload, side: 900)) // back on screen: no placeholder flash
    }
    @Namespace private var zoom

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("Base64Image")
        #endif
        Group {
            if let image {
                Image(uiImage: image).resizable().scaledToFit()
                    .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).stroke(Theme.cardStroke, lineWidth: 0.5))
                    .scaledFrame(maxHeight: Theme.Size.box280)
                    .contentShape(.rect)
                    .onTapGesture { open() }
                    #if os(macOS)
                    // Dragged out to Finder or another app, as a picture in a Mac app is (#2113).
                    .draggable(Image(nsImage: image)) { Image(nsImage: image).resizable().scaledToFit().scaledFrame(maxHeight: Theme.Size.box120) }
                    #endif
                    .matchedTransitionSource(id: pageZoom == nil ? "image" : payload.identity, in: pageZoom ?? zoom)
                    .accessibilityAddTraits(.isButton)
                    .accessibilityLabel("Image, tap to enlarge")
            } else if let aspect = payload.aspect { // lazy: hold the image's shape while it downloads
                RoundedRectangle(cornerRadius: Theme.Radius.control).fill(Theme.Fill.well)
                    .aspectRatio(aspect, contentMode: .fit)
                    .scaledFrame(maxHeight: Theme.Size.box280)
                    .overlay { if failed { notLoaded } else { ProgressView().controlSize(.small) } }
            } else {
                RoundedRectangle(cornerRadius: Theme.Radius.control).fill(Theme.Fill.well).scaledFrame(height: Theme.Size.box120)
                    .overlay { if failed { notLoaded } }
            }
        }
        .fullScreenCover(isPresented: $showViewer) {
            Group {
                if let title {
                    ImageViewer(items: [ViewerImage(payload: payload, title: title)], index: 0, attach: attachImage)
                } else {
                    ImageViewer(images: gallery.isEmpty ? [payload] : gallery, index: gallery.isEmpty ? 0 : index,
                                conversation: chatImages, attach: attachImage)
                }
            }
            .navigationTransition(.zoom(sourceID: "image", in: zoom))
        }
        .task(id: "\(payload.identity)#\(attempt)") {
            if failed { failed = false }
            // Thumbnails only need ~2x their on-screen size.
            let loaded = await ChatImageLoader.image(payload, side: 900)
            image = loaded
            if loaded == nil, !Task.isCancelled { failed = true }
        }
    }

    /// Said on the placeholder when the picture did not come. One fetched from the hub (`ref`) loads again on a tap;
    /// bytes already here that do not decode would only fail again.
    private var notLoaded: some View {
        ImageNotLoaded(canRetry: payload.ref != nil) { attempt += 1 }
    }

    /// In a chat the page shows the viewer (`viewImages`); elsewhere this view does.
    private func open() {
        guard let viewImages else { return showViewer = true }
        if let title {
            return viewImages(ImageViewing(images: [], index: 0, conversation: [], source: payload.identity,
                                           viewed: [ViewerImage(payload: payload, title: title)]))
        }
        viewImages(ImageViewing(images: gallery.isEmpty ? [payload] : gallery, index: gallery.isEmpty ? 0 : index,
                                conversation: chatImages, source: payload.identity))
    }
}

extension EnvironmentValues {
    /// Fetches the work a fold stands for into the chat (`AppModel.unfold`); nil where nothing can.
    @Entry var openFold: ((Item) async throws -> Void)? = nil
}

/// Work the page left out (`Item.fold`, docs/protocol/protocol.md「Folded work」). It shows only once its work line is open, and
/// then fetches the steps, which take its place.
struct FoldedWorkRow: View {
    @Environment(\.openFold) private var openFold
    /// Drawn for Full Page (#1609): the row as it is, without fetching the steps into the chat.
    @Environment(\.fullPageExport) private var exporting
    var fold: Item
    @State private var failed: String?
    @State private var attempt = 0

    private var steps: Int { fold.fold.map { $0.tools + $0.thoughts } ?? 0 }

    var body: some View {
        HStack(spacing: Theme.Space.s) {
            if let failed {
                ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.caution)
                Text("\(steps) steps didn't load: \(failed)").lineLimit(2)
                Spacer(minLength: 0)
                Button("Retry [Components b8784c]") {
                    self.failed = nil
                    attempt += 1
                }
                .lineLimit(1)
                .fixedSize()
            } else {
                ProgressView().controlSize(.small)
                Text("Loading \(steps) steps…").lineLimit(1)
                Spacer(minLength: 0)
            }
        }
        .font(Theme.Font.caption)
        .foregroundStyle(.secondary)
        .scaledFrame(minHeight: Theme.Size.wellXS)
        .task(id: attempt) {
            guard let openFold, !exporting else { return }
            do {
                try await openFold(fold)
            } catch {
                failed = error.shown
            }
        }
    }
}

/// A tool that operates a Mac, in words (#1323): what it did as a line of steps (「点 AC → 6 → × → 7 → =」), the
/// screenshot it brought back, VNC; its JSON and what the driver answered folded under Raw Input.
struct ComputerToolDetail: View {
    var call: ToolCall
    var input: String
    @State private var raw = false

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Label {
                Text(ComputerSteps.describe(call.name, input: call.input)).font(Theme.Font.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            } icon: {
                if call.isRunning { ProgressView().controlSize(.mini) } else if call.failed {
                    ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.danger)
                } else if call.outcome?.stopped == true, let head = ToolOutcomeText.headline(call.outcome) {
                    ThemedIcon(head.symbol).foregroundStyle(.secondary)
                } else {
                    ThemedIcon(Theme.Icon.success).foregroundStyle(Theme.positive)
                }
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("computer-steps")
            if let result = call.result, result.isError == true {
                Text(ToolPresentation.firstLine(result.cutText)).font(Theme.Font.label).foregroundStyle(call.failed ? Theme.danger : .secondary)
                    .lineLimit(3)
            }
            ForEach(Array(call.images.enumerated()), id: \.offset) { i, img in Base64Image(payload: img, gallery: call.images, index: i) }
            ScreenEntry(call: call)
            RawInputToggle(raw: $raw).accessibilityIdentifier("computer-raw")
            if raw {
                CodeBlock(text: input, maxLines: 14)
                if let result = call.result {
                    ResultBlock(text: result.cutText, isError: result.isError ?? false, cut: result.fullSize.map { (result.id, $0) },
                                outcome: result.outcome)
                }
            }
        }
    }
}
