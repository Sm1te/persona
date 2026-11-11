#if DEBUG && (os(iOS) || os(visionOS))
import SwiftUI
import UIKit

/// Acting on the app (docs/operations/runtime-cli.md「Actions and screenshots」): `activate`, `alert`, `scroll`, `type`, `dismiss-keyboard`,
/// `snapshot`, `pasteboard`, `share`.
/// A real touch (a menu's items, which take no accessibility activation) is `personactl.py tap`'s fallback: `idb`.
extension RuntimeCLI {
    static let actionCommands: [String: Command] = [
        "activate": { a in activate(a) },
        "alert": { a in await alert(a) },
        "scroll": { a in scroll(a) },
        "type": { a in type(a) },
        "dismiss-keyboard": { _ in windows.forEach { $0.endEditing(true) }; return [:] },
        "snapshot": { a in snapshot(a) },
        "pasteboard": { a in pasteboard(a) },
        "share": { a in share(a) },
    ]

    /// Puts a picture (`image`: a solid `width` × `height` pt, default 40 × 30) or `text` on the clipboard, as a copy in
    /// another app would: what the composer's Paste chip (`PasteImageChip`) waits for, without a UI test to set it.
    private static func pasteboard(_ a: Args) -> Reply {
        if let text = a["text"] as? String {
            UIPasteboard.general.string = text
        } else {
            let size = CGSize(width: a["width"] as? Double ?? 40, height: a["height"] as? Double ?? 30)
            UIPasteboard.general.image = UIGraphicsImageRenderer(size: size).image { ctx in
                UIColor.systemTeal.setFill()
                ctx.fill(CGRect(origin: .zero, size: size))
            }
        }
        return ["changeCount": UIPasteboard.general.changeCount, "hasImages": UIPasteboard.general.hasImages]
    }

    /// The system's share sheet for `text` (and `url`, `images` solid pictures and `files` small PDFs, when given), from
    /// the key window: where Share › Persona (`PersonaShare`) is listed, without a share button to find on the page.
    private static func share(_ a: Args) -> Reply {
        var items: [Any] = [a["text"] as? String ?? "Persona"]
        if let u = (a["url"] as? String).flatMap(URL.init(string:)) { items.append(u) }
        let colors: [UIColor] = [.systemTeal, .systemOrange, .systemIndigo, .systemPink]
        for i in 0..<min(colors.count, max(0, a["images"] as? Int ?? 0)) {
            let size = CGSize(width: 800, height: 600)
            let format = UIGraphicsImageRendererFormat.default()
            format.scale = 1
            items.append(UIGraphicsImageRenderer(size: size, format: format).image { ctx in
                colors[i].setFill()
                ctx.fill(CGRect(origin: .zero, size: size))
            })
        }
        for i in 0..<min(4, max(0, a["files"] as? Int ?? 0)) {
            let url = FileManager.default.temporaryDirectory.appending(path: i == 0 ? "Quarterly report.pdf" : "Notes \(i + 1).pdf")
            try? Data("%PDF-1.4\n%Persona share test\n%%EOF\n".utf8).write(to: url)
            items.append(url)
        }
        guard var top = windows.first(where: \.isKeyWindow)?.rootViewController ?? windows.first?.rootViewController else {
            return fail("no window")
        }
        while let next = top.presentedViewController { top = next }
        let sheet = UIActivityViewController(activityItems: items, applicationActivities: nil)
        sheet.popoverPresentationController?.sourceView = top.view
        sheet.popoverPresentationController?.sourceRect = CGRect(x: top.view.bounds.midX, y: top.view.bounds.midY, width: 1, height: 1)
        top.present(sheet, animated: false)
        return ["presented": true, "items": items.count]
    }

    /// What VoiceOver's double tap does to the element `target` finds: a button's action, a menu opening.
    /// `activated` false when the element does not take it (a menu's items). `action`: the element's custom action of
    /// that name instead (VoiceOver's Actions rotor), as a failed bubble's Resend: a menu's item without a touch.
    private static func activate(_ a: Args) -> Reply {
        switch target(a) {
        case .failure(let f): return fail(f.why)
        case .success(let e):
            guard let name = a["action"] as? String else {
                return ["activated": e.object.accessibilityActivate(), "element": e.json]
            }
            let actions = e.object.accessibilityCustomActions ?? []
            guard let action = actions.first(where: { $0.name == name }) else {
                return fail("no action \(name): \(actions.map(\.name))")
            }
            if let run = action.actionHandler { return ["activated": run(action), "element": e.json] }
            guard let t = action.target as? NSObject else { return fail("action \(name) does nothing") }
            _ = t.perform(action.selector, with: action)
            return ["activated": true, "element": e.json]
        }
    }

    /// The alert the app shows (SwiftUI's `.alert` is a `UIAlertController`): its `title` and `buttons`; with `button`,
    /// that one pressed, as a tap does (the alert goes, then its action runs). Its buttons take no accessibility
    /// activation, and a touch needs idb.
    private static func alert(_ a: Args) async -> Reply {
        var shown: UIAlertController?
        for w in windows where !w.isHidden {
            var vc = w.rootViewController
            while let v = vc, shown == nil {
                if let alert = v as? UIAlertController, !alert.isBeingDismissed { shown = alert }
                vc = v.presentedViewController
            }
        }
        guard let shown else { return fail("no alert") }
        let buttons = shown.actions.compactMap(\.title)
        guard let title = a["button"] as? String else { return ["title": shown.title ?? "", "buttons": buttons] }
        guard let action = shown.actions.first(where: { $0.title == title }) else { return fail("no button \(title): \(buttons)") }
        // The action's handler is UIKit's to keep (no public way to press it): the alert's own block, as a tap calls it.
        typealias Handler = @convention(block) (UIAlertAction) -> Void
        let handler = (action.value(forKey: "handler") as AnyObject?).map { unsafeBitCast($0, to: Handler.self) }
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            shown.dismiss(animated: false) { done.resume() }
        }
        handler?(action)
        return ["pressed": title, "buttons": buttons]
    }

    /// The largest scroll view on screen, moved by `pages` screens (negative: up; 0.8 of its height each) or `to` top /
    /// bottom, without animation (no deceleration: what is under test is where things land, not the fling). Only one
    /// that is drawn: the shell's page behind has a list as large as the one on screen (`frame`: which one moved).
    private static func scroll(_ a: Args) -> Reply {
        AgentsFeed.pagingProbe?.reader.cancel()
        var views: [UIScrollView] = []
        func collect(_ v: UIView) {
            if let s = v as? UIScrollView, !s.isHidden, s.window != nil, s.contentSize.height > s.bounds.height + 1, drawn(s) {
                views.append(s)
            }
            v.subviews.forEach(collect)
        }
        windows.forEach(collect)
        guard let s = views.max(by: { $0.bounds.width * $0.bounds.height < $1.bounds.width * $1.bounds.height }) else {
            return fail("no scroll view")
        }
        let top = -s.adjustedContentInset.top
        let bottom = max(top, s.contentSize.height + s.adjustedContentInset.bottom - s.bounds.height)
        var y = s.contentOffset.y
        switch a["to"] as? String {
        case "top": y = top
        case "bottom": y = bottom
        default: y += CGFloat(a["pages"] as? Double ?? 1) * s.bounds.height * 0.8
        }
        s.setContentOffset(CGPoint(x: s.contentOffset.x, y: min(bottom, max(top, y))), animated: false)
        let f = s.convert(s.bounds, to: nil)
        return ["offset": Double(s.contentOffset.y), "contentHeight": Double(s.contentSize.height),
                "frame": [f.minX, f.minY, f.width, f.height].map { Double($0) }]
    }

    nonisolated(unsafe) fileprivate static var responder: UIResponder?

    /// Whatever UIKit says has focus: an action sent to no target goes to the first responder, which records itself
    /// (`rtcliRecordFirstResponder`). Walking the windows' subviews missed a search bar's field in the navigation bar.
    private static func firstResponder() -> UIResponder? {
        responder = nil
        UIApplication.shared.sendAction(#selector(UIResponder.rtcliRecordFirstResponder(_:)), to: nil, from: nil, for: nil)
        defer { responder = nil }
        return responder
    }

    /// Types `text` into the field that has focus; with element filters, focuses that element first: accessibility
    /// activation, and when that leaves no field focused (a SwiftUI text field often), the text view under the element
    /// made first responder (what a tap on it does, without a touch: the hub has none on a real phone).
    private static func type(_ a: Args) -> Reply {
        if ["id", "idPrefix", "label", "labelContains", "labelPrefix", "trait"].contains(where: { a[$0] != nil }) {
            let e: Element
            switch target(a) {
            case .failure(let f): return fail(f.why)
            case .success(let found): e = found
            }
            _ = e.object.accessibilityActivate()
            if !(firstResponder() is UIKeyInput) { focus(e) }
        }
        guard let r = firstResponder() as? UIKeyInput else { return fail("no text field has focus") }
        r.insertText(a["text"] as? String ?? "")
        return ["typed": true]
    }

    /// The text field or view drawn for `e`: inside its view, else under its middle.
    private static func focus(_ e: Element) {
        func input(in v: UIView) -> UIView? {
            if v is UIKeyInput, v.canBecomeFirstResponder { return v }
            for s in v.subviews { if let f = input(in: s) { return f } }
            return nil
        }
        var found = view(of: e.object).flatMap { input(in: $0) }
        if found == nil, let w = window, let hit = w.hitTest(w.convert(CGPoint(x: e.frame.midX, y: e.frame.midY), from: nil), with: nil) {
            // The hit view's own subtree, then only the views it sits in (a field editor inside its text field).
            found = input(in: hit)
            var up = hit.superview
            while found == nil, let u = up {
                if u is UIKeyInput, u.canBecomeFirstResponder { found = u }
                up = u.superview
            }
        }
        found?.becomeFirstResponder()
    }

    /// The app's windows as a PNG (no status bar, unlike `simctl io screenshot`) at `scale` (the screen's when 0),
    /// written to `path` (a simulator's files are the Mac's) or returned as base64. `format` "jpeg" (`quality`, 0.8):
    /// a fraction of the size, for the hub (a real phone has no `simctl io`).
    private static func snapshot(_ a: Args) -> Reply {
        guard let image = render(scale: a["scale"] as? Double ?? 0) else { return fail("no window") }
        let jpeg = a["format"] as? String == "jpeg"
        guard let png = jpeg ? image.jpegData(compressionQuality: a["quality"] as? Double ?? 0.8) : image.pngData() else {
            return fail("cannot encode the image")
        }
        if let p = a["path"] as? String {
            do { try png.write(to: URL(fileURLWithPath: p)) } catch { return fail("\(error)") }
            return ["path": p, "bytes": png.count]
        }
        return ["png": png.base64EncodedString(), "format": jpeg ? "jpeg" : "png"] // "png" is the key for either
    }
}

extension UIResponder {
    /// RuntimeCLI.firstResponder's probe: the first responder answers the nil-targeted action.
    @objc fileprivate func rtcliRecordFirstResponder(_ sender: Any?) {
        RuntimeCLI.responder = self
    }
}
#endif
