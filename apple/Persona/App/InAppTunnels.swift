import Foundation
import Observation

/// The in-app tunnels the app runs, one per paired hub that has one (docs/operations/network.md「Connecting to hub through an in-app tunnel」): the
/// core's tunnel up, and a loopback forward to the hub's address inside the tunnel's network. A tunnel that is up
/// routes its hub's requests through it (HubTransport), as SSHTunnels does; closing the last one takes the core's
/// tunnel down again, so it is up only while the app needs it.
@MainActor @Observable
final class InAppTunnels {
    static let shared = InAppTunnels()

    enum State: Equatable {
        case starting
        /// Carrying the hub's traffic.
        case up
        /// Not up, and why (for the connection details and the tunnel's page in Settings › hub).
        case down(String)
    }

    /// By paired hub id.
    private(set) var states: [String: State] = [:]
    /// The paired hubs whose requests go through their tunnel right now.
    private(set) var routed: Set<String> = []
    /// The hubs that went through their tunnel last time, kept while the app was in the background: back in front,
    /// their tunnel is started while the direct way is asked, not after it timed out.
    @ObservationIgnored private(set) var recent: Set<String> = []

    private struct Open {
        var route: InAppTunnelRoute
        var hub: URL
        var local: URL
    }
    @ObservationIgnored private var tunnels: [String: Open] = [:]
    private struct Starting {
        var epoch: Int
        var route: InAppTunnelRoute
        var hub: URL
        var task: Task<URL, Error>
    }
    @ObservationIgnored private var starting: [String: Starting] = [:]
    /// Moved on for a hub by every close of it (and for all by closeAll): a start still under way when its hub was
    /// closed, removed or set up anew never routes afterwards.
    @ObservationIgnored private var epochs: [String: Int] = [:]
    /// Starts under way, the ones a closed hub left behind included: the core's tunnel stays up while there are any.
    @ObservationIgnored private var inFlight = 0
    /// The loopback port each hub's forward had: the next one takes it again when it is free, so addresses handed out
    /// (a page open in the in-app browser) stay good.
    @ObservationIgnored private var ports: [String: Int] = [:]
    @ObservationIgnored private var directWatch: [String: Task<Void, Never>] = [:]
    /// SSH tunnels that dial their server through the core, by paired hub id: the server (`host:port`) and the
    /// loopback port of its forward (`relay`).
    @ObservationIgnored private var relays: [String: String] = [:]
    @ObservationIgnored private var relayPorts: [String: Int] = [:]
    /// Closing forwards and taking the core's tunnel down, in order: a start waits for it, so a disconnect that was
    /// still on its way never takes down the tunnel just brought up.
    @ObservationIgnored private var cleanup: Task<Void, Never>?

    /// How long bringing the tunnel up may take (signing in to the node, the handshake, a second node).
    static let connectTimeout = 30

    /// The tunnel for a hub, up: the one that is there when it goes to the same place, else a new one. Routes the
    /// hub's requests through it.
    @discardableResult
    func open(_ id: String, route: InAppTunnelRoute, hub: URL) async throws -> URL {
        if let s = starting[id] {
            if s.epoch == epochs[id, default: 0], s.route == route, s.hub == hub { return try await s.task.value }
            // A start to somewhere else is under way: this one takes its place.
            epochs[id, default: 0] += 1
        }
        let epoch = epochs[id, default: 0]
        let task = Task { try await self.start(id, route: route, hub: hub, epoch: epoch) }
        starting[id] = Starting(epoch: epoch, route: route, hub: hub, task: task)
        defer { if starting[id]?.epoch == epoch, starting[id]?.route == route { starting[id] = nil } }
        return try await task.value
    }

    private func start(_ id: String, route: InAppTunnelRoute, hub: URL, epoch: Int) async throws -> URL {
        inFlight += 1
        defer { inFlight -= 1 }
        if let t = tunnels[id], t.route != route || t.hub != hub { close(id) }
        // close(id) above moved the epoch on: this start is the hub's new one.
        let epoch = max(epoch, epochs[id, default: 0])
        epochs[id] = epoch
        let current = { self.epochs[id, default: 0] == epoch }
        if tunnels[id] == nil { states[id] = .starting }
        var forwarded = false
        do {
            await cleanup?.value
            guard current() else { throw CancellationError() }
            // Kept as it is when it is up already; brought up again when a node change or the server ended it.
            try await InAppTunnel.connect(route, timeout: Self.connectTimeout)
            guard current() else { throw CancellationError() }
            let local = try await InAppTunnel.forward(route.target, port: ports[id] ?? 0, probe: route)
            forwarded = true
            guard current() else { throw CancellationError() }
            if let p = local.port { ports[id] = p }
            tunnels[id] = Open(route: route, hub: hub, local: local)
            HubTransport.route(hub, via: local)
            routed.insert(id)
            recent.insert(id)
            states[id] = .up
            return local
        } catch {
            if current(), tunnels[id]?.route == route, tunnels[id]?.hub == hub {
                // Up before (a reopen whose connect failed): it is not the way to the hub any more.
                if let local = tunnels.removeValue(forKey: id)?.local { HubTransport.unroute(hub, from: local) }
                routed.remove(id)
            }
            if current(), !(error is CancellationError) { states[id] = .down(error.localizedDescription) }
            let target = route.target
            let unused = forwarded && !inUse(target)
            enqueue { if unused { await InAppTunnel.stopForward(target) } }
            disconnectWhenUnused()
            throw error
        }
    }

    /// The hub's requests go straight to it again; its forward closes, and the core's tunnel with the last one.
    func close(_ id: String) {
        close(id, forget: true)
    }

    /// `forget`: no longer the way to start first next time (`recent`).
    func close(_ id: String, forget: Bool) {
        epochs[id, default: 0] += 1
        starting[id] = nil
        directWatch.removeValue(forKey: id)?.cancel()
        routed.remove(id)
        if forget { recent.remove(id) }
        states[id] = nil
        if let t = tunnels.removeValue(forKey: id) {
            HubTransport.unroute(t.hub, from: t.local)
            let target = t.route.target
            let shared = inUse(target)
            enqueue { if !shared { await InAppTunnel.stopForward(target) } }
        }
        disconnectWhenUnused()
    }

    /// A forward through the core to an SSH server for that hub's SSH tunnel (`SSHRoute.viaInAppTunnel`): the core's
    /// tunnel up through `carrier` (a hub's own in-app route: its probe, its sign-in), then a loopback forward to
    /// `server`; its port, the same one again while it is kept. The core's tunnel stays up while any such forward is
    /// kept, and the hub's own in-app tunnel coming or going leaves it alone. Released by `releaseRelay`.
    func relay(_ id: String, to server: String, carrier: InAppTunnelRoute) async throws -> Int {
        inFlight += 1
        defer {
            inFlight -= 1
            disconnectWhenUnused()
        }
        await cleanup?.value
        // Kept as it is when it is up already; brought up again when the network or the server ended it.
        try await InAppTunnel.connect(carrier, timeout: Self.connectTimeout)
        if relays[id] == server, let p = relayPorts[id] { return p }
        let local = try await InAppTunnel.forward(server, port: relayPorts[id] ?? 0, probe: carrier)
        guard let p = local.port else { throw InAppTunnel.Failure(message: String(localized: "The tunnel core returned an invalid address")) }
        if relays[id] != nil { releaseRelay(id) }
        relays[id] = server
        relayPorts[id] = p
        return p
    }

    /// The SSH tunnel of hub `id` no longer needs its forward: it closes unless something else goes to the same
    /// server, and the core's tunnel with the last user.
    func releaseRelay(_ id: String) {
        guard let server = relays.removeValue(forKey: id) else { return }
        let shared = inUse(server)
        enqueue { if !shared { await InAppTunnel.stopForward(server) } }
        disconnectWhenUnused()
    }

    /// A forward to `target` is still kept by a hub's in-app tunnel or an SSH tunnel's relay.
    private func inUse(_ target: String) -> Bool {
        tunnels.values.contains { $0.route.target == target } || relays.values.contains(target)
    }

    /// Every tunnel closes and the core's tunnel goes down (the app went to the background on the phone: iOS would
    /// cut it anyway; back in front, each group's socket picks its way again).
    func closeAll() {
        for id in Set(tunnels.keys).union(routed).union(starting.keys).union(epochs.keys) { close(id, forget: false) }
    }

    /// The core's tunnel down once nothing uses it: no tunnel open and no start under way when the turn comes (a start
    /// that gives up queues this again itself).
    private func disconnectWhenUnused() {
        enqueue { @MainActor [weak self] in
            guard let self, self.tunnels.isEmpty, self.relays.isEmpty, self.inFlight == 0 else { return }
            await InAppTunnel.disconnect()
        }
    }

    private func enqueue(_ op: @escaping @MainActor () async -> Void) {
        let previous = cleanup
        cleanup = Task { @MainActor in
            await previous?.value
            await op()
        }
    }

    /// While a group goes through a tunnel only because the hub's own addresses did not answer: ask them again every
    /// so often and move back once one does (`answers`; `moveBack` reconnects, and picking the way again closes the
    /// tunnel).
    func watchDirect(_ id: String, every: Duration = .seconds(120), answers: @escaping @MainActor () async -> Bool,
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

    /// The hub answers through `route` (「测试连接」): the tunnel up, a forward to the hub's address, and its
    /// health check through it. The forward is left for the tunnel that uses it next; the core's tunnel goes down
    /// again unless a tunnel or a start uses it by then.
    func verify(_ route: InAppTunnelRoute) async throws {
        inFlight += 1
        defer {
            inFlight -= 1
            disconnectWhenUnused()
        }
        await cleanup?.value
        try await InAppTunnel.connect(route, timeout: Self.connectTimeout)
        let local = try await InAppTunnel.forward(route.target, port: 0, probe: route)
        var req = URLRequest(url: local.appending(path: "api/v1/health"), timeoutInterval: 10)
        req.httpMethod = "GET"
        RequestClientMetadata.current.apply(to: &req)
        let (data, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200,
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any], obj["ok"] as? Bool == true else {
            throw InAppTunnel.Failure(message: String(localized: "Reached \(route.target), but it doesn't look like a hub (/api/v1/health didn't answer properly)"))
        }
    }
}

extension AppModel {
    // MARK: In-app tunnel (docs/operations/network.md「Connecting to hub through an in-app tunnel」)

    /// After a group's hello: the paired hub the socket is on takes its in-app tunnel from the hub's extension that
    /// contributes one (docs/extensions/extensions.md「In-App tunnels」), kept for the times its own addresses do not answer (when the
    /// tunnel is needed, the hub cannot be asked). No such extension switched on: the tunnel goes. A request that
    /// fails, or a device that may not read the extension's settings, leaves what was there.
    func refreshInAppTunnel(_ group: String) async {
        guard let link = link(group), !link.isDemo, link.features.contains("extensions"), let on = link.live.currentConfig,
              let hub = registry.members(group).first(where: { $0.configs.contains(on) }) else { return }
        let api = link.api
        guard let list = try? await api.plugins() else { return }
        guard let id = InAppTunnelRoute.extensionOn(list) else {
            setInAppTunnel(nil, hub: hub.id)
            return
        }
        guard let detail = try? await api.plugin(id) else { return }
        let always = registry.hubs.first(where: { $0.id == hub.id })?.inAppTunnel?.always ?? false
        setInAppTunnel(InAppTunnelRoute(extension: detail, always: always), hub: hub.id)
    }

    /// The hub's in-app tunnel changed (nil: removed): the hub's tunnel is made again from it, and the group's socket
    /// picks its way again. Only its name or the switch 「总是走隧道」 changing keeps the tunnel, if it is up, as it is.
    func setInAppTunnel(_ route: InAppTunnelRoute?, hub id: String) {
        guard let hub = registry.hubs.first(where: { $0.id == id }), hub.inAppTunnel != route else { return }
        updateHub(id) { $0.inAppTunnel = route }
        let sameTarget = hub.inAppTunnel != nil && hub.inAppTunnel?.target == route?.target
        if sameTarget, route?.always == hub.inAppTunnel?.always || route?.always == false || InAppTunnels.shared.routed.contains(id) {
            guard let link = link(hub.group), !link.isDemo else { return }
            link.live.route = routePicker(hub.group)
            return
        }
        InAppTunnels.shared.close(id)
        guard let link = link(hub.group), !link.isDemo else { return }
        link.live.route = routePicker(hub.group)
        link.live.disconnect()
        link.live.connect(link.fallback)
    }

    /// The paired hub a group's socket is on, its tunnel, and whether the socket goes through it: what the connection
    /// details and Settings › hub say.
    func inAppTunnelState(_ group: String) -> (hub: PairedHub, route: InAppTunnelRoute, routed: Bool, state: InAppTunnels.State?)? {
        guard let link = link(group), let on = link.live.currentConfig,
              let hub = registry.members(group).first(where: { $0.configs.contains(on) }), let route = hub.inAppTunnel, route.isUsable else { return nil }
        let tunnels = InAppTunnels.shared
        return (hub, route, tunnels.routed.contains(hub.id), tunnels.states[hub.id])
    }
}

extension InAppTunnelRoute {
    /// The first extension the hub lists, switched on, that contributes a tunnel; its id.
    nonisolated static func extensionOn(_ extensions: [HubExtension]) -> String? {
        extensions.first { $0.enabled != false && !($0.contributes?["tunnels"] ?? []).isEmpty }?.id
    }

    /// The tunnel an extension's detail says: its first tunnel contribution, switched on, gives the name, and its
    /// settings (`target_host`, `target_port`, optional `ssh_port`) where the tunnel goes; with this phone's 「总是走隧道」. Nil when it has
    /// no tunnel switched on or names no host.
    nonisolated init?(extension detail: HubExtensionDetail, always: Bool) {
        let tunnels = (detail.contributions ?? []).filter { $0.point == "tunnels" }
        if !tunnels.isEmpty, !tunnels.contains(where: \.enabled) { return nil }
        guard var route = InAppTunnelRoute(extensionConfig: detail.config, always: always) else { return nil }
        route.name = tunnels.first(where: \.enabled)?.title?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        self = route
    }

    /// The tunnel the extension's settings say (`target_host`, `target_port`); nil when they name no host.
    nonisolated init?(extensionConfig config: [String: JSONValue], always: Bool) {
        guard case .string(let raw)? = config["target_host"] else { return nil }
        let host = raw.trimmingCharacters(in: .whitespaces)
        guard !host.isEmpty else { return nil }
        var port = Self.defaultTargetPort
        if case .number(let n)? = config["target_port"], let p = Int(exactly: n) { port = p }
        var ssh: Int?
        if case .number(let n)? = config["ssh_port"], let p = Int(exactly: n), (1...65535).contains(p) { ssh = p }
        self.init(targetHost: host, targetPort: port, always: always, sshPort: ssh)
    }
}
