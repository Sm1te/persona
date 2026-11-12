// A child session (a Codex subagent, an Agent a Paseo Agent created) as one card in its parent's chat (#5120, after cindy's
// AgentTaskCard): never a conversation of its own, and no link to one. The same TaskCard as a subagent call's: "Subagent",
// its title and its status word; then what ran it, the model it runs on with its effort, and how long it took, or
// "Running for …" each second while it runs. Its parent's transcript names no call for it: everything comes from the
// child's own row (lib/use-child-sessions.ts), which the card reads itself (a child's update redraws only its card). The head opens the card in
// place, remembered per card: what it was asked, its latest words (folded past four lines) and its last tool, read from
// the child's transcript only while it is open (lib/child-detail.ts).
import { memo, useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { sessionAgent, agentName } from "../../lib/agent-kind";
import { childCall, childCardKey } from "../../lib/child-cards";
import { readChildDetail, type ChildDetail } from "../../lib/child-detail";
import { stateOf } from "../../lib/format";
import { effortTitle } from "../../lib/session-actions";
import { getSession, useStore } from "../../lib/store";
import { modelChipLabel } from "../../lib/tasks";
import { useChildRow } from "../../lib/use-child-sessions";
import { mcpServer, toolDisplayName, toolLabel } from "../../lib/turns";
import type { Session } from "../../lib/types";
import { tsMs, workDuration, type AgentStatus } from "../../lib/work-groups";
import { Collapse } from "../../ui/atoms";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { compactText, SubagentResult } from "./SubagentCard";
import { ModelChip, RunningFor, TaskCard } from "./TaskCard";

/** How a child is going, by its own row: running while it works or waits on the user (or its background work runs),
 *  failed with a turn error, stopped when it was interrupted, completed otherwise. */
export function childStatus(s: Session): AgentStatus {
  const own = stateOf(s);
  if (own === "working" || own === "needs_you" || s.background_working) return "running";
  if (s.turn_error) return "failed";
  if (s.status === "interrupted") return "stopped";
  return "completed";
}

/** The efforts a card's model chip names (cindy's badge levels); any other value is left out. */
const EFFORTS = new Set(["minimal", "low", "medium", "high", "xhigh", "max", "ultra"]);
export const knownEffort = (effort: string | undefined): string | undefined => (effort && EFFORTS.has(effort) ? effort : undefined);

/** How often an open card of a running child reads its transcript again, at most. */
const CHILD_REREAD_MS = 5000;

/** The model chip's words: the model as a subagent card says it, and the effort it runs at ("GPT-5.5 · High"). */
export function childModelChip(s: Pick<Session, "model" | "effort">): string {
  const effort = knownEffort(s.effort);
  return [modelChipLabel(s.model), effort ? effortTitle(effort) : ""].filter(Boolean).join(" · ");
}

/** A tool named the way the chat's tool rows name it: another server's MCP tool as "<Server> · <Tool>". */
export function childToolName(name: string): string {
  return mcpServer(name) !== null ? toolDisplayName(name) : toolLabel(name);
}

/** The card's second line: what ran it (not in a Persona conversation, where it is Persona itself), the model chip, and
 *  how long it took once it is over (while it runs, "Running for …" ticks there instead). */
export function childMeta(s: Session, harness: boolean): { harness?: string; model?: string; took?: string; since?: number } {
  const running = childStatus(s) === "running";
  const start = tsMs(s.started_at) ?? undefined;
  const end = tsMs(s.updated_at) ?? undefined;
  const agent = sessionAgent(s);
  const model = childModelChip(s);
  return {
    ...(harness ? { harness: agentName(agent.agent, agent.label) } : {}),
    ...(model ? { model } : {}),
    ...(!running && start !== undefined && end !== undefined && end >= start ? { took: workDuration(end - start) } : {}),
    ...(running && start !== undefined ? { since: start } : {}),
  };
}

export const ChildSessionCard = memo(function ChildSessionCard({ mid, sid, scope }: { mid: string; sid: string; scope: string }) {
  // the row its parent's chat holds (a paging hub's is a list summary), else the store's
  const held = useChildRow(mid, sid);
  const stored = useStore(() => (held ? undefined : getSession(mid, sid)));
  const child = held ?? stored;
  const { lineStyle } = useChatContext();
  const [open, setOpen] = useExpanded(childCardKey(scope, sid));
  if (!child) return null;
  const status = childStatus(child);
  const title = compactText(child.title, 96) || t("chat-subagentcard.untitled_task");
  const m = childMeta(child, lineStyle !== "assistant");
  const parts = [
    m.harness && <span key="harness">{m.harness}</span>,
    m.model && <ModelChip key="model">{m.model}</ModelChip>,
    m.took && <span key="took">{m.took}</span>,
    m.since !== undefined && <RunningFor key="running" since={m.since} />,
  ].filter(Boolean);
  return (
    <TaskCard
      icon="agent"
      label={t("chat-subagentcard.subagent")}
      title={title}
      status={status}
      meta={parts}
      onClick={() => setOpen((o) => !o)}
      expanded={open}
      ariaLabel={open ? t("chat-subagentcard.collapse_details") : t("chat-subagentcard.expand_details")}
      data={{ "data-child-card": status, "data-tool-use": childCall(sid) }}
    >
      <Collapse open={open} lazy>
        <ChildDetails mid={mid} child={child} running={status === "running"} />
      </Collapse>
    </TaskCard>
  );
});

/** The opened part: mounted only while the card is open, and only then is the child's transcript read. Until it is in,
 *  and when it has no words of the child's, the row's last words stand for its result. */
function ChildDetails({ mid, child, running }: { mid: string; child: Session; running: boolean }) {
  const sid = child.session_id;
  const detail = useChildDetail(mid, sid, child.updated_at, running);
  const prompt = detail?.prompt ?? "";
  const tool = detail?.lastTool;
  return (
    <div className="mt-2 border-l-2 border-tool-rail pl-3 text-[13px] leading-5 text-ink-2" data-subagent-details data-child-details={detail ? "read" : "reading"}>
      {prompt && <p className="mb-1" data-subagent-prompt>{compactText(prompt, 260)}</p>}
      <SubagentResult item={detail?.result} fallback={child.last_text ?? ""} mid={mid} sid={sid} />
      {tool && <p className="text-[12px] leading-4 text-ink-3" data-subagent-last-tool>{t("chat-subagentcard.last_tool", { tool: childToolName(tool) })}</p>}
    </div>
  );
}

/** The child's transcript as the opened card shows it: read on opening, and again as the child's row moves while it
 *  runs (at most every CHILD_REREAD_MS) and once more when it stops. A failed read keeps what was shown. */
function useChildDetail(mid: string, sid: string, rev: string | undefined, running: boolean): ChildDetail | null {
  const [detail, setDetail] = useState<ChildDetail | null>(null);
  const [read, setRead] = useState(0);
  const last = useRef(0);
  // the row moved: read again now, or once the wait since the last read is over (a burst of pushes is one read)
  useEffect(() => {
    if (!last.current) return;
    const wait = running ? last.current + CHILD_REREAD_MS - Date.now() : 0;
    if (wait <= 0) return void setRead((n) => n + 1);
    const timer = setTimeout(() => setRead((n) => n + 1), wait);
    return () => clearTimeout(timer);
  }, [rev, running]);
  useEffect(() => {
    let live = true;
    const ctl = new AbortController();
    last.current = Date.now();
    readChildDetail(mid, sid, ctl.signal).then(
      (d) => live && setDetail(d),
      () => {},
    );
    return () => {
      live = false;
      ctl.abort();
    };
  }, [mid, sid, read]);
  return detail;
}
