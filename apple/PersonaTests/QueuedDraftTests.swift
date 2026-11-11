import Foundation
import Testing
@testable import Persona

/// A queued message taken back to edit is read apart into what the composer held (#3022): Claude Code's queue keeps
/// only text, with each photo as an `[Image #n]` placeholder and a `path:` line in the node's `<attached_images>` block.
struct QueuedDraftTests {
    private let standIns: Set<String> = ["请看图片", "请看视频", "参考上面的内容。"] // i18n-ignore

    /// Two photos and words, as a real transcript's queue held them (made-up words and paths): the placeholders up
    /// front, the wrapper the hub dropped leaving its line breaks, a `path:` line per photo.
    @Test func claudesQueueGivesTheWordsAndThePhotosBack() {
        let q = "[Image #387] [Image #388]\n\n\n把这两张截图里的问题一起修了\n<attached_images>\npath: /Users/me/.persona/uploads/2026-11-10/aaaa1111.jpg\npath: /Users/me/.persona/uploads/2026-11-10/bbbb2222.jpg\n</attached_images>"
        let d = QueuedDraft(q, standIns: standIns)
        #expect(d.text == "把这两张截图里的问题一起修了")
        #expect(d.imagePaths == ["/Users/me/.persona/uploads/2026-11-10/aaaa1111.jpg", "/Users/me/.persona/uploads/2026-11-10/bbbb2222.jpg"])
        #expect(d.files.isEmpty && d.contexts.isEmpty)
    }

    /// The node's own block (each path, then its `path:` line) and an older hub's `<pasted_content>` wrapper: each photo once.
    @Test func theNodesBlockAndThePastedWrapperGiveEachPhotoOnce() {
        let q = "<pasted_content id=\"b7fb\">\n看这个\n\n<attached_images>\n/tmp/a.png\npath: /tmp/a.png\n~/b.jpg\npath: ~/b.jpg\n</attached_images>\n</pasted_content id=\"b7fb\">"
        let d = QueuedDraft(q, standIns: standIns)
        #expect(d.text == "看这个")
        #expect(d.imagePaths == ["/tmp/a.png", "~/b.jpg"])
    }

    /// Photos without words went with the composer's stand-in words: back, the box is empty.
    @Test func photosAloneLeaveTheBoxEmpty() {
        let d = QueuedDraft("[Image #4]\n\n\n请看图片\n<attached_images>\npath: /tmp/a.jpg\n</attached_images>", standIns: standIns)
        #expect(d.text.isEmpty)
        #expect(d.imagePaths == ["/tmp/a.jpg"])
    }

    /// The same words typed by hand, with nothing else, are what was written.
    @Test func standInWordsWithoutAttachmentsStay() {
        #expect(QueuedDraft("请看图片", standIns: standIns).text == "请看图片")
        #expect(QueuedDraft("请看图片", standIns: standIns).wordsOnly)
    }

    /// A video's tag and a quote's block go back to their bars; a file uploaded from the phone is a path in the words
    /// and stays there.
    @Test func videosAndQuotesComeOutOfTheWords() {
        let quote = ContextRef(kind: "quote", title: "构建日志", url: "", text: "error: missing semicolon", msg: "m1")
        let video = AttachedFile(path: "/tmp/screen-20261110.mov", name: "screen.mov", size: 2_048_000, duration: 12)
        let q = quote.xml + "\n\n" + AttachedFile.message("看下录屏，日志在 /tmp/report-20261110.pdf", files: [video])
        let d = QueuedDraft(q, standIns: standIns)
        #expect(d.text == "看下录屏，日志在 /tmp/report-20261110.pdf")
        #expect(d.files.map(\.path) == [video.path])
        #expect(d.files.first?.name == "screen.mov")
        #expect(d.contexts.map(\.text) == ["error: missing semicolon"])
        #expect(d.contexts.first?.msg == "m1")
        #expect(d.imagePaths.isEmpty)
    }

    /// Words only: given back as they were, line breaks and all.
    @Test func wordsOnlyAreGivenBackAsTheyAre() {
        let d = QueuedDraft("顺便检查一下深色模式下小组件的对比度\n不够的话调一下文字颜色", standIns: standIns)
        #expect(d.text == "顺便检查一下深色模式下小组件的对比度\n不够的话调一下文字颜色")
        #expect(d.wordsOnly)
    }
}
