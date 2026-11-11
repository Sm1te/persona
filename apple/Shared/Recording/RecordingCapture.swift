#if os(iOS) || os(visionOS) || os(watchOS)
import AVFoundation
import Observation
#if os(iOS) || os(visionOS)
import UIKit
#else
import WatchKit
#endif

/// Hourhand's phone / watch session management, sharing the segment engine, pausing, bookmarks and resource protection.
@Observable
final class RecordingCapture {
    static let shared = RecordingCapture()
    enum State { case idle, starting, recording, paused }
    private(set) var state: State = .idle
    private(set) var elapsed: Double = 0
    private(set) var recordingID: UUID?
    private(set) var rolling = false
    var error: String?
    /// Non-blocking hint, e.g. the Bluetooth microphone was not found and the phone mic is used.
    private(set) var notice: String?
    var onSaved: ((UUID) -> Void)?
    /// A recording is about to stop, however it stops (Stop, low storage, a failed write, the audio service reset): while
    /// its clock still runs, so whatever follows it (Persona's voice in a meeting, #2981) can end at the right time.
    @ObservationIgnored var onStopping: ((UUID) -> Void)?
    /// Paused by the system (a phone call, Siri, an alarm), not by the user: it picks up again when the system ends the
    /// interruption, or when the app comes back to the front (`resumeAfterInterruption`).
    private(set) var interrupted = false
    /// The system ended an interruption but the microphone could not be opened again (the app was in the background): the
    /// phone tells the user to come back (a notification), since nothing else will resume it.
    @ObservationIgnored var onInterruptionNotResumed: (() -> Void)?
    /// The recorded time now, read without observing it (`elapsed` changes four times a second).
    var timeline: Double { writer?.elapsed ?? sampleElapsed }
    @ObservationIgnored private var sampleElapsed: Double = 0
    /// The microphone's recent level, 0...1 (the writer's smoothed, perceptual level, read-only), for the live page's meter.
    /// Not observed: a meter reads it from its own clock, many times a second.
    var inputLevel: Double {
        #if DEBUG
        if writer == nil { return sampleLevel }
        #endif
        return Double(writer?.level ?? 0)
    }
    #if DEBUG
    /// Screenshots and the DEBUG flow (`-showMeetingSample`): the level a recording with no microphone shows.
    @ObservationIgnored var sampleLevel: Double = 0
    #endif
    @ObservationIgnored private var input: RecordingInput = .phone
    /// Persona may answer out loud during this recording (#2981: a meeting it takes part in). The phone's microphone
    /// session then also lets a Bluetooth headset play (A2DP is listening only, so the recording keeps its microphone);
    /// a wired headset and the loudspeaker play anyway. Off for every other recording, whose session stays as it was.
    @ObservationIgnored private(set) var playsAloud = false
    @ObservationIgnored private var writer: RecordingSegmentWriter?
    @ObservationIgnored private var ticker: Task<Void, Never>?
    @ObservationIgnored private var observers: [NSObjectProtocol] = []
    @ObservationIgnored private var generation = UUID()
    @ObservationIgnored private var activeMicOwner: UUID?
    @ObservationIgnored private var resumeTask: Task<Void, Never>?

    private init() {
        RecordingLibrary.shared.recover(source: Self.source)
        let center = NotificationCenter.default
        observers.append(center.addObserver(forName: AVAudioSession.interruptionNotification, object: nil, queue: .main) { @Sendable [weak self] note in
            let type = (note.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt).flatMap(AVAudioSession.InterruptionType.init)
            MainActor.assumeIsolated { self?.interruption(type) }
        })
        observers.append(center.addObserver(forName: .AVAudioEngineConfigurationChange, object: nil, queue: .main) { @Sendable [weak self] note in
            let engine = note.object.map { ObjectIdentifier($0 as AnyObject) }
            MainActor.assumeIsolated {
                // Every engine posts it (dictation's, a voice call's): only this recording's own engine matters.
                guard let self, self.state == .recording, self.writer?.owns(engine) == true else { return }
                do { try self.writer?.restartAfterConfigurationChange() }
                catch {
                    // Often the route switching to a call before its interruption arrives: paused as by one, so the call's
                    // end (or coming back to the app) picks it up again, and what came before is kept.
                    self.error = String(localized: "The microphone changed, so recording paused")
                    self.writer?.pause(); self.state = .paused; self.interrupted = true
                    if let id = self.recordingID, let segment = self.writer?.cut() { self.save(segment, id: id) }
                }
            }
        })
        observers.append(center.addObserver(forName: AVAudioSession.mediaServicesWereResetNotification, object: nil, queue: .main) { @Sendable [weak self] _ in
            MainActor.assumeIsolated {
                // Paused too (during a call): the engine it would resume is gone with the reset.
                guard let self, self.state == .recording || self.state == .paused else { return }
                self.stop(); self.error = String(localized: "The system audio service reset. What was finished is saved; start recording again.")
            }
        })
    }

    private static var source: RecordingEntry.Source {
        #if os(watchOS)
        .watch
        #else
        .phone
        #endif
    }

    private func configureSession(token: UUID) async throws {
        let session = AVAudioSession.sharedInstance()
        guard generation == token else { return }
        notice = nil
        #if os(iOS) || os(visionOS)
        #if os(iOS)
        if input == .bluetooth {
            try session.setCategory(.playAndRecord, mode: .default, options: [.allowBluetoothHFP])
            try session.setActive(true)
            if let port = await bluetoothInput(session, token: token) {
                guard generation == token else { return }
                try session.setPreferredInput(port)
                return
            }
            guard generation == token else { return }
            notice = String(localized: "No glasses or Bluetooth headset microphone found; using the phone's microphone")
        }
        #endif
        try session.setCategory(.playAndRecord, mode: .default, options: Self.phoneOptions(playsAloud: playsAloud))
        try session.setActive(true)
        #if os(iOS)
        let ports = session.availableInputs ?? []
        if let index = RecordingInput.preferredIndex(ports.map(\.portType), for: .phone) {
            try session.setPreferredInput(ports[index])
        }
        #else
        if let mic = session.availableInputs?.first(where: { $0.portType == .builtInMic }) { try session.setPreferredInput(mic) }
        #endif
        #else
        try session.setCategory(.record, mode: .default)
        try session.setActive(true)
        #endif
    }

    #if os(iOS) || os(visionOS)
    /// The phone microphone's session options: the loudspeaker rather than the earpiece, and a Bluetooth headset for
    /// listening when Persona may speak (`playsAloud`).
    static func phoneOptions(playsAloud: Bool) -> AVAudioSession.CategoryOptions {
        #if os(iOS)
        playsAloud ? [.defaultToSpeaker, .allowBluetoothA2DP] : [.defaultToSpeaker]
        #else
        [.defaultToSpeaker]
        #endif
    }

    /// Persona started or stopped taking part while recording: the same session, with or without the headset for
    /// listening. A Bluetooth microphone's session plays through that headset already.
    func setPlaysAloud(_ on: Bool) {
        guard playsAloud != on else { return }
        playsAloud = on
        guard writer != nil, input == .phone else { return }
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .default, options: Self.phoneOptions(playsAloud: on))
    }
    #endif

    #if os(iOS)
    /// The Bluetooth HFP input appears only after the route settles, so poll for up to two seconds.
    private func bluetoothInput(_ session: AVAudioSession, token: UUID) async -> AVAudioSessionPortDescription? {
        var waited = 0
        while generation == token, !Task.isCancelled {
            let ports = session.availableInputs ?? []
            if let index = RecordingInput.preferredIndex(ports.map(\.portType), for: .bluetooth) { return ports[index] }
            guard waited < 10 else { return nil }
            do { try await Task.sleep(for: .milliseconds(200)) } catch { return nil }
            waited += 1
        }
        return nil
    }

    /// Name of the input the current route records from, shown while recording.
    var inputName: String? {
        guard state == .recording || state == .paused else { return nil }
        return AVAudioSession.sharedInstance().currentRoute.inputs.first?.portName
    }
    #endif

    private static func permission() async -> Bool {
        if #available(iOS 17.0, watchOS 10.0, *) { return await AVAudioApplication.requestRecordPermission() }
        return await withCheckedContinuation { continuation in
            AVAudioSession.sharedInstance().requestRecordPermission { @Sendable allowed in continuation.resume(returning: allowed) }
        }
    }

    /// `title` / `meeting`: what the recording is called and, for a meeting recording, its meeting (docs/apple/recording.md).
    /// `playsAloud`: Persona takes part in this meeting and may answer out loud.
    func start(locale: String = "zh-CN", input: RecordingInput = .phone, title: String = "", meeting: RecordingMeeting? = nil, rolling: Bool = false,
               playsAloud: Bool = false) async {
        guard state == .idle else { return }
        generation = UUID(); let token = generation
        state = .starting; error = nil; notice = nil; self.rolling = rolling
        self.input = input
        self.playsAloud = playsAloud
        let allowed = await Self.permission()
        guard generation == token, state == .starting else { return }
        guard allowed else { state = .idle; error = String(localized: "Allow Persona to use the microphone in Settings"); return }
        if let problem = Self.resourceProblem(starting: true) { state = .idle; error = problem; return }
        let micOwner = UUID()
        guard RecordingMicrophone.shared.claim(micOwner) else {
            state = .idle; error = String(localized: "The microphone is in use for dictation. Finish dictating before recording."); return
        }
        activeMicOwner = micOwner
        do {
            try await configureSession(token: token)
            // Cancelled while Bluetooth discovery was running: cancelStarting() already released the session and mic.
            guard generation == token else { return }
            let entry = try RecordingLibrary.shared.create(source: Self.source, locale: locale, title: title, meeting: meeting, kind: rolling ? .rolling : .clip)
            recordingID = entry.id
            let writer = RecordingSegmentWriter(directory: RecordingLibrary.shared.folder(entry.id))
            writer.segmentLength = Self.segmentLength
            writer.onSegment = { @Sendable [weak self] segment in
                Task { @MainActor in
                    self?.save(segment, id: entry.id)
                }
            }
            try writer.start()
            self.writer = writer
            elapsed = 0; state = .recording
            startTicker()
        } catch {
            guard generation == token else { return }
            if let id = recordingID { RecordingLibrary.shared.change(id) { $0.active = false; $0.expectedSegments = 0 } }
            recordingID = nil; state = .idle; self.rolling = false
            releaseAudioSession()
            self.error = String(localized: "Can't open the microphone. Try again later.")
        }
    }

    func cancelStarting() {
        guard state == .starting else { return }
        generation = UUID(); state = .idle; notice = nil; rolling = false
        releaseAudioSession()
    }

    func togglePause() {
        #if DEBUG
        if writer == nil, recordingID != nil, sampleElapsed > 0 {
            if state == .recording { state = .paused } else if state == .paused { state = .recording }
            return
        }
        #endif
        guard let writer else { return }
        if state == .recording {
            interrupted = false
            resumeTask?.cancel(); resumeTask = nil
            writer.pause(); state = .paused
        } else if state == .paused {
            resume(writer)
        }
    }

    /// After a phone call, or when the app comes back to the front while one left the recording paused: carries on.
    /// Nothing when the user paused it.
    func resumeAfterInterruption() {
        guard interrupted, state == .paused, let writer else { return }
        resume(writer)
    }

    private func resume(_ writer: RecordingSegmentWriter) {
        guard resumeTask == nil else { return }
        let token = generation
        resumeTask = Task { @MainActor [weak self] in
            defer { self?.resumeTask = nil }
            guard let self else { return }
            do {
                try await self.configureSession(token: token)
                guard self.generation == token, self.writer === writer, self.state == .paused else { return }
                try writer.resume()
                self.state = .recording; self.interrupted = false; self.error = nil
            } catch {
                guard self.generation == token else { return }
                self.error = String(localized: "Can't open the microphone; the recording is still saved")
                // Still `interrupted`: coming back to the app tries again.
                if self.interrupted { self.onInterruptionNotResumed?() }
            }
        }
    }

    /// Closes the segment being written and goes on in a new one, so what was just said is transcribed now rather than
    /// at the segment's end (#2981: asking Persona about it).
    func flushSegment() {
        guard state == .recording, let writer, let id = recordingID, let segment = writer.cut() else { return }
        save(segment, id: id)
    }

    func addMark() {
        #if DEBUG
        if writer == nil, let id = recordingID, sampleElapsed > 0 {
            let time = sampleElapsed
            RecordingLibrary.shared.change(id) { entry in
                if let last = entry.marks.last, time - last < 1 { return }
                entry.marks.append(time)
            }
            return
        }
        #endif
        guard let id = recordingID, let writer else { return }
        let time = writer.elapsed
        RecordingLibrary.shared.change(id) { entry in
            if let last = entry.marks.last, time - last < 1 { return }
            entry.marks.append(time)
        }
    }

    /// Erases the last few minutes of a rolling recording everywhere; the segment being written is dropped when it finalizes.
    func eraseRecent() {
        guard rolling, let id = recordingID else { return }
        let now = Date.now
        RecordingLibrary.shared.erase(id, from: now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60), to: now)
    }

    @discardableResult func stop() -> UUID? {
        if state == .starting { cancelStarting(); return nil }
        #if DEBUG
        if writer == nil, let id = recordingID, sampleElapsed > 0 { return stopSample(id) }
        #endif
        guard let writer, let id = recordingID else { return nil }
        onStopping?(id)
        generation = UUID()
        resumeTask?.cancel(); resumeTask = nil
        ticker?.cancel(); ticker = nil
        writer.stop()
        // A stop on a segment boundary leaves no tail segment: take every finalized part before writing the end message.
        for segment in writer.finishedSegments { save(segment, id: id) }
        RecordingLibrary.shared.change(id) { entry in
            entry.active = false
            entry.expectedSegments = entry.nextIndex
            entry.endedAt = .now
        }
        self.writer = nil; recordingID = nil; state = .idle; interrupted = false; notice = nil; rolling = false; playsAloud = false
        releaseAudioSession()
        onSaved?(id)
        return id
    }

    private func releaseAudioSession() {
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setPreferredInput(nil)
        #endif
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        if let owner = activeMicOwner {
            activeMicOwner = nil
            RecordingMicrophone.shared.release(owner)
        }
    }

    private func save(_ segment: RecordingSegmentWriter.Segment, id: UUID) {
        RecordingLibrary.shared.addSegment(id, RecordingSegment(index: segment.index, start: segment.start, duration: segment.duration,
                                                                startedAt: segment.startedAt))
        onSaved?(id)
    }

    private func startTicker() {
        ticker = Task { [weak self] in
            var count = 0
            while !Task.isCancelled {
                do { try await Task.sleep(for: .milliseconds(250)) } catch { return }
                guard let self, let writer = self.writer else { return }
                self.elapsed = writer.elapsed
                writer.segmentLength = Self.segmentLength
                count += 1
                if let failure = writer.failure ?? RecordingLibrary.shared.error { self.stop(); self.error = failure; return }
                if count % 20 == 0, let problem = Self.resourceProblem(starting: false) { self.stop(); self.error = problem; return }
            }
        }
    }

    private func interruption(_ type: AVAudioSession.InterruptionType?) {
        if type == .began, state == .recording, let writer, let id = recordingID {
            interrupted = true
            resumeTask?.cancel(); resumeTask = nil
            writer.pause(); state = .paused
            // A long call: what was said before it is kept even if the app is stopped before the call ends (a segment
            // still open would be deleted by `recover`). The next segment starts when the recording resumes.
            if let segment = writer.cut() { save(segment, id: id) }
            if rolling {
                error = String(localized: "Rolling recording paused — tap Continue to resume")
                #if os(watchOS)
                WKInterfaceDevice.current().play(.notification)
                #endif
            }
        } else if type == .ended {
            // The system says whether to resume (`shouldResume`); a recording resumes either way: the meeting goes on.
            resumeAfterInterruption()
        }
    }

    #if DEBUG
    /// Screenshots (`-showMeetingSample`): `id` shows as being recorded `elapsed` seconds in, paused or paused by a call,
    /// with no microphone (Stop ends it through `stopSample`).
    func showSample(_ id: UUID, elapsed: Double, paused: Bool = false, interrupted: Bool = false) {
        recordingID = id; self.elapsed = elapsed; sampleElapsed = elapsed
        self.interrupted = interrupted
        state = paused || interrupted ? .paused : .recording
    }

    /// The DEBUG flow (`-showMeetingSample flow`): moves a sample recording's clock and level on.
    func advanceSample(elapsed: Double, level: Double) {
        guard writer == nil, state != .idle else { return }
        sampleElapsed = elapsed; self.elapsed = elapsed; sampleLevel = level
    }

    /// Stop for a sample recording: what `stop()` does for a real one after the microphone is closed (the entry ends, the
    /// same callbacks run), so the wrap-up steps advance as they would.
    private func stopSample(_ id: UUID) -> UUID {
        onStopping?(id)
        RecordingLibrary.shared.change(id) { entry in
            entry.active = false
            entry.expectedSegments = entry.nextIndex
            entry.endedAt = .now
        }
        recordingID = nil; state = .idle; interrupted = false; notice = nil; sampleElapsed = 0; sampleLevel = 0
        onSaved?(id)
        return id
    }
    #endif

    private static var segmentLength: Double {
        #if SHARE_EXTENSION
        RecordingAudioSpec.segmentSeconds // Share › Persona compiles Shared but never records (no UIApplication there)
        #elseif os(iOS) || os(visionOS)
        UIApplication.shared.applicationState == .active ? RecordingAudioSpec.liveSegmentSeconds : RecordingAudioSpec.segmentSeconds
        #else
        WatchRecordingTransfer.shared.reachable ? RecordingAudioSpec.liveSegmentSeconds : RecordingAudioSpec.segmentSeconds
        #endif
    }

    private static func resourceProblem(starting: Bool) -> String? {
        let attributes = try? FileManager.default.attributesOfFileSystem(forPath: NSHomeDirectory())
        let free = (attributes?[.systemFreeSize] as? NSNumber)?.int64Value
        if let free, free < (starting ? 300_000_000 : 150_000_000) { return String(localized: "Not enough storage. Saved recordings won't be lost.") }
        #if os(watchOS)
        let device = WKInterfaceDevice.current(); device.isBatteryMonitoringEnabled = true
        if device.batteryState == .unplugged, device.batteryLevel >= 0, device.batteryLevel < 0.05 { return String(localized: "Watch battery is too low. Saved recordings won't be lost.") }
        #endif
        return nil
    }
}
#endif
