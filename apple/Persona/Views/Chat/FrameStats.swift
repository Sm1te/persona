#if DEBUG
import Foundation
import QuartzCore
#if os(macOS)
import AppKit
#endif

/// Simulator profiling (`-showAutoScroll YES`, ChatView): measures every frame while the chat list sweeps end to end
/// and writes the hitches (frames late by more than half a frame) to Documents/frames.json for scripts to read.
@MainActor
final class FrameStats: NSObject {
    static let shared = FrameStats()

    private var link: CADisplayLink?
    private var last: CFTimeInterval = 0
    private var expected: CFTimeInterval = 1.0 / 60
    private var deltas: [CFTimeInterval] = []
    /// Main-thread time between two frames (run loop wake → after Core Animation's commit), in ms.
    private var busies: [Double] = []
    private var busy: CFTimeInterval = 0
    private var passStart: CFTimeInterval?
    private var observers: [CFRunLoopObserver] = []

    private var bodiesAtStart: [String: Int] = [:]
    private var pushAtStart = (passes: 0, ms: 0.0)

    func start() {
        MainThreadWatchdog.shared.reset()
        bodiesAtStart = PerfCount.bodies
        pushAtStart = (PerfCount.passes, PerfCount.totalMS)
        deltas = []
        busies = []
        busy = 0
        last = 0
        let wake = CFRunLoopObserverCreateWithHandler(nil, CFRunLoopActivity.afterWaiting.rawValue, true, CFIndex.min) { _, _ in
            MainActor.assumeIsolated { FrameStats.shared.passStart = CACurrentMediaTime() }
        }
        let sleep = CFRunLoopObserverCreateWithHandler(nil, CFRunLoopActivity.beforeWaiting.rawValue, true, CFIndex.max) { _, _ in
            MainActor.assumeIsolated {
                let s = FrameStats.shared
                if let t = s.passStart { s.busy += CACurrentMediaTime() - t }
                s.passStart = nil
            }
        }
        for o in [wake, sleep] { CFRunLoopAddObserver(CFRunLoopGetMain(), o, .commonModes) }
        observers = [wake, sleep].compactMap { $0 }
        #if os(iOS) || os(visionOS)
        let link = CADisplayLink(target: self, selector: #selector(tick(_:)))
        #else
        // A Mac's display link comes from the screen it draws on.
        guard let link = NSScreen.main?.displayLink(target: self, selector: #selector(tick(_:))) else { return }
        #endif
        link.add(to: .main, forMode: .common)
        self.link = link
    }

    @objc private func tick(_ link: CADisplayLink) {
        if last > 0 { deltas.append(link.timestamp - last); busies.append(busy * 1000) }
        busy = 0
        expected = max(link.targetTimestamp - link.timestamp, 1.0 / 120)
        last = link.timestamp
    }

    var running: Bool { link != nil }

    @discardableResult
    func stop(label: String) -> [String: Any] {
        link?.invalidate()
        link = nil
        for o in observers { CFRunLoopRemoveObserver(CFRunLoopGetMain(), o, .commonModes) }
        observers = []
        let busy = busies.sorted()
        func bpct(_ p: Double) -> Double { busy.isEmpty ? 0 : busy[min(busy.count - 1, Int(Double(busy.count) * p))] }
        let total = deltas.reduce(0, +)
        let late = deltas.filter { $0 > expected * 1.5 }
        let hitchTime = late.reduce(0) { $0 + ($1 - expected) }
        let sorted = deltas.sorted()
        func pct(_ p: Double) -> Double { sorted.isEmpty ? 0 : sorted[min(sorted.count - 1, Int(Double(sorted.count) * p))] * 1000 }
        // Typed: in the `Any` literal below Swift 6.2 (Xcode 26.0) took `busy.last` for `last(where:)`, a closure
        // JSONSerialization throws on.
        let busyMax: Double = busy.last ?? 0
        let result: [String: Any] = [
            "label": label,
            "frames": deltas.count,
            "seconds": total,
            "hitches": late.count,
            "hitch_ms_per_s": total > 0 ? hitchTime * 1000 / total : 0,
            "p50_ms": pct(0.5), "p95_ms": pct(0.95), "p99_ms": pct(0.99),
            "max_ms": (sorted.last ?? 0) * 1000,
            "busy_p50_ms": bpct(0.5), "busy_p95_ms": bpct(0.95), "busy_max_ms": busyMax,
            // When the long frames came (seconds from the start) and how long they were (ms).
            "long": zip(deltas.indices, deltas).filter { $0.1 > 0.05 }.map { i, d in
                [Double(deltas[..<i].reduce(0, +)), d * 1000] },
            // View bodies evaluated meanwhile (with `-perfCounts YES`).
            "bodies": PerfCount.bodies.compactMapValues { $0 }.reduce(into: [String: Int]()) { r, kv in
                let n = kv.value - (bodiesAtStart[kv.key] ?? 0)
                if n > 0 { r[kv.key] = n } },
            // Hub pushes applied meanwhile, and the main thread's time for them (until the run loop went idle).
            "push_passes": PerfCount.passes - pushAtStart.passes, "push_ms": PerfCount.totalMS - pushAtStart.ms,
            "main_thread_blocks": MainThreadWatchdog.shared.report,
            "busy_over_8ms": busy.filter { $0 > 8 }.count, "busy_over_16ms": busy.filter { $0 > 16 }.count,
        ]
        if let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first,
           let data = try? JSONSerialization.data(withJSONObject: result, options: [.prettyPrinted, .sortedKeys]) {
            try? data.write(to: dir.appending(path: "frames.json"))
            try? data.write(to: dir.appending(path: "frames-\(label).json"))
        }
        return result
    }
}

#if os(iOS) || os(visionOS)
import UIKit

/// Frames measured on a UI test's cue (`-showFrameProbe YES`, the performance UI tests, #5730): the test posts the
/// Darwin notification `start` before an interaction and `stop` after it, and each stop sends what `FrameStats`
/// measured in between to the paired hub's `POST /mock/frames` (the mock hub keeps it for the test to read).
/// XCTHitchMetric and the scroll signposts report nothing on a simulator, this does.
@MainActor enum FrameProbe {
    static let start = "com.persona.perf.frames.start"
    static let stop = "com.persona.perf.frames.stop"
    static let glideNotice = "com.persona.perf.frames.glide"

    static func listenIfAsked() {
        guard UserDefaults.standard.bool(forKey: "showFrameProbe") else { return }
        let center = CFNotificationCenterGetDarwinNotifyCenter()
        // Darwin notifications come on the run loop that registered them, the main one.
        let callback: CFNotificationCallback = { @Sendable _, _, name, _, _ in
            let n = name?.rawValue as String?
            DispatchQueue.main.async { MainActor.assumeIsolated { FrameProbe.handle(n) } }
        }
        for name in [start, stop, glideNotice] {
            CFNotificationCenterAddObserver(center, nil, callback, name as CFString, nil, .deliverImmediately)
        }
    }

    private static func handle(_ name: String?) {
        let stats = FrameStats.shared
        if name == glideNotice {
            Task { await runGlide() }
        } else if name == start {
            if stats.running { stats.stop(label: "probe") }
            stats.start()
        } else if name == stop, stats.running {
            let result = stats.stop(label: "probe")
            guard let hub = AppModel.shared.api?.config,
                  let body = try? JSONSerialization.data(withJSONObject: result) else { return }
            var r = URLRequest(url: hub.url.appending(path: "mock/frames"))
            r.httpMethod = "POST"
            r.setValue("application/json", forHTTPHeaderField: "Content-Type")
            hub.authorize(&r)
            r.httpBody = body
            Task { _ = try? await URLSession.shared.data(for: r) }
        }
    }

    /// Holds the actual UIKit scroll view for the entire run. The UI test only polls the mock; it never requests
    /// accessibility snapshots while frames are recorded. Tagged results are custom display-link cadence, not GPU FPS.
    private static func runGlide() async {
        guard let hub = AppModel.shared.api?.config else { return }
        var request = URLRequest(url: hub.url.appending(path: "mock/frames_glide_context"), timeoutInterval: 10)
        hub.authorize(&request)
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              (response as? HTTPURLResponse)?.statusCode == 200,
              var context = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              context["run_id"] is String, context["name"] is String else { return }
        context["sample_source"] = "app-list-glide"
        let stats = FrameStats.shared
        if stats.running {
            context["error"] = "another frame measurement is already running"
        } else if let list = PagePerf.mainScrollView() {
            context["viewport_height_pt"] = Double(list.bounds.height)
            context["content_height_pt"] = Double(list.contentSize.height)
            list.setContentOffset(CGPoint(x: list.contentOffset.x, y: -list.adjustedContentInset.top), animated: false)
            try? await Task.sleep(for: .milliseconds(500))
            let startY = list.contentOffset.y
            stats.start()
            await ListGlide.run(list, seconds: 3, speed: 2000)
            let moved = abs(list.contentOffset.y - startY)
            await ListGlide.run(list, seconds: 3, speed: -2000)
            let frames = stats.stop(label: "probe-glide")
            context.merge(frames) { _, measured in measured }
            context["scroll_offset_delta_pt"] = Double(moved)
        } else {
            context["error"] = "no visible, vertically scrollable main view"
        }
        guard let body = try? JSONSerialization.data(withJSONObject: context) else { return }
        var upload = URLRequest(url: hub.url.appending(path: "mock/frames"), timeoutInterval: 10)
        upload.httpMethod = "POST"
        upload.setValue("application/json", forHTTPHeaderField: "Content-Type")
        hub.authorize(&upload)
        upload.httpBody = body
        _ = try? await URLSession.shared.data(for: upload)
    }
}

/// Any page, measured (`-showPagePerf YES`, docs/operations/testing.md「Whole App」): 10 s after launch, 8 s on the page as it is
/// (a script pushes hub updates meanwhile), then the page's main scroll view (the largest one on screen) glides
/// down for 3 s and back up for 3 s at 2000 pt/s. Writes Documents/frames-page-open.json (the first 10 s),
/// frames-page-idle.json and frames-page-glide.json.
/// `-showPagePerfSpeed <pt/s>` and `-showPagePerfSeconds <s>` change the glide (a slow read through a long reply, a fling);
/// `-showPagePerfRepeat <n>` glides n times, each its own file after the first (frames-page-glide-2.json, …): the first
/// pass lays rows out for the first time, the later ones show what scrolling back over them costs.
@MainActor enum PagePerf {
    /// Pages told a finger is on their scroll view (true) and lifted (false), so they treat a glide as the user's
    /// scrolling (a chat stops following its newest end), by the page (`ChatView` registers its own). Each answers
    /// only for its own list: several chats can be mounted at once (Persona's conversation stays in the shell).
    static var touchers: [ObjectIdentifier: (UIScrollView, Bool) -> Void] = [:]

    private static func touch(_ list: UIScrollView, _ down: Bool) {
        for toucher in touchers.values { toucher(list, down) }
    }

    static func runIfAsked() {
        let d = UserDefaults.standard
        guard d.bool(forKey: "showPagePerf") else { return }
        let speed = d.double(forKey: "showPagePerfSpeed") > 0 ? d.double(forKey: "showPagePerfSpeed") : 2000
        let seconds = d.double(forKey: "showPagePerfSeconds") > 0 ? d.double(forKey: "showPagePerfSeconds") : 3
        let repeats = max(1, d.integer(forKey: "showPagePerfRepeat"))
        // The first 10 s too (frames-page-open.json): launch, the page opening and its first layout.
        FrameStats.shared.start()
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(10))
            FrameStats.shared.stop(label: "page-open")
            FrameStats.shared.start()
            // For the script: push now (Documents/page-idle-started).
            if let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first {
                try? Data().write(to: dir.appending(path: "page-idle-started"))
            }
            try? await Task.sleep(for: .seconds(8))
            FrameStats.shared.stop(label: "page-idle")
            try? await Task.sleep(for: .seconds(1))
            guard let list = mainScrollView() else { return }
            for n in 1...repeats {
                touch(list, true)
                list.setContentOffset(CGPoint(x: list.contentOffset.x, y: -list.adjustedContentInset.top), animated: false)
                try? await Task.sleep(for: .seconds(1))
                FrameStats.shared.start()
                await ListGlide.run(list, seconds: seconds, speed: CGFloat(speed))
                await ListGlide.run(list, seconds: seconds, speed: CGFloat(-speed))
                FrameStats.shared.stop(label: n == 1 ? "page-glide" : "page-glide-\(n)")
                touch(list, false)
            }
        }
    }

    /// `-showListOffset <pt>`: 8 s after launch the page's main scroll view sits that far from its start, and
    /// Documents/list-offset-set says so: screenshots of the same place in two builds or two switches.
    static func offsetIfAsked() {
        let y = UserDefaults.standard.double(forKey: "showListOffset")
        guard y > 0 else { return }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(8))
            guard let list = mainScrollView() else { return }
            touch(list, true)
            list.setContentOffset(CGPoint(x: list.contentOffset.x, y: -list.adjustedContentInset.top + y), animated: false)
            try? await Task.sleep(for: .seconds(0.5))
            touch(list, false)
            try? await Task.sleep(for: .seconds(2))
            if let dir = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask).first {
                try? Data().write(to: dir.appending(path: "list-offset-set"))
            }
        }
    }

    /// The tallest-content scroll view that is on screen and taller than half the window.
    static func mainScrollView() -> UIScrollView? {
        guard let window = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
            .flatMap(\.windows).first(where: \.isKeyWindow) else { return nil }
        var best: UIScrollView?
        func visit(_ v: UIView) {
            if v.isHidden || v.alpha < 0.01 { return }
            if let s = v as? UIScrollView, s.isScrollEnabled, s.bounds.height > window.bounds.height / 2,
               s.contentSize.height > s.bounds.height,
               s.convert(s.bounds, to: window).intersects(window.bounds),
               s.contentSize.height > (best?.contentSize.height ?? 0) {
                best = s
            }
            for sub in v.subviews { visit(sub) }
        }
        visit(window)
        return best
    }
}

/// Moves a scroll view at a steady speed, one step per display frame (`-showAutoScroll`'s glide).
@MainActor final class ListGlide: NSObject {
    private var done: CheckedContinuation<Void, Never>?
    private var list: UIScrollView?
    private var speed: CGFloat = 0
    private var until: CFTimeInterval = 0
    private var last: CFTimeInterval = 0
    private var link: CADisplayLink?

    static func run(_ list: UIScrollView, seconds: Double, speed: CGFloat) async {
        let g = ListGlide()
        await withCheckedContinuation { c in
            g.done = c
            g.list = list
            g.speed = speed
            g.until = CACurrentMediaTime() + seconds
            let l = CADisplayLink(target: g, selector: #selector(tick(_:)))
            l.add(to: .main, forMode: .common)
            g.link = l
        }
    }

    @objc private func tick(_ l: CADisplayLink) {
        guard let list else { return }
        if last > 0 {
            let maxY = max(0, list.contentSize.height - list.bounds.height + list.adjustedContentInset.bottom)
            let y = min(maxY, max(-list.adjustedContentInset.top, list.contentOffset.y + speed * (l.timestamp - last)))
            list.contentOffset.y = y
        }
        last = l.timestamp
        if l.timestamp >= until {
            l.invalidate()
            done?.resume()
            done = nil
        }
    }
}
#endif
#endif
