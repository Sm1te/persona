import AVFoundation
import CoreMedia
import CoreVideo
import Observation
import SwiftUI

/// One Mac's VNC (#1343): the native client (RFBClient) over the hub's relay, signed in with the sign-in this device
/// keeps (ScreenCredentials, #1322) or the one typed in, and its frames for every layer that shows them (the page's,
/// the picture-in-picture's). One per Mac at a time (`VNC.session`); it goes when nothing shows it any more.
@MainActor @Observable final class RFBSession {
    enum Phase: Equatable {
        case connecting
        /// The Mac wants its account and password: none kept, or the kept one refused (failed says so).
        case signIn(failed: String?)
        case live
        case closed(String?)
    }

    let ref: MachineRef
    private(set) var phase: Phase = .connecting
    /// The Mac's screen, in its pixels.
    private(set) var size: CGSize = .zero
    /// The server's screens on it (ExtendedDesktopSize, #80); empty from a server that sends none (macOS's, as far as is known).
    /// Kept across reconnections until the next list, so a chosen display does not flash to all of them.
    private(set) var screens: [RFBClient.Screen] = []
    /// Where the pointer is, on the Mac's screen (the last one sent; the middle before any).
    private(set) var pointer: CGPoint = .zero
    private(set) var cursor: RFBClient.Cursor?
    /// What the Mac last put on its clipboard (ServerCutText).
    private(set) var macClipboard: String?
    /// Frames a second shown, and how long the last one took from the mock's Screen Sharing to here (its stamp).
    private(set) var fps: Double = 0
    private(set) var latency: Double?
    /// Megabits a second from the Mac lately (the page's numbers in DEBUG builds).
    private(set) var mbps: Double = 0
    @ObservationIgnored private var counted: (bytes: Int, at: Date)?
    /// A frame of this connection has been shown: the small window shows it instead of its card (VNCPiPCard).
    private(set) var pictured = false
    /// When the last frame of this connection was copied out (not observed: read when it matters), and which
    /// connection this is (a new one each run): a check made on one connection is not answered by the next (#4380).
    @ObservationIgnored private(set) var lastFrame: Date?
    @ObservationIgnored private(set) var connection = 0

    @ObservationIgnored private var client: RFBClient?
    @ObservationIgnored private let sink = RFBFrameSink()
    @ObservationIgnored private var asked: CheckedContinuation<RFBClient.Credentials?, Never>?
    @ObservationIgnored private var typed: (RFBClient.Credentials, remember: Bool)?
    @ObservationIgnored private var refusedKept = false
    @ObservationIgnored private var buttons: UInt8 = 0
    /// Keys and pastes in the order they were made (#2374): a paste waits on the hub, so what follows waits behind it.
    @ObservationIgnored private var input: RFBInputQueue!

    init(ref: MachineRef) {
        self.ref = ref
        input = RFBInputQueue(send: { [weak self] e in self?.client?.key(e.keysym, down: e.down) },
                              write: { [weak self] text in
                                  // No connection: its ⌘V could not go out, so the paste counts as failed.
                                  guard let self, self.client != nil else { return false }
                                  return await self.setMacClipboard(text)
                              })
    }

    func start() {
        guard client == nil, let api = AppModel.shared.api(ref.group) else {
            if AppModel.shared.api(ref.group) == nil { phase = .closed(String(localized: "Can't reach the hub")) }
            return
        }
        run(RFBClient(transport: RFBWebSocket(api: api, machine: ref.id)) { [weak self] in await self?.credentials() })
    }

    /// Follows this client and starts it: start's over the hub's relay, or a test's over a scripted transport.
    func run(_ c: RFBClient) {
        c.onState = { [weak self] s in Task { @MainActor in self?.state(s) } }
        c.onCutText = { [weak self] text in Task { @MainActor in self?.macClipboard = text } }
        c.onCursor = { [weak self] cursor in Task { @MainActor in self?.cursor = cursor } }
        c.onResize = { [weak self] w, h in Task { @MainActor in self?.resized(width: w, height: h) } }
        c.onScreens = { [weak self] screens in Task { @MainActor in self?.screens = screens } }
        sink.attach(c) { [weak self, weak c] fps, latency in
            // Stamped here, when the frame was copied: a report still queued from before a suspension keeps its time.
            let at = Date.now, bytes = c?.received ?? 0
            Task { @MainActor in
                guard let self else { return }
                self.fps = fps
                self.latency = latency
                self.lastFrame = at
                if let was = self.counted, at.timeIntervalSince(was.at) >= 1 {
                    self.mbps = Double(bytes - was.bytes) * 8 / 1e6 / at.timeIntervalSince(was.at)
                    self.counted = (bytes, at)
                } else if self.counted == nil {
                    self.counted = (bytes, at)
                }
                if !self.pictured { self.pictured = true }
            }
        }
        client = c
        phase = .connecting
        pictured = false
        lastFrame = nil
        counted = nil
        connection += 1
        c.start()
    }

    func close() {
        sink.detach()
        client?.close()
        client = nil
        asked?.resume(returning: nil)
        asked = nil
        if case .closed = phase {} else { phase = .closed(nil) }
    }

    /// Shown in this layer from now on (until it goes). Updates are always asked for the whole screen, even when one
    /// display or a zoomed-in part is shown: macOS's Screen Sharing leaves most smaller requests unanswered (#3067).
    func show(in layer: AVSampleBufferDisplayLayer, timed: Bool = false) { sink.add(layer, timed: timed) }
    func hide(from layer: AVSampleBufferDisplayLayer) { sink.remove(layer) }

    // MARK: Signing in

    /// What the client asks when the Mac wants a sign-in: the kept one first, else wait for the form.
    private func credentials() async -> RFBClient.Credentials? {
        if !refusedKept, let kept = ScreenCredentials.load(ref) {
            typed = nil
            return .init(account: kept.account, password: kept.password)
        }
        if let t = typed { return t.0 }
        return await withCheckedContinuation { k in
            asked = k
            phase = .signIn(failed: refusedKept ? String(localized: "The saved password no longer works (the Mac's password may have changed). Enter it again; it replaces the saved one once connected.") : nil)
        }
    }

    /// The form's account and password; `remember`: kept on this device once the Mac takes them.
    func signIn(account: String, password: String, remember: Bool) {
        let c = RFBClient.Credentials(account: account, password: password)
        typed = (c, remember)
        phase = .connecting
        if let k = asked {
            asked = nil
            k.resume(returning: c)
        } else {
            restart()
        }
    }

    /// The whole screen asked for again: a live connection answers with a frame (#4380).
    func refresh() { client?.refresh() }

    /// A new connection in place of this one, even while it still says live (it went quiet, #4380).
    func reconnect() { restart() }

    private func restart() {
        sink.detach()
        client?.close()
        client = nil
        start()
    }

    private func state(_ s: RFBClient.State) {
        switch s {
        case .connecting, .signingIn:
            if case .signIn = phase {} else { phase = .connecting }
        case let .live(width, height, _):
            size = CGSize(width: width, height: height)
            if pointer == .zero { pointer = CGPoint(x: width / 2, y: height / 2) }
            phase = .live
            input.signedIn() // once a connection: the Mac drops keys for a moment after it (#3075)
            if let t = typed {
                if t.remember { ScreenCredentials.save(.init(account: t.0.account, password: t.0.password), for: ref) } else { ScreenCredentials.forget(ref) }
                typed = nil
            }
            refusedKept = false
        case .refused(let why):
            if typed == nil, ScreenCredentials.load(ref) != nil, !refusedKept {
                refusedKept = true // the kept one: ask for a new one, on a new connection
                restart()
                return
            }
            typed = nil
            phase = .signIn(failed: why.isEmpty || why.contains("Authentication") ? String(localized: "Wrong account or password") : String(localized: "Couldn't sign in: \(why)"))
            client = nil
        case .failed(let why):
            if case .closed = phase { return }
            phase = .closed(why)
            client = nil
        case .closed:
            break
        }
    }

    /// The Mac's screen took a new size mid-session (DesktopSize, #2372): the geometry follows it and the pointer is
    /// kept on it, without sending anything (the next move sends where it is).
    private func resized(width: Int, height: Int) {
        size = CGSize(width: width, height: height)
        pointer = Self.clamped(pointer, to: size)
    }

    // MARK: Pointer, keys, clipboard

    /// p kept on a screen of this size (its last pixel at most).
    nonisolated static func clamped(_ p: CGPoint, to size: CGSize) -> CGPoint {
        CGPoint(x: min(max(0, p.x), max(0, size.width - 1)), y: min(max(0, p.y), max(0, size.height - 1)))
    }

    /// The pointer to p on the Mac's screen, with these buttons down.
    func move(to p: CGPoint, buttons: UInt8? = nil) {
        let q = Self.clamped(p, to: size)
        pointer = q
        if let buttons { self.buttons = buttons }
        client?.pointer(x: Int(q.x), y: Int(q.y), buttons: self.buttons)
    }

    func click(at p: CGPoint, button: UInt8 = 1) {
        move(to: p, buttons: 0)
        move(to: p, buttons: button)
        move(to: p, buttons: 0)
    }

    /// The wheel: up (positive) or down, a notch at a time.
    func scroll(notches: Int, at p: CGPoint) {
        let b: UInt8 = notches > 0 ? 8 : 16
        for _ in 0..<min(abs(notches), 20) {
            move(to: p, buttons: buttons | b)
            move(to: p, buttons: buttons & ~b)
        }
    }

    func key(_ keysym: UInt32, down: Bool) { input.keys([(keysym, down)]) }

    /// A key pressed and let go, with these modifiers held around it.
    func press(_ keysym: UInt32, with modifiers: [UInt32] = []) { input.press(keysym, with: modifiers) }

    /// A pause between the keys before it and after it, measured from when those before it went out.
    func pause(_ duration: Duration) { input.pause(duration) }

    /// Text typed: Latin-1 a key at a time; anything else (Chinese from the input method) by the Mac's clipboard and
    /// ⌘V, through its node (`computer/clipboard`); in the order typed (#2374). A paste whose clipboard write fails
    /// is left out (no ⌘V), with one toast for a run of failures.
    func type(_ text: String) {
        input.type(text) { [weak self] in
            guard let self else { return }
            let name = AppModel.shared.machine(self.ref)?.name ?? self.ref.id
            AppModel.shared.toast = Toast(String(localized: "Couldn't write to \(name)'s clipboard; some text wasn't typed"), style: .error)
        }
    }

    nonisolated static func keysym(_ ch: Unicode.Scalar) -> UInt32 {
        switch ch {
        case "\n", "\r": return 0xff0d
        case "\t": return 0xff09
        default: return ch.value < 256 ? ch.value : 0x0100_0000 + ch.value
        }
    }

    /// Text onto the Mac's clipboard, then ⌘V there, after the keys and pastes before it; false (and no ⌘V) when the
    /// clipboard did not take it.
    @discardableResult func paste(_ text: String) async -> Bool { await input.paste(text).value }

    /// Onto the Mac's clipboard, there by the time this says true (#3075): through its node (pbcopy, which answers once
    /// the text is on the clipboard). RFB's own clipboard (ClientCutText) only when the node cannot (one too old for it):
    /// it carries Latin-1 alone and says nothing back, and macOS's Screen Sharing hands it to the clipboard on its own
    /// time, so a ⌘V right after it could paste what was there before. False when neither could: no connection, no hub,
    /// or the node failed for text outside Latin-1.
    func setMacClipboard(_ text: String) async -> Bool {
        if let api = AppModel.shared.api(ref.group), (try? await api.setComputerClipboard(machine: ref.id, text)) != nil { return true }
        guard text.unicodeScalars.allSatisfy({ $0.value < 256 }), let client else { return false }
        client.cutText(text)
        return true
    }

    /// The Mac's clipboard now: through its node, else what it last sent.
    func readMacClipboard() async -> String? {
        if let text = try? await AppModel.shared.api(ref.group)?.computerClipboard(machine: ref.id), !text.isEmpty { return text }
        return macClipboard
    }

    /// The keysyms of the modifiers (macOS's Screen Sharing: ⌘ as Super, ⌥ as Alt).
    static let command: UInt32 = 0xffeb, option: UInt32 = 0xffe9, control: UInt32 = 0xffe3, shift: UInt32 = 0xffe1
}

/// Every Mac's VNC session (one each), and how many hold it (the page, the small window): closed a moment after the
/// last lets go, so passing it from one to the other keeps the connection.
@MainActor enum VNC {
    private static var sessions: [MachineRef: (session: RFBSession, holders: Int)] = [:]
    private static var closing: [MachineRef: Task<Void, Never>] = [:]

    static func hold(_ ref: MachineRef) -> RFBSession {
        closing.removeValue(forKey: ref)?.cancel()
        if let s = sessions[ref] {
            sessions[ref] = (s.session, s.holders + 1)
            if case .closed = s.session.phase { s.session.start() }
            return s.session
        }
        let s = RFBSession(ref: ref)
        sessions[ref] = (s, 1)
        s.start()
        return s
    }

    static func release(_ ref: MachineRef) {
        guard let s = sessions[ref] else { return }
        sessions[ref] = (s.session, max(0, s.holders - 1))
        guard s.holders <= 1 else { return }
        closing[ref] = Task {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled, let now = sessions[ref], now.holders == 0 else { return }
            sessions[ref] = nil
            closing[ref] = nil
            now.session.close()
        }
    }

    static func current(_ ref: MachineRef) -> RFBSession? { sessions[ref]?.session }
}
