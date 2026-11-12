// Start an agent on one of your computers: machine, folder (recent ones first), the first message, and at the right of its
// box the model pill (#5104, as Cindy's new task: the user picks a model in the unified model panel, the launcher and its
// engine come with it; views/NewAgentModel.tsx), where the profile's presets (#2225), the thinking effort and Claude Code's
// ultracode switch (#3428: off each time, never remembered) also are. On top, the recent setups (#2134): a tap fills every
// field with one that started an agent before, and never starts.
import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { hubLabel } from "../lib/mesh";
import { api, ApiError, credentialOwner, enc, errorText, getToken, isAbort, subscribeCredentialOwner } from "../lib/api";
import { defaultManager, useKnownDefault } from "../lib/manager-choice";
import { pickAttachment, releaseAttachments, type LocalAttachment } from "../lib/attachments";
import type { DraftImage } from "../lib/drafts";
import { withContexts } from "../lib/context-xml";
import type { ContextRef } from "../lib/links";
import { ForwardBar } from "./chat/ForwardBar";
import { browserStartKeys, launchAgent, presetPrompt, type NewAgentPreset } from "../lib/new-agent";
import type { WorkspaceTarget } from "../lib/workspace-target";
import { AttachmentTray } from "../ui/AttachmentTray";
import { modelPurpose, modelSummary, type ModelBinding } from "../lib/model-config";
import {
  effortChoices, fitEffort, followedBinding, launcherAgent, machineLaunchers, pickerLauncher, startPicks, takesEffort, tierLeftOut, type LauncherDefault, type PickerTier,
} from "../lib/launcher-models";
import { choiceCarries, modelTitle, type ModelChoice } from "../lib/new-agent-panel";
import { readModels } from "../lib/session-models";
import { BYPASS_MODE, BYPASS_WARNING, effortTitle, modeTitle, PERMISSION_MODES } from "../lib/session-actions";
import {
  clearRecentSetups, forgetSetup, formHolds, loadRecentSetups, machineModel, recentCards, rememberSetup, setupKey, setupTime, type RecentCard, type RecentSetup,
} from "../lib/recent-setups";
import { baseName, shortPath } from "../lib/format";
import { addAgentWorktree, agentBranch, agentNameHint, suggestedPath, worktreeStatus, type WorktreeStatus } from "../lib/worktree";
import { getState, useStore } from "../lib/store";
import type { Launcher, Machine } from "../lib/types";
import { Button, cx, Dialog, IconButton, Menu, Spinner, Switch, toast, useMenu } from "../ui/atoms";
import { Pill } from "./settings/SettingsParts";
import { startBlocked, ultracodeStartOffered } from "../lib/ultracode";
import { UltracodeStartRow } from "./chat/Ultracode";
import { Icon } from "../ui/icons";
import { ProviderName } from "../ui/ProviderName";
import { t } from "../i18n";
import { useAddComputer } from "./machines/AddComputerEntry";
import { useScopedMachines } from "../lib/current-assistant";
import { currentHosts, engineName, startHosts } from "../lib/engines";
import { initialLauncher, launcherBlock } from "../lib/launcher-health";
import { navigate } from "../lib/router";
import { settingsHref } from "./settings/nav";
import { LauncherBlockedHelp } from "./LauncherBlockedHelp";
import { launcherTitle, namedLauncher, namesFromProviders, providerIdNote, providerLabel, providerTitle, type Named } from "../lib/provider-names";
import { WorkSessionForm } from "./WorkSessionForm";
import { NewAgentModel, useModelList } from "./NewAgentModel";

/** A forwarded picture (lib/drafts.ts DraftImage, base64) as a file for the first message's tray. */
function draftImageFile(img: DraftImage): File {
  const bin = atob(img.data);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new File([bytes], img.name, { type: img.mime });
}

interface Dirs {
  path: string;
  parent?: string;
  entries: { name: string; path: string; is_git?: boolean }[];
  recent?: { path: string; name: string; last_used?: string }[];
}

type NewAgentProps = { open: boolean; onClose: () => void; preset?: NewAgentPreset | null };

export function NewAgent(props: NewAgentProps) {
  if (props.preset?.workSession) return <WorkSessionForm open={props.open} onClose={props.onClose} />;
  return <AgentForm {...props} />;
}

function AgentForm({ open, onClose, preset }: NewAgentProps) {
  // with several assistants, the current one's computers: the agent started here shows in its list (docs/assistant/managers.md "Clients")
  const machines = useScopedMachines();
  const online = useMemo(() => machines.filter((m) => m.online), [machines]);
  // no computer at all (docs/assistant/onboarding.md 4.5): the "Computer" field says why it is empty and adds one, nothing else to fill in
  const loaded = useStore((s) => s.loaded);
  const none = loaded && machines.length === 0;
  const adding = useAddComputer();
  const [mid, setMid] = useState(() => localStorage.getItem("new.machine") ?? "");
  const [cwd, setCwd] = useState("");
  const [workspace, setWorkspace] = useState<WorkspaceTarget | null>(null);
  const [launcher, setLauncher] = useState("");
  const [launchers, setLaunchers] = useState<Launcher[]>([]);
  // the model configuration's providers' names (#5029): titles for a launcher ID this computer's list doesn't have (a
  // preset's, a recent setup's, the default's)
  const [providerNames, setProviderNames] = useState<Named[]>([]);
  const [unified, setUnified] = useState(true);
  const [modelDefault, setModelDefault] = useState("");
  // the hub's "Default: …" for "New agent" (#2455); undefined from an older hub
  const [hubDefault, setHubDefault] = useState<LauncherDefault | null | undefined>(undefined);
  // "Models"' "New agent" as it applies now (provider, model, effort); null: the hub has no model configuration
  const [configured, setConfigured] = useState<ModelBinding | null>(null);
  const [loadingModels, setLoadingModels] = useState(false);
  const [launchersOf, setLaunchersOf] = useState(""); // the machine `launchers` and `configured` are for
  // the grayed-out launcher whose "how to fix" is open (#1643); "": none
  const [helpFor, setHelpFor] = useState("");
  // this start's model (an id from the list; "": the default) and effort ("": the launcher's), never saved
  const [model, setModel] = useState("");
  const [effort, setEffort] = useState("");
  // the presets this computer offers (tiers of the profile in use), and the one picked: it fills the launcher, model and
  // effort as picking them by hand would; a pick by hand after it unmarks it. Never saved.
  const [tiers, setTiers] = useState<PickerTier[]>([]);
  const [tier, setTier] = useState<PickerTier | null>(null);
  // a launcher picked on the computer chosen before that this one can't run (#5104): the form went back to the default
  const [dropped, setDropped] = useState<{ name: string; computer: string } | null>(null);
  // the lists read again in this opening (once each: a list read since stays as it is)
  const readThisTime = useRef(new Set<string>());
  // ultracode for this start: off whenever the sheet opens, never saved (the server's rule: no choice carries over)
  const [ultracode, setUltracode] = useState(false);
  const features = useStore((s) => s.features);
  // Claude Code's permission mode for this start ("": the launcher's), remembered per computer like iOS's mode.<machine>
  const [mode, setMode] = useState("");
  const [host, setHost] = useState(""); // the engine host to open in (#2576); "": the machine's default
  const [dirs, setDirs] = useState<Dirs | null>(null);
  const [browse, setBrowse] = useState("");
  const [loadingDirs, setLoadingDirs] = useState(false);
  const [prompt, setPrompt] = useState(() => localStorage.getItem("draft:new-agent") ?? "");
  const keys = useMemo(browserStartKeys, []);
  const [attachments, setAttachments] = useState<LocalAttachment[]>([]);
  // a message forwarded to a new Agent (#3899, lib/forward.ts): its card over the first message, which carries it in front
  const [forward, setForward] = useState<ContextRef | null>(null);
  const [dragging, setDragging] = useState(false);
  const fileInput = useRef<HTMLInputElement>(null);
  const submitted = useRef(false);
  const preparation = useRef<AbortController | null>(null);
  const promptRevision = useRef(0);
  const credential = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const knownDefault = useKnownDefault();
  const manager = machines.find((m) => m.id === mid)?.manager || knownDefault || defaultManager();
  const targetOnline = online.some((m) => m.id === mid);
  const workspaceCurrent = !workspace || (workspace.owner === credential && workspace.manager === manager
    && workspace.machineId === mid && workspace.cwd === cwd && Boolean(workspace.host.trim() && workspace.workspaceId.trim()));
  const retirePreparation = () => {
    const previous = preparation.current;
    preparation.current = null;
    previous?.abort();
  };
  const dismiss = () => {
    retirePreparation();
    // Keep admission closed until another opening, including events in the same React batch.
    submitted.current = true;
    onClose();
  };
  // a preset's folder, put in once its computer is the chosen one (choosing a computer loads that one's last folder)
  const presetCwd = useRef<{ mid: string; cwd: string } | null>(null);
  // The recent setups (#2134), read again each time the sheet opens; the card a tap filled the form from (its mark stays
  // while the form still holds what it put in).
  const [recents, setRecents] = useState(loadRecentSetups);
  const [pickedSetup, setPickedSetup] = useState("");
  // a card for another computer (or one whose launchers are still being read): its folder and mode go in with the
  // computer, its launcher and model once that computer's launchers are read
  const pendingSetup = useRef<RecentSetup | null>(null);
  // the computer the open sheet shows, and the model choice as it stands: a pick made on one computer goes to the next
  // one picked when that one can run it (Cindy keeps a choice the target can run and falls back to its default otherwise)
  const shownMid = useRef("");
  const choiceNow = useRef<ModelChoice>({ launcher: "", model: "", effort: "" });
  choiceNow.current = { launcher, model, effort };
  // the choice was made by hand in this opening (a model, a preset, the follow row): only that one goes along; one a
  // computer's pick merely restored gives way to the next computer's own (#2134)
  const touched = useRef(false);
  // that choice on its way to the computer picked next, until its launchers are read (another pick before then passes it on)
  const carrying = useRef<ModelChoice | null>(null);
  const firstMessage = useRef<HTMLTextAreaElement>(null);
  const attachmentsRef = useRef(attachments);
  attachmentsRef.current = attachments;

  useEffect(() => {
    if (!open) {
      setForward(null); // a forward goes with the sheet it came in, not into the next one
      shownMid.current = "";
      touched.current = false;
      carrying.current = null;
      readThisTime.current.clear();
      return;
    }
    submitted.current = false;
    setEffort("");
    setTier(null);
    setDropped(null);
    setRecents(loadRecentSetups());
    setPickedSetup("");
    setUltracode(false);
    setInWorktree(false);
    setWtBranch("");
    setWtError("");
  }, [open]);
  // a worktree of its own (#6115): offered for a folder in a Git repository (what the computer says of it), off each
  // time the sheet opens
  const [inWorktree, setInWorktree] = useState(false);
  const [wtBranch, setWtBranch] = useState("");
  const [wtBusy, setWtBusy] = useState(false);
  const [wtError, setWtError] = useState("");
  // Cleanup fences completions even when the transport ignores abort. A target change starts a new admission scope.
  useLayoutEffect(() => {
    submitted.current = false;
    setWtBusy(false);
    setWtError("");
    return retirePreparation;
  }, [open, preset, mid, cwd, workspace, manager, credential, targetOnline]);
  const [repo, setRepo] = useState<WorktreeStatus | null>(null);
  useEffect(() => {
    setRepo(null);
    const dir = cwd.trim();
    if (!open || !mid || !dir || workspace) return;
    const c = new AbortController();
    const timer = setTimeout(() => void worktreeStatus(mid, dir, c.signal).then((st) => { if (!c.signal.aborted) setRepo(st); }), 400);
    return () => {
      clearTimeout(timer);
      c.abort();
    };
  }, [open, mid, cwd, workspace, manager, credential]);
  const worktreeOffered = !workspace && Boolean(repo?.path);
  useEffect(() => {
    if (!open) return;
    presetCwd.current = null;
    if (!preset) {
      setWorkspace(null);
      return;
    }
    if (preset.prompt) {
      promptRevision.current++;
      setPrompt((draft) => presetPrompt(preset.prompt, draft));
    }
    if (preset.forward) setForward(preset.forward);
    if (preset.images?.length) {
      const picked: LocalAttachment[] = [];
      for (const img of preset.images) {
        try { picked.push(pickAttachment(draftImageFile(img))); }
        catch (e) { toast(errorText(e), "error"); }
      }
      setAttachments((current) => [...current, ...picked]);
    }
    const target = preset.workspace;
    setWorkspace(target ? { ...target, owner: target.owner ?? credentialOwner(),
      manager: target.manager ?? (getState().machines.find((m) => m.id === target.machineId)?.manager || defaultManager()) } : null);
    if (target) {
      presetCwd.current = { mid: target.machineId, cwd: target.cwd };
      setMid(target.machineId);
      setCwd(target.cwd);
      setInWorktree(false);
      return;
    }
    if (preset.machineId && preset.cwd && online.some((m) => m.id === preset.machineId)) {
      presetCwd.current = { mid: preset.machineId, cwd: preset.cwd };
      setMid(preset.machineId);
      setCwd(preset.cwd);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, preset]);
  useEffect(() => {
    if (prompt) localStorage.setItem("draft:new-agent", prompt);
    else localStorage.removeItem("draft:new-agent");
  }, [prompt]);
  useEffect(() => () => releaseAttachments(attachmentsRef.current), []);

  const addFiles = (files: FileList | File[]) => {
    const picked: LocalAttachment[] = [];
    for (const file of Array.from(files)) {
      try { picked.push(pickAttachment(file)); }
      catch (e) { toast(errorText(e), "error"); }
    }
    setAttachments((current) => [...current, ...picked]);
  };

  useEffect(() => {
    // a preset's computer (set just above, in the same commit) is not overridden by the first one online
    if (open && !workspace && !preset?.workspace && !presetCwd.current && (!mid || !online.some((m) => m.id === mid)) && online[0]) setMid(online[0].id);
  }, [open, online, mid, workspace, preset]);

  // The launcher a computer's pick puts in once its launchers are read, and the model that goes with it (#2134): a card's own
  // when its launcher is still there; else the choice made on the computer shown before when this one can run it (#5104);
  // else the model configuration (`effective`; null: not followed) or the last one used here, unless this computer can't
  // run it now (#1643: then the first that can), with this computer's latest model for it in the recent setups. No model
  // list for it (the configuration runs no launcher): no model.
  const restoreLauncher = (machine: string, card: RecentSetup | null, ls: Launcher[], effective: ModelBinding | null, carry: ModelChoice | null = null) => {
    if (!card && carry && (carry.launcher || effective !== null) && choiceCarries(carry, ls)) {
      touched.current = true; // still the user's: it goes on to the computer after this one too
      setLauncher(carry.launcher);
      setModel(carry.model);
      setEffort(carry.effort);
      return;
    }
    if (!card && carry?.launcher) {
      // what was picked can't run here: this computer's default, said under the box
      setDropped({ name: launcherTitle(carry.launcher, [...ls, ...providerNames]), computer: machines.find((m) => m.id === machine)?.name ?? machine });
      setEffort("");
    }
    const own = card && (card.launcher ? ls.some((l) => l.name === card.launcher) : effective !== null);
    const chosen = own ? card.launcher : effective !== null ? "" : initialLauncher(localStorage.getItem(`new.launcher.${machine}`), ls);
    const runs = pickerLauncher(chosen, followedBinding(chosen, effective));
    const picked = !runs ? "" : own ? card.model : machineModel(loadRecentSetups(), machine, chosen);
    setLauncher(chosen);
    setModel(picked);
  };

  useEffect(() => {
    if (!open || !mid) return;
    localStorage.setItem("new.machine", mid);
    // another computer picked while the sheet is open: the choice made on the one before goes along if this one can run it
    const before = shownMid.current;
    shownMid.current = mid;
    const carry = before && before !== mid ? (carrying.current ?? (touched.current ? choiceNow.current : null)) : null;
    carrying.current = carry;
    touched.current = false;
    setLaunchers([]);
    setProviderNames([]);
    setHelpFor("");
    setDropped(null);
    const controller = new AbortController();
    // nothing of the computer before goes to this one unless restoreLauncher puts it back (a read that fails leaves none)
    setLauncher("");
    setModel("");
    setTiers([]);
    setTier(null);
    setModelDefault("");
    setHubDefault(undefined);
    setConfigured(null);
    setLoadingModels(true);
    Promise.all([
      machineLaunchers(mid, { signal: controller.signal }),
      modelPurpose("agent.new").catch((e) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    ])
      .then(([r, config]) => {
        if (controller.signal.aborted) return;
        const ls = r.launchers ?? [];
        setLaunchers(ls);
        setHubDefault(r.default);
        setTiers(r.tiers ?? []);
        setLaunchersOf(mid);
        setUnified(config !== null);
        if (config) {
          setModelDefault(modelSummary(config.purpose, config.providers));
          setConfigured(config.purpose.effective ?? {});
          setProviderNames(namesFromProviders(config.providers));
        }
        const card = pendingSetup.current?.machine === mid ? pendingSetup.current : null;
        if (card) pendingSetup.current = null;
        carrying.current = null;
        restoreLauncher(mid, card, ls, config ? (config.purpose.effective ?? {}) : null, carry);
      })
      .catch((e) => { if (!controller.signal.aborted) toast(errorText(e), "error"); })
      .finally(() => { if (!controller.signal.aborted) setLoadingModels(false); });
    const p = presetCwd.current;
    if (p?.mid === mid) presetCwd.current = null;
    const card = pendingSetup.current?.machine === mid ? pendingSetup.current : null;
    setCwd(card ? card.cwd : p?.mid === mid ? p.cwd : (localStorage.getItem(`new.cwd.${mid}`) ?? ""));
    setMode(card?.agent_type === "claude" ? card.mode : (localStorage.getItem(`new.mode.${mid}`) ?? ""));
    setBrowse("");
    return () => {
      controller.abort();
      setLaunchersOf("");
    };
  }, [open, mid]);

  useEffect(() => {
    if (!open || !mid || workspace) return;
    const controller = new AbortController();
    setLoadingDirs(true);
    api<Dirs>(`/machines/${enc(mid)}/dirs?path=${enc(browse)}`, { signal: controller.signal })
      .then((dirs) => { if (!controller.signal.aborted) setDirs(dirs); })
      .catch((error) => { if (!controller.signal.aborted && !isAbort(error)) setDirs(null); })
      .finally(() => { if (!controller.signal.aborted) setLoadingDirs(false); });
    return () => controller.abort();
  }, [open, mid, browse, workspace, manager, credential]);

  // The model list of the launcher that will run (the chosen one, or the model configuration's), read on each opening and
  // kept for the page's life with the chat's (lib/session-models.ts): the pill names its model, a start sends what it
  // takes. Starting never waits for it.
  const followed = followedBinding(launcher, unified ? configured : null);
  const listLauncher = launchersOf === mid ? pickerLauncher(launcher, followed) : "";
  useEffect(() => {
    if (!open || !mid || !listLauncher) return;
    const key = `${mid}/${listLauncher}`;
    void readModels(mid, listLauncher, { again: !readThisTime.current.has(key), first: true });
    readThisTime.current.add(key);
  }, [open, mid, listLauncher]);
  const models = useModelList(open ? mid : "", listLauncher);
  // an older hub has no list (null): no model is picked
  useEffect(() => {
    if (models.list === null) setModel("");
  }, [models.list]);
  // A choice made by hand (a model or the follow row picked in the panel, a preset, Try anyway): the launcher, model and
  // effort it runs on. A preset is marked until something else is picked (picking its own setup again keeps it).
  const pickModel = (c: ModelChoice, preset: PickerTier | null = null) => {
    touched.current = true;
    carrying.current = null;
    setHelpFor("");
    setDropped(null);
    if (preset || c.launcher !== launcher || c.model !== model || c.effort !== effort) setTier(preset);
    setLauncher(c.launcher);
    setModel(c.model);
    setEffort(c.effort);
  };
  // a preset: the launcher, model and effort it fills in (the model is checked once the list is in)
  const pickTier = (x: PickerTier) => pickModel({ launcher: x.launcher, model: x.model ?? "", effort: x.effort ?? "" }, x);
  const followDefault = () => pickModel({ launcher: "", model: "", effort: "" });

  // Where it opens, when the machine enables more than one engine (#2576).
  const picked = online.find((m) => m.id === mid);
  const hosts = picked ? startHosts(picked) : [];
  const hostDefault = picked ? currentHosts(picked).default : "";
  useEffect(() => { setHost(""); }, [open, mid]);
  const agent = launcherAgent(listLauncher, launchers, models.list);
  // the launcher this start runs (the chosen one, or the one Model settings' default runs) when this computer can't start
  // it now (#1643): said under the box with a way to see how to fix it; Start stays enabled
  const runs = launchersOf === mid ? listLauncher : "";
  const runsBlock = runs ? launcherBlock(launchers.find((l) => l.name === runs)) : null;
  const defaultRuns = unified && launchersOf === mid ? pickerLauncher("", followedBinding("", configured)) : "";
  const defaultBlock = defaultRuns ? launcherBlock(launchers.find((l) => l.name === defaultRuns)) : null;
  const helpLauncher = helpFor ? launchers.find((l) => l.name === helpFor) : undefined;
  const helpBlock = helpLauncher ? launcherBlock(helpLauncher) : null;
  // what a launcher ID is called (#5029): this computer's launchers first, then the model configuration's providers
  const names: Named[] = [...launchers, ...providerNames];
  const efforts = effortChoices(models.list, model, followed);
  const effortsKey = efforts.join(" ");
  useEffect(() => { setEffort((e) => fitEffort(e, effortsKey ? effortsKey.split(" ") : [])); }, [effortsKey]);
  // offered with Claude Code whose effort can be set here; grayed when this computer's Claude Code is known to be too old
  const ultracodeShown = !loadingModels && efforts.length > 0 && ultracodeStartOffered(features, agent);
  // what of the picked preset this launcher can't take, said once its list is in
  const tierMissed = tier && listLauncher === tier.launcher && models.list !== undefined ? tierLeftOut(tier, { agent, list: models.list, followed }) : [];
  // the follow row's second line: what Model settings › New agent runs (the hub's default, by display names)
  const defaultList = useModelList(open ? mid : "", defaultRuns).list;
  const followDetail = hubDefault
    ? [hubDefault.provider && launcherTitle(hubDefault.provider, names), hubDefault.model && modelTitle(defaultList, hubDefault.model), hubDefault.effort && effortTitle(hubDefault.effort)].filter(Boolean).join(" · ")
    : modelDefault;
  const ultracodeBlocked = ultracodeShown ? startBlocked(picked?.claude_version) : "";

  // The recent setups' cards (#2134): not when another page opened the sheet on a folder of its own (a space's or a
  // knowledge entry's New agent), which a card would replace.
  const cards = useMemo(() => recentCards(recents, machines), [recents, machines]);
  const presetFolder = Boolean(preset?.machineId && preset.cwd);
  const settled = launchersOf === mid && !loadingModels;
  const pickedCard = pickedSetup ? cards.find((c) => setupKey(c.setup) === pickedSetup) : undefined;
  const pickedShown = pickedCard && (!settled || formHolds(pickedCard.setup, { machine: mid, cwd, launcher, model, mode, effort })) ? pickedSetup : "";
  // A tap fills every field and puts the focus in the first message; Start stays the user's.
  const pickSetup = ({ setup, online: up }: RecentCard) => {
    if (!up || !online.some((m) => m.id === setup.machine)) return;
    setPickedSetup(setupKey(setup));
    setTier(null);
    touched.current = false;
    carrying.current = null;
    setEffort(setup.agent_type === "claude" ? setup.effort : "");
    pendingSetup.current = null;
    if (setup.machine !== mid) {
      pendingSetup.current = setup;
      setMid(setup.machine);
    } else {
      setCwd(setup.cwd);
      setBrowse("");
      if (setup.agent_type === "claude") setMode(setup.mode);
      if (launchersOf === mid) restoreLauncher(mid, setup, launchers, unified ? configured : null);
      else pendingSetup.current = setup; // the launchers being read put it in
    }
    setDropped(null);
    setHelpFor("");
    firstMessage.current?.focus();
  };

  const start = () => {
    if (!open || submitted.current || loadingModels || !workspaceCurrent || !online.some((m) => m.id === mid) || !cwd.trim()) return;
    // Read ownership at admission too: credential/store changes can precede React's next render.
    if (workspace && (workspace.owner !== credentialOwner()
      || workspace.manager !== (getState().machines.find((m) => m.id === mid)?.manager || defaultManager()))) return;
    submitted.current = true;
    const controller = new AbortController();
    preparation.current = controller;
    const owner = credentialOwner();
    const token = getToken();
    const revision = promptRevision.current;
    const sent = forward ? withContexts([forward], prompt, attachments.some((a) => a.mime.startsWith("image/"))) : prompt.trim();
    const snapshot = { text: sent, attachments: [...attachments] };
    const current = () => preparation.current === controller && !controller.signal.aborted && owner === credentialOwner()
      && getState().machines.some((m) => m.id === mid && m.online)
      && manager === (getState().machines.find((m) => m.id === mid)?.manager || defaultManager());
    localStorage.setItem(`new.cwd.${mid}`, cwd.trim());
    if (launcher && !unified) localStorage.setItem(`new.launcher.${mid}`, launcher);
    const picks = startPicks({ agent, list: models.list, model, effort, followed });
    // a Codex keeps its own modes; the choice stays for this computer's next Claude Code
    const permissionMode = takesEffort(agent) ? mode : "";
    if (takesEffort(agent)) {
      if (mode) localStorage.setItem(`new.mode.${mid}`, mode);
      else localStorage.removeItem(`new.mode.${mid}`);
    }
    const opensIn = workspace?.host ?? (host || undefined);
    // this start as a recent setup (#2134): the folder picked (not the worktree made from it), the model and effort sent;
    // mode and effort only for Claude Code
    const claude = agent === "claude";
    const setup = { machine: mid, cwd: cwd.trim(), launcher, model: picks.model ?? "", agent_type: agent, mode: claude ? permissionMode : "", effort: claude ? (picks.effort ?? "") : "" };
    // only an explicit "on" is sent: off is what a new Claude is anyway
    const ultra = ultracodeShown && !ultracodeBlocked && ultracode ? true : undefined;
    const launch = (dir: string) => {
      if (!current()) return;
      // The launch owns its previews; removing or unmounting the editable draft cannot revoke them.
      const sent = snapshot.attachments.map((a) => ({ ...a, url: a.url ? URL.createObjectURL(a.file) : undefined }));
      rememberSetup({ ...setup, at: setupTime() });
      launchAgent({ machineId: mid, cwd: dir, workspaceId: workspace?.workspaceId, launcher, ...picks, permissionMode: permissionMode || undefined, host: opensIn, ultracode: ultra, text: snapshot.text, attachments: sent });
      const ids = new Set(snapshot.attachments.map((a) => a.id));
      releaseAttachments(attachmentsRef.current.filter((a) => ids.has(a.id)));
      attachmentsRef.current = attachmentsRef.current.filter((a) => !ids.has(a.id));
      setAttachments(attachmentsRef.current);
      if (promptRevision.current === revision) setPrompt("");
      setForward(null);
      dismiss();
    };
    // in a worktree of its own (#6115): made first; a failure says why here, and nothing starts
    if (!workspace && inWorktree && worktreeOffered) {
      setWtBusy(true);
      setWtError("");
      void addAgentWorktree(mid, cwd.trim(), agentNameHint(prompt, cwd), wtBranch, { signal: controller.signal, token, tokenOwner: owner })
        .then(
          (wt) => launch(wt.path),
          (e) => {
            if (!current()) return;
            submitted.current = false;
            setWtError(errorText(e));
          },
        )
        .finally(() => {
          if (!current()) return;
          preparation.current = null;
          setWtBusy(false);
        });
      return;
    }
    launch(workspace ? workspace.cwd : cwd.trim());
  };

  if (none)
    return (
      <>
        <Dialog
          open={open}
          onClose={dismiss}
          title={t("newagent.title")}
          width={520}
          footer={
            <Button tone="quiet" onClick={dismiss}>
              {t("common.cancel")}
            </Button>
          }
        >
          <Field label={t("newagent.computer")}>
            <div data-testid="new-agent-no-computer" className="flex flex-col items-start gap-3 rounded-control bg-inset px-3.5 py-3 shadow-hairline">
              <div className="flex items-start gap-2.5">
                <Icon name="machine" size={15} className="mt-0.5 shrink-0 text-ink-3" />
                <div className="flex min-w-0 flex-col gap-0.5">
                  <span className="text-[13px] font-medium text-ink">{t("newagent.no_computer_title")}</span>
                  <span className="text-[12.5px] leading-relaxed text-ink-2">{t("newagent.no_computer_body")}</span>
                </div>
              </div>
              <Button
                tone="primary"
                icon="plus"
                onClick={() => {
                  dismiss(); // the add flow is a dialog of its own, not one over this
                  adding.open();
                }}
              >
                {t("machines.add")}
              </Button>
            </div>
          </Field>
        </Dialog>
        {adding.dialog}
      </>
    );

  return (
    <>
      <Dialog
        open={open}
        onClose={dismiss}
        title={t("newagent.title")}
        width={520}
        footer={
          <>
            <span className="mr-auto min-w-0 truncate text-[11.5px] text-ink-3">{cwd ? shortPath(cwd) : t("newagent.pick_folder")}</span>
            <Button tone="quiet" onClick={dismiss}>
              {t("common.cancel")}
            </Button>
            <Button tone="primary" busy={wtBusy} disabled={loadingModels || !workspaceCurrent || !online.some((m) => m.id === mid) || !cwd.trim()} onClick={start}>
              {t("newagent.start")}
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-4">
          {cards.length > 0 && !presetFolder && (
            <RecentSetups
              names={names}
              cards={cards}
              machines={machines}
              picked={pickedShown}
              onPick={pickSetup}
              onForget={(setup) => setRecents(forgetSetup(setup))}
              onClear={() => {
                setRecents(clearRecentSetups());
                setPickedSetup("");
              }}
            />
          )}
          <Field label={t("newagent.computer")}>
            <div className="flex flex-wrap gap-1.5">
              {machines.map((m) => (
                <button
                  key={m.id}
                  type="button"
                  disabled={Boolean(workspace) || !m.online}
                  onClick={() => setMid(m.id)}
                  className={cx(
                    "flex h-8 items-center gap-1.5 rounded-control px-2.5 text-[12.5px] whitespace-nowrap transition-[background-color,box-shadow,color]",
                    workspace && mid === m.id ? "disabled:opacity-100" : "disabled:opacity-40",
                    mid === m.id ? "bg-ink font-medium text-surface" : "bg-surface text-ink-2 shadow-btn enabled:hover:bg-hover enabled:hover:text-ink",
                  )}
                >
                  <span className={cx("size-1.5 rounded-full", m.online ? "bg-green" : "bg-ink-3")} />
                  {m.name}
                  {hubLabel(m, machines) && <span className={mid === m.id ? "opacity-70" : "text-ink-3"}>{hubLabel(m, machines)}</span>}
                </button>
              ))}
            </div>
          </Field>

          {workspace ? <Field label={t("newagent-workspace.destination")}>
            <div data-testid="new-agent-workspace" className="flex min-w-0 flex-col gap-1.5 rounded-control bg-inset px-3 py-2.5 text-[12.5px] text-ink shadow-hairline">
              <span className="font-medium [overflow-wrap:anywhere]">{workspace.label || baseName(workspace.cwd) || t("newagent-workspace.destination")}</span>
              <span className="font-mono text-ink-2 [overflow-wrap:anywhere]">{workspace.cwd}</span>
              <span className="text-ink-2 [overflow-wrap:anywhere]">{engineName(workspace.host, picked)}</span>
              <p className="text-[11.5px] text-ink-3">{t("newagent-workspace.existing_hint")}</p>
              {!workspaceCurrent && <p role="alert" className="text-red">{t("newagent-workspace.owner_changed")}</p>}
              <Button tone="quiet" onClick={() => {
                retirePreparation();
                presetCwd.current = null;
                setWorkspace(null);
                setHost("");
                setInWorktree(false);
                setRepo(null);
              }}>{t("newagent-workspace.choose_folder")}</Button>
            </div>
          </Field> : <Field label={t("newagent.folder")}>
            <input
              value={cwd}
              onChange={(e) => setCwd(e.target.value)}
              placeholder={t("newagent.folder_placeholder")}
              spellCheck={false}
              className="h-9 w-full rounded-control bg-inset px-3 font-mono text-[12.5px] text-ink shadow-hairline outline-none placeholder:font-sans placeholder:text-ink-3 focus:shadow-[0_0_0_1px_var(--line-strong)]"
            />
            <div className="mt-1.5 max-h-52 overflow-y-auto rounded-control bg-surface p-1 shadow-hairline">
              {!browse && (dirs?.recent?.length ?? 0) > 0 && (
                <>
                  <div className="px-2 pt-1 pb-0.5 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">{t("newagent.recent")}</div>
                  {dirs!.recent!.slice(0, 6).map((d) => (
                    <DirRow key={d.path} name={d.name || baseName(d.path)} path={d.path} sub={shortPath(d.path)} selected={cwd === d.path} onPick={() => setCwd(d.path)} />
                  ))}
                </>
              )}
              <div className="flex items-center gap-1 px-2 pt-1.5 pb-0.5 text-[10.5px] font-medium tracking-[0.06em] text-ink-3 uppercase">
                {browse && dirs?.parent !== undefined && (
                  <button type="button" aria-label={t("newagent.parent_folder")} onClick={() => setBrowse(dirs!.parent!)} className="-ml-1 flex size-4.5 items-center justify-center rounded-[4px] hover:bg-hover hover:text-ink">
                    <Icon name="chevronLeft" size={12} stroke={2.4} />
                  </button>
                )}
                <span className="truncate normal-case tracking-normal">{dirs?.path ? shortPath(dirs.path) : t("newagent.home_folder")}</span>
                {loadingDirs && <Spinner size={10} />}
              </div>
              {dirs?.entries?.map((d) => (
                <DirRow key={d.path} name={d.name} path={d.path} git={d.is_git} selected={cwd === d.path} onPick={() => setCwd(d.path)} onOpen={() => setBrowse(d.path)} />
              ))}
            </div>
          </Field>}
          {worktreeOffered && (
            <div data-testid="new-agent-worktree" className="-mt-2 rounded-control bg-surface px-3 py-2 shadow-hairline">
              <div className="flex items-center gap-2">
                <Icon name="branch" size={13} stroke={2.2} className="shrink-0 text-ink-2" />
                <span className="min-w-0 flex-1 truncate text-[12.5px] font-medium text-ink">{t("newagent.worktree")}</span>
                <Switch on={inWorktree} onChange={setInWorktree} label={t("newagent.worktree")} />
              </div>
              <p className="mt-1 pl-[21px] text-[11.5px] leading-snug text-ink-3">{t("newagent.worktree_hint")}</p>
              {inWorktree && (
                <label className="mt-1.5 flex items-center gap-2 pl-[21px]">
                  <span className="shrink-0 text-[11.5px] text-ink-3">{t("newagent.worktree_branch")}</span>
                  <input
                    value={wtBranch}
                    onChange={(e) => {
                      setWtBranch(e.target.value);
                      setWtError("");
                    }}
                    placeholder={agentBranch(agentNameHint(prompt, cwd))}
                    spellCheck={false}
                    data-worktree-branch
                    className="h-7 min-w-0 flex-1 rounded-control bg-inset px-2 font-mono text-[12px] text-ink shadow-hairline outline-none placeholder:text-ink-3 focus:shadow-[0_0_0_1px_var(--line-strong)]"
                  />
                </label>
              )}
              {inWorktree && repo?.checkouts && (
                <p data-worktree-path className="mt-1 truncate pl-[21px] font-mono text-[11px] text-ink-3">
                  {t("newagent.worktree_path", { path: shortPath(suggestedPath(repo.checkouts, wtBranch.trim() || agentBranch(agentNameHint(prompt, cwd)))) })}
                </p>
              )}
              {wtError && <p role="alert" className="mt-1 pl-[21px] text-[11.5px] leading-snug text-red">{t("newagent.worktree_failed", { error: wtError })}</p>}
            </div>
          )}

          {!workspace && hosts.length > 1 && (
            <Field label={t("lib-engines.start_in")}>
              <div role="radiogroup" aria-label={t("lib-engines.start_in")} data-testid="start-host" className="flex flex-wrap gap-1.5">
                {hosts.map((h) => (
                  <Choice key={h} radio selected={(host || hostDefault) === h} onClick={() => setHost(h)}>
                    {engineName(h, picked)}
                    {h === hostDefault ? ` · ${t("lib-engines.default")}` : ""}
                  </Choice>
                ))}
              </div>
            </Field>
          )}

          {takesEffort(agent) && (
            <Field label={t("newagent.permission_mode")}>
              <div role="radiogroup" aria-label={t("newagent.permission_mode")} className="flex flex-wrap gap-1.5">
                <Choice radio selected={mode === ""} onClick={() => setMode("")}>{t("newagent.follow_launcher")}</Choice>
                {PERMISSION_MODES.map((m) => (
                  <Choice key={m.id} radio selected={mode === m.id} onClick={() => setMode(m.id)} title={m.detail}>
                    {m.title}
                  </Choice>
                ))}
              </div>
              <p className={cx("mt-1.5 text-[11.5px]", mode === BYPASS_MODE ? "text-red" : "text-ink-3")}>
                {mode === BYPASS_MODE ? BYPASS_WARNING : PERMISSION_MODES.find((m) => m.id === mode)?.detail ?? t("newagent.permission_mode_hint")}
              </p>
            </Field>
          )}

          <Field label={t("newagent.first_message_optional")}>
            <div className={cx("flex flex-col gap-2 rounded-control bg-inset shadow-hairline focus-within:shadow-[0_0_0_1px_var(--line-strong)]", dragging && "outline-2 outline-accent")}
              onDragOver={(e) => { if (e.dataTransfer.types.includes("Files")) { e.preventDefault(); setDragging(true); } }}
              onDragLeave={(e) => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setDragging(false); }}
              onDrop={(e) => { e.preventDefault(); setDragging(false); addFiles(e.dataTransfer.files); }}>
              {forward && <div className="px-2 pt-2" data-new-agent-forward><ForwardBar forward={forward} onRemove={() => setForward(null)} /></div>}
              {attachments.length > 0 && <div className="px-3 pt-2"><AttachmentTray attachments={attachments} onRemove={(id) => {
                releaseAttachments(attachments.filter((a) => a.id === id));
                setAttachments((current) => current.filter((a) => a.id !== id));
              }} /></div>}
              <textarea
                ref={firstMessage}
                value={prompt}
                onChange={(e) => { promptRevision.current++; setPrompt(e.target.value); }}
                onPaste={(e) => { if (e.clipboardData.files.length) { e.preventDefault(); addFiles(e.clipboardData.files); } }}
                onKeyDown={(e) => {
                  if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                  if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); start(); }
                }}
                rows={3}
                aria-label={t("newagent.first_message")}
                placeholder={keys ? t("newagent.prompt_placeholder_keys", { keys }) : t("newagent.prompt_placeholder")}
                className="w-full resize-none bg-transparent px-3 py-2 text-[13px] leading-relaxed text-ink outline-none placeholder:text-ink-3"
              />
              <div className="flex items-center gap-2 px-2 pb-2">
                <input ref={fileInput} type="file" multiple hidden onChange={(e) => { if (e.currentTarget.files) addFiles(e.currentTarget.files); e.currentTarget.value = ""; }} />
                <button type="button" aria-label={t("newagent.add_files")} title={t("newagent.add_files")} onClick={() => fileInput.current?.click()} className="flex size-7 shrink-0 items-center justify-center rounded-control text-ink-3 hover:bg-hover hover:text-ink"><Icon name="plus" size={16} stroke={2} /></button>
                {/* the hint gives way to the model pill (on a phone it is only the + button's tooltip) */}
                <span title={t("newagent.attach_hint")} className="min-w-0 flex-1 truncate text-[11.5px] text-ink-3 max-sm:hidden">{t("newagent.attach_hint")}</span>
                <span className="ml-auto flex min-w-0 shrink justify-end" data-testid="new-agent-model">
                  <NewAgentModel
                    mid={mid}
                    launchers={launchers}
                    ready={settled}
                    choice={{ launcher, model, effort }}
                    runs={runs}
                    configured={unified ? configured : null}
                    tiers={tiers}
                    follow={unified ? { label: t("newagent.follow_row"), active: launcher === "", detail: followDetail, warning: defaultBlock?.reason } : undefined}
                    fallback={modelDefault || t("chat-modelpill.default_model")}
                    ultracode={ultracodeShown ? <div data-testid="new-agent-ultracode"><UltracodeStartRow on={ultracode} onChange={setUltracode} blocked={ultracodeBlocked} /></div> : undefined}
                    ultracodeOn={ultracodeShown && !ultracodeBlocked && ultracode}
                    onChoice={pickModel}
                    onPreset={(name) => {
                      const x = tiers.find((v) => v.name === name);
                      if (x) pickTier(x);
                    }}
                    onFollow={followDefault}
                    onHelp={setHelpFor}
                    onConfigure={() => {
                      onClose();
                      navigate(settingsHref("models", null, false, null, "providers"));
                    }}
                  />
                </span>
              </div>
            </div>
            <ModelNotes>
              {dropped && (
                <Note data-testid="new-agent-dropped" icon="info">{t("newagent.carry_dropped", { name: dropped.name, computer: dropped.computer })}</Note>
              )}
              {runs && models.list === undefined && models.error && !models.loading && (
                <Note data-testid="new-agent-models-failed" icon="warning" tone="orange">{t("newagent.models_failed")}</Note>
              )}
              {runsBlock && (
                // what runs this time can't start here (#1643): one flow of words that wraps (a phone has no hover), the link a unit
                <Note data-testid="new-agent-blocked" icon="warning" tone="orange">
                  <Pill tone="orange">{t("newagent.runs_this_time")}</Pill>{" "}
                  <ProviderName of={namedLauncher(runs, names)} className="text-ink-2" idClassName="text-[11px]" /> <span className="text-ink-3">{runsBlock.reason}</span>{" "}
                  <button
                    type="button"
                    aria-expanded={helpFor === runs}
                    aria-controls={helpFor === runs ? "new-agent-blocked-help" : undefined}
                    aria-label={t("newagent.how_to_fix_named", { name: launcherTitle(runs, names) })}
                    onClick={() => setHelpFor((open) => (open === runs ? "" : runs))}
                    className="whitespace-nowrap text-accent-ink hover:underline"
                  >
                    {t("newagent.how_to_fix")}
                  </button>
                </Note>
              )}
              {tier && (
                <Note data-testid="new-agent-preset-hint" icon={tierMissed.length ? "warning" : "layers"} tone={tierMissed.length ? "orange" : undefined}>
                  {tierMissed.length
                    ? t("newagent.preset_left_out", { what: tierMissed.map((v) => (v === tier.effort ? effortTitle(v) : v)).join(" · ") })
                    : t("newagent.preset_picked", { name: tier.name, what: tier.description || tierRuns(tier, names) })}
                </Note>
              )}
            </ModelNotes>
            {helpLauncher && helpBlock && (
              <LauncherBlockedHelp
                launcher={helpLauncher}
                block={helpBlock}
                onTryAnyway={() => pickModel({ launcher: helpLauncher.name, model: "", effort: "" })}
                onAddProvider={() => {
                  onClose();
                  navigate("/settings?tab=models&add=1"); // the provider wizard, as readiness's connect_model
                }}
                onClose={() => setHelpFor("")}
              />
            )}
          </Field>
        </div>
      </Dialog>
      {adding.dialog}
    </>
  );
}

/** The lines under the first message box about the model choice (what was dropped, what can't start here, the preset);
 *  nothing when there are none. */
function ModelNotes({ children }: { children: React.ReactNode }) {
  const lines = (Array.isArray(children) ? children : [children]).filter(Boolean);
  if (!lines.length) return null;
  return <div aria-live="polite" className="mt-1.5 flex flex-col gap-1">{lines}</div>;
}

function Note({ icon, tone, children, ...rest }: { icon: "info" | "warning" | "layers"; tone?: "orange"; children: React.ReactNode; "data-testid"?: string }) {
  return (
    <p {...rest} className={cx("flex min-w-0 items-start gap-1.5 text-[11.5px] leading-snug", tone === "orange" ? "text-orange" : "text-ink-3")}>
      <Icon name={icon} size={12} className={cx("mt-px shrink-0", tone === "orange" && "text-orange")} />
      <span className="min-w-0 [overflow-wrap:anywhere]">{children}</span>
    </p>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="mb-1.5 text-[12px] font-medium text-ink-2">{label}</div>
      {children}
    </div>
  );
}

// The recent setups (#2134): up to three cards in a row, each a setup that started an agent (folder · computer, then launcher
// · model · mode · effort); an offline computer's is dimmed and can't be picked. The ⋯ removes one or clears them all.
function RecentSetups({ cards, machines, names, picked, onPick, onForget, onClear }: { cards: RecentCard[]; machines: Machine[]; /** What a launcher ID is called (#5029). */ names: Named[]; /** The card the form holds (setupKey); "": none. */ picked: string; onPick: (card: RecentCard) => void; onForget: (setup: RecentSetup) => void; onClear: () => void }) {
  const menu = useMenu();
  const title = (s: RecentSetup) => `${baseName(shortPath(s.cwd))} · ${machines.find((m) => m.id === s.machine)?.name ?? s.machine}`;
  return (
    <div data-testid="recent-setups">
      <div className="mb-1.5 flex h-[18px] items-center justify-between gap-2">
        <span className="text-[12px] font-medium text-ink-2">{t("newagent.recent_setups")}</span>
        <IconButton icon="more" size={22} iconSize={14} label={t("newagent.recent_options")} onClick={menu.open} active={Boolean(menu.anchor)} />
        {menu.anchor && (
          <Menu
            anchor={menu.anchor}
            onClose={menu.close}
            align="right"
            width={232}
            items={[
              { key: "remove", label: t("newagent.recent_remove"), icon: "x", onSelect: () => {}, children: cards.map((c) => ({ key: setupKey(c.setup), label: title(c.setup), onSelect: () => onForget(c.setup) })) },
              { key: "clear", label: t("newagent.recent_clear"), icon: "trash", danger: true, divider: true, onSelect: onClear },
            ]}
          />
        )}
      </div>
      <div className="grid grid-cols-1 gap-1.5 sm:grid-cols-3">
        {cards.map((c) => {
          const s = c.setup;
          const on = picked === setupKey(s);
          const claude = s.agent_type === "claude";
          // the launcher by its display name (#5029), its startup ID in the card's tooltip; an rc alias keeps its ID in mono
          const runsOn = s.launcher ? namedLauncher(s.launcher, names) : null;
          const runsId = runsOn ? providerIdNote(runsOn) : "";
          const parts: React.ReactNode[] = [
            runsOn ? <span key="l" className={cx(!runsId && "font-mono")}>{providerTitle(runsOn)}</span> : t("newagent.follow_model_settings"),
            s.model && <span key="m" className="font-mono">{s.model}</span>,
            // "Allow everything" said with the warning it has in the form
            claude && s.mode && (s.mode === BYPASS_MODE
              ? <span key="p" className="inline-flex items-center gap-0.5 font-medium text-orange"><Icon name="warning" size={11} className="shrink-0" />{modeTitle(s.mode)}</span>
              : modeTitle(s.mode)),
            claude && s.effort && effortTitle(s.effort),
            !c.online && t("machines.offline"),
          ].filter(Boolean);
          return (
            <button
              key={setupKey(s)}
              type="button"
              data-recent-setup
              data-bypass={(claude && s.mode === BYPASS_MODE) || undefined}
              aria-pressed={on}
              disabled={!c.online}
              title={[shortPath(s.cwd), runsId].filter(Boolean).join("\n")}
              onClick={() => onPick(c)}
              className={cx(
                "flex min-w-0 flex-col gap-0.5 rounded-control px-2.5 py-2 text-left transition-[background-color,box-shadow,opacity] disabled:opacity-45",
                on ? "bg-accent-tint shadow-[0_0_0_1.5px_var(--accent)]" : "bg-surface shadow-btn enabled:hover:bg-hover",
              )}
            >
              <span className="flex min-w-0 items-baseline gap-1 text-[12.5px] leading-snug">
                <span className={cx("min-w-0 shrink truncate font-medium", on ? "text-accent-ink" : "text-ink")}>{baseName(shortPath(s.cwd))}</span>
                <span className="shrink-0 text-ink-3">·</span>
                <span className="min-w-0 shrink-[2] truncate text-ink-2">{machines.find((m) => m.id === s.machine)?.name ?? s.machine}</span>
              </span>
              <span className="line-clamp-2 text-[11.5px] leading-snug text-ink-3">
                {parts.map((p, i) => (
                  <span key={i}>
                    {i > 0 && " · "}
                    {p}
                  </span>
                ))}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}

/** What a preset runs on, in a line: its launcher ("Title (id)", #5029), model and effort (its name). */
function tierRuns(x: PickerTier, names: Named[]): string {
  return [providerLabel(namedLauncher(x.launcher, names)), x.model, x.effort && effortTitle(x.effort)].filter(Boolean).join(" · ");
}

function Choice({ selected, onClick, title, radio, children }: { selected: boolean; onClick: () => void; title?: string; /** One of a radiogroup. */ radio?: boolean; children: React.ReactNode }) {
  return (
    <button
      type="button"
      role={radio ? "radio" : undefined}
      aria-checked={radio ? selected : undefined}
      aria-pressed={radio ? undefined : selected}
      onClick={onClick}
      title={title}
      className={cx(
        "h-7 rounded-control px-2.5 text-[12px] whitespace-nowrap transition-colors",
        selected ? "bg-ink text-surface" : "bg-surface text-ink-2 shadow-btn hover:bg-hover hover:text-ink",
      )}
    >
      {children}
    </button>
  );
}

// A folder to start in: picked with a click, opened with a double click or its chevron.
function DirRow({ name, path, sub, git, selected, onPick, onOpen }: { name: string; path: string; sub?: string; git?: boolean; selected: boolean; onPick: () => void; onOpen?: () => void }) {
  return (
    <div className={cx("group/dir flex h-8 items-center gap-2 rounded-[6px] px-2 transition-colors pointer-coarse:min-h-11", selected ? "bg-accent-tint" : "hover:bg-hover")}>
      <button type="button" onClick={onPick} onDoubleClick={onOpen} className="flex min-w-0 flex-1 items-center gap-2 self-stretch text-left" title={path}>
        <Icon name={git ? "git" : "folder"} size={13} className={selected ? "text-accent-ink" : "text-ink-3"} />
        <span className={cx("shrink-0 truncate text-[12.5px]", selected ? "font-medium text-accent-ink" : "text-ink")}>{name}</span>
        {sub && <span className="min-w-0 truncate font-mono text-[11px] text-ink-3">{sub}</span>}
      </button>
      {onOpen && (
        <button type="button" aria-label={t("newagent.open_folder", { name })} onClick={onOpen} className="touch-hit flex size-5 items-center justify-center rounded-[4px] text-ink-3 opacity-0 transition-opacity group-hover/dir:opacity-100 hover:bg-hover-2 hover:text-ink focus-visible:opacity-100 [@media(hover:none)]:opacity-100">
          <Icon name="chevronRight" size={12} stroke={2.4} />
        </button>
      )}
    </div>
  );
}
