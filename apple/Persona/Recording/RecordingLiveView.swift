#if os(iOS)
import SwiftUI

/// What the live sheet shows while a meeting records (docs/design/proposals/meeting-recording.md section 4C): the title and
/// the state, the time, a level meter, Persona's part, and one timeline of what is heard, bookmarked and asked, with the bar
/// that keeps Ask Persona, Pause, Stop and Bookmark under the thumb. At accessibility sizes everything above the bar scrolls
/// as one view and the bar stays pinned.
struct RecordingLiveView: View {
    var id: UUID
    var exchanges: [MeetingAsk.Exchange]
    var hide: () -> Void

    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var capture = RecordingCapture.shared
    @State private var library = RecordingLibrary.shared
    @StoredValue(RecordingInput.storageKey) private var inputRaw = RecordingInput.phone.rawValue
    @State private var items: [RecordingTimeline.Item] = []
    @State private var follow = RecordingFollow()
    @State private var following = true
    @State private var position = ScrollPosition(edge: .bottom)
    @State private var showAttendee = false
    /// The role Turn on Persona or the role menu asked for while nobody was chosen to attend (#3862): it goes on once the
    /// picker names someone; closing the picker without a pick leaves Persona off.
    @State private var pendingRole: MeetingAttendance?

    private var paused: Bool { capture.state == .paused }

    var body: some View {
        if let entry = library.entry(id) {
            layout(entry)
                .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) {
                    RecordingLiveBar(entry: entry, paused: paused)
                }
                .navigationDestination(isPresented: $showAttendee) {
                    // The main chat shows as chosen only when it was picked for this meeting, never by default (#3862).
                    MeetingAttendeePicker(selected: entry.attendance.flatMap { $0.key ?? ($0.mainPicked ? model.assistantMain : nil) }) {
                        MeetingRecorder.shared.setSession($0, recordingID: id)
                        if let role = pendingRole { MeetingRecorder.shared.setRole(role, recordingID: id.uuidString) }
                        pendingRole = nil
                    }
                }
                .onChange(of: showAttendee) { _, shown in if !shown { pendingRole = nil } }
                .onChange(of: exchanges.compactMap(\.answer)) { old, new in
                    // An answer arriving on screen: announced, with no haptic while the microphone is open.
                    if new.count > old.count, let latest = new.last { AccessibilityNotification.Announcement(latest).post() }
                }
        }
    }

    // MARK: Layout

    @ViewBuilder private func layout(_ entry: RecordingEntry) -> some View {
        if size.isAccessibilitySize {
            // A fixed one-line header (the mark, the time and Hide) stays above whatever scrolls, so the state and the
            // time are on screen at any text size.
            VStack(spacing: Theme.Space.s) {
                compactHeader
                scroller {
                    VStack(alignment: .leading, spacing: Theme.Space.m) {
                        VStack(alignment: .leading, spacing: Theme.Space.s) {
                            titleRow(entry)
                            stateLine(entry)
                            interruptedNote
                        }
                        .padding(.horizontal, Theme.Space.page)
                        strip(entry)
                        rows(entry)
                    }
                }
            }
        } else {
            VStack(spacing: Theme.Space.m) {
                head(entry)
                strip(entry)
                scroller { rows(entry) }
            }
        }
    }

    /// The accessibility-size header: the mark, the time and Hide, on one line.
    private var compactHeader: some View {
        HStack(spacing: Theme.Space.m) {
            RecordingStateDot(paused: paused)
            RecordingClock(running: capture.state == .recording, font: Theme.Font.title)
                .foregroundStyle(paused ? Color.secondary : Color.primary)
                .dynamicTypeSize(...Theme.Recording.timerLargestSize)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text("Recording time"))
                .accessibilityValue(RecordingClock.spoken(capture.timeline))
                .accessibilityIdentifier("recording-live-timer")
            Spacer(minLength: Theme.Space.s)
            RecordingSheetCloseButton(title: String(localized: "Hide"), identifier: "recording-live-hide") { hide() }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.top, Theme.Space.s)
    }

    @ViewBuilder private var interruptedNote: some View {
        if capture.interrupted {
            Text("It carries on by itself when the call ends, or when you come back to Persona.")
                .font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }

    private func head(_ entry: RecordingEntry) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            titleRow(entry)
            stateLine(entry)
            timer
            // A call's note takes the flat meter's place, so entering and leaving the pause does not move the page.
            ZStack {
                RecordingLevelMeter(level: { capture.inputLevel }, paused: paused)
                    .opacity(capture.interrupted ? 0 : 1)
                interruptedNote.multilineTextAlignment(.center)
            }
            .frame(maxWidth: .infinity)
            .recordingMotion(reduced: reduceMotion, value: capture.interrupted)
            if RecordingInput(rawValue: inputRaw) == .bluetooth, RecordingInput.bluetoothConnected, let name = capture.inputName {
                Text("Microphone: \(name)").font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
            }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.top, Theme.Space.s)
    }

    private func titleRow(_ entry: RecordingEntry) -> some View {
        let title = Text(entry.displayTitle).font(Theme.Font.title).lineLimit(2)
            .accessibilityAddTraits(.isHeader)
            .accessibilityIdentifier("recording-now-title")
        return HStack(alignment: .center, spacing: Theme.Space.m) {
            title.frame(maxWidth: .infinity, alignment: .leading)
            if !size.isAccessibilitySize {
                RecordingSheetCloseButton(title: String(localized: "Hide"), identifier: "recording-live-hide") { hide() }
            }
        }
    }

    /// The chip and the state: never the color alone, the dot becomes a pause glyph and the word changes.
    private func stateLine(_ entry: RecordingEntry) -> some View {
        let word = Text(capture.interrupted ? String(localized: "Paused by a call") : paused ? String(localized: "Paused")
                        : String(localized: "Recording [Recordings Section 5254d3]"))
            .font(Theme.Font.secondaryStrong)
            .foregroundStyle(paused ? Color.secondary : Palette.recordingRed)
            .contentTransition(.opacity)
            .accessibilityIdentifier("recording-now-state")
        let state = HStack(spacing: Theme.Space.s) { RecordingStateDot(paused: paused); word }
            .accessibilityElement(children: .combine)
        let chip = Chip(text: String(localized: "Meeting"), symbol: Theme.Icon.meeting, look: .tint(Theme.accent))
        return Group {
            if size.isAccessibilitySize {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    if entry.isMeeting { chip }
                    state
                }
            } else {
                HStack(spacing: Theme.Space.m) {
                    if entry.isMeeting { chip }
                    state
                }
            }
        }
        .recordingMotion(reduced: reduceMotion, value: paused)
    }

    /// The time, large. Read by VoiceOver as "Recording time, 2 minutes, 5 seconds", refreshed once a minute: the
    /// system-drawn text does not redraw the page, so the spoken value does it on its own slow clock.
    private var timer: some View {
        TimelineView(.periodic(from: .now, by: Theme.Recording.spokenTimeRefresh)) { _ in
            RecordingClock(running: capture.state == .recording, font: Theme.Font.display)
                .foregroundStyle(paused ? Color.secondary : Color.primary)
                .dynamicTypeSize(...Theme.Recording.timerLargestSize)
                .frame(maxWidth: .infinity)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text("Recording time"))
                .accessibilityValue(RecordingClock.spoken(capture.timeline))
                .accessibilityIdentifier("recording-live-timer")
        }
    }

    @ViewBuilder private func strip(_ entry: RecordingEntry) -> some View {
        if entry.isMeeting, entry.source == .phone, entry.attendance != nil || MeetingAttendees.offered(model) {
            RecordingPersonaStrip(entry: entry, showAttendee: $showAttendee, pendingRole: $pendingRole)
                .padding(.horizontal, Theme.Space.page)
        }
    }

    // MARK: The timeline

    private func rows(_ entry: RecordingEntry) -> some View {
        RecordingTimelineRows(entry: entry, exchanges: exchanges, paused: paused, items: $items)
            .padding(.horizontal, Theme.Space.page)
            .padding(.vertical, Theme.Space.s)
    }

    /// A scroll view that follows its newest end until the reader's finger takes it (`larkFollowsEnd`, the pattern of the
    /// Feishu chat), and a Jump to Latest button while it is away.
    private func scroller<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        ScrollView { content() }
            .scrollPosition($position)
            .larkFollowsEnd($position, following: $following, active: true)
            .onChange(of: following) { _, now in
                if now { follow.attach() } else { follow.detach(items: items) }
            }
            .overlay(alignment: .bottomTrailing) {
                if !following {
                    RecordingJumpButton(unread: follow.unread(in: items)) { following = true }
                        .padding(.trailing, Theme.Space.page)
                        .padding(.bottom, Theme.Space.s)
                        .transition(reduceMotion ? .opacity : .opacity.combined(with: .offset(y: Theme.Recording.arrivalOffset)))
                }
            }
            .animation(reduceMotion ? nil : (following ? Theme.Motion.conceal : Theme.Motion.reveal), value: following)
    }
}

// MARK: Persona's part

/// One row: whether Persona is listening, taking part or off, with a menu for the role and for who attends. A problem
/// shows under it. Turning Persona on with nobody chosen to attend opens the attendee picker first (#3862: the main chat
/// is never the default).
struct RecordingPersonaStrip: View {
    var entry: RecordingEntry
    @Binding var showAttendee: Bool
    /// The role to turn on once the picker names who attends.
    @Binding var pendingRole: MeetingAttendance?
    @Environment(\.dynamicTypeSize) private var size
    @State private var live = MeetingLive.shared
    @State private var sync = RecordingSync.shared

    private var attendance: RecordingAttendance { entry.attendance ?? RecordingAttendance(role: .off) }
    /// The role as the hub has it: off while nobody is chosen to attend.
    private var role: MeetingAttendance { attendance.sentRole }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(spacing: Theme.Space.m) {
                label
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("meeting-attending")
                Spacer(minLength: Theme.Space.s)
                menu
            }
            .padding(.leading, Theme.Space.m)
            .padding(.vertical, Theme.Space.xs)
            .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            if let problem = MeetingAttendanceProblem.text(entry, live: live, sync: sync) {
                NoticeBanner(text: problem, tone: .caution)
                    .accessibilityIdentifier("meeting-attendance-problem")
            }
        }
    }

    /// Off is an invitation, not a status: tapping it turns Persona on as a listener.
    @ViewBuilder private var label: some View {
        let content = Label {
            Text(word).font(Theme.Font.bodyStrong).lineLimit(size.isAccessibilitySize ? 3 : 2)
        } icon: {
            ThemedIcon(role.attends ? Theme.Icon.voiceListening : Theme.Icon.attendanceOff).foregroundStyle(Theme.ai)
        }
        if role == .off {
            Button { turn(.listener) } label: {
                content.foregroundStyle(Theme.accent).frame(maxWidth: .infinity, alignment: .leading).contentShape(.rect)
            }
            .buttonStyle(.plain)
        } else {
            content
        }
    }

    private var word: String {
        switch role {
        case .off: String(localized: "Turn on Persona")
        case .listener: String(localized: "Persona is listening")
        case .participant: String(localized: "Persona is taking part")
        }
    }

    private var menu: some View {
        Menu {
            Picker(selection: Binding(get: { role }, set: { turn($0) })) {
                ForEach(MeetingAttendance.allCases, id: \.self) { Text($0.title).tag($0) }
            } label: { Text("Persona") }
            if role.attends {
                Button { showAttendee = true } label: { Label("Who Attends…", systemImage: Theme.Icon.people) }
                    .accessibilityIdentifier("meeting-attendee")
            }
        } label: {
            ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading).foregroundStyle(Theme.accent)
                .tapTargetFrame()
                .contentShape(.rect)
        }
        .accessibilityLabel(Text("Persona"))
        .accessibilityIdentifier("meeting-attendance-now")
    }

    /// Sets the role; with nobody chosen to attend it stays off and the picker opens, the role going on once one is picked.
    private func turn(_ next: MeetingAttendance) {
        guard !MeetingRecorder.shared.setRole(next, recordingID: entry.id.uuidString) else { return }
        pendingRole = next
        showAttendee = true
    }
}
#endif
