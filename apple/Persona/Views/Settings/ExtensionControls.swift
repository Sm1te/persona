import SwiftUI

// UI version 6's controls in rows (docs/extensions/extensions.md "UI version 6", "Controls", #827): a switch or a
// stepper at the row's end, a slider on a line of its own under it. What a change does is the model's
// (`ExtensionViewModel.change`: shown at once, one request a control, a failure put back); here is what is drawn, when
// a change is sent (a switch at once, a slider when the finger lifts, a stepper 0.6 s after the last tap) and its
// confirm. The controls are the system's, so VoiceOver reads and adjusts them as any other (their value as written).

nonisolated extension ExtensionRowControl {
    /// A control this app draws: a known kind with a name and an action; a slider also needs `min < max`.
    var drawable: Bool {
        guard !name.isEmpty, !action.id.isEmpty, ExtensionRowControlKind.all.contains(kind) else { return false }
        guard kind == ExtensionRowControlKind.slider else { return true }
        guard let lo = min, let hi = max, lo.isFinite, hi.isFinite else { return false }
        return lo < hi
    }

    /// Its value as the extension drew it, as the wire writes it: a switch "true" / "false", else the number.
    var wire: String {
        kind == ExtensionRowControlKind.switch ? (on == true ? "true" : "false") : write(value ?? min ?? 0)
    }

    /// A slider's or a stepper's step: its own, else a hundredth of a slider's range (a stepper's: 1).
    var stepSize: Double {
        if let s = step, s > 0, s.isFinite { return s }
        if kind == ExtensionRowControlKind.slider, let lo = min, let hi = max, hi > lo { return (hi - lo) / 100 }
        return 1
    }

    /// Where a slider or a stepper can go: its min…max, open where it has none.
    var bounds: ClosedRange<Double> {
        let lo = min ?? -Double.greatestFiniteMagnitude
        return lo...Swift.max(lo, max ?? Double.greatestFiniteMagnitude)
    }

    /// `v` on the control's grid (min + k·step), held to min…max, without float dust (22.499999 → 22.5).
    func snap(_ v: Double) -> Double {
        let base = min ?? 0
        let x = Swift.min(Swift.max(base + ((v - base) / stepSize).rounded() * stepSize, bounds.lowerBound), bounds.upperBound)
        let scale = pow(10, Double(decimals))
        return (x * scale).rounded() / scale
    }

    /// The decimals its numbers have: its step's or its minimum's, at most 6.
    var decimals: Int { Swift.min(6, Swift.max(Self.decimals(of: stepSize), Self.decimals(of: min ?? 0))) }

    private static func decimals(of n: Double) -> Int {
        guard n.isFinite, n != n.rounded() else { return 0 }
        let s = String(n)
        if let e = s.range(of: "e-") { return Int(s[e.upperBound...]) ?? 0 }
        return s.split(separator: ".").last.map(\.count) ?? 0
    }

    /// A number as the wire writes it: a dot, no exponent and no trailing zeros ("22.5", "60").
    func write(_ v: Double) -> String {
        var s = String(format: "%.\(decimals)f", v)
        if s.contains(".") {
            while s.hasSuffix("0") { s.removeLast() }
            if s.hasSuffix(".") { s.removeLast() }
        }
        return s == "-0" ? "0" : s
    }

    /// The value as people read it: the extension's `display` for the value it drew, else the number in the user's
    /// way of writing numbers and the unit.
    func written(_ v: Double) -> String {
        if let d = display, !d.isEmpty, let drawn = value, abs(drawn - v) < stepSize / 2 { return d }
        let n = v.formatted(.number.precision(.fractionLength(0...decimals)))
        guard let u = unit, !u.isEmpty else { return n }
        return n + " " + u
    }
}

/// A row with a control (UI version 6): the row's look with a switch or a stepper at its end, or a slider on a line
/// under it. A row with its own action does it when its words are tapped; the control beside them stays the
/// control's. A control the extension disables, or one on a card that cannot send it, is drawn dimmed.
struct ExtensionControlRow: View {
    var row: ExtensionRowComponent
    var control: ExtensionRowControl
    var model: (any ExtensionViewControlling)?
    @Binding var link: LinkTarget?
    @Environment(AppModel.self) private var app
    @State private var confirming = false
    /// The value the confirm asks about: kept after the alert clears its flag, which it does before its button runs.
    @State private var asking: String?
    /// A stepper's last tap (or a slider's last move without a drag: VoiceOver, a keyboard), sent once they pause.
    @State private var stepping: (value: String, task: Task<Void, Never>)?
    @State private var dragging = false

    private var name: String { control.name }
    private var usable: Bool { model?.sendsControls == true && control.disabled != true }
    private var shown: String { model?.controlValues[name] ?? control.wire }
    private var number: Double { Double(shown) ?? control.value ?? control.min ?? 0 }

    var body: some View {
        Group {
            switch control.kind {
            case ExtensionRowControlKind.slider: sliderRow
            case ExtensionRowControlKind.stepper: stepperRow
            default: switchRow
            }
        }
        .alert(control.action.confirm ?? "", isPresented: $confirming) {
            Button { if let v = asking { send(v) } } label: { Text(confirmTitle).lineLimit(1) }
                .accessibilityIdentifier("plugin-ui-confirm")
            Button(role: .cancel) { model?.preview(name, nil) } label: { Text("Cancel").lineLimit(1) }
                .accessibilityIdentifier("plugin-ui-confirm-cancel")
        }
        .onDisappear { flushStep() }
    }

    // MARK: Kinds

    @ViewBuilder private var switchRow: some View {
        let isOn = Binding(get: { shown == "true" }, set: { change($0 ? "true" : "false") })
        if let action = row.action, let model {
            HStack(spacing: Theme.Space.m) {
                opening(action, model) { spinner }
                Toggle(isOn: isOn) { Text(row.title) }
                    .labelsHidden()
                    .disabled(!usable)
                    .accessibilityIdentifier("plugin-ui-switch-\(name)")
            }
        } else {
            // A settings row: the switch is labeled by the whole row, as one element for VoiceOver.
            Toggle(isOn: isOn) {
                ExtensionComponentRowLabel(row: row, model: model, chevron: false) { spinner }
            }
            .disabled(!usable)
            .accessibilityIdentifier("plugin-ui-switch-\(name)")
        }
    }

    private var sliderRow: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            words {
                HStack(spacing: Theme.Space.s) {
                    spinner
                    valueText
                }
            }
            Slider(value: Binding(get: { Swift.min(Swift.max(number, control.bounds.lowerBound), control.bounds.upperBound) },
                                  set: { slide(to: $0) }),
                   // No `step:`: iOS 26 draws a tick for every step (a hundred dots under a 0–100 slider); the value
                   // is put on the control's grid as it moves (`snap`).
                   in: control.bounds) {
                Text(row.title)
            } onEditingChanged: { editing in
                // A drag sends when the finger lifts.
                dragging = editing
                if !editing { stepping?.task.cancel(); stepping = nil; change(shown) }
            }
            .labelsHidden()
            .disabled(!usable)
            .accessibilityValue(control.written(number))
            .accessibilityIdentifier("plugin-ui-slider-\(name)")
        }
    }

    private var stepperRow: some View {
        words {
            HStack(spacing: Theme.Space.s) {
                spinner
                valueText
                Stepper(value: Binding(get: { number }, set: { stepTo($0) }), in: control.bounds, step: control.stepSize) {
                    Text(row.title)
                }
                .labelsHidden()
                .fixedSize()
                .disabled(!usable)
                .accessibilityValue(control.written(number))
                .accessibilityIdentifier("plugin-ui-stepper-\(name)")
            }
        }
    }

    // MARK: Parts

    /// The row's words with `trailing` at their end: its action when it has one (tapping the words), else just them.
    @ViewBuilder private func words<T: View>(@ViewBuilder trailing: () -> T) -> some View {
        if let action = row.action, let model {
            opening(action, model) { trailing() }
        } else {
            ExtensionComponentRowLabel(row: row, model: model, chevron: false) { trailing() }
        }
    }

    private func opening<T: View>(_ action: ExtensionAction, _ model: any ExtensionViewControlling,
                                  @ViewBuilder trailing: () -> T) -> some View {
        // Borderless: in a list, a plain button takes the whole row's taps, the control's too.
        ExtensionActionButton(action: action, title: row.title, model: model, link: $link) {
            ExtensionComponentRowLabel(row: row, model: model, chevron: true) { trailing() }
        }
        .buttonStyle(.borderless)
        .foregroundStyle(Color.primary)
    }

    @ViewBuilder private var spinner: some View {
        if model?.slowControls.contains(name) == true {
            ProgressView().controlSize(.small).accessibilityHidden(true)
        }
    }

    private var valueText: some View {
        Text(control.written(number))
            .font(Theme.Font.body.monospacedDigit())
            .foregroundStyle(.secondary)
            .lineLimit(1)
            .contentTransition(.numericText())
            // The title gives way first: the value is what the control is about.
            .fixedSize()
            .accessibilityHidden(true)
    }

    private var confirmTitle: String {
        guard control.kind == ExtensionRowControlKind.switch else { return String(localized: "Confirm") }
        return asking == "true" ? String(localized: "Turn On") : String(localized: "Turn Off")
    }

    // MARK: Sending

    /// A slider moved: shown at once; dragged, it goes when the finger lifts, else (VoiceOver, a keyboard) as a
    /// stepper's taps.
    private func slide(to v: Double) {
        guard usable else { return }
        if dragging { model?.preview(name, control.write(control.snap(v))) } else { stepTo(v) }
    }

    /// A stepper tapped: the value shows at once and goes 0.6 s after the last tap.
    private func stepTo(_ v: Double) {
        guard usable else { return }
        let value = control.write(control.snap(v))
        model?.preview(name, value)
        stepping?.task.cancel()
        let task = Task {
            try? await Task.sleep(for: .milliseconds(600))
            guard !Task.isCancelled else { return }
            stepping = nil
            change(value)
        }
        stepping = (value, task)
    }

    /// A stepper's taps not sent yet go now (the row scrolled away or the page closed).
    private func flushStep() {
        guard let s = stepping else { return }
        s.task.cancel()
        stepping = nil
        change(s.value)
    }

    /// The user changed it to `value`: asked first when the action says so (cancelled, it goes back), else sent.
    private func change(_ value: String) {
        guard usable else { return }
        if let confirm = control.action.confirm, !confirm.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            model?.preview(name, value)
            asking = value
            confirming = true
            return
        }
        send(value)
    }

    private func send(_ value: String) {
        guard let model else { return }
        Task { ExtensionOutcome.show(await model.change(control, to: value), app: app, link: $link) }
    }
}
