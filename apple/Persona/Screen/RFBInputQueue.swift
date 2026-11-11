import Foundation

/// Keys and pastes to the Mac in the order they were made (#2374). A paste waits on the Mac's clipboard being written
/// (through the hub) before its ⌘V, so everything made after it (keys, further pastes) waits behind it on one tail;
/// with nothing pending, keys go out at once. A paste whose clipboard write fails sends no ⌘V (it would paste whatever
/// was on the Mac's clipboard before); what comes after it still goes out.
///
/// A new session's first seconds (#3075): macOS's Screen Sharing drops every key event it gets in roughly the first 2 to
/// 3 seconds after a client signs in. Measured on macOS 26 against a Terminal window: a ⌘V or a Return sent then never
/// arrived (the latest loss 3.05 s in, on a heavily loaded Mac; 2.2 s on a quiet one), pointer events counted at once,
/// and nothing on the wire says when keys start to count. The app signs in again each time it comes back from the
/// background, so a paste of text just copied in another app was lost. Keys, and a paste's ⌘V, made in a session's first
/// `settle` wait until then and go out in order; the paste's clipboard write does not wait.
@MainActor final class RFBInputQueue {
    /// One key event: its keysym, and down or up.
    typealias Event = (keysym: UInt32, down: Bool)

    private let send: @MainActor (Event) -> Void
    private let write: @MainActor (String) async -> Bool
    /// How long after signing in the Mac starts taking keys, with a margin.
    private let settle: Duration
    /// Until when the session that just signed in drops keys; nil: it takes them.
    private var keysFrom: ContinuousClock.Instant?
    private var tail: Task<Bool, Never>?
    private var serial = 0
    /// A paste of typed text failed and was told: the next failures in the same run say nothing more.
    private var typedFailed = false

    /// `send`: a key event onto the wire; `write`: text onto the Mac's clipboard, true once it is there.
    init(settle: Duration = .milliseconds(3500), send: @escaping @MainActor (Event) -> Void,
         write: @escaping @MainActor (String) async -> Bool) {
        self.settle = settle
        self.send = send
        self.write = write
    }

    /// Nothing waiting: keys go out as they are made.
    var idle: Bool { tail == nil && !settling }

    /// A session signed in (each connection, a reconnect too): its keys wait out its first seconds.
    func signedIn() { keysFrom = ContinuousClock.now + settle }

    private var settling: Bool { keysFrom.map { ContinuousClock.now < $0 } ?? false }

    /// Until the session takes keys (at once when it does); a newer session's sign-in meanwhile moves it later.
    private func keysTaken() async {
        while let from = keysFrom, ContinuousClock.now < from {
            try? await Task.sleep(until: from, clock: .continuous)
        }
        keysFrom = nil
    }

    /// These key events, in order, after whatever is pending.
    func keys(_ events: [Event]) {
        guard tail != nil || settling else {
            for e in events { send(e) }
            return
        }
        enqueue { [send, weak self] in
            await self?.keysTaken()
            for e in events { send(e) }
            return true
        }
    }

    /// A pause after whatever is pending, before what comes next: from when the keys before it went out (a new
    /// session's keys wait for it first), not from when it was asked for.
    func pause(_ duration: Duration) {
        enqueue { [weak self] in
            await self?.keysTaken()
            try? await Task.sleep(for: duration)
            return true
        }
    }

    /// A key pressed and let go, with these modifiers held around it.
    func press(_ keysym: UInt32, with modifiers: [UInt32] = []) {
        keys(modifiers.map { ($0, true) } + [(keysym, true), (keysym, false)] + modifiers.reversed().map { ($0, false) })
    }

    /// Text typed: Latin-1 a key at a time, anything else (Chinese from the input method) pasted, in the order typed.
    /// `failed` once for a run of failed pastes: the run ends at a paste that goes through, or when everything has gone
    /// out before the next text.
    func type(_ text: String, failed: @escaping @MainActor () -> Void) {
        if idle { typedFailed = false }
        var plain: [Event] = []
        var other = ""
        func flushPlain() {
            if !plain.isEmpty { keys(plain) }
            plain = []
        }
        func flushOther() {
            guard !other.isEmpty else { return }
            paste(other) { [weak self] ok in
                guard let self else { return }
                if ok {
                    self.typedFailed = false
                } else if !self.typedFailed {
                    self.typedFailed = true
                    failed()
                }
            }
            other = ""
        }
        for ch in text.unicodeScalars {
            if ch.value < 256 || ch == "\n" || ch == "\t" {
                flushOther()
                let k = RFBSession.keysym(ch)
                plain += [(k, true), (k, false)]
            } else {
                flushPlain()
                other.unicodeScalars.append(ch)
            }
        }
        flushPlain()
        flushOther()
    }

    /// The text onto the Mac's clipboard and then ⌘V there, after whatever is pending; the task's value (and `done`,
    /// called in the queue's order before anything after it runs) is whether the clipboard took it (no ⌘V when not).
    @discardableResult func paste(_ text: String, done: (@MainActor (Bool) -> Void)? = nil) -> Task<Bool, Never> {
        enqueue { [send, write, weak self] in
            let ok = await write(text)
            if ok {
                await self?.keysTaken()
                for e in Self.commandV { send(e) }
            }
            done?(ok)
            return ok
        }
    }

    /// ⌘V as key events (macOS's Screen Sharing takes ⌘ as Super).
    static let commandV: [Event] = [(RFBSession.command, true), (0x76, true), (0x76, false), (RFBSession.command, false)]

    @discardableResult private func enqueue(_ op: @escaping @MainActor () async -> Bool) -> Task<Bool, Never> {
        let previous = tail
        serial &+= 1
        let id = serial
        let task = Task { @MainActor [weak self] in
            _ = await previous?.value
            let ok = await op()
            if let self, self.serial == id { self.tail = nil }
            return ok
        }
        tail = task
        return task
    }
}
