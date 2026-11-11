import SwiftUI

/// What a file tool's permission card asks to change (#4621), read off its `tool_input` the way the chat reads the same
/// call once it runs (`ToolDetail`): Claude Code's Edit (old and new text), MultiEdit (each edit), Write (the whole new
/// file), and an Edit that carries a unified `diff`. Nil for any other tool and for one with nothing to compare: that
/// card shows its subject (`permissionSubject`) as before.
nonisolated struct PermissionDiff: Equatable, Sendable {
    enum Change: Equatable, Sendable {
        case edit(old: String, new: String)
        case unified(String)

        /// Compared once and kept (`ToolDiffMemo`), the same diff the chat draws for the call.
        var content: ToolDiffMemo.Content {
            switch self {
            case .edit(let old, let new): ToolDiffMemo.edit(old: old, new: new)
            case .unified(let diff): ToolDiffMemo.unified(diff)
            }
        }
    }

    var path: String?
    var changes: [Change]

    init?(_ ix: Interaction) {
        guard ix.kind == .permission, let input = ix.toolInput else { return nil }
        var changes: [Change] = []
        switch ToolNaming.tableName(ix.toolName ?? "") {
        case "Edit":
            if let diff = input["diff"]?.string {
                if !diff.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { changes = [.unified(diff)] }
            } else if let edit = Self.edit(input) {
                changes = [edit]
            }
        case "MultiEdit":
            changes = (input["edits"]?.array ?? []).compactMap(Self.edit)
        case "Write":
            if let content = input["content"]?.string, !content.isEmpty { changes = [.edit(old: "", new: content)] }
        default:
            break
        }
        guard !changes.isEmpty else { return nil }
        path = input["file_path"]?.string.flatMap { $0.isEmpty ? nil : $0 }
        self.changes = changes
    }

    private static func edit(_ e: JSONValue) -> Change? {
        let old = e["old_string"]?.string ?? "", new = e["new_string"]?.string ?? ""
        return old.isEmpty && new.isEmpty ? nil : .edit(old: old, new: new)
    }

    /// Compares new cards' changes off the main thread, so a card's first frame reads them from the memo.
    static func prewarm(_ cards: some Sequence<Interaction>) {
        let changes = cards.compactMap(PermissionDiff.init).flatMap(\.changes)
        guard !changes.isEmpty else { return }
        Task.detached(priority: .userInitiated) {
            for change in changes { _ = change.content }
        }
    }
}

/// A file tool's permission card (#4621): the file and the change, drawn as the chat draws the call once it runs (its
/// file header, then each edit's diff), a few rows of each until Show All so the card's choices stay in view.
struct PermissionDiffView: View {
    var diff: PermissionDiff
    /// The card's id: what each diff's Show All is known by.
    var key: String
    @Environment(\.dynamicTypeSize) private var typeSize

    /// Rows of each diff before Show All; fewer at the accessibility sizes, where each row is several times as tall.
    static func maxLines(_ size: DynamicTypeSize) -> Int { size.isAccessibilitySize ? 4 : 8 }

    var body: some View {
        let contents = diff.changes.map(\.content)
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if let path = diff.path {
                DiffFileHeader(path: path, adds: contents.reduce(0) { $0 + $1.adds }, dels: contents.reduce(0) { $0 + $1.dels })
            }
            ForEach(Array(contents.enumerated()), id: \.offset) { n, content in
                DiffView(content, pinKey: "permission-diff-\(key)-\(n)", maxLines: Self.maxLines(typeSize))
            }
        }
    }
}
