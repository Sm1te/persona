import Foundation
import Observation
#if os(iOS) || os(visionOS)
import UIKit
#endif

// Whether a conversation's page offers Voice Chat (docs/assistant/voice.md 6.1「显示条件」, 3.1「版本协商」), and the voice
// extension's own read-only endpoints the app asks: /info, /models, /voices. Pure apart from the client calls; the Mac
// compiles it and is told "not here"; Apple Vision Pro has the phone's call audio.

/// The extension's `GET /info` (docs/assistant/voice.md 5.6): which protocol it speaks and what it still needs.
nonisolated struct VoiceInfo: Decodable, Equatable, Sendable {
    var protocolVersion: Int
    var features: [String]
    /// What it still needs before it can talk (no upstream address or key), in words; empty when it is set up.
    var setup: [String]
    var warnings: [String]

    init(protocolVersion: Int, features: [String] = [], setup: [String] = [], warnings: [String] = []) {
        self.protocolVersion = protocolVersion
        self.features = features
        self.setup = setup
        self.warnings = warnings
    }

    private enum CodingKeys: String, CodingKey {
        case features, setup, warnings
        case protocolVersion = "protocol"
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        protocolVersion = (try? c.decodeIfPresent(Int.self, forKey: .protocolVersion)) ?? 1
        features = (try? c.decodeIfPresent([String].self, forKey: .features)) ?? []
        // One reason or a list of them.
        if let one = try? c.decodeIfPresent(String.self, forKey: .setup) {
            setup = one.isEmpty ? [] : [one]
        } else {
            setup = (try? c.decodeIfPresent([String].self, forKey: .setup)) ?? []
        }
        warnings = (try? c.decodeIfPresent([String].self, forKey: .warnings)) ?? []
    }
}

extension VoiceInfo {
    /// The extension can say a given text in the realtime voice (`POST …/speak`, #5004); older ones can't.
    nonisolated static let speakFeature = "speak"
}

/// The realtime voice saying a text (`POST …/speak`): mono PCM16, little-endian, at `rate`, in pieces as they come.
nonisolated struct VoiceSpeech: Sendable {
    var rate: Int
    var audio: AsyncThrowingStream<Data, Error>

    struct Body: Encodable, Sendable {
        var text: String
        var model: String?
        var voice: String?
        var lang: String?
    }

    /// The header with the audio's rate in Hz; without it, the rate the services speak at.
    static let rateHeader = "X-Persona-Audio-Rate"
    static let defaultRate = 24000
    /// What one piece holds: 100 ms at 24 kHz, so the player gets the voice as it comes, not byte by byte.
    static let pieceBytes = 4800

    /// The response's bytes in pieces of `pieceBytes`, read off the main actor; ending the stream (a stop) cancels
    /// the request.
    static func chunks(_ bytes: URLSession.AsyncBytes) -> AsyncThrowingStream<Data, Error> {
        AsyncThrowingStream { continuation in
            let reader = Task.detached {
                do {
                    var piece = Data(capacity: pieceBytes)
                    for try await byte in bytes {
                        piece.append(byte)
                        if piece.count >= pieceBytes {
                            continuation.yield(piece)
                            piece = Data(capacity: pieceBytes)
                        }
                    }
                    if !piece.isEmpty { continuation.yield(piece) }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { @Sendable _ in reader.cancel() }
        }
    }
}

/// What asking `/info` came to.
nonisolated enum VoiceInfoState: Equatable, Sendable {
    case info(VoiceInfo)
    /// 404: the hub or the extension is from before protocol 2.
    case missing
    /// 403: the hub does not let this device reach extensions (it may only answer).
    case forbidden
    /// The request failed otherwise (offline…): asked again later.
    case failed(String)
}

nonisolated enum VoicePlatform: Equatable, Sendable {
    /// iPhone, iPad and Apple Vision Pro: the ones with call audio (`PhoneVoiceAudio`); not the Mac yet.
    case phone
    case other

    static var current: VoicePlatform {
        #if os(iOS) || os(visionOS)
        .phone
        #else
        .other
        #endif
    }
}

/// Whether Voice Chat is offered on a conversation, and if not, why (the 「+」 menu says the why).
nonisolated enum VoiceAvailability: Equatable, Sendable {
    case available(extensionID: String)
    /// Not on this platform (the Mac).
    case unsupported
    /// No conversation to bind (the assistant's home before there is one).
    case noConversation
    /// The conversation cannot take messages from here (a read-only source).
    case readOnly
    /// The hub has no voice extension running (none, switched off, or a hub too old to list them).
    case noExtension
    /// The voice extension runs but has no upstream yet (no address or key).
    case needsSetup([String])
    /// The extension is from before protocol 2 (no /info, or a lower protocol): iOS has no protocol 1 client.
    case outdated
    /// This device may only answer: the hub refuses it the extension.
    case limitedDevice
    /// Not known yet (being asked, or the last ask failed).
    case unknown

    var isAvailable: Bool { if case .available = self { true } else { false } }

    /// The extension the app talks to: the first one with `kind == "voice"` that runs (as Android and hub/channels.go).
    static func pick(_ extensions: [HubExtension]) -> HubExtension? {
        extensions.first { $0.kind == "voice" && $0.status == "running" && validID($0.id) }
    }

    /// Extension ids are a-z, 0-9, `_` and `-` (docs/extensions/extensions.md): anything else must not become a path.
    static func validID(_ id: String) -> Bool {
        !id.isEmpty && id.allSatisfy { ("a"..."z").contains($0) || ("0"..."9").contains($0) || $0 == "_" || $0 == "-" }
    }

    /// `extensions`: the group's (`GET /api/v1/extensions`); nil while not known. `info`: the picked extension's /info.
    static func evaluate(platform: VoicePlatform, session: Session?, extensions: [HubExtension]?, info: VoiceInfoState?) -> VoiceAvailability {
        guard platform == .phone else { return .unsupported }
        guard let session else { return .noConversation }
        // The composer's own rule: a call hands things over only where typing could. A conversation with no source
        // (herdr) takes messages without capabilities; a source has to say it does.
        guard session.canSend else { return .readOnly }
        guard let extensions else { return .unknown }
        guard let voice = pick(extensions) else {
            if let waiting = extensions.first(where: { $0.kind == "voice" && $0.status == "needs_setup" }) {
                return .needsSetup(waiting.needsSetup ?? [])
            }
            return .noExtension
        }
        switch info {
        case nil, .failed?: return .unknown
        case .forbidden?: return .limitedDevice
        case .missing?: return .outdated
        case .info(let i)?:
            guard i.protocolVersion >= 2 else { return .outdated }
            guard i.setup.isEmpty else { return .needsSetup(i.setup) }
            return .available(extensionID: voice.id)
        }
    }
}

/// The models the extension offers (`GET …/models`).
nonisolated struct VoiceModels: Decodable, Equatable, Sendable {
    struct Model: Decodable, Equatable, Sendable {
        var id: String
        var name: String?
    }

    var models: [Model]
    var `default`: String?

    init(models: [Model] = [], default: String? = nil) {
        self.models = models
        self.default = `default`
    }

    private enum CodingKeys: String, CodingKey { case models, `default` }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        models = (try? c.decodeIfPresent([Model].self, forKey: .models)) ?? []
        `default` = try? c.decodeIfPresent(String.self, forKey: .default)
    }
}

/// The voices of a model (`GET …/voices?model=`).
nonisolated struct VoiceVoices: Decodable, Equatable, Sendable {
    struct Voice: Decodable, Equatable, Sendable {
        var id: String
        var name: String?
        /// "female", "male" or nil.
        var gender: String?
        var languages: [String]?
    }

    var voices: [Voice]
    var `default`: String?
    /// The hub could not read the service's list just now: this is the last good one, or the dialect's built-in one
    /// (#2411). Shown all the same, and asked for again the next time a picker opens.
    var stale: Bool

    private enum CodingKeys: String, CodingKey { case voices, `default`, stale }

    init(voices: [Voice] = [], default: String? = nil, stale: Bool = false) {
        self.voices = voices
        self.default = `default`
        self.stale = stale
    }

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        voices = (try? c.decodeIfPresent([Voice].self, forKey: .voices)) ?? []
        `default` = try? c.decodeIfPresent(String.self, forKey: .default)
        stale = (try? c.decodeIfPresent(Bool.self, forKey: .stale)) ?? false
    }
}

/// What the hub's model configuration says for the extension (the use `plugin.<id>`, docs/assistant/voice.md 5.7).
nonisolated struct VoiceModelChoice: Equatable, Sendable {
    var model: String?
    /// The use's status when it is not usable as it stands; nil when it is.
    var status: String?

    /// The hub's status word for a usable model nobody has tried yet (docs/assistant/model-config.md); not a problem. Matched
    /// against what the hub sends, so it is never translated.
    static let unverified = "未验证" // i18n-ignore

    init(model: String?, status: String?) {
        self.model = model.flatMap { $0.trimmingCharacters(in: .whitespaces).isEmpty ? nil : $0 }
        let s = status?.trimmingCharacters(in: .whitespaces)
        self.status = s.flatMap { $0.isEmpty || $0 == Self.unverified ? nil : $0 }
    }
}

/// The model a call starts with.
nonisolated enum VoiceModelPick: Equatable, Sendable {
    /// nil: the extension's default.
    case ready(String?)
    /// Why it cannot start: the use's status, or the model no longer offered.
    case unavailable(String)

    /// The model to send, as Android's resolveVoiceModel: no configuration (an older hub) or none chosen takes the
    /// extension's default; a use whose status says it is not usable stops the call; so does a chosen model the
    /// extension no longer lists (only when it lists any: an empty or unreadable list cannot tell).
    static func resolve(choice: VoiceModelChoice?, models: VoiceModels?) -> VoiceModelPick {
        guard let choice else { return .ready(nil) }
        if let status = choice.status { return .unavailable(status) }
        guard let model = choice.model else { return .ready(nil) }
        if let listed = models?.models, !listed.isEmpty, !listed.contains(where: { $0.id == model }) {
            return .unavailable(model)
        }
        return .ready(model)
    }
}

extension APIClient {
    /// `GET /api/v1/extensions/<id>/info`: the extension's protocol, or why it cannot be asked.
    func voiceInfo(_ extensionID: String) async -> VoiceInfoState {
        do {
            return .info(try await get(extensionPath("/\(extensionID)/info")))
        } catch let error as APIError where error.status == 404 {
            return .missing
        } catch let error as APIError where error.status == 403 {
            return .forbidden
        } catch {
            return .failed(error.localizedDescription)
        }
    }

    /// `GET …/models`; an extension without the endpoint (404) offers none.
    func voiceModels(_ extensionID: String) async throws -> VoiceModels {
        do {
            return try await get(extensionPath("/\(extensionID)/models"))
        } catch let error as APIError where error.status == 404 {
            return VoiceModels()
        }
    }

    /// `GET …/voices?model=`: the voices of `model` (nil: the extension's default model).
    func voiceVoices(_ extensionID: String, model: String?) async throws -> VoiceVoices {
        try await get(extensionPath("/\(extensionID)/voices"), query: ["model": model ?? ""])
    }

    /// `POST …/speak` (docs/assistant/voice.md, #5004): the realtime voice saying `text` with `model` and `voice` (nil:
    /// the extension's default model, the model's default voice), streamed back as it is made. Throws as `request` does
    /// when the extension refuses (`no_upstream`, `upstream_*`, `no_audio`), and when this client cannot stream.
    func voiceSpeak(_ extensionID: String, text: String, model: String?, voice: String?, lang: String?) async throws -> VoiceSpeech {
        let body = VoiceSpeech.Body(text: text, model: model, voice: voice, lang: lang)
        let (response, bytes) = try await streamed("POST", extensionPath("/\(extensionID)/speak"), body: body)
        let rate = response.value(forHTTPHeaderField: VoiceSpeech.rateHeader).flatMap(Int.init) ?? VoiceSpeech.defaultRate
        return VoiceSpeech(rate: rate, audio: VoiceSpeech.chunks(bytes))
    }

    /// The use `plugin.<id>`; nil on a hub without model configuration (404).
    func voiceModelChoice(_ extensionID: String) async throws -> VoiceModelChoice? {
        do {
            let detail = try await modelPurpose("plugin." + extensionID)
            return VoiceModelChoice(model: detail.purpose.effective.model, status: detail.purpose.status)
        } catch let error as APIError where error.status == 404 {
            return nil
        }
    }
}

/// What each group's hub said about voice, kept 60 seconds (docs/assistant/voice.md 6.3「/info 按组缓存 60 秒」): its extensions and
/// the voice extension's /info. Asked again after that and back in the foreground (the last answer stays until the new
/// one comes, so the entry does not blink), and forgotten when a setting changed (the settings page calls `forget`).
@MainActor @Observable final class VoiceSupport {
    static let shared = VoiceSupport(watchesApp: true)

    struct Probe: Equatable {
        var extensions: [HubExtension]
        var info: VoiceInfoState?
    }

    static let ttl: TimeInterval = 60

    private(set) var probes: [String: Probe] = [:]
    /// When each group was last asked: kept apart from `probes`, so asking again and hearing the same does not redraw.
    @ObservationIgnored private var askedAt: [String: Date] = [:]
    @ObservationIgnored private var asking: Set<String> = []
    @ObservationIgnored private var observers: [NSObjectProtocol] = []

    /// `watchesApp`: ask again when the app comes back to the foreground (the shared one).
    init(watchesApp: Bool = false) {
        #if os(iOS) || os(visionOS)
        guard watchesApp else { return }
        observers.append(NotificationCenter.default.addObserver(forName: UIApplication.willEnterForegroundNotification, object: nil,
                                                                queue: .main) { @Sendable [weak self] _ in
            MainActor.assumeIsolated { self?.expire() }
        })
        #endif
    }

    /// Whether `session`'s page offers a call, from what the group's hub said last.
    func availability(for session: Session?, group: String, platform: VoicePlatform = .current) -> VoiceAvailability {
        let probe = probes[group]
        return VoiceAvailability.evaluate(platform: platform, session: session, extensions: probe?.extensions, info: probe?.info)
    }

    /// Asks the group's hub unless its answer is younger than a minute. Not on platforms without calls.
    func refresh(group: String, api: APIClient, now: Date = .now, platform: VoicePlatform = .current) async {
        guard platform == .phone, !asking.contains(group) else { return }
        if let probe = probes[group], let at = askedAt[group], now.timeIntervalSince(at) < Self.ttl {
            if case .failed? = probe.info {} else { return }
        }
        asking.insert(group)
        defer { asking.remove(group) }
        let extensions: [HubExtension]
        do {
            extensions = try await api.plugins()
        } catch let error as APIError where error.status == 404 {
            extensions = []
        } catch {
            return
        }
        var info: VoiceInfoState?
        if let voice = VoiceAvailability.pick(extensions) { info = await api.voiceInfo(voice.id) }
        let probe = Probe(extensions: extensions, info: info)
        askedAt[group] = now
        if probes[group] != probe { probes[group] = probe }
    }

    /// Back in the foreground: the next refresh asks again, but what the hubs said stays until it answers.
    func expire() {
        askedAt = [:]
    }

    /// Forgets what the hubs said (a setting changed): the entry waits for the next answer.
    func forget(group: String? = nil) {
        if let group {
            probes[group] = nil
            askedAt[group] = nil
        } else {
            if !probes.isEmpty { probes = [:] }
            askedAt = [:]
        }
    }
}
