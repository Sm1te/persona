import SwiftUI

// MARK: - A pressed message, lifted over the dimmed chat (#603)

// As Telegram does: a long press dims and blurs the chat, draws the pressed message again above it, and puts the bar of
// actions right next to it. Lightly: the chat's words still show through, faintly (`Theme.Lift`). A message taller than the screen shows the part around the press, its ends faded, and scrolls
// inside; the bar always stays on screen, so a long answer never has to be scrolled to reach Copy. A second long press on
// it, or Select Text, selects its words where they are (`LiftSelection.swift`).

/// What a message's row offers in its bar (`messageActionBar`), for the pressable part inside it (`messageLongPress`).
struct MessageBarInfo {
    var id: String
    var text: String
    var isUser: Bool
    var date: Date?
    var pending: Bool
    /// Who wrote it, when not the chat's Agent or Persona (`messageActionBar(from:)`).
    var from: String? = nil
    var delete: MessageBarDelete? = nil
}

/// The bar's last item on a message that is only on this phone (`PendingMessage.onlyHere`, #4626), in the danger color:
/// Delete, or Don't Send for one waiting for the network, the words of its mark's menu.
struct MessageBarDelete {
    var title: String
    var run: () -> Void

    /// For a message sent from here: Delete when it failed, Don't Send while it waits for the network, nil while the hub
    /// may have it (sending, sent).
    static func of(_ msg: PendingMessage, run: @escaping () -> Void) -> MessageBarDelete? {
        guard msg.onlyHere else { return nil }
        return MessageBarDelete(title: msg.state == .waiting ? String(localized: "Don't Send") : String(localized: "Delete"), run: run)
    }
}

extension EnvironmentValues {
    @Entry var messageBar: MessageBarInfo? = nil
}

/// The pressed message: what its bar offers, where it was (global) and where in it the press landed, and the part drawn
/// again. card: words on the page with nothing around them (an answer), lifted on a card of their own.
///
/// The part is drawn again exactly as it was (#3188): offered the size the chat offered it (`offer`), not the size it
/// took (a bubble that hugs its widest line, offered that width rounded to pixels, lost the line's last character to
/// the next line, and every line after it wrapped differently), and in the environment it was drawn in (`environment`:
/// the chat sets the conversation's layout, the file scope its path links need and MarkdownUI's soft breaks around its rows).
struct MessageLift {
    var bar: MessageBarInfo
    var frame: CGRect
    var press: CGPoint
    var preview: AnyView
    var card: Bool
    var offer: ProposedViewSize? = nil
    var environment: EnvironmentValues? = nil
}

/// Where a pressable part is, written on every scrolled frame without drawing anything again (not observed). Written
/// during layout too (`RecordsOffer`), which is not the main actor's. Views hold it in `@State`, so its stored values
/// start as plain values, no calls (performance rule P3).
nonisolated final class LiftPlace {
    nonisolated struct Frames: Equatable {
        static let zero = Frames()
        var global: CGRect = .zero
        /// In its row's coordinates (`MessageMenu.row`), where the press is reported.
        var row: CGRect = .zero
    }
    var frames = Frames.zero
    /// The size the chat offered it (`RecordsOffer`) and the environment around it (`LongPressAt`).
    var offer: ProposedViewSize?
    var environment: EnvironmentValues?
}

/// Lays out its one view as its parent asks, and keeps what that parent offered it (`LiftPlace.offer`), for the same
/// view drawn again elsewhere to be offered the same (`Offers`).
struct RecordsOffer: Layout {
    let place: LiftPlace

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        subviews.first?.sizeThatFits(proposal) ?? .zero
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        place.offer = proposal
        subviews.first?.place(at: bounds.origin, proposal: proposal)
    }
}

/// Offers its one view `offer` whatever its parent offers (and is as large as the view takes then); nil: what the parent offers.
struct Offers: Layout {
    let offer: ProposedViewSize?

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        subviews.first?.sizeThatFits(offer ?? proposal) ?? .zero
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: offer ?? proposal)
    }
}

extension EnvironmentValues {
    /// Inside a lifted copy (`MessageLiftLayer`), for its whole life, selecting or not.
    @Entry var liftCopy = false
}

extension View {
    /// A block of the words drawn as one picture in the text view over a lifted message (a table): its texts' lines
    /// are not lines of the words (`LiftLayout.snap`).
    func oneBlockInLift() -> some View { modifier(OneBlockInLift()) }
}

private struct OneBlockInLift: ViewModifier {
    @Environment(\.liftCopy) private var lifted

    func body(content: Content) -> some View {
        #if DEBUG
        let drop = lifted || LiftLines.on
        #else
        let drop = lifted
        #endif
        content.transformPreference(Text.LayoutKey.self) { if drop { $0 = [] } }
    }
}

extension View {
    /// The whole environment a view was drawn in elsewhere (its own, where there is none).
    @ViewBuilder func environment(from values: EnvironmentValues?) -> some View {
        if let values { environment(\EnvironmentValues.self, values) } else { self }
    }
}

/// Where the lifted message and its bar go.
nonisolated enum LiftLayout {
    struct Placement: Equatable {
        /// The lifted message's frame: the whole message, or the part of it that fits.
        var preview: CGRect
        /// How far into the message that part starts (0 when all of it shows).
        var offset: CGFloat
        var bar: CGRect
        /// The bar under the message (else above it).
        var below: Bool
    }

    /// message: where it is; press: how far down in it the press landed; bounds: the screen inside its safe area. The
    /// message stays where it is when it can; one taller than what is left beside the bar shows that much, around the
    /// press, at the same place on screen. The bar goes under it, above it when there is no room under it, and when
    /// there is room for neither the message moves up for the bar to fit under it. trailing: the bar lines up with the
    /// message's right edge (your own messages).
    static func place(message: CGRect, press: CGFloat, bar: CGSize, bounds: CGRect, margin: CGFloat, gap: CGFloat,
                      trailing: Bool) -> Placement {
        let room = max(bounds.height - 2 * margin - gap - bar.height, 0)
        let height = min(message.height, room)
        let offset = height < message.height ? min(max(press - height / 2, 0), message.height - height) : 0
        let top = bounds.minY + margin, bottom = bounds.maxY - margin
        var y = min(max(message.minY + offset, top), bottom - height)
        let below: Bool
        if y + height + gap + bar.height <= bottom {
            below = true
        } else if y - gap - bar.height >= top {
            below = false
        } else {
            below = true
            y = max(bottom - bar.height - gap - height, top)
        }
        let preview = CGRect(x: message.minX, y: y, width: message.width, height: height)
        let x = trailing ? preview.maxX - bar.width : preview.minX
        let barX = min(max(x, bounds.minX + margin), bounds.maxX - margin - bar.width)
        let barY = below ? preview.maxY + gap : preview.minY - gap - bar.height
        return Placement(preview: preview, offset: offset, bar: CGRect(x: barX, y: barY, width: bar.width, height: bar.height), below: below)
    }

    /// A selection handle held at y in a lifted message's shown part, `height` tall: how fast that part scrolls on, in
    /// points a second (up below zero). Nothing outside the bands at its ends, `band` tall (less in a short part, a third
    /// of it at most); in one, faster the deeper the handle is, `speed` at the edge and past it.
    static func edgeScroll(at y: CGFloat, height: CGFloat, band: CGFloat, speed: CGFloat) -> CGFloat {
        let band = min(band, height / 3)
        guard band > 0 else { return 0 }
        if y < band { return -speed * min((band - y) / band, 1) }
        if y > height - band { return speed * min((y - (height - band)) / band, 1) }
        return 0
    }

    /// A line of the text view over a lifted message's words (`LiftLayout.snap`): its baseline as laid out, whether
    /// it holds a picture (a table), and the quote it is in (its `ReplyBlock`), if any.
    struct SnapLine: Equatable {
        var baseline: CGFloat
        var block = false
        var quote: Int?
        /// It starts a block whose top the text view cannot place (a code block under its title bar): it takes the
        /// next row, as the line after a picture does.
        var fresh = false
    }

    /// Where `SnapLine` goes: `at`, its baseline; `hidden`, a line of a folded quote past its fold.
    struct Snapped: Equatable {
        var at: CGFloat
        var hidden = false
    }

    /// Where the lines of the text view over a lifted message's words go (#3188): on the chat's own lines. lines: the
    /// text view's, in order; rows: the chat's baselines, sorted, without the lines a folded quote hides; folds: the
    /// folded quotes' cards, top to bottom. Each line takes the nearest row ahead within `reach` of where it would be
    /// (its baseline, moved as much as the line before it was), so a line the chat has no row for (a rule) or a row the
    /// text view has no line for is passed over; a picture keeps that move, and the line after it (and a `fresh` line)
    /// takes the next row.
    /// A quote whose first line lands in a folded card shows as many lines as the card has rows: the rest are hidden
    /// (at the card's bottom), and the line after them takes the next row, as after a picture. The rest of the text
    /// view's lines (and all of them, with no rows) stay as they are, moved as much as the line before.
    static func snap(_ lines: [SnapLine], to rows: [CGFloat], folds: [ClosedRange<CGFloat>] = [], reach: CGFloat) -> [Snapped] {
        var out: [Snapped] = [], next = 0, moved: CGFloat = 0, afterBlock = false
        // The folded quote being passed: which, its card, and how many of its rows are left.
        var folding: (quote: Int, card: ClosedRange<CGFloat>, left: Int)?
        var started: Set<Int> = []
        for line in lines {
            let at = line.baseline + moved
            if let f = folding, line.quote == f.quote, f.left == 0 {
                out.append(Snapped(at: f.card.upperBound, hidden: true))
                afterBlock = true
                continue
            }
            guard !line.block else {
                out.append(Snapped(at: at))
                afterBlock = true
                continue
            }
            if line.fresh { afterBlock = true }
            var best: Int?
            var k = next
            while k < rows.count, afterBlock || rows[k] <= at + reach {
                if afterBlock { best = k; break }
                if abs(rows[k] - at) <= reach, best.map({ abs(rows[k] - at) < abs(rows[$0] - at) }) ?? true { best = k }
                k += 1
            }
            afterBlock = false
            guard let b = best else {
                out.append(Snapped(at: at))
                continue
            }
            moved = rows[b] - line.baseline
            next = b + 1
            out.append(Snapped(at: rows[b]))
            if let q = line.quote, !started.contains(q) {
                started.insert(q)
                if let card = folds.first(where: { $0.contains(rows[b]) }) {
                    folding = (q, card, rows[b...].prefix { card.contains($0) }.count - 1)
                }
            } else if let f = folding, line.quote == f.quote {
                folding?.left = max(f.left - 1, 0)
            }
        }
        return out
    }

    /// Where the text view's lines may break, so they break where the chat's do (#3188). TextKit does not wrap as
    /// SwiftUI does: a sentence the chat broke after 12 characters, it filled to 15. texts: the chat's words, one
    /// `WordBreaks` a text, in order; paragraphs: the text view's that wrap, in order (a list item's past its marker).
    /// Each paragraph takes the next text as long as it, or as long as it and the paragraphs right after it (one line
    /// break apart: a line break inside a Markdown paragraph is a paragraph here and a line of one text there); texts
    /// none is as long as (a list's markers, a code block's title, a button) are passed over, and a paragraph no text is
    /// as long as keeps TextKit's breaks. In `held` a line breaks only at `at`.
    static func follow(_ texts: [WordBreaks], paragraphs: [NSRange]) -> (held: IndexSet, at: IndexSet) {
        var held = IndexSet(), at = IndexSet()
        var next = 0, i = 0
        while i < paragraphs.count {
            let p = paragraphs[i]
            var runs: [(end: Int, length: Int)] = [(i, p.length)]
            var j = i
            while j + 1 < paragraphs.count, paragraphs[j + 1].location == NSMaxRange(paragraphs[j]) + 1 {
                j += 1
                runs.append((j, NSMaxRange(paragraphs[j]) - p.location))
            }
            guard next < texts.count, let k = texts[next...].firstIndex(where: { t in runs.contains { $0.length == t.length } }),
                  let run = runs.first(where: { $0.length == texts[k].length }) else {
                i += 1
                continue
            }
            held.insert(integersIn: p.location..<(p.location + run.length))
            for b in texts[k].at where b > 0 && b < run.length { at.insert(p.location + b) }
            next = k + 1
            i = run.end + 1
        }
        return (held, at)
    }
}

/// A text's words as the chat laid them out (`Text.Layout`): how long they are and where each line after the first
/// starts, in UTF-16 units (an `NSString`'s) from their start.
struct WordBreaks: Equatable {
    var length: Int
    var at: [Int] = []
}

/// A folded quote's card (`ReplyQuote`): its words are all laid out, the lines past the fold hidden, so those are no
/// lines of the words to put the text view's on or to measure (#3188).
struct FoldedQuotes: PreferenceKey {
    static var defaultValue: [Anchor<CGRect>] { [] }
    static func reduce(value: inout [Anchor<CGRect>], nextValue: () -> [Anchor<CGRect>]) { value += nextValue() }
}

/// A line of the words a view shows (`Text.LayoutKey`), in its coordinates: its glyphs, where its text starts, its
/// first glyph's left edge, its baseline and its last glyph's right edge.
struct WordLine: Equatable {
    var glyphs: Int
    var origin: CGPoint
    var minX: CGFloat
    var baseline: CGFloat
    var maxX: CGFloat
}

/// The lines of the words under a view and its folded quotes' cards, in its coordinates (`readingShownWords`).
struct ShownWords: PreferenceKey, Equatable {
    var lines: [WordLine] = []
    var folds: [CGRect] = []
    /// Where each text's lines break, all of them (a folded quote's too), in order (`LiftLayout.follow`).
    var texts: [WordBreaks] = []

    static var defaultValue: ShownWords { ShownWords() }
    static func reduce(value: inout ShownWords, nextValue: () -> ShownWords) {
        let n = nextValue()
        value.lines += n.lines
        value.folds += n.folds
        value.texts += n.texts
    }

    /// The lines that show: not those of a text in a folded quote past its card's bottom.
    var shown: [WordLine] {
        lines.filter { l in !folds.contains { $0.contains(l.origin) && l.baseline > $0.maxY + 0.5 } }
    }

    /// The baselines of the lines that show, sorted, one a row (a numbered item's marker and its words share one).
    var rows: [CGFloat] {
        var rows: [CGFloat] = []
        for b in shown.map(\.baseline).sorted() where rows.last.map({ b - $0 > 0.5 }) ?? true { rows.append(b) }
        return rows
    }

    /// The folded quotes' cards from top to bottom, as the rows' spans (`LiftLayout.snap`).
    var foldSpans: [ClosedRange<CGFloat>] { folds.sorted { $0.minY < $1.minY }.map { $0.minY...$0.maxY } }

    static func measure(_ layouts: Text.LayoutKey.Value, _ geo: GeometryProxy) -> [WordLine] {
        var out: [WordLine] = []
        for a in layouts {
            let p = geo[a.origin]
            for line in a.layout {
                let n = line.reduce(0) { $0 + $1.count }
                guard n > 0, let fr = line.first, let lr = line.last, let fg = fr.first, let lg = lr.last else { continue }
                out.append(WordLine(glyphs: n, origin: p, minX: p.x + fg.typographicBounds.rect.minX,
                                    baseline: p.y + line.typographicBounds.origin.y, maxX: p.x + lg.typographicBounds.rect.maxX))
            }
        }
        return out
    }

    /// Each text's line breaks, in the order the texts come.
    static func breaks(_ layouts: Text.LayoutKey.Value) -> [WordBreaks] {
        layouts.compactMap { a in
            var first: Text.Layout.CharacterIndex?, last: Text.Layout.CharacterIndex?
            var starts: [Text.Layout.CharacterIndex] = []
            for line in a.layout {
                let at = line.flatMap(\.characterIndices)
                guard let lo = at.min(), let hi = at.max() else { continue }
                if first == nil { first = lo } else { starts.append(lo) }
                last = max(last ?? hi, hi)
            }
            guard let first, let last else { return nil }
            return WordBreaks(length: first.distance(to: last) + 1, at: starts.map { first.distance(to: $0) })
        }
    }
}

extension View {
    /// What its words show as (`ShownWords`: their lines, its folded quotes), for `read` to draw over them, in its
    /// coordinates. Read where it is: it goes no further up (a reader around this one reads its own).
    func readingShownWords<V: View>(@ViewBuilder _ read: @escaping (ShownWords) -> V) -> some View {
        backgroundPreferenceValue(FoldedQuotes.self) { folds in
            GeometryReader { geo in Color.clear.preference(key: ShownWords.self, value: ShownWords(folds: folds.map { geo[$0] })) }
        }
        .backgroundPreferenceValue(Text.LayoutKey.self) { layouts in
            GeometryReader { geo in
                Color.clear.preference(key: ShownWords.self,
                                       value: ShownWords(lines: ShownWords.measure(layouts, geo), texts: ShownWords.breaks(layouts)))
            }
        }
        .overlayPreferenceValue(ShownWords.self) { shown in read(shown) }
        .transformPreference(ShownWords.self) { $0 = ShownWords() }
    }
}

extension View {
    /// The chat page under `MessageLiftLayer`: blurred a little while a message is lifted over it. The radius goes to
    /// zero rather than the modifier away, so the page keeps its identity (and its scroll position).
    func liftBlur(_ menu: MessageMenu?) -> some View { modifier(LiftBlur(menu: menu)) }
}

private struct LiftBlur: ViewModifier {
    var menu: MessageMenu?

    func body(content: Content) -> some View {
        // Not opaque: the page's background is behind this view, not in it, and an opaque blur fills what is clear with black.
        content.blur(radius: menu?.lift == nil ? 0 : Theme.Lift.blur)
    }
}

/// A page that floats a bar over the chat in it (Persona's conversation: its top bar) draws
/// that chat's lifted message itself, over the bar, so the whole page dims as an Agents chat does. Drawn inside the
/// chat, a long message ran under the bar, its first lines out of reach (#3188). The chat hands its menu here
/// (`ChatView`); the lifted copy brings what it needs from where it was pressed (`MessageLongPress`).
@Observable
final class MessageLiftHost {
    /// The menu of the chat on the page.
    var menu: MessageMenu?
}

extension EnvironmentValues {
    /// Set by such a page for its chat (and nothing inside that chat).
    @Entry var messageLiftHost: MessageLiftHost? = nil
}

#if DEBUG
/// For the UI tests (#3188), with `-showLiftLines YES`: where each line of a pressed message's words is drawn in the chat,
/// in its lifted copy, and in the text view over that copy while its words are selected, for a test to check that
/// nothing moves. A line: its glyphs, its first glyph's left edge, its baseline, its last glyph's right edge (and for the
/// text view, the last one that is not a space's, both no further than its right edge, `edge`, and whether its last
/// character is a CJK closing bracket: TextKit 1 draws that one narrower than SwiftUI at a line's end), in the window,
/// at a tenth of a point.
@Observable final class LiftLines {
    static let shared = LiftLines()
    static let on = UserDefaults.standard.bool(forKey: "showLiftLines")
    /// By message id.
    var chat: [String: String] = [:]
    var lift = ""
    var select = ""
    /// The text view's right edge, in the window.
    var edge: CGFloat = 0

    typealias Line = (glyphs: Int, minX: CGFloat, baseline: CGFloat, maxX: CGFloat, wordsMaxX: CGFloat, closes: Bool)

    static func encode(_ lines: [Line]) -> String {
        lines.sorted { ($0.baseline, $0.minX) < ($1.baseline, $1.minX) }
            .map { String(format: "%ld,%.1f,%.1f,%.1f,%.1f,%ld", $0.glyphs, $0.minX, $0.baseline, $0.maxX, $0.wordsMaxX, $0.closes ? 1 : 0) }
            .joined(separator: ";")
    }

    /// The lines a view shows (`ShownWords.shown`), in the window: its origin there is `at`.
    static func lines(_ shown: [WordLine], at o: CGPoint) -> String {
        encode(shown.map { ($0.glyphs, o.x + $0.minX, o.y + $0.baseline, o.x + $0.maxX, o.x + $0.maxX, false) })
    }

    /// A CJK closing bracket (」, ）, 】…).
    static func closesCJK(_ c: UnicodeScalar) -> Bool { c.properties.generalCategory == .closePunctuation && c.value >= 0x3000 }
}

/// Writes where the lines of the texts under it are (`LiftLines`).
struct LiftLinesReader: ViewModifier {
    let write: (String) -> Void

    func body(content: Content) -> some View {
        if LiftLines.on {
            content.readingShownWords { shown in
                GeometryReader { geo in
                    let s = LiftLines.lines(shown.shown, at: geo.frame(in: .global).origin)
                    Color.clear.onAppear { write(s) }.onChange(of: s) { _, s in write(s) }
                }
            }
        } else {
            content
        }
    }
}
#endif

/// The dimmed chat, the lifted message and its bar, over the whole chat page while a message is pressed.
struct MessageLiftLayer: View {
    var menu: MessageMenu

    var body: some View {
        if let lift = menu.lift {
            GeometryReader { geo in
                LiftedMessage(lift: lift, menu: menu, space: geo.frame(in: .global))
            }
            .ignoresSafeArea()
            .transition(.opacity)
        }
    }
}

private struct LiftedMessage: View {
    var lift: MessageLift
    var menu: MessageMenu
    /// The layer's frame, global.
    var space: CGRect
    @State private var fade = Fade()
    /// The drawn copy's height (the same as the message's where it was, unless something it reads differs).
    @State private var height: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private struct Fade: Equatable {
        var top = false
        var bottom = false
    }

    /// Around an answer on its card: less at the sides, where the answer already keeps the page's margin.
    private var pad: EdgeInsets {
        lift.card ? EdgeInsets(top: Theme.Space.m, leading: Theme.Space.s, bottom: Theme.Space.m, trailing: Theme.Space.s) : EdgeInsets()
    }

    /// The screen inside its safe area, in the layer's coordinates. The window's: the chat page may be laid out past the
    /// safe area and report none.
    private var bounds: CGRect {
        var safe = space
        #if os(iOS)
        if let w = UIApplication.shared.connectedScenes.compactMap({ ($0 as? UIWindowScene)?.keyWindow }).first {
            safe = w.bounds.inset(by: w.safeAreaInsets).intersection(space)
        }
        #endif
        return safe.offsetBy(dx: -space.minX, dy: -space.minY)
    }

    /// Its words are being selected (`MessageMenu.inPlace`).
    private var selecting: Bool { menu.inPlace != nil }

    var body: some View {
        let bounds = bounds
        let f = lift.frame.offsetBy(dx: -space.minX, dy: -space.minY)
        let message = CGRect(x: f.minX - pad.leading, y: f.minY - pad.top, width: f.width + pad.leading + pad.trailing,
                             height: height > 0 ? height : f.height + pad.top + pad.bottom)
        let count = MessageActionBar.count(id: lift.bar.id, isUser: lift.bar.isUser, menu: menu, pending: lift.bar.pending,
                                           deletes: lift.bar.delete != nil)
        let barWidth = min(MessageActionBar.width(count: count), bounds.width - 2 * Theme.Space.s)
        let p = LiftLayout.place(message: message, press: lift.press.y + pad.top, bar: CGSize(width: barWidth, height: MessageActionBar.height),
                                 bounds: bounds, margin: Theme.Space.s, gap: Theme.Space.s, trailing: lift.bar.isUser)
        ZStack(alignment: .topLeading) {
            #if DEBUG
            if LiftLines.on {
                let l = LiftLines.shared
                Color.clear.allowsHitTesting(false).accessibilityElement().accessibilityIdentifier("lift-lines")
                    .accessibilityValue("chat=\(l.chat[lift.bar.id] ?? "")|lift=\(l.lift)|edge=\(String(format: "%.1f", l.edge))|select=\(selecting ? l.select : "")")
            }
            #endif
            backdrop
            preview(p, whole: message.height)
                .frame(width: p.preview.width, height: p.preview.height)
                .position(x: p.preview.midX, y: p.preview.midY)
            // Out of the way of the edit menu while the words are selected.
            if !selecting {
                MessageActionBar(id: lift.bar.id, text: lift.bar.text, isUser: lift.bar.isUser, date: lift.bar.date, menu: menu,
                                 pending: lift.bar.pending, from: lift.bar.from, delete: lift.bar.delete,
                                 itemWidth: MessageActionBar.itemWidth(barWidth, count: count))
                    .position(x: p.bar.midX, y: p.bar.midY)
                    .transition(.scale(scale: 0.85, anchor: p.below ? .top : .bottom).combined(with: .opacity))
            }
        }
        .accessibilityAddTraits(.isModal)
        .accessibilityAction(.escape) { menu.close() }
    }

    /// Dims the chat behind, which `liftBlur` blurs; a tap or a pull down puts the message back. While its words are
    /// selected, a tap ends that and brings the bar back.
    private var backdrop: some View {
        Rectangle()
            .fill(Theme.Lift.scrim)
            .contentShape(.rect)
            .onTapGesture { if selecting { menu.endSelection() } else { menu.close() } }
            .gesture(DragGesture(minimumDistance: Theme.Space.l).onEnded { if $0.translation.height > Theme.Size.tapTarget { menu.close() } })
            .accessibilityElement()
            .accessibilityLabel(String(localized: "Close"))
            .accessibilityAddTraits(.isButton)
            .accessibilityAction { menu.close() }
    }

    @ViewBuilder private func preview(_ p: LiftLayout.Placement, whole: CGFloat) -> some View {
        let cut = p.preview.height < whole - 1
        let shape = RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
        ScrollView(.vertical) {
            Offers(offer: lift.offer) { lift.preview }
                .environment(\.liftSelect, menu.inPlace)
                .environment(\.liftCopy, true)
                .environment(menu)
                .environment(from: lift.environment) // outside those: they are this copy's own
                .frame(width: lift.frame.width, alignment: .leading)
                .padding(pad)
                .allowsHitTesting(selecting)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        }
        #if os(iOS)
        .gesture(LiftPress(enabled: !selecting) { menu.selectInPlace(at: $0) })
        #endif
        .scrollDisabled(!cut)
        .scrollIndicators(.hidden)
        // Opens at the part that was pressed (the same place on screen).
        .defaultScrollAnchor(UnitPoint(x: 0, y: cut ? p.offset / max(whole - p.preview.height, 1) : 0))
        .onScrollGeometryChange(for: Fade.self) { g in
            Fade(top: g.contentOffset.y > 1, bottom: g.contentOffset.y + g.containerSize.height < g.contentSize.height - 1)
        } action: { _, f in fade = f }
        .mask {
            if cut {
                LinearGradient(stops: stops(height: p.preview.height), startPoint: .top, endPoint: .bottom)
            } else {
                Rectangle()
            }
        }
        .background { if lift.card { shape.fill(Theme.card) } }
        .clipShape(lift.card ? AnyShape(shape) : AnyShape(Rectangle()))
        .overlay { if lift.card { shape.stroke(Theme.cardStroke, lineWidth: 0.5) } }
        .shadow(color: Theme.Fill.scrim, radius: Theme.Space.l, y: Theme.Space.xs)
        // One element when it all shows; a long one keeps its parts, for VoiceOver to go through them and scroll it there.
        .accessibilityElement(children: selecting || cut ? .contain : .combine)
        .accessibilityIdentifier("message-lift")
    }

    /// Opaque, fading over the fade's width at an end with more of the message beyond it.
    private func stops(height: CGFloat) -> [Gradient.Stop] {
        let f = min(Theme.Size.fadeEdge * 2 / max(height, 1), 0.25)
        return [.init(color: fade.top ? .clear : .black, location: 0), .init(color: .black, location: f),
                .init(color: .black, location: 1 - f), .init(color: fade.bottom ? .clear : .black, location: 1)]
    }
}
