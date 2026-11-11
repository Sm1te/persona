// Task-review decisions use the existing row and draft actions, including legacy reviews without an acceptance ask.
// Dismissal is round-bound and persisted by the manager; Later only defers this browser session, as on reply drafts.
import { openTask } from "../../lib/handoff";
import { useRef, useState } from "react";
import { t } from "../../i18n";
import { api, credentialOwner, enc, errorText } from "../../lib/api";
import { decisionLaterKey, decisionRound } from "../../lib/assistant-entries";
import { later } from "../../lib/feed";
import { defaultManager, MANAGER_HEADER, shownManager } from "../../lib/manager-choice";
import { getState, putTodo, refreshTodos } from "../../lib/store";
import { latestReview } from "../../lib/supervision";
import { askOptions, openAsk, type Todo } from "../../lib/todos";
import { Button, toast } from "../../ui/atoms";
import { AcceptRow } from "./Brief";
import { DecisionRow } from "./DecisionRow";

type Review = NonNullable<Todo["decision_review"]>;

export function TaskDecision({ todo, rowKey }: { todo: Todo; rowKey: string }) {
  const ask = openAsk(todo);
  const review = latestReview(todo.supervision);
  const round = decisionRound(todo);
  const admission = useRef(false);
  const [busy, setBusy] = useState(false);
  const [dismissing, setDismissing] = useState(false);
  const owner = credentialOwner();
  const manager = todo.manager || defaultManager();
  const selection = shownManager();
  const current = () => owner === credentialOwner() && selection === shownManager();
  const open = () => openTask(todo.id);
  const apply = (state: Review) => {
    if (!current()) return;
    const now = getState().todos.find((t) => t.id === todo.id && (t.manager || defaultManager()) === manager);
    // An old write cannot hide a newer review that arrived while it was pending.
    if (now && decisionRound(now) === round) putTodo({ ...now, decision_review: state });
    void refreshTodos();
  };
  const write = (undo?: string) => api<Review>(`/todos/${enc(todo.id)}/decision-${undo ? "undo-dismiss" : "dismiss"}`, {
    method: "POST", json: { review_at: round, ...(undo ? { dismissal_id: undo } : {}) },
    headers: { [MANAGER_HEADER]: manager }, isAuthContextCurrent: current,
  });
  const dismiss = async () => {
    if (admission.current || !round || !current()) return;
    admission.current = true;
    setBusy(true);
    setDismissing(true);
    try {
      const state = await write();
      if (!current()) return;
      apply(state);
      toast(t("assistant-decisionrow.dismissed"), "ok", {
        label: t("assistant-tasks.undo_dismiss"),
        run: async () => {
          if (!current() || !state.dismissal_id) return;
          try {
            const restored = await write(state.dismissal_id);
            apply(restored);
          } catch (error) {
            if (current()) toast(t("assistant-decisionrow.undo_failed", { error: errorText(error) }), "error");
          }
        },
      });
    } catch (error) {
      admission.current = false;
      setBusy(false);
      setDismissing(false);
      if (current()) toast(t("assistant-tasks.dismiss_failed", { error: errorText(error) }), "error");
    }
  };
  const defer = () => {
    if (admission.current || !current()) return;
    later(decisionLaterKey(todo));
  };
  const controls = <div data-decision-actions className={`${ask && askOptions(ask).length ? "" : "sticky -bottom-2.5 "}-mx-3 -mb-2.5 mt-1 flex items-center gap-2 border-t border-line bg-field px-3 py-2.5`}>
    <Button className="min-h-11" disabled={busy} onClick={defer}>{t("assistant-tasks.later")}</Button>
    <Button className="min-h-11" disabled={busy || !round} busy={dismissing} onClick={dismiss}>{t("assistant-tasks.dismiss_draft")}</Button>
  </div>;
  if (ask && askOptions(ask).length) return <DecisionRow todo={todo} ask={ask} rowKey={rowKey} admission={admission} onBusyChange={setBusy} blocked={busy} footer={controls} />;
  return <div data-needs-row={rowKey} className="flex min-w-0 flex-col gap-1.5 rounded-control bg-field px-3 py-2.5">
    {ask ? <AcceptRow stacked admission={admission} onBusyChange={setBusy} blocked={busy} row={{ id: todo.id, title: todo.title, waited_s: Math.max(0, (Date.now() - Date.parse(ask.asked_at)) / 1000), outcome: ask.outcome, verdict: ask.verdict, recommend: ask.recommend, asked_at: ask.asked_at }} /> : <>
      <div className="flex min-w-0 items-center gap-2 text-sm"><button type="button" onClick={open} className="min-w-0 flex-1 truncate text-left font-medium hover:underline">{todo.title}</button><span className="shrink-0 text-xs text-ink-2">{t("assistant-tasks.review_needs_user")}</span></div>
      {(review?.reason || review?.note) && <p data-decision-legacy className="whitespace-pre-wrap text-sm select-text [overflow-wrap:anywhere]">{review.reason || review.note}</p>}
      {review?.recommend && <p className="text-xs text-ink-2">{t("assistant-brief.recommend", { option: review.recommend })}</p>}
      {review?.options?.length ? <ul className="text-xs text-ink-2">{review.options.map((option) => <li key={option.label}>{option.label}{option.leads_to ? t("assistant-decisionrow.leads_to", { leads: option.leads_to }) : ""}</li>)}</ul> : null}
      <p className="text-xs text-ink-2">{t("assistant-decisionrow.missing_options")}</p>
      <div><Button disabled={busy} onClick={open}>{t("assistant-decisionrow.another")}</Button></div>
    </>}
    {controls}
  </div>;
}
