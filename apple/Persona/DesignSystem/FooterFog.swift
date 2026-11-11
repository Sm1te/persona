import SwiftUI

/// The chat's last stretch, where the list runs on behind its footer (#3696): the page's own color laid over it, clear
/// at the top and solid by the bottom of the footer's controls, along the eased curve of the fog under the top bar
/// (`TopFog`, #2909).
/// No line cuts a message: what scrolls down dims as it nears the footer, goes on dimming behind the capsules over the
/// composer, and is gone by the composer itself. The footer's controls and the jump-to-latest arrow are drawn over it, so
/// they float on the chat rather than sit under a band of page.
///
/// One gradient of a fixed size laid at the list's bottom: scrolling, a streaming reply and typing never draw it again,
/// and the keyboard only moves it. The page color alone, no material: on the solid page it shows exactly what a mask on
/// the list would, without a tinted band where nothing is behind it and without an offscreen pass over the whole list.
struct FooterFog: View {
    /// How tall it is.
    var height: CGFloat
    /// How much of it at the bottom is solid page: what is behind the footer's last gap, under its controls, is gone, not
    /// faintly there between them and the composer.
    var solid: CGFloat = 0

    var body: some View {
        let start = height > 0 ? min(1, max(0, solid / height)) : 0
        LinearGradient(stops: EasedFade.stops(Theme.pageBackground, from: start, over: 1 - start), startPoint: .bottom, endPoint: .top)
            .frame(height: height)
            .allowsHitTesting(false)
            .accessibilityHidden(true)
    }
}

/// Opaque up to `start`, then out to clear over `length` (unit points of the gradient) along a smoothstep curve: no kink
/// where the fade begins or ends for the eye to read as an edge (#2909). The fogs at both ends of the chat use it.
enum EasedFade {
    static func stops(_ color: Color, from start: CGFloat, over length: CGFloat) -> [Gradient.Stop] {
        let steps = 8
        let fade = (0...steps).map { i -> Gradient.Stop in
            let t = CGFloat(i) / CGFloat(steps)
            let eased = t * t * (3 - 2 * t)
            return .init(color: color.opacity(1 - eased), location: min(1, start + length * t))
        }
        return [.init(color: color, location: 0)] + fade
    }
}
