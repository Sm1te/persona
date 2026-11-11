import SwiftUI

extension View {
    /// What the chat draws, named for readers of the accessibility tree (the shared sync cases played on screen,
    /// #2952): `chat.user.<item>`, `chat.answer.<item>`, `chat.outbox.<state>.<message>`, `chat.card.<interaction>`,
    /// `chat.live`. A container, so what is inside keeps its own ids; nothing is drawn differently.
    func chatMark(_ id: String) -> some View {
        accessibilityElement(children: .contain).accessibilityIdentifier(id)
    }
}
