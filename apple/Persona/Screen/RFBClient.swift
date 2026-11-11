import CoreGraphics
import Foundation

/// Bytes to and from a Mac's Screen Sharing: the hub's port relay (RFBWebSocket), or a test's.
nonisolated protocol RFBTransport: AnyObject, Sendable {
    func send(_ bytes: [UInt8]) async throws
    /// The next bytes that came; throws once it is closed.
    func receive() async throws -> [UInt8]
    func close()
}

/// The hub's relay of the Mac's 5900 (`/api/v1/machines/{mid}/ports/5900/connect`, docs/operations/ports.md): one binary
/// WebSocket message a TCP read or write, through the app's own way to the hub (APIClient: its SSH tunnel, its pinned
/// session) and with its device token.
nonisolated final class RFBWebSocket: RFBTransport, @unchecked Sendable {
    private let task: URLSessionWebSocketTask

    init(api: APIClient, machine: String) {
        var comps = URLComponents(url: api.url("/api/v1/machines/\(machine)/ports/5900/connect"), resolvingAgainstBaseURL: false)!
        comps.scheme = comps.scheme == "https" ? "wss" : "ws"
        var req = URLRequest(url: comps.url!)
        api.config.authorize(&req)
        req.setValue(LanguageStore.shared.launched, forHTTPHeaderField: "Accept-Language")
        req.timeoutInterval = 15
        task = api.session.webSocketTask(with: req)
        task.delegate = HubRedirects.shared
        task.maximumMessageSize = 64 << 20
        task.resume()
    }

    func send(_ bytes: [UInt8]) async throws { try await task.send(.data(Data(bytes))) }

    func receive() async throws -> [UInt8] {
        switch try await task.receive() {
        case .data(let d): return [UInt8](d)
        case .string(let s): return Array(s.utf8)
        @unknown default: return []
        }
    }

    func close() { task.cancel(with: .normalClosure, reason: nil) }
}

/// What came in, read a few bytes at a time.
nonisolated final class RFBReader: @unchecked Sendable {
    private let transport: RFBTransport
    private var buffer: [UInt8] = []
    private var at = 0
    /// Every byte that came so far.
    private(set) var received = 0

    init(_ transport: RFBTransport) { self.transport = transport }

    func bytes(_ n: Int) async throws -> [UInt8] {
        while buffer.count - at < n {
            let more = try await transport.receive()
            received += more.count
            // What was read goes once it is all of the buffer or a good part of it, not on every message.
            if at > 0, at == buffer.count || at >= 1 << 16 {
                buffer.removeFirst(at)
                at = 0
            }
            buffer.append(contentsOf: more)
        }
        defer { at += n }
        return Array(buffer[at..<(at + n)])
    }

    func u8() async throws -> UInt8 { try await bytes(1)[0] }
    func u16() async throws -> Int { let b = try await bytes(2); return Int(b[0]) << 8 | Int(b[1]) }
    func u32() async throws -> UInt32 {
        let b = try await bytes(4)
        return UInt32(b[0]) << 24 | UInt32(b[1]) << 16 | UInt32(b[2]) << 8 | UInt32(b[3])
    }
    func s32() async throws -> Int32 { Int32(bitPattern: try await u32()) }
}

/// What the client sends from the handshake's end on, in order, one at a time (RFBClient). A pointer event that only
/// moves (its buttons as the event before it had) replaces one still waiting that only moved too: when the way to the
/// Mac stalls, the pointer goes straight to where the finger is instead of replaying the path, while presses, releases
/// and wheel notches all go, where they were made.
nonisolated final class RFBOutbox: @unchecked Sendable {
    private let lock = NSLock()
    private var queue: [(bytes: [UInt8], motion: Bool)] = []
    private var waiter: CheckedContinuation<[UInt8]?, Never>?
    private var finished = false
    /// The one task that takes messages was cancelled: it takes no more.
    private var cancelled = false
    /// The buttons of the last pointer event queued (sent or not).
    private var buttons: UInt8?

    func send(_ bytes: [UInt8]) {
        let k: CheckedContinuation<[UInt8]?, Never>? = lock.withLock {
            guard !finished else { return nil }
            var motion = false
            if bytes.count == 6, bytes[0] == 5 {
                motion = bytes[1] == buttons
                buttons = bytes[1]
                if motion, let last = queue.last, last.motion, last.bytes[1] == bytes[1] {
                    queue[queue.count - 1].bytes = bytes
                    return nil
                }
            }
            if let w = waiter {
                waiter = nil
                return w
            }
            queue.append((bytes, motion))
            return nil
        }
        k?.resume(returning: bytes)
    }

    /// The next message, waiting for one; nil once finished, or once the task waiting is cancelled (the client's
    /// task group ends with its reader: the sending task must end too).
    func next() async -> [UInt8]? {
        await withTaskCancellationHandler {
            await withCheckedContinuation { k in
                let now: [UInt8]?? = lock.withLock {
                    if !queue.isEmpty { return .some(queue.removeFirst().bytes) }
                    if finished || cancelled { return .some(nil) }
                    waiter = k
                    return nil
                }
                if let now { k.resume(returning: now) }
            }
        } onCancel: {
            let k: CheckedContinuation<[UInt8]?, Never>? = lock.withLock {
                cancelled = true
                defer { waiter = nil }
                return waiter
            }
            k?.resume(returning: nil)
        }
    }

    /// Waiting messages are dropped, and next answers nil from now on.
    func finish() {
        let k: CheckedContinuation<[UInt8]?, Never>? = lock.withLock {
            finished = true
            queue = []
            defer { waiter = nil }
            return waiter
        }
        k?.resume(returning: nil)
    }

    /// Messages waiting (tests).
    var waiting: [[UInt8]] { lock.withLock { queue.map(\.bytes) } }
}

/// The native client of a Mac's Screen Sharing (#1343): RFB 3.8 signed in with Apple Remote Desktop's account and
/// password (or VeNCrypt Plain, or none), the screen kept in an RFBFramebuffer from Raw, CopyRect and ZRLE updates,
/// and the pointer, keys and clipboard sent back. It reads on a task of its own; what it says comes through the
/// callbacks, on that task (onFrame with the framebuffer at hand, to copy from before the next update).
nonisolated final class RFBClient: @unchecked Sendable {
    struct Credentials: Sendable, Equatable {
        var account: String
        var password: String
    }

    enum State: Sendable, Equatable {
        case connecting
        case signingIn
        case live(width: Int, height: Int, name: String)
        /// The Mac turned the sign-in down (its reason, when it gave one).
        case refused(String)
        case failed(String)
        case closed
    }

    enum Failure: Error, Equatable {
        case protocolError(String)
        case refused(String)
        case noCredentials
    }

    /// One of the server's screens (ExtendedDesktopSize): its id and where it is on the framebuffer, in pixels.
    struct Screen: Sendable, Equatable {
        var id: UInt32
        var frame: CGRect
    }

    /// The Mac's cursor (the Cursor pseudo-encoding): BGRA pixels with the mask applied, and its hot spot.
    struct Cursor: Sendable {
        var width: Int, height: Int, hotX: Int, hotY: Int
        var pixels: [UInt8]
    }

    private let transport: RFBTransport
    private let reader: RFBReader
    private let credentials: @Sendable () async -> Credentials?
    private(set) var framebuffer = RFBFramebuffer(width: 1, height: 1)
    /// Held while an update writes the framebuffer: whoever copies it out takes it too (withFramebuffer).
    private let framebufferLock = NSLock()

    /// The framebuffer as it is between two writes (a frame copied out, at its own pace).
    func withFramebuffer<T>(_ body: (RFBFramebuffer) -> T) -> T { framebufferLock.withLock { body(framebuffer) } }
    /// The framebuffer with what changed since the last call (all of it after a new size): a frame copied out of the
    /// parts that need it. The damage starts again empty.
    func withDamage<T>(_ body: (RFBFramebuffer, RFBRegion) -> T) -> T {
        framebufferLock.withLock {
            let d = damage
            damage = RFBRegion()
            return body(framebuffer, d)
        }
    }
    /// What changed since withDamage last took it; framebufferLock guards it.
    private var damage = RFBRegion()
    private let zrle = RFBZRLE()
    private var task: Task<Void, Never>?
    /// The pointer, keys and clipboard, sent one after another in the order they came (from the handshake's end on).
    let outbox = RFBOutbox()
    /// Bytes that came from the Mac so far (the page's numbers in DEBUG builds).
    var received: Int { reader.received }

    var onState: @Sendable (State) -> Void = { _ in }
    /// After each update: the framebuffer, and the part of it that changed.
    var onFrame: @Sendable (RFBFramebuffer, CGRect) -> Void = { _, _ in }
    var onCutText: @Sendable (String) -> Void = { _ in }
    var onCursor: @Sendable (Cursor) -> Void = { _ in }
    /// The Mac's screen took a new size mid-session (DesktopSize): its width and height, before the frame drawn at it.
    var onResize: @Sendable (Int, Int) -> Void = { _, _ in }
    /// The server's screens on the framebuffer (ExtendedDesktopSize), with the first full update and on every change.
    var onScreens: @Sendable ([Screen]) -> Void = { _ in }

    /// Encodings asked for, best first: ZRLE, CopyRect, Raw, then DesktopSize, ExtendedDesktopSize, Cursor and LastRect.
    static let encodings: [Int32] = [16, 1, 0, -223, -308, -239, -224]

    init(transport: RFBTransport, credentials: @escaping @Sendable () async -> Credentials?) {
        self.transport = transport
        reader = RFBReader(transport)
        self.credentials = credentials
    }

    func start() {
        task = Task.detached { [self] in
            onState(.connecting)
            do {
                try await handshake()
                try await withThrowingTaskGroup(of: Void.self) { g in
                    g.addTask { [self] in while let m = await outbox.next() { try await transport.send(m) } }
                    g.addTask { [self] in try await run() }
                    try await g.next()
                    g.cancelAll()
                }
            } catch let Failure.refused(why) {
                onState(.refused(why))
            } catch {
                onState(Task.isCancelled ? .closed : .failed(Self.describe(error)))
            }
            transport.close()
        }
    }

    func close() {
        task?.cancel()
        outbox.finish()
        transport.close()
        onState(.closed)
    }

    // MARK: Out

    /// The pointer at (x, y) of the Mac's screen, with these buttons down (bit 0 left, 1 middle, 2 right; 3, 4 the
    /// wheel up and down).
    func pointer(x: Int, y: Int, buttons: UInt8) {
        let x = UInt16(clamping: max(0, x)), y = UInt16(clamping: max(0, y))
        send([5, buttons, UInt8(x >> 8), UInt8(x & 0xff), UInt8(y >> 8), UInt8(y & 0xff)])
    }

    func key(_ keysym: UInt32, down: Bool) {
        send([4, down ? 1 : 0, 0, 0] + RFBAuth.be32(keysym))
    }

    /// Text onto the Mac's clipboard (ClientCutText: Latin-1 is all RFB carries; anything else goes as "?").
    func cutText(_ text: String) {
        let bytes = text.unicodeScalars.map { $0.value < 256 ? UInt8($0.value) : UInt8(ascii: "?") }
        send([6, 0, 0, 0] + RFBAuth.be32(UInt32(bytes.count)) + bytes)
    }

    /// Asks for the whole screen again (a non-incremental update): the Mac answers even when nothing changed, so a
    /// connection that went away without saying so shows up as no frame (#4380).
    func refresh() { send(requestBytes(incremental: false)) }

    /// A FramebufferUpdateRequest for the whole framebuffer as it is now. Never a part of it, even when the views show
    /// less (#3067): macOS's Screen Sharing leaves a request narrower than the screen, or one not starting at its top,
    /// unanswered while the part changes.
    private func requestBytes(incremental: Bool) -> [UInt8] {
        let (w, h) = withFramebuffer { (UInt16(clamping: $0.width), UInt16(clamping: $0.height)) }
        return [3, incremental ? 1 : 0, 0, 0, 0, 0, UInt8(w >> 8), UInt8(w & 0xff), UInt8(h >> 8), UInt8(h & 0xff)]
    }

    private func send(_ bytes: [UInt8]) { outbox.send(bytes) }

    // MARK: In

    private func handshake() async throws {
        let version = String(decoding: try await reader.bytes(12), as: UTF8.self)
        guard version.hasPrefix("RFB ") else { throw Failure.protocolError(String(localized: "Not Screen Sharing: \(version.prefix(12))")) }
        try await transport.send(Array("RFB 003.008\n".utf8))
        let count = Int(try await reader.u8())
        if count == 0 { throw Failure.protocolError(try await reason()) }
        let offered = try await reader.bytes(count)
        guard let type = RFBAuth.preferred.first(where: offered.contains) else {
            throw Failure.protocolError(String(localized: "This Mac’s Screen Sharing asks for a sign-in method that isn’t supported yet (\(offered.map(String.init).joined(separator: ", ")))"))
        }
        try await transport.send([type])
        switch type {
        case RFBAuth.ard: try await ard()
        case RFBAuth.veNCrypt: try await veNCrypt()
        default: break
        }
        if try await reader.u32() != 0 { throw Failure.refused(try await reason()) }
        try await transport.send([1]) // ClientInit: shared
        let width = try await reader.u16(), height = try await reader.u16()
        _ = try await reader.bytes(16) // its pixel format: the client asks for its own next
        let name = String(decoding: try await reader.bytes(Int(try await reader.u32())), as: UTF8.self)
        framebufferLock.withLock {
            framebuffer = RFBFramebuffer(width: width, height: height)
            damage = RFBRegion(CGRect(x: 0, y: 0, width: width, height: height))
        }
        // SetPixelFormat: 32 bits, depth 24, little-endian, true colour, 8 bits each, red at 16, green 8, blue 0.
        try await transport.send([0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0])
        var setEncodings: [UInt8] = [2, 0, 0, UInt8(Self.encodings.count)]
        for e in Self.encodings { setEncodings += RFBAuth.be32(UInt32(bitPattern: e)) }
        try await transport.send(setEncodings)
        onState(.live(width: width, height: height, name: name))
        try await request(incremental: false)
    }

    private func sign() async throws -> Credentials {
        onState(.signingIn)
        guard let c = await credentials() else { throw Failure.noCredentials }
        return c
    }

    private func ard() async throws {
        let generator = try await reader.bytes(2)
        let length = try await reader.u16()
        let prime = try await reader.bytes(length), serverKey = try await reader.bytes(length)
        let c = try await sign()
        let r = try RFBAuth.ard(generator: generator, prime: prime, serverKey: serverKey, account: c.account, password: c.password)
        try await transport.send(r.sealed + r.publicKey)
    }

    private func veNCrypt() async throws {
        let version = try await reader.bytes(2)
        guard version == [0, 2] else { throw Failure.protocolError("VeNCrypt \(version)") }
        try await transport.send([0, 2])
        guard try await reader.u8() == 0 else { throw Failure.protocolError(String(localized: "VeNCrypt 0.2 was refused")) }
        let n = Int(try await reader.u8())
        var subtypes: [UInt32] = []
        for _ in 0..<n { subtypes.append(try await reader.u32()) }
        guard subtypes.contains(RFBAuth.plain) else { throw Failure.protocolError(String(localized: "VeNCrypt offers no Plain")) }
        try await transport.send(RFBAuth.be32(RFBAuth.plain))
        let c = try await sign()
        try await transport.send(RFBAuth.plainCredentials(account: c.account, password: c.password))
    }

    private func reason() async throws -> String {
        let n = Int(try await reader.u32())
        return String(decoding: try await reader.bytes(min(n, 4096)), as: UTF8.self)
    }

    private func request(incremental: Bool) async throws {
        try await transport.send(requestBytes(incremental: incremental))
    }

    private func run() async throws {
        while !Task.isCancelled {
            switch try await reader.u8() {
            case 0: try await update()
            case 1: // colour map entries: not for a true-colour format
                _ = try await reader.bytes(1)
                _ = try await reader.u16()
                _ = try await reader.bytes(try await reader.u16() * 6)
            case 2: break // bell
            case 3:
                _ = try await reader.bytes(3)
                let n = Int(try await reader.u32())
                let text = try await reader.bytes(n)
                onCutText(String(text.map { Character(Unicode.Scalar($0)) }))
            case let t: throw Failure.protocolError(String(localized: "Unknown message \(t)"))
            }
        }
    }

    /// One update, then the next asked for (incrementally) once it is in. Asking earlier made macOS's Screen Sharing
    /// answer later (#3067): typing p50 374 ms against 215 over a 25 ms, 30 Mbit/s link when asked as a small update
    /// started, and up to 2 s of silence when the request reached it while it was still writing a big one.
    private func update() async throws {
        _ = try await reader.bytes(1)
        let count = try await reader.u16()
        var dirty = CGRect.null
        for _ in 0..<count {
            let x = try await reader.u16(), y = try await reader.u16(), w = try await reader.u16(), h = try await reader.u16()
            let encoding = try await reader.s32()
            switch encoding {
            case 0:
                let data = try await reader.bytes(w * h * 4)
                framebufferLock.withLock { data.withUnsafeBytes { framebuffer.raw(x: x, y: y, w: w, h: h, $0) } }
            case 1:
                let sx = try await reader.u16(), sy = try await reader.u16()
                framebufferLock.withLock { framebuffer.copy(x: x, y: y, w: w, h: h, from: sx, sy) }
            case 16:
                let compressed = try await reader.bytes(Int(try await reader.u32()))
                let tiles = try zrle.inflate(compressed)
                try framebufferLock.withLock { try zrle.draw(tiles, x: x, y: y, w: w, h: h, into: framebuffer) }
            case -223:
                resize(width: w, height: h)
                onResize(w, h)
                dirty = CGRect(x: 0, y: 0, width: w, height: h)
                continue
            case -308:
                // ExtendedDesktopSize: x is the reason, y the status, w × h the framebuffer; then the screens. A
                // failed status answers a client's resize request, which this client never sends.
                let screens = try await extendedScreens()
                guard y == 0 else { continue }
                if w != framebuffer.width || h != framebuffer.height {
                    resize(width: w, height: h)
                    onResize(w, h)
                    dirty = CGRect(x: 0, y: 0, width: w, height: h)
                }
                onScreens(screens)
                continue
            case -239:
                let pixels = try await reader.bytes(w * h * 4), mask = try await reader.bytes((w + 7) / 8 * h)
                onCursor(Self.cursor(width: w, height: h, hotX: x, hotY: y, pixels: pixels, mask: mask))
                continue
            case -224:
                break
            default:
                throw Failure.protocolError(String(localized: "Unknown encoding \(encoding)"))
            }
            if encoding == -224 { break }
            let r = CGRect(x: x, y: y, width: w, height: h)
            dirty = dirty.union(r)
            framebufferLock.withLock { damage.add(r) }
        }
        if !dirty.isNull { onFrame(framebuffer, dirty) }
        try await request(incremental: true)
    }

    private func resize(width: Int, height: Int) {
        framebufferLock.withLock {
            framebuffer.resize(width: width, height: height)
            damage = RFBRegion(CGRect(x: 0, y: 0, width: width, height: height))
        }
    }

    /// ExtendedDesktopSize's screens: a count, three bytes of padding, then id, x, y, width, height and flags for each.
    private func extendedScreens() async throws -> [Screen] {
        let count = Int(try await reader.u8())
        _ = try await reader.bytes(3)
        var screens: [Screen] = []
        for _ in 0..<count {
            let id = try await reader.u32()
            let x = try await reader.u16(), y = try await reader.u16(), w = try await reader.u16(), h = try await reader.u16()
            _ = try await reader.u32() // flags
            screens.append(Screen(id: id, frame: CGRect(x: x, y: y, width: w, height: h)))
        }
        return screens
    }

    /// The cursor's pixels, see-through where its mask says.
    static func cursor(width: Int, height: Int, hotX: Int, hotY: Int, pixels: [UInt8], mask: [UInt8]) -> Cursor {
        var out = pixels
        let row = (width + 7) / 8
        for y in 0..<height {
            for x in 0..<width where mask[y * row + x / 8] & (0x80 >> UInt8(x % 8)) == 0 {
                out[(y * width + x) * 4 + 3] = 0
            }
            for x in 0..<width where mask[y * row + x / 8] & (0x80 >> UInt8(x % 8)) != 0 {
                out[(y * width + x) * 4 + 3] = 255
            }
        }
        return Cursor(width: width, height: height, hotX: hotX, hotY: hotY, pixels: out)
    }

    private static func describe(_ error: Error) -> String {
        switch error {
        case Failure.protocolError(let s): return s
        case Failure.noCredentials: return String(localized: "Not signed in to this Mac's Screen Sharing")
        case let e as URLError where e.code == .badServerResponse: return String(localized: "The hub won't connect to this screen (the extension is off, or this device can view but not control)")
        // What the network says in its own words ("Socket is not connected") when the Mac or the hub hangs up, or
        // nothing answers: said plainly (the page, the small window's card).
        case let e as URLError where e.code == .timedOut: return String(localized: "Connection timed out")
        case let e as URLError where [.cannotConnectToHost, .cannotFindHost, .notConnectedToInternet].contains(e.code): return String(localized: "Can't reach the hub")
        case let e as URLError where e.code == .networkConnectionLost: return String(localized: "Connection lost")
        case let e as NSError where e.domain == NSPOSIXErrorDomain && [ENOTCONN, ECONNRESET, EPIPE].contains(Int32(e.code)): return String(localized: "Connection lost")
        default: return error.localizedDescription
        }
    }
}
