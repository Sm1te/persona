import Foundation
import Testing
@testable import Persona

/// What a file tool's permission card shows of the change it asks for (#4621): the same diff the chat draws once the call
/// runs, else the card's subject as before.
struct PermissionDiffTests {
    private func permission(tool: String?, input: JSONValue?, kind: InteractionKind = .permission) -> Interaction {
        Interaction(
            id: "ix", machineID: "m1", sessionID: "s1", sessionTitle: nil, cwd: nil,
            kind: kind, createdAt: nil, expiresAt: nil, toolName: tool, toolUseID: nil, questions: nil,
            plan: nil, toolInput: input, summary: "/work/app/csv.py", screen: nil, dialog: nil
        )
    }

    @Test func editComparesItsOldAndNewText() {
        let input = JSONValue.object([
            "file_path": .string("/work/app/csv.py"),
            "old_string": .string("a\nb"),
            "new_string": .string("a\nc"),
        ])
        let diff = PermissionDiff(permission(tool: "Edit", input: input))
        #expect(diff?.path == "/work/app/csv.py")
        #expect(diff?.changes == [.edit(old: "a\nb", new: "a\nc")])
        let content = diff?.changes[0].content
        #expect(content?.adds == 1)
        #expect(content?.dels == 1)
    }

    @Test func anEditThatCarriesAPatchShowsIt() {
        let patch = "@@ -1 +1 @@\n-a\n+b\n"
        let input = JSONValue.object(["file_path": .string("/work/app/csv.py"), "diff": .string(patch)])
        #expect(PermissionDiff(permission(tool: "Edit", input: input))?.changes == [.unified(patch)])
        // An empty patch has nothing to show, even with old and new text beside it.
        let empty = JSONValue.object(["diff": .string(" \n"), "old_string": .string("a"), "new_string": .string("b")])
        #expect(PermissionDiff(permission(tool: "Edit", input: empty)) == nil)
    }

    @Test func multiEditShowsEachEdit() {
        let input = JSONValue.object([
            "file_path": .string("/work/app/csv.py"),
            "edits": .array([
                .object(["old_string": .string("a"), "new_string": .string("b")]),
                .object(["old_string": .string(""), "new_string": .string("")]),
                .object(["old_string": .string("c"), "new_string": .string("d")]),
            ]),
        ])
        #expect(PermissionDiff(permission(tool: "MultiEdit", input: input))?.changes
            == [.edit(old: "a", new: "b"), .edit(old: "c", new: "d")])
    }

    @Test func writeShowsTheWholeNewFile() {
        let input = JSONValue.object(["file_path": .string("/work/app/test_csv.py"), "content": .string("x\ny\n")])
        let diff = PermissionDiff(permission(tool: "Write", input: input))
        #expect(diff?.changes == [.edit(old: "", new: "x\ny\n")])
        #expect(diff?.changes[0].content.adds == 2)
        #expect(diff?.changes[0].content.dels == 0)
        #expect(PermissionDiff(permission(tool: "Write", input: .object(["content": .string("")]))) == nil)
    }

    @Test func aChangeWithoutAPathStillShows() {
        let input = JSONValue.object(["file_path": .string(""), "old_string": .string("a"), "new_string": .string("b")])
        let diff = PermissionDiff(permission(tool: "Edit", input: input))
        #expect(diff?.changes == [.edit(old: "a", new: "b")])
        #expect(diff?.path == nil)
    }

    @Test func cardsWithNothingToCompareKeepTheirSubject() {
        // Only the path (a Codex edit read off its terminal, an app-server approval).
        #expect(PermissionDiff(permission(tool: "Edit", input: .object(["file_path": .string("/work/app/csv.py")]))) == nil)
        #expect(PermissionDiff(permission(tool: "Edit", input: nil)) == nil)
        #expect(PermissionDiff(permission(tool: "MultiEdit", input: .object(["edits": .array([])]))) == nil)
        // Other tools, another server's tool of the same name, and cards that are not permission cards.
        #expect(PermissionDiff(permission(tool: "Bash", input: .object(["command": .string("ls"), "content": .string("x")]))) == nil)
        let edit = JSONValue.object(["old_string": .string("a"), "new_string": .string("b")])
        #expect(PermissionDiff(permission(tool: "mcp__files__Edit", input: edit)) == nil)
        #expect(PermissionDiff(permission(tool: "Edit", input: edit, kind: .question)) == nil)
        #expect(PermissionDiff(permission(tool: nil, input: edit)) == nil)
    }
}
