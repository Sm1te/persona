// What an opened step shows besides its raw input (iOS ToolViews.swift ToolDetail, Android ToolDetails.kt /
// ContentPresentation.kt): a Codex patch as a diff, the questions an AskUserQuestion or the assistant's ask_user asked
// (with what the user picked), and the hub's cut texts (full_size) with the mark it adds where it cut them.
import type { Op } from "./compare";
import { parseDiff, toolFile } from "./files";
import type { Interaction } from "./types";

/** A Codex file change (the hub maps it to an Edit with `diff`): one block per hunk; a new or deleted file the hub
 *  sends as its whole content (no hunks) is all added, or all removed when `change` is `delete` (Android patchDiff). */
export function patchDiffs(patch: string, change?: string): Op[][] {
  if (!patch.split("\n").some((l) => l.startsWith("@@"))) {
    if (!patch) return [];
    return [patch.replace(/\n$/, "").split("\n").map((text) => ({ kind: change === "delete" ? ("del" as const) : ("add" as const), text }))];
  }
  const out: Op[][] = [];
  for (const l of parseDiff(patch)) {
    if (l.kind === "hunk") out.push([]);
    else if (l.kind === "add" || l.kind === "del" || l.kind === "context") out[out.length - 1].push({ kind: l.kind === "context" ? "same" : l.kind, text: l.text });
  }
  return out.filter((d) => d.length);
}

/** The base name of a tool: an MCP tool's own name (mcp__persona__ask_user → ask_user). */
export function baseToolName(name: string): string {
  return name.startsWith("mcp__") ? (name.split("__").pop() ?? name) : name;
}

export interface ToolQuestion {
  question: string;
  header?: string;
  multiSelect: boolean;
  options: { label: string; description?: string }[];
}

const s = (v: unknown) => (typeof v === "string" ? v.trim() : "");

/** The assistant's password card (`secret_run`, #1137): commands run on one of your computers with a password typed into
 *  the card. */
export function isSecretRun(ix: Pick<Interaction, "kind" | "tool_name">): boolean {
  return ix.kind === "permission" && ix.tool_name === "secret_run";
}

/** The hand-off card (`secret_handoff`, #3962): secret values a provider holds, handed to one Agent run once the
 *  user approves on this card (never from a quick path: docs/extensions/secret-providers.md "Who may approve a hand-off"). */
export function isSecretHandoff(ix: Pick<Interaction, "kind" | "tool_name">): boolean {
  return ix.kind === "permission" && ix.tool_name === "secret_handoff";
}

/** One variable a hand-off card hands over: its name, and the item as its provider describes it (never a reference). */
export interface HandoffVariable {
  name: string;
  provider: string;
  title: string;
}

/** A hand-off card's variables, the Agent, the computer, where it runs and the start of what it is asked. */
export function secretHandoffInput(input: Record<string, unknown> | undefined): {
  variables: HandoffVariable[];
  agent: string;
  machine: string;
  cwd: string;
  prompt: string;
} {
  const raw = Array.isArray(input?.variables) ? input.variables : [];
  const variables = raw.flatMap((v): HandoffVariable[] => {
    if (!v || typeof v !== "object") return [];
    const o = v as Record<string, unknown>;
    const name = s(o.name);
    return name ? [{ name, provider: s(o.provider_name) || s(o.provider), title: s(o.title) }] : [];
  });
  return { variables, agent: s(input?.agent), machine: s(input?.machine_name), cwd: s(input?.cwd), prompt: s(input?.prompt) };
}

/** A password card's computer, purpose and commands, the commands exactly as sent. */
export function secretRunInput(input: Record<string, unknown> | undefined): { machine: string; purpose: string; commands: string[] } {
  const commands = Array.isArray(input?.commands) ? input.commands.filter((c): c is string => typeof c === "string" && c.trim() !== "") : [];
  return { machine: s(input?.machine_name) || s(input?.machine), purpose: s(input?.purpose), commands };
}

/** The questions an AskUserQuestion / ask_user call asked (empty when its input has none). */
export function toolQuestions(input: Record<string, unknown> | undefined): ToolQuestion[] {
  const qs = Array.isArray(input?.questions) ? (input.questions as unknown[]) : [];
  return qs.flatMap((raw) => {
    const q = (raw ?? {}) as Record<string, unknown>;
    // kept untrimmed: the hub writes the answer with the raw strings (`- <question> → <answer>`), so matching needs them
    const question = typeof q.question === "string" ? q.question : "";
    if (!question.trim()) return [];
    const options = (Array.isArray(q.options) ? (q.options as unknown[]) : []).flatMap((o) => {
      const opt = (typeof o === "string" ? { label: o } : (o ?? {})) as Record<string, unknown>;
      const label = typeof opt.label === "string" ? opt.label : "";
      return label.trim() ? [{ label, description: s(opt.description) || undefined }] : [];
    });
    return [{ question, header: s(q.header) || undefined, multiSelect: q.multiSelect === true || q.multi_select === true, options }];
  });
}

/** Each question's answer in the hub's ask_user result (`The user answered:\n- <question> → <answer>`); empty when
 *  nobody answered or the text is not the hub's (iOS AskUserAnswers.parse). */
export function askUserAnswers(result: string, questions: ToolQuestion[]): Record<string, string> {
  if (!result.startsWith("The user answered:")) return {};
  const marks = questions
    .map((q) => {
      const mark = `\n- ${q.question} → `;
      const at = result.indexOf(mark);
      return at >= 0 ? { q: q.question, at, end: at + mark.length } : null;
    })
    .filter((m): m is { q: string; at: number; end: number } => m !== null)
    .sort((a, b) => a.at - b.at);
  const out: Record<string, string> = {};
  marks.forEach((m, n) => {
    const answer = result.slice(m.end, n + 1 < marks.length ? marks[n + 1].at : undefined).trim();
    if (answer) out[m.q] = answer;
  });
  return out;
}

// i18n-ignore-start: the suffixes Claude writes after an option, matched as written
const RECOMMENDED_SUFFIXES = [" (Recommended)", "(Recommended)", "（推荐）", "(推荐)"];
// i18n-ignore-end

/** An option's words without Claude's "(Recommended)" suffix, and whether it had one (iOS QuestionOption.display).
 *  Only for showing it: the answer sent back keeps the whole label. */
export function optionDisplay(label: string): { text: string; recommended: boolean } {
  const trimmed = label.trimEnd();
  for (const suffix of RECOMMENDED_SUFFIXES) {
    if (trimmed.endsWith(suffix) && trimmed.length > suffix.length) return { text: trimmed.slice(0, -suffix.length).trim(), recommended: true };
  }
  return { text: label, recommended: false };
}

/** Whether an option is among what was picked (a multi-select answer joins its picks with ", "). */
export function pickedOption(label: string, answer: string | undefined, multiSelect: boolean): boolean {
  if (answer === undefined) return false;
  // the label is kept raw (see toolQuestions) and the answer comes trimmed: compare without the edge whitespace
  const want = label.trim();
  return answer.trim() === want || (multiSelect && answer.split(", ").some((a) => a.trim() === want));
}

/** An answer typed instead of picking one of the options. */
export function typedAnswer(q: ToolQuestion, answer: string | undefined): string | undefined {
  if (answer === undefined || q.options.some((o) => pickedOption(o.label, answer, q.multiSelect))) return undefined;
  return answer;
}

/** The note the hub appends where it cut a long text for the page (`\n…` then the hub's Chinese "truncated, … N bytes in all"). */
const CUT_MARK = "\n…（已截断"; // i18n-ignore: matches the hub's text

/** A cut text without the hub's note (the "Show all" button says it instead). */
export function withoutCutMark(text: string): string {
  const at = text.lastIndexOf(CUT_MARK);
  return at >= 0 ? text.slice(0, at) : text;
}


/** One thing a permission card names, under a small label: the command it runs, the file, the address, the search. */
export interface PermissionField {
  label: "command" | "file" | "url" | "query" | "pattern" | "path";
  value: string;
}

/** What a permission card says the tool will do, field by field (Android permissionSummary): the agent's own sentence
 *  (`description`, WebFetch's `prompt`) as plain text, then what it acts on. null for a tool it does not know, or one
 *  whose input lacks the field it acts on: the card shows the hub's summary or the raw input then. */
export function permissionDetail(tool: string | undefined, input: Record<string, unknown> | undefined): { note?: string; fields: PermissionField[] } | null {
  const inp = input ?? {};
  const note = s(inp.description) || undefined;
  switch (tool) {
    case "Bash":
    case "exec_command":
    case "shell": {
      // an argv array: quote the parts with spaces so it reads as the one command it is
      const cmd = Array.isArray(inp.command) ? (inp.command as unknown[]).map((a) => (/\s/.test(String(a)) ? `'${String(a)}'` : String(a))).join(" ") : s(inp.command) || s(inp.cmd);
      return cmd ? { note, fields: [{ label: "command", value: cmd }] } : note ? { note, fields: [] } : null;
    }
    case "Read":
    case "Edit":
    case "MultiEdit":
    case "Write":
    case "NotebookEdit": {
      const path = toolFile(tool, inp)?.path;
      return path ? { fields: [{ label: "file", value: path }] } : null;
    }
    case "WebFetch":
      return s(inp.url) ? { note: s(inp.prompt) || undefined, fields: [{ label: "url", value: s(inp.url) }] } : null;
    case "WebSearch":
      return s(inp.query) ? { fields: [{ label: "query", value: s(inp.query) }] } : null;
    case "Grep":
    case "Glob": {
      if (!s(inp.pattern)) return null;
      const fields: PermissionField[] = [{ label: "pattern", value: s(inp.pattern) }];
      if (s(inp.path)) fields.push({ label: "path", value: s(inp.path) });
      return { fields };
    }
  }
  return null;
}

/** What the hub's summary adds to the fields (a Codex card's reason after its command, a gate's own sentence): the
 *  summary without the first field it starts with; "" when it only repeats it. */
export function summaryExtra(summary: string | undefined, fields: PermissionField[]): string {
  const sum = s(summary);
  const first = fields[0]?.value ?? "";
  if (!sum || sum === first) return "";
  // only a whole first line (or more) counts as repeating it: "curl x" does not repeat inside "curl xyz"
  return first && sum.startsWith(first) && /^\s*(\n|$)/.test(sum.slice(first.length)) ? sum.slice(first.length).trim() : sum;
}

/** Why another agent asks, shown above what it will do (iOS AgentPermissionForm, Android InteractionPanel): only a
 *  card with another agent shows it, as on iOS and Android (a Claude Code card's hook reason stays hidden). "" when there is none. */
export function permissionReason(agent: string | undefined, reason: string | undefined): string {
  return agent && agent !== "claude" ? s(reason) : "";
}
