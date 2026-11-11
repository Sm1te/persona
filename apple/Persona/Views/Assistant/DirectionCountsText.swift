import Foundation

/// A direction's work under its row in the sidebar (#4331): "N working · N need you", only the parts that are not
/// zero, and nothing while it holds nothing.
nonisolated enum DirectionCountsText {
    /// The line, or nil when neither part is non-zero.
    static func label(working: Int, needsYou: Int) -> String? {
        switch (working > 0, needsYou > 0) {
        case (true, true): return String(localized: "\(working) working · \(needsYou) need you")
        case (true, false): return String(localized: "\(working) working")
        case (false, true): return String(localized: "\(needsYou) need you")
        case (false, false): return nil
        }
    }
}
