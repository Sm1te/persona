import Foundation
import NaturalLanguage

/// Which language dictation listens in (#5005; docs/apple/ios.md ("Dictation language")). On-device recognition hears only
/// the one language it is set to, and Persona's interface language is not always the one spoken: an iPhone in English,
/// say, whose owner speaks Chinese. First match wins:
/// 1. the owner's choice (Settings › Appearance › Language › Dictation), when it is not Automatic;
/// 2. the conversation's language: the draft and the owner's own recent messages in this chat, when the language
///    recognizer is sure;
/// 3. the language of the last sure choice (1 or 2), so a new, empty chat follows the last one;
/// 4. Persona's interface language, as before; then Simplified Chinese.
/// Within a language the region comes from the owner's preferred languages, then the phone's region, then the language's
/// usual one. Only languages the phone can recognize are picked.
nonisolated enum DictationLanguage {
    /// The owner's choice: a recognizer locale ("zh-CN"); absent: Automatic.
    static let choiceKey = "dictation.language"
    /// The last sure choice, written only when it changes.
    static let recentKey = "dictation.language.recent"
    /// What dictation always used before it followed the interface language.
    static let fallback = "zh-CN" // i18n-ignore

    enum Source: Equatable { case choice, conversation, recent, interface, fallback }

    struct Pick: Equatable {
        var locale: String
        var source: Source
        /// Worth remembering for the next chat: the owner said so, or the conversation did.
        var sure: Bool { source == .choice || source == .conversation }
    }

    /// What the choice is made from.
    struct Context {
        /// The owner's choice; nil: Automatic.
        var choice: String?
        /// The draft, then the owner's messages in this chat, newest first.
        var texts: [String]
        var recent: String?
        /// Persona's interface language ("en", "zh-Hans", "ja").
        var interface: String
        /// The phone's preferred languages (`Locale.preferredLanguages`), first first.
        var preferred: [String]
        /// The phone's region ("US").
        var region: String?
        /// The locales on-device recognition has ("zh-CN", "en-US"…).
        var supported: [String]
    }

    static func pick(_ c: Context) -> Pick {
        let supported = c.supported.map(normalized)
        let have = Set(supported)
        if let choice = c.choice.map(normalized), have.contains(choice) { return Pick(locale: choice, source: .choice) }
        let languages = Set(supported.map(language))
        if let written = written(c.texts, among: languages),
           let locale = locale(for: written, preferred: c.preferred, region: c.region, supported: supported) {
            return Pick(locale: locale, source: .conversation)
        }
        if let recent = c.recent.map(normalized), have.contains(recent) { return Pick(locale: recent, source: .recent) }
        if let locale = locale(for: language(of: c.interface), preferred: c.preferred, region: c.region, supported: supported) {
            return Pick(locale: locale, source: .interface)
        }
        return Pick(locale: fallback, source: .fallback)
    }

    // MARK: What the owner writes in

    /// How much of each text is read, and how many texts: enough to tell the language, never a long pasted log.
    static let textRunes = 400
    static let textCount = 6
    /// How sure the recognizer has to be, and how many letters it needs to be read at all ("ok" says nothing).
    static let sureAbove = 0.8
    static let fewestLetters = 3

    /// The language `texts` are written in (`language(_:)` form: "zh-Hans", "en"…), when it is one of `languages` and
    /// the recognizer is sure; nil otherwise.
    static func written(_ texts: [String], among languages: Set<String>) -> String? {
        let sample = texts.lazy.map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
            .prefix(textCount).map { String($0.prefix(textRunes)) }.joined(separator: "\n")
        guard sample.filter(\.isLetter).count >= fewestLetters, !languages.isEmpty else { return nil }
        let recognizer = NLLanguageRecognizer()
        recognizer.languageConstraints = languages.map(NLLanguage.init(rawValue:))
        recognizer.processString(sample)
        guard let best = recognizer.languageHypotheses(withMaximum: 1).max(by: { $0.value < $1.value }), best.value >= sureAbove else { return nil }
        let found = language(of: best.key.rawValue)
        return languages.contains(found) ? found : nil
    }

    /// The owner's own words in a chat, newest first: what they typed, not what another session, a channel, a meeting
    /// or Persona put in on their side.
    static func ownerTexts(_ items: [Item]) -> [String] {
        var out: [String] = []
        for item in items.reversed() where out.count < textCount && typedByOwner(item) {
            if let text = item.text { out.append(text) }
        }
        return out
    }

    private static func typedByOwner(_ item: Item) -> Bool {
        guard item.role == "user", item.kind == .text, item.wake != true else { return false }
        return item.from == nil && item.injected == nil && item.source == nil
    }

    // MARK: Locales

    /// The language a locale or a language tag stands for, as the recognizer and the language recognizer both name it:
    /// Chinese by script ("zh-CN" → "zh-Hans", "zh-TW" and "zh-HK" → "zh-Hant"), anything else by its language ("en-US"
    /// → "en").
    static func language(of tag: String) -> String {
        let parts = normalized(tag).split(separator: "-").map(String.init)
        guard let first = parts.first?.lowercased() else { return "" }
        guard first == "zh" else { return first }
        if parts.contains(where: { $0.caseInsensitiveCompare("Hant") == .orderedSame }) { return "zh-Hant" }
        if parts.contains(where: { $0.caseInsensitiveCompare("Hans") == .orderedSame }) { return "zh-Hans" }
        return ["TW", "HK", "MO"].contains(region(of: tag) ?? "") ? "zh-Hant" : "zh-Hans"
    }

    /// The recognizer locale for `language` ("zh-Hans", "en"): the region of the owner's preferred language for it, else
    /// the phone's region, else the language's usual one, else the first; nil when the phone recognizes none of it.
    static func locale(for language: String, preferred: [String], region: String?, supported: [String]) -> String? {
        let candidates = supported.map(normalized).filter { Self.language(of: $0) == language }.sorted()
        guard !candidates.isEmpty else { return nil }
        func inRegion(_ r: String?) -> String? { r.flatMap { r in candidates.first { Self.region(of: $0) == r } } }
        for tag in preferred where Self.language(of: tag) == language {
            if let match = inRegion(Self.region(of: tag)) { return match }
        }
        return inRegion(region) ?? inRegion(usualRegion[language]) ?? candidates.first
    }

    /// Where a language is spoken when nothing else says where.
    static let usualRegion = ["zh-Hans": "CN", "zh-Hant": "TW", "en": "US", "ja": "JP", "ko": "KR", "fr": "FR", "de": "DE",
                              "es": "ES", "it": "IT", "pt": "BR", "ru": "RU", "yue": "CN"]

    /// The region subtag ("US" of "en-US", "CN" of "zh-Hans-CN"), if any.
    static func region(of tag: String) -> String? {
        normalized(tag).split(separator: "-").dropFirst().map(String.init)
            .first { ($0.count == 2 && $0.allSatisfy(\.isLetter)) || ($0.count == 3 && $0.allSatisfy(\.isNumber)) }?.uppercased()
    }

    static func normalized(_ tag: String) -> String { tag.replacingOccurrences(of: "_", with: "-") }

    // MARK: Settings

    /// The languages the Dictation picker offers: the one for each of the owner's preferred languages, then each of
    /// Persona's, that the phone can recognize; one per language, in that order.
    static func options(interface: [String], preferred: [String], region: String?, supported: [String]) -> [String] {
        var seen = Set<String>(), out: [String] = []
        for tag in preferred + interface {
            let lang = language(of: tag)
            guard !seen.contains(lang), let locale = locale(for: lang, preferred: preferred, region: region, supported: supported) else { continue }
            seen.insert(lang)
            out.append(locale)
        }
        return out
    }

    /// A locale's name in its own language (Chinese in Chinese, "English (United States)"), as the language picker
    /// names languages.
    static func name(_ locale: String) -> String {
        let id = normalized(locale)
        return Locale(identifier: id).localizedString(forIdentifier: id) ?? id
    }
}
