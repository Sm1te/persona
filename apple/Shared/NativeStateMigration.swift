import Foundation
import Security

/// All previous-brand literals live here, including data keys, protocol names and stable system action IDs.
/// New readers prefer canonical data, then accept previous data. Compatibility never deletes a previous store.
nonisolated enum LegacyNames {
    static let product = "jarvis"
    static let capitalizedProduct = "Jarvis"
    static let urlScheme = product
    static let conversationPath = "j"
    static let operationIDHeader = "X-\(capitalizedProduct)-Op-Id"
    static let clientHeader = "X-\(capitalizedProduct)-Client"
    static let contextTag = "\(product)-context"
    static let fileTag = "\(product)-file"
    static let pushRelayInfoKey = "\(capitalizedProduct)PushRelay"
    static let wakeKey = "\(product)_wake"
    static let prefsKey = "jarvis.agents.prefs"
    static let pushService = "jarvis.push"
    static let startedByField = "started_by_jarvis"
    static let recordingKey = "jarvisRecording"
    static let recordingReceiptKey = "jarvisRecordingReceipt"
    static let pushInfo = "jarvis push v1"
    static let pushGroupKey = "jarvis_group"
    static let runtimeProtocol = "jarvis-runtime.v1"
    static let controlAskKind = "jarvis.control.ask"
    static let controlAgentsKind = "jarvis.control.agents"
    static let activityType = "\(capitalizedProduct)ActivityAttributes"
    static let infoKeys = ["PersonaAppGroup": "JarvisAppGroup", "PersonaPushKeychainGroup": "JarvisPushKeychainGroup",
                           "PersonaPushRelay": "JarvisPushRelay", "PersonaGettingStartedURL": "JarvisGettingStartedURL"]
    // Retired selection tokens are kept for compatibility tests; they are not mapped to supported personas.
    static let retiredLookIDs = ["jarvis-visor", "jarvis-circuit"]
    static let looks = ["jarvis": "persona-visor", "jarvis-circuit": "persona-circuit", "jarvis-pixel": "persona-pixel",
                        "jarvis-facet": "persona-facet", "jarvis-blob": "persona-blob"]
}

/// A non-destructive bridge, retained for upgrade, mixed-version operation and reinstalling a previous app.
/// The name is kept for existing call sites; this is no longer a hard-cutover migration.
nonisolated enum NativeStateMigration {
    static let previousPrefsKey = LegacyNames.prefsKey
    static let previousPushService = LegacyNames.pushService
    static let previousTerminalTheme = LegacyNames.product
    static let previousLooks = LegacyNames.looks
    static let prefsKey = "persona.agents.prefs"
    static let personaKey = "persona.id.v2"
    static let previousPersonaKey = "persona.id"
    static let terminalKey = "terminalTheme.v2"
    static let previousTerminalKey = "terminalTheme"

    static func infoString(_ key: String, bundle: Bundle = .main) -> String? {
        for name in [key, LegacyNames.infoKeys[key]].compactMap({ $0 }) {
            if let value = bundle.object(forInfoDictionaryKey: name) as? String,
               !value.isEmpty, !value.contains("$(") { return value }
        }
        return nil
    }

    static func lookID(_ value: String) -> String {
        let parts = value.split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
        guard let id = parts.first, let mapped = previousLooks[String(id)] else { return value }
        return mapped + (parts.count == 2 ? "/" + parts[1] : "")
    }

    static func previousLookID(_ value: String) -> String {
        let parts = value.split(separator: "/", maxSplits: 1, omittingEmptySubsequences: false)
        guard let id = parts.first, let previous = previousLooks.first(where: { $0.value == id })?.key else { return value }
        return previous + (parts.count == 2 ? "/" + parts[1] : "")
    }

    static func readPersonaID(from store: UserDefaults) -> String? {
        let value = [personaKey, previousPersonaKey].compactMap { store.string(forKey: $0) }.first { !$0.isEmpty }
        return value.map(lookID)
    }

    static func writePersonaID(_ id: String, defaults store: UserDefaults) {
        store.set(lookID(id), forKey: personaKey)
        store.set(previousLookID(id), forKey: previousPersonaKey)
    }

    static func readTerminalID(from store: UserDefaults) -> String? {
        let value = [terminalKey, previousTerminalKey].compactMap { store.string(forKey: $0) }.first { !$0.isEmpty }
        return value.map { $0 == previousTerminalTheme ? "persona" : $0 }
    }

    static func writeTerminalID(_ id: String, defaults store: UserDefaults) {
        store.set(id, forKey: terminalKey)
        store.set(id == "persona" ? previousTerminalTheme : id, forKey: previousTerminalKey)
    }

    static func readPrefs<T: Decodable>(_ type: T.Type, defaults store: UserDefaults) -> T? {
        for key in [prefsKey, previousPrefsKey] {
            if let data = store.data(forKey: key), let value = try? JSONDecoder().decode(type, from: data) { return value }
        }
        return nil
    }

    static func writePrefs(_ data: Data, defaults store: UserDefaults) {
        store.set(data, forKey: prefsKey)
        store.set(data, forKey: previousPrefsKey)
    }

    /// Seed missing canonical values only. Readers still fall back on every load; neither conflicts nor old keys are deleted.
    @discardableResult
    static func defaults(_ store: UserDefaults, domainName: String? = nil) -> Bool {
        if store.object(forKey: prefsKey) == nil, let old = store.object(forKey: previousPrefsKey) { store.set(old, forKey: prefsKey) }
        if store.object(forKey: personaKey) == nil, let id = readPersonaID(from: store) { store.set(id, forKey: personaKey) }
        if store.object(forKey: terminalKey) == nil, let id = readTerminalID(from: store) { store.set(id, forKey: terminalKey) }
        return true
    }

    static func prepare() {
        _ = defaults(.standard)
        if let group = infoString("PersonaAppGroup"), let shared = UserDefaults(suiteName: group) { _ = defaults(shared) }
        // Existing Fleet files, cached catalogs, drafts, outboxes, recordings and opaque archives remain untouched.
    }

    private static let pushLock = NSLock()
    nonisolated(unsafe) private static var preparedPushScopes: Set<String> = []

    static func preparePushKeys(accessGroup: String?) -> Bool {
        #if os(macOS) && DEBUG
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil
            || ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil { return true }
        #endif
        pushLock.lock()
        defer { pushLock.unlock() }
        let scope = accessGroup ?? ""
        if preparedPushScopes.contains(scope) { return true }
        guard pushKeys(accessGroup: accessGroup) else { return false }
        preparedPushScopes.insert(scope)
        return true
    }

    /// Fill missing accounts in both directions, preserving bytes and protection. Existing conflicts are never overwritten.
    /// Reading a new key takes priority; a previous key remains available to the previous app and for sealed old pushes.
    @discardableResult
    static func pushKeys(accessGroup: String?, access: NativeMigrationKeychain = .system) -> Bool {
        func query(service: String, account: String? = nil) -> [String: Any] {
            var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service]
            if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
            if let account { query[kSecAttrAccount as String] = account }
            #if os(macOS)
            query[kSecUseDataProtectionKeychain as String] = true
            #endif
            return query
        }
        func copyMissing(from source: String, to destination: String) -> Bool {
            var list = query(service: source)
            list[kSecReturnData as String] = true
            list[kSecReturnAttributes as String] = true
            list[kSecMatchLimit as String] = kSecMatchLimitAll
            let (status, result) = access.read(list)
            if status == errSecItemNotFound { return true }
            guard status == errSecSuccess, let items = result as? [[String: Any]] else { return false }
            for item in items {
                guard let account = item[kSecAttrAccount as String] as? String,
                      account == "push-key" || account.hasPrefix("push-key."),
                      let bytes = item[kSecValueData as String] as? Data else { return false }
                var target = query(service: destination, account: account)
                target[kSecReturnData as String] = true
                target[kSecMatchLimit as String] = kSecMatchLimitOne
                let (existingStatus, _) = access.read(target)
                if existingStatus == errSecSuccess { continue }
                guard existingStatus == errSecItemNotFound else { return false }
                var add = query(service: destination, account: account)
                add[kSecValueData as String] = bytes
                for key in [kSecAttrAccessible, kSecAttrSynchronizable, kSecAttrAccessGroup, kSecAttrLabel, kSecAttrDescription] {
                    if let value = item[key as String] { add[key as String] = value }
                }
                let added = access.add(add)
                guard added == errSecSuccess || added == errSecDuplicateItem else { return false }
                let (readStatus, copied) = access.read(target)
                guard readStatus == errSecSuccess, copied as? Data == bytes else { return false }
            }
            return true
        }
        return copyMissing(from: previousPushService, to: "persona.push")
            && copyMissing(from: "persona.push", to: previousPushService)
    }
}

/// Injectable Security calls keep unit tests away from the developer's real keychain.
nonisolated struct NativeMigrationKeychain {
    var read: ([String: Any]) -> (OSStatus, Any?)
    var add: ([String: Any]) -> OSStatus
    var delete: ([String: Any]) -> OSStatus
    static var system: Self {
        Self(read: { query in
            var value: AnyObject?
            let status = SecItemCopyMatching(query as CFDictionary, &value)
            return (status, value)
        }, add: { SecItemAdd($0 as CFDictionary, nil) }, delete: { SecItemDelete($0 as CFDictionary) })
    }
}

// Share › Persona compiles all of Shared (Session included), so it has the envelope too.
#if !WIDGET_EXTENSION && (!NATIVE_MIGRATION_STORAGE_ONLY || SHARE_EXTENSION)
/// Keep the canonical synthesized session encoder, adding only the previous marker for old readers.
nonisolated struct NativeSessionEnvelope: Encodable {
    var session: Session

    func encode(to encoder: Encoder) throws {
        try session.encode(to: encoder)
        var fields = encoder.container(keyedBy: Field.self)
        try fields.encode(session.startedByPersona, forKey: Field(stringValue: LegacyNames.startedByField)!)
    }

    private struct Field: CodingKey {
        var stringValue: String
        var intValue: Int? { nil }
        init?(stringValue: String) { self.stringValue = stringValue }
        init?(intValue: Int) { return nil }
    }
}
#endif

#if os(iOS) && !NATIVE_MIGRATION_STORAGE_ONLY
import ActivityKit

/// A real nominal type, not an alias pretending to migrate the OS identity. Previous hubs use this attributes-type.
nonisolated struct JarvisActivityAttributes: ActivityAttributes {
    typealias ContentState = FleetState
    var kind = "fleet"
}
typealias LegacyFleetActivityAttributes = JarvisActivityAttributes
#endif

#if !os(watchOS) && !NATIVE_MIGRATION_STORAGE_ONLY
import AppIntents

#if !WIDGET_EXTENSION
// Literal properties in concrete extensions are extracted correctly; indirect references silently use the Swift type name.
// Public actions keep stable identities so actions created by the new app also run after reinstalling the previous app.
extension AskPersonaIntent { nonisolated static let persistentIdentifier = "AskJarvisIntent" }
extension PersonaStatusIntent { nonisolated static let persistentIdentifier = "JarvisStatusIntent" }

/// Hidden adapters also accept actions saved by the short-lived canonical-identity builds.
struct CompatibleAskAssistantIntent: AppIntent {
    nonisolated static let persistentIdentifier = "AskPersonaIntent"
    nonisolated static let title: LocalizedStringResource = "Ask Persona"
    nonisolated static let isDiscoverable = false
    nonisolated static var supportedModes: IntentModes { .background }
    @Parameter(title: "Question [Ask Persona Intent 33a861]", requestValueDialog: "What do you want to ask Persona?")
    var question: String
    nonisolated init() {}
    nonisolated init(question: String) { self.question = question }
    #if (os(iOS) || os(visionOS)) && compiler(>=6.4)
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> & ShowsSnippetView {
        try await AskPersonaIntent(question: question).perform()
    }
    #else
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        try await AskPersonaIntent(question: question).perform()
    }
    #endif
}

struct CompatibleAssistantStatusIntent: AppIntent {
    nonisolated static let persistentIdentifier = "PersonaStatusIntent"
    nonisolated static let title: LocalizedStringResource = "Persona Status"
    nonisolated static let isDiscoverable = false
    nonisolated static var supportedModes: IntentModes { .background }
    nonisolated init() {}
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        try await PersonaStatusIntent().perform()
    }
}
#endif

#if os(iOS)
extension OpenPersonaAssistantIntent { nonisolated static let persistentIdentifier = "OpenJarvisAssistantIntent" }
extension OpenPersonaAgentsIntent { nonisolated static let persistentIdentifier = "OpenJarvisAgentsIntent" }

#if !WIDGET_EXTENSION
struct CompatibleOpenAssistantIntent: AppIntent {
    nonisolated static let persistentIdentifier = "OpenPersonaAssistantIntent"
    nonisolated static let title: LocalizedStringResource = "Open Persona Assistant"
    nonisolated static let isDiscoverable = false
    nonisolated static var supportedModes: IntentModes { .foreground(.immediate) }
    nonisolated init() {}
    nonisolated func perform() async throws -> some IntentResult { try await OpenPersonaAssistantIntent().perform() }
}

struct CompatibleOpenAgentsIntent: AppIntent {
    nonisolated static let persistentIdentifier = "OpenPersonaAgentsIntent"
    nonisolated static let title: LocalizedStringResource = "Open Persona Agents"
    nonisolated static let isDiscoverable = false
    nonisolated static var supportedModes: IntentModes { .foreground(.immediate) }
    nonisolated init() {}
    nonisolated func perform() async throws -> some IntentResult { try await OpenPersonaAgentsIntent().perform() }
}
#endif
#endif
#endif
