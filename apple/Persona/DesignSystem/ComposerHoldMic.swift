#if os(iOS)
import BeautifulUI
import SwiftUI

/// What a held mic is doing, shared by the mic (which takes the finger) and the composer's box (which draws the strip,
/// the circle under the finger and the lock over itself, `ComposerHoldOverlay`).
@Observable
final class ComposerHold {
    /// The finger is holding the mic and dictation is on; what is heard waits here, not in the draft.
    private(set) var holding = false
    /// Where the finger is from where it went down.
    var translation: CGSize = .zero
    private(set) var heard = ""
    private(set) var since = Date.now
    /// The mic's frame in the box (`ComposerHold.space`).
    var micFrame: CGRect = .zero
    /// The draft when the hold began; what is heard goes after it.
    @ObservationIgnored private var draft = ""
    /// Locked: dictation goes on with the finger up, writing into the draft as a tapped mic does.
    @ObservationIgnored private var locked = false
    @ObservationIgnored private var run = UUID()

    static let space = "composer-hold"

    /// The finger has held long enough: dictation starts, what it hears kept aside until the finger decides. `hints`
    /// tell it the language spoken (`Dictation.start`), after the draft.
    func begin(_ dictation: Dictation, text: Binding<String>, hints: [String] = [], reduceMotion: Bool) {
        draft = text.wrappedValue
        heard = ""
        locked = false
        translation = .zero
        since = .now
        run = UUID(); let mine = run
        Haptics.tap()
        withAnimation(reduceMotion ? nil : Theme.Motion.holdAppear) { holding = true }
        Task {
            let problem = await dictation.start(hints: [draft] + hints) { [weak self] words in
                guard let self, self.run == mine else { return }
                if self.locked { text.wrappedValue = DictationHold.joined(self.draft, words) } else { self.heard = words }
            }
            if let problem {
                AppModel.shared.toast = Toast(problem, style: .error)
                if run == mine { end(reduceMotion: reduceMotion) }
            } else if run != mine || (!holding && !locked) {
                dictation.stop() // the finger let go while the microphone was still being asked for
            }
        }
    }

    func finish(_ outcome: DictationHold.Outcome, _ dictation: Dictation, text: Binding<String>, reduceMotion: Bool) {
        guard holding else { return }
        switch outcome {
        case .keep:
            dictation.stop()
            text.wrappedValue = DictationHold.joined(draft, heard)
        case .cancel:
            dictation.stop()
        case .lock:
            locked = true
            text.wrappedValue = DictationHold.joined(draft, heard)
        }
        if outcome != .lock { run = UUID() }
        end(reduceMotion: reduceMotion)
    }

    private func end(reduceMotion: Bool) {
        withAnimation(reduceMotion ? nil : Theme.Motion.holdLeave) {
            holding = false
            translation = .zero
        }
    }
}

/// The shell composer's mic on the iPhone: a tap turns dictation on or off as before; held, it dictates while held
/// (`DictationHold`), Telegram's voice button.
struct ComposerHoldMic: View {
    let dictation: Dictation
    @Binding var text: String
    let hold: ComposerHold
    var id: String
    /// What tells dictation the language spoken besides the draft: the owner's recent messages in this chat.
    var hints: () -> [String] = { [] }
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    /// Counts the touch toward a hold; cancelled when the finger lifts first (a tap).
    @State private var pending: Task<Void, Never>?
    /// The finger is down; the gesture resets it when the system takes the touch away without an end.
    @GestureState private var touching = false
    /// This touch was decided while the finger was still down: the rest of it changes nothing.
    @State private var decided = false

    var body: some View {
        ComposerMicFace(listening: dictation.isListening && !hold.holding)
            .gesture(press)
            .onChange(of: touching) { _, down in
                guard !down else { return }
                pending?.cancel()
                pending = nil
                decided = false // a touch the system took away leaves the next one free
                // Taken away mid-hold (a call, a system alert): it goes on, as Telegram's locks.
                if hold.holding { hold.finish(.lock, dictation, text: $text, reduceMotion: reduceMotion) }
            }
            .onGeometryChange(for: CGRect.self) { $0.frame(in: .named(ComposerHold.space)) } action: { hold.micFrame = $0 }
            .onDisappear { // the line's mic gone under the finger (the box opened): the hold carries on, as a taken-away touch does
                pending?.cancel()
                pending = nil
                if hold.holding { hold.finish(.lock, dictation, text: $text, reduceMotion: reduceMotion) }
            }
            .accessibilityElement()
            .accessibilityAddTraits(.isButton)
            .accessibilityLabel(dictation.isListening ? String(localized: "Stop Dictation") : String(localized: "Dictate"))
            .accessibilityHint("Touch and hold to dictate. Slide left to cancel, or up to keep dictating.")
            .accessibilityAction { dictation.toggle(into: $text, hints: hints) }
            .accessibilityIdentifier(id)
            .shellScaled()
    }

    private var press: some Gesture {
        DragGesture(minimumDistance: 0, coordinateSpace: .global)
            .updating($touching) { _, down, _ in down = true }
            .onChanged { value in
                if hold.holding {
                    hold.translation = value.translation
                    if let outcome = DictationHold.whileHeld(value.translation) {
                        decided = true
                        hold.finish(outcome, dictation, text: $text, reduceMotion: reduceMotion)
                    }
                } else if pending == nil, !decided, !dictation.isListening {
                    pending = Task {
                        try? await Task.sleep(for: Theme.Hold.delay)
                        guard !Task.isCancelled else { return }
                        decided = true // a hold, even one that could not start: letting go is no tap
                        hold.begin(dictation, text: $text, hints: hints(), reduceMotion: reduceMotion)
                    }
                }
            }
            .onEnded { value in
                pending?.cancel()
                pending = nil
                if hold.holding {
                    hold.finish(DictationHold.onRelease(value.translation, velocity: value.velocity), dictation, text: $text,
                                reduceMotion: reduceMotion)
                } else if !decided {
                    dictation.toggle(into: $text, hints: hints) // a tap
                }
                decided = false
            }
    }
}

/// Over the box while the mic is held: a strip with the recording dot, the time, what is heard so far and "Slide to
/// cancel" following the finger at half its pace; the circle under the finger; the lock above the mic.
struct ComposerHoldOverlay: View {
    let hold: ComposerHold
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.buiTheme) private var theme

    var body: some View {
        // Each part comes and goes with its own transition: one around them all would hide theirs.
        ZStack(alignment: .topLeading) {
            if hold.holding { strip.transition(.opacity) }
            if hold.holding { lock }
            if hold.holding { circle }
        }
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }

    private var surface: Color { AppStyle.current.usesBeautifulUI ? theme.colors.surface : Theme.Shell.composer }

    private var strip: some View {
        HStack(spacing: Theme.Space.s) {
            RecordingDot(blinks: !reduceMotion)
            Text(timerInterval: hold.since...Date.distantFuture, countsDown: false)
                .font(Theme.Font.body.monospacedDigit())
                .fixedSize()
            VStack(alignment: .leading, spacing: 0) {
                if !hold.heard.isEmpty {
                    Text(verbatim: hold.heard)
                        .font(Theme.Font.secondary)
                        .lineLimit(1)
                        .truncationMode(.head)
                }
                slideLabel
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .clipped() // the label slides away under the time
            Spacer(minLength: hold.micFrame.width)
        }
        .padding(.leading, Theme.Space.l)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(surface, in: .rect(cornerRadius: Theme.Shell.composerRadius, style: .continuous))
    }

    /// Telegram's: it follows the finger at half its pace and fades over the last points before the drop.
    private var slideLabel: some View {
        HStack(spacing: Theme.Space.xs) {
            ThemedIcon(Theme.Icon.slideToCancel)
            Text("Slide to cancel").lineLimit(1)
        }
        .font(Theme.Font.secondary)
        .foregroundStyle(.secondary)
        .offset(x: -DictationHold.labelShift(hold.translation))
        .opacity(DictationHold.labelOpacity(hold.translation))
    }

    private var lock: some View {
        let progress = DictationHold.lockProgress(hold.translation)
        let mic = hold.micFrame
        let arrow: CGFloat = max(0, 1 - Theme.Hold.lockArrowFade * progress)
        return VStack(spacing: Theme.Space.xxs) {
            ThemedIcon(Theme.Icon.locked).font(Theme.Font.secondary)
            ThemedIcon(Theme.Icon.slideToLock).font(Theme.Font.label)
                .opacity(arrow)
        }
        .foregroundStyle(.secondary)
        .frame(width: Theme.Hold.lockWidth, height: Theme.Hold.lockHeight - Theme.Hold.lockShrink * progress)
        .modifier(ComposerSurface())
        .position(x: mic.midX, y: mic.midY - Theme.Hold.lockLift + Theme.Hold.lockShrink * progress / 2)
        .offset(y: -DictationHold.travel(hold.translation).up)
        .transition(.offset(y: Theme.Hold.lockLift).combined(with: .opacity))
    }

    private var circle: some View {
        let mic = hold.micFrame
        let (left, up) = DictationHold.travel(hold.translation)
        return ThemedIcon(Theme.Icon.dictating)
            .font(Theme.Font.title)
            .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.canvas : Theme.Shell.inkText)
            .frame(width: Theme.Hold.circle, height: Theme.Hold.circle)
            .background(AppStyle.current.usesBeautifulUI ? theme.colors.ink : Theme.Shell.ink, in: .circle)
            .scaleEffect(DictationHold.circleScale(hold.translation))
            .position(x: mic.midX - left, y: mic.midY - up)
            .transition(.scale(scale: Theme.Motion.holdAppearScale).combined(with: .opacity))
    }
}

/// The red dot of a recording: it blinks on a one-second beat (Telegram's), or stays lit with Reduce Motion.
private struct RecordingDot: View {
    var blinks: Bool
    @State private var dim = false

    var body: some View {
        Circle()
            .fill(Theme.danger)
            .scaledFrame(width: Theme.Size.dotXL, height: Theme.Size.dotXL)
            .opacity(dim ? 0 : 1)
            .onAppear {
                guard blinks else { return }
                withAnimation(Theme.Motion.holdBlink.repeatForever(autoreverses: true)) { dim = true }
            }
    }
}
#endif
