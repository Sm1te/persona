import Foundation
import Testing
@testable import Persona

/// By App (#3756): the agents in a section per app they run in, as the hub sends them.
struct AppGroupsTests {
    private static func session(_ id: String, agent: String, surface: String?, state: String = "idle", updated: TimeInterval) -> Session {
        let surfaceField = surface.map { #","agent_surface":"\#($0)""# } ?? ""
        let date = ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: updated))
        let json = #"{"machine_id":"m","session_id":"\#(id)","title":"\#(id)","cwd":"/w","status":"\#(state)","state":"\#(state)","agent":"\#(agent)","updated_at":"\#(date)"\#(surfaceField)}"#
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func desktopAppsFirstThenOtherWithOfflineAgentsNewestFirst() {
        let sessions = [
            Self.session("term-claude", agent: "claude", surface: "cli", updated: 50),
            Self.session("codex-old", agent: "codex", surface: "desktop", state: "offline", updated: 10),
            Self.session("claude-offline", agent: "claude", surface: "desktop", state: "offline", updated: 40),
            Self.session("codex-new", agent: "codex", surface: "desktop", state: "working", updated: 30),
            Self.session("claude-new", agent: "claude", surface: "desktop", state: "working", updated: 20),
            Self.session("unknown-client", agent: "codex", surface: nil, updated: 60),
            Self.session("trae-app", agent: "trae", surface: "desktop", updated: 5),
            Self.session("amp-app", agent: "amp", surface: "desktop", updated: 6),
        ]
        let groups = AppGroups.build(sessions)
        #expect(groups.map(\.id) == ["app/desktop/claude code", "app/desktop/codex", "app/desktop/amp", "app/desktop/trae", AppGroups.otherID])
        #expect(groups.map { $0.sessions.map(\.sessionID) } == [
            ["claude-offline", "claude-new"], // offline ones stay, the latest first
            ["codex-new", "codex-old"],
            ["amp-app"], // other desktop apps after Claude's and Codex's, by name
            ["trae-app"],
            ["unknown-client", "term-claude"], // no surface reported: not a desktop app's
        ])
        #expect(groups[0].kind?.surface == .desktop && groups[0].kind?.name == "Claude Code")
        #expect(groups[0].title == String(localized: "Claude desktop app") && groups[1].title == String(localized: "Codex App"))
        let trae = "TRAE"
        #expect(groups[3].title == String(localized: "\(trae) desktop app"))
        #expect(groups[4].kind == nil && groups[4].title == String(localized: "Other"))
    }

    private static func placed(_ id: String, agent: String = "claude", cwd: String, updated: TimeInterval, machine: String = "m",
                               extra: [String: Any] = [:]) -> Session {
        var fields: [String: Any] = ["machine_id": machine, "session_id": id, "title": id, "cwd": cwd, "status": "idle", "state": "idle",
                                     "agent": agent, "agent_surface": "desktop",
                                     "updated_at": ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: updated))]
        fields.merge(extra) { _, new in new }
        do { return try JSON.decoder.decode(Session.self, from: JSONSerialization.data(withJSONObject: fields)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    /// As the desktop apps' sidebars: an app's agents by project, the latest project first, each project's agents the
    /// latest first; the repository's main checkout names a worktree's project; a home directory is no project (Other,
    /// last); the same folder on another machine is another project.
    @Test func anAppsAgentsGoByProject() {
        let sessions = [
            Self.placed("gateway-old", cwd: "/Users/me/code/gateway", updated: 10),
            Self.placed("app-new", cwd: "/Users/me/code/app", updated: 50),
            Self.placed("gateway-new", cwd: "/Users/me/code/gateway/", updated: 40),
            Self.placed("app-worktree", cwd: "/Users/me/code/app/.claude/worktrees/fix", updated: 20,
                        extra: ["repo_root": "/Users/me/code/app"]),
            Self.placed("at-home", cwd: "/Users/me", updated: 60),
            Self.placed("no-dir", cwd: "", updated: 5),
            Self.placed("gateway-elsewhere", cwd: "/Users/me/code/gateway", updated: 30, machine: "other-mac"),
        ]
        let app = AppGroups.build(sessions)[0]
        #expect(app.showsProjects)
        #expect(app.projects.map(\.title) == ["app", "gateway", "gateway", String(localized: "Other")])
        #expect(app.projects.map { $0.sessions.map(\.sessionID) } == [
            ["app-new", "app-worktree"], ["gateway-new", "gateway-old"], ["gateway-elsewhere"], ["at-home", "no-dir"],
        ])
        #expect(app.projects.last?.isOther == true && app.projects.last?.id == app.id + "/project/other#")
        #expect(app.projects.first?.machine == sessions[1].machineRef && app.projects.last?.machine == nil)
        #expect(Set(app.projects.map(\.id)).count == app.projects.count)
        #expect(app.sessions.count == sessions.count) // the section counts them all
    }

    /// The Codex App's chats go by the App's projects, with the node's spaces: `codex-project:<root>`. A chat in no project
    /// is in Other, as in the App's sidebar: the App keeps it in <home>/Documents/Codex/<date>/<name>, and the node names
    /// one with no folder `codex-chats:`.
    @Test func codexAppChatsGoByTheAppsProjects() {
        func chat(_ id: String, root: String?, name: String, updated: TimeInterval) -> Session {
            Self.placed(id, agent: "codex", cwd: root ?? "", updated: updated,
                        extra: ["space_kind": "codex_app", "space_id": root.map { "codex-project:" + $0 } ?? "codex-chats:/Users/me/.codex",
                                "space_name": name, "repo_root": root ?? ""])
        }
        let app = AppGroups.build([
            chat("csv", root: "/Users/me/code/billing", name: "billing", updated: 30),
            chat("pages", root: "/Users/me/code/billing", name: "billing", updated: 10),
            chat("scratch", root: "/Users/me/Documents/Codex/2026-11-06/w-i", name: "w-i", updated: 50),
            chat("no-folder", root: nil, name: "Codex chats", updated: 40),
            chat("at-home", root: "/Users/me", name: "me", updated: 20),
        ])[0]
        #expect(app.projects.map(\.title) == ["billing", String(localized: "Other")])
        #expect(app.projects.map { $0.sessions.map(\.sessionID) } == [["csv", "pages"], ["scratch", "no-folder", "at-home"]])
        #expect(AppGroups.isCodexChatFolder("/Users/me/Documents/Codex/2026-10-27/pei-z"))
        #expect(AppGroups.isCodexChatFolder("C:\\Users\\me\\Documents\\Codex\\2026-10-27\\pei-z"))
        #expect(!AppGroups.isCodexChatFolder("/Users/me/Documents/Codex/notes/pei-z"))
        #expect(!AppGroups.isCodexChatFolder("/Users/me/Documents/ChatGPT/botmux"))
    }

    /// The Codex App's projects as its sidebar orders them (the node's `space_index`, from the App's project-order), then
    /// any it does not number by their latest chat, Other last. Another app's agents keep the latest project first,
    /// whatever their space's number (a herdr workspace's).
    @Test func codexAppProjectsGoInTheAppsOrder() {
        func chat(_ id: String, root: String?, place: Int?, updated: TimeInterval) -> Session {
            var extra: [String: Any] = ["space_kind": "codex_app", "space_id": root.map { "codex-project:" + $0 } ?? "codex-chats:/Users/me/.codex",
                                        "space_name": root.map { ($0 as NSString).lastPathComponent } ?? "Codex chats", "repo_root": root ?? ""]
            if let place { extra["space_index"] = place }
            return Self.placed(id, agent: "codex", cwd: root ?? "", updated: updated, extra: extra)
        }
        let codex = AppGroups.build([
            chat("site-chat", root: "/Users/me/code/site", place: 2, updated: 50),
            chat("tools-chat", root: "/Users/me/code/tools", place: 1, updated: 10),
            chat("tools-sub", root: "/Users/me/code/tools", place: 1, updated: 5),
            chat("unlisted-new", root: "/Users/me/.codex/.chatgpt-projects/g-p-1", place: nil, updated: 60),
            chat("unlisted-old", root: "/Users/me/code/old", place: nil, updated: 20),
            chat("loose", root: nil, place: nil, updated: 70),
        ])[0]
        #expect(codex.projects.map(\.title) == ["tools", "site", "g-p-1", "old", String(localized: "Other")])
        #expect(codex.projects.first?.sessions.map(\.sessionID) == ["tools-chat", "tools-sub"])
        let claude = AppGroups.build([
            Self.placed("numbered-old", cwd: "/Users/me/code/a", updated: 10, extra: ["space_kind": "herdr", "space_id": "w1", "space_index": 1]),
            Self.placed("latest", cwd: "/Users/me/code/b", updated: 30),
        ])[0]
        #expect(claude.projects.map(\.title) == ["b", "a"])
    }

    /// A working tree's project is its repository's, from its path when an older node says nothing; when the home
    /// directory is a repository itself, a session's own folder is its project; one hub's folder is not another hub's.
    @Test func projectsFromRepositoriesAndHubs() {
        let tree = Self.placed("tree", cwd: "/Users/me/code/app/.claude/worktrees/fix", updated: 3)
        #expect(AppGroups.project(of: tree)?.name == "app")
        let underHomeRepo = Self.placed("notes", cwd: "/Users/me/notes", updated: 2,
                                        extra: ["repo_root": "/Users/me", "worktree": "/Users/me"])
        #expect(AppGroups.project(of: underHomeRepo)?.name == "notes")
        var otherHub = Self.placed("hub2", cwd: "/Users/me/code/app", updated: 1)
        otherHub.group = "second-hub"
        #expect(AppGroups.project(of: otherHub)?.key != AppGroups.project(of: Self.placed("hub1", cwd: "/Users/me/code/app", updated: 1))?.key)
    }

    /// Other's section (terminals and the like) is not by project: its agents are listed as before.
    @Test func otherIsNotByProject() {
        let other = AppGroups.build([Self.session("t", agent: "claude", surface: "cli", updated: 1)])[0]
        #expect(other.kind == nil && other.projects.isEmpty && !other.showsProjects)
    }

    /// Every agent in Other: no headings, the section lists them as before.
    @Test func noProjectsNoHeadings() {
        let app = AppGroups.build([Self.placed("a", cwd: "/Users/me", updated: 1), Self.placed("b", cwd: "/home/me/", updated: 2)])[0]
        #expect(!app.showsProjects && app.projects.count == 1)
        for home in ["/", "~", "/root", "/Users/me", "/home/me", "C:\\Users\\me", "C:\\"] {
            #expect(AppGroups.isHome(home), "\(home)")
        }
        for project in ["/Users/me/code", "/srv/app", "/home/me/work", "C:\\Users\\me\\code"] {
            #expect(!AppGroups.isHome(project), "\(project)")
        }
    }

    /// The Mac's ↑ / ↓ go over what is shown: an open section's open projects, in their order.
    @MainActor @Test func theMacsRowsFollowTheProjectsAndTheirFolds() throws {
        let groups = AppGroups.build([
            Self.placed("a1", cwd: "/x/a", updated: 30), Self.placed("b1", cwd: "/x/b", updated: 20), Self.placed("a2", cwd: "/x/a", updated: 10),
        ])
        let folded = try #require(groups.first?.projects.dropFirst().first).id
        let rows = AgentListRows(order: AgentListOrder(isPinned: { _ in false }, isMuted: { _ in false }), grouping: .app,
                                 isFolded: { $0 == folded })
        #expect(rows.apps(groups).map(\.sessionID) == ["a1", "a2"])
    }

    @Test func claudeWhenTheAgentIsUnnamedAndCodexByItsSourceKind() {
        let unnamed = Self.session("unnamed", agent: "", surface: "desktop", updated: 2)
        var byKind = Self.session("by-kind", agent: "", surface: "desktop", updated: 1)
        byKind.sourceKind = "codex_history"
        let groups = AppGroups.build([unnamed, byKind])
        #expect(groups.map(\.id) == ["app/desktop/claude code", "app/desktop/codex"])
        #expect(groups.map { $0.sessions.map(\.sessionID) } == [["unnamed"], ["by-kind"]])
    }

    @Test func agentsUpdatedAtTheSameTimeKeepOneOrder() {
        let a = Self.session("a", agent: "claude", surface: "desktop", updated: 7)
        let b = Self.session("b", agent: "claude", surface: "desktop", updated: 7)
        #expect(AppGroups.build([b, a]).first?.sessions.map(\.sessionID) == AppGroups.build([a, b]).first?.sessions.map(\.sessionID))
    }

    @Test func anAppWithNoAgentHasNoSection() {
        #expect(AppGroups.build([]).isEmpty)
        let onlyDesktop = AppGroups.build([Self.session("a", agent: "claude", surface: "desktop", updated: 1)])
        #expect(onlyDesktop.map(\.id) == ["app/desktop/claude code"])
        let onlyOther = AppGroups.build([Self.session("a", agent: "codex", surface: "cli", updated: 1)])
        #expect(onlyOther.map(\.id) == [AppGroups.otherID])
    }

    @MainActor @Test func byAppIsOfferedAfterByMachineAndTheDefaultStays() {
        #expect(DevGrouping.standard == .repo)
        let offered = DevGrouping.offered(supportsTodos: true)
        #expect(offered.firstIndex(of: .app) == offered.firstIndex(of: .machine).map { $0 + 1 })
        #expect(DevGrouping(rawValue: "app") == .app)
        #expect(DevGrouping.app.title == String(localized: "By App") && DevGrouping.app.symbol == Theme.Icon.byApp)
    }
}
