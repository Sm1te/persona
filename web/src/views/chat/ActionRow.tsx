// One tool call in a work group (cindy's AgentActionRow): [status][verb][what it worked on][+N -N][›]. A command and
// the like open in place (what went in, what came out, in full); an edit opens its diff over the page; a read opens
// the file in the side panel.
import { memo, useEffect, useMemo, useRef, useState } from "react";
import { useExpanded } from "./expanded";
import { createPortal } from "react-dom";
import { useCloseOnBack } from "../../lib/back-close";
import { diffLines, type Op } from "../../lib/compare";
import { fileName, toolFile } from "../../lib/files";
import { openPanel } from "../../lib/panel";
import { getSession } from "../../lib/store";
import { taskBadge } from "../../lib/tasks";
import { ownEntry, personaToolVerb, SUBJECT_KEYS, toolChip, toolDisplayName, toolLabel } from "../../lib/turns";
import type { Item } from "../../lib/types";
import type { ToolStep } from "../../lib/work-groups";
import { cx, Dialog, EASE, IconButton, Spinner } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { NARROW, useMedia } from "../../ui/media-query";
import { patchDiffs } from "../../lib/tool-detail";
import { useChatContext } from "./chat-context";
import { useFullText } from "./FullText";
import { hasStepDetail, showsResult, StepDetail } from "./StepDetail";
import { useReveal } from "./search-reveal";
import { edgeMask, useEdgeFade } from "./edge-fade";
import { Thumb } from "./Steps";
import { t } from "../../i18n";

/** The row's chrome, shared with the thought rows (ThinkingRow.tsx) so a mixed list reads as one (cindy's
 *  activityRowChrome): radius, padding, the hover lift. On a touch screen a row is a finger tall (40). */
export const ROW_CHROME = "flex w-full min-w-0 gap-1.5 rounded-[8px] px-2 py-[3px] [@media(pointer:coarse)]:min-h-10 text-left";
export const ROW_HOVER = "group select-none transition-colors duration-150 hover:bg-tool-hover motion-reduce:transition-none";
const ROW = `${ROW_CHROME} items-center ${ROW_HOVER}`;
export const CHEVRON_SLOT = "ml-auto flex size-[18px] shrink-0 items-center justify-center rounded-[8px] text-tool-ink transition-colors duration-150 group-hover:bg-hover-2 motion-reduce:transition-none";
export const STATUS_SLOT = "inline-flex h-[18px] w-4 shrink-0 items-center justify-center text-tool-ink";

const COMMAND_TOOLS = new Set(["Bash", "exec_command", "shell", "PowerShell"]);
const FILE_TOOLS = new Set(["Read", "Edit", "Write", "MultiEdit", "NotebookEdit"]);
export const DIFF_TOOLS = new Set(["Edit", "Write", "MultiEdit"]);

/** cindy's verbAggregator (its Chinese wording is the zh-Hans text). */
const VERB: Record<string, string> = {
  Edit: t("chat-actionrow.verb_edit"),
  MultiEdit: t("chat-actionrow.verb_edit"),
  NotebookEdit: t("chat-actionrow.verb_edit"),
  Write: t("chat-actionrow.verb_create"),
  Bash: t("chat-actionrow.verb_run"),
  exec_command: t("chat-actionrow.verb_run"),
  shell: t("chat-actionrow.verb_run"),
  Read: t("chat-actionrow.verb_read"),
  TodoWrite: t("chat-actionrow.verb_update"),
  Glob: t("chat-actionrow.verb_search"),
  Grep: t("chat-actionrow.verb_search"),
  LS: t("chat-actionrow.verb_list"),
  WebFetch: t("chat-actionrow.verb_visit"),
  WebSearch: t("chat-actionrow.verb_visit"),
};

/** Claude Code's newer tools (iOS ToolPresentation.knownVerb, #2986): what each did, then what it was about. */
const DONE: Record<string, string> = {
  TaskCreate: t("chat-actionrow.verb_task_create"),
  TaskUpdate: t("chat-actionrow.verb_task_update"),
  TaskList: t("chat-actionrow.verb_task_list"),
  TaskGet: t("chat-actionrow.verb_task_get"),
  TaskStop: t("chat-actionrow.verb_task_stop"),
  KillShell: t("chat-actionrow.verb_task_stop"),
  KillBash: t("chat-actionrow.verb_task_stop"),
  TaskOutput: t("chat-actionrow.verb_task_output"),
  BashOutput: t("chat-actionrow.verb_task_output"),
  SendMessage: t("chat-actionrow.verb_send_message"),
  ListAgents: t("chat-actionrow.verb_list_agents"),
  ScheduleWakeup: t("chat-actionrow.verb_schedule_wakeup"),
  CronCreate: t("chat-actionrow.verb_cron_create"),
  CronList: t("chat-actionrow.verb_cron_list"),
  CronDelete: t("chat-actionrow.verb_cron_delete"),
  RemoteTrigger: t("chat-actionrow.verb_remote_trigger"),
  Monitor: t("chat-actionrow.verb_monitor"),
  LSP: t("chat-actionrow.verb_lsp"),
  EnterWorktree: t("chat-actionrow.verb_enter_worktree"),
  ExitWorktree: t("chat-actionrow.verb_exit_worktree"),
  SendUserFile: t("chat-actionrow.verb_send_file"),
  Artifact: t("chat-actionrow.verb_artifact"),
  PushNotification: t("chat-actionrow.verb_push_notification"),
  ReportFindings: t("chat-actionrow.verb_report_findings"),
  ToolSearch: t("chat-actionrow.verb_load_tools"),
  WaitForMcpServers: t("chat-actionrow.verb_wait_mcp"),
  ListMcpResourcesTool: t("chat-actionrow.verb_list_mcp_resources"),
  ReadMcpResourceTool: t("chat-actionrow.verb_read_mcp_resource"),
  Skill: t("chat-actionrow.verb_skill"),
};

const PARAM_MAX = 60;

export function truncateParam(text: string, max = PARAM_MAX): string {
  const t = text.trim();
  return t.length <= max ? t : `${t.slice(0, Math.max(0, max - 3)).trimEnd()}...`;
}

const str = (v: unknown) => (typeof v === "string" ? v.trim() : "");
/** A command call's command (Codex sends it as an argv array). */
const commandOf = (inp: Record<string, unknown>) => (Array.isArray(inp.command) ? (inp.command as unknown[]).join(" ") : str(inp.command) || str(inp.cmd));

export interface ActionPresentation {
  /** Hidden when the agent described the command itself: its sentence is the row. */
  verb?: string;
  param?: { text: string; title?: string };
  /** A file the call worked on: drawn as a chip with its name. */
  file?: string;
  /** A command without a description: on a quieter line under the row. */
  raw?: string;
}

/** How a call reads in its row (cindy's describeToolUse + extractDisplayParam). */
export function presentAction(use: Item): ActionPresentation {
  const name = use.tool_name ?? "";
  const inp = (use.tool_input ?? {}) as Record<string, unknown>;
  if (COMMAND_TOOLS.has(name)) {
    const cmd = commandOf(inp);
    const desc = str(inp.description);
    if (desc) return { param: { text: desc, title: cmd || undefined } };
    return { verb: t("chat-actionrow.verb_run_command"), raw: cmd || undefined };
  }
  if (FILE_TOOLS.has(name)) {
    const path = toolFile(name, inp)?.path ?? "";
    return { verb: VERB[name], ...(path ? { file: path, param: { text: truncateParam(fileName(path)), title: path } } : {}) };
  }
  if (name === "Grep" || name === "Glob") return { verb: VERB[name], param: { text: truncateParam(str(inp.pattern)) } };
  if (name === "WebFetch") return { verb: VERB[name], param: { text: truncateParam(str(inp.url)), title: str(inp.url) } };
  if (name === "WebSearch") return { verb: VERB[name], param: { text: truncateParam(str(inp.query)) } };
  if (name === "LS") return { verb: VERB[name], param: { text: truncateParam(str(inp.path)) } };
  if (name.startsWith("mcp__")) {
    // Persona's own tools read as what they do ("Read Agent chat"), then what they were given
    const own = personaToolVerb(name);
    if (own) {
      const what = [own.what, toolChip(use)].filter(Boolean).join(" · ");
      return { verb: own.verb, ...(what ? { param: { text: truncateParam(what), title: name } } : {}) };
    }
    // another server's tool in words ("GitHub · Create issue"), then what it was given
    const what = [toolDisplayName(name), toolChip(use)].filter(Boolean).join(" · ");
    return { verb: t("chat-actionrow.verb_call"), param: { text: truncateParam(what), title: name } };
  }
  const chip = toolChip(use);
  const done = ownEntry(DONE, name);
  if (done) return { verb: done, ...(chip ? { param: { text: truncateParam(chip), title: chip } } : {}) };
  return { verb: ownEntry(VERB, name) ?? t("chat-actionrow.verb_call"), param: { text: truncateParam(chip ? `${toolLabel(name)} · ${chip}` : toolLabel(name)), title: chip || undefined } };
}

// ── diffs ──

/** A line diff of an edit's old and new text (lib/compare.ts's Myers diff); too big to diff: all out, all in. */
export function lineDiff(oldText: string, newText: string): Op[] {
  const a = oldText ? oldText.split("\n") : [];
  const b = newText ? newText.split("\n") : [];
  return diffLines(a, b) ?? [...a.map((text) => ({ kind: "del" as const, text })), ...b.map((text) => ({ kind: "add" as const, text }))];
}

/** An edit's changes, one diff per change (MultiEdit has several; Write is all new). */
export function editDiffs(use: Item): Op[][] | null {
  const inp = (use.tool_input ?? {}) as Record<string, unknown>;
  switch (use.tool_name) {
    case "Edit":
      // Codex: the patch as a unified diff (the hub maps a file change to an Edit with `diff`); Claude: old / new
      if (typeof inp.diff === "string") {
        // the hub sends an empty diff for a change with no text (an empty new file, a pure move): open the file instead
        const d = patchDiffs(inp.diff, str(inp.change));
        return d.length ? d : null;
      }
      return typeof inp.old_string === "string" || typeof inp.new_string === "string" ? [lineDiff(String(inp.old_string ?? ""), String(inp.new_string ?? ""))] : null;
    case "Write":
      return typeof inp.content === "string" ? [lineDiff("", inp.content)] : null;
    case "MultiEdit":
      return Array.isArray(inp.edits) ? (inp.edits as Record<string, unknown>[]).map((e) => lineDiff(String(e.old_string ?? ""), String(e.new_string ?? ""))) : null;
  }
  return null;
}

export function diffStats(diffs: Op[][]): { add: number; del: number } {
  let add = 0;
  let del = 0;
  for (const d of diffs) for (const l of d) l.kind === "add" ? add++ : l.kind === "del" && del++;
  return { add, del };
}

/** What a call opens to in place: its input as text. */
export function inlineInput(use: Item): string {
  const name = use.tool_name ?? "";
  const inp = (use.tool_input ?? {}) as Record<string, unknown>;
  if (COMMAND_TOOLS.has(name)) {
    return [commandOf(inp), str(inp.cwd) && `cwd: ${str(inp.cwd)}`].filter(Boolean).join("\n");
  }
  if (name === "Grep") return [`pattern: ${str(inp.pattern)}`, ...["path", "glob", "type", "output_mode"].filter((k) => str(inp[k])).map((k) => `${k}: ${str(inp[k])}`)].join("\n");
  if (name === "Glob") return str(inp.path) ? `${str(inp.pattern)}\nin: ${str(inp.path)}` : str(inp.pattern);
  if (name === "WebFetch") return [str(inp.url), str(inp.prompt)].filter(Boolean).join("\n");
  if (name === "WebSearch") return str(inp.query);
  if (name === "Monitor") return str(inp.command) || str((inp.ws as Record<string, unknown> | undefined)?.url);
  return argumentLines(inp);
}

/** Any other call's input as `key: value` lines, not a JSON dump (iOS ToolArgumentsDetail, #2986): the telling fields
 *  first, then the rest by name; text as written, lists of words joined, anything nested as one line of JSON. */
export function argumentLines(inp: Record<string, unknown>): string {
  const keys = [...SUBJECT_KEYS.filter((k) => Object.hasOwn(inp, k)), ...Object.keys(inp).filter((k) => !SUBJECT_KEYS.includes(k)).sort()];
  const shown = (v: unknown): string => {
    if (typeof v === "string") return v.trim();
    if (v === null) return "null";
    if (Array.isArray(v) && v.every((x) => x === null || typeof x !== "object")) return v.map(String).join(", ");
    if (typeof v === "object") return Object.keys(v as object).length ? JSON.stringify(v) : "";
    return String(v);
  };
  return keys.flatMap((k) => (shown(inp[k]) ? [`${k}: ${shown(inp[k])}`] : [])).join("\n");
}

export const ActionRow = memo(function ActionRow({ step, running, mid, sid, trace }: { step: ToolStep; running?: boolean; mid: string; sid: string; trace?: boolean }) {
  const { use, result, images } = step;
  const name = use.tool_name ?? "";
  const { tasks, scope } = useChatContext();
  const p = useMemo(() => presentAction(use), [use]);
  const diffs = useMemo(() => (DIFF_TOOLS.has(name) ? editDiffs(use) : null), [name, use]);
  const stats = diffs ? diffStats(diffs) : null;
  // kept by the chat, not the row (expanded.ts): a row drawn again (a group regrouped, a line rebuilt) stays as the reader left it
  const [open, setOpen] = useExpanded(`${scope}act:${use.id}`);
  const [diffOpen, setDiffOpen] = useExpanded(`${scope}diff:${use.id}`);
  const inPlace = trace || !FILE_TOOLS.has(name);
  const call = use.tool_use_id;
  const bg = call && (tasks.started.has(call) || tasks.notes.has(call)) ? taskBadge(tasks.notes.get(call) ?? []) : null;
  const error = Boolean(result?.is_error);

  const activate = () => {
    if (trace) return setOpen((o) => !o);
    if (diffs) return setDiffOpen(true);
    if (inPlace) return setOpen((o) => !o);
    const f = toolFile(name, use.tool_input);
    if (f) openPanel({ kind: "file", mid, path: f.path, cwd: getSession(mid, sid)?.cwd, line: f.line });
  };
  const ids = useMemo(() => [use.id, ...(result ? [result.id] : [])], [use.id, result]);
  // a search hit in its input or output (search-reveal.ts): its detail opens in place
  useReveal(scope, ids, () => {
    if (!inPlace || open) return;
    setOpen(true);
    return () => setOpen(false);
  });
  const label = [p.verb, p.param?.text].filter(Boolean).join(" ");
  // A supervision trace is historical: show the original parameters/result, never today's file in the side panel.
  const detail = open && !trace && hasStepDetail(use);
  const input = open && !detail ? trace ? JSON.stringify(use.tool_input ?? {}, null, 2) : inlineInput(use) : "";
  const full = useFullText(result, mid, sid);
  const output = result && (trace || showsResult(name, result)) ? full.text : "";
  return (
    <div className="flex min-w-0 flex-col" data-action-row={name} data-tool-use={call} data-items={ids.join(" ")}>
      <button type="button" onClick={activate} aria-expanded={inPlace ? open : undefined} aria-label={label || name} title={p.param?.title} className={ROW}>
        <span role="img" aria-label={running ? t("chat-actionrow.status_running") : error ? t("chat-actionrow.status_error") : t("chat-actionrow.status_done")} className={cx(STATUS_SLOT, error && "text-red")}>
          {running ? <Spinner size={13} /> : <Icon name={error ? "x" : "check"} size={13} />}
        </span>
        {p.verb && <span className="shrink-0 text-[length:var(--chat-secondary)] whitespace-nowrap text-tool-ink">{p.verb}</span>}
        {p.param &&
          (p.file ? (
            <span className="inline-flex max-w-full min-w-0 items-center gap-[3px] rounded-[4px] border border-line bg-field px-[5px] py-[0.5px] font-mono text-[13px] leading-[1.385] whitespace-nowrap text-ink-2 transition-colors group-hover:bg-hover-2" data-file-chip>
              <Icon name="read" size={12} className="shrink-0 text-ink-3" />
              <span className="truncate">{p.param.text}</span>
            </span>
          ) : (
            <span className="min-w-0 truncate text-[length:var(--chat-secondary)] font-medium text-tool-ink">{p.param.text}</span>
          ))}
        <span className="flex-1" />
        {stats && (stats.add > 0 || stats.del > 0) && (
          <span className="flex shrink-0 gap-1 font-mono text-[13px] font-medium tabular-nums">
            <span className="text-diff-add-fg">+{stats.add}</span>
            <span className="text-diff-del-fg">-{stats.del}</span>
          </span>
        )}
        {bg && <span className={cx("shrink-0 rounded-[4px] px-1 text-[11px] font-medium whitespace-nowrap", bg.failed ? "bg-red-tint text-red" : "bg-field text-ink-2")}>{bg.text}</span>}
        <span aria-hidden className={CHEVRON_SLOT}>
          <Icon name={inPlace && open ? "chevron" : "chevronRight"} size={13} stroke={2.2} />
        </span>
      </button>
      {p.raw && !open && (
        <div title={p.raw} className="min-w-0 truncate px-2 pb-[3px] pl-[30px] font-mono text-[12px] leading-[1.5] text-tool-ink">
          {p.raw}
        </div>
      )}
      {images.length > 0 && (
        <div className="flex min-w-0 flex-wrap gap-1.5 py-2 pl-8">
          {images.map((im) => (
            <Thumb key={im.id} item={im} mid={mid} sid={sid} />
          ))}
        </div>
      )}
      {open && (
        <div className="mx-2 mt-1 mb-1 cursor-text rounded-[6px] border border-line bg-neutral-bubble px-[10px] py-2 font-mono text-[12px] leading-[17px] text-ink select-text" data-inline-detail>
          {detail && (
            <div className="font-sans">
              <StepDetail use={use} result={result} />
            </div>
          )}
          {input && <pre className="m-0 whitespace-pre-wrap [overflow-wrap:anywhere]">{input}</pre>}
          {output && (
            <>
              {(input || detail) && <div className="my-2 h-px bg-tool-ink/20" />}
              <pre className={cx("m-0 whitespace-pre-wrap [overflow-wrap:anywhere]", error ? "text-red" : "text-tool-ink")}>{output}</pre>
              {full.more}
            </>
          )}
          {!input && !detail && !output && <span className="text-tool-ink">{t("chat-actionrow.no_content")}</span>}
        </div>
      )}
      {diffs && diffOpen && <DiffDialog onClose={() => setDiffOpen(false)} path={p.file ?? ""} diffs={diffs} mid={mid} sid={sid} />}
    </div>
  );
});

/** How many lines of a diff the phone's full-screen view shows before "Expand". */
export const DIFF_FOLD_LINES = 40;

/** The first `max` lines of an edit's changes (cut inside the change they end in), and how many lines that leaves out. */
export function foldDiffs(diffs: Op[][], max = DIFF_FOLD_LINES): { shown: Op[][]; hidden: number } {
  const total = diffs.reduce((n, d) => n + d.length, 0);
  if (total <= max) return { shown: diffs, hidden: 0 };
  const shown: Op[][] = [];
  let left = max;
  for (const d of diffs) {
    if (left <= 0) break;
    shown.push(d.slice(0, left));
    left -= d.length;
  }
  return { shown, hidden: total - max };
}

function DiffStats({ diffs }: { diffs: Op[][] }) {
  const s = diffStats(diffs);
  return (
    <span className="shrink-0 font-mono text-[12px] font-medium tabular-nums">
      <span className="text-diff-add-fg">+{s.add}</span> <span className="text-diff-del-fg">-{s.del}</span>
    </span>
  );
}

export function DiffBlocks({ diffs }: { diffs: Op[][] }) {
  return diffs.map((d, n) => (
    <div key={n} className="min-w-fit overflow-hidden rounded-[8px] py-1 font-mono text-[12px] leading-[1.6] shadow-hairline">
      {d.map((l, i) => (
        <div key={i} className={cx("flex min-w-fit pr-3", l.kind === "add" && "bg-diff-add-bg", l.kind === "del" && "bg-diff-del-bg")} data-diff-line={l.kind}>
          <span className={cx("w-6 shrink-0 text-center select-none", l.kind === "add" ? "text-diff-add-fg" : l.kind === "del" ? "text-diff-del-fg" : "text-ink-3")}>{l.kind === "add" ? "+" : l.kind === "del" ? "−" : " "}</span>
          <span className="whitespace-pre text-ink">{l.text || " "}</span>
        </div>
      ))}
    </div>
  ));
}

/** An edit's changes over the page: whole lines on red and green, the file one click away. On a narrow screen they
 *  take the whole screen instead (DiffScreen). */
function DiffDialog({ onClose, path, diffs, mid, sid }: { onClose: () => void; path: string; diffs: Op[][]; mid: string; sid: string }) {
  const narrow = useMedia(NARROW);
  const openFile = () => {
    onClose();
    openPanel({ kind: "file", mid, path, cwd: getSession(mid, sid)?.cwd });
  };
  if (narrow) return <DiffScreen onClose={onClose} path={path} diffs={diffs} onOpenFile={path ? openFile : undefined} />;
  return (
    <Dialog
      open
      onClose={onClose}
      width={860}
      title={
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate">{path ? fileName(path) : t("chat-actionrow.changes")}</span>
          <DiffStats diffs={diffs} />
        </span>
      }
      footer={
        path ? (
          <button type="button" onClick={openFile} className="flex h-8 items-center gap-1.5 rounded-control px-3 text-[12.5px] font-medium whitespace-nowrap text-ink-2 shadow-btn transition-colors hover:bg-hover">
            <Icon name="read" size={13} />
            {t("chat-actionrow.open_file")}
          </button>
        ) : undefined
      }
    >
      {path && <div className="mb-2 truncate font-mono text-[11.5px] text-ink-3" title={path}>{path}</div>}
      <div className="flex max-h-[60vh] flex-col gap-2 overflow-auto" data-diff>
        <DiffBlocks diffs={diffs} />
      </div>
    </Dialog>
  );
}

/** The diff on a phone: the whole visible screen (above the keyboard), Back or ✕ to close; long lines scroll sideways
 *  under a fade at the right edge while there is more, and a long diff shows its first 40 lines until "Expand". */
function DiffScreen({ onClose, path, diffs, onOpenFile }: { onClose: () => void; path: string; diffs: Op[][]; onOpenFile?: () => void }) {
  useCloseOnBack(true, onClose);
  const [all, setAll] = useState(false);
  const { shown, hidden } = useMemo(() => (all ? { shown: diffs, hidden: 0 } : foldDiffs(diffs)), [all, diffs]);
  const body = useRef<HTMLDivElement>(null);
  const { edges, measure } = useEdgeFade(body, [shown]);
  // the chat's own Esc (interrupt) leaves it alone while a dialog is open
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    addEventListener("keydown", key);
    return () => removeEventListener("keydown", key);
  }, [onClose]);
  const mask = edgeMask(edges);
  return createPortal(
    <div
      role="dialog"
      aria-modal="true"
      aria-label={path ? t("chat-actionrow.changes_in", { name: fileName(path) }) : t("chat-actionrow.changes")}
      className="fixed inset-x-0 z-40 flex flex-col bg-surface pt-[env(safe-area-inset-top)]"
      style={{ top: "var(--kb-top)", height: "var(--kb-h)", animation: `slide-up 220ms ${EASE} both` }}
      data-diff-screen
    >
      <div className="flex h-12 shrink-0 items-center gap-2 border-b border-line px-2">
        <IconButton icon="x" label={t("chat-actionrow.close")} onClick={onClose} size={36} className="touch-hit" />
        <span className="min-w-0 flex-1 truncate text-[15px] font-semibold text-ink" title={path || undefined}>
          {path ? fileName(path) : t("chat-actionrow.changes")}
        </span>
        <DiffStats diffs={diffs} />
        {onOpenFile && (
          <button type="button" onClick={onOpenFile} className="flex h-9 shrink-0 items-center gap-1.5 rounded-control px-3 text-[13px] font-medium whitespace-nowrap text-ink-2 shadow-btn transition-colors hover:bg-hover">
            <Icon name="read" size={13} />
            {t("chat-actionrow.open_file")}
          </button>
        )}
      </div>
      {path && <div className="shrink-0 truncate px-4 pt-2 font-mono text-[11.5px] text-ink-3">{path}</div>}
      <div ref={body} onScroll={measure} className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto overscroll-contain px-3 pt-2 pb-[max(12px,env(safe-area-inset-bottom))]" style={{ maskImage: mask, WebkitMaskImage: mask }} data-diff>
        <DiffBlocks diffs={shown} />
        {hidden > 0 && (
          <button type="button" onClick={() => setAll(true)} className="sticky left-0 flex h-10 w-fit shrink-0 items-center gap-1.5 rounded-control px-3 text-[13px] font-medium whitespace-nowrap text-accent-ink" data-diff-more>
            <Icon name="chevron" size={13} stroke={2.2} />
            {t("chat-actionrow.show_rest", { count: hidden })}
          </button>
        )}
      </div>
    </div>,
    document.body,
  );
}
