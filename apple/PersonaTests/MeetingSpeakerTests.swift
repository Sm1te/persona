#if os(iOS)
import AVFoundation
import Foundation
import Testing
@testable import Persona

/// Spoken meeting answers (#5004, docs/apple/recording.md「Persona in meetings」): said in the hub's realtime voice when it
/// offers one, with the model and voice the owner uses; in the phone's on-device voice when it doesn't, fails, or has not
/// begun in time; only while the recording runs and the role still speaks; and the stretch Persona speaks marked from
/// when its audio began, so the transcript drops its own voice.
@MainActor struct MeetingSpeakerTests {
    private let recording = UUID()
    private let route = MeetingVoiceRoute(api: nil, extensionID: "voice", model: "voice-deep", voice: "calm")

    /// What the speaker did with the world around it.
    @MainActor final class World {
        var route: MeetingVoiceRoute?
        var synthesized: [(text: String, lang: String?, model: String?, voice: String?)] = []
        /// What `synthesize` does: hand back the stream the test feeds, or throw.
        var failure: Error?
        var stream: AsyncThrowingStream<Data, Error>.Continuation?
        var players: [FakePlayer] = []
        let device = FakeDeviceSpeech()
        var maySpeak = true
        var timeline = 100.0
        var spans: [(UUID, RecordingSpan)] = []
        var routed = 0
        var restored = 0
    }

    private func speaker(_ world: World, within: Duration = .seconds(30)) -> MeetingSpeaker {
        MeetingSpeaker(deps: MeetingSpeaker.Deps(
            realtime: { world.route },
            synthesize: { route, text, lang in
                world.synthesized.append((text, lang, route.model, route.voice))
                if let failure = world.failure { throw failure }
                let (audio, continuation) = AsyncThrowingStream<Data, Error>.makeStream()
                world.stream = continuation
                return VoiceSpeech(rate: 24000, audio: audio)
            },
            realtimeWithin: within,
            player: {
                let p = FakePlayer()
                world.players.append(p)
                return p
            },
            onDevice: world.device,
            maySpeak: { _ in world.maySpeak },
            timeline: { world.timeline },
            spoken: { world.spans.append(($0, $1)) },
            routeToSpeaker: { world.routed += 1; return true },
            restoreOutput: { world.restored += 1 }))
    }

    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async throws {
        for _ in 0..<300 where !done() { try await Task.sleep(for: .milliseconds(10)) }
        #expect(done(), "\(what)", sourceLocation: sourceLocation)
    }

    private func piece(_ ms: Int = 100) -> Data { Data(count: 48 * ms) } // 24 kHz PCM16

    // MARK: The realtime voice

    @Test func theRealtimeVoiceSaysItWithTheOwnersModelAndVoice() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        let id = UUID()
        s.speak("Bo owns the regression test, due Friday.", id: id, recording: recording, lang: "en", locale: "zh-CN")
        #expect(s.speaking == id, "Stop Speaking shows while the voice is asked for")
        try await until("the hub was asked") { world.stream != nil }
        let asked = try #require(world.synthesized.first)
        #expect(asked.text == "Bo owns the regression test, due Friday." && asked.lang == "en")
        #expect(asked.model == "voice-deep" && asked.voice == "calm", "the model of plugin.<id> and the voice picked on this device")
        #expect(s.openSpan(for: recording) == nil, "nothing is said yet: a sentence heard now is the room's")

        world.timeline = 101.5
        world.stream?.yield(piece())
        try await until("its audio plays") { world.players.first?.pieces.count == 1 }
        #expect(world.players.first?.rate == 24000)
        #expect(s.voice == .realtime)
        #expect(s.openSpan(for: recording)?.start == 101.5, "the stretch begins with the audio, not the request")
        world.stream?.yield(piece())
        world.timeline = 104
        world.stream?.finish()
        try await until("it plays out what it has") { world.players.first?.finishing == true }
        #expect(world.device.said.isEmpty, "the on-device voice says nothing")
        world.players.first?.playOut()
        #expect(s.speaking == nil)
        #expect(world.spans.map { $0.1 } == [RecordingSpan(start: 101.5, end: 104)])
        #expect(world.routed == 1 && world.restored == 1, "the loudspeaker for the answer, then back")
    }

    @Test func cutOffMidwayWhatCamePlaysOut() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("A long answer.", id: UUID(), recording: recording, lang: nil, locale: "en-US")
        try await until("asked") { world.stream != nil }
        world.stream?.yield(piece())
        try await until("playing") { world.players.first?.pieces.count == 1 }
        world.stream?.finish(throwing: URLError(.networkConnectionLost))
        try await until("what came plays out") { world.players.first?.finishing == true }
        #expect(world.device.said.isEmpty, "never said twice")
    }

    // MARK: Falling back to the on-device voice

    @Test func withoutTheRealtimeVoiceTheOnDeviceVoiceSaysIt() async throws {
        let world = World() // no voice extension, an older one without speak, or not set up
        let s = speaker(world)
        s.speak("回归测试由张三负责。", id: UUID(), recording: recording, lang: "zh-CN", locale: "zh-CN")
        try await until("the on-device voice says it") { world.device.said.count == 1 }
        #expect(world.device.said.first?.text == "回归测试由张三负责。")
        #expect(world.synthesized.isEmpty && world.players.isEmpty)
        #expect(s.voice == .onDevice)
        #expect(s.openSpan(for: recording)?.start == 100)
        world.timeline = 103
        world.device.finish()
        #expect(s.speaking == nil)
        #expect(world.spans.map { $0.1 } == [RecordingSpan(start: 100, end: 103)])
    }

    @Test(arguments: [
        APIError(status: 503, message: "no realtime voice service", fields: ["code": "no_upstream"]),
        APIError(status: 502, message: "refused", fields: ["code": "upstream_forbidden"]),
        APIError(status: 404, message: "not found"),
    ])
    func aRefusedRealtimeVoiceFallsBack(_ refusal: APIError) async throws {
        let world = World()
        world.route = route
        world.failure = refusal
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("the on-device voice says it") { world.device.said.count == 1 }
        #expect(world.players.isEmpty)
        #expect(s.voice == .onDevice)
    }

    @Test func offlineFallsBack() async throws {
        let world = World()
        world.route = route
        world.failure = URLError(.notConnectedToInternet)
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("the on-device voice says it") { world.device.said.count == 1 }
    }

    @Test func aRealtimeVoiceThatSendsNoAudioFallsBack() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("asked") { world.stream != nil }
        world.stream?.finish()
        try await until("the on-device voice says it") { world.device.said.count == 1 }
        #expect(world.players.isEmpty)
    }

    @Test func aRealtimeVoiceThatIsLateFallsBackAndStaysQuiet() async throws {
        let world = World()
        world.route = route
        let s = speaker(world, within: .milliseconds(50))
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("the on-device voice says it once the realtime one is late") { world.device.said.count == 1 }
        world.stream?.yield(piece()) // too late: the request was cancelled
        try await Task.sleep(for: .milliseconds(100))
        #expect(world.players.isEmpty, "the answer is never said twice")
        #expect(s.voice == .onDevice)
    }

    // MARK: The rules

    @Test func stoppedWhileTheVoiceIsAskedForNothingIsSaid() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("asked") { world.stream != nil }
        s.stop()
        #expect(s.speaking == nil)
        world.stream?.yield(piece())
        try await Task.sleep(for: .milliseconds(100))
        #expect(world.players.isEmpty && world.device.said.isEmpty)
        #expect(world.spans.isEmpty, "nothing was said, nothing is marked")
        #expect(world.restored == 1)
    }

    @Test func noLongerAllowedToSpeakWhenTheAudioComesNothingIsSaid() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("asked") { world.stream != nil }
        world.maySpeak = false // the role became Listen, or the recording stopped
        world.stream?.yield(piece())
        try await until("it gives up") { s.speaking == nil }
        #expect(world.players.isEmpty && world.device.said.isEmpty && world.spans.isEmpty)
    }

    @Test func noLongerAllowedToSpeakWhenFallingBackNothingIsSaid() async throws {
        let world = World()
        world.maySpeak = false
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("it gives up") { s.speaking == nil }
        #expect(world.device.said.isEmpty && world.spans.isEmpty)
    }

    @Test func aNewAnswerCutsTheOneBeingSaid() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("First.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("asked") { world.stream != nil }
        world.stream?.yield(piece())
        try await until("playing") { world.players.count == 1 }
        let first = world.stream
        world.stream = nil
        world.timeline = 102
        let second = UUID()
        s.speak("Second.", id: second, recording: recording, lang: "en", locale: "en-US")
        #expect(world.players.first?.stopped == true)
        #expect(world.spans.map { $0.1 } == [RecordingSpan(start: 100, end: 102)], "the first one's stretch, up to the cut")
        first?.yield(piece())
        try await until("the second is asked for") { world.stream != nil }
        #expect(world.synthesized.map(\.text) == ["First.", "Second."])
        #expect(world.players.first?.pieces.count == 1, "nothing more of the first plays")
        #expect(s.speaking == second)
    }

    @Test func anInterruptionCutsItAndMarksTheStretch() async throws {
        let world = World()
        world.route = route
        let s = speaker(world)
        s.speak("Ship it.", id: UUID(), recording: recording, lang: "en", locale: "en-US")
        try await until("asked") { world.stream != nil }
        world.stream?.yield(piece())
        try await until("playing") { world.players.count == 1 }
        world.timeline = 100.8
        world.players.first?.playOut() // the player says it ended: a call took the audio
        #expect(s.speaking == nil)
        #expect(world.spans.map { $0.1 } == [RecordingSpan(start: 100, end: 100.8)])
    }

    // MARK: The parts

    @Test func onlyAnExtensionThatSpeaksIsUsed() {
        #expect(MeetingVoiceRoutes.speaks(VoiceInfo(protocolVersion: 2, features: ["target", "runs", "speak"])))
        #expect(!MeetingVoiceRoutes.speaks(VoiceInfo(protocolVersion: 2, features: ["target", "runs", "asks", "resume", "suspend"])),
                "an older hub")
        #expect(!MeetingVoiceRoutes.speaks(VoiceInfo(protocolVersion: 2, features: ["speak"], setup: ["the key"])), "not set up")
        #expect(!MeetingVoiceRoutes.speaks(VoiceInfo(protocolVersion: 1, features: ["speak"])))
    }

    @Test func theRequestSaysOnlyWhatItKnows() throws {
        let full = try JSONSerialization.jsonObject(with: JSON.encoder.encode(
            VoiceSpeech.Body(text: "Hi", model: "voice-deep", voice: "calm", lang: "en"))) as? [String: String]
        #expect(full == ["text": "Hi", "model": "voice-deep", "voice": "calm", "lang": "en"])
        let bare = try JSONSerialization.jsonObject(with: JSON.encoder.encode(
            VoiceSpeech.Body(text: "Hi", model: nil, voice: nil, lang: nil))) as? [String: String]
        #expect(bare == ["text": "Hi"], "the extension's default model and the model's default voice")
    }

    @Test func pcmBecomesFloatsAcrossOddPieces() throws {
        let format = try #require(AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: 24000, channels: 1, interleaved: false))
        var carry: UInt8?
        // 0x4000 (16384) and 0xC000 (-16384), the second split across two pieces.
        let first = try #require(MeetingVoicePlayer.buffer(Data([0x00, 0x40, 0x00]), carry: &carry, format: format))
        #expect(first.frameLength == 1 && first.floatChannelData?[0][0] == 0.5)
        #expect(carry == 0x00)
        let second = try #require(MeetingVoicePlayer.buffer(Data([0xC0]), carry: &carry, format: format))
        #expect(second.frameLength == 1 && second.floatChannelData?[0][0] == -0.5)
        #expect(carry == nil)
        #expect(MeetingVoicePlayer.buffer(Data([0x01]), carry: &carry, format: format) == nil, "half a sample waits for the rest")
    }
}

@MainActor final class FakeDeviceSpeech: MeetingDeviceSpeech {
    var said: [(text: String, voice: String?)] = []
    var stops = 0
    private var ended: (@MainActor () -> Void)?

    func speak(_ text: String, voice: AVSpeechSynthesisVoice?, ended: @escaping @MainActor () -> Void) {
        said.append((text, voice?.language))
        self.ended = ended
    }

    func stop() {
        stops += 1
        ended = nil
    }

    /// The synthesizer says it finished.
    func finish() {
        let ended = ended
        self.ended = nil
        ended?()
    }
}

@MainActor final class FakePlayer: MeetingVoicePlayback {
    var rate: Int?
    var pieces: [Data] = []
    var finishing = false
    var stopped = false
    private var started: (@MainActor () -> Void)?
    private var ended: (@MainActor () -> Void)?

    func start(rate: Int, started: @escaping @MainActor () -> Void, ended: @escaping @MainActor () -> Void) throws {
        self.rate = rate
        self.started = started
        self.ended = ended
    }

    func play(_ pcm: Data) {
        pieces.append(pcm)
        if pieces.count == 1 { started?() }
    }

    func finish() { finishing = true }

    func stop() {
        stopped = true
        started = nil
        ended = nil
    }

    /// Everything played, or an interruption cut it.
    func playOut() {
        let ended = ended
        self.ended = nil
        ended?()
    }
}
#endif
