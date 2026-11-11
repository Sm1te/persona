import BeautifulUI
import SwiftUI

/// The shell's composer (docs/apple/ios.md「New shell」), ChatGPT's: one line — [+] Ask Persona… [mic] — that grows into a card
/// while typing, the field on top and a row under it: [+] … [model · effort] [mic] [send]. With Voice Chat offered
/// (`voice`, docs/assistant/voice.md 6.1) the line is [+] … [mic] [voice], or [+] … [voice] [stop] while the agent works (dictation
/// is then in the card's row), and the card's row has voice in send's place while nothing is typed. The assistant's
/// conversation and an agent's (in the shell) both type here (#1091). The field is one view in both
/// shapes (only the buttons around it come and go), so focus and the IME's marked text survive the change.
/// The callers keep their own state (drafts, uploads, SendSafety in `Composer`; the starter's text in StarterComposer).
struct ShellComposerBar<Attach: View, Modes: View>: View {
    var placeholder: String
    @Binding var text: String
    @Binding var selection: TextSelection?
    var focused: FocusState<Bool>.Binding
    var canSend: Bool
    /// Something is attached (photos, quotes): the card stays open, there is something to send.
    var hasAttachments = false
    /// Set while the agent works: a stop button takes send's place while nothing is typed.
    var stop: (() -> Void)?
    /// Voice Chat (docs/assistant/voice.md 6.1): set where the conversation offers a voice call; nil, the composer is as it was.
    var voice: (() -> Void)? = nil
    /// What the model button says, ChatGPT's "6.1 Sol Medium": the model, then the effort (`SessionModes.composerTitle`);
    /// nil: no button. Its menu says what needs saying about the permission mode (Allow Everything), not the button (#1117).
    var modesTitle: SessionModes.ComposerTitle?
    /// What goes with the message, quoted at the top of the card (#986); quotes keep it open (`hasAttachments`).
    var quotes: Binding<[ContextRef]>? = nil
    /// Always the card, never the one line (the Mac: the field and its row of buttons stay, as a desktop app's box).
    var alwaysOpen = false
    /// The conversation whose sent messages ↑ / ↓ and ⌃R bring back (the Mac, `MacSendHistory`); nil: none.
    var history: String? = nil
    /// The main conversation cannot answer yet: send says what to do first and goes there, the draft stays.
    var gate: SendGate? = nil
    /// The iPhone's 「+」 opens the composer's panel (`AttachSheet`, Telegram's attachment menu) from where the button is,
    /// in the window, and touched and held, the menu of `attach` (#3225); nil: a tap opens the menu of `attach`.
    var openAttach: ((CGRect) -> Void)? = nil
    /// Glow While Replying (`ComposerGlow`): the band around the bar while the assistant's reply is being written. Drawn
    /// inside the bar's animations so it follows the surface when the line grows into the card.
    var glows = false
    var send: () -> Void
    @ViewBuilder var attach: () -> Attach
    @ViewBuilder var modes: () -> Modes
    @State private var dictation = Dictation()
    @Environment(\.buiTheme) private var theme
    #if os(iOS)
    /// The mic held down (`ComposerHoldMic`): the box draws the strip, the circle and the lock over itself.
    @State private var hold = ComposerHold()
    @State private var plusFrame: CGRect = .zero
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// The draft's height at its full width, and a line's height at the current Dynamic Type size.
    @State private var textHeight: CGFloat = 0
    @State private var lineHeight: CGFloat = 0
    @State private var viewportHeight: CGFloat = 0
    @State private var enlargeWidth: CGFloat = Theme.Size.tapTarget
    /// The full-page editor is up (`ComposerPageEditor`), on the same draft.
    @State private var enlarged = false
    /// It went down because its Send sent: the keyboard does not come back to the emptied field.
    @State private var sentFromPage = false
    /// What the box's field is bound to while the page is up, so it cannot move the caller's `selection` under the
    /// sheet (the page hands its cursor back to that when it goes down).
    @State private var parkedSelection: TextSelection?
    /// Asks the page for the keyboard: something asked for the box's (a file's path inserted, `PathInsert.edit`; ⌘I)
    /// while the page is up. A focus binding does not reach into a sheet, so the page keeps its own.
    @State private var pageFocusRequest = 0

    private var offersEnlarge: Bool {
        ComposerGrowth.showsExpandButton(text: text, focused: focused.wrappedValue, textHeight: textHeight, lineHeight: lineHeight, maximumLines: maximumLines)
    }

    /// Count a trailing newline as the empty line it starts.
    private var measuredText: String { text.isEmpty ? " " : text.hasSuffix("\n") ? text + " " : text }
    #endif

    private var maximumLines: Int {
        #if os(iOS)
        ComposerGrowth.maximumLines(viewportHeight: viewportHeight, lineHeight: lineHeight)
        #else
        Theme.Shell.composerMaximumLines // Other platforms retain their existing composer height.
        #endif
    }

    private var fieldRightInset: CGFloat {
        let inset = expanded ? Theme.Space.l : Theme.Space.xs
        #if os(iOS)
        return inset + (offersEnlarge ? enlargeWidth : 0)
        #else
        return inset
        #endif
    }

    private var fieldMinimumHeight: CGFloat {
        #if os(iOS)
        // Even when quotes compress the text, the corner button's target must stay above the toolbar.
        Theme.Size.tapTarget + (offersEnlarge ? Theme.Space.xs : 0)
        #else
        Theme.Size.tapTarget
        #endif
    }

    private var fieldSelection: Binding<TextSelection?> {
        #if os(iOS)
        enlarged ? $parkedSelection : $selection
        #else
        $selection
        #endif
    }

    /// ⌘⏎ sends from the box; while the page is up, from the page only (its own Send has it).
    private var sendShortcut: KeyboardShortcut? {
        #if os(iOS)
        if enlarged { return nil }
        #endif
        return KeyboardShortcut(.return, modifiers: .command)
    }

    /// The icons' ink: the text color, or in the BeautifulUI style its quieter second ink.
    private var ink: Color { ComposerInk.icon(theme) }

    /// How far the send button's tap area sits from the card's right and bottom edges. In the BeautifulUI style the
    /// button's circle is concentric with the card's corner: its gap to both edges is the card's radius less its own,
    /// so the gap along the corner is the same as along the sides. The other styles keep their row inset.
    private var sendInset: CGFloat {
        guard AppStyle.current.usesBeautifulUI else { return Theme.Space.s }
        let well = Theme.Size.wellL
        return Theme.Shell.composerRadius - well / 2 - (Theme.Size.tapTarget - well) / 2
    }

    private var expanded: Bool { alwaysOpen || focused.wrappedValue || !text.isEmpty || hasAttachments }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("ShellComposerBar")
        #endif
        VStack(alignment: .leading, spacing: 0) {
            VStack(spacing: 0) {
                if expanded, let quotes, !quotes.wrappedValue.isEmpty {
                    QuoteBar(refs: quotes)
                        .padding(.leading, Theme.Space.l)
                        .padding(.trailing, Theme.Space.s)
                        .padding(.top, Theme.Space.m)
                        .transition(quoteTransition)
                }
            }
            #if os(iOS)
            .clipped() // the quotes slide in from under the box's top edge (Telegram's reply bar)
            #endif
            HStack(alignment: .bottom, spacing: Theme.Space.xs) {
                if !expanded { plus }
                TextField(placeholder, text: $text, selection: fieldSelection, axis: .vertical)
                    .font(Theme.Font.body)
                    .lineLimit(expanded ? 1...maximumLines : 1...1)
                    #if os(iOS) || os(visionOS)
                    // Grown taller, the field must not stay scrolled down a line (#909, TextViewScroll).
                    .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { _ in
                        #if os(iOS)
                        if focused.wrappedValue, !enlarged { TextViewScroll.settleFocused(revealCaret: true) }
                        #else
                        if focused.wrappedValue { TextViewScroll.settleFocused() }
                        #endif
                    }
                    #endif
                    .focused(focused)
                    #if os(macOS)
                    .textFieldStyle(.plain) // the card is the field's box; the Mac's own bezel drew a second one inside
                    .macComposerKeys(focused: focused.wrappedValue, canSend: canSend, // ⏎ sends, ⇧⏎ a new line
                                     history: history.map { MacComposerHistory(key: $0, text: $text, focus: { focused.wrappedValue = true }) }, send: send)
                    #endif
                    .padding(.leading, expanded ? Theme.Space.l : Theme.Space.xs)
                    .padding(.trailing, fieldRightInset)
                    .padding(.top, expanded ? Theme.Space.l : 0)
                    .scaledFrame(minHeight: fieldMinimumHeight)
                    .accessibilityIdentifier("shell-composer-field")
                    #if os(iOS)
                    // Measure at full width, before the button's reserved inset, not the field's capped height.
                    // Not while the page is up: it has its own lines, and the box measures again as it comes back.
                    .background(alignment: .topLeading) {
                        if !enlarged { measuring }
                    }
                    .overlay(alignment: .topTrailing) {
                        if offersEnlarge {
                            enlargeButton
                                .padding(.trailing, Theme.Space.s)
                                .padding(.top, Theme.Space.xs)
                        }
                    }
                    #endif
                if !expanded {
                    // While the agent works, voice and stop share the line and dictation waits in the card's row.
                    if voice == nil || stop == nil { mic }
                    if let voice { voiceButton(voice) }
                    if let stop { stopButton(stop) }
                }
            }
            #if os(iOS)
            // Prefer the native, line-limited text region over surrounding content without forcing UIKit's size.
            .layoutPriority(1)
            #endif
            if expanded {
                HStack(spacing: Theme.Space.xs) {
                    plus
                    #if os(iOS)
                    if focused.wrappedValue { dismissKeyboardButton }
                    #endif
                    Spacer(minLength: Theme.Space.s)
                    if let modesTitle {
                        Menu { modes() } label: { modesLabel(modesTitle) }
                            .menuStyle(.button)
                            .buttonStyle(.plain)
                            .accessibilityLabel("Model and thinking effort")
                            .accessibilityValue(modesTitle.spoken)
                            .accessibilityIdentifier("shell-composer-modes")
                    }
                    mic
                    // One place for send and what it replaces, so they swap over each other rather than side by side.
                    ZStack(alignment: .trailing) {
                        if let gate {
                            SendGateButton(gate: gate)
                        } else if canSend {
                            HStack(spacing: Theme.Space.flush) {
                                ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "shell-composer-send",
                                                    pressScales: telegramMotion) { endDictation(then: sendTyped) }
                                    .keyboardShortcut(sendShortcut)
                            }
                            .transition(telegramMotion ? .composerSend : .scale.combined(with: .opacity))
                        } else if voice != nil || stop != nil {
                            HStack(spacing: Theme.Space.xs) {
                                if let voice { voiceButton(voice) }
                                if let stop { stopButton(stop) }
                            }
                            .transition(telegramMotion ? .composerSwapped : .scale.combined(with: .opacity))
                        }
                    }
                    .animation(swapMotion, value: canSend)
                }
                .padding(.leading, Theme.Space.s)
                .padding(.trailing, sendInset)
                .padding(.bottom, sendInset)
                .transition(.opacity.combined(with: .move(edge: .bottom)))
            }
        }
        .padding(.horizontal, expanded ? 0 : Theme.Space.xs)
        .padding(.vertical, expanded ? 0 : Theme.Space.xxs)
        .modifier(ComposerSurface())
        #if os(iOS)
        .overlay { ComposerHoldOverlay(hold: hold) }
        .coordinateSpace(.named(ComposerHold.space))
        #endif
        .composerGlow(glows)
        .contentShape(.rect(cornerRadius: Theme.Shell.composerRadius))
        .onTapGesture { focused.wrappedValue = true }
        .shellScaled()
        .animation(openMotion, value: expanded)
        .animation(Theme.Motion.state, value: canSend)
        .animation(focusMotion, value: focused.wrappedValue)
        #if os(iOS)
        .animation(Theme.Motion.composerAccessory, value: quotes?.wrappedValue.isEmpty == false)
        // A line more or less in the draft: the box follows at once, briefly eased (Telegram's 0.1 s).
        .animation(reduceMotion ? nil : Theme.Motion.composerGrow, value: text)
        .background {
            ComposerViewportReader { viewportHeight = $0 }
                .allowsHitTesting(false)
                .accessibilityHidden(true)
        }
        #if DEBUG
        .background { if ScreenProbe.enabled { ComposerLinesProbe(maximumLines: maximumLines, viewport: viewportHeight, line: lineHeight) } }
        #endif
        .animation(Theme.Motion.state, value: offersEnlarge)
        .onChange(of: focused.wrappedValue) { _, wants in
            // The box asked for while the page is up: the page takes it, the hidden box under the sheet does not.
            guard enlarged, wants else { return }
            focused.wrappedValue = false
            pageFocusRequest += 1
        }
        .sheet(isPresented: $enlarged, onDismiss: {
            // Collapse or a swipe down: back to the box, the text and the cursor where the page left them.
            if !sentFromPage { focused.wrappedValue = true }
            sentFromPage = false
        }) {
            ComposerPageEditor(placeholder: placeholder, text: $text, selection: $selection, focusRequest: pageFocusRequest,
                               canSend: canSend, stop: stop.map { stop in { endDictation(then: stop) } }, quotes: quotes,
                               collapse: { enlarged = false }, send: sendFromPage)
                .presentationSizing(.page) // the iPad's too: a page's height, not the small form
        }
        #endif
        .onDisappear { if dictation.isListening { dictation.stop() } }
    }

    #if os(iOS)
    /// An icon in the text area's corner, never competing with the model and send controls below it.
    private var enlargeButton: some View {
        Button(action: enlarge) {
            ThemedIcon(Theme.Icon.expandMessage)
                .font(Theme.Font.secondary)
                .foregroundStyle(ink)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .onGeometryChange(for: CGFloat.self) { $0.size.width } action: { enlargeWidth = $0 }
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Expand the message box")
        .accessibilityIdentifier("shell-composer-expand")
        .transition(.opacity)
    }

    /// The draft's lines, measured in the field's width (`textHeight`, `lineHeight`).
    private var measuring: some View {
        ZStack(alignment: .topLeading) {
            Text(verbatim: measuredText)
                .lineLimit(maximumLines + 1)
                .frame(maxWidth: .infinity, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { textHeight = $0 }
            Text(verbatim: " ").fixedSize()
                .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { lineHeight = $0 }
        }
        .font(Theme.Font.body)
        .padding(.horizontal, expanded ? Theme.Space.l : Theme.Space.xs) // the field's own
        .hidden()
        .accessibilityHidden(true)
    }

    private func enlarge() {
        // What the keyboard is still composing goes into the draft first: the page's editor is another text view (#2203).
        if focused.wrappedValue {
            TypedText.commitFocused(into: $text)
        } else {
            // An unfocused field's selection is stale (a restored draft's, PathInsert.edit): the page starts at the end.
            selection = TextSelection(insertionPoint: text.endIndex)
        }
        if dictation.isListening { dictation.stop() }
        enlarged = true
    }

    /// The page's Send (or ⌘⏎ there), after it confirmed what the keyboard was composing: as the box's send
    /// (`sendTyped`), then the page goes down and the keyboard with it.
    private func sendFromPage() {
        sentFromPage = true
        enlarged = false
        send()
    }

    private var dismissKeyboardButton: some View {
        Button(action: dismissKeyboard) {
            ThemedIcon(Theme.Icon.dismissKeyboard)
                .font(Theme.Font.heading)
                .fontWeight(.medium)
                .foregroundStyle(ink)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Hide Keyboard")
        .accessibilityIdentifier("shell-composer-dismiss-keyboard")
        .transition(.opacity)
    }

    private func dismissKeyboard() {
        Haptics.tap()
        if focused.wrappedValue { TypedText.commitFocused(into: $text) }
        focused.wrappedValue = false
        Keyboard.dismiss()
    }
    #endif

    @ViewBuilder private var plus: some View {
        #if os(iOS)
        if let openAttach {
            // A tap opens the panel; touch and hold opens the menu (Safari's tabs button, Mail's compose button).
            Menu { attach() } label: { plusFace } primaryAction: { openAttach(plusFrame) }
                .menuStyle(.button)
                .buttonStyle(.plain)
                .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { plusFrame = $0 }
                .accessibilityLabel("Add photo or file")
                .accessibilityIdentifier("shell-composer-add")
        } else {
            plusMenu
        }
        #else
        plusMenu
        #endif
    }

    private var plusMenu: some View {
        Menu { attach() } label: { plusFace }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .accessibilityLabel("Add photo or file")
            .accessibilityIdentifier("shell-composer-add")
    }

    private var plusFace: some View {
        ThemedIcon(Theme.Icon.add)
            .font(Theme.Font.title)
            .fontWeight(.regular)
            .foregroundStyle(ink)
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
            .contentShape(.circle)
    }

    /// The model button's face, ChatGPT's: the model in the text's ink, a little bold, then the effort in the quieter
    /// ink, a space apart, on one line; a long model name is cut at its end, the effort stays. Its trailing room is the
    /// round button's, so the gap to the mic matches the mic's to send / stop (#1117).
    private func modesLabel(_ title: SessionModes.ComposerTitle) -> some View {
        HStack(spacing: Theme.Space.xs) {
            Text(title.model)
                .font(Theme.Font.secondaryStrong)
                .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.ink : Color.primary)
                .truncationMode(.tail)
            if let effort = title.effort {
                Text(effort)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.ink2 : Color.secondary)
                    .fixedSize()
            }
            // Claude Code's ultracode on, or a Codex thread at "ultra" (#1307): read out with the title (`spoken`).
            if title.mark != nil {
                UltracodeMark(font: Theme.Font.labelStrong, spoken: nil).transition(.opacity)
            }
        }
        .animation(UltracodeMark.motion, value: title.mark)
        .lineLimit(1)
        .padding(.leading, Theme.Space.s)
        .padding(.trailing, Theme.Space.xs)
        .scaledFrame(minHeight: Theme.Size.tapTarget)
        .contentShape(.capsule)
    }

    /// The box opening and closing as the keyboard comes and goes: on the iPhone with the keyboard's own spring.
    private var openMotion: Animation {
        #if os(iOS)
        Theme.Motion.keyboard
        #else
        Theme.Motion.layout
        #endif
    }

    private var focusMotion: Animation {
        #if os(iOS)
        Theme.Motion.keyboard
        #else
        Theme.Motion.standard
        #endif
    }

    /// Telegram's motion (send growing in, the quote bar sliding) is the iPhone's and iPad's; the Mac's card keeps its own.
    private var telegramMotion: Bool {
        #if os(iOS)
        true
        #else
        false
        #endif
    }

    private var quoteTransition: AnyTransition { telegramMotion ? .composerAccessory : .opacity }

    private var swapMotion: Animation {
        #if os(iOS)
        reduceMotion ? Theme.Motion.state : Theme.Motion.composerSwap
        #else
        Theme.Motion.state
        #endif
    }

    /// Speech to text into the field (`ComposerMicButton`, shared with an agent's composer); on the iPhone it can also
    /// be held down (`ComposerHoldMic`).
    @ViewBuilder private var mic: some View {
        #if os(iOS)
        ComposerHoldMic(dictation: dictation, text: $text, hold: hold, id: "shell-composer-mic")
        #else
        ComposerMicButton(dictation: dictation, text: $text, id: "shell-composer-mic")
        #endif
    }

    private func stopButton(_ stop: @escaping () -> Void) -> some View {
        ComposerRoundButton.stop(id: "shell-composer-stop") { endDictation(then: stop) }
    }

    /// Round in send's place; beside stop (the agent works) a plain icon like dictation's, so the line keeps one round button.
    private func voiceButton(_ voice: @escaping () -> Void) -> some View {
        ComposerVoiceButton(round: stop == nil, id: "shell-composer-voice") {
            focused.wrappedValue = false
            endDictation(then: voice)
        }
    }

    /// Sends what the field shows: the keyboard's composition confirmed into the draft first (`TypedText`, #2203).
    private func sendTyped() {
        #if os(iOS) || os(visionOS)
        if focused.wrappedValue { TypedText.commitFocused(into: $text) }
        #endif
        #if os(macOS)
        if let history { MacSendHistory.shared.record(history, text) }
        #endif
        send()
    }

    private func endDictation(then action: () -> Void) {
        if dictation.isListening { dictation.stop() }
        action()
    }
}

/// The composer's surface: the white card with its stroke and shadow, or in the BeautifulUI style its raised surface.
/// The voice bar wears it too (it takes the composer's place in a call, docs/assistant/voice.md 6.2).
struct ComposerSurface: ViewModifier {
    func body(content: Content) -> some View {
        if AppStyle.current.usesBeautifulUI {
            content.buiSurface(radius: Theme.Shell.composerRadius, elevation: .raised)
        } else {
            content
                .background {
                    RoundedRectangle(cornerRadius: Theme.Shell.composerRadius, style: .continuous)
                        .fill(Theme.Shell.composer)
                        .shadow(color: .black.opacity(0.08), radius: 12, y: 4)
                }
                .overlay {
                    RoundedRectangle(cornerRadius: Theme.Shell.composerRadius, style: .continuous)
                        .strokeBorder(Theme.Shell.composerStroke, lineWidth: Theme.Size.hairline)
                }
        }
    }
}

#if os(iOS)
/// The composer's full page (#3167, ChatGPT's): the same draft and cursor as the box (`text`, `selection` are the
/// caller's, `model.drafts` stays the one copy), its quotes on top, Collapse and Send in the bar. Photos and files stay in the
/// composer under the sheet and go with the message all the same.
private struct ComposerPageEditor: View {
    var placeholder: String
    @Binding var text: String
    /// The caller's cursor: the page's own starts from it, takes it again when something moved it (a file's path
    /// inserted) and gives it back as the page goes down. Bound straight to it, every keystroke's cursor went round
    /// through the caller's view and came back late: a letter now and then went missing (iPad, both styles).
    @Binding var selection: TextSelection?
    /// Bumped when something asked the box for the keyboard: the page takes it.
    var focusRequest: Int
    var canSend: Bool
    var stop: (() -> Void)?
    var quotes: Binding<[ContextRef]>?
    var collapse: () -> Void
    var send: () -> Void
    @FocusState private var focused: Bool
    /// Sent once: a second tap or ⌘⏎ before the page is gone does not send again.
    @State private var sent = false
    @State private var cursor: TextSelection?
    /// The caller's cursor as the page took it: moved since, someone else put it somewhere.
    @State private var taken: TextSelection?

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                if let quotes, !quotes.wrappedValue.isEmpty {
                    QuoteBar(refs: quotes)
                        .padding(.horizontal, Theme.Space.l)
                        .accessibilityIdentifier("composer-page-quotes")
                }
                TextEditor(text: $text, selection: $cursor)
                    .font(Theme.Font.body)
                    .focused($focused)
                    .scrollContentBackground(.hidden)
                    .overlay(alignment: .topLeading) {
                        if text.isEmpty {
                            // Where the text view's first line starts: its inset, then its line padding.
                            Text(placeholder)
                                .font(Theme.Font.body)
                                .foregroundStyle(.tertiary)
                                .padding(.top, Theme.Space.s)
                                .padding(.leading, Theme.Space.xs + Theme.Space.xxs)
                                .allowsHitTesting(false)
                                .accessibilityHidden(true)
                        }
                    }
                    .padding(.horizontal, Theme.Space.m)
                    .shellScaled() // the box's type size, a size up
                    .accessibilityLabel(placeholder)
                    .accessibilityIdentifier("composer-page-field")
                HStack {
                    Spacer()
                    if !canSend, let stop {
                        ComposerRoundButton.stop(id: "composer-page-stop", stop)
                    } else {
                        ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "composer-page-send", action: sendOnce)
                            .keyboardShortcut(.return, modifiers: .command)
                            .disabled(!canSend)
                    }
                }
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.s)
            }
            .frame(maxWidth: ChatView.readableWidth) // an iPad's page: the conversation's measure, not the window's
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            .themedPage()
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItemGroup(placement: .topBarTrailing) {
                    if focused {
                        Button(action: dismissPageKeyboard) {
                            ThemedIcon(Theme.Icon.dismissKeyboard)
                        }
                        .accessibilityLabel("Hide Keyboard")
                        .accessibilityIdentifier("composer-page-dismiss-keyboard")
                    }
                    Button {
                        commitTyped()
                        collapse()
                    } label: {
                        ThemedIcon(Theme.Icon.collapseMessage)
                    }
                    .accessibilityLabel("Collapse the message box")
                    .accessibilityIdentifier("composer-page-collapse")
                }
            }
        }
        .onAppear {
            cursor = selection ?? TextSelection(insertionPoint: text.endIndex)
            taken = selection
            focused = true
        }
        .onChange(of: focusRequest) {
            if selection != taken { // where the caller put it (after an inserted path); not for a bare ⌘I
                cursor = selection
                taken = selection
            }
            focused = true
        }
        // A swipe down, or the page going away under it (another conversation, the tree swapped): what the keyboard
        // is still composing stays in the draft. Not after a send: the text view could still hold the sent text.
        .onDisappear {
            guard !sent else { return }
            commitTyped()
            selection = cursor
        }
    }

    private func sendOnce() {
        guard !sent else { return }
        sent = true
        commitTyped()
        send()
    }

    /// The keyboard's composition confirmed into the draft (`TypedText`, #2203); only while the page's editor has the
    /// keyboard, never another text field's text.
    private func commitTyped() {
        if focused { TypedText.commitFocused(into: $text) }
    }

    private func dismissPageKeyboard() {
        Haptics.tap()
        commitTyped()
        focused = false
        Keyboard.dismiss()
    }
}
#endif

#if DEBUG && os(iOS)
/// `probe.composer-lines` (`-probes YES`): whether the focused text view cuts a line at its top or its foot, for a test
/// that the composer shows whole lines while typing (#3703). Read from the text's line fragments (TextKit 2 only:
/// asking a TextKit 2 view for its layout manager would turn it into a TextKit 1 one).
private struct ComposerLinesProbe: View {
    var maximumLines: Int
    var viewport: CGFloat
    var line: CGFloat
    @State private var lines = ComposerLines()

    var body: some View {
        Color.clear
            .scaledFrame(width: Theme.Size.nonvisualProbe, height: Theme.Size.nonvisualProbe)
            .task {
                while !Task.isCancelled {
                    if let view = FirstResponder.current as? UITextView, let read = ComposerLines(view) { lines = read }
                    try? await Task.sleep(for: .milliseconds(300))
                }
            }
            .screenProbe("composer-lines", lines.sized(maximumLines: maximumLines, viewport: viewport, line: line))
    }
}

struct ComposerLines: Encodable, Equatable {
    /// Lines of text, and whether one is cut by the text view's top or foot edge (part of it shows, part does not).
    var count = 0
    var cutTop = false
    var cutFoot = false
    var visible = 0.0
    var offset = 0.0
    /// What the box was sized by: its line limit, the height it measured above the keyboard, a line's height.
    var maximumLines = 0
    var viewport = 0.0
    var line = 0.0

    init() {}

    func sized(maximumLines: Int, viewport: CGFloat, line: CGFloat) -> ComposerLines {
        var sized = self
        sized.maximumLines = maximumLines
        sized.viewport = viewport.rounded()
        sized.line = line.rounded()
        return sized
    }

    @MainActor init?(_ view: UITextView) {
        guard let layout = view.textLayoutManager, let range = layout.textContentManager?.documentRange else { return nil }
        layout.ensureLayout(for: range)
        var lines: [CGRect] = []
        layout.enumerateTextLayoutFragments(from: range.location, options: [.ensuresLayout]) { fragment in
            let origin = fragment.layoutFragmentFrame.origin
            for line in fragment.textLineFragments {
                lines.append(line.typographicBounds.offsetBy(dx: origin.x, dy: origin.y))
            }
            return true
        }
        // What the view's bounds show (it clips to them), in the text container's coordinates.
        let container = view.textContainerInset
        let top = view.contentOffset.y - container.top
        let foot = view.contentOffset.y + view.bounds.height - container.top
        count = lines.count
        // Cut: more than a quarter of the line on each side of the edge. A sliver of a line's leading past the edge (a
        // scroll to the caret leaves a few points) shows no glyph.
        func cut(_ line: CGRect, at edge: CGFloat) -> Bool {
            let quarter = line.height / 4
            return edge - line.minY > quarter && line.maxY - edge > quarter
        }
        cutTop = lines.contains { cut($0, at: top) }
        cutFoot = lines.contains { cut($0, at: foot) }
        visible = (foot - top).rounded()
        offset = top.rounded()
    }
}
#endif

