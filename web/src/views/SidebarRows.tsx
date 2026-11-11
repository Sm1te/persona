// The sidebar's rows: Persona's main conversation, the pinned heading, a page's row, an agent's row and the personal
// agents. Imports only SidebarParts among the sidebar's files.
import { memo, useCallback, useMemo, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { awaitsReply, sessionSource, sourceName } from "../lib/agent-groups";
import { addSection, arrangeSessions, assignSection, GROUP_LABEL, isDefaultView, providerLabel, refreshAgentSessions, removeSection, resetView, setView, SHOW_LABEL, SORT_LABEL, toggleProvider, togglePin, totalUnread, updateAgentPrefs, useAgentList, useAgentPrefs, viewOf, type AgentPrefs, type AgentSession, type GroupBy, type ShowMode, type SortBy } from "../lib/agents";
import { ago, backgroundWaiting, sessionCan, sessionPreview, sourceSubtitle, sessionTitle, stateOf, tabLabel } from "../lib/format";
import { firstAndCurrent } from "../lib/persona-nav";
import { href, navigate, routePath, type Route } from "../lib/router";
import { rememberSessionRow, useStore } from "../lib/store";
import { draftLine, rowLine } from "../lib/row-line";
import { usageLimitLabel } from "../lib/usage-limit";
import { useLeftDraft } from "../lib/drafts";
import { planInRow, planLabel, planLine } from "../lib/plan";
import { answeredSession, answerPending, lastAnsweredAt } from "./assistant/answered-lookup";
import type { AgentState, Session } from "../lib/types";
import { cx, EASE, Kbd, Menu, useMenu, useNow, type MenuItem } from "../ui/atoms";
import { StatusMark } from "../ui/StatusMark";
import { runningWorkflows, workflowLine, workflowShortLine } from "../lib/workflows";
import { ultracodeMarkShown } from "../lib/ultracode";
import { Rolling } from "../ui/Rolling";
import { AppMark } from "../ui/AppMark";
import { BrandMark } from "../ui/BrandMark";
import { Icon, type IconName } from "../ui/icons";
import { UltraMark } from "./chat/Ultracode";
import { contextMenuProps } from "../ui/context-menu";
import type { MenuAt } from "../ui/menu-model";
import { rowDetail, useAgentDetails, useContextAsUsed } from "../lib/context-usage";
import { SessionMark } from "../ui/AgentMark";
import { AgentAvatar } from "./agents/AgentAvatar";
import { useFolded } from "../lib/use-folded";
import { ariaKeyshortcuts, formatChord } from "../lib/shortcuts";
import { PagedSessionList } from "./agents/PagedSessionList";
import { runtimeIdentity } from "../lib/runtime-list";
import { heldSeen } from "../lib/seen-outbox";
import { SESSIONS_LIST_FEATURE } from "../lib/session-list";
import { personalHideDeps, personalOrder, personalSessions, removeAgentSession, useGoingIds } from "../lib/agents";
import { BADGE_CLASS, CURRENT_ROW, type GoFn, GroupCount, type RegFn, ROW_TITLE_MIN, type RowMenuFn, Section } from "./SidebarParts";

// Persona's main conversation: the column's first and strongest entry (the app's mark, always in ink, #2004), with the
// conversation's state when it is working or wants you. A line under it parts it from the pages.
// The small "Main chat" under the name keeps the brand row readable as a conversation, not a heading (#6293); a draft
// waiting in the box takes that line (#5568).
// Its menu (Copy session ID, End…: iOS's long press on the main row) opens on a right click, the menu key or its ⋯.
// Memoized like SessionRow: a push that leaves the main conversation as it was does not render it again.
export const PersonaRow = memo(function PersonaRow({ reg, hover, active, go, main, chord, onMenu: menuOf }: { reg: RegFn; hover: (k: string | null) => void; active: boolean; go: GoFn; main?: Session; /** The "Open Persona" shortcut as bound now ("": none). */ chord?: string; /** The conversation rows' menu, opened on `main`: none without it. */ onMenu?: RowMenuFn }) {
  const to: Route = { name: "persona" };
  const st = main ? stateOf(main) : undefined;
  const pending = main?.pending_interactions ?? 0;
  const onMenu = main && menuOf ? (anchor: HTMLElement, at?: MenuAt) => menuOf(main, anchor, at) : undefined;
  // one ref for the row's life, so a re-render does not let go of it and register it again (useGlide's reg)
  const ref = useMemo(() => reg("persona"), [reg]);
  const aside = onMenu && !active ? "group-hover/row:invisible" : undefined;
  // words left unsent in the main conversation's box, once you are not in it (#5568)
  const draft = draftLine(useLeftDraft(main ? `${main.machine_id}/${main.session_id}` : null));
  const link = (
    <a
      ref={ref}
      href={href(to)}
      onClick={go(to)}
      {...(onMenu ? contextMenuProps((at, el) => onMenu(el, at)) : {})}
      onMouseEnter={() => hover("persona")}
      onFocus={() => hover("persona")}
      onBlur={() => hover(null)}
      aria-current={active ? "page" : undefined}
      aria-keyshortcuts={chord ? ariaKeyshortcuts(chord) : undefined}
      title={t("sidebar.persona_main")}
      // pinned over the list (#5581), its own hover fill: the gliding highlight passes under the pinned box
      className={cx("relative z-10 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-[background-color,transform] duration-150 active:scale-[0.98] [@media(pointer:coarse)]:min-h-11", active ? CURRENT_ROW : "hover:bg-hover focus-visible:bg-hover", onMenu && active && "pr-8", onMenu && "[@media(hover:none)]:pr-11")}
    >
      <AppMark size={18} radius={5} className="shadow-hairline" />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-[13.5px] font-semibold text-ink">Persona</span>
        {draft ? <SecondLine line={draft} draft /> : <SecondLine line={{ text: t("sidebar.main_chat") }} draft={false} />}
      </span>
      {chord && <span aria-hidden data-persona-chord className={cx("hidden sm:inline-flex [@media(hover:none)]:hidden", aside)}><Kbd>{formatChord(chord)}</Kbd></span>}
      {pending > 0 ? (
        <span className={cx("flex h-4.5 min-w-4.5 items-center justify-center rounded-full bg-field px-1 text-[10.5px] font-semibold text-attention tabular-nums", aside)}>{pending}</span>
      ) : st === "working" || st === "needs_you" || st === "unread" ? (
        <span className={cx("flex", aside)}><StatusMark state={st} /></span>
      ) : null}
    </a>
  );
  if (!onMenu) return link;
  return (
    <div className="group/row relative">
      {link}
      <button
        type="button"
        aria-label={t("conversation-menu.actions")}
        aria-haspopup="menu"
        title={t("sidebar.more")}
        tabIndex={-1}
        onClick={(e) => onMenu(e.currentTarget.parentElement ?? e.currentTarget)}
        className={cx("absolute top-1/2 right-1 z-20 size-6 -translate-y-1/2 items-center justify-center rounded-[6px] text-ink-3 hover:text-ink group-hover/row:flex [@media(hover:none)]:right-0 [@media(hover:none)]:flex [@media(hover:none)]:size-11 [@media(hover:none)]:bg-transparent", active ? "flex hover:bg-hover" : "hidden bg-hover hover:bg-hover-2")}
      >
        <Icon name="more" size={14} />
      </button>
    </div>
  );
});

/** A group's small title inside a sidebar section (Persona chats' 常驻): the same type
 *  as the section's own title, optionally with an icon before it and a ⋯ for the group's actions. */
export function SubHeading({ label, icon, onMenu, menuLabel }: { label: string; icon?: IconName; onMenu?: (at: MenuAt) => void; menuLabel?: string }) {
  return (
    <div role="heading" aria-level={3} className="flex items-center gap-1 px-2 pt-0.5 pb-0.5" {...(onMenu ? contextMenuProps((at) => onMenu(at)) : {})}>
      <span className="flex min-w-0 flex-1 items-center gap-1 truncate whitespace-nowrap text-[11px] font-semibold text-ink-3">
        {icon && <Icon name={icon} size={10} stroke={2.2} className="shrink-0" />}
        {label}
      </span>
      {onMenu && <button
        type="button"
        aria-label={menuLabel}
        aria-haspopup="menu"
        title={t("sidebar.more")}
        onClick={(e) => onMenu({ anchor: e.currentTarget })}
        className="touch-hit relative z-10 flex size-5 shrink-0 items-center justify-center rounded-[5px] text-ink-3 hover:bg-hover hover:text-ink-2"
      >
        <Icon name="more" size={12} />
      </button>}
    </div>
  );
}

type NavRowProps = { onMenu?: (at: MenuAt, el: HTMLElement, button: boolean) => void; menuLabel?: string; k: string; reg: RegFn; hover: (k: string | null) => void; active: boolean; to: Route; go: GoFn; icon: IconName; label: string; badge?: number | string; badgeTone?: keyof typeof BADGE_CLASS; hint?: string; title?: string; /** A state shown as a dot before the hint ("Automatic"'s rows: some run). */ dot?: AgentState };

// The pages take nothing from the sessions, yet every sessions push re-renders the sidebar: memoized like SessionRow
// (sameProps), a page row renders again only when its own props change (a badge, the highlight).
export const NavRow = memo(function NavRow({ k, reg, hover, active, to, go, icon, label, badge, badgeTone, hint, title, onMenu, menuLabel, dot }: NavRowProps) {
  // one ref for the row's life, so a re-render does not let go of it and register it again (useGlide's reg)
  const ref = useMemo(() => reg(k), [reg, k]);
  const link = (
    <a
      ref={ref}
      {...(onMenu ? contextMenuProps((at, el) => onMenu(at, el, false)) : {})}
      href={href(to)}
      title={title}
      onClick={go(to)}
      onMouseEnter={() => hover(k)}
      onFocus={() => hover(k)}
      onBlur={() => hover(null)}
      aria-current={active ? "page" : undefined}
      className={cx("relative z-10 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-transform duration-150 active:scale-[0.98] [@media(pointer:coarse)]:min-h-11", active && CURRENT_ROW)}
    >
      <span className={active ? "text-ink" : "text-ink-3"}>
        <Icon name={icon} size={14} stroke={1.8} />
      </span>
      <span className={cx("min-w-0 flex-1 truncate text-[13px]", active ? "font-medium text-ink" : "text-ink-2")}>{label}</span>
      {dot && <StatusMark state={dot} size="sm" />}
      {hint && <span className="text-[11px] text-ink-3 tabular-nums">{hint}</span>}
      {badge !== undefined && (
        <span className={cx("flex h-4.5 min-w-4.5 shrink-0 items-center justify-center whitespace-nowrap rounded-full px-1 text-[10.5px] font-semibold tabular-nums", BADGE_CLASS[badgeTone ?? "accent"])} style={{ animation: `pop-in 250ms ${EASE} both` }} key={badge}>
          {badge}
        </span>
      )}
    </a>
  );
  if (!onMenu) return link;
  // the ⋯ shows over the row's end on hover (a pointer); on a touch screen it stays, in the room the row leaves for it
  return (
    <div className="group/row relative">
      {link}
      <button
        type="button"
        aria-label={menuLabel}
        aria-haspopup="menu"
        title={t("sidebar.more")}
        tabIndex={-1}
        onClick={(e) => {
          const box = e.currentTarget.parentElement ?? e.currentTarget;
          onMenu({ anchor: box }, box, true);
        }}
        className="absolute top-1/2 right-1 z-20 hidden size-6 -translate-y-1/2 items-center justify-center rounded-[6px] bg-hover text-ink-3 hover:bg-hover-2 hover:text-ink group-hover/row:flex [@media(hover:none)]:right-0 [@media(hover:none)]:flex [@media(hover:none)]:size-11 [@media(hover:none)]:bg-transparent"
      >
        <Icon name="more" size={14} />
      </button>
    </div>
  );
}, sameProps);

// two shapes as well as two colors, so they tell apart without color: a bare prompt (no frame) and a window with a title bar
const SOURCE_ICON = { terminal: { name: "run", tone: "text-source-terminal" }, desktop: { name: "appWindow", tone: "text-source-desktop" } } as const;

/** Where the agent was opened (#2526), as a mark: a terminal or a desktop app's window in its own color, Lark's mark for a
 *  Lark group (a click opens its Feishu topic). Nothing for other sources. Its name is the tooltip and what a screen reader
 *  reads. At the row's end, so the title starts where an unmarked one does (#3043). */
function SourceTag({ s, className }: { s: Session; className?: string }) {
  const source = sessionSource(s);
  if (!source) return null;
  const label = sourceName(source);
  const mark = source === "feishu" ? <BrandMark brand="lark" size={16} fit="badge" /> : <Icon name={SOURCE_ICON[source].name} size={14} className={SOURCE_ICON[source].tone} />;
  const cls = cx("flex size-4 shrink-0 items-center justify-center", className);
  if (source === "feishu" && s.external_url) {
    // a link inside the row's link: opened by hand, so the row does not navigate too
    const open = (e: React.SyntheticEvent) => {
      e.preventDefault();
      e.stopPropagation();
      window.open(s.external_url, "_blank", "noopener");
    };
    const title = t("lib-agent-groups.open_feishu_topic");
    return (
      <span data-source={source} role="link" tabIndex={0} title={`${label} · ${title}`} aria-label={`${label} · ${title}`} onClick={open} onKeyDown={(e) => e.key === "Enter" && open(e)} className={cx(cls, "cursor-pointer rounded-[4px] hover:bg-hover-2")}>
        {mark}
      </span>
    );
  }
  return <span data-source={source} role="img" title={label} aria-label={label} className={cls}>{mark}</span>;
}

/** A row's line under its title (rowLine, or draftLine): the red lead, then the words. */
function SecondLine({ line, draft }: { line: { lead?: string; text: string; tone?: "needs" | "reply" }; draft: boolean }) {
  return (
    <span data-row-line data-draft={draft ? "" : undefined} className="truncate text-[11.5px] leading-4 text-ink-3">
      {line.lead && <span className={cx("font-medium", line.tone === "reply" ? "text-green" : "text-attention")}>{line.lead}</span>}
      {line.text}
    </span>
  );
}

// `prefix`: a word before the title (a tab's name, a pane's agent), in `prefixClass`
type SessionRowProps = { menuLabel?: string; /** The mark at the start when the state is not shown (a Persona conversation's; pinned: the pin). */ icon?: IconName; s: Session; k: string; reg: RegFn; hover: (k: string | null) => void; active: boolean; to: Route; go: GoFn; tab?: boolean; label?: string; prefix?: string; prefixClass?: string; hint?: string; /** The hint is whole, never cut (a lead's counts). */ hintFull?: boolean; host?: string; showState?: boolean; showTodoTag?: boolean; onMenu?: RowMenuFn; endRoom?: boolean };

// Every sessions push re-renders the sidebar and the Agents list, while lib/sessions-delta.ts keeps the object of each
// session that did not change: a row renders again only when one of its props did. The props compare by identity
// (`go` and the menus are stable), except the route, which the call sites build anew and which is its path (routePath).
function sameProps<P extends { to: Route }>(a: P, b: P): boolean {
  const keys = Object.keys(a) as (keyof P)[];
  if (keys.length !== Object.keys(b).length) return false;
  return keys.every((key) => (key === "to" ? routePath(a.to) === routePath(b.to) : Object.is(a[key], b[key])));
}

export const SessionRow = memo(function SessionRow({ s: given, k, reg, hover, active, to, go, tab, label, prefix, prefixClass, hint, hintFull, host, showState = true, showTodoTag, onMenu, menuLabel = t("sidebar.agent_actions"), endRoom, icon = "chat" }: SessionRowProps) {
  // what it asks you, or what it did, under its title (#3195): no hover or click needed to see why it is red or green
  const ix = useStore((st) => st.interactions.find((i) => i.machine_id === given.machine_id && i.session_id === given.session_id));
  // a card answered here takes its red dot and "Has a question" line away at once, not at the hub's next push (#4286)
  const justAnswered = useStore((st) => lastAnsweredAt(st.answered, given.machine_id, given.session_id));
  const pending = useStore((st) => answerPending(st.answered, given.machine_id, given.session_id));
  // seen on this page and not yet recorded by the hub (lib/seen-outbox.ts): the paged list's rows come from the list
  // itself, not the store, so the row holds it seen too
  const seenMark = useStore((st) => st.seen[runtimeIdentity(given)]);
  const s = heldSeen(answeredSession(given, Boolean(ix), justAnswered, pending), seenMark, Date.now());
  const st = stateOf(s);
  // Awaiting Reply (#5096): seen, but it asked you something you have not answered
  const awaiting = useStore((x) => awaitsReply(s, x.mutes));
  // where it was opened, marked at the row's end: not on a tab with several panes (endRoom), where a herdr tab is a
  // terminal by definition, each pane's row under it says so, and the mark's room is the title's
  const source = showState && !s.assistant && !endRoom ? sessionSource(s) : undefined;
  // a desktop app's runtime that can be written to: its mark says where it is and the agent's mark which agent, so the
  // runtime's name at the end only repeats them (it stays in the tooltip); a read-only one still says why
  const sameAsMarks = source === "desktop" && s.source_kind === "runtime" && sessionCan(s, "send");
  const tail = [sameAsMarks ? "" : sourceSubtitle(s), hint ?? (tab ? tabLabel(s) : "")].filter(Boolean).join(" · ");
  const preview = sessionPreview(s);
  // Settings › Appearance › show details on agent rows (#2430): a second line under an agent's title — folder · model · context · age
  const details = useAgentDetails();
  const contextUsed = useContextAsUsed();
  const withDetail = details && showState && !s.assistant;
  // when it was last active, at the row's end after its hint (#4236; the work-item row, DESIGN_SYSTEM 3.4): not twice
  // when the details line shows it; a tab's row, whose end is the room for "N agents", keeps it to offline and
  // stateless rows without a hint, as before
  const ageAtEnd = !withDetail && !!s.updated_at && (!endRoom || (!tail && (st === "offline" || !showState)));
  // beside the age, a row that already carries its marks (where it was opened, its to-do) drops the hint from its end,
  // where it would only squeeze the title; the hint stays in the row's tooltip
  const tailShown = !!tail && !(ageAtEnd && (source || (showTodoTag && s.todo)));
  // the age at the row's end, in the details line, or a background workflow's progress
  // (#2935) moves on with the clock: the memo keeps an unchanged row from re-rendering on a push, so a minute tick
  // does it, and only in a row that shows one of them. The ages read the clock when the row renders: `now` is the
  // mount time until the first tick, and a row may only start showing an age long after it mounted
  const showsAge = ageAtEnd || (withDetail && !!s.updated_at);
  const now = useNow(60_000, showsAge || runningWorkflows(s).length > 0 || s.turn_error_kind === "usage_limit");
  const detail = withDetail ? rowDetail(s, contextUsed) : [];
  const workflow = showState && !s.assistant && st !== "offline" ? workflowShortLine(s) : "";
  const workflowTitle = workflow ? workflowLine(s, now) : "";
  // its own task list (#5302): 3/5 · the step in progress
  const plan = showState && !s.assistant && st !== "offline" && planInRow(s, st === "working") ? planLine(s) : "";
  // what sits at the row's end steps aside for the hover ⋯; the open row leaves room for its ⋯ instead
  const aside = active ? undefined : "group-hover/row:invisible";
  // behind a prefix (a tab's name, a pane's agent) the title keeps room for a few characters: the prefix shrinks first
  const titleMin = prefix ? ROW_TITLE_MIN : "min-w-0";
  // with words at the end (where it came from, a hint) the title keeps its own width first: those words give way before it does
  const titleFirst = tailShown ? "basis-auto" : undefined;
  // one ref per key, so letting go takes out only this row's entry (keyedRefs)
  const regRow = useMemo(() => reg(k), [reg, k]);
  // words left unsent in its box, once you are not in it (#5568): in place of what it asks or did
  const draft = draftLine(useLeftDraft(`${given.machine_id}/${given.session_id}`));
  const line = draft ?? (showState && !s.assistant ? rowLine(s, ix, awaiting) : undefined);
  // ultracode on (#3428): the workflow mark in the AI colour right after the title; a row without it is drawn as before.
  // Not on an offline row: the node keeps the last value it saw after the process is gone, and a resume starts it off.
  const ultra = showState && ultracodeMarkShown(s);
  const link = (
    <a
      ref={regRow}
      href={href(to)}
      onClick={(event) => { rememberSessionRow(given); go(to)(event); }}
      // right click (at the pointer), the menu key and Shift+F10 (beside the row): the row's menu, the same as its ⋯
      {...(onMenu ? contextMenuProps((at, el) => onMenu(s, el, at)) : {})}
      onMouseEnter={() => hover(k)}
      onFocus={() => hover(k)}
      onBlur={() => hover(null)}
      aria-current={active ? "page" : undefined}
      // its mark's name is in the row's tooltip too: the mark steps aside while the row is hovered
      title={`${sessionTitle(s)}${source ? `\n${sourceName(source)}` : ""}${sourceSubtitle(s) ? `\n${sourceSubtitle(s)}` : ""}${hint ? `\n${hint}` : ""}${s.todo ? `\n${t("sidebar.todo_tag", { title: s.todo.title })}` : ""}${preview ? `\n${preview}` : ""}`}
      // a touch screen: rows a finger can hit (44), and room at the end for the ⋯ that is always there; the open row
      // keeps its ⋯ too, so it has room for it
      className={cx("relative z-10 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-transform duration-150 active:scale-[0.98] [@media(pointer:coarse)]:min-h-11", active && CURRENT_ROW, // room for "N agents" and the ⋯ beside it (instead of the ⋯'s own)
        endRoom ? "pr-[108px] [@media(hover:none)]:pr-[120px]" : cx(onMenu && active && "pr-8", onMenu && "[@media(hover:none)]:pr-11"))}
    >
      {/* the agent's mark before its state (#6390): what runs there, then how it is doing */}
      {showState && !s.assistant && <SessionMark s={s} size={14} />}
      <span className="flex size-3.5 shrink-0 items-center justify-center">{showState || st === "working" ? <StatusMark state={usageLimitLabel(s) && st === "unread" ? "idle" : st} awaiting={awaiting} waiting={backgroundWaiting(s)} /> : <Icon name={icon} size={12} className="text-ink-3" />}</span>
      {prefix && <span className={prefixClass}>{prefix}</span>}
      {/* with the mark, the title sits in the column whether a second line shows or not: the same element through a
          workflow's start and end, so the mark does not fade in again */}
      {ultra || detail.length > 0 || workflow || line || plan ? (
        <span className={cx("flex flex-1 flex-col", titleMin, titleFirst)}>
          {ultra ? (
            <span className="flex min-w-0 items-center gap-1">
              <span className={cx("min-w-0 truncate text-[13px]", active ? "font-medium text-ink" : "text-ink-2", st === "unread" && !active && !usageLimitLabel(s) && "font-medium text-ink")}>
                {label ?? sessionTitle(s)}
              </span>
              <UltraMark size={10} />
            </span>
          ) : (
            <span className={cx("truncate text-[13px]", active ? "font-medium text-ink" : st === "offline" ? "text-ink-3" : "text-ink-2", st === "unread" && !active && !usageLimitLabel(s) && "font-medium text-ink")}>
              {label ?? sessionTitle(s)}
            </span>
          )}
          {line && <SecondLine line={line} draft={!!draft} />}
          {plan && (
            <span data-plan-line title={planLabel(s)} aria-label={planLabel(s)} className="flex h-4 min-w-0 items-center gap-1 text-[11px] leading-4 text-ink-3">
              <Icon name="list" size={10} stroke={2.2} className="shrink-0" />
              <span className="truncate tabular-nums">{plan}</span>
            </span>
          )}
          {/* one line high: a piece that does not fit wraps out of sight instead of squeezing the others */}
          {workflow && (
            <span data-workflow-line title={workflowTitle} aria-label={workflowTitle} className="flex h-4 min-w-0 items-center gap-1 text-[11px] leading-4 text-ai">
              <Icon name="workflow" size={10} stroke={2.2} className="shrink-0" />
              {/* its count rolls to the new one (#3434); the name and phase fade in when they change */}
              <span className="truncate tabular-nums"><Rolling value={workflow} /></span>
            </span>
          )}
          {detail.length > 0 && <span className="flex h-4 min-w-0 flex-wrap gap-x-1 overflow-hidden whitespace-nowrap text-[11px] leading-4 text-ink-3">
            {detail.map((d, i) => (
              <span key={i} title={d.title} className={cx("flex shrink-0 items-center gap-1 tabular-nums", d.danger ? "font-medium text-orange" : d.warn && "font-medium text-orange")}>
                {i > 0 && <span aria-hidden="true">·</span>}
                {d.text}
              </span>
            ))}
          </span>}
        </span>
      ) : (
        <span className={cx("flex-1 truncate text-[13px]", titleMin, titleFirst, active ? "font-medium text-ink" : st === "offline" ? "text-ink-3" : "text-ink-2", st === "unread" && !active && "font-medium text-ink")}>
          {label ?? sessionTitle(s)}
        </span>
      )}
      {host && <span className={cx("shrink-0 rounded bg-hover px-1 text-[10px] leading-4 text-ink-3", aside)}>{host}</span>}
      {showTodoTag && s.todo && (
        <span
          className={cx("flex shrink-0 items-center justify-center rounded bg-hover px-1 py-0.5 text-ink-3", aside)}
          title={t("sidebar.todo_tag", { title: s.todo.title })}
          aria-label={t("sidebar.todo_tag", { title: s.todo.title })}
        >
          <Icon name="todo" size={10} stroke={2} />
        </span>
      )}
      {source && <SourceTag s={s} className={aside} />}
      {/* the row's ⋯ takes the end on hover: what is there steps aside instead of showing half under it */}
      {tailShown && <span className={cx(hintFull ? "shrink-0 whitespace-nowrap tabular-nums" : "max-w-[72px] min-w-6 shrink-[20] truncate", "text-[11px] text-ink-3", aside)}>{tail}</span>}
      {ageAtEnd && <span data-row-age className={cx("shrink-0 text-[11px] text-ink-3", aside)}>{ago(s.updated_at, Date.now(), "narrow")}</span>}
      {(s.pending_interactions ?? 0) > 0 && <span className={cx("flex h-4 min-w-4 shrink-0 items-center justify-center rounded-full bg-field px-1 text-[10px] font-semibold text-attention tabular-nums", aside)}>{s.pending_interactions}</span>}
    </a>
  );
  if (!onMenu) return link;
  // the ⋯ shows over the row's end on hover (a pointer); on the open row and on a touch screen it stays, in the room
  // the row leaves for it
  return (
    <div className="group/row relative">
      {link}
      <button
        type="button"
        aria-label={menuLabel}
        aria-haspopup="menu"
        title={t("sidebar.more")}
        tabIndex={-1}
        onClick={(e) => onMenu(s, e.currentTarget.parentElement ?? e.currentTarget)}
        className={cx("absolute top-1/2 right-1 z-20 size-6 -translate-y-1/2 items-center justify-center rounded-[6px] text-ink-3 hover:text-ink group-hover/row:flex [@media(hover:none)]:right-0 [@media(hover:none)]:flex [@media(hover:none)]:size-11 [@media(hover:none)]:bg-transparent", active ? "flex hover:bg-hover" : "hidden bg-hover hover:bg-hover-2")}
      >
        <Icon name="more" size={14} />
      </button>
    </div>
  );
}, sameProps);

// Personal agents: the Lark gateway's personal agents with a view menu (group / sort / show), a search field, pins and custom
// sections — the prefs live on the hub so iOS sees the same pins.
type MenuPage = "root" | "group" | "sort" | "show";
function agentViewItems(page: MenuPage, setPage: (p: MenuPage) => void, p: AgentPrefs, providers: string[]): MenuItem[] {
  const v = viewOf(p);
  const back: MenuItem = { key: "back", label: t("sidebar.back"), keepOpen: true, onSelect: () => setPage("root") };
  if (page === "group")
    return [back, ...(Object.keys(GROUP_LABEL) as GroupBy[]).map((g, i) => ({ key: g, label: GROUP_LABEL[g], divider: i === 0, checked: v.group === g, onSelect: () => updateAgentPrefs((x) => setView(x, { group: g })) }))];
  if (page === "sort")
    return [back, ...(Object.keys(SORT_LABEL) as SortBy[]).map((s, i) => ({ key: s, label: SORT_LABEL[s], divider: i === 0, checked: v.sort === s, onSelect: () => updateAgentPrefs((x) => setView(x, { sort: s })) }))];
  if (page === "show")
    return [
      back,
      ...(Object.keys(SHOW_LABEL) as ShowMode[]).map((s, i) => ({ key: s, label: SHOW_LABEL[s], divider: i === 0, checked: v.show === s, keepOpen: true, onSelect: () => updateAgentPrefs((x) => setView(x, { show: s })) })),
      ...providers.map((pr, i) => ({ key: `p:${pr}`, label: providerLabel(pr), hint: i === 0 ? t("sidebar.sources") : undefined, divider: i === 0, checked: !v.providers.length || v.providers.includes(pr), keepOpen: true, onSelect: () => updateAgentPrefs((x) => (viewOf(x).providers.length ? toggleProvider(x, pr) : setView(x, { providers: providers.filter((y) => y !== pr) }))) })),
    ];
  const showHint = v.show !== "all" ? SHOW_LABEL[v.show] : v.providers.length ? t("sidebar.source_count", { count: v.providers.length }) : SHOW_LABEL.all;
  return [
    { key: "group", label: t("sidebar.group_by"), hint: GROUP_LABEL[v.group], submenu: true, onSelect: () => setPage("group") },
    { key: "sort", label: t("sidebar.sort_by"), hint: SORT_LABEL[v.sort], submenu: true, onSelect: () => setPage("sort") },
    { key: "show", label: t("sidebar.show"), hint: showHint, submenu: true, onSelect: () => setPage("show") },
    {
      key: "section",
      label: t("sidebar.new_group"),
      icon: "plus",
      divider: true,
      onSelect: () => {
        const name = prompt(t("sidebar.group_name"))?.trim();
        if (name) updateAgentPrefs((x) => addSection(x, name));
      },
    },
    { key: "reset", label: t("sidebar.reset_view"), divider: true, disabled: isDefaultView(p), onSelect: () => updateAgentPrefs(resetView) },
    { key: "all", label: t("sidebar.view_all"), onSelect: () => navigate({ name: "agents" }) },
  ];
}


// Row actions (⋯ and right click): pin, move to a custom section, remove a section, remove from the list.
function agentRowItems(s: AgentSession, p: AgentPrefs): MenuItem[] {
  const pinned = p.pins.includes(s.id);
  const cur = p.assign[s.id];
  return [
    { key: "pin", label: pinned ? t("sidebar.unpin") : t("sidebar.pin"), icon: "pin", onSelect: () => updateAgentPrefs((x) => togglePin(x, s.id)) },
    ...p.sections.map((sec, i) => ({ key: `sec:${sec.id}`, label: t("sidebar.move_to_group", { name: sec.name }), divider: i === 0, checked: cur === sec.id, onSelect: () => updateAgentPrefs((x) => assignSection(x, s.id, cur === sec.id ? null : sec.id)) })),
    ...(cur ? [{ key: "unsec", label: t("sidebar.remove_from_group"), onSelect: () => updateAgentPrefs((x) => assignSection(x, s.id, null)) }] : []),
    { key: "hide", label: t("lib-agents.remove"), icon: "archive", divider: true, onSelect: () => void removeAgentSession(s, personalHideDeps) },
  ];
}

// memoized: its props are stable (reg, hover, go) or a string, and it reads the agents from their own store, so a
// sessions push does not reach it
const personalIdentity = (s: AgentSession) => s.id;
export const PersonalAgents = memo(function PersonalAgents(props: { reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn }) {
  const bounded = useStore((s) => s.features.includes(SESSIONS_LIST_FEATURE));
  const { loaded, sources } = useAgentList();
  // Keep the subscription mounted while hidden so setup changes can reveal the section without a reload.
  if (!loaded || !sources?.length || sources.some((s) => !s.enabled || ["missing", "disabled", "no_accounts"].includes(s.setup ?? "") || s.accounts?.length === 0)) return null;
  return bounded ? <BoundedPersonalAgents {...props} /> : <LegacyPersonalAgents {...props} />;
});

function BoundedPersonalAgents({ reg, hover, activeKey, go }: { reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn }) {
  const prefs = useAgentPrefs();
  const [rowMenu, setRowMenu] = useState<{ s: AgentSession; el: HTMLElement } | null>(null);
  const onMenu = useCallback((s: AgentSession, el: HTMLElement) => setRowMenu({ s, el }), []);
  const [folded, toggle] = useFolded("personal-agents", false, { holds: activeKey.startsWith("a:") });
  const page = useSyncExternalStore(personalSessions.subscribe, personalSessions.getSnapshot, personalSessions.getSnapshot);
  const going = useGoingIds();
  if (!page.loaded || !page.rows.some((s) => !going.has(s.id))) return null;
  return <Section title={t("sidebar.personal_agents")} folded={folded} onToggle={toggle} right={<GroupCount n={page.total} label={t("sidebar.agent_count", { count: page.total })} />}>
    {!folded && <PagedSessionList root={personalSessions} path="/agents/sessions" identity={personalIdentity} compare={personalOrder} event="agents" personal
      renderRow={(s) => (going.has(s.id) ? null : <AgentRow s={s} reg={reg} hover={hover} active={activeKey === `a:${s.id}`} go={go} pinned={prefs.pins.includes(s.id)} onMenu={onMenu} />)} />}
    {rowMenu && <Menu anchor={rowMenu.el} onClose={() => setRowMenu(null)} align="right" width={200} items={agentRowItems(rowMenu.s, prefs)} />}
  </Section>;
}

const LegacyPersonalAgents = memo(function LegacyPersonalAgents({ reg, hover, activeKey, go }: { reg: RegFn; hover: (k: string | null) => void; activeKey: string; go: GoFn }) {
  const { sessions: listed, loaded, error } = useAgentList();
  // a row removed here leaves at once, before the hub's re-pull
  const going = useGoingIds();
  const sessions = useMemo(() => (going.size ? listed.filter((s) => !going.has(s.id)) : listed), [listed, going]);
  const [retrying, setRetrying] = useState(false);
  const retry = async () => {
    if (retrying) return;
    setRetrying(true);
    try {
      await refreshAgentSessions();
    } finally {
      setRetrying(false);
    }
  };
  const prefs = useAgentPrefs();
  const [folded, toggle] = useFolded("personal-agents", false, { holds: activeKey.startsWith("a:") });
  const [q, setQ] = useState("");
  const [searching, setSearching] = useState(false);
  const [all, setAll] = useState<Record<string, boolean>>({});
  const menu = useMenu();
  const [page, setPage] = useState<MenuPage>("root");
  const [rowMenu, setRowMenu] = useState<{ s: AgentSession; el: HTMLElement } | null>(null);
  // the same function on every render, so the memoized rows skip a re-render
  const onRowMenu = useCallback((s: AgentSession, el: HTMLElement) => setRowMenu({ s, el }), []);
  if (!loaded || !sessions.length) return null;
  const groups = arrangeSessions(sessions, prefs, q);
  const unread = totalUnread(sessions);
  const providers = [...new Set(sessions.map((s) => s.provider))];
  const filtered = !isDefaultView(prefs) || !!q;
  const sectionOf = (key: string) => (key.startsWith("sec:") ? key.slice(4) : "");
  return (
    <Section
      title={t("sidebar.personal_agents")}
      folded={folded}
      onToggle={toggle}
      right={
        <>
          {unread ? <span title={t("sidebar.unread_messages_count", { count: unread })} className="flex h-4 min-w-4 items-center justify-center rounded-full bg-accent-tint px-1 text-[10px] font-semibold text-accent-ink tabular-nums"><span aria-hidden>{unread}</span><span className="sr-only">{t("sidebar.unread_messages_count", { count: unread })}</span></span> : sessions.length > 0 && <GroupCount n={sessions.length} label={t("sidebar.agent_count", { count: sessions.length })} />}
          <button type="button" title={t("sidebar.search_personal")} aria-label={t("sidebar.search_personal")} onClick={() => setSearching((x) => !x || !!q)} className={cx("touch-hit relative z-10 flex size-5 items-center justify-center rounded-[5px] text-ink-3 hover:bg-hover hover:text-ink-2", (searching || q) && "text-ink-2")}>
            <Icon name="search" size={12} />
          </button>
          <button
            type="button"
            title={t("sidebar.filter_sort")}
            aria-label={t("sidebar.filter_sort")}
            aria-haspopup="menu"
            onClick={(e) => {
              setPage("root");
              menu.open(e);
            }}
            className={cx("touch-hit relative z-10 flex size-5 items-center justify-center rounded-[5px] hover:bg-hover hover:text-ink-2", filtered ? "text-accent" : "text-ink-3", menu.anchor && "bg-hover")}
          >
            <Icon name="filter" size={12} />
          </button>
          {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} align="right" width={220} items={agentViewItems(page, setPage, prefs, providers)} />}
        </>
      }
    >
      {!loaded && <p className="px-2 py-1 text-[12px] text-ink-3">{t("sidebar.loading")}</p>}
      {error && <div className="relative z-10 px-2 py-1">
        <p role="alert" className="text-[12px] break-words text-red">{t("sidebar.personal_failed", { reason: error })}</p>
        <button type="button" disabled={retrying} onClick={() => void retry()} className="touch-hit mt-1 rounded-control px-2 py-1 text-xs leading-normal whitespace-nowrap text-accent-ink hover:bg-hover disabled:cursor-wait disabled:opacity-50">
          {t(retrying ? "sidebar.loading" : "sidebar.retry")}
        </button>
      </div>}
      {(searching || q) && (
        <input
          autoFocus
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              setQ("");
              setSearching(false);
            }
          }}
          placeholder={t("sidebar.search_personal")}
          className="relative z-10 mx-1 mb-1 h-7 rounded-control bg-inset px-2 text-[12px] text-ink shadow-hairline outline-none placeholder:text-ink-3"
        />
      )}
      {loaded && !error && !groups.length && <p className="px-2 py-1 text-[12px] text-ink-3">{t("sidebar.no_match")}</p>}
      {groups.map((g) => {
        const more = !q && !all[g.key] && g.key !== "pinned" && g.sessions.length > 5;
        const isOpen = (s: AgentSession) => activeKey === `a:${s.id}`;
        const rows = more ? firstAndCurrent(g.sessions, 5, isOpen) : g.sessions;
        const sec = sectionOf(g.key);
        return (
          <div key={g.key} className="flex flex-col gap-px">
            {g.label && (groups.length > 1 || g.key === "pinned" || sec) && (
              <div className="group/g flex items-center gap-1.5 px-2 pt-1.5 pb-0.5 text-[11px] text-ink-3">
                {g.key === "pinned" && <Icon name="pin" size={10} />}
                <span className="min-w-0 flex-1 truncate">{g.label}</span>
                {g.unread > 0 && <span className="tabular-nums text-accent-ink">{g.unread}</span>}
                {sec && (
                  <button type="button" title={t("sidebar.delete_group")} aria-label={t("sidebar.delete_group_named", { name: g.label })} onClick={() => confirm(t("sidebar.delete_group_confirm", { name: g.label })) && updateAgentPrefs((x) => removeSection(x, sec))} className="relative z-10 opacity-0 group-hover/g:opacity-100 hover:text-ink-2">
                    <Icon name="x" size={10} />
                  </button>
                )}
              </div>
            )}
            {rows.map((s) => (
              <AgentRow key={s.id} s={s} reg={reg} hover={hover} active={isOpen(s)} go={go} pinned={prefs.pins.includes(s.id)} onMenu={onRowMenu} />
            ))}
            {g.sessions.length > 5 && !q && g.key !== "pinned" && (
              <button type="button" onClick={() => setAll((x) => ({ ...x, [g.key]: !x[g.key] }))} className="relative z-10 w-full rounded-[7px] px-2 py-1 text-left text-[12px] text-ink-3 hover:text-ink-2">
                {more ? t("sidebar.show_more", { count: g.sessions.length - rows.length }) : t("sidebar.show_less")}
              </button>
            )}
          </div>
        );
      })}
      {rowMenu && <Menu anchor={rowMenu.el} onClose={() => setRowMenu(null)} align="right" width={200} items={agentRowItems(rowMenu.s, prefs)} />}
    </Section>
  );
});

// memoized (props by identity: `s` is kept while the agent is unchanged, the functions are stable)
export const AgentRow = memo(function AgentRow({ s, reg, hover, active, go, pinned, onMenu }: { s: AgentSession; reg: RegFn; hover: (k: string | null) => void; active: boolean; go: GoFn; pinned: boolean; onMenu: (s: AgentSession, el: HTMLElement) => void }) {
  const k = `a:${s.id}`;
  // one ref for the row's life, so a re-render does not let go of it and register it again (useGlide's reg)
  const regRow = useMemo(() => reg(k), [reg, k]);
  const to: Route = { name: "agents", id: s.id };
  const unread = s.unreadCount > 0;
  return (
    <div className="group/row relative" onContextMenu={(e) => {
      e.preventDefault();
      onMenu(s, e.currentTarget);
    }}>
      <a
        ref={regRow}
        href={href(to)}
        onClick={go(to)}
        onMouseEnter={() => hover(k)}
        onFocus={() => hover(k)}
        onBlur={() => hover(null)}
        aria-current={active ? "page" : undefined}
        title={`${s.title || t("lib-format.untitled_session")} · ${providerLabel(s.provider)}`}
        className={cx("relative z-10 flex w-full items-center gap-2 rounded-[7px] px-2 sidebar-row transition-transform duration-150 active:scale-[0.98] [@media(hover:none)]:pr-11 [@media(pointer:coarse)]:min-h-11", active && cx(CURRENT_ROW, "pr-7"))}
      >
        <AgentAvatar s={s} size={16} />
        <span className={cx("min-w-0 flex-1 truncate text-[13px]", active || unread ? "font-medium text-ink" : "text-ink-2")}>{s.title || t("lib-format.untitled_session")}</span>
        <span className={cx("flex shrink-0 items-center", !active && "group-hover/row:invisible")}>
          {unread ? (
            <span className="flex h-4 min-w-4 items-center justify-center rounded-full bg-accent px-1 text-[10px] font-semibold text-white tabular-nums">{s.unreadCount}</span>
          ) : (
            s.rankTimeMs > 0 && <span className="text-[11px] text-ink-3">{ago(new Date(s.rankTimeMs).toISOString())}</span>
          )}
        </span>
      </a>
      <button
        type="button"
        title={pinned ? t("sidebar.unpin") : t("sidebar.pin")}
        aria-label={pinned ? t("sidebar.unpin") : t("sidebar.pin")}
        onClick={() => updateAgentPrefs((x) => togglePin(x, s.id))}
        className={cx("absolute top-1/2 right-1 z-20 flex size-5 -translate-y-1/2 items-center justify-center rounded-[5px] hover:bg-hover group-hover/row:opacity-100 focus-visible:opacity-100 [@media(hover:none)]:hidden", active ? "opacity-100" : "opacity-0", pinned ? "text-accent" : "text-ink-3")}
      >
        <Icon name="pin" size={11} />
      </button>
      {/* a touch screen has no hover pin and no right click: the row's menu ("Pin" is in it) behind a ⋯ that stays */}
      <button
        type="button"
        aria-label={t("sidebar.personal_actions")}
        aria-haspopup="menu"
        title={t("sidebar.more")}
        onClick={(e) => onMenu(s, e.currentTarget.parentElement ?? e.currentTarget)}
        className="absolute top-1/2 right-0 z-20 hidden size-11 -translate-y-1/2 items-center justify-center text-ink-3 [@media(hover:none)]:flex"
      >
        <Icon name="more" size={14} />
      </button>
    </div>
  );
});
