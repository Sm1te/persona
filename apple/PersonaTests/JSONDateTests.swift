import Foundation
import Testing
@testable import Persona

/// `JSON.parseDate` reads the hub's usual timestamp straight from its bytes (#4681): the same `Date` as the
/// ISO8601DateFormatter path it replaced, which still decides every other shape.
struct JSONDateTests {
    /// The path before #4681, kept here as the reference. Its formatters are made per call: a formatter is not
    /// Sendable, and the cases run in parallel.
    private static func reference(_ s: String) -> Date? {
        guard !s.isEmpty else { return nil }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        var t = s
        if let dot = t.firstIndex(of: "."), let end = t[dot...].firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) {
            let frac = t[t.index(after: dot)..<end]
            if frac.count > 3 { t.replaceSubrange(t.index(after: dot)..<end, with: frac.prefix(3)) }
        }
        return fractional.date(from: t) ?? plain.date(from: t)
    }

    @Test(arguments: [
        "2026-11-11T10:46:29Z", "2026-11-11T10:46:29.1Z", "2026-11-11T10:46:29.12Z", "2026-11-11T10:46:29.123Z",
        "2026-11-11T10:46:29.123456789Z", "2026-11-11T10:46:29.123456789+08:00", "2026-11-11T10:46:29-05:30",
        "2024-02-29T23:59:59.999Z", "1970-01-01T00:00:00Z", "1969-12-31T23:59:59.999Z", "1900-01-01T00:00:00Z",
        "2000-02-29T12:00:00Z", "9999-12-31T23:59:59.999Z",
    ])
    func usualShapeIsReadDirectly(_ s: String) throws {
        let fast = try #require(JSON.internetDate(s))
        #expect(fast == Self.reference(s))
        #expect(JSON.parseDate(s) == fast)
    }

    /// What the direct read leaves alone still means what it meant: Go's zero time, lenient or odd forms, junk.
    @Test(arguments: [
        "0001-01-01T00:00:00Z", "1899-12-31T23:59:59Z", "2023-02-29T00:00:00Z", "2100-02-29T00:00:00Z",
        "2026-11-11T24:00:00Z", "2026-11-11T23:59:60Z", "2026-11-11T10:46:29+0800", "2026-11-11T10:46:29+08",
        "2026-11-11T10:46:29Z ", "2026-11-11T10:46:29.123Zjunk", "2026-1-11T10:46:29Z", "2026-11-11 10:46:29Z",
        "2026-11-11t10:46:29z", "2026-11-11T10:46:29", "2026-11-11T10:46:29.Z", "2026-11-11", "", "garbage",
    ])
    func otherShapesKeepTheFormatters(_ s: String) {
        #expect(JSON.internetDate(s) == nil)
        #expect(JSON.parseDate(s) == Self.reference(s))
    }

    @Test func randomTimestampsMatchTheFormatters() {
        var rng = SystemRandomNumberGenerator()
        func r(_ range: ClosedRange<Int>) -> Int { Int.random(in: range, using: &rng) }
        func pad(_ v: Int, _ width: Int) -> String { String(repeating: "0", count: max(0, width - String(v).count)) + String(v) }
        for _ in 0..<5_000 {
            var s = "\(pad(r(1890...2100), 4))-\(pad(r(1...12), 2))-\(pad(r(1...31), 2))T"
                + "\(pad(r(0...23), 2)):\(pad(r(0...59), 2)):\(pad(r(0...59), 2))"
            if r(0...2) > 0 { s += "." + (0..<r(1...9)).map { _ in String(r(0...9)) }.joined() }
            s += [
                "Z", "+\(pad(r(0...14), 2)):\(pad([0, 30, 45][r(0...2)], 2))", "-\(pad(r(0...12), 2)):\(pad([0, 30][r(0...1)], 2))",
            ][r(0...2)]
            let reference = Self.reference(s)
            if let fast = JSON.internetDate(s) { #expect(fast == reference, "\(s)") }
            #expect(JSON.parseDate(s) == reference, "\(s)")
        }
    }
}
