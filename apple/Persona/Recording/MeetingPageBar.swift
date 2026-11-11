#if os(iOS)
import BeautifulUI
import SwiftUI

/// The meeting page's bottom bar (#3862, docs/assistant/meeting-minutes.md「The meeting card」), pinned under the thumb:
/// the Just Me switch, the composer, and Pause, Bookmark and Stop while the meeting records; the composer alone after it.
/// The composer is #4486's typed question (`MeetingQuestionField`: the shell composer's field and round send button) with
/// a [+] menu before it, not `ShellComposerBar`: that one's dictation would take the microphone the recording holds. Its
/// words go to the room (`…/ask`), to the main chat with Just Me (`…/just-me`), or after the meeting to the follow-up
/// (`…/followup`, then the conversation's messages). While the field has the keyboard, Pause, Bookmark and Stop step
/// aside so the thread stays in view.
///
/// At accessibility sizes the bar is at most two rows, the field and one row of icons: Pause or Continue, Stop, a More
/// menu (Bookmark, Just Me, What Was Just Said) and send. Why the field takes no words then shows among what the page
/// scrolls (`notice`), and with the field as its spoken hint.
///
/// Stop asks first, as the live sheet's bar does: a stopped recording cannot be continued.
struct MeetingPageBar: View {
    var entry: RecordingEntry
    var mode: MeetingPage.Mode
    var paused = false
    @Binding var justMe: Bool
    /// The field has the keyboard: the page steps aside as the bar does (`MeetingPage`).
    var focus: FocusState<Bool>.Binding
    /// At accessibility sizes, why the field takes no words now, for the page to show (nil at other sizes: the bar says it).
    @Binding var notice: String?

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.buiTheme) private var theme
    @State private var capture = RecordingCapture.shared
    @State private var ask = MeetingAsk.shared
    @State private var conversation = MeetingConversation.shared
    @State private var sync = RecordingSync.shared
    @State private var draft = ""
    @State private var confirmingStop = false

    private enum Target { case room, justMe, followup }

    private var target: Target { mode == .after ? .followup : justMe ? .justMe : .room }
    private var role: MeetingAttendance { entry.attendance?.role ?? .off }
    /// The last upload's `200` had no `room`: an older manager took the meeting with Persona off (`RecordingSync`).
    private var roomMissing: Bool { sync.roomMissing.contains(entry.id) }
    /// Persona is off and Just Me is off: there is nobody to ask.
    private var silenced: Bool { target == .room && !role.attends }
    /// Persona's role attends but it can't be asked now (`MeetingAsk.canAsk`): the hub took the meeting without its room.
    /// Just Me still goes (it needs only a meeting that records).
    private var unreachable: Bool { target == .room && role.attends && !MeetingAsk.canAsk(entry, roomMissing: roomMissing) }
    /// Why the follow-up can't be written now (after the meeting).
    private var hold: MeetingConversation.FollowupHold? { target == .followup ? conversation.followupHold(entry) : nil }
    /// More words than the route takes (`MeetingConversation.maxText`): Send stays off and the caption says so, rather
    /// than the end of the owner's words being dropped.
    private var overLong: Bool {
        switch target {
        case .room: false
        case .justMe: MeetingConversation.tooLong(draft)
        case .followup: conversation.followupLimited(entry.id) && MeetingConversation.tooLong(draft)
        }
    }
    /// The keyboard is up: the controls under the composer step aside (#4486's rule).
    private var writing: Bool { focus.wrappedValue }

    private var canSend: Bool {
        guard MeetingAsk.hasWords(draft), !overLong else { return false }
        switch target {
        // One question at a time, deliberately stricter than the hub's two: an answer sits next to its question.
        case .room: return MeetingAsk.canAsk(entry, roomMissing: roomMissing) && !ask.busy
        // Just Me needs only a meeting that records; it never waits on the room's question.
        case .justMe: return entry.active
        case .followup: return hold == nil
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if mode == .live, !size.isAccessibilitySize { justMeRow }
            MeetingQuestionField(text: $draft, focus: focus, placeholder: placeholder, sendLabel: String(localized: "Send"),
                                 canSend: canSend, disabled: fieldDisabled, largeTypeHint: blocked ?? "", send: send) {
                plus
            } under: {
                if mode == .live, !writing { largeControls }
            }
            if let caption {
                Text(caption).font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-ask-caption")
            }
            if mode == .live, !writing, !size.isAccessibilitySize { controls }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .background { Theme.groupedPage.ignoresSafeArea(edges: .bottom) }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: writing)
        .onChange(of: fieldDisabled) { _, disabled in
            if disabled { focus.wrappedValue = false }
        }
        .onChange(of: size.isAccessibilitySize ? blocked : nil, initial: true) { _, now in
            if notice != now { notice = now }
        }
        .confirmationDialog("Stop Recording?", isPresented: $confirmingStop, titleVisibility: .visible) {
            Button("Stop Recording", role: .destructive) { MeetingRecorder.shared.stop(recordingID: entry.id.uuidString) }
        } message: {
            Text("The audio is saved on this device and its text goes to Knowledge. A stopped recording can't be continued.")
        }
    }

    // MARK: Just Me

    private var justMeRow: some View {
        Toggle(isOn: $justMe) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Just Me").font(Theme.Font.bodyStrong)
                Text("Only you see it, in the main chat").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        }
        .disabled(!entry.active)
        .accessibilityIdentifier("meeting-just-me")
    }

    // MARK: The composer

    private var placeholder: String {
        switch target {
        case .room:
            silenced ? String(localized: "Persona is off")
                : unreachable ? String(localized: "Persona can't take part right now") : String(localized: "Ask Persona…")
        case .justMe: String(localized: "Ask privately…")
        case .followup: String(localized: "Ask about this meeting…")
        }
    }

    private var fieldDisabled: Bool { silenced || unreachable || hold != nil }

    /// Why the field can't take words now, and the way out when there is one here (an older manager has none: the role
    /// is on, and nothing on the phone changes it).
    private var blocked: String? {
        if silenced { return String(localized: "Turn Persona on in the menu to ask, or use Just Me.") }
        if unreachable { return String(localized: "Persona can't take part in this meeting right now.") }
        switch hold {
        case .finishing?: return String(localized: "Persona is finishing the meeting. You can ask about it in a moment.")
        // Not "in a moment": the hub has no end of the meeting until a later upload lands, or 12 hours after the last.
        case .endRefused?:
            return sync.problem ?? String(localized: "Recording sync couldn't finish. Try again after checking the hub's diagnostics.")
        case .noFollowup?: return String(localized: "This hub can't take questions about this meeting right now.")
        case nil: return nil
        }
    }

    /// The caption under the field: why it takes no words (at accessibility sizes the page shows that), or that the words
    /// are too many to send.
    private var caption: String? {
        if !size.isAccessibilitySize, let blocked { return blocked }
        guard overLong else { return nil }
        return String(localized: "Too long to send. Shorten it to \(MeetingConversation.maxText.formatted()) characters.")
    }

    /// [+]: what else the composer can send. Phase 1 has one item, the question about what was just said, asked of the
    /// room; with nothing to offer (Just Me, after the meeting, Persona off or not reachable) the button is not there. At
    /// accessibility sizes its items are in More.
    @ViewBuilder private var plus: some View {
        if canAskHeard {
            Menu {
                heardButton
            } label: {
                plusFace
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .accessibilityLabel(Text("Add [Meeting Composer]"))
            .accessibilityIdentifier("meeting-plus")
            .shellScaled()
        }
    }

    private var canAskHeard: Bool { mode == .live && target == .room && MeetingAsk.canAsk(entry, roomMissing: roomMissing) }

    private var heardButton: some View {
        Button { askHeard() } label: { Label("What Was Just Said", systemImage: Theme.Icon.question) }
            .disabled(ask.busy)
            .accessibilityIdentifier("meeting-heard")
    }

    /// The shell composer's [+] (`ShellComposerBar.plusFace`): the add symbol in the composer's ink, no circle, so send
    /// stays the row's one solid button.
    private var plusFace: some View {
        ThemedIcon(Theme.Icon.add)
            .font(Theme.Font.title)
            .fontWeight(.regular)
            .foregroundStyle(ComposerInk.icon(theme))
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
            .contentShape(.circle)
    }

    // MARK: Pause, Bookmark, Stop

    private var controls: some View {
        HStack(spacing: Theme.Space.m) {
            barButton(pauseTitle, paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
                .accessibilityIdentifier("recording-pause")
            barButton(String(localized: "Bookmark"), Theme.Icon.pin) { addBookmark() }
                .accessibilityIdentifier("recording-bookmark")
            barButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
                .accessibilityIdentifier("recording-stop")
        }
    }

    /// Accessibility sizes, in the row under the field before send: Pause or Continue, Stop and More side by side as icons
    /// (their names are their accessibility labels), so the bar stays two rows tall whatever the text size and Pause stays
    /// one tap away. More holds Bookmark, the Just Me switch and the [+] menu's question.
    @ViewBuilder private var largeControls: some View {
        iconButton(pauseTitle, paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
            .accessibilityIdentifier("recording-pause")
        iconButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
            .accessibilityIdentifier("recording-stop")
        Menu {
            Button { addBookmark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin) }
                .accessibilityIdentifier("recording-bookmark")
            Toggle(isOn: $justMe) {
                Label("Just Me", systemImage: Theme.Icon.justMe)
                Text("Only you see it, in the main chat")
            }
            .disabled(!entry.active)
            .accessibilityIdentifier("meeting-just-me")
            if canAskHeard { heardButton }
        } label: {
            ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
        .accessibilityLabel(Text("More"))
        .accessibilityIdentifier("recording-more")
    }

    private var pauseTitle: String { paused ? String(localized: "Continue") : String(localized: "Pause") }

    /// A standard button, its icon over its label, at least a finger tall.
    private func barButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: Theme.Space.xxs) {
                ThemedIcon(symbol).font(Theme.Font.heading).scaledFrame(height: Theme.Size.icon)
                Text(title).font(Theme.Font.captionStrong).lineLimit(1).minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
    }

    private func iconButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ThemedIcon(symbol).font(Theme.Font.heading)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
        .accessibilityLabel(Text(title))
    }

    // MARK: Actions

    /// Sends the draft where the bar points; the keyboard goes down after, so the new question is in view.
    private func send() {
        guard canSend else { return }
        let text = draft
        let sent: Bool
        switch target {
        case .room: sent = ask.ask(entry, text: text) != nil
        case .justMe: sent = conversation.sendJustMe(entry, text: text) != nil
        case .followup: sent = conversation.sendFollowup(entry.id, text: text) != nil
        }
        guard sent else { return }
        draft = ""
        focus.wrappedValue = false
        Haptics.tap()
    }

    /// The question about what was just said (`…/ask` with the time of the tap).
    private func askHeard() {
        guard ask.ask(entry) != nil else { return }
        Haptics.tap()
    }

    /// Pause: the haptic after the microphone is paused (a pulse on touch-down would overlap live capture). Continue:
    /// before it opens again. The state is announced.
    private func togglePause() {
        if paused {
            Haptics.tap()
            capture.togglePause()
            AccessibilityNotification.Announcement(String(localized: "Recording continued")).post()
        } else {
            capture.togglePause()
            Haptics.tap()
            AccessibilityNotification.Announcement(String(localized: "Recording paused")).post()
        }
    }

    private func addBookmark() {
        MeetingRecorder.shared.addMark(recordingID: entry.id.uuidString)
        AccessibilityNotification.Announcement(String(localized: "Bookmark added")).post()
    }
}
#endif
