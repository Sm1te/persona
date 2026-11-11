import SwiftUI

/// The height the chat page has (the list and the composer together), for what sits above the composer to size
/// itself by. A class of its own so that only the views reading it are redrawn as it changes (the keyboard sliding).
@Observable final class FooterRoom {
    var height: CGFloat = 0
    /// The footer's own height (the cards, the composer and what is between them): the conversation has the rest.
    var footer: CGFloat = 0
    /// How short the conversation's list may get besides what the header covers (its margin and the hairline).
    var listFloor: CGFloat = 0
    /// The header over the list: its height now (the Agents header folds to a slim bar), read only while the keyboard is
    /// up. Nil: a fixed one (`fixedHeader`).
    @ObservationIgnored weak var chrome: ChatChrome?
    var fixedHeader: CGFloat = 0
    var header: CGFloat {
        guard let chrome else { return fixedHeader }
        return chrome.height - min(max(chrome.collapse, 0), chrome.maxCollapse)
    }
}

extension EnvironmentValues {
    /// The height a pending card's form measures its cap by: the length of the container the cards sit in (the page's
    /// window or column), as `containerRelativeFrame` gives it outside their scroll area. 0 until measured.
    @Entry var cardRoom: CGFloat = 0
    /// The height a whole card may take while the keyboard is up (the cards' area): its form scrolls inside so that the
    /// card fits it. A card taller than the area, its form scrolling inside too, took the swipes, and the area never
    /// showed its last options (#1912). 0: no such limit.
    @Entry var cardFits: CGFloat = 0
}

/// The agent's pending cards (questions, permissions) above the composer. Where they fit they are laid out as they
/// always were; past half the page they scroll on their own under 「N 个待处理」, the permissions first and the newest
/// next, so the most urgent is in sight: the page's header, its back button and the conversation stay on screen
/// however many there are (#926).
struct PendingCards: View {
    struct Card: Identifiable {
        var interaction: Interaction
        /// Answerable from here (the session lets the phone approve); debug samples always are.
        var enabled: Bool
        var id: String { interaction.id }
    }

    var cards: [Card]
    var room: FooterRoom
    @State private var contentHeight: CGFloat = 0
    /// The height the cards' area has now (what `height(over:cap:)` gave it last).
    @State private var shown: CGFloat = 0
    /// The cards' container's length (see `cardRoom`).
    @State private var containerLength: CGFloat = 0
    /// 「N 个待处理」's height, once it showed (the cards' room in a voice call).
    @State private var countHeight: CGFloat = 0
    /// What the footer holds besides the cards' area (`room.footer - shown`): the keyboard's cap is the page less it. It does
    /// not move with the cap, so a cap made from it settles at once; held while it moves less than a point, so the float
    /// noise of the subtraction never reaches the cap and redraws the page every frame (#1580).
    @State private var footerRest: CGFloat = 0
    @State private var position = ScrollPosition(edge: .top)

    /// The share of the page the cards may take before they scroll.
    private static let share: CGFloat = 0.5

    var body: some View {
        let cap = cap
        // With the keyboard up the area is laid out as scrolling whether or not the cards fit: its margins, count and clip
        // then do not change as a card's form fits itself to it, so nothing goes back and forth.
        let keyboard = keyboardRoom
        let over = cap > 0 && (keyboard || contentHeight > cap + 1)
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            // Not over a lone card with the keyboard up: it says nothing there, and the room is the card's.
            if over, !(keyboard && cards.count == 1) {
                Text("\(cards.count) pending")
                    .font(Theme.Font.sectionHeader).foregroundStyle(.secondary).lineLimit(1)
                    .padding(.horizontal, Theme.Space.xs)
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { h in
                        if h > 0, abs(h - countHeight) >= 1 { countHeight = h } // the last one stays: the cap in a call counts it
                    }
                    .accessibilityIdentifier("pending-cards-count")
            }
            // One scroll view whether or not they fit: a card keeps what is typed into it in its own state.
            ScrollView {
                VStack(spacing: Theme.Space.m) {
                    ForEach(over ? Self.urgentFirst(cards) : cards) { card in
                        InteractionPanel(interaction: card.interaction).disabled(!card.enabled)
                            .chatMark("chat.card." + card.interaction.id)
                    }
                }
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { contentHeight = $0 }
            }
            .scrollPosition($position)
            .accessibilityIdentifier("pending-cards")
            // Fitting, it is the stack it always was: not scrollable, nothing clipped (the cards' shadows).
            // Scrolling only when the cards do not fit: an area with nothing to scroll neither moves nor bounces. Not
            // `scrollDisabled`, which passes down and stopped a card's own form from scrolling (#1912).
            .scrollBounceBehavior(.basedOnSize)
            // Scrolling, what scrolls out is hidden by a mask, not the area's own clip: clipped to the area's rectangle,
            // a card's shadow (wider than the card) was cut flush with its sides and showed as a gray block with square
            // corners behind it (#2065). The mask reaches out to the footer's margins at the sides and fades out in the
            // margins above and below, where the shadow is.
            .scrollClipDisabled()
            .mask { edgeFade(over: over) }
            // Scrolling, room for the first card's shadow under the top edge.
            .contentMargins(.top, over ? Theme.Space.l : 0, for: .scrollContent)
            .contentMargins(.bottom, over ? Theme.Space.s : 0, for: .scrollContent)
            // As tall as the cards as measured, at most the cap; before the first measure, its own size.
            .frame(height: height(over: over, cap: cap, keyboard: keyboard))
            .fixedSize(horizontal: false, vertical: contentHeight == 0)
            .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { shown = $0 }
        }
        .environment(\.cardRoom, containerLength)
        .environment(\.cardFits, keyboard ? cap - Theme.Space.l - Theme.Space.s : 0)
        // The container's length, read out here where the container is still the page's, not the scroll area.
        .background {
            Color.clear.containerRelativeFrame(.vertical)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { containerLength = $0 }
                .hidden()
        }
        // A new card while they scroll: back to the top, where the most urgent is.
        .onChange(of: cards.map(\.id)) { old, new in
            if over, !Set(new).isSubset(of: Set(old)) { withAnimation(Theme.Motion.standard) { position.scrollTo(edge: .top) } }
        }
        .onChange(of: room.footer - shown, initial: true) { _, o in
            if abs(o - footerRest) >= 1 { footerRest = o }
        }
    }

    /// The cards' mask: while they scroll, clear at the area's top and bottom edges and opaque from the cards' margins
    /// in, out to the footer's margins at the sides; while they fit, the area and some room around it for the shadows.
    private func edgeFade(over: Bool) -> some View {
        VStack(spacing: 0) {
            LinearGradient(colors: [.clear, .black], startPoint: .top, endPoint: .bottom).frame(height: over ? Theme.Space.l : 0)
            Rectangle()
            LinearGradient(colors: [.black, .clear], startPoint: .top, endPoint: .bottom).frame(height: over ? Theme.Space.s : 0)
        }
        .padding(.horizontal, -Theme.Space.m)
        .padding(.vertical, over ? 0 : -Theme.Space.xl)
    }

    /// How tall the cards may be before they scroll: half the page. While the keyboard is up (the page is short, and a
    /// card is often what is being answered) they take what the conversation has, down to the list's floor, so the card
    /// is whole or scrolls in all the room there is and the composer stays over the keyboard (#1912: half of what the
    /// keyboard left showed a question's first two options, and on a small iPhone pushed the composer under it).
    /// The keyboard is up and the page's room is known: the cards take the conversation's room (`cap`).
    private var keyboardRoom: Bool { KeyboardState.shared.isUp && room.footer > 0 }

    private var cap: CGFloat {
        let half = room.height * Self.share
        guard keyboardRoom else {
            // In a voice call the bar is taller than the composer it replaces (at large text sizes, much taller), and the
            // conversation's last rows are the call's captions: the cards give up what of their half would leave the
            // conversation less than `voiceThreadRoom` over the floor. Measured against the header as it stands open
            // (`fixedHeader`), not as it folds: the list's height moves the fold, and the fold moved the cap, back and
            // forth every frame.
            // What the footer holds besides the cards comes from the bar's own height (`VoiceBarRoom`) and the count's,
            // never from the footer as measured: that moves with the cap, and a cap within a point of half flipped between
            // the two every frame.
            guard inCall, room.height > 0 else { return half }
            let others = VoiceBarRoom.shared.height + countHeight + Theme.Space.s * 3 + Theme.Space.m
            let fits = room.height - room.fixedHeader - room.listFloor - Theme.Size.voiceThreadRoom - others
            return max(Theme.Size.tapTarget, min(half, fits))
        }
        // Before the first measure (a card arriving with the keyboard up), the footer as it stands: the cards are not in it yet.
        let rest = footerRest > 0 ? footerRest : room.footer - shown
        return max(Theme.Size.tapTarget, room.height - rest - room.header - room.listFloor)
    }

    /// A voice call is on in the cards' conversation (docs/assistant/voice.md 6.2).
    private var inCall: Bool {
        cards.first.map { VoiceCalls.shared.isLive(on: $0.interaction.sessionKey) } ?? false
    }

    /// As tall as the cards (with the scrolling margins), at most the cap; with the keyboard up no taller than they need,
    /// so a card that fits sits right over the composer.
    private func height(over: Bool, cap: CGFloat, keyboard: Bool) -> CGFloat? {
        guard contentHeight > 0 else { return nil }
        guard over else { return contentHeight }
        return keyboard ? min(cap, contentHeight + Theme.Space.l + Theme.Space.s) : cap
    }

    /// Permissions first (they hold a tool call back), then everything newest first.
    static func urgentFirst(_ cards: [Card]) -> [Card] {
        cards.enumerated().sorted { a, b in
            let (pa, pb) = (a.element.interaction.kind == .permission, b.element.interaction.kind == .permission)
            if pa != pb { return pa }
            let (ta, tb) = (a.element.interaction.createdAt ?? .distantPast, b.element.interaction.createdAt ?? .distantPast)
            return ta != tb ? ta > tb : a.offset > b.offset
        }.map(\.element)
    }
}
