// The inbox list (docs/assistant/inbox.md"App"): filters by source and kind, search, then Needs you (agents waiting on you first),
// FYI, Collapsed and Done, each a section that opens / closes from its title.
import { Fragment, memo, useCallback, useEffect, useMemo, useRef, useState, type Dispatch, type SetStateAction } from "react";
import {
  agentDraft, agentWorking, conversationRoute, fromLine, interactionAgentTitle, interactionAsk, interactionTitle, isDone, isLark,
  itemKey, itemRoute, listInboxMutes, muteItem, muteLine, muteShortName, muteTitle, policyMenuTitle, sameItem, unmuteInbox,
  BULK_CONFIRM_AT, bulkScope, bulkScopeName, connectorSources, KIND_FILTERS, kindLabel, labelIsUrgent, mailAccounts, mailShort, matches, reloadInbox, sections, sendReply, setAllDone, setDone, shortTime, TIERS,
  useInbox, waitingAgents, type InboxItem, type InboxMute, type SectionKey, doneWithHistory, loadOlderDone, loadDoneDays, loadDoneGroup,
} from "../../lib/inbox";
import { dayGroupCounts, doneGroups, groupBySource, groupByTime, openAtFirst, sortTimeKeys, timeRange, timeTitle, type Grouping, type InboxGroup } from "../../lib/inbox-groups";
import { useMailAccounts } from "../../lib/mail";
import { hubTranslation, useTranslatePrefs } from "../../lib/translate";
import { machineWithHub } from "../../lib/mesh";
import { href, navigate, onLinkClick } from "../../lib/router";
import { openedItemState } from "../../lib/inbox-nav";
import { useInboxSources } from "../../lib/inbox-sources";
import { subscriptionsHref, useSubscriptionsShown } from "../../lib/subscriptions";
import { type Manager } from "../../lib/managers";
import { scoped, useScope, useScoped } from "../../lib/current-assistant";
import { useStore } from "../../lib/store";
import type { Interaction } from "../../lib/types";
import { t } from "../../i18n";
import { requestRoute } from "../../lib/card-focus";
import { requestHome } from "../../lib/child-sessions";
import { Button, Collapse, cx, Dialog, IconButton, Loading, Menu, toast, useMenu, type MenuItem } from "../../ui/atoms";
import { errorText } from "../../lib/api";
import { contextMenuProps } from "../../ui/context-menu";
import type { MenuAt } from "../../ui/menu-model";
import { Icon } from "../../ui/icons";
import { openPolicyDialog, RulesDialog } from "./PolicyDialog";
import { isNoHover } from "../../ui/media-query";
import { scrollBehavior } from "../../ui/stream/motion";
import { DraftDialog } from "./InboxDialogs";
import { TriageBanner } from "./TriageBanner";
import { UnreadNotice } from "./UnreadNotice";
import { EventMark } from "./InboxEvent";
import { FolderBar, FolderLine, FolderSettingsDialog, openFolderDialog, useFoldersShown } from "./InboxFolders";
import { folderCrumbs, UNFILED } from "../../lib/inbox-folders";
import { AgentWorking, Chip, Count, DraftCard, Glyph, ItemAvatar, Spark, StatusMenu, Tag, type GlyphName } from "./InboxParts";
import { CheckMark } from "../settings/ProviderFields";
import { OfflineState, useShownOffline } from "../ManagerParts";

function useStored(key: string): [string, (v: string) => void] {
  const [v, setV] = useState(() => localStorage.getItem(key) ?? "");
  return [v, (x: string) => {
    setV(x);
    if (x) localStorage.setItem(key, x);
    else localStorage.removeItem(key);
  }];
}

/** The groups opened or closed by hand, kept on this device (#2059): "<manager group>|<section>|<group>" → open. */
function useFolds(): [Record<string, boolean>, (key: string, open: boolean) => void] {
  const [folds, setFolds] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem("inbox.folds") ?? "{}") as Record<string, boolean>;
    } catch {
      return {};
    }
  });
  const put = (key: string, open: boolean) => setFolds((o) => {
    const n = { ...o, [key]: open };
    localStorage.setItem("inbox.folds", JSON.stringify(n));
    return n;
  });
  return [folds, put];
}

/** The sections open at first (in every manager's group): Needs you and FYI. */
const OPEN_AT_FIRST = new Set(["action", "fyi"]);

/** Selecting (#2059): the picked items' keys (itemKey), null when not selecting. */
type Picked = Set<string> | null;

/** `selected`: the item opened (its id and manager). `picked` / `onPicked`: selecting, from the page's "Select". */
export function InboxList({ selected, picked = null, onPicked }: { selected?: { id: string; manager?: string }; picked?: Picked; onPicked?: Dispatch<SetStateAction<Picked>> }) {
  // with several assistants the list is the current one's (docs/assistant/managers.md "Clients"); one: every item
  const mine = useScope();
  const items = useScoped(useInbox((s) => s.items));
  const loaded = useInbox((s) => s.loaded);
  const loading = useInbox((s) => s.loading);
  const error = useInbox((s) => s.error);
  const triage = useInbox((s) => s.triage);
  const storeLoaded = useStore((s) => s.loaded);
  // The waiting agents come with the store, often after the items: painting the items first and then the agents over
  // them pushed every row down (a layout shift of 600px on a phone), so the list waits for both and paints once.
  const ready = loaded && storeLoaded;
  const [source, setSource] = useStored("inbox.source");
  const [kind, setKind] = useStored("inbox.kind");
  // Needs you, FYI and Collapsed by source or by time (#2059), kept on this device; Done is always by time
  const [groupingSaved, setGrouping] = useStored("inbox.grouping");
  const grouping: Grouping = groupingSaved === "time" ? "time" : "source";
  const [folds, putFold] = useFolds();
  const history = useInbox((st) => st.history);
  const historyMore = useInbox((st) => st.historyMore);
  const historyLoading = useInbox((st) => st.historyLoading);
  const historyError = useInbox((st) => st.historyError);
  const doneDays = useInbox((st) => st.doneDays);
  const groupLoading = useInbox((st) => st.groupLoading);
  const groupComplete = useInbox((st) => st.groupComplete);
  const [storedFolder, setFolder] = useStored("inbox.folder");
  const [query, setQuery] = useState("");
  const foldersShown = useFoldersShown(items);
  // folders hidden (turned off, nothing filed): a folder picked before does not filter what can't be seen
  const folder = foldersShown ? storedFolder : "";
  // The agents waiting, picked in the store: the list holds the store's own interaction objects, so a sessions or
  // machines push that leaves them as they were keeps the same array (useStore compares it item by item) and the list,
  // with every row in it, does not render again.
  const agents = useScoped(useStore((st) => waitingAgents(st.interactions, st.sessions, st.mutes, { source, kind, query, folder })));
  // the sections opened or closed by hand, by "<manager group>:<section>" (each manager's group keeps its own; one
  // manager: the group ""), the others as they are at first
  const [toggled, setToggled] = useState<Set<string>>(() => new Set());
  const isOpen = (group: string, k: string) => OPEN_AT_FIRST.has(k) !== toggled.has(`${group}:${k}`);
  const [editing, setEditing] = useState<InboxItem | null>(null);
  const accounts = useMailAccounts();
  const more = useMenu();
  const [mutesOpen, setMutesOpen] = useState(false);
  const [rulesOpen, setRulesOpen] = useState(false);
  const subscriptionsShown = useSubscriptionsShown();
  const [foldersOpen, setFoldersOpen] = useState(false);
  // Clean up all over many items asks first; the items are the ones shown when it was asked.
  const [confirm, setConfirm] = useState<{ items: InboxItem[]; scope: string } | null>(null);

  // The opened item stays in sight in the list (a link straight to it, a click in the detail).
  const selectedKey = selected ? itemKey(selected) : "";
  useEffect(() => {
    if (!selected) return;
    const rows = [...document.querySelectorAll<HTMLElement>(`[data-item="${CSS.escape(selected.id)}"]`)];
    (rows.find((el) => sameItem({ id: selected.id, manager: el.dataset.manager }, selected)) ?? rows[0])?.scrollIntoView({ block: "nearest" });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedKey, loaded, storeLoaded]);

  const s = useMemo(() => sections(items, { source, kind, query, folder }), [items, source, kind, query, folder]);
  // what the folder bar counts: the items the other filters leave
  const inFilters = useMemo(() => (foldersShown ? items.filter((it) => matches(it, { source, kind, query })) : []), [foldersShown, items, source, kind, query]);
  // several assistants: the list's one group is the current assistant's (its Done history and day counts are asked of
  // it by name); one: the group "" as before
  const byManager = useMemo(() => (mine.manager ? [{ manager: mine.manager, sec: s, ags: agents }] : null), [mine.manager, s, agents]);
  // the hub's list of mail accounts is the default assistant's: with several, the chips are the accounts the current
  // one's items name
  const chips = mailAccounts(mine.several ? [] : accounts, items);
  const connectors = useMemo(() => connectorSources(items), [items]);
  const failed = Boolean(error) && !items.length;
  const empty = !s.tiers.action.length && !s.tiers.working.length && !s.tiers.fyi.length && !s.tiers.noise.length && !agents.length;
  // the current assistant offline (several assistants): the hub's merged list leaves it out, so nothing here means it
  // couldn't answer, not that it has nothing; it says so instead of "All caught up" or the first-run page
  const offline = useShownOffline();
  const away = ready && empty && !failed && Boolean(offline);
  // nothing ever came in, nothing filtered: is anything connected at all? (still unknown: the plain "All caught up")
  const sources = useInboxSources(ready && empty && !failed && !away && !items.length && !query && !source && !kind && !folder);
  const unconnected = sources && !sources.connected ? sources : null;
  // Clean up all: the open items the list shows (its filters and search); the agents waiting are not items.
  const openInView = kind === "agent" ? [] : [...s.tiers.action, ...s.tiers.working, ...s.tiers.fyi, ...s.tiers.noise];
  const filterNames = [
    source === "feishu" ? t("inbox-inboxlist.source_lark") : connectors.find((c) => c.id === source)?.name ?? mailShort(chips.find((a) => a.id === source)?.email ?? ""),
    KIND_FILTERS.find((k) => k.key === kind)?.label ?? "",
    folder === UNFILED ? t("inbox-folders.unfiled") : folderCrumbs(folder).at(-1)?.name ?? "",
  ];
  const scope = bulkScope(filterNames, query, openInView.length);
  // Selecting (#2059): the open items in sight can be picked (in the sections shown open; In progress is always open); the
  // agents waiting and the done items cannot. What is picked counts and is acted on only while the list shows it.
  const picking = picked !== null;
  // What each manager's sections show (#2059): their groups, titled when there are two or more (or agents above the
  // items), and Done with the hub's history, by when it was done.
  const now = useMemo(() => Date.now(), [items, history]);
  const order = ["lark", ...chips.map((a) => `mail:${a.id}`), ...connectors.map((c) => `c:${c.id}`)];
  const mailName = (account: string) => chips.find((a) => a.id === account)?.email ?? account;
  const groupOpen = (group: string, section: string, key: string) => folds[`${group}|${section}|${key}`] ?? openAtFirst(section, key);
  const filtered = Boolean(source || kind || query || folder);
  // Done's groups (#2969). With the hub's day counts (#2967) every group they name is listed with its count, loaded or not,
  // and loads its own items when it opens; filtered or searched, the counts are of what is loaded ("n+" until the group
  // is complete). Without them, as before: what the history has loaded, "n+" on the last group while there is more.
  const doneGroupsOf = (group: string, done: InboxItem[]): { groups: DoneGroup[]; next?: DoneGroup } => {
    const loaded = doneGroups(done, now);
    const days = doneDays[group];
    if (!days) return { groups: loaded.map((g, i) => ({ ...g, count: g.items.length, plus: i === loaded.length - 1 && historyMore.includes(group), more: false, range: timeRange(g.key, now) })) };
    const counts = dayGroupCounts(days, now, filtered ? 0 : loaded.find((g) => g.key === "just")?.items.length ?? 0);
    const byKey = new Map(loaded.map((g) => [g.key, g.items]));
    // complete: a page of its own reached its start, or the list holds it (the done items of its last days, the hub's
    // inboxKeepDone), or (unfiltered) as many are loaded as the hub counts
    const complete = (key: string, loadedCount: number) => groupComplete.includes(`${group}|${key}`) || timeRange(key, now).start >= now - LIST_DONE_MS
      || (!filtered && loadedCount >= (counts.get(key) ?? 0));
    const groupOf = (key: string): DoneGroup => {
      const items = byKey.get(key) ?? [];
      const done = complete(key, items.length);
      return { key, title: timeTitle(key), items, count: filtered ? items.length : Math.max(items.length, counts.get(key) ?? 0), plus: filtered && !done, more: !done, range: timeRange(key, now) };
    };
    // filtered, only the groups with items that match show; "Show older" under Done loads the newest one the hub counts
    // that hasn't all come
    const pending = filtered ? sortTimeKeys(counts.keys(), now).find((key) => !complete(key, 0)) : undefined;
    return { groups: sortTimeKeys(filtered ? byKey.keys() : [...byKey.keys(), ...counts.keys()], now).map(groupOf), next: pending ? groupOf(pending) : undefined };
  };
  const layout = (byManager ?? [{ manager: null, sec: s, ags: agents }]).map(({ manager, sec, ags }) => {
    const tiers = TIERS.map((tier) => {
      const list = sec.tiers[tier.key];
      const waiting = tier.key === "action" ? ags : [];
      const groups: InboxGroup[] = tier.key === "working" ? [] : grouping === "time" ? groupByTime(list, now, (it) => it.updated_at) : groupBySource(list, order, mailName);
      return { tier, list, waiting, groups, titled: tier.key !== "working" && (groups.length > 1 || (waiting.length > 0 && list.length > 0)) };
    });
    const done = doneWithHistory(sec.done, scoped(history, mine), { source, kind, query, folder });
    const group = manager?.id ?? "";
    const { groups: dg, next } = doneGroupsOf(group, done);
    return { manager, group, tiers, done, doneGroups: dg, doneNext: next };
  });
  // the open items in sight: in sections and groups shown open (In progress is always open)
  const shownOpen = kind === "agent" ? [] : layout.flatMap(({ group, tiers }) => tiers.filter(({ tier }) => tier.key === "working" || isOpen(group, tier.key))
    .flatMap(({ tier, list, groups, titled }) => (titled ? groups.filter((g) => groupOpen(group, tier.key, g.key)).flatMap((g) => g.items) : list)));
  const chosen = picking ? openInView.filter((it) => picked.has(itemKey(it))) : [];
  const shownKeys = new Set(shownOpen.map(itemKey));
  const chosenFolded = chosen.filter((it) => !shownKeys.has(itemKey(it))).length;
  // a group's check: picks all of it, folded or not, or unpicks all of it when all are picked
  const pickGroup = (list: InboxItem[]) => onPicked?.((p) => {
    if (!p) return p;
    const n = new Set(p);
    const all = list.every((it) => n.has(itemKey(it)));
    for (const it of list) {
      if (all) n.delete(itemKey(it));
      else n.add(itemKey(it));
    }
    return n;
  });
  const historyManagers = byManager ? byManager.map((g) => g.manager.id) : [undefined];
  // Done's history from the hub (the list holds only the last few days): each manager's day counts once the list is in
  // (#2967), so Done lists every day's group and count before it opens; a manager without them gets a first page of its
  // history instead, and more on "Show older", as before
  useEffect(() => {
    if (!ready) return;
    void loadDoneDays(historyManagers).then((without) => {
      if (without.length && !history.length) void loadOlderDone(without);
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ready, mine.manager?.id]);
  // A Done group shown open whose items haven't all come loads its first page by itself, once while the list is up;
  // the next pages come on its "Show older", so opening 1+ month doesn't stream in every item of it
  const autoLoaded = useRef(new Set<string>());
  useEffect(() => {
    for (const { manager, group, doneGroups: dg } of layout) {
      if (!doneDays[group] || !isOpen(group, "done")) continue;
      for (const g of dg) {
        const id = `${group}|${g.key}`;
        if (!g.more || !groupOpen(group, "done", g.key) || autoLoaded.current.has(id)) continue;
        autoLoaded.current.add(id);
        void loadDoneGroup(manager?.id, g.key, g.range);
      }
    }
  });
  const jumpTo = (group: string, key: string) => {
    if (!isOpen(group, "done")) toggle(group, "done");
    putFold(`${group}|done|${key}`, true);
    requestAnimationFrame(() => scrollListTo(document.querySelector(`[data-group="${CSS.escape(`${group}|done|${key}`)}"]`)));
  };
  const allChosen = shownOpen.length > 0 && shownOpen.every((it) => picked?.has(itemKey(it)));
  const pick = useCallback((it: InboxItem) => onPicked?.((p) => {
    if (!p) return p;
    const n = new Set(p);
    if (n.has(itemKey(it))) n.delete(itemKey(it));
    else n.add(itemKey(it));
    return n;
  }), [onPicked]);
  // Select all adds what is in sight to what is picked (a folded group picked whole stays picked); Deselect all clears all
  const pickAll = () => onPicked?.((p) => (allChosen ? new Set() : new Set([...(p ?? []), ...shownOpen.map(itemKey)])));
  // Mark as done: one bulk request per manager, then the toast with Undo (setAllDone); selecting ends.
  const markChosenDone = () => {
    void setAllDone(chosen, "done");
    onPicked?.(null);
  };
  // Clean up all files them as Dismiss (dismiss): the user set them aside, they did not handle them.
  const runBulk = () => {
    if (!openInView.length) return;
    if (openInView.length >= BULK_CONFIRM_AT) setConfirm({ items: openInView, scope: bulkScopeName(filterNames, query) });
    else void setAllDone(openInView, "dismiss");
  };
  const toggle = (group: string, k: string) => setToggled((o) => {
    const n = new Set(o);
    const at = `${group}:${k}`;
    if (n.has(at)) n.delete(at);
    else n.add(at);
    return n;
  });
  // Agents have no source: picking one leaves the Agent filter, and the Agent filter leaves the source.
  const pickSource = (v: string) => {
    setSource(source === v ? "" : v);
    if (kind === "agent") setKind("");
  };
  const pickKind = (v: string) => {
    setKind(kind === v ? "" : v);
    if (v === "agent") setSource("");
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col">
    {/* `overflow-y-auto` alone makes this a scroll container on both axes, so a row or a long chip (a connector's name)
        wider than the 380px column let the whole list scroll sideways and cut off every card (#3262); `overflow-x-clip`
        clips that overflow instead, and is not a scroll container, so `scrollIntoView` above cannot shift it either. */}
    <div className="min-h-0 flex-1 overflow-y-auto overflow-x-clip" data-picking={picking || undefined}>
      <div className="mx-auto max-w-[760px] px-3 pt-3 pb-16 sm:px-4">
        <label className="flex h-8 items-center gap-2 rounded-control bg-surface px-2.5 shadow-btn focus-within:shadow-[inset_0_0_0_1px_var(--accent)]">
          <Icon name="search" size={13} stroke={2} className="shrink-0 text-ink-3" />
          <input value={query} onChange={(e) => setQuery(e.target.value)} placeholder={t("inbox-inboxlist.search_placeholder")} aria-label={t("inbox-inboxlist.search_label")} className="min-w-0 flex-1 bg-transparent text-[12.5px] text-ink outline-none placeholder:text-ink-3" />
          {query && <IconButton icon="x" label={t("inbox-inboxlist.clear")} size={20} iconSize={11} onClick={() => setQuery("")} />}
        </label>
        {/* A phone: the chips in one row that scrolls sideways, Clean up all in the ⋯ menu at its end. */}
        <div className="mt-2.5 flex items-center gap-1.5">
        <div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5 max-sm:-my-1 max-sm:flex-nowrap max-sm:overflow-x-auto max-sm:py-1 max-sm:scrollbar-none max-sm:[&>*]:shrink-0" role="toolbar" aria-label={t("inbox-inboxlist.filters")}>
          <Chip on={!source && !kind} onClick={() => { setSource(""); setKind(""); }}>{t("inbox-inboxlist.all")}</Chip>
          <Chip on={source === "feishu"} onClick={() => pickSource("feishu")}>{t("inbox-inboxlist.source_lark")}</Chip>
          {chips.map((a) => (
            <Chip key={a.id} on={source === a.id} onClick={() => pickSource(a.id)} title={a.email}>{mailShort(a.email)}</Chip>
          ))}
          {connectors.map((c) => (
            <Chip key={`c:${c.id}`} on={source === c.id} onClick={() => pickSource(c.id)}>{c.name}</Chip>
          ))}
          <span aria-hidden className="mx-0.5 h-4 w-px bg-line-strong" />
          {KIND_FILTERS.map((k) => (
            <Chip key={k.key} on={kind === k.key} onClick={() => pickKind(k.key)}>{k.label}</Chip>
          ))}
          <button
            type="button"
            disabled={!openInView.length || picking}
            title={t("inbox-inboxlist.clean_all_scope", { scope })}
            onClick={runBulk}
            className="ml-auto inline-flex h-7 items-center gap-1.5 rounded-control bg-surface px-2.5 text-[12px] whitespace-nowrap text-ink-2 shadow-btn transition-colors duration-150 hover:bg-hover hover:text-ink disabled:pointer-events-none disabled:opacity-45 max-sm:hidden"
          >
            <Icon name="check" size={12} stroke={2.6} />
            {t("inbox-inboxlist.clean_all")}
            <span className="text-ink-3">{scope}</span>
          </button>
        </div>
        <IconButton icon="more" label={t("inbox-inboxlist.more")} onClick={more.open} active={Boolean(more.anchor)} />
        </div>
        <div role="radiogroup" aria-label={t("inbox-inboxlist.grouping")} className="mt-2.5 flex w-fit gap-0.5 rounded-full bg-inset p-0.5 shadow-hairline">
          {(["source", "time"] as const).map((g) => (
            <button key={g} type="button" role="radio" aria-checked={grouping === g} onClick={() => setGrouping(g === "source" ? "" : g)}
              className={cx("touch-hit relative z-10 flex h-6 items-center justify-center whitespace-nowrap rounded-full px-2.5 text-[11.5px] transition-[background-color,color,box-shadow] duration-150",
                grouping === g ? "bg-surface font-medium text-ink shadow-btn" : "text-ink-2 hover:bg-hover hover:text-ink")}>
              {g === "source" ? t("inbox-inboxlist.by_source") : t("inbox-inboxlist.by_time")}
            </button>
          ))}
        </div>
        {more.anchor && (
          <Menu anchor={more.anchor} onClose={more.close} align="right" width={240}
            items={[
              { key: "clean-all", label: t("inbox-inboxlist.clean_all"), hint: scope, icon: "check", disabled: !openInView.length, onSelect: runBulk },
              { key: "muted", label: t("inbox-inboxlist.muted_list"), icon: "bellOff", divider: true, onSelect: () => setMutesOpen(true) },
              { key: "rules", label: t("inbox-inboxlist.rules_list"), icon: "target", onSelect: () => setRulesOpen(true) },
              { key: "folders", label: t("inbox-folders.settings_menu"), icon: "folder", onSelect: () => setFoldersOpen(true) },
              // source subscriptions (#2041): every subscription, with the Inbox's other settings
              ...(subscriptionsShown ? [{ key: "subscriptions", label: t("inbox-inboxlist.subscriptions"), icon: "loop" as const, onSelect: () => navigate(subscriptionsHref()) }] : []),
            ]} />
        )}
        {mutesOpen && <MutesDialog onClose={() => setMutesOpen(false)} />}
        {rulesOpen && <RulesDialog onClose={() => setRulesOpen(false)} />}
        {foldersOpen && <FolderSettingsDialog onClose={() => setFoldersOpen(false)} />}
        {foldersShown && <FolderBar items={inFilters} folder={folder} onPick={setFolder} />}
        <BulkConfirm confirm={confirm} onClose={() => setConfirm(null)} />

        <TriageBanner triage={triage} />
        <UnreadNotice triage={triage} />
        {/* never loaded (an error and no items): its own empty state with "Retry", not "All caught up" under the error (#2970);
            with agents waiting the list is not empty, so the reason stays a line over it */}
        {ready && failed && !empty && <p className="mt-4 text-[12.5px] text-red">{error}</p>}
        {!ready && <div className="mt-6 px-1"><Loading /></div>}
        {ready && failed && empty && (
          <div className="mt-10 flex flex-col items-center gap-2 text-center" role="status" data-inbox-failed>
            <span className="flex size-10 items-center justify-center rounded-full bg-field text-ink-3">
              <Icon name="inbox" size={17} />
            </span>
            <p className="text-[13.5px] font-medium text-ink">{t("inbox-inboxlist.failed_title")}</p>
            <p className="max-w-80 text-[12.5px] text-ink-3">{t("inbox-inboxlist.failed_hint")}</p>
            <p className="max-w-80 text-[11.5px] break-words text-ink-3">{error}</p>
            <Button tone="secondary" icon="retry" busy={loading} onClick={() => void reloadInbox()} className="mt-2">
              {loading ? t("inbox-inboxlist.retrying") : t("inbox-inboxlist.retry")}
            </Button>
          </div>
        )}
        {away && offline && <OfflineState offline={offline} className="mt-10" />}
        {ready && empty && !failed && !away && unconnected && (
          <div className="mt-10 flex flex-col items-center gap-2 text-center" data-inbox-no-sources>
            <span className="flex size-10 items-center justify-center rounded-full bg-field text-ink-3">
              <Icon name="inbox" size={17} />
            </span>
            <p className="text-[13.5px] font-medium text-ink">{t("inbox-inboxlist.no_sources_title")}</p>
            <p className="max-w-80 text-[12.5px] leading-relaxed text-ink-3">{t("inbox-inboxlist.no_sources_hint")}</p>
            <div className="mt-2 flex flex-wrap justify-center gap-1.5">
              {unconnected.offers.map((o, i) => (
                <Button key={o.id} tone={i === 0 ? "primary" : "secondary"} icon={i === 0 ? "plus" : undefined} onClick={() => navigate(o.href)}>
                  {t("inbox-inboxlist.no_sources_connect", { name: o.name })}
                </Button>
              ))}
            </div>
          </div>
        )}
        {ready && empty && !failed && !away && !unconnected && (
          <div className="mt-10 flex flex-col items-center gap-2 text-center">
            <span className="flex size-10 items-center justify-center rounded-full bg-green-tint text-green">
              <Icon name="check" size={17} stroke={2.6} />
            </span>
            <p className="text-[13.5px] font-medium text-ink">{query ? t("inbox-inboxlist.empty_no_match") : t("inbox-inboxlist.empty_all_done")}</p>
            <p className="text-[12.5px] text-ink-3">{query ? t("inbox-inboxlist.empty_no_match_hint") : t("inbox-inboxlist.empty_all_done_hint")}</p>
          </div>
        )}

        {layout.map(({ manager, group, tiers, done, doneGroups: dg, doneNext: nextGroup }) => {
          const exact = Boolean(doneDays[group]);
          const more = exact ? Boolean(nextGroup) || dg.some((g) => g.plus) : historyMore.includes(group);
          const doneCount = exact ? dg.reduce((n, g) => n + g.count, 0) : done.length;
          const moreError = historyError[group] ?? "";
          const isSelected = (it: InboxItem) => !!selected && sameItem(it, selected);
          const row = (it: InboxItem, off = false) => (
            <ItemRow key={itemKey(it)} it={it} selected={isSelected(it)} showAccount={chips.length > 1} folder={foldersShown ? folder : undefined} onEdit={setEditing} pick={picking && !off ? pick : undefined} picked={picked?.has(itemKey(it)) ?? false} off={off && picking} />
          );
          const groupOf = (section: string, g: { key: string; title: string; items: InboxItem[] }, body: React.ReactNode, opts: { more?: boolean; pickable?: boolean; count?: number } = {}) => {
            const id = `${group}|${section}|${g.key}`;
            const n = picking && opts.pickable ? g.items.filter((it) => picked.has(itemKey(it))).length : 0;
            return (
              <Group key={id} id={id} title={g.title} count={opts.count ?? g.items.length} more={opts.more} open={groupOpen(group, section, g.key)} onToggle={(open) => putFold(id, open)}
                pick={picking && opts.pickable ? { state: n === 0 ? "none" : n === g.items.length ? "all" : "some", picked: n, run: () => pickGroup(g.items) } : undefined}>
                {body}
              </Group>
            );
          };
          return (
            <Fragment key={group}>
              {ready && tiers.map(({ tier, list, waiting, groups, titled }) => {
                if (!list.length && !waiting.length) return null;
                const clear = tier.key === "fyi" ? t("inbox-inboxlist.clear_fyi") : tier.key === "noise" ? t("inbox-inboxlist.clear_noise") : undefined;
                const agentRows = waiting.map((ix) => <AgentRow key={ix.id} ix={ix} off={picking} />);
                return (
                  <Section key={tier.key} k={tier.key} title={tier.title} count={sectionCount(list.length, waiting.length)} open={tier.key === "working" || isOpen(group, tier.key)} onToggle={tier.key === "working" ? undefined : () => toggle(group, tier.key)}
                    action={clear && list.length && !picking ? { label: clear, run: () => setAllDone(list, true) } : undefined}>
                    {titled ? (
                      <>
                        {waiting.length > 0 && groupOf(tier.key, { key: "agents", title: t("lib-inbox.group_agents"), items: [] }, agentRows, { count: waiting.length })}
                        {groups.map((g) => groupOf(tier.key, g, g.items.map((it) => row(it)), { pickable: true }))}
                      </>
                    ) : (
                      <>
                        {agentRows}
                        {list.map((it) => row(it))}
                      </>
                    )}
                  </Section>
                );
              })}
              {ready && doneCount > 0 && (
                <Section k="done" title={t("inbox-inboxlist.done")} count={more ? { text: `${doneCount}+`, label: `${doneCount}+` } : sectionCount(doneCount, 0)} open={isOpen(group, "done")} onToggle={() => toggle(group, "done")}
                  menu={dg.length > 1 ? [
                    ...dg.map((g) => ({ key: `jump:${g.key}`, label: g.title, hint: g.plus ? `${g.count}+` : String(g.count), onSelect: () => jumpTo(group, g.key) })),
                    { key: "fold-all", label: t("inbox-inboxlist.fold_all"), divider: true, onSelect: () => dg.forEach((g) => putFold(`${group}|done|${g.key}`, false)) },
                    { key: "unfold-all", label: t("inbox-inboxlist.unfold_all"), onSelect: () => dg.forEach((g) => putFold(`${group}|done|${g.key}`, true)) },
                  ] : undefined}>
                  {dg.length > 1 ? dg.map((g) => {
                    const id = `${group}|${g.key}`;
                    return groupOf("done", g, (
                      <>
                        {g.items.map((it) => row(it, true))}
                        {exact && g.more && !filtered && <OlderButton busy={groupLoading.includes(id)} error={moreError} run={() => void loadDoneGroup(manager?.id, g.key, g.range)} />}
                      </>
                    ), { more: g.plus, count: g.count });
                  }) : done.map((it) => row(it, true))}
                  {!exact && more && history.length > 0 && <OlderButton busy={historyLoading} error={moreError} run={() => void loadOlderDone(historyManagers)} />}
                  {nextGroup && <OlderButton busy={groupLoading.includes(`${group}|${nextGroup.key}`)} error={moreError} run={() => void loadDoneGroup(manager?.id, nextGroup.key, nextGroup.range)} />}
                </Section>
              )}
            </Fragment>
          );
        })}
        {loading && loaded && <span className="sr-only">{t("inbox-inboxlist.refreshing")}</span>}
      </div>
      {editing && <DraftDialog item={editing} onClose={() => setEditing(null)} />}
    </div>
    {picking && (
      <div role="toolbar" aria-label={t("inbox-inboxlist.selection")} data-pick-bar className="shrink-0 border-t border-line bg-inset px-3 py-2.5 sm:px-4 max-sm:pb-[calc(10px+env(safe-area-inset-bottom))]">
        <div className="mx-auto flex max-w-[760px] items-center gap-2">
          <Button tone="quiet" onClick={pickAll} disabled={!shownOpen.length}>{allChosen ? t("inbox-inboxlist.deselect_all") : t("inbox-inboxlist.select_all")}</Button>
          <span className="min-w-0 flex-1 truncate text-[12px] text-ink-3 tabular-nums" aria-live="polite">{chosenFolded ? t("inbox-inboxlist.picked_folded", { count: chosen.length, folded: chosenFolded }) : t("inbox-inboxlist.selected_count", { count: chosen.length })}</span>
          <Button tone="primary" icon="check" disabled={!chosen.length} onClick={markChosenDone}>{t("inbox-inboxlist.mark_done_title")}</Button>
        </div>
      </div>
    )}
    </div>
  );
}

/**
 * A section title's count, saying what it counts (#3288): the sidebar's "Inbox" badge counts messages only, while "Needs you"
 * also lists the agents waiting on you (those are "Overview"'s count), so the two are written apart: "4 messages · 7 agents", each
 * with its unit, which a phone has no hover to show (#4146).
 */
export function sectionCount(items: number, agents: number): { text: string; label: string } {
  const parts = [items > 0 && t("inbox-inboxlist.count_items", { count: items }), agents > 0 && t("inbox-inboxlist.count_agents", { count: agents })].filter((x): x is string => Boolean(x));
  return {
    text: items > 0 && agents > 0 ? `${t("inbox-inboxlist.count_items_short", { count: items })} · ${t("inbox-inboxlist.count_agents", { count: agents })}` : agents > 0 ? t("inbox-inboxlist.count_agents", { count: agents }) : String(items),
    label: parts.join(t("inbox-inboxlist.count_separator")),
  };
}

/** A section of the list; without `onToggle` it is always open (In progress: what the AI is on never hides). */
function Section({ k, title, count, open, onToggle, action, menu, children }: { k: SectionKey | "done"; title: string; count: { text: string; label: string }; open: boolean; onToggle?: () => void; action?: { label: string; run: () => void }; /** Done's "Jump to": its groups with their counts. */ menu?: MenuItem[]; children: React.ReactNode }) {
  const jump = useMenu();
  const head = (
    <>
      <span className={cx("text-[12px] font-medium whitespace-nowrap", k === "action" ? "text-ink" : "text-ink-3")} data-section-title>{title}</span>
      <span title={count.label} className="text-[11.5px] whitespace-nowrap text-ink-3 tabular-nums"><span aria-hidden>{count.text}</span><span className="sr-only">{count.label}</span></span>
    </>
  );
  return (
    <section className="mt-4" data-section={k}>
      <div className="flex items-center gap-2 px-1 pb-1">
        {onToggle ? (
          <button type="button" onClick={onToggle} aria-expanded={open} className="flex min-w-0 flex-1 items-center gap-1.5 text-left">
            {head}
            <Icon name="chevronRight" size={11} stroke={2.4} className="text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : undefined }} />
          </button>
        ) : (
          <h3 className="flex min-w-0 flex-1 items-center gap-1.5">{head}</h3>
        )}
        {action && (
          <button type="button" onClick={action.run} className="h-6 rounded-[6px] px-1.5 text-[11.5px] whitespace-nowrap text-ink-3 transition-colors hover:bg-hover hover:text-ink">
            {action.label}
          </button>
        )}
        {menu && (
          <button type="button" onClick={jump.open} aria-haspopup="menu" aria-expanded={Boolean(jump.anchor)} className="inline-flex h-6 items-center gap-0.5 rounded-[6px] px-1.5 text-[11.5px] whitespace-nowrap text-ink-3 transition-colors hover:bg-hover hover:text-ink">
            {t("inbox-inboxlist.jump_to")}
            <Icon name="chevronDown" size={10} stroke={2.4} />
          </button>
        )}
        {menu && jump.anchor && <Menu anchor={jump.anchor} onClose={jump.close} align="right" width={220} items={menu} />}
      </div>
      {/* the clip pulled out by 2px with 2px of padding inside: the cards stay put and the selected one's 1.5px ring
          (and the cards' shadow) is not cut at the sides; the top 2px stays while closed, so the header keeps its 6px */}
      <Collapse open={open} unclipped className="-mx-0.5" clipClassName="px-0.5 pt-0.5">
        <div inert={!open} className="flex flex-col gap-1.5 pb-0.5">{children}</div>
      </Collapse>
    </section>
  );
}

/**
 * Scrolls the list's own scroller (the nearest box that scrolls) so `el` is at its top. Not scrollIntoView: that scrolls
 * every box above it too, the app shell included, which hides its overflow but still scrolls from code; Jump To then
 * lifted the whole app and left a blank band under it (#2969).
 */
function scrollListTo(el: Element | null) {
  let box = el?.parentElement;
  while (box && !/auto|scroll/.test(getComputedStyle(box).overflowY)) box = box.parentElement;
  if (!el || !box) return;
  box.scrollTo?.({ top: box.scrollTop + el.getBoundingClientRect().top - box.getBoundingClientRect().top, behavior: scrollBehavior() });
}

/** How long the list keeps done items (the hub's inboxKeepDone): Done's groups within it are complete without a page. */
const LIST_DONE_MS = 3 * 86_400_000;

/** A group of Done (#2969): its items loaded so far, its count (the hub's when it has day counts), whether the count
 *  reads "n+" (plus), whether more of its items can load (more) and its time range (where its pages start). */
type DoneGroup = InboxGroup & { count: number; plus: boolean; more: boolean; range: { start: number; end: number } };

/** "Show older" under Done or in one of its groups: one more page, with why the last one failed. */
function OlderButton({ busy, error, run }: { busy: boolean; error: string; run: () => void }) {
  return (
    <div className="flex items-center gap-2 px-1 pt-1">
      <Button tone="quiet" busy={busy} onClick={run}>{busy ? t("inbox-inboxlist.loading_older") : t("inbox-inboxlist.load_older")}</Button>
      {error && <span className="min-w-0 truncate text-[11.5px] text-red" title={error}>{t("inbox-inboxlist.older_failed", { error })}</span>}
    </div>
  );
}

/** A group's tick while selecting: none, some ("–") or all of it picked. */
function GroupTick({ state }: { state: "none" | "some" | "all" }) {
  return (
    <span aria-hidden className={cx("flex size-4 shrink-0 items-center justify-center rounded-chip text-white transition-colors duration-150", state === "none" ? "shadow-[inset_0_0_0_1.5px_var(--line-strong)]" : "bg-accent")}>
      {state === "all" && <Icon name="check" size={10} stroke={3} />}
      {state === "some" && <span className="h-[2px] w-2 rounded-full bg-white" />}
    </span>
  );
}

/**
 * A group inside a section (#2059): its title sticks to the top while its rows scroll by, and folds it to the title and
 * count. Selecting, its tick picks or unpicks all of it, folded or not; "n selected" says what is picked in it.
 */
function Group({ id, title, count, more, open, onToggle, pick, children }: { id: string; title: string; count: number; more?: boolean; open: boolean; onToggle: (open: boolean) => void; pick?: { state: "none" | "some" | "all"; picked: number; run: () => void }; children: React.ReactNode }) {
  return (
    <div data-group={id}>
      <div className="sticky top-0 z-10 -mx-1 flex items-center gap-1.5 bg-canvas/95 px-2 pt-2 pb-0.5 backdrop-blur-sm">
        {pick && (
          <button type="button" role="checkbox" aria-checked={pick.state === "all" ? true : pick.state === "some" ? "mixed" : false} aria-label={t("inbox-inboxlist.pick_group", { name: title })} onClick={pick.run} className="touch-hit flex shrink-0 items-center">
            <GroupTick state={pick.state} />
          </button>
        )}
        {/* under the section's title (12px, medium), not as big (#2969) */}
        <button type="button" onClick={() => onToggle(!open)} aria-expanded={open} className="flex min-w-0 flex-1 items-center gap-1.5 text-left text-[11px] text-ink-2">
          <Icon name="chevronRight" size={10} stroke={2.4} className="shrink-0 text-ink-3 transition-transform duration-200" style={{ transform: open ? "rotate(90deg)" : undefined }} />
          <span className="truncate">{title}</span>
          <span className="shrink-0 text-ink-3 tabular-nums">{more ? `${count}+` : count}</span>
        </button>
        {pick && pick.picked > 0 && <span className="shrink-0 text-[11px] text-accent-ink tabular-nums">{t("inbox-inboxlist.group_picked", { count: pick.picked })}</span>}
      </div>
      {/* the same 2px of room as the section's (#2703), so a selected card's ring isn't cut inside a group; the header's
          bottom padding gives up those 2px to keep its 4px */}
      <Collapse open={open} className="-mx-0.5" clipClassName="px-0.5 pt-0.5">
        <div inert={!open} className="flex flex-col gap-1.5 pb-1">{children}</div>
      </Collapse>
    </div>
  );
}

/** Room for the row's "Done" check, always shown on a touch screen (#4141), right of the time. */
const TOUCH_DONE_ROOM = "[@media(hover:none)]:mr-8";

/** The row's "Done" / "Move back": on a touch screen marking done says so with "Undo", since a tap there can be a slip. */
export async function markFromRow(it: InboxItem, done: boolean, touch = isNoHover()): Promise<void> {
  // after the request: "Undo" cannot race it, and a failure shows its own error instead of this
  const ok = await setDone(it, done);
  if (ok && done && touch) toast(t("inbox-inboxlist.marked_done"), "ok", { label: t("lib-inbox.undo"), run: () => void setDone(it, false) });
}

/**
 * Memoized: a push that leaves this item's object as it was (or one for sessions and machines) skips the row. Selecting
 * (#2059): with `pick` the row is a checkbox that picks it instead of opening it; `off`: it cannot be picked (a done item).
 */
const ItemRow = memo(function ItemRow({ it, selected, showAccount, folder, onEdit, pick, picked = false, off = false }: { it: InboxItem; selected: boolean; showAccount: boolean; /** the folder picked; undefined: folders are not shown */ folder?: string; onEdit: (it: InboxItem) => void; pick?: (it: InboxItem) => void; picked?: boolean; off?: boolean }) {
  const to = itemRoute(it);
  const done = isDone(it);
  const label = kindLabel(it);
  // a suggestion takes the AI twin's draft's place, and the event's handling the AI twin's "working on it"
  const draft = !done && !it.suggestion && agentDraft(it);
  const working = !done && !it.handling && agentWorking(it);
  const mark = done ? (it.done_by === "manager" ? <EventMark it={it} /> : null) : it.suggestion || it.handling ? <EventMark it={it} /> : null;
  const rows = !done ? it.doc?.rows ?? [] : [];
  const first = rows[0];
  const translated = hubTranslation(it, useTranslatePrefs());
  const from = fromLine(it, translated?.preview);
  // a right click: "Done" / "Move back", then what it can be muted by (iOS: the row's long press)
  // (the menu key / Shift+F10 open it next to the row, see ui/context-menu.ts)
  const [menu, setMenu] = useState<MenuAt | null>(null);
  const picking = Boolean(pick) || off;
  const head = (
    <>
        {pick && <CheckMark on={picked} className="mt-3" />}
        <ItemAvatar it={it} />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-1.5">
            <span className={cx("truncate text-[13.5px]", done ? "text-ink-2" : "font-semibold text-ink")}>{translated?.subject || it.title}</span>
            {translated && <span role="img" aria-label={t("inbox-inboxlist.translated")} title={t("inbox-inboxlist.translated")} className="shrink-0 text-ink-3"><Icon name="translate" size={12} /></span>}
            {label && <Tag urgent={labelIsUrgent(it)}>{label}</Tag>}
            <span className="flex-1" />
            <span className={cx("shrink-0 text-[11.5px] text-ink-3 tabular-nums group-focus-within:invisible group-hover:invisible", TOUCH_DONE_ROOM)}>{shortTime(it.updated_at)}</span>
          </span>
          {/* on a touch screen the check's 44 pt tap area reaches this line: keep the summary and count clear of it */}
          <span className="mt-0.5 flex items-start gap-2 [@media(hover:none)]:pr-8">
            <span className="min-w-0 flex-1">
              {it.summary ? (
                <>
                  <span className={cx("line-clamp-2 text-[12.5px] leading-[1.5] break-words", done ? "text-ink-2" : "text-ink")}><Spark />{it.summary}</span>
                  <span className="mt-0.5 block truncate text-[11.5px] text-ink-2">{from}</span>
                </>
              ) : (
                <span className="line-clamp-2 text-[12.5px] leading-[1.5] break-words text-ink-2">{from}</span>
              )}
              {showAccount && !isLark(it) && !it.connector && <span className="mt-0.5 block truncate text-[11px] text-ink-3">{it.account_name}</span>}
              {folder !== undefined && <FolderLine it={it} folder={folder} />}
            </span>
            {it.count > 1 && !done && <Count n={it.count} accent={it.kind === "mention"} />}
          </span>
        </span>
    </>
  );
  return (
    // selecting: a click anywhere on the card picks it (its draft and the rest under it are inert, so clicks there land here)
    <div data-item={it.id} data-manager={it.manager} data-picked={picked || undefined} onClick={pick ? () => pick(it) : undefined} {...(picking ? {} : contextMenuProps((at) => setMenu(at)))}
      className={cx("group relative scroll-my-3 rounded-[14px] transition-[background-color,box-shadow,opacity] duration-150", !off && "hover:bg-inset", off && "opacity-45",
        picked ? "bg-accent-tint shadow-[0_0_0_1.5px_var(--accent)]" : selected && !picking ? "bg-inset shadow-[0_0_0_1.5px_var(--accent)]" : "bg-surface shadow-card")}>
      {pick ? (
        <button type="button" role="checkbox" aria-checked={picked} className="flex w-full gap-3 rounded-[14px] p-3 text-left outline-offset-[-2px]">{head}</button>
      ) : off ? (
        <div inert className="flex gap-3 rounded-[14px] p-3">{head}</div>
      ) : (
        <a href={href(to)} onClick={(e) => onLinkClick(e, to, openedItemState())} aria-current={selected ? "page" : undefined} className="flex gap-3 rounded-[14px] p-3 outline-offset-[-2px]">{head}</a>
      )}
      {(first || draft || working || mark) && (
        <div inert={picking} className={cx("-mt-1 flex flex-col gap-2 pr-3 pb-3", pick ? "pl-[92px]" : "pl-[64px]")}>
          {first && (
            <div className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-ink-2">
              <Glyph name="reply" size={12} className="shrink-0 -scale-y-100 text-ink-3" />
              <span className="min-w-0 truncate">{first.title}</span>
              {first.status?.[0] && <StatusMenu it={it} row={first} status={first.status[0]} />}
              {rows.length > 1 && <span className="shrink-0 text-ink-3">{t("inbox-inboxlist.more_rows", { count: rows.length - 1 })}</span>}
            </div>
          )}
          {draft && <DraftCard it={it} onEdit={() => onEdit(it)} onSend={(text, changes) => void sendReply(it, text, changes)} />}
          {working && <AgentWorking it={it} />}
          {mark}
        </div>
      )}
      {/* Shown on hover with a mouse; on a touch screen (no hover, #4141) always there as a check beside the time, so an
          item is marked without opening it, with "Undo" in the toast in case the tap was a slip. */}
      {!picking && <button
        type="button"
        onClick={() => void markFromRow(it, !done)}
        aria-label={done ? t("inbox-inboxlist.move_back_title") : t("inbox-inboxlist.mark_done_title")}
        className="touch-hit absolute top-2.5 right-2.5 flex h-6 items-center gap-1 rounded-[7px] bg-surface px-2 text-[11.5px] font-medium whitespace-nowrap text-ink-2 opacity-0 shadow-btn transition-opacity duration-150 group-focus-within:opacity-100 group-hover:opacity-100 hover:text-ink focus-visible:opacity-100 [@media(hover:none)]:w-6 [@media(hover:none)]:justify-center [@media(hover:none)]:px-0 [@media(hover:none)]:opacity-100"
        title={done ? t("inbox-inboxlist.move_back_title") : t("inbox-inboxlist.mark_done_title")}
      >
        {done ? <Glyph name="undo" size={12} stroke={2.2} /> : <Icon name="check" size={12} stroke={2.6} />}
        <span className="[@media(hover:none)]:hidden">{done ? t("inbox-inboxlist.move_back") : t("inbox-inboxlist.done")}</span>
      </button>}
      {menu && <Menu anchor={menu.anchor ?? null} point={menu.point} onClose={() => setMenu(null)} width={224} items={rowMenu(it, done, folder !== undefined)} />}
    </div>
  );
});

/** A row's menu: "Done" (or "Move back"), then "Mute this repository / chat / sender" with the name under it (iOS InboxView.rowMenu),
 *  and "Move to folder…" when the list shows folders. */
export function rowMenu(it: InboxItem, done: boolean, folders = false): MenuItem[] {
  const items: MenuItem[] = [
    done
      ? { key: "undone", label: t("inbox-inboxlist.move_back"), icon: "inbox", onSelect: () => void setDone(it, false) }
      : { key: "done", label: t("inbox-inboxlist.done"), icon: "check", onSelect: () => void markFromRow(it, true, true) }, // a menu pick: "Undo" on any screen (#4162)
  ];
  (it.mutes ?? []).forEach((m, i) => items.push({ key: `mute:${m.tag}:${m.value}`, label: muteTitle(m), hint: muteShortName(m), icon: "bellOff", divider: i === 0, onSelect: () => void muteItem(it, m) }));
  // then a rule for each (docs/assistant/inbox.md "Policies"): the tier its items get, what the AI twin does with them
  (it.mutes ?? []).forEach((m, i) => items.push({ key: `policy:${m.tag}:${m.value}`, label: policyMenuTitle(m), hint: muteShortName(m), icon: "target", divider: i === 0, onSelect: () => openPolicyDialog({ item: it, option: m, source: it.source }) }));
  if (folders) items.push({ key: "folder", label: t("inbox-folders.move_menu"), hint: it.folder, icon: "folder", divider: true, onSelect: () => openFolderDialog(it) });
  return items;
}

/** One manager's mutes in the "Muted" dialog (several managers: each keeps its own, read with its name). */
type MuteGroup = { manager: Manager; mutes: InboxMute[] | null; error: string };

/** Each installed manager's mutes, the default first; one that can't answer (offline) says why under its name. */
async function mutesByManager(managers: Manager[]): Promise<MuteGroup[]> {
  const read = await Promise.allSettled(managers.map((m) => listInboxMutes(m.id)));
  return managers.map((m, i) => {
    const r = read[i];
    return r.status === "fulfilled" ? { manager: m, mutes: r.value, error: "" } : { manager: m, mutes: null, error: errorText(r.reason) };
  });
}

/** "Muted" (the ⋯ menu; iOS InboxMutesView): every source's muted repositories, chats and senders, newest first, each
 *  with "Unmute". Only what comes in later comes back. With several assistants, the current one's. */
function MutesDialog({ onClose }: { onClose: () => void }) {
  // several assistants: the current one's mutes, asked of it by name
  const mine = useScope().manager;
  const managers = useMemo(() => (mine ? [mine] : null), [mine]);
  const [list, setList] = useState<InboxMute[] | null>(null);
  const [groups, setGroups] = useState<MuteGroup[] | null>(null);
  const [failed, setFailed] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const several = managers?.map((m) => m.id).join(",") ?? "";
  const load = () => {
    setFailed("");
    if (managers) void mutesByManager(managers).then(setGroups);
    else listInboxMutes().then(setList, (e) => setFailed(errorText(e)));
  };
  useEffect(load, [several]);
  const unmute = async (m: InboxMute) => {
    const key = `${m.manager ?? ""}:${m.id}`;
    setBusy(key);
    try {
      await unmuteInbox(m);
      setList((l) => (l ?? []).filter((x) => x.id !== m.id));
      setGroups((gs) => gs?.map((g) => (g.manager.id === m.manager ? { ...g, mutes: g.mutes?.filter((x) => x.id !== m.id) ?? null } : g)) ?? gs);
      toast(t("inbox-inboxlist.unmuted", { name: m.name }), "ok");
    } catch (e) {
      toast(t("inbox-inboxlist.unmute_failed", { error: errorText(e) }), "error");
    } finally {
      setBusy(null);
    }
  };
  const row = (m: InboxMute) => {
    const key = `${m.manager ?? ""}:${m.id}`;
    return (
      <li key={key} data-mute={m.id} className="flex items-center gap-3 rounded-control px-1 py-2 hover:bg-hover">
        <Icon name="bellOff" size={14} className="shrink-0 text-ink-3" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] text-ink" title={m.name}>{m.name}</span>
          <span className="mt-0.5 block truncate text-[11.5px] text-ink-3">{muteLine(m)}</span>
        </span>
        <Button tone="quiet" busy={busy === key} disabled={busy !== null && busy !== key} onClick={() => void unmute(m)}>
          {t("inbox-inboxlist.unmute")}
        </Button>
      </li>
    );
  };
  // several assistants: the current one's group when it has something to say (mutes, or why they could not be read)
  const shownGroups = groups?.filter((g) => g.error || g.mutes?.length) ?? null;
  const empty = managers ? shownGroups?.length === 0 : list?.length === 0;
  return (
    <Dialog open onClose={onClose} title={t("inbox-inboxlist.muted_list")} width={420} footer={<Button tone="primary" onClick={onClose}>{t("inbox-inboxlist.close")}</Button>}>
      {failed ? (
        <div className="flex flex-col items-start gap-2">
          <p className="text-[12.5px] break-words text-red">{t("inbox-inboxlist.mutes_load_failed", { error: failed })}</p>
          <Button tone="secondary" icon="retry" onClick={load}>{t("inbox-inboxlist.retry")}</Button>
        </div>
      ) : (managers ? !groups : !list) ? (
        <div className="py-2"><Loading /></div>
      ) : empty ? (
        <div className="flex flex-col items-center gap-2 py-4 text-center">
          <span className="flex size-10 items-center justify-center rounded-full bg-field text-ink-3">
            <Icon name="bellOff" size={17} />
          </span>
          <p className="text-[13.5px] font-medium text-ink">{t("inbox-inboxlist.muted_empty_title")}</p>
          <p className="max-w-[300px] text-[12.5px] leading-relaxed text-ink-3">{t("inbox-inboxlist.muted_empty_hint")}</p>
        </div>
      ) : (
        <>
          {shownGroups ? (
            shownGroups.map((g) => (
              <section key={g.manager.id} data-mutes-manager={g.manager.id} className="mb-2">
                {g.error ? (
                  <p className="px-1 py-1.5 text-[12px] break-words text-ink-3">{g.manager.online ? t("inbox-inboxlist.mutes_load_failed", { error: g.error }) : t("inbox-inboxlist.mutes_offline")}</p>
                ) : (
                  <ul className="-mx-1 flex flex-col">{g.mutes!.map(row)}</ul>
                )}
              </section>
            ))
          ) : (
            <ul className="-mx-1 flex flex-col">{list!.map(row)}</ul>
          )}
          <p className="mt-2 text-[11.5px] leading-relaxed text-ink-3">{t("inbox-inboxlist.muted_note")}</p>
        </>
      )}
    </Dialog>
  );
}

const IX_GLYPH: Record<Interaction["kind"], GlyphName | "list" | "terminal"> = { question: "question", plan: "list", permission: "hand", screen: "terminal", elicitation: "sheet" };

/**
 * An agent waiting on you: which agent, what it asks, then what it waits for (amber) and where. Memoized, and it reads
 * from the store only its route and its place, whose fields are strings: a sessions or machines push renders it again
 * only when one of those changed.
 */
const AgentRow = memo(function AgentRow({ ix, off = false }: { ix: Interaction; off?: boolean }) {
  // a child session's card opens its parent's page, where it is answered (#5120)
  const to = useStore((s) => ix.session_id === "" ? requestRoute(ix) : conversationRoute(ix.machine_id, requestHome(ix), s.sessions, s.assistant));
  const persona = to.name === "persona";
  const where = useStore((s) => (persona ? "Persona" : machineWithHub(ix.machine_id, s.machines)));
  const ask = interactionAsk(ix);
  const glyph = IX_GLYPH[ix.kind];
  return (
    <a href={href(to)} onClick={(e) => onLinkClick(e, to)} inert={off} className={cx("flex gap-3 rounded-[14px] bg-surface p-3 shadow-card transition-[background-color,opacity] duration-150 hover:bg-inset", off && "opacity-45")}>
      <span className="flex size-10 shrink-0 items-center justify-center rounded-[10px] bg-field text-attention">
        {glyph === "list" || glyph === "terminal" ? <Icon name={glyph} size={18} stroke={2} /> : <Glyph name={glyph} size={18} stroke={2} />}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate text-[13.5px] font-semibold text-ink">{interactionAgentTitle(ix)}</span>
          <span className="flex-1" />
          <span className="shrink-0 text-[11.5px] text-ink-3 tabular-nums">{shortTime(ix.created_at)}</span>
        </span>
        {ask ? (
          <>
            <span className="mt-0.5 line-clamp-2 text-[12.5px] leading-[1.5] text-ink">{ask}</span>
            <span className="mt-0.5 block truncate text-[11.5px]">
              <span className="font-medium text-attention">{interactionTitle(ix)}</span>
              <span className="text-ink-3"> · {where}</span>
            </span>
          </>
        ) : (
          <>
            <span className="mt-0.5 block truncate text-[12.5px] font-medium text-attention">{interactionTitle(ix)}</span>
            <span className="mt-0.5 block truncate text-[11.5px] text-ink-3">{where}</span>
          </>
        )}
      </span>
    </a>
  );
});

/** "Will clean up N items": asked before Clean up all over BULK_CONFIRM_AT items or more. */
function BulkConfirm({ confirm, onClose }: { confirm: { items: InboxItem[]; scope: string } | null; onClose: () => void }) {
  const n = confirm?.items.length ?? 0;
  return (
    <Dialog
      open={Boolean(confirm)}
      onClose={onClose}
      title={t("inbox-inboxlist.confirm_clean_title")}
      width={380}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>{t("common.cancel")}</Button>
          <Button
            tone="primary"
            icon="check"
            onClick={() => {
              if (confirm) void setAllDone(confirm.items, "dismiss");
              onClose();
            }}
          >
            {t("inbox-inboxlist.confirm_clean", { count: n })}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">{t("inbox-inboxlist.confirm_body", { count: n, scope: confirm?.scope ?? "" })}</p>
    </Dialog>
  );
}
