// Settings › Model providers › Add › "Paste a configuration" (#5193): a box for what the user runs today (a shell alias,
// export lines or a .env file, Claude Code's settings.json, a curl command, a cindy provider JSON), read by the hub
// (POST /api/v1/launchers/import/parse) once it stops changing, at once on a paste. Each provider found is a card: its
// host, models, sign-in, what it brings and leaves out, and why parts were not used; on an address already stored, a
// choice between adding its models there, replacing that provider's key, or a new provider. The wizard keeps the state
// (lib/provider-paste.ts) and its Continue goes on. The text never leaves the request body: no URL, log or report.
import { useEffect, useId, useRef, useState } from "react";
import { api, ApiError, errorText } from "../../lib/api";
import { providerTitle } from "../../lib/provider-names";
import { answered, draftModels, headerLines, noteText, pasteActions, pasteChoice, sameAction, type PasteAction, type PasteDraft, type PasteNote, type PasteResult, type PasteState } from "../../lib/provider-paste";
import { hostOf, importBrings, importLeftOut } from "../../lib/providers";
import { cx, SECRET_INPUT } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { textareaClass } from "../assistant/parts";
import { Pill } from "./SettingsParts";
import { arrowKeys, AUTO_TEST_MS, BusyLine, CheckMark, ChoiceRows, Labelled, LineAction } from "./ProviderFields";
import { t } from "../../i18n";

/** The edits that put a whole text in at once. */
const PASTED = new Set(["insertFromPaste", "insertFromDrop"]);
const EXAMPLE = "export ANTHROPIC_BASE_URL=https://…\nexport ANTHROPIC_AUTH_TOKEN=…";

const FORMAT: Record<string, () => string> = {
  shell: () => t("settings-providerpaste.format_shell"),
  settings: () => t("settings-providerpaste.format_settings"),
  curl: () => t("settings-providerpaste.format_curl"),
  cindy: () => t("settings-providerpaste.format_cindy"),
};

export function ProviderPaste({ paste, setPaste }: { paste: PasteState; setPaste: React.Dispatch<React.SetStateAction<PasteState>> }) {
  const id = useId();
  const hintId = useId();
  const [reading, setReading] = useState(false);
  const [error, setError] = useState<{ text: string; retry: boolean } | null>(null);
  // one per text: an answer for an older one is dropped
  const seq = useRef(0);
  // the latest change was a paste (or a drop): read it without waiting
  const now = useRef(false);

  const read = async (text: string, mine: number) => {
    setReading(true);
    setError(null);
    try {
      const answer = await api<PasteResult>("/launchers/import/parse", { method: "POST", json: { text } });
      if (mine === seq.current) setPaste((s) => answered(s, text, answer));
    } catch (e) {
      if (mine !== seq.current) return;
      // an older hub has no such route
      const old = e instanceof ApiError && e.status === 404;
      setError({ text: old ? t("settings-providerpaste.hub_old") : t("settings-providerpaste.read_failed", { error: errorText(e) }), retry: !old });
    } finally {
      if (mine === seq.current) setReading(false);
    }
  };

  useEffect(() => {
    const mine = ++seq.current;
    const at = now.current;
    now.current = false;
    setReading(false);
    setError(null);
    if (!paste.text.trim()) {
      setPaste((s) => (s.result ? { ...s, result: null, draft: -1, action: null } : s));
      return;
    }
    // read already (back from "Connect")
    if (paste.result?.text === paste.text) return;
    if (at) return void read(paste.text, mine);
    const timer = setTimeout(() => void read(paste.text, mine), AUTO_TEST_MS);
    return () => clearTimeout(timer);
  }, [paste.text]);

  const answer = paste.result?.answer;
  const stale = !!paste.result && paste.result.text !== paste.text;
  const drafts = answer?.drafts ?? [];
  const chosen = pasteChoice(paste);
  const pick = (i: number) => setPaste((s) => ({ ...s, draft: i, action: null }));
  return (
    <div className="flex flex-col gap-3">
      <Labelled label={t("settings-providerpaste.label")} hint={t("settings-providerpaste.hint")} htmlFor={id} describedById={hintId}>
        <textarea id={id} aria-describedby={hintId} value={paste.text} rows={6} placeholder={EXAMPLE} autoFocus {...SECRET_INPUT} autoComplete="off"
          onChange={(e) => {
            now.current = PASTED.has((e.nativeEvent as InputEvent).inputType);
            const text = e.target.value;
            setPaste((s) => ({ ...s, text }));
          }}
          className={cx(textareaClass, "font-mono")} />
      </Labelled>
      {reading ? (
        <BusyLine>{t("settings-providerpaste.reading")}</BusyLine>
      ) : error ? (
        <div role="alert" className="flex items-start gap-1.5 text-[12px] text-red">
          <Icon name="warning" size={12} stroke={2.4} className="mt-0.5 shrink-0" />
          <span className="min-w-0 flex-1 break-words">{error.text}</span>
          {error.retry && <LineAction onClick={() => void read(paste.text, ++seq.current)}>{t("settings-providerwizard.retry")}</LineAction>}
        </div>
      ) : (
        answer && !stale && FORMAT[answer.format] && <p role="status" className="text-[12px] text-ink-2">{FORMAT[answer.format]!()}</p>
      )}
      {answer && (
        <div className={cx("flex flex-col gap-3", stale && "opacity-60")}>
          {drafts.length === 0 && <p className="text-[12.5px] text-ink-2">{t("settings-providerpaste.nothing")}</p>}
          {drafts.length === 1 && (
            <div className="overflow-hidden rounded-card bg-surface shadow-btn">
              <DraftCard d={drafts[0]!} />
            </div>
          )}
          {drafts.length > 1 && (
            <div className="flex flex-col gap-1.5">
              <p className="text-[12px] text-ink-2">{t("settings-providerpaste.several", { count: drafts.length })}</p>
              <div role="radiogroup" aria-label={t("settings-providerpaste.drafts_label")} onKeyDown={arrowKeys(drafts.length, paste.draft, "data-draft", pick)}
                className="divide-y divide-line overflow-hidden rounded-card bg-surface shadow-btn">
                {drafts.map((d, i) => (
                  <DraftCard key={i} d={d} radio={{ index: i, checked: paste.draft === i, tabbable: paste.draft === i || (paste.draft < 0 && i === 0), onPick: () => pick(i) }} />
                ))}
              </div>
            </div>
          )}
          {chosen && !!chosen.draft.matches?.length && (
            <ActionChoice d={chosen.draft} action={chosen.action} onAction={(action) => setPaste((s) => ({ ...s, action }))} />
          )}
          <Notes notes={answer.notes} label={t("settings-providerpaste.notes_label")} className="text-ink-2" />
        </div>
      )}
    </div>
  );
}

/** Why parts were not used, a line each. */
function Notes({ notes, label, className }: { notes: PasteNote[]; label: string; className: string }) {
  if (!notes.length) return null;
  return (
    <ul aria-label={label} className={cx("flex flex-col gap-1 text-[11.5px] leading-relaxed", className)}>
      {notes.map((n, i) => (
        <li key={i} className="flex items-start gap-1.5">
          <Icon name="info" size={11} stroke={2.2} className="mt-0.5 shrink-0" />
          <span className="min-w-0 flex-1 break-words">{noteText(n)}</span>
        </li>
      ))}
    </ul>
  );
}

/** One provider the text holds: its name (a cindy provider's, else its aliases in monospace, else its host), host, how
 *  many models and how it signs in, then what it brings and leaves out and its notes. A radio when there are several. */
function DraftCard({ d, radio }: { d: PasteDraft; radio?: { index: number; checked: boolean; tabbable: boolean; onPick: () => void } }) {
  const p = d.provider;
  const host = hostOf(p.base_url);
  const title = p.display_name || d.label || host;
  const openAI = p.kind === "api" && (p.api === "responses" || p.api === "chat");
  const lines = [importBrings(d), ...importLeftOut(d), ...headerLines(d)].filter(Boolean);
  const notes = d.notes ?? [];
  const head = (
    <>
      {radio && <CheckMark on={radio.checked} round className="mt-0.5" />}
      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-center gap-2">
          <span className={cx("truncate text-[13px] font-medium text-ink", !p.display_name && !!d.label && "font-mono")}>{title}</span>
          {p.kind === "api" && <Pill>{t("settings-providerwizard.direct_api")}</Pill>}
        </span>
        <span className="mt-0.5 flex min-w-0 flex-wrap gap-x-2 text-[11.5px] text-ink-3">
          {title !== host && <span className="truncate">{host}</span>}
          <span className="tabular-nums">{t("lib-providers.model_count", { count: draftModels(d).length })}</span>
          {!openAI && <span>{p.auth === "api_key" ? "x-api-key" : "Bearer"}</span>}
        </span>
      </span>
    </>
  );
  return (
    <div data-paste-draft={radio?.index ?? 0}>
      {radio ? (
        <button type="button" role="radio" aria-checked={radio.checked} tabIndex={radio.tabbable ? 0 : -1} data-draft={radio.index} onClick={radio.onPick}
          className="touch-hit flex w-full items-start gap-3 px-3 py-2.5 text-left transition-colors hover:bg-hover pointer-coarse:min-h-14">
          {head}
        </button>
      ) : (
        <div className="flex items-start gap-3 px-3 py-2.5">{head}</div>
      )}
      {(lines.length > 0 || notes.length > 0) && (
        <div className={cx("flex flex-col gap-1 pr-3 pb-2.5 text-[11.5px] leading-relaxed text-ink-3", radio ? "pl-10" : "pl-3")}>
          {lines.map((l) => (
            <p key={l} className="break-words">{l}</p>
          ))}
          <Notes notes={notes} label={t("settings-providerpaste.draft_notes_label", { name: title })} className="text-ink-3" />
        </div>
      )}
    </div>
  );
}

/** For a draft on an address a stored provider has: add its models there (same key), replace that provider's key
 *  (another key), or add it as a new provider. */
function ActionChoice({ d, action, onAction }: { d: PasteDraft; action: PasteAction; onAction: (a: PasteAction) => void }) {
  const options = pasteActions(d);
  const text = (o: (typeof options)[number]) => {
    const name = o.match ? providerTitle(o.match) : "";
    if (o.type === "merge") return { label: t("settings-providerpaste.merge", { name }), hint: t("settings-providerpaste.merge_hint", { name }) };
    if (o.type === "key") return { label: t("settings-providerpaste.key", { name }), hint: t("settings-providerpaste.key_hint", { name }) };
    return { label: t("settings-providerpaste.new"), hint: t("settings-providerpaste.new_hint") };
  };
  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-[12px] text-ink-2">{t("settings-providerpaste.matched", { count: d.matches!.length })}</p>
      <ChoiceRows label={t("settings-providerpaste.action_label")} options={options.map(text)} at={options.findIndex((o) => sameAction(action, o))}
        onPick={(i) => onAction(options[i]!)} />
    </div>
  );
}
