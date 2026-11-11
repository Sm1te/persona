import SwiftUI

// Comments in the chat (#151, #150): the comment blocks a message leads with (`MessageComments`), as cards over the
// composer while they wait and as quote cards in the sent bubble. iOS only: the Mac app is frozen (docs/apple/mac.md),
// so there the blocks stay text as before.

nonisolated extension MessageComments {
    /// What a sent bubble draws of a message's comment blocks: the plain quote before them, the comments and the words
    /// after them; nil when the message starts with no comment (the bubble as it always was).
    static func inBubble(_ text: String) -> Split? {
        #if os(iOS)
        guard let s = split(text), !s.comments.isEmpty else { return nil }
        return s
        #else
        return nil
        #endif
    }
}

extension MessageComment {
    /// 「代码评论 2」: the kind and its number, as the agent reads it in the message.
    func heading(_ number: Int) -> String {
        if element != nil { return String(localized: "Element comment \(number)") }
        if code != nil { return String(localized: "Code comment \(number)") }
        return String(localized: "Comment \(number) [Message Comments]")
    }

    var icon: String { element != nil ? Theme.Icon.pickElement : code != nil ? Theme.Icon.code : Theme.Icon.quote }

    /// Where it points: the file and its lines, the element and its page's path, or the picked words.
    var location: String {
        if let e = element {
            let path = URLComponents(string: e.url)?.path ?? ""
            return path.isEmpty || path == "/" ? e.selector : "\(e.selector) · \(path)"
        }
        if let c = code { return "\(c.file):\(MessageComments.range(c.from, c.to))" }
        return Self.flat(quoted)
    }

    /// The place in a few words, for a bubble's heading: the file's name and its lines, the element, or the words.
    var shortLocation: String {
        if let e = element { return e.selector }
        if let c = code { return "\((c.file as NSString).lastPathComponent):\(MessageComments.range(c.from, c.to))" }
        return location
    }

    /// What a review compared, when the comment is on a diff.
    var scope: String? {
        guard let d = code?.diff else { return nil }
        switch d {
        case .staged: return String(localized: "Staged")
        case .unstaged: return String(localized: "Unstaged")
        case .uncommitted: return String(localized: "Uncommitted")
        case .commit(let sha): return String(localized: "Commit \(String(sha.prefix(8)))")
        case .branchSince(let base): return String(localized: "Branch · \(base)")
        case .since(let base): return String(localized: "Since \(base)")
        }
    }

    /// The note, or (with none) what it is about.
    var summary: String { note.isEmpty ? Self.flat(quoted) : note }

    /// Everything, when a card opens: the note, then the code, the HTML or the words.
    var full: String { note.isEmpty ? quoted : note + "\n\n" + quoted }

    #if DEBUG
    /// `-showSampleComments YES`: what a review and a page's preview put over the box.
    static let samples = [
        MessageComment(quoted: "+let city: String?", note: "Make the city optional: old caches have none.",
                       code: .init(file: "Shared/Forecast.swift", from: 7, to: 7, diff: .branchSince("4f2a9c1"), lang: "diff")),
        MessageComment(quoted: " return cached\n+?? last", note: "Fall back to the last cache so the widget is never empty.",
                       code: .init(file: "Shared/Forecast.swift", from: 30, to: 31, diff: .branchSince("4f2a9c1"), lang: "diff")),
        MessageComment(quoted: "<button class=\"save\">Save</button>", note: "Make it the full-width primary button.",
                       element: .init(url: "http://localhost:5173/settings", selector: "button.save", component: "SaveButton",
                                      source: "src/settings/SaveButton.tsx:14", text: "Save")),
    ]
    #endif

    private static func flat(_ s: String) -> String {
        s.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }.joined(separator: " ")
    }
}

/// The comments waiting to lead the next message, in a row over the composer; × drops one. They go out as `> `
/// blocks in front of what is typed (`MessageComments.message`).
struct CommentCardsRow: View {
    @Binding var comments: [MessageComment]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: Theme.Space.s) {
                ForEach(Array(comments.enumerated()), id: \.element.id) { i, c in
                    CommentCard(comment: c, number: i + 1) {
                        withAnimation(Theme.Motion.state) { comments.removeAll { $0.id == c.id } }
                    }
                    .transition(.move(edge: .bottom).combined(with: .opacity))
                }
            }
            .padding(.horizontal, Theme.Space.page)
        }
        .scrollBounceBehavior(.basedOnSize, axes: .horizontal)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("composer.comments")
    }
}

/// One waiting comment: its kind and number, where it points, a line of its note; ×.
struct CommentCard: View {
    var comment: MessageComment
    var number: Int
    var remove: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.xs) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                HStack(spacing: Theme.Space.xs) {
                    ThemedIcon(comment.icon)
                    // The kind and number stay whole; the review's scope gives way first.
                    Text(comment.heading(number)).lineLimit(1).layoutPriority(1)
                    if let scope = comment.scope { Text(scope).foregroundStyle(.secondary).lineLimit(1) }
                }
                .font(Theme.Font.captionStrong)
                .foregroundStyle(Theme.accent)
                // The middle goes first: the path's start and the lines are what tell comments apart.
                Text(comment.location).font(Theme.Font.code).foregroundStyle(.secondary).lineLimit(1).truncationMode(.middle)
                Text(comment.summary).font(Theme.Font.secondary).foregroundStyle(.primary).lineLimit(1)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("composer.comment")
            Spacer(minLength: 0)
            Button(action: remove) {
                ThemedIcon(Theme.Icon.close).font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                    .scaledFrame(width: Theme.Size.wellXS, height: Theme.Size.wellXS)
                    .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(String(localized: "Remove Comment"))
            .accessibilityIdentifier("composer.comment.remove")
        }
        .padding(Theme.Space.m)
        .scaledFrame(width: Theme.Size.box240, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
    }
}

/// A comment block at the top of a sent bubble, drawn as its quotes are: its kind, number and place, then its note; a
/// tap opens it to the whole note and the code (the agent got the block as written).
struct CommentQuote: View {
    var comment: MessageComment
    var number: Int
    @State private var open = false

    var body: some View {
        let heading = [comment.heading(number), comment.shortLocation].joined(separator: " · ")
        QuoteCard(heading: heading, summary: open ? comment.full : comment.summary, lines: open ? .max : 2,
                  tint: Theme.Quote.tint, ink: Theme.Quote.summary) {
            Haptics.tap()
            withAnimation(Theme.Motion.state) { open.toggle() }
        }
        .accessibilityIdentifier("bubble.comment")
    }
}
