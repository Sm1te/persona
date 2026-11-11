#if os(iOS)
import SwiftUI

/// The live sheet's bottom bar (docs/design/proposals/meeting-recording.md section 4C, items 6 and 7): pinned under the
/// thumb whatever is scrolled. Ask Persona is the only solid control (when Persona attends), with the keyboard button
/// beside it; Pause or Continue, Stop and Bookmark are three standard buttons, icon over label. At accessibility sizes the
/// bar stacks Ask Persona and Stop, with a More menu for the rest, every label on one line.
///
/// A typed question is written in the bar itself (#4486, DESIGN_SYSTEM.md 4.4): the keyboard button opens a row over Ask
/// Persona with the composer's field and send button, so the timeline and the answers so far stay in sight above the
/// keyboard. The keyboard button becomes Cancel while the row is open; while its field has the keyboard, Pause, Stop and
/// Bookmark step aside (at accessibility sizes Ask Persona too, and the row has its own Cancel).
///
/// Stop asks first (verb: Stop everywhere): a stopped recording cannot be continued. The Live Activity's Stop stays one tap.
struct RecordingLiveBar: View {
    var entry: RecordingEntry
    var paused: Bool

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var capture = RecordingCapture.shared
    @State private var ask = MeetingAsk.shared
    @State private var confirmingStop = false
    /// The typed question's row is open (`questionRow`), and what is typed in it.
    @State private var typing = false
    @State private var question = ""
    @FocusState private var questionFocused: Bool

    private var attends: Bool { entry.attended }
    /// The question row has the keyboard: the controls it does not need make room for the timeline.
    private var writing: Bool { attends && typing && questionFocused }
    /// Send needs words, and one question at a time (as Ask Persona).
    private var canSend: Bool { MeetingAsk.hasWords(question) && !ask.busy }

    var body: some View {
        VStack(spacing: Theme.Space.s) {
            if attends, typing {
                questionRow.transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if size.isAccessibilitySize { stacked } else { standard }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .background { Theme.groupedPage.ignoresSafeArea(edges: .bottom) }
        .confirmationDialog("Stop Recording?", isPresented: $confirmingStop, titleVisibility: .visible) {
            Button("Stop Recording", role: .destructive) { MeetingRecorder.shared.stop(recordingID: nil) }
        } message: {
            Text("The audio is saved on this device and its text goes to Knowledge. A stopped recording can't be continued.")
        }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: typing)
        .animation(reduceMotion ? nil : Theme.Motion.state, value: writing)
        // Persona stopped attending (the role turned off, the hub can't follow): no question to finish.
        .onChange(of: attends) { _, now in if !now { closeQuestion() } }
    }

    /// What a typed question can be about, and how the answer comes (said aloud only when Persona takes part).
    private var questionHint: String {
        (entry.attendance?.role.speaks ?? false)
            ? String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers out loud.")
            : String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers on screen.")
    }

    // MARK: Layouts

    @ViewBuilder private var standard: some View {
        if attends {
            HStack(spacing: Theme.Space.m) {
                askButton
                typeButton
            }
        }
        if !writing {
            HStack(spacing: Theme.Space.m) {
                pauseButton
                stopButton
                bookmarkButton
            }
            .transition(.opacity)
        }
    }

    /// Accessibility sizes: Ask Persona full width, then Pause or Continue, Stop and More side by side as icons (their names
    /// are their accessibility labels), so the bar is two rows tall whatever the text size and Pause stays one tap away.
    @ViewBuilder private var stacked: some View {
        if !writing { stackedControls.transition(.opacity) }
    }

    @ViewBuilder private var stackedControls: some View {
        if attends { askButton }
        HStack(spacing: Theme.Space.m) {
            iconButton(paused ? String(localized: "Continue") : String(localized: "Pause"), paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
                .accessibilityIdentifier("recording-pause")
            iconButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
                .accessibilityIdentifier("recording-stop")
            Menu {
                Button { addBookmark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin) }
                if attends {
                    Button { openQuestion() } label: { Label("Type a Question", systemImage: Theme.Icon.keyboard) }
                        .disabled(ask.busy && !typing)
                        .accessibilityIdentifier("meeting-ask-type")
                }
            } label: {
                ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading)
                    .frame(maxWidth: .infinity)
                    .scaledFrame(minHeight: Theme.Size.tapTarget)
            }
            .themedButton()
            .accessibilityLabel(Text("More"))
            .accessibilityIdentifier("recording-more")
        }
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

    // MARK: Controls

    private var askButton: some View {
        Button { askNow() } label: {
            HStack(spacing: Theme.Space.s) {
                if ask.busy { ProgressView() } else if !size.isAccessibilitySize { ThemedIcon(Theme.Icon.question) }
                Text(ask.busy ? String(localized: "Persona is looking…") : String(localized: "Ask Persona")).lineLimit(1).minimumScaleFactor(0.6)
            }
            .font(Theme.Font.bodyStrong)
            .frame(maxWidth: .infinity)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton(prominent: true)
        // One question at a time, deliberately stricter than the hub's two: an answer sits next to its question.
        .disabled(ask.busy)
        .accessibilityHint(Text("Asks about what was just said"))
        .accessibilityIdentifier("meeting-ask")
    }

    /// Opens the question row, or (open) cancels it.
    private var typeButton: some View {
        Button { typing ? closeQuestion() : openQuestion() } label: {
            ThemedIcon(typing ? Theme.Icon.close : Theme.Icon.keyboard).font(Theme.Font.heading)
                .contentTransition(.symbolEffect(.replace))
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
        }
        .themedButton()
        .clipShape(.circle)
        .disabled(ask.busy && !typing)
        .accessibilityLabel(typing ? Text("Cancel") : Text("Type a Question"))
        .accessibilityIdentifier(typing ? "meeting-ask-cancel" : "meeting-ask-type")
    }

    /// The typed question, in the bar (#4486): the hint, then the composer's field (it grows to a few lines) and its round
    /// send button, which waits for words (`MeetingQuestionField`, which the meeting page's bar shares). Return sends too.
    /// At accessibility sizes the field takes the full width, with Cancel and send under it.
    private var questionRow: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            // At accessibility sizes the hint is several lines: while the keyboard is up it gives the room back to the
            // timeline (the field's spoken hint keeps it).
            if !(size.isAccessibilitySize && questionFocused) {
                Text(questionHint)
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .accessibilityIdentifier("meeting-ask-hint")
            }
            MeetingQuestionField(text: $question, focus: $questionFocused, placeholder: String(localized: "Ask about this meeting"),
                                 sendLabel: String(localized: "Ask [Meeting Ask]"), canSend: canSend, largeTypeHint: questionHint,
                                 // Opened by the keyboard button or More: the keyboard comes up with it.
                                 focusOnAppear: true, send: send) {
                // At accessibility sizes the row under the field has its own Cancel, the keyboard button being away.
                Button { closeQuestion() } label: {
                    Label("Cancel", systemImage: Theme.Icon.close).lineLimit(1)
                }
                .themedButton()
                .accessibilityIdentifier("meeting-ask-cancel")
            }
        }
    }

    private var pauseButton: some View {
        barButton(paused ? String(localized: "Continue") : String(localized: "Pause"), paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
            .accessibilityIdentifier("recording-pause")
    }

    private var stopButton: some View {
        barButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
            .accessibilityIdentifier("recording-stop")
    }

    private var bookmarkButton: some View {
        barButton(String(localized: "Bookmark"), Theme.Icon.pin) { addBookmark() }
            .accessibilityIdentifier("recording-bookmark")
    }

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

    // MARK: Actions

    private func askNow() {
        ask.ask(entry)
        Haptics.tap()
    }

    private func openQuestion() {
        typing = true
        questionFocused = true
    }

    /// Cancel: the row and what was typed go (as the alert's Cancel did).
    private func closeQuestion() {
        questionFocused = false
        typing = false
        question = ""
    }

    /// Asks through the same path as before (`MeetingAsk.ask(_:text:)`), then the row closes with the keyboard so the
    /// question and Persona looking at it show in the timeline.
    private func send() {
        guard canSend, ask.ask(entry, text: question) != nil else { return }
        closeQuestion()
        Haptics.tap()
    }

    /// Pause: the haptic after the microphone is paused (a pulse on touch-down would overlap live capture). Continue: before
    /// it opens again. The state is announced.
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
