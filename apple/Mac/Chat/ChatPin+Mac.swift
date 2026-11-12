import AppKit
import SwiftUI

/// The Mac's `ChatPin` (iOS: `Views/Chat/ChatPin.swift`): keeps a clicked header where it was while what it opens
/// settles. Same calls as on iOS; the chat list is an `NSScrollView` here, moved through its clip view.
@MainActor final class ChatPin {
    weak var list: NSScrollView? {
        didSet {
            guard list !== oldValue, let clip = list?.contentView else { return }
            if let watch { NotificationCenter.default.removeObserver(watch) }
            clip.postsBoundsChangedNotifications = true
            watch = NotificationCenter.default.addObserver(forName: NSView.boundsDidChangeNotification, object: clip, queue: nil) { @Sendable [weak self] _ in
                MainActor.assumeIsolated { self?.boundsChanged() }
            }
        }
    }
    private var watch: (any NSObjectProtocol)?
    var letGo: () -> Void = {}
    /// Set by the chat: a held header began, or ended, rising to show what opened under it (`rising`). Meanwhile the
    /// newest end is off the screen for a moment, and ↓ came up for that moment only (#1123).
    var onRising: (Bool) -> Void = { _ in }
    /// Set by the chat: a row is opening or closing under the click (`toggleInPlace`). True when the chat holds the list
    /// there instead of following what comes in next (`ChatFollow.toggledRow`, as on iOS, #3005).
    var anchor: () -> Bool = { false }
    /// Set by the chat: a kept hold glided to the newest end (iOS, #1173). The Mac pin does not glide; never called.
    var landed: () -> Void = {}

    nonisolated static let space = "chatContent"
    /// How long a tapped header is held at most.
    nonisolated static let holdTime: Double = 1.5

    nonisolated struct Spot: Equatable, Sendable { var y: CGFloat; var content: CGFloat }

    private struct Held {
        var key: String; var at: Spot; var base: CGFloat; var until: Date; var slow: Bool; var rise: CGFloat
        /// How far it has risen so far, and when that was worked out; whether it has glided (as on iOS).
        var risen: CGFloat = 0
        var risenAt: CFTimeInterval = 0
        var rising = false
        var glided = false
    }
    private var held: Held?
    private var last: [String: Spot] = [:]
    /// Where the held header last said it was in the content.
    private var reach: CGFloat = 0

    /// `rise`: how far up the header may go to show what opens under it (as on iOS).
    /// `keep`: held until `release()` (as on iOS).
    func hold(_ key: String, from: String? = nil, slow: Bool = false, rise: CGFloat = 0, keep: Bool = false) {
        guard let list, let at = last[from ?? key] else { return }
        // A row under the kept header: the kept hold has it in place already, from now on where it is (as on iOS).
        if !keep, let k = held, k.until == .distantFuture, let kept = last[k.key], at.content < kept.content {
            held = Held(key: k.key, at: kept, base: Self.offset(list), until: k.until, slow: true, rise: 0)
            return
        }
        held = Held(key: key, at: at, base: Self.offset(list), until: keep ? .distantFuture : .now.addingTimeInterval(Self.holdTime),
                    slow: slow || keep, rise: max(rise, 0))
        letGo()
    }

    var holding: Bool { held.map { Date.now <= $0.until } ?? false }

    func release() {
        held = nil
        gliding = false
    }

    /// The list's height changed under a held header (iOS puts the header back, #3005). Not followed up in the frozen
    /// Mac app (docs/apple/mac.md): there the header may move with the list's bottom edge.
    func resized(by d: CGFloat) {}

    /// The kept hold lasts the usual time from now (as on iOS).
    func unkeep() {
        if var h = held, h.until == .distantFuture {
            h.until = .now.addingTimeInterval(Self.holdTime)
            held = h
        }
    }

    #if DEBUG
    /// Where `key` last reported itself on screen (`UnfoldTrace`).
    func screenY(_ key: String) -> CGFloat? { last[key]?.y }
    /// How tall the list's content is (`UnfoldTrace`).
    var contentHeight: CGFloat? { list?.documentView?.frame.height }
    /// What is held, for `ScrollTrace`: its key, and whether it is kept.
    var traceHeld: String { held.map { "\($0.key)\($0.until == .distantFuture ? "+kept" : "")" } ?? "-" }
    /// The iPhone's trace says where its scroll view is; the Mac's says nothing more.
    var traceFrame: String { "-" }
    #endif

    func moved(_ key: String, to spot: Spot) {
        last[key] = spot
        guard let h = held, h.key == key, let list else { return }
        if Date.now > h.until { held = nil; return }
        if !h.slow, abs(spot.y - h.at.y) < 0.5 { held = nil; return }
        let grown = spot.content - h.at.content
        reach = spot.content
        rising(grown)
        guard var h = held else { return }
        let still = h.base + grown, target = Self.within(list, wanted(h, grown: grown))
        // Folded at the newest end: it stays for this frame and the list glides to where it can be (as on iOS).
        if !h.glided, target - still >= 0.5, !Theme.Motion.reduced {
            h.glided = true
            held = h
            return glide(list, from: still, to: target)
        }
        if gliding { return }
        put(list, target)
    }

    /// The list is gliding to where the held header goes (`glide`): nothing else puts it meanwhile. Where it is
    /// meant to be this frame.
    private var gliding = false
    private var glidePoint: CGFloat = 0

    /// From `from` to `to` in `glideTime`, easing out, a step every frame. The clip view takes an offset past the
    /// newest end, but the layout of the shorter list then put it back at the newest end: for a frame the header showed
    /// where it lands, then jumped up to glide down (the animator's scroll ended there too). So whatever moves the list
    /// meanwhile, it is put back where the glide is, in the same pass (`boundsChanged`), before that frame is drawn.
    /// (A larger content margin there instead: SwiftUI moved the list by it when it arrived.)
    private func glide(_ list: NSScrollView, from: CGFloat, to: CGFloat) {
        gliding = true
        glidePoint = from
        put(list, from)
        var at = CACurrentMediaTime(), gone: Double = 0
        Task { @MainActor [weak self] in
            while true {
                try? await Task.sleep(for: .milliseconds(8))
                guard let self, self.gliding, let list = self.list else { return }
                // At most a frame's worth at a time (as the rise): the fold's own long frame took it most of the way.
                let now = CACurrentMediaTime()
                gone += min(now - at, 1.0 / 60)
                at = now
                let t = min(gone / Self.glideTime, 1)
                self.glidePoint = from + (to - from) * (1 - (1 - t) * (1 - t))
                self.put(list, self.glidePoint)
                if t >= 1 { self.gliding = false; return }
            }
        }
    }

    private func boundsChanged() {
        guard gliding, let list, abs(Self.offset(list) - glidePoint) >= 0.5 else { return }
        put(list, glidePoint)
    }

    /// As long as iOS's animated scroll.
    nonisolated static let glideTime: Double = 0.35

    /// Where the list keeps the held header: where it was, but for how far it has risen (as on iOS).
    private func wanted(_ h: Held, grown: CGFloat) -> CGFloat { grown > 0 ? h.base + grown - h.risen : h.base + grown }

    /// The rise reached a part at a time, each frame closing the gap (as on iOS's `rising`).
    private func rising(_ grown: CGFloat) {
        guard var h = held else { return }
        let full = min(max(grown, 0), h.rise)
        if Theme.Motion.reduced || full <= h.risen + 0.5 {
            if h.risen > full || Theme.Motion.reduced { h.risen = full; held = h }
            return
        }
        guard !h.rising else { return }
        h.rising = true
        h.risenAt = CACurrentMediaTime()
        held = h
        onRising(true)
        Task { @MainActor [weak self] in
            defer { self?.onRising(false) }
            while true {
                try? await Task.sleep(for: .milliseconds(8))
                guard let self, var h = self.held, let list = self.list else { return }
                let grown = self.reach - h.at.content
                let full = min(max(grown, 0), h.rise), now = CACurrentMediaTime()
                // At most a frame's worth at a time: after a long frame (the steps laid out) it went most of the way at once.
                h.risen += (full - h.risen) * (1 - exp(-min(now - h.risenAt, 1.0 / 60) / Self.riseTau))
                h.risenAt = now
                if full - h.risen < 0.5 { h.risen = full; h.rising = false }
                self.held = h
                self.put(list, Self.within(list, self.wanted(h, grown: grown)))
                if !h.rising { return }
            }
        }
    }

    /// The rise's time constant (as on iOS).
    nonisolated static let riseTau: Double = 0.1

    private func put(_ list: NSScrollView, _ y: CGFloat) {
        guard abs(y - Self.offset(list)) >= 0.5 else { return }
        list.contentView.scroll(to: NSPoint(x: list.contentView.bounds.origin.x, y: y))
        list.reflectScrolledClipView(list.contentView)
    }

    /// iOS brings what opened at the newest end above the composer; a Mac window is tall enough that it stays in view.
    func show(_ key: String, bottom: CGFloat) {}

    private static func offset(_ list: NSScrollView) -> CGFloat { list.contentView.bounds.origin.y }

    private static func within(_ list: NSScrollView, _ y: CGFloat) -> CGFloat {
        let inset = list.contentInsets
        let height = list.documentView?.frame.height ?? 0
        return min(max(y, -inset.top), max(-inset.top, height + inset.bottom - list.contentView.bounds.height))
    }

    /// Sits in the list's content and hands the scroll view around it to the pin.
    struct Finder: NSViewRepresentable {
        let pin: ChatPin

        func makeNSView(context: Context) -> Probe {
            let v = Probe()
            v.pin = pin
            return v
        }

        func updateNSView(_ v: Probe, context: Context) { v.pin = pin }

        final class Probe: NSView {
            weak var pin: ChatPin?

            override func hitTest(_ point: NSPoint) -> NSView? { nil }

            override func viewDidMoveToWindow() {
                super.viewDidMoveToWindow()
                if let list = enclosingScrollView { pin?.list = list }
            }
        }
    }
}
