import Foundation
import Testing
import UIKit
@testable import Persona

struct AssistantTopBarTests {
    /// The chat chip's launcher submenus (#1644): the IDs typed before come marked, and "Other Model…" is offered for
    /// Claude Code and Codex launchers only.
    @MainActor @Test func theChipKnowsTypedModels() throws {
        let options = try JSON.decoder.decode([AgentModelOption].self, from: Data(#"""
            [{"id":"default","name":"Default","default":true},{"id":"opus","name":"Opus 5.5"},
             {"id":"vendor/model-x","name":"vendor/model-x","custom":true}]
            """#.utf8))
        #expect(options.map { $0.custom == true } == [false, false, true])
        #expect(AssistantTopBar.takesTypedModel(Launcher(name: "claude")))
        #expect(AssistantTopBar.takesTypedModel(Launcher(name: "cx", agent: "codex")))
        #expect(!AssistantTopBar.takesTypedModel(Launcher(name: "traex", baseURL: "(traex)")))
        #expect(!AssistantTopBar.takesTypedModel(Launcher(name: "rt", agent: "dsh")))
    }

    /// "Other Model…" in the chip only where the hub keeps a typed pick for the next message (`typed_models`): an older
    /// hub dropped it without a word.
    @MainActor @Test func theChipOffersOtherModelOnlyWhereTheHubKeepsIt() {
        let claude = Launcher(name: "claude")
        #expect(AssistantTopBar.offersTypedModel(claude, features: [ModelID.feature, "ultracode"]))
        #expect(!AssistantTopBar.offersTypedModel(claude, features: ["ultracode"]))
        #expect(!AssistantTopBar.offersTypedModel(claude, features: []))
        #expect(!AssistantTopBar.offersTypedModel(Launcher(name: "rt", agent: "dsh"), features: [ModelID.feature]))
        #expect(ModelID.feature == "typed_models")
    }

    @Test func copyInfoHasWhatAnotherAgentNeeds() {
        let t = AssistantCopyText.info(title: "早报", isMain: false, sessionID: "abc-123", machine: "Server", machineID: "server",
                                       cwd: "/home/me/.persona/assistant", launcher: "claude", model: "Opus")
        #expect(t == """
        Persona 助手的对话 「早报」
        会话 ID：abc-123
        所在机器：Server（server）
        工作目录：/home/me/.persona/assistant
        启动器：claude · 模型：Opus
        在那台机器上接着聊：cd /home/me/.persona/assistant && claude --resume abc-123
        """)
        // The new-conversation page: no session lines.
        let home = AssistantCopyText.info(title: nil, isMain: false, sessionID: nil, machine: nil, machineID: "server",
                                          cwd: "~/.persona/assistant", launcher: "claude", model: nil)
        #expect(home == "Persona 助手（hub 上的 Claude）\n所在机器：server\n工作目录：~/.persona/assistant\n启动器：claude")
    }

    @Test func dragSettles() {
        #expect(AssistantDrawer.settlesOpen(progress: 0.6, velocity: 0, width: 300))
        #expect(!AssistantDrawer.settlesOpen(progress: 0.4, velocity: 0, width: 300))
        #expect(AssistantDrawer.settlesOpen(progress: 0.2, velocity: 900, width: 300)) // flicked open
        #expect(!AssistantDrawer.settlesOpen(progress: 0.9, velocity: -900, width: 300)) // flicked shut
        #expect(AssistantDrawer.settlesOpen(progress: 0.4, velocity: 400, width: 300)) // still moving towards open
        #expect(AssistantDrawer.settlesOpen(progress: 0.22, velocity: 495, width: 360)) // a short quick flick (the simulator's)
        #expect(!AssistantDrawer.settlesOpen(progress: 0.25, velocity: 212, width: 360)) // half way, let go slowly: back
    }

    /// The sidebar waits underneath the page: only a small vertical lift, never a horizontal slide, zoom or fade.
    @Test @MainActor func sidebarStaysInPlaceWhileThePageSlidesOff() {
        let drawer = AssistantDrawer()
        let sidebar = UIView(frame: CGRect(x: 0, y: 0, width: 300, height: 800))
        drawer.sidebarView = sidebar
        for width in [CGFloat(250), 300, 380] {
            drawer.width = width
            for progress in [CGFloat(0.01), 0.25, 0.5, 0.75, 1] {
                drawer.progress = progress
                #expect(sidebar.transform.tx == 0)
                #expect(sidebar.transform.ty == (1 - progress) * Theme.Motion.drawerLift)
                #expect(sidebar.transform.a == 1 && sidebar.transform.d == 1)
                #expect(sidebar.transform.b == 0 && sidebar.transform.c == 0)
                #expect(sidebar.alpha == 1)
                #expect(!sidebar.isHidden && !sidebar.accessibilityElementsHidden)
            }
        }
        #expect(sidebar.transform == .identity)
    }

    /// Under iPhone Duo's side bars the page stays and the sidebar slides in over it from the leading edge, under the
    /// finger, without the lift (#3755); closed it is hidden as anywhere else. Back under horizontal bars it lifts again.
    @Test @MainActor func sidebarSlidesOverAPageThatStaysUnderSideBars() {
        let drawer = AssistantDrawer()
        let sidebar = UIView(frame: CGRect(x: 0, y: 0, width: 300, height: 800))
        drawer.sidebarView = sidebar
        drawer.width = 300
        drawer.inPlace = true
        for progress in [CGFloat(0.01), 0.25, 0.5, 0.75, 1] {
            drawer.progress = progress
            #expect(sidebar.transform.tx == -(1 - progress) * 300)
            #expect(sidebar.transform.ty == 0)
            #expect(sidebar.transform.a == 1 && sidebar.transform.d == 1)
            #expect(sidebar.alpha == 1 && !sidebar.isHidden && !sidebar.accessibilityElementsHidden)
        }
        drawer.progress = 0.5
        drawer.width = 250
        #expect(sidebar.transform.tx == -125)
        drawer.progress = 0
        #expect(sidebar.isHidden && sidebar.accessibilityElementsHidden)
        drawer.inPlace = false
        drawer.progress = 0.5
        #expect(sidebar.transform.tx == 0)
        #expect(sidebar.transform.ty == Theme.Motion.drawerLift / 2)
    }

    /// Dragging back retraces the same lift and completely removes the closed sidebar from VoiceOver.
    @Test @MainActor func closingSidebarRetracesItsLiftAndHidesIt() {
        let drawer = AssistantDrawer()
        let sidebar = UIView()
        drawer.sidebarView = sidebar
        drawer.progress = 1
        drawer.dragBegan()
        drawer.drag(-drawer.width / 2)
        #expect(drawer.progress == 0.5)
        #expect(sidebar.transform.tx == 0)
        #expect(sidebar.transform.ty == Theme.Motion.drawerLift / 2)
        #expect(sidebar.alpha == 1 && !sidebar.isHidden)
        drawer.drag(-drawer.width)
        #expect(drawer.progress == 0 && !drawer.isOut)
        #expect(sidebar.isHidden && sidebar.accessibilityElementsHidden)
        drawer.dragBegan()
        drawer.drag(drawer.width / 2)
        #expect(sidebar.transform.ty == Theme.Motion.drawerLift / 2)
        #expect(!sidebar.isHidden && !sidebar.accessibilityElementsHidden)
    }

    /// The drawer takes a touch within 45° of horizontal — a thumb's slant — and while closed only to the right.
    @Test func dragDirection() {
        #expect(AssistantDrawer.takes(dx: 11, dy: 6, isOpen: false)) // 30°: the drag that did nothing before
        #expect(AssistantDrawer.takes(dx: 10, dy: -8, isOpen: false)) // 40° upwards
        #expect(!AssistantDrawer.takes(dx: 6, dy: 10, isOpen: false)) // steeper: the list scrolls
        #expect(!AssistantDrawer.takes(dx: -12, dy: 2, isOpen: false)) // to the left while closed
        #expect(AssistantDrawer.takes(dx: -12, dy: 2, isOpen: true)) // closing
    }

    @Test func searchMatchesTitleOrLastMessage() throws {
        func s(_ id: String, _ title: String, _ last: String?) throws -> Session {
            var o: [String: Any] = ["session_id": id, "status": "idle", "title": title, "assistant": true]
            if let last { o["last_text"] = last }
            return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
        }
        let all = [try s("1", "早报 · 周三", "两个会议"), try s("2", "Weekly Report", nil), try s("3", "吃饭", "已经加到日历")]
        #expect(AssistantSidebarSearch.filter(all, "日历").map(\.sessionID) == ["3"])
        #expect(AssistantSidebarSearch.filter(all, "ｗｅｅｋｌｙ").map(\.sessionID) == ["2"]) // width and case
        #expect(AssistantSidebarSearch.filter(all, "Growth", names: ["3": "Growth"]).map(\.sessionID) == ["3"]) // a direction by its name
        #expect(AssistantSidebarSearch.filter(all, "  ").count == 3)
    }

    /// The sidebar's days: Today / Yesterday / Previous 7 Days / Earlier counted from midnight, newest first, no time goes to Earlier.
    @Test func conversationsGroupByDay() throws {
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "Asia/Shanghai")!
        let now = cal.date(from: DateComponents(year: 2026, month: 10, day: 30, hour: 9))!
        func s(_ id: String, hoursAgo: Double?) throws -> Session {
            let o: [String: Any] = ["session_id": id, "status": "idle", "title": id, "assistant": true]
            var x = try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
            x.updatedAt = hoursAgo.map { now.addingTimeInterval(-$0 * 3600) }
            return x
        }
        // 8 h ago is today (01:00); 10 h ago is yesterday (23:00); 3 days ago; 30 days ago; never.
        let groups = AssistantSidebarGroups.group([try s("old", hoursAgo: 720), try s("today", hoursAgo: 8), try s("none", hoursAgo: nil),
                                                   try s("yesterday", hoursAgo: 10), try s("week", hoursAgo: 72),
                                                   try s("today2", hoursAgo: 1)], now: now, calendar: cal)
        #expect(groups.map(\.title) == ["今天", "昨天", "最近 7 天", "更早"])
        #expect(groups.map { $0.sessions.map(\.sessionID) } == [["today2", "today"], ["yesterday"], ["week"], ["old", "none"]])
    }

    /// The sidebar's Directions: the leader conversations in the hub's order, never the main one, none the list lacks.
    @Test func directionsFollowTheHubsOrder() throws {
        func s(_ id: String) throws -> Session {
            let o: [String: Any] = ["session_id": id, "machine_id": "hub", "status": "idle", "title": "1110 | explore | \(id)", "assistant": true]
            return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
        }
        let all = [try s("main"), try s("mine"), try s("lead-b"), try s("lead-a")]
        let main = all[0].key
        let picked = AssistantSidebarGroups.directions(all, leaders: ["lead-a", "gone", "main", "lead-b"], main: main)
        #expect(picked.map(\.sessionID) == ["lead-a", "lead-b"])
        #expect(AssistantSidebarGroups.directions(all, leaders: [], main: main).isEmpty)
    }

    /// The sidebar's Recent (#4873): the newest five of the user's own chats, never the main one or a direction (listed
    /// above it) and never one Persona opened by itself; nothing when no chat is left.
    @Test func recentIsTheNewestFiveOfTheRest() throws {
        let now = Date(timeIntervalSince1970: 1_800_000_000)
        func s(_ id: String, hoursAgo: Double?, origin: String? = nil) throws -> Session {
            var o: [String: Any] = ["session_id": id, "machine_id": "hub", "status": "idle", "title": id, "assistant": true]
            if let origin { o["origin"] = origin }
            var x = try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: o))
            x.updatedAt = hoursAgo.map { now.addingTimeInterval(-$0 * 3600) }
            return x
        }
        let all = [try s("main", hoursAgo: 0), try s("lead", hoursAgo: 0.5), try s("inbox", hoursAgo: 0.2, origin: "inbox"),
                   try s("task", hoursAgo: 0.3), try s("a", hoursAgo: 1), try s("b", hoursAgo: 2), try s("wx", hoursAgo: 3, origin: "channel"),
                   try s("d", hoursAgo: 4), try s("e2", hoursAgo: 5), try s("e1", hoursAgo: 5), try s("never", hoursAgo: nil)]
        let main = all[0].key
        let tasks = AssistantTaskChats(tasks: [], runs: [APIClient.ScheduleRun(id: "1", kind: "routine", refID: "t", title: "", due: now, at: now,
                                                                               status: "ok", machineID: "hub", sessionID: "task")])
        let recent = AssistantSidebarGroups.recent(all, leaders: ["lead"], main: main, tasks: tasks, hubSays: true)
        #expect(recent.map(\.sessionID) == ["a", "b", "wx", "d", "e1"]) // equal times: by session id
        // An ended direction comes back among the rest; a hub that never says who opened them leaves only the tasks' out.
        #expect(AssistantSidebarGroups.recent(all, leaders: [], main: main, tasks: tasks, hubSays: true).map(\.sessionID).first == "lead")
        let older = all.map { var x = $0; x.origin = nil; return x }
        #expect(AssistantSidebarGroups.recent(older, leaders: ["lead"], main: main, tasks: tasks, hubSays: false).map(\.sessionID)
            == ["inbox", "a", "b", "wx", "d"])
        #expect(AssistantSidebarGroups.recent(Array(all.prefix(2)), leaders: ["lead"], main: main, tasks: tasks, hubSays: true).isEmpty)
    }

    /// A direction's name comes from GET /api/v1/assistant/leaders: current ones with a name only.
    @Test func directionNamesSkipEndedAndBlank() {
        let names = APIClient.directionNames([
            .init(id: "a", name: "Product", ended: nil),
            .init(id: "b", name: "  ", ended: nil),
            .init(id: "c", name: "Old", ended: true),
            .init(id: "", name: "Nobody", ended: nil),
        ])
        #expect(names == ["a": "Product"])
    }

    /// A drag to the right on something that scrolls sideways and is not at its left edge scrolls it, not the sidebar.
    @Test @MainActor func sidewaysScrollersGoFirst() {
        let root = UIView(frame: CGRect(x: 0, y: 0, width: 400, height: 800))
        let code = UIScrollView(frame: CGRect(x: 0, y: 100, width: 400, height: 100))
        code.contentSize = CGSize(width: 1200, height: 100)
        let line = UIView(frame: CGRect(x: 0, y: 0, width: 1200, height: 100))
        code.addSubview(line)
        root.addSubview(code)
        let list = UIScrollView(frame: CGRect(x: 0, y: 300, width: 400, height: 400)) // scrolls only up and down
        list.contentSize = CGSize(width: 400, height: 4000)
        root.addSubview(list)
        let here = CGPoint(x: 200, y: 150)
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: false)) // at its left edge: the sidebar may open
        code.contentOffset.x = 300
        #expect(DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: false))
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: CGPoint(x: 200, y: 500), in: root, towardsRight: false))
        // A drag to the left (the shell's page to Agents) scrolls it on while it has more to the right, and pages at its end.
        #expect(DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: true))
        code.contentOffset.x = 800
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: true))
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: CGPoint(x: 200, y: 500), in: root, towardsRight: true))
    }

    /// On an iPad the chat's scroll views run on under the sidebar, inset by it on their left: a table wider than what
    /// shows of it scrolls, though not wider than its bounds (#5737).
    @Test @MainActor func aSidewaysScrollerInsetUnderTheSidebar() {
        let root = UIView(frame: CGRect(x: 0, y: 0, width: 820, height: 800))
        let table = UIScrollView(frame: CGRect(x: 32, y: 100, width: 788, height: 100))
        table.contentInset.left = 372
        table.contentSize = CGSize(width: 751, height: 100)
        table.contentOffset.x = -372
        table.addSubview(UIView(frame: CGRect(x: 0, y: 0, width: 751, height: 100)))
        root.addSubview(table)
        let here = CGPoint(x: 615, y: 150)
        #expect(DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: true))
        table.contentOffset.x = 751 - 788 // at its right end
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: true))
        table.contentSize.width = 300 // fits in what shows: nothing to scroll
        table.contentOffset.x = -372
        #expect(!DrawerPan.Coordinator.sidewaysScroller(at: here, in: root, towardsRight: true))
    }

    /// The shell's strip: on Assistant a drag to the left pages (to the right is the drawer's), on Agents a drag to the right.
    @Test @MainActor func stripDirection() {
        let pager = ShellPager()
        #expect(DrawerPan.Coordinator.pages(pager: pager, dx: -20))
        #expect(!DrawerPan.Coordinator.pages(pager: pager, dx: 20))
        pager.slide(to: 1, animated: false)
        #expect(DrawerPan.Coordinator.pages(pager: pager, dx: 20))
        #expect(!DrawerPan.Coordinator.pages(pager: pager, dx: -20))
    }

    /// The pager lets go like the drawer: a flick goes its way, otherwise past half way.
    @Test @MainActor func pagerSettles() {
        let pager = ShellPager()
        pager.width = 400
        var landed: [Int] = []
        pager.settled = { landed.append($0) }
        pager.dragBegan()
        pager.drag(-120) // 30% towards Agents, slowly: back
        pager.letGo(velocity: -50)
        #expect(landed.isEmpty)
        pager.dragBegan()
        pager.drag(-120) // the same, flicked: over
        pager.letGo(velocity: -800)
        #expect(landed == [1])
        pager.slide(to: 1, animated: false)
        pager.dragBegan()
        pager.drag(240) // 60% back towards Assistant
        pager.letGo(velocity: 0)
        #expect(landed == [1, 0])
    }
}
