// A decision in the Needs you tray that can be answered on its row (#3804, docs/assistant/todos.md "Waiting for the
// owner"): what is to be decided, where the task came from and where it stands in one line, what each option leads to,
// the options as buttons (the recommended one first, the only solid one), what happens when nobody answers, and
// "Other…", which opens the task to answer in words. The answers that are no option — "You decide", "This isn't what I
// asked for", "Tell me the background first" — and the options that do not fit the row are in its ⋯ menu. Every answer
// goes through POST /todos/{id}/accept (lib/feed acceptBrief), as the recommendation's OK always did.
import { openTask } from "../../lib/handoff";
import { useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { acceptBrief, type AskAnswer } from "../../lib/feed";
import { clock } from "../../lib/format";
import { refreshTodos } from "../../lib/store";
import { askOptions, type AskOption, type AskResponse, type Todo, type TodoAsk } from "../../lib/todos";
import { Button, IconButton, Menu, toast, useMenu } from "../../ui/atoms";
import type { MenuItem } from "../../ui/menu-model";
import { RecommendedTag } from "../../ui/RecommendedTag";

/** Where answering the round stands: not yet, being sent, or answered (with what the row then says). */
type Answering = { state: "idle" } | { state: "busy"; key: string } | { state: "done"; text: string };

/** The fixed answers in the menu's order. */
const RESPONSES: AskResponse[] = ["decide_yourself", "not_what_i_asked", "explain_first"];

/** The fixed answers this hub takes for the ask; none from a hub that does not know them. */
function askResponses(ask: TodoAsk): AskResponse[] {
  return RESPONSES.filter((r) => ask.responses?.includes(r));
}

const RESPONSE_LABEL: Record<AskResponse, string> = {
  decide_yourself: "assistant-decisionrow.decide_yourself",
  not_what_i_asked: "assistant-decisionrow.not_what_i_asked",
  explain_first: "assistant-decisionrow.explain_first",
};
const RESPONSE_DETAIL: Record<AskResponse, string> = {
  decide_yourself: "assistant-decisionrow.decide_yourself_detail",
  not_what_i_asked: "assistant-decisionrow.not_what_i_asked_detail",
  explain_first: "assistant-decisionrow.explain_first_detail",
};

/** What happens when nobody answers: the recommendation is taken at the deadline, or never; "" when the hub said neither. */
export function defaultLine(ask: Pick<TodoAsk, "default" | "deadline">): string {
  if (ask.default === "never") return t("assistant-decisionrow.default_never");
  const at = ask.default === "taken" ? clock(ask.deadline) : "";
  return at ? t("assistant-decisionrow.default_at", { time: at }) : "";
}

/** How many of the option buttons fit beside the row's fixed controls, the first always: each button's width is read
 *  while it is drawn and kept, so a row that widens again gets its buttons back. All of them until it is laid out. */
function useFitting(count: number, round: string) {
  const row = useRef<HTMLDivElement>(null);
  const widths = useRef<number[]>([]);
  const [fit, setFit] = useState(count);
  useLayoutEffect(() => {
    widths.current = [];
    setFit(count);
  }, [count, round]);
  useLayoutEffect(() => {
    const el = row.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const measure = () => {
      const options = [...el.querySelectorAll<HTMLElement>("[data-decision-option]")];
      // a label's own width, also while its button is cut short: the button less what the label shows, plus all of it
      options.forEach((label, i) => {
        const button = label.closest("button") ?? label;
        widths.current[i] = button.offsetWidth - label.clientWidth + label.scrollWidth;
      });
      if (widths.current.length < count || !el.clientWidth) return; // not laid out, or some never drawn yet
      const box = getComputedStyle(el);
      const gap = parseFloat(box.columnGap) || 0;
      let left = el.clientWidth - (parseFloat(box.paddingLeft) || 0) - (parseFloat(box.paddingRight) || 0);
      for (const tail of el.querySelectorAll<HTMLElement>("[data-decision-tail]")) left -= tail.offsetWidth + gap;
      let n = 0;
      for (const w of widths.current.slice(0, count)) {
        if (n > 0 && w > left) break;
        left -= w + gap;
        n++;
      }
      setFit(Math.max(1, n));
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [count, round, fit]);
  return { row, fit: Math.min(fit, count) };
}

export function DecisionRow({ todo, ask, rowKey, admission, onBusyChange, blocked = false, footer }: { todo: Pick<Todo, "id" | "title">; ask: TodoAsk; rowKey: string; admission?: { current: boolean }; onBusyChange?: (busy: boolean) => void; blocked?: boolean; footer?: ReactNode }) {
  const options = askOptions(ask);
  const responses = askResponses(ask);
  const [answering, setAnswering] = useState<Answering>({ state: "idle" });
  // Admit one answer synchronously, before React disables the other buttons.
  const ownAdmission = useRef(false);
  const admitted = admission ?? ownAdmission;
  const menu = useMenu();
  const { row, fit } = useFitting(options.length, ask.asked_at);
  const busy = answering.state === "busy";
  const disabled = busy || blocked;
  const open = () => openTask(todo.id);

  const answer = (key: string, body: AskAnswer, done: string) => {
    if (admitted.current || answering.state !== "idle") return;
    admitted.current = true;
    onBusyChange?.(true);
    setAnswering({ state: "busy", key });
    acceptBrief(todo.id, ask.asked_at, body).then(
      (res) => {
        setAnswering({ state: "done", text: res === "handled" ? t("assistant-brief.handled") : done });
        if (res === "handled") void refreshTodos(); // acceptBrief reads them again only when the hub took it
      },
      (e) => {
        admitted.current = false;
        onBusyChange?.(false);
        setAnswering({ state: "idle" });
        toast(t("assistant-decisionrow.failed", { error: errorText(e) }), "error");
      },
    );
  };
  // A step that cannot be undone, or that other people will see, takes two presses: the first arms the option.
  const twoStep = ask.why_owner === "irreversible" || ask.why_owner === "visible_to_others";
  const [armed, setArmed] = useState<number | null>(null);
  const choose = (o: AskOption, i: number) => {
    if (twoStep && armed !== i) {
      setArmed(i);
      return;
    }
    answer(`option:${i}`, { option: o.label }, t("assistant-decisionrow.chosen", { option: o.label }));
  };
  const respond = (r: AskResponse) => answer(`response:${r}`, { response: r }, t("assistant-decisionrow.responded"));

  const items: MenuItem[] = [
    ...options.slice(fit).map((o, i): MenuItem => ({ key: `option:${fit + i}`, label: armed === fit + i ? t("assistant-decisionrow.confirm", { option: o.label }) : o.label, detail: o.leads_to, onSelect: () => choose(o, fit + i) })),
    ...responses.map((r, i): MenuItem => ({ key: `response:${r}`, label: t(RESPONSE_LABEL[r]), detail: t(RESPONSE_DETAIL[r]), divider: i === 0 && fit < options.length, onSelect: () => respond(r) })),
  ];
  const fallback = defaultLine(ask);
  const question = ask.question || ask.verdict;

  return (
    <div data-needs-row={rowKey} data-decision-row={ask.why_owner || ""} className="flex min-w-0 flex-col gap-1.5 rounded-control bg-field px-3 py-2.5">
      <div className="flex min-w-0 items-center gap-2">
        <button type="button" onClick={open} className="block min-w-0 flex-1 truncate text-left text-[12px] text-ink-2 hover:underline" title={t("assistant-brief.open_row", { title: todo.title })}>
          {todo.title}
        </button>
        <span className="shrink-0 text-xs text-ink-2">{t("assistant-tasks.review_needs_user")}</span>
      </div>
      <p data-decision-question className="text-[13.5px] font-medium select-text [overflow-wrap:anywhere]">
        {question}
      </p>
      {ask.background && (
        <p data-decision-background className="line-clamp-2 text-[12px] text-ink-2 [overflow-wrap:anywhere]" title={ask.background}>
          {ask.background}
        </p>
      )}
      <ul className="flex min-w-0 flex-col gap-0.5 text-[12px] text-ink-2">
        {options.map((o, i) => (
          <li key={i} data-decision-leads className="line-clamp-2 [overflow-wrap:anywhere]" title={o.leads_to ? t("assistant-decisionrow.leads", { option: o.label, leads: o.leads_to }) : o.label}>
            <span className="font-medium text-ink">{o.label}</span>
            {i === 0 && <RecommendedTag />}
            {o.leads_to ? t("assistant-decisionrow.leads_to", { leads: o.leads_to }) : ""}
          </li>
        ))}
      </ul>
      {(fallback || ask.cost_of_waiting) && (
        <p className="text-[12px] text-ink-2 [overflow-wrap:anywhere]">
          {fallback && <span data-decision-default={ask.default}>{fallback}</span>}
          {fallback && ask.cost_of_waiting ? t("assistant-decisionrow.separator") : ""}
          {ask.cost_of_waiting && <span data-decision-cost>{t("assistant-decisionrow.cost", { cost: ask.cost_of_waiting })}</span>}
        </p>
      )}
      {ask.response && answering.state !== "done" && (
        <p data-decision-response={ask.response} className="truncate text-[12px] text-ink-2">
          {t("assistant-decisionrow.responded")}
        </p>
      )}
      <div className={footer ? "sticky -bottom-2.5 bg-field" : undefined}>
      {answering.state === "done" ? (
        <p data-decision-done className="truncate text-[12.5px] text-ink-2" title={answering.text}>
          {answering.text}
        </p>
      ) : (
        // the padding keeps the buttons' rings and shadows inside what the row clips
        <div ref={row} className="-m-1 flex min-w-0 items-center gap-2 overflow-hidden p-1">
          {options.slice(0, fit).map((o, i) => (
            <Button
              key={i}
              tone={i === 0 ? "primary" : "secondary"}
              busy={busy && answering.key === `option:${i}`}
              disabled={disabled}
              title={i === 0 ? t("assistant-decisionrow.choose_recommended", { option: o.label, title: todo.title }) : t("assistant-decisionrow.choose", { option: o.label, title: todo.title })}
              onClick={() => choose(o, i)}
              // alone and still wider than the row, the button gives way and cuts its label; it never wraps
              className={fit === 1 ? "min-w-0 shrink!" : ""}
            >
              <span data-decision-option={i} data-decision-armed={armed === i ? "" : undefined} className="truncate">
                {armed === i ? t("assistant-decisionrow.confirm", { option: o.label }) : o.label}
              </span>
            </Button>
          ))}
          <span data-decision-tail className="flex shrink-0">
            <Button tone="quiet" disabled={disabled} title={t("assistant-decisionrow.another_row", { title: todo.title })} onClick={open}>
              {t("assistant-decisionrow.another")}
            </Button>
          </span>
          {items.length > 0 && (
            <span data-decision-tail className="flex shrink-0">
              <IconButton icon="more" label={t("assistant-decisionrow.more")} onClick={menu.open} active={Boolean(menu.anchor)} disabled={disabled} size={32} />
            </span>
          )}
        </div>
      )}
      {footer}
      </div>
      {menu.anchor && <Menu anchor={menu.anchor} onClose={menu.close} side="top" align="right" width={264} items={items} />}
    </div>
  );
}
