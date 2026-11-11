import Foundation
import Network
import Observation

/// A loopback listener that carries every connection made to it through a SOCKS5 proxy to the hub
/// (docs/operations/network.md "Connecting to hub through a local SOCKS5 proxy"): each connection dials the proxy, has it
/// CONNECT to the hub's address, then the bytes go both ways untouched, HTTP and WebSocket alike (as the SSH tunnel's
/// listener does). Nothing is read from a connection until its way to the hub is there, so no request bytes are lost.
nonisolated final class LocalProxyForward: @unchecked Sendable {
    /// Where each connection goes, and how.
    struct Dial: Hashable, Sendable {
        var proxyHost: String
        var proxyPort: Int
        var targetHost: String
        var targetPort: Int
        var username: String?
        var password: String = ""

        func handshake() throws -> SOCKS5Handshake {
            try SOCKS5Handshake(host: targetHost, port: targetPort, login: username.map { (username: $0, password: password) })
        }

        /// `host:port` of the hub as the proxy is asked for it (IPv6 in brackets).
        var target: String { (targetHost.contains(":") ? "[\(targetHost)]" : targetHost) + ":\(targetPort)" }
    }

    let dial: Dial
    private let queue = DispatchQueue(label: "persona.local-proxy")
    private let lock = NSLock()
    private var listener: NWListener?
    private var port = 0
    private var stopped = false
    private var pipes: [ObjectIdentifier: ProxyPipe] = [:]
    /// Told how each connection's dial went (nil: it got through), on any thread. Set before `listen`.
    var onDial: (@Sendable (SOCKS5Error?) -> Void)?

    init(dial: Dial) {
        self.dial = dial
    }

    /// The loopback port it listens on (0 before `listen`).
    var localPort: Int { lock.withLock { port } }
    /// `http://127.0.0.1:<port>`: the hub's address through the proxy.
    var localURL: URL? { localPort == 0 ? nil : URL(string: "http://127.0.0.1:\(localPort)") }
    var isListening: Bool { lock.withLock { listener?.state == .ready } }
    /// Connections carried through the proxy right now.
    var openConnections: Int { lock.withLock { pipes.count } }

    /// One connection through the proxy to the hub's address, closed again: a proxy that is not running, or a login it
    /// refuses, fails here, not on the first request. Whether the hub answers there it does not say: many proxies
    /// (ssh -D, mihomo, Xray) accept the CONNECT before they try the address.
    func check() async throws {
        let conn = try await SOCKS5Dial.connect(host: dial.proxyHost, port: dial.proxyPort, handshake: try dial.handshake(), queue: queue)
        conn.cancel()
    }

    /// Listens on 127.0.0.1: at `preferred` when it is free (the port the last forward had, so addresses handed out
    /// stay good), else on any free one. Already listening: that port.
    @discardableResult
    func listen(preferring preferred: Int) async throws -> Int {
        if isListening { return localPort }
        let l: NWListener
        do {
            l = try await bind(preferred)
        } catch where preferred != 0 {
            l = try await bind(0)
        }
        let p = Int(l.port?.rawValue ?? 0)
        let old: NWListener? = try lock.withLock {
            guard !stopped else {
                l.cancel()
                throw CancellationError()
            }
            defer {
                listener = l
                port = p
            }
            return listener
        }
        old?.cancel()
        return p
    }

    /// The listener closes and every connection through it is cut.
    func stop() {
        let (l, open) = lock.withLock { () -> (NWListener?, [ProxyPipe]) in
            stopped = true
            defer {
                listener = nil
                pipes = [:]
            }
            return (listener, Array(pipes.values))
        }
        l?.cancel()
        open.forEach { $0.close() }
    }

    private func bind(_ port: Int) async throws -> NWListener {
        let params = NWParameters.tcp
        params.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: NWEndpoint.Port(rawValue: UInt16(port)) ?? .any)
        params.allowLocalEndpointReuse = true
        let l = try NWListener(using: params)
        l.newConnectionHandler = { @Sendable [weak self] conn in self?.accept(conn) }
        return try await withCheckedThrowingContinuation { (cont: CheckedContinuation<NWListener, Error>) in
            let once = RunOnce()
            l.stateUpdateHandler = { @Sendable state in
                switch state {
                case .ready: once.run { cont.resume(returning: l) }
                case .failed(let e), .waiting(let e):
                    once.run { cont.resume(throwing: e) }
                    l.cancel()
                case .cancelled: once.run { cont.resume(throwing: CancellationError()) }
                default: break
                }
            }
            l.start(queue: queue)
        }
    }

    private func accept(_ local: NWConnection) {
        guard !lock.withLock({ stopped }) else {
            local.cancel()
            return
        }
        local.start(queue: queue)
        let dial = dial, queue = queue
        Task { @Sendable [weak self] in
            do {
                let remote = try await SOCKS5Dial.connect(host: dial.proxyHost, port: dial.proxyPort, handshake: try dial.handshake(), queue: queue)
                self?.onDial?(nil)
                let pipe = ProxyPipe(local: local, remote: remote) { [weak self] p in self?.drop(p) }
                guard let self, self.add(pipe) else {
                    pipe.close()
                    return
                }
                pipe.start()
            } catch {
                self?.onDial?(error as? SOCKS5Error ?? .closed)
                local.cancel()
            }
        }
    }

    private func add(_ pipe: ProxyPipe) -> Bool {
        lock.withLock {
            guard !stopped else { return false }
            pipes[ObjectIdentifier(pipe)] = pipe
            return true
        }
    }

    private func drop(_ pipe: ProxyPipe) {
        lock.withLock { _ = pipes.removeValue(forKey: ObjectIdentifier(pipe)) }
    }
}

/// One connection made to the forward and its connection through the proxy: what one side reads the other writes, a
/// read waits until the last bytes went out (back pressure). One side ending its writing (a FIN) ends the other's;
/// both ended, or any error, and both connections go.
nonisolated private final class ProxyPipe: @unchecked Sendable {
    let local: NWConnection
    let remote: NWConnection
    private let onClose: @Sendable (ProxyPipe) -> Void
    private let lock = NSLock()
    private var ended = 0
    private var closed = false

    init(local: NWConnection, remote: NWConnection, onClose: @escaping @Sendable (ProxyPipe) -> Void) {
        self.local = local
        self.remote = remote
        self.onClose = onClose
    }

    func start() {
        let watch: @Sendable (NWConnection.State) -> Void = { [weak self] state in
            switch state {
            case .failed, .cancelled: self?.close()
            default: break
            }
        }
        local.stateUpdateHandler = watch
        remote.stateUpdateHandler = watch
        pump(from: local, to: remote)
        pump(from: remote, to: local)
    }

    private func pump(from: NWConnection, to: NWConnection) {
        from.receive(minimumIncompleteLength: 1, maximumLength: 64 << 10) { @Sendable [self] data, _, complete, error in
            if let data, !data.isEmpty {
                to.send(content: data, completion: .contentProcessed { @Sendable [self] err in
                    if err != nil { close() } else if complete { end(to) } else { pump(from: from, to: to) }
                })
            } else if error != nil {
                close()
            } else if complete {
                end(to)
            } else {
                pump(from: from, to: to)
            }
        }
    }

    /// One way is over (its sender closed its writing): the other side is told the same.
    private func end(_ to: NWConnection) {
        to.send(content: nil, contentContext: .finalMessage, isComplete: true, completion: .contentProcessed { @Sendable [self] _ in
            if lock.withLock({ ended += 1; return ended == 2 }) { close() }
        })
    }

    func close() {
        let first = lock.withLock { () -> Bool in
            defer { closed = true }
            return !closed
        }
        guard first else { return }
        local.cancel()
        remote.cancel()
        onClose(self)
    }
}

extension LocalProxyRoute {
    /// How the forward for `hub` dials: this proxy, its login, and the hub's address from there. Nonisolated like the rest
    /// of the route (`HubTransport.swift`): Swift 6.4 gives an extension's members here the file's main actor (6.2 did
    /// not), and refused the call from its tests (#1378).
    nonisolated func dial(for hub: URL) -> LocalProxyForward.Dial {
        LocalProxyForward.Dial(proxyHost: host.trimmingCharacters(in: .whitespaces).trimmingCharacters(in: CharacterSet(charactersIn: "[]")), proxyPort: port,
                               targetHost: targetHost(for: hub), targetPort: targetPort(for: hub),
                               username: login?.username, password: login?.password ?? "")
    }
}

/// The local proxies the app goes through, one forward per paired hub that needs its own (docs/operations/network.md
/// "Connecting to hub through a local SOCKS5 proxy"). A forward that is up routes its hub's requests through it
/// (HubTransport), as SSHTunnels does; closing it sends them straight to the hub again.
@MainActor @Observable
final class LocalProxies {
    static let shared = LocalProxies()

    enum State: Equatable {
        case starting
        /// Carrying the hub's traffic.
        case up
        /// Not up, and why (for the connection details and Settings › hub › Local Proxy).
        case down(String)
    }

    /// By paired hub id.
    private(set) var states: [String: State] = [:]
    /// The paired hubs whose requests go through their proxy right now.
    private(set) var routed: Set<String> = []
    /// The hubs that went through their proxy last time, kept while the app was in the background: back in front,
    /// their forward is started while the direct way is asked, not after it timed out.
    @ObservationIgnored private(set) var recent: Set<String> = []

    private struct Open {
        var route: LocalProxyRoute
        var hub: URL
        var forward: LocalProxyForward
    }
    @ObservationIgnored private var forwards: [String: Open] = [:]
    private struct Starting {
        var epoch: Int
        var route: LocalProxyRoute
        var hub: URL
        var task: Task<URL, Error>
    }
    @ObservationIgnored private var starting: [String: Starting] = [:]
    /// Moved on for a hub by every close of it: a start still under way when its hub was closed, or set up anew,
    /// never routes afterwards.
    @ObservationIgnored private var epochs: [String: Int] = [:]
    /// The loopback port each hub's forward had: the next one takes it again when it is free, so addresses handed out
    /// (a page open in the in-app browser) stay good.
    @ObservationIgnored private var ports: [String: Int] = [:]
    @ObservationIgnored private var directWatch: [String: Task<Void, Never>] = [:]

    /// The forward for a hub, up: the one there is when it goes the same way, else a new one; either way the proxy is
    /// asked for the hub once first, so a proxy that stopped working is not taken. Routes the hub's requests through it.
    @discardableResult
    func open(_ id: String, route: LocalProxyRoute, hub: URL) async throws -> URL {
        if let s = starting[id] {
            if s.epoch == epochs[id, default: 0], s.route == route, s.hub == hub { return try await s.task.value }
            // A start with other settings (saved just now) is under way: this one takes its place.
            epochs[id, default: 0] += 1
        }
        let epoch = epochs[id, default: 0]
        let task = Task { try await self.start(id, route: route, hub: hub, epoch: epoch) }
        starting[id] = Starting(epoch: epoch, route: route, hub: hub, task: task)
        defer { if starting[id]?.epoch == epoch { starting[id] = nil } }
        return try await task.value
    }

    private func start(_ id: String, route: LocalProxyRoute, hub: URL, epoch: Int) async throws -> URL {
        let current = { self.epochs[id, default: 0] == epoch }
        let forward: LocalProxyForward
        if let o = forwards[id], o.route == route, o.hub == hub {
            forward = o.forward
        } else {
            drop(id, forget: true)
            forward = LocalProxyForward(dial: route.dial(for: hub))
            forward.onDial = { @Sendable [weak self, weak forward] failure in
                Task { @MainActor in
                    guard let self, let forward, let key = self.forwards.first(where: { $0.value.forward === forward })?.key else { return }
                    let now = failure.map { State.down($0.localizedDescription) } ?? .up
                    if self.states[key] != now { self.states[key] = now }
                }
            }
            forwards[id] = Open(route: route, hub: hub, forward: forward)
            states[id] = .starting
        }
        do {
            try await forward.check()
            guard current() else { throw CancellationError() }
            let port = try await forward.listen(preferring: ports[id] ?? 0)
            guard current(), forwards[id]?.forward === forward, let url = forward.localURL else { throw CancellationError() }
            ports[id] = port
            HubTransport.route(hub, via: url)
            routed.insert(id)
            recent.insert(id)
            states[id] = .up
            return url
        } catch {
            if forwards[id]?.forward === forward {
                drop(id, forget: false)
                if current(), !(error is CancellationError) { states[id] = .down(error.localizedDescription) }
            } else {
                forward.stop()
            }
            throw error
        }
    }

    /// The hub's requests go straight to it again; its forward closes, and a start under way for it never routes.
    func close(_ id: String) {
        close(id, forget: true)
    }

    /// `forget`: no longer the way to start first next time (`recent`).
    func close(_ id: String, forget: Bool) {
        epochs[id, default: 0] += 1
        starting[id] = nil
        drop(id, forget: forget)
    }

    /// The hub's forward stops and its route goes (only its own: another way may have routed the hub since).
    private func drop(_ id: String, forget: Bool) {
        directWatch.removeValue(forKey: id)?.cancel()
        routed.remove(id)
        if forget { recent.remove(id) }
        states[id] = nil
        guard let o = forwards.removeValue(forKey: id) else { return }
        o.forward.stop()
        if let url = o.forward.localURL { HubTransport.unroute(o.hub, from: url) }
    }

    /// Every forward closes (the app went to the background on the phone: iOS would cut them anyway; back in front,
    /// each group's socket picks its way again).
    func closeAll() {
        for id in Set(forwards.keys).union(routed).union(starting.keys) { close(id, forget: false) }
    }

    /// Whether the direct way is being asked again for this hub (a group that went through its proxy only because
    /// the direct way did not answer).
    func watchesDirect(_ id: String) -> Bool { directWatch[id] != nil }

    /// While a group goes through a proxy only because the hub's own addresses did not answer: ask them again every
    /// so often and move back once one does (`answers`; `moveBack` reconnects, and picking the way again closes the
    /// forward).
    func watchDirect(_ id: String, every: Duration = HubWay.recheck, answers: @escaping @MainActor () async -> Bool,
                     moveBack: @escaping @MainActor () -> Void) {
        guard directWatch[id] == nil else { return }
        directWatch[id] = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: every)
                guard let self, !Task.isCancelled, self.routed.contains(id) else { break }
                if await answers() {
                    self.directWatch[id] = nil
                    moveBack()
                    return
                }
            }
            self?.directWatch[id] = nil
        }
    }

    /// The hub answers through `route` (Test Connection and Save): the proxy asked for the hub's address once, then
    /// the hub's health check through a forward of its own, which closes again. Kept only when it is this hub
    /// (`hubID`, once known) and it takes plain HTTP from the proxy's side: a hub that sees the proxy as the internet
    /// answers its health check all the same, then refuses every request.
    func verify(_ route: LocalProxyRoute, hub: URL, hubID: String? = nil) async throws {
        let forward = LocalProxyForward(dial: route.dial(for: hub))
        let dialFailure = DialFailure()
        forward.onDial = { @Sendable failure in if let failure { dialFailure.set(failure) } }
        defer { forward.stop() }
        try await forward.check()
        try await forward.listen(preferring: 0)
        let target = forward.dial.target
        guard let local = forward.localURL, let url = Self.healthURL(hub: hub, via: local) else { throw LocalProxyCheck.notAHub(target) }
        var req = URLRequest(url: url, timeoutInterval: 10)
        req.httpMethod = "GET"
        RequestClientMetadata.current.apply(to: &req)
        let data: Data, resp: URLResponse
        do {
            // A redirect is not followed: it would leave the proxy (an http port that sends to https).
            (data, resp) = try await APIClient.uncachedSession.data(for: req, delegate: NoRedirects())
        } catch {
            // The proxy said yes to the CONNECT, but many say so before they try the address.
            if let e = dialFailure.value { throw e }
            if let e = error as? URLError, [.badServerResponse, .cannotParseResponse].contains(e.code) { throw LocalProxyCheck.notAHub(target) }
            throw LocalProxyCheck.noAnswer(target)
        }
        guard (resp as? HTTPURLResponse)?.statusCode == 200,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any], obj["ok"] as? Bool == true else {
            throw LocalProxyCheck.notAHub(target)
        }
        if let hubID, !hubID.isEmpty, let answered = obj["hub_id"] as? String, !answered.isEmpty, answered != hubID {
            throw LocalProxyCheck.otherHub(target)
        }
        // The hub leaves its version out for a source it counts as the internet, where it refuses plain HTTP (TLS only).
        guard obj["version"] != nil else { throw LocalProxyCheck.seenAsInternet(target) }
    }

    /// The last dial that failed on Test Connection's own forward, from any thread.
    private nonisolated final class DialFailure: @unchecked Sendable {
        private let lock = NSLock()
        private var error: SOCKS5Error?
        var value: SOCKS5Error? { lock.withLock { error } }
        func set(_ e: SOCKS5Error) { lock.withLock { error = e } }
    }

    /// Answers a redirect with the redirect itself. The completion-handler form: the async one crashes the compiler
    /// (Xcode 27.1) emitting its Objective-C thunk (#1378, as `PushRelay`'s did).
    private nonisolated final class NoRedirects: NSObject, URLSessionTaskDelegate, Sendable {
        func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
                        newRequest request: URLRequest, completionHandler: @escaping @Sendable (URLRequest?) -> Void) {
            completionHandler(nil)
        }
    }

    /// `/api/v1/health` under the hub's address (its path kept, as every request through the forward has it), on the
    /// forward's loopback address.
    nonisolated static func healthURL(hub: URL, via local: URL) -> URL? {
        guard var comps = URLComponents(url: hub, resolvingAgainstBaseURL: false) else { return nil }
        var base = comps.percentEncodedPath
        if base.hasSuffix("/") { base.removeLast() }
        comps.percentEncodedPath = base + "/api/v1/health"
        comps.query = nil
        comps.fragment = nil
        comps.scheme = local.scheme
        comps.host = local.host()
        comps.port = local.port
        return comps.url
    }
}

/// What Test Connection found past the proxy, each with the address the proxy was asked for.
nonisolated enum LocalProxyCheck: LocalizedError, Equatable {
    /// Nothing answered through the proxy: it could not reach the address, or nothing listens there.
    case noAnswer(String)
    /// Something answered there, but not a hub.
    case notAHub(String)
    /// A hub answered, but another one than this device is paired with.
    case otherHub(String)
    /// The hub counts connections through the proxy as coming from the internet, and takes only TLS from there.
    case seenAsInternet(String)

    var errorDescription: String? {
        switch self {
        case .noAnswer(let target):
            String(localized: "Got no answer from \(target) through the proxy: the proxy may not reach that address, or the hub isn't listening there.")
        case .notAHub(let target):
            String(localized: "Reached \(target) through the proxy, but it doesn't look like a hub (/api/v1/health didn't answer properly). Check the hub's address and port.")
        case .otherHub(let target):
            String(localized: "\(target) is another hub, not the one this device is paired with. Check the hub's address.")
        case .seenAsInternet(let target):
            String(localized: "The hub at \(target) treats connections through the proxy as coming from the internet and refuses plain HTTP from there. Enter the hub's address inside its LAN or VPN.")
        }
    }
}

extension AppModel {
    // MARK: Local proxy (docs/operations/network.md "Connecting to hub through a local SOCKS5 proxy")

    /// Settings › hub › Local Proxy saved (nil: removed): the hub's forward is made again from it, and the group's
    /// socket picks its way again.
    func setLocalProxy(_ route: LocalProxyRoute?, hub id: String) {
        guard let hub = registry.hubs.first(where: { $0.id == id }), hub.localProxy != route else { return }
        updateHub(id) { $0.localProxy = route }
        LocalProxies.shared.close(id)
        guard let link = link(hub.group), !link.isDemo else { return }
        link.live.route = routePicker(hub.group)
        link.live.disconnect()
        link.live.connect(link.fallback)
    }

    /// The paired hub a group's socket is on, its proxy, and whether the socket goes through it: what the connection
    /// details and Settings › hub say.
    func localProxyState(_ group: String) -> (hub: PairedHub, route: LocalProxyRoute, routed: Bool, state: LocalProxies.State?)? {
        guard let link = link(group), let on = link.live.currentConfig,
              let hub = registry.members(group).first(where: { $0.configs.contains(on) }), let route = hub.localProxy, route.isUsable else { return nil }
        let proxies = LocalProxies.shared
        return (hub, route, proxies.routed.contains(hub.id), proxies.states[hub.id])
    }
}
