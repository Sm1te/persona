import Foundation

extension Session {
    /// A Codex agent, by its agent or where it was read from (Android's `hasCodexIdentity`). TRAEX is not: only Codex's
    /// provenance defines the hierarchy below.
    nonisolated var hasCodexIdentity: Bool {
        agent == "codex" || (sourceKind?.hasPrefix("codex_") ?? false)
    }

    /// A Codex subagent (`thread_spawn`): the node says so, or names the chat or the conversation it hangs off
    /// (Android's `isCodexSubagent`).
    nonisolated var isCodexSubagent: Bool {
        guard hasCodexIdentity else { return false }
        if isSubagent == true { return true }
        if let parent = parentSessionID, !parent.isEmpty { return true }
        if let parent = parentThreadID, !parent.isEmpty { return true }
        if let root = rootSessionID, !root.isEmpty, root != sessionID { return true }
        return false
    }

    /// Another session started this one (the hub's `Session.IsSubtask`, Android's `isSubtask`): a runtime's session that
    /// says it is a subagent or names its parent (a Paseo agent another agent created, #4494), whichever agent runs it;
    /// from any other source, a Codex subagent. Terminal panes are never subtasks, whatever relationship they carry.
    nonisolated var isSubtask: Bool {
        guard sourceKind == "runtime" else { return isCodexSubagent }
        return isSubagent == true || !(parentSessionID ?? "").isEmpty
    }

    /// A subtask listed under its parent's Subtasks row and left out of every count (Android's `isNestedSubtask`). One
    /// that needs the user is not: it is a row of its own, in Needs You and counted (docs/design/DESIGN_SYSTEM.md 3.4)
    /// until it no longer needs them. One whose parent the list does not draw is a row of its own too
    /// (`CodexTasks.build`), still not counted.
    nonisolated var isNestedSubtask: Bool { isSubtask && state != .needsYou }
}

/// Subtasks under the session that started them: Codex subagents under their chat, as the Codex App's sidebar shows
/// them, and a runtime's sessions under the one that created them (a Paseo agent's, #4494), as Android's Agents list
/// (`CodexTaskForest`) does. The list lists and counts the others (`roots`), and a session's subtasks are behind its
/// Subtasks row (`children`). A subtask whose parent is not in the list (another page, another machine, archived) is a
/// root of its own; so is one in a cycle of corrupt parents, never every member of it.
nonisolated struct CodexTasks: Sendable {
    /// A subtask under a listed session, `depth` levels below the session's direct ones.
    struct Task: Hashable, Sendable, Identifiable {
        var session: Session
        var depth: Int
        var id: SessionKey { session.key }
    }

    var roots: [Session] = []
    var children: [SessionKey: [Session]] = [:]

    /// Hangs each `isNestedSubtask` under its parent, on its own hub and machine. A runtime's subtask: the session its
    /// parent session id names, whichever agent runs it, else the one its root session id names. A Codex subagent, among
    /// the Codex sessions: the one its parent session id names, else (naming none) the only one of its Codex home whose
    /// thread its parent thread id names, else the one its root session id names.
    ///
    /// `runs` says which rows the list of running sessions draws, where the list has one (Android's `runs`): a subtask it
    /// draws hangs under its parent only while it draws a row above the subtask too. A working agent whose creator was
    /// let go would be drawn nowhere, so it is a row of its own (#4494). One that list does not draw stays under its
    /// parent, as a finished Codex thread's subagents do. The rows in `ownRows` hang under nothing: rows of their own the
    /// user opened under a quick filter, which stay where they were while it is on (a subtask that needed the user and was
    /// answered, too).
    static func build(_ sessions: [Session], ownRows: Set<SessionKey> = [], runs: ((Session) -> Bool)? = nil) -> Self {
        guard let parent = parents(sessions, ownRows: ownRows, runs: runs) else { return Self(roots: sessions) }
        var roots: [Session] = []
        var members: [Int: [Int]] = [:]
        for i in sessions.indices {
            if parent[i] >= 0 { members[parent[i], default: []].append(i) } else { roots.append(sessions[i]) }
        }
        var children: [SessionKey: [Session]] = [:]
        for (p, list) in members {
            let newestFirst = list.map { (index: $0, at: sessions[$0].updatedAt ?? .distantPast, order: order(sessions[$0].key)) }
                .sorted { $0.at != $1.at ? $0.at > $1.at : $0.order < $1.order }
            children[sessions[p].key] = newestFirst.map { sessions[$0.index] }
        }
        return Self(roots: roots, children: children)
    }

    /// Where each of `sessions` hangs (`build`'s rule): the index of its parent among them, -1 for none; nil when none
    /// hangs at all, the usual list, so that one costs a single pass. Works on indices: a session is a large value, and
    /// a list of thousands copied into dictionaries made the pass several times slower (#4767).
    static func parents(_ sessions: [Session], ownRows: Set<SessionKey> = [], runs: ((Session) -> Bool)? = nil) -> [Int]? {
        let nested = nestedSubtasks(sessions, ownRows: ownRows)
        guard !nested.isEmpty else { return nil }
        return parents(sessions, nested: nested, runs: runs)
    }

    /// The indices of the `isNestedSubtask` ones that are not in `ownRows`. A function of its own: inside the one below,
    /// whose local functions capture the list, the same loop read each session ten times slower (measured, #4767).
    private static func nestedSubtasks(_ sessions: [Session], ownRows: Set<SessionKey>) -> [Int] {
        var nested: [Int] = []
        for i in sessions.indices where sessions[i].isNestedSubtask && (ownRows.isEmpty || !ownRows.contains(sessions[i].key)) {
            nested.append(i)
        }
        return nested
    }

    private static func parents(_ sessions: [Session], nested: [Int], runs: ((Session) -> Bool)?) -> [Int] {
        var index: [SessionKey: Int] = [:]
        index.reserveCapacity(sessions.count)
        for i in sessions.indices where index[sessions[i].key] == nil { index[sessions[i].key] = i }
        struct Thread: Hashable { var machine: MachineRef; var instance: String?; var thread: String }
        var byThread: [Thread: [Int]] = [:]
        for i in sessions.indices where sessions[i].hasCodexIdentity {
            guard let thread = sessions[i].sourceThreadID, !thread.isEmpty else { continue }
            byThread[Thread(machine: sessions[i].machineRef, instance: sessions[i].sourceInstanceID, thread: thread), default: []].append(i)
        }
        /// The session `id` names beside `s`; with `codex`, only a Codex one.
        func named(_ s: Session, _ id: String?, codex: Bool) -> Int? {
            guard let id, !id.isEmpty, let i = index[SessionKey(group: s.group, machineID: s.machineID, sessionID: id)],
                  !codex || sessions[i].hasCodexIdentity else { return nil }
            return i
        }
        /// Its parent's index. A function, so each session is read where it is rather than copied out (measured, #4767).
        func parentOf(_ s: Session) -> Int? {
            var found: Int?
            if s.sourceKind == "runtime" {
                found = named(s, s.parentSessionID, codex: false) ?? named(s, s.rootSessionID, codex: false)
            } else {
                found = named(s, s.parentSessionID, codex: true)
                if found == nil, s.parentSessionID?.isEmpty ?? true, let thread = s.parentThreadID, !thread.isEmpty,
                   let same = byThread[Thread(machine: s.machineRef, instance: s.sourceInstanceID, thread: thread)], same.count == 1 {
                    found = same[0]
                }
                if found == nil { found = named(s, s.rootSessionID, codex: true) }
            }
            guard let found, sessions[found].sessionID != s.sessionID || sessions[found].key != s.key else { return nil }
            return found
        }
        var parent = [Int](repeating: -1, count: sessions.count)
        for i in nested { if let found = parentOf(sessions[i]) { parent[i] = found } }
        // A cycle keeps one member as a root, the same one every time: walking from each start in a fixed order, the
        // first member met twice loses its parent. Corrupt parents are rare: one linear pass looks for a cycle first.
        var mark = [UInt8](repeating: 0, count: sessions.count) // 1: on this walk, 2: walked
        var cycle = false
        for start in nested where mark[start] == 0 {
            var walk: [Int] = []
            var c = start
            while c >= 0 && mark[c] == 0 { mark[c] = 1; walk.append(c); c = parent[c] }
            if c >= 0 && mark[c] == 1 { cycle = true }
            for w in walk { mark[w] = 2 }
        }
        if cycle {
            let starts = nested.filter { parent[$0] >= 0 }.map { (order: order(sessions[$0].key), index: $0) }
                .sorted { $0.order < $1.order }
            for start in starts {
                var visited = Set<Int>()
                var c = start.index
                while c >= 0, visited.insert(c).inserted { c = parent[c] }
                if c >= 0 { parent[c] = -1 }
            }
        }
        if let runs {
            // Asked only of the subtasks and the sessions above them, once each.
            var drawn = [Int8](repeating: -1, count: sessions.count)
            func draws(_ i: Int) -> Bool {
                if drawn[i] < 0 { drawn[i] = runs(sessions[i]) ? 1 : 0 }
                return drawn[i] == 1
            }
            // Decided on the whole tree first: one subtask let go does not let go of the ones under it.
            var loose: [Int] = []
            for i in nested where parent[i] >= 0 && draws(i) {
                var above = parent[i]
                while above >= 0 && !draws(above) { above = parent[above] }
                if above < 0 { loose.append(i) }
            }
            for i in loose { parent[i] = -1 }
        }
        return parent
    }

    private static func order(_ k: SessionKey) -> String { "\(k.hubGroup ?? "")/\(k.machineID)/\(k.sessionID)" }

    /// Every subtask under key, each followed by its own a level further in, newest first on each level.
    func flattened(under key: SessionKey) -> [Task] {
        var out: [Task] = []
        var seen: Set<SessionKey> = [key]
        func walk(_ parent: SessionKey, _ depth: Int) {
            for s in children[parent] ?? [] where seen.insert(s.key).inserted {
                out.append(Task(session: s, depth: depth))
                walk(s.key, depth + 1)
            }
        }
        walk(key, 0)
        return out
    }

    /// The ones of tasks (`flattened`) that pass, and the ones they hang off, in their order.
    static func kept(_ tasks: [Task], passing: (Session) -> Bool) -> [Task] {
        var keep = [Bool](repeating: false, count: tasks.count)
        // Walking back from a kept task, the first one shallower than it is its parent, and so on up: `above` is the
        // depth of the task kept last (-1: none yet, nothing to keep for).
        var above = -1
        for i in tasks.indices.reversed() where passing(tasks[i].session) || tasks[i].depth < above {
            keep[i] = true
            above = tasks[i].depth
        }
        return tasks.indices.filter { keep[$0] }.map { tasks[$0] }
    }
}
