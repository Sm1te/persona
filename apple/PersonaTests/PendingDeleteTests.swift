import Foundation
import Testing
@testable import Persona

/// A message only on this phone (failed, or waiting for the network) is deleted from its long press as from its mark's
/// menu (#4626): the bubble and its outbox entry go and nothing is sent, Undo puts it back, and a relaunch does not
/// bring it back. One the hub may have offers no Delete.
@MainActor
@Suite(.serialized)
struct PendingDeleteTests {
    private let key = SessionKey(machineID: "box", sessionID: "pending-delete")
    /// The app's disk: a folder of the test's own (as OutboxRestoreTests).
    private let folder = FileManager.default.temporaryDirectory.appending(path: "pending-delete-\(UUID().uuidString)")

    private func message(_ state: PendingMessage.State, _ text: String = "Is the museum open on Sunday?") -> PendingMessage {
        var p = PendingMessage(text: text, images: [])
        p.state = state
        return p
    }

    /// What the next launch finds in the outbox, once this one's last write (300 ms after a change) is done.
    private func relaunched() async throws -> [PendingMessage] {
        try await Task.sleep(for: .milliseconds(600))
        let after = AppModel(stateFolder: folder)
        after.restoreOutbox()
        let back = after.chat(key).pending
        try await Task.sleep(for: .milliseconds(600)) // what the restore writes back
        return back
    }

    @Test func onlyAMessageTheHubNeverTookHasDelete() {
        #expect(message(.failed).onlyHere)
        #expect(message(.waiting).onlyHere)
        #expect(!message(.sending).onlyHere) // it may be arriving
        #expect(!message(.sent).onlyHere) // the hub has it
        #expect(MessageBarDelete.of(message(.failed), run: {})?.title == String(localized: "Delete"))
        #expect(MessageBarDelete.of(message(.waiting), run: {})?.title == String(localized: "Don't Send")) // as its clock's menu says
        #expect(MessageBarDelete.of(message(.sending), run: {})?.title == nil)
        #expect(MessageBarDelete.of(message(.sent), run: {})?.title == nil)
    }

    /// The bar makes room for it: Copy, Select Text, Share and Delete.
    @Test func theBarCountsDelete() {
        let menu = MessageMenu()
        #expect(MessageActionBar.count(id: "x", isUser: true, menu: menu, pending: true) == 3)
        #expect(MessageActionBar.count(id: "x", isUser: true, menu: menu, pending: true, deletes: true) == 4)
    }

    @Test func aDeletedMessageStaysGoneAfterARelaunch() async throws {
        defer { try? FileManager.default.removeItem(at: folder) }
        let failed = message(.failed), waiting = message(.waiting, "And on Monday?"), kept = message(.failed, "Book two tickets")
        let model = AppModel(stateFolder: folder)
        model.chat(key).pending = [failed, waiting, kept]
        model.discard(key, failed)
        model.discard(key, waiting)
        #expect(model.chat(key).pending.map(\.id) == [kept.id])
        #expect(model.toast?.action?.title == String(localized: "Undo"))
        #expect(try await relaunched().map(\.id) == [kept.id])
    }

    @Test func undoPutsItBackWhereItWas() async throws {
        defer { try? FileManager.default.removeItem(at: folder) }
        let first = message(.failed), second = message(.failed, "And on Monday?")
        let model = AppModel(stateFolder: folder)
        model.chat(key).pending = [first, second]
        model.discard(key, first)
        try #require(model.toast?.action).run()
        #expect(model.chat(key).pending.map(\.id) == [first.id, second.id])
        #expect(model.chat(key).pending.first?.state == .failed) // still the user's to resend or delete
        #expect(try await relaunched().map(\.id) == [first.id, second.id])
    }

    /// Deleted, then its transcript copy came in after all (a send whose outcome was unknown): Undo adds no second one.
    @Test func undoAfterItsCopyArrivedAddsNothing() throws {
        defer { try? FileManager.default.removeItem(at: folder) }
        let failed = message(.failed)
        let model = AppModel(stateFolder: folder)
        model.chat(key).pending = [failed]
        model.discard(key, failed)
        var copy = Item(id: "l4#0", seq: 4, ts: Date(), role: "user", kind: .text, text: failed.text, toolUseID: nil, toolName: nil,
                        toolInput: nil, isError: nil, image: nil)
        copy.clientMsgID = failed.id.uuidString
        _ = model.chat(key).apply([copy], span: ItemSpan(history: "h", from: nil, to: 4), backlog: true)
        try #require(model.toast?.action).run()
        #expect(model.chat(key).pending.isEmpty)
    }
}
