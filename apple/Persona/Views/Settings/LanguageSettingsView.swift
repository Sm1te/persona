import Speech
import SwiftUI
#if os(macOS)
import AppKit
#endif

/// Settings › Appearance › Language (docs/protocol/i18n.md「Choosing a language」, docs/apple/ios.md「Interface language」): Match System / 「简体中文」 / English / 「日本語」.
/// The choice is written where Foundation reads it when the app starts, so it shows the next time the app opens;
/// until then the page says so (the Mac can reopen itself).
struct LanguageSettingsPage: View {
    @State private var choice = LanguageStore.shared.choice

    private var store: LanguageStore { .shared }

    var body: some View {
        SettingsForm {
            Section {
                // Wrap the MainActor-isolated method in a closure instead of passing `set: pick`: Xcode 26.6
                // (Swift 6.3) crashes IRGen emitting the reabstraction thunk for the isolated method value when
                // building for macOS (#4157). Keep the wrapper until the compiler is fixed.
                Picker("Language", selection: Binding(get: { choice }, set: { pick($0) })) {
                    ForEach(AppLanguage.allCases) { language in
                        Text(language == .system ? systemTitle : language.title)
                            .lineLimit(1)
                            .tag(language)
                            .accessibilityIdentifier("language-\(language.rawValue)")
                    }
                }
                .pickerStyle(.inline)
                .labelsHidden()
            } footer: {
                Text("With Match System, Persona uses Simplified Chinese when your first system language is any Chinese (Traditional too), Japanese for Japanese, and English for everything else. Apple Watch, the Dynamic Island and widgets follow this setting, and so do notifications and the assistant's replies from your hub.")
            }
            if store.needsRelaunch {
                Section {
                    VStack(alignment: .leading, spacing: Theme.Space.s) {
                        Text(relaunchNote)
                            .font(Theme.Font.body)
                        #if os(macOS)
                        Button(String(localized: "Reopen Now"), action: Self.relaunch)
                            .themedButton(prominent: true)
                            .lineLimit(1)
                            .accessibilityIdentifier("language-relaunch")
                        #endif
                    }
                    .padding(.vertical, Theme.Space.xs)
                }
            }
            DictationLanguageSection()
        }
        .navigationTitle("Language")
        #if os(iOS) || os(visionOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
    }

    /// Match System, and what it comes to on this device.
    private var systemTitle: String {
        let name = AppLanguage(rawValue: AppLanguage.resolve(store.systemFirst))?.title ?? ""
        return String(localized: "Match System (\(name))")
    }

    private var relaunchNote: String {
        let name = AppLanguage(rawValue: store.current)?.title ?? store.current
        #if os(macOS)
        return String(localized: "Persona switches to \(name) the next time it opens.")
        #else
        return String(localized: "Persona switches to \(name) after you quit it (swipe it up in the App Switcher) and open it again.")
        #endif
    }

    private func pick(_ language: AppLanguage) {
        store.choice = language
        choice = language
        #if os(iOS)
        WatchSync.shared.push(language: store.current)
        #endif
    }

    #if os(macOS)
    /// Opens a new copy of the app, then quits this one.
    static func relaunch() {
        let config = NSWorkspace.OpenConfiguration()
        config.createsNewApplicationInstance = true
        NSWorkspace.shared.openApplication(at: Bundle.main.bundleURL, configuration: config) { _, _ in
            Task { @MainActor in NSApp.terminate(nil) }
        }
    }
    #endif
}

/// The language dictation listens in (#5005, `DictationLanguage`): Automatic, or one the phone recognizes that the owner
/// uses. Takes effect at the next tap on a mic.
struct DictationLanguageSection: View {
    /// "" is Automatic.
    @State private var choice = UserDefaults.standard.string(forKey: DictationLanguage.choiceKey) ?? ""
    @State private var options: [String] = []

    var body: some View {
        Section {
            Picker(selection: Binding(get: { choice }, set: { pick($0) })) {
                Text("Automatic").lineLimit(1).tag("")
                ForEach(options, id: \.self) { locale in
                    Text(DictationLanguage.name(locale)).lineLimit(1).tag(locale)
                }
                // A language picked before that the phone no longer lists among the owner's still shows as picked.
                if !choice.isEmpty, !options.contains(choice) { Text(DictationLanguage.name(choice)).lineLimit(1).tag(choice) }
            } label: {
                Text("Dictation").lineLimit(1)
            }
            .pickerStyle(.menu)
            .accessibilityIdentifier("dictation-language")
        } footer: {
            Text("Automatic listens for the language you write in the chat, else the one it last listened for, else Persona's language. Choose a language if it often guesses wrong.")
        }
        .task { options = await Self.options() }
    }

    private func pick(_ locale: String) {
        choice = locale
        if locale.isEmpty {
            UserDefaults.standard.removeObject(forKey: DictationLanguage.choiceKey)
        } else {
            UserDefaults.standard.set(locale, forKey: DictationLanguage.choiceKey)
        }
    }

    /// Asked off the main actor: the recognizer's list is the system's.
    private static func options() async -> [String] {
        await Task.detached(priority: .userInitiated) {
            DictationLanguage.options(interface: AppLanguage.codes, preferred: Locale.preferredLanguages,
                                      region: Locale.current.region?.identifier,
                                      supported: SFSpeechRecognizer.supportedLocales().map(\.identifier))
        }.value
    }
}

extension AppLanguage {
    /// The row's value in Settings › Appearance: what the interface will be in.
    static var settingsSummary: String {
        let store = LanguageStore.shared
        let name = AppLanguage(rawValue: store.current)?.title ?? store.current
        return store.choice == .system ? String(localized: "Match System (\(name))") : name
    }
}
