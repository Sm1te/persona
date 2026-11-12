import { t } from "../i18n";
import { agentId, agentName, sessionAgent } from "./agent-kind";
import { modelShort } from "./context-usage";
import { baseName, failureReason, sessionPreview, sessionTitle, stateOf } from "./format";
import { machineWithHub } from "./mesh";
import { isPinned, type AgentPins } from "./pins";
import { plainMarkdown } from "./plain-markdown";
import { requestHome } from "./child-sessions";
import type { AgentState, Interaction, Machine, Session } from "./types";

export type SearchScope = "all" | "title" | "conversation" | "user" | "assistant" | "errors";

export const SEARCH_SCOPES: { key: SearchScope; labelKey: string }[] = [
  { key: "all", labelKey: "lib-agent-filter.scope_all" },
  { key: "title", labelKey: "lib-agent-filter.scope_title" },
  { key: "conversation", labelKey: "lib-agent-filter.scope_conversation" },
  { key: "user", labelKey: "lib-agent-filter.scope_user" },
  { key: "assistant", labelKey: "lib-agent-filter.scope_assistant" },
  { key: "errors", labelKey: "lib-agent-filter.scope_errors" },
];

export type SinceWindow = "24h" | "7d" | "30d";

export interface AgentDimensionFilters {
  query: string;
  scope: SearchScope;
  harnesses: string[];
  projects: string[];
  models: string[];
  machines: string[];
  pinnedOnly: boolean;
  since?: SinceWindow;
  searchAsCode: boolean;
  customPlanCode?: string;
}

export const DEFAULT_DIMENSION_FILTERS: AgentDimensionFilters = {
  query: "",
  scope: "all",
  harnesses: [],
  projects: [],
  models: [],
  machines: [],
  pinnedOnly: false,
  searchAsCode: false,
};

export interface FacetOption {
  id: string;
  label: string;
  count: number;
}

export interface AgentFacetCounts {
  harnesses: FacetOption[];
  projects: FacetOption[];
  models: FacetOption[];
  machines: FacetOption[];
  pinned: number;
}

export interface ParsedSearchQuery {
  text: string;
  terms: string[];
  machines: string[];
  projects: string[];
  harnesses: string[];
  models: string[];
  states: AgentState[];
  pinned?: boolean;
  hasErrors?: boolean;
  since?: SinceWindow;
}

export interface EvidenceSnippet {
  role: "user" | "agent" | "error" | "title";
  roleLabel: string;
  before: string;
  match: string;
  after: string;
}

export interface SearchPlan {
  code: string;
  predicates: ((s: Session, ctx: EvalContext) => boolean)[];
  ranker?: (a: Session, b: Session, ctx: EvalContext) => number;
  highlightTerms: string[];
  source: "builtin" | "provider";
}

export interface EvalContext {
  machines: Machine[];
  pins: AgentPins;
  interactions: Interaction[];
  now: number;
}

/** Pluggable contract for extension-backed Search as Code (`search_as_code` capability). */
export interface SearchAsCodeProvider {
  id: string;
  compile: (query: string, facets: AgentFacetCounts) => SearchPlan | null;
}

let activeProvider: SearchAsCodeProvider | null = null;

export function registerSearchAsCodeProvider(provider: SearchAsCodeProvider | null): void {
  activeProvider = provider;
}

export function getSearchAsCodeProvider(): SearchAsCodeProvider | null {
  return activeProvider;
}

/** Normalized harness id for a session (`claude`, `codex`, `opencode`, `pi`, `gemini`, `cursor`, `trae`, or custom). */
export function sessionHarnessId(s: Session): string {
  const { agent } = sessionAgent(s);
  if (agent) return agentId(agent);
  if (s.launcher) {
    const low = s.launcher.trim().toLowerCase();
    for (const k of ["claude", "codex", "opencode", "pi", "gemini", "cursor", "trae"]) {
      if (low.includes(k)) return k;
    }
  }
  return agentId(s.agent);
}

export function sessionHarnessLabel(s: Session): string {
  const { agent, label } = sessionAgent(s);
  return agentName(agent || s.launcher, label || s.agent_name);
}

/** Normalized project name for a session (from repo_root, space_name, workspace_cwd, or cwd). */
export function sessionProjectName(s: Session): string {
  const repo = baseName(s.repo_root);
  if (repo) return repo;
  if (s.space_name?.trim()) return s.space_name.trim();
  const ws = baseName(s.workspace_cwd);
  if (ws) return ws;
  return baseName(s.cwd) || "";
}

/** Normalized model identifier for a session (from `model` or `launcher`). */
export function sessionModelId(s: Session): string {
  const raw = s.model?.trim() || "";
  if (raw) return modelShort(raw) || raw;
  const launcher = s.launcher?.trim() || "";
  if (!launcher) return "";
  const cleaned = launcher.replace(/^(claude|codex|opencode|gemini|pi)[-_:/\s]+/i, "").trim();
  return modelShort(cleaned) || cleaned;
}

/** Whether a session has an active error, turn error, start refusal, or blocked state. */
export function sessionHasError(s: Session, interactions: readonly Interaction[] = []): boolean {
  if (s.start_error || s.turn_error || failureReason(s) || s.attention_reason) return true;
  if (s.work?.phase === "blocked") return true;
  const st = stateOf(s);
  if (st === "needs_you") return true;
  // a child session's card is its parent's (#5120)
  return interactions.some((ix) => ix.machine_id === s.machine_id && requestHome(ix) === s.session_id);
}

/** Toggle an item in a multi-select facet array (Orca All/multi-select semantics: empty array means All). */
export function toggleFacetValue(current: readonly string[], value: string, allValues?: readonly string[]): string[] {
  const next = current.includes(value) ? current.filter((x) => x !== value) : [...current, value];
  if (allValues && allValues.length > 0 && next.length === allValues.length && allValues.every((v) => next.includes(v))) {
    return [];
  }
  return next;
}

/** Extract available facet options with session counts. */
export function buildAgentFacets(sessions: readonly Session[], machines: readonly Machine[], pins: AgentPins): AgentFacetCounts {
  const harnessMap = new Map<string, { label: string; count: number }>();
  const projectMap = new Map<string, { label: string; count: number }>();
  const modelMap = new Map<string, { label: string; count: number }>();
  const machineMap = new Map<string, { label: string; count: number }>();
  let pinned = 0;

  for (const s of sessions) {
    if (s.assistant) continue;
    if (isPinned(s, pins)) pinned++;

    const hid = sessionHarnessId(s);
    const hlabel = sessionHarnessLabel(s);
    const h = harnessMap.get(hid);
    if (h) h.count++;
    else harnessMap.set(hid, { label: hlabel, count: 1 });

    const proj = sessionProjectName(s);
    if (proj) {
      const p = projectMap.get(proj);
      if (p) p.count++;
      else projectMap.set(proj, { label: proj, count: 1 });
    }

    const mod = sessionModelId(s);
    if (mod) {
      const m = modelMap.get(mod);
      if (m) m.count++;
      else modelMap.set(mod, { label: mod, count: 1 });
    }

    const mid = s.machine_id;
    if (mid) {
      const mname = machineWithHub(mid, [...machines]) || mid;
      const mc = machineMap.get(mid);
      if (mc) mc.count++;
      else machineMap.set(mid, { label: mname, count: 1 });
    }
  }

  const toSorted = (map: Map<string, { label: string; count: number }>): FacetOption[] =>
    [...map.entries()]
      .map(([id, v]) => ({ id, label: v.label, count: v.count }))
      .sort((a, b) => b.count - a.count || a.label.localeCompare(b.label));

  return {
    harnesses: toSorted(harnessMap),
    projects: toSorted(projectMap),
    models: toSorted(modelMap),
    machines: toSorted(machineMap),
    pinned,
  };
}

const SINCE_MS: Record<SinceWindow, number> = {
  "24h": 24 * 3600_000,
  "7d": 7 * 24 * 3600_000,
  "30d": 30 * 24 * 3600_000,
};

function parseSinceToken(raw: string): SinceWindow | undefined {
  const v = raw.trim().toLowerCase();
  if (v === "24h" || v === "1d" || v === "2d" || v === "48h") return "24h";
  if (v === "7d" || v === "1w") return "7d";
  if (v === "30d" || v === "1m") return "30d";
  return undefined;
}

/** Parse structured inline operators (`machine:`, `host:`, `project:`, `repo:`, `cwd:`, `harness:`, `agent:`, `model:`, `state:`, `is:`, `since:`). */
export function parseSearchQuery(raw: string): ParsedSearchQuery {
  const out: ParsedSearchQuery = {
    text: "",
    terms: [],
    machines: [],
    projects: [],
    harnesses: [],
    models: [],
    states: [],
  };
  const tokens = raw.trim().split(/\s+/).filter(Boolean);
  for (const token of tokens) {
    const colon = token.indexOf(":");
    if (colon > 0 && colon < token.length - 1) {
      const key = token.slice(0, colon).toLowerCase();
      const val = token.slice(colon + 1);
      const low = val.toLowerCase();
      if (key === "machine" || key === "host") {
        out.machines.push(low);
        continue;
      }
      if (key === "project" || key === "repo" || key === "cwd" || key === "path") {
        out.projects.push(low);
        continue;
      }
      if (key === "harness" || key === "agent" || key === "launcher") {
        out.harnesses.push(low);
        continue;
      }
      if (key === "model") {
        out.models.push(low);
        continue;
      }
      if (key === "state" || key === "status") {
        if (["needs_you", "working", "unread", "idle", "offline", "unknown"].includes(low)) {
          out.states.push(low as AgentState);
          continue;
        }
      }
      if (key === "is") {
        if (low === "pinned" || low === "pin") {
          out.pinned = true;
          continue;
        }
        if (low === "error" || low === "errors" || low === "failed" || low === "blocked") {
          out.hasErrors = true;
          continue;
        }
        if (["needs_you", "working", "unread", "idle", "offline"].includes(low)) {
          out.states.push(low as AgentState);
          continue;
        }
      }
      if (key === "since" || key === "after" || key === "updated") {
        const sw = parseSinceToken(low);
        if (sw) {
          out.since = sw;
          continue;
        }
      }
    }
    out.terms.push(token);
  }
  out.text = out.terms.join(" ");
  return out;
}

/** Strip noisy tool-output prefixes/dumps for `conversation` ("Without tools") and `assistant` scopes. */
export function proseOnlyText(raw: string | undefined): string {
  if (!raw) return "";
  const filteredLines = raw
    .split(/\r?\n/)
    .filter((line) => !/^\s*(\$ |Tool:|tool_use|tool_result|function_call|Running `?[\w./:-]+`?)/i.test(line))
    .join("\n");
  return plainMarkdown(filteredLines).trim();
}

/** Check whether a session's text fields match `terms` under the chosen `SearchScope`. */
export function matchesSearchScope(s: Session, terms: readonly string[], scope: SearchScope, ctx: EvalContext): boolean {
  if (terms.length === 0) {
    return scope === "errors" ? sessionHasError(s, ctx.interactions) : true;
  }
  const haystacks = scopeHaystacks(s, scope, ctx);
  const joined = haystacks.map((h) => h.text.toLowerCase()).join("\n");
  return terms.every((term) => joined.includes(term.toLowerCase()));
}

function scopeHaystacks(s: Session, scope: SearchScope, ctx: EvalContext): { role: EvidenceSnippet["role"]; text: string }[] {
  const title = sessionTitle(s);
  const userAsk = [s.asks, s.goal?.objective, s.todo?.title].filter(Boolean).join(" · ");
  const assistantProse = [proseOnlyText(s.last_text), proseOnlyText(s.work?.line)].filter(Boolean).join(" · ");
  const errText = [s.start_error, s.turn_error, failureReason(s), s.attention_reason, s.work?.phase === "blocked" ? s.work.line : ""]
    .filter(Boolean)
    .join(" · ");
  const meta = [s.cwd, s.repo_root, s.branch, s.space_name, s.slot_name, sessionHarnessLabel(s), sessionModelId(s), s.activity]
    .filter(Boolean)
    .join(" · ");

  switch (scope) {
    case "title":
      return [{ role: "title", text: [title, s.space_name, s.slot_name].filter(Boolean).join(" · ") }];
    case "conversation":
      return [
        { role: "title", text: title },
        { role: "user", text: userAsk },
        { role: "agent", text: assistantProse },
      ];
    case "user":
      return [{ role: "user", text: [userAsk, s.title].filter(Boolean).join(" · ") }];
    case "assistant":
      return [{ role: "agent", text: assistantProse }];
    case "errors":
      if (!sessionHasError(s, ctx.interactions)) return [];
      return [{ role: "error", text: [errText, title, sessionPreview(s)].filter(Boolean).join(" · ") }];
    case "all":
    default: {
      const agentParts = [...new Set([plainMarkdown(sessionPreview(s)), plainMarkdown(s.last_text ?? ""), meta].map((x) => x.trim()).filter(Boolean))];
      return [
        ...(errText ? [{ role: "error" as const, text: errText }] : []),
        ...(userAsk ? [{ role: "user" as const, text: userAsk }] : []),
        { role: "agent", text: agentParts.join(" · ") },
        { role: "title", text: title },
      ];
    }
  }
}

/** Build a role-attributed evidence snippet with highlighted match span when a search query is active. */
export function buildEvidenceSnippet(s: Session, query: string, scope: SearchScope, ctx: EvalContext): EvidenceSnippet | null {
  const parsed = parseSearchQuery(query);
  const terms = parsed.terms.length > 0 ? parsed.terms : [];
  if (terms.length === 0 && scope !== "errors") return null;

  const candidates = scopeHaystacks(s, scope, ctx).filter((c) => c.text.trim().length > 0);
  const roleLabelOf = (role: EvidenceSnippet["role"]): string => {
    switch (role) {
      case "user":
        return t("lib-agent-filter.role_user");
      case "error":
        return t("lib-agent-filter.role_error");
      case "title":
        return t("lib-agent-filter.role_title");
      case "agent":
      default:
        return t("lib-agent-filter.role_agent");
    }
  };

  for (const c of candidates) {
    const text = c.text.replace(/\s+/g, " ").trim();
    if (!text) continue;
    for (const term of terms) {
      const idx = text.toLowerCase().indexOf(term.toLowerCase());
      if (idx >= 0) {
        const start = Math.max(0, idx - 28);
        const end = Math.min(text.length, idx + term.length + 52);
        const before = (start > 0 ? "…" : "") + text.slice(start, idx);
        const match = text.slice(idx, idx + term.length);
        const after = text.slice(idx + term.length, end) + (end < text.length ? "…" : "");
        return { role: c.role, roleLabel: roleLabelOf(c.role), before, match, after };
      }
    }
  }

  if (scope === "errors" && sessionHasError(s, ctx.interactions)) {
    const errCandidate = candidates[0]?.text.replace(/\s+/g, " ").trim().slice(0, 90) || sessionPreview(s);
    if (errCandidate) {
      return { role: "error", roleLabel: roleLabelOf("error"), before: "", match: errCandidate, after: "" };
    }
  }
  return null;
}

/**
 * Compile a natural-language or operator-rich query into an inspectable Search Plan (`Search as Code`,
 * inspired by Perplexity's "Rethinking Search as Code Generation").
 */
export function compileSearchAsCode(
  rawQuery: string,
  filters: AgentDimensionFilters,
  facets: AgentFacetCounts,
): SearchPlan {
  // Plain search (Search as Code off) never turns words into filters: only the explicit operators and the menu's choices
  // filter, and every other word is matched as text, like iOS (#4397). The word lists and a provider apply only when on.
  const nl = filters.searchAsCode;
  if (nl && activeProvider && !filters.customPlanCode) {
    const extPlan = activeProvider.compile(rawQuery, facets);
    if (extPlan) return extPlan;
  }

  const parsed = parseSearchQuery(rawQuery);
  const lowQ = rawQuery.toLowerCase();
  const clauses: string[] = [];
  const predicates: ((s: Session, ctx: EvalContext) => boolean)[] = [];
  const highlightTerms = [...parsed.terms];

  // 1. Detect machines from filter pills, operators, or natural language mentions
  const machineTargets = new Set<string>([...filters.machines.map((m) => m.toLowerCase()), ...parsed.machines]);
  for (const m of nl ? facets.machines : []) {
    const mid = m.id.toLowerCase();
    const mlabel = m.label.toLowerCase();
    if (parsed.terms.some((w) => w.toLowerCase() === mid || w.toLowerCase() === mlabel)) {
      machineTargets.add(mid);
    }
  }
  if (machineTargets.size > 0) {
    const list = [...machineTargets];
    clauses.push(list.length === 1 ? `s.machine === ${JSON.stringify(list[0])}` : `${JSON.stringify(list)}.includes(s.machine)`);
    predicates.push((s, ctx) => {
      const mid = s.machine_id.toLowerCase();
      const mname = machineWithHub(s.machine_id, ctx.machines).toLowerCase();
      return list.some((target) => mid.includes(target) || mname.includes(target));
    });
  }

  // 2. Detect projects from filter pills, operators, or natural language mentions
  const projectTargets = new Set<string>([...filters.projects.map((p) => p.toLowerCase()), ...parsed.projects]);
  for (const p of nl ? facets.projects : []) {
    const pid = p.id.toLowerCase();
    if (pid.length >= 2 && parsed.terms.some((w) => w.toLowerCase() === pid)) {
      projectTargets.add(pid);
    }
  }
  if (projectTargets.size > 0) {
    const list = [...projectTargets];
    clauses.push(list.length === 1 ? `s.project === ${JSON.stringify(list[0])}` : `/${list.join("|")}/i.test(s.project)`);
    predicates.push((s) => {
      const proj = sessionProjectName(s).toLowerCase();
      const cwd = (s.cwd || "").toLowerCase();
      return list.some((target) => proj.includes(target) || cwd.includes(target));
    });
  }

  // 3. Detect harnesses from filter pills, operators, or natural language mentions
  const harnessTargets = new Set<string>([...filters.harnesses.map((h) => h.toLowerCase()), ...parsed.harnesses]);
  for (const h of nl ? ["claude", "codex", "opencode", "pi", "gemini", "cursor", "trae"] : []) {
    if (parsed.terms.some((w) => w.toLowerCase() === h)) harnessTargets.add(h);
  }
  if (harnessTargets.size > 0) {
    const list = [...harnessTargets];
    clauses.push(list.length === 1 ? `s.harness === ${JSON.stringify(list[0])}` : `${JSON.stringify(list)}.includes(s.harness)`);
    predicates.push((s) => {
      const hid = sessionHarnessId(s).toLowerCase();
      const hlabel = sessionHarnessLabel(s).toLowerCase();
      return list.some((target) => hid.includes(target) || hlabel.includes(target));
    });
  }

  // 4. Detect models from filter pills, operators, or natural language mentions
  const modelTargets = new Set<string>([...filters.models.map((m) => m.toLowerCase()), ...parsed.models]);
  for (const m of nl ? ["opus", "sonnet", "haiku", "fable", "argon", "gpt-5", "o3"] : []) {
    if (parsed.terms.some((w) => w.toLowerCase().includes(m))) modelTargets.add(m);
  }
  if (modelTargets.size > 0) {
    const list = [...modelTargets];
    clauses.push(`/${list.join("|")}/i.test(s.model)`);
    predicates.push((s) => {
      const mod = `${sessionModelId(s)} ${s.model ?? ""} ${s.launcher ?? ""}`.toLowerCase();
      return list.some((target) => mod.includes(target));
    });
  }

  // 5. Pinned intent
  const wantPinned = filters.pinnedOnly || parsed.pinned === true || (nl && /(?:置顶|pinned)/i.test(lowQ));
  if (wantPinned) {
    clauses.push("s.isPinned");
    predicates.push((s, ctx) => isPinned(s, ctx.pins));
  }

  // 6. Error / blocked intent
  const wantErrors =
    filters.scope === "errors" ||
    parsed.hasErrors === true ||
    (nl && /(?:报错|错误|失败|阻塞|异常|error|errors|failed|failing|blocked|crash)/i.test(lowQ));
  if (wantErrors) {
    clauses.push("s.hasErrors");
    predicates.push((s, ctx) => sessionHasError(s, ctx.interactions));
  }

  // 7. State intent
  const stateTargets = new Set<AgentState>(parsed.states);
  if (nl && /(?:工作中|正在跑|运行中|running|working)/i.test(lowQ)) stateTargets.add("working");
  if (nl && /(?:需要你|等待审批|待确认|needs_you|waiting for you)/i.test(lowQ)) stateTargets.add("needs_you");
  if (nl && /(?:未读|unread)/i.test(lowQ)) stateTargets.add("unread");
  if (stateTargets.size > 0) {
    const list = [...stateTargets];
    clauses.push(list.length === 1 ? `s.state === ${JSON.stringify(list[0])}` : `${JSON.stringify(list)}.includes(s.state)`);
    predicates.push((s) => list.includes(stateOf(s)));
  }

  // 8. Time window intent (`since:24h` or natural language "最近两天" / "今天" / "本周")
  let sinceWindow: SinceWindow | undefined = filters.since ?? parsed.since;
  if (!sinceWindow && nl) {
    if (/(?:最近一天|今天|24小时|24h|最近两天|两天内|48h|yesterday|today)/i.test(lowQ)) sinceWindow = "24h";
    else if (/(?:最近一周|本周|七天|7天|7d|past week)/i.test(lowQ)) sinceWindow = "7d";
    else if (/(?:最近一个月|30天|30d|past month)/i.test(lowQ)) sinceWindow = "30d";
  }
  if (sinceWindow) {
    const windowMs = SINCE_MS[sinceWindow];
    clauses.push(`s.updatedWithin(${JSON.stringify(sinceWindow)})`);
    predicates.push((s, ctx) => {
      const ts = Date.parse(s.updated_at ?? s.state_since ?? "");
      return Number.isFinite(ts) && ctx.now - ts <= windowMs;
    });
  }

  // 9. Remaining free-text keywords (excluding tokens already absorbed into structured clauses when in Search-as-Code mode)
  // i18n-ignore
  const cjkStopWords = ["置顶", "报错", "错误", "失败", "阻塞", "异常", "工作中", "正在跑", "运行中", "需要你", "未读", "最近一天", "今天", "最近两天", "两天内", "最近一周", "本周"];
  const absorbedWords = new Set<string>([
    ...machineTargets,
    ...projectTargets,
    ...harnessTargets,
    ...modelTargets,
    ...cjkStopWords,
    "pinned",
    "error",
    "errors",
    "failed",
    "blocked",
    "running",
    "working",
    "unread",
  ]);
  const residualTerms = parsed.terms.filter((w) => {
    if (!filters.searchAsCode) return true;
    return !absorbedWords.has(w.toLowerCase());
  });

  if (residualTerms.length > 0) {
    clauses.push(`s.matches(${JSON.stringify(residualTerms.join(" "))}, ${JSON.stringify(filters.scope)})`);
    predicates.push((s, ctx) => matchesSearchScope(s, residualTerms, filters.scope, ctx));
  } else if (filters.scope === "errors" && !wantErrors) {
    clauses.push("s.hasErrors");
    predicates.push((s, ctx) => sessionHasError(s, ctx.interactions));
  }

  const expr = clauses.length > 0 ? clauses.join(" && ") : "true";
  const defaultCode = `session.where(s => ${expr})`;
  const finalCode = (nl && filters.customPlanCode?.trim()) || defaultCode;

  // If the user edited the Search Plan code directly, also honor custom `/regex/i.test(s.<field>)` or `s.<field> === "..."`
  if (nl && filters.customPlanCode?.trim() && filters.customPlanCode.trim() !== defaultCode) {
    const custom = filters.customPlanCode.trim();
    predicates.push((s, ctx) => evaluateCustomPlanCode(custom, s, ctx));
  }

  return {
    code: finalCode,
    predicates,
    highlightTerms: residualTerms.length > 0 ? residualTerms : highlightTerms,
    source: "builtin",
  };
}

function evaluateCustomPlanCode(code: string, s: Session, ctx: EvalContext): boolean {
  const record: Record<string, string | boolean> = {
    machine: s.machine_id.toLowerCase(),
    project: sessionProjectName(s).toLowerCase(),
    harness: sessionHarnessId(s).toLowerCase(),
    model: sessionModelId(s).toLowerCase(),
    state: stateOf(s),
    title: sessionTitle(s).toLowerCase(),
    isPinned: isPinned(s, ctx.pins),
    hasErrors: sessionHasError(s, ctx.interactions),
  };
  for (const m of code.matchAll(/s\.(machine|project|harness|model|state|title)\s*===\s*["']([^"']+)["']/g)) {
    const field = m[1];
    const target = m[2].toLowerCase();
    if (!String(record[field] ?? "").includes(target)) return false;
  }
  for (const m of code.matchAll(/\/([^/]+)\/[a-z]*\.test\(s\.(machine|project|harness|model|title)\)/g)) {
    try {
      const re = new RegExp(m[1], "i");
      if (!re.test(String(record[m[2]] ?? ""))) return false;
    } catch {
      // ignore invalid user regex while typing
    }
  }
  if (/\bs\.isPinned\b/.test(code) && !record.isPinned) return false;
  if (/\bs\.hasErrors\b/.test(code) && !record.hasErrors) return false;
  return true;
}

/** Whether any dimension filter, search query, scope, or Search-as-Code mode is currently active. */
export function hasActiveDimensionFilters(f: AgentDimensionFilters): boolean {
  return Boolean(
    f.query.trim() ||
      f.scope !== "all" ||
      f.harnesses.length > 0 ||
      f.projects.length > 0 ||
      f.models.length > 0 ||
      f.machines.length > 0 ||
      f.pinnedOnly ||
      f.since ||
      f.searchAsCode ||
      f.customPlanCode?.trim(),
  );
}

/** Apply all orthogonal dimension filters, search scope, inline operators, and Search-as-Code predicates to `sessions`. */
export function applyDimensionFilters(
  sessions: readonly Session[],
  filters: AgentDimensionFilters,
  ctx: EvalContext,
  facets?: AgentFacetCounts,
): { sessions: Session[]; plan: SearchPlan } {
  const computedFacets = facets ?? buildAgentFacets(sessions, ctx.machines, ctx.pins);
  const plan = compileSearchAsCode(filters.query, filters, computedFacets);
  if (!hasActiveDimensionFilters(filters)) {
    return { sessions: [...sessions], plan };
  }
  const filtered = sessions.filter((s) => plan.predicates.every((fn) => fn(s, ctx)));
  if (plan.ranker) {
    filtered.sort((a, b) => plan.ranker!(a, b, ctx));
  }
  return { sessions: filtered, plan };
}
