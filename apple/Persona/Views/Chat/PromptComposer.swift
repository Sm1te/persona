import AVFoundation
import BeautifulUI
import OSLog
import Speech
import SwiftUI

/// The composer's box, shared by an agent's chat (`Composer`) and a new assistant conversation (`StarterComposer`):
/// BeautifulUI's prompt bar laid out as a toolbar. The draft is on top; under it + (photos, camera, files), @, /, the
/// model, the mic, and send — or, while the agent works and nothing is typed, stop.
/// On iOS the mic and stop are the shell composer's (`ComposerMicButton`, `ComposerRoundButton`, #1079), drawn over the
/// bar where its own would be; the Mac keeps the bar's.
/// The callers keep their own state: the chat's drafts, SendSafety and uploads stay in `Composer`.
struct PromptComposer: View {
    var placeholder: String
    @Binding var text: String
    @Binding var selection: TextSelection?
    var focused: FocusState<Bool>.Binding
    /// Photos or quoted context the caller holds: sendable without text.
    var hasAttachments: Bool
    var disabled = false
    /// Set while the agent works.
    var stop: (() -> Void)?
    var attach: [PromptSource]
    /// Built when the @ menu opens (it reads the chat), not on every keystroke.
    var mentions: () -> [PromptSource] = { [] }
    var commands: [PromptCommand] = []
    var models: [PromptModel] = []
    /// The model shown; `pickModel` gets the one picked from the menu.
    var modelID = ""
    var pickModel: (String) -> Void = { _ in }
    var onAttach: (PromptSource) -> Void
    /// True when the caller took the mention (a quoted session); otherwise its `insert` is typed.
    var onMention: (PromptSource) -> Bool = { _ in false }
    /// True when the caller took the command (/clear asks first); otherwise its `insert` is typed.
    var onCommand: (PromptCommand) -> Bool = { _ in false }
    /// What goes with the message, quoted over the draft inside the bar's surface (#986).
    var quotes: Binding<[ContextRef]>? = nil
    var send: () -> Void

    @State private var bar = PromptBarModel(selectedModelID: "")
    @State private var mentionRows: [PromptSource] = []
    @State private var dictation = Dictation()

    /// `PromptBar`'s control side, and how far its controls sit in from its edges (its composer's padding): where it
    /// draws send, which the shared buttons line up with.
    private static let control: CGFloat = 34
    private static let barInset: CGFloat = 6

    /// The bar would show its stop: the agent works and there is nothing to send. Ours stands in send's place.
    private var stopping: Bool {
        stop != nil && !hasAttachments && text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        #if DEBUG || PERF_SIGNPOST
        let _ = PerfTrace.body("PromptComposer")
        #endif
        var o = PromptBar.Options()
        o.layout = .stacked
        o.attachSources = attach
        o.showsMentionButton = true
        o.showsCommandButton = !commands.isEmpty
        o.controlSize = Self.control
        o.inputSize = Theme.Level.body.size * Theme.Font.scale / 1.2 // body size once the theme's ×1.2 applies
        o.cornerRadius = Theme.Radius.panel
        o.drawsSurface = false // the style's own surface (glass, paper…), below
        o.canSendExtra = hasAttachments
        o.text = $text
        o.selection = $selection
        o.focus = focused
        #if os(iOS) || os(visionOS)
        o.showsDictation = false // the shared mic and stop, over the bar (`sharedButtons`)
        #else
        o.onStop = stop
        o.onDictation = toggleDictation
        #endif
        o.onAttachSource = onAttach
        o.onPickSource = onMention
        o.onPickCommand = onCommand
        return VStack(alignment: .leading, spacing: 0) {
            if let quotes, !quotes.wrappedValue.isEmpty {
                QuoteBar(refs: quotes)
                    .padding(.horizontal, Theme.Space.m)
                    .padding(.top, Theme.Space.m)
            }
            PromptBar(model: bar, sources: mentionRows, commands: commands, models: models, placeholder: placeholder,
                      options: o, onSend: { _ in if !disabled { stopDictation(); commitTyped(); send() } })
                #if os(iOS) || os(visionOS)
                .mask { SendCutout(cut: stopping, control: Self.control, inset: Self.barInset).fill(style: FillStyle(eoFill: true)) }
                .overlay(alignment: .bottomTrailing) { sharedButtons }
                #endif
        }
            // The glass goes behind the bar, not around it: on iOS 27 a view with a glass effect takes no taps outside
            // its shape, and the bar's menus hang above it (#1005).
            .background { Color.clear.surface(interactive: true, in: .rect(cornerRadius: Theme.Radius.panel, style: .continuous)) }
            .onAppear { bar.selectedModelID = modelID }
            .onChange(of: modelID) { _, id in bar.selectedModelID = id }
            .onChange(of: bar.selectedModelID) { _, id in
                guard id != modelID else { return }
                // "Other Model…" is an action, not a model (#1644): the menu goes back to the one that runs.
                if id == PromptItems.otherModel.id { bar.selectedModelID = modelID }
                pickModel(id)
            }
            .onChange(of: bar.token?.kind == .mention) { _, open in if open { mentionRows = mentions() } }
            .onChange(of: dictation.isListening) { _, on in bar.isListening = on }
            .onDisappear { stopDictation() }
            #if DEBUG
            .task { await debugMenu() }
            #endif
    }

    #if os(iOS) || os(visionOS)
    /// The shell composer's mic and stop, where the bar draws its send: the stop's circle in send's corner (the same gap
    /// to the bar's edges; it is a little larger), the mic on the toolbar's middle line, as far before the stop as in
    /// the shell's composer.
    private var sharedButtons: some View {
        Color.clear
            .frame(width: Self.control, height: Self.control)
            .overlay(alignment: .bottomTrailing) {
                ZStack {
                    if let stop, stopping {
                        ComposerRoundButton.stop {
                            stopDictation()
                            stop()
                        }
                    }
                }
                .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
                .shellScaled()
                // The circle's edges, not its tap area's, on send's.
                .alignmentGuide(.trailing) { $0[.trailing] - Self.wellMargin($0.width) }
                .alignmentGuide(.bottom) { $0[.bottom] - Self.wellMargin($0.height) }
                .animation(Theme.Motion.state, value: stopping)
            }
            .overlay(alignment: .trailing) {
                ComposerMicButton(dictation: dictation, text: $text)
                    // Its middle a tap area and `xs` before the stop's, as in the shell's composer.
                    .alignmentGuide(.trailing) { d in
                        d[.trailing] + d.width / 2 + d.width * Theme.Size.wellL / Theme.Size.tapTarget / 2 + Theme.Space.xs
                    }
            }
            .padding(Self.barInset)
    }

    /// Between a round button's tap area (`side` wide) and its circle.
    private static func wellMargin(_ side: CGFloat) -> CGFloat {
        side * (Theme.Size.tapTarget - Theme.Size.wellL) / Theme.Size.tapTarget / 2
    }
    #endif

    #if os(macOS)
    private func toggleDictation() {
        if dictation.isListening { stopDictation(); return }
        let base = text.isEmpty || text.last?.isWhitespace == true ? text : text + " "
        Task {
            if let problem = await dictation.start(hints: [text], update: { heard in text = base + heard }) {
                AppModel.shared.toast = Toast(problem, style: .error)
            }
        }
    }
    #endif

    private func stopDictation() { if dictation.isListening { dictation.stop() } }

    /// The keyboard's composition confirmed into the draft before it goes (`TypedText`, #2203).
    private func commitTyped() {
        #if os(iOS) || os(visionOS)
        if focused.wrappedValue { TypedText.commitFocused(into: $text) }
        #endif
    }

    #if DEBUG
    /// Screenshots: `-showPromptMenu mention|command|attach|model` opens that menu a few seconds in.
    private func debugMenu() async {
        guard let which = UserDefaults.standard.string(forKey: "showPromptMenu") else { return }
        try? await Task.sleep(for: .seconds(5))
        switch which {
        case "mention": mentionRows = mentions(); text = "@"
        case "command": text = "/"
        case "attach": bar.isSourceMenuOpen = true
        default: bar.isModelMenuOpen = true
        }
    }
    #endif
}

/// What the composer's menus offer.
enum PromptItems {
    static func attachments(camera: Bool, glasses: Bool = false) -> [PromptSource] {
        var out = [PromptSource(id: "photo", name: String(localized: "Photos"), detail: String(localized: "Photos or videos, up to 4"), icon: .symbol(Theme.Icon.photos), kind: .attach)]
        if camera { out.append(PromptSource(id: "camera", name: String(localized: "Take Photo [Chat View 6e3a10]"), detail: String(localized: "Take one with the camera"), icon: .symbol("camera"), kind: .attach)) }
        if glasses { out.append(PromptSource(id: "glasses", name: String(localized: "Glasses Photo"), detail: String(localized: "Take one with the Meta glasses"), icon: .symbol(Theme.Icon.glasses), kind: .attach)) }
        out.append(PromptSource(id: "file", name: String(localized: "Files"), detail: String(localized: "Uploads to that computer and inserts the path"), icon: .symbol("doc"), kind: .attach))
        return out
    }

    /// Persona' quick messages under short "/" words (picking one types its message, sent with send), then Claude
    /// Code's own commands.
    static func commands(clear: Bool, loop: Bool = false) -> [PromptCommand] {
        var out = SlashCommand.commands(loop: loop).map { PromptCommand($0.word, detail: $0.detail, insert: $0.text) }
        if clear { out.append(PromptCommand("/clear", detail: String(localized: "Clear context and start a new session"))) }
        return out
    }

    /// Claude Code's `/model` aliases: picking one sends `/model <alias>` to the session.
    static let claudeModels: [PromptModel] = [
        PromptModel(id: "default", name: String(localized: "Default [Prompt Composer 844b8c]"), tag: String(localized: "Account default")),
        PromptModel(id: "opus", name: "Opus", tag: String(localized: "Smartest")),
        PromptModel(id: "sonnet", name: "Sonnet", tag: String(localized: "Balanced")),
        PromptModel(id: "haiku", name: "Haiku", tag: String(localized: "Fastest")),
    ]

    /// The model menu's last row (#1644): opens the field for a model ID typed by hand (`SessionTypedModelSheet`).
    static let otherModel = PromptModel(id: "persona.typed-model", name: String(localized: "Other Model…"), tag: String(localized: "Type a model ID"))

    /// What a session runs, as its model menu's selection. With the launcher's list (`RunningModelLists`, #3754): the row
    /// with that ID, else the alias that stands for it (opus → claude-opus-…; Default last, as nothing was picked then).
    /// Without it, the fixed aliases: the one its model reads as ("claude-opus-…" → opus). "default" while nothing is
    /// known; any other model (one typed by hand, #1644) is its own ID, so no row is ticked for it and Default switches back.
    static func runningModelID(_ model: String?, list: [AgentModel]? = nil) -> String {
        let m = (model ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        // Not a model ID (Claude's `<synthetic>`): nothing known.
        guard ModelID.isValid(m) else { return "default" }
        guard let list, !list.isEmpty else {
            return ["opus", "sonnet", "haiku"].first { m.lowercased().contains($0) } ?? m
        }
        if let row = list.first(where: { $0.id.caseInsensitiveCompare(m) == .orderedSame }) { return row.id }
        if let alias = list.first(where: { !$0.isDefault && $0.resolved == m }) ?? list.first(where: { $0.resolved == m }) {
            return alias.id
        }
        return m
    }

    /// The model a session runs when its menu doesn't name it (a typed ID); nil: a row does, or nothing is known.
    static func unlistedModel(_ model: String?, list: [AgentModel]? = nil) -> String? {
        let id = runningModelID(model, list: list)
        if let list, !list.isEmpty { return list.contains { $0.id == id } || id == "default" ? nil : id }
        return claudeModels.contains { $0.id == id } ? nil : id
    }

    /// A running session's model menu: the launcher's list when it has been read (New Agent's rows for that launcher:
    /// Default, the agent's models with their versions, the IDs typed before, #3754), else the fixed aliases; then the
    /// model it runs when the menu doesn't name it, by its ID (the one ticked; a menu row has no room for a second line
    /// saying it).
    static func runningModels(_ model: String?, list: [AgentModel]? = nil) -> [PromptModel] {
        let rows = list.flatMap { $0.isEmpty ? nil : $0.map { PromptModel(id: $0.id, name: menuName($0), tag: $0.detail ?? "") } }
        return (rows ?? claudeModels)
            + (unlistedModel(model, list: list).map { [PromptModel(id: $0, name: $0, tag: String(localized: "Current"))] } ?? [])
    }

    /// A list row's name in a running session's menu: an alias says the version it stands for ("Opus" → "Opus 5.5",
    /// from its `resolved`), so it reads apart from the versions beside it; Default, typed IDs and models that name
    /// their version keep their name. A menu row has room for one line only.
    static func menuName(_ m: AgentModel) -> String {
        guard !m.isDefault, !m.custom, let resolved = m.resolved, resolved != m.id,
              let version = SessionModes.modelName(resolved), version != resolved,
              !m.displayName.localizedCaseInsensitiveContains(version) else { return m.displayName }
        return version
    }

    /// BeautifulUI's model menu for a running session: `runningModels` (the button names the ticked one), then "Other Model…".
    static func composerModels(running model: String?, list: [AgentModel]? = nil) -> [PromptModel] {
        runningModels(model, list: list) + [otherModel]
    }

    /// What picking a row of a running Claude Code session's model menu does where the pick is typed as `/model`
    /// (the shell composer's, BeautifulUI's): "Other Model…" opens the field (its ID goes through POST …/modes, #1644),
    /// the model it runs already does nothing, another row sends `/model <alias>`.
    enum ModelPick: Equatable {
        case typeID
        case keep
        case send(String)
    }

    static func modelPick(_ picked: String, running model: String?, list: [AgentModel]? = nil) -> ModelPick {
        if picked == otherModel.id { return .typeID }
        if picked == runningModelID(model, list: list) { return .keep }
        return .send("/model \(picked)")
    }

    /// Files the chat's tools read or wrote, newest first: picking one types `@path` (relative to the session's
    /// folder), which Claude Code and Codex read as that file.
    static func files(_ items: [Item], cwd: String) -> [PromptSource] {
        MentionRows.usedPaths(items).map { path in
            let rel = !cwd.isEmpty && path.hasPrefix(cwd + "/") ? String(path.dropFirst(cwd.count + 1)) : path
            let name = (rel as NSString).lastPathComponent
            let dir = (rel as NSString).deletingLastPathComponent
            return PromptSource(id: "file:" + path, name: name, detail: dir.isEmpty ? String(localized: "Used in this session") : dir,
                                icon: .symbol(Theme.Icon.document), insert: "@\(rel) ")
        }
    }

    /// Other agents' sessions, most recently active first: picking one quotes it (title, folder, state, its last
    /// words) as context for the message.
    static func sessions(_ all: [Session], except key: SessionKey?) -> [PromptSource] {
        all.filter { $0.key != key && !$0.assistant }
            .sorted { ($0.updatedAt ?? .distantPast) > ($1.updatedAt ?? .distantPast) }
            .prefix(8)
            .map { s in
                PromptSource(id: "session:" + s.id, name: s.displayTitle, detail: "\(s.agentLabel) · \(s.folderName)",
                             icon: .symbol(Theme.Icon.agentChat))
            }
    }

    /// The quote for a picked session (`ContextRef`, sent as a `<persona-context>` tag before the message).
    static func quote(_ source: PromptSource, in all: [Session]) -> ContextRef? {
        guard source.id.hasPrefix("session:"), let s = all.first(where: { "session:" + $0.id == source.id }) else { return nil }
        return quote(s)
    }

    /// A session's quote: its title, agent and folder, and its last words (the shell composer's @ list, #2319, too).
    static func quote(_ s: Session) -> ContextRef {
        var lines = ["另一个会话：\(s.displayTitle)（\(s.agentLabel)）", "目录：\(s.cwd)"] // i18n-ignore
        if let last = s.lastText?.trimmingCharacters(in: .whitespacesAndNewlines), !last.isEmpty {
            lines.append("它最近说：\(String(last.prefix(600)))") // i18n-ignore
        }
        return ContextRef(kind: "context", title: s.displayTitle, url: "persona://session/\(s.machineID)/\(s.sessionID)",
                          text: lines.joined(separator: "\n"))
    }
}

/// Voice to text for the composer's mic: what is heard goes into the draft as it comes; a second tap ends it.
@Observable
final class Dictation {
    private(set) var isListening = false
    @ObservationIgnored private var engine: AVAudioEngine?
    @ObservationIgnored private var request: SFSpeechAudioBufferRecognitionRequest?
    @ObservationIgnored private var task: SFSpeechRecognitionTask?
    @ObservationIgnored private var update: ((String) -> Void)?
    @ObservationIgnored private let microphoneOwner = UUID()
    @ObservationIgnored private var listeningRun = UUID()

    /// A recognizer for the language the owner speaks (`DictationLanguage`): their choice, else the language of `hints`
    /// (the draft and their recent messages in this chat), else the last sure choice, else the interface language.
    /// A sure choice is remembered for the next chat, written only when it changed.
    private static func recognizer(hints: [String]) -> SFSpeechRecognizer? {
        let defaults = UserDefaults.standard
        let context = DictationLanguage.Context(
            choice: defaults.string(forKey: DictationLanguage.choiceKey), texts: hints,
            recent: defaults.string(forKey: DictationLanguage.recentKey),
            interface: Bundle.main.preferredLocalizations.first ?? "zh-Hans", preferred: Locale.preferredLanguages,
            region: Locale.current.region?.identifier, supported: SFSpeechRecognizer.supportedLocales().map(\.identifier))
        let pick = DictationLanguage.pick(context)
        if pick.sure, pick.locale != context.recent { defaults.set(pick.locale, forKey: DictationLanguage.recentKey) }
        Self.log.info("dictation in \(pick.locale, privacy: .public) (\(String(describing: pick.source), privacy: .public))")
        return SFSpeechRecognizer(locale: Locale(identifier: pick.locale))
    }

    private static let log = Logger(subsystem: "persona", category: "dictation")

    /// Starts listening; `update` gets the whole transcript so far. `hints` are what tells the language spoken: the
    /// draft, then the owner's recent messages in this chat, newest first. Returns what went wrong, if it could not start.
    func start(hints: [String] = [], update: @escaping (String) -> Void) async -> String? {
        #if DEBUG
        // `-showDictationSample "<words>"`: hears these words, one every beat, instead of the microphone (simulators
        // may have none), for screenshots and recordings of the composer's mic.
        if let sample = UserDefaults.standard.string(forKey: "showDictationSample"), !sample.isEmpty {
            return startSample(sample, update: update)
        }
        #endif
        #if os(iOS) || os(visionOS)
        guard RecordingCapture.shared.state == .idle else { return String(localized: "A recording is in progress. Stop it before dictating.") }
        #endif
        guard await Self.allowed() else { return String(localized: "No access to the microphone or speech recognition. Turn it on in Settings › Persona.") }
        #if os(iOS) || os(visionOS)
        guard RecordingCapture.shared.state == .idle else { return String(localized: "A recording is in progress. Stop it before dictating.") }
        #endif
        guard let recognizer = Self.recognizer(hints: hints), recognizer.isAvailable else {
            return String(localized: "Speech recognition isn't available right now")
        }
        guard RecordingMicrophone.shared.claim(microphoneOwner) else { return String(localized: "The microphone is in use. Stop the recording or dictation first.") }
        #if os(iOS) || os(visionOS) // a Mac has no audio session to configure: the engine takes the input as it is
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.record, mode: .measurement, options: .duckOthers)
            try session.setActive(true, options: .notifyOthersOnDeactivation)
        } catch {
            RecordingMicrophone.shared.release(microphoneOwner)
            return String(localized: "Couldn't open the microphone")
        }
        #endif
        let engine = AVAudioEngine()
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        Self.tap(engine.inputNode, into: request)
        engine.prepare()
        do { try engine.start() } catch {
            engine.inputNode.removeTap(onBus: 0)
            #if os(iOS) || os(visionOS)
            try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
            #endif
            RecordingMicrophone.shared.release(microphoneOwner)
            return String(localized: "Couldn't open the microphone")
        }
        self.engine = engine
        self.request = request
        self.update = update
        isListening = true
        listeningRun = UUID(); let run = listeningRun
        task = Self.recognize(recognizer, request) { [weak self] heard, done in
            Task { @MainActor in
                guard let self, self.listeningRun == run else { return }
                if let heard { self.update?(heard) }
                if done { self.stop() }
            }
        }
        return nil
    }

    #if DEBUG
    private func startSample(_ sample: String, update: @escaping (String) -> Void) -> String? {
        guard RecordingMicrophone.shared.claim(microphoneOwner) else { return String(localized: "The microphone is in use. Stop the recording or dictation first.") }
        self.update = update
        isListening = true
        listeningRun = UUID(); let run = listeningRun
        let words = sample.split(separator: " ").map(String.init)
        Task { @MainActor [weak self] in
            for count in 1...max(1, words.count) {
                try? await Task.sleep(for: .milliseconds(350))
                guard let self, self.listeningRun == run else { return }
                self.update?(words.prefix(count).joined(separator: " "))
            }
        }
        return nil
    }
    #endif

    func stop() {
        guard RecordingMicrophone.shared.release(microphoneOwner) else { return }
        listeningRun = UUID()
        engine?.stop()
        engine?.inputNode.removeTap(onBus: 0)
        request?.endAudio()
        task?.finish()
        engine = nil
        request = nil
        task = nil
        update = nil
        isListening = false
        #if os(iOS) || os(visionOS)
        try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        #endif
    }

    private static func allowed() async -> Bool {
        let speech = await withCheckedContinuation { c in
            // Answered on a background queue: `@Sendable` keeps the block off the main actor (BackgroundCallbackTests).
            SFSpeechRecognizer.requestAuthorization { @Sendable in c.resume(returning: $0) }
        }
        guard speech == .authorized else { return false }
        return await AVAudioApplication.requestRecordPermission()
    }

    // The audio tap and the recognizer call back off the main thread: their closures are made here, outside the
    // main actor, so they are not taken for main-actor code.
    nonisolated private static func tap(_ input: AVAudioInputNode, into request: SFSpeechAudioBufferRecognitionRequest) {
        input.installTap(onBus: 0, bufferSize: 1024, format: input.outputFormat(forBus: 0)) { buffer, _ in
            request.append(buffer)
        }
    }

    nonisolated private static func recognize(_ recognizer: SFSpeechRecognizer, _ request: SFSpeechAudioBufferRecognitionRequest,
                                              report: @escaping @Sendable (String?, Bool) -> Void) -> SFSpeechRecognitionTask {
        recognizer.recognitionTask(with: request) { result, error in
            report(result?.bestTranscription.formattedString, error != nil || result?.isFinal == true)
        }
    }
}

#if os(iOS) || os(visionOS)
/// What of `PromptBar` shows: all of it but, while the shared stop stands in its place, the bar's own send. The menus
/// hang above the bar, so the shape reaches far past its edges.
private nonisolated struct SendCutout: Shape {
    var cut: Bool
    var control: CGFloat
    var inset: CGFloat
    private static let reach: CGFloat = 4000

    func path(in rect: CGRect) -> Path {
        var p = Path(rect.insetBy(dx: -Self.reach, dy: -Self.reach))
        if cut {
            p.addRect(CGRect(x: rect.maxX - inset - control, y: rect.maxY - inset - control, width: control, height: control))
        }
        return p
    }
}
#endif
