// How a View's sections look (docs/extensions/extensions.md "Declarative UI" "UI slots"), with the web's design system: every kind of
// item — text, metric, progress, row (with an icon, a picture or a thumbnail), button, link, field, divider, chart,
// stat_grid, segmented, UI version 3's markdown, table, empty and loading, and UI version 4's code hosting components
// (CodeViews.tsx: header, breadcrumb, file_tree, code, diff, comment_thread, composer) and split layout — and a
// section's "Load more". An unknown item is left out. Shared by an extension's pages (settings/ExtensionView.tsx, which loads them and carries out what a
// press answers) and the assistant's cards (ui/AssistantCard.tsx, which says which actions may be pressed now, which
// one was answered, and when the fields are locked). A tone becomes a role color.
import { Suspense, useEffect, useRef, type ReactNode } from "react";
import { useNativeState, type NativeLocal } from "./native-state";
import ReactMarkdown from "react-markdown";
import remarkGfm from "../../ui/remark-gfm-parse";
import { lazyPage } from "../../lib/lazy-page";
import { openLink } from "../../lib/links";
import { useLinkManager } from "../../lib/manager-choice";
import {
  blocks, initials, isHttps, numberProblem, percent, tableRows, toneOf,
  type RowIcon, type UIAction, type UIItem, type UIMore, type UISection, type UITone, type UIView,
} from "../../lib/extension-ui";
import { presenceStyle, usePresence, type Present } from "../../ui/motion";
import { Rolling } from "../../ui/Rolling";
import { scrollBehavior } from "../../ui/stream/motion";
import { t } from "../../i18n";
import { Button, cx, EASE, Loading, Spinner } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { Switch } from "../settings/SettingsParts";
import { ChartFrame, LABEL_H, plotHeight } from "./ChartFrame";
import { ExtensionChart } from "./view-sections-lazy";
import { NEW_ICON_GLYPH } from "./icon-map";
import { SegmentedTrack } from "../../ui/Segmented";
import { colorOf, hue, hueTint } from "../../lib/extension-ui-v6";
import { Badge, Carousel, DateInput, glyphOf, Grid, ImageItem, MultiChoice, MultilineInput, SliderLine, Sparkline, TrailingControl, useImage, useRowMenu, type V6Handlers } from "./ViewBody6";

// UI version 4's code hosting items (CodeViews.tsx, DiffReview.tsx) are their own chunks too: only an extension's code
// review or file browser draws them, never the assistant's cards (ui/AssistantCard.tsx), which bring this file into the
// app's first chunk (#1794). They load with the first such item, not at idle (lib/lazy-page.ts), and each is drawn under
// CodeItem's Suspense, so the page-level one never shows.
const codeViews = () => import("./CodeViews");
const Header = lazyPage(() => codeViews().then((m) => m.Header));
const Breadcrumb = lazyPage(() => codeViews().then((m) => m.Breadcrumb));
const FileTree = lazyPage(() => codeViews().then((m) => m.FileTree));
const CodeView = lazyPage(() => codeViews().then((m) => m.CodeView));
const CommentThread = lazyPage(() => codeViews().then((m) => m.CommentThread));
const Composer = lazyPage(() => codeViews().then((m) => m.Composer));
const DiffView = lazyPage(() => import("./DiffReview").then((m) => m.DiffView));

function CodeItem({ children }: { children: ReactNode }) {
  return <Suspense fallback={<Loading />}>{children}</Suspense>;
}

// Only chart items draw it (ExtensionChart): registered on the first screen in view-sections-lazy.tsx, so the idle
// preload still fetches it now that this file is a chunk of its own (#3614).

const TEXT: Record<UITone, string> = { neutral: "text-ink", positive: "text-green", caution: "text-orange", danger: "text-red" };
const BAR: Record<UITone, string> = { neutral: "bg-accent", positive: "bg-green", caution: "bg-orange", danger: "bg-red" };

const inputClass =
  "h-8 w-full rounded-control bg-field px-2.5 text-[12.5px] text-ink shadow-inset-field outline-none placeholder:text-ink-3 focus:shadow-[inset_0_0_0_1px_var(--accent)] disabled:opacity-60";

/** What every item of a view is drawn with: the fields' values, what is busy, and what a press does. */
export interface ViewHandlers extends V6Handlers {
  values: Record<string, string>;
  setValue: (name: string, v: string) => void;
  /** The id of the action under way ("" none). */
  busy: string;
  onPress: (a: UIAction, destructive?: boolean) => void;
  onChoose: (item: UIItem, value: string) => void;
  /** A section's "Load more" (by scrolling to it or pressing it). */
  onMore?: (more: UIMore) => void;
  /** Opt-in observation deduplication across native stream remounts; explicit presses still use onMore. */
  onObserveMore?: (more: UIMore) => boolean;
  /** Whether an action may be pressed now (all of them when absent). */
  canPress?: (a: UIAction) => boolean;
  /** The id of the action answered: marked, the others stay as they are. */
  chosen?: string;
  /** The fields cannot be changed (an answered card: what was sent shows). */
  locked?: boolean;
  /** UI version 4: an action sent with values of the component's own (a file's path, a line, a comment typed). */
  onAct?: (a: UIAction, values?: Record<string, string>) => void;
  onNativeAct?: (a: UIAction, values?: Record<string, string>) => Promise<boolean>;
  /** Opt-in native stream binding: callbacks and controlled drafts belong to the stable node, not an action ID. */
  nodeHandlers?: (key: string) => ViewHandlers;
  draft?: { value: string; setValue: (value: string) => void };
  nativeAvailable?: () => boolean;
  nativeFetch?: (a: UIAction, values?: Record<string, string>) => Promise<import("../../lib/extension-ui").UIActionResult | null>;
  local?: NativeLocal;
  /** Native-only vocabulary supplement; undefined delegates to the unchanged legacy drawing. */
  renderItem?: (item: UIItem, handlers: ViewHandlers) => ReactNode | undefined;
  /** An assistant card's html component (#4215): drawn only by a caller that runs it; extensions never pass this. */
  renderHtml?: (item: UIItem) => ReactNode;
}

export { INERT } from "./view-sections-lazy";

/** A view's sections. `embedded`: inside another page (lighter); `compact`: a card (no chrome, small charts). */
export function ViewSections({ view, embedded, compact, ...handlers }: ViewHandlers & { view: UIView; embedded?: boolean; compact?: boolean }) {
  if (view.layout === "split" && !compact && !embedded) return <SplitSections view={view} {...handlers} />;
  return (
    <>
      {view.sections.map((s, i) => (
        <Section key={s.key ?? i} section={s} embedded={embedded} compact={compact} {...handlers} />
      ))}
    </>
  );
}

/** Scrolls the page to a file's diff (a file tree without an action, on a page of diffs). */
function revealDiff(path: string) {
  const el = [...document.querySelectorAll<HTMLElement>("[data-diff-path]")].find((e) => e.dataset.diffPath === path);
  el?.scrollIntoView({ behavior: scrollBehavior(), block: "start" });
}

/** The side pane's title on a narrow screen: its first section's, with how many files its tree has. */
function sideLabel(sections: UISection[]): string {
  const title = sections.find((s) => s.title)?.title || t("extensions-viewbody.side_pane");
  const files = sections.flatMap((s) => s.items).filter((it) => it.type === "file_tree").reduce((n, it) => n + (it.entries ?? []).filter((e) => e.kind !== "dir").length, 0);
  return files ? `${title} · ${files}` : title;
}

/**
 * A split view (UI version 4): wide, the side pane's sections in a column at the start that stays in view, the rest
 * beside it; narrow, the side pane folds into a bar on top that opens in place (and closes once a file is picked).
 */
function SplitSections({ view, ...handlers }: ViewHandlers & { view: UIView }) {
  const side = view.sections.filter((s) => s.pane === "side");
  const main = view.sections.filter((s) => s.pane !== "side");
  const [open, setOpen] = useNativeState(handlers.local, "side-open", false);
  const label = sideLabel(side);
  const picked = () => setOpen(false);
  return (
    <div className="flex flex-col gap-4 lg:grid lg:grid-cols-[minmax(240px,300px)_minmax(0,1fr)] lg:items-start lg:gap-5">
      {side.length > 0 && (
        <aside aria-label={label} className="min-w-0 lg:sticky lg:top-4 lg:max-h-[calc(100dvh-8rem)] lg:overflow-y-auto lg:rounded-card lg:bg-surface lg:shadow-card">
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            className="flex h-10 w-full items-center gap-2 rounded-card bg-surface px-4 text-left text-[13px] font-medium whitespace-nowrap text-ink shadow-card transition-colors hover:bg-hover lg:hidden"
          >
            <Icon name="list" size={14} className="shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 truncate">{label}</span>
            <Icon name="chevronRight" size={12} stroke={2.2} className={cx("shrink-0 text-ink-3 transition-transform duration-150", open && "rotate-90")} />
          </button>
          <div className={cx(open ? "mt-2 block overflow-hidden rounded-card bg-surface shadow-card" : "hidden", "lg:mt-0 lg:block lg:rounded-none lg:bg-transparent lg:shadow-none")}>
            {side.map((s, i) => (
              <Section key={s.key ?? `side${i}`} section={s} bare onPicked={picked} {...handlers} />
            ))}
          </div>
        </aside>
      )}
      <div className="flex min-w-0 flex-col gap-6">
        {main.map((s, i) => (
          <Section key={s.key ?? `main${i}`} section={s} {...handlers} />
        ))}
      </div>
    </div>
  );
}

interface SectionProps extends ViewHandlers {
  section: UISection;
  embedded?: boolean;
  compact?: boolean;
  /** In a split view's side pane: no card of its own (the pane is one). */
  bare?: boolean;
  /** A file was picked in the side pane. */
  onPicked?: () => void;
}

/** The components drawn as cards of their own, one under another, not inside the section's card. */
const STANDALONE = new Set(["code", "diff", "comment_thread", "grid", "carousel", "html"]);

function Section({ section, embedded, compact, bare, onPicked, ...rest }: SectionProps) {
  // Section-level callbacks (including MoreLine) use the section's key under the explicit stream adapter.
  if (section.key && rest.nodeHandlers) rest = { ...rest, ...rest.nodeHandlers(section.key), nodeHandlers: rest.nodeHandlers };
  const items = section.items ?? [];
  const more = section.more && rest.onMore ? section.more : undefined;
  // runs of ordinary items share the section's card; a code, a diff or a thread is a card of its own
  const runs: { standalone: boolean; items: UIItem[] }[] = [];
  for (const it of items) {
    const standalone = STANDALONE.has(it.type) && !compact && !bare;
    const last = runs[runs.length - 1];
    if (last && !standalone && !last.standalone) last.items.push(it);
    else runs.push({ standalone, items: [it] });
  }
  if (!runs.length) runs.push({ standalone: false, items: [] });
  return (
    <section style={{ animation: `fade-up 280ms ${EASE} both` }} className={cx(runs.length > 1 && "flex flex-col gap-3", bare && "py-0.5")}>
      {section.title && <h3 className={cx("font-medium text-ink-3", compact ? "px-4 pt-1 text-[11.5px]" : embedded ? "mb-1.5 px-1 text-[11.5px]" : bare ? "px-3 pt-2 pb-0.5 text-[11.5px]" : "mb-1.5 px-1 text-[12px]", runs.length > 1 && "-mb-1.5")}>{section.title}</h3>}
      {runs.map((run, ri) =>
        run.standalone ? (
          <div key={run.items[0].key ?? `s${ri}`}>
            <Item item={run.items[0]} compact={compact} onPicked={onPicked} {...rest} />
          </div>
        ) : (
          <Run key={run.items[0]?.key ?? `r${ri}`} items={run.items} embedded={embedded} compact={compact} bare={bare} onPicked={onPicked} more={ri === runs.length - 1 ? more : undefined} {...rest} />
        ),
      )}
      {more && runs[runs.length - 1].standalone && <MoreLine more={more} busy={rest.busy} onMore={rest.onMore!} onObserveMore={rest.onObserveMore} preserveObserver={!!rest.nodeHandlers} divided={false} />}
      {section.footer && <p className={cx("leading-relaxed text-ink-3", compact ? "px-4 pb-1 text-[11px]" : "mt-1.5 px-1 text-[11.5px]")}>{section.footer}</p>}
    </section>
  );
}

function Run({ items, embedded, compact, bare, onPicked, more, ...rest }: Omit<SectionProps, "section"> & { items: UIItem[]; more?: UIMore }) {
  const bs = blocks(items);
  // the first row of the run: a row after it begins the list's next entry even when the one before has more lines under
  // its row (a progress, a text: an account and its quota), so the hairline goes above it too
  const firstRow = bs.findIndex((b) => b.kind === "item" && b.item.type === "row");
  // each block by its key: one the extension adds comes in, one it takes away fades out where it was (#2757)
  const list: Present[] = bs.map((b, i) => {
    // rows that follow each other read as a list, and charts one under another as separate charts: a hairline between them
    const prev = i > 0 && bs[i - 1].kind === "item" ? (bs[i - 1] as { item: UIItem }).item.type : "";
    const nextEntry = b.kind === "item" && b.item.type === "row" && firstRow >= 0 && i > firstRow && prev !== "divider";
    const afterRow = b.kind === "item" && (nextEntry || (b.item.type === "chart" && prev === "chart"));
    // an item's key keeps it (and the list's place) when the extension draws the view again
    const key = (b.kind === "item" ? b.item.key : b.items[0]?.key) ?? `#${i}`;
    if (b.kind === "metrics" && !rest.renderItem) return { key, draw: (style) => <Metrics key={key} items={b.items} style={style} /> };
    if (b.kind === "metrics") return { key, draw: (style) => <div key={key} style={style}>{b.items.map((item) => <Item key={item.key} item={item} {...rest} />)}</div> };
    if (b.kind === "buttons")
      return { key, draw: (style) => (
        <div key={key} className="flex flex-wrap items-center gap-2 px-4 py-2.5" style={style}>
          {b.items.map((it, j) => (
            <ItemButton key={it.key ?? j} item={it} busy={rest.busy} onPress={rest.onPress} nodeHandlers={rest.nodeHandlers} canPress={rest.canPress} chosen={rest.chosen} />
          ))}
        </div>
      ) };
    const divided = afterRow || (b.kind === "item" && i > 0 && b.item.type === "composer");
    return { key, draw: (style) => (
      <div key={key} className={cx(divided && "border-t border-line")} style={style}>
        <Item item={b.item} compact={compact} onPicked={onPicked} {...rest} />
      </div>
    ) };
  });
  const drawn = usePresence(list);
  return (
      <div className={cx("overflow-hidden", compact || bare ? "" : embedded ? "rounded-control bg-inset shadow-hairline" : "rounded-card bg-surface shadow-card")}>
        {drawn.map(({ entry, phase }) => entry.draw(presenceStyle(phase)))}
        {more && <MoreLine more={more} busy={rest.busy} onMore={rest.onMore!} onObserveMore={rest.onObserveMore} preserveObserver={!!rest.nodeHandlers} divided={bs.length > 0} />}
      </div>
  );
}

/**
 * "Load more" at a section's end: asked for once when it scrolls into view (the extension answers with the longer view), or
 * when pressed. The line is a new one each time the view changes, so the next page is asked for when it shows again.
 */
function MoreLine({ more, busy, onMore, onObserveMore, divided, preserveObserver = false }: { more: UIMore; busy: string; onMore: (m: UIMore) => void; onObserveMore?: (m: UIMore) => boolean; divided: boolean; preserveObserver?: boolean }) {
  const ref = useRef<HTMLButtonElement>(null);
  const asked = useRef(false);
  const latestOnMore = useRef<(m: UIMore) => boolean | void>(onObserveMore ?? onMore);
  latestOnMore.current = onObserveMore ?? onMore;
  const observerCallback = preserveObserver ? undefined : onMore;
  const pending = busy === more.action.id;
  useEffect(() => {
    asked.current = false; // a new view: its "Load more" may ask once
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((entries) => {
      if (preserveObserver && busy) return;
      if (entries.some((e) => e.isIntersecting) && !asked.current) {
        const accepted = latestOnMore.current(more);
        asked.current = !preserveObserver || accepted !== false;
      }
    }, { rootMargin: "120px" });
    io.observe(el);
    return () => io.disconnect();
    // Stream handlers are version-bound; rebinding alone must not replay an unchanged callback.
  }, [more, observerCallback, preserveObserver ? !!busy : undefined]);
  return (
    <button
      ref={ref}
      type="button"
      disabled={!!busy}
      onClick={() => onMore(more)}
      className={cx("flex w-full items-center justify-center gap-2 px-4 py-2.5 text-[12.5px] whitespace-nowrap text-accent-ink transition-colors enabled:hover:bg-hover disabled:text-ink-3", divided && "border-t border-line")}
    >
      {pending && <Spinner />}
      <span className="truncate">{pending ? t("extensions-viewbody.loading") : more.title || t("extensions-viewbody.load_more")}</span>
    </button>
  );
}

function Metrics({ items, style }: { items: UIItem[]; style?: React.CSSProperties }) {
  return (
    <div className={cx("grid gap-x-4 gap-y-3 px-4 py-3", items.length === 1 ? "grid-cols-1" : items.length === 2 ? "grid-cols-2" : "grid-cols-2 sm:grid-cols-3")} style={style}>
      {items.map((it, i) => (
        <div key={i} className="min-w-0">
          <div className="flex min-w-0 items-center gap-1.5 text-[11.5px] text-ink-3">
            {/* UI version 6: the metric's icon, in its color */}
            {glyphOf(it.icon, ROW_ICON) && <Icon name={glyphOf(it.icon, ROW_ICON)!} size={13} className="shrink-0" style={it.icon_color ? { color: hue(it.icon_color) } : undefined} />}
            <span className="min-w-0 truncate">{it.label}</span>
          </div>
          <div className="mt-0.5 flex min-w-0 items-end gap-2">
            <span className={cx("min-w-0 truncate text-[20px] leading-tight font-semibold tabular-nums", TEXT[toneOf(it.tone)])}>{it.value && <Rolling value={it.value} />}</span>
            {it.spark && <Sparkline spark={it.spark} width={64} height={22} className="mb-0.5" />}
          </div>
          {it.detail && <div className="mt-0.5 truncate text-[11.5px] text-ink-3">{it.detail}</div>}
        </div>
      ))}
    </div>
  );
}

function ItemButton(props: { item: UIItem; busy: string; onPress: SectionProps["onPress"]; canPress?: ViewHandlers["canPress"]; chosen?: string; nodeHandlers?: ViewHandlers["nodeHandlers"] }) {
  const scoped = props.item.key ? props.nodeHandlers?.(props.item.key) : undefined;
  return <NativeItemButton {...props} {...scoped} />;
}

function NativeItemButton({ item, busy, onPress, canPress, chosen }: { item: UIItem; busy: string; onPress: SectionProps["onPress"]; canPress?: ViewHandlers["canPress"]; chosen?: string }) {
  if (!item.action) return null;
  const destructive = item.role === "destructive";
  const picked = !!chosen && chosen === item.action.id;
  return (
    <Button
      tone={item.action.submit ? "primary" : "secondary"}
      busy={busy === item.action.id}
      icon={picked ? "check" : undefined}
      disabled={(!!busy && busy !== item.action.id) || (canPress ? !canPress(item.action) : false)}
      onClick={() => onPress(item.action!, destructive)}
      title={item.title}
      className={cx("max-w-full min-w-0", destructive && "text-red")}
    >
      {/* one line: a title too long for the card is cut short, never wider than it */}
      <span className="truncate">{item.title}</span>
    </Button>
  );
}

function Item(props: Omit<SectionProps, "section" | "embedded" | "onMore" | "bare"> & { item: UIItem }) {
  const scoped = props.item.key ? props.nodeHandlers?.(props.item.key) : undefined;
  if (!scoped) return <NativeItem {...props} />;
  const handlers = { ...props, ...scoped };
  const custom = props.renderItem?.(props.item, handlers);
  return <div data-native-key={props.item.key}>{custom === undefined ? <NativeItem {...handlers} /> : custom}</div>;
}

function NativeItem({ item: it, compact, values, setValue, busy, onPress, onChoose, canPress, chosen, locked, onAct, onPicked, draft, local, nativeFetch, nativeAvailable, renderHtml, ...v6 }: Omit<SectionProps, "section" | "embedded" | "onMore" | "bare"> & { item: UIItem }) {
  const manager = useLinkManager();
  const pressable = (a: UIAction) => (canPress ? canPress(a) : true);
  const tiles = { glyphs: ROW_ICON, busy, onPress, canPress, ...v6 };
  const code = { act: onAct ?? ((a: UIAction) => onPress(a)), busy: locked ? busy || "stream-locked" : busy, reveal: revealDiff, local, fetch: nativeFetch, canFetch: nativeAvailable };
  switch (it.type) {
    case "html":
      return renderHtml ? renderHtml(it) : null;
    case "header":
      return <CodeItem><Header item={it} native={!!local} /></CodeItem>;
    case "breadcrumb":
      return <CodeItem><Breadcrumb item={it} {...code} /></CodeItem>;
    case "file_tree":
      return <CodeItem><FileTree item={it} onPicked={onPicked} {...code} /></CodeItem>;
    case "code":
      return <CodeItem><CodeView item={it} {...code} /></CodeItem>;
    case "diff":
      return <CodeItem key={local?.identity}><DiffView item={it} {...code} /></CodeItem>;
    case "comment_thread":
      return <CodeItem><CommentThread item={it} {...code} /></CodeItem>;
    case "composer":
      return locked && !local ? null : <CodeItem><Composer item={it} draft={draft} {...code} /></CodeItem>;
    case "chart":
      return (
        // the chart's own frame (header, detail) around an empty plot of its height while its chunk loads, so the
        // page-level Suspense never shows and nothing jumps
        <Suspense fallback={<ChartFrame item={it} compact={compact}><div aria-hidden data-chart-placeholder style={{ height: plotHeight(compact) + LABEL_H }} /></ChartFrame>}>
          <ExtensionChart item={it} compact={compact} />
        </Suspense>
      );
    // UI version 6
    case "image":
      return <ImageItem item={it} compact={compact} onImageFailed={v6.onImageFailed} />;
    case "grid":
      return <Grid item={it} compact={compact} {...tiles} />;
    case "carousel":
      return <Carousel item={it} compact={compact} {...tiles} />;
    case "markdown":
      return <ViewMarkdown text={it.text ?? ""} compact={compact} />;
    case "table":
      return <Table item={it} compact={compact} />;
    case "empty":
      return <Empty item={it} compact={compact} busy={busy} onPress={onPress} />;
    case "loading":
      return (
        <div className={cx("flex items-center justify-center gap-2 px-4 text-[12.5px] text-ink-3", compact ? "py-3" : "py-6")}>
          <Spinner />
          <span className="truncate">{it.title || t("extensions-viewbody.loading")}</span>
        </div>
      );
    case "stat_grid":
      return <StatGrid item={it} compact={compact} />;
    case "segmented":
      return <Segmented item={it} value={values[it.name ?? ""] ?? it.value ?? ""} busy={!!busy || locked || (it.action ? !pressable(it.action) : false)} pending={busy === it.action?.id} onChoose={(v) => onChoose(it, v)} />;
    case "text": {
      const style = it.style ?? "body";
      if (style === "code")
        return (
          <div className="px-4 py-2.5">
            <pre className="overflow-x-auto rounded-[8px] bg-inset px-2.5 py-2 font-mono text-[11.5px] leading-relaxed whitespace-pre-wrap break-all text-ink-2 shadow-hairline select-text">{it.text}</pre>
          </div>
        );
      // UI version 6: a section's big title and a small heading (headings, so a screen reader can move by them)
      if (style === "title" || style === "headline") {
        const H = style === "title" ? "h3" : "h4";
        return <H className={cx("px-4 break-words text-ink select-text", style === "title" ? "pt-3 pb-1 text-[17px] leading-snug font-semibold" : "pt-2.5 pb-0.5 text-[14px] leading-snug font-semibold")}>{it.text}</H>;
      }
      return (
        <p className={cx("px-4 py-2.5 leading-relaxed whitespace-pre-wrap break-words select-text", style === "secondary" ? "text-[12.5px] text-ink-2" : style === "caption" ? "text-[11.5px] text-ink-3" : "text-[13px] text-ink")}>
          {it.text}
        </p>
      );
    }
    case "metric":
      return <Metrics items={[it]} />;
    case "progress": {
      const pct = percent(it.ratio);
      const tone = toneOf(it.tone);
      return (
        <div className="px-4 py-2.5">
          <div className="flex items-baseline gap-3">
            <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{it.label}</span>
            {it.detail && <span className={cx("shrink-0 truncate text-[12px] tabular-nums", tone === "neutral" ? "text-ink-3" : TEXT[tone])}>{it.detail}</span>}
          </div>
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-hover-2" role="progressbar" aria-label={it.label} aria-valuenow={pct} aria-valuemin={0} aria-valuemax={100}>
            <div className={cx("h-full rounded-full transition-[width] duration-300", BAR[tone])} style={{ width: `${pct}%`, transitionTimingFunction: EASE }} />
          </div>
        </div>
      );
    }
    case "row":
      return <Row item={it} busy={busy} onPress={onPress} pressable={pressable} chosen={chosen} h={v6} />;
    case "button":
      return (
        <div className="px-4 py-2.5">
          <ItemButton item={it} busy={busy} onPress={onPress} canPress={canPress} chosen={chosen} />
        </div>
      );
    case "link": {
      if (!isHttps(it.url)) return null;
      const host = new URL(it.url!).host;
      return (
        <a
          href={it.url}
          target="_blank"
          rel="noreferrer noopener"
          onClick={(e) => {
            if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
            e.preventDefault();
            openLink(it.url!, it.title, manager);
          }}
          className="flex items-center gap-3 px-4 py-2.5 whitespace-nowrap transition-colors hover:bg-hover"
        >
          <span className="min-w-0 flex-1 truncate text-[13px] text-accent-ink">{it.title || host}</span>
          <span className="min-w-0 shrink truncate text-[12px] text-ink-3">{host}</span>
          <Icon name="external" size={12} className="shrink-0 text-ink-3" />
        </a>
      );
    }
    case "field":
      return <><Field item={it} value={values[it.name ?? ""] ?? ""} locked={locked || !!local && it.kind === "secret"} onChange={(v) => it.name && setValue(it.name, v)} />{local && it.detail !== undefined && <p className="px-4 pb-2 text-xs text-ink-3">{it.detail}</p>}</>;
    case "divider":
      return <div role="separator" className="mx-4 my-1 border-t border-line" />;
    default:
      return null;
  }
}

// stat_grid: 2 a row when narrow, 3 when there is room, all in one row when wide (a container query: a card is narrow
// on a wide page)
const STAT_COLS: Record<number, string> = {
  1: "grid-cols-1",
  2: "grid-cols-2",
  3: "grid-cols-2 @xs:grid-cols-3",
  4: "grid-cols-2 @lg:grid-cols-4",
  5: "grid-cols-2 @xs:grid-cols-3 @2xl:grid-cols-5",
  6: "grid-cols-2 @xs:grid-cols-3 @2xl:grid-cols-6",
};

function StatGrid({ item, compact }: { item: UIItem; compact?: boolean }) {
  const stats = (item.stats ?? []).slice(0, 6);
  return (
    <div className="@container">
      <div className={cx("grid gap-x-4 gap-y-3 px-4", compact ? "py-2" : "py-3", STAT_COLS[stats.length] ?? STAT_COLS[6])}>
        {stats.map((st, i) => {
          const tone = toneOf(st.tone);
          return (
            <div key={i} className="min-w-0">
              <div className="truncate text-[11.5px] text-ink-3">{st.label}</div>
              <div className={cx("mt-0.5 truncate leading-tight font-semibold text-ink", compact ? "text-[18px]" : "text-[20px]")} title={st.value}>
                <Rolling value={st.value} />
              </div>
              {/* UI version 6: the stat's trend, under its number (beside it, a narrow column would cut the number short) */}
              {st.spark && <Sparkline spark={st.spark} width={64} height={18} className="mt-1" />}
              {st.detail && <div className={cx("mt-0.5 truncate text-[11.5px] tabular-nums", tone === "neutral" ? "text-ink-3" : TEXT[tone])}>{st.detail}</div>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

/** segmented: one of a few, acting as soon as it is chosen (the choice shows at once, the view follows). */
function Segmented({ item, value, busy, pending, onChoose }: { item: UIItem; value: string; busy: boolean; pending: boolean; onChoose: (v: string) => void }) {
  const choices = item.choices ?? [];
  return (
    <div className="flex items-center gap-2 px-4 py-2.5">
      <SegmentedTrack role="radiogroup" label={item.label || item.name || ""} className="flex max-w-full items-center gap-0.5 overflow-x-auto rounded-control p-0.5" thumbClassName="rounded-[7px]">
        {choices.map((c) => {
          const on = c.value === value;
          return (
            <button
              key={c.value}
              type="button"
              role="radio"
              aria-checked={on}
              data-selected={on}
              disabled={busy && !on}
              onClick={() => !on && onChoose(c.value)}
              className={cx(
                "relative z-[1] flex h-7 shrink-0 items-center whitespace-nowrap rounded-[7px] px-2.5 text-[12px] transition-colors duration-150 disabled:opacity-60",
                on ? "font-medium text-ink" : "text-ink-3 hover:text-ink-2",
              )}
            >
              {c.title || c.value}
            </button>
          );
        })}
      </SegmentedTrack>
      {pending && <Spinner />}
    </div>
  );
}

function Field({ item: it, value, locked, onChange }: { item: UIItem; value: string; locked?: boolean; onChange: (v: string) => void }) {
  const id = `extension-ui-${it.name}`;
  if (it.kind === "toggle")
    return (
      <div className="px-4 py-2.5">
        <div className="flex items-center gap-3">
          <span className="min-w-0 flex-1 truncate text-[13px] text-ink">{it.label}</span>
          <Switch on={value === "true"} disabled={locked} onChange={(on) => onChange(on ? "true" : "false")} label={it.label || it.name || ""} />
        </div>
        {it.detail && <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">{it.detail}</p>}
      </div>
    );
  const problem = it.kind === "number" ? numberProblem(value) : "";
  let control: React.ReactNode;
  // UI version 6
  if (it.kind === "date" || it.kind === "time" || it.kind === "datetime") control = <DateInput id={id} item={it} value={value} locked={locked} onChange={onChange} />;
  else if (it.kind === "multiline") control = <MultilineInput id={id} item={it} value={value} locked={locked} onChange={onChange} />;
  else if (it.kind === "multichoice") control = <MultiChoice id={id} item={it} value={value} locked={locked} onChange={onChange} />;
  else if (it.kind === "choice") {
    const choices = it.choices ?? [];
    control =
      choices.length <= 4 ? (
        <div id={id} role="radiogroup" aria-label={it.label} className="flex flex-wrap gap-1.5">
          {choices.map((c) => {
            const on = value === c.value;
            return (
              <button
                key={c.value}
                type="button"
                role="radio"
                aria-checked={on}
                disabled={locked}
                onClick={() => onChange(c.value)}
                className={cx(
                  "flex h-7 items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 text-[12px] transition-[background-color,box-shadow,color] duration-150 disabled:opacity-60",
                  on ? "bg-accent-tint font-medium text-accent-ink shadow-[inset_0_0_0_1px_var(--accent)]" : "text-ink-2 shadow-hairline hover:bg-hover",
                )}
              >
                {on && <Icon name="check" size={12} stroke={2.2} />}
                {c.title || c.value}
              </button>
            );
          })}
        </div>
      ) : (
        <select id={id} value={value} disabled={locked} onChange={(e) => onChange(e.target.value)} className={cx(inputClass, "max-w-[320px]")}>
          {choices.map((c) => (
            <option key={c.value} value={c.value}>
              {c.title || c.value}
            </option>
          ))}
        </select>
      );
  } else {
    control = (
      <input
        id={id}
        type={it.kind === "secret" ? "password" : "text"}
        inputMode={it.kind === "number" ? "decimal" : undefined}
        value={value}
        disabled={locked}
        onChange={(e) => onChange(e.target.value)}
        placeholder={it.placeholder}
        spellCheck={false}
        autoComplete={it.kind === "secret" ? "new-password" : "off"}
        autoCapitalize="off"
        className={cx(inputClass, it.kind === "number" && "max-w-[200px] font-mono", it.kind === "secret" && "font-mono", problem && "shadow-[inset_0_0_0_1px_var(--red)]")}
      />
    );
  }
  return (
    <div className="px-4 py-2.5">
      <label htmlFor={id} className="mb-1.5 block text-[12px] font-medium text-ink-2">
        {it.label}
      </label>
      {control}
      {it.detail && <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">{it.detail}</p>}
      {problem && (
        <p role="alert" className="mt-1 text-[11.5px] text-red">
          {problem}
        </p>
      )}
    </div>
  );
}


// ── UI version 3 ─────────────────────────────────────────

/** A row's icon from the fixed vocabulary, as one of the web's icons. */
export const ROW_ICON: Record<RowIcon, IconName> = {
  extension: "extension", plugin: "extension", chart: "chart", gauge: "gauge", list: "list", bell: "bell", doc: "read", calendar: "calendar",
  money: "money", person: "person", chat: "chat", mail: "mail", link: "link", folder: "folder", star: "star", check: "check",
  warning: "warning", clock: "clock", tag: "tag", search: "search", settings: "gear", branch: "branch", commit: "commit", code: "code",
  pull_request: "pullRequest", merge: "merge", file: "read",
  ...NEW_ICON_GLYPH,
};

/** What leads a row: its icon (UI version 6: in its color), a picture (its initials until it loads, or when it does not), or a thumbnail. */
function RowLead({ item, onImageFailed }: { item: UIItem; onImageFailed?: () => void }) {
  // UI version 6: an https url, or the hub's path for the extension's own picture (ref)
  const thumb = useImage(item.thumbnail, onImageFailed);
  const avatar = useImage(item.avatar, onImageFailed);
  if (item.thumbnail) {
    const wide = item.thumbnail.aspect === "wide";
    return (
      <span className={cx("flex shrink-0 items-center justify-center overflow-hidden rounded-[8px] bg-inset shadow-hairline", wide ? "h-9 w-16" : "size-10")}>
        {thumb.url ? <img src={thumb.url} alt="" referrerPolicy="no-referrer" loading="lazy" onError={thumb.onError} className="size-full object-cover" /> : <Icon name="image" size={14} className="text-ink-3" />}
      </span>
    );
  }
  if (item.avatar) {
    return (
      <span className="flex size-8 shrink-0 items-center justify-center overflow-hidden rounded-full bg-accent-tint text-[12px] font-medium text-accent-ink">
        {avatar.url ? <img src={avatar.url} alt="" referrerPolicy="no-referrer" loading="lazy" onError={avatar.onError} className="size-full object-cover" /> : initials(item.avatar.text, item.title)}
      </span>
    );
  }
  const glyph = glyphOf(item.icon, ROW_ICON);
  if (!glyph) return null;
  const colored = !!colorOf(item.icon_color);
  return (
    <span
      className={cx("flex size-7 shrink-0 items-center justify-center rounded-[8px]", colored ? "" : "bg-inset text-ink-2 shadow-hairline")}
      style={colored ? { background: hueTint(item.icon_color), color: hue(item.icon_color) } : undefined}
    >
      <Icon name={glyph} size={14} />
    </span>
  );
}

/**
 * A row: what leads it, its title and subtitle, its value (UI version 6: with a spark), its badge; pressing it does its
 * action. UI version 6: a switch or a stepper at its end (a slider on a line under it), and its extra actions in a
 * menu (right click, a long press, the … button). The control and the … sit beside the row's button, never in it.
 */
function Row({ item: it, busy, onPress, pressable, chosen, h }: { item: UIItem; busy: string; onPress: ViewHandlers["onPress"]; pressable: (a: UIAction) => boolean; chosen?: string; h: V6Handlers }) {
  const menu = useRowMenu(it.actions, ROW_ICON, onPress, !busy);
  const c = it.control;
  const slider = c?.kind === "slider";
  const body = (
    <>
      <RowLead item={it} onImageFailed={h.onImageFailed} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px] text-ink">{it.title}</div>
        {it.subtitle && <div className="mt-0.5 truncate text-[11.5px] text-ink-3">{it.subtitle}</div>}
      </div>
      {it.spark && <Sparkline spark={it.spark} />}
      {it.value && !slider && <span className="shrink-0 truncate text-[12.5px] text-ink-2 tabular-nums"><Rolling value={it.value} /></span>}
      {/* a slider's row says the value it is at, as the thumb moves */}
      {slider && <span className="shrink-0 truncate text-[12.5px] text-ink-2 tabular-nums">{sliderText(c, h)}</span>}
      {it.badge?.text && <Badge badge={it.badge} />}
      {it.action && (busy === it.action.id ? <Spinner /> : <Icon name={chosen === it.action.id ? "check" : "chevronRight"} size={12} stroke={2} className={cx("shrink-0", chosen === it.action.id ? "text-accent" : "text-ink-3")} />)}
    </>
  );
  const line = it.action ? (
    <button
      type="button"
      disabled={!!busy || !pressable(it.action)}
      aria-pressed={chosen ? chosen === it.action.id : undefined}
      onClick={() => onPress(it.action!)}
      className={cx("flex min-w-0 flex-1 w-full items-center gap-3 py-2.5 pl-4 text-left whitespace-nowrap transition-colors enabled:hover:bg-hover", chosen === it.action.id && "bg-accent-tint", c && !slider ? "pr-2" : menu.button ? "pr-1" : "pr-4")}
    >
      {body}
    </button>
  ) : (
    <div className={cx("flex min-w-0 flex-1 items-center gap-3 py-2.5 pl-4 whitespace-nowrap", c && !slider ? "pr-2" : menu.button ? "pr-1" : "pr-4")}>{body}</div>
  );
  if (!c && !menu.button) return line;
  return (
    <div {...menu.props} className="group/row">
      <div className="flex items-center">
        {line}
        {c && !slider && (
          <span className={cx("flex shrink-0 items-center", menu.button ? "pr-1" : "pr-4")}>
            <TrailingControl control={c} label={it.title || c.name} h={h} />
          </span>
        )}
        {menu.button && <span className="flex shrink-0 items-center pr-3">{menu.button}</span>}
      </div>
      {slider && <SliderLine control={c} label={it.title || c.name} h={h} />}
      {menu.menu}
    </div>
  );
}

/** A slider row's value as shown (the user's while it is on its way). */
function sliderText(c: NonNullable<UIItem["control"]>, h: V6Handlers): string {
  const v = Number(h.controls?.[c.name] ?? c.value ?? c.min ?? 0);
  return c.display && v === c.value ? c.display : `${v}${c.unit ? ` ${c.unit}` : ""}`;
}

// markdown: only the safe part — headings, paragraphs, lists, emphasis, code, quotes and https links. No HTML, no
// pictures; anything else shows as its text.
// UI version 4 adds GFM's tables and task lists (a pull request's description has them).
const MD_ALLOWED = ["p", "h1", "h2", "h3", "ul", "ol", "li", "strong", "em", "del", "code", "pre", "blockquote", "a", "br", "hr", "table", "thead", "tbody", "tr", "th", "td", "input"];

export function ViewMarkdown({ text, compact }: { text: string; compact?: boolean }) {
  const manager = useLinkManager();
  return (
    <div className={cx("prose-j prose-sm px-4 py-2.5 select-text", compact && "prose-compact")}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        allowedElements={MD_ALLOWED}
        unwrapDisallowed
        skipHtml
        urlTransform={(url) => (isHttps(url) ? url : "")}
        components={{
          h1: ({ children }) => <h3>{children}</h3>,
          h2: ({ children }) => <h4>{children}</h4>,
          h3: ({ children }) => <h5>{children}</h5>,
          a: ({ href, children }) =>
            href ? (
              <a
                href={href}
                target="_blank"
                rel="noreferrer noopener"
                onClick={(e) => {
                  if (e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey) return;
                  e.preventDefault();
                  openLink(href, undefined, manager);
                }}
              >
                {children}
              </a>
            ) : (
              <span>{children}</span>
            ),
        }}
      >
        {text.slice(0, 20000)}
      </ReactMarkdown>
    </div>
  );
}

/** table: columns with their titles, a row a line; wider than the page scrolls sideways instead of squeezing. */
function Table({ item, compact }: { item: UIItem; compact?: boolean }) {
  const columns = (item.columns ?? []).slice(0, 8);
  const rows = tableRows({ ...item, columns });
  const align = (i: number) => (columns[i]?.align === "end" ? "text-right" : "text-left");
  return (
    <div className={cx("px-4", compact ? "py-2" : "py-2.5")}>
      <div className="overflow-x-auto">
        <table className="w-full border-collapse text-[12.5px]">
          {item.caption && <caption className="pb-1.5 text-left text-[11.5px] text-ink-3">{item.caption}</caption>}
          <thead>
            <tr>
              {columns.map((c, i) => (
                <th key={i} scope="col" className={cx("border-b border-line px-2 py-1.5 font-medium whitespace-nowrap text-ink-3 first:pl-0 last:pr-0", align(i))}>
                  {c.title}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, j) => (
              <tr key={j} className="border-b border-line last:border-b-0">
                {r.map((cell, i) => (
                  <td key={i} className={cx("px-2 py-1.5 whitespace-nowrap text-ink tabular-nums select-text first:pl-0 last:pr-0", align(i))}>
                    {cell}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/** empty: nothing here yet, in the web's empty-state look, with a button to do something about it. */
function Empty({ item, compact, busy, onPress }: { item: UIItem; compact?: boolean; busy: string; onPress: ViewHandlers["onPress"] }) {
  const glyph = glyphOf(item.icon, ROW_ICON);
  // UI version 6: the icon in its color
  const tinted = colorOf(item.icon_color) ? { background: hueTint(item.icon_color), color: hue(item.icon_color) } : undefined;
  return (
    <div className={cx("flex flex-col items-center gap-2 px-6 text-center", compact ? "py-4" : "py-8")}>
      <span className={cx("flex size-9 items-center justify-center rounded-[10px]", tinted ? "" : "bg-inset text-ink-3 shadow-hairline")} style={tinted}>
        <Icon name={glyph ?? "extension"} size={16} />
      </span>
      <p className="text-[13px] font-medium text-ink">{item.title}</p>
      {item.detail && <p className="max-w-[420px] text-[12px] leading-relaxed break-words text-ink-3">{item.detail}</p>}
      {item.button && (
        <Button tone="secondary" busy={busy === item.button.action.id} disabled={!!busy && busy !== item.button.action.id} onClick={() => onPress(item.button!.action)} className="mt-1">
          {item.button.title}
        </Button>
      )}
    </div>
  );
}
