import Foundation

// A session's dev server on the iPhone (#150, docs/web/preview.md "iPhone"): no browser on the computer as the web has; the
// page opens through the port forwarding in the link card (docs/operations/ports.md), and the card's bar adds back /
// forward, a screenshot and picking an element to comment on. This file is what the web's lib/preview.ts is: the hub's
// answers, the servers to offer, the command a Start runs, and a picked element as an element comment.

/// A session's dev servers: the entries of its project's `.claude/launch.json`, whether each is running, and what is
/// wrong with one that cannot be used. `GET /api/v1/machines/{mid}/preview/servers?cwd=`.
nonisolated struct PreviewServers: Decodable, Sendable, Equatable {
    struct Server: Decodable, Sendable, Equatable {
        var name: String
        var port: Int?
        /// An entry that names a page (a server you start yourself when it has no command).
        var url: String?
        var command: [String]?
        var env: [String: String]?
        var cwd: String?
        var listening: Bool
        var title: String?
        /// `no_name`, `no_port`, `not_local`, `no_run`, `bad_env`: it cannot be previewed.
        var problem: String?
    }

    /// The launch.json read, when there is one.
    var file: String?
    var servers: [Server]
    /// The file could not be read (not JSON, too big).
    var error: String?
}

/// What Start answers: the terminal tab the command runs in, and the port to wait for.
nonisolated struct PreviewStart: Decodable, Sendable, Equatable {
    var pane: String?
    var terminal: String?
    var port: Int?
}

extension APIClient {
    func previewServers(machine: String, cwd: String) async throws -> PreviewServers {
        try await get("/api/v1/machines/\(machine)/preview/servers", query: ["cwd": cwd])
    }

    private struct PreviewStartBody: Encodable {
        var cwd: String
        var name: String
        var workspace: String
        var kind: String
    }

    /// Runs a launch.json entry in a new terminal tab of the session's workspace (`kind`: its space's host, herdr…).
    func previewStart(machine: String, cwd: String, name: String, workspace: String, kind: String) async throws -> PreviewStart {
        try await post("/api/v1/machines/\(machine)/preview/start", body: PreviewStartBody(cwd: cwd, name: name, workspace: workspace, kind: kind), timeout: 60)
    }
}

/// One server the preview offers: a launch.json entry, or a port an Agent of the session listens on.
nonisolated struct PreviewOption: Hashable, Sendable, Identifiable {
    enum From: Hashable, Sendable { case launch, agent }

    var name: String
    var port: Int
    /// Where it opens: a path on its server (an entry's `url`).
    var path: String
    var listening: Bool
    var title: String?
    /// The entry's command: it can be started from here.
    var command: [String]?
    var env: [String: String]?
    var cwd: String?
    /// The program an Agent's port belongs to (`node`).
    var process: String?
    var from: From

    var id: String { from == .launch ? "launch:\(name)" : "port:\(port)" }
    var link: PortLink { PortLink(port: port, path: path, secure: false) }
}

/// The element picked in a page, as `preview_pick.js` describes it, with the page's address and title.
nonisolated struct PickedElement: Decodable, Sendable, Equatable {
    struct Source: Decodable, Sendable, Equatable {
        var file: String
        var line: Int?
    }

    struct Rect: Decodable, Sendable, Equatable {
        var x: Double, y: Double, w: Double, h: Double
    }

    var selector: String
    var tag: String
    var text: String
    var html: String
    var component: String?
    var source: Source?
    var rect: Rect?
    var url: String
    var title: String?

    /// What the bar calls it: the selector's last step (`button.save`).
    var label: String { selector.components(separatedBy: " > ").last.map { $0.isEmpty ? tag : $0 } ?? tag }
}

nonisolated enum PreviewLogic {
    /// The hub feature that says it has previews.
    static let feature = "preview"

    /// What the empty list shows: what to put in .claude/launch.json (the web's `LAUNCH_EXAMPLE`).
    static let launchExample = """
    {
      "version": "0.0.1",
      "configurations": [
        {
          "name": "web",
          "runtimeExecutable": "npm",
          "runtimeArgs": ["run", "dev"],
          "port": 5173
        }
      ]
    }
    """

    /// The usable launch.json entries in the file's order, then the Agents' ports none of them has: the web's
    /// `previewOptions`.
    static func options(_ res: PreviewServers?, ports: [ListeningPort]) -> [PreviewOption] {
        var out: [PreviewOption] = []
        for s in res?.servers ?? [] where s.problem == nil {
            guard let port = s.port, port > 0 else { continue }
            var path = "/"
            if let u = s.url.flatMap(URLComponents.init(string:)) {
                let p = u.percentEncodedPath + (u.percentEncodedQuery.map { "?" + $0 } ?? "") + (u.percentEncodedFragment.map { "#" + $0 } ?? "")
                path = p.isEmpty ? "/" : p
            }
            // an Agent's port is listening even when the probe missed it (still compiling)
            let listening = s.listening || ports.contains { $0.port == port }
            out.append(PreviewOption(name: s.name, port: port, path: path, listening: listening, title: s.title,
                                     command: s.command, env: s.env, cwd: s.cwd, from: .launch))
        }
        for p in ports where !out.contains(where: { $0.port == p.port }) {
            let title = p.title.flatMap { $0.isEmpty ? nil : $0 }
            out.append(PreviewOption(name: title ?? ":\(p.port)", port: p.port, path: "/", listening: true, title: title,
                                     process: p.process, from: .agent))
        }
        return out
    }

    /// The entries launch.json has that cannot be used, for the list to say why.
    static func problems(_ res: PreviewServers?) -> [PreviewServers.Server] {
        (res?.servers ?? []).filter { $0.problem != nil }
    }

    /// The command line an entry runs, as the confirmation shows it: each word quoted as the node quotes it, its
    /// `env` in front (`env 'PORT=8080' go run ./cmd/api`: `=` is not one of the node's bare characters). The web's
    /// `commandLine`.
    static func commandLine(_ o: PreviewOption) -> String {
        func q(_ s: String) -> String {
            s.wholeMatch(of: /[A-Za-z0-9_.\/-]+/) != nil ? s : "'" + s.replacingOccurrences(of: "'", with: #"'\''"#) + "'"
        }
        let env = (o.env ?? [:]).keys.sorted().map { q("\($0)=\(o.env![$0]!)") }
        return ((env.isEmpty ? [] : ["env"] + env) + (o.command ?? []).map(q)).joined(separator: " ")
    }

    /// The folder a Start runs in, as the confirmation names it: relative to the session's folder when inside it,
    /// else with the home folder as `~`.
    static func folder(_ cwd: String?, session: String, home: String?) -> String? {
        guard let cwd, !cwd.isEmpty else { return nil }
        let root = session.hasSuffix("/") ? String(session.dropLast()) : session
        if !root.isEmpty, cwd.hasPrefix(root + "/") { return String(cwd.dropFirst(root.count + 1)) }
        if cwd == root { return "." }
        return Fmt.path(cwd, home: home)
    }

    /// The page as the computer sees it, for an element comment's `Page:`: the phone's forwarded loopback address
    /// (`http://localhost:<phone port>/…`) back to the computer's port (`http://localhost:5173/…`).
    static func machinePage(_ url: String, machinePort: Int?) -> String {
        guard let machinePort, var comps = URLComponents(string: url),
              ["localhost", "127.0.0.1", "::1", "[::1]"].contains(comps.host ?? "") else { return url }
        comps.host = "localhost"
        comps.port = machinePort
        return comps.string ?? url
    }

    /// What the address in the bar shows: the page's path on the server it previews, else the whole address. The
    /// web's `addressOf`.
    static func address(_ url: URL?, localPort: Int?) -> String {
        guard let url else { return "" }
        if let localPort, url.port == localPort, ["localhost", "127.0.0.1", "::1"].contains(url.host() ?? "") {
            var comps = URLComponents(url: url, resolvingAgainstBaseURL: false)
            comps?.scheme = nil
            comps?.host = nil
            comps?.port = nil
            let rest = comps?.string ?? url.path()
            return rest.isEmpty ? "/" : rest
        }
        return url.absoluteString
    }

    // MARK: A picked element as a comment

    /// The element the user picked as an element comment (the web's `elementQuote`): its HTML as the quoted code, cut to
    /// `MessageComments.maxCodeLines` lines; the source relative to the session's folder; the text to 120 characters.
    static func comment(_ el: PickedElement, page: String, cwd: String, note: String) -> MessageComment {
        let lines = prettyHTML(el.html).components(separatedBy: "\n")
        let max = MessageComments.maxCodeLines
        let kept = Array(lines.prefix(max))
        var spot = MessageComment.Element(url: page, selector: el.selector.isEmpty ? el.tag : el.selector, omitted: lines.count - kept.count)
        if let c = el.component, !c.isEmpty { spot.component = c }
        if let s = el.source, !s.file.isEmpty {
            let file = relativeSource(s.file, cwd: cwd)
            spot.source = (s.line ?? 0) > 0 ? "\(file):\(s.line!)" : file
        }
        let text = el.text.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.isEmpty { spot.text = text.utf16.count > 120 ? prefix(text, units: 119) + "…" : text }
        return MessageComment(quoted: kept.joined(separator: "\n"), note: note, element: spot)
    }

    /// A source path a dev build gave, relative to the session's folder when inside it (webpack's and vite's are absolute).
    static func relativeSource(_ file: String, cwd: String) -> String {
        let f = file.replacing(/^webpack:\/\/\/?(?:\.\/)?/, with: "").replacing(/^file:\/\//, with: "")
        let root = cwd.replacing(/\/+$/, with: "")
        return !root.isEmpty && f.hasPrefix(root + "/") ? String(f.dropFirst(root.count + 1)) : f
    }

    /// outerHTML with a line per tag when it came as one long line (most frameworks write it so), so 60 lines say more.
    static func prettyHTML(_ html: String) -> String {
        if html.contains("\n") || html.utf16.count < 120 { return html }
        return html.replacing(/>\s*</, with: ">\n<")
    }

    /// The first `units` UTF-16 units (the web's characters), never half a surrogate pair.
    private static func prefix(_ s: String, units: Int) -> String {
        var cut = Array(s.utf16.prefix(units))
        if let last = cut.last, UTF16.isLeadSurrogate(last) { cut.removeLast() }
        return String(decoding: cut, as: UTF16.self)
    }
}
