// "All" on the assistant's trays, the dock and the "Today" card: the task board, a chunk of its own loaded when it opens
// (#4656), so neither it nor lib/task-board is part of the first screen.
import { useMemo, useState } from "react";
import { boardSections, type BoardSectionKey } from "../../lib/task-board";
import { startFailure, useFeed } from "../../lib/feed";
import { plainMarkdown } from "../../lib/plain-markdown";
import { getState, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { dockAgents, isRemovable, mostUrgentAgent, openAsk, type Todo, type TodoStatus } from "../../lib/todos";
import { usageLimitLabel } from "../../lib/usage-limit";
import { sessionTitle } from "../../lib/format";
import { stateName } from "../../lib/state-labels";
import { t } from "../../i18n";
import { Dialog, IconButton, Menu } from "../../ui/atoms";
import { StatusMark } from "../../ui/StatusMark";
import { Icon } from "../../ui/icons";
import { answeredSessions } from "./answered-lookup";
import { Empty } from "./parts";
import { openAgent, openTask } from "../../lib/handoff";
import { place, progressOf, removeTaskNow, statusOf, TaskMark, taskAsk, taskInteraction, useTaskLookup } from "./Tasks";

const BOARD_TITLE: Record<BoardSectionKey, string> = {
  waiting: stateName("needs_you"),
  review: t("assistant-tasks.review_pending"),
  working: stateName("working"),
  unread: stateName("unread"),
  stopped: stateName("follow_up"),
  done: stateName("done"),
  failed: stateName("failed"),
};

/** What a manual agent on the board is doing: machine · project · agent · its last words (iOS agentSubtitle). */
function agentLine(s: Session): string {
  const machine = getState().machines.find((m) => m.id === s.machine_id)?.name ?? s.machine_id;
  const folder = s.cwd ? (s.cwd.split("/").filter(Boolean).at(-1) ?? s.cwd) : undefined;
  const last = s.last_text?.trim() ? plainMarkdown(s.last_text.trim()) : undefined;
  return [machine, folder, s.agent, last].filter(Boolean).join(" · ");
}

/**
 * "All" on the dock and on the "Today" card (iOS TaskBoardSheet): this conversation's tasks, and the agents its dock shows,
 * by where they are. A row opens the task (or the agent) beside the conversation; one nothing of runs can be removed.
 */
// Mounted only while open (TodayCard / TaskDock render it conditionally), so the store subscriptions and the
// session lookup below don't run for every "Today" card in the history on each session update.
export function TaskBoard({ open, conversation, onClose }: { open: boolean; conversation: string; onClose: () => void }) {
  const todos = useStore((s) => s.todos);
  const sessions = useStore((s) => s.sessions);
  const main = useStore((s) => s.assistant?.main);
  const lookup = useTaskLookup();
  const plans = useFeed((s) => s.plans);
  const interactions = useStore((s) => s.interactions);
  const answered = useStore((s) => s.answered);
  const [menuFor, setMenuFor] = useState<{ todo: Todo; el: HTMLElement } | null>(null);
  const sections = useMemo(() => {
    if (!open) return [];
    const rows = todos.filter((td) => td.origin === conversation).map((todo) => ({ todo, status: statusOf(todo, lookup, plans) }));
    return boardSections(rows, dockAgents(answeredSessions(Object.values(sessions).flat(), interactions, answered), todos, conversation, main));
  }, [open, todos, sessions, conversation, main, lookup, plans, interactions, answered]);
  const subtitle = (todo: Todo, status: TodoStatus) => {
    const lead = mostUrgentAgent(todo, lookup)?.session;
    const where = place(todo, lead);
    const failure = status === "failed" ? startFailure(todo, plans) : undefined;
    const ask = status === "needs_you" ? taskAsk(taskInteraction(todo, interactions, lead)) : undefined;
    const limit = status === "usage_limit" ? (usageLimitLabel(lead) ?? t("lib-usage-limit.short")) : undefined;
    // what its result waits on from the user (#2056), before its agent's last step
    const verdict = openAsk(todo)?.verdict;
    const now = failure ?? ask ?? limit ?? verdict ?? progressOf(lead)?.text;
    return [where, now].filter(Boolean).join(" · ");
  };
  return (
    <Dialog open={open} onClose={onClose} title={t("assistant-tasks.board_title")} width={480}>
      {sections.length === 0 ? (
        <Empty icon="todo" title={t("assistant-tasks.board_empty_title")}>
          {t("assistant-tasks.board_empty_body")}
        </Empty>
      ) : (
        <div className="flex flex-col gap-4" data-testid="task-board">
          {sections.map((sec) => (
            <section key={sec.key} aria-label={BOARD_TITLE[sec.key]}>
              <h3 className="mb-1 px-1 text-[12px] font-medium text-ink-3">
                {t("assistant-tasks.board_section", { title: BOARD_TITLE[sec.key], count: sec.rows.length + sec.agents.length })}
              </h3>
              <div className="divide-y divide-line overflow-hidden rounded-card bg-field">
                {sec.rows.map(({ todo, status }) => (
                  <div key={todo.id} className="flex items-center hover:bg-hover">
                    <button
                      type="button"
                      onClick={() => {
                        onClose();
                        openTask(todo.id);
                      }}
                      onContextMenu={(e) => {
                        if (!isRemovable(status)) return;
                        e.preventDefault();
                        setMenuFor({ todo, el: e.currentTarget });
                      }}
                      className="flex min-w-0 flex-1 items-center gap-3 px-3 py-2.5 text-left"
                    >
                      <span className="flex w-4 shrink-0 justify-center">
                        <TaskMark status={status} />
                      </span>
                      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                        <span className="truncate text-[13.5px] font-medium text-ink">{todo.title}</span>
                        <span className="truncate text-[12px] text-ink-3">{subtitle(todo, status)}</span>
                      </span>
                      <Icon name="chevronRight" size={13} className="shrink-0 text-ink-3" />
                    </button>
                    {isRemovable(status) && (
                      <IconButton icon="trash" label={t("assistant-tasks.board_remove", { title: todo.title })} onClick={() => removeTaskNow(todo)} className="mr-1.5" />
                    )}
                  </div>
                ))}
                {sec.agents.map(({ session: s, state }) => (
                  <button
                    key={`${s.machine_id}/${s.session_id}`}
                    type="button"
                    onClick={() => {
                      onClose();
                      openAgent(s.machine_id, s.session_id);
                    }}
                    className="flex w-full min-w-0 items-center gap-3 px-3 py-2.5 text-left hover:bg-hover"
                  >
                    <span className="flex w-4 shrink-0 justify-center">
                      <StatusMark state={state} />
                    </span>
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <span className="truncate text-[13.5px] font-medium text-ink">{sessionTitle(s)}</span>
                      <span className="flex min-w-0 items-center gap-1 text-[12px] text-ink-3">
                        <Icon name="agent" size={11} className="shrink-0" />
                        <span className="truncate">{agentLine(s)}</span>
                      </span>
                    </span>
                    <Icon name="chevronRight" size={13} className="shrink-0 text-ink-3" />
                  </button>
                ))}
              </div>
            </section>
          ))}
        </div>
      )}
      {menuFor && (
        <Menu
          anchor={menuFor.el}
          onClose={() => setMenuFor(null)}
          items={[{ key: "remove", label: t("assistant-tasks.remove_ellipsis"), icon: "trash", danger: true, onSelect: () => removeTaskNow(menuFor.todo) }]}
        />
      )}
    </Dialog>
  );
}
