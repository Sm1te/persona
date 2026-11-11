import Foundation
import Testing
import UIKit
@testable import Persona

/// A new conversation's first message (`StarterDraft.message`, #3805): both starters build it the one way, as a
/// conversation's composer does — quotes first, then what was typed (or what to look at), then the videos' tags.
@MainActor struct StarterDraftMessageTests {
    @Test func typedTextGoesAsItIs() {
        #expect(StarterDraft().message("周五的发布", files: []) == "周五的发布")
    }

    @Test func nothingTypedSaysWhatToLookAt() {
        let draft = StarterDraft()
        #expect(draft.message("", files: []) == String(localized: "See the context above."))
        draft.held.images = [(UIImage(), ImagePayload(mime: "image/jpeg", data: ""))]
        #expect(draft.message("", files: []) == String(localized: "Please look at the image [Assistant View 5ad878]"))
    }

    @Test func quotesGoFirst() {
        let draft = StarterDraft()
        let quote = ContextRef(kind: "quote", title: "博客迁移到 Astro", url: "persona://session/studio/a1", text: "迁移计划")
        draft.quotes = [quote]
        #expect(draft.message("看一下", files: []) == quote.xml + "\n\n看一下")
    }
}
