import Foundation
import Observation
import PersonaSSH
#if canImport(UIKit)
import UIKit
#endif

/// This device's SSH key (docs/operations/network.md「Connecting to hub through SSH」): one Ed25519 key per device, made the first time it is
/// needed, its private half only in this device's keychain (never synced, never restored onto another device, never
/// logged).
enum SSHKeys {
    static let account = "ssh.identity"

    struct Unavailable: LocalizedError {
        var errorDescription: String? { String(localized: "Can't read this device's SSH key right now (unlock the device and try again)") }
    }

    /// The key, made and saved when there is none yet. A keychain that cannot be read now (before the first unlock)
    /// is not "no key": nothing is made over it.
    static func identity() throws -> SSHIdentity {
        #if DEBUG
        // `-seedSSHKey <base64 of a throwaway key's 32-byte seed>`: end-to-end runs that authorize the key up front (the
        // Mac's snapshot runs read no keychain).
        if let s = UserDefaults.standard.string(forKey: "seedSSHKey"), let raw = Data(base64Encoded: s),
           let id = try? SSHIdentity(rawRepresentation: raw) { return id }
        #endif
        switch Keychain.read(account: account) {
        case .found(let data):
            if let id = try? SSHIdentity(rawRepresentation: data) { return id }
        case .failed:
            throw Unavailable()
        case .missing:
            break
        }
        let id = SSHIdentity.generate()
        guard Keychain.save(id.rawRepresentation, account: account, thisDeviceOnly: true) else { throw Unavailable() }
        return id
    }

    /// `persona-<device name>`: the comment on the public key, so the server's authorized_keys says whose it is.
    @MainActor static var comment: String { "persona-" + UIDevice.current.name }
}

/// The SSH tunnels the app runs, one per paired hub that needs its own (docs/operations/network.md「Connecting to hub through SSH」). A tunnel
/// that is up routes its hub's requests through it (HubTransport); closing it sends them straight to the hub again.
@MainActor @Observable
final class SSHTunnels {
    static let shared = SSHTunnels()

    enum State: Equatable {
        case starting
        /// Carrying the hub's traffic.
        case up
        /// Not up, and why (for the connection details and Settings › hub › SSH Tunnel).
        case down(String)
    }

    /// By paired hub id ("pairing": the hub being paired).
    private(set) var states: [String: State] = [:]
    /// The paired hubs whose requests go through their tunnel right now.
    private(set) var routed: Set<String> = []
    /// The hubs that went through their tunnel last time, kept while the app was in the background: back in front,
    /// their tunnel is started while the direct way is asked, not after it timed out.
    @ObservationIgnored private(set) var recent: Set<String> = []

    private struct Open {
        var route: SSHRoute
        var hub: URL
        var tunnel: SSHTunnel
    }
    @ObservationIgnored private var tunnels: [String: Open] = [:]
    @ObservationIgnored private var starting: [String: Task<URL, Error>] = [:]
    /// The loopback port each hub's tunnel had: the next one takes it again when it is free, so addresses handed out
    /// (a page open in the in-app browser) stay good.
    @ObservationIgnored private var ports: [String: Int] = [:]
    @ObservationIgnored private var directWatch: [String: Task<Void, Never>] = [:]
    /// Moved on for a hub by every close of it: a start still waiting for its in-app tunnel relay when its hub was
    /// closed gives the relay back instead of dialling.
    @ObservationIgnored private var closes: [String: Int] = [:]

    static let pairingID = "pairing"

    /// The tunnel for a hub, up: the one that is there when it goes to the same place, else a new one. Routes the
    /// hub's requests through it. `carrier`: the in-app route whose core dials the server, for a route set to go
    /// through the in-app tunnel (`SSHRoute.viaInAppTunnel`).
    @discardableResult
    func open(_ id: String, route: SSHRoute, hub: URL, carrier: InAppTunnelRoute? = nil) async throws -> URL {
        if let t = tunnels[id], t.route == route, t.hub == hub, let url = t.tunnel.localURL, !Self.needsUser(t.tunnel.phase) {
            // The core's tunnel up again when the network ended it; the relay's port stays the one the tunnel dials.
            if route.viaInAppTunnel { _ = try await relay(id, route, carrier) }
            HubTransport.route(hub, via: url)
            routed.insert(id)
            return url
        }
        if let pending = starting[id] { return try await pending.value }
        let task = Task { try await self.start(id, route: route, hub: hub, carrier: carrier) }
        starting[id] = task
        defer { starting[id] = nil }
        return try await task.value
    }

    private func start(_ id: String, route: SSHRoute, hub: URL, carrier: InAppTunnelRoute?) async throws -> URL {
        close(id)
        let identity = try SSHKeys.identity()
        var config = route.tunnelConfig
        if route.viaInAppTunnel {
            states[id] = .starting
            let epoch = closes[id, default: 0]
            let port: Int
            do { port = try await relay(id, route, carrier) } catch {
                if closes[id, default: 0] == epoch { states[id] = .down(error.localizedDescription) }
                throw error
            }
            guard closes[id, default: 0] == epoch else {
                InAppTunnels.shared.releaseRelay(id)
                throw CancellationError()
            }
            config.host = "127.0.0.1"
            config.port = port
        }
        // The key stays pinned for the server in `route`, whatever address is dialled.
        let tunnel = SSHTunnel(config: config, identity: identity, pinnedHostKey: route.hostKey)
        tunnel.onPhase = { [weak self, weak tunnel] phase in
            Task { @MainActor in
                // Looked up by the tunnel, not `id`: `adopt` moves it under the paired hub's id after pairing.
                guard let self, let tunnel, let key = Self.key(of: tunnel, in: self.tunnels, \.tunnel) else { return }
                switch phase {
                case .connected: self.states[key] = .up
                case .failed(let e): self.states[key] = .down(e.localizedDescription)
                case .connecting, .idle: break // dialling again on the next request: still the way the hub is reached
                }
            }
        }
        tunnels[id] = Open(route: route, hub: hub, tunnel: tunnel)
        states[id] = .starting
        do {
            let port = try await tunnel.start(preferring: ports[id] ?? 0)
            guard tunnels[id]?.tunnel === tunnel, let url = tunnel.localURL else {
                tunnel.stop()
                throw CancellationError()
            }
            ports[id] = port
            HubTransport.route(hub, via: url)
            routed.insert(id)
            recent.insert(id)
            states[id] = .up
            return url
        } catch {
            if tunnels[id]?.tunnel === tunnel {
                tunnels[id] = nil
                states[id] = .down(error.localizedDescription)
                InAppTunnels.shared.releaseRelay(id)
            }
            tunnel.stop()
            throw error
        }
    }

    /// The hub's requests go straight to it again; its tunnel closes.
    func close(_ id: String) {
        close(id, forget: true)
    }

    /// `forget`: no longer the way to start first next time (`recent`).
    func close(_ id: String, forget: Bool) {
        closes[id, default: 0] += 1
        InAppTunnels.shared.releaseRelay(id)
        directWatch.removeValue(forKey: id)?.cancel()
        routed.remove(id)
        if forget { recent.remove(id) }
        guard let t = tunnels.removeValue(forKey: id) else { states[id] = nil; return }
        t.tunnel.stop()
        if let url = t.tunnel.localURL { HubTransport.unroute(t.hub, from: url) }
        states[id] = nil
    }

    /// Every tunnel closes (the app went to the background on the phone: iOS would cut them anyway; back in front,
    /// each group's socket picks its way again).
    func closeAll() {
        for id in Set(tunnels.keys).union(routed).union(starting.keys) { close(id, forget: false) }
        HubTransport.clearAll()
    }

    /// The id a tunnel is kept under now: the one it was started with, or the paired hub's once `adopt` moved it (#2379:
    /// its phase was written under the old id after the move, so the reason it went down never showed).
    nonisolated static func key<V, T: AnyObject>(of tunnel: T, in table: [String: V], _ get: (V) -> T) -> String? {
        table.first { get($0.value) === tunnel }?.key
    }

    /// The hub just paired keeps the tunnel it was paired through, under its own id.
    func adopt(_ from: String, as id: String) {
        guard from != id, let t = tunnels.removeValue(forKey: from) else { return }
        close(id)
        tunnels[id] = t
        states[id] = states.removeValue(forKey: from)
        if routed.remove(from) != nil { routed.insert(id) }
        if recent.remove(from) != nil { recent.insert(id) }
        ports[id] = ports.removeValue(forKey: from)
    }

    /// The server's key, after connecting, logging in and opening the hub's address once with `route` (「测试连接」).
    /// Not pinned yet: fails with `.hostKeyUnknown` carrying it, for the user to confirm.
    func verify(_ route: SSHRoute, carrier: InAppTunnelRoute? = nil) async throws -> (fingerprint: String, algorithm: String) {
        let identity = try SSHKeys.identity()
        guard route.viaInAppTunnel else {
            return try await SSHTunnel(config: route.tunnelConfig, identity: identity, pinnedHostKey: route.hostKey).verify()
        }
        let id = "verify." + UUID().uuidString
        defer { InAppTunnels.shared.releaseRelay(id) }
        var config = route.tunnelConfig
        config.host = "127.0.0.1"
        config.port = try await relay(id, route, carrier)
        return try await SSHTunnel(config: config, identity: identity, pinnedHostKey: route.hostKey).verify()
    }

    /// The loopback port the in-app tunnel core forwards to the route's SSH server.
    private func relay(_ id: String, _ route: SSHRoute, _ carrier: InAppTunnelRoute?) async throws -> Int {
        guard InAppTunnel.available else { throw InAppTunnel.missing }
        guard let carrier else { throw InAppTunnel.Failure(message: String(localized: "No paired hub has an in-app tunnel to reach the SSH server through")) }
        return try await InAppTunnels.shared.relay(id, to: route.server, carrier: carrier.sshProbe)
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

    private static func needsUser(_ phase: SSHTunnel.Phase) -> Bool {
        if case .failed(let e) = phase { return e.needsUser }
        return false
    }
}

extension SSHRoute {
    var tunnelConfig: SSHTunnelConfig {
        SSHTunnelConfig(host: host.trimmingCharacters(in: .whitespaces), port: port, user: user.trimmingCharacters(in: .whitespaces),
                        targetHost: targetHost.trimmingCharacters(in: .whitespaces), targetPort: targetPort)
    }
}

extension AppModel {
    // MARK: SSH tunnel (docs/operations/network.md「Connecting to hub through SSH」)

    /// Settings › hub › SSH Tunnel saved (nil: removed): the hub's tunnel is made again from it, and the group's socket picks
    /// its way again.
    func setSSH(_ route: SSHRoute?, hub id: String) {
        guard let hub = registry.hubs.first(where: { $0.id == id }), hub.ssh != route else { return }
        updateHub(id) { $0.ssh = route }
        SSHTunnels.shared.close(id)
        guard let link = link(hub.group), !link.isDemo else { return }
        link.live.route = routePicker(hub.group)
        link.live.disconnect()
        link.live.connect(link.fallback)
    }

    /// The paired hub a group's socket is on, and whether it goes through its tunnel: what the connection details and
    /// Settings › hub say.
    func tunnel(_ group: String) -> (hub: PairedHub, routed: Bool, state: SSHTunnels.State?)? {
        guard let link = link(group), let on = link.live.currentConfig,
              let hub = registry.members(group).first(where: { $0.configs.contains(on) }), let route = hub.ssh, route.isUsable else { return nil }
        let tunnels = SSHTunnels.shared
        return (hub, tunnels.routed.contains(hub.id), tunnels.states[hub.id])
    }
}
