import Foundation
import Testing
@testable import Persona

/// UI version 6's controls in rows (docs/extensions/extensions.md "UI version 6", "Controls", #827): the numbers a
/// slider or a stepper writes, and what a change does: shown at once, sent with `{name: value}`, one request a control
/// at a time with only the latest kept, the answer drawn, a failure put back, a redraw meanwhile keeping the user's
/// value.
@MainActor
struct ExtensionControlsTests {
    private static let path = "/api/v1/plugins/home/-/ui/page"

    private nonisolated static func view(on: Bool = false, minutes: Int = 10) -> String {
        """
        {"sections":[{"items":[
          {"type":"row","key":"inbox","title":"Add to Inbox","control":{"kind":"switch","name":"to_inbox","on":\(on),
           "action":{"id":"set","values":{"subscription":"s1"}}}},
          {"type":"row","key":"every","title":"Every","control":{"kind":"stepper","name":"minutes","value":\(minutes),
           "min":10,"max":60,"step":5,"unit":"min","action":{"id":"set"}}}
        ]}]}
        """
    }

    private nonisolated static func row(on: Bool) -> String {
        #"{"type":"row","key":"inbox","title":"Add to Inbox","control":{"kind":"switch","name":"to_inbox","on":\#(on),"action":{"id":"set"}}}"#
    }

    private func body(_ r: LocalRequest) throws -> [String: JSONValue] {
        guard case .object(let o) = try JSON.decoder.decode(JSONValue.self, from: r.body ?? Data()) else { return [:] }
        return o
    }

    private func sentValues(_ r: LocalRequest) throws -> [String: String] {
        guard case .object(let v)? = try body(r)["values"] else { return [:] }
        return v.compactMapValues { if case .string(let s) = $0 { s } else { nil } }
    }

    private func isAction(_ r: LocalRequest) throws -> Bool { try body(r)["action"] != nil }

    private func control(_ m: ExtensionViewModel, _ key: String) throws -> ExtensionRowControl {
        try #require(m.view?.sections.flatMap(\.items).first { $0.key == key }?.row?.control)
    }

    @Test func theNumbersAControlWrites() {
        let slider = ExtensionRowControl(kind: "slider", name: "b", value: 0.3, min: 0, max: 1, action: .init(id: "set"))
        #expect(slider.drawable && slider.stepSize == 0.01 && slider.decimals == 2)
        #expect(slider.snap(0.30000000004) == 0.3 && slider.write(slider.snap(0.30000000004)) == "0.3")
        #expect(slider.snap(-4) == 0 && slider.snap(7) == 1, "held to min…max")
        let k = ExtensionRowControl(kind: "slider", name: "k", value: 4000, min: 2700, max: 6500, step: 100, display: "4000 K",
                                    action: .init(id: "set"))
        #expect(k.snap(4049) == 4000 && k.snap(4051) == 4100 && k.write(4100) == "4100" && k.wire == "4000")
        #expect(k.written(4000) == "4000 K" && k.written(4100) == Double(4100).formatted(.number.precision(.fractionLength(0...0))),
                "its display only for the value it drew")
        let t = ExtensionRowControl(kind: "stepper", name: "t", value: 22, step: 0.5, unit: "°C", action: .init(id: "set"))
        #expect(t.snap(22.49) == 22.5 && t.write(22.5) == "22.5" && t.written(22.5).hasSuffix(" °C"))
        #expect(t.bounds.lowerBound < -1e300 && t.bounds.upperBound > 1e300, "a stepper without bounds has none")
        let s = ExtensionRowControl(kind: "switch", name: "s", on: true, action: .init(id: "toggle"))
        #expect(s.drawable && s.wire == "true" && ExtensionRowControl(kind: "switch", name: "s", action: .init(id: "t")).wire == "false")

        #expect(!ExtensionRowControl(kind: "slider", name: "b", min: 1, max: 1, action: .init(id: "set")).drawable, "a slider needs min < max")
        #expect(!ExtensionRowControl(kind: "slider", name: "b", action: .init(id: "set")).drawable)
        #expect(!ExtensionRowControl(kind: "dial", name: "b", action: .init(id: "set")).drawable, "a kind this app does not know")
        #expect(!ExtensionRowControl(kind: "switch", name: "", action: .init(id: "set")).drawable)
        #expect(!ExtensionRowControl(kind: "switch", name: "s").drawable, "no action")
    }

    @Test func aSwitchSendsItsValueAndDrawsTheAnswer() async throws {
        let hub = FakeHub()
        hub.on("POST", Self.path) { [self] r in
            Data((try isAction(r) ? #"{"items":[\#(Self.row(on: true))]}"# : #"{"view":\#(Self.view())}"#).utf8)
        }
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        await m.load()
        let o = await m.change(try control(m, "inbox"), to: "true")
        #expect(o.error == nil)
        let sent = try #require(hub.sent("POST", Self.path).last)
        #expect(try body(sent)["action"] == .string("set"))
        #expect(try sentValues(sent) == ["subscription": "s1", "to_inbox": "true"], "the action's values and the control's")
        #expect(try control(m, "inbox").on == true, "the answer's row in place of the old one")
        #expect(m.controlValues.isEmpty && m.slowControls.isEmpty)
        #expect(try control(m, "every").value == 10, "the rest of the page as it was")
    }

    @Test func aFailurePutsBackTheExtensionsValue() async throws {
        let hub = FakeHub()
        hub.on("POST", Self.path) { [self] r in
            if try isAction(r) { throw APIError(status: 502, message: "the device is offline") }
            return Data(#"{"view":\#(Self.view())}"#.utf8)
        }
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        await m.load()
        let o = await m.change(try control(m, "inbox"), to: "true")
        #expect(o.error != nil)
        #expect(try m.controlValues["to_inbox"] == nil && control(m, "inbox").on == false)
    }

    @Test func anAnswerThatDrawsNothingDrawsThePageAgain() async throws {
        let hub = FakeHub()
        let drawn = Recorder()
        hub.on("POST", Self.path) { [self] r in
            if try isAction(r) { return Data(#"{"toast":"Saved"}"#.utf8) }
            drawn.record(r)
            return Data(#"{"view":\#(Self.view(on: drawn.requests.count > 1))}"#.utf8)
        }
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        await m.load()
        let o = await m.change(try control(m, "inbox"), to: "true")
        #expect(o.toast == "Saved" && drawn.requests.count == 2)
        #expect(try control(m, "inbox").on == true && m.controlValues.isEmpty)
    }

    @Test func oneRequestAControlWithOnlyTheLatestKept() async throws {
        let gate = Gate()
        let state = Recorder()
        var client = APIClient(config: ServerConfig(url: URL(string: "https://fake.invalid")!, token: "t"))
        client.transport = { r in
            let isAction = (try? JSON.decoder.decode([String: JSONValue].self, from: r.body ?? Data()))?["action"] != nil
            guard isAction else { return Data(#"{"view":\#(Self.view())}"#.utf8) }
            await state.record(r)
            await gate.wait()
            return Data(#"{"toast":"ok","items":[]}"#.utf8)
        }
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { client }))
        await m.load()
        let every = try control(m, "every")
        let first = Task { await m.change(every, to: "15") }
        for _ in 0..<10_000 where state.requests.isEmpty { await Task.yield() }
        try #require(state.requests.count == 1, "the first change is on its way")
        #expect(m.controlValues["minutes"] == "15", "shown at once")
        // Changed twice more while the first is on its way: neither goes yet, the latest shows.
        _ = await m.change(every, to: "20")
        _ = await m.change(every, to: "25")
        #expect(state.requests.count == 1 && m.controlValues["minutes"] == "25")
        // A redraw meanwhile (ui.invalidate, refresh_after) keeps the user's value.
        await m.refresh()
        #expect(try m.controlValues["minutes"] == "25" && control(m, "every").value == 10)
        // Over a second on its way, a spinner shows.
        try await Task.sleep(for: .milliseconds(1200))
        #expect(m.slowControls == ["minutes"])
        gate.open()
        _ = await first.value
        #expect(try state.requests.map { try sentValues($0)["minutes"] } == ["15", "25"], "the first, then only the latest")
        #expect(m.controlValues.isEmpty && m.slowControls.isEmpty)
    }

    @Test func aDisabledControlSendsNothing() async throws {
        let hub = FakeHub()
        hub.on("POST", Self.path, json: #"{"view":\#(Self.view())}"#)
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        await m.load()
        var c = try control(m, "inbox")
        c.disabled = true
        let before = hub.requests.count
        _ = await m.change(c, to: "true")
        #expect(hub.requests.count == before && m.controlValues.isEmpty)
    }

    @Test func aPreviewGoesBackUnlessItsChangeIsOnItsWay() async throws {
        let hub = FakeHub()
        hub.on("POST", Self.path, json: #"{"view":\#(Self.view())}"#)
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        await m.load()
        m.preview("to_inbox", "true")
        #expect(m.controlValues["to_inbox"] == "true")
        m.preview("to_inbox", nil)
        #expect(m.controlValues["to_inbox"] == nil, "a confirm cancelled: the extension's value again")
    }
}

/// Holds a request until opened; once open, lets every later one through.
@MainActor private final class Gate {
    private var waiters: [CheckedContinuation<Void, Never>] = []
    private var isOpen = false

    func wait() async {
        if isOpen { return }
        await withCheckedContinuation { waiters.append($0) }
    }

    func open() {
        isOpen = true
        let w = waiters
        waiters = []
        w.forEach { $0.resume() }
    }
}

@MainActor private final class Recorder {
    private(set) var requests: [LocalRequest] = []
    func record(_ r: LocalRequest) { requests.append(r) }
}
