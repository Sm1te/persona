# Area: the Agent chat and the assistant chat (#4339)

Part of the [Android alignment plan](README.md) (#4194). Status: plan, 2026-11-11. Found by reading both apps' code and by screenshots of both on the same mock hub data (iPhone 17 Pro simulator, Pixel 9 emulator, Glass, light and dark).

[Phase 2](phase-2-interactions.md) covers the gestures and movements in a chat (keyboard, send button, unfolding, text selection, where pending requests sit) and [phase 3](phase-3-feature-gaps.md) the missing features (main chat cards, file edits and review, the voice bar). This document covers the rest of what a chat shows and how it reads, on the two chat pages people use most.

## Requirements

### Goal

An Agent's session and a conversation with the assistant read the same on Android as on iOS: the same header, the same work summary and tool names, the same composer controls, the same records in the conversation, and the same places to answer a request.

### The Agent chat

**C1. Tools named in words.** iOS names another server's MCP tool "<Server> · <Tool>" ("GitHub · Create issue") and puts any unknown name into words (`DESIGN_SYSTEM.md` section 4.7). Android falls back to a "Use" verb with a subject taken from the input, so the server and the tool never show. Requirement: section 4.7's naming, from the same table.

**C2. Subagents.** iOS draws a subagent as a card with its status, run time, prompt, reports, latest steps and View Subagent; parallel subagents share a grid ("Running 3 agents"), and a running card stays visible under a folded work line. The header's third line says what running subagents do, and a tap swaps the transcript in place with a way back. Android draws an ordinary tool row with a "View sub-agent" button that opens a separate read-only page. Requirement: as on iOS.

**C3. Model and effort in the composer.** iOS shows a text button such as "Opus High ✦" in the composer's row; its menu holds the model (with Other Model…), the effort, ultracode and the permission mode. Android has an icon-only Mode button opening a sheet with a fixed model list and no typed model, a chip above the field when the setting isn't the default, and a permission-mode icon in the header. Requirement: the iOS button and menu; the header's mode icon goes (Q11).

**C4. The header.** iOS folds the header into a slim capsule while scrolling and after 3 s without a touch, and the title opens Session Info; its second line reads "Working 22 min · machine · space › tab". Android's header never folds, the title opens a status sheet, and the second line shows the source, the machine and the raw working directory, which section 5.6 keeps off primary screens. Requirement: as on iOS.

**C5. The work summary.** iOS draws a plain caption line with a spinner and a chevron, and opened steps hang on a thin rail; it reads "Working 39 s · Tools 2 · Thinking 1" and, when done, "Tools 3 · 1 min 20 s". Android draws a rounded card with a wrench icon and hairlines between steps, and reads "<current action> · N steps · time". Requirement: the iOS line, rail and words; super rule 2 is already met on both.

**C6. Queued messages.** iOS folds the queue, and each message offers Send Now (with a confirmation) and Move Up / Down. Android always shows the queue open with Pull Back, Copy and Delete. Requirement: as on iOS.

**C7. The terminal.** iOS opens it as a full-screen cover dragged down to close, from ⋯ or a terminal mark on the live work line. Android pushes a separate page from a header icon. Requirement: as on iOS; the header's terminal icon goes (Q11).

**C8. Loops and background work.** iOS shows them as a pill on the header's second line; its sheet stops the loop and opens the output. Android shows chips above the composer that open the status sheet, with no Stop Loop. Requirement: as on iOS.

**C9. Smaller differences.**

- Session Info also shows the Lark link, the questions Persona answered for this agent, How It Runs, the Codex route, ultracode and the context window bar.
- An unsent message keeps its place and offers Resend / Delete or Retry Now / Don't Send from a small menu and a long press.
- Share sends a picture of the message.
- A strip of the images the agent looked at sits before the answer.
- The newest "While you were away" recap is open as the last line until tapped or typing starts.
- The plan pill opens the whole plan in a sheet.
- Loading shows skeleton rows; a load error is a line in the list.
- "Claude is getting ready…" while the agent starts.
- The placeholder names the agent ("Message Claude").
- A pill above the composer while the agent operates a Mac.
- A note when resuming turned ultracode off.
- The latest answer Persona gave for this agent as a card above the composer.
- Video attachments and pasted images.

### The assistant chat

**C10. Requests answered above the composer.** On iOS the conversation's own cards and the oldest question of its tasks show above the composer and are answered there. On Android they stay hidden until the user opens the Needs You tray and taps Review. Requirement: as on iOS; with phase 2 Q4 the Agent chat takes the same place.

**C11. One blank page.** iOS has no home page: a blank page comes only from New Chat, and its first message starts a new conversation. On Android, Back from any conversation (or Home in the menu) opens a home page whose messages go to the main conversation, and New Chat opens a nearly identical page that starts a new one. Requirement (Q10): drop the home page; Back from a conversation goes to the main conversation, and New Chat is the only blank page.

**C12. Conversation history.** iOS swipes a row left to archive; its menu has Pin, Rename…, Archive, Copy ID, Copy Diagnostics and End…; a long press previews the last message; "Archived (N)" opens the archived conversations. Android's sheet has Pin, Copy ID, Archive and End: no rename, no swipe, no preview and no archive list, so a replaced main conversation can't be reached. Requirement: as on iOS. This takes over "archived chats" from phase 3 F9.

**C13. Task records in the conversation.** iOS draws receipts, review lines, reports, Persona's hand-offs to tasks (folded), message cards (Send / Edit / Later) and the hub's notice lines, keeps supervision as one folded line, and its work line ticks while running. Android hides those records (interruption records aside), moves supervision into a Background Records sheet, and its work line is a static "Checked N places". Requirement: as on iOS.

**C14. Connection state in words.** iOS floats a "Connecting…" pill under the top bar. Android's Glass top bar drops its connection line and only tints the background red. Requirement: as on iOS.

**C15. Where a message goes.** On iOS a "Message 'x' ×" chip stays above the composer while a message is routed to one task, and @ lists the conversation's tasks. On Android the route is set from a task's Say and shows only inside the open In Progress tray. Requirement: as on iOS.

**C16. The new conversation's composer.** iOS's model menu holds the launcher, the mode and the effort, and @ mentions sessions and paths. Android has the launcher chip only. Requirement: as on iOS.

**C17. Needs You on the assistant page.** iOS puts 1 to 3 rows above the composer, as many as fit, and "N more" opens Agents or the Inbox; Android lists them inside the scroll and "more" always opens the Inbox. Requirement: as on iOS, counting sessions ([Agents page](agents-page.md) A1).

**C18. Smaller differences.**

- The "Model not connected · Fix it" card from `turn_error_code`, which Android doesn't decode. Done (11-12): `TurnErrorFix.kt` (`not_logged_in`, `auth_failed`, `model_route`, iOS's words) draws the card in the place of the latest error line of Persona's conversation, the agent's own words folded under it; Fix It opens the main chat's model or the launcher's provider (`TurnErrorFixTest`).
- The "next one needs you" strip after answering, which Android turns off in assistant conversations.
- New Chat disabled while on the new conversation's page.
- The In Progress tray's "Shelved N" line and the pill while the model operates a Mac.
- The assistant menu's items: Details, Back to Main Chat, the copy items, the per-conversation launcher and model ("From the next message, use"), Interrupt, Connection Details and Screen.
- The user's bubble and "Persona is getting ready…" while the conversation starts, and a failed start kept as a failed bubble with Resend / Delete.
- The sidebar's Meeting Recording and Health rows, with phase 3 F9.

### Owner decisions (2026-11-11, #4208)

**Q10. Android's assistant home page: drop it, as on iOS.** Back from a conversation goes to the main conversation, and New Chat is the only blank page. The home's cards (sort the Inbox, Agent progress, the knowledge base) and its online devices stay reachable from the sidebar and the Agents page.

**Q11. The Agent chat header's terminal and permission-mode buttons: drop both, as on iOS.** iOS has Back, the title and ⋯ only; the terminal opens from ⋯ and the work line, and the mode sits in the composer's model menu. `docs/android/android.md` says Android's mode button follows iOS (#3719), but the iOS code has no header mode button; that doc is corrected with C3.

### iOS behind, or Android only

- iOS issues: the assistant switcher with several assistants (Android follows `docs/assistant/managers.md`, iOS shows chips under the top bar without counts and doesn't keep the pick); the code block title bar with Copy that section 3.11 asks of every platform.
- Android only, each an iOS issue or an intentional difference in the parity list: search in a chat (already on phase 3's list); the task progress chip under the header; the count on Jump to Latest; the time under each bubble; Hand to Persona and Use as Context in the message menu; "Load earlier messages".

### Acceptance criteria

- Each change shown next to iOS on the same mock data, light and dark (principle 4).
- Tool names follow section 4.7 on both, checked by shared cases (principle 6).
- No raw path, session ID or engine name in a chat's header (section 5.6).
- The owner looked at the screenshots of C3, C4, C10 and C11 before merge (section 1.4).

### Out of scope

- What phases 2 and 3 already cover (listed at the top).
- Brief and Board beyond keeping them working.

## Technical design

### Where the code is

| Concern | iOS | Android |
| --- | --- | --- |
| Agent chat page, header, notes | `apple/Persona/Views/Chat/ChatView.swift` | `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatScreen.kt`, `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatChrome.kt` |
| Tool names | `apple/Persona/Views/Chat/ToolNaming.swift` | `android/core/domain/src/main/kotlin/persona/core/domain/Rows.kt`, `android/feature/chat/src/main/kotlin/persona/feature/chat/ContentPresentation.kt` |
| Subagents | `apple/Persona/Views/Chat/SubagentCard.swift` | `android/feature/chat/src/main/kotlin/persona/feature/chat/ToolDetails.kt` |
| Work summary | `ChatView.swift` | `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Chat.kt` |
| Composer model and mode | `apple/Persona/DesignSystem/ShellComposerBar.swift`, `apple/Persona/Views/Chat/SessionModes.swift` | `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatModes.kt` |
| Queue, status, Session Info | `ChatView.swift` | `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatQueueStrip.kt`, `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatStatusSheet.kt`, `android/feature/chat/src/main/kotlin/persona/feature/chat/ChatSessionInfoSheet.kt` |
| Terminal | `ChatView.swift` | `android/app/src/main/java/persona/android/ui/ShellChat.kt` |
| Assistant page, history | `apple/Persona/Views/Assistant/AssistantView.swift`, `apple/Persona/Views/Assistant/AssistantSidebar.swift`, `apple/Persona/Views/Assistant/AssistantTopBar.swift` | `android/feature/assistant/src/main/kotlin/persona/feature/assistant/AssistantScreen.kt` |
| Trays and task records | `apple/Persona/Views/Assistant/Tasks/AssistantActionBar.swift`, `apple/Persona/Views/Assistant/Tasks/TaskViews.swift` | `android/feature/chat/src/main/kotlin/persona/feature/chat/AssistantTrays.kt`, `android/feature/chat/src/main/kotlin/persona/feature/chat/AssistantTasks.kt`, `android/core/domain/src/main/kotlin/persona/core/domain/tasks/AssistantTaskCards.kt` |
| Connection state | `apple/Persona/Views/Shell/ShellStatus.swift` | `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Page.kt` |
| Needs You on the assistant page | `apple/Persona/Views/Shell/ShellHome.swift` | `android/feature/assistant/src/main/kotlin/persona/feature/assistant/NeedsYou.kt` |

### Notes per item

- **C1.** Port `ToolNaming.swift`'s table and humanizer to core/domain, and add a shared fixture of tool names and their words (en, zh-Hans, ja) that both platforms read (phase 4 R2). The `MOCK_TOOL_NAMES=1` scene in `docs/operations/testing.md` covers each case.
  Done (11-11): the naming half of `ToolNaming.swift` is in `android/core/domain/src/main/kotlin/persona/core/domain/ToolNaming.kt` (spelled words, `humanize`, `serverName`, `mcpLabel`), with iOS's cases in `ToolNamingTest`; a tool no table knows reads in words in the chat's rows and in permission summaries, and another server's tool is never matched to a built-in one by its bare name. Still to do: picking a subject from an unknown tool's input (`ToolNaming.subject`), the arguments as `key: value` lines, and the shared fixture.
- **C2.** A subagent card composable in feature/chat fed by the tool's subagent deliveries (`docs/protocol/protocol.md`, "Subagent deliveries as tool results"); viewing in place swaps the chat list's source and keeps a footer back to the main agent.
- **C3.** A text button in the composer row that names the model and effort, opening a `JMenu` with the model list, Other Model…, effort, ultracode and the mode. The model list and the typed model follow `SessionModes.swift`.
- **C4.** The header folds on scroll and on a 3 s idle timer (off when animations are off); the title opens `ChatSessionInfoSheet`. The second line takes the state word and "machine · space › tab" from the session, never `cwd`.
  Second line done (11-11): the state ("Working 22 min" from the session's `state_since`), then the source, the computer and "space › tab"; quiet states show their dot only; the computer goes first when the line is too narrow (`chatPlace`, `ChatHeaderLineTest`, `ChatHeaderLineShotsTest`). The folding header and the title opening Session Info are still to do.
- **C5.** Redraw the work summary in `Chat.kt` for Glass only: caption text, spinner, chevron and a rail; the words come from one function with tests.
  Words done (11-12): `workSummary` reads as iOS's `WorkLine` ("Reading a.kt +2 · 39 s", "Working 39 s · Tools 2 · Thinking 1", "Tools 3 · Thinking 1 · 1m 20s"), with `workNow` for the step it is on and iOS's durations; Android keeps its failed and unanswered counts after them (`ToolPresentationTest`). Drawing done (11-12): on Glass the summary is a plain caption with the spinner and a chevron after the words, and the opened steps hang on a rail (`workRail`) with no hairlines (`WorkTraceShotsTest`); Brief and Board unchanged.
- **C10 and phase 2 Q4.** Show the assistant conversation's cards in the same stack above the composer that phase 2 T6 builds for the Agent chat; the tray keeps its Review for older items.
- **C11.** Back from a conversation opens the main conversation; the home page and its `newChat` twin in `AssistantScreen.kt` become one New Chat page.
- **C12.** Rename through `POST /api/v1/sessions/{mid}/{sid}/rename` with `title`, which records a user-given title for assistant conversations (`docs/protocol/protocol.md`); a swipe on the history row; an Archived page reusing `android/feature/agents/src/main/kotlin/persona/feature/agents/AgentArchivesScreen.kt`'s list.
- **C13.** `AssistantTaskCards.kt` stops filtering the record kinds iOS draws; each kind gets a renderer next to the interruption record's.
- **C14.** A connection pill in the shared chrome, fed by the connection state the red tint already reads.
  Done (11-12): `JLinkPill` under the home bar on the home pages (Assistant and Agents), whose shared bar replaced their titles in every style; an Agent's chat keeps saying it in its title (`HomeLinkPillTest`).
- **C18.** Decode `turn_error_code` in `android/core/contracts/src/main/kotlin/persona/core/contracts/Models.kt` and draw the card; `spec/fixtures/wire` has no case for it yet, so add one.

### Order of work

1. C1, C4's second line, C14 and C18's error card: small, and each fixes a written rule.
2. C10 with phase 2 T6, and C17 with the Agents page's A1.
3. C3 and C5.
4. C2, C12 and C13.
5. C11 and C7.
6. The rest of C9 and C18.

### Verification

- `scripts/app/android.sh test`; shared fixtures for tool names and wire.
- Paired screenshots on the same mock data, light and dark: `MOCK_TOOL_NAMES=1` for C1, `MOCK_TASKS=1` for C10, C13 and C15.

### Risks

- The folding header must not hide the state when a request arrives; iOS opens it again then, and Android must too.
- Showing the assistant conversation's cards above the composer takes room on small phones; phase 2 T6's height cap applies.
- Dropping the home page changes where Back lands; the release notes say so (principle 1).
