import BeautifulUI
import SwiftUI

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
}

/// Codex subagents under the chat that spawned them, as the Codex App's sidebar and Android's Agents list
/// (`CodexTaskForest`) show them: the list lists and counts the chats (`roots`), and a chat's subagents are behind its
/// Subtasks row (`children`). A subagent whose chat is not in the list (another page, another machine, archived) is a
/// root of its own; so is one in a cycle of corrupt parents, never every member of it.
nonisolated struct CodexTasks: Sendable {
    /// A subagent under a listed chat, `depth` levels below the chat's direct ones.
    struct Task: Hashable, Sendable, Identifiable {
        var session: Session
        var depth: Int
        var id: SessionKey { session.key }
    }

    var roots: [Session] = []
    var children: [SessionKey: [Session]] = [:]

    static func build(_ sessions: [Session]) -> Self {
        let codex = sessions.filter(\.hasCodexIdentity)
        guard codex.contains(where: \.isCodexSubagent) else { return Self(roots: sessions) }
        var bySession: [SessionKey: Session] = [:]
        for s in codex where bySession[s.key] == nil { bySession[s.key] = s }
        struct Thread: Hashable { var machine: MachineRef; var instance: String?; var thread: String }
        var byThread: [Thread: [Session]] = [:]
        for s in codex {
            guard let thread = s.sourceThreadID, !thread.isEmpty else { continue }
            byThread[Thread(machine: s.machineRef, instance: s.sourceInstanceID, thread: thread), default: []].append(s)
        }
        func sibling(_ s: Session, _ id: String?) -> Session? {
            guard let id, !id.isEmpty else { return nil }
            return bySession[SessionKey(group: s.group, machineID: s.machineID, sessionID: id)]
        }
        var parents: [SessionKey: SessionKey] = [:]
        for s in codex where s.isCodexSubagent {
            var parent = sibling(s, s.parentSessionID)
            if parent == nil, s.parentSessionID?.isEmpty ?? true, let thread = s.parentThreadID, !thread.isEmpty,
               let same = byThread[Thread(machine: s.machineRef, instance: s.sourceInstanceID, thread: thread)], same.count == 1 {
                parent = same[0]
            }
            if parent == nil { parent = sibling(s, s.rootSessionID) }
            if let parent, parent.key != s.key { parents[s.key] = parent.key }
        }
        // A cycle keeps one member as a root, the same one every time: walking from each start in a fixed order, the
        // first member met twice loses its parent.
        for start in parents.keys.sorted(by: { order($0) < order($1) }) {
            var visited = Set<SessionKey>()
            var current: SessionKey? = start
            while let c = current, visited.insert(c).inserted { current = parents[c] }
            if let c = current { parents[c] = nil }
        }
        var children: [SessionKey: [Session]] = [:]
        for s in sessions { if let parent = parents[s.key] { children[parent, default: []].append(s) } }
        for (parent, members) in children {
            children[parent] = members.sorted {
                let a = $0.updatedAt ?? .distantPast, b = $1.updatedAt ?? .distantPast
                return a != b ? a > b : order($0.key) < order($1.key)
            }
        }
        return Self(roots: sessions.filter { parents[$0.key] == nil }, children: children)
    }

    private static func order(_ k: SessionKey) -> String { "\(k.hubGroup ?? "")/\(k.machineID)/\(k.sessionID)" }

    /// Every subagent under key, each followed by its own a level further in, newest first on each level.
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

/// A Codex chat's Subtasks row (Android's "Subtasks (N)"): under the chat's row, its subagents a level in when opened.
/// Laid out as the list's other fold lines: By Repository's tree line, Compact List's line, else the idle agents' row.
struct CodexSubtasksRow: View {
    /// `card`: a row of a quiet space's card (the capsules), `last` ending it.
    enum Style { case regular, compact, tree(level: Int), card(last: Bool) }
    var count: Int
    var open: Bool
    var style: Style
    @Environment(\.buiTheme) private var theme

    private var title: String { String(localized: "Subtasks (\(count))") }

    var body: some View {
        Group {
            switch style {
            case .tree(let level):
                #if os(iOS)
                RepoTreeLine(level: level) {
                    ThemedIcon(Theme.Icon.subagent).font(Theme.Font.caption).foregroundStyle(.tertiary)
                } content: {
                    HStack(spacing: Theme.Space.xs) {
                        Text(title).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                        Spacer(minLength: 0)
                        chevron.font(Theme.Font.captionStrong)
                    }
                }
                #else
                regular
                #endif
            case .compact:
                HStack(spacing: CompactList.gap) {
                    ThemedIcon(Theme.Icon.subagent)
                        .font(CompactList.metaStrong)
                        .foregroundStyle(.secondary)
                        .scaledFrame(width: CompactList.mark)
                    Text(title).font(CompactList.title).foregroundStyle(.secondary).lineLimit(1)
                    Spacer(minLength: 0)
                    chevron.font(CompactList.metaStrong)
                }
            case .regular:
                regular
            case .card(let last):
                regular
                    .padding(.horizontal, Theme.Space.m)
                    .modifier(TouchHeight()) // a finger's height with the card's color under all of it (#3753)
                    .background(UnevenRoundedRectangle(bottomLeadingRadius: last ? theme.radii.card : 0,
                                                       bottomTrailingRadius: last ? theme.radii.card : 0, style: .continuous)
                        .fill(theme.colors.surface))
            }
        }
        .contentShape(.rect)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(title)
        .accessibilityValue(open ? String(localized: "Expanded") : String(localized: "Collapsed"))
        .accessibilityAddTraits(.isButton)
    }

    /// The idle agents' row's layout (`IdleFoldRow`).
    private var regular: some View {
        HStack(spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.subagent).foregroundStyle(.secondary)
            Text(title).font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(2) // its count, at large sizes too
            Spacer(minLength: 0)
            chevron.font(Theme.Font.captionStrong)
        }
        .padding(.vertical, Theme.Space.xs)
    }

    private var chevron: some View {
        ThemedIcon(open ? Theme.Icon.collapse : Theme.Icon.expand).foregroundStyle(.tertiary)
    }
}
