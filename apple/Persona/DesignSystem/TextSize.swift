import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

/// The app's own text size (Settings › Interface Style › Text Size: Small / Standard / Large), on top of the system's. It moves the Dynamic Type
/// size one step down or up from whatever the system is set to, so every `Theme.Font` level, every `@ScaledMetric`
/// and BeautifulUI's sizes (`ScaledMetric` too) move together and keep their ratios; changing the system's text size
/// still applies underneath. At either end of the range it stays where it is.
enum TextSize: String, CaseIterable, Identifiable {
    case small, standard, large

    var id: String { rawValue }

    var title: String {
        switch self {
        case .small: String(localized: "Small", comment: "Text size: small")
        case .standard: String(localized: "Standard", comment: "Text size: standard")
        case .large: String(localized: "Large", comment: "Text size: large")
        }
    }

    private var step: Int {
        switch self {
        case .small: -1
        case .standard: 0
        case .large: 1
        }
    }

    func applied(to system: DynamicTypeSize) -> DynamicTypeSize {
        let all = DynamicTypeSize.allCases
        guard let i = all.firstIndex(of: system) else { return system }
        return all[min(max(i + step, 0), all.count - 1)]
    }

    /// `ui.textSize` (also a launch argument: `-ui.textSize large`).
    static var current: TextSize { TextSize(rawValue: UserDefaults.standard.string(forKey: "ui.textSize") ?? "") ?? .standard }

    /// The Mac's fonts at this size (`Theme.Font.scale`: body 12 / 13 / 14); the Mac has no system text size under it,
    /// so it applies to `.large` as `macLook` does. Nothing on the iPhone, where the text styles follow Dynamic Type.
    func applyToMacFonts() {
        #if os(macOS)
        Theme.Font.scale = Theme.textScale(applied(to: .large))
        #endif
    }
}

extension Theme {
    /// How much larger body text is at this Dynamic Type size than at the default: for text drawn at a point size of
    /// its own (Lark messages and cards keep Feishu's sizes), so it follows the system and Small / Standard / Large too.
    static func textScale(_ size: DynamicTypeSize) -> CGFloat {
        if let k = textScales[size] { return k }
        let traits = UITraitCollection(preferredContentSizeCategory: UIContentSizeCategory(size))
        let k = UIFontMetrics(forTextStyle: .body).scaledValue(for: 17, compatibleWith: traits) / 17
        textScales[size] = k
        return k
    }

    /// Rows in long lists ask for every redraw; there are twelve sizes.
    private static var textScales: [DynamicTypeSize: CGFloat] = [:]
}

/// A value made for the current text size (`Theme.Font.scale`) and kept until it changes: Markdown themes, whose sizes
/// are points, follow Small / Standard / Large on the Mac that way (#833). The scale never changes on the iPhone: made once.
@MainActor final class PerTextScale<Value> {
    private let make: () -> Value
    private var made: (scale: CGFloat, value: Value)?

    init(_ make: @escaping () -> Value) { self.make = make }

    var value: Value {
        if let made, made.scale == Theme.Font.scale { return made.value }
        let v = make()
        made = (Theme.Font.scale, v)
        return v
    }
}

extension View {
    /// The level's line height (`Theme.Level`) for text that wraps: messages, previews, descriptions. It scales with
    /// the text size like the font does.
    func lineHeight(_ level: Theme.Level) -> some View { modifier(LineHeight(level: level)) }
}

private struct LineHeight: ViewModifier {
    let level: Theme.Level
    @Environment(\.dynamicTypeSize) private var typeSize

    func body(content: Content) -> some View {
        content.lineSpacing(level.extraLeading * Theme.textScale(typeSize))
    }
}

extension Theme {
    /// How much a size that goes with text (`Theme.Size`, `scaledFrame`) grows: the text's scale, up to 1.6 × at the
    /// accessibility sizes, where a 44 pt button at the text's 3 × would push everything off screen.
    static func sizeScale(_ size: DynamicTypeSize) -> CGFloat { min(textScale(size), 1.6) }
}

extension View {
    /// A frame whose sizes (`Theme.Size`) follow the text size: the same points at the default size, larger or smaller
    /// with Small / Standard / Large and the system's text size (`Theme.sizeScale`). Fixed and flexible frames as SwiftUI's.
    func scaledFrame(width: CGFloat? = nil, height: CGFloat? = nil, alignment: Alignment = .center) -> some View {
        modifier(ScaledFrame(width: width, height: height, alignment: alignment))
    }

    func scaledFrame(minWidth: CGFloat? = nil, maxWidth: CGFloat? = nil, minHeight: CGFloat? = nil, maxHeight: CGFloat? = nil,
                     alignment: Alignment = .center) -> some View {
        modifier(ScaledFlexibleFrame(minWidth: minWidth, maxWidth: maxWidth, minHeight: minHeight, maxHeight: maxHeight, alignment: alignment))
    }

    /// A square a finger hits (`Theme.Size.tapTarget`): larger with the text like `scaledFrame`, never smaller than the
    /// token, which `scaledFrame` alone takes under 44 pt at Small and the system's smaller sizes. For a small control
    /// whose hit area is wider than what it draws (the delete button in an uploaded avatar's corner).
    func tapTargetFrame(alignment: Alignment = .center) -> some View {
        scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget, alignment: alignment)
            .frame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget, alignment: alignment)
    }

    /// At least as tall as a finger needs (`Theme.Size.tapTarget`), as wide as it is: a row or a heading that is a button
    /// and draws less than that (#3753). Grows with the text like `tapTargetFrame`, never below the token.
    func tapTargetHeight(alignment: Alignment = .center) -> some View {
        scaledFrame(minHeight: Theme.Size.tapTarget, alignment: alignment)
            .frame(minHeight: Theme.Size.tapTarget, alignment: alignment)
    }
}

extension View {
    /// At most `width` wide (scaled like `scaledFrame`), sized to what the view takes inside it: unlike
    /// `scaledFrame(maxWidth:)` it offers the view that width even where the parent offers none, as a horizontal
    /// `ScrollView` or `fixedSize()` does. There a flexible frame passes the open offer on, the view takes its full
    /// width, the frame clamps only its own size, and whatever is drawn inside (a pill's capsule) spills over its
    /// neighbours (#3010). Put it outside the background, so the background is drawn at the capped size.
    func scaledWidthCap(_ width: CGFloat) -> some View { modifier(ScaledWidthCap(width: width)) }
}

private struct ScaledWidthCap: ViewModifier {
    let width: CGFloat
    @Environment(\.dynamicTypeSize) private var typeSize

    func body(content: Content) -> some View { WidthCap(width: width * Theme.sizeScale(typeSize)) { content } }
}

/// The layout under `scaledWidthCap(_:)`: offers its one subview the narrower of the parent's width and the cap, and is
/// exactly as large as the subview says it is.
struct WidthCap: Layout {
    var width: CGFloat

    func offer(_ proposal: ProposedViewSize) -> ProposedViewSize {
        ProposedViewSize(width: min(proposal.width ?? width, width), height: proposal.height)
    }

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        subviews.first?.sizeThatFits(offer(proposal)) ?? .zero
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        subviews.first?.place(at: bounds.origin, proposal: ProposedViewSize(bounds.size))
    }
}

private struct ScaledFrame: ViewModifier {
    let width: CGFloat?, height: CGFloat?, alignment: Alignment
    @Environment(\.dynamicTypeSize) private var typeSize

    func body(content: Content) -> some View {
        let k = Theme.sizeScale(typeSize)
        content.frame(width: width.map { $0 * k }, height: height.map { $0 * k }, alignment: alignment)
    }
}

private struct ScaledFlexibleFrame: ViewModifier {
    let minWidth: CGFloat?, maxWidth: CGFloat?, minHeight: CGFloat?, maxHeight: CGFloat?, alignment: Alignment
    @Environment(\.dynamicTypeSize) private var typeSize

    func body(content: Content) -> some View {
        content.frame(minWidth: scaled(minWidth), maxWidth: scaled(maxWidth), minHeight: scaled(minHeight), maxHeight: scaled(maxHeight),
                      alignment: alignment)
    }

    /// .infinity stays infinite.
    private func scaled(_ v: CGFloat?) -> CGFloat? { v.map { $0.isFinite ? $0 * Theme.sizeScale(typeSize) : $0 } }
}
