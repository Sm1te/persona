# Area: the Agents page (#4338)

Part of the [Android alignment plan](README.md) (#4194). Status: plan, 2026-11-11. Found by reading both apps' code and by screenshots of both on the same mock hub data (iPhone 17 Pro simulator, Pixel 9 emulator, Glass, light and dark).

The phase documents compare the platforms by feature. This one compares one primary screen as a whole: what the Agents page shows, how it groups and counts, and how its rows look. [Phase 3](phase-3-feature-gaps.md) F7 keeps the missing groupings and the git and worktree actions; everything else about the page is here.

## Requirements

### Goal

The Agents page on Android shows the same sessions as on iOS, in the same groupings with the same shapes and counts, and its rows carry the same content. Each change brings the page in line with `docs/design/DESIGN_SYSTEM.md` sections 3.4 and 4.8 or with the iOS reference; most change what a primary screen shows, so they are large changes (section 1.4).

### Changes

**A1. Needs You counts and shows sessions.** On iOS the section has one row per session that waits for the user, with its requests stacked under the row, and a session that waits in its terminal with no request gets a row too. The header counts sessions, every row shows, muted sessions are included, and the section hides while searching. On Android the section is "Needs your decision": one large card per request, a header that counts requests, the first three cards and then "N more", no row for a session that waits only in its terminal, and muted sessions left out. The Assistant home's "Things that need you now" counts requests the same way. This breaks the 11-10 decision "Needs You counts sessions" (`docs/design/DECISIONS.md`). Requirement: one row per session as on iOS, the same count as the tab badge, on the Agents page and on the Assistant home.

**A2. One line per session in By Repository.** iOS draws location views with their own one-line tree rows: state dot, agent mark, title, muted bell, then a Needs You chip or the time. Android draws its full two- or three-line row inside the tree. Requirement: the one-line tree row in By Repository (section 3.4: "Views grouped by location draw their own tree rows").

**A3. The computer filter shows on the page.** On iOS a chosen computer shows as an "Only <computer>" chip that clears it. On Android, with the shared home chrome, the computer is a checked item in the list settings sheet, the computer chips don't show, and the title that would name it is replaced by the switcher, so nothing on the page says the list is filtered. Requirement: the iOS chip.

**A4. The grouping control picks a grouping.** iOS shows a pill with the grouping's own icon, its name and a chevron, and it opens a Group By picker. Android shows "Grouping: X" with a folder icon for every grouping, and it opens the whole list settings sheet, where the groupings are unlabeled items between computers, range, sort and display. Requirement: the iOS pill and picker; computers, sort and display move where iOS keeps them (the menu under the Agents switch and ⋯ › Display).

**A5. Groupings with the iOS shapes.**

- Android's "By computer and space" is iOS's By Project (computer, space, tab, agent) under another name: rename it. iOS's flat By Machine, one section per computer, is missing on Android (phase 3 F7 now lists it).
- By To-do: iOS is flat, one section per to-do with its status-tinted count and a › to its detail, then Started Manually and Unassigned, and it is offered only when the hub has to-dos. Android nests project, to-do and agent, splits "Opened by hand" and "No to-do" by project, and always offers it.
- By Status: iOS has eight sections, "Waiting in the background" included, each header with a count in its state's color; Seen starts folded and opens from its header; rows drop the state word. Android has seven sections, counts on three of them only, and Seen behind "N more idle".

**A6. Folds are kept.** iOS saves folds on the phone and keeps them through relaunches and grouping changes; a search opens folds only while it runs. Android resets them on every search keystroke, range change and grouping change, and loses them on relaunch (only herdr tab folds are saved). Requirement: as on iOS.

**A7. Headers as on iOS.**

- Repository: iOS draws a card with the folder, the bold name and a chevron, no counts, with its actions in the long-press menu. Android draws a plain heading with marks, state counts and a ⋯ sheet.
- Worktree: iOS shows a Primary chip on the main one, the branch when it differs from the name, "Detached HEAD @sha" as a caution, "N agents" or "Idle", and dims it when the computer is offline. Android shows the icon, the name and counts.
- Computer: iOS draws it only when the list shows more than one computer, with a background-waiting clock and an idle count. Android draws it always.

**A8. Idle repositories.** iOS gives each computer an "Idle Repositories" section with a count, each idle repository one row with "N agents · tab names" and when it last moved; idle sessions show as "2 idle · review · research" with a "Hide N Idle" row. Android keeps idle repositories inline after the busy ones, without times, and "N more idle" can't be folded again.

**A9. Row content.** The work-item row in section 3.4, as iOS's default Avatar style draws it:

- Leading: the agent mark in a round well with the state on its corner. Android puts the agent mark and the state mark side by side.
- Line 2: up to two lines of the latest words, a state word only for Needs You, Awaiting Reply and Waiting in the background. Android writes a state word for every state but Seen.
- Line 3: source · tab · space › tab · computer (the computer only when the list mixes computers), the model's short name, a to-do chip and the muted bell. Android shows "computer / space" only outside the tree and never the model.
- Row styles: Avatar, Two Lines, State Cards and Compact List in ⋯ › Display and in Settings. Android has Compact and Detailed only.

**A10. Search.** iOS matches every word across twelve fields (path, repository root, question, model, agent and errors among them) and takes `machine:`, `project:`, `agent:`, `model:`, `state:`, `is:pinned`, `is:error` and `since:`. Android matches the whole query as one substring in five fields. Requirement: as on iOS.

**A11. Smaller differences.**

- A "Waiting in the background" quick filter, and the selected chip in its own state's color (Android always tints it like Needs You).
- Collapse All keeps the computer headings open.
- A long list shows 20 rows, then "Show All N".
- Sort only under By Status.
- Interrupt in the row's long-press menu.
- A tab with several agents shows as a tab row in By Repository too, not only by computer.
- The space page: two-column cards by urgency, the computer opening its projects page, the path shortened with ~, a dashed "Open a new tab" card.
- The Personal Agents pill beside the grouping pill.

### Already decided by the spec: which assistant's computers show

`docs/assistant/managers.md` ("Clients", "Pages"): Agents shows the current assistant's sessions only. Android does this; iOS shows every assistant's computers, which is why the same mock data gives iOS "Needs you 2, Terminal 8" and Android "Needs you 1, Terminal 3". This is an iOS issue, not an Android change.

### Android only

Each becomes an iOS issue or a row in the intentional-differences list, decided in the parity list (phase 0): the range (Running / All / History) and Archives; the botmux chip; Codex subtasks under their row; the confirmation before Mark All as Read; "N of M loaded"; the summary title outside the shared chrome. The request countdown is already on phase 3's list.

### Acceptance criteria

- With the same mock hub data and the same assistant, the Needs You section, the quick filter counts and the tab badge show the same numbers on both, each counting sessions (principle 4).
- Each grouping both platforms offer has the same sections, order, headers and row content, shown side by side in light and dark (principle 4).
- Folds survive a relaunch and a grouping change; a search leaves them as they were (principle 3).
- The owner looked at the screenshots of A1, A2, A5 and A9 before merge (`DESIGN_SYSTEM.md` section 1.4).

### Out of scope

- The groupings and actions in phase 3 F7.
- Brief and Board beyond keeping them working.

## Technical design

### Where the code is

| Concern | iOS | Android |
| --- | --- | --- |
| Page, sections, Needs You | `apple/Persona/Views/Chats/ChatsView.swift` | `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentsScreen.kt`, `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentsListInfo.kt` |
| Groupings and order | `apple/Persona/Views/Chats/AgentListProjection.swift`, `apple/Persona/Views/Chats/AgentListOrder.swift` | `android/core/domain/src/main/kotlin/persona/core/domain/sort/AgentList.kt`, `android/core/domain/src/main/kotlin/persona/core/domain/sort/AgentRepos.kt` |
| Tree rows and headers | `apple/Persona/Views/Chats/RepoTreeRows.swift` | `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Agents.kt`, `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentsTree.kt` |
| Row styles | `apple/Persona/Views/Chats/AgentRowStyles.swift` | `Agents.kt` |
| Menus and the grouping control | `apple/Persona/Views/Chats/AgentsMenus.swift` | `AgentsScreen.kt`, `android/core/designsystem/src/main/kotlin/persona/core/designsystem/AgentHomeComponents.kt` |
| List state | stored values in `ChatsView.swift` | `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentsViewModel.kt`, `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentsState.kt` |
| Space page | `apple/Persona/Views/Chats/SpaceView.swift` | `android/feature/agents/src/main/kotlin/persona/feature/agents/SpaceScreen.kt` |

### A1

- Android already builds one row per session for the tab badge (`android/app/src/main/java/persona/android/ui/ShellCounts.kt`) from the rule in `spec/fixtures/needs-you`. Build the section from the same rows: a session row with its requests under it, answered in the row as section 3.4 allows (choices, low-risk tool approvals), the rest opening the session.
- `visibleAgentRequests` in `AgentsListInfo.kt` stops dropping muted sessions; the `take(3)` and "N more" go; terminal-only waits come in through the same rows.
- The Assistant home's list (`android/feature/assistant/src/main/kotlin/persona/feature/assistant/NeedsYou.kt`) takes the same rows.
- "Requests and history" moves under ⋯, where iOS keeps its history.
- Tests: the needs-you fixtures, plus a page test that the header count equals the badge.
- Done (11-11): `agentNeedsYouRows` in `AgentsListInfo.kt` builds the section from `needsYouRows`: muted sessions included, the computer filter applied, shown under All and Needs You and never while searching. A session waiting only in its terminal is a "Terminal needs you" row that opens it. The section's title is the state name. The header, the page's summary and the Needs You filter count these rows. Left: "Requests and history" stays a row (not yet under ⋯), and the Assistant home's list goes with decision 10 (#4339 C11) instead of being recounted.

### A2, A7, A8, A9

- New tree-row and header components in the design system next to `AgentRow`; By Repository uses them, other views keep the work-item row.
- Row content comes from one function in core/domain that picks line 2's state word and line 3's parts, tested against cases written from `AgentRowStyles.swift` and `RepoTreeRows.swift`; a shared fixture under `spec/fixtures/` if iOS reads it too (phase 4 R2).
- The model's short name: the session already carries the model; port iOS's `modelShortName` (in `AgentListProjection.swift`) to core/domain with tests.
- Row styles: a stored choice in `AgentsState.kt`, shown in ⋯ › Display and Settings › Interface style.

### A3, A4

- The grouping pill: `AgentHomeComponents.kt` takes the grouping's icon; the picker is a `JMenu` with the groupings only. Done (11-11) in `AgentsScreen.kt`: the pill shows the grouping's own icon and name with a chevron and opens a Group By sheet with the groupings only; with the home chrome the rest of the list's settings sit behind a ⋯ button until they move under the Agents switch (A4's second half). By Status has no generated glyph for iOS's two dots and shows a board of columns.
- Computers, hub, range and sort move to a menu under the Agents switch, as `AgentsMenus.swift` does; display options into ⋯ › Display.
- The "Only <computer>" chip clears `machineFilter`. Done (11-11): with the home chrome, a chosen computer shows as a selected "Only <computer>" chip above the grouping pill (`AgentsHomeAffordancesTest`); the computers and range menu (A4) is still to move.

### A5

- Rename `MachineSpace` to By Project in the strings (en, zh-Hans, ja), keeping the stored value.
- By To-do: flatten `AgentList.kt`'s to-do branch to iOS's order; hide the choice when the hub has no to-dos.
- Done (11-12): By To-do is iOS's flat list (`TodosLogic.buildTodoAgentGroups`): a section per open to-do, its title, one count in the to-do's state color and the arrow to the to-do; then "Opened by hand" (open) and "No to-do", folded under any section above unless something in it needs you, open while searching (`TodoDataTest`, `AgentsTreeTest`, `AgentsTodoTreeShotsTest`). The project headings and their helpers are gone. Android keeps its rule that an agent's own to-do wins over a stale agents list.
- By Status: add the background-waiting section, counts on every header, Seen folded.
- Done (11-11): Android reads the hub's `background_waiting` (`AgentSession.waitsInBackground`) and By Status has iOS's eight sections in rank order (`AgentStateSections`); each header carries one count with its state's dot, Waiting in the background a clock; Seen starts folded and opens from its header (kept as `!state/idle` in the folds, `ByStatusTest`, `AgentsFoldAllTest`), with no "N more idle" row; rows under a section leave their state word out, Muted's keep it. The shared fixtures in `spec/fixtures/sort` follow. Still to do: the background-waiting agent counted apart from Working with its own quick filter (A9) and its lighter dot (Android has no such tone yet), and By To-do's layout.
- Done (11-11): "By computer and space" reads By project (按项目, プロジェクト別), the stored value unchanged; the sheet offers By To-do only while the hub has to-dos (`offeredGroupings`, `AgentsListInfoTest`). The two layouts are still to change.

### A6

- Store folds per grouping in `AgentsState.kt`, as herdr tab folds already are.
- `AgentsViewModel.kt` stops clearing `folded` in `query`, `range` and `grouping`; a search expands through a separate set that is dropped when the query clears.
- Done (11-11): folds per grouping in DataStore (`AgentsPreferences.folds`), read on start and on a grouping change; a search stashes them and opens every fold, and restores them when it clears (`AgentsFoldAllTest`).

### A10

Port `AgentListProjection.swift`'s query parser to core/domain with its cases as tests.

### Order of work

1. A1, which fixes a rule the spec already has.
2. A3 and A6, small and invisible to the default look.
3. A4 and A5.
4. A2 and A9, then A7 and A8.
5. A10 and A11.

### Verification

- `scripts/app/android.sh test`; the needs-you fixtures.
- Paired screenshots of each grouping on the same mock data and the same assistant, light and dark (phase 4 R1; by hand until it exists).

### Risks

- The one-line tree row loses the latest line in By Repository; that is iOS's choice, and the latest line stays in the other views.
- Moving computers and range out of the sheet touches muscle memory; the release notes say where they went (principle 1).
