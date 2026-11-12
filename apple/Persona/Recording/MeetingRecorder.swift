#if os(iOS)
import AVFoundation
import Foundation
import OSLog
import UIKit
import UserNotifications

/// "We're meeting now, record it" in one step (docs/apple/recording.md「Meeting recordings」): the drawer's Meeting Recording,
/// the Recordings page, the home screen's menu, the Control, Siri / Shortcuts / the Action button. Names the recording after
/// the calendar event happening now, keeps the Live Activity in step, picks a recording a call paused up again when the app
/// comes back, and holds the app open after a stop in the background until the last text is transcribed and sent.
@MainActor final class MeetingRecorder {
    static let shared = MeetingRecorder()

    /// The language a recording starts in (`RecordingDefaults`): the Recordings page's picker remembers it.
    static var locale: String { UserDefaults.standard.string(forKey: RecordingDefaults.localeKey) ?? RecordingDefaults.locale }

    /// The microphone was never allowed: the app has to be in front to ask.
    static var needsForeground: Bool { AVAudioApplication.shared.recordPermission != .granted }
    static var inForeground: Bool { UIApplication.shared.applicationState == .active }

    private let capture = RecordingCapture.shared
    private var started = false
    private let log = Logger(subsystem: "persona", category: "meeting-recorder")

    /// From `PhoneRecordingReceiver.start` at launch.
    func start() {
        guard !started else { return }
        started = true
        RecordingActivity.shared.start()
        MinutesRelay.shared.start()
        capture.onInterruptionNotResumed = { MeetingRecorder.notifyPaused() }
        // However a meeting stops: nothing more is said in it, and its questions go (#2981).
        capture.onStopping = { MeetingAsk.shared.ended($0) }
        MeetingLive.shared.start()
        // The room of every upload 200 of a room meeting (#3862).
        MeetingConversation.shared.start()
        // A call that ended while the app was away (or never said so): coming back carries on, unless another app's audio
        // (a VoIP call without CallKit, still going) holds the route: taking the session would cut into that call.
        NotificationCenter.default.addObserver(forName: UIApplication.didBecomeActiveNotification, object: nil, queue: .main) { @Sendable _ in
            Task { @MainActor in
                guard !AVAudioSession.sharedInstance().isOtherAudioPlaying else { return }
                RecordingCapture.shared.resumeAfterInterruption()
            }
        }
    }

    enum Outcome: Equatable {
        case started
        /// Something records already (this one or a note): left alone.
        case alreadyRecording
        case failed(String)
    }

    /// Starts a meeting recording named after the calendar event happening now (a dated "Meeting" without one). In front
    /// (`inForeground`), the Recordings page opens (unless the caller opened it: `openPage`), and the calendar is asked
    /// for once if it never was, after the recording has begun, so the prompt costs no seconds of the meeting.
    /// `attendance`: Persona's part (#2981), as picked on the Recordings page; nil (the drawer, Siri, the Control, the Action
    /// button) starts with the hub's default role and the session picked last that isn't the main chat, else off
    /// (`MeetingAttendees.defaultAttendance`), or, with the meeting conversation, as a room meeting in the default role,
    /// not private (`MeetingAttendees.startAttendance`).
    @discardableResult
    func startMeeting(inForeground: Bool, openPage: Bool? = nil, attendance: RecordingAttendance? = nil) async -> Outcome {
        if openPage ?? inForeground { Self.openRecordings() }
        guard capture.state == .idle else { return .alreadyRecording }
        let meeting = await MeetingCalendar.meetingOffMain() ?? RecordingMeeting()
        let picked = attendance != nil
        let attendance = attendance ?? MeetingAttendees.startAttendance(AppModel.shared)
        await capture.start(locale: Self.locale, input: RecordingInput.chosen, title: meeting.title ?? "", meeting: meeting,
                            playsAloud: attendance.sentRole.speaks)
        guard capture.state == .recording, let id = capture.recordingID else {
            return .failed(capture.error ?? String(localized: "Can't open the microphone. Try again later."))
        }
        // A room meeting (#3862) keeps its attendance whatever the role: every upload carries `room` (`off` included),
        // so the hub has it at once. Nothing to remember: it has no attending session.
        if attendance.room {
            RecordingLibrary.shared.change(id) { $0.attendance = attendance }
            RecordingSync.shared.attendanceChanged(id)
        } else if attendance.sentRole.attends {
            // Off (or nobody chosen to attend, #3862) leaves the recording exactly as it was before attendance existed.
            // Attending, the hub hears of it at once.
            RecordingLibrary.shared.change(id) { $0.attendance = attendance }
            // Only a pick is remembered: a start with the default keeps the one picked last.
            if picked { MeetingAttendees.remember(attendance.key, title: attendance.sessionTitle) }
            RecordingSync.shared.attendanceChanged(id)
        }
        RecordingActivity.shared.refresh()
        Haptics.success()
        // Not awaited: an intent must not wait on the user answering the calendar prompt.
        if inForeground, meeting.eventID == nil, MeetingCalendar.canAsk {
            Task {
                guard await MeetingCalendar.requestAccess(), let found = await MeetingCalendar.meetingOffMain() else { return }
                RecordingLibrary.shared.change(id) { entry in
                    if entry.title.isEmpty { entry.title = found.title ?? "" }
                    entry.meeting = found
                }
            }
        }
        return .started
    }

    /// Stops the recording (the activity's Stop: `id` is the one it shows; a stale one stops nothing).
    func stop(recordingID: String?) {
        guard let id = capture.recordingID, recordingID == nil || recordingID == id.uuidString else { return }
        _ = capture.stop()
        finishInBackground(id)
    }

    /// The activity's Ask Persona: asks about what was just said.
    func ask(recordingID: String?) {
        guard let id = capture.recordingID, recordingID == nil || recordingID == id.uuidString,
              let entry = RecordingLibrary.shared.entry(id) else { return }
        MeetingAsk.shared.ask(entry)
        Haptics.tap()
    }

    /// Persona's part in the meeting being recorded changes (the page's picker; the activity's Turn Off): from now on the
    /// role applies, and the hub hears of it before any more lines or questions go. False when it would attend a meeting
    /// with nobody chosen to (no session named, the main chat not picked, none picked before that isn't the main chat):
    /// it stays off, never falling to the main chat (#3862), and the page opens the attendee picker.
    @discardableResult
    func setRole(_ role: MeetingAttendance, recordingID: String?) -> Bool {
        guard let id = capture.recordingID, recordingID == nil || recordingID == id.uuidString,
              let entry = RecordingLibrary.shared.entry(id), entry.isMeeting, entry.source == .phone else { return true }
        guard (entry.attendance?.sentRole ?? .off) != role else { return true }
        let at = capture.timeline
        // A room meeting (#3862) keeps its room, photos and private mark, and has no attending session to look up.
        if entry.attendance?.room == true {
            RecordingLibrary.shared.change(id) { e in
                guard var a = e.attendance else { return }
                a.set(role, at: at)
                e.attendance = a
            }
            capture.setPlaysAloud(role.speaks)
            if !role.speaks { MeetingSpeaker.shared.stop() }
            RecordingSync.shared.attendanceChanged(id)
            return true
        }
        // Turned on with nobody chosen yet: the session picked last that isn't the main chat, attending from now (what
        // came before is not sent); none, and it stays off until the owner picks.
        let lastPicked = role.attends ? MeetingAttendees.preferred(AppModel.shared) : nil
        guard let next = MeetingAttendees.turned(entry.attendance, to: role, at: at, lastPicked: lastPicked) else { return false }
        RecordingLibrary.shared.change(id) { $0.attendance = next }
        capture.setPlaysAloud(role.speaks)
        if !role.speaks { MeetingSpeaker.shared.stop() }
        RecordingSync.shared.attendanceChanged(id)
        return true
    }

    /// The meeting page's Private Meeting switch (#3862): `PUT …/private`; the mark the hub stored. Throws `APIError`.
    @discardableResult
    func setPrivate(_ value: Bool, recordingID: UUID) async throws -> Bool {
        try await MeetingConversation.shared.setPrivate(recordingID, value)
    }

    /// Another session attends the meeting being recorded, or the first one, picked before Persona is turned on (the
    /// picker `setRole`'s false opens): the role is left as it is. The main chat attends only this way (`mainPicked`).
    func setSession(_ candidate: MeetingAttendees.Candidate, recordingID: UUID) {
        guard capture.recordingID == recordingID, RecordingLibrary.shared.entry(recordingID)?.attendance?.room != true else { return }
        // Never the main chat on a hub with the meeting conversation, even for a meeting begun before it had one (#3862).
        if candidate.main, MeetingAttendees.conversation(AppModel.shared) { return }
        RecordingLibrary.shared.change(recordingID) { e in
            e.attendance = MeetingAttendees.choose(candidate, in: e.attendance ?? RecordingAttendance(role: .off))
        }
        MeetingAttendees.remember(candidate.main ? nil : candidate.key, title: candidate.title)
        RecordingSync.shared.attendanceChanged(recordingID)
    }

    func addMark(recordingID: String?) {
        guard let id = capture.recordingID, recordingID == nil || recordingID == id.uuidString else { return }
        capture.addMark()
        Haptics.tap()
    }

    /// Stopped while the app is away (the Lock Screen's Stop): the audio session ends, and with it the app's right to run.
    /// Hold on (30 s at most) for the last segment's text and its upload, so the minutes don't wait for the app's next launch.
    func finishInBackground(_ id: UUID) {
        guard !Self.inForeground else { return }
        AppDelegate.inBackground("recording-finish", done: {}) {
            for _ in 0..<30 {
                try? await Task.sleep(for: .seconds(1))
                let done = await MainActor.run { () -> Bool in
                    guard let entry = RecordingLibrary.shared.entry(id) else { return true }
                    let sync = RecordingSync.shared.status(entry)
                    return RecordingTranscription.shared.current != id && entry.transcribedAll && sync != .pending && sync != .uploading
                }
                if done { break }
            }
        }
    }

    /// The Recordings page in front: the iPhone's shell or the iPad's sidebar pushes it on the Persona page (the old tabs
    /// have no route there; the recording runs anyway). Launched by the home screen's menu or a Control, the app has no
    /// shell yet: it tries again for a few seconds.
    static func openRecordings() {
        RecordingLivePresenter.shared.open() // a sheet swiped away on the page already in front comes back (`recordingLiveSheet`)
        let model = AppModel.shared
        model.shellSettings = false
        model.selectedTab = .assistant
        if !route(model) {
            Task {
                for _ in 0..<10 {
                    try? await Task.sleep(for: .milliseconds(500))
                    if route(model) { return }
                }
            }
        }
    }

    private static func route(_ model: AppModel) -> Bool {
        if let shell = model.tabRouter as? ShellState {
            shell.show(.recordings)
            shell.drawer.close()
            return true
        }
        if let pad = model.tabRouter as? PadRouter {
            pad.show(.recordings)
            return true
        }
        return false
    }

    /// The system ended a call but the recording could not pick up again in the background: say so, since nothing else will.
    static func notifyPaused() {
        guard !inForeground else { return }
        let content = UNMutableNotificationContent()
        content.title = String(localized: "Recording Paused")
        content.body = String(localized: "A call or another app paused the recording. Open Persona to continue it.")
        content.sound = .default
        content.userInfo = [RecordingLink.notificationKey: true]
        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: "recording-paused", content: content, trigger: nil))
    }

    /// A watch recording's first segment or end arrived: one made during a calendar event is that meeting's, named after
    /// it (the watch has no calendar); a meeting started on the watch keeps being one without an event. Recordings
    /// already named or matched stay as they are.
    static func tagWatchRecording(_ id: UUID, at date: Date) {
        guard let entry = RecordingLibrary.shared.entry(id), entry.meeting?.eventID == nil, entry.meeting?.title == nil,
              entry.title.isEmpty, let meeting = MeetingCalendar.meeting(at: date) else { return }
        RecordingLibrary.shared.change(id) { entry in
            entry.title = meeting.title ?? ""
            entry.meeting = meeting
        }
    }
}
#endif
