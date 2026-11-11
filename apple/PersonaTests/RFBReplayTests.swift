import Foundation
import Testing
@testable import Persona

/// What the live screen costs the phone (#3067): a vncbench recording (`go run ./cmd/vncbench record`, VNCREC1)
/// replayed at its own pace through the app's RFBClient and RFBFrameSink, and the CPU time, updates, bytes copied into
/// pixel buffers and decode time per update printed. Skipped unless TEST_RUNNER_PERSONA_VNC_REPLAY names a recording
/// (several: separated by commas).
struct RFBReplayTests {
    @Test func replayARecording() async throws {
        guard let files = ProcessInfo.processInfo.environment["PERSONA_VNC_REPLAY"], !files.isEmpty else { return }
        for file in files.split(separator: ",").map(String.init) {
            let t = try ReplayTransport(file: file)
            let client = RFBClient(transport: t) { nil }
            let sink = RFBFrameSink()
            let seen = ReplaySeen()
            client.onState = { s in
                switch s {
                case .failed, .closed, .refused: seen.end()
                default: break
                }
            }
            sink.attach(client) { _, _ in }
            let inner = client.onFrame
            client.onFrame = { fb, r in
                seen.frame()
                inner(fb, r)
            }
            let cpu0 = Self.cpu()
            client.start()
            for _ in 0..<12000 where !seen.ended { try await Task.sleep(for: .milliseconds(5)) }
            try await Task.sleep(for: .milliseconds(100)) // the last copy
            let cpu = Self.cpu() - cpu0, span = t.span
            print(String(format: "replay %@: %.1f MB over %.1f s, %d updates; CPU %.3f s (%.1f%% of the recording), %.2f ms an update; copied %.1f MB",
                         (file as NSString).lastPathComponent, Double(t.bytes) / 1e6, span, seen.frames, cpu,
                         100 * cpu / max(span, 0.001), 1000 * cpu / Double(max(seen.frames, 1)), Double(sink.copied) / 1e6))
            #expect(seen.ended && seen.frames > 0)
        }
    }

    private static func cpu() -> Double {
        var u = rusage()
        getrusage(RUSAGE_SELF, &u)
        return Double(u.ru_utime.tv_sec + u.ru_stime.tv_sec) + Double(u.ru_utime.tv_usec + u.ru_stime.tv_usec) / 1e6
    }
}

nonisolated private final class ReplaySeen: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0, done = false
    var frames: Int { lock.withLock { count } }
    var ended: Bool { lock.withLock { done } }
    func frame() { lock.withLock { count += 1 } }
    func end() { lock.withLock { done = true } }
}

/// A recording said at its pace after a made-up greeting with no sign-in (the client's requests are ignored).
nonisolated private final class ReplayTransport: RFBTransport, @unchecked Sendable {
    private let chunks: [(at: UInt64, bytes: [UInt8])]
    private let head: [UInt8]
    private let lock = NSLock()
    private var next = -1
    private var start: UInt64 = 0
    var span: Double { Double(chunks.last?.at ?? 0) / 1e9 }
    var bytes: Int { chunks.reduce(0) { $0 + $1.bytes.count } }

    init(file: String) throws {
        let d = try [UInt8](Data(contentsOf: URL(fileURLWithPath: file)))
        guard let nl = d.firstIndex(of: 10) else { throw URLError(.cannotParseResponse) }
        let header = String(decoding: d[0..<nl], as: UTF8.self).split(separator: " ")
        guard header.count == 3, header[0] == "VNCREC1", let w = Int(header[1]), let h = Int(header[2]) else { throw URLError(.cannotParseResponse) }
        var out: [(UInt64, [UInt8])] = []
        var at = nl + 1
        while at + 12 <= d.count {
            var t: UInt64 = 0, n = 0
            for k in 0..<8 { t = t << 8 | UInt64(d[at + k]) }
            for k in 8..<12 { n = n << 8 | Int(d[at + k]) }
            guard at + 12 + n <= d.count else { break }
            out.append((t, Array(d[(at + 12)..<(at + 12 + n)])))
            at += 12 + n
        }
        chunks = out
        func u16(_ v: Int) -> [UInt8] { [UInt8(v >> 8), UInt8(v & 0xff)] }
        var head = Array("RFB 003.889\n".utf8) + [1, 1] + RFBAuth.be32(0)
        head += u16(w) + u16(h) + [32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0] + RFBAuth.be32(0)
        self.head = head
    }

    func send(_ bytes: [UInt8]) async throws {}

    func receive() async throws -> [UInt8] {
        let k = lock.withLock { () -> Int in
            next += 1
            if next == 0 { start = DispatchTime.now().uptimeNanoseconds }
            return next
        }
        if k == 0 { return head }
        guard k - 1 < chunks.count else { throw URLError(.networkConnectionLost) }
        let c = chunks[k - 1], due = start + c.at, now = DispatchTime.now().uptimeNanoseconds
        if due > now { try await Task.sleep(nanoseconds: due - now) }
        return c.bytes
    }

    func close() {}
}
