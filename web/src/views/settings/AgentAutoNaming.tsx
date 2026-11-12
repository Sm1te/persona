import { useEffect, useRef, useState } from "react";
import { t } from "../../i18n";
import { api, credentialOwner, getToken } from "../../lib/api";
import { MANAGER_HEADER } from "../../lib/manager-choice";
import { Row, SettingsHint, settingsInputClass, Switch } from "./SettingsParts";

export interface AgentAutoNamingState {
  manager?: string;
  agent_auto_naming?: boolean;
  agent_auto_naming_max?: number;
  agent_auto_naming_interval_hours?: number;
}

const MAX_NAMES = 1000;
const MAX_HOURS = 8760;
const DEFAULT_INTERVAL_HOURS = 24;
const whole = (value: unknown, min: number, max: number): value is number => typeof value === "number" && Number.isInteger(value) && value >= min && value <= max;

/** Field presence is the capability check: older hubs must not appear to support these controls. */
export function supportsAgentAutoNaming(info: AgentAutoNamingState | null): boolean {
  return typeof info?.agent_auto_naming === "boolean" && whole(info.agent_auto_naming_max, 1, MAX_NAMES)
    && whole(info.agent_auto_naming_interval_hours, 0, MAX_HOURS);
}

/** Reuses the Assistant page's read; only an owner-matching, echoed save advances its displayed values. */
export function AgentAutoNaming({ info, manager, owner, onSaved }: {
  info: AgentAutoNamingState; manager: string; owner: string; onSaved: (info: AgentAutoNamingState) => void;
}) {
  const [token] = useState(getToken);
  const lifetime = useRef(0);
  const admission = useRef(false);
  const [busy, setBusy] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState("");
  useEffect(() => { lifetime.current++; return () => { lifetime.current++; }; }, []);
  const enabled = info.agent_auto_naming!;
  const maximum = info.agent_auto_naming_max!;
  const interval = info.agent_auto_naming_interval_hours!;
  const disabled = busy || info.manager !== manager || owner !== credentialOwner();
  const save = async (patch: Partial<AgentAutoNamingState>) => {
    if (admission.current || disabled || !supportsAgentAutoNaming(info)) return;
    admission.current = true;
    const generation = lifetime.current;
    const current = () => generation === lifetime.current && owner === credentialOwner();
    setBusy(true); setError("");
    try {
      const result = await api<AgentAutoNamingState>("/assistant", {
        method: "PUT", json: patch, token, tokenOwner: owner, headers: { [MANAGER_HEADER]: manager },
      });
      if (!current()) return;
      if (result.manager !== manager || !supportsAgentAutoNaming(result)
        || Object.entries(patch).some(([key, value]) => result[key as keyof AgentAutoNamingState] !== value)) {
        throw new Error("Unconfirmed agent naming settings");
      }
      onSaved({ agent_auto_naming: result.agent_auto_naming, agent_auto_naming_max: result.agent_auto_naming_max,
        agent_auto_naming_interval_hours: result.agent_auto_naming_interval_hours });
    } catch {
      if (current()) setError(t("settings-assistantsettings.auto_naming_save_failed"));
    } finally {
      if (current()) { admission.current = false; setBusy(false); setAttempt((n) => n + 1); }
    }
  };
  return <>
    <Row label={t("settings-assistantsettings.auto_naming")} hint={t("settings-assistantsettings.auto_naming_hint")} wrapHint>
      <Switch label={t("settings-assistantsettings.auto_naming")} on={enabled} disabled={disabled} onChange={(on) => void save({ agent_auto_naming: on })} />
    </Row>
    {enabled && <>
      <Row label={t("settings-assistantsettings.auto_naming_max")} hint={t("settings-assistantsettings.auto_naming_max_hint")} wrapHint stackOnNarrow>
        <NamingNumber key={`max-${attempt}-${maximum}`} value={maximum} max={MAX_NAMES} disabled={disabled}
          label={t("settings-assistantsettings.auto_naming_max")} onCommit={(value) => void save({ agent_auto_naming_max: value })} />
      </Row>
      <Row label={t("settings-assistantsettings.auto_naming_periodic")} hint={t("settings-assistantsettings.auto_naming_periodic_hint")} wrapHint>
        <Switch label={t("settings-assistantsettings.auto_naming_periodic")} on={interval !== 0} disabled={disabled}
          onChange={(on) => void save({ agent_auto_naming_interval_hours: on ? DEFAULT_INTERVAL_HOURS : 0 })} />
      </Row>
      {interval !== 0 && <>
        <Row label={t("settings-assistantsettings.auto_naming_interval")} hint={t("settings-assistantsettings.auto_naming_interval_hint")} wrapHint stackOnNarrow>
          <NamingNumber key={`interval-${attempt}-${interval}`} value={interval} max={MAX_HOURS} disabled={disabled}
            label={t("settings-assistantsettings.auto_naming_interval")} onCommit={(value) => void save({ agent_auto_naming_interval_hours: value })} />
        </Row>
        {maximum === 1 && <SettingsHint caution>{t("settings-assistantsettings.auto_naming_raise_max")}</SettingsHint>}
      </>}
    </>}
    {error && <SettingsHint alert>{error}</SettingsHint>}
  </>;
}

function NamingNumber({ value, max, disabled, label, onCommit }: {
  value: number; max: number; disabled: boolean; label: string; onCommit: (value: number) => void;
}) {
  const [draft, setDraft] = useState(String(value));
  const commit = () => {
    const number = Number(draft);
    if (draft.trim() === "" || !whole(number, 1, max)) { setDraft(String(value)); return; }
    if (number !== value) onCommit(number);
  };
  return <input type="number" inputMode="numeric" min={1} max={max} step={1} value={draft} disabled={disabled}
    aria-label={label} className={`${settingsInputClass} text-right tabular-nums`} onChange={(e) => setDraft(e.target.value)}
    onBlur={commit} onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); if (e.key === "Escape") { e.preventDefault(); setDraft(String(value)); } }} />;
}
