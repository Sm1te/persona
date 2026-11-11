import CoreGraphics

/// Where the 「+」 panel (`AttachSheet`) sits and where it goes when let go: Telegram's attachment menu.
///
/// The sheet is as tall as the screen under the status bar (less `Theme.Attach.topGap`). `offset` is how far its top is
/// pushed down from there: `rest` at rest, 0 open all the way. `drop` is how far the whole sheet has been pulled down
/// past rest, on its way out. A finger moves both one to one, no rubber band: up from rest it opens the sheet, down past
/// rest it drops it.
nonisolated enum AttachSheetPhysics {
    enum Settle: Equatable {
        case dismiss
        case open
        case rest
    }

    /// How far down the sheet rests, from the screen's longer side (a quarter of an ordinary phone's).
    static func rest(width: CGFloat, height: CGFloat) -> CGFloat {
        let fraction = width <= Theme.Attach.narrowWidth ? Theme.Attach.restFractionNarrow : Theme.Attach.restFraction
        return (max(width, height) * fraction).rounded(.down)
    }

    /// The sheet's place after the finger moved `travel` (down positive) from where it started (`startOffset`).
    static func follow(startOffset: CGFloat, travel: CGFloat, rest: CGFloat) -> (offset: CGFloat, drop: CGFloat) {
        let total = startOffset + travel
        return (min(max(total, 0), rest), max(0, total - rest))
    }

    /// Where it goes when let go. `velocity` is points per second, down positive. `gridAtTop`: open all the way, a quick
    /// fling closes it only when the grid under the finger was not scrolled (else the fling was the grid's).
    static func settle(offset: CGFloat, drop: CGFloat, velocity: CGFloat, rest: CGFloat, startedOpen: Bool, gridAtTop: Bool) -> Settle {
        let v = startedOpen && !gridAtTop ? 0 : velocity
        if drop > Theme.Attach.dismissDistance { return .dismiss }
        if drop > 0, v > Theme.Attach.dismissSpeed { return .dismiss }
        if startedOpen, offset == 0, drop == 0, v > Theme.Attach.dismissFling { return .dismiss }
        if startedOpen {
            return v > Theme.Attach.settleSpeed || offset > rest / 2 ? .rest : .open
        }
        return v < -Theme.Attach.settleSpeed || offset < rest / 2 ? .open : .rest
    }

    /// From 1 at rest to 0 open all the way: the sheet narrows and its top sits a little lower as it rests.
    static func restProgress(offset: CGFloat, rest: CGFloat) -> CGFloat {
        rest > 0 ? min(1, max(0, offset / rest)) : 0
    }

    /// The sheet's horizontal scale: `Theme.Attach.restInset` narrower at rest, the screen's width open.
    static func scale(offset: CGFloat, rest: CGFloat, width: CGFloat) -> CGFloat {
        guard width > 0 else { return 1 }
        return 1 - Theme.Attach.restInset / width * restProgress(offset: offset, rest: rest)
    }

    /// How far a keyboard ending at `end` (screen coordinates, as its notification gives them) reaches up from the bottom
    /// of `screen`: 0 when it ends off the screen (going down) or either is unknown.
    static func keyboardReach(end: CGRect?, screen: CGRect?) -> CGFloat {
        guard let end, let screen, !end.isEmpty else { return 0 }
        return max(0, screen.maxY - max(end.minY, screen.minY))
    }

    /// A spring's starting speed for the way left: the finger's speed over the distance still to go, as a share per second.
    static func springVelocity(_ velocity: CGFloat, remaining: CGFloat) -> CGFloat {
        abs(remaining) > 1 ? abs(velocity / remaining) : 0
    }

    /// The tab bar's width for `tabs` tabs on a screen `width` wide.
    static func barWidth(tabs: Int, width: CGFloat) -> CGFloat {
        let widths = Theme.Attach.barWidths
        if tabs >= 2, tabs - 2 < widths.count { return min(widths[tabs - 2], width - Theme.Attach.barInset * 2) }
        return width - Theme.Attach.barInset * 2
    }

    /// The grid's cell side: `columns` across `width`, `Theme.Attach.gridSpacing` apart, rounded down to the pixel.
    static func cell(width: CGFloat, columns: Int = Theme.Attach.columns, scale: CGFloat = 3) -> CGFloat {
        let side = (width - Theme.Attach.gridSpacing * CGFloat(columns - 1)) / CGFloat(columns)
        return (side * scale).rounded(.down) / scale
    }
}
