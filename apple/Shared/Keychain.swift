import Foundation
import Security

nonisolated enum Keychain {
    // The phone app's bundle id on the phone, the watch (the watch app's is <id>.watchkitapp) and in Share › Persona,
    // whose Info.plist names it (`PersonaAppBundleID`; the extension's own is <id>.share).
    private static let service = (Bundle.main.object(forInfoDictionaryKey: "PersonaAppBundleID") as? String).flatMap { $0.isEmpty ? nil : $0 }
        ?? (Bundle.main.bundleIdentifier ?? "persona").replacingOccurrences(of: ".watchkitapp", with: "")

    /// Whether it was written (callers that must not lose what they replace read it back: HubStore).
    /// `thisDeviceOnly`: never leaves this device (not restored onto another one from a backup, not synced): the SSH
    /// tunnel's private key (docs/operations/network.md「Connecting to hub through SSH」).
    @discardableResult
    static func save(_ data: Data, account: String, thisDeviceOnly: Bool = false) -> Bool {
        #if os(macOS) && DEBUG
        // Screenshots (`-macSnapshot`) never touch the keychain, writes included: one paired with a mock hub
        // (`-pairURL`) keeps the pairing in memory instead of leaving an item in this Mac's login keychain.
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil { return false }
        #endif
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecAttrAccount as String: account]
        SecItemDelete(query as CFDictionary)
        var add = query
        add[kSecValueData as String] = data
        add[kSecAttrAccessible as String] = thisDeviceOnly ? kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly : kSecAttrAccessibleAfterFirstUnlock
        return SecItemAdd(add as CFDictionary, nil) == errSecSuccess
    }

    /// What reading an item came to: there, not there, or not readable now (before the first unlock after boot
    /// `errSecInteractionNotAllowed`, which a push woken in the background can meet). Not readable is not "not there".
    enum Read: Equatable, Sendable {
        case found(Data)
        case missing
        case failed(OSStatus)
    }

    static func read(account: String) -> Read {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecAttrAccount as String: account, kSecReturnData as String: true,
                                    kSecMatchLimit as String: kSecMatchLimitOne]
        var out: AnyObject?
        #if os(macOS) && DEBUG
        // As in `load`: screenshots and the PersonaMacTests host never touch the keychain.
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil { return .missing }
        if ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil { return .missing }
        #endif
        let status = SecItemCopyMatching(query as CFDictionary, &out)
        switch status {
        case errSecSuccess: return (out as? Data).map { .found($0) } ?? .failed(status)
        case errSecItemNotFound: return .missing
        default: return .failed(status)
        }
    }

    static func load(account: String) -> Data? {
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecAttrAccount as String: account, kSecReturnData as String: true,
                                    kSecMatchLimit as String: kSecMatchLimitOne]
        var out: AnyObject?
        #if os(macOS) && DEBUG
        // Screenshots (`-macSnapshot`, Mac/App/MacSnapshot.swift) run while the Mac is locked, and reading an item another
        // (unsigned) build wrote then blocks in securityd for good, prompt skipped or not: they use demo data, no keychain.
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil { return nil }
        // The same for the app hosting PersonaMacTests: on a locked Mac its launch otherwise never gets past this read
        // ("The test runner hung before establishing connection"); no unit test needs the keychain.
        if ProcessInfo.processInfo.environment["XCTestConfigurationFilePath"] != nil { return nil }
        #endif
        guard SecItemCopyMatching(query as CFDictionary, &out) == errSecSuccess else { return nil }
        return out as? Data
    }

    static func delete(account: String) {
        #if os(macOS) && DEBUG
        if UserDefaults.standard.string(forKey: "macSnapshot") != nil { return } // as in `save`
        #endif
        let query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                    kSecAttrAccount as String: account]
        SecItemDelete(query as CFDictionary)
    }

    /// The single pairing and the watch's credential from before several hubs (docs/protocol/multi-hub.md): `HubStore` moves
    /// them into the default group and drops them once the registry is written.
    static let legacyServerAccount = "server"
    static let legacyWatchAccount = "watch"
}

/// The keychain as `HubStore` uses it, so tests can make reads fail and writes not stick (`MultiHubTests`).
nonisolated struct KeychainAccess: Sendable {
    var read: @Sendable (String) -> Keychain.Read
    var write: @Sendable (Data, String) -> Bool
    var delete: @Sendable (String) -> Void

    static let system = KeychainAccess(read: { Keychain.read(account: $0) }, write: { Keychain.save($0, account: $1) },
                                       delete: { Keychain.delete(account: $0) })
}
