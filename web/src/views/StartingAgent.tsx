import { useEffect, useState } from "react";
import { shortPath } from "../lib/format";
import { deliverLaunch, type AgentLaunch } from "../lib/new-agent";
import { namedLauncher, providerIdNote, providerLabel, providerTitle, useProviderNames } from "../lib/provider-names";
import { navigate } from "../lib/router";
import { useStore, putLaunch } from "../lib/store";
import { releaseAttachments } from "../lib/attachments";
import { AttachmentTray } from "../ui/AttachmentTray";
import { Button, Loading } from "../ui/atoms";
import { Icon } from "../ui/icons";
import { t } from "../i18n";

export function StartingAgent({ launch, onToggleSidebar }: { launch: AgentLaunch; onToggleSidebar?: () => void }) {
  const machine = useStore((s) => s.machines.find((m) => m.id === launch.machineId));
  const [text, setText] = useState(launch.text);
  const [launcher, setLauncher] = useState(launch.launcher);
  // the launcher by the provider's display name; the line's tooltip says it with its startup ID (#5029)
  const runsOn = namedLauncher(launch.launcher, useProviderNames());
  const subtitle = (name: string) => [machine?.name ?? launch.machineId, shortPath(launch.cwd), name, launch.model].filter(Boolean).join(" · ");
  const editable = launch.phase === "failed" && !launch.session && !launch.uncertain && !launch.repeatOnly;
  useEffect(() => {
    if (launch.phase === "ready" && launch.session) navigate({ name: "session", mid: launch.machineId, sid: launch.session.session_id }, true);
  }, [launch]);
  const retry = () => {
    if (launch.uncertain && !confirm(launch.session ? t("startingagent.resend_confirm") : t("startingagent.restart_confirm"))) return;
    // the model and effort were picked for the launcher it had: another launcher starts with its own
    const other = launcher.trim() !== launch.launcher;
    if (editable) putLaunch({ ...launch, text: text.trim(), launcher: launcher.trim(), outgoing: undefined, ...(other && { model: undefined, effort: undefined }) });
    void deliverLaunch(launch.id, launch.clearable);
  };
  const label = launch.phase === "creating" ? t("startingagent.creating") : launch.phase === "uploading" ? t("startingagent.uploading") : t("startingagent.sending");
  return (
    <div className="flex h-full min-w-0 flex-col">
      <header className="flex h-14 shrink-0 items-center gap-2 border-b border-line bg-page/85 px-3 backdrop-blur-md sm:px-4">
        {onToggleSidebar && <button type="button" aria-label={t("startingagent.sidebar")} onClick={onToggleSidebar} className="flex size-7 items-center justify-center rounded-control text-ink-3 hover:bg-hover lg:hidden"><Icon name="sidebar" /></button>}
        <div className="min-w-0 flex-1"><h1 className="truncate text-[14px] font-semibold text-ink">{t("startingagent.title")}</h1><p className="truncate text-[11.5px] text-ink-3" title={providerIdNote(runsOn) ? subtitle(providerLabel(runsOn)) : undefined}>{subtitle(providerTitle(runsOn))}</p></div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-[760px] flex-col gap-7 px-5 pt-6 pb-10 sm:px-8">
          {(launch.text || launch.attachments.length > 0) && <div className="flex flex-col items-end gap-1.5 pl-[12%]">
            <AttachmentTray attachments={launch.attachments} onRemove={editable ? (id) => {
              releaseAttachments(launch.attachments.filter((a) => a.id === id));
              putLaunch({ ...launch, attachments: launch.attachments.filter((a) => a.id !== id), outgoing: undefined });
            } : undefined} />
            {launch.text && <div className="max-w-full rounded-[14px] bg-user-bubble px-3.5 py-2 text-[14px] leading-[1.55] whitespace-pre-wrap text-ink [overflow-wrap:anywhere]">{launch.text}</div>}
          </div>}
          {launch.phase === "failed" ? <div role="alert" className="flex flex-col items-start gap-3">
            <p className="text-[13px] text-red">{launch.error}</p>
            <p className="text-[12px] text-ink-3">{launch.uncertain ? t("startingagent.uncertain") : t("startingagent.kept")}</p>
            {editable && <div className="flex w-full flex-col gap-2">
              <label className="text-[12px] text-ink-2">{t("startingagent.launcher_label")}<input value={launcher} onChange={(e) => setLauncher(e.target.value)} className="mt-1 w-full rounded-control bg-inset px-3 py-2 font-mono text-[12px] text-ink shadow-hairline outline-none" /></label>
              <label className="text-[12px] text-ink-2">{t("startingagent.first_message")}<textarea value={text} onChange={(e) => setText(e.target.value)} rows={3} className="mt-1 w-full resize-none rounded-control bg-inset px-3 py-2 text-[13px] text-ink shadow-hairline outline-none" /></label>
            </div>}
            {launch.draft && <pre className="max-h-40 max-w-full overflow-auto rounded-control bg-inset px-3 py-2 text-[12px] whitespace-pre-wrap text-ink-2">{launch.draft}</pre>}
            <div className="flex flex-wrap gap-2">
              <Button icon="retry" onClick={retry}>{launch.clearable !== undefined ? t("startingagent.clear_and_send") : t("startingagent.retry")}</Button>
              {launch.session && <Button tone="quiet" onClick={() => navigate({ name: "session", mid: launch.machineId, sid: launch.session!.session_id })}>{t("startingagent.view_session")}</Button>}
              {!launch.session && <Button tone="quiet" onClick={() => navigate({ name: "home" })}>{t("startingagent.view_list")}</Button>}
            </div>
          </div> : <div className="flex flex-col gap-2"><Loading label={label} /><p className="text-[12px] text-ink-3">{t("startingagent.background")}</p></div>}
        </div>
      </div>
    </div>
  );
}
