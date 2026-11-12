import Foundation

/// An upstream refusal Claude Code recorded (#2528, #4489; docs/protocol/protocol.md「Going back to the configured
/// model」): on a notice item (`Item.modelRefusal`), and on the session while its fallback is still in use
/// (`Session.modelFallback`). Only the public fields are read: an older hub's `raw` (the record's whole line) is never
/// decoded, kept or shown. Every field is optional; a value of an unexpected type is left unread rather than dropping
/// the item or session it came with.
nonisolated struct ModelRefusal: Codable, Hashable, Sendable {
    /// `model_refusal_fallback` when Claude Code switched models; another subtype when it only refused.
    var subtype: String
    var originalModel: String?
    var fallbackModel: String?
    var category: String?
    /// `session`: later turns of the session may keep using the fallback.
    var scope: String?
    var explanation: String?
    /// The refusal's own words, shown as they are.
    var text: String?

    static let fallbackSubtype = "model_refusal_fallback"

    enum CodingKeys: String, CodingKey {
        case subtype, category, scope, explanation, text
        case originalModel = "original_model", fallbackModel = "fallback_model"
    }

    init(subtype: String, originalModel: String? = nil, fallbackModel: String? = nil, category: String? = nil,
         scope: String? = nil, explanation: String? = nil, text: String? = nil) {
        self.subtype = subtype
        self.originalModel = originalModel
        self.fallbackModel = fallbackModel
        self.category = category
        self.scope = scope
        self.explanation = explanation
        self.text = text
    }

    init(from decoder: Decoder) {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        // A field that isn't a non-empty string counts as absent (as the web's `publicRefusal`).
        func field(_ key: CodingKeys) -> String? {
            guard let s = (try? c?.decodeIfPresent(String.self, forKey: key)) ?? nil, !s.isEmpty else { return nil }
            return s
        }
        subtype = field(.subtype) ?? ""
        originalModel = field(.originalModel)
        fallbackModel = field(.fallbackModel)
        category = field(.category)
        scope = field(.scope)
        explanation = field(.explanation)
        text = field(.text)
    }

    /// Claude Code switched to another model, and says which.
    var isFallback: Bool { subtype == Self.fallbackSubtype && fallbackModel != nil }

    /// The notice's text: the refusal's words, else its explanation, else its subtype; never the record's line.
    var publicText: String {
        if let text, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return text }
        return explanation ?? subtype
    }
}

/// Going back to the configured model is pending (`Session.modelRestore`): the next start uses it. It ends with a newer
/// reply (from any model), a replaced or missing fallback record, or a node restart; it never says the model runs.
nonisolated struct ModelRestore: Codable, Hashable, Sendable {
    /// `next_start`, or a state a later hub adds.
    var state: String
    /// The configured model the next start uses.
    var to: String?

    static let nextStart = "next_start"

    enum CodingKeys: String, CodingKey { case state, to }

    init(state: String, to: String? = nil) {
        self.state = state
        self.to = to
    }

    init(from decoder: Decoder) {
        let c = try? decoder.container(keyedBy: CodingKeys.self)
        state = ((try? c?.decodeIfPresent(String.self, forKey: .state)) ?? nil) ?? ""
        to = ((try? c?.decodeIfPresent(String.self, forKey: .to)) ?? nil).flatMap { $0.isEmpty ? nil : $0 }
    }

    var pending: Bool { state == Self.nextStart }
}

extension Item {
    /// The item as it is kept and drawn: a refusal notice's text is the refusal's public words, never an older hub's
    /// record line (#4489).
    func publicRefusal() -> Item {
        guard let r = modelRefusal, text != r.publicText else { return self }
        var item = self
        item.text = r.publicText
        return item
    }
}

extension Array where Element == Item {
    /// Refusal notices with their public words (#4489); the same array when there is none, so an ordinary page isn't
    /// copied.
    func publicRefusals() -> [Item] { contains { $0.modelRefusal != nil } ? map { $0.publicRefusal() } : self }
}
