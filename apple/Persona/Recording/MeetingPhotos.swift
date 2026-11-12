#if os(iOS)
import CoreGraphics
import Foundation
import ImageIO
import Observation
import UIKit
import UniformTypeIdentifiers
import Vision

/// A photo as the hub keeps it (`Photo`, docs/assistant/meeting-minutes.md "Phase 3: photos").
nonisolated struct MeetingPhoto: Codable, Hashable, Sendable, Identifiable {
    /// Counts from 1 per recording, never reused.
    var n: Int
    /// The phone's id for the photo (lowercase uuid); the room's message about it is `photo-<id>`.
    var id: String
    /// Seconds from the start of the recording.
    var at: Double
    var mime: String
    var width: Int?
    var height: Int?
    /// What the phone read in it.
    var text: String?
    var caption: String?
    /// The room's description of it.
    var note: String?
    /// The current minutes cite it.
    var cited: Bool?
    /// The hub sent it into the room, so deleting it withdraws it there.
    var forwarded: Bool?
    /// RFC 3339.
    var addedAt: String?

    enum CodingKeys: String, CodingKey {
        case n, id, at, mime, width, height, text, caption, note, cited, forwarded
        case addedAt = "added_at"
    }
}

/// One meeting photo as the pages draw it: from the hub's list (`GET …/photos`), from this phone (taken or picked here,
/// on its way), or both while a stored one waits for the list to have it. What a row shows is worked out here, once per
/// change of the photos (`MeetingPhotos.Album`), so drawing a row parses, trims and sorts nothing (docs/apple/performance.md
/// P4).
nonisolated struct MeetingPhotoItem: Identifiable, Equatable, Sendable {
    /// The photo's own id, lowercase (the room's `photo-<id>`).
    var photoID: String
    var listed: MeetingPhoto?
    /// This phone's copy: on its way, failed, or stored and not listed yet.
    var local: MeetingPhotos.Local?
    /// Its number at the hub, once stored: the delete and the hub's copy of the image need it.
    var n: Int?
    /// Seconds into the recording.
    var at: Double?
    /// The owner's caption, else what was read in it.
    var words: String?
    /// Its first `Theme.Recording.photoTextLines` lines: what the row shows beside the thumbnail, and VoiceOver says.
    var preview: String?
    /// The room's description of it.
    var note: String?
    /// When it was added: on this phone when taken or picked here, else the hub's `added_at`.
    var addedAt: Date?
    /// There is a picture to show: the hub's copy, or this phone's prepared one (kept in the recording's folder, or in
    /// memory while it goes).
    var hasImage = false
    /// Retry can send it: it failed with a refusal that may go otherwise (`MeetingPhotos.canRetry(after:)`), and its
    /// JPEG is here.
    var retryable = false

    var id: String { "photo-" + photoID }

    /// The hub stored it: it is the meeting's material.
    var stored: Bool { n != nil }

    var failed: Bool {
        if case .failed? = local?.state { return true }
        return false
    }

    /// The hub has it (Delete asks the hub), or it stopped on its way (Delete settles it with the hub first). One on its
    /// way waits: the hub may be storing it right now.
    var deletable: Bool { stored || failed }

    /// A photo the room's thread holds before the hub's list was read: known only by its id.
    init(photoID: String) {
        self.photoID = photoID
    }

    /// `inMemory`: its prepared JPEG is in memory (being sent, or its file couldn't be written).
    init(photoID: String, listed: MeetingPhoto?, local: MeetingPhotos.Local?, inMemory: Bool) {
        self.photoID = photoID
        self.listed = listed
        // The list is the truth for a stored photo: a local copy that still says it goes never shows over it (`apply`
        // reconciles such a copy away).
        let mine = listed == nil || local?.state.isStored == true ? local : nil
        self.local = mine
        n = listed?.n ?? mine?.state.storedNumber
        at = listed?.at ?? mine?.at
        let text = listed?.caption ?? listed?.text ?? mine?.text
        words = text.flatMap { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0 }
        preview = words.map { words in
            let lines = words.split(separator: "\n", maxSplits: Theme.Recording.photoTextLines, omittingEmptySubsequences: true)
            return lines.prefix(Theme.Recording.photoTextLines).joined(separator: "\n")
        }
        note = listed?.note.flatMap { $0.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? nil : $0 }
        addedAt = mine?.takenAt ?? listed?.addedAt.flatMap { JSON.parseDate($0) }
        let here = mine?.file != nil || inMemory
        hasImage = n != nil || here
        if case .failed(_, _, let retry)? = mine?.state { retryable = retry && here }
    }
}

/// The photos of the meetings this phone records (#3862 phase 3): those it is sending, and those the hub lists. A photo
/// is prepared off the main actor, one at a time (`MeetingPhotoPipeline`), kept as a JPEG in the recording's folder, and
/// sent to `POST …/photos` under its own id until the hub stores it; `GET …/photos` is the truth for everything stored.
/// The pages read a recording's `Album`, never the whole store.
@MainActor @Observable
final class MeetingPhotos {
    static let shared = MeetingPhotos(deps: .live)

    /// One photo taken or picked on this phone.
    nonisolated struct Local: Identifiable, Equatable, Sendable, Codable {
        nonisolated enum State: Equatable, Sendable, Codable {
            case preparing
            case uploading
            case stored(n: Int)
            /// `retry`: Retry may go otherwise (no answer, the hub busy or read-only, the meeting full until a photo is
            /// deleted). False for a refusal the hub would give again (a picture it can't take, a meeting that takes no
            /// more photos, a hub without them, a device without access): the row offers Delete Photo instead.
            case failed(message: String, code: String?, retry: Bool = true)

            var isStored: Bool {
                if case .stored = self { return true }
                return false
            }

            var storedNumber: Int? {
                if case .stored(let n) = self { return n }
                return nil
            }
        }
        /// The photo's `id`; a retry sends the same one (a new one only after `op_id_reused`).
        var id: UUID
        var recordingID: UUID
        var at: Double
        var takenAt: Date
        var state: State
        /// Recognized on the phone.
        var text: String?
        /// File name of the prepared JPEG in the recording's `photos` folder; nil until prepared (or when it couldn't be
        /// written: then it is sent from memory and not kept over a relaunch).
        var file: String?
    }

    nonisolated enum Source: Sendable {
        /// JPEG/HEIC bytes of the taken picture.
        case camera(Data)
        case library(Data)
        /// A library pick whose bytes Photos still has to hand over (from iCloud, maybe): its row shows at once, and the
        /// bytes are fetched when its turn to be prepared comes. Nil: it couldn't be read, and its row goes again.
        case picked(@Sendable () async -> Data?)
    }

    /// One recording's photos as its pages draw them, kept up to date as the store changes: the thread and the minutes
    /// read this, never the store's lists, so a change to another recording's photos redraws nothing of theirs, and a
    /// row's content is built once per change, not in `body` (docs/apple/performance.md P4, P5).
    @MainActor @Observable
    final class Album {
        /// Every photo by its id (lowercase).
        fileprivate(set) var items: [String: MeetingPhotoItem] = [:]
        /// The ids in the order the thread draws those its room part doesn't hold: the hub's by their time in the
        /// meeting, then this phone's not listed yet, oldest first.
        fileprivate(set) var order: [String] = []
        /// The hub's number of each listed photo → its id, for the minutes' citations.
        fileprivate(set) var numbers: [Int: String] = [:]
        /// The hub's list was read: a photo the room or the minutes name and the list doesn't have is gone, not unknown.
        fileprivate(set) var loaded = false
        /// The photos whose Delete Photo runs now, and why one failed (by photo id).
        fileprivate(set) var deleting: Set<String> = []
        fileprivate(set) var problems: [String: String] = [:]
    }

    struct Deps {
        var api: () -> APIClient?
        /// `RecordingLibrary.shared.folder(id)`.
        var folder: (UUID) -> URL
        /// Downscales, encodes and reads the picture (`MeetingPhotoPipeline.prepare`), letting the original go first.
        var prepare: @Sendable (MeetingPhotoPipeline.Original, _ locale: String) async -> MeetingPhotoPipeline.Prepared?
        var now: () -> Date
        var wait: (Duration) async -> Void
        /// As `MeetingConversation`'s: the hub has no upload of the recording (`404 recording_not_found`), it goes again
        /// now; whether the hub stored it within a few seconds.
        var reupload: (UUID) async -> Bool
        /// The recordings whose folder may hold photos not sent yet (`start` reads their `photos.json`).
        var recordings: () -> [UUID] = { RecordingLibrary.shared.entries.filter { $0.attendance?.photos == true }.map(\.id) }
    }

    /// The waits between automatic tries of an upload that got no answer or one the hub may give otherwise later (5xx,
    /// 408, 429): one more try after each, then it fails and Retry sends the same id again.
    static let backoff: [Duration] = [.seconds(2), .seconds(4), .seconds(8), .seconds(16), .seconds(30), .seconds(60)]
    /// What `thumbnail` makes when the caller names no size (pixels on the long side).
    static let thumbSide = 240
    /// The full image for the viewer: the hub's largest (4096 px), so nothing is cut below a screen's size.
    static let viewerSide = 4096
    nonisolated static let listFile = "photos.json"
    nonisolated static let photosDirectory = "photos"
    /// Refusals the hub makes only after it would have answered a repeat of a stored photo with that photo (the
    /// contract's order of `POST` errors): a photo refused so was never stored.
    nonisolated static let refusedUnstored: Set<String> = ["kb_read_only", "not_meeting", "transcript_expired", "photos_full"]

    private(set) var local: [Local] = [] {
        didSet { reproject(Self.recordings(changedFrom: oldValue, to: local)) }
    }
    private(set) var listed: [UUID: [MeetingPhoto]] = [:] {
        didSet { reproject(Set(oldValue.keys).union(listed.keys).filter { oldValue[$0] != listed[$0] }) }
    }
    /// `GET …/photos` answered at least once for the recording (so a photo it doesn't list is gone, not unknown).
    private(set) var loaded: Set<UUID> = [] {
        didSet { reproject(oldValue.symmetricDifference(loaded)) }
    }
    /// How many photos of the last pick couldn't be read (still in iCloud), by recording: the bar says so until the next
    /// pick.
    private(set) var unread: [UUID: Int] = [:]

    @ObservationIgnored var deps: Deps
    @ObservationIgnored private var started = false
    /// The launch read of `photos.json` is in: every photo still to send is in `local`, so a JPEG none of them or the
    /// hub's list names is one the hub no longer has.
    @ObservationIgnored private var resumed = false
    @ObservationIgnored private var albums: [UUID: Album] = [:]
    /// The prepared JPEG of each photo not stored yet: sent from memory, so a failed write of the file loses nothing.
    @ObservationIgnored private var pending: [UUID: Data] = [:]
    /// What each photo still to be prepared is made from, and the recording's locale (Vision's languages). One is
    /// prepared at a time, in the order they were added (`worker`).
    @ObservationIgnored private var sources: [UUID: (source: Source, locale: String)] = [:]
    @ObservationIgnored private var queue: [UUID] = []
    @ObservationIgnored private var worker: Task<Void, Never>?
    /// One `GET …/photos` per recording at a time; those asked for meanwhile share one read that runs once it is back.
    @ObservationIgnored private var lastRead: [UUID: Read] = [:]
    @ObservationIgnored private var waitingRead: [UUID: Read] = [:]
    /// Counts the hub's stores: a read that began before a photo was stored doesn't list it yet, which is not a delete.
    @ObservationIgnored private var storeCount = 0
    @ObservationIgnored private var storedAt: [UUID: Int] = [:]
    /// Numbers deleted here: a read that began before the delete still lists them (`n` is never reused).
    @ObservationIgnored private var deleted: [UUID: Set<Int>] = [:]
    /// The `photos.json` writes, one chain per recording so they land in order.
    @ObservationIgnored private var saves: [UUID: Task<Void, Never>] = [:]
    @ObservationIgnored private var loading: Task<Void, Never>?
    @ObservationIgnored private let thumbs: NSCache<NSString, UIImage> = {
        let cache = NSCache<NSString, UIImage>()
        cache.totalCostLimit = 48 << 20
        return cache
    }()
    @ObservationIgnored private var inflight: [String: Task<UIImage?, Never>] = [:]
    /// The thumbnails kept for each photo (`<recording>/<photo>`), so a photo that went takes its own with it, and a
    /// count bumped then, so a decode already running doesn't put one back.
    @ObservationIgnored private var thumbKeys: [String: Set<String>] = [:]
    @ObservationIgnored private var thumbGeneration: [String: Int] = [:]

    private struct Read {
        var token: UUID
        var task: Task<Bool, Never>
    }

    init(deps: Deps) {
        self.deps = deps
    }

    /// From `MeetingRecorder.start`: resumes uploads a relaunch left (`photos.json` in each recording's folder). A photo
    /// that was still being prepared is dropped (its original is gone); a failed one waits for Retry.
    func start() {
        guard !started else { return }
        started = true
        let folders = deps.recordings().map { Folder(recording: $0, url: deps.folder($0)) }
        guard !folders.isEmpty else {
            resumed = true
            return
        }
        loading = Task {
            let found = await Task.detached(priority: .utility) { Self.load(folders) }.value
            resume(found)
        }
    }

    /// A photo for `entry`'s meeting; nil when the meeting doesn't take photos. Returns the photo id. Its row shows at
    /// once (Preparing…); photos are prepared one after another.
    @discardableResult
    func add(_ source: Source, to entry: RecordingEntry, at: Double) -> UUID? {
        guard entry.roomMeeting, entry.attendance?.photos == true else { return nil }
        let photo = Local(id: UUID(), recordingID: entry.id, at: max(0, at), takenAt: deps.now(), state: .preparing)
        sources[photo.id] = (source, entry.locale)
        queue.append(photo.id)
        local.append(photo)
        work()
        return photo.id
    }

    /// The library's picks: a row each at once, their bytes fetched one at a time as each one's turn to be prepared
    /// comes. A new pick forgets how many of the last one couldn't be read.
    @discardableResult
    func pick(_ loads: [@Sendable () async -> Data?], to entry: RecordingEntry, at: Double) -> [UUID] {
        if unread[entry.id] != nil { unread[entry.id] = nil }
        return loads.compactMap { add(.picked($0), to: entry, at: at) }
    }

    func unreadPicks(_ recording: UUID) -> Int { unread[recording] ?? 0 }

    /// A failed photo again, under the same id (the hub returns the stored photo for a repeat), when Retry can help.
    func retry(_ id: UUID) {
        guard let photo = local.first(where: { $0.id == id }), canRetry(photo) else { return }
        edit(id) { $0.state = .uploading }
        persist(photo.recordingID)
        Task { await deliver(id) }
    }

    /// Whether `retry` sends it: it failed with a refusal that may go otherwise, and its JPEG is here (a photo that was
    /// never prepared has nothing to send).
    func canRetry(_ photo: Local) -> Bool {
        guard case .failed(_, _, let retry) = photo.state, retry else { return false }
        return photo.file != nil || pending[photo.id] != nil
    }

    /// Drops a failed photo the owner gives up on (its JPEG too). One on its way or stored stays.
    func discard(_ id: UUID) {
        guard let photo = local.first(where: { $0.id == id }), case .failed = photo.state else { return }
        local.removeAll { $0.id == id }
        pending[id] = nil
        forgetThumbs(photo.recordingID, photoID: Self.wireID(id))
        if let file = photo.file { removeFiles([file], of: photo.recordingID) }
        persist(photo.recordingID)
    }

    /// `GET /api/v1/recordings/{id}/photos`; whether a list read that began after this call was applied (false: offline,
    /// the hub away, an answer it couldn't read). One read per recording at a time; calls meanwhile share the next.
    @discardableResult
    func refresh(_ recording: UUID) async -> Bool {
        // A read waiting to start begins after this call too: it answers for it.
        if let waiting = waitingRead[recording] { return await waiting.task.value }
        let previous = lastRead[recording]?.task
        let token = UUID()
        let task = Task<Bool, Never> {
            if let previous { _ = await previous.value }
            if waitingRead[recording]?.token == token { waitingRead[recording] = nil }
            return await read(recording)
        }
        let scheduled = Read(token: token, task: task)
        lastRead[recording] = scheduled
        if previous != nil { waitingRead[recording] = scheduled }
        let applied = await task.value
        if lastRead[recording]?.token == token { lastRead[recording] = nil }
        return applied
    }

    /// `DELETE …/photos/{n}`; throws APIError; refreshes after. Its JPEG on this phone goes too.
    func delete(_ recording: UUID, n: Int) async throws {
        guard let api = deps.api() else { throw URLError(.notConnectedToInternet) }
        do {
            _ = try await api.request("DELETE", Self.path(recording, "photos/\(n)"))
        } catch let error as APIError where Self.absent(error) {
            // Already gone (another device, the transcript cut, the recording itself): what the owner asked for.
        }
        deleted[recording, default: []].insert(n)
        let photoID = listed[recording]?.first { $0.n == n }?.id.lowercased()
        if listed[recording]?.contains(where: { $0.n == n }) == true { listed[recording]?.removeAll { $0.n == n } }
        let mine = local.filter { $0.recordingID == recording && ($0.state == .stored(n: n) || Self.wireID($0.id) == photoID) }
        if !mine.isEmpty {
            let ids = Set(mine.map(\.id))
            local.removeAll { ids.contains($0.id) }
            for id in ids {
                pending[id] = nil
                storedAt[id] = nil
            }
        }
        var files = Set(mine.compactMap(\.file))
        if let photoID {
            files.insert(Self.fileName(photoID))
            forgetThumbs(recording, photoID: photoID)
        }
        for photo in mine { forgetThumbs(recording, photoID: Self.wireID(photo.id)) }
        removeFiles(files, of: recording)
        persist(recording)
        await refresh(recording)
    }

    /// Delete Photo, confirmed (the row's menu, its button on a failed photo, its accessibility action). A stored photo
    /// goes at the hub (`delete`). A failed one may have been stored with its answer lost, so unless its refusal says it
    /// never was, the list is read first: one it has is deleted there by its number, one it doesn't only leaves this
    /// phone; no list (offline) leaves the photo as it is and says why. The album says the delete runs meanwhile.
    func remove(_ item: MeetingPhotoItem, of recording: UUID) async {
        let album = self.album(recording)
        let key = item.photoID
        guard !album.deleting.contains(key) else { return }
        album.deleting.insert(key)
        if album.problems[key] != nil { album.problems[key] = nil }
        do {
            if let n = item.n {
                try await delete(recording, n: n)
            } else if let mine = item.local, case .failed(_, let code, _) = mine.state {
                let unsent = (mine.file == nil && pending[mine.id] == nil) || Self.refusedUnstored.contains(code ?? "")
                if !unsent {
                    guard await refresh(recording) else { throw URLError(.cannotConnectToHost) }
                    if let n = listed[recording]?.first(where: { $0.id.lowercased() == key })?.n {
                        try await delete(recording, n: n)
                    } else {
                        discard(mine.id)
                    }
                } else {
                    discard(mine.id)
                }
            }
        } catch {
            album.problems[key] = Self.message(error, deleting: true)
        }
        album.deleting.remove(key)
    }

    /// The pages' view of one recording's photos (made the first time it is asked for).
    func album(_ recording: UUID) -> Album {
        if let album = albums[recording] { return album }
        let album = Album()
        albums[recording] = album
        project(recording, into: album)
        return album
    }

    func local(for recording: UUID) -> [Local] { local.filter { $0.recordingID == recording } }

    func listed(for recording: UUID) -> [MeetingPhoto] { listed[recording] ?? [] }

    /// A small image for the thread: the local JPEG, else `GET …/photos/{n}` (memory cache, NSCache with cost);
    /// downsampled off main to `side` pixels on its long side (`Theme.Recording.photoThumbSide` × the screen's scale).
    func thumbnail(_ recording: UUID, photoID: String, n: Int?, side: Int = MeetingPhotos.thumbSide) async -> UIImage? {
        let photo = Self.thumbPhoto(recording, photoID)
        let key = "\(photo)@\(side)"
        if let hit = thumbs.object(forKey: key as NSString) { return hit }
        if let running = inflight[key] { return await running.value }
        let generation = thumbGeneration[photo] ?? 0
        let task = Task<UIImage?, Never> { await self.decoded(recording, photoID: photoID, n: n, side: side) }
        inflight[key] = task
        let image = await task.value
        inflight[key] = nil
        // A photo that went meanwhile (deleted, no longer listed) keeps nothing here.
        if let image, let cg = image.cgImage, thumbGeneration[photo] ?? 0 == generation {
            thumbs.setObject(image, forKey: key as NSString, cost: cg.bytesPerRow * cg.height)
            thumbKeys[photo, default: []].insert(key)
        }
        return image
    }

    /// The full-size image for the viewer (same sources, not downsampled below the screen size).
    func image(_ recording: UUID, photoID: String, n: Int?) async -> UIImage? {
        await decoded(recording, photoID: photoID, n: n, side: Self.viewerSide)
    }

    /// The photo's JPEG as it is (same sources): the meeting page opens it in the app's one image viewer (`ImageViewer`),
    /// which decodes it there.
    func data(_ recording: UUID, photoID: String, n: Int?) async -> Data? {
        await bytes(recording, photoID: photoID, n: n)
    }

    /// For tests: waits for the launch read of `photos.json`, the photos being prepared, and every write of the list so
    /// far.
    func flushed() async {
        await loading?.value
        await worker?.value
        for save in Array(saves.values) { await save.value }
    }

    /// For tests: whether a thumbnail of the photo is kept in memory.
    func hasThumbnail(_ recording: UUID, photoID: String) -> Bool { thumbKeys[Self.thumbPhoto(recording, photoID)]?.isEmpty == false }

    /// What a failed upload or delete says, in plain words.
    static func message(_ error: Error, deleting: Bool = false) -> String {
        if error is Unreadable { return unreadable }
        if let api = error as? APIError {
            switch api.code {
            case "photos_full": return String(localized: "This meeting has 50 photos, the most it takes.")
            case "transcript_expired": return String(localized: "This meeting's transcript has expired, so it takes no more photos.")
            case "not_meeting": return String(localized: "This recording isn't a meeting.")
            case "kb_read_only": return String(localized: "Knowledge is read-only on the hub, so this can't change now.")
            case "forbidden": return String(localized: "This device isn't allowed to make changes")
            case "too_large": return unreadable
            default: break
            }
            // An older manager, or one with photos off: the route isn't there at all.
            if api.status == 404, api.code == nil { return String(localized: "This hub doesn't take meeting photos.") }
            if api.status == 403, api.code == nil { return String(localized: "This device isn't allowed to make changes") }
            if api.status == 400, !deleting { return unreadable }
        }
        if error.isOffline || error.isTimeout { return String(localized: "Couldn't reach the hub. Check the connection and retry.") }
        return deleting ? String(localized: "Couldn't delete the photo. Retry.") : String(localized: "Couldn't upload the photo. Retry.")
    }

    /// Whether Retry can go otherwise after an upload failed with `error`: not for a picture the phone or the hub can't
    /// read, a meeting that takes no more photos, a hub without the photo routes or a device without access, which would
    /// all be refused again. A full meeting can, once a photo is deleted; a read-only knowledge base, once it isn't.
    nonisolated static func canRetry(after error: Error) -> Bool {
        if error is Unreadable { return false }
        guard let api = error as? APIError else { return true }
        switch api.code {
        case "not_meeting", "transcript_expired", "too_large", "forbidden", "recording_deleted", "photo_deleted": return false
        case "kb_read_only", "photos_full", "recording_not_found", "op_id_reused": return true
        default: break
        }
        if [400, 403, 410].contains(api.status) { return false }
        if api.status == 404, api.code == nil { return false }
        return true
    }

    private static var unreadable: String { String(localized: "The photo couldn't be read. Try another one.") }

    // MARK: Preparing and sending

    /// Prepares the photos waiting, one at a time: a 48 MP original decoded and read by Vision at once for every photo of
    /// a pick would take a recording app's memory (docs/apple/performance.md P11). Each is sent once prepared, while the
    /// next one is.
    private func work() {
        guard worker == nil else { return }
        worker = Task {
            while !queue.isEmpty {
                let id = queue.removeFirst()
                await prepareOne(id)
            }
            worker = nil
        }
    }

    private func prepareOne(_ id: UUID) async {
        guard let photo = local.first(where: { $0.id == id }), photo.state == .preparing else {
            sources[id] = nil
            return
        }
        guard let (original, locale) = await take(id) else { return }
        guard original.data != nil else {
            // A pick Photos couldn't hand over (still in iCloud): its row goes, and the bar says some couldn't be read.
            local.removeAll { $0.id == id }
            unread[photo.recordingID, default: 0] += 1
            return
        }
        let result = await deps.prepare(original, locale)
        await prepared(id, result)
    }

    /// The photo's bytes, out of `sources`: once this returns, the box the pipeline gets holds the only copy of the
    /// original, which it lets go of as soon as it is downscaled.
    private func take(_ id: UUID) async -> (MeetingPhotoPipeline.Original, String)? {
        guard let (source, locale) = sources.removeValue(forKey: id) else { return nil }
        switch source {
        case .camera(let data), .library(let data): return (MeetingPhotoPipeline.Original(data), locale)
        case .picked(let load): return (MeetingPhotoPipeline.Original(await load()), locale)
        }
    }

    private func prepared(_ id: UUID, _ result: MeetingPhotoPipeline.Prepared?) async {
        guard let photo = local.first(where: { $0.id == id }) else { return }
        guard let result else {
            edit(id) { $0.state = .failed(message: Self.unreadable, code: nil, retry: false) }
            return
        }
        pending[id] = result.jpeg
        let name = Self.fileName(Self.wireID(id))
        let folder = deps.folder(photo.recordingID)
        let saved = await Task.detached(priority: .utility) { Self.write(result.jpeg, name: name, in: folder) }.value
        guard local.contains(where: { $0.id == id }) else {
            pending[id] = nil
            return
        }
        edit(id) {
            $0.text = result.text
            $0.file = saved ? name : nil
            $0.state = .uploading
        }
        persist(photo.recordingID)
        Task { await deliver(id) }
    }

    /// What to do after a failed upload.
    private enum Next {
        /// No answer, or one the hub may give otherwise later: the same id again after a wait.
        case again
        /// `404 recording_not_found`: the upload goes first, then the same id once more.
        case reupload
        /// `409 op_id_reused`: the hub holds another body under this id, so the photo goes under a new one.
        case renew
        /// `410 photo_deleted` (the owner deleted it elsewhere) or `410 recording_deleted` (the whole recording): it
        /// never comes back.
        case gone
        case stop
    }

    private nonisolated static func next(_ error: Error) -> Next {
        guard let api = error as? APIError else { return error is Unreadable ? .stop : .again }
        switch api.code {
        case "recording_not_found": return .reupload
        case "op_id_reused": return .renew
        case "photo_deleted", "recording_deleted": return .gone
        default: break
        }
        if api.status >= 500 || api.status == 408 || api.status == 429 || api.status == 0 { return .again }
        return .stop
    }

    /// Sends the photo until the hub stored it or a refusal stops it. The loop goes on only while the photo is still
    /// `.uploading` here: a list read that already has it (its answer lost) takes it out of `local`, and the next try
    /// never goes.
    private func deliver(_ first: UUID) async {
        var id = first
        var again = 0, reuploaded = false, renewed = false
        while let photo = local.first(where: { $0.id == id }), photo.state == .uploading {
            do {
                let stored = try await post(photo)
                return store(id, stored)
            } catch {
                switch Self.next(error) {
                case .again where again < Self.backoff.count:
                    await deps.wait(Self.backoff[again])
                    again += 1
                case .reupload where !reuploaded:
                    reuploaded = true
                    guard await deps.reupload(photo.recordingID) else { return fail(id, error) }
                case .renew where !renewed:
                    renewed = true
                    id = await renew(id)
                case .gone:
                    return discardGone(id)
                case .again, .reupload, .renew, .stop:
                    return fail(id, error)
                }
            }
        }
    }

    private func post(_ photo: Local) async throws -> MeetingPhoto {
        guard let api = deps.api() else { throw URLError(.notConnectedToInternet) }
        let file = photo.file.map { Self.photosFolder(deps.folder(photo.recordingID)).appending(path: $0) }
        let memory = pending[photo.id]
        let id = Self.wireID(photo.id), at = photo.at, text = photo.text
        let path = Self.path(photo.recordingID, "photos")
        // The body is a megabyte of base64: built and encoded off the main actor. Every try sends the same bytes.
        let data = try await Task.detached(priority: .userInitiated) {
            guard let jpeg = memory ?? file.flatMap(Self.read) else { throw Unreadable() }
            let body = Upload(id: id, at: at, mime: MeetingPhotoPipeline.mime, data: jpeg.base64EncodedString(), text: text)
            return try await api.request("POST", path, body: body, timeout: 60)
        }.value
        // A 2xx without the photo is no answer yet: the same id again (the hub returns the stored photo).
        guard let stored = try? JSON.decoder.decode(Stored.self, from: data).photo else { throw URLError(.badServerResponse) }
        return stored
    }

    private func store(_ id: UUID, _ photo: MeetingPhoto) {
        // A try that comes back after a list already had the photo (its local copy gone) changes nothing.
        guard let mine = local.first(where: { $0.id == id }), mine.state == .uploading else { return }
        let recording = mine.recordingID
        // Deleted here meanwhile (by its number): a late answer never brings it back.
        if deleted[recording]?.contains(photo.n) == true { return discardGone(id) }
        storeCount += 1
        storedAt[id] = storeCount
        edit(id) { $0.state = .stored(n: photo.n) }
        pending[id] = nil
        var list = listed[recording] ?? []
        list.removeAll { $0.id.lowercased() == photo.id.lowercased() || $0.n == photo.n }
        list.append(photo)
        list.sort { $0.n < $1.n }
        if listed[recording] != list { listed[recording] = list }
        persist(recording)
        Task { await refresh(recording) }
    }

    private func fail(_ id: UUID, _ error: Error) {
        // A try that comes back after a list already had the photo changes nothing: the list's word stands.
        guard let mine = local.first(where: { $0.id == id }), mine.state == .uploading else { return }
        edit(id) {
            $0.state = .failed(message: Self.message(error), code: (error as? APIError)?.code, retry: Self.canRetry(after: error))
        }
        persist(mine.recordingID)
    }

    /// `410 photo_deleted` (the hub stored it, its answer was lost, and the owner deleted it since) or `410
    /// recording_deleted`: it goes here too, its JPEG and its entry in `photos.json` with it, so no launch brings it back.
    private func discardGone(_ id: UUID) {
        guard let photo = local.first(where: { $0.id == id }) else { return }
        local.removeAll { $0.id == id }
        pending[id] = nil
        forgetThumbs(photo.recordingID, photoID: Self.wireID(id))
        if let file = photo.file { removeFiles([file], of: photo.recordingID) }
        persist(photo.recordingID)
    }

    /// The photo under a new id, its JPEG renamed after it; returns the new id.
    private func renew(_ id: UUID) async -> UUID {
        let fresh = UUID()
        guard let photo = local.first(where: { $0.id == id }) else { return fresh }
        var name = photo.file
        if let file = photo.file {
            let folder = Self.photosFolder(deps.folder(photo.recordingID))
            let renamed = Self.fileName(Self.wireID(fresh))
            let moved = await Task.detached(priority: .utility) {
                (try? FileManager.default.moveItem(at: folder.appending(path: file), to: folder.appending(path: renamed))) != nil
            }.value
            if moved { name = renamed }
        }
        if let data = pending.removeValue(forKey: id) { pending[fresh] = data }
        edit(id) {
            $0.id = fresh
            $0.file = name
        }
        persist(photo.recordingID)
        return fresh
    }

    private func edit(_ id: UUID, _ change: (inout Local) -> Void) {
        guard let index = local.firstIndex(where: { $0.id == id }) else { return }
        var photo = local[index]
        change(&photo)
        if local[index] != photo { local[index] = photo }
    }

    // MARK: Reading

    /// One `GET …/photos`; whether its list was applied.
    private func read(_ recording: UUID) async -> Bool {
        guard let api = deps.api() else { return false }
        let began = storeCount
        let data: Data
        do {
            data = try await api.request("GET", Self.path(recording, "photos"))
        } catch let error as APIError where Self.gone(error) {
            // The hub has no such recording (yet, or any more): nothing is listed. The JPEGs here stay: no list said
            // which photos went.
            apply(recording, [], began: began, whole: false)
            return true
        } catch {
            // Offline, a timeout, the hub away, or a 404 without a code (a manager without the photo routes, after a
            // rollback): what was listed stands; an event or the page reads again.
            return false
        }
        guard let answer = try? JSON.decoder.decode(PhotoList.self, from: data) else { return false }
        apply(recording, answer.photos, began: began, whole: answer.whole, unknown: answer.unknown)
        return true
    }

    /// Only these two say the recording itself is gone: any other refusal leaves what is known as it is.
    private nonisolated static func gone(_ error: APIError) -> Bool {
        (error.status == 404 && error.code == "recording_not_found") || (error.status == 410 && error.code == "recording_deleted")
    }

    /// A `DELETE …/photos/{n}` refusal that means the photo is no longer there.
    private nonisolated static func absent(_ error: APIError) -> Bool {
        (error.status == 404 && ["photo_not_found", "recording_not_found"].contains(error.code ?? ""))
            || (error.status == 410 && error.code == "recording_deleted")
    }

    /// A read's list becomes `listed`. A photo stored here leaves `local` once the list has it, or once a read that began
    /// after it was stored doesn't (deleted elsewhere); one stored after the read began stays and keeps its place. One
    /// still uploading or failed that the list has was stored with its answer lost: it leaves `local` too (its JPEG
    /// stays for the thumbnail), so a try still on its way changes nothing when it comes back, and `photos.json` forgets
    /// it. `whole`: every entry of the list was read, so the JPEGs of photos it no longer has can go.
    private func apply(_ recording: UUID, _ photos: [MeetingPhoto], began: Int, whole: Bool, unknown: Set<String> = []) {
        let removed = deleted[recording] ?? []
        var list = photos.filter { !removed.contains($0.n) }
        var ids = Set(list.map { $0.id.lowercased() })
        let before = listed[recording] ?? []
        var dropped: Set<UUID> = []
        var settled = false
        for photo in local where photo.recordingID == recording {
            let key = Self.wireID(photo.id)
            switch photo.state {
            case .stored:
                if ids.contains(key) {
                    dropped.insert(photo.id)
                } else if (storedAt[photo.id] ?? 0) > began {
                    if let kept = before.first(where: { $0.id.lowercased() == key }) {
                        list.append(kept)
                        ids.insert(key)
                    }
                } else {
                    dropped.insert(photo.id)
                }
            case .uploading, .failed:
                if ids.contains(key) {
                    dropped.insert(photo.id)
                    settled = true
                }
            case .preparing:
                break
            }
        }
        list.sort { $0.n < $1.n }
        if !dropped.isEmpty {
            local.removeAll { dropped.contains($0.id) }
            for id in dropped {
                storedAt[id] = nil
                pending[id] = nil
            }
        }
        if settled { persist(recording) }
        if listed[recording] != list { listed[recording] = list }
        if !loaded.contains(recording) { loaded.insert(recording) }
        if whole { reconcileFiles(recording, unknown: unknown) }
    }

    /// After a list the hub answered whole: the JPEGs here of photos it no longer lists (deleted elsewhere, or cut with
    /// the transcript) go from this phone too, with their thumbnails. Not before the launch read of `photos.json` is
    /// in, so a photo still to send is never taken for one of those; what stays is decided once the folder is listed,
    /// so a photo added meanwhile is kept.
    private func reconcileFiles(_ recording: UUID, unknown: Set<String>) {
        guard resumed else { return }
        let folder = Self.photosFolder(deps.folder(recording))
        Task {
            let names = await Task.detached(priority: .utility) { Self.names(in: folder) }.value
            var keep = unknown
            for photo in listed[recording] ?? [] { keep.insert(photo.id.lowercased()) }
            for photo in local where photo.recordingID == recording { keep.insert(Self.wireID(photo.id)) }
            let stale = names.filter { $0.hasSuffix(".jpg") && !keep.contains(String($0.dropLast(4)).lowercased()) }
            guard !stale.isEmpty else { return }
            for name in stale { forgetThumbs(recording, photoID: String(name.dropLast(4))) }
            removeFiles(Set(stale), of: recording)
        }
    }

    // MARK: The pages' view

    private func reproject(_ recordings: Set<UUID>) {
        for recording in recordings {
            if let album = albums[recording] { project(recording, into: album) }
        }
    }

    /// Builds the recording's album from the hub's list and this phone's photos; each part is written only when it
    /// changed, so a row redraws only for a change of the photos it shows.
    private func project(_ recording: UUID, into album: Album) {
        let theirs = listed[recording] ?? []
        let mine = local.filter { $0.recordingID == recording }
        var byID: [String: Local] = [:]
        for photo in mine { byID[Self.wireID(photo.id)] = photo }
        var items: [String: MeetingPhotoItem] = [:]
        var numbers: [Int: String] = [:]
        for photo in theirs {
            let key = photo.id.lowercased()
            let here = byID[key]
            items[key] = MeetingPhotoItem(photoID: key, listed: photo, local: here, inMemory: here.map { pending[$0.id] != nil } ?? false)
            numbers[photo.n] = key
        }
        var onTheirWay: [(offset: Int, photo: Local)] = []
        for (offset, photo) in mine.enumerated() {
            let key = Self.wireID(photo.id)
            guard items[key] == nil else { continue }
            items[key] = MeetingPhotoItem(photoID: key, listed: nil, local: photo, inMemory: pending[photo.id] != nil)
            if let n = photo.state.storedNumber { numbers[n] = key }
            onTheirWay.append((offset, photo))
        }
        let order = theirs.sorted { ($0.at, $0.n) < ($1.at, $1.n) }.map { $0.id.lowercased() }
            + onTheirWay.sorted { ($0.photo.takenAt, $0.offset) < ($1.photo.takenAt, $1.offset) }.map { Self.wireID($0.photo.id) }
        if album.items != items { album.items = items }
        if album.order != order { album.order = order }
        if album.numbers != numbers { album.numbers = numbers }
        let isLoaded = loaded.contains(recording)
        if album.loaded != isLoaded { album.loaded = isLoaded }
    }

    private nonisolated static func recordings(changedFrom old: [Local], to new: [Local]) -> Set<UUID> {
        var before: [UUID: [Local]] = [:], after: [UUID: [Local]] = [:]
        for photo in old { before[photo.recordingID, default: []].append(photo) }
        for photo in new { after[photo.recordingID, default: []].append(photo) }
        return Set(before.keys).union(after.keys).filter { before[$0] != after[$0] }
    }

    // MARK: Images

    private func decoded(_ recording: UUID, photoID: String, n: Int?, side: Int) async -> UIImage? {
        guard let data = await bytes(recording, photoID: photoID, n: n) else { return nil }
        let image = await Task.detached(priority: .userInitiated) { MeetingPhotoPipeline.thumbnail(data, side: side) }.value
        return image.map { UIImage(cgImage: $0) }
    }

    /// The photo's bytes: the JPEG not sent yet, the one kept in the recording's folder, else the hub's copy.
    private func bytes(_ recording: UUID, photoID: String, n: Int?) async -> Data? {
        let mine = local.first { $0.recordingID == recording && Self.wireID($0.id) == photoID.lowercased() }
        if let mine, let data = pending[mine.id] { return data }
        let file = Self.photosFolder(deps.folder(recording)).appending(path: mine?.file ?? Self.fileName(photoID.lowercased()))
        if let data = await Task.detached(priority: .userInitiated, operation: { Self.read(file) }).value { return data }
        guard let n, let api = deps.api() else { return nil }
        return try? await api.request("GET", Self.path(recording, "photos/\(n)"), timeout: 60)
    }

    private nonisolated static func thumbPhoto(_ recording: UUID, _ photoID: String) -> String {
        "\(wireID(recording))/\(photoID.lowercased())"
    }

    /// The thumbnails of a photo that went: out of the cache, and a decode still running keeps none.
    private func forgetThumbs(_ recording: UUID, photoID: String) {
        let photo = Self.thumbPhoto(recording, photoID)
        thumbGeneration[photo, default: 0] += 1
        for key in thumbKeys.removeValue(forKey: photo) ?? [] { thumbs.removeObject(forKey: key as NSString) }
    }

    // MARK: Files

    /// One recording's folder, for the launch read.
    private nonisolated struct Folder: Sendable {
        var recording: UUID
        var url: URL
    }

    /// Writes the photos not stored yet that have a JPEG to `photos.json` (none: the file goes), off the main actor and
    /// in order. A recording whose folder is gone (deleted) gets nothing written back.
    private func persist(_ recording: UUID) {
        let keep = local.filter { photo in
            guard photo.recordingID == recording, photo.file != nil else { return false }
            return !photo.state.isStored
        }
        let folder = deps.folder(recording)
        let previous = saves[recording]
        saves[recording] = Task.detached(priority: .utility) {
            await previous?.value
            Self.writeList(keep, in: folder)
        }
    }

    private func removeFiles(_ names: Set<String>, of recording: UUID) {
        guard !names.isEmpty else { return }
        let folder = Self.photosFolder(deps.folder(recording))
        Task.detached(priority: .utility) {
            for name in names { try? FileManager.default.removeItem(at: folder.appending(path: name)) }
        }
    }

    /// The photos `photos.json` kept, back in `local`. One a read already lists was stored, its answer lost before the
    /// relaunch: it isn't shown as failed again.
    private func resume(_ found: [Local]) {
        var touched: Set<UUID> = []
        for photo in found where !local.contains(where: { $0.id == photo.id }) {
            touched.insert(photo.recordingID)
            let key = Self.wireID(photo.id)
            if listed[photo.recordingID]?.contains(where: { $0.id.lowercased() == key }) == true { continue }
            switch photo.state {
            case .preparing, .stored:
                continue
            case .uploading:
                local.append(photo)
                Task { await deliver(photo.id) }
            case .failed:
                local.append(photo)
            }
        }
        for recording in touched { persist(recording) }
        resumed = true
    }

    private nonisolated static func photosFolder(_ folder: URL) -> URL {
        folder.appending(path: photosDirectory, directoryHint: .isDirectory)
    }

    private nonisolated static func fileName(_ id: String) -> String { id + ".jpg" }

    private nonisolated static func read(_ file: URL) -> Data? {
        try? Data(contentsOf: file) // perf-ok: nonisolated, only called inside Task.detached
    }

    private nonisolated static func names(in folder: URL) -> [String] {
        (try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? [] // perf-ok: nonisolated, only called inside Task.detached
    }

    /// The JPEG into `<folder>/photos/`; false when the recording's folder is gone or the write failed.
    private nonisolated static func write(_ jpeg: Data, name: String, in folder: URL) -> Bool {
        guard FileManager.default.fileExists(atPath: folder.path) else { return false }
        let dir = photosFolder(folder)
        do {
            try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            try jpeg.write(to: dir.appending(path: name), options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
            return true
        } catch {
            return false
        }
    }

    private nonisolated static func writeList(_ photos: [Local], in folder: URL) {
        let file = folder.appending(path: listFile)
        guard !photos.isEmpty else {
            try? FileManager.default.removeItem(at: file)
            return
        }
        guard FileManager.default.fileExists(atPath: folder.path), let data = try? JSONEncoder().encode(photos) else { return }
        try? data.write(to: file, options: .atomic)
    }

    /// The photos each folder's `photos.json` keeps, those whose JPEG is still there.
    private nonisolated static func load(_ folders: [Folder]) -> [Local] {
        folders.flatMap { folder -> [Local] in
            guard let data = read(folder.url.appending(path: listFile)),
                  let photos = try? JSONDecoder().decode([Local].self, from: data) else { return [] }
            let dir = photosFolder(folder.url)
            return photos.filter { photo in
                guard photo.recordingID == folder.recording, let file = photo.file else { return false }
                return FileManager.default.fileExists(atPath: dir.appending(path: file).path)
            }
        }
    }

    // MARK: Wire

    private nonisolated static func wireID(_ id: UUID) -> String { id.uuidString.lowercased() }

    private nonisolated static func path(_ id: UUID, _ route: String) -> String {
        "/api/v1/recordings/\(wireID(id))/\(route)"
    }

    /// The prepared JPEG is gone (a relaunch after its file was lost) or never was.
    private nonisolated struct Unreadable: Error {}

    /// `POST …/photos`'s body: the same on every try of one id, or the hub answers `409 op_id_reused`.
    private nonisolated struct Upload: Encodable, Sendable {
        var id: String
        var at: Double
        var mime: String
        var data: String
        var text: String?
    }

    private nonisolated struct Stored: Decodable, Sendable {
        var photo: MeetingPhoto
    }

    /// `GET …/photos`: a photo of another shape is left out, not the whole list. `unknown`: the ids of those left out,
    /// whose JPEGs here stay; `whole`: every entry was read or at least named, so the list says which photos went.
    private nonisolated struct PhotoList: Decodable, Sendable {
        var photos: [MeetingPhoto]
        var unknown: Set<String>
        var whole: Bool

        enum CodingKeys: String, CodingKey { case photos }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            let entries = try c.decode([Lenient<MeetingPhoto>].self, forKey: .photos)
            photos = entries.compactMap(\.value)
            unknown = Set(entries.compactMap { $0.value == nil ? $0.id?.lowercased() : nil })
            whole = entries.allSatisfy { $0.value != nil || $0.id != nil }
        }
    }

    private nonisolated struct Lenient<Value: Decodable>: Decodable {
        var value: Value?
        /// The entry's `id`, when it has one: what is known of an entry of another shape.
        var id: String?

        private enum Key: String, CodingKey { case id }

        init(from decoder: Decoder) throws {
            value = try? Value(from: decoder)
            id = try? decoder.container(keyedBy: Key.self).decode(String.self, forKey: .id)
        }
    }
}

extension MeetingPhotos.Deps {
    /// The app's: the hub-level group's hub (`AppModel.api`), the recordings' folders, the real pipeline.
    static var live: Self {
        Self(api: { AppModel.shared.api },
             folder: { RecordingLibrary.shared.folder($0) },
             prepare: { original, locale in await MeetingPhotoPipeline.prepare(original, locale: locale) },
             now: { .now },
             wait: { try? await Task.sleep(for: $0) },
             reupload: MeetingConversation.Deps.live.reupload)
    }
}

/// Off-main image work: downscale + JPEG, and text recognition. Pure; no UI types except CGImage.
nonisolated enum MeetingPhotoPipeline {
    struct Prepared: Sendable {
        var jpeg: Data
        var width: Int
        var height: Int
        var text: String?
    }

    /// The picture as it came (megabytes for a 48 MP HEIC, tens for a ProRAW one), held here alone so `prepare` can let
    /// go of it as soon as it is downscaled, before the slow text read.
    nonisolated final class Original: @unchecked Sendable {
        // Set once by whoever makes it, then read and cleared once by the one `prepare` it is handed to.
        var data: Data?

        init(_ data: Data?) { self.data = data }
    }

    static let maxSide = 1600
    static let maxBytes = 1 << 20
    static let mime = "image/jpeg"
    /// The contract's limit on `text`, in characters (Unicode scalars, as the hub counts).
    static let maxText = 8000
    /// JPEG qualities tried in turn until the photo fits `maxBytes`.
    static let qualities = [0.75, 0.6, 0.45, 0.3]

    /// Downscales (ImageIO `CGImageSourceCreateThumbnailAtIndex` with `kCGImageSourceThumbnailMaxPixelSize`, `…WithTransform`,
    /// `…FromImageAlways`) and encodes JPEG (`CGImageDestination`, quality 0.75, then 0.6, 0.45, 0.3 until ≤ maxBytes).
    /// Upright (the orientation applied), never enlarged, with no metadata (no location); a transparent one on white.
    static func downscale(_ data: Data, maxSide: Int = MeetingPhotoPipeline.maxSide,
                          maxBytes: Int = MeetingPhotoPipeline.maxBytes) -> (jpeg: Data, image: CGImage)? {
        guard let decoded = thumbnail(data, side: maxSide) else { return nil }
        let image = opaque(decoded)
        for quality in qualities {
            if let jpeg = jpeg(image, quality: quality), jpeg.count <= maxBytes { return (jpeg, image) }
        }
        return nil
    }

    /// Vision text recognition of `image`; nil when nothing was read. Languages from the recording's locale. Runs off
    /// the caller's actor.
    @concurrent
    static func recognize(_ image: CGImage, locale: String) async -> String? {
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        request.usesLanguageCorrection = true
        let supported = (try? request.supportedRecognitionLanguages()) ?? []
        let wanted = languages(locale).filter { supported.contains($0) }
        if !wanted.isEmpty { request.recognitionLanguages = wanted }
        do {
            try VNImageRequestHandler(cgImage: image, options: [:]).perform([request])
        } catch {
            return nil
        }
        let lines = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }
        return cut(lines.joined(separator: "\n"))
    }

    /// Downscaled, encoded and read, off the caller's actor; nil when the original is no image this phone can read. The
    /// original goes once it is downscaled: only the small image and its JPEG wait for Vision.
    @concurrent
    static func prepare(_ original: Original, locale: String) async -> Prepared? {
        guard let (jpeg, image) = scaled(original) else { return nil }
        let text = await recognize(image, locale: locale)
        return Prepared(jpeg: jpeg, width: image.width, height: image.height, text: text)
    }

    /// `prepare` of bytes the caller keeps.
    static func prepare(_ data: Data, locale: String) async -> Prepared? {
        await prepare(Original(data), locale: locale)
    }

    /// The downscaled image and its JPEG; the original is out of `original` when this returns, and with this scope it is
    /// gone.
    private static func scaled(_ original: Original) -> (jpeg: Data, image: CGImage)? {
        guard let data = original.data else { return nil }
        original.data = nil
        return downscale(data)
    }

    /// A thumbnail of at most `side` px from JPEG/PNG bytes (ImageIO), for the thread.
    static func thumbnail(_ data: Data, side: Int) -> CGImage? {
        guard side > 0, let source = CGImageSourceCreateWithData(data as CFData, nil), CGImageSourceGetCount(source) > 0 else {
            return nil
        }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: side,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        return CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary)
    }

    /// The recognition languages for a recording's locale: Chinese and Japanese meetings write English too.
    static func languages(_ locale: String) -> [String] {
        let code = locale.lowercased()
        if code.hasPrefix("zh") { return ["zh-Hans", "en-US"] }
        if code.hasPrefix("ja") { return ["ja-JP", "en-US"] }
        return ["en-US"]
    }

    /// The recognized lines as sent: trimmed, at most `maxText` Unicode scalars; nil when empty.
    static func cut(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let scalars = trimmed.unicodeScalars
        guard scalars.count > maxText else { return trimmed }
        let short = String(String.UnicodeScalarView(scalars.prefix(maxText))).trimmingCharacters(in: .whitespacesAndNewlines)
        return short.isEmpty ? nil : short
    }

    static func jpeg(_ image: CGImage, quality: Double) -> Data? {
        let out = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(out, UTType.jpeg.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(destination, image, [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
        return CGImageDestinationFinalize(destination) ? out as Data : nil
    }

    /// An image with transparency drawn on white (JPEG has no alpha, and would show it black).
    private static func opaque(_ image: CGImage) -> CGImage {
        switch image.alphaInfo {
        case .none, .noneSkipFirst, .noneSkipLast: return image
        default: break
        }
        guard let space = CGColorSpace(name: CGColorSpace.sRGB),
              let context = CGContext(data: nil, width: image.width, height: image.height, bitsPerComponent: 8, bytesPerRow: 0,
                                      space: space, bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else { return image }
        let rect = CGRect(x: 0, y: 0, width: image.width, height: image.height)
        context.setFillColor(CGColor(gray: 1, alpha: 1))
        context.fill(rect)
        context.draw(image, in: rect)
        return context.makeImage() ?? image
    }
}
#endif
