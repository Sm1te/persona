import Foundation
import Observation

/// The servers a session's Preview offers (`PreviewChooser`, #150) and starting one: the web's `PreviewPanel` without its
/// page, which on the iPhone is the link card. Reads `…/preview/servers` for the session's folder; Start runs an entry
/// (`…/preview/start`) and looks at its port every 1.5 s for up to 90 s, until it answers.
@MainActor @Observable
final class PreviewModel {
    struct Deps {
        var api: @MainActor () -> APIClient?
        var now: @MainActor () -> Date = { .now }
        /// The wait between two looks at a started server (none in tests).
        var pause: @MainActor (Duration) async throws -> Void = { try await Task.sleep(for: $0) }
    }

    enum Phase: Equatable {
        case loading
        /// Persona on that computer is from before previews (`node_outdated`).
        case old
        case failed(String)
        case choose
    }

    /// An entry Start ran, while its port does not answer yet.
    struct Starting: Equatable {
        var option: PreviewOption
        var terminal: String?
        var since: Date
    }

    enum StartResult: Equatable {
        /// Its port answers: open it.
        case ready(PreviewOption)
        /// The computer could not run it (the hub's words).
        case failed(String)
        /// Nothing answered within `patience`.
        case slow(PreviewOption)
        /// Cancel, the list closed, or the servers could not be read (the phase says why).
        case stopped
    }

    static let interval: Duration = .milliseconds(1500)
    static let patience: TimeInterval = 90

    let machine: String
    let cwd: String
    private(set) var phase: Phase = .loading
    private(set) var servers: PreviewServers?
    private(set) var starting: Starting?
    /// Start was asked and the hub has not answered yet.
    private(set) var sending = false
    @ObservationIgnored private let deps: Deps

    init(machine: String, cwd: String, deps: Deps) {
        self.machine = machine
        self.cwd = cwd
        self.deps = deps
    }

    func options(ports: [ListeningPort]) -> [PreviewOption] { PreviewLogic.options(servers, ports: ports) }
    var problems: [PreviewServers.Server] { PreviewLogic.problems(servers) }

    /// Reads the servers again; nil when that failed, and the phase says why.
    @discardableResult func load() async -> PreviewServers? {
        guard let api = deps.api() else {
            phase = .failed(String(localized: "Not connected to a hub [Port Presence c03cfc]"))
            return nil
        }
        do {
            let res = try await api.previewServers(machine: machine, cwd: cwd)
            servers = res
            phase = .choose
            return res
        } catch {
            guard !error.isCancellation else { return nil }
            starting = nil
            if let e = error as? APIError, e.code == "node_outdated" { phase = .old } else { phase = .failed(error.localizedDescription) }
            return nil
        }
    }

    /// Runs an entry in a new terminal tab of the session's workspace (`kind`: the host its space is in), then waits for
    /// its port. `ports`: the session's own ports now (an Agent's port counts as answering).
    func start(_ o: PreviewOption, workspace: String, kind: String, ports: () -> [ListeningPort]) async -> StartResult {
        guard let api = deps.api() else { return .failed(String(localized: "Not connected to a hub [Port Presence c03cfc]")) }
        sending = true
        do {
            let res = try await api.previewStart(machine: machine, cwd: cwd, name: o.name, workspace: workspace, kind: kind)
            sending = false
            starting = Starting(option: o, terminal: res.terminal, since: deps.now())
        } catch {
            sending = false
            return error.isCancellation ? .stopped : .failed(error.localizedDescription)
        }
        return await wait(ports: ports)
    }

    /// Looks at the started entry's port until it answers, Cancel, or `patience` runs out. One look at a time.
    func wait(ports: () -> [ListeningPort]) async -> StartResult {
        guard let s = starting else { return .stopped }
        while true {
            do { try await deps.pause(Self.interval) } catch { return .stopped }
            guard starting == s, !Task.isCancelled else { return .stopped }
            guard let res = await load() else { return .stopped }
            // a reply that comes back after Cancel opens nothing
            guard starting == s else { return .stopped }
            if let now = PreviewLogic.options(res, ports: ports()).first(where: { $0.port == s.option.port }), now.listening {
                starting = nil
                return .ready(now)
            }
            if deps.now().timeIntervalSince(s.since) > Self.patience {
                starting = nil
                return .slow(s.option)
            }
        }
    }

    func cancelStart() { starting = nil }
}
