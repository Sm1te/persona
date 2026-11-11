import Foundation
@testable import Persona
import Testing

/// The iPhone preview's logic (#150, docs/web/preview.md "iPhone"): the servers it offers, the command Start shows, the
/// address and `Page:` of a forwarded page, and a picked element as the same element comment the web writes (the cases
/// of web/src/lib/preview.test.ts).
struct PreviewLogicTests {
    private static let res = PreviewServers(
        file: "/home/me/proj/.claude/launch.json",
        servers: [
            .init(name: "web", port: 5173, command: ["npm", "run", "dev"], cwd: "/home/me/proj", listening: true, title: "Acme"),
            .init(name: "api", port: 8080, command: ["go", "run", "."], env: ["PORT": "8080", "MODE": "a b"], listening: false),
            .init(name: "docs", port: 4000, url: "http://localhost:4000/guide?x=1#top", listening: false),
            .init(name: "staging", listening: false, problem: "not_local"),
        ])

    @Test func listsTheUsableEntriesThenTheAgentsOtherPorts() {
        let got = PreviewLogic.options(Self.res, ports: [ListeningPort(port: 8080), ListeningPort(port: 3000, process: "node", title: "Storybook")])
        #expect(got.map(\.id) == ["launch:web", "launch:api", "launch:docs", "port:3000"])
        #expect(got.map(\.port) == [5173, 8080, 4000, 3000])
        #expect(got.map(\.path) == ["/", "/", "/guide?x=1#top", "/"])
        // an agent listens on 8080: running, even if the probe missed it
        #expect(got.map(\.listening) == [true, true, false, true])
        #expect(got.map(\.from) == [.launch, .launch, .launch, .agent])
        #expect(got[3].name == "Storybook")
        #expect(got[3].process == "node")
        #expect(got[3].link == PortLink(port: 3000, path: "/", secure: false))
        #expect(PreviewLogic.problems(Self.res).map(\.name) == ["staging"])
        #expect(PreviewLogic.options(nil, ports: []).isEmpty)
        #expect(PreviewLogic.options(nil, ports: [ListeningPort(port: 9000)]).map(\.name) == [":9000"])
    }

    @Test func showsTheCommandAsTheNodeRunsIt() {
        let api = PreviewLogic.options(Self.res, ports: [])[1]
        #expect(PreviewLogic.commandLine(api) == "env 'MODE=a b' 'PORT=8080' go run .")
        let o = { (c: [String]) in PreviewOption(name: "x", port: 1, path: "/", listening: false, command: c, from: .launch) }
        #expect(PreviewLogic.commandLine(o(["npm", "run", "dev; rm -rf ~"])) == "npm run 'dev; rm -rf ~'")
        #expect(PreviewLogic.commandLine(o(["sh", "-c", "it's"])) == #"sh -c 'it'\''s'"#)
    }

    @Test func namesTheFolderAStartRunsIn() {
        #expect(PreviewLogic.folder("/home/me/proj/server", session: "/home/me/proj", home: "/home/me") == "server")
        #expect(PreviewLogic.folder("/home/me/proj", session: "/home/me/proj/", home: "/home/me") == ".")
        #expect(PreviewLogic.folder("/home/me/other", session: "/home/me/proj", home: "/home/me") == "~/other")
        #expect(PreviewLogic.folder(nil, session: "/home/me/proj", home: nil) == nil)
    }

    @Test func thePageIsTheComputersAddress() {
        #expect(PreviewLogic.machinePage("http://localhost:52011/settings?tab=1#a", machinePort: 5173) == "http://localhost:5173/settings?tab=1#a")
        #expect(PreviewLogic.machinePage("http://127.0.0.1:5173/", machinePort: 5173) == "http://localhost:5173/")
        // not a forwarded page, or no port known: as it is
        #expect(PreviewLogic.machinePage("https://example.com/a", machinePort: 5173) == "https://example.com/a")
        #expect(PreviewLogic.machinePage("http://localhost:52011/", machinePort: nil) == "http://localhost:52011/")
    }

    @Test func theAddressIsThePathOnItsServer() {
        #expect(PreviewLogic.address(URL(string: "http://localhost:5173/settings?tab=1#a"), localPort: 5173) == "/settings?tab=1#a")
        #expect(PreviewLogic.address(URL(string: "http://127.0.0.1:5173"), localPort: 5173) == "/")
        #expect(PreviewLogic.address(URL(string: "http://localhost:3000/x"), localPort: 5173) == "http://localhost:3000/x")
        #expect(PreviewLogic.address(nil, localPort: 5173) == "")
    }

    private static let el = PickedElement(
        selector: "main > form > button.save", tag: "button", text: "  Save   changes ",
        html: #"<button class="save" type="submit"><span class="icon"></span><span>Save changes</span></button>"#,
        component: "SaveButton", source: .init(file: "/home/me/proj/src/components/SaveButton.tsx", line: 12),
        url: "http://localhost:52011/settings")

    @Test func aPickedElementIsTheWebsElementComment() {
        let c = PreviewLogic.comment(Self.el, page: "http://localhost:5173/settings", cwd: "/home/me/proj", note: "Make it primary")
        #expect(c.element == MessageComment.Element(url: "http://localhost:5173/settings", selector: "main > form > button.save", component: "SaveButton",
                                                    source: "src/components/SaveButton.tsx:12", text: "Save   changes", omitted: 0))
        #expect(MessageComments.block(c, number: 2) == [
            "Element comment 2",
            "Page: http://localhost:5173/settings",
            "Element: main > form > button.save",
            "Component: SaveButton (src/components/SaveButton.tsx:12)",
            "Text: Save changes",
            "```html",
            #"<button class="save" type="submit"><span class="icon"></span><span>Save changes</span></button>"#,
            "```",
            "",
            "Make it primary",
        ].joined(separator: "\n"))
        #expect(Self.el.label == "button.save")
    }

    @Test func cutsLongHTMLAndSaysSo() {
        var long = Self.el
        long.html = (0..<80).map { "<li>\($0)</li>" }.joined(separator: "\n")
        long.component = ""
        long.source = nil
        let c = PreviewLogic.comment(long, page: long.url, cwd: "/home/me/proj", note: "")
        #expect(c.quoted.components(separatedBy: "\n").count == 60)
        #expect(c.element?.omitted == 20)
        let block = MessageComments.block(c, number: 1)
        #expect(block.contains("Truncated: the first 60 of 80 lines"))
        #expect(!block.contains("Component:"))
    }

    @Test func longTextAndOneLineHTML() {
        var el = Self.el
        el.text = String(repeating: "a", count: 130)
        #expect(PreviewLogic.comment(el, page: el.url, cwd: "", note: "").element?.text == String(repeating: "a", count: 119) + "…")
        #expect(PreviewLogic.prettyHTML("<a>x</a>") == "<a>x</a>")
        #expect(PreviewLogic.prettyHTML("<div>" + String(repeating: "<p>x</p>", count: 20) + "</div>").components(separatedBy: "\n").count == 22)
        #expect(PreviewLogic.relativeSource("webpack:///./src/App.tsx", cwd: "/p") == "src/App.tsx")
        #expect(PreviewLogic.relativeSource("file:///p/src/App.vue", cwd: "/p/") == "src/App.vue")
    }

    @Test func readsWhatThePickScriptSends() throws {
        let json = #"{"selector":"main > button.save","tag":"button","text":"Save","html":"<button>Save</button>","role":"","label":"","component":"","source":null,"rect":{"x":1,"y":2,"w":120,"h":37},"url":"http://localhost:5173/","title":"Acme"}"#
        let el = try JSONDecoder().decode(PickedElement.self, from: Data(json.utf8))
        #expect(el.source == nil)
        #expect(el.rect?.w == 120)
        #expect(el.title == "Acme")
    }
}

/// Reading the servers and starting one (`PreviewModel`) against a hub in memory.
@MainActor
struct PreviewModelTests {
    private static let servers = #"{"servers":[{"name":"web","port":5173,"listening":true,"title":"Acme"},{"name":"api","port":8080,"command":["go","run","."],"listening":false}]}"#

    private final class Clock {
        var now = Date(timeIntervalSince1970: 1_000)
    }

    private func model(_ hub: FakeHub, clock: Clock = Clock(), pause: @escaping @MainActor (Duration) async throws -> Void = { _ in }) -> PreviewModel {
        PreviewModel(machine: "mac", cwd: "/home/me/proj", deps: .init(api: { hub.client }, now: { clock.now }, pause: pause))
    }

    @Test func readsTheServersOfTheSessionsFolder() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers", json: Self.servers)
        let m = model(hub)
        #expect(m.phase == .loading)
        await m.load()
        #expect(m.phase == .choose)
        #expect(m.options(ports: []).map(\.name) == ["web", "api"])
        #expect(hub.sent("GET", "/api/v1/machines/mac/preview/servers").first?.query["cwd"] == "/home/me/proj")
    }

    @Test func anOldComputerSaysSo() async {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers") { _ in
            throw APIError(status: 409, message: "update Persona on this computer", fields: ["code": "node_outdated"])
        }
        let m = model(hub)
        await m.load()
        #expect(m.phase == .old)
    }

    @Test func startsAnEntryAndOpensItOnceItsPortAnswers() async throws {
        let hub = FakeHub()
        var looks = 0
        hub.on("GET", "/api/v1/machines/mac/preview/servers") { _ in
            looks += 1
            return Data((looks < 3 ? Self.servers : Self.servers.replacingOccurrences(of: #""listening":false"#, with: #""listening":true"#)).utf8)
        }
        hub.on("POST", "/api/v1/machines/mac/preview/start", json: #"{"pane":"w1:p9","terminal":"pane:w1:p9","port":8080}"#)
        let m = model(hub)
        await m.load()
        let api = try #require(m.options(ports: []).first { $0.name == "api" })
        let result = await m.start(api, workspace: "w1", kind: "herdr") { [] }
        guard case .ready(let o) = result else { Issue.record("not ready: \(result)"); return }
        #expect(o.port == 8080 && o.listening)
        #expect(m.starting == nil)
        let body = try JSONSerialization.jsonObject(with: try #require(hub.sent("POST", "/api/v1/machines/mac/preview/start").first?.body)) as? [String: String]
        #expect(body == ["cwd": "/home/me/proj", "name": "api", "workspace": "w1", "kind": "herdr"])
    }

    @Test func givesUpAfterNinetySeconds() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers", json: Self.servers)
        hub.on("POST", "/api/v1/machines/mac/preview/start", json: #"{"port":8080}"#)
        let clock = Clock()
        let m = model(hub, clock: clock) { d in clock.now += Double(d.components.seconds) + Double(d.components.attoseconds) / 1e18 }
        await m.load()
        let api = try #require(m.options(ports: []).first { $0.name == "api" })
        let result = await m.start(api, workspace: "w1", kind: "herdr") { [] }
        #expect(result == .slow(api))
        #expect(m.starting == nil)
        // a look every 1.5 s for 90 s, not more
        #expect((60...62).contains(hub.sent("GET", "/api/v1/machines/mac/preview/servers").count - 1))
    }

    @Test func anAgentsPortCountsAsAnswering() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers", json: Self.servers)
        hub.on("POST", "/api/v1/machines/mac/preview/start", json: #"{"port":8080}"#)
        let m = model(hub)
        await m.load()
        let api = try #require(m.options(ports: []).first { $0.name == "api" })
        let result = await m.start(api, workspace: "w1", kind: "herdr") { [ListeningPort(port: 8080)] }
        #expect(result == .ready(PreviewLogic.options(m.servers, ports: [ListeningPort(port: 8080)])[1]))
    }

    @Test func cancelStopsWaiting() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers", json: Self.servers)
        hub.on("POST", "/api/v1/machines/mac/preview/start", json: #"{"port":8080}"#)
        final class Box { var model: PreviewModel? }
        let box = Box()
        let m = model(hub) { _ in box.model?.cancelStart() }
        box.model = m
        await m.load()
        let api = try #require(m.options(ports: []).first { $0.name == "api" })
        #expect(await m.start(api, workspace: "w1", kind: "herdr") { [] } == .stopped)
        #expect(m.starting == nil)
    }

    @Test func aRefusedStartSaysWhy() async throws {
        let hub = FakeHub()
        hub.on("GET", "/api/v1/machines/mac/preview/servers", json: Self.servers)
        hub.on("POST", "/api/v1/machines/mac/preview/start") { _ in throw APIError(status: 403, message: "only full devices can start servers") }
        let m = model(hub)
        await m.load()
        let api = try #require(m.options(ports: []).first { $0.name == "api" })
        #expect(await m.start(api, workspace: "w1", kind: "herdr") { [] } == .failed("only full devices can start servers"))
        #expect(m.starting == nil && !m.sending)
    }
}
