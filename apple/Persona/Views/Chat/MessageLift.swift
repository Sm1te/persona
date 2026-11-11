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
struct MessageLift {
    var bar: MessageBarInfo
    var frame: CGRect
    var press: CGPoint
    var preview: AnyView
    var card: Bool
}

/// Where a pressable part is, written on every scrolled frame without drawing anything again (not observed).
final class LiftPlace {
    nonisolated struct Frames: Equatable {
        var global: CGRect = .zero
        /// In its row's coordinates (`MessageMenu.row`), where the press is reported.
        var row: CGRect = .zero
    }
    var frames = Frames()
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
}

extension View {
    /// The chat page under `MessageLiftLayer`: blurred a little while a message is lifted over it. The radius goes to
    /// zero rather than the modifier away, so the page keeps its identity (and its scroll position).
    func liftBlur(_ menu: MessageMenu) -> some View { modifier(LiftBlur(menu: menu)) }
}

private struct LiftBlur: ViewModifier {
    var menu: MessageMenu

    func body(content: Content) -> some View {
        // Not opaque: the page's background is behind this view, not in it, and an opaque blur fills what is clear with black.
        content.blur(radius: menu.lift == nil ? 0 : Theme.Lift.blur)
    }
}

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
    /// The drawn message's height, which its words take again while they are selected (laid out by a text view).
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
                             height: selecting && height > 0 ? height : f.height + pad.top + pad.bottom)
        let count = MessageActionBar.count(id: lift.bar.id, isUser: lift.bar.isUser, menu: menu, pending: lift.bar.pending,
                                           deletes: lift.bar.delete != nil)
        let barWidth = min(MessageActionBar.width(count: count), bounds.width - 2 * Theme.Space.s)
        let p = LiftLayout.place(message: message, press: lift.press.y + pad.top, bar: CGSize(width: barWidth, height: MessageActionBar.height),
                                 bounds: bounds, margin: Theme.Space.s, gap: Theme.Space.s, trailing: lift.bar.isUser)
        ZStack(alignment: .topLeading) {
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
            lift.preview
                .environment(\.liftSelect, menu.inPlace)
                .environment(menu)
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
        .accessibilityElement(children: selecting ? .contain : .combine)
    }

    /// Opaque, fading over the fade's width at an end with more of the message beyond it.
    private func stops(height: CGFloat) -> [Gradient.Stop] {
        let f = min(Theme.Size.fadeEdge * 2 / max(height, 1), 0.25)
        return [.init(color: fade.top ? .clear : .black, location: 0), .init(color: .black, location: f),
                .init(color: .black, location: 1 - f), .init(color: fade.bottom ? .clear : .black, location: 1)]
    }
}
