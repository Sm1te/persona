import { memo, useMemo, useState } from "react";
import { t } from "../../i18n";
import { flipPin } from "../../lib/agent-actions";
import {
  buildEvidenceSnippet,
  DEFAULT_DIMENSION_FILTERS,
  SEARCH_SCOPES,
  sessionHarnessLabel,
  sessionHasError,
  sessionModelId,
  sessionProjectName,
  toggleFacetValue,
  type AgentDimensionFilters,
  type AgentFacetCounts,
  type EvalContext,
  type SearchPlan,
  type SearchScope,
} from "../../lib/agent-chatlist-filter";
import { awaitsReply, sessionSource, sourceName, type AgentMutes } from "../../lib/agent-groups";
import { ago, backgroundWaiting, sessionPreview, sessionTitle, stateOf } from "../../lib/format";
import { sessionCard } from "../../lib/folded-rows";
import { machineWithHub } from "../../lib/mesh";
import { plainMarkdown } from "../../lib/plain-markdown";
import { href, type Route } from "../../lib/router";
import { draftLine, rowLine } from "../../lib/row-line";
import { useLeftDraft } from "../../lib/drafts";
import { rememberSessionRow, useStore } from "../../lib/store";
import type { Session } from "../../lib/types";
import { ultracodeMarkShown } from "../../lib/ultracode";
import { SessionMark } from "../../ui/AgentMark";
import { cx, Menu, useMenu, useNow, type MenuItem } from "../../ui/atoms";
import { contextMenuProps } from "../../ui/context-menu";
import { Icon } from "../../ui/icons";
import { SearchField } from "../../ui/SearchField";
import { StatusMark } from "../../ui/StatusMark";
import { UltraMark } from "../chat/Ultracode";
import { CURRENT_ROW, type GoFn, type RegFn, type RowMenuFn } from "../SidebarParts";

type FilterPage = "root" | "scope" | "harnesses" | "projects" | "models" | "machines";
type FacetKey = Exclude<FilterPage, "root" | "scope">;

const FACET_PAGES: { key: FacetKey; labelKey: string }[] = [
  { key: "harnesses", labelKey: "lib-agent-filter.filter_harness" },
  { key: "projects", labelKey: "lib-agent-filter.filter_project" },
  { key: "models", labelKey: "lib-agent-filter.filter_model" },
  { key: "machines", labelKey: "lib-agent-filter.filter_machine" },
];

/** How many filters are on (the Filter button's count): the query and Search as Code are not filters of their own. */
export function activeFilterCount(f: AgentDimensionFilters): number {
  return (
    f.harnesses.length + f.projects.length + f.models.length + f.machines.length + (f.pinnedOnly ? 1 : 0) + (f.scope !== "all" ? 1 : 0) + (f.since ? 1 : 0)
  );
}

// The Agents list's search (#4397): one row, the field and a Filter button whose menu holds the scope, Pinned, the four facets,
// the chat-list view and Search as Code. Chips for what is on appear only while something is, and the plan bar only with
// Search as Code on.
export function AgentDimensionFilterBar({
  filters,
  onChange,
  facets,
  plan,
  chatList,
  onToggleChatList,
}: {
  filters: AgentDimensionFilters;
  onChange: (next: AgentDimensionFilters) => void;
  facets: AgentFacetCounts;
  plan: SearchPlan;
  chatList: boolean;
  onToggleChatList: () => void;
}) {
  const filterMenu = useMenu();
  const [page, setPage] = useState<FilterPage>("root");
  const [editingPlan, setEditingPlan] = useState(false);

  const activeScope = SEARCH_SCOPES.find((s) => s.key === filters.scope) ?? SEARCH_SCOPES[0];
  const count = activeFilterCount(filters);

  const resetFilters = () => {
    onChange({ ...DEFAULT_DIMENSION_FILTERS, query: filters.query, searchAsCode: filters.searchAsCode, customPlanCode: filters.customPlanCode });
  };

  const menuItems = (): MenuItem[] => {
    const back: MenuItem = { key: "back", label: t("sidebar.back"), keepOpen: true, onSelect: () => setPage("root") };
    if (page === "scope") {
      return [
        back,
        ...SEARCH_SCOPES.map((sc, i) => ({
          key: sc.key,
          label: t(sc.labelKey),
          divider: i === 0,
          radio: true,
          checked: filters.scope === sc.key,
          keepOpen: true,
          onSelect: () => onChange({ ...filters, scope: sc.key as SearchScope }),
        })),
      ];
    }
    if (page !== "root") {
      const options = facets[page];
      const selected = filters[page];
      const allIds = options.map((o) => o.id);
      const set = (next: string[]) => onChange({ ...filters, [page]: next });
      return [
        back,
        { key: "__all__", label: t("lib-agent-filter.all"), divider: true, checked: selected.length === 0, keepOpen: true, onSelect: () => set([]) },
        ...options.map((o) => ({
          key: o.id,
          label: o.label,
          hint: String(o.count),
          checked: selected.includes(o.id),
          keepOpen: true,
          onSelect: () => set(toggleFacetValue(selected, o.id, allIds)),
        })),
      ];
    }
    const facetRows = FACET_PAGES.filter((f) => facets[f.key].length > 1 || filters[f.key].length > 0).map(
      (f, i): MenuItem => ({
        key: f.key,
        label: t(f.labelKey),
        hint: filters[f.key].length > 0 ? String(filters[f.key].length) : t("lib-agent-filter.all"),
        divider: i === 0,
        submenu: true,
        onSelect: () => setPage(f.key),
      }),
    );
    return [
      { key: "scope", label: t("lib-agent-filter.search_in"), hint: t(activeScope.labelKey), submenu: true, onSelect: () => setPage("scope") },
      {
        key: "pinned",
        label: t("lib-agent-filter.filter_pinned"),
        hint: facets.pinned > 0 ? String(facets.pinned) : undefined,
        checked: filters.pinnedOnly,
        keepOpen: true,
        onSelect: () => onChange({ ...filters, pinnedOnly: !filters.pinnedOnly }),
      },
      ...facetRows,
      { key: "chat-list", label: t("lib-agent-filter.chat_list_view"), divider: true, checked: chatList, onSelect: onToggleChatList },
      {
        key: "search-as-code",
        label: t("lib-agent-filter.search_as_code"),
        checked: filters.searchAsCode,
        onSelect: () => onChange({ ...filters, searchAsCode: !filters.searchAsCode, customPlanCode: undefined }),
      },
      ...(count > 0 ? [{ key: "reset", label: t("lib-agent-filter.reset_filters"), divider: true, onSelect: resetFilters }] : []),
    ];
  };

  const chips: { key: string; label: string; onRemove: () => void }[] = [];
  if (filters.scope !== "all") {
    chips.push({ key: "scope", label: `${t("lib-agent-filter.search_in")}: ${t(activeScope.labelKey)}`, onRemove: () => onChange({ ...filters, scope: "all" }) });
  }
  if (filters.pinnedOnly) {
    chips.push({ key: "pinned", label: t("lib-agent-filter.filter_pinned"), onRemove: () => onChange({ ...filters, pinnedOnly: false }) });
  }
  for (const f of FACET_PAGES) {
    for (const id of filters[f.key]) {
      const label = facets[f.key].find((x) => x.id === id)?.label ?? id;
      chips.push({
        key: `${f.key}:${id}`,
        label: `${t(f.labelKey)}: ${label}`,
        onRemove: () => onChange({ ...filters, [f.key]: filters[f.key].filter((x) => x !== id) }),
      });
    }
  }
  if (filters.since) {
    chips.push({ key: "since", label: `since:${filters.since}`, onRemove: () => onChange({ ...filters, since: undefined }) });
  }

  const filterLabel = count > 0 ? `${t("lib-agent-filter.filter")} (${count})` : t("lib-agent-filter.filter");
  return (
    <div className="mt-1.5 flex flex-col gap-1.5 px-1" data-testid="agent-dimension-filter-bar">
      <div className="flex items-center gap-1">
        <SearchField
          tone="inset"
          size="sm"
          className="min-w-0 flex-1"
          value={filters.query}
          onChange={(q) => onChange({ ...filters, query: q })}
          placeholder={t("lib-agent-filter.search_placeholder")}
        />
        <button
          type="button"
          onClick={(e) => {
            setPage("root");
            filterMenu.open(e);
          }}
          aria-haspopup="menu"
          aria-expanded={Boolean(filterMenu.anchor)}
          aria-label={filterLabel}
          title={filterLabel}
          data-testid="agent-filter-button"
          className={cx(
            "touch-hit relative z-10 flex h-8 min-w-8 shrink-0 items-center justify-center gap-1 rounded-control px-2 text-[11.5px] shadow-hairline transition-colors",
            count > 0 || filters.searchAsCode || chatList ? "bg-accent-tint font-medium text-accent-ink" : "bg-inset text-ink-2 hover:bg-hover hover:text-ink",
          )}
        >
          <Icon name="filter" size={13} stroke={2} />
          {count > 0 && <span className="tabular-nums">{count}</span>}
        </button>
      </div>

      {filterMenu.anchor && (
        <Menu anchor={filterMenu.anchor} onClose={filterMenu.close} align="right" width={220} header={t("lib-agent-filter.filter")} items={menuItems()} />
      )}

      {chips.length > 0 && (
        <div className="flex flex-wrap items-center gap-1 text-[11px]" data-testid="scoped-filter-strip">
          {chips.map((chip) => (
            <span
              key={chip.key}
              className="inline-flex max-w-full items-center gap-1 rounded-full bg-inset py-0.5 pr-1 pl-2 text-[11px] font-medium text-ink shadow-hairline"
            >
              <span className="min-w-0 truncate">{chip.label}</span>
              <button
                type="button"
                aria-label={t("lib-agent-filter.remove_filter", { name: chip.label })}
                onClick={chip.onRemove}
                className="flex size-4 shrink-0 items-center justify-center rounded-full text-ink-3 hover:bg-hover hover:text-ink"
              >
                <Icon name="x" size={9} stroke={2.4} />
              </button>
            </span>
          ))}
          <button
            type="button"
            data-testid="scoped-filter-reset"
            onClick={resetFilters}
            className="ml-auto shrink-0 whitespace-nowrap rounded-control px-1.5 py-0.5 text-[11px] font-medium text-accent-ink hover:bg-hover"
          >
            {t("lib-agent-filter.reset_filters")}
          </button>
        </div>
      )}

      {filters.searchAsCode && (
        <div
          data-testid="search-as-code-plan"
          className="flex items-center gap-1.5 rounded-control bg-inset px-2 py-1 text-[11px] shadow-hairline ring-1 ring-accent/25"
        >
          <Icon name="code" size={12} stroke={2} className="shrink-0 text-accent-ink" />
          {editingPlan ? (
            <input
              autoFocus
              data-testid="search-as-code-input"
              aria-label={t("lib-agent-filter.search_plan")}
              value={filters.customPlanCode ?? plan.code}
              onChange={(e) => onChange({ ...filters, customPlanCode: e.target.value })}
              onBlur={() => setEditingPlan(false)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === "Escape") setEditingPlan(false);
              }}
              className="min-w-0 flex-1 bg-transparent font-mono text-[11px] text-ink outline-none"
            />
          ) : (
            <button
              type="button"
              onClick={() => setEditingPlan(true)}
              title={`${t("lib-agent-filter.search_plan")}: ${plan.code}`}
              className="min-w-0 flex-1 truncate text-left font-mono text-[11px] text-ink hover:text-accent-ink"
            >
              {plan.code}
            </button>
          )}
          <button
            type="button"
            data-testid="search-as-code-off"
            aria-label={t("lib-agent-filter.search_as_code_off")}
            title={t("lib-agent-filter.search_as_code_off")}
            onClick={() => {
              setEditingPlan(false);
              onChange({ ...filters, searchAsCode: false, customPlanCode: undefined });
            }}
            className="flex size-4 shrink-0 items-center justify-center rounded-full text-ink-3 hover:bg-hover hover:text-ink"
          >
            <Icon name="x" size={9} stroke={2.4} />
          </button>
        </div>
      )}
    </div>
  );
}

/** IM Chat-List style Agent Session Row:
 *  - Left: Harness avatar tile + live status dot badge on the bottom-right corner
 *  - Top line: Bold session title (+ Ultracode / Pinned indicator) + relative timestamp on the right
 *  - Second line: Role-attributed search evidence snippet (with highlighted match) OR live last message / tool summary
 *  - Bottom compact chips: `Machine · Project · Harness · Model`
 *  - Right action: One-click Pin / Unpin button + `⋯` actions menu
 */
export const AgentChatListRow = memo(function AgentChatListRow({
  s,
  k,
  reg,
  hover,
  active,
  to,
  go,
  pinned,
  mutes,
  filters,
  ctx,
  onMenu,
}: {
  s: Session;
  k: string;
  reg: RegFn;
  hover: (k: string | null) => void;
  active: boolean;
  to: Route;
  go: GoFn;
  pinned: boolean;
  mutes: AgentMutes;
  filters: AgentDimensionFilters;
  ctx: EvalContext;
  onMenu?: RowMenuFn;
}) {
  const st = stateOf(s);
  const awaiting = awaitsReply(s, mutes);
  const ix = useStore((state) => sessionCard(state.interactions, s.machine_id, s.session_id));
  const draft = draftLine(useLeftDraft(`${s.machine_id}/${s.session_id}`));
  const line: { lead?: string; text: string; tone?: "needs" | "reply" } | undefined = draft ?? rowLine(s, ix, awaiting);
  const previewText = line?.text || plainMarkdown(sessionPreview(s)) || s.activity || "";
  const now = useNow(60_000, Boolean(s.updated_at));
  const timeLabel = s.updated_at ? ago(s.updated_at, now) : "";
  const ultra = ultracodeMarkShown(s);
  const hasErr = sessionHasError(s, ctx.interactions);

  const evidence = useMemo(
    () => (filters.query.trim() || filters.scope === "errors" ? buildEvidenceSnippet(s, filters.query, filters.scope, ctx) : null),
    [s, filters.query, filters.scope, ctx],
  );

  const machineLabel = machineWithHub(s.machine_id, ctx.machines) || s.machine_id;
  const projectLabel = sessionProjectName(s);
  const harnessLabel = sessionHarnessLabel(s);
  const modelLabel = sessionModelId(s);
  const source = sessionSource(s);
  const regRow = useMemo(() => reg(k), [reg, k]);

  return (
    <div
      data-testid="agent-chatlist-row"
      data-session-id={s.session_id}
      data-pinned={pinned ? "true" : "false"}
      className={cx(
        "group/row relative rounded-[10px] transition-colors",
        pinned && !active && "bg-inset/45 ring-1 ring-line/60",
      )}
    >
      <a
        ref={regRow}
        href={href(to)}
        onClick={(e) => {
          rememberSessionRow(s);
          go(to)(e);
        }}
        {...(onMenu ? contextMenuProps((at, el) => onMenu(s, el, at)) : {})}
        onMouseEnter={() => hover(k)}
        onFocus={() => hover(k)}
        onBlur={() => hover(null)}
        aria-current={active ? "page" : undefined}
        className={cx(
          "relative z-10 flex w-full items-start gap-2.5 rounded-[10px] px-2.5 py-2 transition-colors",
          active ? CURRENT_ROW : "hover:bg-hover",
        )}
      >
        {/* Left: Harness Avatar + corner live StatusMark badge */}
        <span className="relative mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-full bg-inset shadow-hairline">
          <SessionMark s={s} size={17} />
          <span className="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-canvas ring-1 ring-canvas">
            <StatusMark state={st} size="sm" awaiting={awaiting} waiting={backgroundWaiting(s)} />
          </span>
        </span>

        {/* Center column: Title + Time, Second line (Evidence snippet or Last message), Metadata chips */}
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          {/* Line 1: Session Title + Ultracode + Pinned badge + Relative Activity Time */}
          <span className="flex min-w-0 items-center gap-1.5">
            {pinned && (
              <span className="shrink-0 text-accent" title={t("sidebar.pinned")}>
                <Icon name="pin" size={11} stroke={2.3} />
              </span>
            )}
            <span
              className={cx(
                "min-w-0 flex-1 truncate text-[13px] leading-tight",
                active || st === "unread" || st === "needs_you" ? "font-semibold text-ink" : st === "offline" ? "text-ink-3" : "font-medium text-ink-2",
              )}
            >
              {sessionTitle(s)}
            </span>
            {ultra && <UltraMark size={10} />}
            {timeLabel && (
              <span className="shrink-0 text-[11px] text-ink-3 tabular-nums group-hover/row:invisible">
                {timeLabel}
              </span>
            )}
          </span>

          {/* Line 2: Role-attributed Evidence Snippet when searching, else Last message / status line */}
          {evidence ? (
            <span data-testid="agent-evidence-snippet" className="truncate text-[11.5px] leading-4 text-ink-2">
              <span
                className={cx(
                  "mr-1 font-semibold",
                  evidence.role === "error" ? "text-red" : evidence.role === "user" ? "text-accent-ink" : "text-ink-2",
                )}
              >
                {evidence.roleLabel}
              </span>
              <span>{evidence.before}</span>
              {evidence.match && (
                <mark className="rounded-[3px] bg-accent-tint px-0.5 font-medium text-accent-ink">
                  {evidence.match}
                </mark>
              )}
              <span>{evidence.after}</span>
            </span>
          ) : (
            (line?.lead || previewText) && (
              <span className="truncate text-[11.5px] leading-4 text-ink-3">
                {line?.lead && (
                  <span className={cx("mr-1 font-medium", line.tone === "reply" ? "text-green" : "text-attention")}>
                    {line.lead}
                  </span>
                )}
                {previewText}
              </span>
            )
          )}

          {/* Line 3: Compact metadata capsules (Machine · Project · Harness · Model) */}
          <span className="mt-0.5 flex flex-wrap items-center gap-1 text-[10.5px] leading-4 text-ink-3" data-testid="agent-chatlist-chips">
            {machineLabel && (
              <span className="inline-flex max-w-[96px] items-center gap-0.5 truncate rounded bg-inset px-1.5 py-px">
                <Icon name="machine" size={9} stroke={2} />
                <span className="truncate">{machineLabel}</span>
              </span>
            )}
            {projectLabel && (
              <span className="inline-flex max-w-[110px] items-center gap-0.5 truncate rounded bg-inset px-1.5 py-px">
                <Icon name="folder" size={9} stroke={2} />
                <span className="truncate">{projectLabel}</span>
              </span>
            )}
            {harnessLabel && (
              <span className="inline-flex max-w-[96px] items-center gap-0.5 truncate rounded bg-inset px-1.5 py-px">
                <span className="truncate">{harnessLabel}</span>
              </span>
            )}
            {modelLabel && (
              <span className="inline-flex max-w-[110px] items-center gap-0.5 truncate rounded bg-inset px-1.5 py-px font-mono text-[10px]">
                <span className="truncate">{modelLabel}</span>
              </span>
            )}
            {source && (
              <span className="inline-flex items-center rounded bg-inset px-1 py-px" title={sourceName(source)}>
                {sourceName(source)}
              </span>
            )}
            {hasErr && st !== "needs_you" && (
              <span className="inline-flex items-center gap-0.5 rounded bg-red/10 px-1.5 py-px font-medium text-red">
                <Icon name="warning" size={9} stroke={2.2} />
              </span>
            )}
          </span>
        </span>
      </a>

      {/* Hover / right actions: One-click Pin/Unpin button + More menu button */}
      <div className="absolute top-1.5 right-1.5 z-20 hidden items-center gap-0.5 group-hover/row:flex [@media(hover:none)]:flex">
        <button
          type="button"
          data-testid="agent-row-pin-button"
          aria-label={pinned ? t("sidebar.unpin") : t("sidebar.pin")}
          title={pinned ? t("sidebar.unpin") : t("sidebar.pin")}
          onClick={(e) => {
            e.preventDefault();
            e.stopPropagation();
            void flipPin(s);
          }}
          className={cx(
            "flex size-6 items-center justify-center rounded-[6px] bg-hover transition-colors hover:bg-hover-2",
            pinned ? "text-accent" : "text-ink-3 hover:text-ink",
          )}
        >
          <Icon name="pin" size={12} stroke={2.1} />
        </button>
        {onMenu && (
          <button
            type="button"
            aria-label={t("sidebar.agent_actions")}
            aria-haspopup="menu"
            title={t("sidebar.more")}
            onClick={(e) => {
              e.preventDefault();
              e.stopPropagation();
              onMenu(s, e.currentTarget.parentElement ?? e.currentTarget);
            }}
            className="flex size-6 items-center justify-center rounded-[6px] bg-hover text-ink-3 hover:bg-hover-2 hover:text-ink"
          >
            <Icon name="more" size={13} />
          </button>
        )}
      </div>
    </div>
  );
});
