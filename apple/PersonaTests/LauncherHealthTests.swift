import Foundation
import Testing
@testable import Persona

/// New Agent's launchers with their health on the computer (#1643, docs/server/launcher-health.md): decoded when the hub
/// sends it and not needed when it does not, grayed out with why when blocked, never left out, and not picked by default.
@MainActor struct LauncherHealthTests {
    private func launchers(_ json: String) throws -> [Launcher] {
        try JSON.decoder.decode(APIClient.LaunchersResponse.self, from: Data(json.utf8)).launchers
    }

    private func launcher(_ name: String, state: String? = nil, kind: String? = nil, error: String? = nil) -> Launcher {
        Launcher(name: name, health: state.map { LauncherHealth(state: $0, kind: kind, error: error) })
    }

    // MARK: Decoding

    @Test func anOlderHubSendsNoHealth() throws {
        let ls = try launchers(#"{"launchers":[{"name":"claude","model":"opus"},{"name":"codex","agent":"codex"}]}"#)
        #expect(ls.map(\.name) == ["claude", "codex"])
        #expect(ls.allSatisfy { $0.health == nil && $0.block == nil })
    }

    @Test func decodesTheHubsHealthRecords() throws {
        let ls = try launchers("""
        {"launchers":[
          {"name":"claude","model":"opus","health":{"state":"ok","checked_at":"2026-11-09T08:00:00.123456Z"}},
          {"name":"work_gw","base_url":"https://gateway.example.com","health":{"state":"blocked","kind":"not_in_terminal",
            "error":"  launcher “work_gw” not found in the terminal's shell (bash)  ","checked_at":"2026-11-09T08:01:00Z"}},
          {"name":"team_gw","health":{"state":"failing","kind":"timeout","error":"context deadline exceeded","checked_at":"2026-11-09T08:02:00Z"}}
        ],"default":{"provider":"claude","model":"opus"}}
        """)
        #expect(ls[0].health?.state == "ok" && ls[0].health?.kind == nil && ls[0].health?.checkedAt != nil)
        #expect(ls[0].block == nil)
        let h = try #require(ls[1].health)
        #expect(h.state == LauncherHealth.blockedState && h.kind == LauncherHealth.notInTerminalKind)
        #expect(h.checkedAt == JSON.parseDate("2026-11-09T08:01:00Z"))
        #expect(ls[1].baseURL == "https://gateway.example.com")
        #expect(ls[2].health?.state == "failing")
    }

    @Test func aHealthRecordItCannotReadKeepsTheLauncher() throws {
        // A newer or broken record (no state, another shape) drops the record, never the launcher or the list.
        let ls = try launchers(#"{"launchers":[{"name":"a","health":{"kind":"auth"}},{"name":"b","health":"blocked"},{"name":"c","health":null}]}"#)
        #expect(ls.map(\.name) == ["a", "b", "c"])
        #expect(ls.allSatisfy { $0.health == nil })
    }

    @Test func aCodexThatStartsWithoutAFirstMessage() throws {
        // #4961: a computer that opens Codex with no first message says so; an older one doesn't, and needs one.
        let ls = try launchers(#"{"launchers":[{"name":"claude"},{"name":"codex","agent":"codex","prompt_optional":true},{"name":"old","agent":"codex"}]}"#)
        #expect(ls.map(\.promptOptional) == [nil, true, nil])
        #expect(ls.map(\.needsFirstMessage) == [false, false, true])
    }

    @Test func throughTheAPIClient() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/box/launchers",
               json: #"{"launchers":[{"name":"work_gw","health":{"state":"blocked","kind":"not_in_terminal","checked_at":"2026-11-09T08:01:00Z"}}]}"#)
        let r = try await hub.client.launcherList(machine: "box")
        #expect(r.launchers.first?.block?.notInTerminal == true)
        #expect(r.default == nil)
    }

    // MARK: Grayed out, with why

    @Test func onlyABlockedRecordGraysItOut() {
        #expect(launcher("a").block == nil)
        #expect(launcher("a", state: "ok").block == nil)
        // The hub is still retrying or repairing these: offered as before.
        #expect(launcher("a", state: "failing", kind: "timeout").block == nil)
        #expect(launcher("a", state: "repairing", kind: "auth").block == nil)
        #expect(launcher("a", state: "blocked", kind: "auth").block != nil)
    }

    @Test func notInTheTerminalSaysSo() throws {
        let b = try #require(launcher("work_gw", state: "blocked", kind: "not_in_terminal", error: "  bash: work_gw: command not found \n").block)
        #expect(b.notInTerminal)
        #expect(b.reason == String(localized: "Not available in this computer's terminal"))
        #expect(b.reason == "这台电脑的终端里用不了") // the tests run in zh-Hans
        #expect(b.title == String(localized: "Not Available in the Terminal"))
        #expect(b.detail == "bash: work_gw: command not found")
    }

    @Test func otherBlockedKindsSayTheirOwnReason() throws {
        let auth = try #require(launcher("a", state: "blocked", kind: "auth", error: "401").block)
        let route = try #require(launcher("a", state: "blocked", kind: "model_route").block)
        let old = try #require(launcher("a", state: "blocked", kind: "cli_outdated").block)
        let slow = try #require(launcher("a", state: "blocked", kind: "timeout").block)
        let other = try #require(launcher("a", state: "blocked", kind: "other", error: "   ").block)
        let unknown = try #require(launcher("a", state: "blocked", kind: "from_a_newer_hub").block)
        let none = try #require(launcher("a", state: "blocked").block)
        let reasons = [auth, route, old, slow, other].map(\.reason)
        #expect(Set(reasons).count == reasons.count)
        #expect(!reasons.contains(String(localized: "Not available in this computer's terminal")))
        #expect(unknown.reason == other.reason && none.reason == other.reason && none.kind == "")
        let terminal = [auth, route, old, slow, other, unknown].filter { $0.notInTerminal }
        #expect(terminal.isEmpty)
        #expect(auth.title == String(localized: "Unavailable on This Computer"))
        #expect(auth.detail == "401" && other.detail == nil && route.detail == nil)
    }

    // MARK: The picker's list and first pick

    @Test func blockedLaunchersStayListed() {
        let ls = [launcher("claude", state: "ok"), launcher("work_gw", state: "blocked", kind: "not_in_terminal"),
                  Launcher(name: "codex", model: "gpt-5.5", agent: "codex")]
        #expect(LauncherChoices.shown(ls, query: "").map(\.name) == ["claude", "work_gw", "codex"])
        #expect(LauncherChoices.shown(ls, query: " work ").map(\.name) == ["work_gw"])
        #expect(LauncherChoices.shown(ls, query: "GPT").map(\.name) == ["codex"])
        #expect(LauncherChoices.shown(ls, query: "nothing").isEmpty)
    }

    @Test func theFirstPickIsOneThatCanStart() {
        let blocked = launcher("work_gw", state: "blocked", kind: "not_in_terminal")
        let ls = [blocked, launcher("claude", state: "ok"), launcher("sonnetcc")]
        #expect(LauncherChoices.initial(saved: "sonnetcc", in: ls) == "sonnetcc")
        // The last one used is blocked here now: the first that is not, not a start that fails.
        #expect(LauncherChoices.initial(saved: "work_gw", in: ls) == "claude")
        #expect(LauncherChoices.initial(saved: "gone", in: ls) == "claude")
        #expect(LauncherChoices.initial(saved: nil, in: ls) == "claude")
        // Every one blocked: still something to start with (the reason is on the row).
        let all = [blocked, launcher("team_gw", state: "blocked", kind: "auth")]
        #expect(LauncherChoices.initial(saved: "team_gw", in: all) == "team_gw")
        #expect(LauncherChoices.initial(saved: nil, in: all) == "work_gw")
        #expect(LauncherChoices.initial(saved: "x", in: []) == "")
    }

    @Test func aPlainMenuSaysWhyUnderASectionPerReason() {
        // Run Agent Here: a blocked one is still a choice, after the ones that can start, under its reason (a menu
        // titles a section, it has no second line for an option): one section per reason, in the order listed.
        let ls = [launcher("work_gw", state: "blocked", kind: "not_in_terminal"), launcher("claude", state: "ok"),
                  launcher("team_gw", state: "blocked", kind: "auth"), launcher("slow", state: "failing", kind: "timeout"),
                  launcher("home_gw", state: "blocked", kind: "not_in_terminal")]
        let groups = LauncherChoices.blockedByReason(ls)
        #expect(groups.map(\.reason) == [String(localized: "Not available in this computer's terminal"), String(localized: "Its key or sign-in was rejected")])
        #expect(groups.map { $0.launchers.map(\.name) } == [["work_gw", "home_gw"], ["team_gw"]])
        #expect(LauncherChoices.blockedByReason([launcher("claude", state: "ok")]).isEmpty)
    }

    // MARK: Read fresh for a start

    /// The pickers of a start read the computer's list again, not what the app cached when another page first asked:
    /// a start that fails blocks a launcher there, one that works clears it, and the sheet must say so the next time.
    @Test func aStartReadsTheHealthAgainNotTheCache() async throws {
        let hub = FakeHub()
        let path = "/api/v1/machines/box/launchers"
        let fine = #"{"launchers":[{"name":"work_gw","health":{"state":"ok"}},{"name":"claude"}]}"#
        let blocked = #"{"launchers":[{"name":"work_gw","health":{"state":"blocked","kind":"not_in_terminal"}},{"name":"claude"}]}"#
        hub.on("GET", path, json: fine)
        let model = AppModel()
        model.apiForTests = hub.client
        let ref = MachineRef(id: "box")
        // Another page (the assistant's launcher menu) filled the cache first.
        #expect(try await model.launchers(ref).allSatisfy { $0.block == nil })

        // A start with work_gw failed: the hub blocked it. New Agent opens again on the same computer.
        hub.on("GET", path, json: blocked)
        let now = try await model.launchersForStart(ref)
        #expect(now.first { $0.name == "work_gw" }?.block?.notInTerminal == true)
        #expect(LauncherChoices.initial(saved: "work_gw", in: now) == "claude")
        // The cache has it too, for the pages that read it.
        #expect(try await model.launchers(ref).first { $0.name == "work_gw" }?.block != nil)

        // Fixed, Try Anyway worked and the hub cleared it: the next sheet offers it again.
        hub.on("GET", path, json: fine)
        let cleared = try await model.launchersForStart(ref)
        #expect(cleared.allSatisfy { $0.block == nil })
        #expect(LauncherChoices.initial(saved: "work_gw", in: cleared) == "work_gw")
        #expect(hub.sent("GET", path).count == 3)
    }

    // MARK: A failed start's sentence

    @Test func aCodedStartFailureKeepsItsCode() throws {
        let coded = try JSON.decoder.decode(Session.self, from: Data("""
        {"machine_id":"box","session_id":"s1","status":"offline","start_error":"The shell of new terminal tabs on Box (bash) has no command “work_gw”.",
         "start_error_code":"launcher_missing","start_error_args":{"launcher":"work_gw","where":"terminal","pane_shell":"bash"}}
        """.utf8))
        #expect(coded.startErrorCode == "launcher_missing")
        #expect(coded.startErrorArgs?["where"] == "terminal" && coded.startErrorArgs?["launcher"] == "work_gw")
        let plain = try JSON.decoder.decode(Session.self, from: Data(#"{"machine_id":"box","session_id":"s2","status":"offline","start_error":"rpc: connection closed"}"#.utf8))
        #expect(plain.startError == "rpc: connection closed" && plain.startErrorCode == nil && plain.startErrorArgs == nil)
        // An odd args value drops only itself.
        let odd = try JSON.decoder.decode(Session.self, from: Data(#"{"session_id":"s3","start_error":"x","start_error_code":"launcher_missing","start_error_args":["a"]}"#.utf8))
        #expect(odd.startErrorCode == "launcher_missing" && odd.startErrorArgs == nil)
    }
}
