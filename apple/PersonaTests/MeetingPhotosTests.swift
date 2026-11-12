import CoreGraphics
import Foundation
import ImageIO
import Observation
import Testing
import UIKit
import UniformTypeIdentifiers
import Vision
@testable import Persona

/// Photos in a room meeting (#3862 phase 3; contract docs/assistant/meeting-minutes.md "Phase 3: photos"): the phone's
/// pipeline (downscale, JPEG, text recognition), the upload under the photo's own id and its retries, the words for each
/// refusal, the hub's list, the delete, and the photos not sent yet kept over a relaunch. The hub is a `FakeHub`.
@MainActor struct MeetingPhotosTests {
    // MARK: Fixtures

    private func meeting(photos: Bool = true, locale: String = "en-US") -> RecordingEntry {
        var e = RecordingEntry(id: UUID(), createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .phone, title: "Weekly",
                               locale: locale, active: true)
        e.meeting = RecordingMeeting(title: "Weekly")
        e.attendance = RecordingAttendance.room(.participant, private: false, photos: photos)
        return e
    }

    /// A folder for the recordings of one case, with `entry`'s own folder made (as the recorder makes it).
    private func folder(for entry: RecordingEntry) throws -> URL {
        let root = FileManager.default.temporaryDirectory.appending(path: "meeting-photos-\(UUID())", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: root.appending(path: entry.id.uuidString), withIntermediateDirectories: true)
        return root
    }

    private final class Waits { var list: [Duration] = [] }
    private final class Reuploads { var ids: [UUID] = [] }

    /// Counts a fake hub route's calls, so one can answer differently the first time.
    private final class Calls {
        var count = 0
        func next() -> Int { count += 1; return count }
    }

    /// `wait`: what a backoff wait does (by default: nothing, it is noted in `waits`); `client`: another way to the hub
    /// than `hub`'s own; `prepare`: another pipeline.
    private func store(_ hub: FakeHub, root: URL, recordings: [UUID] = [], waits: Waits? = nil, reuploads: Reuploads? = nil,
                       jpeg: Data = Self.smallJPEG, text: String? = "Q3 budget", wait: ((Duration) async -> Void)? = nil,
                       client: APIClient? = nil,
                       prepare: (@Sendable (MeetingPhotoPipeline.Original, String) async -> MeetingPhotoPipeline.Prepared?)? = nil)
        -> MeetingPhotos {
        let client = client ?? hub.client
        return MeetingPhotos(deps: MeetingPhotos.Deps(
            api: { client },
            folder: { root.appending(path: $0.uuidString, directoryHint: .isDirectory) },
            prepare: prepare ?? { _, _ in MeetingPhotoPipeline.Prepared(jpeg: jpeg, width: 400, height: 300, text: text) },
            now: { Date(timeIntervalSince1970: 1_800_000_100) },
            wait: { duration in
                waits?.list.append(duration)
                await wait?(duration)
            },
            reupload: { id in
                reuploads?.ids.append(id)
                return true
            },
            recordings: { recordings }))
    }

    private func path(_ id: UUID, _ route: String) -> String { "/api/v1/recordings/\(id.uuidString.lowercased())/\(route)" }

    private func body(_ request: LocalRequest?) throws -> [String: Any] {
        try JSONSerialization.jsonObject(with: request?.body ?? Data("{}".utf8)) as? [String: Any] ?? [:]
    }

    private func ids(_ hub: FakeHub, _ recording: UUID) throws -> [String] {
        try hub.sent("POST", path(recording, "photos")).map { try body($0)["id"] as? String ?? "" }
    }

    private func refused(_ status: Int, _ code: String? = nil) -> APIError {
        APIError(status: status, message: "x", fields: code.map { ["code": $0] } ?? [:])
    }

    /// Waits (5 s at most) until `done`: the store's own task got as far as the case needs.
    private func until(_ what: String, _ done: () -> Bool, sourceLocation: SourceLocation = #_sourceLocation) async {
        for _ in 0..<500 {
            if done() { return }
            try? await Task.sleep(for: .milliseconds(10))
        }
        #expect(done(), "timed out waiting: \(what)", sourceLocation: sourceLocation)
    }

    private func failed(_ store: MeetingPhotos, _ recording: UUID) -> Bool {
        if case .failed? = store.local(for: recording).last?.state { true } else { false }
    }

    /// The photo routes of a hub over a list in memory: `POST` stores (the same id returns the stored photo), `GET` lists
    /// them oldest first. `refuse` may throw for the n-th `POST` instead; `lose` stores the n-th and loses its answer.
    @MainActor private final class Hub {
        var photos: [[String: Any]] = []
        let calls = Calls()
        var refuse: @MainActor (Int) throws -> Void = { _ in }
        var lose: @MainActor (Int) -> Bool = { _ in false }

        func answer(_ req: LocalRequest) throws -> Data {
            let call = calls.next()
            try refuse(call)
            let sent = (try JSONSerialization.jsonObject(with: req.body ?? Data())) as? [String: Any] ?? [:]
            let id = sent["id"] as? String ?? ""
            if let same = photos.first(where: { $0["id"] as? String == id }) { return try JSONSerialization.data(withJSONObject: ["photo": same]) }
            var photo: [String: Any] = ["n": photos.count + 1, "id": id, "at": sent["at"] ?? 0, "mime": sent["mime"] ?? "", "width": 400,
                                        "height": 300, "cited": false, "forwarded": true, "added_at": "2026-11-11T10:00:00Z"]
            if let text = sent["text"] { photo["text"] = text }
            photos.append(photo)
            if lose(call) { throw URLError(.networkConnectionLost) }
            return try JSONSerialization.data(withJSONObject: ["photo": photo])
        }

        func list() throws -> Data { try JSONSerialization.data(withJSONObject: ["photos": photos]) }
    }

    private func photoHub(_ recording: UUID) -> (FakeHub, Hub) {
        let hub = FakeHub(), state = Hub()
        hub.on("POST", path(recording, "photos")) { req in try state.answer(req) }
        hub.on("GET", path(recording, "photos")) { _ in try state.list() }
        return (hub, state)
    }

    // MARK: Pictures

    /// A w×h picture: a gradient (opaque unless `alpha`), or seeded noise, which JPEG can't make small.
    private static func picture(_ w: Int, _ h: Int, alpha: Bool = false, noise: Bool = false) -> CGImage {
        let ctx = CGContext(data: nil, width: w, height: h, bitsPerComponent: 8, bytesPerRow: 0, space: CGColorSpace(name: CGColorSpace.sRGB)!,
                            bitmapInfo: (alpha ? CGImageAlphaInfo.premultipliedLast : CGImageAlphaInfo.noneSkipLast).rawValue)!
        if noise, let pixels = ctx.data {
            var seed: UInt64 = 42
            let bytes = pixels.assumingMemoryBound(to: UInt8.self)
            for i in 0..<(ctx.bytesPerRow * h) {
                seed = seed &* 6_364_136_223_846_793_005 &+ 1_442_695_040_888_963_407
                bytes[i] = UInt8(truncatingIfNeeded: seed >> 33)
            }
        } else {
            for x in stride(from: 0, to: w, by: 8) {
                ctx.setFillColor(CGColor(srgbRed: Double(x) / Double(w), green: 0.4, blue: 0.6, alpha: alpha ? 0.5 : 1))
                ctx.fill(CGRect(x: x, y: 0, width: 8, height: h))
            }
        }
        return ctx.makeImage()!
    }

    private static func encoded(_ image: CGImage, _ type: UTType = .jpeg) -> Data {
        let out = NSMutableData()
        let dest = CGImageDestinationCreateWithData(out, type.identifier as CFString, 1, nil)!
        CGImageDestinationAddImage(dest, image, [kCGImageDestinationLossyCompressionQuality: 0.9] as CFDictionary)
        #expect(CGImageDestinationFinalize(dest))
        return out as Data
    }

    private static let smallJPEG = encoded(picture(400, 300))

    /// Pixel size and type of encoded bytes, as the hub reads them.
    private func info(_ data: Data) -> (width: Int, height: Int, type: String)? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil), let type = CGImageSourceGetType(source),
              let props = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = props[kCGImagePropertyPixelWidth] as? Int, let height = props[kCGImagePropertyPixelHeight] as? Int else { return nil }
        return (width, height, type as String)
    }

    // MARK: The pipeline

    /// A camera-sized photo goes at 1600 px on its long side, as a JPEG of at most 1 MiB; the image Vision reads is that one.
    @Test func aLargePhotoGoesAt1600PixelsAsJPEG() throws {
        let out = try #require(MeetingPhotoPipeline.downscale(Self.encoded(Self.picture(4000, 3000))))
        let got = try #require(info(out.jpeg))
        #expect(got.width == 1600 && got.height == 1200)
        #expect(got.type == UTType.jpeg.identifier)
        #expect(out.jpeg.count <= MeetingPhotoPipeline.maxBytes)
        #expect(out.image.width == 1600 && out.image.height == 1200)
    }

    @Test func aTallPhotoKeepsItsShape() throws {
        let out = try #require(MeetingPhotoPipeline.downscale(Self.encoded(Self.picture(1000, 3000))))
        let got = try #require(info(out.jpeg))
        #expect(got.height == 1600)
        #expect(abs(got.width - 533) <= 1)
    }

    /// A PNG from the library (a screenshot, transparency and all) goes as an opaque JPEG.
    @Test func aPNGGoesAsJPEG() throws {
        let out = try #require(MeetingPhotoPipeline.downscale(Self.encoded(Self.picture(2000, 1000, alpha: true), .png)))
        let got = try #require(info(out.jpeg))
        #expect(got.type == UTType.jpeg.identifier)
        #expect(got.width == 1600 && got.height == 800)
        #expect([CGImageAlphaInfo.none, .noneSkipFirst, .noneSkipLast].contains(out.image.alphaInfo))
    }

    @Test func aSmallPhotoIsNeverEnlarged() throws {
        let out = try #require(MeetingPhotoPipeline.downscale(Self.encoded(Self.picture(800, 600))))
        let got = try #require(info(out.jpeg))
        #expect(got.width == 800 && got.height == 600)
    }

    /// A photo JPEG can't make small at the first quality is encoded again at lower ones until it fits.
    @Test func aPhotoTooLargeAtTheFirstQualityStillFits() throws {
        let noisy = Self.encoded(Self.picture(1600, 1200, noise: true), .png)
        let image = try #require(MeetingPhotoPipeline.thumbnail(noisy, side: MeetingPhotoPipeline.maxSide))
        let quality = try #require(MeetingPhotoPipeline.qualities.first)
        let first = try #require(MeetingPhotoPipeline.jpeg(image, quality: quality))
        try #require(first.count > MeetingPhotoPipeline.maxBytes, "the case needs a picture over 1 MiB at the first quality")
        let out = try #require(MeetingPhotoPipeline.downscale(noisy))
        #expect(out.jpeg.count <= MeetingPhotoPipeline.maxBytes)
        #expect(info(out.jpeg)?.type == UTType.jpeg.identifier)
    }

    @Test func bytesThatAreNoImageAreNothing() async {
        #expect(MeetingPhotoPipeline.downscale(Data("not a picture".utf8)) == nil)
        #expect(await MeetingPhotoPipeline.prepare(Data("not a picture".utf8), locale: "en-US") == nil)
        #expect(MeetingPhotoPipeline.thumbnail(Data(), side: 100) == nil)
    }

    @Test func aThumbnailIsAtMostItsSide() throws {
        let thumb = try #require(MeetingPhotoPipeline.thumbnail(Self.encoded(Self.picture(4000, 3000)), side: 120))
        #expect(thumb.width == 120 && thumb.height == 90)
        let png = try #require(MeetingPhotoPipeline.thumbnail(Self.encoded(Self.picture(300, 600), .png), side: 120))
        #expect(png.height == 120 && png.width == 60)
    }

    /// The recording's language picks Vision's; what is read goes as lines, at most 8000 characters, nothing when empty.
    @Test func recognitionLanguagesAndTheTextsLimit() {
        #expect(MeetingPhotoPipeline.languages("zh-CN") == ["zh-Hans", "en-US"])
        #expect(MeetingPhotoPipeline.languages("ja-JP") == ["ja-JP", "en-US"])
        #expect(MeetingPhotoPipeline.languages("en-US") == ["en-US"])
        #expect(MeetingPhotoPipeline.languages("de-DE") == ["en-US"])
        #expect(MeetingPhotoPipeline.cut(String(repeating: "字", count: 9000))?.unicodeScalars.count == 8000)
        #expect(MeetingPhotoPipeline.cut(" \n Q3 budget\nExport cap \n") == "Q3 budget\nExport cap")
        #expect(MeetingPhotoPipeline.cut(" \n ") == nil)
    }

    /// A whiteboard's large dark words are read on the phone. Skipped only where Vision has no English model.
    @Test func recognitionReadsAWhiteboard() async throws {
        let request = VNRecognizeTextRequest()
        request.recognitionLevel = .accurate
        guard let languages = try? request.supportedRecognitionLanguages(), languages.contains("en-US") else { return }
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        format.opaque = true
        let size = CGSize(width: 1600, height: 900)
        let board = UIGraphicsImageRenderer(size: size, format: format).image { context in
            UIColor.white.setFill()
            context.fill(CGRect(origin: .zero, size: size))
            let words: [NSAttributedString.Key: Any] = [.font: UIFont.boldSystemFont(ofSize: 110), .foregroundColor: UIColor.black]
            for (i, line) in ["QUARTERLY BUDGET", "EXPORT LIMIT 4 MB"].enumerated() {
                (line as NSString).draw(at: CGPoint(x: 80, y: 120 + i * 260), withAttributes: words)
            }
        }
        let image = try #require(board.cgImage)
        let text = try #require(await MeetingPhotoPipeline.recognize(image, locale: "en-US"))
        #expect(text.uppercased().contains("BUDGET"))
        #expect(text.contains("\n"), "one line per line read")
        let prepared = try #require(await MeetingPhotoPipeline.prepare(Self.encoded(image, .png), locale: "zh-CN"))
        #expect(prepared.width == 1600 && prepared.height == 900)
        #expect(prepared.text?.uppercased().contains("BUDGET") == true)
    }

    // MARK: The upload

    /// The photo goes at once under its own id (lowercase) with its time, the JPEG as base64 and the words read on the
    /// phone; it is stored, kept in the recording's folder, and the hub's copy is listed.
    @Test func aPhotoGoesUnderItsOwnID() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let photos = store(hub, root: root)
        let id = try #require(photos.add(.camera(Data("camera bytes".utf8)), to: e, at: 12.5))
        #expect(photos.local(for: e.id).map(\.id) == [id])
        await until("listed") { photos.listed(for: e.id).count == 1 }
        let post = try #require(hub.sent("POST", path(e.id, "photos")).first)
        let sent = try body(post)
        #expect(sent["id"] as? String == id.uuidString.lowercased())
        #expect(sent["at"] as? Double == 12.5)
        #expect(sent["mime"] as? String == "image/jpeg")
        #expect(sent["data"] as? String == Self.smallJPEG.base64EncodedString())
        #expect(sent["text"] as? String == "Q3 budget")
        #expect(sent["caption"] == nil)
        #expect(post.timeout == 60)
        let listed = try #require(photos.listed(for: e.id).first)
        #expect(listed.id == id.uuidString.lowercased() && listed.n == 1 && listed.forwarded == true)
        await until("the local copy gives way to the listed one") { photos.local(for: e.id).isEmpty }
        let jpeg = root.appending(path: e.id.uuidString).appending(path: "photos/\(id.uuidString.lowercased()).jpg")
        #expect(FileManager.default.fileExists(atPath: jpeg.path), "kept for the thread's thumbnail")
        await photos.flushed()
        #expect(!FileManager.default.fileExists(atPath: root.appending(path: e.id.uuidString).appending(path: "photos.json").path),
                "nothing left to send")
    }

    /// Nothing read in it: no `text` at all. A meeting started without photos takes none.
    @Test func aPhotoWithoutWordsSendsNoText() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let photos = store(hub, root: root, text: nil)
        #expect(photos.add(.library(Data("x".utf8)), to: meeting(photos: false), at: 1) == nil)
        _ = try #require(photos.add(.library(Data("x".utf8)), to: e, at: 1))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        #expect(try body(hub.sent("POST", path(e.id, "photos")).first)["text"] == nil)
    }

    /// The hub away for a moment (503): the same id again after the first wait, and the hub keeps one photo.
    @Test func aServerErrorSendsTheSameIDAgain() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { if $0 == 1 { throw APIError(status: 503, message: "busy") } }
        let waits = Waits()
        let photos = store(hub, root: root, waits: waits)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        #expect(try ids(hub, e.id) == [id.uuidString.lowercased(), id.uuidString.lowercased()])
        #expect(waits.list == [.seconds(2)])
        #expect(state.photos.count == 1)
    }

    /// No answer at all, again and again: six more tries after the backoff's waits, then it fails; Retry sends the same id.
    @Test func anUploadThatNeverGetsThroughFailsAndRetriesTheSameID() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { _ in throw URLError(.networkConnectionLost) }
        let waits = Waits()
        let photos = store(hub, root: root, waits: waits)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("failed") { failed(photos, e.id) }
        #expect(hub.sent("POST", path(e.id, "photos")).count == MeetingPhotos.backoff.count + 1)
        #expect(waits.list == MeetingPhotos.backoff)
        let photo = try #require(photos.local(for: e.id).first)
        #expect(photo.state == .failed(message: MeetingPhotos.message(URLError(.networkConnectionLost)), code: nil))
        #expect(photos.canRetry(photo))

        state.refuse = { _ in }
        photos.retry(id)
        await until("listed") { photos.listed(for: e.id).count == 1 }
        #expect(Set(try ids(hub, e.id)) == [id.uuidString.lowercased()])
    }

    /// `409 op_id_reused`: the hub holds another photo under that id, so this one goes under a new id, its JPEG renamed.
    @Test func aReusedIDGoesUnderANewOne() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { if $0 == 1 { throw APIError(status: 409, message: "reused", fields: ["code": "op_id_reused"]) } }
        let photos = store(hub, root: root)
        let first = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        let sent = try ids(hub, e.id)
        #expect(sent.count == 2 && sent[0] == first.uuidString.lowercased() && sent[1] != sent[0])
        #expect(photos.listed(for: e.id).first?.id == sent[1])
        let dir = root.appending(path: e.id.uuidString).appending(path: "photos")
        await photos.flushed()
        #expect(FileManager.default.fileExists(atPath: dir.appending(path: "\(sent[1]).jpg").path))
        #expect(!FileManager.default.fileExists(atPath: dir.appending(path: "\(sent[0]).jpg").path))
    }

    /// The hub doesn't have the recording yet: it is uploaded first, then the same id goes once more.
    @Test func aRecordingTheHubDoesntHaveIsUploadedFirst() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { if $0 == 1 { throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) } }
        let reuploads = Reuploads()
        let photos = store(hub, root: root, reuploads: reuploads)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        #expect(reuploads.ids == [e.id])
        #expect(Set(try ids(hub, e.id)) == [id.uuidString.lowercased()])
    }

    /// A refusal the hub would give again fails at once with its own words and its code; a hub without the photo routes
    /// (a 404 with no code) isn't asked again on its own.
    @Test func aRefusalFailsAtOnceWithItsWords() async throws {
        for refusal in [refused(413, "photos_full"), refused(409, "transcript_expired"), refused(404)] {
            let e = meeting()
            let root = try folder(for: e)
            defer { try? FileManager.default.removeItem(at: root) }
            let (hub, state) = photoHub(e.id)
            state.refuse = { _ in throw refusal }
            let reuploads = Reuploads()
            let photos = store(hub, root: root, reuploads: reuploads)
            _ = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
            await until("failed") { failed(photos, e.id) }
            #expect(hub.sent("POST", path(e.id, "photos")).count == 1, "\(refusal.code ?? "404")")
            #expect(reuploads.ids.isEmpty)
            let retry = refusal.code == "photos_full"
            #expect(MeetingPhotos.canRetry(after: refusal) == retry, "\(refusal.code ?? "404")")
            #expect(photos.local(for: e.id).first?.state == .failed(message: MeetingPhotos.message(refusal), code: refusal.code, retry: retry))
            await photos.flushed()
        }
    }

    /// A picture the phone can't read never goes, and there is nothing to retry.
    @Test func anUnreadablePictureFailsOnThePhone() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let client = hub.client
        let photos = MeetingPhotos(deps: MeetingPhotos.Deps(api: { client }, folder: { root.appending(path: $0.uuidString) },
                                                            prepare: { data, locale in await MeetingPhotoPipeline.prepare(data, locale: locale) },
                                                            now: { .now }, wait: { _ in }, reupload: { _ in false }, recordings: { [] }))
        let id = try #require(photos.add(.library(Data("not a picture".utf8)), to: e, at: 3))
        await until("failed") { failed(photos, e.id) }
        let photo = try #require(photos.local(for: e.id).first)
        #expect(photo.state == .failed(message: MeetingPhotos.message(refused(400)), code: nil, retry: false))
        #expect(!photos.canRetry(photo))
        #expect(hub.sent("POST", path(e.id, "photos")).isEmpty)
        photos.discard(id)
        #expect(photos.local(for: e.id).isEmpty)
    }

    /// Each reason reads as its own sentence; the contract's shared ones read as the meeting conversation's.
    @Test func everyRefusalHasItsWords() {
        let fallback = MeetingPhotos.message(refused(500))
        let own = [refused(413, "photos_full"), refused(409, "transcript_expired"), refused(404), refused(400)].map { MeetingPhotos.message($0) }
        for (i, text) in own.enumerated() { #expect(text != fallback, "\(i)") }
        #expect(Set(own).count == own.count, "each its own")
        #expect(MeetingPhotos.message(refused(413, "too_large")) == MeetingPhotos.message(refused(400)))
        #expect(MeetingPhotos.message(refused(409, "not_meeting")) == MeetingConversation.message(refused(409, "not_meeting")))
        #expect(MeetingPhotos.message(refused(403, "kb_read_only")) == MeetingConversation.message(refused(403, "kb_read_only")))
        #expect(MeetingPhotos.message(URLError(.notConnectedToInternet)) == MeetingConversation.message(URLError(.notConnectedToInternet)))
        #expect(MeetingPhotos.message(refused(500), deleting: true) != fallback, "a failed delete says so")
    }

    // MARK: The hub's list

    /// `GET …/photos` lists oldest first with the room's note, the citation and whether the room has it; a photo of
    /// another shape is left out; a recording the hub doesn't have lists nothing, and that is known.
    @Test func theHubsListIsRead() async throws {
        let hub = FakeHub(), id = UUID(), gone = UUID()
        hub.on("GET", path(id, "photos"), json: """
            {"photos":[
             {"n":1,"id":"6f1e2a3b-0000-4000-8000-000000000001","at":12.5,"mime":"image/jpeg","width":1600,"height":1200,
              "text":"Q3 budget","note":"Whiteboard: the export cap is 4 MB.","cited":true,"forwarded":true,"added_at":"2026-11-11T10:00:00Z"},
             {"n":2,"id":"6f1e2a3b-0000-4000-8000-000000000002","at":30,"mime":"image/png","width":10,"height":10,"cited":false,
              "forwarded":false,"added_at":"2026-11-11T10:01:00Z"},
             {"n":"three"}
            ]}
            """)
        hub.on("GET", path(gone, "photos")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) }
        let photos = store(hub, root: FileManager.default.temporaryDirectory)
        #expect(!photos.loaded.contains(id))
        await photos.refresh(id)
        let listed = photos.listed(for: id)
        #expect(listed.map(\.n) == [1, 2])
        #expect(listed[0].note == "Whiteboard: the export cap is 4 MB.")
        #expect(listed[0].cited == true && listed[0].forwarded == true)
        #expect(listed[0].addedAt == "2026-11-11T10:00:00Z")
        #expect(listed[0].width == 1600 && listed[0].text == "Q3 budget")
        #expect(listed[1].note == nil && listed[1].forwarded == false && listed[1].mime == "image/png")
        #expect(photos.loaded.contains(id))

        await photos.refresh(gone)
        #expect(photos.listed(for: gone).isEmpty)
        #expect(photos.loaded.contains(gone), "gone, not unknown")
    }

    /// A photo stored here joins those the hub already lists (one added on another device, with the room's note), in
    /// the hub's order, and once: the `200`'s photo first, then the list read after it, which takes the local copy's place.
    @Test func aStoredPhotoJoinsTheListedOnes() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.photos = [["n": 1, "id": "6f1e2a3b-0000-4000-8000-000000000001", "at": 2.5, "mime": "image/jpeg", "width": 1600,
                         "height": 1200, "note": "Whiteboard: the export cap is 4 MB.", "cited": false, "forwarded": true,
                         "added_at": "2026-11-11T09:59:00Z"]]
        let photos = store(hub, root: root)
        await photos.refresh(e.id)
        try #require(photos.listed(for: e.id).map(\.n) == [1])
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 30))
        await until("stored") { photos.listed(for: e.id).count == 2 }
        await until("the local copy gives way to the listed one") { photos.local(for: e.id).isEmpty }
        await photos.refresh(e.id)
        let listed = photos.listed(for: e.id)
        #expect(listed.map(\.n) == [1, 2], "never twice")
        #expect(listed.last?.id == id.uuidString.lowercased())
        #expect(listed.first?.note == "Whiteboard: the export cap is 4 MB.", "the other photo keeps its note")
        await photos.flushed()
    }

    /// `DELETE …/photos/{n}` drops it and reads the list again; one already gone at the hub counts as deleted; a refusal
    /// throws and changes nothing.
    @Test func aDeletedPhotoLeavesTheList() async throws {
        let hub = FakeHub(), id = UUID()
        let first = #"{"n":1,"id":"6f1e2a3b-0000-4000-8000-000000000001","at":12.5,"mime":"image/jpeg","cited":false,"forwarded":true}"#
        let second = #"{"n":2,"id":"6f1e2a3b-0000-4000-8000-000000000002","at":30,"mime":"image/jpeg","cited":false,"forwarded":false}"#
        hub.on("GET", path(id, "photos"), json: #"{"photos":[\#(first),\#(second)]}"#)
        hub.on("DELETE", path(id, "photos/1")) { _ in
            hub.on("GET", self.path(id, "photos"), json: #"{"photos":[\#(second)]}"#)
            return Data(#"{"deleted":true}"#.utf8)
        }
        let photos = store(hub, root: FileManager.default.temporaryDirectory)
        await photos.refresh(id)
        try #require(photos.listed(for: id).count == 2)
        try await photos.delete(id, n: 1)
        #expect(hub.sent("DELETE", path(id, "photos/1")).count == 1)
        #expect(photos.listed(for: id).map(\.n) == [2])
        #expect(hub.sent("GET", path(id, "photos")).count == 2, "read again after")

        hub.on("DELETE", path(id, "photos/2")) { _ in throw APIError(status: 403, message: "ro", fields: ["code": "kb_read_only"]) }
        await #expect(throws: APIError.self) { try await photos.delete(id, n: 2) }
        #expect(photos.listed(for: id).map(\.n) == [2], "a refusal changes nothing")

        hub.on("DELETE", path(id, "photos/2")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "photo_not_found"]) }
        hub.on("GET", path(id, "photos"), json: #"{"photos":[]}"#)
        try await photos.delete(id, n: 2)
        #expect(photos.listed(for: id).isEmpty)
    }

    // MARK: Over a relaunch

    /// A photo not stored yet is kept in the recording's folder (`photos.json` and its JPEG): the next launch shows it
    /// again, and Retry sends it under the same id. Once stored, `photos.json` goes and the JPEG stays.
    @Test func aPhotoNotSentYetOutlivesARelaunch() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { _ in throw APIError(status: 403, message: "ro", fields: ["code": "kb_read_only"]) }
        let before = store(hub, root: root, recordings: [e.id])
        let id = try #require(before.add(.camera(Data("x".utf8)), to: e, at: 42))
        await until("failed") { failed(before, e.id) }
        await before.flushed()
        let dir = root.appending(path: e.id.uuidString)
        let kept = try JSONDecoder().decode([MeetingPhotos.Local].self, from: Data(contentsOf: dir.appending(path: "photos.json")))
        #expect(kept.map(\.id) == [id])
        #expect(kept.first?.at == 42 && kept.first?.text == "Q3 budget")
        #expect(kept.first?.file == "\(id.uuidString.lowercased()).jpg")

        state.refuse = { _ in }
        let after = store(hub, root: root, recordings: [e.id])
        after.start()
        await after.flushed()
        let back = try #require(after.local(for: e.id).first)
        #expect(back.id == id && back.at == 42)
        guard case .failed(_, let code, let retry) = back.state else { Issue.record("not failed: \(back.state)"); return }
        #expect(retry, "read-only can change: Retry stays")
        #expect(code == "kb_read_only")
        after.retry(id)
        await until("listed") { after.listed(for: e.id).count == 1 }
        #expect(Set(try ids(hub, e.id)) == [id.uuidString.lowercased()])
        await after.flushed()
        #expect(!FileManager.default.fileExists(atPath: dir.appending(path: "photos.json").path))
        #expect(FileManager.default.fileExists(atPath: dir.appending(path: "photos/\(id.uuidString.lowercased()).jpg").path))
    }

    /// At launch an upload under way goes on; one still being prepared (its original gone) and one whose JPEG is lost
    /// are dropped.
    @Test func aRelaunchResumesAnUploadAndDropsWhatItCantSend() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let dir = root.appending(path: e.id.uuidString)
        try FileManager.default.createDirectory(at: dir.appending(path: "photos"), withIntermediateDirectories: true)
        let sending = UUID(), preparing = UUID(), lost = UUID()
        let at = Date(timeIntervalSince1970: 1_800_000_050)
        let file = { (id: UUID) in "\(id.uuidString.lowercased()).jpg" }
        for id in [sending, preparing] { try Self.smallJPEG.write(to: dir.appending(path: "photos/\(file(id))")) }
        let saved: [MeetingPhotos.Local] = [
            .init(id: sending, recordingID: e.id, at: 5, takenAt: at, state: .uploading, text: "Q3 budget", file: file(sending)),
            .init(id: preparing, recordingID: e.id, at: 6, takenAt: at, state: .preparing, text: nil, file: file(preparing)),
            .init(id: lost, recordingID: e.id, at: 7, takenAt: at, state: .uploading, text: nil, file: file(lost)),
        ]
        try JSONEncoder().encode(saved).write(to: dir.appending(path: "photos.json"))
        let (hub, _) = photoHub(e.id)
        let photos = store(hub, root: root, recordings: [e.id])
        photos.start()
        await until("listed") { photos.listed(for: e.id).count == 1 }
        #expect(try ids(hub, e.id) == [sending.uuidString.lowercased()])
        let sent = try body(hub.sent("POST", path(e.id, "photos")).first)
        #expect(sent["data"] as? String == Self.smallJPEG.base64EncodedString(), "the kept JPEG, as prepared")
        #expect(sent["at"] as? Double == 5)
        #expect(!photos.local(for: e.id).contains { $0.id == preparing || $0.id == lost })
    }

    // MARK: Thumbnails

    /// The thread's thumbnail comes from the JPEG kept here; a photo this phone never had comes from the hub once.
    @Test func thumbnailsComeFromThePhoneThenTheHub() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let other = "6f1e2a3b-0000-4000-8000-000000000009"
        let png = Self.encoded(Self.picture(300, 200), .png)
        hub.on("GET", path(e.id, "photos/9")) { _ in png }
        let photos = store(hub, root: root)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        let mine = try #require(await photos.thumbnail(e.id, photoID: id.uuidString.lowercased(), n: nil, side: 64))
        #expect(max(mine.size.width, mine.size.height) * mine.scale <= 64)
        let theirs = try #require(await photos.thumbnail(e.id, photoID: other, n: 9, side: 50))
        #expect(max(theirs.size.width, theirs.size.height) * theirs.scale <= 50)
        _ = await photos.thumbnail(e.id, photoID: other, n: 9, side: 50)
        #expect(hub.sent("GET", path(e.id, "photos/9")).count == 1, "kept in memory")
        let full = try #require(await photos.image(e.id, photoID: other, n: 9))
        #expect(full.size.width * full.scale == 300)
    }

    // MARK: An answer lost (#3862 review: P0)

    /// Lets a request through only once `release` is called (a try on its way while the case does something else).
    private nonisolated final class Gate: @unchecked Sendable {
        private let lock = NSLock()
        private var open = false
        private var waiting: CheckedContinuation<Void, Never>?

        func wait() async {
            await withCheckedContinuation { (c: CheckedContinuation<Void, Never>) in
                let now = lock.withLock { () -> Bool in
                    if open { return true }
                    waiting = c
                    return false
                }
                if now { c.resume() }
            }
        }

        func release() {
            let c = lock.withLock { () -> CheckedContinuation<Void, Never>? in
                open = true
                defer { waiting = nil }
                return waiting
            }
            c?.resume()
        }
    }

    private nonisolated final class Counter: @unchecked Sendable {
        private let lock = NSLock()
        private var n = 0
        func next() -> Int { lock.withLock { n += 1; return n } }
        var count: Int { lock.withLock { n } }
    }

    /// The hub's client with the second `POST` held at `gate` until it is released, then answered by `late` (nil: as the
    /// hub answers it).
    private func heldClient(_ hub: FakeHub, gate: Gate, posts: Counter, late: Error?) -> APIClient {
        var client = hub.client
        let inner = client.transport!
        client.transport = { req in
            if req.method == "POST", posts.next() == 2 {
                await gate.wait()
                if let late { throw late }
            }
            return try await inner(req)
        }
        return client
    }

    private func photosFile(_ root: URL, _ e: RecordingEntry) -> URL {
        root.appending(path: e.id.uuidString).appending(path: MeetingPhotos.listFile)
    }

    /// The first `POST` is stored and its answer lost; every try after it fails (the phone offline), so the photo reads
    /// Couldn't upload and is kept for a relaunch. The next list has it: it is the meeting's material, and nothing of it is
    /// left to send, in this run and in the next one (which loaded it as failed before the list came).
    @Test func aStoredPhotoWhoseAnswerWasLostIsStoredOnceListed() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.lose = { $0 == 1 }
        state.refuse = { if $0 > 1 { throw URLError(.notConnectedToInternet) } }
        let before = store(hub, root: root, recordings: [e.id])
        let id = try #require(before.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("failed") { failed(before, e.id) }
        await before.flushed()
        #expect(state.photos.count == 1, "the hub has it")
        #expect(FileManager.default.fileExists(atPath: photosFile(root, e).path), "kept as failed")

        // A relaunch first: the photo comes back failed, then the list has it.
        let after = store(hub, root: root, recordings: [e.id])
        after.start()
        await after.flushed()
        try #require(failed(after, e.id))
        #expect(await after.refresh(e.id))
        #expect(after.local(for: e.id).isEmpty, "stored, not Couldn't upload")
        let item = try #require(after.album(e.id).items[id.uuidString.lowercased()])
        #expect(item.stored && item.n == 1 && !item.failed)
        await after.flushed()
        #expect(!FileManager.default.fileExists(atPath: photosFile(root, e).path), "nothing left to send at the next launch")
        let jpeg = root.appending(path: e.id.uuidString).appending(path: "photos/\(id.uuidString.lowercased()).jpg")
        #expect(FileManager.default.fileExists(atPath: jpeg.path), "its JPEG stays for the thumbnail")

        // The run that sent it reads the list too.
        #expect(await before.refresh(e.id))
        #expect(before.local(for: e.id).isEmpty)
        #expect(before.listed(for: e.id).map(\.n) == [1])
    }

    /// The answer lost, and the list read while the upload waits out its backoff: the photo is stored there, and the try
    /// after the wait never goes.
    @Test func aListReadDuringTheBackoffEndsTheUpload() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.lose = { $0 == 1 }
        final class Holder { var photos: MeetingPhotos? }
        let holder = Holder(), waits = Waits()
        let photos = store(hub, root: root, waits: waits, wait: { _ in _ = await holder.photos?.refresh(e.id) })
        holder.photos = photos
        _ = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        await until("its local copy gone") { photos.local(for: e.id).isEmpty }
        try await Task.sleep(for: .milliseconds(50))
        #expect(hub.sent("POST", path(e.id, "photos")).count == 1, "no try after the list had it")
        #expect(waits.list == [.seconds(2)])
        #expect(state.photos.count == 1)
    }

    /// A try still on its way when the list says the photo is stored: a refusal that comes back late changes nothing
    /// (it never reads Couldn't upload over the list's word).
    @Test func aLateFailureNeverUndoesTheList() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.lose = { $0 == 1 }
        let gate = Gate(), posts = Counter()
        let photos = store(hub, root: root, client: heldClient(hub, gate: gate, posts: posts, late: refused(413, "photos_full")))
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("the second try on its way") { posts.count == 2 }
        #expect(await photos.refresh(e.id))
        #expect(photos.local(for: e.id).isEmpty)
        gate.release()
        try await Task.sleep(for: .milliseconds(100))
        #expect(photos.local(for: e.id).isEmpty, "the late refusal didn't bring a failed copy back")
        #expect(photos.album(e.id).items[id.uuidString.lowercased()]?.stored == true)
        await photos.flushed()
        #expect(!FileManager.default.fileExists(atPath: photosFile(root, e).path))
    }

    /// A try still on its way when the owner deletes the photo the list has: its late `200` never brings it back.
    @Test func aLateAnswerNeverBringsADeletedPhotoBack() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.lose = { $0 == 1 }
        hub.on("DELETE", path(e.id, "photos/1")) { _ in
            state.photos.removeAll()
            return Data(#"{"deleted":true}"#.utf8)
        }
        let gate = Gate(), posts = Counter()
        let photos = store(hub, root: root, client: heldClient(hub, gate: gate, posts: posts, late: nil))
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("the second try on its way") { posts.count == 2 }
        #expect(await photos.refresh(e.id))
        try await photos.delete(e.id, n: 1)
        // The hub answers the held try with the photo as it stored it, before the delete.
        state.photos = [["n": 1, "id": id.uuidString.lowercased(), "at": 3, "mime": "image/jpeg", "cited": false, "forwarded": true]]
        gate.release()
        try await Task.sleep(for: .milliseconds(100))
        #expect(photos.listed(for: e.id).isEmpty, "deleted stays deleted")
        #expect(photos.local(for: e.id).isEmpty)
        #expect(photos.album(e.id).items.isEmpty)
    }

    // MARK: What the list's refusals mean (P2)

    /// Only `404 recording_not_found` and `410 recording_deleted` mean the recording (and so its photos) is gone; a `404`
    /// without a code is a manager without the photo routes, which says nothing about the photos: what was known stands,
    /// and a list never read stays unread.
    @Test func onlyTheRecordingsOwnRefusalsEmptyTheList() async throws {
        let hub = FakeHub(), id = UUID(), fresh = UUID(), deletedRecording = UUID()
        hub.on("GET", path(id, "photos"), json: #"{"photos":[{"n":1,"id":"6f1e2a3b-0000-4000-8000-000000000001","at":2,"mime":"image/jpeg","forwarded":true}]}"#)
        let photos = store(hub, root: FileManager.default.temporaryDirectory)
        #expect(await photos.refresh(id))
        hub.on("GET", path(id, "photos")) { _ in throw APIError(status: 404, message: "no route") }
        #expect(await photos.refresh(id) == false)
        #expect(photos.listed(for: id).map(\.n) == [1], "still listed")
        #expect(photos.loaded.contains(id))
        #expect(photos.album(id).items.count == 1)

        hub.on("GET", path(fresh, "photos")) { _ in throw APIError(status: 404, message: "no route") }
        #expect(await photos.refresh(fresh) == false)
        #expect(!photos.loaded.contains(fresh), "a room photo isn't Photo removed over an unknown answer")
        #expect(!photos.album(fresh).loaded)

        hub.on("GET", path(deletedRecording, "photos")) { _ in throw APIError(status: 410, message: "gone", fields: ["code": "recording_deleted"]) }
        #expect(await photos.refresh(deletedRecording))
        #expect(photos.loaded.contains(deletedRecording) && photos.listed(for: deletedRecording).isEmpty)
        hub.on("GET", path(id, "photos")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) }
        #expect(await photos.refresh(id))
        #expect(photos.listed(for: id).isEmpty)
    }

    // MARK: Refusals that stay (P3, P17)

    /// Retry only where it can go otherwise: not for a picture the hub can't take, a meeting that takes no more photos,
    /// a hub without the routes or a device without access; yes after no answer, a busy or read-only hub, or a full
    /// meeting (once a photo is deleted).
    @Test func retryOnlyWhereItCanHelp() {
        for error in [refused(400), refused(413, "too_large"), refused(409, "not_meeting"), refused(409, "transcript_expired"),
                      refused(404), refused(403, "forbidden"), refused(403), refused(410, "recording_deleted")] as [Error] {
            #expect(!MeetingPhotos.canRetry(after: error), "\(error)")
        }
        for error in [URLError(.timedOut), refused(503), refused(429), refused(403, "kb_read_only"), refused(413, "photos_full"),
                      refused(404, "recording_not_found")] as [Error] {
            #expect(MeetingPhotos.canRetry(after: error), "\(error)")
        }
        #expect(MeetingPhotos.message(refused(403, "forbidden")) != MeetingPhotos.message(refused(500)), "its own words")
    }

    /// A refusal the hub would give again keeps the photo with its words and no Retry, over a relaunch too; Retry does
    /// nothing for it, and Delete Photo drops it without asking the hub's list (the hub refused it after the point where it
    /// would have answered with a stored one).
    @Test func aRefusalThatStaysHasNoRetryEvenAfterARelaunch() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { _ in throw APIError(status: 409, message: "trimmed", fields: ["code": "transcript_expired"]) }
        let before = store(hub, root: root, recordings: [e.id])
        let id = try #require(before.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("failed") { failed(before, e.id) }
        await before.flushed()
        let after = store(hub, root: root, recordings: [e.id])
        after.start()
        await after.flushed()
        let back = try #require(after.local(for: e.id).first)
        #expect(back.state == .failed(message: MeetingPhotos.message(refused(409, "transcript_expired")), code: "transcript_expired",
                                      retry: false))
        #expect(!after.canRetry(back))
        let item = try #require(after.album(e.id).items[id.uuidString.lowercased()])
        #expect(item.failed && !item.retryable && item.deletable)
        after.retry(id)
        try await Task.sleep(for: .milliseconds(50))
        #expect(hub.sent("POST", path(e.id, "photos")).count == 1, "Retry sent nothing")
        await after.remove(item, of: e.id)
        #expect(after.local(for: e.id).isEmpty)
        #expect(hub.sent("GET", path(e.id, "photos")).isEmpty, "never on the hub: nothing to ask")
    }

    /// `410 recording_deleted` on a try: the recording is gone at the hub, so the photo goes for good, its JPEG and its
    /// entry in `photos.json` with it, and no launch brings it back. A delete answered so (or `404 recording_not_found`)
    /// is done.
    @Test func aDeletedRecordingTakesItsPhotosForGood() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { _ in throw APIError(status: 410, message: "gone", fields: ["code": "recording_deleted"]) }
        let photos = store(hub, root: root, recordings: [e.id])
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("dropped") { photos.local(for: e.id).isEmpty && hub.sent("POST", path(e.id, "photos")).count == 1 }
        await photos.flushed()
        #expect(!FileManager.default.fileExists(atPath: photosFile(root, e).path))
        let jpeg = root.appending(path: e.id.uuidString).appending(path: "photos/\(id.uuidString.lowercased()).jpg")
        await until("its JPEG gone") { !FileManager.default.fileExists(atPath: jpeg.path) }
        let next = store(hub, root: root, recordings: [e.id])
        next.start()
        await next.flushed()
        #expect(next.local(for: e.id).isEmpty, "not back at launch")

        let other = UUID()
        hub.on("GET", path(other, "photos"), json: #"{"photos":[{"n":1,"id":"6f1e2a3b-0000-4000-8000-000000000001","at":2,"mime":"image/jpeg"},{"n":2,"id":"6f1e2a3b-0000-4000-8000-000000000002","at":3,"mime":"image/jpeg"}]}"#)
        await next.refresh(other)
        hub.on("DELETE", path(other, "photos/1")) { _ in throw APIError(status: 410, message: "gone", fields: ["code": "recording_deleted"]) }
        hub.on("DELETE", path(other, "photos/2")) { _ in throw APIError(status: 404, message: "no", fields: ["code": "recording_not_found"]) }
        try await next.delete(other, n: 1)
        try await next.delete(other, n: 2)
        #expect(next.listed(for: other).isEmpty)
    }

    // MARK: Delete Photo on a failed one (P8)

    /// A failed photo whose upload may have been stored (its answer lost, every try after it failed): Delete Photo reads
    /// the list first; the hub has it, so it is deleted there by its number.
    @Test func deletingAFailedPhotoTheHubHasDeletesItThere() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.lose = { $0 == 1 }
        state.refuse = { if $0 > 1 { throw URLError(.notConnectedToInternet) } }
        hub.on("DELETE", path(e.id, "photos/1")) { _ in
            state.photos.removeAll()
            return Data(#"{"deleted":true}"#.utf8)
        }
        let photos = store(hub, root: root)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("failed") { failed(photos, e.id) }
        let item = try #require(photos.album(e.id).items[id.uuidString.lowercased()])
        #expect(item.n == nil && item.deletable)
        await photos.remove(item, of: e.id)
        #expect(hub.sent("DELETE", path(e.id, "photos/1")).count == 1, "deleted at the hub")
        #expect(state.photos.isEmpty)
        #expect(photos.local(for: e.id).isEmpty && photos.listed(for: e.id).isEmpty)
        #expect(photos.album(e.id).problems.isEmpty && photos.album(e.id).deleting.isEmpty)
    }

    /// The hub doesn't have it: it only leaves this phone. With no list to read (offline), it stays, and why is said.
    @Test func deletingAFailedPhotoTheHubLacksOnlyDropsItHere() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        state.refuse = { _ in throw URLError(.notConnectedToInternet) }
        let photos = store(hub, root: root)
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("failed") { failed(photos, e.id) }
        let key = id.uuidString.lowercased()

        hub.on("GET", path(e.id, "photos")) { _ in throw URLError(.notConnectedToInternet) }
        await photos.remove(try #require(photos.album(e.id).items[key]), of: e.id)
        #expect(photos.local(for: e.id).count == 1, "the hub may have it: it stays")
        #expect(photos.album(e.id).problems[key] == MeetingPhotos.message(URLError(.cannotConnectToHost), deleting: true))

        hub.on("GET", path(e.id, "photos"), json: #"{"photos":[]}"#)
        await photos.remove(try #require(photos.album(e.id).items[key]), of: e.id)
        #expect(photos.local(for: e.id).isEmpty)
        #expect(hub.sent("DELETE", path(e.id, "photos/1")).isEmpty)
        #expect(photos.album(e.id).items.isEmpty && photos.album(e.id).problems.isEmpty)
    }

    // MARK: Files of photos that went (P10)

    /// A stored photo the next list no longer has (deleted elsewhere, or cut with the transcript) takes its JPEG and its
    /// thumbnail with it; a photo still to send keeps its own.
    @Test func aPhotoTheListNoLongerHasLeavesThePhone() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, state) = photoHub(e.id)
        let photos = store(hub, root: root)
        photos.start()
        let id = try #require(photos.add(.camera(Data("x".utf8)), to: e, at: 3))
        await until("listed") { photos.listed(for: e.id).count == 1 }
        await until("the local copy gives way") { photos.local(for: e.id).isEmpty }
        let key = id.uuidString.lowercased()
        _ = try #require(await photos.thumbnail(e.id, photoID: key, n: 1, side: 64))
        #expect(photos.hasThumbnail(e.id, photoID: key))
        let jpeg = root.appending(path: e.id.uuidString).appending(path: "photos/\(key).jpg")
        #expect(FileManager.default.fileExists(atPath: jpeg.path))

        state.photos.removeAll()
        #expect(await photos.refresh(e.id))
        await until("its JPEG gone") { !FileManager.default.fileExists(atPath: jpeg.path) }
        #expect(!photos.hasThumbnail(e.id, photoID: key))

        // A photo on its way (failing for now) keeps its JPEG through the same reads.
        state.refuse = { _ in throw URLError(.notConnectedToInternet) }
        let waiting = try #require(photos.add(.camera(Data("y".utf8)), to: e, at: 4))
        await until("failed") { failed(photos, e.id) }
        #expect(await photos.refresh(e.id))
        try await Task.sleep(for: .milliseconds(100))
        let kept = root.appending(path: e.id.uuidString).appending(path: "photos/\(waiting.uuidString.lowercased()).jpg")
        #expect(FileManager.default.fileExists(atPath: kept.path))
    }

    /// After a relaunch (stored photos aren't in `photos.json`): a JPEG of a photo the list no longer has goes once the
    /// launch read is in; one of another shape in the list stays.
    @Test func aRelaunchStillClearsTheJPEGsOfPhotosThatWent() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let dir = root.appending(path: e.id.uuidString).appending(path: "photos")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let gone = "6f1e2a3b-0000-4000-8000-00000000000a", listed = "6f1e2a3b-0000-4000-8000-00000000000b"
        let odd = "6f1e2a3b-0000-4000-8000-00000000000c"
        for name in [gone, listed, odd] { try Self.smallJPEG.write(to: dir.appending(path: "\(name).jpg")) }
        let hub = FakeHub()
        hub.on("GET", path(e.id, "photos"), json: #"{"photos":[{"n":2,"id":"\#(listed)","at":3,"mime":"image/jpeg"},{"n":"three","id":"\#(odd)"}]}"#)
        let photos = store(hub, root: root, recordings: [e.id])
        photos.start()
        await photos.flushed()
        #expect(await photos.refresh(e.id))
        await until("the gone one's JPEG gone") { !FileManager.default.fileExists(atPath: dir.appending(path: "\(gone).jpg").path) }
        #expect(FileManager.default.fileExists(atPath: dir.appending(path: "\(listed).jpg").path))
        #expect(FileManager.default.fileExists(atPath: dir.appending(path: "\(odd).jpg").path), "named, though unreadable")
    }

    // MARK: One at a time (P4), rows at once (P19)

    private nonisolated final class Running: @unchecked Sendable {
        private let lock = NSLock()
        private var now = 0
        private(set) var most = 0
        func enter() { lock.withLock { now += 1; most = max(most, now) } }
        func leave() { lock.withLock { now -= 1 } }
        var peak: Int { lock.withLock { most } }
    }

    /// Four photos picked at once are prepared one after another, never side by side.
    @Test func photosArePreparedOneAtATime() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let running = Running(), jpeg = Self.smallJPEG
        let photos = store(hub, root: root, prepare: { _, _ in
            running.enter()
            try? await Task.sleep(for: .milliseconds(30))
            running.leave()
            return MeetingPhotoPipeline.Prepared(jpeg: jpeg, width: 400, height: 300, text: nil)
        })
        for at in 1...4 { _ = photos.add(.library(Data("x".utf8)), to: e, at: Double(at)) }
        await until("all listed") { photos.listed(for: e.id).count == 4 }
        #expect(running.peak == 1)
    }

    /// The pipeline lets the original go once it is downscaled, before Vision reads it.
    @Test func thePipelineLetsTheOriginalGo() async throws {
        let original = MeetingPhotoPipeline.Original(Self.encoded(Self.picture(800, 600)))
        let prepared = await MeetingPhotoPipeline.prepare(original, locale: "en-US")
        #expect(prepared?.width == 800)
        #expect(original.data == nil)
    }

    /// Picked photos show at once, Preparing…, before Photos hands their bytes over; one it can't hand over leaves again
    /// and is counted until the next pick.
    @Test func pickedPhotosShowAtOnce() async throws {
        let e = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        let (hub, _) = photoHub(e.id)
        let photos = store(hub, root: root)
        let gate = Gate()
        let ids = photos.pick([
            { await gate.wait(); return Data("x".utf8) },
            { await gate.wait(); return nil },
        ], to: e, at: 7)
        #expect(ids.count == 2)
        #expect(photos.local(for: e.id).map(\.state) == [.preparing, .preparing], "both rows at once")
        #expect(photos.album(e.id).order == ids.map { $0.uuidString.lowercased() })
        gate.release()
        await until("one listed") { photos.listed(for: e.id).count == 1 }
        await until("the unread one gone") { photos.local(for: e.id).isEmpty }
        #expect(photos.unreadPicks(e.id) == 1)
        _ = photos.pick([{ Data("y".utf8) }], to: e, at: 8)
        #expect(photos.unreadPicks(e.id) == 0, "a new pick starts over")
    }

    // MARK: The pages' view (P5, P11)

    /// A recording's album: the hub's photos by their time in the meeting (forwarded or not: one whose room message isn't
    /// loaded is still there), then this phone's on their way; a change to another recording's photos leaves it alone.
    @Test func anAlbumHoldsItsRecordingsPhotosOnly() async throws {
        let e = meeting(), other = meeting()
        let root = try folder(for: e)
        defer { try? FileManager.default.removeItem(at: root) }
        try FileManager.default.createDirectory(at: root.appending(path: other.id.uuidString), withIntermediateDirectories: true)
        let hub = FakeHub()
        hub.on("GET", path(e.id, "photos"), json: """
            {"photos":[
             {"n":1,"id":"6f1e2a3b-0000-4000-8000-000000000001","at":40,"mime":"image/jpeg","forwarded":true,"note":"  ","added_at":"2026-11-11T10:00:00Z"},
             {"n":2,"id":"6f1e2a3b-0000-4000-8000-000000000002","at":12,"mime":"image/jpeg","forwarded":false,"caption":"Board"}
            ]}
            """)
        hub.on("POST", path(other.id, "photos")) { _ in throw URLError(.notConnectedToInternet) }
        let photos = store(hub, root: root)
        let album = photos.album(e.id)
        #expect(!album.loaded)
        #expect(await photos.refresh(e.id))
        #expect(album.loaded)
        #expect(album.order == ["6f1e2a3b-0000-4000-8000-000000000002", "6f1e2a3b-0000-4000-8000-000000000001"])
        #expect(album.numbers == [1: "6f1e2a3b-0000-4000-8000-000000000001", 2: "6f1e2a3b-0000-4000-8000-000000000002"])
        let first = try #require(album.items["6f1e2a3b-0000-4000-8000-000000000001"])
        #expect(first.note == nil, "a blank note is none")
        #expect(first.addedAt == JSON.parseDate("2026-11-11T10:00:00Z"), "read once, when the list came")
        #expect(album.items["6f1e2a3b-0000-4000-8000-000000000002"]?.preview == "Board")

        let touched = Counter()
        withObservationTracking {
            _ = album.items
            _ = album.order
            _ = album.loaded
        } onChange: {
            _ = touched.next()
        }
        let theirs = try #require(photos.add(.camera(Data("x".utf8)), to: other, at: 1))
        await until("the other's photo failed") { failed(photos, other.id) }
        #expect(touched.count == 0, "another recording's photos redraw nothing here")
        #expect(photos.album(other.id).order == [theirs.uuidString.lowercased()])
    }
}
