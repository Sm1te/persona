// One conversation: an agent in herdr on one of your computers, or a Persona assistant conversation.
import { useAssistantTitle, useManagerOffline } from "../ManagerParts";
import { LinkManager } from "../../lib/manager-choice";
import { alsoOpenText } from "../../lib/engines";
import { lazy, memo, Suspense, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { api, ApiError, errorText, pastHub, sessionPath } from "../../lib/api";
import { ago, attentionReason, backgroundWaiting, externalLink, failureReason, isStoppedAgent, resumeCommand, sessionCan, sessionDeletable, sessionMenuActions, sourceSubtitle, sessionTitle, shortPath, stateOf, tabLabel, uuid } from "../../lib/format";
import { conversationPage } from "../../lib/conversation-page";
import { navigate, withWorkView, type SessionView } from "../../lib/router";
import { canShowTerminal, composerSlot, otherView, useSessionView, viewOnlyRow } from "../../lib/session-view";
import { openLink } from "../../lib/links";
import { openPanel, togglePanel, usePanel } from "../../lib/panel";
import { machineWithHub } from "../../lib/mesh";
import { hasModes, isMuted, toggleMute } from "../../lib/session-actions";
import { attachLive, flipPin } from "../../lib/agent-actions";
import { isPinned } from "../../lib/pins";
import { acknowledgeLaunch } from "../../lib/new-agent";
import { useRuntimeSession } from "../../lib/runtime-session";
import { ArchivedNotice, SessionDetailNotice } from "../../ui/SessionDetailNotice";
import { REVIEW_FEATURE } from "../../lib/session-review";
import { getSession, getState, hubHas, listenSession, refresh, refreshTodos, saveMutes, useStore, viewSession } from "../../lib/store";
import { PREVIEW_FEATURE } from "../../lib/preview";
import { agentsWorking, buildTaskIndex, stableSubagents, STOP_LOOP_MESSAGE, subagentCall, subagentCardKey, terminalCalls, type TaskIndex } from "../../lib/tasks";
import { stabilizeLines, stableValue } from "../../lib/stable-lines";
import { planProgress, workLineStyle } from "../../lib/work-groups";
import { echoed, taggedSeen } from "../../lib/echo";
import { acceptOutgoing, forgetOutgoing, inSendOrder, keepFailed, keepOutgoing, noteWritten, outboxKey, placeFailed, restoreOutbox, retryOutbox, SENT_GRACE_MS, settleOutbox } from "../../lib/outbox";
import { apiOp } from "../../lib/ops";
import { conversationKey, pickedLauncher, pickedModel } from "../../lib/assistant-chat";
import { unlinkAgentFromTodo } from "../../lib/todos";
import { LinkTodoDialog } from "../LinkTodoDialog";
import { t as tr } from "../../i18n";
import { lastEditablePrompt, promptsOf, rewindBlock, rewindDone, rewindSession, rewindTarget, type RewindRestore } from "../../lib/rewind";
import { EDIT_SEND_SETTLE_MS } from "../../lib/edit-last-key";
import { sendDraft } from "../../lib/drafts";
import { chatLines, freshWords, openRecap, parseUserText, segments, type ChatLine, type UserMessage } from "../../lib/turns";
import type { Item, Session, Subagent } from "../../lib/types";
import { anchoredScroll, followsAgain, isScrollbarPress, jumpLabel, leavesOnKey, leavesOnScrollbarDrag, leavesOnTouch, leavesOnWheel, followsAfterUnfold, UNFOLD_PAUSE, type ScrollAnchor } from "../../lib/disclosure";
import { Button, copyText, cx, Dialog, IconButton, Loading, Menu, StatePill, toast, useMenu, useNow, type MenuItem } from "../../ui/atoms";
import { awaitsReply } from "../../lib/agent-groups";
import { StatusMark } from "../../ui/StatusMark";
import { Icon } from "../../ui/icons";
import { StoppedAgentLine } from "./StoppedAgentLine";
import { scrollBehavior } from "../../ui/stream/motion";
import { segmentChanges, shiftAbove } from "./offscreen";
import { glide } from "./glide";
import { AGENT_COMMANDS, Composer, type Outgoing } from "./Composer";
import { agentCommands } from "../../lib/composer-commands";
import { CLEAR_MAIN_FEATURE, clearMainContext, isClearCommand, mainCommands } from "../../lib/main-clear";
import { PendingCards } from "./Interactions";
import { focusedCard, focusedFirst } from "../../lib/card-focus";
import { lostWith } from "../../lib/dock-focus";
import { FailureNote, showsFailureNote } from "./FailureNote";
import { CloseoutCard } from "./CloseoutCard";
import { shownCloseout } from "../../lib/closeout";
import { AWAIT_MS, awaitingTurn, DOCK_MAX, turnStart } from "./dock";
import { HEADER_SLOT, HEADER_SLOT_END, HEADER_SLOT_START } from "./header";
import { ContextMeter } from "./ContextMeter";
import { AgentModelPill } from "./ModelPill";
import { NARROW, NO_HOVER, PHONE, SHORT, useFinePointer, useMedia } from "../../lib/viewport";
import { UserBubble } from "./SegmentView";
import { scrollsAFold } from "./fold-scroll";
import { ActivityChips, GoalChip } from "./Activity";
import { paneRoute, tabSessions } from "../../lib/herdr-tree";
import { CommentMarks } from "./CommentMarks";
import { PaneTabs } from "./PaneTabs";
import { RunningStatusBar } from "./RunningStatusBar";
import { latestPlan, PlanPill, usePlanOutOfView } from "./PlanCard";
import { publishPlan } from "../../lib/plan-store";
import { ChatContext, LiveClockContext, type ChatContextValue } from "./chat-context";
import { addFreshWords, resetFreshWords } from "./fresh-words";
import { backgroundLine, backgroundWaitLine, waitingOnWorkflow, workflowAgentFor, workflowCalls, workflowCallsKey } from "../../lib/workflows";
import { waitingOnSubagents } from "../../lib/background-work";
import { doneSubagentLink, useSubagentLink } from "../../lib/subagent-link";
import { openExpanded } from "./expanded";
import { seekCall } from "./jump";
import { navRailTicks, type NavRailSource, type NavRailTick } from "../../lib/nav-rail";
import { containsInput, indexedNavTicks, userInputRevision, windowUserInputs, USER_INPUT_PAGE_SIZE } from "../../lib/user-input-index";
import { useRevealedInputSeq, useUserInputs } from "./useUserInputs";
import { NavRail } from "./NavRail";
import { useChatScrollbar } from "./useChatScrollbar";
import { ChatLineView, closedRecap, closeRecap, OpenRecapLine } from "./Lines";
import { useTranscript } from "./useTranscript";
import { ArchiveCloseDialog, CloseAgentDialog, DeleteSessionDialog, HeaderModeButton, ModeChips, RenameDialog, RenameSpaceDialog, SessionInfoDialog } from "./SessionDialogs";
import { showsInHeader } from "../../lib/header-mode";
import { archiveItems } from "../AgentMenus";
import { ChatSidebarButton } from "../ShellSwitch";
import { appArchiveDeps, deleteInsteadOfArchive, restoreAgent, useArchived } from "../../lib/agent-archives";
import { UltracodeLostNote } from "./Ultracode";
import { isUltracodeQueued, ultracodeQueueDone } from "../../lib/ultracode";
import { ChatActionsContext, liveValue, type ChatActions } from "./chat-actions";
import { QueueStrip } from "./QueueStrip";
import { ChatSearch, type LoadOutcome } from "./ChatSearch";
import { forgetReveals, revealItem, undoReveals, clearReveal } from "./search-reveal";
import { forwardDraftOf, openForward, peekJump, takeJump } from "../../lib/forward";
import { lineOfItem, loadEarlierForSearch, untilStill, type ChatSearchHit, type SearchLoadOutcome } from "../../lib/chat-search";
import { chatFiles } from "../../lib/mentions";
import { sameMessage } from "../../lib/queue";
import { OutboxBubbles } from "./OutboxBubbles";
import { MainRuntimeSlot } from "../assistant/MainRuntimeSlot";
import { AssistantFace, AssistantLauncherChip, CompactionStrip, useAssistantMenu } from "../assistant/AssistantChat";
import { Segmented } from "../assistant/parts";
import { PageBoundary } from "../PageBoundary";
import { ViewLayer } from "../session/ViewLayer";
import { conversationTitle } from "../assistant/conversations";
import { chatPlaceIn, dropChatPlace, firstLineInView, handoffFrom, type ChatPlace, type HandoffFrom } from "../../lib/handoff";
import { externalOpening } from "../../lib/conversation-origins";
import { usePersona } from "../assistant/Persona";
import { voiceCalls, useVoiceCall, useVoiceState } from "../../lib/voice/calls";
import { agentLabel } from "../../lib/mentions";
import { VoiceBar } from "./voice/VoiceBar";
import { useVoiceEntry, VoiceEnded, VoiceTail } from "./voice/VoiceParts";
import { ChatOnlyNote, FailureCard, ReadinessCard, useReadinessGate, type ReadinessGate } from "../assistant/Readiness";
import { codedFailure, refreshReadiness, turnErrorFix, type CodedFailure } from "../../lib/readiness";
import { FALLBACK_UNAVAILABLE } from "../../lib/model-fallback";
import { ModelFallbackCard } from "./ModelFallbackCard";
import { usageLimitLabel } from "../../lib/usage-limit";
import { displayLines, getFeedState, loadedFrom, loadFeed, loadToday, sendRouted, startOfDay, useFeed, type FeedItem } from "../../lib/feed";
import { CardConversation } from "../../ui/AssistantCard";
import { SessionMark } from "../../ui/AgentMark";
import { AssistantActionBar, DayLine, FeedLineView, QuickResultCard, RouteChip, TaskQuestions } from "../assistant/Tasks";
import { t } from "../../i18n";
import { lazyOverlay } from "../LazyOverlay";

// The terminal view (TerminalPanel and its stream / cell parsing) loads on demand: most visits only read the chat, so it
// stays out of the main chunk. A page that can switch to it fetches it in the background (loadTerminal below), so the
// first switch does not slide in an empty layer.
const loadTerminal = () => import("../session/TerminalPanel");
// Sharing settings load on their first open, outside the first screen (docs/web/web.md "First screen").
const SharingDialog = lazyOverlay(() => import("../../sharing/SharingDialog").then((m) => m.default));
const TerminalPanel = lazy(() => loadTerminal().then((m) => ({ default: m.TerminalPanel })));
// only on an Agent's page opened from a conversation's hand-off card (#5096): not part of the first screen
const HandoffBack = lazy(() => import("./HandoffBack").then((m) => ({ default: m.HandoffBack })));

const NO_FEED: FeedItem[] = [];
const NO_PANES: Session[] = [];
// the assistant has no subagents: one array, so the chat context does not change with every render
const NO_SUBAGENTS: Subagent[] = [];
// no fine pointer: the rail draws nothing, so its ticks are not read
const NO_RAIL_TICKS: NavRailTick[] = [];
/** Going to a search hit: how long the rows that hold it get to open before the line it is on stands in, when to give
 *  up, and how long it stays lit (Android: 3 s). */
const FIND_MS = 400;
const GIVE_UP_MS = 2000;
const HIT_MS = 3000;
const SEARCH_NOTICE: Record<SearchLoadOutcome, string | null> = {
  start: null,
  stopped: "chat-chatsearch.stopped",
  busy: "chat-chatsearch.busy",
  load_failed: "chat-chatsearch.load_failed",
  no_progress: "chat-chatsearch.no_progress",
  page_cap: "chat-chatsearch.page_cap",
};
const nextFrame = () => new Promise<void>((r) => requestAnimationFrame(() => r()));

interface Pending {
  /** The client_msg_id it went with. */
  id: string;
  msg: UserMessage;
  at: number;
  acceptedAt?: number;
  /** The node held it back until an ultracode change waiting for the turn is through (#3428): it goes again then. */
  waits?: boolean;
  /** Its POST has not been answered yet (it may be behind earlier ones): the bubble does not time out. */
  posting?: boolean;
}

/** How many times a message held back for an ultracode change goes again before it is taken as not sent. */
const ULTRACODE_SEND_TRIES = 4;
/** How long after the hub took a stop the button stays blocked if the agent keeps working: it may have ignored it. */
const STOP_SETTLE_MS = 15_000;

/** "Branch to new chat" (#2137): the node copies the conversation up to this reply's turn into a new one, which opens here. */
async function forkAt(mid: string, sid: string, itemId: string, assistant: boolean, space?: string, manager?: string) {
  try {
    const r = await api<{ session_id: string }>(`${sessionPath(mid, sid)}/fork`, { method: "POST", json: { at_item_id: itemId } });
    await refresh();
    // a health conversation's branch runs in the health space too (docs/assistant/health.md)
    // a Persona conversation's branch is on its computer and its manager's (lib/conversation-page.ts)
    navigate(!assistant ? { name: "session", mid, sid: r.session_id } : space === "health" ? { name: "health", sid: r.session_id } : conversationPage(r.session_id, mid, manager));
    toast(t("chat-chatview.forked"), "ok");
  } catch (e) {
    toast(t("chat-chatview.fork_failed", { error: errorText(e) }), "error");
  }
}

/** "Rewind to here" (#2436): the node drives Claude's /rewind to before the message; the message then goes back into the composer. */
async function rewindTo(mid: string, sid: string, draftKey: string, items: Item[], messageId: string, text: string, restore: RewindRestore) {
  const target = rewindTarget(items, messageId);
  if (!target) throw new Error(tr("rewind.no_text"));
  const res = await rewindSession(mid, sid, target, restore);
  const done = rewindDone(res, restore);
  toast(done.text, done.tone);
  if (res.input_cleared) sendDraft(draftKey, { type: "replace", text });
  else toast(tr("rewind.input_kept"), "info");
}

export function ChatView({ mid, sid, assistant, starting: launching, onToggleSidebar, topCenter, inPage, terminalAvailable, banner, offline, mainCache, refreshError, onRefresh, headerActions }: {
  mid: string;
  sid: string;
  assistant?: boolean;
  starting?: boolean;
  onToggleSidebar?: () => void;
  /** On a narrow screen, in the header's middle in place of the title (the shell's "Assistant" | "Agents" switch). */
  topCenter?: React.ReactNode;
  /** The agent's own page (`/s/…`, `/t/…`): its terminal is a view of the middle, switched in place with the header's
   *  "Terminal" | "Chat" and ⌘J (docs/web/web.md "Terminal ⇄ chat"). Elsewhere the chat is all there is. */
  inPage?: boolean;
  /** The work browser has verified that this session has an interactive pane. Standalone chats use their existing policy. */
  terminalAvailable?: boolean;
  /** Under the header: the assistant page's manager switcher (docs/assistant/managers.md "Clients"). */
  banner?: React.ReactNode;
  /** The conversation's manager is offline: what the box says instead, and nothing can be sent. */
  offline?: string;
  /** Only the main conversation keeps its latest page across browser loads. */
  mainCache?: { scope: string; manager?: string };
  refreshError?: string;
  onRefresh?: () => void;
  /** The page's own buttons at the header's end, before the permission mode and ⋯ (Health's New and its past conversations, #4329). */
  headerActions?: React.ReactNode;
}) {
  const { session, detail, retry: retryDetail } = useRuntimeSession(mid, sid);
  // opened from a conversation's hand-off card (#5096): the way back to it, until put away
  const [cameFrom, setCameFrom] = useState<HandoffFrom | undefined>(() => (inPage && !assistant ? handoffFrom() : undefined));
  // the main conversation, with a hub that clears its context in place: /clear in its command list (#5095)
  const clears = useStore((s) => Boolean(assistant) && s.assistant?.main === sid && hubHas(s.features, CLEAR_MAIN_FEATURE));
  // an archived agent (lib/agent-archives.ts) is not in the lists: its chat says so and offers Restore
  const archivedHere = useArchived(mid, sid);
  // its manager offline (several managers): nothing can be sent, wherever it was opened from; the page's own word first
  const ownerOffline = useManagerOffline(session?.manager);
  // the main conversation is titled with the current assistant's name once there are several
  const mainTitle = useAssistantTitle();
  const offlineLine = offline ?? ownerOffline;
  const listed = useStore((s) => (s.sessions[mid] ?? []).find((x) => x.session_id === sid)?.subagents);
  // the same array while nothing a card shows changed (#5602): the context, and every exchange reading it, stay put
  const lastSubagents = useRef<Subagent[] | undefined>(undefined);
  const subagents = stableSubagents(lastSubagents.current, listed);
  lastSubagents.current = subagents;
  const machineName = useStore((s) => machineWithHub(mid, s.machines));
  // only the tab's own rows (the same objects, the same array): a push for another agent on the machine leaves the chat be
  const panes = useStore((s) => (session && !assistant ? tabSessions(s.sessions[mid] ?? [], session) : NO_PANES));
  const siblings = useStore((s) => (session?.space_id ? (s.sessions[mid] ?? []).filter((x) => x.space_id === session.space_id && stateOf(x) !== "offline") : []));
  const interactions = useStore((s) => s.interactions.filter((i) => i.machine_id === mid && i.session_id === sid));
  const answered = useStore((s) => Object.values(s.answered).filter((a) => a.ix.machine_id === mid && a.ix.session_id === sid));
  const delegated = useStore((s) => Object.values(s.delegated).filter((d) => d.ix.machine_id === mid && d.ix.session_id === sid));
  // the list as pushed: its updated_at and rev are what tell the page it fell behind (useTranscript)
  const tr = useTranscript(mid, sid, { starting: Boolean(launching), updatedAt: session?.updated_at, rev: session?.rev, cache: mainCache, assistant });
  const trNow = useRef(tr);
  trNow.current = tr;
  const railChat = useRef(`${mid}/${sid}`);
  railChat.current = `${mid}/${sid}`;
  const railRun = useRef(0);
  useEffect(() => () => { railRun.current++; }, [mid, sid]);
  const fine = useFinePointer();
  // a Persona conversation's send the hub refused: a card above the composer, not a toast (docs/assistant/onboarding.md 4.4),
  // while its failed bubble (`entry`) is there
  const [sendFailure, setSendFailure] = useState<{ chat: string; failure: CodedFailure; entry: string } | null>(null);
  // the main conversation's setup gate, for deliver (defined before the gate is)
  const blockedRef = useRef<ReadinessGate["blocked"]>(undefined);
  const [pending, setPending] = useState<Pending[]>(() => {
    const launch = Object.values(getState().launches).find((l) => l.phase === "ready" && l.machineId === mid && l.session?.session_id === sid);
    const out = launch?.outgoing;
    if (!launch || !out || (!out.text && !out.images.length)) return [];
    const id = launch.messageId;
    return [{ id, at: Date.now(), msg: { id: `pending:${id}`, ...parseUserText(out.text), images: out.images.map((image, i) => ({ id: `pending:${id}#${i}`, seq: 0, role: "user", kind: "image", image })) } }];
  });
  const [draftRefusal, setDraftRefusal] = useState<{ id: string; draft: string; clearable?: string } | null>(null);
  // what other pages sent here ("Ask Persona", "Hand to AI"…): shown like this page's own sending bubbles (lib/outbox.ts)
  const outbox = useStore((s) => s.outbox[outboxKey(mid, sid)] ?? []);
  const online = useStore((s) => s.conn === "online");
  // a message Claude queued shows in the queue strip, not as a sending bubble too (ChatView.swift chatScroll)
  const queued = session?.queued ?? [];
  const shownPending = queued.length ? pending.filter((p) => !queued.some((q) => sameMessage(q, p.msg.text))) : pending;
  // a queued message taken back or deleted (QueueStrip) is not on its way any more: its sending bubble goes with it,
  // rather than showing again until the turn ends (#4342)
  // the photos a Persona chat's queued message went with, when it was sent from here (its queue holds only the words)
  const queuedPhotos = (raw: string) => pending.find((p) => sameMessage(raw, p.msg.text))?.msg.images?.length ?? 0;
  const withdrawn = (text: string) => {
    const gone = pending.find((p) => !p.posting && sameMessage(text, p.msg.text));
    if (!gone) return;
    forgetOutgoing(outboxKey(mid, sid), gone.id);
    setPending((ps) => ps.filter((p) => p.id !== gone.id));
  };
  const outgoing = shownPending.length + outbox.filter((e) => e.state !== "failed").length;
  const persona = usePersona();
  const state = session ? stateOf(session) : "unknown";
  // Voice chat (docs/web/web.md "Voice"): the call belongs to this page (the same chat may be open twice, a page and a
  // side panel), and ends as the page leaves the conversation. ChatView learns only whether a call is on here.
  const voiceId = useId();
  const voiceOwner = useMemo(() => ({ mid, sid, owner: voiceId }), [mid, sid, voiceId]);
  useEffect(() => () => voiceCalls.leave(voiceOwner), [voiceOwner]);
  const voiceState = useVoiceState(voiceOwner);
  const voiceCall = useVoiceCall(voiceOwner);
  const voiceLabel = assistant ? persona.name : session ? agentLabel(session) : "Claude";
  const itemsNow = useRef(tr.items);
  itemsNow.current = tr.items;
  const readItems = useCallback(() => itemsNow.current, []);
  const voiceEntry = useVoiceEntry({ ...voiceOwner, session, items: readItems, label: voiceLabel, off: Boolean(offlineLine) || !sessionCan(session, "send") });
  // the message is in, the hub has not said working yet (#3654): the live row covers that wait too
  const segs = useMemo(() => segments(tr.items), [tr.items]);
  const lastSeg = segs[segs.length - 1];
  const [awaitNow, setAwaitNow] = useState(() => Date.now());
  const turnFailure = session?.failure_reason ? { since: session.state_since } : undefined;
  // a message whose wait the hub took a stop for (#2450): it is no longer worked on, so Working… and Interrupt go at
  // once instead of after AWAIT_MS; a turn the hub reports later still shows through `state`
  const [stoppedTurn, setStoppedTurn] = useState<string>();
  const awaitedTurn = awaitingTurn(lastSeg, state, awaitNow, turnFailure) ? lastSeg?.user?.id : undefined;
  const awaiting = awaitedTurn !== undefined && awaitedTurn !== stoppedTurn;
  const awaitingNow = useRef<string | undefined>(undefined);
  awaitingNow.current = awaiting ? awaitedTurn : undefined;
  const lastAnswered = Boolean(lastSeg?.steps.length || lastSeg?.answer);
  useEffect(() => {
    // draw again when the wait runs out, so a message nobody picks up stops looking worked on; once something answers
    // it there is nothing to wait for (and no clock to reset on every streamed item)
    if (lastAnswered) return;
    const at = lastSeg?.user?.ts ? Date.parse(lastSeg.user.ts) : NaN;
    if (Number.isNaN(at)) return;
    setAwaitNow(Date.now());
    const left = at + AWAIT_MS - Date.now();
    if (left <= 0) return;
    const timer = window.setTimeout(() => setAwaitNow(Date.now()), left + 10);
    return () => clearTimeout(timer);
  }, [lastSeg, state, lastAnswered]);
  const working = state === "working" || Boolean(tr.streaming) || outgoing > 0 || awaiting;
  // still starting only until the conversation has anything in it: the flag the caller passes may outlive the start
  const starting = Boolean(launching) && !tr.items.length && !tr.streaming && state !== "working";
  // a new conversation with its first message on the way and no transcript yet (#1697): the lines the transcript opens
  // with ("Chat started", today) are there already and no loader goes above the message, so it doesn't move when they come
  const opening = Boolean(assistant) && !tr.items.length && outgoing > 0;

  // ── the middle of an agent's page: its conversation, or its terminal in place (lib/session-view.ts) ──
  const switchable = Boolean(inPage && terminalAvailable !== false && !assistant && canShowTerminal(session));
  const openFiles = () => session && togglePanel({ kind: "files", mid, sid, cwd: session.cwd });
  const [view, setView] = useSessionView(mid, sid, switchable);
  // The terminal connects when first shown and stays connected while the page is on screen (the chat is hidden under it,
  // not gone: coming back is instant). In the background, or when the agent stops being reachable, it lets the pane go.
  const [termKept, setTermKept] = useState(false);
  if (switchable && view === "term" && !termKept) setTermKept(true);
  if (!switchable && termKept) setTermKept(false);
  useEffect(() => {
    // a failed prefetch is not an error yet: the lazy load retries it on the switch, under PageBoundary
    if (switchable) loadTerminal().catch(() => {});
  }, [switchable]);
  const viewNow = useRef(view);
  viewNow.current = view;
  useEffect(() => {
    const hide = () => document.visibilityState === "hidden" && viewNow.current !== "term" && setTermKept(false);
    document.addEventListener("visibilitychange", hide);
    return () => document.removeEventListener("visibilitychange", hide);
  }, []);


  // what the messages' menus, the image viewer and the @ menu read from this chat (chat-actions.ts)
  const items = useRef(tr.items);
  items.current = tr.items;
  const draftKey = `${mid}/${sid}`;
  const canCompose = sessionCan(session, "send");
  // a stopped Agent (#4754): a read-only line with Terminal and Resume where the composer goes
  const stoppedLine = composerSlot(session, { assistant: Boolean(assistant), offline: Boolean(offlineLine) }) === "stopped";
  const openTerminal = () => (switchable ? setView("term") : navigate({ name: "session", mid, sid, view: "term" }));
  // The main conversation is always Persona (#1313): not its first message, the node's title for it.
  const mainConversation = useStore((s) => s.assistant?.main);
  const isMain = Boolean(assistant && (mainCache || sid === mainConversation));
  // No row for this chat yet is not "you may not write": the box takes typing and says it is connecting, or, when the hub
  // answered that the session is gone, says that instead. Only the main conversation may already send; every other chat
  // waits for its row, because the node would start an Agent for an id nobody has. A lookup that found nothing leaves
  // `session` a stand-in (useRuntimeSession), which is no row either. Retries are refused along with sends.
  const noRow = !session || detail.missing;
  // the hub could not say what this chat is (an older hub without the cached row, or a session it no longer has): the
  // notice above the chat says so, and an opening line under it would claim the chat started
  const noDetails = Boolean(detail.error) && (detail.missing || !session);
  const gone = detail.missing && !isMain;
  const refusedNoRow = noRow && !isMain;
  const unknownGate = useRef({ isMain, gone: detail.missing });
  unknownGate.current = { isMain, gone: detail.missing };
  const noRowBlock = useMemo(() => (refusedNoRow ? { label: t(gone ? "chat-chatview.session_gone" : "chat-chatview.connecting") } : undefined), [refusedNoRow, gone]);
  // A Persona conversation without a title yet is "New chat", as All chats and the sidebar call it, not its folder's name (#1697)
  const named = session ?? archivedHere;
  const title = isMain ? mainTitle : named ? (assistant ? conversationTitle(named) : sessionTitle(named)) : assistant ? t("chat-chatview.new_chat") : t("chat-chatview.session");
  const canFork = Boolean(session?.can_fork);
  // "Rewind to here" (#2436): offered on your messages of a session in a terminal; off with a reason while it cannot. Live
  // (chat-actions.ts): it changes with every turn, and in `actions` it would render every message's actions again.
  const rewindOff = rewindBlock(session, working);
  const [rewindLive] = useState(() => liveValue(rewindOff));
  useLayoutEffect(() => rewindLive.set(rewindOff), [rewindLive, rewindOff]);
  const canRewind = canCompose && !assistant;
  // Edit last message (#2945) and ↑ in an empty box (#4503), Slack's way: ↑, Esc twice or the safeguards card's button put
  // the last prompt in the box with an "Editing" bar, and the conversation-only rewind to before it runs when the edit is
  // sent (commitEdit), so starting or cancelling an edit changes nothing on the computer. One edit, of this conversation:
  // another one shown is not editing. `editingLast`: that rewind is running (the composer read-only).
  const [editingLast, setEditingLast] = useState(false);
  const editLastBusy = useRef(false);
  const [editTarget, setEditTarget] = useState<{ key: string; id: string } | null>(null);
  const editing = editTarget?.key === draftKey ? editTarget : null;
  const editNow = useRef(editing);
  editNow.current = editing;
  const editLastMessage = useCallback(() => {
    if (editLastBusy.current || !canRewind || rewindLive.get() || noRow || tr.loading || tr.error) return;
    const last = lastEditablePrompt(items.current);
    if (!last) return;
    setEditTarget({ key: draftKey, id: last.id });
    sendDraft(draftKey, { type: "replace", text: last.raw, edit: true });
  }, [canRewind, rewindLive, noRow, tr.loading, tr.error, draftKey]);
  const commitEdit = useCallback(async (): Promise<boolean | { clearDraft: string }> => {
    const target = editNow.current;
    if (!target) return true;
    if (editLastBusy.current) return false;
    // another message came after it: rewinding to before the old one would take that one too
    if (promptsOf(items.current).at(-1)?.id !== target.id) {
      setEditTarget(null);
      toast(t("rewind.edit_ended"), "info");
      return false;
    }
    const block = rewindLive.get();
    if (block) {
      toast(t("rewind.edit_wait", { reason: block }), "error");
      return false;
    }
    const at = rewindTarget(items.current, target.id);
    if (!at) {
      setEditTarget(null);
      toast(t("rewind.no_text"), "error");
      return false;
    }
    editLastBusy.current = true;
    setEditingLast(true);
    try {
      const res = await rewindSession(mid, sid, at, "conversation");
      setEditTarget((cur) => (cur === target ? null : cur));
      // Claude puts the old prompt back into its box a moment after the rewind answers, even when the node saw it empty
      // (#4605): wait for that, then the send clears exactly that prompt before typing (refused otherwise, kept in the box)
      await new Promise((r) => setTimeout(r, EDIT_SEND_SETTLE_MS));
      return { clearDraft: res.prompt || at.prompt };
    } catch (e) {
      toast(errorText(e), "error");
      return false;
    } finally {
      editLastBusy.current = false;
      setEditingLast(false);
    }
  }, [mid, sid, rewindLive]);
  // The last message changed while editing (one from the phone, a rewind from the menu): the words stay as a new message.
  const lastPromptId = useMemo(() => promptsOf(tr.items).at(-1)?.id, [tr.items]);
  useEffect(() => {
    if (!editing || editingLast || tr.loading || !tr.items.length || lastPromptId === editing.id) return;
    setEditTarget(null);
    toast(t("rewind.edit_ended"), "info");
  }, [editing, editingLast, tr.loading, tr.items.length, lastPromptId]);
  const editProp = useMemo(() => (editing ? { commit: commitEdit, cancel: () => setEditTarget(null) } : undefined), [editing, commitEdit]);
  const editableLast = useMemo(() => lastEditablePrompt(tr.items), [tr.items]);
  const onEditLastMessage = canRewind && !rewindOff && !noRow && !tr.loading && !tr.error && !editingLast && editableLast ? editLastMessage : undefined;
  // the chat a forward comes from, read when Forward is picked (lib/forward.ts): a push does not draw every message again
  const forwardChat = useRef<{ session?: Session; title: string }>({ title });
  forwardChat.current = { session: session ?? archivedHere ?? undefined, title };
  const actions = useMemo<ChatActions>(
    () => ({
      draftKey,
      title,
      canCompose,
      forward: (pick) => {
        const machine = getState().machines.find((m) => m.id === mid)?.name;
        openForward(forwardDraftOf({ mid, sid, assistant: Boolean(assistant), title: forwardChat.current.title, items: items.current, session: getSession(mid, sid) ?? forwardChat.current.session, machine }, pick));
      },
      speaker: assistant ? persona.name : "Agent",
      images: () => items.current.filter((i) => i.kind === "image"),
      fork: canFork ? (itemId: string) => void forkAt(mid, sid, itemId, Boolean(assistant), session?.space, session?.manager) : undefined,
      rewind: canRewind ? { block: rewindLive, run: (messageId, text, restore) => rewindTo(mid, sid, draftKey, items.current, messageId, text, restore) } : undefined,
    }),
    [draftKey, title, canCompose, canFork, canRewind, rewindLive, mid, sid, assistant, persona.name],
  );
  // the assistant works on any computer, so its @ menu offers workspaces too
  const mention = useMemo(() => ({ cwd: session?.cwd, self: draftKey, files: () => chatFiles(items.current), workspaces: Boolean(assistant) }), [session?.cwd, draftKey, assistant]);

  // a sent message stays as a "sending" bubble until the transcript has it (lib/echo.ts): an item the hub tied to it
  // (any kind, a notice too), or else a latest user message with its words; one that answered one answers no other
  const claimed = useRef(new Set<string>());
  useEffect(() => {
    // what a reload interrupted, once the hub's first page can tell what went in (a cached page cannot),
    // and not before this chat is known: a restored bubble's Retry posts without passing through deliver
    if (tr.firstRead && !refusedNoRow) restoreOutbox(mid, sid, pending.map((p) => p.id));
    const users = [...segs.slice(-4).flatMap((seg) => (seg.user && !seg.user.injected ? [seg.user] : [])), ...taggedSeen(tr.items)];
    users.forEach((user) => acknowledgeLaunch(mid, sid, user));
    settleOutbox(mid, sid, users);
    if (!pending.length) return;
    const shown = echoed(pending, users, (p) => ({ id: p.id, text: p.msg.text, images: p.msg.images, at: p.at }), claimed.current);
    const drop = new Set([...shown].map((p) => p.id));
    const now = Date.now();
    for (const p of pending) {
      if (drop.has(p.id) || (!p.waits && !p.posting && state !== "working" && now - (p.acceptedAt ?? p.at) >= SENT_GRACE_MS)) {
        forgetOutgoing(outboxKey(mid, sid), p.id);
      }
    }
    setPending((ps) => {
      // a sent one waits for its copy while the agent works and SENT_GRACE_MS after, as the outbox and the apps do: the
      // transcript can write it late (a long context compacted first, #1119); one held back for an ultracode change
      // waits as long as the turn does
      return ps.filter((p) => !drop.has(p.id) && (p.waits || p.posting || state === "working" || now - (p.acceptedAt ?? p.at) < SENT_GRACE_MS));
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [segs, tr.firstRead, refusedNoRow]);

  // A quiet transcript must not leave accepted bubbles forever. Reloads use the same stored deadline; local
  // composer bubbles retain their existing grace while the agent works. Never expire a POST still in flight.
  useEffect(() => {
    const expiring = state === "working" ? [] : pending.filter((p) => !p.posting && !p.waits);
    const deadlines = [...expiring, ...outbox.filter((e) => e.state === "sent")].map((e) => (e.acceptedAt ?? e.at) + SENT_GRACE_MS);
    if (!deadlines.length) return;
    const timer = setTimeout(() => {
      settleOutbox(mid, sid, []);
      const expired = new Set(expiring.filter((p) => Date.now() - (p.acceptedAt ?? p.at) >= SENT_GRACE_MS).map((p) => p.id));
      for (const id of expired) forgetOutgoing(outboxKey(mid, sid), id);
      if (expired.size) setPending((ps) => ps.filter((p) => !expired.has(p.id)));
    }, Math.max(0, Math.min(...deadlines) - Date.now()));
    return () => clearTimeout(timer);
  }, [mid, sid, pending, outbox, state]);

  // seen: on open, when the page comes back to the front, and whenever a turn ends while it is on screen (lib/store.ts
  // viewSession)
  useEffect(() => viewSession(mid, sid), [mid, sid]);

  // what came in over the WebSocket after the chat subscribed: the agent's words among it fade in (contract §3); the
  // first page, older pages and unfolded ranges come over HTTP and do not. Outside React (fresh-words.ts): a new reply
  // renders its own words, not every exchange; added before the transcript renders it, so its words mount fading.
  useEffect(() => {
    resetFreshWords(mid, sid);
    const off = listenSession(mid, sid, { items: (e) => addFreshWords(mid, sid, freshWords(items.current, e.items ?? [])) });
    return () => {
      off();
      resetFreshWords(mid, sid);
    };
  }, [mid, sid]);

  // ── scrolling (lib/disclosure.ts "following the end"): the end stays in view until the reader scrolls away from it;
  // older pages landing above keep the view still ──
  const scroller = useRef<HTMLDivElement>(null);
  // the element itself, for the effects that listen on it: once the session arrives the page's frame can change (a
  // terminal to switch to) and React then draws the chat in another element, so listeners left on the first one stop
  // following the end (an agent's page opened by its address did not keep its live text in view, #1309)
  const [scrollEl, setScrollEl] = useState<HTMLDivElement | null>(null);
  useChatScrollbar(scrollEl);
  const scrollRef = useCallback((el: HTMLDivElement | null) => {
    scroller.current = el;
    if (el) setScrollEl(el);
  }, []);
  const atEnd = useRef(true);
  // the jump's smooth scroll running: its first frames, a few px off the end, do not start following again
  const jumping = useRef(0);
  const [away, setAway] = useState(false);
  // A row a jump brought into view (the rail, a search hit, a comment): it stays `at` px under the view's top while what
  // is around it settles (pictures coming in, exchanges drawn for the first time). Held while the view moves and a
  // moment after; the reader scrolling, or the view going to the end, lets it go.
  const landing = useRef<{ row: Element | Range; at: number; quiet: number } | null>(null);
  const holdLanding = useCallback((ms: number) => {
    const l = landing.current;
    if (!l) return;
    clearTimeout(l.quiet);
    l.quiet = window.setTimeout(() => (landing.current = null), ms);
  }, []);
  const land = useCallback((row: Element | Range, at: number) => {
    clearTimeout(landing.current?.quiet);
    landing.current = { row, at, quiet: 0 };
    holdLanding(1200);
  }, [holdLanding]);
  // a smooth jump on its way (glide.ts): it reads where to go each frame, and the ResizeObserver leaves the view to it
  const gliding = useRef<(() => void) | null>(null);
  const stopGlide = useCallback(() => {
    gliding.current?.();
    gliding.current = null;
  }, []);
  useEffect(() => stopGlide, [stopGlide]);
  const glideTo = useCallback((aim: () => number) => {
    const sc = scroller.current;
    stopGlide();
    if (!sc) return;
    if (scrollBehavior() === "auto") sc.scrollTop = aim();
    else gliding.current = glide(sc, aim, () => (gliding.current = null));
  }, [stopGlide]);
  // something came in since the reader left the end: the pill says "New messages"
  const [newSince, setNewSince] = useState(false);
  const follow = useCallback((on: boolean) => {
    atEnd.current = on;
    setAway(!on);
    if (on) {
      setNewSince(false);
      landing.current = null;
    }
  }, []);
  const lastTop = useRef(0);
  const dragging = useRef(false);
  const before = useRef<{ h: number; top: number } | null>(null);
  const first = useRef(true);
  // Back from an Agent's page this conversation handed work to (#5096): where the chat was, kept on its history entry
  // (lib/handoff.ts); it opens there instead of at the end, once, and goes to the end if that line does not come in time.
  // `back`: this chat opened at a kept place (then the place handling, not the opening below, sets following).
  const kept = useRef<{ key: string; place?: ChatPlace; back?: boolean }>({ key: "" });
  if (kept.current.key !== `${mid}/${sid}`) {
    const place = assistant ? chatPlaceIn() : undefined;
    const back = Boolean(place && !place.end && place.line);
    kept.current = { key: `${mid}/${sid}`, place: back ? place : undefined, back };
  }
  useEffect(() => {
    if (assistant) dropChatPlace();
  }, [assistant, mid, sid]);

  useEffect(() => {
    lastTop.current = 0;
    if (kept.current.back) {
      if (kept.current.place) follow(false); // still waiting for its line: not pulled to the end meanwhile
      return;
    }
    first.current = true;
    follow(true);
  }, [mid, sid, follow]);

  useEffect(() => {
    if (!kept.current.place) return;
    const late = window.setTimeout(() => {
      if (!kept.current.place) return;
      kept.current.place = undefined;
      follow(true);
      const el = scroller.current;
      if (el) el.scrollTop = el.scrollHeight;
    }, GIVE_UP_MS * 2);
    return () => clearTimeout(late);
  }, [mid, sid, follow]);

  const lastId = tr.items[tr.items.length - 1]?.id;
  useEffect(() => {
    if (lastId && !atEnd.current) setNewSince(true);
  }, [lastId]);

  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (before.current) {
      el.scrollTop = before.current.top + (el.scrollHeight - before.current.h);
      before.current = null;
      return;
    }
    const place = kept.current.place;
    // the line it was at, `offset` under the view's top again; until that line comes in, the chat opens at its end as
    // it always does (and goes to the line once it is there)
    const row = place && el.querySelector(`[data-line="${CSS.escape(place.line!)}"]`)?.firstElementChild;
    if (place && row) {
      kept.current.place = undefined;
      el.scrollTop += row.getBoundingClientRect().top - el.getBoundingClientRect().top - place.offset;
      first.current = false;
      follow(false);
      land(row, place.offset);
      return;
    }
    if (atEnd.current || first.current) {
      el.scrollTop = el.scrollHeight;
      if (tr.items.length) first.current = false;
    }
  }, [tr.items, pending, outbox, tr.streaming, interactions.length]);

  // A card that unfolds (#1124): following holds off for UNFOLD_PAUSE while the clicked row stays where it was on
  // screen (anchoredScroll), then picks up again if the end is still near; a long card opened at the end stays put to
  // be read, and the pill brings the end back. Held until the sizes stop changing for a moment, or the reader scrolls.
  const anchor = useRef<(ScrollAnchor & { el: Element; quiet: number }) | null>(null);
  useEffect(() => {
    const sc = scroller.current;
    if (!sc) return;
    let resume = 0;
    // the reader scrolled (wheel, finger, keys) while an unfold held following off: the pause does not pull them back
    let moved = false;
    const drop = () => {
      if (anchor.current) clearTimeout(anchor.current.quiet);
      anchor.current = null;
    };
    const readerMoves = () => {
      moved = true;
      drop();
      stopGlide();
      landing.current = null;
    };
    const onClick = (e: MouseEvent) => {
      const el = e.target instanceof Element ? e.target.closest("button[aria-expanded]") : null;
      if (!el || !sc.contains(el)) return;
      drop();
      clearTimeout(resume);
      const was = atEnd.current;
      atEnd.current = false;
      moved = false;
      anchor.current = { el, edge: "top", at: el.getBoundingClientRect().top, follow: false, quiet: window.setTimeout(drop, 1000) };
      resume = window.setTimeout(() => {
        if (!was) return;
        if (!followsAfterUnfold({ wasAtEnd: was, readerMoved: moved, gap: sc.scrollHeight - sc.scrollTop - sc.clientHeight })) return follow(false);
        drop();
        follow(true);
        sc.scrollTop = sc.scrollHeight;
      }, UNFOLD_PAUSE);
    };
    sc.addEventListener("click", onClick, true);
    for (const ev of ["wheel", "touchstart", "keydown", "pointerdown"]) sc.addEventListener(ev, readerMoves, { passive: true });
    return () => {
      drop();
      clearTimeout(resume);
      sc.removeEventListener("click", onClick, true);
      for (const ev of ["wheel", "touchstart", "keydown", "pointerdown"]) sc.removeEventListener(ev, readerMoves);
    };
  }, [follow, scrollEl, stopGlide]);

  // content that grows by itself (images loading, a live group's steps) keeps the end in view; a held row stays put.
  // A ResizeObserver runs after layout and before paint, so the offset moves in the same frame as the height. The view
  // itself is watched too: what sits under it (the composer growing, the task dock, a question card) makes it shorter
  // without the content changing, and at the end the newest line stays in view. So are the exchanges that skip drawing
  // off screen (watchSegment): one drawn for the first time above the view swaps its guessed height for its own, and
  // the view moves by as much, so what is on screen stays (offscreen.ts).
  const heights = useRef(new WeakMap<Element, number>());
  const resized = useRef<ResizeObserver | null>(null);
  const sizes = useCallback(() => (resized.current ??= new ResizeObserver((entries) => {
      const sc = scroller.current;
      if (!sc) return;
      const changes = segmentChanges(entries, heights.current);
      if (gliding.current) return;
      const viewTop = sc.getBoundingClientRect().top;
      const l = landing.current;
      if (l && (l.row instanceof Range ? l.row.startContainer : l.row).isConnected) {
        const off = l.row.getBoundingClientRect().top - viewTop - l.at;
        if (Math.abs(off) >= 0.5) {
          sc.scrollTop += off;
          holdLanding(1200);
        }
        return;
      }
      const a = anchor.current;
      if (a && a.el.isConnected) {
        const r = a.el.getBoundingClientRect();
        const next = anchoredScroll(a, {
          scrollTop: sc.scrollTop, scrollHeight: sc.scrollHeight, clientHeight: sc.clientHeight,
          viewTop: sc.getBoundingClientRect().top, edgeAt: r.top, elTop: r.top,
        });
        if (Math.abs(next.scrollTop - sc.scrollTop) >= 0.5) sc.scrollTop = next.scrollTop;
        clearTimeout(a.quiet);
        a.quiet = window.setTimeout(() => (anchor.current = null), 600);
        return;
      }
      if (atEnd.current) sc.scrollTop = sc.scrollHeight;
      else {
        const shift = shiftAbove(changes, viewTop);
        if (shift) sc.scrollTop += shift;
      }
    })), [holdLanding]);
  useEffect(() => {
    const view = scroller.current;
    const el = view?.firstElementChild;
    if (!view || !el) return;
    const ro = sizes();
    ro.observe(el);
    ro.observe(view);
    return () => {
      ro.unobserve(el);
      ro.unobserve(view);
    };
  }, [scrollEl, sizes]);
  // Its height as this commit's layout effects see it is where its changes count from: an earlier page landing keeps
  // the view still by that layout (the guessed heights), and the browser may draw the new exchanges later in the same
  // frame, before the observer's first look at them. Read in the layout effects, where the chat reads its own height
  // anyway; nothing in between writes to the page, so it is one layout for all of them.
  const watchSegment = useCallback((section: HTMLElement) => {
    const ro = sizes();
    const h = section.getBoundingClientRect().height;
    if (h > 0) heights.current.set(section, h);
    ro.observe(section);
    return () => ro.unobserve(section);
  }, [sizes]);

  // the reader leaving the end: the wheel or a trackpad going up, a finger pulling the list down, PageUp / ↑ / Home,
  // the scroll bar dragged up (onScroll)
  useEffect(() => {
    const sc = scroller.current;
    if (!sc) return;
    const size = () => ({ scrollHeight: sc.scrollHeight, clientHeight: sc.clientHeight });
    const leave = () => follow(false);
    // (checked before sizes are read: no layout read per wheel or touch event while away from the end)
    // a wheel or a finger scrolling a folded message (fold-scroll.tsx) stays in it: the chat does not move
    const wheel = (e: WheelEvent) => atEnd.current && !scrollsAFold(e.target, sc, e.deltaY) && leavesOnWheel({ deltaX: e.deltaX, deltaY: e.deltaY, ...size() }) && leave();
    let startY: number | null = null;
    const touchStart = (e: TouchEvent) => (startY = e.touches[0]?.clientY ?? null);
    const touchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY;
      if (atEnd.current && startY !== null && y !== undefined && !scrollsAFold(e.target, sc, startY - y) && leavesOnTouch({ startY, y, ...size() })) leave();
    };
    const key = (e: KeyboardEvent) => {
      const t = e.target instanceof Element ? e.target : null;
      if (t?.closest("input, textarea, [contenteditable=true]")) return;
      if (atEnd.current && leavesOnKey({ key: e.key, ...size() })) leave();
    };
    const down = (e: PointerEvent) => (dragging.current = isScrollbarPress({ targetIsView: e.target === sc }));
    const up = () => (dragging.current = false);
    sc.addEventListener("wheel", wheel, { passive: true });
    sc.addEventListener("touchstart", touchStart, { passive: true });
    sc.addEventListener("touchmove", touchMove, { passive: true });
    sc.addEventListener("pointerdown", down);
    window.addEventListener("pointerup", up);
    window.addEventListener("keydown", key);
    return () => {
      sc.removeEventListener("wheel", wheel);
      sc.removeEventListener("touchstart", touchStart);
      sc.removeEventListener("touchmove", touchMove);
      sc.removeEventListener("pointerdown", down);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("keydown", key);
    };
  }, [follow, scrollEl]);

  const loadEarlierNearTop = () => {
    const el = scroller.current;
    if (!el || el.scrollTop >= 300 || !tr.hasMore || tr.loadingEarlier || first.current) return;
    before.current = { h: el.scrollHeight, top: el.scrollTop };
    void tr.loadEarlier().finally(() => {
      // nothing came: forget the anchor
      requestAnimationFrame(() => (before.current = null));
    });
  };

  const onScroll = () => {
    const el = scroller.current;
    if (!el) return;
    const gap = el.scrollHeight - el.scrollTop - el.clientHeight;
    const delta = el.scrollTop - lastTop.current;
    lastTop.current = el.scrollTop;
    holdLanding(600);
    if (atEnd.current) {
      if (leavesOnScrollbarDrag({ dragging: dragging.current, delta })) follow(false);
    } else if (!anchor.current && !jumping.current && followsAgain({ gap, delta })) follow(true);
    if (!first.current && !atEnd.current && el.scrollTop < 300) revealLoadedInputs();
    loadEarlierNearTop();
  };

  // Reaching the top only loads older pages through a scroll event, and a view that cannot scroll (a page shorter than
  // it: the main conversation's newest rounds are often two folded background rounds) or a page that landed without
  // moving it sends none. Each change asks again, so older pages load until the top is out of reach.
  useEffect(() => {
    loadEarlierNearTop();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tr.items, tr.hasMore, tr.loadingEarlier]);

  const toEnd = () => {
    const el = scroller.current;
    if (!el) return;
    follow(true);
    glideTo(() => el.scrollHeight - el.clientHeight);
  };

  // ── sending ──
  // true: it went, or it stays in the chat as a failed bubble (`failed`); false: it never went and nothing shows it
  const deliver = useCallback(
    async (out: Outgoing, id: string, clearDraft?: string, attempt = 0, keepInBox = false): Promise<boolean> => {
      // the main conversation's setup gate holds every path that sends, not only the composer (4.4)
      if (blockedRef.current) {
        blockedRef.current.onClick?.();
        return false;
      }
      const row = getSession(mid, sid);
      if (row && !unknownGate.current.gone ? !sessionCan(row, "send") : !unknownGate.current.isMain) {
        toast(isStoppedAgent(row) ? t("chat-chatview.stopped_line") : sourceSubtitle(session) || t("chat-chatview.session_unavailable"), "error");
        return false;
      }
      // a sending bubble shows picked contexts as quote cards, like the transcript will (not the raw tags)
      const p: Pending = { id, at: Date.now(), posting: true, msg: { id: `pending:${id}`, ...parseUserText(out.text), images: out.images.map((image, i) => ({ id: `pending:${id}#${i}`, seq: 0, role: "user", kind: "image", image })) } };
      // a new try: an earlier failure's card no longer applies (its bubble stays, with Retry)
      setSendFailure(null);
      noteWritten(id, p.at);
      setPending((ps) => [...ps.filter((x) => x.id !== id), p]);
      follow(true); // your own message: to the end, whatever you were reading
      const key = outboxKey(mid, sid);
      const launcher = assistant ? pickedLauncher(conversationKey(mid, sid)) : undefined;
      const model = assistant ? pickedModel(conversationKey(mid, sid)) : undefined;
      keepOutgoing(key, { id, text: out.text, images: out.images, at: p.at, launcher, model });
      try {
        // the bubble stays as sending while a gap (a hub restart) is waited out; the id is the op id too
        await inSendOrder(key, () => apiOp(`${sessionPath(mid, sid)}/messages`, {
          method: "POST",
          opId: id,
          json: { text: out.text, images: out.images.length ? out.images : undefined, client_msg_id: id, clear_draft: clearDraft, launcher, model },
        }));
        const acceptedAt = Date.now();
        acceptOutgoing(key, id, acceptedAt);
        setPending((ps) => ps.map((x) => (x.id === id ? { ...x, posting: false, acceptedAt } : x)));
        return true;
      } catch (e) {
        // An ultracode change waits for the turn (docs/protocol/protocol.md "Claude Code ultracode"): the node holds messages back
        // so the next turn cannot overtake it. The bubble stays and says so, the box is free (and its stop button there),
        // and the message goes again once the change is through, with a new id (the hub keeps its answer to this one).
        if (!assistant && isUltracodeQueued(e) && attempt < ULTRACODE_SEND_TRIES) {
          setPending((ps) => ps.map((x) => (x.id === id ? { ...x, waits: true } : x)));
          void ultracodeQueueDone(mid, sid).then((through) => {
            setPending((ps) => ps.filter((x) => x.id !== id));
            if (through) {
              forgetOutgoing(key, id);
              void deliver(out, uuid(), clearDraft, attempt + 1);
            } else failed(e, out, p);
          });
          return true;
        }
        setPending((ps) => ps.filter((x) => x.id !== id));
        // an edit's send the node did not take (#4605): nothing went, so the words go back into the box with the node's
        // reason (a draft in Claude's box…), not into a failed bubble whose Retry would send them without the rewind's care
        if (keepInBox && e instanceof ApiError && e.body.delivered === "none") {
          forgetOutgoing(key, id);
          toast(errorText(e), "error");
          return false;
        }
        failed(e, out, p);
        return true;
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `failed` reads only stable setters and these
    [mid, sid, session, assistant, follow],
  );

  /** Keeps a message that did not go as a failed bubble where it was sent, with why, "Retry" and "Delete", the box empty
   *  (lib/outbox.ts keepFailed; as in the apps, #1122). The bubble says why (lib/outbox.ts failureText), so no toast
   *  repeats it; what needs you more gets more: the computer's draft (a dialog that can clear it), the main
   *  conversation's setup (a card above the composer with what to do). */
  function failed(e: unknown, out: Outgoing, p: Pending) {
    keepFailed(mid, sid, { id: p.id, text: out.text, images: out.images, at: p.at, launcher: assistant ? pickedLauncher(conversationKey(mid, sid)) : undefined, model: assistant ? pickedModel(conversationKey(mid, sid)) : undefined }, e);
    if (e instanceof ApiError && e.code === "input_draft") {
      setDraftRefusal({ id: p.id, draft: String(e.body.draft ?? ""), clearable: typeof e.body.clearable === "string" ? e.body.clearable : undefined });
    } else if (assistant && !(e instanceof ApiError && (e.code === "dialog_open" || e.body.delivered === "unknown"))) {
      setSendFailure({ chat: `${mid}/${sid}`, failure: codedFailure(e), entry: p.id });
      void refreshReadiness({ force: true });
    }
  }

  const onSend = async (out: Outgoing): Promise<boolean> => {
    // /clear in the main conversation clears its context in place, it is not words for Persona (#5095); an edit of the
    // last message stays an edit (its rewind already ran)
    if (clears && !out.edit && isClearCommand(out.text, out.images.length)) return clearMainContext(sid);
    // Routed to a task ("Tell "xx""): straight to its agent, not to Persona (docs/assistant/unified-entry.md "Routing").
    if (assistant && out.images.length && getFeedState().routes[sid]) {
      toast(t("chat-chatview.route_no_images"), "error");
      return false;
    }
    if (assistant && !out.images.length && out.text.trim() && getFeedState().routes[sid]) {
      try {
        // in turn: the route is used up by the first message that takes it, and the next one must see that
        if (await inSendOrder(outboxKey(mid, sid), () => sendRouted(sid, out.text))) return true;
      } catch (e) {
        toast(t("chat-chatview.not_delivered", { error: errorText(e) }), "error");
        return false;
      }
    }
    return deliver(out, uuid(), out.edit?.clearDraft, 0, Boolean(out.edit));
  };
  // the composer is memoized: one function, running the latest onSend
  const sendNow = useRef(onSend);
  sendNow.current = onSend;
  const send = useCallback((out: Outgoing) => sendNow.current(out), []);
  // The assistant's tasks (docs/assistant/unified-entry.md): the conversation's feed, and the main one's "Today" card.
  const feed = useFeed((s) => (assistant ? (s.feeds[sid] ?? NO_FEED) : NO_FEED));
  useEffect(() => {
    if (!assistant) return;
    void loadFeed(sid);
    if (sid === mainConversation) void loadToday(sid);
  }, [assistant, sid, mainConversation]);
  // The "Today" card's actions say something to Persona here.
  useEffect(() => {
    if (!assistant) return;
    const say = (e: Event) => {
      const d = (e as CustomEvent<{ mid: string; sid: string; text: string }>).detail;
      if (d.mid === mid && d.sid === sid) void deliver({ text: d.text, images: [] }, uuid());
    };
    window.addEventListener("persona:say", say);
    return () => window.removeEventListener("persona:say", say);
  }, [assistant, mid, sid, deliver]);

  const [stopping, setStopping] = useState(false);
  const stopInFlight = useRef(false); // the press that comes before `stopping` has drawn
  const stopTimer = useRef(0); // an earlier stop's wait must not end a later stop's block
  const stopDone = useCallback(() => {
    clearTimeout(stopTimer.current);
    stopInFlight.current = false;
    setStopping(false);
  }, []);
  const interrupt = useCallback(async () => {
    if (stopInFlight.current || !sessionCan(getSession(mid, sid), "interrupt")) return;
    stopInFlight.current = true;
    setStopping(true);
    const waited = awaitingNow.current;
    try {
      // the hub gives the computer 90 s: past that the link is dead, and a request that never ends must not hold the block
      const r = await apiOp<{ state?: string }>(`${sessionPath(mid, sid)}/interrupt`, { method: "POST", timeout: pastHub(90_000) });
      // a repeat after a gap can read as a success: the hub lost track of the first try, so nothing says it stopped
      if (r.state === "unknown") throw new Error(t("lib-new-agent.outcome_unknown"));
      toast(t("chat-chatview.interrupted"));
      if (waited) setStoppedTurn(waited);
      stopTimer.current = window.setTimeout(stopDone, STOP_SETTLE_MS);
    } catch (e) {
      toast(t("chat-chatview.interrupt_failed", { error: errorText(e) }), "error");
      stopDone();
    }
  }, [mid, sid, stopDone]);
  // the turn ended, and not necessarily by this stop
  useEffect(() => {
    if (state !== "working") stopDone();
  }, [state, stopDone]);

  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      // in the terminal view Esc belongs to the program in the terminal; a message still waiting for its turn to start can be
      // stopped as a running turn can, as the Interrupt button shows for both
      if (e.key === "Escape" && (state === "working" || awaiting) && view !== "term" && !document.querySelector("[role=dialog],[role=menu]")) {
        e.preventDefault();
        void interrupt();
      }
    };
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, awaiting, session, view]);

  // The hub says its turn is over and a workflow it launched keeps it working (#3434): the exchange is drawn finished
  // (its work folded, the run's card inside it), and the running bar says the workflow, timed from its start, instead
  // of the turn's "Working…" clock going on. Other background work (a command, a Monitor, a subagent) leaves the turn as
  // it was. A message of yours on its way, or one it is answering, is a turn again.
  // The same for an agent that only waits on background commands (#6045): its turn is drawn finished.
  // The same for a background subagent still running after the turn ended (#5030, cindy's background subtasks).
  const subtasks = waitingOnSubagents(session, subagents);
  const backgroundOnly = (waitingOnWorkflow(session) || Boolean(session && backgroundWaiting(session)) || subtasks) && !tr.streaming && !outgoing && !awaiting;
  const lastLive = working && !backgroundOnly && !tr.loading;
  // the main conversation's readiness: the card for what is missing, and the send button saying it (4.4)
  const gate = useReadinessGate(isMain);
  blockedRef.current = gate.blocked;
  const failedEntry = sendFailure?.chat === `${mid}/${sid}` ? outbox.find((e) => e.id === sendFailure.entry && e.state === "failed") : undefined;
  const failure = failedEntry ? sendFailure!.failure : null;
  // the hub's word on the last turn's model error, for its card (5.2 turn_error_code)
  const turnFix = assistant ? turnErrorFix(session?.turn_error_code) : null;
  // A subscription usage limit: its error card says when it resumes. The clock drops the wait once the grace is past.
  useNow(30_000, session?.turn_error_kind === "usage_limit");
  const limitLine = session ? usageLimitLabel(session) : undefined;
  // a line that comes out the same as last time keeps its object, so a push re-renders only the lines it changed
  // (lib/stable-lines.ts); the task index likewise, or every row reading the context would render again
  const lastLines = useRef<ChatLine[] | undefined>(undefined);
  // the main conversation folds rounds it did not start from the user (docs/assistant/assistant.md "Background rounds")
  const foldBackground = useStore((s) => s.assistant?.main_background_folded) !== false && isMain;
  const lines = useMemo(
    () => stabilizeLines(lastLines.current, chatLines(tr.items, { live: lastLive && !outgoing, background: foldBackground })),
    [tr.items, lastLive, outgoing, foldBackground],
  );
  lastLines.current = lines;
  // the last two exchanges are always drawn (the one going on, the one just done); those before them skip drawing while
  // off screen (SegmentView `offscreen`). Not on a touch-only screen: a script moving the view (keeping it still as one
  // is drawn) can stop a fling there.
  const touch = useMedia(NO_HOVER);
  const drawnLast = useMemo(() => new Set(lines.filter((l) => l.kind === "segment").slice(-2).map((l) => l.id)), [lines]);
  const offscreen = (line: ChatLine) => (!touch && line.kind === "segment" && !line.live && !drawnLast.has(line.id) ? watchSegment : undefined);
  const linesNow = useRef(lines);
  linesNow.current = lines;
  const limitOn = limitLine ? lines.reduce((id, line) => (line.kind === "apiError" ? line.id : id), "") : "";
  // failed messages where they were sent: before the first message written after them (lib/outbox.ts placeFailed)
  const placed = useMemo(() => placeFailed(lines, outbox), [lines, outbox]);
  const failedBefore = (id: string) => {
    const list = placed.before.get(id);
    return list ? [<OutboxBubbles key={`failed:${id}`} mid={mid} sid={sid} entries={list} canRetry={!refusedNoRow} />] : [];
  };
  const lastTasks = useRef<TaskIndex | undefined>(undefined);
  const tasks = useMemo(() => stableValue(lastTasks.current, buildTaskIndex(tr.items)), [tr.items]);
  lastTasks.current = tasks;
  // Only lightweight positions are fetched for navigation; older transcript bodies wait for a selection.
  const { seq: revealedSeq, reveal: revealLoadedInputs } = useRevealedInputSeq(`${mid}/${sid}/${tr.history ?? ""}`, tr.loading ? undefined : tr.items[0]?.seq);
  const loadedRailTicks = useMemo(() => {
    if (!fine) return NO_RAIL_TICKS;
    const ticks = navRailTicks(lines);
    return revealedSeq === undefined ? ticks.slice(-USER_INPUT_PAGE_SIZE) : ticks;
  }, [fine, lines, revealedSeq]);
  const inputRevision = useMemo(() => userInputRevision(tr.items), [tr.items]);
  const completeInputIndex = useUserInputs(mid, sid, tr.history, inputRevision, fine && !tr.loading, tr.reload);
  const inputIndex = useMemo(() => completeInputIndex ? windowUserInputs(completeInputIndex, revealedSeq) : null, [completeInputIndex, revealedSeq]);
  const railTicks = useMemo(() => inputIndex ? indexedNavTicks(inputIndex, lines, tr.items) : loadedRailTicks, [inputIndex, lines, tr.items, loadedRailTicks]);
  /** Brings a row (or a run of words) into view in the chat's own scroller (scrollIntoView would also scroll an opening
   *  group's clipped box): following the end stops, and the jump's first frames do not pick it up again (`jumping`). */
  const landRow = useCallback(
    (row: HTMLElement | Range, topOffset: number) => {
      const sc = scroller.current;
      if (!sc) return;
      follow(false);
      clearTimeout(jumping.current);
      jumping.current = window.setTimeout(() => (jumping.current = 0), 1200);
      land(row, -topOffset);
      glideTo(() => sc.scrollTop + row.getBoundingClientRect().top - sc.getBoundingClientRect().top + topOffset);
    },
    [follow, land, glideTo],
  );
  const railJump = useCallback(
    async (lineId: string) => {
      const scope = `${mid}/${sid}`;
      const run = ++railRun.current;
      const deadline = Date.now() + 120_000;
      const target = inputIndex?.inputs.find((input) => `seg:${input.id}` === lineId);
      const cancelled = () => railChat.current !== scope || run !== railRun.current || Boolean(inputIndex?.history && trNow.current.history && inputIndex.history !== trNow.current.history);
      follow(false);
      if (!target || containsInput(trNow.current.items, target)) {
        const owner = target ? lineOfItem(linesNow.current, trNow.current.items, target.id) : lineId;
        const row = owner ? scroller.current?.querySelector<HTMLElement>(`[data-line="${CSS.escape(owner)}"]`)?.firstElementChild : null;
        if (row instanceof HTMLElement) { landRow(row, -12); return; }
      }
      if (target && !containsInput(trNow.current.items, target)) {
        // Reuse ordinary backward pages, keeping history contiguous rather than inserting an isolated window.
        await loadEarlierForSearch({
          get: () => trNow.current,
          load: async () => {
            const sc = scroller.current;
            if (sc) before.current = { h: sc.scrollHeight, top: sc.scrollTop };
            try { await trNow.current.loadEarlier(); }
            finally { requestAnimationFrame(() => (before.current = null)); }
          },
          frame: nextFrame,
          stopped: () => cancelled() || Date.now() >= deadline || containsInput(trNow.current.items, target),
          onPage: () => {},
          pages: Number.MAX_SAFE_INTEGER,
        });
        if (cancelled()) return;
        if (!containsInput(trNow.current.items, target)) { toast(t("chat-navrail.target_failed"), "error"); return; }
      }
      const result = await untilStill<HTMLElement>({
        frame: nextFrame, now: () => performance.now(), cancelled, giveUpMs: GIVE_UP_MS,
        locate: () => {
          const owner = target ? lineOfItem(linesNow.current, trNow.current.items, target.id) : lineId;
          const row = owner ? scroller.current?.querySelector<HTMLElement>(`[data-line="${CSS.escape(owner)}"]`)?.firstElementChild : null;
          return row instanceof HTMLElement ? { y: row.getBoundingClientRect().top, value: row } : null;
        },
      });
      if (result === "cancelled") return;
      if (result === "missing") { toast(t("chat-navrail.target_failed"), "error"); return; }
      landRow(result.value, -12);
    },
    [mid, sid, inputIndex, follow, landRow],
  );
  const railSource = useMemo<NavRailSource>(() => ({ ticks: railTicks, total: inputIndex?.total, select: railJump }), [railTicks, inputIndex?.total, railJump]);
  // the subagents the agent is waiting on (#5602): running, launched in the foreground, their calls not answered yet
  const callsAnswered = useMemo(() => terminalCalls(tr.items, tasks), [tr.items, tasks]);
  const waitingOn = useMemo(() => (assistant ? null : agentsWorking(subagents, callsAnswered)), [assistant, subagents, callsAnswered]);
  // the live work lines' clocks on screen (WorkGroup's Elapsed): while one ticks, the running bar shows no time (#3534)
  const [clocks, setClocks] = useState(0);
  const countClock = useCallback((delta: 1 | -1) => setClocks((n) => n + delta), []);
  // while a message of yours is on its way the live line steps aside (lastLive needs !outgoing): the time stays where it was
  const barClock = useRef(true);
  if (!outgoing) barClock.current = clocks === 0;
  // "Jump to the step that started it" (#2719): open the groups the call is folded in, stop following the end, bring its row to the middle
  // and light it a moment; a call in a folded run of work is fetched first (jump.ts seekCall).
  const expandFold = tr.expandFold;
  const jumpToCall = useCallback(
    async (call: string) => {
      const scope = `${mid}/${sid}:`;
      const frame = () => new Promise<void>((r) => requestAnimationFrame(() => r()));
      const found = await seekCall(scope, call, items.current, expandFold, frame);
      if (!found) return void toast(t("chat-chatview.call_not_loaded"));
      follow(false);
      // the groups just opened grow over the next frames (Collapse): move once the row holds still, the chat's own
      // scroller only (scrollIntoView would also scroll the opening group's clipped box)
      let row: HTMLElement | null = null;
      let last = NaN;
      for (let still = 0, n = 0; n < 40 && still < 3; n++) {
        await frame();
        row = scroller.current?.querySelector<HTMLElement>(`[data-tool-use="${CSS.escape(call)}"]`) ?? null;
        const top = row?.getBoundingClientRect().top ?? NaN;
        still = row && Math.abs(top - last) < 0.5 ? still + 1 : 0;
        last = top;
      }
      const sc = scroller.current;
      if (!row || !sc) return void toast(t("chat-chatview.call_not_loaded"));
      landRow(row, -(sc.clientHeight - row.getBoundingClientRect().height) / 2);
      row.animate?.([{ backgroundColor: "var(--accent-tint)" }, { backgroundColor: "transparent" }], { duration: 1800, easing: "ease-out" });
    },
    [mid, sid, follow, expandFold, landRow],
  );
  const jump = useCallback((call: string) => void jumpToCall(call), [jumpToCall]);
  // A link to one of its subagents' conversations (#5030, lib/subagent-link.ts): that subagent's card in view, opened; a
  // workflow agent's, its run's card. Once the chat and its subagents are in; a subagent no longer listed: just the chat.
  const linkedAgent = useSubagentLink(mid, sid);
  useEffect(() => {
    if (!linkedAgent || !session || tr.loading || detail.loading) return;
    doneSubagentLink(mid, sid);
    const agent = subagents?.find((a) => a.id === linkedAgent);
    const call = agent ? subagentCall(agent) : workflowAgentFor(session, `${sid}@${linkedAgent}`)?.run.tool_use_id;
    if (!call) return;
    if (agent) openExpanded([subagentCardKey(`${mid}/${sid}:`, call)]);
    jump(call);
  }, [linkedAgent, session, tr.loading, detail.loading, subagents, mid, sid, jump]);
  // its workflow runs by the Workflow call that launched each (#3434): rebuilt only when a run starts or goes, not with
  // each push of a run's progress (the cards read that themselves), so the exchanges do not all render again
  const runs = session?.workflows;
  const wfKey = workflowCallsKey(runs);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const wfCalls = useMemo(() => workflowCalls(runs), [wfKey]);
  // Preserve conversation identity; execution summaries have the same collapsed default everywhere (#2602).
  const lineStyle = workLineStyle(assistant, (session ?? archivedHere)?.assistant);
  // an extension opened this conversation (#3758): "From Lark /agent" under its first message, once the page reaches it
  const openedWith = useMemo(() => (assistant ? externalOpening(session, tr.items, tr.hasMore) : undefined), [assistant, session, tr.items, tr.hasMore]);
  const openingId = openedWith?.id;
  const openingOrigin = openedWith?.origin;
  // A stopped agent's closeout is a card above the transcript (#3612, CloseoutCard): while it is there, the reply that
  // carried the raw block, the last exchange's, shows without it.
  const closeoutShown = !assistant && Boolean(shownCloseout(session));
  const closeoutSegment = useMemo(() => {
    if (!closeoutShown) return undefined;
    const last = lines.findLast((line) => line.kind === "segment");
    return last?.kind === "segment" ? last.seg.id : undefined;
  }, [closeoutShown, lines]);
  const ctx = useMemo<ChatContextValue>(
    () => ({ tasks, subagents: assistant ? NO_SUBAGENTS : (subagents ?? NO_SUBAGENTS), scope: `${mid}/${sid}:`, jumpToCall: jump, workflowCalls: wfCalls, lineStyle, opening: openingId && openingOrigin ? { id: openingId, origin: openingOrigin } : undefined, closeoutSegment }),
    [tasks, subagents, assistant, mid, sid, jump, wfCalls, lineStyle, openingId, openingOrigin, closeoutSegment],
  );
  // ── "Search in chat" (ChatSearch.tsx, Android ChatSearchState): where the reader was when it opened, going to a hit (the
  // rows that hold it open, search-reveal.ts), lit for HIT_MS, and "Back to where you were" ──
  const [search, setSearch] = useState<{ open: boolean; query: string }>({ open: false, query: "" });
  const origin = useRef<{ atEnd: boolean; line?: string; offset: number } | null>(null);
  const [picked, setPicked] = useState(false);
  const going = useRef(0);
  // the ring on the hit gone to: one timer, restarted when another (or the same) row lights
  const lit = useRef<{ el: HTMLElement; timer: number } | null>(null);
  const unlight = useCallback(() => {
    if (!lit.current) return;
    clearTimeout(lit.current.timer);
    lit.current.el.removeAttribute("data-search-hit");
    lit.current = null;
  }, []);
  useEffect(() => {
    setSearch({ open: false, query: "" });
    setPicked(false);
    origin.current = null;
    going.current++;
    unlight();
    forgetReveals();
  }, [mid, sid, unlight]);
  const openSearch = useCallback(() => {
    const sc = scroller.current;
    if (sc && !origin.current) {
      // the first line still in view, and how far under the view's top it starts
      origin.current = { atEnd: atEnd.current, ...firstLineInView(sc) };
    }
    setSearch((x) => ({ ...x, open: true }));
  }, []);
  const closeSearch = useCallback(() => {
    setSearch((x) => ({ ...x, open: false }));
    if (!picked) origin.current = null;
  }, [picked]);
  // brings an item into view, opening what holds it, and lights it: a search hit, or a forward's original (#3899)
  const seekItem = useCallback((itemId: string, quiet = false) => {
    const sc = scroller.current;
    if (!sc) return;
    follow(false);
    revealItem(ctx.scope, itemId);
    const run = ++going.current;
    void untilStill<HTMLElement>({
      frame: nextFrame,
      now: () => performance.now(),
      cancelled: () => run !== going.current,
      giveUpMs: GIVE_UP_MS,
      // the rows that hold it take a few frames to open: the line it is on only once they had the time
      locate: (waited) => {
        const exact = sc.querySelector<HTMLElement>(`[data-items~="${CSS.escape(itemId)}"]`);
        const lineId = exact || waited <= FIND_MS ? undefined : lineOfItem(linesNow.current, trNow.current.items, itemId);
        const el = exact ?? (lineId ? sc.querySelector<HTMLElement>(`[data-line="${CSS.escape(lineId)}"]`)?.firstElementChild : null);
        return el instanceof HTMLElement ? { y: el.getBoundingClientRect().top - sc.getBoundingClientRect().top + sc.scrollTop, value: el } : null;
      },
    }).then((r) => {
      if (r === "cancelled") return;
      clearReveal();
      if (r === "missing") return void (quiet || toast(t("chat-chatsearch.not_found")));
      const el = r.value;
      const tall = el.getBoundingClientRect().height > sc.clientHeight;
      el.scrollIntoView({ block: tall ? "start" : "center", behavior: "auto" });
      land(el, el.getBoundingClientRect().top - sc.getBoundingClientRect().top);
      unlight();
      void el.getBoundingClientRect(); // the same row picked again lights again
      el.setAttribute("data-search-hit", "");
      lit.current = { el, timer: window.setTimeout(unlight, HIT_MS) };
    });
  }, [ctx.scope, follow, unlight, land]);
  const goTo = useCallback((hit: ChatSearchHit) => {
    setSearch((x) => ({ ...x, open: false }));
    setPicked(true);
    seekItem(hit.itemId);
  }, [seekItem]);
  // a forward's card opened this chat at its original (#3899, lib/forward.ts jumpWhenOpen): brought into view once the
  // transcript is in and holds it; one further back stays where the chat opens, as iOS does
  useEffect(() => {
    const item = peekJump(mid, sid);
    if (!item || tr.loading) return;
    takeJump(mid, sid);
    if (tr.items.some((i) => i.id === item)) requestAnimationFrame(() => seekItem(item, true));
  }, [mid, sid, tr.loading, tr.items, seekItem]);
  const backToOrigin = useCallback(() => {
    const o = origin.current;
    origin.current = null;
    setPicked(false);
    landing.current = null;
    const run = ++going.current;
    unlight();
    undoReveals();
    const sc = scroller.current;
    if (!sc || !o) return;
    if (o.atEnd) return void requestAnimationFrame(() => requestAnimationFrame(() => run === going.current && toEnd()));
    // what the hits opened closes over a few hundred ms (Collapse): keep the origin line where it was until it holds
    void untilStill<true>({
      frame: nextFrame,
      now: () => performance.now(),
      cancelled: () => run !== going.current,
      giveUpMs: GIVE_UP_MS,
      locate: () => {
        const el = o.line ? sc.querySelector<HTMLElement>(`[data-line="${CSS.escape(o.line)}"]`)?.firstElementChild : null;
        if (!el) return null;
        const scTop = sc.getBoundingClientRect().top;
        sc.scrollTop += el.getBoundingClientRect().top - scTop - o.offset;
        return { y: el.getBoundingClientRect().top - scTop + sc.scrollTop, value: true };
      },
    }).then((r) => r === "missing" && toast(t("chat-chatsearch.origin_changed")));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [unlight]);
  // "Load earlier and keep searching" (lib/chat-search.ts loadEarlierForSearch): the history's own pages, one after another, the
  // view kept still as each lands above it
  const loadForSearch = useCallback(async (onPage: (n: number) => void, stopped: () => boolean): Promise<LoadOutcome> => {
    const outcome = await loadEarlierForSearch({
      get: () => trNow.current,
      load: async () => {
        const sc = scroller.current;
        if (sc) before.current = { h: sc.scrollHeight, top: sc.scrollTop };
        try {
          await trNow.current.loadEarlier();
        } finally {
          // nothing came: forget the anchor, or the next change of the chat jumps to it
          requestAnimationFrame(() => (before.current = null));
        }
      },
      frame: nextFrame,
      stopped,
      onPage,
    });
    const key = SEARCH_NOTICE[outcome];
    return key ? t(key) : null;
  }, []);
  const rounds = useMemo(() => segs.filter((x) => x.user).length, [segs]);

  // the newest plan of the chat (lib/work-groups.ts planSteps): while its card is out of view, a pill above the composer
  const plan = useMemo(() => latestPlan(segs), [segs]);
  const planOut = usePlanOutOfView(scroller, plan?.id);
  // the side panel's Plan follows this chat's newest plan (#5602)
  useEffect(() => publishPlan(mid, sid, plan ? { todos: plan.todos, live: lastLive } : undefined), [mid, sid, plan, lastLive]);
  const lastLine = lines[lines.length - 1];
  // "Retry" on an API error card: only when it is the last thing, the agent is not working (someone may have retried already)
  const retry = useCallback(() => void deliver({ text: t("chat-chatview.continue_text"), images: [] }, uuid()), [deliver]);
  // the failure notice "Retry" was pressed on: kept here, not in the note, so it stays gone while the session goes through
  // needs-you and back before the hub clears it (iOS ChatView @State retried)
  const [retriedOn, setRetried] = useState<{ chat: string; reason?: string }>();
  const retried = retriedOn?.chat === `${mid}/${sid}` ? retriedOn.reason : undefined;
  // a turn that failed on the refusal fallback's missing model (#2528): "Continue" would send it to the same model
  const canRetry =
    lastLine?.kind === "apiError" && state !== "working" && !outgoing && sessionCan(session, "send") && session?.turn_error_code !== FALLBACK_UNAVAILABLE;
  // Claude Code's "while you were away" recap, open in full while it is the newest thing and not closed: the chat's last
  // line, in the flow with the agent's words (#1360: above the composer it covered the end of the reply)
  const recapKey = `${mid}/${sid}`;
  const [recapClosed, setRecapClosed] = useState(() => closedRecap(recapKey));
  useEffect(() => setRecapClosed(closedRecap(recapKey)), [recapKey]);
  const newestRecap = !assistant && lastLine?.kind === "recaps" ? lastLine.items[lastLine.items.length - 1] : undefined;
  const pendingRecap = newestRecap && recapClosed !== newestRecap.id ? newestRecap : undefined;
  const open = useMemo(() => openRecap(lines, pendingRecap), [lines, pendingRecap]);
  const dismissRecap = () => {
    if (!pendingRecap) return;
    closeRecap(recapKey, pendingRecap.id);
    setRecapClosed(pendingRecap.id);
  };
  // On a phone (#1996): what is under the chat stays within about half of the visible height (dock.ts); the keyboard is
  // up while the composer has the focus on a touch screen alone
  const narrow = useMedia(NARROW);
  // a narrow screen's cards fold and its dock keeps to DOCK_MAX (#1282)
  const compactDock = narrow;
  const short = useMedia(SHORT);
  const [typing, setTyping] = useState(false);
  const keyboard = typing && !fine;
  const composerBox = useRef<HTMLDivElement>(null);
  // after a switch the keyboard goes to what is now shown (not on a touch screen: that would raise the keyboard)
  const termLayer = useRef<HTMLDivElement>(null);
  const shown = useRef(view);
  useEffect(() => {
    if (shown.current === view) return;
    shown.current = view;
    if (!fine) return;
    const frame = requestAnimationFrame(() => {
      const box = view === "term" ? termLayer.current : composerBox.current;
      box?.querySelector("textarea")?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [view, fine]);
  // A card answered above the composer takes the focused button with it: the keyboard goes back to the composer (#5154).
  // Not on a touch screen alone (that would raise the keyboard), nor when something else took the focus.
  const dockFocus = useRef<Element | null>(null);
  const onDockFocus = (e: React.FocusEvent) => {
    dockFocus.current = composerBox.current?.contains(e.target) ? null : e.target;
  };
  useEffect(() => {
    if (!lostWith(dockFocus.current, document.activeElement, document.body)) return;
    dockFocus.current = null;
    if (fine) composerBox.current?.querySelector("textarea")?.focus({ preventScroll: true });
  });
  // the run's time counts from the turn's start, as its "Working…" does (not from the session's last state change)
  const stateSince = session?.state_since;
  const runStart = useMemo(() => {
    const live = lines.flatMap((l) => l.kind === "supervision" ? l.lines : [l]).findLast((l) => l.kind === "segment" && l.live);
    return (live?.kind === "segment" ? turnStart(live.seg) : undefined) ?? stateSince;
  }, [lines, stateSince]);
  const planPill = plan && planOut && (lastLive || !planProgress(plan.todos).allDone) ? (
    <PlanPill todos={plan.todos} onClick={() => openPanel({ kind: "plan", mid, sid })} />
  ) : null;
  // Persona's own chats too (#3654), and from the moment the message is in, not from the hub's next push
  const background = useMemo(() => backgroundLine(session), [session]);
  const wait = useMemo(() => (outgoing || awaiting ? null : backgroundWaitLine(session)), [outgoing, awaiting, session]);
  const running = session ? <RunningStatusBar mid={mid} sid={sid} working={state === "working" || awaiting} startedAt={runStart} clock={barClock.current} agents={waitingOn} background={background} wait={wait} subtasks={subtasks && !outgoing && !awaiting} /> : null;
  const queue =
    session && queued.length > 0 ? (
      <QueueStrip session={session} draftKey={draftKey} canEdit={canCompose} compact={short} persona={assistant ? persona.name : undefined} onWithdrawn={withdrawn}
        photosOf={assistant ? queuedPhotos : undefined} />
    ) : null;
  // with the plan's pill showing, "Jump to bottom" goes beside it (over the chat, they hid each other)
  const jumpInDock = Boolean(planPill);
  const pills = jumpInDock && away ? (
    <div className="flex shrink-0 items-center justify-center gap-2">
      {planPill}
      <JumpPill label={jumpLabel(newSince)} onClick={toEnd} inline />
    </div>
  ) : planPill;
  // the header and the composer are memoized (a transcript push changes neither): what they get stays the same object
  // until it changes. The assistant's face reads only the last item's role and kind (lib/persona.ts personaState).
  const stopLoop = useCallback(() => void deliver({ text: STOP_LOOP_MESSAGE, images: [] }, uuid()), [deliver]);
  // cached content stays up when a read fails: the pill says so, the error page is for an empty chat
  const refreshFailed = tr.items.length > 0 ? tr.error || refreshError : undefined;
  const lastItem = assistant ? tr.items[tr.items.length - 1] : undefined;
  const lastRole = lastItem?.role;
  const lastKind = lastItem?.kind;
  const faceLast = useMemo(() => (lastRole && lastKind ? { role: lastRole, kind: lastKind } : undefined), [lastRole, lastKind]);
  // the main conversation's /clear (#5095, lib/main-clear.ts): Persona's other conversations take no commands
  const commands = useMemo(() => (assistant ? (clears ? mainCommands() : []) : agentCommands(AGENT_COMMANDS, session)), [assistant, clears, session]);
  const footer = useMemo(() => (session ? <ComposerFooter session={session} assistant={assistant} /> : null), [session, assistant]);
  // an Agent's model pill (#5032); a Persona conversation keeps its launcher chip in the footer for now (#5033)
  const trailing = useMemo(() => (session && !assistant ? <AgentModelPill session={session} /> : null), [session, assistant]);

  const pendingCards = (
    <PendingCards
      interactions={focusedFirst(interactions, focusedCard())}
      answered={answered}
      delegated={delegated}
      offerNext={inPage && !assistant ? { mid, sid } : undefined}
      narrow={compactDock}
      short={short}
      keyboard={keyboard}
      onDismissKeyboard={() => composerBox.current?.querySelector("textarea")?.blur()}
    />
  );

  const page = (
    <div className="flex h-full min-w-0 flex-col">
      <Header
        session={session}
        title={title}
        machineName={machineName}
        assistant={assistant}
        main={isMain}
        siblings={siblings}
        onToggleSidebar={onToggleSidebar}
        onInterrupt={interrupt}
        onStopLoop={stopLoop}
        onJumpToCall={jump}
        last={faceLast}
        onSearch={openSearch}
        topCenter={topCenter}
        view={switchable ? view : undefined}
        onView={switchable ? setView : undefined}
        viewDot={view === "term" && (interactions.length > 0 || state === "needs_you")}
        actions={headerActions}
      />
      {isMain && <MainRuntimeSlot sid={sid} />}
      {banner}
      {cameFrom && <Suspense fallback={null}><HandoffBack from={cameFrom} onDismiss={() => setCameFrom(undefined)} /></Suspense>}
      {/* a known session keeps its chat: a failed background read is retried quietly (runtime-session.ts) */}
      {archivedHere ? <ArchivedNotice session={archivedHere} /> : noDetails && <SessionDetailNotice error={detail.error} retry={retryDetail} />}
      {session && panes.length > 1 && <PaneTabs sessions={panes} current={sid} onSelect={(s) => navigate(withWorkView(paneRoute(s), terminalAvailable !== undefined), true)} />}

      {/* the page's middle: the chat and (once shown) the terminal stacked in one box; without a terminal to show it adds no box */}
      <div className={switchable ? "relative min-h-0 flex-1" : "contents"}>
      <ViewLayer bare={!switchable} on={view === "chat"} from="start">
      {/* what a stopped agent said of its own work, above the transcript and not part of it (#3612) */}
      {closeoutShown && session && <CloseoutCard key={`${mid}/${sid}`} session={session} />}
      <div className="relative min-h-0 flex-1">
        {(tr.notLive || refreshFailed) && <NotLivePill error={refreshFailed} onReload={() => { tr.reload(); onRefresh?.(); }} lower={Boolean(picked && !search.open)} />}
        <div ref={scrollRef} onScroll={onScroll} data-chat-scroll className="relative h-full overflow-y-auto [overflow-anchor:none]">
          <ChatActionsContext.Provider value={actions}>
          <div data-chat-content className="mx-auto flex w-full max-w-[880px] flex-col gap-7 px-5 pt-6 pb-10 sm:px-8">
            {session?.start_error && (session.start_error_code ? (
              // a refusal with a code (#1642): the hub's whole sentence in the user's language, its remedy last, under a title
              <div role="alert" data-testid="start-error-coded" className="rounded-control bg-inset px-3 py-2 text-[13px]">
                <div className="font-medium text-red">{t("chat-chatview.start_failed_title")}</div>
                <p className="mt-0.5 leading-relaxed text-ink-2 [overflow-wrap:anywhere]">{session.start_error}</p>
              </div>
            ) : <div role="alert" className="rounded-control bg-inset px-3 py-2 text-[13px] text-red">{t("chat-chatview.start_failed", { error: session.start_error })}</div>)}
            {starting && !tr.loading && !outgoing && <Loading label={t("chat-chatview.starting_agent")} />}
            {tr.loadingEarlier && (
              <div className="flex justify-center py-2">
                <Loading label={t("chat-chatview.loading_earlier")} />
              </div>
            )}
            {/* also over a chat whose first page came back empty (#2450): it reads as loaded and empty, not as still loading;
                not under a notice that the chat's details are missing */}
            {((!tr.hasMore && !tr.loading && (tr.items.length > 0 || (!tr.error && !starting && !session?.start_error && !noDetails))) || opening) && <div className="text-center text-[11.5px] text-ink-3">{session?.cwd && !assistant ? t("chat-chatview.started_in", { path: shortPath(session.cwd) }) : t("chat-chatview.chat_start")}</div>}
            {opening && <DayLine date={startOfDay(Date.now())} />}
            {tr.loading && !tr.items.length && !opening && (
              <div className="flex justify-center py-16">
                <Loading label={starting ? t("chat-chatview.preparing") : t("chat-chatview.loading_chat")} />
              </div>
            )}
            {tr.error && !tr.items.length && (
              <div className="flex flex-col items-center gap-3 py-16 text-center">
                <p className="text-[13px] text-ink-2">{tr.error}</p>
                <Button onClick={tr.reload} icon="retry">
                  {t("chat-chatview.retry")}
                </Button>
              </div>
            )}
            <LiveClockContext.Provider value={countClock}>
            <ChatContext.Provider value={ctx}>
              <CardConversation.Provider value={assistant ? sid : ""}>
              {assistant
                ? displayLines(lines, feed, tr.hasMore ? loadedFrom(tr.items) : undefined).flatMap((d) => [
                    ...failedBefore(d.kind === "chat" ? d.id : ""),
                    d.kind === "feed" ? (
                      <FeedLineView key={d.id} item={d.item} simplified />
                    ) : d.kind === "quick" ? (
                      <QuickResultCard key={d.id} result={d.result} />
                    ) : d.kind === "day" ? (
                      <DayLine key={d.id} date={d.date} />
                    ) : (
                      <ChatLineView
                        key={d.id}
                        line={d.line}
                        mid={mid}
                        sid={sid}
                        streaming={(d.line.kind === "segment" || d.line.kind === "supervision") && d.line.live ? tr.streaming || undefined : undefined}
                        expandFold={tr.expandFold}
                        onRetry={canRetry && d.line.id === lines.at(-1)?.id ? retry : undefined}
                        onEditLastMessage={d.line.id === lines.at(-1)?.id ? onEditLastMessage : undefined}
                        fix={d.line.id === lines.at(-1)?.id ? turnFix : undefined}
                        limit={d.line.kind === "apiError" && d.line.id === limitOn ? limitLine : undefined}
                        assistant={assistant}
                        offscreen={offscreen(d.line)}
                      />
                    ),
                  ])
                : lines.flatMap((line, i) => [...failedBefore(line.id), open?.line === line.id ? (
                <OpenRecapLine key={line.id} open={open} onClose={dismissRecap} />
              ) : (
                <ChatLineView
                  key={line.id}
                  line={line}
                  // only a live line streams: the others keep their memo
                  mid={mid}
                  sid={sid}
                  streaming={(line.kind === "segment" || line.kind === "supervision") && line.live ? tr.streaming || undefined : undefined}
                  expandFold={tr.expandFold}
                  onRetry={canRetry && i === lines.length - 1 ? retry : undefined}
                  onEditLastMessage={i === lines.length - 1 ? onEditLastMessage : undefined}
                  limit={line.kind === "apiError" && line.id === limitOn ? limitLine : undefined}
                  assistant={assistant}
                  offscreen={offscreen(line)}
                />
              )])}
              </CardConversation.Provider>
            </ChatContext.Provider>
            </LiveClockContext.Provider>
            {/* what is on its way, and failed messages with nothing sent after them, in the order they were written */}
            {[...shownPending.map((p) => ({ at: p.at, el: (
              <div key={p.id} className="flex flex-col gap-3">
                <UserBubble msg={p.msg} mid={mid} sid={sid} pending sendState="sending" />
              </div>
            ) })), ...placed.rest.map((e) => ({ at: e.at, el: <OutboxBubbles key={`outbox:${e.id}`} mid={mid} sid={sid} entries={[e]} canRetry={!refusedNoRow} /> }))]
              .sort((a, b) => a.at - b.at)
              .map((x) => x.el)}
            {(shownPending.length > 0 || outbox.some((e) => e.state === "sending") || (starting && outgoing > 0)) && (
              <div className="flex items-center gap-2.5">
                <Loading label={starting ? t("chat-chatview.preparing") : shownPending.some((p) => p.waits) ? t("ultracode.send_waits") : online ? t("chat-chatview.delivering") : t("chat-chatview.waiting_for_hub")} />
              </div>
            )}
            {voiceState && <VoiceTail owner={voiceOwner} persona={persona.name} />}
          </div>
          <CommentMarks scroller={scroller} jump={landRow} />
          </ChatActionsContext.Provider>
        </div>
        {/* A transient edge overlay; native scrolling and the centered text column keep their existing layout. */}
        <NavRail ticks={railSource.ticks} total={railSource.total} scroller={scrollEl} onJump={railSource.select} reset={`${mid}/${sid}`} />
        {away && !jumpInDock && <JumpPill label={jumpLabel(newSince)} onClick={toEnd} />}
        {picked && !search.open && <ReturnPill onClick={backToOrigin} />}
      </div>

      <div
        data-chat-dock
        // at most DOCK_MAX on every width (#5375): what sits over the composer gives up its height first and scrolls in
        // its own box, the chat above shrinks by what the dock takes and is never drawn over. Should the composer alone
        // need more (a keyboard leaving little room), it grows up over the chat (justify-end) instead of down off the screen.
        className="mx-auto flex max-h-(--dock-max) w-full max-w-[880px] flex-col justify-end gap-2 px-4 pb-4 sm:px-7 [@media(max-height:500px)]:pb-2"
        style={{ "--dock-max": DOCK_MAX } as React.CSSProperties}
        onInput={pendingRecap ? dismissRecap : undefined}
        onFocus={onDockFocus}
      >
        {/* everything over the composer: it shrinks and scrolls before the composer does (the composer grew, and its
            cards, task questions and notes stood taller than the dock, drawn up over the chat or pushing it to nothing);
            the box clips what is in it, so its children keep a little room for their shadows (p-1; -m-1 gives it back) */}
        <div data-chat-dock-above data-scroll-box className="-m-1 flex min-h-0 shrink flex-col gap-2 overflow-y-auto overscroll-contain p-1 empty:hidden">
        {short ? (
          // a phone on its side: the plan's pill, the run's status and the queue share one line
          (pills || running || queue) && (
            <div className="flex shrink-0 items-center gap-2">
              {pills && <div className="shrink-0">{pills}</div>}
              {running && <div className="shrink-0">{running}</div>}
              {queue && <div className="min-w-0 flex-1">{queue}</div>}
            </div>
          )
        ) : (
          <>
            {pills}
            {running}
          </>
        )}
        {assistant && (
          <>
            {voiceState === "live" && <TaskQuestions conversation={sid} />}
            {/* The conversation's own cards are in the chat under the bar (#4883, as iOS): Needs you counts them and
                lists a row for each that closes the tray onto its card. */}
            {voiceState !== "live" && <AssistantActionBar key={sid} conversation={sid} ownAsks={interactions} />}
            {voiceState !== "live" && <RouteChip conversation={sid} />}
          </>
        )}
        {pendingCards}
        {!assistant && session?.model_fallback && <ModelFallbackCard session={session} headless={false} />}
        {assistant && session && <CompactionStrip session={session} />}
        {failure ? <FailureCard failure={failure} onClose={() => setSendFailure(null)} onHelp={gate.help} onConfirm={gate.confirm} onRetry={failedEntry && !refusedNoRow && sessionCan(session, "send") && !gate.blocked ? () => void retryOutbox(mid, sid, failedEntry.id) : undefined} /> : <ReadinessCard gate={gate} />}
        <ChatOnlyNote gate={gate} />
        {gate.dialog}
        {session && !assistant && <AttentionNote session={session} />}
        {session && !assistant && <ConflictNote session={session} />}
        {/* a resume turned ultracode off (#3428): one line with "Turn back on" */}
        {session && !assistant && <UltracodeLostNote session={session} />}
        {session && !assistant && showsFailureNote(session, { retried }) && (
          <FailureNote
            session={session}
            onRetry={() => {
              setRetried({ chat: `${mid}/${sid}`, reason: failureReason(session) });
              retry();
            }}
          />
        )}
        {!short && queue}
        {voiceState === "ended" && <VoiceEnded owner={voiceOwner} />}
        </div>
        {voiceEntry.dialog}
        {voiceState === "live" && voiceCall && (
          <div className="shrink-0">
            <VoiceBar call={voiceCall} session={session} cards={interactions} label={voiceLabel} persona={persona.name} onStopAgent={interrupt} />
          </div>
        )}
        {/* kept while a call is on (its draft, attachments and quotes stay), the bar in its place */}
        <div ref={composerBox} className="shrink-0" hidden={voiceState === "live"}>
        {stoppedLine ? (
          <StoppedAgentLine mid={mid} sid={sid} onTerminal={openTerminal} />
        ) : (
        <Composer
          draftKey={draftKey}
          placeholder={offlineLine ? offlineLine : refusedNoRow ? t(gone ? "chat-chatview.session_gone_placeholder" : "chat-chatview.connecting_placeholder") : viewOnlyRow(session) ? t("chat-chatview.view_only") : state === "working" || state === "needs_you" ? t("chat-chatview.queue_placeholder") : assistant ? t("chat-chatview.ask_placeholder", { name: persona.name }) : state === "offline" ? t("chat-chatview.resume_placeholder") : t("chat-chatview.agent_placeholder")}
          machineId={mid}
          uploadDest={session?.space === "health" ? "health" : undefined}
          working={state === "working" || awaiting}
          needsYou={state === "needs_you"}
          onSend={send}
          disabled={Boolean(offlineLine) || (!noRow && !sessionCan(session, "send"))}
          onEditLastMessage={onEditLastMessage}
          editingLast={editProp}
          loadingDraft={editingLast}
          // Persona's conversations too, the main one included (#1467): the node stops their turn like an agent's
          onStop={sessionCan(session, "interrupt") ? interrupt : undefined}
          stopping={stopping}
          commands={commands}
          suggestion={session?.suggestion}
          mention={mention}
          autoFocus
          onFocusChange={setTyping}
          blocked={gate.blocked ?? noRowBlock}
          footer={footer}
          trailing={trailing}
          voice={voiceEntry.offer}
        />
        )}
        </div>
      </div>
      </ViewLayer>
      {switchable && termKept && (
        <ViewLayer on={view === "term"} from="end" enter layerRef={termLayer}>
          {/* Files opens beside it (the Files panel, as on the "Terminal" page); the page's header is the panel's, so the panel keeps a toolbar of its own controls ("Single pane" / "Full view"…) */}
          <PageBoundary page={`term:${mid}/${sid}`}>
            <Suspense fallback={<div className="flex h-full items-center justify-center"><Loading /></div>}>
              <TerminalPanel mid={mid} sid={sid} onFiles={session?.cwd ? openFiles : undefined} onClose={() => setView("chat")} embedded />
            </Suspense>
          </PageBoundary>
        </ViewLayer>
      )}
      </div>

      <ChatSearch
        open={search.open}
        query={search.query}
        onQuery={(query) => setSearch((x) => ({ ...x, query }))}
        items={tr.items}
        rounds={rounds}
        hasMore={tr.hasMore}
        onLoadEarlier={loadForSearch}
        onPick={goTo}
        onClose={closeSearch}
      />
      <Dialog
        open={Boolean(draftRefusal)}
        onClose={() => setDraftRefusal(null)}
        title={t("chat-chatview.draft_title")}
        footer={
          <>
            <Button tone="quiet" onClick={() => setDraftRefusal(null)}>
              {t("chat-chatview.not_now")}
            </Button>
            {draftRefusal?.clearable !== undefined && (
              <Button
                tone="primary"
                onClick={() => {
                  const r = draftRefusal!;
                  setDraftRefusal(null);
                  if (!refusedNoRow) void retryOutbox(mid, sid, r.id, r.clearable); // under a new id: the refused one keeps its answer
                }}
              >
                {t("chat-chatview.clear_and_send")}
              </Button>
            )}
          </>
        }
      >
        <p className="text-[13px] leading-relaxed text-ink-2">{t("chat-chatview.draft_detail")}{draftRefusal?.clearable === undefined && t("chat-chatview.draft_multiline")}</p>
        {draftRefusal?.draft && <pre className="mt-2.5 max-h-40 overflow-auto rounded-[8px] bg-inset px-2.5 py-2 font-mono text-[12px] whitespace-pre-wrap text-ink shadow-hairline">{draftRefusal.draft}</pre>}
      </Dialog>
    </div>
  );
  // a link tapped in the chat opens what it opens as the chat's manager
  return <LinkManager.Provider value={session?.manager}>{page}</LinkManager.Provider>;
}

/** Over the chat's top edge when the page keeps getting its items by asking for them, not by push (lib/stale-page.ts). */
function NotLivePill({ onReload, lower, error }: { onReload: () => void; /** The search's return pill is at the same edge: under it. */ lower?: boolean; error?: string }) {
  return (
    <div role="status" data-testid="chat-not-live" className={lower ? "absolute top-14 left-1/2 z-10 -translate-x-1/2" : "absolute top-3 left-1/2 z-10 -translate-x-1/2"} style={{ animation: "fade-up 150ms ease-out both" }}>
      <div className="flex h-8 items-center gap-2 rounded-full border border-line bg-surface px-3 text-[12px] leading-none whitespace-nowrap text-ink-2 shadow-raised">
        <span title={error}>{t(error ? "chat-chatview.refresh_failed" : "chat-chatview.not_live")}</span>
        <button type="button" onClick={onReload} className="font-medium text-accent-ink hover:underline">
          {t("chat-chatview.not_live_reload")}
        </button>
      </div>
    </div>
  );
}

/** After going to a search hit: back to where the reader was when the search opened (Android ReturnChip). */
function ReturnPill({ onClick }: { onClick: () => void }) {
  return (
    <div className="absolute top-3 left-1/2 z-10 -translate-x-1/2" style={{ animation: "fade-up 150ms ease-out both" }}>
      <button
        type="button"
        onClick={onClick}
        data-search-return
        className="flex h-8 items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1.5 text-[12px] leading-none font-medium whitespace-nowrap text-ink-2 shadow-raised transition-colors duration-150 hover:bg-hover active:scale-[0.98]"
      >
        <Icon name="arrowLeft" size={14} className="shrink-0" />
        <span className="translate-y-[0.5px]">{t("chat-chatsearch.return")}</span>
      </button>
    </div>
  );
}

/** Away from the end: "New messages" / "Jump to bottom" over the chat's bottom edge (the reference client's JumpToBottomChip). */
function JumpPill({ label, onClick, inline }: { label: string; onClick: () => void; /** In a row (beside the plan's pill), not over the chat. */ inline?: boolean }) {
  return (
    <div aria-live="polite" className={inline ? "shrink-0" : "absolute bottom-3 left-1/2 z-10 -translate-x-1/2"} style={{ animation: "fade-up 150ms ease-out both" }}>
      <button
        type="button"
        onClick={onClick}
        aria-label={label}
        data-jump-pill
        className="flex h-8 items-center gap-1.5 rounded-full border border-line bg-surface px-3 py-1.5 text-[12px] leading-none font-medium whitespace-nowrap text-ink-2 shadow-raised transition-colors duration-150 hover:bg-hover active:scale-[0.98]"
      >
        <Icon name="arrowDown" size={14} className="shrink-0" />
        <span className="translate-y-[0.5px]">{label}</span>
      </button>
    </div>
  );
}

export function ComposerFooter({ session, assistant }: { session: Session; assistant?: boolean }) {
  // the launcher and model are the composer's model pill (an Agent's, #5032) or the launcher chip (a Persona
  // conversation's): they switch there, so the line keeps only where the chat comes from and runs
  const parts = [sourceSubtitle(session), ...(assistant ? [] : [shortPath(session.cwd)])].filter(Boolean);
  const info = useMenu();
  // a Persona conversation (the main one too) switches its modes whatever its state: the hub applies them (#2416). An
  // Agent's effort (a Codex thread's too) is on its model pill
  const modes = assistant || (hasModes(session) && stateOf(session) !== "offline");
  return (
    <span className="flex min-w-0 items-center gap-2">
      {modes && <ModeChips session={session} assistant={assistant} withInfo={parts.length > 0} />}
      {assistant && <AssistantLauncherChip session={session} />}
      {parts.length > 0 && <span className="min-w-0 truncate max-sm:hidden" title={[sourceSubtitle(session), session.cwd].filter(Boolean).join(" · ")}>{parts.join(" · ")}</span>}
      {/* phone width (#3533): the line would show a letter or two, so it is a button that lists the parts in full */}
      {parts.length > 0 && <IconButton icon="info" label={t("chat-chatview.session_info")} onClick={info.open} active={Boolean(info.anchor)} size={22} iconSize={13} className="sm:hidden" />}
      {info.anchor && <Menu anchor={info.anchor} onClose={info.close} side="top" align="right" width={280} header={t("chat-chatview.session_info")} items={footerRows(session, assistant)} />}
    </span>
  );
}

/** The composer footer's parts as menu rows (#3533): the value in full (wrapped), what it is beside it; choosing one
 *  copies it, except the source, which is a sentence to read rather than a value to paste. The launcher and model are
 *  the model pill's (#5032). */
export function footerRows(session: Session, assistant?: boolean): MenuItem[] {
  const rows: [string, string, string | undefined][] = [
    ["source", t("chat-chatview.composer_source"), sourceSubtitle(session)],
    ...(assistant ? [] : ([["folder", t("chat-sessiondialogs.folder"), shortPath(session.cwd)]] as [string, string, string | undefined][])),
  ];
  return rows.filter(([, , v]) => v).map(([key, what, v]) => ({ key, label: v!, hint: what, wrap: true, onSelect: key === "source" ? () => {} : () => void copyText(key === "folder" ? session.cwd! : v!) }));
}

/** The session is live in another engine host too (#2576): this one owns it, and messages go here only. */
function ConflictNote({ session }: { session: Session }) {
  const text = alsoOpenText(session);
  if (!text) return null;
  return (
    <div role="status" data-testid="chat-also-open" className="flex items-start gap-2 rounded-[12px] bg-orange-tint px-3 py-2 text-[12.5px]" style={{ animation: "fade-up 200ms ease-out both" }}>
      <span className="flex h-[18px] shrink-0 items-center text-orange"><Icon name="warning" size={13} /></span>
      <span className="line-clamp-2 min-w-0 flex-1 text-ink-2">{text}</span>
    </div>
  );
}

/** Above the composer while a session needs you and its host says why (a botmux attention, iOS AttentionNote):
 *  the reason and "Open in Lark" its topic. */
function AttentionNote({ session }: { session: Session }) {
  const reason = attentionReason(session);
  if (!reason) return null;
  const link = externalLink(session);
  return (
    <div role="status" data-testid="chat-attention" className="flex items-start gap-2 rounded-[12px] border-l-2 border-attention bg-inset px-3 py-2 text-[12.5px]" style={{ animation: "fade-up 200ms ease-out both" }}>
      <span className="flex h-[18px] shrink-0 items-center"><StatusMark state="needs_you" /></span>
      <span className="line-clamp-3 min-w-0 flex-1 text-ink-2">{reason}</span>
      {link && (
        <a href={link} target="_blank" rel="noopener noreferrer" className="flex shrink-0 items-center gap-1 whitespace-nowrap font-medium text-accent hover:underline">
          <Icon name="external" size={12} />
          {t("chat-chatview.open_in_lark")}
        </a>
      )}
    </div>
  );
}

// memo: a chat renders again for each transcript item, and the header shows none of them
export const Header = memo(function Header({
  session,
  title,
  machineName,
  assistant,
  main,
  siblings,
  onToggleSidebar,
  onInterrupt,
  onStopLoop,
  onJumpToCall,
  last,
  onSearch,
  topCenter,
  view,
  onView,
  viewDot,
  subtitle,
  actions,
}: {
  session?: Session;
  title: string;
  machineName: string;
  assistant?: boolean;
  /** The assistant's main conversation: no subtitle (iOS's AssistantTopBar has none) and no "Rename". */
  main?: boolean;
  siblings: Session[];
  onToggleSidebar?: () => void;
  onInterrupt: () => void;
  onStopLoop: () => void;
  /** Brings the step with this tool_use_id into view (a workflow's "Show where it started", #2719). */
  onJumpToCall?: (call: string) => void;
  /** The assistant's face: the last item's role and kind. */
  last?: Pick<Item, "role" | "kind">;
  /** "Search in chat" (ChatSearch.tsx). */
  onSearch?: () => void;
  topCenter?: React.ReactNode;
  /** On an agent's page with a terminal to show: the view in the middle, and how to change it (the "Terminal" | "Chat" switch). */
  view?: SessionView;
  onView?: (view: SessionView) => void;
  /** The conversation has something that wants an answer while the terminal is shown. */
  viewDot?: boolean;
  /** A conversation still starting has no session to make its subtitle from: what it will say (the new-conversation page, #1697). */
  subtitle?: string;
  /** The page's own buttons, before the permission mode and ⋯ (ChatView `headerActions`). */
  actions?: React.ReactNode;
}) {
  const menu = useMenu();
  const tabs = useMenu();
  const panel = usePanel();
  const mutes = useStore((s) => s.mutes);
  const [closing, setClosing] = useState(false);
  const [archiveClosing, setArchiveClosing] = useState(false);
  const features = useStore((s) => s.features);
  // archived (the Archived page listed it, or it was archived here): the menu offers Restore instead of archive / delete
  const archived = useArchived(session?.machine_id ?? "", session?.session_id ?? "");
  const [dialog, setDialog] = useState<"info" | "rename" | "space" | "delete" | null>(null);
  const muted = Boolean(session && !assistant && isMuted(session, mutes));
  const pinned = useStore((s) => Boolean(session && isPinned(session, s.pins)));
  // "Mute": the hub stops pushing this agent's "Needs you" / "Finished" (an agent's switch sits on top of its space's)
  const flipMute = async () => {
    if (!session) return;
    const next = toggleMute(session, getState().mutes);
    try {
      await saveMutes(next);
      toast(isMuted(session, next) ? t("chat-chatview.muted") : t("chat-chatview.unmuted"), "ok");
    } catch (e) {
      toast(t("chat-chatview.change_failed", { error: errorText(e) }), "error");
    }
  };
  const [linkTodoOpen, setLinkTodoOpen] = useState(false);
  const [sharingOpen, setSharingOpen] = useState(false);
  const state = session ? stateOf(session) : "unknown";
  // Awaiting Reply (#5096): seen, but it asked you something you have not answered
  const awaiting = session ? awaitsReply(session, mutes) : false;
  // a Persona conversation's subtitle ends with its age: the memo keeps pushes from drawing the header again, so a minute
  // tick does (docs/lessons/web.md, SessionRow)
  useNow(60_000, Boolean(!main && assistant && session?.updated_at));
  const place = subtitle !== undefined
    ? subtitle
    : main
    ? ""
    : assistant
    ? [sourceSubtitle(session), session?.launcher, session?.updated_at ? ago(session.updated_at) : ""].filter(Boolean).join(" · ")
    : [sourceSubtitle(session), machineName, session?.space_name, session && tabLabel(session)].filter(Boolean).join(" › ");
  // A subscription usage limit waiting out its reset, in place of reading the turn as interrupted.
  useNow(30_000, session?.turn_error_kind === "usage_limit");
  const limitLine = session ? usageLimitLabel(session) : undefined;
  const sub = [limitLine, place].filter(Boolean).join(" · ");
  const allowed = session ? sessionMenuActions(session) : [];
  const attach = () => session && attachLive(session);
  const others = siblings.filter((s) => s.session_id !== session?.session_id);
  const todoLine = Boolean(session?.todo && !assistant);
  // the session's files and Git open in the side panel (docs/web/web.md "Side panel"); the assistant has none. Its terminal is
  // the page's other view, switched in place (view / onView)
  const here = (p: typeof panel, kind: string) => Boolean(p && p.kind === kind && "sid" in p && session && p.mid === session.machine_id && p.sid === session.session_id);
  const showTerminal = Boolean(view && onView);
  const showFiles = Boolean(session && !assistant && session.cwd);
  const flipView = () => view && onView?.(otherView(view));
  const openFiles = () => session && togglePanel({ kind: "files", mid: session.machine_id, sid: session.session_id, cwd: session.cwd });
  const openGit = () => session && togglePanel({ kind: "git", mid: session.machine_id, sid: session.session_id, cwd: session.cwd });
  // the session's dev server, from a browser on its computer (docs/web/preview.md); a hub without previews offers none
  const previews = useStore((s) => hubHas(s.features, PREVIEW_FEATURE));
  const openPreview = () => session && togglePanel({ kind: "preview", mid: session.machine_id, sid: session.session_id, cwd: session.cwd });
  const openReview = () => session && togglePanel({ kind: "review", mid: session.machine_id, sid: session.session_id, cwd: session.cwd });
  const reviews = useStore((s) => hubHas(s.features, REVIEW_FEATURE));
  const othersNeed = others.some((s) => stateOf(s) === "needs_you");
  const phone = useMedia(PHONE);
  const spaceRows: MenuItem[] = siblings
    .slice()
    .sort((a, b) => (a.slot_index ?? 0) - (b.slot_index ?? 0))
    .map((s) => ({
      key: s.session_id,
      label: `${tabLabel(s) || sessionTitle(s)} · ${sessionTitle(s)}`,
      state: stateOf(s),
      checked: s.session_id === session?.session_id,
      onSelect: () => navigate({ name: "session", mid: s.machine_id, sid: s.session_id }),
    }));
  const { items: assistantItems, dialogs: assistantDialogs } = useAssistantMenu(assistant ? session : undefined);
  const feishu = externalLink(session);

  const own: MenuItem[] = session
    ? [
        // phone width: "Same space" leaves the header row to the title (#3576) and opens from ⋯ instead
        ...(phone && others.length > 0 ? [spaceAgentsItem(others, () => tabs.openAt(menu.anchor))] : []),
        ...assistantItems,
        ...(!assistant && !main ? [{ key: "share", label: t("chat-chatview.share"), icon: "link" as const, onSelect: () => setSharingOpen(true) }] : []),
        ...(session.todo
          ? [
              {
                key: "todo-detail",
                label: t("chat-chatview.todo_label", { title: session.todo.title }),
                icon: "todo" as const,
                onSelect: () => navigate(`/todos?id=${encodeURIComponent(session.todo!.id)}`),
              },
              {
                key: "unlink-todo",
                label: t("chat-chatview.unlink_todo"),
                icon: "x" as const,
                onSelect: async () => {
                  try {
                    await unlinkAgentFromTodo(session.todo!.id, session.machine_id, session.session_id);
                    await refreshTodos();
                    await refresh();
                    toast(t("chat-chatview.todo_unlinked"), "ok");
                  } catch (e) {
                    toast(t("chat-chatview.unlink_failed", { error: errorText(e) }), "error");
                  }
                },
              },
              {
                key: "relink-todo",
                label: t("chat-chatview.relink_todo"),
                icon: "todo" as const,
                onSelect: () => setLinkTodoOpen(true),
              },
            ]
          : !assistant
            ? [
                {
                  key: "link-todo",
                  label: t("chat-chatview.link_todo"),
                  icon: "todo" as const,
                  onSelect: () => setLinkTodoOpen(true),
                },
              ]
            : []),
        ...(allowed.includes("attach") ? [{ key: "attach", label: t("chat-chatview.attach"), icon: "link" as const, onSelect: () => void attach() }] : []),
        ...(showTerminal ? [{ key: "terminal", label: t("chat-chatview.terminal"), icon: "terminal" as const, checked: view === "term", onSelect: flipView }] : []),
        ...(showFiles ? [{ key: "files", label: t("chat-chatview.files"), icon: "folder" as const, checked: here(panel, "files"), onSelect: openFiles }, { key: "git", label: "Git", icon: "git" as const, checked: here(panel, "git"), onSelect: openGit }, ...(reviews ? [{ key: "review", label: t("chat-chatview.review_changes"), icon: "pullRequest" as const, checked: here(panel, "review"), onSelect: openReview }] : [])] : []),
        ...(showFiles && previews ? [{ key: "preview", label: t("chat-chatview.preview"), icon: "web" as const, checked: here(panel, "preview"), onSelect: openPreview }] : []),
        ...(allowed.includes("stop") ? [{ key: "stop", label: t("chat-chatview.interrupt"), icon: "stop" as const, hint: "Esc", onSelect: onInterrupt }] : []),
        ...(feishu ? [{ key: "feishu", label: t("chat-chatview.open_in_lark"), icon: "external" as const, onSelect: () => openLink(feishu) }] : []),
        ...(onSearch ? [{ key: "search", label: t("chat-chatsearch.menu"), icon: "search" as const, onSelect: onSearch }] : []),
        { key: "info", label: t("chat-chatview.session_info"), icon: "info", onSelect: () => setDialog("info") },
        ...(!main && sessionCan(session, "send") && (session.assistant || !session.source_kind) ? [{ key: "rename", label: t("chat-chatview.rename"), icon: "pencil" as const, onSelect: () => setDialog("rename") }] : []),
        ...(!assistant && session.space_id && !session.source_kind ? [{ key: "space", label: t("chat-chatview.rename_space"), icon: "folder" as const, onSelect: () => setDialog("space") }] : []),
        ...(!assistant ? [{ key: "mute", label: muted ? t("chat-chatview.unmute") : t("chat-chatview.mute"), icon: muted ? ("bell" as const) : ("bellOff" as const), onSelect: () => void flipMute() }] : []),
        ...(!assistant ? [{ key: "pin", label: pinned ? t("chat-chatview.unpin") : t("chat-chatview.pin"), icon: "pin" as const, onSelect: () => void flipPin(session) }] : []),
        { key: "sid", label: t("chat-chatview.copy_session_id"), icon: "copy", onSelect: () => copyText(session.session_id) },
        ...(session.cwd ? [{ key: "cwd", label: t("chat-chatview.copy_folder"), icon: "folder" as const, onSelect: () => copyText(session.cwd) }] : []),
        ...(allowed.includes("resume") ? [{ key: "resume", label: t("chat-chatview.copy_resume_command"), icon: "terminal" as const, onSelect: () => copyText(resumeCommand(session)) }] : []),
        ...(!assistant && allowed.includes("close") ? [{ key: "close", label: t("chat-chatview.close_agent_menu"), icon: "x" as const, danger: true, onSelect: () => setClosing(true) }] : []),
        ...(archived
          ? [{ key: "restore", label: t("chat-chatview.restore"), icon: "undo" as const, divider: true, onSelect: () => void restoreAgent(archived, appArchiveDeps) }]
          : archiveItems(session, features, () => setArchiveClosing(true))),
        ...(!archived && deleteInsteadOfArchive(features) && sessionDeletable(session) ? [{ key: "delete", label: t("chat-chatview.delete_chat"), icon: "trash" as const, danger: true, onSelect: () => setDialog("delete") }] : []),
      ]
    : [];
  const items: MenuItem[] = own;

  return (
    <header className="@container flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4">
      <div className={headerSlot(Boolean(topCenter), "start")}>
        {onToggleSidebar && <ChatSidebarButton session={session} counted={!topCenter} onClick={onToggleSidebar} />}
      </div>
      <div className={cx("min-w-0 flex-1", Boolean(topCenter) && "max-lg:hidden")}>
        <div className="flex min-w-0 items-center gap-2">
          {session && !assistant && <SessionMark s={session} size={16} />}
          {session && !assistant && <StatusMark state={state} awaiting={awaiting} waiting={Boolean(session && backgroundWaiting(session))} />}
          {assistant && <AssistantFace session={session} last={last} />}
          {/* narrow: the dot before the title says the state; the pill would leave the title a word or two */}
          <h1 className="truncate text-[14px] font-semibold text-ink">{title}</h1>
          {/* wide, the pill gives the dot's state its words, not a second dot (#3956: the state is said once) */}
          {session && !assistant && (state !== "idle" || awaiting) && <span className="hidden @min-[800px]:contents"><StatePill state={state} awaiting={awaiting} waiting={Boolean(session && backgroundWaiting(session))} dot={false} /></span>}
          {/* narrow: the context bar gives way to the title too (#3532); "Session info" still has the token count. The header's own
              width decides (a container), so a side panel beside the chat narrows it as a phone does; the pill and the
              bar go below 800px, the chips' words below 640px, so the title keeps room in between (#3670) */}
          {session && !assistant && <span className="hidden @min-[800px]:contents"><ContextMeter session={session} /></span>}
          {/* 680–800px (an iPad in landscape with the sidebar open, #4443): the share alone, so what is left stays in sight */}
          {session && !assistant && <span className="hidden @min-[680px]:contents @min-[800px]:hidden"><ContextMeter session={session} compact /></span>}
        </div>
        {sub && !todoLine && <div title={sub} className="truncate text-[11.5px] text-ink-3">{sub}</div>}
        {/* the todo it works for, up top (#1836): opens the todo, "Change to-do…" picks another */}
        {todoLine && session?.todo && (
          <div className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-ink-3">
            <button type="button" onClick={() => navigate(`/todos?id=${encodeURIComponent(session.todo!.id)}`)} title={t("chat-chatview.todo_label", { title: session.todo.title })}
              className="flex min-w-0 items-center gap-1 whitespace-nowrap rounded px-1 text-ink-2 hover:bg-hover hover:text-ink">
              <Icon name="todo" size={11} stroke={2} className="shrink-0" />
              <span className="truncate">{session.todo.title}</span>
            </button>
            <button type="button" onClick={() => setLinkTodoOpen(true)} className="shrink-0 whitespace-nowrap rounded px-1 hover:bg-hover hover:text-ink">{t("chat-chatview.relink")}</button>
            {sub && <span title={sub} className="min-w-0 truncate">· {sub}</span>}
          </div>
        )}
      </div>
      {topCenter && <div className="flex min-w-0 flex-1 justify-center lg:hidden">{topCenter}</div>}
      <div className={headerSlot(Boolean(topCenter), "end")}>
      {session?.goal && !assistant && <GoalChip goal={session.goal} />}
      {session && !assistant && <ActivityChips session={session} onStopLoop={onStopLoop} onJumpToCall={onJumpToCall} />}
      {others.length > 0 && (
        <button
          type="button"
          onClick={tabs.open}
          aria-label={t("chat-chatview.same_space_count", { count: others.length })}
          title={t("chat-chatview.same_space_count", { count: others.length })}
          className="touch-hit flex h-7 shrink-0 items-center gap-1.5 rounded-[8px] px-2 text-[12px] font-medium whitespace-nowrap text-ink-2 shadow-btn transition-colors hover:bg-hover hover:text-ink max-sm:hidden"
        >
          <Icon name="layers" size={13} />
          <span>{t("chat-chatview.same_space")}</span> {others.length}
          {othersNeed && <span className="absolute -top-0.5 -right-0.5 size-2 rounded-full bg-attention shadow-[0_0_0_2px_var(--page)]" />}
        </button>
      )}
      {tabs.anchor && (
        <Menu
          anchor={tabs.anchor}
          onClose={tabs.close}
          align="right"
          width={360}
          header={session?.space_name ? t("chat-chatview.space_header", { name: session.space_name }) : undefined}
          items={spaceRows}
        />
      )}
      {showFiles && <IconButton icon="folder" label={t("chat-chatview.files")} onClick={openFiles} active={here(panel, "files") || here(panel, "git")} className="hidden sm:flex" />}
      {view && onView && (
        <span className="hidden sm:contents">
          <Segmented
            label={t("chat-chatview.terminal_or_chat")}
            value={view}
            onChange={onView}
            compact
            options={[
              { v: "term", label: t("chat-chatview.terminal") },
              { v: "chat", label: t("chat-chatview.chat"), dot: viewDot },
            ]}
          />
        </span>
      )}
      {/* phone width: one button to the other view, as iOS's header has (#3576) */}
      {view && onView && (
        <span className="relative flex shrink-0 sm:hidden">
          <IconButton icon={view === "chat" ? "terminal" : "chat"} label={t(view === "chat" ? "chat-chatview.terminal" : "chat-chatview.chat")} onClick={flipView} />
          {view === "term" && viewDot && <span aria-hidden className="pointer-events-none absolute top-0.5 right-0.5 size-1.5 rounded-full bg-attention" />}
        </span>
      )}
      {/* wide screens have New chat at the top of the sidebar; a phone's sidebar is a drawer */}
      {assistant && session?.space !== "health" && <IconButton icon="compose" label={t("chat-chatview.new_chat")} onClick={() => navigate({ name: "persona-new" })} className="lg:hidden" />}
      {actions}
      {/* the permission mode, always in view and changed here (#3956, iOS #3719): the end of the header's controls, before ⋯,
          at every width */}
      {showsInHeader(session, assistant) && session && <HeaderModeButton session={session} />}
      {items.length > 0 && (
        <span className="relative flex shrink-0">
          <IconButton icon="more" label={t("chat-chatview.more")} onClick={menu.open} active={Boolean(menu.anchor)} />
          {othersNeed && <span aria-hidden className="pointer-events-none absolute -top-0.5 -right-0.5 size-2 rounded-full bg-attention shadow-[0_0_0_2px_var(--page)] sm:hidden" />}
        </span>
      )}
      </div>
      {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} align="right" items={items} />}
      {session && <CloseAgentDialog open={closing} onClose={() => setClosing(false)} onClosed={() => navigate({ name: "home" })} session={session} machineName={machineName} />}
      {session && archiveClosing && <ArchiveCloseDialog open onClose={() => setArchiveClosing(false)} session={session} machineName={machineName} />}
      {session && <SessionInfoDialog open={dialog === "info"} onClose={() => setDialog(null)} session={session} machineName={machineName} />}
      {session && <RenameDialog open={dialog === "rename"} onClose={() => setDialog(null)} session={session} />}
      {session && <RenameSpaceDialog open={dialog === "space"} onClose={() => setDialog(null)} session={session} />}
      {session && <DeleteSessionDialog open={dialog === "delete"} onClose={() => setDialog(null)} session={session} machineName={machineName} />}
      <LinkTodoDialog open={linkTodoOpen} session={session} onClose={() => setLinkTodoOpen(false)} />
      {session && sharingOpen && !assistant && !main && <SharingDialog key={`${session.machine_id}/${session.session_id}/${session.manager ?? ""}`} session={session} onClose={() => setSharingOpen(false)} />}
      {assistantDialogs}
    </header>
  );
});

/** The ⋯ row for the space's other agents on a phone, where the header has no "Same space" button: it opens the same menu
 *  the button does (`open`, at ⋯), red when one of them needs you. */
export function spaceAgentsItem(others: Session[], open: () => void): MenuItem {
  return {
    key: "space-agents",
    label: t("chat-chatview.same_space_count", { count: others.length }),
    icon: "layers",
    state: others.some((s) => stateOf(s) === "needs_you") ? "needs_you" : undefined,
    onSelect: open,
  };
}

/** The header's side slots around a switch in its middle (narrow screens, header.ts); wide, or without one, no boxes
 *  at all. */
function headerSlot(centered: boolean, side: "start" | "end"): string {
  if (!centered) return "contents";
  return cx(HEADER_SLOT, side === "start" ? HEADER_SLOT_START : HEADER_SLOT_END, "lg:contents");
}

