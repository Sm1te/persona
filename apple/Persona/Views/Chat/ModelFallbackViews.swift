import SwiftUI

/// What an upstream refusal and its fallback say (#2528, #4489), as the web (`chat-modelfallback`): the refusal's own line
/// in the transcript, and the card above the composer while the session still runs on the fallback model. Models are
/// named by their short names (`SessionModes.modelName`; the raw id when it has none).
enum ModelFallbackText {
    /// `Session.turnErrorCode` when the turn failed because the fallback model is missing: Continue would go to it again.
    static let unavailableCode = "model_fallback_unavailable"

    static func model(_ id: String?) -> String { SessionModes.modelName(id) ?? id ?? "?" }

    // MARK: The refusal's line

    static var refusalTitle: String { String(localized: "The upstream refused this request") }
    static var refusalTextLabel: String { String(localized: "Refusal text") }

    /// The facts under the title, in the web's order: the switch (or that none is recorded), later turns, category,
    /// explanation. The refusal's words go apart (`ModelRefusal.text`).
    static func refusalFacts(_ r: ModelRefusal) -> [String] {
        var out: [String] = []
        if r.isFallback {
            let from = model(r.originalModel), to = model(r.fallbackModel)
            out.append(String(localized: "Claude Code switched from \(from) to \(to) on its own"))
            if r.scope == "session" { out.append(String(localized: "Later turns of this session may keep using it")) }
        } else {
            out.append(String(localized: "The record doesn't say another model was used"))
        }
        if let c = r.category { out.append(String(localized: "Category: \(c)")) }
        if let e = r.explanation { out.append(String(localized: "Explanation: \(e)")) }
        return out
    }

    /// The refusal's words when it has any (shown as they are).
    static func words(_ r: ModelRefusal) -> String? {
        guard let t = r.text, !t.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return nil }
        return t
    }

    // MARK: The card above the composer

    struct Card: Equatable {
        var title: String
        var lines: [String]
        /// Back to configured model is offered: a Persona conversation not already going back.
        var offersRestore: Bool
    }

    /// Nil unless the session still runs on a fallback model. `headless`: a Persona conversation, which can go back from
    /// here; a terminal session gets only the /model step.
    static func card(_ s: Session, headless: Bool) -> Card? {
        guard let f = s.modelFallback, f.isFallback else { return nil }
        let pending = s.modelRestore?.pending == true
        var lines: [String] = []
        if let o = f.originalModel { let m = model(o); lines.append(String(localized: "Originally requested model: \(m)")) }
        let fallback = model(f.fallbackModel)
        lines.append(String(localized: "Fallback model: \(fallback)"))
        if pending, let to = s.modelRestore?.to { let m = model(to); lines.append(String(localized: "Configured model: \(m)")) }
        if let e = s.turnError, !e.isEmpty { lines.append(String(localized: "Error since: \(e)")) }
        if pending { lines.append(String(localized: "Which model actually runs is only known from this session's next reply.")) }
        lines.append(headless
            ? String(localized: "No message is resent; the same request may be refused again.")
            : String(localized: "For a terminal session, type /model in Claude Code yourself; it may also change the default model for new sessions."))
        return Card(title: pending ? String(localized: "The next start will use the configured model")
                        : String(localized: "After an upstream refusal, this session switched to a fallback model"),
                    lines: lines, offersRestore: headless && !pending)
    }

    /// A refused going back, in words: the codes the hub documents, else its own words.
    static func restoreError(_ error: Error) -> String {
        switch (error as? APIError)?.code ?? "" {
        case "restore_busy": String(localized: "This conversation is working, so nothing was closed. Try again once it stops.")
        case "restore_unsupported": String(localized: "This session can't go back to the configured model from here. Use /model in its terminal.")
        case "restore_nothing": String(localized: "This session no longer runs on a fallback model.")
        case "restore_unknown": String(localized: "Persona can't tell right now which model this session uses or is configured with, so nothing was closed. Try again later.")
        case "node_outdated": String(localized: "personad on this computer is too old to go back to the configured model. Update it.")
        default: error.shown ?? String(localized: "Try again later.")
        }
    }
}

extension AppModel {
    /// Back to configured model on a Persona conversation's fallback card; a refusal is said in a toast. True when the
    /// hub took it (the card then waits for the session to say the next start is pending).
    func restoreConfiguredModel(_ key: SessionKey) async -> Bool {
        guard let api = api(key.group) else { return false }
        Haptics.tap()
        do {
            try await api.restoreModel(key)
            return true
        } catch {
            Haptics.error()
            toast = Toast(ModelFallbackText.restoreError(error), style: .error)
            return false
        }
    }
}

/// An upstream refusal in the transcript (#4489): a line of its own after the exchange, never folded into the work row.
/// What happened (the switch, or that none is recorded), then the refusal's words as they are.
struct RefusalNotice: View {
    var refusal: ModelRefusal

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.error).font(Theme.Font.label).foregroundStyle(Theme.caution)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(ModelFallbackText.refusalTitle).font(Theme.Font.labelStrong).foregroundStyle(Theme.cautionText)
                ForEach(ModelFallbackText.refusalFacts(refusal), id: \.self) { line in
                    Text(line).font(Theme.Font.label).foregroundStyle(.secondary)
                }
                if let words = ModelFallbackText.words(refusal) {
                    Text(ModelFallbackText.refusalTextLabel).font(Theme.Font.label).foregroundStyle(.tertiary)
                        .padding(.top, Theme.Space.xxs)
                    Text(words).font(Theme.Font.label).foregroundStyle(.secondary).textSelection(.enabled)
                        .accessibilityIdentifier("refusal-text")
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, Theme.Space.m)
        .padding(.vertical, Theme.Space.s)
        .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("refusal-notice")
        .contextMenu { CopyButton(text: ModelFallbackText.words(refusal) ?? refusal.publicText) }
    }
}

/// Over the composer while the session still runs on a fallback model (#4489): which models, the error since, and what
/// can be done. A terminal session: only the /model step. A Persona conversation: Back to configured model, and New Main
/// Chat on the main conversation where the hub has it. While going back is pending: only that the next start uses the
/// configured model, and which; nothing more is claimed.
struct ModelFallbackCard: View {
    @Environment(AppModel.self) private var model
    let key: SessionKey
    var session: Session
    /// The conversation is answering: going back waits until it stops (the hub would refuse, `restore_busy`).
    var working: Bool
    @State private var busy = false

    var body: some View {
        if let card = ModelFallbackText.card(session, headless: session.assistant == true) {
            let restore = card.offersRestore && !working && !busy
            let newMain = session.assistant == true && model.canStartNewMain(key)
            ReadinessCard(title: card.title, detail: card.lines.joined(separator: "\n"), tone: .caution,
                          actionTitle: restore ? String(localized: "Back to Configured Model") : nil,
                          action: restore ? { go() } : nil,
                          altTitle: newMain ? String(localized: "New Main Chat") : nil,
                          alt: newMain ? { model.newMainAsked = true } : nil,
                          busy: busy)
                .accessibilityIdentifier("model-fallback-card")
        }
    }

    private func go() {
        busy = true
        Task {
            _ = await model.restoreConfiguredModel(key)
            busy = false
        }
    }
}
