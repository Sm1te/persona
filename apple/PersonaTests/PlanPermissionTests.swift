import Testing
@testable import Persona

/// A planned task's permission on its card and in Edit (#3026): Auto, which the hub may settle an unset task to from
/// Claude Code's default mode, has its own name instead of reading as "Ask every step".
@MainActor struct PlanPermissionTests {
    @Test func autoHasItsOwnName() {
        #expect(PlanTaskRow.permissions.contains("auto"))
        #expect(PlanTaskRow.permissionLabel("auto") == "自动判断")
        #expect(PlanTaskRow.permissionLabel("default") == "每步都问")
        #expect(Set(PlanTaskRow.permissions.map(PlanTaskRow.permissionLabel)).count == PlanTaskRow.permissions.count)
    }

    @Test func readOnlyIsARequestNotASandbox() {
        #expect(String(localized: "Read-only") == "已要求只读")
    }
}
