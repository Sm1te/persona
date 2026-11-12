import { useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { agentName } from "../lib/agent-kind";
import { api, ApiError, credentialOwner, enc, errorText, pastHub, subscribeCredentialOwner } from "../lib/api";
import { pickAttachment, releaseAttachments, type LocalAttachment } from "../lib/attachments";
import { useScopedMachines } from "../lib/current-assistant";
import { shortPath } from "../lib/format";
import { launcherBlock } from "../lib/launcher-health";
import { machineLaunchers, modelName, type AgentModel } from "../lib/launcher-models";
import { hubLabel } from "../lib/mesh";
import { launchAgent } from "../lib/new-agent";
import { providerLabel } from "../lib/provider-names";
import { getState } from "../lib/store";
import type { Launcher } from "../lib/types";
import { AttachmentTray } from "../ui/AttachmentTray";
import { Button, Dialog, Spinner, toast } from "../ui/atoms";
import { useAddComputer } from "./machines/AddComputerEntry";

interface Dirs {
  path: string;
  parent?: string;
  entries: { name: string; path: string; is_git?: boolean }[];
  recent?: { path: string; name: string }[];
}

interface StartOptions {
  agent_type: string;
  model_mode: "selectable" | "fixed" | "default";
  models?: AgentModel[];
  fixed_model?: string;
  prompt_required: boolean;
  inline_prompt: boolean;
  attachments: boolean;
}

const input = "h-9 w-full min-w-0 rounded-control bg-inset px-3 text-[13px] text-ink shadow-hairline outline-none focus:shadow-[0_0_0_1px_var(--line-strong)] disabled:opacity-50";
const label = "flex min-w-0 flex-col gap-1.5 text-[12px] font-medium text-ink-2";

type Load<T> = { key: string; data?: T; error?: string };
function useTarget<T>(key: string, read: (signal: AbortSignal) => Promise<T>): Load<T> | undefined {
  const [result, setResult] = useState<Load<T>>();
  useLayoutEffect(() => {
    setResult(undefined);
    if (!key) return;
    const controller = new AbortController();
    void read(controller.signal).then(
      (data) => { if (!controller.signal.aborted) setResult({ key, data }); },
      (error) => { if (!controller.signal.aborted) setResult({ key, error: workSessionError(error) }); },
    );
    return () => controller.abort();
    // read only uses the values in this request's key
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);
  return result?.key === key ? result : undefined;
}

function workSessionError(e: unknown) {
  if (e instanceof ApiError && e.code === "work_launcher_unavailable") return t("work-session.launcher_unavailable");
  return errorText(e);
}

export function WorkSessionForm({ open, onClose }: { open: boolean; onClose: () => void }) {
  const machines = useScopedMachines();
  const adding = useAddComputer();
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const [mid, setMid] = useState("");
  const [cwd, setCwd] = useState("");
  const [browse, setBrowse] = useState("");
  const [harness, setHarness] = useState("");
  const [launcher, setLauncher] = useState("");
  const [model, setModel] = useState("");
  const [prompt, setPrompt] = useState("");
  const [attachments, setAttachments] = useState<LocalAttachment[]>([]);
  const [revision, setRevision] = useState(0);
  const files = useRef<HTMLInputElement>(null);
  const submitted = useRef(false);
  const keptFiles = useRef(attachments);
  keptFiles.current = attachments;
  useLayoutEffect(() => () => releaseAttachments(keptFiles.current), []);
  useLayoutEffect(() => { if (open) submitted.current = false; }, [open]);

  const machine = machines.find((m) => m.id === mid);
  const offline = !!mid && (!machine?.online || machine.trust === "pending");
  const target = open && machine?.online && machine.trust !== "pending" ? JSON.stringify([owner, mid, machine.manager, machine.engine, machine.engine_default, revision]) : "";
  const dirPath = `/machines/${enc(mid)}/dirs?path=`;
  const dirs = useTarget(target && JSON.stringify([target, browse]), (signal) => api<Dirs>(dirPath + enc(browse), { signal }));
  const project = useTarget(target && cwd && JSON.stringify([target, cwd]), (signal) => api<Dirs>(dirPath + enc(cwd), { signal }));
  const launcherList = useTarget(target, (signal) => machineLaunchers(mid, { signal }));
  const groups = new Map<string, Launcher[]>();
  for (const l of launcherList?.data?.launchers ?? []) {
    if (launcherBlock(l)) continue;
    const agent = l.agent_type || l.agent || "claude";
    if (!groups.has(agent)) groups.set(agent, []);
    groups.get(agent)!.push(l);
  }
  const matching = groups.get(harness) ?? [];
  const validLauncher = matching.some((l) => l.name === launcher);
  const optionsKey = target && project?.data && validLauncher ? JSON.stringify([target, cwd, harness, launcher]) : "";
  const options = useTarget(optionsKey, (signal) => api<StartOptions>(
    `/machines/${enc(mid)}/launchers/${enc(launcher)}/start-options`, { signal, timeout: pastHub(85_000) },
  ));
  const opts = options?.data;
  const models = opts?.model_mode === "selectable" ? (opts.models ?? []) : [];
  const compatible = opts?.agent_type === harness;
  // Reconnecting may change what the same launcher can run. A removed model
  // should be picked again rather than silently replaced with the default.
  const staleModel = !!opts && !!model && !models.some((m) => m.id === model);
  const canStart = !!target && !!project?.data && validLauncher && compatible && !staleModel
    && (!opts?.prompt_required || !!prompt.trim()) && (opts?.attachments || attachments.length === 0);

  const chooseLauncher = (name: string) => { setLauncher(name); setModel(""); };
  const chooseHarness = (agent: string) => {
    setHarness(agent);
    const list = groups.get(agent) ?? [];
    chooseLauncher(list.length === 1 ? list[0].name : "");
  };
  const chooseDevice = (id: string) => {
    setMid(id);
    setBrowse("");
    setCwd("");
    setHarness("");
    chooseLauncher("");
  };
  const close = () => { submitted.current = true; onClose(); };
  const add = (list: FileList) => {
    const picked: LocalAttachment[] = [];
    for (const file of Array.from(list)) {
      try { picked.push(pickAttachment(file)); }
      catch (e) { toast(errorText(e), "error"); }
    }
    setAttachments((current) => [...current, ...picked]);
  };
  const start = () => {
    const live = getState().machines.find((m) => m.id === mid);
    if (!canStart || submitted.current || !opts || !live?.online || live.trust === "pending"
      || live.manager !== machine?.manager || live.engine !== machine?.engine || live.engine_default !== machine?.engine_default
      || credentialOwner() !== owner) return;
    submitted.current = true;
    launchAgent({ machineId: mid, cwd: project!.data!.path, launcher, agentType: harness, workSession: true,
      model: model || undefined, inlinePrompt: opts.inline_prompt, text: prompt.trim(), attachments });
    keptFiles.current = []; // The launch owns the attachment previews after this point.
    setAttachments([]);
    setPrompt("");
    onClose();
  };

  const listed = dirs?.data;
  const entries = listed?.entries ?? [];
  const recent = !browse ? (listed?.recent ?? []).filter((d) => d.path !== listed?.path && !entries.some((e) => e.path === d.path)) : [];
  const visible = cwd === listed?.path || entries.some((d) => d.path === cwd) || recent.some((d) => d.path === cwd);
  const err = dirs?.error || project?.error || launcherList?.error || options?.error;

  return <>
    <Dialog open={open} onClose={close} title={t("personaassistant.start_work_session")} width={520} footer={<>
      <Button tone="quiet" onClick={close}>{t("common.cancel")}</Button>
      <Button tone="primary" disabled={!canStart} onClick={start}>{t("newagent.start")}</Button>
    </>}>
      <div className="flex flex-col gap-4">
        <label className={label}>{t("newagent.computer")}
          <select className={input} value={mid} onChange={(e) => chooseDevice(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {mid && !machine && <option value={mid} disabled>{mid}</option>}
            {machines.map((m) => <option key={m.id} value={m.id} disabled={!m.online || m.trust === "pending"}>
              {[m.name, hubLabel(m, machines), !m.online ? t("machines.offline") : ""].filter(Boolean).join(" · ")}
            </option>)}
          </select>
        </label>
        {machines.length === 0 && <div><Button icon="plus" onClick={() => { close(); adding.open(); }}>{t("machines.add")}</Button></div>}
        {offline && <p role="status" className="text-[13px] text-orange">{t(machine?.trust === "pending" ? "work-session.unconfirmed" : "work-session.offline")}</p>}
        <div className="flex flex-col gap-1.5">
          <label className={label}>{t("work-session.project")}
            <select className={input} value={cwd} disabled={!target || !listed} onChange={(e) => setCwd(e.target.value)}>
              <option value="">{t("work-session.choose")}</option>
              {!visible && cwd && <option value={cwd}>{shortPath(cwd)}</option>}
              {listed?.path && <option value={listed.path}>{shortPath(listed.path)}</option>}
              {recent.length > 0 && <optgroup label={t("newagent.recent")}>{recent.map((d) => <option key={d.path} value={d.path}>{shortPath(d.path)}</option>)}</optgroup>}
              {entries.map((d) => <option key={d.path} value={d.path}>{d.name}{d.is_git ? " · Git" : ""}</option>)}
            </select>
          </label>
          {target && <div className="flex flex-wrap items-center gap-1">
            <Button tone="quiet" icon="chevronLeft" disabled={!listed?.parent || listed.parent === listed.path} onClick={() => setBrowse(listed!.parent!)}>{t("newagent.parent_folder")}</Button>
            <Button tone="quiet" icon="folder" disabled={!cwd || !project?.data || cwd === listed?.path} onClick={() => setBrowse(cwd)}>{t("work-session.browse")}</Button>
            {!dirs && <Spinner size={12} />}
          </div>}
        </div>
        <label className={label}>{t("work-session.harness")}
          <select className={input} value={harness} disabled={!project?.data || !groups.size} onChange={(e) => chooseHarness(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {harness && !groups.has(harness) && <option value={harness} disabled>{agentName(harness)}</option>}
            {[...groups.keys()].map((agent) => <option value={agent} key={agent}>{agentName(agent)}</option>)}
          </select>
        </label>
        {project?.data && launcherList?.data && !groups.size && <p role="status" className="text-[13px] text-ink-3">{t("work-session.no_launchers")}</p>}
        <label className={label}>{t("work-session.launcher")}
          <select className={input} value={launcher} disabled={!project?.data || !matching.length} onChange={(e) => chooseLauncher(e.target.value)}>
            <option value="">{t("work-session.choose")}</option>
            {launcher && !validLauncher && <option value={launcher} disabled>{launcher}</option>}
            {matching.map((l) => <option value={l.name} key={l.name}>{providerLabel(l)}</option>)}
          </select>
        </label>
        <label className={label}>{t("work-session.model")}
          <select aria-label={t("work-session.model")} className={input} value={model}
            disabled={!compatible || (opts?.model_mode !== "selectable" && !staleModel)} onChange={(e) => setModel(e.target.value)}>
            <option value="">{!opts ? t(optionsKey && !options ? "work-session.checking" : "work-session.choose")
              : opts.model_mode === "fixed" ? t("work-session.fixed", { model: opts.fixed_model ?? "" })
              : t("work-session.default")}</option>
            {model && !models.some((m) => m.id === model) && <option value={model} disabled>{model}</option>}
            {models.map((m) => <option key={m.id} value={m.id}>{modelName(m)}{m.resolved ? ` · ${m.resolved}` : ""}</option>)}
          </select>
          {opts?.model_mode === "default" && <span className="font-normal text-ink-3">{t("work-session.no_override")}</span>}
          {opts?.model_mode === "selectable" && <span className="font-normal text-ink-3">{t("work-session.one_start")}</span>}
          {staleModel && <span role="status" className="font-normal text-orange">{t("work-session.model_changed")}</span>}
        </label>
        {opts && !compatible && <p role="alert" className="text-[13px] text-red">{t("work-session.harness_changed")}</p>}
        {err && <div role="alert" className="flex flex-wrap items-center gap-2 text-[13px] text-red">
          <span>{err}</span><Button tone="quiet" icon="retry" onClick={() => setRevision((n) => n + 1)}>{t("startingagent.retry")}</Button>
        </div>}
        <label className={label}>{t(opts?.prompt_required ? "newagent.first_message" : "newagent.first_message_optional")}
          <textarea aria-label={t("newagent.first_message")} value={prompt} rows={3}
            className="w-full resize-none rounded-control bg-inset px-3 py-2 text-[13px] font-normal text-ink shadow-hairline outline-none"
            onChange={(e) => setPrompt(e.target.value)}
            onDragOver={(e) => { if (opts?.attachments && e.dataTransfer.types.includes("Files")) e.preventDefault(); }}
            onDrop={(e) => { if (opts?.attachments && e.dataTransfer.files.length) { e.preventDefault(); add(e.dataTransfer.files); } }}
            onPaste={(e) => { if (opts?.attachments && e.clipboardData.files.length) { e.preventDefault(); add(e.clipboardData.files); } }}
            onKeyDown={(e) => {
              if (!e.nativeEvent.isComposing && e.keyCode !== 229 && e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                start();
              }
            }} />
        </label>
        {attachments.length > 0 && <AttachmentTray attachments={attachments} onRemove={(id) => {
          releaseAttachments(attachments.filter((a) => a.id === id));
          setAttachments((current) => current.filter((a) => a.id !== id));
        }} />}
        {opts?.attachments && <div>
          <input ref={files} type="file" multiple hidden onChange={(e) => { if (e.currentTarget.files) add(e.currentTarget.files); e.currentTarget.value = ""; }} />
          <Button icon="plus" tone="quiet" onClick={() => files.current?.click()}>{t("newagent.add_files")}</Button>
        </div>}
        {opts && !opts.attachments && attachments.length > 0 && <p role="status" className="text-[13px] text-orange">{t("work-session.remove_attachments")}</p>}
      </div>
    </Dialog>
    {adding.dialog}
  </>;
}
