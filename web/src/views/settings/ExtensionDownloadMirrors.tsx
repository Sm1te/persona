import { useCallback, useLayoutEffect, useRef, useState, useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { credentialOwner, errorText, getToken, subscribeCredentialOwner } from "../../lib/api";
import { getExtensionMarketSettings, setExtensionMarketSettings, validMirrorURL, type ExtensionMarketSettings } from "../../lib/extension-market-settings";
import { Button, Loading } from "../../ui/atoms";
import { ConfigurationManager } from "./ConfigurationManager";
import { Group, Row, settingsInputClass } from "./SettingsParts";

/** Independent of the hub's updater: older hubs and failed update reads must not hide download settings. */
export function ExtensionDownloadMirrors() {
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const storageVersion = useRef(0);
  const [storageGeneration, setStorageGeneration] = useState(0);
  useLayoutEffect(() => {
    const storage = (event: StorageEvent) => {
      if (event.storageArea !== localStorage || (event.key !== "persona.token" && event.key !== null)
        || (event.key !== null && event.oldValue === event.newValue)) return;
      // Both changes may have completed in another tab before this tab reads the token again.
      setStorageGeneration(++storageVersion.current);
    };
    window.addEventListener("storage", storage);
    return () => window.removeEventListener("storage", storage);
  }, []);
  const isStorageGenerationCurrent = useCallback(() => storageVersion.current === storageGeneration, [storageGeneration]);
  return <ConfigurationManager pick title={t("settings-hubsettings.mirror_manager")} rowLabel={t("settings-hubsettings.mirror_manager_row")} hint={t("settings-hubsettings.mirror_manager_hint")} hideSingle>
    {(manager) => <MirrorEditor key={`${owner}/${manager}/${storageGeneration}`} manager={manager} owner={owner} isStorageGenerationCurrent={isStorageGenerationCurrent} />}
  </ConfigurationManager>;
}

function MirrorEditor({ manager, owner, isStorageGenerationCurrent }: { manager: string; owner: string; isStorageGenerationCurrent: () => boolean }) {
  const [token] = useState(getToken);
  const [saved, setSaved] = useState<ExtensionMarketSettings | null>(null);
  const [draft, setDraft] = useState<ExtensionMarketSettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [readError, setReadError] = useState("");
  const [writeError, setWriteError] = useState("");
  const [confirmed, setConfirmed] = useState(false);
  // A returning editor waits for dispatched writes; their results never update a replacement editor.
  const sequence = useRef(0);
  const active = useRef(false);
  const savingNow = useRef(false);
  const reads = useRef(new Set<AbortController>());
  const current = useCallback((n: number) => active.current && sequence.current === n && isStorageGenerationCurrent() && credentialOwner() === owner && getToken() === token, [isStorageGenerationCurrent, owner, token]);
  const load = useCallback(async () => {
    if (!current(sequence.current)) return;
    const n = ++sequence.current;
    const controller = new AbortController();
    reads.current.add(controller);
    const isCurrent = () => !controller.signal.aborted && current(n);
    setLoading(true);
    setReadError("");
    try {
      const value = await getExtensionMarketSettings(manager, { token, tokenOwner: owner, signal: controller.signal, isAuthContextCurrent: isCurrent });
      if (!isCurrent()) return;
      setSaved(value);
      setDraft(value);
    } catch (e) {
      if (isCurrent()) setReadError(errorText(e));
    } finally {
      reads.current.delete(controller);
      if (isCurrent()) setLoading(false);
    }
  }, [current, manager, owner, token]);
  useLayoutEffect(() => {
    active.current = true;
    void load();
    return () => { active.current = false; sequence.current++; reads.current.forEach((controller) => controller.abort()); };
  }, [load]);

  const save = async (value: ExtensionMarketSettings) => {
    if (!current(sequence.current) || !saved || loading || savingNow.current || readError) return;
    const n = ++sequence.current;
    const isCurrent = () => current(n);
    savingNow.current = true;
    setSaving(true);
    setConfirmed(false);
    setWriteError("");
    try {
      const result = await setExtensionMarketSettings(manager, value, { token, tokenOwner: owner, isAuthContextCurrent: isCurrent });
      if (!isCurrent()) return;
      setSaved(result);
      setDraft(result);
      setConfirmed(true);
    } catch (e) {
      if (isCurrent()) setWriteError(errorText(e));
    } finally {
      if (isCurrent()) { savingNow.current = false; setSaving(false); }
    }
  };
  const edit = (field: keyof ExtensionMarketSettings, value: string) => {
    if (!current(sequence.current)) return;
    setDraft((d) => d && { ...d, [field]: value });
    setConfirmed(false);
    setWriteError("");
  };
  const value = draft && { registry_mirror: draft.registry_mirror.trim(), catalog_mirror: draft.catalog_mirror.trim() };
  const invalid = !!value && (!validMirrorURL(value.registry_mirror) || !validMirrorURL(value.catalog_mirror));
  const dirty = !!saved && !!value && (value.registry_mirror !== saved.registry_mirror || value.catalog_mirror !== saved.catalog_mirror);
  const disabled = loading || saving || !saved || !!readError;

  return <Group title={t("settings-hubsettings.mirrors")} footer={t("settings-hubsettings.mirror_security")}>
    <p className="px-4 py-3 text-[12.5px] leading-relaxed text-ink-3">{t("settings-hubsettings.mirror_scope")}</p>
    {loading && <Loading />}
    {readError && <div className="flex items-center gap-3 px-4 py-3">
      <p role="alert" className="min-w-0 flex-1 text-[12.5px] break-words text-red">{t("settings-hubsettings.mirror_read_failed", { error: readError })}</p>
      <Button icon="retry" onClick={() => void load()}>{t("settings-hubsettings.retry")}</Button>
    </div>}
    {draft && <>
      <Row label={<label className="flex min-w-0 flex-col gap-1.5">
        <span className="truncate">{t("settings-hubsettings.registry_mirror")}</span>
        <input type="url" autoComplete="off" spellCheck={false} className={settingsInputClass} value={draft.registry_mirror}
          disabled={disabled} onChange={(e) => edit("registry_mirror", e.target.value)} aria-describedby="registry-mirror-hint" />
      </label>} />
      <p id="registry-mirror-hint" className="px-4 py-3 text-[12.5px] leading-relaxed text-ink-3">{t("settings-hubsettings.registry_mirror_hint")}</p>
      <Row label={<label className="flex min-w-0 flex-col gap-1.5">
        <span className="truncate">{t("settings-hubsettings.catalog_mirror")}</span>
        <input type="url" autoComplete="off" spellCheck={false} className={settingsInputClass} value={draft.catalog_mirror}
          disabled={disabled} onChange={(e) => edit("catalog_mirror", e.target.value)} aria-describedby="catalog-mirror-hint" />
      </label>} />
      <p id="catalog-mirror-hint" className="px-4 py-3 text-[12.5px] leading-relaxed text-ink-3">{t("settings-hubsettings.catalog_mirror_hint")}</p>
      {invalid && <p role="alert" className="px-4 py-3 text-[12.5px] break-words text-red">{t("settings-hubsettings.mirror_invalid")}</p>}
      {writeError && <p role="alert" className="px-4 py-3 text-[12.5px] break-words text-red">{t("settings-hubsettings.mirror_write_failed", { error: writeError })}</p>}
      {confirmed && <p role="status" className="px-4 py-3 text-[12.5px] text-green">{t("settings-hubsettings.mirror_saved")}</p>}
      <div className="flex justify-end gap-2 px-4 py-2.5">
        <Button disabled={disabled || !(saved?.registry_mirror || saved?.catalog_mirror)} onClick={() => void save({ registry_mirror: "", catalog_mirror: "" })}>
          {t("settings-hubsettings.mirror_clear")}
        </Button>
        <Button tone="accent" busy={saving} disabled={disabled || invalid || !dirty} onClick={() => value && void save(value)}>
          {t("settings-hubsettings.mirror_save")}
        </Button>
      </div>
    </>}
  </Group>;
}
