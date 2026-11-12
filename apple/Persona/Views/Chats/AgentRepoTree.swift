import Foundation

/// Where an agent works: the repository and the working tree in it. The host may name the repository (`repo_root`,
/// #2840: Paseo keeps its worktrees under its own folder): a working directory inside it is its main checkout, any other
/// one of its trees. Otherwise from the working directory alone, by the web's rules (`web/src/lib/agent-repos.ts`): a
/// path under one of the usual working-tree folders belongs to the directory above that folder; any other path is its
/// own repository's main checkout.
nonisolated struct RepoPlace: Equatable, Sendable {
    /// The repository's path; "" without a working directory.
    var repo: String
    var name: String
    /// The working tree's path.
    var worktree: String
    /// The repository's main checkout, not a tree under it.
    var main: Bool

    /// Where herdr, Claude Code and a home-level collection keep working trees.
    static let markers = ["/.worktrees/", "/.claude/worktrees/", "/.herdr/worktrees/"]

    init(repo: String, name: String, worktree: String, main: Bool) {
        self.repo = repo
        self.name = name
        self.worktree = worktree
        self.main = main
    }

    init(cwd: String, repoRoot: String? = nil) {
        var path = Substring(cwd)
        while path.hasSuffix("/") { path = path.dropLast() }
        guard !path.isEmpty else {
            self.init(repo: "", name: "", worktree: "", main: true)
            return
        }
        var root = Substring(repoRoot ?? "")
        while root.hasSuffix("/") { root = root.dropLast() }
        if !root.isEmpty {
            let inside = path == root || path.hasPrefix(String(root) + "/")
            self.init(repo: String(root), name: Self.baseName(String(root)), worktree: String(inside ? root : path), main: inside)
            return
        }
        for marker in Self.markers {
            guard let at = path.range(of: marker), at.lowerBound > path.startIndex else { continue }
            let rest = path[at.upperBound...].split(separator: "/" as Character)
            let repo = String(path[..<at.lowerBound])
            // ~/.worktrees/<repo>/<tree>: the folder sits at the home level, so the repository is the first part after
            // it and the tree is what follows.
            if rest.count >= 2 {
                self.init(repo: repo + marker + rest[0], name: String(rest[0]), worktree: String(path), main: false)
            } else {
                self.init(repo: repo, name: Self.baseName(repo), worktree: String(path), main: false)
            }
            return
        }
        self.init(repo: String(path), name: Self.baseName(String(path)), worktree: String(path), main: true)
    }

    /// Where the node placed it in git (#1805): its project (`repo_root`, else the working tree itself) and working
    /// tree; nil from an older node, which leaves the inference above.
    static func git(_ s: Session) -> RepoPlace? {
        let worktree = trimmed(s.worktree)
        guard !worktree.isEmpty else { return nil }
        let root = trimmed(s.repoRoot)
        let repo = root.isEmpty ? worktree : root
        return RepoPlace(repo: repo, name: baseName(repo), worktree: worktree, main: worktree == repo)
    }

    /// A session's place: the node's when it says, else from its working directory.
    static func of(_ s: Session) -> RepoPlace { git(s) ?? RepoPlace(cwd: s.cwd, repoRoot: s.repoRoot) }

    private static func trimmed(_ path: String?) -> String {
        var path = Substring(path ?? "")
        while path.hasSuffix("/") { path = path.dropLast() }
        return String(path)
    }

    private static func baseName(_ path: String) -> String {
        path.split(separator: "/" as Character).last.map(String.init) ?? ""
    }
}

/// By Repository (docs/apple/ios.md「Information architecture」): every agent under its machine, its repository (`RepoPlace`) and the
/// working tree in it, as Orca's projects with the machine above them. Plain data, unit-tested apart from the list
/// (`AgentRepoTreeTests`); the list orders the agents inside a tree (`AgentListOrder`).
nonisolated struct RepoTree {
    struct Worktree: Identifiable, Sendable {
        /// Its fold id.
        var id: String
        /// Its branch when the node says (#1805), else its folder's name (the main checkout: the repository's). A linked
        /// tree goes by the name its host gave the place instead, when its sessions agree on one (#4768, `hostPlaceName`);
        /// its id does not change with the name.
        var name: String
        var path: String
        var main: Bool
        var sessions: [Session]
        /// The branch checked out there, when the node says (#1805).
        var branch: String? = nil
        /// The spaces its host directly nests under it (Orca's `parent_space_id`); each keeps its own children and
        /// its own「N 个子项」fold, so deeper levels remain under their actual parent.
        var children: [Worktree] = []
        /// The fold id of its「N 个子项」row; folded until opened (`childFoldSuffix`).
        var childrenFoldID: String { id + RepoTree.childFoldSuffix }
        var agentsFoldID: String { id + RepoTree.agentsFoldSuffix }
        /// Its agents and its children's.
        var treeSessions: [Session] { sessions + children.flatMap(\.treeSessions) }
        var catalog = false
        var workspace: HostWorkspace? = nil

        /// Repository grouping names the root; nested cards repeat a repository only at a parent/child boundary.
        func repositoryBadge(parent: HostWorkspace?, inheritedName: String) -> String? {
            guard let workspace, let parent, let name = workspace.projectName, !name.isEmpty else { return nil }
            guard workspace.machineRef == parent.machineRef && workspace.source == parent.source else { return name }
            if let id = workspace.projectID, !id.isEmpty, let parentID = parent.projectID, !parentID.isEmpty {
                return id == parentID ? nil : name
            }
            if let path = workspace.projectPath, !path.isEmpty, let parentPath = parent.projectPath, !parentPath.isEmpty {
                func normalized(_ value: String) -> String {
                    var path = value
                    while path.count > 1 && path.hasSuffix("/") { path.removeLast() }
                    return path
                }
                return normalized(path) == normalized(parentPath) ? nil : name
            }
            let parentName = parent.projectName.flatMap { $0.isEmpty ? nil : $0 } ?? inheritedName
            return name == parentName ? nil : name
        }
    }

    /// One repository on one machine.
    struct Repo: Identifiable, Sendable {
        /// Its fold id.
        var id: String
        var machine: MachineRef
        var name: String
        var path: String
        /// The main checkout first, then by name.
        var worktrees: [Worktree]
        var sessions: [Session] { worktrees.flatMap(\.treeSessions) }
        /// Only its main checkout, nothing nested under it and no branch the node named: its agents sit right under it,
        /// with no working-tree level. A named branch keeps its level (#1805: machine → project → branch → agents).
        var flat: Bool {
            worktrees.count == 1 && worktrees[0].main && worktrees[0].children.isEmpty && !worktrees[0].catalog && worktrees[0].branch == nil
        }
        var structured: Bool { worktrees.contains { $0.catalog || !$0.children.isEmpty } }
    }

    /// One machine's repositories, by name.
    struct Host: Identifiable, Sendable {
        var ref: MachineRef
        var repos: [Repo]
        var id: String { ref.path }
        var sessions: [Session] { repos.flatMap(\.sessions) }
    }

    /// The name a repository goes by when its agents have no working directory.
    static let unnamed = String(localized: "Other")
    /// A working tree's「N 个子项」fold id: its id and this.
    static let childFoldSuffix = String(localized: "/sub-items")
    static let agentsFoldSuffix = "/workspace-agents"

    /// A session's space on its machine, unique across hosts; nil outside a space.
    static func spaceKey(_ s: Session) -> String? {
        s.spaceID.map { s.machineRef.path + "/" + (s.spaceKind ?? "") + ":" + $0 }
    }

    /// The space each nested space hangs under (`parent_space_id`): only a parent that is listed (it has a live agent),
    /// on the same machine and host, and not itself; a space whose parents loop back stays at the top (#3075).
    static func parents(_ sessions: [Session]) -> [String: String] {
        var parentOf: [String: String] = [:]
        let listed = Set(sessions.compactMap(spaceKey))
        for s in sessions {
            guard let key = spaceKey(s), parentOf[key] == nil, let pid = s.parentSpaceID, !pid.isEmpty else { continue }
            let parent = s.machineRef.path + "/" + (s.spaceKind ?? "") + ":" + pid
            if parent != key && listed.contains(parent) { parentOf[key] = parent }
        }
        func loops(_ key: String) -> Bool {
            var seen: Set<String> = [key]
            var at = parentOf[key]
            while let p = at {
                if !seen.insert(p).inserted { return true }
                at = parentOf[p]
            }
            return false
        }
        return parentOf.filter { !loops($0.key) }
    }

    /// `sessions` (the live agents the list shows) under their machines in `machines`' order (the hub's); a machine it
    /// does not list goes last. A space nested under another (Orca's child worktrees) goes under its top space's working
    /// tree, whatever its own directory says.
    private struct Node {
        var machine: MachineRef
        var path: String
        var name: String
        var index: Int
        var parent: String?
        var sessions: [Session]
        var catalog = false
        var workspace: HostWorkspace? = nil
    }

    static func build(_ sessions: [Session], machines: [MachineRef], workspaces: [HostWorkspace] = [],
                      includeEmpty: Bool = true, search: String = "") -> [Host] {
        var nodes: [String: Node] = [:]
        var loose: [Session] = []
        for s in sessions {
            guard s.spaceKind == "orca", let key = spaceKey(s) else { loose.append(s); continue }
            if nodes[key] != nil { nodes[key]!.sessions.append(s); continue }
            nodes[key] = Node(machine: s.machineRef, path: s.cwd, name: s.spaceName ?? "",
                              index: s.spaceIndex ?? Int.max,
                              parent: s.parentSpaceID.map { s.machineRef.path + "/" + (s.spaceKind ?? "") + ":" + $0 }, sessions: [s],
                              workspace: HostWorkspace(machineID: s.machineID, source: "orca", sourceID: s.spaceID!,
                                                       title: s.spaceName ?? s.folderName, cwd: s.cwd, group: s.group))
        }
        // Discovery supplies containers, including existing workspaces with no running Agent. Its source-local identity
        // keeps two workspaces in the same directory distinct and permits cross-project Orca lineage.
        for w in workspaces where w.source == "orca" {
            let old = nodes[w.key]
            nodes[w.key] = Node(machine: w.machineRef, path: w.cwd, name: w.title,
                                index: w.index ?? old?.index ?? Int.max,
                                parent: w.parentKey ?? (w.index == nil ? old?.parent : nil),
                                sessions: old?.sessions ?? [], catalog: true, workspace: w)
        }
        var parentOf: [String: String] = [:]
        for (key, node) in nodes {
            if let parent = node.parent, parent != key, nodes[parent] != nil { parentOf[key] = parent }
        }
        let edges = parentOf
        for key in edges.keys {
            var seen: Set<String> = [key]
            var at = edges[key]
            while let parent = at {
                if !seen.insert(parent).inserted { parentOf.removeValue(forKey: key); break }
                at = edges[parent]
            }
        }
        var children: [String: [String]] = [:]
        for (key, parent) in parentOf { children[parent, default: []].append(key) }
        func beforeNode(_ a: String, _ b: String) -> Bool {
            nodes[a]!.index != nodes[b]!.index ? nodes[a]!.index < nodes[b]!.index : a < b
        }
        func tree(_ key: String) -> Worktree? {
            let n = nodes[key]!
            let below = (children[key] ?? []).sorted(by: beforeNode).compactMap(tree)
            let matches = includeEmpty && !search.isEmpty && (n.name + " " + n.path).localizedStandardContains(search)
            guard includeEmpty && search.isEmpty || matches || !n.sessions.isEmpty || !below.isEmpty else { return nil }
            let name = n.name.isEmpty ? n.path.split(separator: "/").last.map(String.init) ?? unnamed : n.name
            return Worktree(id: "tree/space/" + key, name: name, path: n.path, main: n.workspace?.isMain ?? false,
                            sessions: n.sessions, children: below, catalog: n.catalog, workspace: n.workspace)
        }
        var repos: [String: Repo] = [:]
        for key in nodes.keys.filter({ parentOf[$0] == nil }).sorted(by: beforeNode) {
            let n = nodes[key]!
            guard var w = tree(key) else { continue }
            // The node's git place when it sends one (#1805), else the path inference.
            let place = n.sessions.first.flatMap(RepoPlace.git) ?? RepoPlace(cwd: n.path, repoRoot: n.sessions.first?.repoRoot)
            let metadata = n.workspace
            let addressProject = metadata?.sourceID.components(separatedBy: "::")
            let projectID = metadata?.projectID.flatMap { $0.isEmpty ? nil : $0 }
                ?? ((addressProject?.count ?? 0) > 1 ? addressProject?.first.flatMap { $0.isEmpty ? nil : $0 } : nil)
            let repoKey = n.machine.path + "/" + (projectID.map { "project/orca:" + $0 }
                ?? (n.catalog ? "workspace/" + key : place.repo))
            var repo = repos[repoKey] ?? Repo(id: "repo/" + repoKey, machine: n.machine,
                                            name: metadata?.projectName.flatMap { $0.isEmpty ? nil : $0 }
                                                ?? (n.catalog && projectID == nil ? w.name : (place.name.isEmpty ? unnamed : place.name)),
                                            path: metadata?.projectPath.flatMap { $0.isEmpty ? nil : $0 } ?? place.repo, worktrees: [])
            if let name = metadata?.projectName, !name.isEmpty { repo.name = name }
            if let path = metadata?.projectPath, !path.isEmpty { repo.path = path }
            w.main = metadata?.isMain ?? place.main
            if !n.catalog {
                // Orca's sidebar and git name a working tree by its branch.
                let branch = n.sessions.first?.branch.flatMap { $0.isEmpty ? nil : $0 }
                w.id = "tree/" + n.machine.path + "/" + place.worktree
                w.name = branch ?? (place.worktree == place.repo ? repo.name : place.worktree.split(separator: "/").last.map(String.init) ?? "")
                w.branch = branch
                w.path = place.worktree
            }
            if let i = repo.worktrees.firstIndex(where: { $0.id == w.id }) {
                repo.worktrees[i].sessions += w.sessions
                repo.worktrees[i].children += w.children
            } else { repo.worktrees.append(w) }
            repos[repoKey] = repo
        }
        // Sessions without host placement keep the established path-based repository grouping.
        var worktreeIndices = repos.mapValues { repo in
            Dictionary(repo.worktrees.enumerated().map { ($0.element.path, $0.offset) }, uniquingKeysWith: { a, _ in a })
        }
        for s in loose {
            let place = RepoPlace.of(s)
            let key = s.machineRef.path + "/" + place.repo
            if repos[key] == nil {
                repos[key] = Repo(id: "repo/" + key, machine: s.machineRef,
                                  name: place.name.isEmpty ? unnamed : place.name, path: place.repo, worktrees: [])
            }
            // Preserve the projection's linear accumulation for large inventories.
            let branch = s.branch.flatMap { $0.isEmpty ? nil : $0 }
            if let i = worktreeIndices[key]?[place.worktree] {
                repos[key]!.worktrees[i].sessions.append(s)
                // Orca's sidebar and git name a working tree by its branch.
                if repos[key]!.worktrees[i].branch == nil, !repos[key]!.worktrees[i].catalog, let branch {
                    repos[key]!.worktrees[i].branch = branch
                    repos[key]!.worktrees[i].name = branch
                }
            } else {
                let folder = place.worktree == place.repo ? repos[key]!.name : place.worktree.split(separator: "/").last.map(String.init) ?? ""
                let index = repos[key]!.worktrees.count
                repos[key]!.worktrees.append(Worktree(id: "tree/" + s.machineRef.path + "/" + place.worktree, name: branch ?? folder,
                                                    path: place.worktree, main: place.main, sessions: [s], branch: branch))
                worktreeIndices[key, default: [:]][place.worktree] = index
            }
        }
        var hosts: [MachineRef: [Repo]] = [:]
        for var repo in repos.values {
            // A linked tree takes the name its host gave it (#4768, as the web's and Android's By Repository), its branch
            // staying on it and its id its path. The main checkout keeps its name: several workspaces can share it. So
            // does a tree with an Orca agent in it (every tree Orca places has one), which Orca names.
            for i in repo.worktrees.indices {
                let w = repo.worktrees[i]
                guard !w.main, !w.sessions.contains(where: { $0.spaceKind == "orca" }), let name = hostPlaceName(w.sessions) else { continue }
                repo.worktrees[i].name = name
            }
            repo.worktrees.sort { a, b in
                if a.catalog && b.catalog, let ai = a.workspace?.index, let bi = b.workspace?.index, ai != bi { return ai < bi }
                return a.main != b.main ? a.main : before(a.name, a.path, b.name, b.path)
            }
            hosts[repo.machine, default: []].append(repo)
        }
        return hostOrder(Set(hosts.keys), machines: machines).map { ref in
            Host(ref: ref, repos: hosts[ref, default: []].sorted { before($0.name, $0.id, $1.name, $1.id) })
        }
    }

    /// The name the host gave the place `sessions` run in (the web's `hostPlaceName`, Android's `AgentRepos.kt`: Paseo sends
    /// its workspace's title as `slot_name`, with no `slot_id`), when every session that gives one gives the same; two
    /// different names name nothing, and a session without one neither votes nor blocks. A `slot_name` beside a
    /// `slot_id` is a tab's label (herdr), which names no working tree.
    static func hostPlaceName(_ sessions: [Session]) -> String? {
        var name: Substring?
        for i in sessions.indices {
            guard let own = trimmedOrNil(sessions[i].slotName), trimmedOrNil(sessions[i].slotID) == nil else { continue }
            if let name, name != own { return nil }
            name = own
        }
        return name.map(String.init)
    }

    /// The text without the white space around it (as the web's `trim()`), nil when nothing is left. Cheap for the usual
    /// text with none around it: the list builds the tree for every change.
    private static func trimmedOrNil(_ text: String?) -> Substring? {
        guard let text else { return nil }
        var t = Substring(text)
        while t.first?.isWhitespace == true { t.removeFirst() }
        while t.last?.isWhitespace == true { t.removeLast() }
        return t.isEmpty ? nil : t
    }

    /// `refs` in `machines`' order (the hub's); one it does not list goes last, by its path.
    static func hostOrder(_ refs: Set<MachineRef>, machines: [MachineRef]) -> [MachineRef] {
        let index = Dictionary(machines.enumerated().map { ($1, $0) }, uniquingKeysWith: { a, _ in a })
        return refs.sorted { a, b in
            let ia = index[a] ?? Int.max, ib = index[b] ?? Int.max
            return ia != ib ? ia < ib : a.path < b.path
        }
    }

    /// By name as Finder sorts them ("app2" before "app10"), then by path: one order on every render.
    private static func before(_ a: String, _ pathA: String, _ b: String, _ pathB: String) -> Bool {
        switch a.localizedStandardCompare(b) {
        case .orderedAscending: true
        case .orderedDescending: false
        case .orderedSame: pathA < pathB
        }
    }
}
