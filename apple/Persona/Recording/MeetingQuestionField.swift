#if os(iOS)
import SwiftUI

/// The typed question's field and send button (#4486, DESIGN_SYSTEM.md 4.4), one view for the live sheet's bar
/// (`RecordingLiveBar`) and the meeting page's (`MeetingPageBar`, #3862), so the two never drift apart: the composer's field
/// (`ComposerSurface`, a vertical `TextField` that grows to `Theme.Recording.askLines` lines, `askLinesLargeType` at
/// accessibility sizes) and its round send button (`ComposerRoundButton`), which stays in place dimmed
/// (`Theme.Recording.askSendIdle`) until there is something it can send. Return typed at the end of the field sends too.
///
/// At standard sizes `beside` (the meeting page's [+]), the field and send sit on one row. At accessibility sizes the
/// field takes the whole width and the row under it holds `under` (the live bar's Cancel, the meeting page's controls)
/// with send at its trailing edge. The two rows join the stack of the bar that holds the view, with its spacing.
struct MeetingQuestionField<Beside: View, Under: View>: View {
    @Binding var text: String
    var focus: FocusState<Bool>.Binding
    var placeholder: String
    /// Send's spoken name.
    var sendLabel: String
    var canSend: Bool
    /// The field takes no words now (the bar's caption says why): a tap doesn't bring the keyboard up.
    var disabled = false
    /// Spoken with the field at accessibility sizes, where the words that explain it step aside while it has the keyboard.
    var largeTypeHint = ""
    /// The keyboard comes up as the field appears (the live bar opens its row for typing).
    var focusOnAppear = false
    var send: () -> Void
    @ViewBuilder var beside: () -> Beside
    @ViewBuilder var under: () -> Under

    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        if size.isAccessibilitySize {
            field
            HStack(spacing: Theme.Space.m) {
                under()
                Spacer(minLength: Theme.Space.s)
                sendButton
            }
        } else {
            HStack(alignment: .bottom, spacing: Theme.Space.xs) {
                beside()
                field
                sendButton
            }
        }
    }

    private var field: some View {
        TextField(placeholder, text: $text, axis: .vertical)
            .font(Theme.Font.body)
            .lineLimit(1...(size.isAccessibilitySize ? Theme.Recording.askLinesLargeType : Theme.Recording.askLines))
            .focused(focus)
            .submitLabel(.send)
            .onSubmit { send() }
            // A multi-line field takes Return as a new line rather than a submit: one typed at the end sends instead
            // (what can't go is dropped by `send`). A pasted question keeps its lines.
            .onChange(of: text) { old, new in
                guard new == old + "\n" else { return }
                text = old
                send()
            }
            .padding(.horizontal, Theme.Space.l)
            .padding(.vertical, Theme.Space.s)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
            .modifier(ComposerSurface())
            .contentShape(.rect(cornerRadius: Theme.Shell.composerRadius))
            .onTapGesture { if !disabled { focus.wrappedValue = true } }
            .disabled(disabled)
            .accessibilityHint(size.isAccessibilitySize ? Text(largeTypeHint) : Text(verbatim: ""))
            .accessibilityIdentifier("meeting-ask-field")
            .onAppear { if focusOnAppear { focus.wrappedValue = true } }
    }

    private var sendButton: some View {
        ComposerRoundButton(symbol: Theme.Icon.send, label: sendLabel, id: "meeting-ask-send", pressScales: true) { send() }
            .disabled(!canSend)
            .opacity(canSend ? 1 : Theme.Recording.askSendIdle)
    }
}

extension MeetingQuestionField where Beside == EmptyView {
    /// Nothing beside the field at standard sizes (the live bar's row).
    init(text: Binding<String>, focus: FocusState<Bool>.Binding, placeholder: String, sendLabel: String, canSend: Bool,
         disabled: Bool = false, largeTypeHint: String = "", focusOnAppear: Bool = false, send: @escaping () -> Void,
         @ViewBuilder under: @escaping () -> Under) {
        self.init(text: text, focus: focus, placeholder: placeholder, sendLabel: sendLabel, canSend: canSend, disabled: disabled,
                  largeTypeHint: largeTypeHint, focusOnAppear: focusOnAppear, send: send, beside: { EmptyView() }, under: under)
    }
}
#endif
