#if DEBUG
import QuartzCore
import SwiftUI
#if os(macOS)
import AppKit
#endif

/// `-unfoldTrace YES`: every row opened or folded in the chat (`toggleInPlace`, a work line's `toggleWork`) is
/// measured for `window` from the tap (#1123): the frames drawn, the late ones (over 1.5 frames), the longest, and how far
/// the tapped row moved on screen, frame by frame: as drawn where the app can tell (`DrawnProbe`: work line headers),
/// else by its own report (`ChatPin`: a row's own geometry is current, a wrapper's can lag, docs/lessons/ios.md), with
/// `-perfCounts YES` the view bodies drawn meanwhile. The results go out
/// as `probe.unfold` (`-probes YES`) for the UI tests (`UnfoldTests`) and to the console as `UNFOLD …` lines.
@MainActor @Observable
final class UnfoldTrace: NSObject {
    static let enabled = UserDefaults.standard.bool(forKey: "unfoldTrace")
    static let shared = UnfoldTrace()
    /// How long after the tap: what a tap opens may come from the hub a second later (a folded work line's steps), and
    /// the row went from under the finger only then (#3757).
    static let window: CFTimeInterval = 3

    struct Result: Codable, Equatable {
        var what: String
        /// When it was tapped (Unix time, s: the simulator shares the Mac's clock, for finding it in a recording).
        var at: Double = 0
        var frames: Int
        var hitches: Int
        var worstMS: Int
        /// The tapped row's largest distance from where it was at the tap, and where it ended (points, + is down).
        var moved: Int
        var end: Int
        /// Its largest move from one frame to the next: what reads as a jump.
        var step: Int = 0
        /// The row's screen y, frame by frame, from the tap (nil: it did not report, off screen or not pinned).
        var ys: [Int?]
        /// The list's content height, frame by frame, from the tap: the frame the opened or closed part went in or out (#3005).
        var heights: [Int?] = []
        /// View bodies evaluated meanwhile, the most first (`-perfCounts YES`).
        var bodies: [String] = []
        /// Where the row was at the tap (window y).
        var from: Int?
        /// Its top as drawn on screen, frame by frame (`DrawnProbe`: work line headers), beside what SwiftUI reports
        /// (`ys`). `moved`, `end`, `step` and `from` go by it where there is one: once the pin had moved the list from
        /// inside a geometry callback, SwiftUI went on reporting the header 40 pt off for seconds while the screen showed
        /// it still (#3005).
        var drawn: [Int?] = []
    }

    /// The last ten, newest last.
    private(set) var results: [Result] = []

    @ObservationIgnored private var what = ""
    @ObservationIgnored private var y: () -> CGFloat? = { nil }
    @ObservationIgnored private var height: () -> CGFloat? = { nil }
    @ObservationIgnored private var heights: [CGFloat?] = []
    @ObservationIgnored private var y0: CGFloat?
    @ObservationIgnored private var start: CFTimeInterval = 0
    @ObservationIgnored private var tapped = Date.now
    @ObservationIgnored private var last: CFTimeInterval = 0
    @ObservationIgnored private var expected: CFTimeInterval = 1.0 / 60
    @ObservationIgnored private var deltas: [CFTimeInterval] = []
    @ObservationIgnored private var ys: [CGFloat?] = []
    @ObservationIgnored private var link: CADisplayLink?
    @ObservationIgnored private var bodiesAtStart: [String: Int] = [:]
    @ObservationIgnored private var idled = false
    @ObservationIgnored private var drawnTop: () -> CGFloat? = { nil }
    @ObservationIgnored private var drawn: [CGFloat?] = []

    /// Once, `seconds` from now: the same measurement with nothing tapped, what the chat costs at rest.
    func idle(after seconds: Double) {
        guard Self.enabled, !idled else { return }
        idled = true
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(seconds))
            begin("idle") { nil }
        }
    }

    /// A row was tapped to open or fold: `what` names it ("work open", "thought fold"), `y` says where it is now,
    /// `height` how tall the list's content is.
    func begin(_ what: String, height: @escaping () -> CGFloat? = { nil }, drawn: @escaping () -> CGFloat? = { nil },
               y: @escaping () -> CGFloat?) {
        guard Self.enabled else { return }
        finish()
        self.what = what
        self.y = y
        drawnTop = drawn
        self.drawn = []
        self.height = height
        y0 = drawn() ?? y()
        deltas = []
        ys = []
        heights = [height()]
        last = 0
        start = CACurrentMediaTime()
        tapped = .now
        bodiesAtStart = PerfCount.bodies
        #if os(iOS) || os(visionOS)
        let link = CADisplayLink(target: self, selector: #selector(tick(_:)))
        #else
        guard let link = NSScreen.main?.displayLink(target: self, selector: #selector(tick(_:))) else { return }
        #endif
        link.add(to: .main, forMode: .common)
        self.link = link
    }

    @objc private func tick(_ link: CADisplayLink) {
        if last > 0 { deltas.append(link.timestamp - last) }
        expected = max(link.targetTimestamp - link.timestamp, 1.0 / 120)
        last = link.timestamp
        ys.append(y())
        heights.append(height())
        drawn.append(drawnTop())
        if CACurrentMediaTime() - start > Self.window { finish() }
    }

    private func finish() {
        guard let link else { return }
        link.invalidate()
        self.link = nil
        let path = drawn.contains { $0 != nil } ? drawn : ys
        let off = path.map { v in v.flatMap { v in y0.map { v - $0 } } }
        let moved = off.compactMap { $0 }.map(abs).max() ?? 0
        let known = path.compactMap { $0 }
        let step = zip(known, known.dropFirst()).map { abs($1 - $0) }.max() ?? 0
        let r = Result(what: what, at: tapped.timeIntervalSince1970, frames: deltas.count, hitches: deltas.filter { $0 > expected * 1.5 }.count,
                       worstMS: Int(((deltas.max() ?? 0) * 1000).rounded()), moved: Int(moved.rounded()),
                       end: Int((off.last.flatMap { $0 } ?? 0).rounded()), step: Int(step.rounded()), ys: ys.map { $0.map { Int($0.rounded()) } },
                       heights: heights.map { $0.map { Int($0.rounded()) } },
                       bodies: PerfCount.bodies.map { ($0.key, $0.value - (bodiesAtStart[$0.key] ?? 0)) }.filter { $0.1 > 0 }
                           .sorted { $0.1 > $1.1 }.prefix(8).map { "\($0.0) \($0.1)" },
                       from: y0.map { Int($0.rounded()) }, drawn: drawn.map { $0.map { Int($0.rounded()) } })
        print("UNFOLD \(r.what): frames \(r.frames) hitches \(r.hitches) worst \(r.worstMS) ms moved \(r.moved) end \(r.end) ys "
              + r.ys.map { $0.map(String.init) ?? "-" }.joined(separator: " "))
        results = Array((results + [r]).suffix(10))
    }
}
#endif
