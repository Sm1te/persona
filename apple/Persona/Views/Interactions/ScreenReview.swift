import SwiftUI

/// How an answer from a screen card of several questions goes out (#2970). By its option, so the hub's history names
/// what was picked; as its keys when it may be the last answer, because the node presses the review tab's Submit
/// answers after an answer that names its option, and the user reviews first.
nonisolated enum ScreenAnswerRoute {
    /// A choice tapped.
    static func choice(_ o: ScreenParser.Dialog.Option, _ d: ScreenParser.Dialog, step: Int?, hubRead: Bool) -> Resolution {
        guard hubRead, !ScreenStepsLogic.submitsAll(d, current: step) else { return Resolution(keys: [o.key]) }
        return Resolution(option: o.key)
    }

    /// Words for "Type something.": words already in its field are cleared first (to the end, then back to the start,
    /// as the node clears a multi-select field), which only keys can say.
    static func typed(_ o: ScreenParser.Dialog.Option, text: String, _ d: ScreenParser.Dialog, step: Int?, hubRead: Bool) -> Resolution {
        let filled = !(o.text ?? "").isEmpty
        if hubRead, !filled, !ScreenStepsLogic.submitsAll(d, current: step) { return Resolution(text: text, option: o.key) }
        return Resolution(keys: ["enter"], text: text, preKeys: filled ? [o.key, "ctrl+e", "ctrl+u"] : [o.key])
    }

    /// The answer as the review shows it: the labels picked, then the words.
    static func summary(_ d: ScreenParser.Dialog, keys: [String], text: String) -> String {
        let labels = d.options.filter { keys.contains($0.key) && $0.kind == .plain }.map { QuestionOption.display($0.label).0 }
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        return (labels + (t.isEmpty ? [] : [t])).joined(separator: String(localized: ", ", comment: "Separator between items in a list"))
    }
}

/// The answers of a dialog of several questions before they are submitted (#2970): the terminal's review tab, or the
/// review the app shows before the last multi-select answer. A row per question; one not answered says so, and the main
/// button then takes you to it, with Submit Without Them under it.
struct ScreenReview: View {
    var rows: [ScreenReviewRow]
    /// What is being sent (a spinner on its button).
    var sending: String?
    var back: (() -> Void)?
    var submit: () -> Void
    /// Go and answer that question.
    var answer: ((ScreenReviewRow) -> Void)?

    private var missing: ScreenReviewRow? { rows.first { $0.answer == nil } }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Review Your Answers").font(Theme.Font.heading)
            // A line a question, its label in a column of its own: the review fits the cards' area with its buttons
            // and the card's last row in sight.
            Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: Theme.Space.l, verticalSpacing: 0) {
                ForEach(rows) { row in
                    if row.index != rows.first?.index { Divider().gridCellUnsizedAxes(.horizontal) }
                    GridRow(alignment: .firstTextBaseline) {
                        Text(row.label.isEmpty ? (row.question ?? "") : row.label)
                            .font(Theme.Font.labelStrong)
                            .foregroundStyle(.secondary)
                            .padding(.vertical, Theme.Space.sm)
                        answerText(row)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(.vertical, Theme.Space.sm)
                    }
                    .accessibilityElement(children: .combine)
                }
            }
            .padding(.horizontal, Theme.Space.l)
            .padding(.vertical, Theme.Space.xs)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            .accessibilityIdentifier("screen-review")
            footer
        }
    }

    @ViewBuilder private func answerText(_ row: ScreenReviewRow) -> some View {
        switch row.answer {
        case nil:
            Label("Not answered", systemImage: Theme.Icon.error)
                .font(Theme.Font.body)
                .foregroundStyle(Theme.cautionText)
        case let a? where a.isEmpty:
            Text("Answered on the computer").font(Theme.Font.body).foregroundStyle(.secondary)
        case let a?:
            Text(a).font(Theme.Font.body).fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Previous and the main button in a row, with a question left Submit Without Them beside them; when the row has no
    /// room, Submit Without Them goes under it; at the largest text sizes all three stack, the main button first and full
    /// width (docs/design/HIG_AUDIT.md, HIG-003), each label still on one line.
    private var footer: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: Theme.Space.m) {
                previous(wide: false)
                Spacer(minLength: 0)
                if leftOne { withoutThem }
                main(wide: false)
            }
            VStack(alignment: .trailing, spacing: 0) {
                HStack(spacing: Theme.Space.m) {
                    previous(wide: false)
                    Spacer(minLength: 0)
                    main(wide: false)
                }
                if leftOne { withoutThem }
            }
            VStack(spacing: Theme.Space.s) {
                main(wide: true)
                previous(wide: true)
                if leftOne { withoutThem }
            }
        }
        .controlSize(.regular)
        .disabled(sending != nil)
    }

    private var leftOne: Bool { missing != nil && answer != nil }

    @ViewBuilder private func previous(wide: Bool) -> some View {
        if let back {
            Button { back() } label: {
                Text("Previous [Interaction Views 39b1c2]").lineLimit(1).minimumScaleFactor(0.8).frame(maxWidth: wide ? .infinity : nil)
            }
            .themedButton()
            .accessibilityIdentifier("screen-review-back")
        }
    }

    @ViewBuilder private func main(wide: Bool) -> some View {
        if let missing, let answer {
            Button { answer(missing) } label: {
                HStack(spacing: Theme.Space.sm) {
                    if sending == "walk" { ProgressView().controlSize(.small).tint(.white) }
                    Text(missing.label.isEmpty ? String(localized: "Answer Question \(missing.index + 1)") : String(localized: "Answer “\(missing.label)”"))
                        .lineLimit(1).minimumScaleFactor(0.8)
                }
                .padding(.horizontal, Theme.Space.s)
                .frame(maxWidth: wide ? .infinity : nil)
            }
            .themedButton(prominent: true)
            .accessibilityIdentifier("screen-review-answer")
        } else {
            Button { submit() } label: {
                HStack(spacing: Theme.Space.sm) {
                    if sending == "submit" { ProgressView().controlSize(.small).tint(.white) }
                    Text("Submit \(rows.count) Answers").lineLimit(1).minimumScaleFactor(0.8)
                }
                .padding(.horizontal, Theme.Space.s)
                .frame(maxWidth: wide ? .infinity : nil)
            }
            .themedButton(prominent: true)
            .accessibilityIdentifier("screen-review-submit")
        }
    }

    /// A text button, with a finger's room around its words.
    private var withoutThem: some View {
        Button { submit() } label: {
            Text("Submit Without Them").lineLimit(1).minimumScaleFactor(0.8)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .font(Theme.Font.captionMedium)
        .foregroundStyle(Theme.accent)
        .accessibilityIdentifier("screen-review-submit-anyway")
    }
}

/// While the card waits for the next question of its dialog.
struct ScreenMoving: View {
    var body: some View {
        HStack(spacing: Theme.Space.sm) {
            ProgressView().controlSize(.small)
            Text("Switching questions…").font(Theme.Font.captionMedium).foregroundStyle(.secondary).lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityIdentifier("screen-moving")
    }
}
