import AppIntents
import CryptoKit
import Foundation

/// "Handle Notification" (#3848, docs/apple/intents.md "Handle Notification"): hands one item (a title, a body, the app
/// it came from, an optional link and time) to Persona as this device's Shortcuts source
/// (docs/extensions/subscriptions.md "Push sources"). People run it from their own shortcuts and personal automations,
/// such as a message or an email arriving, or the share sheet. It runs in the background. While the hub can't be
/// reached, the item waits on the phone and goes with the next run or when the app connects. Only what the shortcut
/// passes is sent: nothing reads other apps' notifications.
struct HandleNotificationIntent: AppIntent {
    nonisolated static let title: LocalizedStringResource = "Handle Notification"
    nonisolated static let description = IntentDescription(
        "Hands a notification, or anything else you pass in, to Persona as an item of this device's Shortcuts source in Settings › Subscriptions. Only what you pass in is sent: nothing reads other apps' notifications.",
        categoryName: "Subscriptions",
        searchKeywords: ["Notification", "Inbox", "Subscription"]
    )
    nonisolated static var supportedModes: IntentModes { .background }

    @Parameter(title: "Title")
    var heading: String?

    @Parameter(title: "Body", inputOptions: String.IntentInputOptions(multiline: true))
    var body: String?

    @Parameter(title: "App", description: "Where it came from, such as Messages or Mail")
    var app: String?

    @Parameter(title: "Link")
    var link: URL?

    @Parameter(title: "Time", description: "When it happened; now when empty")
    var time: Date?

    nonisolated init() {}

    nonisolated static var parameterSummary: some ParameterSummary {
        Summary("Send \(\.$heading) from \(\.$app) to Persona") {
            \.$body
            \.$link
            \.$time
        }
    }

    /// Returns `sent` or `queued`, for a shortcut that goes on depending on it.
    nonisolated func perform() async throws -> some IntentResult & ProvidesDialog & ReturnsValue<String> {
        guard let item = ShortcutItem(title: heading, body: body, app: app, link: link, time: time) else {
            throw IntentFailureSentence(ShortcutItemFlow.nothingToSend)
        }
        let outcome = try await ShortcutItemFlow.send(item)
        return .result(value: outcome.value, dialog: IntentSupport.dialog(outcome.sentence))
    }
}

/// One item as `POST /api/v1/subscriptions/-/push` takes it.
nonisolated struct ShortcutItem: Codable, Equatable, Sendable {
    var key: String
    var title: String
    var body: String
    var app: String
    var link: String?
    var time: Date

    /// The hub keeps this much of a body.
    static let bodyMost = 8000

    /// nil when there is nothing to send: neither a title nor a body. A link that is not http(s) is left out.
    init?(title: String?, body: String?, app: String?, link: URL?, time: Date?, now: Date = .now) {
        let t = (title ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        let b = String((body ?? "").trimmingCharacters(in: .whitespacesAndNewlines).prefix(Self.bodyMost))
        guard !t.isEmpty || !b.isEmpty else { return nil }
        self.title = t
        self.body = b
        self.app = (app ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
        self.link = link.flatMap { ["http", "https"].contains($0.scheme?.lowercased() ?? "") ? $0.absoluteString : nil }
        self.time = time ?? now
        key = Self.key(app: self.app, title: t, body: b, time: self.time)
    }

    /// A hash of the app, title, body and the minute: a shortcut that fires twice for one notification adds one item.
    static func key(app: String, title: String, body: String, time: Date) -> String {
        let minute = Int((time.timeIntervalSince1970 / 60).rounded(.down))
        let digest = SHA256.hash(data: Data([app, title, body, String(minute)].joined(separator: "\u{0}").utf8))
        return digest.prefix(16).map { String(format: "%02x", $0) }.joined()
    }
}

/// What a push answers: the source as it is now (its label, whether a destination is on) and how many were new.
nonisolated struct ShortcutPushResult: Decodable, Sendable {
    struct Source: Decodable, Sendable {
        var label: String
        var toInbox: Bool
        var toKB: Bool

        enum CodingKeys: String, CodingKey {
            case label
            case toInbox = "to_inbox"
            case toKB = "to_kb"
        }
    }

    var subscription: Source
    var new: Int
    var count: Int
}

nonisolated extension APIClient {
    /// `POST /api/v1/subscriptions/-/push`: this device's items, into its Shortcuts source (made on the first).
    func pushShortcutItems(_ items: [ShortcutItem]) async throws -> ShortcutPushResult {
        struct Body: Encodable { var items: [ShortcutItem] }
        return try await post("/api/v1/subscriptions/-/push", body: Body(items: items), timeout: 20)
    }
}

/// Items that could not go out yet (the hub could not be reached), by hub group, in Application Support (not Caches,
/// which the system may empty): they go with the next Handle Notification or when the app connects to that group. At
/// most 200 a group, none older than the Inbox's week.
actor ShortcutOutbox {
    static let shared = ShortcutOutbox()
    static let most = 200
    static let keep: TimeInterval = 7 * 24 * 3600

    private let url: URL?
    private var held: [String: [ShortcutItem]]?

    init(url: URL? = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first?
        .appending(path: "shortcut-outbox.json")) {
        self.url = url
    }

    /// The group's waiting items, oldest first.
    func items(group: String, now: Date = .now) -> [ShortcutItem] {
        (load()[group] ?? []).filter { now.timeIntervalSince($0.time) < Self.keep }
    }

    func add(_ item: ShortcutItem, group: String, now: Date = .now) {
        var all = load()
        var list = (all[group] ?? []).filter { $0.key != item.key && now.timeIntervalSince($0.time) < Self.keep }
        list.append(item)
        if list.count > Self.most { list.removeFirst(list.count - Self.most) }
        all[group] = list
        save(all)
    }

    func remove(keys: Set<String>, group: String) {
        var all = load()
        guard let list = all[group] else { return }
        let rest = list.filter { !keys.contains($0.key) }
        all[group] = rest.isEmpty ? nil : rest
        save(all)
    }

    /// Read once, on this actor's own executor: an actor is never the main actor, whatever the target's default isolation.
    private func load() -> [String: [ShortcutItem]] {
        if let held { return held }
        let data = url.flatMap { try? Data(contentsOf: $0) } // perf-ok: ShortcutOutbox is an actor, so this runs off the main actor
        let loaded = data.flatMap { try? JSON.decoder.decode([String: [ShortcutItem]].self, from: $0) } ?? [:]
        held = loaded
        return loaded
    }

    private func save(_ all: [String: [ShortcutItem]]) {
        held = all
        guard let url, let data = try? JSON.encoder.encode(all) else { return }
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try? data.write(to: url, options: .atomic)
    }
}

/// Sending an item, apart from the intent so the tests can hand it a FakeHub client and their own outbox. Every
/// failure comes back as one sentence Siri can read.
nonisolated enum ShortcutItemFlow {
    /// Items a push takes (the hub's limit).
    static let pushMost = 20

    enum Outcome: Equatable {
        /// Kept by the hub; `off`: neither of the source's destinations is on yet.
        case sent(label: String, off: Bool)
        /// Kept on the phone until the hub can be reached.
        case queued

        /// The shortcut's result, the same in every language.
        var value: String {
            switch self {
            case .sent: "sent"
            case .queued: "queued"
            }
        }

        var sentence: String {
            switch self {
            case .sent(_, false): String(localized: "Sent to Persona.")
            case .sent(let label, true):
                String(localized: "Sent. To see it, turn on a destination for \(label) in Persona's Settings › Subscriptions.")
            case .queued: String(localized: "The hub can't be reached now, so Persona will send it when it can.")
            }
        }
    }

    static let nothingToSend = String(localized: "Nothing to send: pass a title or a body.")
    static let hubTooOld = String(localized: "Your hub doesn't take items from Shortcuts yet. Update it, then try again.")
    static let viewOnly = String(localized: "This device can only view on your hub, so it can't send items.")
    static let tooMany = String(localized: "Too many items from this device in the last hour. Try again later.")

    /// Sends item with what waits on the phone for the same hub (the newest that fit one push); keeps it when the hub
    /// can't be reached.
    static func send(_ item: ShortcutItem, client: APIClient? = nil, outbox: ShortcutOutbox = .shared) async throws -> Outcome {
        let api: APIClient
        do { api = try client ?? IntentSupport.selectedClient() } catch { throw IntentFailureSentence(IntentFailure.sentence(for: error)) }
        let waiting = await outbox.items(group: api.group).filter { $0.key != item.key }
        let batch = Array(waiting.suffix(pushMost - 1)) + [item]
        do {
            let res = try await api.pushShortcutItems(batch)
            await outbox.remove(keys: Set(batch.map(\.key)), group: api.group)
            return .sent(label: res.subscription.label, off: !res.subscription.toInbox && !res.subscription.toKB)
        } catch where queueable(error) {
            await outbox.add(item, group: api.group)
            return .queued
        } catch {
            throw IntentFailureSentence(failure(error))
        }
    }

    /// What waits for the group goes out, a push at a time, until it is empty or the hub can't take it.
    static func flush(_ api: APIClient, outbox: ShortcutOutbox = .shared) async {
        while true {
            let waiting = await outbox.items(group: api.group)
            guard !waiting.isEmpty else { return }
            let batch = Array(waiting.prefix(pushMost))
            do {
                _ = try await api.pushShortcutItems(batch)
            } catch {
                // Refused for good (an older hub, a view-only device, a bad item): the queue would never empty.
                if !queueable(error), (error as? APIError)?.status != 429 { await outbox.remove(keys: Set(batch.map(\.key)), group: api.group) }
                return
            }
            await outbox.remove(keys: Set(batch.map(\.key)), group: api.group)
        }
    }

    /// Kept for later: the phone is offline, the hub can't be reached or is restarting.
    static func queueable(_ error: Error) -> Bool {
        if error is URLError || (error as NSError).domain == NSURLErrorDomain { return true }
        if let api = error as? APIError { return [502, 503, 504].contains(api.status) }
        return false
    }

    static func failure(_ error: Error) -> String {
        if let api = error as? APIError {
            switch api.status {
            case 404, 405: return hubTooOld
            case 403 where api.fields["code"] == "read_only_device": return viewOnly
            case 429: return tooMany
            default: break
            }
        }
        return IntentFailure.sentence(for: error, host: IntentSupport.host())
    }
}
