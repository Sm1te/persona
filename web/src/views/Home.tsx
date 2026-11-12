// Overview: what needs you, what is running, what just finished — Beautiful UI's Task Rows — and the computers.
import { useEffect, useMemo, useRef, useState } from "react";
import { homeKey } from "../lib/folded-rows";
import { ago, interactionCanAnswer, sessionCan, sessionPreview, shortDuration, sourceSubtitle, sessionTitle, stateOf, tabLabel } from "../lib/format";
import { usePendingLine } from "../lib/card-answer";
import { cardState, requestRoute } from "../lib/card-focus";
import { interactionAsk } from "../lib/inbox";
import { interactionKicker } from "../lib/row-line";
import { answerLines, type Resolution } from "../lib/answered";
import { quickAnswers, withHeld, type HeldRow, type QuickChoice } from "../lib/quick-answers";
import { optionDisplay, permissionDetail } from "../lib/tool-detail";
import { cardToolName } from "../lib/turns";
import { plainMarkdown } from "../lib/plain-markdown";
import { homeOverview, paneRoute, type TabGroup } from "../lib/herdr-tree";
import { agentLabel } from "../lib/mentions";
import { hubLabel, machineWithHub } from "../lib/mesh";
import { isPending, machineErrorText } from "../lib/add-computer";
import { machineDeployment } from "../lib/machine-deploy";
import { classifyConnection } from "../lib/node-health";
import { href, navigate, onLinkClick, type Route } from "../lib/router";
import { useScrollMemory } from "../lib/scroll-memory";
import { useScopedAgentSessions, useScopedCounts, useScopedInteractions, useScopedMachines, useScopedTodos } from "../lib/current-assistant";
import { getSession, useStore, withTaskRows } from "../lib/store";
import { discardOutbox, outboxEntry, queueToSession, retryOutboxEntry } from "../lib/outbox";
import { homeTasks, type HomeTask } from "../lib/home-tasks";
import { heldDelivery, makeSessionLookup, orphanedLine, TODO_STATUS_INFO, TODO_TONE_CLASS, todoStatusLabel } from "../lib/todos";
import { BUSY_MOTION, spinStyle } from "../lib/busy-motion";
import type { AgentState, Interaction, Machine, Session } from "../lib/types";
import { Button, Collapse, cx, EASE, IconButton, Loading, useNow } from "../ui/atoms";
import { RecommendedTag } from "../ui/RecommendedTag";
import { StatusMark } from "../ui/StatusMark";
import { Icon } from "../ui/icons";
import { MarkSeenDialog } from "./AgentMenus";
import { answeredLookup, answeredSessions } from "./assistant/answered-lookup";
import { resolve } from "./chat/Interactions";
import { OverviewCards } from "./extensions/ExtensionSlots";
import { TodoStageBar } from "./TodoStageBar";
import { t } from "../i18n";
import { markAllSeenLabel, STATE_LABEL, stateCount, stateName } from "../lib/state-labels";

function greeting() {
  const h = new Date().getHours();
  return h < 5 ? t("home.greeting_late") : h < 11 ? t("home.greeting_morning") : h < 13 ? t("home.greeting_noon") : h < 18 ? t("home.greeting_afternoon") : t("home.greeting_evening");
}

export function Home({ onNewAgent, onToggleSidebar }: { onNewAgent: () => void; onToggleSidebar: () => void }) {
  // with several assistants, the current one's computers, cards, agents and tasks (docs/assistant/managers.md "Clients")
  const machines = useScopedMachines();
  const interactions = useScopedInteractions();
  const answered = useStore((s) => s.answered);
  // a card answered here moves its agent out of "Stuck" at once, not at the hub's next push (#4286)
  const agentSessions = useScopedAgentSessions();
  const sessions = useMemo(() => answeredSessions(agentSessions, interactions, answered), [agentSessions, interactions, answered]);
  const loaded = useStore((s) => s.loaded);
  const noMachines = loaded && machines.length === 0;
  const counts = useScopedCounts();
  const now = useNow(5000, counts.working > 0);

  // the agents of one herdr tab are one row (#2320), filed under the most urgent of them; a card waiting counts as
  // something to do, and "Mark all as seen" marks every unread agent, wherever its tab is filed
  const { groups, unread, markAllAt, quiet } = useMemo(() => homeOverview(sessions, interactions), [sessions, interactions]);
  // every open task with where it stands (#6450, the web side of the Lark bot's /tasks)
  const todos = useScopedTodos();
  const taskRows = useStore((s) => s.taskRows);
  // read as the Tasks page reads them: a card answered here moves its task at once too
  const tasks = useMemo(() => homeTasks(todos, answeredLookup(withTaskRows(makeSessionLookup(agentSessions), taskRows), interactions, answered)), [todos, agentSessions, taskRows, interactions, answered]);
  const name = (mid: string) => machineWithHub(mid, machines);
  const [confirmSeen, setConfirmSeen] = useState<Session[] | null>(null);
  const markAll = <MarkAllSeen count={unread.length} onClick={() => setConfirmSeen(unread)} />;

  // A card answered on its row (#4090) stays in its slot while it is sent and, answered, says so for a moment before
  // it folds away: the next row does not slide under the pointer and take a second click meant for this one.
  const [held, setHeld] = useState<HeldRow[]>([]);
  const answer = async (ix: Interaction, index: number, before: string | undefined, c: QuickChoice) => {
    const phase = (p: HeldRow["phase"]) => setHeld((h) => h.map((x) => (x.ix.id === ix.id ? { ...x, phase: p } : x)));
    setHeld((h) => [...h.filter((x) => x.ix.id !== ix.id), { ix, index, before, phase: "sending", pick: c.key, body: c.body }]);
    if (!(await resolve(ix, c.body))) return setHeld((h) => h.filter((x) => x.ix.id !== ix.id)); // it said why (toast)
    phase("done");
    setTimeout(() => phase("leaving"), ANSWERED_HOLD_MS);
    setTimeout(() => setHeld((h) => h.filter((x) => x.ix.id !== ix.id)), ANSWERED_HOLD_MS + 400);
  };

  // Back from a card opened here lands where the reader was, not at the top (#5133)
  const scroller = useRef<HTMLDivElement>(null);
  useScrollMemory(scroller, "/overview");

  return (
    <div className="flex h-full flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line px-3 sm:px-4 lg:border-transparent">
        <IconButton icon="sidebar" label={t("home.sidebar")} onClick={onToggleSidebar} className="lg:hidden" />
        <span className="flex-1" />
        {!noMachines && (
          <Button tone="secondary" icon="plus" onClick={onNewAgent}>
            {t("home.new_agent")}
          </Button>
        )}
      </header>
      <div ref={scroller} className="min-h-0 flex-1 overflow-y-auto">
        {/* wide enough for two or three columns of rows: a desktop browser no longer reads one narrow column between margins */}
        <div className="@container mx-auto flex max-w-[1920px] flex-col gap-8 px-5 pt-6 pb-16 sm:px-8 sm:pt-10">
          <div style={{ animation: `fade-up 420ms ${EASE} both` }}>
            <h1 className="text-[22px] font-semibold tracking-tight text-ink">{greeting()}</h1>
            <div className="mt-1 flex items-center gap-2">
              <p className="min-w-0 flex-1 text-[13.5px] text-ink-2">
                {!loaded ? t("home.connecting") : quiet ? noMachines ? t("home.no_machines_summary") : t("home.all_quiet") : t("home.summary", { list: [counts.needs && t("home.summary_needs", { count: counts.needs }), counts.working && stateCount("working", counts.working), counts.unread && stateCount("unread", counts.unread)].filter(Boolean).join(t("home.summary_separator")) })}
              </p>
              {loaded && markAllAt === "summary" && markAll}
            </div>
          </div>

          {/* the extensions' cards (docs/extensions/extensions.md"UI slots" overview_card), under the agents' summary */}
          <OverviewCards />

          {/* Before the first snapshot the socket streams the machines' agents and cards in one by one, each moving the
              groups already painted, and "Loading" then went from over them: until it is in only "Loading", then all at once. */}
          {!loaded && <Loading label={t("common.loading")} />}

          {/* the headline counts agents, this counts cards: say so when one agent has several cards waiting */}
          {loaded && (interactions.length > 0 || held.length > 0) && (
            <Group title={stateName("needs_you")} count={interactions.length} hint={new Set(interactions.map(homeKey)).size < interactions.length ? t("home.needs_you_hint") : undefined} grid="cards">
              {withHeld(interactions, held).map(({ ix, held: h }, i, rows) => {
                const s = sessions.find((x) => x.machine_id === ix.machine_id && x.session_id === ix.session_id);
                const title = ix.session_title || (s ? sessionTitle(s) : ix.cwd || t("home.session"));
                // what the row answers in one tap, as iOS's "Needs you" cards; the rest (plans, forms, typing) opens the chat
                const quick = interactionCanAnswer(ix, s ?? getSession(ix.machine_id, ix.session_id)) ? quickAnswers(ix) : null;
                // allowed in one tap, a permission says the tool and what it runs or touches (iOS: "Bash: swift test"),
                // not the agent's description of it
                const tool = quick && ix.kind === "permission" ? cardToolName(ix) : "";
                // and a terminal dialog what it is about (the command under its question), as iOS's card does. Both
                // as they are: read as Markdown, `ls **/*.ts` lost its stars and `a <in >out` its redirections
                const about = quick && ix.kind === "screen" ? [ix.dialog?.question, ix.dialog?.detail].filter(Boolean).join(" · ") : "";
                const raw = (tool && permissionDetail(ix.tool_name, ix.tool_input)?.fields[0]?.value) || about;
                const ask = (ix.kind === "plan" ? t("home.plan_ready") : raw || plainMarkdown(interactionAsk(ix))).replace(/\s+/g, " ").trim();
                const done = h !== undefined && h.phase !== "sending";
                // every row in a Collapse, so an answered one folds away where it is; answered, it keeps its words
                // and its height ("Answered" in the buttons' place) until then
                return (
                  // the room around the row (-m-2 / p-2) is its shadow's: the collapse clips what is outside it
                  <Collapse key={ix.id} open={h?.phase !== "leaving"} className="-m-2">
                    <div className="p-2">
                      <TaskRow
                        i={i}
                        state="needs_you"
                        done={done}
                        to={requestRoute(ix)}
                        title={title}
                        tags={rowTags(title, [name(ix.machine_id), s?.space_name, s && tabLabel(s)])}
                        detail={(tool && ask && ask !== tool ? t("home.permission_detail", { tool, what: ask }) : ask) || undefined}
                        pill={!done && <Pill>{interactionKicker(ix)}</Pill>}
                        entry={cardState(ix.id)}
                        actions={done ? <AnsweredLine ix={ix} body={h.body} /> : quick && <QuickAnswers ix={ix} choices={quick} sending={h?.pick} onPick={(c) => void answer(ix, i, rows[i + 1]?.ix.id, c)} />}
                      />
                    </div>
                  </Collapse>
                );
              })}
            </Group>
          )}

          {loaded && groups.needs.length > 0 && (
            <Group title={t("home.waiting_on_computer")} count={groups.needs.length} hint={t("home.waiting_on_computer_hint")} grid>
              {groups.needs.map((g, i) => (
                <GroupTask key={g.key} g={g} i={i} machine={name(g.lead.machine_id)} pill={<Pill>{stateName("needs_you")}</Pill>} />
              ))}
            </Group>
          )}

          {loaded && tasks.rows.length > 0 && (
            <Group
              title={t("home.tasks")}
              count={tasks.total}
              right={tasks.total > tasks.rows.length && <a href={href({ name: "todos" })} onClick={(e) => onLinkClick(e, { name: "todos" })} className="text-[12px] text-ink-3 hover:text-ink-2">{t("home.tasks_all")}</a>}
              grid
            >
              {tasks.rows.map((r) => (
                <TaskCard key={r.todo.id} task={r} />
              ))}
            </Group>
          )}

          {loaded && groups.working.length > 0 && (
            <Group title={stateName("working")} count={groups.working.length} grid>
              {groups.working.map((g, i) => (
                <GroupTask key={g.key} g={g} i={i} machine={name(g.lead.machine_id)} pill={<When>{g.lead.state_since ? shortDuration(now - Date.parse(g.lead.state_since)) : ""}</When>} />
              ))}
            </Group>
          )}

          {loaded && groups.unread.length > 0 && (
            <Group
              title={stateName("unread")}
              count={groups.unread.length}
              right={markAllAt === "section" && markAll}
              grid
            >
              {groups.unread.map((g, i) => (
                <GroupTask key={g.key} g={g} i={i} machine={name(g.lead.machine_id)} pill={<When>{ago(g.lead.updated_at) || stateName("done")}</When>} footer={sessionCan(g.lead, "send") ? <ContinueAction s={g.lead} title={sessionTitle(g.launcher ?? g.sessions[0]!)} several={g.sessions.length > 1} /> : undefined} />
              ))}
            </Group>
          )}

          {loaded && quiet && interactions.length === 0 && held.length === 0 && (
            <div className="flex flex-col items-start gap-3 rounded-card bg-surface p-4 shadow-card">
              <span className={cx("flex size-8 items-center justify-center rounded-full", noMachines ? "bg-field text-ink-2" : "bg-green-tint text-green")}>
                <Icon name={noMachines ? "machine" : "check"} size={15} stroke={noMachines ? undefined : 2.6} />
              </span>
              <div>
                <p className="text-[13.5px] font-medium text-ink">{noMachines ? t("home.no_machines") : t("home.all_set")}</p>
                <p className="mt-0.5 text-[12.5px] text-ink-2">{noMachines ? t("home.no_machines_hint") : t("home.all_set_hint")}</p>
              </div>
              <Button tone="primary" icon={noMachines ? "machine" : "plus"} onClick={noMachines ? () => navigate({ name: "machines" }) : onNewAgent}>
                {noMachines ? t("home.open_machines") : t("home.new_agent")}
              </Button>
            </div>
          )}

          {loaded && machines.length > 0 && (
            <Group title={t("home.computers")} count={machines.length} right={<a href={href({ name: "machines" })} onClick={(e) => onLinkClick(e, { name: "machines" })} className="text-[12px] text-ink-3 hover:text-ink-2">{t("home.monitor")}</a>}>
              <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 @min-[60rem]:grid-cols-3 @min-[84rem]:grid-cols-4 @min-[110rem]:grid-cols-5">
                {machines.map((m) => {
                  const mine = m.online ? sessions.filter((s) => s.machine_id === m.id && stateOf(s) !== "offline") : [];
                  const hub = hubLabel(m, machines);
                  const detail = { name: "machine" as const, mid: m.id };
                  return (
                    <a key={m.id} href={href(detail)} onClick={(e) => onLinkClick(e, detail)} className="flex items-center gap-3 rounded-card bg-surface px-3 py-2.5 shadow-card transition-colors hover:bg-hover">
                      <span className={cx("flex size-8 shrink-0 items-center justify-center rounded-[8px]", m.online ? "bg-field text-ink-2" : "bg-inset text-ink-3")}>
                        <Icon name="machine" size={15} />
                      </span>
                      <span className="min-w-0 flex-1">
                        <span className="flex min-w-0 items-center gap-1.5">
                          <span className="truncate text-[13px] font-medium text-ink">{m.name}</span>
                          {hub && <span className="shrink-0 truncate text-[11.5px] text-ink-3">{hub}</span>}
                        </span>
                        <span className="block truncate text-[11.5px] text-ink-3" title={!m.online && m.error && offlineLine(m) !== m.error ? m.error : undefined} data-machine-line>{m.online ? t("home.machine_agents", { count: mine.length }) + (m.system?.name ? ` · ${m.system.name}` : "") : offlineLine(m)}</span>
                      </span>
                      <span className={cx("size-2 shrink-0 rounded-full", m.online ? "bg-green" : "bg-line-strong")} title={m.online ? t("home.online") : t("home.offline")} />
                    </a>
                  );
                })}
              </div>
            </Group>
          )}
        </div>
      </div>
      <MarkSeenDialog list={confirmSeen} onClose={() => setConfirmSeen(null)} />
    </div>
  );
}

// Marks every unread agent seen, after the dialog says how many (MarkSeenDialog).
function MarkAllSeen({ count, onClick }: { count: number; onClick: () => void }) {
  return (
    <button type="button" onClick={onClick} title={t("home.mark_all_seen_title", { count })} className="flex h-6 shrink-0 items-center gap-1 whitespace-nowrap rounded-control px-1.5 text-[12px] text-ink-3 transition-colors hover:bg-hover hover:text-ink-2">
      <Icon name="check" size={12} stroke={2.4} />
      {markAllSeenLabel()}
    </button>
  );
}

// `grid`: its rows go two, then three to a line as the page gets wider; "cards" (rows with buttons, read before
// answering) only once each still has room for its question and buttons.
const GRID = {
  rows: "grid grid-cols-1 items-start gap-1.5 @min-[60rem]:grid-cols-2 @min-[100rem]:grid-cols-3",
  cards: "grid grid-cols-1 items-start gap-1.5 @min-[84rem]:grid-cols-2 @min-[120rem]:grid-cols-3",
};

function Group({ title, count, children, right, hint, grid }: { title: string; count: number; children: React.ReactNode; right?: React.ReactNode; hint?: string; grid?: true | "cards" }) {
  return (
    <section>
      <div className={cx("flex items-baseline gap-2", hint ? "mb-1" : "mb-2.5")}>
        <h2 className="text-[13px] font-medium text-ink">{title}</h2>
        <span className="text-[12px] text-ink-3 tabular-nums">{count}</span>
        <span className="flex-1" />
        {right}
      </div>
      {/* what the section is and why its rows are here, for a name that does not say it alone */}
      {hint && <p className="mb-2.5 text-[12.5px] leading-[1.5] text-ink-2">{hint}</p>}
      <div className={grid ? GRID[grid === true ? "rows" : grid] : "flex flex-col gap-1.5"}>{children}</div>
    </section>
  );
}

// One task on the overview: its title (opens the task), its status, the stage bar with its PR, and what holds its
// delivery or how many agents it has (the Tasks page's words). The PR link is beside the title link, not inside it.
function TaskCard({ task: { todo, status, agents } }: { task: HomeTask }) {
  const to: Route = { name: "todos", id: todo.id };
  const held = heldDelivery(todo);
  const orphan = status !== "needs_you" ? orphanedLine(todo) : undefined;
  const line = [held && t(`todos.delivery_${held.state}`), agents > 0 && t("todos.agent_count", { count: agents })].filter(Boolean).join(" · ");
  return (
    <div className="flex flex-col gap-2 rounded-card bg-surface px-3 py-2.5 shadow-card" data-task={todo.id}>
      <div className="flex min-w-0 items-center gap-2">
        <a href={href(to)} onClick={(e) => onLinkClick(e, to)} className="min-w-0 flex-1 truncate text-[13px] font-medium text-ink hover:underline">
          {todo.title}
        </a>
        <span className={cx("inline-flex h-5 shrink-0 items-center rounded-full bg-field px-1.5 text-[11px] font-medium whitespace-nowrap", TODO_TONE_CLASS[TODO_STATUS_INFO[status].tone])}>{todoStatusLabel(todo, status)}</span>
      </div>
      <TodoStageBar stage={todo.stage} stageKind={todo.stage_kind} prURL={todo.pr_url} />
      {line && <p className={cx("truncate text-[11.5px]", held ? "text-orange" : "text-ink-3")}>{line}</p>}
      {/* nobody is working on it (#3618): the hub's finding and why; a card that waits for the user says that instead */}
      {orphan && (
        <p data-orphaned-line className="truncate text-[11.5px] text-ink-3">
          <span className="font-medium text-orange">{orphan.lead}</span>
          {orphan.reason && <> · {orphan.reason}</>}
        </p>
      )}
    </div>
  );
}

function Pill({ children }: { children: React.ReactNode }) {
  return <span className="inline-flex h-5 shrink-0 items-center rounded-full bg-field px-1.5 text-[11px] font-medium whitespace-nowrap text-attention">{children}</span>;
}

/** When a row last changed or how long it has worked: quiet text, its badge already says the state. */
function When({ children }: { children: React.ReactNode }) {
  return <span className="shrink-0 text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums">{children}</span>;
}

/** A row's place as small tags, without one that only repeats the title: a tab is often named after its agent's first
 *  words, and the title is not said twice. A short name repeats it only when equal ("app" is not "app store"'s). */
export function rowTags(title: string, parts: (string | false | null | undefined)[]): string[] {
  const norm = (x: string) => x.replace(/[.…\s]+$/u, "").trim().toLowerCase();
  const head = norm(title);
  const repeats = (x: string) => {
    const tag = norm(x);
    if (tag === head) return true;
    const [short, long] = tag.length < head.length ? [tag, head] : [head, tag];
    return short.length >= 8 && long.startsWith(short);
  };
  const kept = parts.filter((x): x is string => typeof x === "string" && x.trim() !== "").map((x) => x.trim());
  return [...new Set(kept)].filter((x) => !repeats(x));
}

function SessionTask({ s, i, machine, pill, footer }: { s: Session; i: number; machine: string; pill: React.ReactNode; footer?: React.ReactNode }) {
  const title = sessionTitle(s);
  // the source is a tag once, not again in front of the latest words
  return <TaskRow i={i} state={stateOf(s)} to={{ name: "session", mid: s.machine_id, sid: s.session_id }} title={title} tags={rowTags(title, [sourceSubtitle(s), machine, s.space_name, tabLabel(s)])} detail={sessionPreview(s)} pill={pill} footer={footer} />;
}

// One tab's agents as one row (#2320): the "Lead"'s title (the first pane's when the panes are peers), the most urgent
// one's latest words and state, and how many there are; it opens the tab on that most urgent pane.
function GroupTask({ g, i, machine, pill, footer }: { g: TabGroup; i: number; machine: string; pill: React.ReactNode; footer?: React.ReactNode }) {
  if (g.sessions.length === 1) return <SessionTask s={g.lead} i={i} machine={machine} pill={pill} footer={footer} />;
  const head = g.launcher ?? g.sessions[0]!;
  const s = g.lead;
  const preview = sessionPreview(s);
  return (
    <TaskRow
      i={i}
      state={g.state}
      to={paneRoute(s)}
      title={sessionTitle(head)}
      tags={rowTags(sessionTitle(head), [machine, s.space_name, tabLabel(s)])}
      detail={s === head ? preview : agentLabel(s) && preview ? t("home.agent_preview", { agent: agentLabel(s), preview }) : agentLabel(s) || preview}
      pill={
        <>
          <span className="flex shrink-0 items-center gap-1 text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums" title={g.sessions.map((x) => `${agentLabel(x)} · ${sessionTitle(x)} · ${STATE_LABEL[stateOf(x)]}`).join("\n")}>
            <span aria-hidden className="flex items-center gap-0.5">
              {g.sessions.map((x) => <StatusMark key={x.session_id} state={stateOf(x)} size="sm" />)}
            </span>
            <span className="max-sm:sr-only">{t("home.machine_agents", { count: g.sessions.length })}</span>
          </span>
          {pill}
        </>
      }
      footer={footer}
    />
  );
}

// A finished agent handed its next piece of work from its row (#6449, the web side of the Lark bot's "reply to the done
// card to go on", #5438): one line, sent the way the chat page sends and kept in its outbox, so the chat shows the same
// message (and, when it did not go, the same reason and Retry). Sent, the agent starts working and its row moves to
// Working with the hub's next push. The agent is the one the row named when the line was opened: in a tab of several,
// the row's lead can change while the user types.
function ContinueAction({ s, title, several }: { s: Session; title: string; several: boolean }) {
  const [to, setTo] = useState<Session | null>(null);
  const [text, setText] = useState("");
  const [phase, setPhase] = useState<"idle" | "sending" | "sent" | "failed">("idle");
  const [failed, setFailed] = useState<{ id: string; why: string } | null>(null);
  const wrap = useRef<HTMLDivElement>(null);
  const refocus = useRef(false);
  useEffect(() => {
    if (!to && refocus.current) {
      refocus.current = false;
      wrap.current?.querySelector("button")?.focus(); // back where the keyboard was, not the page's top
    }
  }, [to]);
  const close = () => {
    refocus.current = true;
    setTo(null);
  };
  const send = async () => {
    const words = text.trim();
    if (!to || !words || phase === "sending") return;
    const { machine_id: mid, session_id: sid } = to;
    setPhase("sending");
    // the line again after a failure is the outbox's Retry of that message (same id while the hub never answered, so
    // it goes in once); changed, the old one is dropped and this one goes as new
    const prev = failed && outboxEntry(mid, sid, failed.id);
    if (prev && prev.text !== words) discardOutbox(mid, sid, prev.id);
    const r = prev?.state === "failed" && prev.text === words ? retryOutboxEntry(mid, sid, prev.id) : queueToSession(mid, sid, { text: words });
    if (await r.sent) {
      setFailed(null);
      setText("");
      setPhase("sent");
      close();
    } else {
      setFailed({ id: r.id, why: outboxEntry(mid, sid, r.id)?.error ?? "" });
      setPhase("failed");
    }
  };
  const who = to && several ? agentLabel(to) || sessionTitle(to) : "";
  return (
    <div ref={wrap} className="flex flex-col gap-1 px-3 pb-2 pl-10.5" data-continue>
      {to ? (
        <form className="flex items-center gap-1.5" onSubmit={(e) => { e.preventDefault(); void send(); }}>
          <input
            autoFocus
            value={text}
            readOnly={phase === "sending"}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              // the Enter that picks an input-method candidate is the input method's, not a send (#2733, Safari #4667)
              if (e.nativeEvent.isComposing || e.keyCode === 229) {
                if (e.key === "Enter") e.preventDefault();
                return;
              }
              if (e.key === "Escape") close();
            }}
            aria-label={who ? t("home.continue_placeholder_to", { agent: who }) : t("home.continue_placeholder")}
            placeholder={who ? t("home.continue_placeholder_to", { agent: who }) : t("home.continue_placeholder")}
            className="min-w-0 flex-1 rounded-control bg-field px-2.5 py-1.5 text-[13px] text-ink outline-none placeholder:text-ink-3"
          />
          <Button tone="primary" type="submit" busy={phase === "sending"} disabled={!text.trim() || phase === "sending"}>{t("home.continue_send")}</Button>
        </form>
      ) : (
        <div className="flex">
          <Button tone="secondary" icon="send" onClick={() => { setTo(s); setPhase("idle"); }}>
            {t("home.continue")}
            {/* every row has one: the row's title tells them apart to a screen reader */}
            <span className="sr-only">{t("home.continue_label", { title })}</span>
          </Button>
        </div>
      )}
      {/* always there, so screen readers read what it comes to say */}
      <span role="status" className={cx("truncate text-[12px]", phase === "failed" ? "text-red" : "text-ink-3")}>
        {phase === "sent" ? t("home.continue_sent") : phase === "failed" && to ? (failed?.why ? t("home.continue_failed", { why: failed.why }) : t("home.continue_failed_plain")) : ""}
      </span>
    </div>
  );
}

const ANSWERED_HOLD_MS = 1600;

/** A row's one-tap answers (lib/quick-answers.ts), under its words; the one being sent spins, the others wait. An answer
 *  on its way from anywhere (this row, the chat's card, a notification) holds them all too, also after leaving Overview
 *  and coming back, and the row says which one, as the chat's card does (#2793). */
function QuickAnswers({ ix, choices, sending, onPick }: { ix: Interaction; choices: QuickChoice[]; sending?: string; onPick: (c: QuickChoice) => void }) {
  const { pending, line } = usePendingLine(ix);
  return (
    <div className="flex flex-wrap gap-1.5 px-3 pb-2 pl-10.5">
      {line && <p data-row-pending role="status" className="basis-full truncate text-[12px] text-ink-2">{line}</p>}
      {choices.map((c) => {
        const shown = optionDisplay(c.label);
        return (
          <Button key={c.key} tone={c.primary ? "primary" : "secondary"} busy={sending === c.key} disabled={sending !== undefined || pending} title={c.hint || shown.text} onClick={() => onPick(c)} className="max-w-full">
            <span className="min-w-0 truncate">{shown.text}</span>
            {shown.recommended && <RecommendedTag />}
          </Button>
        );
      })}
    </div>
  );
}

/** In an answered row the buttons' place: "Answered" and the answer, a button's height, for a moment before it folds. */
function AnsweredLine({ ix, body }: { ix: Interaction; body: Resolution }) {
  // the hub may say it was answered already, elsewhere: what took effect, as the chat's answered card shows it
  const done = useStore((s) => s.answered[ix.id]);
  const answer = answerLines(ix, done?.resolution ?? body).map((l) => optionDisplay(l.answer).text).join(t("lib-answered.separator"));
  return (
    <div className="px-3 pb-2 pl-10.5">
      <p role="status" className="flex h-8 min-w-0 items-center gap-1.5 text-[12.5px] font-medium text-green" style={{ animation: `fade-in 200ms ${EASE} both` }}>
        <Icon name="check" size={13} stroke={2.6} className="shrink-0" />
        <span className="truncate">{t("home.answered", { answer })}</span>
      </p>
    </div>
  );
}

// One Task Rows capsule, compact: status badge, the title alone on its line (it is what tells rows apart), then
// where it runs as small tags and the latest words; when on the right. With `actions` (a card's one-tap answers) they
// sit under it in the same capsule, outside the link, and its words take up to three lines: read before answering.
function TaskRow({ i, state, done, to, title, tags, detail, pill, entry, actions, footer }: { i: number; state: AgentState; done?: boolean; to: Route; title: string; tags: string[]; detail?: string; pill: React.ReactNode; entry?: Record<string, unknown>; actions?: React.ReactNode; /** Under the row, the row itself drawn as without it (Continue, #6449). */ footer?: React.ReactNode }) {
  const below = actions ?? footer;
  const enter = { animation: `fade-up 420ms ${EASE} ${Math.min(i, 8) * 50}ms both` };
  const link = (
    <a
      href={href(to)}
      onClick={(e) => onLinkClick(e, to, entry)}
      title={[title, ...tags].join(" · ")}
      className={cx("group flex min-w-0 items-center gap-2.5 rounded-card px-3 py-2 transition-[background-color,transform] duration-150", !below && "bg-surface shadow-card hover:bg-inset active:scale-[0.995]")}
      style={below ? undefined : enter}
    >
      {done ? (
        <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-field text-green" aria-label={t("chat-interactions.answered")}>
          <Icon name="check" size={11} stroke={3.2} />
        </span>
      ) : (
        <Badge state={state} />
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-[13.5px] leading-[18px] font-medium text-ink">{title}</span>
        {(detail || tags.length > 0) && (
          <span className={cx("mt-0.5 flex min-w-0 gap-1.5 text-[12.5px] leading-[18px] text-ink-2", actions ? "flex-col items-start gap-0.5" : "items-center")}>
            {tags.length > 0 && (
              <span className="hidden shrink-0 items-center gap-1 sm:flex" data-row-tags>
                {tags.map((x) => (
                  <span key={x} className="max-w-[12em] truncate rounded-chip bg-field px-1.5 text-[11px] leading-[17px] text-ink-3">{x}</span>
                ))}
              </span>
            )}
            {detail && <span className={cx("min-w-0", actions ? "line-clamp-3 w-full [overflow-wrap:anywhere]" : "flex-1 truncate")} title={actions ? detail : undefined}>{detail}</span>}
          </span>
        )}
      </span>
      {pill}
      <Icon name="chevronRight" size={13} stroke={2.2} className="shrink-0 text-ink-3 transition-transform duration-150 group-hover:translate-x-0.5" />
    </a>
  );
  if (!below) return link;
  return (
    // the capsule shades while the link part is hovered, not while a button is
    <div className="rounded-card bg-surface shadow-card transition-colors duration-150 has-[>a:hover]:bg-inset" style={enter}>
      {link}
      {below}
    </div>
  );
}

function Badge({ state }: { state: AgentState }) {
  if (state === "working") {
    const size = 20,
      stroke = 2,
      r = (size - stroke) / 2,
      c = 2 * Math.PI * r;
    return (
      <span className="relative inline-flex size-5 shrink-0 items-center justify-center" aria-label={STATE_LABEL[state]}>
        <svg width={size} height={size} className={`absolute inset-0 ${BUSY_MOTION}`} style={spinStyle(1100)}>
          <circle cx={10} cy={10} r={r} fill="none" stroke="var(--line)" strokeWidth={stroke} />
          <circle cx={10} cy={10} r={r} fill="none" stroke="var(--orange)" strokeWidth={stroke} strokeLinecap="round" strokeDasharray={`${c * 0.28} ${c * 0.72}`} />
        </svg>
      </span>
    );
  }
  if (state === "unread")
    return (
      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-green-tint text-green" aria-label={STATE_LABEL[state]}>
        <Icon name="check" size={11} stroke={3.2} />
      </span>
    );
  if (state === "needs_you")
    return (
      <span className="flex size-5 shrink-0 items-center justify-center rounded-full bg-field text-attention" aria-label={STATE_LABEL[state]}>
        <Icon name="hand" size={13} stroke={2.2} />
      </span>
    );
  return (
    <span className="flex size-5 shrink-0 items-center justify-center">
      <StatusMark state={state} />
    </span>
  );
}

/** An offline computer's line on the overview, read in the machines page's order (#4038): waiting to be confirmed, the
 * hub's error_code in our words, a planned restart, then the plain reason from the error text (#3633); else the raw
 * error or "Offline". */
export function offlineLine(m: Machine): string {
  if (isPending(m)) return t("machines.never_connected");
  const coded = machineErrorText(m.error_code);
  if (coded) return coded;
  if (machineDeployment(m).expectedDisconnect) return t("machines.waiting_node");
  return classifyConnection(m)?.title ?? (m.error || t("home.offline"));
}
