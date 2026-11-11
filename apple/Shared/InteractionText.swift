import Foundation

/// The first heading line of an agent's plan: the first non-empty line without Markdown markers
/// ("### Plan" -> "Plan"), what every card and the Mac menu bar title shows. An empty plan is Plan.
nonisolated enum PlanText {
    static func title(_ plan: String) -> String {
        for line in plan.split(separator: "\n") {
            let t = line.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "#* "))
            if !t.isEmpty { return t }
        }
        return String(localized: "Plan [Interaction Text 2619dd]")
    }
}

/// The assistant's password card (`secret_run`, docs/assistant/assistant.md): commands the Agent proposes for one of the
/// user's computers, run by that computer's node with a password typed into the card on the iPhone.
nonisolated enum SecretRun {
    /// The tool's name on the card (`tool_name`), and the base name of its call in the chat (`mcp__persona__secret_run`).
    static let tool = "secret_run"
}

nonisolated extension Interaction {
    /// The password card. Its raw tool name is never shown: its title names it, its summary says what it runs.
    var isSecretRun: Bool { kind == .permission && toolName == SecretRun.tool }
    /// The hand-off card (`SecretHandoff`): approved on the card only, never from a quick path.
    var isSecretHandoff: Bool { kind == .permission && toolName == SecretHandoff.tool }
}

/// The hand-off card (`secret_handoff`, docs/extensions/secret-providers.md "Who may approve a hand-off"): secret values
/// a provider holds, handed to one Agent run once the user approves on the card. Its approval says so on its own request
/// (`feature`), the hub's condition for letting it through; every quick path is off (`Interaction.requiresInput`).
nonisolated enum SecretHandoff {
    static let tool = "secret_handoff"
    /// What the card's own approval declares (`X-Persona-Features`), and nothing else does.
    static let feature = "secret_handoff"
}

/// What a hand-off card hands over and where (its `tool_input`): never a reference or a value.
nonisolated struct SecretHandoffRequest: Sendable {
    nonisolated struct Variable: Sendable, Identifiable {
        var name: String
        /// The item as its provider describes it, after the provider's name ("1Password › GitHub token").
        var item: String
        var id: String { name }
    }
    var variables: [Variable]
    var agent: String
    var machine: String
    var cwd: String
    var prompt: String

    init(_ input: JSONValue?) {
        func text(_ v: JSONValue?) -> String { v?.string?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "" }
        variables = (input?["variables"]?.array ?? []).compactMap { v in
            let name = text(v["name"])
            guard !name.isEmpty else { return nil }
            let provider = text(v["provider_name"]).isEmpty ? text(v["provider"]) : text(v["provider_name"])
            let title = text(v["title"])
            return Variable(name: name, item: title.isEmpty ? provider : provider + " › " + title)
        }
        agent = text(input?["agent"])
        machine = text(input?["machine_name"])
        cwd = text(input?["cwd"])
        prompt = text(input?["prompt"])
    }
}

nonisolated extension Interaction {
    /// One line on what a needs-you card asks, chosen by its kind — the same wording on the chat card, the inbox's
    /// Needs You row and the Mac menu bar: a plan's first heading, a terminal dialog's question, a question card's
    /// first question, a permission card's tool with what it runs, and an MCP form's or link's message.
    /// - Parameter includeToolName: a permission card shows `toolName: permissionTarget`; pass false to show the
    ///   target only and fall back to the tool name, as the inbox row does (its next line already names the card kind).
    ///   The password card (`isSecretRun`) shows its summary alone either way.
    /// - Returns: nil when the card carries no such text; the caller falls back to its generic title.
    func askLine(includeToolName: Bool) -> String? {
        switch kind {
        case .question:
            return questions?.first?.question
        case .plan:
            return PlanText.title(plan ?? "")
        case .permission:
            if includeToolName, !isSecretRun {
                let line = [toolName, permissionTarget].compactMap { $0 }.joined(separator: permissionSeparator)
                return line.isEmpty ? nil : line
            }
            // The inbox row: trim whitespace-only summaries so it never draws a blank asks line.
            if let s = permissionTarget?.trimmingCharacters(in: .whitespacesAndNewlines), !s.isEmpty { return s }
            if let t = toolName?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty, !isSecretRun { return t }
            return nil
        case .screen:
            return screenDialog?.question
        case .elicitation:
            // An MCP form's or link's message ("Please choose"); its title says which it is.
            guard let m = elicitation?.message?.trimmingCharacters(in: .whitespacesAndNewlines), !m.isEmpty else { return nil }
            return m
        }
    }

    private var permissionSeparator: String {
        // Same separator on the chat card, the inbox row and the Mac menu bar.
        String(localized: ": ", comment: "Separator between a tool name and what it runs, e.g. Bash：npm test")
    }
}

nonisolated extension Interaction {
    /// What a permission call acts on, picked the way the node's `toolSummary` and Android's `permissionSummary()` pick
    /// it: the hub's summary; else the tool's own field (a Bash command, a file's path, a URL, a query); else the first
    /// of a few common fields. What the list's row, the Inbox, the Mac menu bar (`askLine`) and a folded card show.
    /// Nil when the card carries none of these.
    var permissionTarget: String? {
        if let s = summary, !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return s }
        guard let input = toolInput?.object, !input.isEmpty else { return nil }
        func pick(_ keys: String...) -> String? {
            keys.lazy.compactMap { input[$0]?.string }.first { !$0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
        }
        let picked: String? = switch toolName ?? "" {
        case "Bash": pick("command")
        case "Edit", "Write", "Read", "MultiEdit", "NotebookEdit": pick("file_path", "notebook_path")
        case "WebFetch": pick("url")
        case "WebSearch": pick("query")
        default: nil
        }
        return picked ?? pick("description", "command", "path", "url", "query", "prompt")
    }

    /// What a permission card shows for its call, in code type (docs/design/PRINCIPLES.md, "Progressive disclosure": a
    /// command reads as a command, never as raw JSON): `permissionTarget`, else the input as `key: value` lines.
    /// Nil when the card carries none of these.
    var permissionSubject: String? {
        if let target = permissionTarget { return target }
        guard let input = toolInput?.object, !input.isEmpty else { return nil }
        return input.keys.sorted().map { "\($0): \(Self.inlineText(input[$0] ?? .null))" }.joined(separator: "\n")
    }

    /// One input value on one line: strings, numbers and booleans as they read, anything nested as compact JSON.
    private static func inlineText(_ value: JSONValue) -> String {
        if let s = value.string { return s }
        if case .null = value { return "null" }
        let enc = JSONEncoder()
        enc.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return (try? enc.encode(value)).map { String(decoding: $0, as: UTF8.self) } ?? ""
    }
}

nonisolated extension Interaction {
    /// A card on no session (#4059): `personad ask` run outside herdr, or before the pane's agent hooked in, has no
    /// session the node could tie it to (docs/integrations/script-cli.md「Attribution」).
    var isSessionless: Bool { sessionID.isEmpty }

    /// Whether the app may answer it: a sessionless card always (no session source says no); else as its session's source
    /// allows (`canApprove`; a read-only Codex history does not), and not while that session is unknown.
    func canAnswer(session: Session?) -> Bool { isSessionless || session?.canApprove == true }

    /// Where a sessionless card comes from, for its list row: the title the node gave it, else its directory's name.
    var sessionlessPlace: String? {
        if let t = sessionTitle?.trimmingCharacters(in: .whitespacesAndNewlines), !t.isEmpty { return t }
        let dir = cwd.map { URL(fileURLWithPath: $0).lastPathComponent } ?? ""
        return dir.isEmpty || dir == "/" ? nil : dir
    }
}
