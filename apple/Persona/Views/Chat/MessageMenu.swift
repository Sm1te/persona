import SwiftUI
#if canImport(UIKit)
import UIKit
import UIKit.UIGestureRecognizerSubclass
#endif

/// Message actions: long-press a message and it is lifted over the dimmed chat with the bar of Copy / Select Text / Quote /
/// Forward (and Edit for your own messages) next to it, as Telegram does (#603, `MessageLift`). Tapping outside closes it.
/// Forward (#3807) takes Share's place on iPhone and iPad where the chat forwards; Share as Image is in its picker.
@Observable
final class MessageMenu {
    var selected: String?
    /// The pressed message, drawn again over the dimmed chat (`MessageLiftLayer`).
    var lift: MessageLift?
    /// Where the press landed, in the message row's coordinates (`MessageMenu.row`), where the bar is placed.
    var anchor: CGPoint = .zero
    /// Text shown in the Mac's Select Text window (iOS selects in place, `inPlace`).
    var selecting: SelectableText?
    /// The lifted message's words selected where they are (#603, `LiftSelection.swift`): a second long press on it, or
    /// Select Text. nil: lifted with its bar.
    var inPlace: InPlaceSelect?
    /// The message being shared as a card.
    var sharing: ShareMessage?
    /// Forward (#3807): the chat makes the pressed message a forward and opens its picker (`forwarding`). nil: the bar
    /// keeps Share (the Mac, a chat that cannot forward).
    @ObservationIgnored var forward: ((MessageBarInfo) -> Void)?
    /// The message whose Forward was tapped: the chat shows the picker (`ForwardSheet`).
    var forwarding: ForwardDraft?
    /// Where the chat's messages come from ("app › leader"), printed on share cards (read when sharing: the
    /// session may not be loaded yet when the chat binds the menu).
    @ObservationIgnored var source: () -> String = { "" }
    /// Who answers in this chat ("Claude" / "Codex" / "TRAEX"), for share cards.
    @ObservationIgnored var agent: () -> String = { "Claude" }
    /// Quote: the text, the message's id, whether it is the user's own, and the part quoted (nil: the whole message;
    /// `ContextRef.selectionPart`: words picked in Select Text).
    @ObservationIgnored var quote: ((String, String, Bool, String?) -> Void)?
    /// A message dragged to the left is quoted (Telegram's reply, #1058): Agents chats. The assistant's pages sit in
    /// the shell's pager, whose own sideways swipe this would take. Observed: the chat turns it on as it appears, after
    /// its first rows may have been drawn without it (they never got the drag, now and then).
    var swipeQuotes = false
    @ObservationIgnored var edit: ((String) -> Void)?
    /// Branch to New Chat (#2137): a new conversation from the reply with this item id. canFork: the chat's session can.
    @ObservationIgnored var fork: ((String) -> Void)?
    @ObservationIgnored var canFork: () -> Bool = { false }
    /// Rewind to Here (#2915) on your own message: (item id, its text, what to restore). rewindBlock: nil when the chat's
    /// session never offers it, else why it is off now ("" when on).
    @ObservationIgnored var rewind: ((String, String, Rewind.Restore) -> Void)?
    @ObservationIgnored var rewindBlock: () -> String? = { nil }
    /// The message of yours whose Rewind was tapped: the chat asks what to restore (a dialog of the page, like `more`).
    var rewinding: RewindAsk?
    /// The reply whose ⋯ was tapped: the chat shows its menu (a dialog of the page, not of the bar, which the list's
    /// tap-to-close takes away with the same tap).
    var more: String?
    /// The press that opened the bar. Its own lift must not close it: the list's simultaneous tap-to-close gesture
    /// recognizes that lift as a tap (only presses longer than ~2 s fail it), and a held finger's small drift shows
    /// up as list scrolling.
    /// How wide the chat is (`MessageMenu.area`): the bar stays inside it, not the screen — beside a list on a wide
    /// display (iPhone Duo open) the screen is much wider than the chat.
    @ObservationIgnored var areaWidth: CGFloat = 0
    /// The chat's coordinate space, set by the chat on its panes.
    static let area = "message-menu-area"
    /// A message row's coordinate space (`messageActionBar`): the press is reported in it, where the bar is placed, not
    /// in the pressed bubble's, which sits at the right of the row for your own messages (#501).
    static let row = "message-menu-row"
    @ObservationIgnored private var pressing = false
    @ObservationIgnored private var pressEnded = Date.distantPast

    #if os(iOS) || os(visionOS)
    func pressChanged(_ state: UIGestureRecognizer.State) {
        switch state {
        case .began: pressing = true
        case .ended, .cancelled, .failed: pressing = false; pressEnded = .now
        default: break
        }
    }
    #else
    /// The Mac's secondary click (LongPressAt in apple/Mac/Media/MessageGestures.swift) reports the same phases.
    func pressChanged(_ state: PressPhase) {
        switch state {
        case .began: pressing = true
        case .ended, .cancelled, .failed: pressing = false; pressEnded = .now
        default: break
        }
    }
    #endif

    /// A tap or a scroll on the list: ignored while the press that opened the bar is held or has just lifted.
    func dismiss() {
        guard !pressing, Date.now.timeIntervalSince(pressEnded) > 0.3 else { return }
        close()
    }

    func close() {
        if selected != nil || lift != nil || inPlace != nil {
            withAnimation(Theme.Motion.conceal) {
                selected = nil
                lift = nil
                inPlace = nil
            }
        }
    }

    /// Selects the lifted message's words where they are: the word at `point` (in the window), or all of them.
    func selectInPlace(at point: CGPoint?) {
        guard lift != nil else { return }
        Haptics.tap()
        withAnimation(Theme.Motion.state) { inPlace = InPlaceSelect(at: point) }
    }

    /// Back to the lifted message with its bar.
    func endSelection() {
        guard inPlace != nil else { return }
        withAnimation(Theme.Motion.conceal) { inPlace = nil }
    }
}

struct RewindAsk: Identifiable, Equatable {
    var id: String
    var text: String
}

struct SelectableText: Identifiable {
    var text: String
    /// Your own message: shown as written, without Markdown (its bubble shows it plain).
    var plain = false
    /// The message's id, for quoting part of it.
    var msg = ""
    /// Its words may be quoted (not a message that has not gone out, #6427).
    var quotable = true
    var id: String { text }
}

extension MessageMenu {
    /// Select Text › Quote (#4549): quotes the picked words of the page's message; nil when the chat does not quote.
    func selectionQuote(of page: SelectableText) -> ((String) -> Void)? {
        guard let quote, page.quotable else { return nil }
        return { picked in quote(picked, page.msg, page.plain, ContextRef.selectionPart) }
    }
}

#if os(iOS)
/// A drag to the left on a message, Telegram's reply (#1058). It fails as soon as the finger goes another way, and
/// the scroll views around it (the chat list, a pager) wait for that: scrolling starts as it always did, while a drag
/// that does go left moves the message and not the list. The left edge's swipe back goes right, so never starts it.
/// On something in the message that scrolls sideways and can still go on to the right (a wide table or code block,
/// #5737) it fails too, so that scrolls; at its right end, or anywhere else, the drag quotes.
final class LeftDragRecognizer: UIPanGestureRecognizer {
    private var start: CGPoint?

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent) {
        start = touches.first?.location(in: view)
        super.touchesBegan(touches, with: event)
    }

    override func touchesMoved(_ touches: Set<UITouch>, with event: UIEvent) {
        if state == .possible, let start, let p = touches.first?.location(in: view) {
            let dx = p.x - start.x, dy = p.y - start.y
            if hypot(dx, dy) > MessageSwipe.decide {
                guard dx < 0, abs(dx) > abs(dy) * MessageSwipe.slope, !scrollsInstead(at: start) else {
                    state = .failed
                    return
                }
            }
        }
        super.touchesMoved(touches, with: event)
    }

    override func reset() {
        super.reset()
        start = nil
    }

    /// A sideways scroller under where the finger went down has room on to the right: the drag is its.
    private func scrollsInstead(at start: CGPoint) -> Bool {
        guard let view, let window = view.window else { return false }
        return DrawerPan.Coordinator.sidewaysScroller(at: view.convert(start, to: window), in: window, towardsRight: true)
    }

    override func shouldBeRequiredToFail(by other: UIGestureRecognizer) -> Bool {
        other.view is UIScrollView && other is UIPanGestureRecognizer
    }

    // It waits for no one but the scroll views. The message's long press (the action bar) is a child's gesture, which
    // SwiftUI makes this one wait for, and the navigation stack's back pans (the edge, and on iOS 26 anywhere in the
    // content) are around it in a navigation stack: now and then the drag lost those races and never started. A drag
    // that goes left has moved past the press's allowance and is not a way back, so they never both happen.
    private static func standsAside(_ other: UIGestureRecognizer) -> Bool {
        other is UILongPressGestureRecognizer || (other is UIPanGestureRecognizer && !(other.view is UIScrollView))
    }

    override func require(toFail other: UIGestureRecognizer) {
        if Self.standsAside(other) { return }
        super.require(toFail: other)
    }

    override func shouldRequireFailure(of other: UIGestureRecognizer) -> Bool {
        Self.standsAside(other) ? false : super.shouldRequireFailure(of: other)
    }

    override func canBePrevented(by preventing: UIGestureRecognizer) -> Bool {
        Self.standsAside(preventing) ? false : super.canBePrevented(by: preventing)
    }
}

/// The drag as SwiftUI sees it: how far left (≤ 0) while it moves, and where it ended.
struct LeftDrag: UIGestureRecognizerRepresentable {
    var moved: (CGFloat) -> Void
    var ended: (CGFloat) -> Void

    func makeUIGestureRecognizer(context: Context) -> LeftDragRecognizer { LeftDragRecognizer() }

    func handleUIGestureRecognizerAction(_ recognizer: LeftDragRecognizer, context: Context) {
        let x = min(0, recognizer.translation(in: recognizer.view).x)
        switch recognizer.state {
        case .began, .changed: moved(x)
        case .ended: ended(x)
        case .cancelled, .failed: ended(0)
        default: break
        }
    }
}

/// UIKit swipe: it waits for a clear sideways flick and lets scroll views have everything else (a SwiftUI drag on the
/// chat list would take its scrolling).
struct SwipeTo: UIGestureRecognizerRepresentable {
    var direction: UISwipeGestureRecognizer.Direction
    var action: () -> Void

    func makeUIGestureRecognizer(context: Context) -> UISwipeGestureRecognizer {
        let g = UISwipeGestureRecognizer()
        g.direction = direction
        return g
    }

    func handleUIGestureRecognizerAction(_ recognizer: UISwipeGestureRecognizer, context: Context) {
        if recognizer.state == .ended { action() }
    }
}

/// UIKit long press: SwiftUI's own gesture does not say where the finger is. Reports it in the enclosing
/// `messageActionBar` row's coordinates (`MessageMenu.row`), so use it inside one.
struct LongPressAt: UIGestureRecognizerRepresentable {
    var changed: (UIGestureRecognizer.State) -> Void = { _ in }
    var action: (CGPoint) -> Void

    func makeUIGestureRecognizer(context: Context) -> UILongPressGestureRecognizer {
        let g = UILongPressGestureRecognizer()
        g.minimumPressDuration = 0.35
        return g
    }

    func handleUIGestureRecognizerAction(_ recognizer: UILongPressGestureRecognizer, context: Context) {
        changed(recognizer.state)
        if recognizer.state == .began { action(context.converter.location(in: .named(MessageMenu.row))) }
    }
}
#endif

extension View {
    /// The pressable part of a message (the bubble / the text). preview: the same part again, lifted over the dimmed chat
    /// when pressed; card: it is words on the page with nothing around them (an answer), lifted on a card of its own.
    func messageLongPress<P: View>(id: String, card: Bool = false, @ViewBuilder preview: @escaping () -> P) -> some View {
        modifier(MessageLongPress(id: id, card: card, preview: preview))
    }
    /// The whole row of a message: hosts the action bar above the press point and lifts the row above its neighbours.
    /// pending: your message not in the transcript yet (`PendingBubble`, #6427): Copy, Select Text and Share only, no
    /// quote (it may never reach the agent) and no Edit (a waiting one would still go out besides the edited one).
    /// from: who wrote it when that is not the chat's Agent or Persona (a relayed or scheduled message's source, another
    /// session's Claude), for a forward's author.
    /// delete: one only on this phone ends in Delete (#4626), on the bar and in the plain context menu alike.
    func messageActionBar(id: String, text: String, isUser: Bool, date: Date? = nil, pending: Bool = false, from: String? = nil,
                          delete: MessageBarDelete? = nil) -> some View {
        modifier(MessageActionBarHost(id: id, text: text, isUser: isUser, date: date, pending: pending, from: from, delete: delete))
    }
    /// Messages shown with the bar outside the chat's list (the background tasks' drawer): a tap or a drag there closes
    /// it, as on the list, and it closes with the sheet (#1388: it stayed until one of its buttons was used).
    func closesMessageBar() -> some View { modifier(ClosesMessageBar()) }
}

private struct ClosesMessageBar: ViewModifier {
    @Environment(MessageMenu.self) private var menu: MessageMenu?

    func body(content: Content) -> some View {
        content
            .simultaneousGesture(TapGesture().onEnded { menu?.dismiss() })
            .onScrollPhaseChange { _, phase in
                if phase == .tracking || phase == .interacting { menu?.dismiss() }
            }
            .onDisappear { menu?.close() }
    }
}

private struct MessageLongPress<P: View>: ViewModifier {
    @Environment(MessageMenu.self) private var menu: MessageMenu?
    @Environment(\.messageBar) private var bar
    @Environment(\.openFile) private var openFile
    @Environment(\.loadImage) private var loadImage
    @Environment(\.rowMemoScope) private var memoScope
    let id: String
    let card: Bool
    let preview: () -> P
    /// Where the part is, kept without drawing anything again (it changes on every scrolled frame).
    @State private var place = LiftPlace()

    func body(content: Content) -> some View {
        if let menu {
            content
                .onGeometryChange(for: LiftPlace.Frames.self) { g in
                    LiftPlace.Frames(global: g.frame(in: .global), row: g.frame(in: .named(MessageMenu.row)))
                } action: { place.frames = $0 }
                .gesture(LongPressAt(changed: menu.pressChanged) { p in
                    Haptics.tap()
                    menu.anchor = p
                    lift(menu)
                })
                // The screenshot switches (`showMenu`, `showRewind`) pick a message without a press.
                .onChange(of: menu.selected == id) { _, on in if on, menu.lift?.bar.id != id { lift(menu) } }
        } else {
            content
        }
    }

    private func lift(_ menu: MessageMenu) {
        guard let bar else { return }
        let f = place.frames
        let press = CGPoint(x: menu.anchor.x - f.row.minX, y: menu.anchor.y - f.row.minY)
        let shown = preview()
            .environment(\.openFile, openFile)
            .environment(\.loadImage, loadImage)
            .environment(\.rowMemoScope, memoScope)
            .environment(\.chatPin, nil) // what opens in the copy does not move the chat under it
        withAnimation(Theme.Motion.state) {
            menu.selected = id
            menu.lift = MessageLift(bar: bar, frame: f.global, press: press, preview: AnyView(shown), card: card)
        }
    }
}

/// How a drag on a message quotes it (#1058).
enum MessageSwipe {
    /// How far the finger travels before the drag is told to be sideways or not.
    static let decide: CGFloat = 10
    /// Sideways is this much more across than down.
    static let slope: CGFloat = 1.3
    /// How far left a message goes before letting go quotes it: past this, a tap of the haptics and the icon fills.
    static var threshold: CGFloat { Theme.Size.tapTarget * 1.5 }
    /// Past the threshold the message follows the finger at this rate (a rubber band).
    static let stretch: CGFloat = 0.25

    /// Where the message sits for a drag of `x` (≤ 0).
    static func offset(_ x: CGFloat) -> CGFloat {
        x >= -threshold ? x : -threshold + (x + threshold) * stretch
    }
}

private struct MessageActionBarHost: ViewModifier {
    @Environment(MessageMenu.self) private var menu: MessageMenu?
    let id: String
    let text: String
    let isUser: Bool
    let date: Date?
    let pending: Bool
    let from: String?
    let delete: MessageBarDelete?
    @State private var drag: CGFloat = 0
    @State private var armed = false

    func body(content: Content) -> some View {
        if let menu {
            swipeable(content, menu)
                .environment(\.messageBar, MessageBarInfo(id: id, text: text, isUser: isUser, date: date, pending: pending, from: from,
                                                            delete: delete))
                .coordinateSpace(.named(MessageMenu.row))
                .zIndex(menu.selected == id ? 1 : 0)
        } else {
            content.contextMenu {
                CopyButton(text: text)
                if let delete {
                    Button(role: .destructive, action: delete.run) { Label(delete.title, systemImage: Theme.Icon.delete) }
                }
            }
        }
    }

    /// The row as a drag to the left moves it, with the reply icon coming in at the right (#1058).
    @ViewBuilder private func swipeable(_ content: Content, _ menu: MessageMenu) -> some View {
        #if os(iOS)
        if menu.swipeQuotes && !pending {
            content
                .offset(x: MessageSwipe.offset(drag))
                .overlay(alignment: .trailing) {
                    if drag < 0 {
                        let progress = min(1, -drag / MessageSwipe.threshold)
                        ThemedIcon(Theme.Icon.reply)
                            .font(Theme.Font.labelStrong)
                            .foregroundStyle(armed ? Color.white : Color.secondary)
                            .scaledFrame(width: Theme.Size.wellM, height: Theme.Size.wellM)
                            .background(armed ? AnyShapeStyle(Theme.accent) : AnyShapeStyle(Theme.Fill.chip), in: .circle)
                            .scaleEffect(0.6 + 0.4 * progress)
                            .opacity(progress)
                            .padding(.trailing, Theme.Space.s)
                            .accessibilityHidden(true)
                    }
                }
                .gesture(LeftDrag(moved: moved, ended: ended))
                .accessibilityAction(named: "Quote") { menu.quote?(text, id, isUser, nil) }
        } else {
            content
        }
        #else
        content
        #endif
    }

    private func moved(_ x: CGFloat) {
        menu?.close()
        drag = x
        let past = -x >= MessageSwipe.threshold
        if past != armed {
            armed = past
            if past { Haptics.tap() }
        }
    }

    private func ended(_ x: CGFloat) {
        if armed { menu?.quote?(text, id, isUser, nil) }
        armed = false
        withAnimation(Theme.Motion.springBack) { drag = 0 }
    }
}

struct MessageActionBar: View {
    var id = ""
    var text: String
    var isUser: Bool
    var date: Date?
    var menu: MessageMenu
    /// Not in the transcript yet (`messageActionBar(pending:)`): Copy, Select Text and Share.
    var pending = false
    /// Who wrote it, when not the chat's Agent or Persona (`messageActionBar(from:)`).
    var from: String? = nil
    /// Only on this phone (`messageActionBar(delete:)`): Delete last, in the danger color.
    var delete: MessageBarDelete?
    var itemWidth: CGFloat = Self.item

    static let height: CGFloat = 62
    static let item: CGFloat = 58
    static func width(isUser: Bool, more: Bool = false) -> CGFloat { width(count: (isUser ? 5 : 4) + (more ? 1 : 0)) }
    static func width(count: Int) -> CGFloat { item * CGFloat(count) + 12 }
    /// Each action's width when the bar is `width` wide.
    static func itemWidth(_ width: CGFloat, count: Int) -> CGFloat { min(item, (width - 12) / CGFloat(max(count, 1))) }
    /// How many actions the bar has for this message.
    static func count(id: String, isUser: Bool, menu: MessageMenu, pending: Bool = false, deletes: Bool = false) -> Int {
        if pending { return 3 + (deletes ? 1 : 0) }
        return (isUser ? 5 : 4) + (forks(id: id, isUser: isUser, menu: menu) ? 1 : 0) + (rewinds(id: id, isUser: isUser, menu: menu) ? 1 : 0)
    }
    /// Your own message ends in Rewind when the chat's session is a Claude agent's and the message is a transcript line
    /// of its own (`<uuid>#<n>`); while the session cannot, the dialog says why.
    static func rewinds(id: String, isUser: Bool, menu: MessageMenu) -> Bool {
        isUser && menu.rewind != nil && menu.rewindBlock() != nil && Rewind.entry(of: id) != nil
    }
    /// Forward in Share's place (#3807): on iPhone and iPad, for a message in the transcript of a chat that forwards.
    static func forwards(menu: MessageMenu, pending: Bool) -> Bool {
        #if os(iOS)
        return !pending && menu.forward != nil
        #else
        return false
        #endif
    }

    /// A reply ends in ⋯ with Branch to New Chat when the session can fork and the reply is a Claude transcript line of its
    /// own (`<uuid>#<n>`).
    static func forks(id: String, isUser: Bool, menu: MessageMenu) -> Bool {
        guard !isUser, menu.fork != nil, menu.canFork(), let line = id.split(separator: "#").first, id.contains("#") else { return false }
        return UUID(uuidString: String(line)) != nil
    }

    var body: some View {
        HStack(spacing: 0) {
            action(String(localized: "Copy [Message Menu 63d90d]"), Theme.Icon.copy) {
                UIPasteboard.general.string = text
                Haptics.success()
            }
            // "Select Text", not "Select": next to Copy a bare Select reads as picking messages (WeChat's multi-select).
            // On iOS it selects all of the lifted message's words where they are (#603); the Mac opens its window.
            #if os(macOS)
            action(String(localized: "Select Text [Message Menu 4d8efc]"), Theme.Icon.select) {
                menu.selecting = SelectableText(text: text, plain: isUser, msg: id, quotable: !pending)
            }
            #else
            action(String(localized: "Select Text [Message Menu 4d8efc]"), Theme.Icon.select, closes: false) {
                menu.selectInPlace(at: nil)
            }
            #endif
            if !pending {
                action(String(localized: "Quote"), "quote.opening") { menu.quote?(text, id, isUser, nil) }
            }
            if Self.forwards(menu: menu, pending: pending) {
                action(String(localized: "Forward [Message Menu]"), Theme.Icon.forwardMessage) {
                    menu.forward?(MessageBarInfo(id: id, text: text, isUser: isUser, date: date, pending: pending, from: from))
                }
            } else {
                action(String(localized: "Share"), Theme.Icon.share) {
                    menu.sharing = ShareMessage(text: text, isUser: isUser, source: menu.source(), date: date, agent: menu.agent())
                }
            }
            if isUser && !pending {
                action(String(localized: "Edit [Message Menu 051836]"), "pencil") { menu.edit?(text) }
            }
            if Self.rewinds(id: id, isUser: isUser, menu: menu) {
                action(String(localized: "Rewind"), Theme.Icon.rewindChat) { menu.rewinding = RewindAsk(id: id, text: text) }
            }
            if Self.forks(id: id, isUser: isUser, menu: menu) {
                action(String(localized: "More"), Theme.Icon.more) { menu.more = id }
            }
            if let delete { // last, as a menu's destructive item is
                action(delete.title, Theme.Icon.delete, tint: Theme.danger, perform: delete.run)
            }
        }
        .padding(.horizontal, Theme.Space.sm)
        .frame(height: Self.height)
        .background(Theme.Lift.bar, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        // Its edge over the dimmed chat.
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).stroke(Theme.cardStroke, lineWidth: 0.5))
        .shadow(color: .black.opacity(0.25), radius: 10, y: 4)
    }

    /// tint: the danger color for Delete; nil, the primary ink.
    private func action(_ title: String, _ symbol: String, closes: Bool = true, tint: Color? = nil,
                        perform: @escaping () -> Void) -> some View {
        Button {
            perform()
            if closes { menu.close() }
        } label: { label(title, symbol, tint: tint) }
        .buttonStyle(.plain)
        .accessibilityLabel(title) // its word only, not the symbol's own name too
    }

    private func label(_ title: String, _ symbol: String, tint: Color?) -> some View {
        VStack(spacing: Theme.Space.xs) {
            // Symbols differ in height: a fixed box keeps every label on one line.
            ThemedIcon(symbol).font(Theme.Font.heading).dynamicTypeSize(...DynamicTypeSize.xxLarge).scaledFrame(height: Theme.Size.iconS)
            Text(title).font(Theme.Font.label).lineLimit(1).minimumScaleFactor(0.8) // Select Text on a narrow bar
        }
        .foregroundStyle(tint.map { AnyShapeStyle($0) } ?? AnyShapeStyle(HierarchicalShapeStyle.primary))
        .frame(width: itemWidth, height: Self.height)
        .contentShape(.rect)
    }
}

#if os(macOS)
/// The Mac's Select Text: the message in a text view where any part of it can be selected and copied. Copy All copies
/// the message as Copy does; Quote, first in the selection's menu, quotes the picked words in the composer (`quote`,
/// #4549). iOS selects the lifted message's words where they are instead (#603, `LiftSelection.swift`).
struct SelectableTextSheet: View {
    @Environment(\.dismiss) private var dismiss
    var text: String
    var plain = false
    var quote: ((String) -> Void)? = nil

    var body: some View {
        NavigationStack {
            SelectableTextView(text: text, plain: plain, quote: quote.map { quote in { picked in dismiss(); quote(picked) } })
                .navigationTitle("Select Text [Message Menu 4d8efc]")
                .navigationBarTitleDisplayMode(.inline)
                .pageToolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button { dismiss() } label: { Label("Done [Extension System Presentation c0b3fb]", systemImage: Theme.Icon.done) }
                    }
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            UIPasteboard.general.string = text
                            Haptics.success()
                            dismiss()
                        } label: { Label("Copy All [Message Menu 911a2b]", systemImage: Theme.Icon.copy).labelStyle(.titleAndIcon).lineLimit(1) }
                    }
                }
        }
        .presentationDetents([.medium, .large])
    }
}
#endif
