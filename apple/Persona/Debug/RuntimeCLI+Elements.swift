#if DEBUG && (os(iOS) || os(visionOS))
import SwiftUI
import UIKit

/// Reading the app (docs/operations/runtime-cli.md「Reading state」): the accessibility tree XCUITest sees (`tree`, `find`, `wait`), the
/// view-body counts (`perf`), a pull from the hub (`refresh`), a chat's history in (`wait-loaded`).
extension RuntimeCLI {
    static let elementCommands: [String: Command] = [
        "tree": { a in ["elements": elements(matching: a).map(\.json)] },
        "find": { a in find(a) },
        "wait": { a in await wait(a) },
        "perf": { _ in perf() },
        "refresh": { _ in await AppModel.shared.refresh(); return [:] },
        "wait-loaded": { a in await waitLoaded(a) },
    ]

    /// What XCUITest turns on: with no assistive technology running SwiftUI builds no accessibility elements. Called
    /// once when the CLI starts (`startIfAsked`), never otherwise.
    static func enableAccessibilityTree() {
        guard let h = dlopen("/usr/lib/libAccessibility.dylib", RTLD_NOW),
              let f = dlsym(h, "_AXSSetAutomationEnabled") else { return }
        typealias Enable = @convention(c) (Int32) -> Void
        unsafeBitCast(f, to: Enable.self)(1)
    }

    struct Element {
        let object: NSObject
        let id: String
        let label: String
        let value: String
        let frame: CGRect
        let traits: UIAccessibilityTraits
        /// Not an element but a container with an id (`.accessibilityElement(children: .contain)`), listed when asked
        /// (`containers`): what holds the elements, as XCUITest sees it (the chat's `chatMark`).
        var container = false
        /// The id of the nearest container with an id around it, listed with `containers`: what holds it in the tree,
        /// not by where it is drawn (an alert over a chat bubble is not in the bubble).
        var holder: String?

        /// The frame is on screen, whether or not something covers it.
        var onScreen: Bool {
            let screen = RuntimeCLI.screenBounds
            return !frame.isEmpty && frame.minX.isFinite && screen.intersects(frame)
        }

        /// On screen, drawn, and what a touch at its middle reaches: no layer above it is hidden or transparent (the
        /// shell keeps the page behind in the tree, drawn at opacity 0), and UIKit's hit test there, in the top-most
        /// window that takes the touch, lands in its view or in a view around it (not in a menu or sheet over it).
        /// SwiftUI's accessibility hit test cannot tell: it answers with the hidden page's elements.
        var visible: Bool {
            guard onScreen, let v = RuntimeCLI.view(of: object), let w = v.window, RuntimeCLI.drawn(v) else { return false }
            let screen = RuntimeCLI.screenBounds
            let mid = CGPoint(x: frame.intersection(screen).midX, y: frame.intersection(screen).midY)
            for top in RuntimeCLI.windows.sorted(by: { $0.windowLevel > $1.windowLevel }) where !top.isHidden {
                guard let hit = top.hitTest(top.convert(mid, from: nil), with: nil) else { continue }
                return hit.isDescendant(of: v) || v.isDescendant(of: hit)
            }
            return false
        }

        var traitNames: [String] {
            var t: [String] = []
            if traits.contains(.button) { t.append("button") }
            if traits.contains(.selected) { t.append("selected") }
            if traits.contains(.header) { t.append("header") }
            if traits.contains(.staticText) { t.append("text") }
            if traits.contains(.notEnabled) { t.append("disabled") }
            if traits.contains(.searchField) { t.append("search") }
            if traits.contains(.image) { t.append("image") }
            return t
        }

        var json: Reply {
            let t = traitNames
            // A frame can be NaN or infinite (an element mid-layout): 0, never a value JSON cannot hold.
            let f = [frame.minX, frame.minY, frame.width, frame.height].map { $0.isFinite ? (Double($0) * 10).rounded() / 10 : 0 }
            var out: Reply = ["id": id, "label": label, "value": value, "traits": t, "visible": visible, "onScreen": onScreen, "frame": f]
            if container { out["container"] = true }
            if let holder { out["in"] = holder }
            // Its custom actions (VoiceOver's Actions rotor), by name: what `activate` `action:` performs.
            if let actions = object.accessibilityCustomActions, !actions.isEmpty { out["actions"] = actions.map(\.name) }
            return out
        }
    }

    /// No layer above `v` is hidden or transparent: the shell keeps the page behind in the window at opacity 0.
    static func drawn(_ v: UIView) -> Bool {
        var l: CALayer? = v.layer
        while let cur = l {
            if cur.isHidden || cur.opacity < 0.01 { return false }
            l = cur.superlayer
        }
        return true
    }

    /// The view an accessibility element is drawn in: itself, or the first view up its containers.
    static func view(of o: NSObject) -> UIView? {
        let container = Selector(("accessibilityContainer"))
        var cur: NSObject? = o
        for _ in 0..<40 {
            guard let c = cur else { return nil }
            if let v = c as? UIView { return v }
            cur = c.responds(to: container) ? c.perform(container)?.takeUnretainedValue() as? NSObject : nil
        }
        return nil
    }

    /// Every accessibility element of the app's windows, top-most window first (menus, sheets), in reading order.
    static func allElements(containers: Bool = false) -> [Element] {
        var out: [Element] = []
        var seen = Set<ObjectIdentifier>()
        func visit(_ o: NSObject, depth: Int, holder: String?) {
            guard depth < 80, seen.insert(ObjectIdentifier(o)).inserted else { return }
            if let v = o as? UIView, v.isHidden || v.alpha < 0.01 { return }
            let id = (o as? UIAccessibilityIdentification)?.accessibilityIdentifier
                ?? (o.responds(to: Selector(("accessibilityIdentifier"))) ? o.value(forKey: "accessibilityIdentifier") as? String : nil)
            var inner = holder
            if o.isAccessibilityElement {
                out.append(Element(object: o, id: id ?? "", label: o.accessibilityLabel ?? "", value: o.accessibilityValue ?? "",
                                   frame: o.accessibilityFrame, traits: o.accessibilityTraits, holder: containers ? holder : nil))
            } else if containers, let id, !id.isEmpty {
                out.append(Element(object: o, id: id, label: o.accessibilityLabel ?? "", value: o.accessibilityValue ?? "",
                                   frame: o.accessibilityFrame, traits: o.accessibilityTraits, container: true, holder: holder))
                inner = id
            }
            if let children = o.accessibilityElements as? [NSObject] {
                for c in children { visit(c, depth: depth + 1, holder: inner) }
            } else {
                let n = o.accessibilityElementCount()
                if n > 0, n != NSNotFound {
                    for i in 0..<n { if let c = o.accessibilityElement(at: i) as? NSObject { visit(c, depth: depth + 1, holder: inner) } }
                }
            }
            if let v = o as? UIView { for s in v.subviews { visit(s, depth: depth + 1, holder: inner) } }
        }
        for w in windows.reversed() where !w.isHidden { visit(w, depth: 0, holder: nil) }
        return out
    }

    /// Filters: `id` (exact), `idPrefix`, `label` (exact), `labelContains`, `labelPrefix`, `trait` ("button"…),
    /// `visibleOnly` (a touch reaches it), `onScreenOnly` (its frame is on screen), `containers` (also the containers
    /// that have an id).
    static func elements(matching a: Args) -> [Element] {
        allElements(containers: a["containers"] as? Bool == true).filter { e in
            if let id = a["id"] as? String, e.id != id { return false }
            if let p = a["idPrefix"] as? String, !e.id.hasPrefix(p) { return false }
            if let l = a["label"] as? String, e.label != l { return false }
            if let l = a["labelContains"] as? String, !e.label.contains(l) { return false }
            if let l = a["labelPrefix"] as? String, !e.label.hasPrefix(l) { return false }
            if let t = a["trait"] as? String, !e.traitNames.contains(t) { return false }
            if a["onScreenOnly"] as? Bool == true, !e.onScreen { return false }
            if a["visibleOnly"] as? Bool == true, !e.visible { return false }
            return true
        }
    }

    /// The `index`-th match, or without `index` the first one a touch reaches (then the first on screen, then the
    /// first at all): what `find` and `wait` answer.
    static func pick(_ a: Args) -> Element? {
        let all = elements(matching: a)
        if let i = a["index"] as? Int { return i >= 0 && i < all.count ? all[i] : nil }
        return all.first(where: \.visible) ?? all.first(where: \.onScreen) ?? all.first
    }

    /// What `activate` and `type` act on: the first match a touch reaches, else why not. `pick`'s fallbacks acted on
    /// the shell's page behind (in the tree at opacity 0) and answered success; `allowHidden` or an `index` keeps them.
    static func target(_ a: Args) -> Result<Element, Failure> {
        if a["index"] != nil || a["allowHidden"] as? Bool == true {
            return pick(a).map { .success($0) } ?? .failure(Failure("not found"))
        }
        let all = elements(matching: a)
        if let e = all.first(where: \.visible) { return .success(e) }
        return .failure(Failure(all.isEmpty ? "not found"
            : "not visible: \(all.count) match, none a touch reaches (allowHidden: true acts on it anyway)"))
    }

    struct Failure: Error {
        let why: String
        init(_ why: String) { self.why = why }
    }

    private static func find(_ a: Args) -> Reply {
        ["count": elements(matching: a).count, "element": pick(a)?.json ?? NSNull()]
    }

    /// Polls every 100 ms until an element matches (with `gone`, until none does), or `timeout` seconds (10) pass.
    /// `stable`: until its frame has also stayed the same for 300 ms. After a launch the list's top comes into place
    /// over a moment, and a test that measured the first match failed (`quick filters are not on screen`).
    private static func wait(_ a: Args) async -> Reply {
        let deadline = Date.now.addingTimeInterval(a["timeout"] as? Double ?? 10)
        let gone = a["gone"] as? Bool ?? false
        let stable = !gone && a["stable"] as? Bool == true
        var frame: CGRect?, since = Date.now
        while true {
            let e = pick(a)
            if gone ? e == nil : e != nil {
                guard stable, let e else { return ["element": e?.json ?? NSNull()] }
                if e.frame != frame { frame = e.frame; since = Date.now }
                else if Date.now.timeIntervalSince(since) >= 0.3 { return ["element": e.json] }
            } else {
                frame = nil
            }
            if Date.now > deadline {
                if gone { return fail("still there") }
                return fail(frame.map { "not stable: still moving at \($0)" } ?? "not found")
            }
            try? await Task.sleep(for: .milliseconds(100))
        }
    }

    /// Waits until the chat on screen (`session` <machine>/<id>, else the open Agents chat or the assistant's) has
    /// had its history from the hub once (`ChatStore.loadedOnce`) or an error, and its lines are built (`version`
    /// unchanged for 300 ms). What a cold launch's fixed sleep waited for: a chat laid out from the disk cache, before
    /// the hub's history came in, looks settled to `settle`.
    private static func waitLoaded(_ a: Args) async -> Reply {
        let m = AppModel.shared
        let started = Date.now, deadline = started.addingTimeInterval(a["timeout"] as? Double ?? 15)
        // The shell keeps the assistant's page (and its chat) shown behind Agents: on Agents, only a chat pushed there.
        let pushed = m.openChat ?? (m.visibleSession != m.assistantCurrent ? m.visibleSession : nil)
        guard let k = key(a["session"] as? String) ?? (m.selectedTab == .chats ? pushed : m.assistantCurrent) else {
            return fail("no chat on screen")
        }
        var store: ChatStore? { m.chats[k] }
        while !(store?.loadedOnce ?? false) && store?.error == nil {
            if Date.now > deadline { return fail("\(name(k)) not loaded: \(store.map { "\($0.items.count) items" } ?? "no chat")") }
            try? await Task.sleep(for: .milliseconds(50))
        }
        var version = store?.version ?? 0, quiet = Date.now
        while Date.now.timeIntervalSince(quiet) < 0.3, Date.now < deadline {
            try? await Task.sleep(for: .milliseconds(50))
            if let v = store?.version, v != version { version = v; quiet = Date.now }
        }
        return ["session": name(k), "items": store?.items.count ?? 0, "rows": store?.rows.count ?? 0,
                "error": store?.error ?? NSNull(), "waited": Int(Date.now.timeIntervalSince(started) * 1000)]
    }

    /// View bodies evaluated, events, main-thread time (PerfCount; the app launched with `-perfCounts YES`).
    private static func perf() -> Reply {
        guard PerfCount.enabled else { return fail("launch with -perfCounts YES") }
        return ["counts": (try? JSONSerialization.jsonObject(with: Data(PerfCount.summary.utf8))) ?? PerfCount.summary]
    }
}
#endif
