// The `brief` item of a morning or evening routine's conversation: the fixed part of the run (what waits for the user,
// what happened in the last hours, what is to be verified), as the hub worked it out when the run started; the same
// rows as the Lark card, with an Accept button for each task to verify and a Handled button for each question that can wait
// (docs/extensions/bots.md "Morning and evening briefings").
import { openTask } from "../../lib/handoff";
import { useRef, useState } from "react";
import { errorText } from "../../lib/api";
import { putTodo, refreshTodos } from "../../lib/store";
import { updateTodo } from "../../lib/todos";
import { acceptBrief, briefBody, briefSetupPage, failedKey, handledLater, type BriefBody, type BriefRow, type FeedItem } from "../../lib/feed";
import { shortDuration } from "../../lib/format";
import { navigate } from "../../lib/router";
import { formatDate, t } from "../../i18n";
import { Button, cx, toast } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { TodoStageBar } from "../TodoStageBar";

/** A section's rows shown at once (the hub cuts the lists as its card does; the totals say how many there were). */
function Section({ icon, tint, title, children }: { icon: IconName; tint: string; title: string; children: React.ReactNode }) {
  return (
    <section className="border-t border-line py-2 first:border-t-0" data-brief-section>
      <div role="heading" aria-level={3} className="mb-1 flex items-center gap-2 text-[12.5px] font-medium text-ink-2">
        <span className={cx("flex size-6 items-center justify-center rounded-control", tint)}>
          <Icon name={icon} size={13} />
        </span>
        {title}
      </div>
      <div className="flex flex-col">{children}</div>
    </section>
  );
}

function More({ n, text = "assistant-brief.more" }: { n: number; text?: string }) {
  return n > 0 ? <div className="py-1 text-[12px] text-ink-2">{t(text, { n })}</div> : null;
}

/** A link the hub gave, if it is a web address: anything else is not drawn as one. */
function webLink(link?: string): string | undefined {
  return link && /^https?:\/\//i.test(link) ? link : undefined;
}

function meta(row: BriefRow): string {
  const parts = [t("assistant-brief.waited", { wait: shortDuration(row.waited_s * 1000) })];
  if (row.nudged) parts.push(t("assistant-brief.nudged"));
  if (row.from) parts.push(row.from);
  return parts.join(" · ");
}

function WaitingRow({ row }: { row: BriefRow }) {
  return (
    <div className="flex items-center gap-3 py-1.5" data-brief-row="waiting">
      <div className="min-w-0 flex-1">
        <div className="text-[13.5px] font-medium [overflow-wrap:anywhere]">{row.title}</div>
        <div className="text-[12px] text-ink-2">{meta(row)}</div>
      </div>
      {webLink(row.link) && (
        <Button href={webLink(row.link)} title={t("assistant-brief.open_row", { title: row.title })}>
          {t("assistant-brief.open")}
        </Button>
      )}
    </div>
  );
}

/** What the Accept button of a task says: it asked, the hub took it, or the round is already over. */
type Accepting = "idle" | "busy" | "finishing" | "finished" | "accepted" | "handled";

/** What the button of a row to verify says: Accept a deployed task, Got it a conclusion, Take recommendation a decision. */
function acceptLabel(row: BriefRow): { label: string; title: string } {
  if (row.outcome === "decide") return { label: t("assistant-brief.take"), title: t("assistant-brief.take_row", { title: row.title }) };
  if (row.outcome) return { label: t("assistant-brief.got_it"), title: t("assistant-brief.accept_row", { title: row.title }) };
  return { label: t("assistant-brief.accept"), title: t("assistant-brief.accept_row", { title: row.title }) };
}

export function AcceptRow({ row, stacked = false, admission, onBusyChange, blocked = false }: { row: BriefRow; stacked?: boolean; admission?: { current: boolean }; onBusyChange?: (busy: boolean) => void; blocked?: boolean }) {
  const [state, setState] = useState<Accepting>("idle");
  // Admit either action synchronously, before React disables the other button.
  const ownAdmission = useRef(false);
  const admitted = admission ?? ownAdmission;
  const button = acceptLabel(row);
  const deciding = row.outcome === "decide";
  const settled = state === "accepted" || state === "handled" || state === "finished";
  // A decision with nothing recommended can still be finished, but has nothing to take.
  const takes = !deciding || !!row.recommend;
  const accept = () => {
    if (!row.asked_at || admitted.current || state !== "idle") return;
    admitted.current = true;
    onBusyChange?.(true);
    setState("busy");
    acceptBrief(row.id, row.asked_at).then(
      (res) => setState(res),
      (e) => {
        admitted.current = false;
        onBusyChange?.(false);
        setState("idle");
        toast(t("assistant-brief.accept_failed", { error: errorText(e) }), "error");
      },
    );
  };
  const finish = () => {
    if (!stacked || !row.asked_at || admitted.current || state !== "idle") return;
    admitted.current = true;
    onBusyChange?.(true);
    setState("finishing");
    // User completion only: unlike /accept, PATCH never routes the recommendation to the Agent.
    updateTodo(row.id, { done: true }).then(
      (todo) => {
        setState("finished");
        // putTodo merges optional fields; explicitly clear the ask omitted by a completed todo.
        putTodo({ ...todo, ask: todo.ask });
        void refreshTodos();
        toast(t("assistant-brief.finished"), "ok");
      },
      (e) => {
        admitted.current = false;
        onBusyChange?.(false);
        setState("idle");
        toast(t("assistant-brief.finish_failed", { error: errorText(e) }), "error");
      },
    );
  };
  return (
    <div className={stacked ? "flex flex-wrap items-center gap-x-3 gap-y-2 py-1.5" : "flex items-center gap-3 py-1.5"} data-brief-row="accept">
      <div className={stacked ? "w-full min-w-0" : "min-w-0 flex-1"}>
        <div className="text-[13.5px] font-medium [overflow-wrap:anywhere]">{row.title}</div>
        {row.verdict && (
          <div className="text-[12.5px] [overflow-wrap:anywhere]" data-brief-verdict={row.outcome ?? "deployed"}>
            {row.verdict}
          </div>
        )}
        {row.detail && <div className="text-[12px] text-ink-2 [overflow-wrap:anywhere]">{row.detail}</div>}
        {row.recommend && <div className="text-[12px] text-ink-2 [overflow-wrap:anywhere]">{t("assistant-brief.recommend", { option: row.recommend })}</div>}
        <div className="text-[12px] text-ink-2">{meta(row)}</div>
      </div>
      <Button title={t("assistant-brief.open_row", { title: row.title })} onClick={() => openTask(row.id)}>
        {t("assistant-brief.open")}
      </Button>
      {/* Only the live Pending row can finish a task; historical brief snapshots must not end a newer run. */}
      {stacked && deciding && !settled && (
        <Button busy={state === "finishing"} disabled={blocked || !row.asked_at || state === "busy"} title={t("assistant-brief.finish_row", { title: row.title })} onClick={finish}>
          {t("assistant-brief.finish")}
        </Button>
      )}
      {settled ? (
        <span className="shrink-0 text-[12.5px] text-ink-2" data-brief-accepted={state}>
          {t(state === "finished" ? "assistant-brief.finished" : state === "handled" ? "assistant-brief.handled" : deciding ? "assistant-brief.taken" : row.outcome ? "assistant-brief.noted" : "assistant-brief.accepted")}
        </span>
      ) : takes ? (
        <Button tone="primary" busy={state === "busy"} disabled={blocked || !row.asked_at || state === "finishing"} title={button.title} onClick={accept}>
          {button.label}
        </Button>
      ) : null}
    </div>
  );
}

/** What the Handled button of a question that can wait says: idle, the hub took it, or it was not in the queue any more. */
type Marking = "idle" | "busy" | "marked" | "handled";

function LaterRow({ row, conversation }: { row: BriefRow; conversation: string }) {
  const [state, setState] = useState<Marking>("idle");
  const mark = () => {
    if (state !== "idle") return;
    setState("busy");
    handledLater(row.id, conversation).then(
      (res) => setState(res),
      (e) => {
        setState("idle");
        toast(t("assistant-brief.later_failed", { error: String(e instanceof Error ? e.message : e) }), "error");
      },
    );
  };
  return (
    <div className="flex items-center gap-3 py-1.5" data-brief-row="later">
      <div className="min-w-0 flex-1">
        <div className="text-[13.5px] font-medium [overflow-wrap:anywhere]">{row.title}</div>
        <div className="text-[12px] text-ink-2">{meta(row)}</div>
      </div>
      {webLink(row.link) && (
        <Button href={webLink(row.link)} title={t("assistant-brief.open_row", { title: row.title })}>
          {t("assistant-brief.open")}
        </Button>
      )}
      {state === "marked" || state === "handled" ? (
        <span className="shrink-0 text-[12.5px] text-ink-2" data-brief-marked={state}>
          {t(state === "marked" ? "assistant-brief.later_marked" : "assistant-brief.handled")}
        </span>
      ) : (
        <Button busy={state === "busy"} title={t("assistant-brief.later_done_row", { title: row.title })} onClick={mark}>
          {t("assistant-brief.later_done")}
        </Button>
      )}
    </div>
  );
}

function clock(iso: string): string {
  return formatDate(iso, { hour: "2-digit", minute: "2-digit" });
}

export function BriefCard({ item }: { item: FeedItem }) {
  const body: BriefBody | undefined = briefBody(item);
  if (!body) return null;
  const past = body.failed.length + body.done.length;
  return (
    <div className="flex flex-col rounded-card bg-surface p-4 shadow-card" data-testid="brief-card">
      <div className="mb-2 flex items-center gap-1.5 text-[12px] font-medium text-ink-2">
        <Icon name="sun" size={13} />
        {body.title || t("assistant-brief.title")}
      </div>

      {body.waiting_unknown ? (
        <Section icon="hand" tint="bg-field text-attention" title={t("assistant-brief.waiting_head", { n: "?" })}>
          <div className="py-1 text-[13px] text-ink-2">{t("assistant-brief.waiting_unknown")}</div>
        </Section>
      ) : (
        <Section icon="hand" tint="bg-field text-attention" title={t("assistant-brief.waiting_head", { n: body.waiting.length })}>
          {body.waiting.length === 0 && <div className="py-1 text-[13px] text-ink-2">{t("assistant-brief.none")}</div>}
          {body.waiting.map((r) => (
            <WaitingRow key={r.id} row={r} />
          ))}
        </Section>
      )}

      <Section icon="clock" tint="bg-accent-tint text-accent" title={t("assistant-brief.past_head", { hours: body.hours, done: body.done_total, failed: body.failed_total })}>
        {past === 0 && <div className="py-1 text-[13px] text-ink-2">{t("assistant-brief.none")}</div>}
        {body.failed.map((x) => (
          <div key={`${x.kind}-${x.id}-${x.at}`} className="flex items-start gap-2 py-1 text-[13px]" data-brief-row="failed">
            <Icon name="stop" size={11} className="mt-1.5 shrink-0 text-attention" />
            <div className="min-w-0 flex-1 [overflow-wrap:anywhere]">
              <span className="font-medium">{x.title}</span> · {t(failedKey(x), { time: clock(x.at) })}
              {x.why && <span className="text-ink-2"> — {x.why}</span>}
            </div>
          </div>
        ))}
        {body.done.map((x) => (
          <div key={x.id} className="flex items-start gap-2 py-1 text-[13px]" data-brief-row="done">
            <Icon name="check" size={12} className="mt-1 shrink-0 text-green" />
            <div className="min-w-0 flex-1 [overflow-wrap:anywhere]">
              <span className="font-medium">{x.title}</span> · {clock(x.at)}
            </div>
          </div>
        ))}
        <More n={body.done_total + body.failed_total - past} />
      </Section>

      <Section icon="check" tint="bg-green-tint text-green" title={t("assistant-brief.accept_head", { n: body.accept.length })}>
        {body.accept.length === 0 && <div className="py-1 text-[13px] text-ink-2">{t("assistant-brief.none")}</div>}
        {body.accept.map((r) => (
          <AcceptRow key={r.id + (r.asked_at ?? "")} row={r} />
        ))}
      </Section>
      {body.later.length > 0 && (
        <Section icon="chat" tint="bg-field text-ink-2" title={t("assistant-brief.later_head", { n: body.later_total })}>
          {body.later.map((r) => (
            <LaterRow key={r.id} row={r} conversation={item.conversation} />
          ))}
          <More n={body.later_total - body.later.length} text="assistant-brief.later_more" />
        </Section>
      )}
      {body.progress_total > 0 && (
        <Section icon="gear" tint="bg-orange-tint text-orange" title={t("assistant-brief.progress_head", { n: body.progress_total })}>
          {body.progress.map((p) => (
            <div key={p.id} className="flex flex-col gap-1 py-1.5" data-brief-row="progress">
              <div className="flex items-center gap-3">
                <div className="min-w-0 flex-1 text-[13.5px] font-medium [overflow-wrap:anywhere]">{p.title}</div>
                <Button onClick={() => openTask(p.id)}>{t("assistant-brief.open")}</Button>
              </div>
              <TodoStageBar stage={p.stage} stageKind={p.stage_kind} />
            </div>
          ))}
          <More n={body.progress_total - body.progress.length} />
        </Section>
      )}
      {/* The week's recommended setup (#5085): the hub's own counts and words, drawn as they came. Opening the settings
          page is all a row does: nothing is turned on from here. */}
      {body.setup.length > 0 && (
        <Section icon="spark" tint="bg-field text-ink-2" title={t("assistant-brief.setup_head")}>
          <div className="py-1 text-[12px] text-ink-2">{t("assistant-brief.setup_intro")}</div>
          {body.setup.map((x) => {
            const page = briefSetupPage(x.id);
            return (
              <div key={x.id} className="flex items-center gap-3 py-1.5" data-brief-row="setup">
                <div className="min-w-0 flex-1 text-[13px] [overflow-wrap:anywhere]">{x.text}</div>
                {page && <Button onClick={() => navigate(page)}>{t("assistant-brief.setup_open")}</Button>}
              </div>
            );
          })}
        </Section>
      )}
    </div>
  );
}
