#if os(iOS)
import SwiftUI

/// The meeting page (#3862, docs/assistant/meeting-minutes.md「The meeting card」): a room meeting's one page. While it
/// records it is the live sheet's content (`RecordingLiveSheet`): the title, the state and the time, Persona's part, the
/// Live box with the newest lines, the thread (Persona, or Just Me), and the bar with the composer, Pause, Bookmark and
/// Stop under the thumb. After Stop the same page goes on as the thread (`.after`): where the minutes are (kept above the
/// thread), **After the Meeting** and the follow-up's composer; a past meeting's page is that one, pushed from its
/// recording. An ordinary recording and a #3746 meeting keep `RecordingLiveView` and `RecordingWrapUp`.
struct MeetingPage: View {
    enum Mode: Equatable { case live, after }

    var id: UUID
    var mode: Mode
    /// Live mode: the exchanges `RecordingLiveSheet` mirrors while capture runs (`MeetingAsk` clears them at Stop).
    var exchanges: [MeetingAsk.Exchange] = []
    /// After mode, shown in the sheet: what ended the recording badly (`RecordingSheetMove.stopError`).
    var captureError: String? = nil
    /// Hide (live) / Done (after, in the sheet); nil when pushed (the navigation bar's back).
    var close: (() -> Void)? = nil

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var capture = RecordingCapture.shared
    @State private var library = RecordingLibrary.shared
    @State private var conversation = MeetingConversation.shared
    @State private var sync = RecordingSync.shared
    @State private var live = MeetingLive.shared
    @State private var speaker = MeetingSpeaker.shared
    /// This page's hold on the meeting's state (`MeetingConversation.watch`): the live page fading out and the after page
    /// fading in each hold their own, so neither ends the other's.
    @State private var owner = UUID()
    @State private var watching: UUID?
    /// The composer writes to the main chat, privately (live only): the thread shows Just Me instead of Persona.
    @State private var justMe = false
    @State private var shown = MeetingThreadView.Shown()
    /// The thread's rows when the reader scrolled away: what came after them is Jump to Latest's count.
    @State private var seen: Set<String> = []
    @State private var following = true
    @State private var position = ScrollPosition(edge: .bottom)
    /// The private mark being changed (shown at once, the hub's answer follows), and why a change failed.
    @State private var pendingPrivate: Bool?
    @State private var privateProblem: String?
    @State private var minutes = RecordingMinutesModel()
    /// The first answer about the minutes is in: until then the status card says nothing about them.
    @State private var minutesAsked = false
    @State private var opening = false
    /// The composer's field has the keyboard: the top of the page steps aside as the bar's controls do, so the thread
    /// keeps its height.
    @FocusState private var writing: Bool
    /// At accessibility sizes, why the composer takes no words (`MeetingPageBar`): shown at the end of what scrolls, so
    /// the bar stays two rows. Likewise that some picked photos couldn't be read (#3862 phase 3), beside it.
    @State private var notice: String?
    @State private var photoNotice: String?

    private var paused: Bool { capture.state == .paused }

    var body: some View {
        Group {
            if let entry = library.entry(id) {
                switch mode {
                case .live: livePage(entry)
                case .after: afterPage(entry)
                }
            } else if mode == .after {
                EmptyStateView(symbol: Theme.Icon.recording, title: String(localized: "Recording Deleted"),
                               message: String(localized: "Deleted from this device. Removal from Knowledge may still be pending."))
            }
        }
        .themedGroupedPage()
        .navigationTitle(close == nil ? String(localized: "Meeting") : "")
        .navigationBarTitleDisplayMode(.inline)
        // `meeting_live` and reconnects read the meeting again only while a page shows it (or it records here).
        .onTabVisibility(appear: { watch(id) }, disappear: { unwatch() })
        .onChange(of: id) { _, new in
            guard watching != nil else { return }
            unwatch()
            watch(new)
        }
    }

    private func watch(_ recording: UUID) {
        guard watching == nil else { return }
        watching = recording
        conversation.watch(recording, by: owner)
    }

    private func unwatch() {
        guard let watching else { return }
        conversation.unwatch(watching, by: owner)
        self.watching = nil
    }

    // MARK: While the meeting records

    @ViewBuilder private func livePage(_ entry: RecordingEntry) -> some View {
        liveLayout(entry)
            .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) {
                VStack(spacing: Theme.Space.flush) {
                    if showsStopSpeaking { stopSpeaking }
                    MeetingPageBar(entry: entry, mode: .live, paused: paused, justMe: $justMe, focus: $writing, notice: $notice,
                                   photoNotice: $photoNotice)
                }
            }
            .onChange(of: exchanges.compactMap(\.answer)) { old, new in
                // A room answer arriving on screen: announced, with no haptic while the microphone is open. Just Me's
                // answers never are: VoiceOver would say them out loud to the room.
                if new.count > old.count, let latest = new.last { AccessibilityNotification.Announcement(latest).post() }
            }
    }

    @ViewBuilder private func liveLayout(_ entry: RecordingEntry) -> some View {
        if size.isAccessibilitySize {
            // A fixed one-line header (the mark, the time, the menu and Hide) stays above whatever scrolls, so the state
            // and the time are on screen at any text size; the rest scrolls as one.
            VStack(spacing: Theme.Space.s) {
                compactHeader(entry)
                scroller {
                    VStack(alignment: .leading, spacing: Theme.Space.m) {
                        VStack(alignment: .leading, spacing: Theme.Space.s) {
                            titleRow(entry)
                            stateLine(clock: false)
                            subtitle(entry)
                        }
                        problems(entry)
                        MeetingLiveBox(entry: entry, exchanges: exchanges, folded: false)
                        threadHeader(entry)
                        thread(entry)
                        noticeBanner
                    }
                    .padding(.horizontal, Theme.Space.page)
                    .padding(.vertical, Theme.Space.s)
                }
            }
        } else {
            // While the field has the keyboard the subtitle steps aside and the Live box keeps only its newest line: the
            // keyboard takes about half the screen, and the thread under them would get none of it. The structure stays
            // the same, so the thread's scroll view keeps its place.
            VStack(alignment: .leading, spacing: Theme.Space.m) {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    titleRow(entry)
                    stateLine(clock: true)
                    if !writing { subtitle(entry) }
                }
                .padding(.horizontal, Theme.Space.page)
                .padding(.top, Theme.Space.s)
                problems(entry)
                MeetingLiveBox(entry: entry, exchanges: exchanges, folded: writing)
                    .padding(.horizontal, Theme.Space.page)
                threadHeader(entry)
                    .padding(.horizontal, Theme.Space.page)
                scroller {
                    thread(entry)
                        .padding(.horizontal, Theme.Space.page)
                        .padding(.vertical, Theme.Space.s)
                }
            }
            .animation(reduceMotion ? nil : Theme.Motion.state, value: writing)
        }
    }

    /// Why the composer takes no words, and picked photos that couldn't be read, at accessibility sizes (the bar says
    /// them at the others).
    @ViewBuilder private var noticeBanner: some View {
        if let notice {
            NoticeBanner(text: notice, tone: .neutral)
                .accessibilityIdentifier("meeting-ask-notice")
        }
        if let photoNotice {
            NoticeBanner(text: photoNotice, tone: .danger)
                .accessibilityIdentifier("meeting-photo-problem")
        }
    }

    /// The accessibility-size header: the mark, the time, the menu and Hide, on one line.
    private func compactHeader(_ entry: RecordingEntry) -> some View {
        HStack(spacing: Theme.Space.m) {
            RecordingStateDot(paused: paused)
            timer(font: Theme.Font.title)
            Spacer(minLength: Theme.Space.s)
            pageMenu(entry)
            hideButton
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.top, Theme.Space.s)
    }

    private func titleRow(_ entry: RecordingEntry) -> some View {
        HStack(alignment: .center, spacing: Theme.Space.s) {
            Text(entry.displayTitle).font(Theme.Font.title).lineLimit(Theme.Recording.pageTitleLines)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("recording-now-title")
                .frame(maxWidth: .infinity, alignment: .leading)
            if !size.isAccessibilitySize {
                pageMenu(entry)
                hideButton
            }
        }
    }

    @ViewBuilder private var hideButton: some View {
        if let close {
            RecordingSheetCloseButton(title: String(localized: "Hide"), identifier: "recording-live-hide") { close() }
        }
    }

    /// The mark, the time and the state in words: never the color alone (the dot becomes a pause glyph, the word changes).
    private func stateLine(clock: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.s) {
                if clock {
                    RecordingStateDot(paused: paused)
                    timer(font: Theme.Font.bodyStrong)
                }
                Text(stateWord)
                    .font(Theme.Font.secondaryStrong)
                    .foregroundStyle(paused ? Color.secondary : Palette.recordingRed)
                    .contentTransition(.opacity)
                    .accessibilityIdentifier("recording-now-state")
            }
            if capture.interrupted {
                Text("It carries on by itself when the call ends, or when you come back to Persona.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .recordingMotion(reduced: reduceMotion, value: paused)
    }

    private var stateWord: String {
        capture.interrupted ? String(localized: "Paused by a call")
            : paused ? String(localized: "Paused") : String(localized: "Recording [Recordings Section 5254d3]")
    }

    /// The time. Read by VoiceOver as "Recording time, 2 minutes, 5 seconds", refreshed once a minute: the system-drawn
    /// text does not redraw the page, so the spoken value does it on its own slow clock.
    private func timer(font: Font) -> some View {
        TimelineView(.periodic(from: .now, by: Theme.Recording.spokenTimeRefresh)) { _ in
            RecordingClock(running: capture.state == .recording, font: font)
                .foregroundStyle(paused ? Color.secondary : Color.primary)
                .dynamicTypeSize(...Theme.Recording.timerLargestSize)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text("Recording time"))
                .accessibilityValue(RecordingClock.spoken(capture.timeline))
                .accessibilityIdentifier("recording-live-timer")
        }
    }

    /// "Persona takes part · On this iPhone · Private": the role (or that Persona can't take part now), where it records,
    /// and the private mark when it is on.
    private func subtitle(_ entry: RecordingEntry) -> some View {
        let parts = [personaWords(entry), String(localized: "On this iPhone"), isPrivate(entry) ? String(localized: "Private") : nil]
        return Text(parts.compactMap { $0 }.joined(separator: " · "))
            .font(Theme.Font.secondary).foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityIdentifier("meeting-page-subtitle")
    }

    /// The role's words, unless it attends and the hub took the meeting without its room (an older manager,
    /// `RecordingSync.roomMissing`): then Persona can't take part right now, whatever the role says.
    private func personaWords(_ entry: RecordingEntry) -> String {
        let role = entry.attendance?.role ?? .off
        return unreachable(entry, role) ? String(localized: "Persona can't take part right now") : Self.roleWords(role)
    }

    /// Persona's role attends, but it can't be asked now (`MeetingAsk.canAsk`, the rule the bar and the asks follow).
    private func unreachable(_ entry: RecordingEntry, _ role: MeetingAttendance) -> Bool {
        role.attends && !MeetingAsk.canAsk(entry, roomMissing: sync.roomMissing.contains(entry.id))
    }

    static func roleWords(_ role: MeetingAttendance) -> String {
        switch role {
        case .participant: String(localized: "Persona takes part")
        case .listener: String(localized: "Persona listens")
        case .off: String(localized: "Persona is off")
        }
    }

    // MARK: Problems

    /// Why Persona can't take part right now (the room, or an older manager), and why its live lines stopped.
    @ViewBuilder private func problems(_ entry: RecordingEntry) -> some View {
        let room = Self.roomProblem(conversation.meeting(id).room, missing: sync.roomMissing.contains(id))
        let attendance = MeetingAttendanceProblem.text(entry, live: live, sync: sync)
        if room != nil || attendance != nil || privateProblem != nil {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                if let room {
                    NoticeBanner(text: room, tone: .caution)
                        .accessibilityIdentifier("meeting-room-problem")
                }
                if let attendance, attendance != room {
                    NoticeBanner(text: attendance, tone: .caution)
                        .accessibilityIdentifier("meeting-attendance-problem")
                }
                if let privateProblem {
                    NoticeBanner(text: privateProblem, tone: .danger)
                        .accessibilityIdentifier("meeting-private-problem")
                }
            }
            .padding(.horizontal, size.isAccessibilitySize ? Theme.Space.flush : Theme.Space.page)
        }
    }

    /// The room's problem in plain words. `missing`: the last upload's answer had no `room` (an older manager after a
    /// rollback), which goes away by itself once an answer carries it again.
    static func roomProblem(_ room: MeetingRoom?, missing: Bool) -> String? {
        if missing { return String(localized: "This hub can't take part in meetings right now. The recording goes on as usual.") }
        guard let room, room.known == .unavailable else { return nil }
        switch room.problem {
        case "runtime_unsupported":
            return String(localized: "Persona can't take part in meetings on the assistant's computer. Questions are answered from the meeting so far.")
        case "node_too_old":
            return String(localized: "The assistant's computer needs an update for meetings. Questions are answered from the meeting so far.")
        case "machine_offline":
            return String(localized: "The assistant's computer is offline. Questions are answered from the meeting so far.")
        case "start_failed":
            return String(localized: "Persona couldn't join the meeting. Questions are answered from the meeting so far.")
        default:
            return String(localized: "Persona can't take part in this meeting right now.")
        }
    }

    /// Seconds from the newest line's end to now, in `liveBehindStep` steps (so it changes a few times a minute).
    static func behind(now: Double, lastEnd: Double) -> Int {
        guard now.isFinite, lastEnd.isFinite, now > lastEnd else { return 0 }
        let step = Double(Theme.Recording.liveBehindStep)
        return Int(((now - lastEnd) / step).rounded()) * Theme.Recording.liveBehindStep
    }

    // MARK: The thread

    /// "Persona" and who may hear its answers, or "Just Me" and that nothing of it is said.
    private func threadHeader(_ entry: RecordingEntry) -> some View {
        let title = Group {
            if justMe {
                // Not the AI's color: what shows under it is the main chat's, kept off the room.
                Label { Text("Just Me") } icon: { ThemedIcon(Theme.Icon.justMe) }
                    .foregroundStyle(Color.primary)
            } else {
                Label { Text("Persona") } icon: { ThemedIcon(Theme.Icon.persona) }
                    .foregroundStyle(Theme.ai)
            }
        }
        .font(Theme.Font.captionStrong)
        let role = entry.attendance?.role ?? .off
        let caption = Text(justMe ? String(localized: "Only on this screen, never said aloud")
                           : unreachable(entry, role) ? String(localized: "Persona can't take part right now") : Self.audience(role))
            .font(Theme.Font.caption).foregroundStyle(.secondary)
        return Group {
            if size.isAccessibilitySize {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) { title; caption }
            } else {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                    title
                    Spacer(minLength: Theme.Space.s)
                    caption.multilineTextAlignment(.trailing)
                }
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
        .accessibilityIdentifier("meeting-thread-header")
    }

    /// Who hears Persona's answers in the room, by its role.
    static func audience(_ role: MeetingAttendance) -> String {
        switch role {
        case .participant: String(localized: "May be heard by the room")
        case .listener: String(localized: "Shown on this screen")
        case .off: String(localized: "Persona is off")
        }
    }

    private func thread(_ entry: RecordingEntry) -> some View {
        MeetingThreadView(entry: entry, mode: mode, justMe: mode == .live && justMe, exchanges: exchanges, shown: $shown)
    }

    /// A scroll view that follows its newest end until the reader's finger takes it (`larkFollowsEnd`, as the live
    /// recording's timeline does), and Jump to Latest with the number of rows that came meanwhile.
    private func scroller<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        ScrollView { content().frame(maxWidth: .infinity, alignment: .leading) }
            .scrollDismissesKeyboard(.interactively)
            .scrollPosition($position)
            .larkFollowsEnd($position, following: $following, active: true)
            .onChange(of: following) { _, now in
                if !now { seen = Set(shown.ids) }
            }
            .overlay(alignment: .bottomTrailing) {
                if !following {
                    RecordingJumpButton(unread: shown.ids.filter { !seen.contains($0) }.count) { following = true }
                        .padding(.trailing, Theme.Space.page)
                        .padding(.bottom, Theme.Space.s)
                        .transition(reduceMotion ? .opacity : .opacity.combined(with: .offset(y: Theme.Recording.arrivalOffset)))
                }
            }
            .animation(reduceMotion ? nil : (following ? Theme.Motion.conceal : Theme.Motion.reveal), value: following)
    }

    /// Stop Speaking for an answer the thread draws as a room message (one drawn as an exchange card has its own).
    private var showsStopSpeaking: Bool {
        guard let speaking = speaker.speaking else { return false }
        return !shown.exchangeRows.contains(speaking)
    }

    private var stopSpeaking: some View {
        Button { MeetingAsk.shared.stopSpeaking() } label: {
            Label("Stop Speaking", systemImage: Theme.Icon.interrupt)
                .font(Theme.Font.bodyStrong)
                .lineLimit(1)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
        .accessibilityIdentifier("meeting-page-stop-speaking")
        .padding(.horizontal, Theme.Space.page)
        .padding(.top, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .background { Theme.groupedPage.ignoresSafeArea(edges: .bottom) }
    }

    // MARK: The page menu

    /// Persona's role (Off turns it off) and the private mark.
    private func pageMenu(_ entry: RecordingEntry) -> some View {
        let role = entry.attendance?.role ?? .off
        return Menu {
            Picker(selection: Binding(get: { role }, set: { MeetingRecorder.shared.setRole($0, recordingID: id.uuidString) })) {
                ForEach(MeetingAttendance.allCases, id: \.self) { Text($0.title).tag($0) }
            } label: {
                Text("Persona")
            }
            .accessibilityIdentifier("meeting-role")
            Toggle(isOn: privateBinding(entry)) {
                Text("Private Meeting")
                Text("Its minutes are never used when Persona answers in later meetings.")
            }
            .disabled(pendingPrivate != nil)
            .accessibilityIdentifier("meeting-private")
        } label: {
            ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading).foregroundStyle(Theme.accent)
                .tapTargetFrame()
                .contentShape(.rect)
        }
        .labelOnlyMenu()
        // Its own name: the bar's [+] and its More menu are other menus.
        .accessibilityLabel(Text("Meeting options"))
        .accessibilityIdentifier("meeting-page-menu")
    }

    /// The private mark as shown: the hub's once it said (absent then: a meeting with no mark counts as private), and
    /// what the meeting started with only until the hub has said anything (the change being made goes first, in the page).
    static func isPrivate(_ entry: RecordingEntry, _ meeting: MeetingConversation.Meeting) -> Bool {
        meeting.privateRead ? (meeting.privateMark ?? true) : (meeting.privateMark ?? entry.attendance?.privateMark ?? true)
    }

    private func isPrivate(_ entry: RecordingEntry) -> Bool {
        pendingPrivate ?? Self.isPrivate(entry, conversation.meeting(id))
    }

    private func privateBinding(_ entry: RecordingEntry) -> Binding<Bool> {
        Binding(get: { isPrivate(entry) }, set: { setPrivate($0) })
    }

    /// `PUT …/private`. A failure is a banner on the page, not a toast: the sheet would cover a toast, and no error
    /// haptic plays while the microphone is open.
    private func setPrivate(_ value: Bool) {
        pendingPrivate = value
        privateProblem = nil
        Task {
            do {
                try await conversation.setPrivate(id, value)
            } catch {
                privateProblem = MeetingConversation.privateMessage(error)
            }
            pendingPrivate = nil
        }
    }

    // MARK: After the meeting

    @ViewBuilder private func afterPage(_ entry: RecordingEntry) -> some View {
        // The minutes' state stays above the thread, which follows its end (DESIGN_SYSTEM 3.6: the end of a recording shows
        // its follow-through): the card, or one line at accessibility sizes and while the keyboard is up, as the live
        // page keeps its header fixed. The structure stays the same, so the thread's scroll view keeps its place.
        VStack(spacing: Theme.Space.s) {
            if size.isAccessibilitySize || writing {
                statusLine(entry)
            } else {
                statusCard(entry)
                    .padding(.horizontal, Theme.Space.page)
                    .padding(.top, Theme.Space.s)
            }
            scroller {
                VStack(alignment: .leading, spacing: Theme.Space.l) {
                    if size.isAccessibilitySize { afterTitle(entry) }
                    if let captureError {
                        NoticeBanner(text: captureError, tone: .danger)
                            .accessibilityIdentifier("meeting-capture-error")
                    }
                    thread(entry)
                    noticeBanner
                }
                .padding(.horizontal, Theme.Space.page)
                .padding(.vertical, Theme.Space.s)
            }
        }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: writing)
        .safeAreaInset(edge: .top, spacing: Theme.Space.flush) {
            if let close {
                HStack {
                    Spacer(minLength: Theme.Space.flush)
                    RecordingSheetCloseButton(title: String(localized: "Done"), identifier: "recording-wrapup-done") { close() }
                }
                .padding(.horizontal, Theme.Space.page)
            }
        }
        .safeAreaInset(edge: .bottom, spacing: Theme.Space.flush) {
            MeetingPageBar(entry: entry, mode: .after, justMe: .constant(false), focus: $writing, notice: $notice)
        }
        // Pushed from the recording's page, the back button returns there: no way to push it again from here.
        .modifier(OpensRecording(id: id, enabled: close != nil, opening: $opening))
        .tabActiveTask(id: "\(entry.revision)|\(minutes.round)|\(entry.transcribedAll)") { await followMinutes() }
        .onChange(of: minutes.state) { old, new in
            // Only minutes that come while the page waits for them are news: a past meeting's, there as it opens, are not.
            guard case .ready = new, [RecordingMinutesModel.State.writing, .waitingForSync, .waitingForText].contains(old) else { return }
            AccessibilityNotification.Announcement(String(localized: "Minutes ready")).post()
        }
    }

    /// The title, when the meeting ran, where the minutes are, and (in the sheet) the way to them. The composer is the
    /// page's primary control, so Open is a standard button.
    private func statusCard(_ entry: RecordingEntry) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            afterTitle(entry)
            minutesLine
            if close != nil {
                Button { opening = true } label: {
                    Text(openTitle)
                        .font(Theme.Font.bodyStrong).lineLimit(1).minimumScaleFactor(0.7)
                        .frame(maxWidth: .infinity)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton()
                .accessibilityIdentifier("recording-wrapup-open")
            }
        }
        .padding(Theme.Space.m)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-after-status")
    }

    /// The card's one-line form: where the minutes are (when the meeting ran, before the hub says), and Open as an icon.
    /// The title scrolls with the thread at accessibility sizes.
    private func statusLine(_ entry: RecordingEntry) -> some View {
        HStack(spacing: Theme.Space.m) {
            Group {
                if minutesShown {
                    minutesLine
                } else {
                    Text(Self.span(entry)).font(Theme.Font.secondaryMedium).foregroundStyle(.secondary)
                }
            }
            .lineLimit(1)
            .minimumScaleFactor(0.7)
            .frame(maxWidth: .infinity, alignment: .leading)
            if close != nil {
                Button { opening = true } label: {
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.heading)
                        .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                }
                .themedButton()
                .accessibilityLabel(Text(openTitle))
                .accessibilityIdentifier("recording-wrapup-open")
            }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.top, Theme.Space.s)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-after-status")
    }

    /// The meeting's title and when it ran.
    @ViewBuilder private func afterTitle(_ entry: RecordingEntry) -> some View {
        Text(entry.displayTitle).font(Theme.Font.title).lineLimit(Theme.Recording.pageTitleLines)
            .accessibilityAddTraits(.isHeader)
            .accessibilityIdentifier("recording-wrapup-title")
        Text(Self.span(entry)).font(Theme.Font.secondary).foregroundStyle(.secondary)
    }

    private var openTitle: String { minutesReady ? String(localized: "Open Minutes") : String(localized: "Open Recording") }

    /// The minutes line has something to say (the hub answered, and there are or will be minutes).
    private var minutesShown: Bool {
        guard minutesAsked else { return false }
        switch minutes.state {
        case .none, .unsupported: return false
        default: return true
        }
    }

    /// 14:00–15:10: from the start to the end of what was recorded.
    static func span(_ entry: RecordingEntry) -> String {
        let end = entry.createdAt.addingTimeInterval(entry.duration)
        return (entry.createdAt..<max(end, entry.createdAt)).formatted(.interval.hour().minute())
    }

    private var minutesReady: Bool {
        if case .ready = minutes.state { true } else { false }
    }

    /// Where the minutes are, in the wrap-up's words: a spinner while they are written, a check once ready.
    @ViewBuilder private var minutesLine: some View {
        if minutesAsked {
            Group {
                switch minutes.state {
                case .writing:
                    HStack(spacing: Theme.Space.s) {
                        ProgressView()
                        Text("Writing the minutes…")
                    }
                case .ready:
                    Label { Text("Minutes ready") } icon: { ThemedIcon(Theme.Icon.successFilled).foregroundStyle(Theme.positive) }
                case .waitingForText:
                    Label { Text("Minutes come next") } icon: { ThemedIcon(Theme.Icon.time).foregroundStyle(.secondary) }
                case .waitingForSync:
                    Label { Text("Waiting for the hub") } icon: { ThemedIcon(Theme.Icon.time).foregroundStyle(.secondary) }
                case .failed:
                    Label { Text("The minutes couldn't be written.") } icon: { ThemedIcon(Theme.Icon.errorFilled).foregroundStyle(Theme.danger) }
                case .none, .unsupported:
                    EmptyView()
                }
            }
            .font(Theme.Font.secondaryMedium)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-after-minutes")
        }
    }

    /// Asks the hub for the minutes while they are on their way, as the wrap-up does.
    private func followMinutes() async {
        while !Task.isCancelled, let entry = library.entry(id) {
            await minutes.load(entry, synced: sync.status(entry) == .synced)
            minutesAsked = true
            guard minutes.waiting else { return }
            try? await Task.sleep(for: .seconds(Theme.Recording.minutesPoll))
        }
    }
}

/// The Live box: the newest transcript lines, how far the text is behind the room, and the whole transcript one tap away.
/// A view of its own, so its lines are taken from the transcript (every segment sorted and filtered, `entry.sentences`)
/// only when the recording changes, not on every pass of the page (P4). `folded` (the keyboard is up): only the newest
/// line, so the thread under it keeps its height.
private struct MeetingLiveBox: View {
    var entry: RecordingEntry
    var exchanges: [MeetingAsk.Exchange]
    var folded: Bool

    @Environment(\.dynamicTypeSize) private var size
    @State private var capture = RecordingCapture.shared
    @State private var rows: [LiveRow] = []

    private var paused: Bool { capture.state == .paused }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                Text("Live").font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                    .accessibilityAddTraits(.isHeader)
                Spacer(minLength: Theme.Space.s)
                behind(rows.last)
            }
            ForEach(folded ? Array(rows.suffix(1)) : rows) { row in liveRow(row) }
            if !folded {
                HStack {
                    Spacer(minLength: Theme.Space.flush)
                    NavigationLink { MeetingTranscriptPage(id: entry.id, exchanges: exchanges) } label: {
                        HStack(spacing: Theme.Space.xs) {
                            Text("Full Transcript").lineLimit(1)
                            ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.captionStrong).accessibilityHidden(true)
                        }
                        .font(Theme.Font.secondaryStrong)
                        .foregroundStyle(Theme.accent)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                        .contentShape(.rect)
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("meeting-transcript")
                }
            }
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.top, Theme.Space.s)
        // Full Transcript's own height closes the box; folded, without it, a margin does.
        .padding(.bottom, folded ? Theme.Space.s : Theme.Space.flush)
        .frame(maxWidth: .infinity, alignment: .leading)
        .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("meeting-live")
        // Every save of the recording moves its revision: the lines are read again then, and only then.
        .onChange(of: entry.revision, initial: true) { rows = Self.rows(entry) }
    }

    /// The last `Theme.Recording.liveLines` sentences, each keyed by its place in the transcript.
    private static func rows(_ entry: RecordingEntry) -> [LiveRow] {
        let all = entry.sentences
        let newest = all.suffix(Theme.Recording.liveLines)
        let first = all.count - newest.count
        return newest.enumerated().map { LiveRow(id: "live-\(first + $0.offset)", start: $0.element.start, end: $0.element.end, text: $0.element.text) }
    }

    private func liveRow(_ row: LiveRow) -> some View {
        let stamp = Text(RecordingFormat.clock(row.start)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
        let words = Text(row.text).font(Theme.Font.body).lineLimit(Theme.Recording.liveLineLength)
        return Group {
            if size.isAccessibilitySize {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) { stamp; words }
            } else {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) { stamp; words }
            }
        }
        .accessibilityElement(children: .combine)
    }

    /// "About 10 s behind" while the meeting records and its text lags; "Listening…" before the first line. The capture's
    /// clock is not observed, so a slow clock refreshes the number.
    private func behind(_ last: LiveRow?) -> some View {
        TimelineView(.periodic(from: .now, by: Theme.Recording.gapRefresh)) { _ in
            Group {
                if let last {
                    let seconds = MeetingPage.behind(now: capture.timeline, lastEnd: last.end)
                    if capture.state == .recording, seconds > 0 { Text("About \(seconds) s behind") }
                } else {
                    Text(paused ? String(localized: "Paused") : String(localized: "Listening…"))
                }
            }
            .font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
        }
    }
}

/// One of the Live box's lines; its id is its place in the transcript, which only grows at the end.
private struct LiveRow: Identifiable, Equatable {
    var id: String
    var start: Double
    var end: Double
    var text: String
}

/// Open Minutes / Open Recording's destination, only where the page has that button (in the sheet).
private struct OpensRecording: ViewModifier {
    var id: UUID
    var enabled: Bool
    @Binding var opening: Bool

    func body(content: Content) -> some View {
        if enabled {
            content.navigationDestination(isPresented: $opening) { RecordingDetailView(id: id) }
        } else {
            content
        }
    }
}

/// The whole transcript of the meeting being recorded, with its bookmarks and questions: the live view's timeline, one
/// tap from the Live box.
private struct MeetingTranscriptPage: View {
    var id: UUID
    var exchanges: [MeetingAsk.Exchange]
    @State private var library = RecordingLibrary.shared
    @State private var capture = RecordingCapture.shared
    @State private var items: [RecordingTimeline.Item] = []

    var body: some View {
        ScrollView {
            if let entry = library.entry(id) {
                RecordingTimelineRows(entry: entry, exchanges: exchanges, paused: capture.state == .paused, items: $items)
                    .padding(.horizontal, Theme.Space.page)
                    .padding(.vertical, Theme.Space.s)
            }
        }
        .themedGroupedPage()
        .navigationTitle(String(localized: "Transcript"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar(.visible, for: .navigationBar)
    }
}
#endif
