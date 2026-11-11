import SwiftUI

// A Mac's VNC (docs/extensions/computer-use.md「Live view」, #1343): opened from a machine's page, a tool, the running work or a
// chat's ··· menu (`AppModel.openVNC`), it comes up in the small window (VNCPiP) when this device keeps the Mac's
// sign-in, else on the whole screen (VNCPage) in a window of its own (ScreenWindow). The app draws it itself
// (RFBSession); the hub's /screen/<machine> page is the web's.

/// Builds the web page's address (the hub's /screen/<machine>, for a browser).
enum ScreenViewer {
    @MainActor static func url(_ model: AppModel, _ ref: MachineRef, unlock: Bool = false) -> URL? {
        guard let api = model.api(ref.group) else { return nil }
        #if os(macOS)
        let keep = "mac"
        #else
        let keep = "phone"
        #endif
        return url(api.url("/screen/\(ref.id)"), token: api.config.token, name: model.machine(ref)?.name, keep: keep, unlock: unlock)
    }

    /// RFC 3986's unreserved characters, the only ones that go into the fragment as they are.
    nonisolated static let unreserved = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")

    /// The page with this device's token and the Mac's name in its fragment (`#token=…&name=…&keep=phone&embed=1`),
    /// each value percent-encoded once: the page reads them back with URLSearchParams. Set as the encoded fragment,
    /// since `URLComponents.fragment` encodes a second time (a `jd_` token arrived as `jd%5F`, #1498). `keep`: this
    /// device remembers the Mac's sign-in (phone | mac, #1322), the page offers 「在这台手机上记住」.
    nonisolated static func url(_ page: URL, token: String, name: String?, keep: String? = nil, unlock: Bool = false) -> URL? {
        func encoded(_ s: String) -> String { s.addingPercentEncoding(withAllowedCharacters: unreserved) ?? "" }
        var parts = ["token=" + encoded(token)]
        if let name, !name.isEmpty { parts.append("name=" + encoded(name)) }
        if let keep { parts.append("keep=" + encoded(keep)) }
        if unlock { parts.append("unlock=1") } // One-Tap Unlock (#1507 a-2): the page asks for the password once it is in
        parts.append("embed=1")
        var c = URLComponents(url: page, resolvingAgainstBaseURL: false)
        c?.percentEncodedFragment = parts.joined(separator: "&")
        return c?.url
    }
}

/// The live screen asked for (`AppModel.screenOpen`).
struct ScreenOpen: Identifiable, Hashable {
    let machine: MachineRef
    /// One-Tap Unlock: once signed in, the kept password goes onto the Mac's lock screen.
    var unlock = false
    /// The chat it was opened from: when its agent waits on the user, the page offers I'm Done.
    var from: SessionKey? = nil
    var id: MachineRef { machine }
}

#if os(iOS)
/// Which ways the app turns (#3075): an iPhone stays upright except while the live screen's page is up, which also
/// turns to either landscape; an iPad turns every way. The app delegate answers with `mask`, which replaces Info.plist's
/// lists (an iPhone's says upright: it launches upright however it is held), so both are spelled out here.
@MainActor enum AppOrientation {
    /// The live screen's page is up (ScreenWindow).
    private(set) static var screenUp = false
    private static var phone: Bool { UIDevice.current.userInterfaceIdiom == .phone }

    static var mask: UIInterfaceOrientationMask {
        guard phone else { return .all }
        return screenUp ? .allButUpsideDown : .portrait
    }

    /// The live screen's page came up (landscape allowed: the phone turns to how it is held) or went (back upright).
    static func screen(_ up: Bool, in scene: UIWindowScene?) {
        guard up != screenUp else { return }
        screenUp = up
        guard let scene else { return }
        // UIKit asks the shell's controllers, and what they present (a machine's page in a sheet), not the screen
        // window's: every one of them reads the policy again.
        for w in scene.windows {
            var c = w.rootViewController
            while let v = c {
                v.setNeedsUpdateOfSupportedInterfaceOrientations()
                c = v.presentedViewController
            }
        }
        // Closed on its side: upright again now, not when the phone next turns.
        if !up, phone, scene.effectiveGeometry.interfaceOrientation != .portrait {
            scene.requestGeometryUpdate(.iOS(interfaceOrientations: .portrait))
        }
    }
}

#endif

#if os(iOS) || os(visionOS)
/// The live screen in a window of its own (#1604): over the shell's window and whatever it presents (a sheet, a page
/// pushed in one), under the toasts' (`ToastWindow`). SwiftUI presents one thing at a time from the root, so a
/// fullScreenCover there was swallowed while a machine page was up in a sheet. `AppModel.screenOpen` shows and hides it.
/// On an iPhone it turns to landscape while it is up, and the app goes back upright as it closes (#3075).
@MainActor final class ScreenWindow {
    static let shared = ScreenWindow()
    private var window: UIWindow?
    private weak var before: UIWindow?

    func show(_ open: ScreenOpen?) {
        guard let open else {
            upright()
            window?.isHidden = true
            window = nil
            before?.makeKey()
            return
        }
        let scenes = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }
        guard let scene = scenes.first(where: { $0.activationState == .foregroundActive }) ?? scenes.first else { return }
        let shell = scene.windows.first { $0.isKeyWindow } ?? scene.windows.first
        if window == nil { before = shell }
        let w = window ?? UIWindow(windowScene: scene)
        w.windowLevel = UIWindow.Level(rawValue: UIWindow.Level.normal.rawValue + 0.5) // under ToastWindow's .normal + 1
        w.backgroundColor = .clear
        w.overrideUserInterfaceStyle = shell?.overrideUserInterfaceStyle ?? .unspecified
        let host = UIHostingController(rootView: ScreenWindowRoot(open: open).environment(AppModel.shared))
        host.view.backgroundColor = .clear
        w.rootViewController = host
        w.isHidden = false
        w.makeKey()
        window = w
        #if os(iOS)
        AppOrientation.screen(true, in: scene)
        #endif
    }

    /// Back to the app's upright policy (an iPhone turns back as the page fades): on closing, before the window goes.
    func upright() {
        #if os(iOS)
        AppOrientation.screen(false, in: window?.windowScene ?? before?.windowScene)
        #endif
    }
}

/// The page fading in as it opens, and out again as it closes, then the window goes.
private struct ScreenWindowRoot: View {
    let open: ScreenOpen
    @State private var visible = false

    var body: some View {
        ZStack {
            if visible {
                VNCPage(ref: open.machine, unlock: open.unlock, from: open.from, onClose: close).transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .onAppear { withAnimation(Theme.Motion.layout) { visible = true } }
        // The small window took this Mac (#1839): the page closes as its × does.
        .onChange(of: AppModel.shared.vncPiP) { _, now in if now == open.machine, visible { close() } }
    }

    private func close() {
        ScreenWindow.shared.upright()
        withAnimation(Theme.Motion.layout) { visible = false } completion: { AppModel.shared.screenOpen = nil }
    }
}
#else
/// The live screen in a window of its own on the Mac (#1604), as `AppModel.screenOpen` says; closing it clears that.
@MainActor final class ScreenWindow: NSObject, NSWindowDelegate {
    static let shared = ScreenWindow()
    private var window: NSWindow?

    func show(_ open: ScreenOpen?) {
        guard let open else {
            let w = window
            window = nil
            w?.close()
            return
        }
        let host = NSHostingController(rootView: VNCPage(ref: open.machine, unlock: open.unlock, from: open.from) { AppModel.shared.screenOpen = nil }
            .environment(AppModel.shared))
        let w = window ?? NSWindow(contentViewController: host)
        w.contentViewController = host
        w.title = "VNC · " + (AppModel.shared.machine(open.machine)?.name ?? open.machine.id)
        w.styleMask.insert([.resizable, .closable, .miniaturizable])
        w.isReleasedWhenClosed = false
        w.delegate = self
        if window == nil {
            w.setContentSize(NSApp.mainWindow.map { NSSize(width: $0.frame.width * 0.75, height: $0.frame.height * 0.8) } ?? w.frame.size)
            w.center()
        }
        w.makeKeyAndOrderFront(nil)
        window = w
    }

    func windowWillClose(_ notification: Notification) {
        guard window != nil else { return }
        window = nil
        AppModel.shared.screenOpen = nil
    }
}
#endif

/// VNC under a tool that operates a Mac: the assistant's computer_state / computer_run (the machine it names), or an
/// agent's own Cua Driver MCP tools (the agent's machine), while that Mac's live screen can be shown.
struct ScreenEntry: View {
    @Environment(AppModel.self) private var model
    @Environment(\.fileScope) private var scope
    let call: ToolCall

    var body: some View {
        if let ref = Self.machine(call.name, input: call.input, chat: scope?.machine, machines: model.machines), model.canShowScreen(ref) {
            Button { model.openVNC(ref) } label: {
                Label("VNC", systemImage: Theme.Icon.screen).font(Theme.Font.labelStrong).lineLimit(1)
            }
            .themedButton()
            .accessibilityIdentifier("tool-screen")
        }
    }

    /// The Mac a tool call operates: the one computer_* names (by id or name, in the chat's group of hubs), or the
    /// chat's own for an agent's Cua Driver tools (mcp__cua-driver__click…).
    static func machine(_ name: String, input: JSONValue, chat: MachineRef?, machines: [Machine]) -> MachineRef? {
        let base = ToolPresentation.baseName(name)
        if base == "computer_state" || base == "computer_run" {
            guard let named = input["machine"]?.string, !named.isEmpty else { return nil }
            let group = chat?.group ?? ""
            return machines.first { $0.group == group && ($0.id == named || $0.name.caseInsensitiveCompare(named) == .orderedSame) }?.ref
        }
        return ComputerSteps.isCuaTool(name) ? chat : nil
    }
}

/// 「正在操作 MacBook · 点 AC → 6」 with the chat's running work (#1343): while the model operates a Mac, and for a few
/// seconds after; a tap opens that Mac's live screen. The task dock's pill face (`DockPillLabel`).
struct ComputerActivityPill: View {
    @Environment(AppModel.self) private var model
    var activity: ComputerActivity?
    /// The chat it is in (VNC offers I'm Done while its agent waits).
    var from: SessionKey? = nil

    var body: some View {
        TimelineView(.periodic(from: .now, by: 1)) { ctx in
            if let a = activity, a.shown(at: ctx.date) {
                Button { Haptics.tap(); model.openVNC(a.machine, from: from) } label: {
                    DockPillLabel(title: a.running ? String(localized: "Controlling \(a.machineName) · \(a.step)") : String(localized: "Just controlled \(a.machineName) · \(a.step)"), state: .working, waiting: false,
                                  maxWidth: Theme.Size.box280) {
                        if a.running {
                            ProgressView().controlSize(.mini)
                        } else {
                            ThemedIcon(Theme.Icon.screen).font(Theme.Font.labelHeavy).foregroundStyle(.secondary)
                        }
                    }
                }
                .buttonStyle(.plain)
                .disabled(!model.canShowScreen(a.machine))
                .accessibilityElement(children: .combine)
                .accessibilityLabel(a.running ? String(localized: "Controlling \(a.machineName), \(a.step)") : String(localized: "Just controlled \(a.machineName), \(a.step)"))
                .accessibilityHint(model.canShowScreen(a.machine) ? String(localized: "Opens VNC") : "")
                .accessibilityIdentifier("computer-activity")
                .transition(.opacity)
            }
        }
        .animation(Theme.Motion.state, value: activity?.toolUseID)
    }

    /// Shown at all (for the rows that only appear when there is something).
    static func shows(_ a: ComputerActivity?) -> Bool { a?.shown(at: .now) ?? false }
}

/// VNC in a chat's ··· menu (#1343, #1604): an agent's chat has its own Mac's, straight away, or none; the assistant's,
/// the Mac it operated last, else every Mac whose live screen can be shown (a submenu when there are several).
struct ScreenMenu: View {
    @Environment(AppModel.self) private var model
    /// An agent's chat: its own Mac, the only one (nothing when it is not a Mac whose screen can be shown).
    var machine: MachineRef? = nil
    /// The assistant's: the Mac it operated last; with none, the Macs whose screen can be shown.
    var recent: MachineRef? = nil
    /// The chat it is in.
    var from: SessionKey? = nil

    var body: some View {
        if let machine {
            if model.canShowScreen(machine), let m = model.machine(machine) { item(m) }
        } else if let recent, model.canShowScreen(recent), let m = model.machine(recent) {
            item(m)
        } else {
            let macs = model.screenMachines
            if macs.count == 1, let m = macs.first {
                item(m)
            } else if !macs.isEmpty {
                Menu {
                    ForEach(macs) { m in Button { model.openVNC(m.ref, from: from) } label: { Text(m.name).lineLimit(1) } }
                } label: {
                    Label("VNC", systemImage: Theme.Icon.screen).lineLimit(1)
                }
            }
        }
    }

    private func item(_ m: Machine) -> some View {
        Button { model.openVNC(m.ref, from: from) } label: { Label("VNC · \(m.name)", systemImage: Theme.Icon.screen).lineLimit(1) }
    }
}
