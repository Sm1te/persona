import Foundation

/// A queued message read apart into what the composer held when it went (#3022), for Take Back to Edit and the queue
/// strip's rows. Claude Code's queue keeps only text (`Session.queued`, its transcript's queue-operation lines): a photo
/// as an `[Image #n]` placeholder up front and a `path: <path>` line in the node's `<attached_images>` block
/// (node `keepImagePaths`; a block a client wrote has the bare paths), a video as a `<persona-file …/>` tag, quoted
/// context as `<persona-context>` blocks, the `<pasted_content>` wrapper of a long message (the hub drops it; an older
/// one did not). Taken apart, the words go back in the box and the rest to its bar.
nonisolated struct QueuedDraft: Sendable {
    /// What was typed: no placeholders, tags or paths of photos.
    var text: String
    /// The photos' paths on the conversation's machine, in the order they went.
    var imagePaths: [String]
    /// The videos that went with it.
    var files: [AttachedFile]
    /// The context it quoted.
    var contexts: [ContextRef]

    /// What the composer puts in place of words when a message has none (`ChatComposer.sendText`): with the photos,
    /// videos or quotes back in their bars, the box stays empty.
    static let standIns: Set<String> = [String(localized: "Please look at the image [Assistant View 5ad878]"),
                                        String(localized: "Please look at the image [Assistant View a3e0a0]"),
                                        String(localized: "See the context above.")]

    private static let imageBlock = try! NSRegularExpression(pattern: #"\s*<attached_images>([\s\S]*?)</attached_images>\s*"#)
    private static let placeholder = try! NSRegularExpression(pattern: #"\[Image #\d+\][ \t]*"#)
    private static let pasted = try! NSRegularExpression(pattern: #"<[\\/]*pasted_content[^>]*>"#)
    private static let pathLine = "path: "

    init(_ queued: String, standIns: Set<String> = QueuedDraft.standIns) {
        let quoted = ContextRef.parse(Self.replacing(Self.pasted, in: queued, with: ""))
        contexts = quoted.refs
        let sent = AttachedFile.parse(quoted.rest)
        files = sent.files
        var rest = sent.rest
        let ns = rest as NSString
        var paths: [String] = []
        for m in Self.imageBlock.matches(in: rest, range: NSRange(location: 0, length: ns.length)) {
            for line in ns.substring(with: m.range(at: 1)).components(separatedBy: .newlines) {
                var p = line.trimmingCharacters(in: .whitespaces)
                if p.hasPrefix(Self.pathLine) { p = String(p.dropFirst(Self.pathLine.count)).trimmingCharacters(in: .whitespaces) }
                // The node's own block before Claude took it has each photo twice (the path, then its `path:` line).
                if p.hasPrefix("/") || p.hasPrefix("~"), !paths.contains(p) { paths.append(p) }
            }
        }
        imagePaths = paths
        rest = Self.replacing(Self.imageBlock, in: rest, with: "\n")
        rest = Self.replacing(Self.placeholder, in: rest, with: "").trimmingCharacters(in: .whitespacesAndNewlines)
        if !(paths.isEmpty && files.isEmpty && contexts.isEmpty), standIns.contains(rest) { rest = "" }
        text = rest
    }

    /// Nothing but words came with it.
    var wordsOnly: Bool { imagePaths.isEmpty && files.isEmpty && contexts.isEmpty }

    private static func replacing(_ re: NSRegularExpression, in s: String, with template: String) -> String {
        re.stringByReplacingMatches(in: s, range: NSRange(location: 0, length: (s as NSString).length), withTemplate: template)
    }
}
