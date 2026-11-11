import CoreGraphics
import Foundation

/// Part of the framebuffer, as a few rectangles in pixels (RFBClient's damage, RFBFrameSink's stale parts): rectangles
/// are added as they come, merged when they overlap or touch, and once there are more than `most` they fold into
/// their bounding box. Copying a region copies what it covers, maybe a little more, never less.
nonisolated struct RFBRegion: Equatable, Sendable {
    /// More than this many rectangles fold into one: copying one big rectangle beats walking many small ones.
    static let most = 24

    private(set) var rects: [CGRect] = []

    var isEmpty: Bool { rects.isEmpty }
    var bounds: CGRect { rects.reduce(CGRect.null) { $0.union($1) } }
    /// The pixels the rectangles cover (they do not overlap).
    var area: Int { rects.reduce(0) { $0 + Int($1.width) * Int($1.height) } }

    init() {}

    init(_ r: CGRect) { add(r) }

    /// Covers r too; empty rectangles are left out.
    mutating func add(_ r: CGRect) {
        var r = r.integral
        guard !r.isEmpty, !r.isNull else { return }
        // Whatever it overlaps or touches joins it, and what the bigger one then reaches, until nothing does.
        var i = 0
        while i < rects.count {
            let o = rects[i]
            if o.contains(r) { return }
            if o.insetBy(dx: -1, dy: -1).intersects(r) {
                r = r.union(o)
                rects.remove(at: i)
                i = 0
            } else {
                i += 1
            }
        }
        rects.append(r)
        if rects.count > Self.most { rects = [bounds] }
    }

    mutating func add(_ other: RFBRegion) { for r in other.rects { add(r) } }

    /// Kept to what lies within r.
    func clipped(to r: CGRect) -> RFBRegion {
        var out = RFBRegion()
        for x in rects { out.add(x.intersection(r)) }
        return out
    }
}
