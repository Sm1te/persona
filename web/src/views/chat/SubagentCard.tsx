// A subagent the agent launched, as one card in the chat where it launched it (#5030, after cindy's AgentTaskCard): never a
// conversation of its own. Two lines: "Subagent", its title and its status word; then what ran it (Claude Code), the model
// it actually ran on (the node reads it from the subagent's own transcript; never the one its call asked for), its tokens,
// its tool calls and how long it took, or "Running for …" each second while it runs. The head opens the card in place,
// remembered per card: what it was asked, what it reported (a long report folds), its last tool and its output file.
// A background subagent's call returns at once with a launch receipt, which is not its result: the card runs until its
// report comes back as a notice.
import { memo, useState } from "react";
import { t } from "../../i18n";
import { agentSummary, isLaunchReceipt, modelChipLabel, subagentCardKey, subagentEnded, subagentFor, subagentTitle } from "../../lib/tasks";
import { compactTokens } from "../../lib/tokens";
import type { Item, Subagent, TaskNote } from "../../lib/types";
import { tsMs, workDuration, type AgentStatus, type ToolStep } from "../../lib/work-groups";
import { agentName } from "../../lib/agent-kind";
import { Collapse, cx } from "../../ui/atoms";
import { useClamped } from "../../ui/use-clamped";
import { useChatContext } from "./chat-context";
import { useExpanded } from "./expanded";
import { isHandbackText } from "./FoldedReport";
import { useFullText } from "./FullText";
import { ModelChip, RunningFor, TaskCard } from "./TaskCard";

/** A one-line piece of a call's input: whitespace runs as one space, at most `max` characters (never half of one). */
export function compactText(text: string | undefined, max: number): string {
  const chars = Array.from((text ?? "").replace(/\s+/g, " ").trim());
  return chars.length <= max ? chars.join("") : `${chars.slice(0, max - 1).join("")}…`;
}

/** What a background subagent reported: its notice's words (the call's own result is only the launch receipt). */
export function reportOf(result: Item | undefined, notes: TaskNote[] | undefined): string {
  if (!isLaunchReceipt(result)) return "";
  const done = notes?.findLast((n) => n.kind === "agent" && n.detail?.trim());
  return done?.detail?.trim() ?? "";
}

/** The item its result is read from: its latest report (a background one's notice, or the call's result once it is one),
 *  else the call's result when that is not the launch receipt. */
function resultItem(result: Item | undefined, reports: Item[]): Item | undefined {
  return reports.at(-1) ?? (result && !isLaunchReceipt(result) ? result : undefined);
}

/** The card's second line: the harness, the model chip, tokens, tool uses, and the time it took (none while it runs:
 *  "Running for …" ticks there instead). */
export function subagentMeta(meta: Subagent | undefined, took: string, harness: boolean): { harness?: string; model: string; tokens?: string; uses?: string; took?: string } {
  return {
    ...(harness ? { harness: agentName("claude") } : {}),
    model: modelChipLabel(meta?.model),
    ...(meta?.tokens ? { tokens: t("chat-subagentcard.tokens", { value: compactTokens(meta.tokens) }) } : {}),
    ...(meta?.steps ? { uses: t("chat-subagentcard.tool_uses", { count: meta.steps }) } : {}),
    ...(took ? { took } : {}),
  };
}

export const SubagentCard = memo(function SubagentCard({ step, status, mid, sid, scope = "" }: { step: ToolStep; status: AgentStatus; mid: string; sid?: string; scope?: string }) {
  const { subagents, tasks, lineStyle } = useChatContext();
  const { use, result } = step;
  const call = use.tool_use_id ?? use.id;
  const inp = (use.tool_input ?? {}) as Record<string, unknown>;
  const meta = subagentFor(subagents, use);
  const [open, setOpen] = useExpanded(subagentCardKey(scope, call));
  const running = status === "running";
  const description = typeof inp.description === "string" ? inp.description : "";
  const prompt = typeof inp.prompt === "string" ? inp.prompt : "";
  const title = compactText(description || (meta ? subagentTitle(meta) : "") || prompt, 96) || t("chat-subagentcard.untitled_task");
  const notes = use.tool_use_id ? tasks.notes.get(use.tool_use_id) : undefined;
  const reports = use.tool_use_id ? tasks.reports.get(use.tool_use_id) ?? [] : [];
  const start = tsMs(meta?.started_at) ?? tsMs(use.ts) ?? undefined;
  const end = running ? undefined : subagentEnded(result, meta);
  const took = start !== undefined && end !== undefined && end >= start ? workDuration(end - start) : "";
  // the Persona chat's own calls: what ran it is Persona itself, not a tool its reader works with
  const m = subagentMeta(meta, took, lineStyle !== "assistant");
  const parts = [
    m.harness && <span key="harness">{m.harness}</span>,
    m.model && <ModelChip key="model">{m.model}</ModelChip>,
    m.tokens && <span key="tokens">{m.tokens}</span>,
    m.uses && <span key="uses">{m.uses}</span>,
    m.took && <span key="took">{m.took}</span>,
    running && start !== undefined && <RunningFor key="running" since={start} />,
  ].filter(Boolean);
  const output = notes?.findLast((n) => n.output_file)?.output_file;
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
      data={{ "data-subagent-card": status, "data-tool-use": use.tool_use_id }}
    >
      <Collapse open={open} lazy>
        <div className="mt-2 border-l-2 border-tool-rail pl-3 text-[13px] leading-5 text-ink-2" data-subagent-details>
          {(prompt || description) && <p className="mb-1" data-subagent-prompt>{compactText(prompt || description, 260)}</p>}
          <SubagentResult item={resultItem(result, reports)} fallback={running ? "" : reportOf(result, notes) || agentSummary(result, meta)} mid={mid} sid={sid} />
          {meta?.last_tool?.name && <p className="text-[12px] leading-4 text-ink-3" data-subagent-last-tool>{t("chat-subagentcard.last_tool", { tool: meta.last_tool.name })}</p>}
          {output && <p className="text-[12px] leading-4 break-all text-ink-3" data-subagent-output>{t("chat-subagentcard.output_file", { path: output })}</p>}
        </div>
      </Collapse>
    </TaskCard>
  );
});

/** What it reported, as plain text: four lines until "Show full result" when it is longer (measured, `useClamped`); a
 *  report the hub cut has its "View full text" too. */
function SubagentResult({ item, fallback, mid, sid }: { item: Item | undefined; fallback: string; mid: string; sid?: string }) {
  const full = useFullText(item?.kind === "tool_result" ? item : undefined, mid, sid);
  // a report the call returned ends with Claude Code's "agentId: …" line, and may start with its hand-back frame: both
  // are for the model
  const said = item?.kind === "tool_result" ? agentSummary({ ...item, text: full.text }, undefined) : (item ? item.task?.detail ?? "" : fallback).trim();
  const text = isHandbackText(said) ? said.split("\n").slice(1).join("\n").trim() : said;
  const [whole, setWhole] = useState(false);
  const clamp = useClamped<HTMLParagraphElement>(whole);
  if (!text) return null;
  return (
    <div className="mb-1" data-subagent-summary data-items={item?.id}>
      <p ref={clamp.ref} className={cx("whitespace-pre-wrap [overflow-wrap:anywhere]", !whole && "line-clamp-4")}>{text}</p>
      {(clamp.cut || whole) && (
        <button type="button" aria-expanded={whole} onClick={() => setWhole((w) => !w)} className="rounded-full text-[12px] text-accent-ink hover:underline">
          {whole ? t("chat-subagentcard.hide_full_result") : t("chat-subagentcard.show_full_result")}
        </button>
      )}
      {full.more}
    </div>
  );
}
