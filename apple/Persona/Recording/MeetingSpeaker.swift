#if os(iOS)
import AVFoundation
import NaturalLanguage
import Observation
import OSLog
import PersonaKit

/// Says Persona's answers out loud in a meeting it takes part in (#2981, docs/apple/recording.md「Persona in meetings」).
/// In the realtime voice of voice chats when the hub offers it (#5004): the voice extension's `speak`, with the model the
/// use `plugin.<id>` picks and the voice picked on this device, streamed and played as it comes. Otherwise, or when that
/// voice fails or has not started within `Deps.realtimeWithin`, with the phone's on-device voice for the answer's
/// language. Either way in the meeting recording's own audio session (so the recording goes on), with the voice call's
/// route rule for where it plays (`VoiceRoute.output`): a connected headset, else the loudspeaker, never the earpiece;
/// the recording keeps its microphone. Every stretch the phone speaks in is marked on the recording
/// (`RecordingAttendance.spoken`), from when its audio actually starts: a sentence at least half inside one is Persona's
/// own voice and stays out of the transcript and the live log (the contract requires it). The rules of `MeetingAsk`
/// (still recording, the role still speaks) are checked again just before the first sound.
@MainActor @Observable
final class MeetingSpeaker {
    static let shared = MeetingSpeaker(deps: .live)

    /// What says an answer.
    enum Voice: Equatable { case realtime, onDevice }

    /// The exchange being said (`MeetingAsk.Exchange.id`), nil when quiet. Set from the moment it is asked to speak: the
    /// realtime voice may take a moment to begin, and Stop Speaking cancels that too.
    private(set) var speaking: UUID?
    /// Which voice says it, once one has begun.
    private(set) var voice: Voice?

    @ObservationIgnored let deps: Deps
    @ObservationIgnored private var recording: UUID?
    /// The answer being said, kept for the on-device voice if the realtime one cannot say it.
    @ObservationIgnored private var pending: (text: String, lang: String?, locale: String)?
    /// Where on the recording this answer's audio began.
    @ObservationIgnored private var spanStart: Double?
    @ObservationIgnored private var overrodeOutput = false
    /// Which answer the work in flight is for: a stop or a new answer moves it on, so late work for an earlier one is
    /// dropped.
    @ObservationIgnored private var run = 0
    @ObservationIgnored private var asking: Task<Void, Never>?
    @ObservationIgnored private var deadline: Task<Void, Never>?
    @ObservationIgnored private var player: (any MeetingVoicePlayback)?
    private static let log = Logger(subsystem: "persona", category: "meeting-speaker")

    init(deps: Deps) {
        self.deps = deps
    }

    /// The longest an answer can take aloud (the contract's 400 characters, slowly): a stretch never runs past it, even
    /// if the system never says the answer ended (an interruption that never ends).
    static let longestAnswer: Double = 150

    /// The stretch being said now, still open: a sentence transcribed meanwhile is checked against it.
    func openSpan(for id: UUID) -> RecordingSpan? {
        guard recording == id, let start = spanStart else { return nil }
        return RecordingSpan(start: start, end: min(max(deps.timeline(), start), start + Self.longestAnswer))
    }

    /// Says `text` for the exchange `id` of `recording`, in `lang` (BCP 47, the hub's), else the language it is written
    /// in, else the recording's (`locale`).
    func speak(_ text: String, id: UUID, recording: UUID, lang: String?, locale: String) {
        stop()
        run += 1
        let run = run
        speaking = id
        self.recording = recording
        pending = (text, lang, locale)
        overrodeOutput = deps.routeToSpeaker()
        asking = Task { [weak self] in await self?.sayRealtime(text, lang: lang, run: run) }
        let within = deps.realtimeWithin
        deadline = Task { [weak self] in
            guard (try? await Task.sleep(for: within)) != nil else { return }
            self?.fallBack(run, why: "no realtime audio within \(within)")
        }
    }

    /// Stops at once (Stop Speaking, a new answer, the role no longer speaking, the recording ending).
    func stop() {
        guard speaking != nil else { return }
        player?.stop()
        if voice == .onDevice { deps.onDevice.stop() }
        finished()
    }

    #if DEBUG
    /// Screenshots (`-showMeetingSample answered`): the exchange shows as being said, with no voice.
    func showSample(_ id: UUID) { speaking = id }
    #endif

    // MARK: The realtime voice

    /// Asks the hub's realtime voice to say it and plays what comes. Nothing it can't do is the meeting's problem: without
    /// that voice, on an error or with no audio at all, the on-device voice says the answer instead.
    private func sayRealtime(_ text: String, lang: String?, run: Int) async {
        guard let route = await deps.realtime() else { return fallBack(run, why: "no realtime voice on this hub") }
        guard self.run == run, voice == nil else { return }
        let speech: VoiceSpeech
        do {
            speech = try await deps.synthesize(route, text, lang)
        } catch {
            return fallBack(run, why: Self.describe(error))
        }
        var began = false
        do {
            for try await chunk in speech.audio {
                guard self.run == run else { return }
                if !began {
                    guard voice == nil else { return } // the on-device voice took over meanwhile
                    guard let recording, deps.maySpeak(recording) else { return finished() }
                    let player = deps.player()
                    do {
                        try player.start(rate: speech.rate,
                                         started: { [weak self] in self?.began(run) },
                                         ended: { [weak self] in self?.ended(run) })
                    } catch {
                        return fallBack(run, why: "the player did not start: \(error.localizedDescription)")
                    }
                    self.player = player
                    voice = .realtime
                    deadline?.cancel()
                    began = true
                    Self.log.info("meeting: saying an answer in the realtime voice (\(speech.rate) Hz)")
                }
                player?.play(chunk)
            }
            guard self.run == run else { return }
            if began { player?.finish() } else { fallBack(run, why: "the realtime voice sent no audio") }
        } catch {
            guard self.run == run else { return }
            // Cut off midway: what came plays out; nothing came: the on-device voice says it all.
            if began {
                Self.log.notice("meeting: the realtime voice stopped early: \(Self.describe(error), privacy: .public)")
                player?.finish()
            } else {
                fallBack(run, why: Self.describe(error))
            }
        }
    }

    /// The on-device voice says the answer: the realtime one could not, or has not begun in time. Once only, and only
    /// while nothing has begun to say it.
    private func fallBack(_ run: Int, why: String) {
        guard self.run == run, voice == nil, let pending, let recording else { return }
        Self.log.notice("meeting: on-device voice for the answer: \(why, privacy: .public)")
        asking?.cancel() // a request still out ends, and the hub closes its realtime session
        deadline?.cancel()
        guard deps.maySpeak(recording) else { return finished() }
        voice = .onDevice
        deps.onDevice.speak(pending.text, voice: Self.voice(lang: pending.lang, text: pending.text, fallback: pending.locale),
                            ended: { [weak self] in self?.ended(run) })
        began(run)
    }

    // MARK: Either voice

    /// Its audio began: the stretch Persona speaks in starts here.
    private func began(_ run: Int) {
        guard self.run == run, spanStart == nil else { return }
        spanStart = deps.timeline()
    }

    /// The answer ended (played out, or cut by an interruption).
    private func ended(_ run: Int) {
        guard self.run == run else { return }
        finished()
    }

    /// The answer ended or was cut: the stretch it took is marked on the recording, and the route goes back. Whatever is
    /// still on its way for it (a late chunk, a player's or the synthesizer's word that it ended) is dropped.
    private func finished() {
        run += 1
        if let id = recording, let span = openSpan(for: id) { deps.spoken(id, span) }
        if overrodeOutput {
            deps.restoreOutput()
            overrodeOutput = false
        }
        asking?.cancel()
        deadline?.cancel()
        asking = nil
        deadline = nil
        player = nil
        speaking = nil
        voice = nil
        recording = nil
        pending = nil
        spanStart = nil
    }

    private static func describe(_ error: Error) -> String {
        if let api = error as? APIError { return "HTTP \(api.status) \(api.code ?? "")" }
        return error.localizedDescription
    }

    // MARK: Language

    static func voice(lang: String?, text: String, fallback: String) -> AVSpeechSynthesisVoice? {
        if let lang, let voice = AVSpeechSynthesisVoice(language: lang) { return voice }
        return AVSpeechSynthesisVoice(language: language(of: text, fallback: fallback))
    }

    /// The BCP 47 language to say `text` in: the one it is written in, when the recognizer is sure, else `fallback`
    /// (the recording's language).
    nonisolated static func language(of text: String, fallback: String) -> String {
        let recognizer = NLLanguageRecognizer()
        recognizer.processString(text)
        switch recognizer.dominantLanguage {
        case .simplifiedChinese, .traditionalChinese: return "zh-CN"
        case .english: return "en-US"
        case .japanese: return "ja-JP"
        default: return fallback
        }
    }
}

/// The hub's realtime voice for a spoken answer: the voice extension and what to ask it for.
struct MeetingVoiceRoute {
    var api: APIClient?
    var extensionID: String
    /// The model the use `plugin.<id>` picks; nil: the extension's default.
    var model: String?
    /// The voice picked on this device for that extension (`VoiceTimbre`); nil: the model's default.
    var voice: String?
}

extension MeetingSpeaker {
    /// What it needs from the rest of the app; the tests hand in fakes.
    struct Deps {
        /// The hub's realtime voice when it can say an answer now (its voice extension runs, is set up and lists
        /// `speak`); nil: the on-device voice says it.
        var realtime: () async -> MeetingVoiceRoute?
        /// The realtime voice saying `text` (`POST …/speak`), streamed.
        var synthesize: (_ route: MeetingVoiceRoute, _ text: String, _ lang: String?) async throws -> VoiceSpeech
        /// How long the realtime voice gets to begin (the hub asked, its session opened, the first audio in) before
        /// the on-device voice says the answer instead.
        var realtimeWithin: Duration = .seconds(8)
        var player: () -> any MeetingVoicePlayback
        var onDevice: any MeetingDeviceSpeech
        /// The recording still runs and Persona may speak in it now (`MeetingAsk`'s own rule).
        var maySpeak: (UUID) -> Bool
        /// Where the recording is now, in seconds.
        var timeline: () -> Double
        /// Marks a stretch Persona spoke in on the recording.
        var spoken: (_ recording: UUID, _ span: RecordingSpan) -> Void
        /// Moves the voice to the loudspeaker when nothing else would play it (`VoiceRoute.output`); whether it did.
        var routeToSpeaker: () -> Bool
        var restoreOutput: () -> Void
    }
}

extension MeetingSpeaker.Deps {
    /// The app's: the hub's voice extension, the recording, the phone's audio.
    static var live: Self {
        Self(realtime: { await MeetingVoiceRoutes.shared.route() },
             synthesize: { route, text, lang in
                 guard let api = route.api else { throw URLError(.notConnectedToInternet) }
                 return try await api.voiceSpeak(route.extensionID, text: text, model: route.model, voice: route.voice, lang: lang)
             },
             player: { MeetingVoicePlayer() },
             onDevice: MeetingDeviceVoice(),
             maySpeak: { MeetingAsk.shared.deps.maySpeak($0) },
             timeline: { RecordingCapture.shared.timeline },
             spoken: { id, span in RecordingLibrary.shared.change(id) { entry in entry.attendance?.spoken.append(span) } },
             routeToSpeaker: {
                 let session = AVAudioSession.sharedInstance()
                 guard VoiceRoute.output(PhoneVoiceAudio.snapshot(session)).speaker else { return false }
                 do {
                     try session.overrideOutputAudioPort(.speaker)
                     return true
                 } catch {
                     Logger(subsystem: "persona", category: "meeting-speaker")
                         .notice("meeting: could not move the answer to the speaker: \(error.localizedDescription, privacy: .public)")
                     return false
                 }
             },
             restoreOutput: { try? AVAudioSession.sharedInstance().overrideOutputAudioPort(.none) })
    }
}

/// Finds the hub's realtime voice for spoken answers: the voice extension the voice chats use (`VoiceSupport`, asked again
/// after a minute), the model its use picks and the voice picked on this device. The model is kept a minute too, so an
/// answer does not wait for two more requests each time.
@MainActor final class MeetingVoiceRoutes {
    static let shared = MeetingVoiceRoutes()

    private var models: [String: (model: String?, at: Date)] = [:]
    static let ttl: TimeInterval = 60

    func route(now: Date = .now) async -> MeetingVoiceRoute? {
        guard let hub = AppModel.shared.hub else { return nil }
        let api = hub.api, group = hub.group
        await VoiceSupport.shared.refresh(group: group, api: api)
        guard let probe = VoiceSupport.shared.probes[group], let voice = VoiceAvailability.pick(probe.extensions),
              case .info(let info)? = probe.info, Self.speaks(info) else { return nil }
        let key = group + "\u{0}" + voice.id
        let model: String?
        if let kept = models[key], now.timeIntervalSince(kept.at) < Self.ttl {
            model = kept.model
        } else {
            switch await VoiceConversation.resolveModel(api, voice.id) {
            case .ready(let picked):
                model = picked
                models[key] = (picked, now)
            case .unavailable:
                return nil // the use says its model can't be used: a call would refuse to start too
            }
        }
        return MeetingVoiceRoute(api: api, extensionID: voice.id, model: model, voice: VoiceTimbre.chosen(group: group, extensionID: voice.id))
    }

    /// The extension can say an answer: protocol 2, set up, and it lists `speak` (older hubs don't).
    nonisolated static func speaks(_ info: VoiceInfo) -> Bool {
        info.protocolVersion >= 2 && info.setup.isEmpty && info.features.contains(VoiceInfo.speakFeature)
    }
}

// MARK: - The on-device voice

/// The phone's own voice for an answer (`AVSpeechSynthesizer`), in the app's audio session as it is.
@MainActor protocol MeetingDeviceSpeech: AnyObject {
    /// Says `text`; `ended` once it finished, was cancelled or was paused by an interruption (which cuts it).
    func speak(_ text: String, voice: AVSpeechSynthesisVoice?, ended: @escaping @MainActor () -> Void)
    func stop()
}

final class MeetingDeviceVoice: NSObject, MeetingDeviceSpeech {
    private let synthesizer = AVSpeechSynthesizer()
    /// The utterance being said, held so its identity can't be reused while it plays: a late "cancelled" from the one
    /// before must not end the next.
    private var utterance: AVSpeechUtterance?
    private var ended: (@MainActor () -> Void)?

    override init() {
        super.init()
        synthesizer.delegate = self
        // The recording's session, as it is: no category of its own, and it is never deactivated afterwards.
        synthesizer.usesApplicationAudioSession = true
    }

    func speak(_ text: String, voice: AVSpeechSynthesisVoice?, ended: @escaping @MainActor () -> Void) {
        let next = AVSpeechUtterance(string: text)
        next.voice = voice
        utterance = next
        self.ended = ended
        synthesizer.speak(next)
    }

    func stop() {
        utterance = nil
        ended = nil
        synthesizer.stopSpeaking(at: .immediate)
    }

    /// The answer the system says is over (finished, cancelled, or paused by an interruption, which cuts it): only the
    /// one being said, compared as the very object, so a late word about the one before never ends the next.
    fileprivate func over(_ said: AVSpeechUtterance, cut: Bool) {
        guard let utterance, utterance === said else { return }
        if cut { synthesizer.stopSpeaking(at: .immediate) }
        let ended = ended
        self.utterance = nil
        self.ended = nil
        ended?()
    }
}

extension MeetingDeviceVoice: AVSpeechSynthesizerDelegate {
    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didFinish utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: false) }
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didCancel utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: false) }
    }

    /// A call or Siri paused it: the answer is cut rather than finished later, out of place in the meeting.
    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didPause utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: true) }
    }
}
#endif
