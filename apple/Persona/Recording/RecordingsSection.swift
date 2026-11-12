#if os(iOS) || os(visionOS)
import SwiftUI
#if os(iOS)
import AVFoundation
#endif

/// Knowledge › Record (docs/apple/recording.md): start a recording, see this device's recordings (the watch's arrive here) with
/// where each one is (recording, transcribing, synced), open one for its transcript. The text goes to the knowledge base
/// by itself (`RecordingSync`); nothing is added to a composer.
struct RecordingsSection: View {
    #if os(iOS)
    @Environment(AppModel.self) private var model
    #endif
    @State private var library = RecordingLibrary.shared
    @State private var transcription = RecordingTranscription.shared
    @State private var sync = RecordingSync.shared
    @State private var capture = RecordingCapture.shared
    /// The recording a swipe asked to delete (its row shows the confirmation), and why a delete failed.
    @State private var pendingDelete: UUID?
    @State private var failure: String?

    var body: some View {
        // The recording being made is the now-recording card, not a row of the list below it.
        let capturing = capture.state == .recording || capture.state == .paused
        let recents = library.entries.filter { !(capturing && $0.id == capture.recordingID) }
        Section {
            RecordingCaptureControls()
        } footer: {
            // Read once, before a recording: not under the controls of one that runs.
            if capture.state == .idle {
                Text("Audio stays on this device. Its text syncs to Knowledge and is kept for \(sync.store.days) days.")
            }
        }
        Section {
            if recents.isEmpty, !capturing {
                EmptyStateView(symbol: Theme.Icon.recording, title: String(localized: "No Recordings Yet"),
                               message: String(localized: "Record meetings, dictation and on-site notes with your phone or Apple Watch; the transcribed text goes into Knowledge."))
                    .listRowBackground(Color.clear)
            }
            ForEach(recents) { entry in
                NavigationLink { RecordingDetailView(id: entry.id) } label: { RecordingRow(entry: entry) }
                    .accessibilityIdentifier("recording-row")
                    // Only the swiped row's dialog shows, so its popover points at that row.
                    .confirmationDialog("Delete This Recording?", isPresented: Binding(get: { pendingDelete == entry.id }, set: { if !$0, pendingDelete == entry.id { pendingDelete = nil } }),
                                        titleVisibility: .visible) {
                        Button("Delete", role: .destructive) { failure = RecordingDeletion.run(entry.id) }
                    } message: { Text("The audio on this device will be deleted now. Its text in Knowledge is removed after sync succeeds; if Knowledge is off or read-only, that deletion stays pending.") }
                    .swipeActions(edge: .trailing) {
                        // A recording the watch is still making has no Delete, as on its page.
                        if !entry.active {
                            // No `.destructive` role: it takes the row off the screen at once, before the confirmation (red comes from the tint).
                            Button { pendingDelete = entry.id } label: {
                                Label("Delete", systemImage: Theme.Icon.delete)
                            }
                            .tint(Theme.danger)
                            .accessibilityIdentifier("recording-row-delete")
                        }
                    }
            }
        } header: {
            if !recents.isEmpty {
                HStack(spacing: Theme.Space.sm) {
                    Text("Recent Recordings")
                    CountBadge(count: recents.count)
                }
                .alert("Couldn't Finish", isPresented: Binding(get: { failure != nil }, set: { if !$0 { failure = nil } })) {
                    Button("OK [Recordings Section de32e2]") { failure = nil }.lineLimit(1)
                } message: { Text(failure ?? "") }
            }
        } footer: {
            if let problem = sync.problem ?? library.error {
                Label(problem, systemImage: Theme.Icon.error).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("recording-sync-problem")
            }
        }
        .task {
            PhoneRecordingReceiver.shared.start()
            transcription.resumePending()
            sync.kick(reconcile: true)
            RecordingRollingKeeper.shared.schedule()
        }
    }
}

/// Deleting a recording, the one way for the list's swipe and the recording's page: stop its transcription, then delete it here
/// and (once sync allows) in Knowledge.
enum RecordingDeletion {
    /// nil: deleted; otherwise the words for the alert.
    @MainActor static func run(_ id: UUID) -> String? {
        RecordingTranscription.shared.cancel(id)
        do { try RecordingSync.shared.delete(id); return nil } catch { return String(localized: "The recording wasn't deleted. Try again later.") }
    }
}

/// One recording: where it came from, how long, and where it is on its way to the knowledge base.
struct RecordingRow: View {
    var entry: RecordingEntry

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(entry.isMeeting ? Theme.Icon.meeting : Theme.Icon.recording).font(Theme.Font.heading).foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.icon)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text(entry.displayTitle).font(Theme.Font.body).lineLimit(1)
                HStack(spacing: Theme.Space.xs) {
                    Text("\(entry.sourceTitle) · \(RecordingFormat.clock(entry.duration)) ·")
                    RecordingStatusText(entry: entry)
                }
                .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
    }
}

/// Recording / Transcribing / Synced…: the one word for where a recording is.
struct RecordingStatusText: View {
    var entry: RecordingEntry
    @State private var transcription = RecordingTranscription.shared
    @State private var sync = RecordingSync.shared

    var body: some View {
        let (text, tone) = Self.status(entry, transcription: transcription, sync: sync)
        Text(text).foregroundStyle(tone).accessibilityIdentifier("recording-status")
    }

    static func status(_ entry: RecordingEntry, transcription: RecordingTranscription, sync: RecordingSync) -> (String, Color) {
        if entry.active {
            if entry.isRolling { return (String(localized: "Rolling recording"), Theme.danger) }
            return (String(localized: "Recording [Recordings Section e35a14]"), Theme.danger)
        }
        if let error = transcription.errors[entry.id] { return (error, Theme.danger) }
        if transcription.current == entry.id { return (String(localized: "Transcribing"), Theme.accent) }
        if !entry.receivedAll { return (entry.expectedSegments == 0 ? String(localized: "No audio recorded") : String(localized: "Waiting for Apple Watch"), .secondary) }
        switch sync.status(entry) {
        case .synced: return (entry.transcribedAll ? String(localized: "Synced to Knowledge") : String(localized: "Partly synced"), Theme.positive)
        case .uploading: return (String(localized: "Syncing"), Theme.accent)
        case .rejected: return (String(localized: "Not accepted by Knowledge"), Theme.danger)
        case .expired: return (String(localized: "Past retention"), .secondary)
        case .pending: return (entry.transcribedAll ? String(localized: "Waiting to sync") : String(localized: "Waiting to transcribe"), .secondary)
        case .waiting: return (entry.transcribedAll ? String(localized: "No speech detected") : String(localized: "Waiting to transcribe"), .secondary)
        }
    }
}

/// Start a recording on this phone (the watch has its own). Meeting Recording leads: one prominent button, named after the
/// calendar event happening now (`MeetingRecorder`, docs/apple/recording.md「Meeting recordings」); the options for it are
/// one row each, and a plain Start Recording is the secondary button under them. While one runs, the controls are one row:
/// the now-recording card, which opens the live sheet (`RecordingLiveSheet`).
struct RecordingCaptureControls: View {
    @State private var capture = RecordingCapture.shared
    @State private var library = RecordingLibrary.shared
    @StoredValue(RecordingDefaults.localeKey) private var locale = RecordingDefaults.locale
    #if os(iOS)
    @StoredValue(RecordingInput.storageKey) private var inputRaw = RecordingInput.phone.rawValue
    @State private var hasBluetooth = false
    /// The calendar's meeting on now (read again each minute while nothing records).
    @State private var meetingNow: RecordingMeeting?
    @State private var calendarAskable = false
    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// Persona's part in the next meeting (#2981): the hub's default until one is picked here for it.
    @State private var pickedRole: MeetingAttendance?
    @State private var pickedAttendee: MeetingAttendees.Candidate?
    /// The next meeting's private mark (#3862): off unless turned on here, for that meeting only.
    @State private var pickedPrivate = false
    /// A start was tapped and recording has not begun (the calendar read, then the microphone): the hero stays, busy.
    @State private var startRequested = false
    #endif

    var body: some View {
        #if os(iOS) // Bluetooth input is iPhone-only (`RecordingInput`); visionOS records with its own microphone
        controls
            .task { // the Microphone picker shows only while a Bluetooth device (glasses, headset) is on the route
                refreshBluetooth()
                for await _ in NotificationCenter.default.notifications(named: AVAudioSession.routeChangeNotification) { refreshBluetooth() }
            }
            .task(id: capture.state == .idle) {
                while capture.state == .idle, !Task.isCancelled {
                    meetingNow = await MeetingCalendar.meetingOffMain()
                    calendarAskable = MeetingCalendar.canAsk
                    try? await Task.sleep(for: .seconds(60))
                }
            }
        #else
        controls
        #endif
    }

    /// The microphone a recording starts with: the Bluetooth one only when chosen and still connected.
    private var input: RecordingInput {
        #if os(iOS)
        hasBluetooth ? (RecordingInput(rawValue: inputRaw) ?? .phone) : .phone
        #else
        .phone
        #endif
    }

    @ViewBuilder private var controls: some View {
        #if os(iOS)
        if capture.state == .recording || capture.state == .paused {
            nowRecording
        } else {
            idleControls
        }
        banners
        #else
        legacyControls
        #endif
    }

    #if os(iOS)
    // MARK: Idle

    /// Where a start is: tapped and not yet recording, or in the capture's own `.starting`.
    private var preparing: RecordingPhase {
        RecordingPhase.derive(.init(startRequested: startRequested, capture: capture.state == .starting ? .starting : .idle))
    }

    @ViewBuilder private var idleControls: some View {
        let busy = preparing != .idle
        hero(busy: busy)
        if capture.state == .starting { startingRow }
        // The plain recording is a quiet second way to start, right under the first, before any option.
        Button { startNote() } label: {
            Label("Start Recording", systemImage: Theme.Icon.recording).font(Theme.Font.body)
                .lineLimit(size.isAccessibilitySize ? 3 : 1).minimumScaleFactor(0.6)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .buttonStyle(.borderless)
        .disabled(busy)
        .accessibilityIdentifier("recording-start")
        if MeetingAttendees.offered(model) {
            personaRow.disabled(busy)
            if MeetingAttendees.conversation(model) {
                // The meeting's own room conversation attends (#3862): no session to pick, and the private mark instead.
                privateRow.disabled(busy)
            } else if role.attends {
                // Never the main chat by default (#3862): Choose until one is picked here or picked before (not the main
                // chat), and the meeting starts with Persona off without one.
                let attendee = self.attendee
                NavigationLink { MeetingAttendeePicker(selected: attendee?.key) { pickedAttendee = $0 } } label: {
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        MeetingAttendeeLabel(title: attendee?.title, detail: attendee?.detail)
                        if attendee == nil {
                            Text("Choose who attends. Until then Persona stays out of the meeting.")
                                .font(Theme.Font.caption).foregroundStyle(.secondary)
                                .fixedSize(horizontal: false, vertical: true)
                                .accessibilityIdentifier("meeting-attendee-needed")
                        }
                    }
                }
                .disabled(busy)
                .accessibilityIdentifier("meeting-attendee")
            }
        }
        Picker("Recording Language", selection: $locale) {
            Text("Chinese").tag("zh-CN")
            Text("English").tag("en-US")
            Text("Japanese").tag("ja-JP")
        }.font(Theme.Font.body).disabled(busy)
        if hasBluetooth {
            Picker("Microphone", selection: $inputRaw) {
                ForEach(RecordingInput.allCases, id: \.rawValue) { Text($0.title).tag($0.rawValue) }
            }.font(Theme.Font.body).disabled(busy)
        }
    }

    /// Meeting Recording, and what it will be called: the calendar's meeting on now, or a way to let it read the calendar.
    /// While the start is in flight the button keeps its label, shows a spinner beside it and is disabled.
    @ViewBuilder private func hero(busy: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Button { startMeeting() } label: {
                HStack(spacing: Theme.Space.s) {
                    // At accessibility sizes the icon goes: the words never truncate, they scale down a little instead.
                    if size.isAccessibilitySize { Text("Meeting Recording") } else {
                        // White like the title: the symbol took the tint, drew in the button's own blue and pushed the title off-center.
                        Label("Meeting Recording", systemImage: Theme.Icon.recording).foregroundStyle(Theme.onSolid)
                    }
                    if busy { ProgressView() }
                }
                .font(Theme.Font.bodyStrong)
                .lineLimit(1)
                .minimumScaleFactor(0.6)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
            }
            .themedButton(prominent: true)
            .disabled(busy)
            .accessibilityIdentifier("recording-meeting-start")
            if let meeting = meetingNow, let title = meeting.title {
                Label(String(localized: "Calendar: \(title) · \(Self.span(meeting))"), systemImage: Theme.Icon.calendarEvent)
                    .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                    .accessibilityIdentifier("recording-meeting-now")
            } else if calendarAskable {
                Button {
                    Task {
                        _ = await MeetingCalendar.requestAccess()
                        calendarAskable = MeetingCalendar.canAsk
                        meetingNow = await MeetingCalendar.meetingOffMain()
                    }
                } label: {
                    Label("Name Recordings After Calendar Meetings", systemImage: Theme.Icon.calendarEvent).font(Theme.Font.caption)
                        .lineLimit(size.isAccessibilitySize ? 4 : 1).multilineTextAlignment(.leading)
                }
                .buttonStyle(.borderless)
            } else {
                Text("Named after the calendar event happening now, if there is one.").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, Theme.Space.xs)
    }

    /// The capture is `.starting` (the microphone): Cancel is possible only now. "Waiting for microphone access" is the
    /// detail line, not the whole block.
    private var startingRow: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.m) {
                Text("Starting…").font(Theme.Font.body)
                Spacer(minLength: Theme.Space.s)
                Button { capture.cancelStarting() } label: { Text("Cancel").lineLimit(1) }
                    .buttonStyle(.borderless)
                    .accessibilityIdentifier("recording-start-cancel")
            }
            Text("Waiting for microphone access").font(Theme.Font.caption).foregroundStyle(.secondary)
        }
        .transition(.opacity)
    }

    /// Persona's part: one menu, the same control and label as while recording, with what it does under it.
    private var personaRow: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Picker(selection: Binding(get: { role }, set: { pickedRole = $0 })) {
                ForEach(MeetingAttendance.allCases, id: \.self) { Text($0.title).tag($0) }
            } label: {
                Label("Persona", systemImage: Theme.Icon.persona)
            }
            .pickerStyle(.menu)
            .font(Theme.Font.body)
            .accessibilityIdentifier("meeting-attendance-role")
            if role.attends {
                Text(role.detail).font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-attendance-detail")
            }
        }
    }

    /// Private Meeting (#3862): whether this meeting's minutes may be used when Persona answers in later ones.
    private var privateRow: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Toggle(isOn: $pickedPrivate) {
                Label("Private Meeting", systemImage: Theme.Icon.privateMeeting)
            }
            .font(Theme.Font.body)
            .accessibilityIdentifier("meeting-private-start")
            Text("Its minutes are never used when Persona answers in later meetings.")
                .font(Theme.Font.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
    }

    /// The role shown and started with: the one picked here, else the hub's default; on a hub without the meeting
    /// conversation, Off while nobody is chosen to attend (the main chat never attends by default, #3862).
    private var role: MeetingAttendance {
        if let pickedRole { return pickedRole }
        guard MeetingAttendees.conversation(model) || attendee != nil else { return .off }
        return MeetingAttendees.defaultRole(model)
    }

    // MARK: Recording

    /// The now-recording card: one tappable row with the recording's mark, title, time and state; it opens the live sheet.
    @ViewBuilder private var nowRecording: some View {
        let entry = capture.recordingID.flatMap(library.entry)
        let paused = capture.state == .paused
        Button { RecordingLivePresenter.shared.open() } label: {
            HStack(spacing: Theme.Space.m) {
                RecordingStateDot(paused: paused).scaledFrame(width: Theme.Size.icon, height: Theme.Size.icon)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(entry?.displayTitle ?? String(localized: "Recording")).font(Theme.Font.bodyStrong)
                        .lineLimit(size.isAccessibilitySize ? 3 : 1)
                        .accessibilityIdentifier("recording-now-title")
                    Text(capture.interrupted ? String(localized: "Paused by a call") : paused ? String(localized: "Paused") : String(localized: "Recording [Recordings Section 5254d3]"))
                        .font(Theme.Font.secondary)
                        .foregroundStyle(paused ? Color.secondary : Palette.recordingRed)
                        .accessibilityIdentifier("recording-now-state")
                    if size.isAccessibilitySize { clock(paused: paused) }
                }
                Spacer(minLength: Theme.Space.s)
                if !size.isAccessibilitySize { clock(paused: paused) }
                ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.captionStrong).foregroundStyle(.tertiary)
            }
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityHint(Text("Opens the recording"))
        .accessibilityIdentifier("recording-now-card")
        .recordingMotion(reduced: reduceMotion, value: paused)
    }

    private func clock(paused: Bool) -> some View {
        RecordingClock(running: !paused, font: Theme.Font.bodyStrong)
            .foregroundStyle(paused ? Color.secondary : Color.primary)
    }

    // MARK: Problems

    /// What went wrong, or is worth knowing, as banners: no microphone permission has a way to Settings, not a dead button.
    @ViewBuilder private var banners: some View {
        if let error = capture.error {
            let denied = error == String(localized: "Allow Persona to use the microphone in Settings")
            Group {
                if denied {
                    NoticeBanner(text: error, tone: .danger) {
                        Button { openSettings() } label: { Text("Open Settings").lineLimit(1) }
                    }
                } else {
                    NoticeBanner(text: error, tone: .danger)
                }
            }
            .listRowBackground(Color.clear)
            .listRowInsets(EdgeInsets())
        }
        if let notice = capture.notice {
            NoticeBanner(text: notice, tone: .neutral)
                .listRowBackground(Color.clear)
                .listRowInsets(EdgeInsets())
        }
    }

    private func openSettings() {
        if let url = URL(string: UIApplication.openSettingsURLString) { UIApplication.shared.open(url) }
    }

    // MARK: Starting

    private func startMeeting() {
        let attendance = startAttendance
        pickedRole = nil; pickedAttendee = nil; pickedPrivate = false
        startRequested = true
        Task {
            await MeetingRecorder.shared.startMeeting(inForeground: true, openPage: false, attendance: attendance)
            startRequested = false
        }
    }

    private func startNote() {
        let chosen = input
        startRequested = true
        Task {
            await capture.start(locale: locale, input: chosen)
            startRequested = false
        }
    }

    /// Who attends the next meeting: the one picked here, else the one picked last unless that was the main chat; nil
    /// until one is chosen (#3862).
    private var attendee: MeetingAttendees.Candidate? { pickedAttendee ?? MeetingAttendees.preferred(model) }

    /// What Meeting Recording starts with: the role and session shown above it. A session picked here that can no longer
    /// attend (its computer went offline meanwhile) gives way to the preferred one; with nobody chosen Persona starts off,
    /// and the main chat attends only when picked here. With the meeting conversation (#3862) it is a room meeting
    /// whatever the role, off included: every upload carries `room` and the private mark.
    private var startAttendance: RecordingAttendance? {
        guard MeetingAttendees.offered(model) else { return nil }
        let role = self.role
        if MeetingAttendees.conversation(model) {
            return RecordingAttendance.room(role, private: pickedPrivate, photos: MeetingAttendees.photos(model))
        }
        let all = MeetingAttendees.candidates(model)
        guard role.attends,
              let chosen = pickedAttendee.flatMap({ p in all.first { $0.key == p.key } }) ?? MeetingAttendees.preferred(model)
        else { return RecordingAttendance(role: .off) }
        return MeetingAttendees.choose(chosen, in: RecordingAttendance(role: role))
    }

    /// 14:00–15:00.
    static func span(_ meeting: RecordingMeeting) -> String {
        guard let start = meeting.start, let end = meeting.end else { return "" }
        return (start..<max(end, start)).formatted(.interval.hour().minute())
    }

    private func refreshBluetooth() {
        hasBluetooth = RecordingInput.bluetoothConnected
    }
    #endif

    #if os(visionOS)
    // MARK: visionOS keeps its controls: the live sheet is the phone's.

    @ViewBuilder private var legacyControls: some View {
        if capture.state == .idle {
            Picker("Recording Language", selection: $locale) {
                Text("Chinese").tag("zh-CN")
                Text("English").tag("en-US")
                Text("Japanese").tag("ja-JP")
            }.font(Theme.Font.body)
            Button { Task { await capture.start(locale: locale, input: .phone) } } label: {
                Label("Start Recording", systemImage: Theme.Icon.recording).lineLimit(1)
            }.accessibilityIdentifier("recording-start")
        } else if capture.state == .starting {
            ProgressView(String(localized: "Waiting for microphone access")).font(Theme.Font.secondary)
            Button("Cancel") { capture.cancelStarting() }.lineLimit(1)
        } else {
            legacyRecordingNow
        }
        if let error = capture.error { Text(error).font(Theme.Font.secondary).foregroundStyle(Theme.danger) }
        if let notice = capture.notice { Text(notice).font(Theme.Font.secondary).foregroundStyle(.secondary) }
    }

    /// A recording in progress: what it is, its time and state, and pause / bookmark / stop.
    @ViewBuilder private var legacyRecordingNow: some View {
        let entry = capture.recordingID.flatMap(library.entry)
        if let entry {
            Text(entry.displayTitle).font(Theme.Font.heading).lineLimit(1)
                .accessibilityIdentifier("recording-now-title")
        }
        HStack(spacing: Theme.Space.s) {
            Text(RecordingFormat.clock(capture.elapsed)).font(Theme.Font.title.monospacedDigit())
            Spacer()
            Text(capture.state == .paused ? String(localized: "Paused") : String(localized: "Recording [Recordings Section 5254d3]"))
                .font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                .accessibilityIdentifier("recording-now-state")
        }
        HStack(spacing: Theme.Space.m) {
            Button { capture.togglePause() } label: {
                Label(capture.state == .paused ? String(localized: "Continue") : String(localized: "Pause"), systemImage: capture.state == .paused ? Theme.Icon.run : Theme.Icon.pause).lineLimit(1)
            }.themedButton()
            Button { capture.addMark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin).lineLimit(1) }.themedButton()
                .accessibilityIdentifier("recording-bookmark")
            Button { _ = capture.stop() } label: { Label("Stop [Car Play Voice Screen c7b24e]", systemImage: Theme.Icon.interrupt).lineLimit(1) }
                .themedButton().tint(Theme.danger).accessibilityIdentifier("recording-stop")
        }
    }
    #endif
}

/// Recordings as a page of its own (the drawer's Meeting Recording, the Control, Siri, the activity's tap): what Knowledge ›
/// Recordings shows, one step closer.
struct RecordingsPage: View {
    #if DEBUG
    @State private var debugRecording: UUID?
    private static var sampleOpened = false
    #endif

    var body: some View {
        List { RecordingsSection() }
            .listStyle(.insetGrouped)
            .themedPage()
            .navigationTitle(String(localized: "Recordings"))
            .navigationBarTitleDisplayMode(.inline)
            #if os(iOS)
            .recordingLiveSheet()
            #endif
            #if DEBUG
            // `-showMeetingSample minutes|pending`: the made-up finished meeting's page (screenshots, UI tests).
            .navigationDestination(item: $debugRecording) { RecordingDetailView(id: $0) }
            .onAppear {
                // Once per launch: back from it, the page appears again.
                if ["minutes", "pending"].contains(UserDefaults.standard.string(forKey: "showMeetingSample")), !Self.sampleOpened {
                    Self.sampleOpened = true
                    debugRecording = RecordingSamples.pastMeeting
                }
            }
            #endif
    }
}

/// One recording's transcript, with re-transcribing in another language and delete (here and in the knowledge base). A
/// meeting also has its private mark, and a room meeting the way to its conversation (#3862).
struct RecordingDetailView: View {
    var id: UUID
    #if os(iOS)
    @Environment(AppModel.self) private var model
    @State private var conversation = MeetingConversation.shared
    /// This page's hold on the meeting's state (`MeetingConversation.watch`), while it shows a meeting.
    @State private var owner = UUID()
    @State private var watching = false
    /// The private mark being changed: shown at once, the hub's answer follows.
    @State private var pendingPrivate: Bool?
    /// The hub said the recording isn't a meeting (`409 not_meeting`): it takes no private mark.
    @State private var notMeeting = false
    #endif
    @Environment(\.dismiss) private var dismiss
    @State private var library = RecordingLibrary.shared
    @State private var transcription = RecordingTranscription.shared
    @State private var sync = RecordingSync.shared
    @State private var keeper = RecordingRollingKeeper.shared
    @State private var deleting = false
    @State private var erasing = false
    @State private var failure: String?
    /// The transcript line a timestamp jumped to, lit for a moment.
    @State private var lit: Int?
    @State private var minutes = RecordingMinutesModel()
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Group {
            if let entry = library.entry(id) { ScrollViewReader { proxy in content(entry, proxy: proxy) } } else {
                EmptyStateView(symbol: Theme.Icon.recording, title: String(localized: "Recording Deleted"), message: String(localized: "Deleted from this device. Removal from Knowledge may still be pending."))
            }
        }
        .themedPage()
        // Each new revision (more text, the end) may bring or change the minutes; while they are on their way, ask again.
        .task(id: "\(library.entry(id)?.revision ?? 0)|\(minutes.round)") { await followMinutes() }
        .navigationTitle("Recording")
        .navigationBarTitleDisplayMode(.inline)
        .task {
            if library.entries.contains(where: \.isRolling) {
                #if os(iOS)
                await RecordingHealthSignals.requestAccess() // HealthKit bookmarks are iOS only
                #endif
                await keeper.run()
            }
        }
        #if os(iOS)
        .onTabVisibility(appear: { watchMeeting() }, disappear: { unwatchMeeting() })
        #endif
        .confirmationDialog("Delete This Recording?", isPresented: $deleting, titleVisibility: .visible) {
            Button("Delete", role: .destructive) {
                if let message = RecordingDeletion.run(id) { failure = message } else { dismiss() }
            }
        } message: { Text("The audio on this device will be deleted now. Its text in Knowledge is removed after sync succeeds; if Knowledge is off or read-only, that deletion stays pending.") }
        .confirmationDialog("Erase the last 10 minutes?", isPresented: $erasing, titleVisibility: .visible) {
            Button("Erase", role: .destructive) {
                let now = Date.now
                library.erase(id, from: now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60), to: now)
            }
        }
        .alert("Couldn't Finish", isPresented: Binding(get: { failure != nil }, set: { if !$0 { failure = nil } })) {
            Button("OK [Recordings Section de32e2]") { failure = nil }.lineLimit(1)
        } message: { Text(failure ?? "") }
    }

    private func content(_ entry: RecordingEntry, proxy: ScrollViewProxy) -> some View {
        List {
            Section {
                Text(entry.displayTitle).font(Theme.Font.heading)
                if let meeting = entry.meeting { RecordingMeetingFacts(meeting: meeting) }
                HStack(spacing: Theme.Space.xs) {
                    if entry.isRolling {
                        Text("\(entry.sourceTitle) · keeps \(Int(RecordingRolling.keepHours))h · \(retainedMinutes(entry)) min now ·")
                    } else {
                        Text("\(entry.sourceTitle) · \(RecordingFormat.clock(entry.duration)) ·")
                    }
                    RecordingStatusText(entry: entry)
                }
                .font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(1)
                if !entry.marks.isEmpty {
                    Label(String(localized: "Bookmarks \(ListFormatter.localizedString(byJoining: entry.marks.map(RecordingFormat.clock)))"), systemImage: Theme.Icon.pin)
                        .font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                if transcription.current == id { ProgressView().tint(Theme.accent) }
            }
            #if os(iOS)
            meetingSection(entry)
            #endif
            if entry.isRolling {
                rollingSections(entry)
            } else {
                RecordingMinutesSections(entry: entry, model: minutes, synced: sync.status(entry) == .synced) {
                    jump(to: $0, in: entry, proxy: proxy)
                }
                Section("Transcript") {
                    if entry.sentences.isEmpty {
                        Text("No transcript yet.").font(Theme.Font.secondary).foregroundStyle(.secondary)
                    }
                    let ids = Self.stableIDs(entry.sentences)
                    ForEach(Array(entry.sentences.enumerated()), id: \.offset) { index, sentence in
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            Text(RecordingFormat.clock(sentence.start)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
                            Text(sentence.text).font(Theme.Font.body).textSelection(.enabled)
                        }
                        .id(ids[index])
                        .listRowBackground(lit == index ? Theme.accentTint : nil)
                        .accessibilityIdentifier(lit == index ? "recording-sentence-lit" : "recording-sentence")
                    }
                }
            }
            if !entry.active {
                Section {
                    if !entry.isRolling {
                        Picker("Transcription Language", selection: Binding(get: { entry.locale }, set: { transcription.setLocale(id, $0) })) {
                            Text("Chinese").tag("zh-CN"); Text("English").tag("en-US"); Text("Japanese").tag("ja-JP")
                        }.disabled(transcription.current == id)
                        if transcription.current == id {
                            Button("Stop Transcribing") { transcription.cancel(id) }.lineLimit(1)
                        } else if !entry.transcribedAll {
                            Button("Resume Transcribing") { transcription.enqueue(id) }.lineLimit(1)
                        }
                    } else {
                        Button(role: .destructive) { erasing = true } label: {
                            Label("Erase last 10 minutes", systemImage: Theme.Icon.delete).lineLimit(1)
                        }
                    }
                    Button("Delete Recording", role: .destructive) { deleting = true }.lineLimit(1)
                        .accessibilityIdentifier("recording-delete")
                }
            }
            // The retention text is read once, at the end: not between the header and the minutes.
            Section {
                EmptyView()
            } footer: {
                Text(entry.isRolling
                    ? String(localized: "Rolling keeps the last \(Int(RecordingRolling.keepHours)) hours. Pull a range to transcribe it; text syncs into Knowledge. Audio stays on this device.")
                    : String(localized: "Only the text and timestamps go into Knowledge; the original audio isn't uploaded. After the retention period (\(sync.store.days) days) it's cleared from this device and Knowledge."))
            }
        }
        // The minutes unfold when they arrive (Reduce Motion: at once).
        .animation(reduceMotion ? nil : Theme.Motion.reveal, value: minutes.state)
    }

    private func followMinutes() async {
        while !Task.isCancelled, let entry = library.entry(id) {
            await minutes.load(entry, synced: sync.status(entry) == .synced)
            guard minutes.waiting else { return }
            try? await Task.sleep(for: .seconds(15))
        }
    }

    #if os(iOS)
    // MARK: The meeting conversation (#3862)

    /// Private Meeting for any meeting (whatever recorded it: a meeting with no mark counts as private until it is turned
    /// off), and Meeting Conversation for a room meeting, or one the hub says has a room or a follow-up. Only with a hub
    /// that lists the meeting conversation.
    @ViewBuilder private func meetingSection(_ entry: RecordingEntry) -> some View {
        let meeting = conversation.meeting(id)
        let showsPrivate = entry.isMeeting && !notMeeting
        let showsThread = entry.roomMeeting || meeting.followup != nil
            || meeting.room.map { $0.known != MeetingRoom.State.none } == true
        if MeetingAttendees.conversation(model), showsPrivate || showsThread {
            Section {
                if showsPrivate {
                    Toggle(isOn: Binding(get: { pendingPrivate ?? MeetingPage.isPrivate(entry, meeting) }, set: { setPrivate($0) })) {
                        Label {
                            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                                Text("Private Meeting").font(Theme.Font.body)
                                Text("Its minutes are never used when Persona answers in later meetings.")
                                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                                    .fixedSize(horizontal: false, vertical: true)
                            }
                        } icon: {
                            ThemedIcon(Theme.Icon.privateMeeting)
                        }
                    }
                    .disabled(pendingPrivate != nil)
                    .accessibilityIdentifier("recording-private")
                }
                if showsThread {
                    NavigationLink { MeetingPage(id: id, mode: .after) } label: {
                        // Drawn as the Private Meeting row beside it: in the style's icon language.
                        Label { Text("Meeting Conversation").lineLimit(1) } icon: { ThemedIcon(Theme.Icon.meeting) }
                    }
                    .accessibilityIdentifier("recording-meeting-conversation")
                }
            }
        }
    }

    /// Reads the meeting's private mark and room while the page shows a meeting (`GET …/live`, then `meeting_live`).
    private func watchMeeting() {
        guard !watching, library.entry(id)?.isMeeting == true, MeetingAttendees.conversation(model) else { return }
        watching = true
        conversation.watch(id, by: owner)
    }

    private func unwatchMeeting() {
        guard watching else { return }
        watching = false
        conversation.unwatch(id, by: owner)
    }

    /// `PUT …/private`; a recording the hub doesn't count as a meeting loses the row instead of showing an error.
    private func setPrivate(_ value: Bool) {
        pendingPrivate = value
        Task {
            do {
                try await conversation.setPrivate(id, value)
            } catch let error as APIError where error.status == 409 && error.code == "not_meeting" {
                notMeeting = true
            } catch {
                failure = MeetingConversation.privateMessage(error)
            }
            pendingPrivate = nil
        }
    }
    #endif

    /// A transcript line's identity: where it starts, in milliseconds, so a row keeps its identity when text is added before
    /// or after it (a line at the same start gets a number after it).
    static func stableIDs(_ sentences: [RecordingSentence]) -> [String] {
        var seen: [Int: Int] = [:]
        return sentences.map { sentence in
            let millis = Int((sentence.start * 1000).rounded())
            let count = seen[millis, default: 0]
            seen[millis] = count + 1
            return count == 0 ? "sentence-\(millis)" : "sentence-\(millis)-\(count)"
        }
    }

    /// To the transcript line said at `seconds` (a timestamp in the minutes), lit for two seconds.
    private func jump(to seconds: Double, in entry: RecordingEntry, proxy: ScrollViewProxy) {
        guard let index = RecordingMinutes.sentenceIndex(at: seconds, in: entry.sentences) else { return }
        let ids = Self.stableIDs(entry.sentences)
        withAnimation(reduceMotion ? nil : Theme.Motion.layout) { proxy.scrollTo(ids[index], anchor: .center) }
        lit = index
        Task {
            try? await Task.sleep(for: .seconds(2))
            if lit == index { withAnimation(Theme.Motion.standard) { lit = nil } }
        }
    }

    @ViewBuilder private func rollingSections(_ entry: RecordingEntry) -> some View {
        Section {
            HStack(spacing: Theme.Space.s) {
                ForEach(RecordingRolling.pullMinutes, id: \.self) { minutes in
                    Button("\(minutes) min") { transcription.pull(id, minutes: minutes) }
                        .themedButton().lineLimit(1)
                }
            }
        } header: {
            Text("Pull")
        } footer: {
            Text("Rolling recordings aren't transcribed until you pull a range.")
        }
        if let range = transcription.pulls[id] {
            let picked = entry.segments(from: range.lowerBound, to: range.upperBound)
            let lines = entry.pulledSentences(from: range.lowerBound, to: range.upperBound)
            let state = entry.pullState(from: range.lowerBound, to: range.upperBound,
                                        transcribing: transcription.current == id, error: transcription.errors[id])
            Section {
                if case .failed = state, let message = state.message {
                    Text(message).font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                    Button { transcription.pull(id, range: range) } label: {
                        Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1)
                    }.accessibilityIdentifier("recording-pull-retry")
                } else if lines.isEmpty, let message = state.message {
                    Text(message).font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                ForEach(Array(lines.enumerated()), id: \.offset) { _, line in
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        Text(line.0.formatted(date: .omitted, time: .standard)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
                        Text(line.1).font(Theme.Font.body).textSelection(.enabled)
                    }
                }
                if !picked.isEmpty {
                    Button { saveAsClip(picked.map(\.index)) } label: {
                        Label("Save as recording", systemImage: Theme.Icon.save).lineLimit(1)
                    }
                }
            } header: {
                Text("Pulled range")
            }
        }
        let runs = keptRuns(entry)
        if !runs.isEmpty {
            Section {
                ForEach(runs) { run in
                    Button { transcription.pull(id, range: run.from...run.to) } label: {
                        HStack(spacing: Theme.Space.m) {
                            Text("\(run.from.formatted(date: .omitted, time: .shortened))–\(run.to.formatted(date: .omitted, time: .shortened))")
                                .font(Theme.Font.body.monospacedDigit()).lineLimit(1)
                            Spacer(minLength: Theme.Space.s)
                            Text(run.keep.title).font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                }
            } header: {
                Text("Kept segments")
            } footer: {
                Text("Bookmarks and health spikes keep a few minutes around them for 7 days.")
            }
        }
    }

    private func retainedMinutes(_ entry: RecordingEntry) -> Int {
        Int((entry.segments.map(\.duration).reduce(0, +) / 60).rounded())
    }

    private func keptRuns(_ entry: RecordingEntry) -> [RecordingKeptRun] {
        var runs: [RecordingKeptRun] = []
        for segment in entry.segments.sorted(by: { $0.index < $1.index }) {
            guard let keep = segment.kept, let from = segment.startedAt, let to = segment.endedAt else { continue }
            if let last = runs.last, last.keep.reason == keep.reason, last.endIndex + 1 == segment.index {
                runs[runs.count - 1].endIndex = segment.index
                runs[runs.count - 1].to = to
            } else {
                runs.append(RecordingKeptRun(id: segment.index, endIndex: segment.index, from: from, to: to, keep: keep))
            }
        }
        return runs
    }

    private func saveAsClip(_ indexes: [Int]) {
        do {
            _ = try library.saveClip(from: id, indexes: indexes)
        } catch {
            failure = String(localized: "Couldn't save a clip from those segments.")
        }
    }
}

/// A meeting recording's meeting: when it was booked, where, and who was invited (from the calendar event it is named after).
struct RecordingMeetingFacts: View {
    var meeting: RecordingMeeting

    var body: some View {
        // When and where on one line.
        let when: String? = {
            guard let start = meeting.start, let end = meeting.end else { return nil }
            return RecordingFormat.span(start, end)
        }()
        let line = [when, meeting.location].compactMap { $0 }.joined(separator: " · ")
        if !line.isEmpty {
            Label(line, systemImage: when == nil ? Theme.Icon.place : Theme.Icon.calendarEvent)
                .font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
        }
        if !meeting.attendees.isEmpty {
            Label(ListFormatter.localizedString(byJoining: meeting.attendees), systemImage: Theme.Icon.people)
                .font(Theme.Font.secondary).foregroundStyle(.secondary).lineLimit(2)
        }
    }
}

private struct RecordingKeptRun: Identifiable {
    var id: Int
    var endIndex: Int
    var from: Date
    var to: Date
    var keep: RecordingKeep
}
#endif
