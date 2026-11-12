import CoreGraphics

/// Where the chat stands with its newest end: the one truth ↓ (Jump to Latest), following new content and holding it back
/// come from (docs/apple/ios-state.md「Back to latest」, #1046). There used to be two: ↓ looked at a 1 pt sentinel at the newest end,
/// following was a flag set and cleared in a dozen places, kept in step by patches (#597, #754); a drag of 40 pt
/// while a reply streamed stopped following and held the stream back, and opening the running turn's work line froze it.
///
/// It changes only on three kinds of input:
/// - the user's own scrolling, once it ends (`released`): near the newest it follows, further it reads. While a finger
///   is on the list nothing changes, and what arrives waits;
/// - what the app does: ↓, sending, another chat (`follow`), a landing (`land` / `landed`), a jump to a quote or a call
///   (`read`), a header opened or folded (`hold` / `endHold`);
/// - the scroll geometry (`scrolled`), which only says whether the list is near the newest.
/// The scroll phase tells the user's scrolling from the app's (Vercel's `useScrollToBottom` cannot: it only has the
/// position). `ChatView` reports and reads; the rules are here, and `ChatFollowStateTests` checks them.
public nonisolated struct ChatFollow: Equatable, Sendable {
    public enum Mode: Equatable, Sendable {
        /// New content brings the list to the newest.
        case following
        /// The user is reading elsewhere: what arrives at the newest end waits (inserted there it would push what they
        /// read), and ↓ shows.
        case reading
        /// The app is taking the list to a saved place or the latest answer.
        case landing
        /// A tapped header is held under the finger while what it opens or closes settles (`ChatPin`): nothing pulls
        /// the list and nothing is held back, so a running turn opened at the newest keeps growing. Anchored (opened at
        /// the newest end), it stays held until the user takes the list, folds it, or goes back to the newest.
        case holding
    }

    /// How close to the newest end still counts as there (pt), as in Vercel's hook: a glance a few lines up while a
    /// reply streams does not stop following.
    public static let tolerance: CGFloat = 100

    public private(set) var mode: Mode = .following
    /// The scroll geometry: the list is within `tolerance` of its newest end. True until it is measured.
    public private(set) var near = true
    /// A finger on the list, or the glide after it.
    public private(set) var touching = false
    /// The finger took the list beyond `tolerance` before letting go: ↓ shows from then on, until the release decides.
    public private(set) var draggedAway = false
    /// What a hold goes back to.
    private var resume: Mode = .following
    /// The hold lasts until something ends it (`hold(anchored:)`), not until the header settles.
    public private(set) var anchored = false

    public init() {}

    /// New content brings the list to the newest end.
    public var follows: Bool { mode == .following && !touching }
    /// What arrives at the newest end waits: reading, and while a finger has the list. Under a finger, what grew at the
    /// newest end (a reply streaming) pushed what the finger held up the screen: by a line or two each time, 23 and
    /// 39 pt in one short drag (2026-11-04, StreamingScrollTests). It comes in once the finger lets go, if the list
    /// follows then.
    public var holdsBack: Bool { mode == .reading || touching || draggedAway }
    /// ↓ shows (once the chat has settled on entry; `ChatView`). While a header is held for a moment, by what the hold
    /// goes back to: a work line opened pushes the newest screens away for that moment, and ↓ came up for it only.
    /// Anchored, the steps grow past the screen and ↓ is the way to them.
    public var arrow: Bool { !near && ((mode == .holding && !anchored ? resume : mode) != .following || draggedAway) }

    // MARK: The scroll geometry

    /// The list's distance from its newest end (pt).
    public mutating func scrolled(gap: CGFloat) {
        near = gap <= Self.tolerance
        if touching, !near { draggedAway = true }
    }

    // MARK: The user's scrolling

    /// A finger moved the list (the scroll phase's `tracking` / `interacting`).
    public mutating func touched() { touching = true }

    /// The finger lifted without having scrolled the list (a tap on a row or a button in it, a press held still): nothing
    /// was scrolled, so nothing is decided and the list goes on as it was (anchored stays anchored). Taken for the end of
    /// a scroll, every tap on a tool row near the newest end decided "following" and ended the anchor, and the next step
    /// took the list to the newest end, the rows just opened off the top (#3005).
    public mutating func tapped() {
        touching = false
        draggedAway = false
    }

    /// The user's scrolling ended (the finger lifted, or its glide stopped): where it stopped decides.
    public mutating func released() {
        guard touching else { return }
        touching = false
        draggedAway = false
        anchored = false
        mode = near ? .following : .reading
    }

    // MARK: What the app does

    /// ↓, a message sent, another chat or subagent, back to the chat from a file: to the newest, following.
    public mutating func follow() {
        mode = .following
        touching = false
        draggedAway = false
        anchored = false
    }

    /// A jump the app makes away from the newest (to a quoted message, a tool call): reading there.
    public mutating func read() {
        mode = .reading
        anchored = false
    }

    /// On the way to a saved place or the latest answer.
    public mutating func land() { mode = .landing }

    /// The landing is over: following if it ended near the newest, reading otherwise.
    public mutating func landed() {
        guard mode == .landing else { return }
        mode = near ? .following : .reading
    }

    /// A header was tapped open or closed. Opening something is not taking the list: whatever it was doing (following
    /// at the newest, reading further up) it goes back to once the header has settled. `anchored` (a running work line
    /// opened at the newest end, its header raised under the top bar; one opened or folded where the chat would follow):
    /// held until the user takes the list or goes back to the newest (↓, sending, a fold that took the list there);
    /// following would scroll the header away, holding back would freeze the turn (#1046).
    public mutating func hold(anchored: Bool = false) {
        if mode != .holding {
            resume = mode == .landing ? (near ? .following : .reading) : mode
        } else if self.anchored {
            // Folded (or another line tapped) while anchored: it was opened at the newest end, following, and goes
            // back there (the pin keeps the folded header where it was, short of the newest).
            resume = .following
        }
        mode = .holding
        self.anchored = anchored
    }

    /// A row in the chat opened or closed in place (a tool call, a thought, a card: `toggleInPlace`): the user reads it.
    /// Where what comes in next would take the list to the newest end (following, or about to go back to it), the list
    /// is held instead, anchored as a work line opened at the newest end is, until the user takes the list, taps ↓ or
    /// sends; true if so, and the chat keeps the row where it is. Left following, the next step, the turn's answer or
    /// the next turn took the list to the newest end, the rows the user had just opened off the top (#3005). Reading
    /// further up, nothing comes in meanwhile (held back) and it stays reading.
    public mutating func toggledRow() -> Bool {
        guard followsNext else { return false }
        hold(anchored: true)
        return true
    }

    /// What comes in next would take the list to the newest end once no finger holds it: following, held for a moment on
    /// the way back to it, or anchored (held where the user reads instead of following). A row is toggled by a tap, its
    /// finger still down (`touching`) when the row's action runs.
    public var followsNext: Bool {
        mode == .holding ? anchored || resume == .following : mode == .following
    }

    /// The header has settled. A finger that took the list meanwhile has decided already (`released`); an anchored
    /// hold goes on.
    public mutating func endHold() {
        guard mode == .holding, !anchored else { return }
        mode = resume
    }
}
