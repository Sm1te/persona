import Foundation

/// A card asking several questions at once through the hook (#2970): Claude Code's AskUserQuestion, the assistant's
/// ask_user, Codex's questions. Its step capsules show progress and the question it is on (they are not buttons), and
/// a review of every answer comes before they go, as on a terminal dialog of several (`ScreenStepsLogic`, `ScreenReview`).
nonisolated enum QuestionSteps {
    /// Whether the card draws steps and a review: several questions, on iPhone and iPad (the Mac app is frozen).
    static func enabled(_ questions: [Question]) -> Bool { ScreenStepsLogic.enabled && questions.count > 1 }

    /// A capsule per question, labeled with its header (Claude's chip, at most a few words), ticked once it has an answer.
    static func steps(_ questions: [Question], answered: (Int) -> Bool) -> [ScreenParser.Dialog.Step] {
        questions.enumerated().map { i, q in .init(label: label(q), done: answered(i)) }
    }

    /// The answer as the review shows it: the typed words, else the picked options in the card's order, without
    /// "(Recommended)". Nil when there is neither.
    static func shown(_ q: Question, picked: Set<String>, typed: String) -> String? {
        let words = typed.trimmingCharacters(in: .whitespacesAndNewlines)
        if !words.isEmpty { return words }
        let labels = q.options.map(\.label).filter(picked.contains).map { QuestionOption.display($0).0 }
        return labels.isEmpty ? nil : labels.joined(separator: ", ")
    }

    /// The review: a row per question, its header (else the question itself) and its answer, nil when it has none.
    static func rows(_ questions: [Question], answers: [Int: String]) -> [ScreenReviewRow] {
        questions.enumerated().map { i, q in
            ScreenReviewRow(index: i, label: label(q), question: q.question, answer: answers[i])
        }
    }

    private static func label(_ q: Question) -> String { q.header?.trimmingCharacters(in: .whitespaces) ?? "" }
}
