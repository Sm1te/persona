import BeautifulUI
import SwiftUI

// The composer's dictation and stop buttons, the shell's (docs/apple/ios.md「New shell」): every composer draws these two, so an
// agent's chat and the assistant's look and behave alike (#1079). Each is a size up like the shell's chrome
// (`shellScaled`), wherever it sits. A field outside the composer dictates with the same tap (`FieldMicButton`).

/// Speech to text into the draft: what is heard goes after what was typed; a second tap ends it. The composer owns the
/// `Dictation` (sending, stopping and leaving the page end it too).
struct ComposerMicButton: View {
    let dictation: Dictation
    @Binding var text: String
    var id = "composer-mic"
    /// What tells dictation the language spoken besides the draft: the owner's recent messages in this chat.
    var hints: () -> [String] = { [] }

    var body: some View {
        Button { dictation.toggle(into: $text, hints: hints) } label: { ComposerMicFace(listening: dictation.isListening) }
            .buttonStyle(.plain)
            .accessibilityLabel(dictation.isListening ? String(localized: "Stop Dictation") : String(localized: "Dictate"))
            .accessibilityIdentifier(id)
            .shellScaled()
    }
}

/// The mic of a text field outside the composer (a to-do's notes, #2188): the composer's tap, drawn like the text buttons
/// beside it (the caller gives the font and color). `willStart` runs just before listening starts, for a field that has
/// to open first. The view owns the `Dictation`: saving, cancelling and leaving end it.
struct FieldMicButton: View {
    let dictation: Dictation
    @Binding var text: String
    var id: String
    var willStart: () -> Void = {}

    var body: some View {
        Button {
            if !dictation.isListening { willStart() }
            dictation.toggle(into: $text)
        } label: {
            ThemedIcon(dictation.isListening ? Theme.Icon.dictating : Theme.Icon.dictate)
                .symbolEffect(.pulse, isActive: dictation.isListening)
                .padding(.horizontal, Theme.Space.xs)
                .contentShape(.rect)
        }
        .buttonStyle(.borderless)
        .accessibilityLabel(dictation.isListening ? String(localized: "Stop Dictation") : String(localized: "Dictate"))
        .accessibilityIdentifier(id)
    }
}

extension Dictation {
    /// A mic's tap: a second one ends it; otherwise what is heard goes after what `text` holds, and why it could not
    /// start shows as a toast. The draft and `hints` tell it the language spoken (`DictationLanguage`).
    func toggle(into text: Binding<String>, hints: () -> [String] = { [] }) {
        if isListening { stop(); return }
        let typed = text.wrappedValue
        let base = typed.isEmpty || typed.last?.isWhitespace == true ? typed : typed + " "
        let told = [typed] + hints()
        Task {
            if let problem = await start(hints: told, update: { heard in text.wrappedValue = base + heard }) {
                AppModel.shared.toast = Toast(problem, style: .error)
            }
        }
    }
}

/// The mic's face: the composer's ink, or the accent and a pulse while it listens.
struct ComposerMicFace: View {
    var listening: Bool
    @Environment(\.buiTheme) private var theme

    var body: some View {
        ThemedIcon(listening ? Theme.Icon.dictating : Theme.Icon.dictate)
            .font(Theme.Font.heading)
            .fontWeight(.medium)
            .foregroundStyle(listening ? Theme.accent : ComposerInk.icon(theme))
            .symbolEffect(.pulse, isActive: listening)
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
            .contentShape(.circle)
    }
}

/// The composer's round button: a symbol in an ink circle, like ChatGPT's send (the style's accent tints nothing here).
struct ComposerRoundButton: View {
    var symbol: String
    var label: String
    var id: String
    /// Shrinks while pressed (Telegram's send); the others keep the plain button's press.
    var pressScales = false
    var action: () -> Void
    @Environment(\.buiTheme) private var theme

    var body: some View {
        Button(action: action) {
            ThemedIcon(symbol)
                .font(Theme.Font.bodyHeavy)
                .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.canvas : Theme.Shell.inkText)
                .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
                .background(AppStyle.current.usesBeautifulUI ? theme.colors.ink : Theme.Shell.ink, in: .circle)
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .contentShape(.circle)
        }
        .modifier(ComposerPress(scales: pressScales))
        .accessibilityLabel(label)
        .accessibilityIdentifier(id)
        .shellScaled()
        .transition(.scale.combined(with: .opacity))
    }
}

extension ComposerRoundButton {
    /// Interrupts the agent: shown in send's place while it works and nothing is typed.
    static func stop(id: String = "composer-stop", _ action: @escaping () -> Void) -> ComposerRoundButton {
        ComposerRoundButton(symbol: Theme.Icon.interrupt, label: String(localized: "Interrupt [Composer Buttons f9d193]"), id: id, action: action)
    }
}

/// Voice Chat from the composer (docs/assistant/voice.md 6.1, `Theme.Icon.voiceChannel`): in send's place a round ink button like
/// send; beside stop (the agent works) a plain icon like dictation's.
struct ComposerVoiceButton: View {
    var round: Bool
    var id = "composer-voice"
    var action: () -> Void
    @Environment(\.buiTheme) private var theme

    var body: some View {
        if round {
            ComposerRoundButton(symbol: Theme.Icon.voiceChannel, label: String(localized: "Voice Chat"), id: id, action: action)
        } else {
            Button(action: action) {
                ThemedIcon(Theme.Icon.voiceChannel)
                    .font(Theme.Font.heading)
                    .fontWeight(.medium)
                    .foregroundStyle(ComposerInk.icon(theme))
                    .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                    .contentShape(.circle)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Voice Chat")
            .accessibilityIdentifier(id)
            .shellScaled()
            .transition(.scale.combined(with: .opacity))
        }
    }
}

/// The round button's press: the plain button's, or shrinking slowly and coming back quicker (`Theme.Motion.composerPress`).
private struct ComposerPress: ViewModifier {
    var scales: Bool

    func body(content: Content) -> some View {
        if scales { content.buttonStyle(ShrinkingPress()) } else { content.buttonStyle(.plain) }
    }

    private struct ShrinkingPress: ButtonStyle {
        func makeBody(configuration: Configuration) -> some View {
            configuration.label
                .scaleEffect(configuration.isPressed ? Theme.Motion.composerPressScale : 1)
                .animation(configuration.isPressed ? Theme.Motion.composerPress : Theme.Motion.composerRelease, value: configuration.isPressed)
        }
    }
}

extension AnyTransition {
    /// Send coming into the round button's place (Telegram's): it grows out of a point by the right edge, clearing a
    /// slight blur, and fades in quicker than it grows (`Theme.Motion.composerSwap`).
    static var composerSend: AnyTransition {
        .modifier(active: ComposerSwapGrowth(scale: Theme.Motion.composerSwapFrom, blur: Theme.Motion.composerSwapBlur),
                  identity: ComposerSwapGrowth(scale: 1, blur: 0))
            .combined(with: .opacity.animation(Theme.Motion.composerSwapFade))
    }

    /// What send takes the place of, or gives it back to: it shrinks a little and fades.
    static var composerSwapped: AnyTransition {
        .scale(scale: Theme.Motion.composerSwapScale).combined(with: .opacity.animation(Theme.Motion.composerSwapFade))
    }

    /// A bar over the composer's field (quotes): it slides in by its own height from under the box's top edge, which
    /// clips it, fading in quicker than it fades out.
    static var composerAccessory: AnyTransition {
        .asymmetric(insertion: .move(edge: .top).combined(with: .opacity.animation(Theme.Motion.composerAccessoryIn)),
                    removal: .move(edge: .top).combined(with: .opacity.animation(Theme.Motion.composerAccessoryOut)))
    }
}

private struct ComposerSwapGrowth: ViewModifier {
    var scale: CGFloat
    var blur: CGFloat

    func body(content: Content) -> some View {
        content.scaleEffect(scale, anchor: .trailing).blur(radius: blur)
    }
}

/// The ink of the composer's icons: the text color, or in the BeautifulUI style its quieter second ink.
enum ComposerInk {
    static func icon(_ theme: BUITheme) -> Color { AppStyle.current.usesBeautifulUI ? theme.colors.ink2 : Color.primary }
}
