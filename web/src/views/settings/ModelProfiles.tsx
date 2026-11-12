import { useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { ApiError, credentialOwner, errorText, getToken, subscribeCredentialOwner } from "../../lib/api";
import { onDefaultManager, onManagerChoice, shownManager } from "../../lib/manager-choice";
import type { ModelProvider } from "../../lib/model-config";
import { modelProfiles, rollbackModelProfile, switchModelProfile, type ModelProfiles as Profiles } from "../../lib/model-profiles";
import { namedLauncher, namesFromProviders, providerIdNote, providerTitle } from "../../lib/provider-names";
import { onHubEvent } from "../../lib/store";
import { t } from "../../i18n";
import { Button, Dialog } from "../../ui/atoms";
import { Group, Pill, Row } from "./SettingsParts";

// A dispatched write can outlive its view and credential. Admit at most one per manager, including across remounts,
// so a late server commit cannot silently undo a later action from this browser.
const pendingWrites = new Set<string>();
const writeListeners = new Set<() => void>();
const subscribeWrites = (listener: () => void) => { writeListeners.add(listener); return () => { writeListeners.delete(listener); }; };
const writesChanged = () => writeListeners.forEach((listener) => listener());
let managerGeneration = 0;
onManagerChoice(() => { managerGeneration++; });
onDefaultManager(() => { managerGeneration++; });
const profileContext = () => JSON.stringify([credentialOwner(), managerGeneration]);
const subscribeProfileContext = (listener: () => void) => {
  const stops = [subscribeCredentialOwner(listener), onManagerChoice(listener), onDefaultManager(listener)];
  return () => stops.forEach((stop) => stop());
};

/** `providers`: the model configuration's, to name a tier's launcher by its display name (#5029). */
export function ModelProfiles({ manager, providers, onChanged }: { manager?: string; providers?: ModelProvider[]; onChanged: () => void }) {
  const context = useSyncExternalStore(subscribeProfileContext, profileContext, profileContext);
  const owner = credentialOwner();
  const target = manager ?? shownManager();
  return <ModelProfilesForOwner key={JSON.stringify([target, context])} manager={target} owner={owner} context={context} providers={providers} onChanged={onChanged} />;
}

function ModelProfilesForOwner({ manager, owner, context, providers, onChanged }: { manager: string; owner: string; context: string; providers?: ModelProvider[]; onChanged: () => void }) {
  const [data, setData] = useState<Profiles | null>(null);
  const [readError, setReadError] = useState("");
  const [actionError, setActionError] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const busy = useSyncExternalStore(subscribeWrites, () => pendingWrites.has(manager), () => false);
  const token = useRef(getToken()).current;
  const mounted = useRef(false);
  const readNumber = useRef(0);
  const readAbort = useRef<AbortController | null>(null);
  const interaction = useRef(0);
  const changed = useRef(onChanged);
  changed.current = onChanged;
  const current = () => mounted.current && profileContext() === context && credentialOwner() === owner;
  const invalidateRead = () => { readNumber.current++; readAbort.current?.abort(); readAbort.current = null; };
  useLayoutEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; invalidateRead(); };
  }, []);
  const load = async () => {
    invalidateRead();
    const number = readNumber.current;
    const edit = interaction.current;
    const controller = new AbortController();
    readAbort.current = controller;
    const isCurrent = () => current() && number === readNumber.current;
    try {
      const result = await modelProfiles(manager, { token, tokenOwner: owner, isAuthContextCurrent: isCurrent, signal: controller.signal });
      if (isCurrent()) { setData(result); if (edit === interaction.current) setReadError(""); }
    } catch (e) {
      if (isCurrent() && edit === interaction.current && !(e instanceof ApiError && e.status === 404)) setReadError(errorText(e));
    } finally {
      if (isCurrent()) readAbort.current = null;
    }
  };
  useEffect(() => {
    void load();
    let wasPending = pendingWrites.has(manager);
    const stopWrites = subscribeWrites(() => {
      const pending = pendingWrites.has(manager);
      // A write from this or a retired view may have committed after our last read.
      if (wasPending && !pending && current()) void load();
      wasPending = pending;
    });
    const stopEvents = onHubEvent("model_profile", () => {
      if (!current()) return;
      // Read after the pending write settles: an event read during it can precede its server commit.
      if (!pendingWrites.has(manager)) void load();
      changed.current();
    });
    return () => { stopWrites(); stopEvents(); invalidateRead(); };
  }, []);
  const apply = async (id?: string) => {
    if (!current() || pendingWrites.has(manager)) return;
    pendingWrites.add(manager);
    writesChanged();
    invalidateRead();
    const edit = ++interaction.current;
    setReadError(""); setActionError("");
    const context = { token, tokenOwner: owner, isAuthContextCurrent: current };
    try {
      const result = await (id ? switchModelProfile(id, manager, context) : rollbackModelProfile(manager, context));
      if (current()) {
        setData(result);
        if (edit === interaction.current) setSelected(null);
        changed.current();
      }
    } catch (e) {
      if (current() && edit === interaction.current) setActionError(errorText(e));
    } finally {
      pendingWrites.delete(manager);
      writesChanged();
    }
  };
  const clearErrors = () => { interaction.current++; setReadError(""); setActionError(""); };
  const error = actionError || readError;
  const profile = data?.profiles.find((p) => p.id === selected);
  const tiers = Object.entries(profile?.tiers ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const names = namesFromProviders(providers);
  if (!data && !error) return null; // old hubs do not expose profiles
  return <>
    <Group title={t("settings-modelprofiles.title")}
      footer={data && !data.active ? `${t("settings-modelprofiles.footer")} ${t("settings-modelprofiles.none_active")}` : t("settings-modelprofiles.footer")}>
      {data?.profiles.map((p) => <Row key={p.id} label={p.name} hint={p.description} onClick={() => { setSelected(p.id); clearErrors(); }}>
        {p.active && <Pill>{t("settings-modelprofiles.active")}</Pill>}
      </Row>)}
      {data?.rollback && <Row label={t("settings-modelprofiles.rollback")} hint={data.rollback.to || t("settings-modelprofiles.custom")}>
        <Button tone="quiet" busy={busy} onClick={() => void apply()}>{t("settings-modelprofiles.rollback")}</Button>
      </Row>}
    </Group>
    {!profile && error && <p role="alert" className="mb-4 text-[13px] text-red">{error}</p>}
    <Dialog open={!!profile} title={profile?.name ?? ""} onClose={() => { if (!busy) { setSelected(null); clearErrors(); } }} footer={<>
      <Button tone="quiet" disabled={busy} onClick={() => { setSelected(null); clearErrors(); }}>{t("common.cancel")}</Button>
      <Button busy={busy} onClick={() => profile && void apply(profile.id)}>{t("settings-modelprofiles.apply")}</Button>
    </>}>
      <p className="mb-4 text-[12px] text-ink-3">{profile?.description}</p>
      <p className="mb-4 text-[12px] text-ink-3">{t("settings-modelprofiles.changes", { count: profile?.changes.length ?? 0 })}</p>
      <p className="mb-1.5 text-[12px] font-semibold text-ink-2">{t("settings-modelprofiles.tiers")}</p>
      {tiers.length ? (
        <ul data-testid="model-profile-tiers" className="mb-4 flex flex-col gap-1.5">
          {tiers.map(([name, x]) => {
            // the launcher by its display name, its startup ID after it in small mono (#5029); an rc alias by its ID
            const l = namedLauncher(x.launcher, names);
            const id = providerIdNote(l);
            return <li key={name} className="rounded-control bg-field px-3 py-2 text-[12px]">
              <div className="flex min-w-0 items-baseline gap-2">
                <span className="shrink-0 font-mono text-ink">{name}</span>
                <span className="min-w-0 [overflow-wrap:anywhere] text-ink-3">
                  {providerTitle(l)}
                  {id && <> <span className="font-mono text-[11px]">{id}</span></>}
                  {[x.model, x.effort].filter(Boolean).map((v) => ` · ${v}`).join("")}
                </span>
              </div>
              {x.description && <p className="mt-0.5 text-ink-2">{x.description}</p>}
            </li>;
          })}
        </ul>
      ) : <p className="mb-4 text-[12px] text-ink-3">{t("settings-modelprofiles.no_tiers")}</p>}
      <details>
        <summary className="cursor-pointer text-[12px] text-ink-3">{t("settings-modelprofiles.details")}</summary>
        <pre className="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-all rounded-control bg-field p-3 text-[12px] text-ink">{JSON.stringify({ purposes: profile?.purposes, launchers: profile?.launchers }, null, 2)}</pre>
      </details>
      {error && <p role="alert" className="mt-3 text-[12px] text-red">{error}</p>}
    </Dialog>
  </>;
}
