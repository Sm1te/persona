// Settings › Model providers › Import (#2455, #5024): pick a computer, preview the providers its claude aliases add up to
// (~/.zshrc, ~/.bashrc, ~/.bash_aliases; GET /api/v1/launchers/import), tick some, choose for a group whose aliases differ in more than the
// model whether each alias becomes a provider of its own, and import them (POST), optionally hiding the imported aliases in
// Persona. The keys never come to the browser: the hub reads them again from that computer and keeps them sealed.
import { useEffect, useId, useState } from "react";
import { api } from "../../lib/api";
import {
  importBrings,
  importCanSplit,
  importCount,
  importDiffers,
  importErrorText,
  importFrom,
  importLeftOut,
  importMerges,
  importOutcome,
  importRow,
  importSources,
  importStays,
  importSummary,
  type ImportedProvider,
  type ImportResult,
} from "../../lib/providers";
import { useStore } from "../../lib/store";
import { Button, cx, Dialog, Loading, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { Pill } from "./SettingsParts";
import { CheckMark, INPUT, Labelled } from "./ProviderFields";
import { formatList, t } from "../../i18n";

export function ProviderImport({ open, onClose, onImported }: { open: boolean; onClose: () => void; onImported: () => void }) {
  const machines = useStore((s) => s.machines);
  const [machine, setMachine] = useState("");
  const [list, setList] = useState<ImportedProvider[] | null>(null);
  const [error, setError] = useState("");
  const [picked, setPicked] = useState<Set<string>>(new Set());
  // the groups left on "A provider for each alias" (all that offer the choice, at first)
  const [split, setSplit] = useState<Set<string>>(new Set());
  const [hide, setHide] = useState(true);
  const [busy, setBusy] = useState(false);
  const hideId = useId();

  useEffect(() => {
    if (!open) return;
    setMachine((m) => m || machines.find((x) => x.online)?.id || machines[0]?.id || "");
  }, [open, machines]);

  useEffect(() => {
    if (!open || !machine) return;
    let live = true;
    setList(null);
    setError("");
    api<{ providers?: ImportedProvider[] }>(`/launchers/import?machine=${encodeURIComponent(machine)}`)
      .then((r) => {
        if (!live) return;
        const ps = r.providers ?? [];
        setList(ps);
        setPicked(new Set(ps.map((p) => p.name)));
        setSplit(new Set(ps.filter(importCanSplit).map((p) => p.name)));
        setHide(true);
      })
      .catch((e) => live && (setError(importErrorText(e)), setList([])));
    return () => void (live = false);
  }, [open, machine]);

  const flip = (set: (f: (s: Set<string>) => Set<string>) => void, name: string, on?: boolean) =>
    set((s) => {
      const n = new Set(s);
      if (on ?? !n.has(name)) n.add(name);
      else n.delete(name);
      return n;
    });

  const items = list ?? [];
  const count = importCount(items, picked, split);
  const sources = importSources(items, picked, split).filter((s) => s.hide !== "none");
  const hiding = sources.filter((s) => s.hide === "ok").map((s) => s.alias);
  const staying = sources.filter((s) => s.hide !== "ok");
  // the note on merging only when this import will merge something, as picked and chosen
  const merges = importMerges(items, picked, split);

  const confirm = async () => {
    if (busy || count === 0) return;
    setBusy(true);
    setError("");
    try {
      const chosen = items.filter((p) => picked.has(p.name));
      const r = await api<ImportResult>("/launchers/import", {
        method: "POST",
        json: {
          machine,
          providers: chosen.map((p) => p.name),
          split: chosen.filter((p) => split.has(p.name) && importCanSplit(p)).map((p) => p.name),
          hide_aliases: sources.length > 0 && hide,
        },
      });
      toast(importSummary(r), "ok");
      onImported();
      onClose();
    } catch (e) {
      setError(importErrorText(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      width={560}
      title={t("settings-providerimport.title")}
      footer={
        <>
          <Button tone="quiet" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <span className="flex-1" />
          <Button tone="primary" busy={busy} disabled={!items.length || count === 0} onClick={() => void confirm()}>
            {count ? t("settings-providerimport.import_count", { count }) : t("settings-providerimport.import")}
          </Button>
        </>
      }
    >
      {machines.length === 0 ? (
        <p className="py-6 text-center text-[12.5px] leading-relaxed text-ink-3">{t("settings-providerimport.no_computers")}</p>
      ) : (
        <div className="flex flex-col gap-3">
          <Labelled label={t("settings-providerimport.computer")} hint={t("settings-providerimport.computer_hint")}>
            <select value={machine} onChange={(e) => setMachine(e.target.value)} aria-label={t("settings-providerimport.computer")} className={INPUT}>
              {machines.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.name}
                  {m.online ? "" : t("settings-providerimport.offline_suffix")}
                </option>
              ))}
            </select>
          </Labelled>
          {!list && !error && <Loading />}
          {error && (
            <p role="alert" className="flex items-start gap-1.5 text-[12.5px] text-red">
              <Icon name="warning" size={13} stroke={2.2} className="mt-0.5 shrink-0" />
              {error}
            </p>
          )}
          {list && !error && list.length === 0 && <p className="py-6 text-center text-[12.5px] text-ink-3">{t("settings-providerimport.empty")}</p>}
          {list && list.length > 0 && (
            <ul aria-label={t("settings-providerimport.list_label")} className="overflow-hidden rounded-card bg-surface shadow-btn">
              {list.map((p) => (
                <ImportItem key={p.name} p={p} on={picked.has(p.name)} split={split.has(p.name)}
                  onToggle={() => flip(setPicked, p.name)} onSplit={(on) => flip(setSplit, p.name, on)} />
              ))}
            </ul>
          )}
          {list && sources.length > 0 && (
            <div className="overflow-hidden rounded-card bg-surface shadow-btn">
              <button type="button" role="checkbox" aria-checked={hide} onClick={() => setHide((h) => !h)} data-import-hide
                aria-labelledby={`${hideId}-label`} aria-describedby={`${hideId}-hint`}
                className="touch-hit flex w-full items-start gap-3 px-3 py-2.5 text-left transition-colors hover:bg-hover">
                <CheckMark on={hide} className="mt-0.5" />
                <span className="min-w-0 flex-1">
                  <span id={`${hideId}-label`} className="block text-[13px] font-medium text-ink">{t("settings-providerimport.hide")}</span>
                  <span id={`${hideId}-hint`} className="mt-0.5 block text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerimport.hide_hint")}</span>
                </span>
              </button>
              {hide && (
                <div className="flex flex-col gap-1 pr-3 pb-2.5 pl-10 text-[11.5px] leading-relaxed text-ink-3" data-import-stays>
                  {hiding.length > 0 && <p className="text-ink-2">{t("settings-providerimport.hide_which", { aliases: formatList(hiding) })}</p>}
                  {staying.map((s) => (
                    <p key={s.alias}>{importStays(s)}</p>
                  ))}
                </div>
              )}
            </div>
          )}
          {merges && <p className="text-[11.5px] text-ink-3">{t("settings-providerimport.merge_note")}</p>}
        </div>
      )}
    </Dialog>
  );
}

/** What one provider brings along and leaves out of its aliases' variables, a line each. */
function EnvLines({ p }: { p: ImportedProvider }) {
  const brings = importBrings(p);
  return (
    <>
      {brings && <p className="break-words">{brings}</p>}
      {importLeftOut(p).map((l) => (
        <p key={l} className="break-words">{l}</p>
      ))}
    </>
  );
}

const hasEnvLines = (p: ImportedProvider) => !!p.env_names?.length || !!p.skipped_env?.length;

/** One group of the preview: the tick and its summary, then what it brings and leaves out, and, when its aliases differ in
 *  more than the model, the choice between a provider for each alias (each listed with its own variables) and one
 *  provider (the first alias's settings). */
function ImportItem({ p, on, split, onToggle, onSplit }: { p: ImportedProvider; on: boolean; split: boolean; onToggle: () => void; onSplit: (on: boolean) => void }) {
  const choice = importCanSplit(p);
  const parts = choice && split;
  const r = importRow(p, split);
  const from = importFrom(p);
  const detail = choice || hasEnvLines(p);
  return (
    <li className="border-b border-line last:border-b-0">
      <button type="button" role="checkbox" aria-checked={on} onClick={onToggle} data-import={p.name}
        className="touch-hit flex w-full items-start gap-3 px-3 py-2.5 text-left transition-colors hover:bg-hover">
        <CheckMark on={on} className="mt-0.5" />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate font-mono text-[13px] font-medium text-ink">{r.name}</span>
            <Pill tone={r.merge ? undefined : "accent"}>{r.action}</Pill>
          </span>
          <span className="mt-0.5 flex min-w-0 flex-wrap gap-x-2 text-[11.5px] text-ink-3">
            <span className="truncate">{r.host}</span>
            <span className="tabular-nums">{r.models}</span>
            {from.map((f) => (
              <span key={f} className="truncate">{f}</span>
            ))}
          </span>
        </span>
      </button>
      {detail && (
        <div className={cx("flex flex-col gap-1.5 pr-3 pb-2.5 pl-10 text-[11.5px] leading-relaxed text-ink-3", !on && "opacity-60")} data-import-detail={p.name}>
          {choice && <p className="text-ink-2">{importDiffers(p)}</p>}
          {choice && <SplitChoice name={p.name} first={p.aliases[0] ?? p.name} split={split} onSplit={onSplit} />}
          {parts ? (
            <ul aria-label={t("settings-providerimport.variants_label", { name: p.name })} className="flex flex-col gap-1.5">
              {importOutcome(p, true).map((v) => {
                const vr = importRow(v);
                return (
                  <li key={v.name} className="min-w-0">
                    <span className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
                      <span className="max-w-full truncate font-mono text-ink-2">{vr.name}</span>
                      <Pill tone={vr.merge ? undefined : "accent"}>{vr.action}</Pill>
                      {v.model && <span className="max-w-full truncate font-mono">{v.model}</span>}
                    </span>
                    <EnvLines p={v} />
                  </li>
                );
              })}
            </ul>
          ) : (
            <EnvLines p={p} />
          )}
        </div>
      )}
    </li>
  );
}

/** "A provider for each alias" / "One provider": a radio group, arrow keys move between the two. */
function SplitChoice({ name, first, split, onSplit }: { name: string; first: string; split: boolean; onSplit: (on: boolean) => void }) {
  const options = [
    { on: true, label: t("settings-providerimport.split"), hint: t("settings-providerimport.split_hint") },
    { on: false, label: t("settings-providerimport.merge"), hint: t("settings-providerimport.merge_hint", { alias: first }) },
  ];
  const key = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (!["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"].includes(e.key)) return;
    e.preventDefault();
    onSplit(!split);
    const next = e.currentTarget.querySelector<HTMLButtonElement>(`[data-split="${!split}"]`);
    next?.focus();
  };
  return (
    <div role="radiogroup" aria-label={t("settings-providerimport.choice_label", { name })} onKeyDown={key}
      className="divide-y divide-line overflow-hidden rounded-card bg-inset shadow-hairline">
      {options.map((o) => {
        const checked = o.on === split;
        return (
          <button key={String(o.on)} type="button" role="radio" aria-checked={checked} tabIndex={checked ? 0 : -1} data-split={String(o.on)}
            onClick={() => onSplit(o.on)} className="touch-hit flex w-full items-start gap-2.5 px-3 py-2 text-left transition-colors hover:bg-hover">
            <CheckMark on={checked} round className="mt-0.5" />
            <span className="min-w-0 flex-1">
              <span className="block truncate text-[12.5px] font-medium text-ink">{o.label}</span>
              <span className="block text-[11.5px] text-ink-3">{o.hint}</span>
            </span>
          </button>
        );
      })}
    </div>
  );
}
