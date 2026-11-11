// "Close finished terminals" (#4754, docs/integrations/engines.md "Agents run in a terminal"): the terminals stopped
// Agents left stay until the user closes them. Per computer: what it would close now (a preview that closes nothing),
// a confirmation listing them (the destructive rung of DESIGN_SYSTEM.md 4.4: the verb again in red, and Cancel), then
// exactly the confirmed panes, a toast with how many went, and the list read again.
import { useState } from "react";
import { t } from "../../i18n";
import { errorText } from "../../lib/api";
import { sessionTitle } from "../../lib/format";
import { getSession } from "../../lib/store";
import { closeFinished, previewFinished, type CloseFinishedResult } from "../../lib/terminal-list";
import type { TermSpace } from "../../lib/terminals";
import { Button, Dialog, toast } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

type State = { mid: string; result?: CloseFinishedResult; busy: boolean } | null;

/** `spacesOf`: a computer's tree as the page shows it (the rows' titles); `reload`: read that computer's panes again. */
export function useCloseFinished({ spacesOf, reload }: { spacesOf: (mid: string) => readonly TermSpace[]; reload: (mid: string) => void }) {
  const [state, setState] = useState<State>(null);
  const start = async (mid: string) => {
    if (state) return;
    setState({ mid, busy: true });
    try {
      const result = await previewFinished(mid);
      if (!result.closed.length) {
        setState(null);
        toast(t("terminal-terminalpage.finished_none"));
        reload(mid);
        return;
      }
      setState({ mid, result, busy: false });
    } catch (e) {
      setState(null);
      toast(t("terminal-terminalpage.close_finished_failed", { error: errorText(e) }), "error");
    }
  };
  const confirm = async () => {
    if (!state?.result || state.busy) return;
    const { mid, result } = state;
    setState({ mid, result, busy: true });
    try {
      const done = await closeFinished(mid, result.closed.map((c) => c.pane));
      toast(t("terminal-terminalpage.closed_finished", { count: done.closed.length }), "ok");
    } catch (e) {
      toast(t("terminal-terminalpage.close_finished_failed", { error: errorText(e) }), "error");
    } finally {
      setState(null);
      reload(mid);
    }
  };
  const rows = state?.result ? state.result.closed.map((c) => ({ key: c.pane, label: finishedLabel(spacesOf(state.mid), c.pane, c.session_id, state.mid) })) : [];
  const kept = state?.result?.kept.length ?? 0;
  const dialog = (
    <Dialog
      open={Boolean(state?.result)}
      onClose={() => !state?.busy && setState(null)}
      title={t("terminal-terminalpage.close_finished_title", { count: rows.length })}
      footer={
        <>
          <Button tone="quiet" disabled={state?.busy} onClick={() => setState(null)}>
            {t("common.cancel")}
          </Button>
          <Button tone="danger" busy={state?.busy} onClick={() => void confirm()}>
            {t("terminal-terminalpage.close_finished_confirm")}
          </Button>
        </>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">{t("terminal-terminalpage.close_finished_detail")}</p>
      <ul className="mt-2 flex flex-col gap-0.5" data-finished-list>
        {rows.map((row) => (
          <li key={row.key} className="flex min-h-8 items-center gap-2 rounded-control bg-inset px-2.5 text-[12.5px] text-ink">
            <Icon name="terminal" size={13} className="shrink-0 text-ink-3" />
            <span className="min-w-0 flex-1 truncate">{row.label}</span>
          </li>
        ))}
      </ul>
      {kept > 0 && <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{t("terminal-terminalpage.close_finished_kept", { count: kept })}</p>}
    </Dialog>
  );
  /** `previewing`: the computer whose preview is out (its button spins). */
  return { start, previewing: state && !state.result ? state.mid : undefined, dialog };
}

/** A finished terminal's line in the confirmation: its row's title in the tree ("Finished: <the Agent's title>"), else
 *  the Agent's title, else a short id. */
export function finishedLabel(spaces: readonly TermSpace[], pane: string, sessionId: string, mid: string): string {
  for (const sp of spaces) for (const tab of sp.tabs) for (const p of tab.panes) if (p.pane === pane) return p.title;
  const s = sessionId ? getSession(mid, sessionId) : undefined;
  return t("lib-terminals.finished", { title: s ? sessionTitle(s) : sessionId.slice(0, 8) || pane });
}
