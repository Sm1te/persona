import Foundation
import Testing
@testable import Persona

/// Which language dictation listens in (#5005, `DictationLanguage`): the owner's choice, else the conversation's language,
/// else the last sure choice, else Persona's interface language; so an iPhone in English whose owner speaks Chinese
/// hears Chinese.
struct DictationLanguageTests {
    /// What on-device recognition offers on a typical phone (a part of it).
    private let supported = ["en-US", "en-GB", "en-AU", "zh-CN", "zh-TW", "zh-HK", "yue-CN", "ja-JP", "fr-FR", "de-DE"]

    private func context(choice: String? = nil, texts: [String] = [], recent: String? = nil, interface: String = "en",
                         preferred: [String] = ["en-US"], region: String? = "US") -> DictationLanguage.Context {
        DictationLanguage.Context(choice: choice, texts: texts, recent: recent, interface: interface, preferred: preferred,
                                  region: region, supported: supported)
    }

    // MARK: The reported case: an English system, a Chinese speaker

    @Test func anEnglishPhoneHearsChineseInAChineseConversation() {
        let pick = DictationLanguage.pick(context(texts: ["", "帮我把明天下午三点的会改到四点", "顺便提醒我带上周报"],
                                                  preferred: ["en-US", "zh-Hans-CN"], region: "US"))
        #expect(pick == .init(locale: "zh-CN", source: .conversation))
        #expect(pick.sure, "remembered for the next chat")
    }

    @Test func anEnglishPhoneHearsChineseWhenTheDraftIsChinese() {
        #expect(DictationLanguage.pick(context(texts: ["今天下午有什么会"])).locale == "zh-CN")
    }

    @Test func aNewChatFollowsTheLastSureChoice() {
        let pick = DictationLanguage.pick(context(texts: [""], recent: "zh-CN"))
        #expect(pick == .init(locale: "zh-CN", source: .recent))
        #expect(!pick.sure)
    }

    @Test func theOwnersChoiceWins() {
        let pick = DictationLanguage.pick(context(choice: "zh-CN", texts: ["Please move the 3 pm meeting to 4"], recent: "en-US"))
        #expect(pick == .init(locale: "zh-CN", source: .choice))
        #expect(DictationLanguage.pick(context(choice: "zh_CN")).locale == "zh-CN", "an identifier with an underscore")
    }

    @Test func aChoiceThePhoneCannotRecognizeIsPassedOver() {
        #expect(DictationLanguage.pick(context(choice: "ko-KR", texts: ["今天下午有什么会"])).source == .conversation)
    }

    // MARK: Without anything to go on: the interface language, as before

    @Test func withNothingElseTheInterfaceLanguage() {
        #expect(DictationLanguage.pick(context()) == .init(locale: "en-US", source: .interface))
        #expect(DictationLanguage.pick(context(preferred: ["en-GB"], region: "GB")).locale == "en-GB")
        #expect(DictationLanguage.pick(context(interface: "zh-Hans", preferred: ["zh-Hans-CN"], region: "CN")).locale == "zh-CN")
        #expect(DictationLanguage.pick(context(interface: "ja", preferred: ["ja-JP"], region: "JP")).locale == "ja-JP")
    }

    @Test func tooLittleToTellIsNotAConversationLanguage() {
        #expect(DictationLanguage.pick(context(texts: ["ok"], recent: "zh-CN")).source == .recent)
        #expect(DictationLanguage.pick(context(texts: ["   ", "👍", "42"])).source == .interface)
    }

    @Test func aChineseInterfaceHearsEnglishInAnEnglishConversation() {
        let pick = DictationLanguage.pick(context(texts: ["Can you check why the login tests fail on CI today?"], interface: "zh-Hans",
                                                  preferred: ["zh-Hans-CN", "en-GB"], region: "CN"))
        #expect(pick == .init(locale: "en-GB", source: .conversation), "the region of the owner's English")
    }

    @Test func traditionalChineseByItsRegion() {
        #expect(DictationLanguage.pick(context(texts: ["請幫我把明天下午三點的會議改到四點"])).locale == "zh-TW")
        #expect(DictationLanguage.pick(context(texts: ["請幫我把明天下午三點的會議改到四點"], preferred: ["en-US", "zh-Hant-HK"])).locale == "zh-HK")
    }

    @Test func nothingRecognizableFallsBackToChinese() {
        let none = DictationLanguage.Context(choice: nil, texts: [], recent: nil, interface: "en", preferred: ["en-US"], region: "US",
                                             supported: [])
        #expect(DictationLanguage.pick(none) == .init(locale: "zh-CN", source: .fallback))
    }

    // MARK: The parts

    @Test func onlyTheOwnersOwnWordsCount() {
        func item(_ seq: Int, role: String = "user", _ text: String) -> Item {
            Item(id: "\(seq)#0", seq: seq, ts: nil, role: role, kind: .text, text: text, toolUseID: nil, toolName: nil,
                 toolInput: nil, isError: nil, image: nil)
        }
        var fromAgent = item(3, "Message from another session")
        fromAgent.from = "review"
        var injected = item(4, "A scheduled run")
        injected.injected = Injected(kind: "followup", line: "A scheduled run")
        var meeting = item(5, "Lines from the meeting")
        meeting.source = .object(["kind": .string("meeting")])
        let items = [item(1, "first"), item(2, role: "assistant", "a reply"), fromAgent, injected, meeting, item(6, "newest")]
        #expect(DictationLanguage.ownerTexts(items) == ["newest", "first"])
        let many = (1...10).map { item($0, "m\($0)") }
        #expect(DictationLanguage.ownerTexts(many) == ["m10", "m9", "m8", "m7", "m6", "m5"])
    }

    @Test func languagesAndRegions() {
        #expect(DictationLanguage.language(of: "zh-CN") == "zh-Hans")
        #expect(DictationLanguage.language(of: "zh_TW") == "zh-Hant")
        #expect(DictationLanguage.language(of: "zh-HK") == "zh-Hant")
        #expect(DictationLanguage.language(of: "zh-Hant-SG") == "zh-Hant")
        #expect(DictationLanguage.language(of: "zh-Hans") == "zh-Hans")
        #expect(DictationLanguage.language(of: "en-US") == "en")
        #expect(DictationLanguage.region(of: "zh-Hans-CN") == "CN")
        #expect(DictationLanguage.region(of: "es-419") == "419")
        #expect(DictationLanguage.region(of: "ja") == nil)
    }

    @Test func thePickerOffersTheOwnersLanguagesFirst() {
        let options = DictationLanguage.options(interface: AppLanguage.codes, preferred: ["en-GB", "zh-Hans-CN", "ko-KR"], region: "GB",
                                                supported: supported)
        #expect(options == ["en-GB", "zh-CN", "ja-JP"], "one per language; Korean isn't recognized on this phone")
    }

    @Test func choicesAreNamedInTheirOwnLanguage() {
        #expect(DictationLanguage.name("zh-CN").hasPrefix("中文"))
        #expect(DictationLanguage.name("en-US").hasPrefix("English"))
        #expect(DictationLanguage.name("ja-JP").hasPrefix("日本語"))
    }
}
