// Settings › Model providers › Add / Edit (#2455, docs/assistant/onboarding.md 4.3 "Three paths" B): (1) "Choose a provider" lists what the hub offers
// (GET /api/v1/launchers/presets: its built-in providers, then the extensions'), a compatible gateway typed by hand last.
// A preset needs only ② the key: it is tested on its own (POST /launchers/test with the preset) and saved with
// assign, and (3) says what now runs on it. The gateway goes (2) "Connect" (a display name in any language, address, auth, key, the
// startup ID under "More options" where the hub makes one up, #5029; once address and key are in it fetches the models
// and tests one by itself, and "Test connection" and "Fetch models" stay for doing it by hand) (3) "Choose models"
// (context window, thinking levels, the default; an ID typed by hand is looked up in the hub's catalog) and, for Claude Code, its tiers
// (#4975: filled in from the checked models, a summary with Adjust), the rest under "Advanced". Editing opens at "Connect" with the
// same form. The logic is lib/providers.ts, lib/provider-tiers.ts and lib/launcher-presets.ts. It reads the main conversation's readiness (4.3 B.3 / B.4): with a
// Claude Code that already answers, "Use it for the main conversation and new Agents" starts off; a key saved for a computer with no Claude Code
// says so (the main conversation still starts claude there) with how to install it.
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { api, enc, errorText } from "../../lib/api";
import {
  assignByDefault,
  assignSummary,
  regionBlocked,
  BUILTIN_PLUGIN,
  displayNameProblem,
  launcherNameProblem,
  loadPresets,
  presetBody,
  presetChoices,
  presetDefaultModel,
  presetHost,
  presetModelLabel,
  presetRunsClaude,
  presetSubtitle,
  presetTestBody,
  presetTestable,
  presetTitle,
  signupHref,
  testableKey,
  type AddResult,
  type LauncherPreset,
  type PresetForm,
} from "../../lib/launcher-presets";
import {
  autoCheckable,
  EFFORT_LEVELS,
  fallbackTitle,
  fillFromCatalog,
  formFromView,
  formModel,
  formProblems,
  mergeDiscovered,
  legacyChatClears,
  newForm,
  probeBody,
  providerBody,
  testModel,
  testText,
  type FormModel,
  type ProviderForm,
  type ProviderModel,
  type ProviderView,
  type TestResult,
} from "../../lib/providers";
import { forgetProviderNames, providerTitle } from "../../lib/provider-names";
import { applyTiers, checkedIds, hasTiers, setTier, tierAdvice, tierLines, tiersSplit, withAutoTiers, type TierAdvice, type TierRow, type Tiers, type TierSlot } from "../../lib/provider-tiers";
import { formScope, newScope, scopeBody, scopeFromView, scopeProblem, scopeShown, scopeSummary, type ScopeForm } from "../../lib/provider-scope";
import { claudeMissing, claudeWorks, currentReadiness, readinessChanged, readinessSettled, useReadiness, type Readiness } from "../../lib/readiness";
import { effortLabel } from "../../lib/routines";
import { composing } from "../../lib/send-history";
import { hubHas, useStore } from "../../lib/store";
import { Button, cx, Dialog, Loading, Spinner, toast } from "../../ui/atoms";
import { Icon, type IconName } from "../../ui/icons";
import { Segmented } from "../assistant/parts";
import { InstallClaudeHelp } from "../assistant/Readiness";
import { Pill, Switch } from "./SettingsParts";
import { CheckMark, Disclosure, FIELD, INPUT, Labelled, SecretField, SMALL, TextField } from "./ProviderFields";
import { ProviderScope } from "./ProviderScope";
import { t } from "../../i18n";

type Step = "pick" | "key" | "done" | "connect" | "models" | "devices";
const CUSTOM = "custom";
const NO_PRESET_FORM: PresetForm = { name: "", display_name: "", api_key: "", model: "" };
/** The hub's hello feature for built-in presets, a name it makes up and assign (docs/integrations/launchers.md "Built-in presets"). */
const BUILTIN_PRESETS_FEATURE = "builtin_presets";
/** The hub's hello feature for making up a gateway's startup ID when none is typed (#5029). */
const AUTO_NAME_FEATURE = "launcher_auto_name";
const stepTitle = (s: Step) =>
  ({
    pick: t("settings-providerwizard.step_pick"),
    key: t("settings-providerwizard.step_key"),
    done: t("settings-providerwizard.step_done"),
    connect: t("settings-providerwizard.step_connect"),
    models: t("settings-providerwizard.step_models"),
    devices: t("settings-providerwizard.step_devices"),
  })[s];

/** The address field's hint: what the base is, and whether its /v1 stays. A Messages base reaches Claude Code (or
 *  omp) as ANTHROPIC_BASE_URL and the hub drops a trailing /v1 from it; Responses, Chat Completions, Codex and
 *  Antigravity bases are used as typed (docs/integrations/launchers.md "A Messages Base URL is stored without a
 *  trailing /v1"). */
function urlHint(f: ProviderForm): string {
  const api = f.kind === "api";
  if (api && f.api === "chat") return t("settings-providerwizard.url_chat_hint");
  if (api && f.api === "responses") return t("settings-providerwizard.url_responses_hint");
  if (!api && f.agent === "codex") return t("settings-providerwizard.url_codex_hint");
  if (!api && f.agent === "agy") return t("settings-providerwizard.url_agy_hint");
  return t("settings-providerwizard.url_gateway_hint");
}

/** What a single-line field keeps of pasted text: no line breaks, no space at the ends. */
const oneLine = (s: string) => s.replace(/[\r\n]+/g, "").trim();

/** A preset's key test: running, or what it answered. */
type KeyTest = { running: true } | { running: false; result: TestResult };

/** What fetching a gateway's models found: how many, or why not; `check` when it was the Connect step's check
 *  (which goes on to test), `seq` the request it answers. */
type Found = { n: number; error?: string; check: boolean; seq: number };

/** How long a pasted or typed key (a gateway's address and key) has to stay the same before it is tested on its own. */
const AUTO_TEST_MS = 700;

export function ProviderWizard({ existing, onClose, onSaved }: { existing: ProviderView | "new" | null; onClose: () => void; onSaved: () => void }) {
  const isNew = existing === "new";
  const old = existing && existing !== "new" ? existing : null;
  const [step, setStep] = useState<Step>("pick");
  const [f, setForm] = useState<ProviderForm>(() => newForm());
  // Every change goes through the tiers' fill-in (#4975): a new Claude Code provider's tiers follow the checked models
  // and the default until the user picks one, in the same render; a saved provider's never change by themselves.
  const setF = useCallback<React.Dispatch<React.SetStateAction<ProviderForm>>>((u) => setForm((x) => withAutoTiers(typeof u === "function" ? u(x) : u)), []);
  // one per opening: an answer for an earlier one is dropped (Settings keeps the wizard mounted)
  const opening = useRef(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [tiersOpen, setTiersOpen] = useState(false);
  const [clearLegacyChat, setClearLegacyChat] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<TestResult | null>(null);
  const [probeModel, setProbeModel] = useState("");
  const [discovering, setDiscovering] = useState(false);
  const [found, setFound] = useState<Found | null>(null);
  const connectionGeneration = useRef(0);
  const discoverySeq = useRef(0);
  const connectionTestSeq = useRef(0);
  // The Connect step's own check (#4881): whether this connection was already checked (by itself or by hand); the
  // discovery (its sequence number) whose end starts the check's test, 0 when none; whether the address is being typed
  // (a key never goes to a half-typed address: it counts once its field is left, the step is, or a paste fills all of
  // it), and the text such a paste is putting there.
  const [checked, setChecked] = useState(false);
  const testAfter = useRef(0);
  const [urlTyping, setUrlTyping] = useState(false);
  const urlPaste = useRef<string | null>(null);
  // the latest test still standing: its model and how it went, so fetching the list again never repeats a test of the
  // same model that is running or passed (a ref: the effect that reads it may run before a newer answer renders)
  const lastTest = useRef<{ model: string; state: "running" | "ok" | "failed" } | null>(null);
  const modelToTest = probeModel.trim() || testModel(f);
  // the hub's providers (built-in ones, then the extensions'); null while loading
  const [presets, setPresets] = useState<LauncherPreset[] | null>(null);
  const [presetsError, setPresetsError] = useState("");
  // a preset's id, or CUSTOM for a gateway typed by hand; "" before anything is picked
  const [choice, setChoice] = useState("");
  const [pf, setPf] = useState(NO_PRESET_FORM);
  // "More options": a preset's names and model, a gateway's startup ID
  const [more, setMore] = useState(false);
  // null: not touched, so it follows the readiness (off while Claude Code already answers)
  const [assignSet, setAssign] = useState<boolean | null>(null);
  const [keyTest, setKeyTest] = useState<KeyTest | null>(null);
  const testSeq = useRef(0);
  const [added, setAdded] = useState<Added | null>(null);
  // pending: the first readiness answer has not come (it can take seconds): the switch waits for it, and so does "Save"
  const { readiness, pending } = useReadiness(isNew);
  const works = claudeWorks(readiness);
  const assign = assignSet ?? assignByDefault(works, keyTest && !keyTest.running ? keyTest.result : null);
  const preset = presets?.find((p) => p.id === choice);
  // a hub with built-in presets names a provider itself and takes assign; an older one needs the name and knows no assign
  const hubNames = useStore((s) => hubHas(s.features, BUILTIN_PRESETS_FEATURE, true));
  // a hub that makes up a gateway's startup ID too: the ID waits under "More options"; an older one needs it typed
  const autoName = useStore((s) => hubHas(s.features, AUTO_NAME_FEATURE));
  // the device scope (docs/integrations/launchers.md "Device scope and on-demand delivery"): an existing provider's
  // when its hub returns one, a new one's when the hub says it takes it
  const scopeOn = useStore((s) => scopeShown(old, s.features));
  const machines = useStore((s) => s.machines);
  const [scope, setScope] = useState<ScopeForm>(() => newScope());
  const [devicesOpen, setDevicesOpen] = useState(false);

  // Settings keeps this component mounted when closed. A request belongs to one opening and connection,
  // even if the user later returns to the same provider or restores the same input values.
  // The name is not part of the connection: typing it keeps what was found and tested.
  useLayoutEffect(() => {
    ++connectionGeneration.current;
    setDiscovering(false);
    setFound(null);
    setTesting(false);
    setTest(null);
    setChecked(false);
    lastTest.current = null;
    return () => { ++connectionGeneration.current; };
  }, [existing, choice, f.kind, f.agent, f.api, f.base_url, f.auth, f.api_key, f.headers, f.headers_touched]);

  // Changing the probe model retires only its test; model edits must still merge with pending discovery.
  useLayoutEffect(() => {
    ++connectionTestSeq.current;
    setTesting(false);
    setTest(null);
    lastTest.current = null;
    return () => { ++connectionTestSeq.current; };
  }, [modelToTest]);

  useEffect(() => {
    if (existing === null) return;
    ++opening.current;
    setError("");
    setAdvanced(false);
    setTiersOpen(false);
    setClearLegacyChat(false);
    setTest(null);
    setFound(null);
    setProbeModel("");
    setUrlTyping(false);
    setF(old ? formFromView(old) : newForm());
    setScope(scopeFromView(old) ?? newScope());
    setDevicesOpen(false);
    setMore(false);
    setStep(old ? "connect" : "pick");
    if (!isNew) return;
    setChoice("");
    setPf(NO_PRESET_FORM);
    setAssign(null);
    setKeyTest(null);
    setAdded(null);
    setPresets(null);
    setPresetsError("");
    let live = true;
    loadPresets()
      .then((list) => live && setPresets(list))
      .catch((e) => live && (setPresets([]), setPresetsError(errorText(e))));
    return () => void (live = false);
  }, [existing]);

  // A preset's key is tested by itself once it stops changing: the hub fills in the address and the default model.
  useEffect(() => {
    const seq = ++testSeq.current;
    if (step !== "key" || !preset || !presetTestable(preset) || !testableKey(pf.api_key)) {
      setKeyTest(null);
      return;
    }
    const timer = setTimeout(() => void runKeyTest(seq), AUTO_TEST_MS);
    return () => clearTimeout(timer);
  }, [step, preset?.id, pf.api_key, pf.model]);

  const runKeyTest = async (seq = ++testSeq.current) => {
    if (!preset) return;
    setKeyTest({ running: true });
    let result: TestResult;
    try {
      result = await api<TestResult>("/launchers/test", { method: "POST", json: presetTestBody(preset, pf.api_key, pf.model === presetDefaultModel(preset) ? "" : pf.model) });
    } catch (e) {
      result = { ok: false, ms: 0, error: errorText(e) };
    }
    if (seq === testSeq.current) setKeyTest({ running: false, result });
  };

  const set = <K extends keyof ProviderForm>(k: K) => (v: ProviderForm[K]) => setF((x) => ({ ...x, [k]: v }));
  const problems = formProblems(f, isNew, !!old?.has_key, autoName);
  // models were found for this connection: choosing them is what comes next
  const foundModels = !!found && !found.error && found.n > 0;
  const openAI = f.kind === "api" && (f.api === "responses" || f.api === "chat");
  const hasLegacyChat = Object.keys(legacyChatClears(old)).length > 0;
  const presetFlow = isNew && choice !== CUSTOM;
  const presetReady = !!preset && (hubNames || !!pf.name.trim()) && !launcherNameProblem(pf.name) && !displayNameProblem(pf.display_name) && !!pf.api_key.trim() && !keyTest?.running;
  const devicesProblem = scopeOn ? scopeProblem(scope) : "";
  const canSave = !devicesProblem && (step === "key" ? presetReady : !problems.connect && !problems.models && !problems.advanced && (!hasLegacyChat || clearLegacyChat));

  const pick = (id: string) => {
    setError("");
    if (id !== choice) {
      const p = presets?.find((x) => x.id === id);
      // The key belongs to the provider it was typed for: carried over, it would be tested against (and saved for)
      // another provider's endpoint.
      setPf({ ...NO_PRESET_FORM, model: p ? presetDefaultModel(p) : "" });
      setMore(false);
    }
    setChoice(id);
    setStep(id === CUSTOM ? "connect" : "key");
  };

  const runTest = async () => {
    if (hasLegacyChat) return;
    setChecked(true);
    const model = modelToTest;
    lastTest.current = { model, state: "running" };
    const generation = connectionGeneration.current;
    const seq = ++connectionTestSeq.current;
    const current = () => generation === connectionGeneration.current && seq === connectionTestSeq.current;
    const answered = (result: TestResult) => {
      lastTest.current = { model, state: result.ok ? "ok" : "failed" };
      setTest(result);
    };
    setTesting(true);
    setTest(null);
    try {
      const result = await api<TestResult>("/launchers/test", { method: "POST", json: probeBody(f, old?.name ?? null, model) });
      if (current()) answered(result);
    } catch (e) {
      if (current()) answered({ ok: false, ms: 0, error: errorText(e) });
    } finally {
      if (current()) setTesting(false);
    }
  };
  const testByHand = () => {
    testAfter.current = 0;
    void runTest();
  };
  /** `check`: part of the step's check, so its end tests the connection (by hand it only fetches). */
  const runDiscover = async (check = false) => {
    if (hasLegacyChat) return;
    setChecked(true);
    const generation = connectionGeneration.current;
    const seq = ++discoverySeq.current;
    testAfter.current = check ? seq : 0;
    const current = () => generation === connectionGeneration.current && seq === discoverySeq.current;
    setDiscovering(true);
    setFound(null);
    try {
      const r = await api<{ models?: ProviderModel[]; error?: string }>("/launchers/discover", { method: "POST", json: probeBody(f, old?.name ?? null) });
      if (!current()) return;
      const list = r.models ?? [];
      setF((x) => {
        if (!current()) return x;
        const models = mergeDiscovered(x.models, list, x.kind === "api" && x.api === "chat");
        return { ...x, models, model: x.model || models.find((m) => m.checked)?.id || "" };
      });
      setFound({ n: list.length, error: r.error, check, seq });
    } catch (e) {
      if (current()) setFound({ n: 0, error: errorText(e), check, seq });
    } finally {
      if (current()) setDiscovering(false);
    }
  };
  // A gateway's address and key are checked by themselves once they stop changing, once per connection (#4881):
  // the models are fetched, then the connection is tested.
  const autoReady = existing !== null && step === "connect" && !checked && !hasLegacyChat && autoCheckable(f, old);
  const autoCheck = autoReady && !urlTyping;
  useEffect(() => {
    if (!autoCheck) return;
    const timer = setTimeout(() => void runDiscover(true), AUTO_TEST_MS);
    return () => clearTimeout(timer);
  }, [autoCheck, existing, f.kind, f.agent, f.api, f.base_url, f.auth, f.api_key, f.headers, f.headers_touched]);

  // The check's test, after the render that put the fetched models in the form (and retired any test of the model
  // before them): the default model, else the first found, else the one typed to test with, whatever the list said (a
  // key may answer without being allowed to list models); none known, nothing is tested. A test of the same model that
  // is running or passed is not sent again (Retry on the list); a test or fetch pressed by hand meanwhile takes over.
  useEffect(() => {
    if (!found || found.seq !== testAfter.current) return;
    testAfter.current = 0;
    const standing = lastTest.current?.model === modelToTest && lastTest.current.state !== "failed";
    if (modelToTest && !standing) void runTest();
  }, [found]);
  // Leaving the step is leaving the URL field (a field removed while focused never blurs).
  useEffect(() => {
    if (step !== "connect") setUrlTyping(false);
  }, [step]);

  const save = async () => {
    if (busy || !canSave) return;
    setBusy(true);
    setError("");
    try {
      if (step === "key" && preset) {
        // a direct API cannot run the main conversation or Agents: nothing to assign, so nothing to wait for
        const direct = !presetRunsClaude(preset);
        let give = false;
        let noClaude = false;
        if (!direct) {
          // never decide on "not known yet": a Claude Code that already answers must not be moved off (4.3 B.3)
          await readinessSettled();
          const before = currentReadiness();
          give = hubNames && (assignSet ?? assignByDefault(claudeWorks(before), keyTest && !keyTest.running ? keyTest.result : null));
          noClaude = claudeMissing(before);
        }
        const result = await api<AddResult>("/launchers", { method: "POST", json: { ...presetBody(preset, pf, give), ...(scopeOn ? scopeBody(scope, null, machines, give) : {}) } });
        forgetProviderNames();
        onSaved();
        setAdded({ result, asked: give, fallback: pf.display_name.trim() || pf.name.trim() || presetTitle(preset), noClaude, direct });
        setStep("done");
        void readinessChanged();
      } else {
        const body = { ...providerBody(f, old, clearLegacyChat), ...(scopeOn ? scopeBody(scope, old, machines) : {}) };
        // both answer {launcher}: a new one's startup ID and display name may be the hub's (#5029)
        const r = await api<AddResult>(isNew ? "/launchers" : `/launchers/${enc(String(body.name))}`, { method: isNew ? "POST" : "PUT", json: body });
        const name = providerTitle(r?.launcher ?? { name: String(body.name), display_name: String(body.display_name) });
        forgetProviderNames();
        toast(isNew ? t("settings-providerwizard.added", { name }) : t("settings-providerwizard.saved", { name }), "ok");
        onSaved();
        void readinessChanged();
        onClose();
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const devices: Step[] = scopeOn ? ["devices"] : [];
  const steps: Step[] = !isNew ? ["connect", "models", ...devices] : presetFlow ? ["pick", "key", "done"] : ["pick", "connect", "models", ...devices];
  const at = steps.indexOf(step);
  const stepProblem = step === "connect" ? problems.connect : step === "models" ? problems.models || problems.advanced : step === "devices" ? devicesProblem : "";
  const next = () => {
    if (stepProblem) {
      // a startup ID that keeps the step from going on is shown where it was typed
      if (step === "connect" && launcherNameProblem(f.name)) setMore(true);
      return setError(stepProblem);
    }
    setError("");
    setStep(steps[at + 1]);
  };
  const back = () => (setError(""), setStep(steps[at - 1]));
  // done is only reached by saving, and once there the provider exists: nothing before it is open again
  const reachable = (s: Step) => {
    if (step === "done") return s === "done";
    if (s === "done") return false;
    const i = steps.indexOf(s);
    return i <= at || !isNew || (s === "models" && !problems.connect) || (s === "devices" && !problems.connect && !problems.models && !problems.advanced);
  };
  const done = step === "done";
  // A gateway's startup ID: typed for a new one (where the hub makes one up, only if wanted, under "More options"), and
  // read-only on a saved one, whose launches and settings refer to it (#5029).
  const idInMore = autoName || !isNew;
  const idField = (
    <TextField label={t("settings-providerwizard.startup_id")} value={f.name} onChange={set("name")} disabled={!isNew} mono
      placeholder={!isNew ? undefined : autoName ? t("settings-providerwizard.startup_id_auto") : t("settings-providerwizard.name_placeholder", { example: "my_gateway" })}
      error={isNew ? launcherNameProblem(f.name) : undefined} hint={isNew ? t("settings-providerwizard.startup_id_hint") : t("settings-providerwizard.startup_id_fixed")} />
  );

  return (
    <Dialog
      open={existing !== null}
      onClose={onClose}
      width={580}
      title={isNew ? t("settings-providerwizard.title_new") : t("settings-providerwizard.title_edit", { name: old ? providerTitle(old) : "" })}
      footer={
        done ? (
          <>
            <span className="flex-1" />
            <Button tone="primary" onClick={onClose}>
              {t("settings-providerwizard.finish")}
            </Button>
          </>
        ) : (
          <>
            <Button tone="quiet" onClick={onClose}>
              {t("common.cancel")}
            </Button>
            <span className="flex-1" />
            {at > 0 && (
              <Button tone="secondary" onClick={back}>
                {t("settings-providerwizard.back")}
              </Button>
            )}
            {step === "connect" && (
              <Button tone={isNew || foundModels ? "primary" : "secondary"} onClick={next}>
                {t("settings-providerwizard.next")}
              </Button>
            )}
            {step === "models" && scopeOn && (
              <Button tone="secondary" onClick={next}>
                {t("settings-providerwizard.next")}
              </Button>
            )}
            {(step === "key" || step === "models" || step === "devices" || !isNew) && step !== "pick" && (
              // one primary per step: once models were found, choosing them (Next) comes before saving
              <Button tone={step === "connect" && foundModels ? "secondary" : "primary"} busy={busy} disabled={!canSave} onClick={() => void save()}>
                {t("common.save")}
              </Button>
            )}
          </>
        )
      }
    >
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          if (step === "key" || step === "models" || step === "devices") void save();
          else if (step === "connect") next();
        }}
      >
        <ol aria-label={t("settings-providerwizard.steps")} className="flex items-center gap-2">
          {steps.map((s, i) => (
            <li key={s} className="flex min-w-0 items-center gap-2">
              {i > 0 && <span aria-hidden className="h-px w-4 shrink-0 bg-line-strong sm:w-8" />}
              <button
                type="button"
                disabled={!reachable(s)}
                aria-current={s === step ? "step" : undefined}
                onClick={() => (setError(""), setStep(s))}
                className="touch-hit flex min-w-0 items-center gap-1.5 rounded-control text-[12px] disabled:opacity-50"
              >
                <span className={cx("flex size-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold tabular-nums", s === step ? "bg-accent text-white" : i < at ? "bg-accent-tint text-accent" : "bg-inset text-ink-3 shadow-hairline")}>
                  {i < at || (done && s === step) ? <Icon name="check" size={10} stroke={3} /> : i + 1}
                </span>
                <span className={cx("truncate whitespace-nowrap", s === step ? "font-medium text-ink" : "text-ink-3")}>{stepTitle(s)}</span>
              </button>
            </li>
          ))}
        </ol>

        {hasLegacyChat && <label className="flex items-start gap-2 rounded-card bg-inset p-3 text-[12px] leading-relaxed text-ink-2">
          <input type="checkbox" className="mt-0.5 shrink-0" checked={clearLegacyChat} onChange={(e) => setClearLegacyChat(e.target.checked)} />
          <span>{t("settings-providerwizard.chat_clear_legacy")}</span>
        </label>}

        {step === "pick" && <PickStep presets={presets} error={presetsError} choice={choice} onPick={pick} />}

        {step === "key" && preset && (
          <KeyStep preset={preset} pf={pf} setPf={setPf} keyTest={keyTest} onRetest={() => void runKeyTest()} onOther={() => (setError(""), setChoice(""), setStep("pick"))} assign={hubNames ? assign : null} setAssign={setAssign} checking={pending && assignSet === null} works={works ? readiness?.machine?.name ?? "" : null} more={more} setMore={setMore}
            devices={scopeOn && (
              <Disclosure title={t("settings-providerwizard.step_devices")} open={devicesOpen} onToggle={() => setDevicesOpen((o) => !o)}
                badge={devicesOpen ? undefined : scopeSummary(formScope(scope, hubNames && assign), (id) => machines.find((m) => m.id === id)?.name || id)}>
                <ProviderScope scope={scope} setScope={setScope} old={null} kind={preset.kind} assign={hubNames && assign} />
              </Disclosure>
            )} />
        )}

        {step === "done" && added && <DoneStep added={added} readiness={readiness} />}

        {step === "connect" && (
          <div className="flex flex-col gap-3">
            {/* left empty, the provider shows what the placeholder says (the hub names a new one after its host) */}
            <TextField label={t("settings-providerwizard.display_name")} value={f.display_name} onChange={set("display_name")}
              placeholder={fallbackTitle(f, autoName) || t("settings-providerwizard.display_name_placeholder")} error={displayNameProblem(f.display_name)} hint={t("settings-providerwizard.display_name_hint")} />
            {!idInMore && idField}
            {isNew && (
              <Labelled label={t("settings-providerwizard.kind")} hint={f.kind === "api" ? t("settings-providerwizard.kind_api_hint") : t("settings-providerwizard.kind_launcher_hint", { agent: f.agent === "codex" ? "Codex" : f.agent === "agy" ? "Antigravity" : f.agent === "omp" ? "omp" : "Claude Code" })}>
                <Segmented<"launcher" | "codex" | "agy" | "omp" | "api">
                  label={t("settings-providerwizard.kind")}
                  value={f.kind === "api" ? "api" : f.agent === "codex" ? "codex" : f.agent === "agy" ? "agy" : f.agent === "omp" ? "omp" : "launcher"}
                  onChange={(v) => {
                    if (v === "api") setF((x) => ({ ...x, kind: "api", agent: "claude", api: "messages" }));
                    else if (v === "codex") setF((x) => ({ ...x, kind: "launcher", agent: "codex", api: "messages", models: x.models.length ? x.models : [formModel({ id: "gpt-5.4", name: "GPT-5.4" })], model: x.model || "gpt-5.4" }));
                    else if (v === "agy") setF((x) => ({ ...x, kind: "launcher", agent: "agy", api: "messages", models: x.agent === "agy" ? x.models : [], model: x.agent === "agy" ? x.model : "" }));
                    else if (v === "omp") setF((x) => ({ ...x, kind: "launcher", agent: "omp", api: "messages", models: x.models.length ? x.models : [formModel({ id: "google-antigravity/gemini-4-argon:high", name: "Gemini 4 Argon (High)" })], model: x.model || "google-antigravity/gemini-4-argon:high" }));
                    else setF((x) => ({ ...x, kind: "launcher", agent: "claude", api: "messages" }));
                  }}
                  options={[{ v: "launcher", label: "Claude Code" }, { v: "codex", label: "Codex" }, { v: "agy", label: "Antigravity" }, { v: "omp", label: "omp" }, { v: "api", label: t("settings-providerwizard.direct_api") }]}
                />
              </Labelled>
            )}
            {isNew && f.kind === "api" && (
              <Labelled label={t("settings-providerwizard.api_protocol")}>
                <Segmented<ProviderForm["api"]> wrap label={t("settings-providerwizard.api_protocol")} value={f.api} onChange={set("api")} options={[{ v: "messages", label: "Anthropic Messages" }, { v: "responses", label: "OpenAI Responses" }, { v: "chat", label: t("settings-providerwizard.protocol_chat") }]} />
              </Labelled>
            )}
            <TextField label={t("settings-providerwizard.base_url")} value={f.base_url} mono inputMode="url" placeholder="https://…"
              onChange={(v) => {
                // typed, or a paste into part of it; a paste over all of it is the address as the user has it
                setUrlTyping(urlPaste.current === null || oneLine(urlPaste.current) !== v.trim());
                urlPaste.current = null;
                set("base_url")(v);
              }}
              onPaste={(e) => {
                const el = e.currentTarget;
                const whole = el.selectionStart === 0 && el.selectionEnd === el.value.length;
                const text = e.clipboardData.getData("text/plain");
                urlPaste.current = whole ? text : null;
                // the same text again changes nothing, so no change follows: it counts here
                if (whole && oneLine(text) === el.value.trim()) setUrlTyping(false);
              }}
              // left for another field or the page; the window losing focus keeps the field focused, and the URL unfinished
              onBlur={(e) => document.activeElement !== e.currentTarget && setUrlTyping(false)}
              hint={urlHint(f)} />
            {!openAI && (
              <Labelled label={t("settings-providerwizard.auth")} hint={f.auth === "bearer" ? t("settings-providerwizard.auth_bearer_hint") : t("settings-providerwizard.auth_key_hint")}>
                <Segmented<ProviderForm["auth"]> label={t("settings-providerwizard.auth")} value={f.auth} onChange={set("auth")} options={[{ v: "bearer", label: "Bearer" }, { v: "api_key", label: "x-api-key" }]} />
              </Labelled>
            )}
            <SecretField label={isNew || !old?.has_key ? "API Key" : t("settings-providerwizard.key_keep")} value={f.api_key} onChange={set("api_key")} placeholder={old?.has_key ? t("settings-providerwizard.key_set") : "sk-…"} hint={t("settings-providerwizard.key_hint")} />
            <div className="flex flex-col gap-2 rounded-card bg-inset p-3 shadow-hairline">
                <div className="flex flex-wrap items-center gap-2">
                  <input value={probeModel} onChange={(e) => setProbeModel(e.target.value)} placeholder={testModel(f) || t("settings-providerwizard.test_model")} aria-label={t("settings-providerwizard.test_model")} autoComplete="off" spellCheck={false}
                    className={cx(FIELD, "h-8 w-full min-w-0 px-2.5 font-mono text-[12.5px] sm:w-auto sm:flex-1")} />
                  <Button tone="secondary" icon="run" busy={testing} disabled={!!problems.reach || hasLegacyChat} onClick={testByHand}>
                    {t("settings-providerwizard.test")}
                  </Button>
                  <Button tone="secondary" icon="download" busy={discovering} disabled={!!problems.reach || hasLegacyChat} onClick={() => void runDiscover()}>
                    {t("settings-providerwizard.discover")}
                  </Button>
                </div>
                <p className="text-[11px] text-ink-3">{hasLegacyChat ? t("settings-providerwizard.chat_probe_legacy") : testModel(f) ? t("settings-providerwizard.test_cost_current", { model: testModel(f) }) : t("settings-providerwizard.test_cost")}</p>
                {!hasLegacyChat && (
                  <ConnectStatus
                    discovering={discovering}
                    found={found}
                    testing={testing}
                    test={test}
                    // before anything ran: a new gateway says it checks itself, and both say when it is about to
                    waiting={autoCheck ? "wait" : autoReady ? "leave" : isNew && !checked ? "hint" : null}
                    onRetryFetch={() => void runDiscover(!!found?.check)}
                    onRetryTest={testByHand}
                  />
                )}
              </div>
            {idInMore && (
              <Disclosure title={t("settings-providerwizard.more_options")} open={more} onToggle={() => setMore((o) => !o)} badge={more ? undefined : t("settings-providerwizard.startup_id")}>
                {idField}
              </Disclosure>
            )}
          </div>
        )}

        {step === "devices" && <ProviderScope scope={scope} setScope={setScope} old={old} kind={f.kind} agent={f.agent} />}

        {step === "models" && (
          <div className="flex flex-col gap-3">
            <ModelList f={f} setF={setF} opening={opening} />
            {hasTiers(f) && <ClaudeTiers f={f} setF={setF} saved={!!old} open={tiersOpen} onToggle={() => setTiersOpen((o) => !o)} />}
            <Disclosure title={t("settings-providerwizard.advanced")} open={advanced} onToggle={() => setAdvanced((o) => !o)}
              badge={advanced ? undefined : hasTiers(f) ? t("settings-providerwizard.advanced_badge_claude") : t("settings-providerwizard.advanced_badge")}>
              <Advanced f={f} setF={setF} set={set} hadHeaders={!!old?.has_headers} hadEnv={!!old?.has_env} />
            </Disclosure>
          </div>
        )}

        {(error || (step === "models" && !canSave && (problems.models || problems.advanced))) && <p role="alert" className="text-[12px] text-red">{error || problems.models || problems.advanced}</p>}
        <button type="submit" hidden />
      </form>
    </Dialog>
  );
}

/** A provider's tile in the list: the built-in ones a spark, an extension's its piece, the gateway a link. */
function Tile({ icon }: { icon: IconName }) {
  return (
    <span aria-hidden className="flex size-8 shrink-0 items-center justify-center rounded-control bg-inset text-ink-2 shadow-hairline">
      <Icon name={icon} size={15} />
    </span>
  );
}

const presetIcon = (p: LauncherPreset): IconName => (p.plugin === BUILTIN_PLUGIN ? "spark" : "extension");

/** ①: the hub's providers in its order, the gateway typed by hand last. Picking one goes on. A built-in one says who
 *  it is for, not its address (that is on ②); an extension's says its kind and host. */
function PickStep({ presets, error, choice, onPick }: { presets: LauncherPreset[] | null; error: string; choice: string; onPick: (id: string) => void }) {
  if (!presets) return <Loading />;
  const row = (id: string, icon: IconName, title: string, detail: string | undefined, meta: string, badge?: string) => (
    <li key={id} className="border-b border-line last:border-b-0">
      <button type="button" onClick={() => onPick(id)} aria-current={choice === id ? "true" : undefined}
        className={cx("flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors pointer-coarse:min-h-14", choice === id ? "bg-accent-tint" : "hover:bg-hover")}>
        <Tile icon={icon} />
        <span className="min-w-0 flex-1">
          <span className="flex min-w-0 items-center gap-2">
            <span className="truncate text-[13px] font-medium text-ink">{title}</span>
            {badge && <Pill>{badge}</Pill>}
          </span>
          {detail && <span className="mt-0.5 line-clamp-2 block text-[11.5px] leading-relaxed text-ink-2">{detail}</span>}
          {meta && <span className="mt-0.5 block truncate text-[11px] text-ink-3">{meta}</span>}
        </span>
        <Icon name="chevronRight" size={12} stroke={2} className="shrink-0 text-ink-3" />
      </button>
    </li>
  );
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[12.5px] leading-relaxed text-ink-2">{t("settings-providerwizard.pick_intro")}</p>
      <ul aria-label={t("settings-providerwizard.provider_type")} className="overflow-hidden rounded-card bg-surface shadow-btn">
        {presets.map((p) =>
          row(p.id, presetIcon(p), presetTitle(p), p.description, p.plugin === BUILTIN_PLUGIN ? "" : presetSubtitle(p), p.plugin === BUILTIN_PLUGIN ? undefined : t("settings-providerwizard.extension_badge")),
        )}
        {row(CUSTOM, "link", t("settings-providerwizard.custom"), t("settings-providerwizard.custom_detail"), "")}
      </ul>
      {error && <p className="text-[12px] text-orange">{t("settings-providerwizard.presets_failed", { error })}</p>}
    </div>
  );
}

/** (2) for a preset: only the key; it is tested by itself. The names and another model wait under "More options". */
function KeyStep({ preset, pf, setPf, keyTest, onRetest, onOther, assign, setAssign, checking, works, more, setMore, devices }: {
  preset: LauncherPreset;
  pf: PresetForm;
  setPf: React.Dispatch<React.SetStateAction<PresetForm>>;
  keyTest: KeyTest | null;
  onRetest: () => void;
  /** Back to the providers (a region block: another key will not help, #4526). */
  onOther: () => void;
  /** null: the hub takes no assign and wants the name (an older hub). */
  assign: boolean | null;
  setAssign: (on: boolean) => void;
  /** Still asking whether the main conversation already runs: the switch waits (it follows the answer). */
  checking: boolean;
  /** The computer whose Claude Code already answers the main conversation (its name, maybe ""); null: none does. */
  works: string | null;
  more: boolean;
  setMore: (fn: (o: boolean) => boolean) => void;
  /** The "Devices" disclosure, when the hub takes a scope. */
  devices?: React.ReactNode;
}) {
  const signup = signupHref(preset);
  const choices = presetChoices(preset);
  const nameProblem = launcherNameProblem(pf.name);
  const hiddenProblem = displayNameProblem(pf.display_name) || (assign !== null ? nameProblem : "");
  const builtin = preset.plugin === BUILTIN_PLUGIN;
  const nameField = (
    <TextField label={t("settings-providerwizard.startup_id")} value={pf.name} onChange={(v) => setPf((x) => ({ ...x, name: v }))} mono
      placeholder={assign === null ? t("settings-providerwizard.name_placeholder", { example: "jv_team" }) : t("settings-providerwizard.startup_id_auto")} error={nameProblem} hint={t("settings-providerwizard.startup_id_hint")} />
  );
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-3 rounded-card bg-inset p-3 shadow-hairline">
        <Tile icon={presetIcon(preset)} />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-[13px] font-medium text-ink">{presetTitle(preset)}</span>
          <span className="block truncate text-[11.5px] text-ink-3">{builtin ? presetHost(preset) : t("settings-providerwizard.extension_note", { plugin: preset.plugin })}</span>
        </span>
      </div>
      {assign === null && nameField}
      <SecretField label="API Key" value={pf.api_key} onChange={(v) => setPf((x) => ({ ...x, api_key: v }))} placeholder={t("settings-providerwizard.key_paste")}
        hint={preset.key_hint ? t("settings-providerwizard.key_hint_with", { hint: preset.key_hint }) : t("settings-providerwizard.key_hint")} />
      {signup && (
        <a href={signup} target="_blank" rel="noopener noreferrer" className="touch-hit inline-flex items-center gap-1 self-start whitespace-nowrap text-[12px] font-medium text-accent hover:underline">
          {t("settings-providerwizard.where_key")}
          <Icon name="external" size={11} stroke={2} />
        </a>
      )}
      {presetTestable(preset) ? (
        <KeyTestStatus keyTest={keyTest} hasKey={!!pf.api_key.trim()} onRetest={onRetest} onOther={onOther} />
      ) : (
        <p className="text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerwizard.key_test_responses")}</p>
      )}
      {!presetRunsClaude(preset) && <p className="text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerwizard.direct_note")}</p>}
      {assign !== null && presetRunsClaude(preset) && (
        <div className="flex items-center gap-3 rounded-card bg-surface p-3 shadow-btn">
          <span className="min-w-0 flex-1">
            <span className="block text-[13px] text-ink">{t("settings-providerwizard.assign_label")}</span>
            <span className="mt-0.5 block text-[11.5px] leading-relaxed text-ink-3">
              {checking
                ? t("settings-providerwizard.assign_checking")
                : works === null
                ? t("settings-providerwizard.assign_hint")
                : works
                  ? t("settings-providerwizard.assign_hint_works", { machine: works })
                  : t("settings-providerwizard.assign_hint_works_here")}
            </span>
          </span>
          {checking ? <Spinner /> : <Switch on={assign} onChange={setAssign} label={t("settings-providerwizard.assign_label")} />}
        </div>
      )}
      <Disclosure title={t("settings-providerwizard.more_options")} open={more} onToggle={() => setMore((o) => !o)}
        badge={more ? undefined : assign === null ? t("settings-providerwizard.more_options_badge_no_id") : t("settings-providerwizard.more_options_badge")}>
        {/* left empty, it shows the preset's name, or the startup ID when one is typed */}
        <TextField label={t("settings-providerwizard.display_name")} value={pf.display_name} onChange={(v) => setPf((x) => ({ ...x, display_name: v }))}
          placeholder={pf.name.trim() || presetTitle(preset)} error={displayNameProblem(pf.display_name)} hint={t("settings-providerwizard.display_name_hint")} />
        {assign !== null && nameField}
        <Labelled label={t("settings-providerwizard.default_model")} hint={preset.models_error ? t("settings-providerwizard.preset_models_error", { error: preset.models_error }) : t("settings-providerwizard.default_model_hint")}>
          {choices.length ? (
            <select value={pf.model} onChange={(e) => setPf((x) => ({ ...x, model: e.target.value }))} aria-label={t("settings-providerwizard.default_model")} className={cx(INPUT, "font-mono")}>
              {choices.map((m) => (
                <option key={m.id} value={m.id}>
                  {presetModelLabel(m)}
                </option>
              ))}
            </select>
          ) : (
            <input value={pf.model} onChange={(e) => setPf((x) => ({ ...x, model: e.target.value }))} placeholder={t("settings-providerwizard.main_model")} aria-label={t("settings-providerwizard.default_model")} autoComplete="off" spellCheck={false} className={cx(INPUT, "font-mono")} />
          )}
        </Labelled>
      </Disclosure>
      {/* folded with a mistake in it: Save is off, so say why here (#5029) */}
      {!more && hiddenProblem && <p role="alert" className="text-[12px] text-red">{hiddenProblem}</p>}
      {devices}
    </div>
  );
}

/** A check running: a spinner and what it is doing. */
function BusyLine({ children }: { children: React.ReactNode }) {
  return (
    <p role="status" className="flex items-center gap-1.5 text-[12px] text-ink-2">
      <Spinner />
      {children}
    </p>
  );
}

/** The one action at the end of a check's failed line (Retry, Test again, Use another), in the line's color. */
function LineAction({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button type="button" onClick={onClick} className="touch-hit shrink-0 whitespace-nowrap font-medium hover:underline">
      {children}
    </button>
  );
}

function KeyTestStatus({ keyTest, hasKey, onRetest, onOther }: { keyTest: KeyTest | null; hasKey: boolean; onRetest: () => void; onOther: () => void }) {
  if (!keyTest) return <p className="text-[11.5px] leading-relaxed text-ink-3">{hasKey ? t("settings-providerwizard.key_test_wait") : t("settings-providerwizard.key_test_cost")}</p>;
  if (keyTest.running) return <BusyLine>{t("settings-providerwizard.key_testing")}</BusyLine>;
  const r = keyTest.result;
  return (
    <div role="status" className={cx("flex items-start gap-2 rounded-card p-3 text-[12px]", r.ok ? "bg-green-tint text-green" : "bg-red-tint text-red")}>
      <Icon name={r.ok ? "check" : "warning"} size={13} stroke={2.4} className="mt-0.5 shrink-0" />
      <span className="min-w-0 flex-1 break-words">
        {r.ok ? t("settings-providerwizard.key_test_ok", { model: r.model ?? "", ms: r.ms }) : r.error || t("settings-providerwizard.key_test_failed")}
      </span>
      {!r.ok && (
        // the region, not the key: testing again or another key will not help, another provider will (#4526)
        <LineAction onClick={regionBlocked(r) ? onOther : onRetest}>
          {regionBlocked(r) ? t("settings-providerwizard.other_provider") : t("settings-providerwizard.retest")}
        </LineAction>
      )}
    </div>
  );
}

/** The Connect step's check: the connection test, then what fetching the models found (it runs first; its line ends
 *  pointing at the next step). A failure says the hub's own words and offers Retry, like a preset's key test (KeyTestStatus). */
function ConnectStatus({ discovering, found, testing, test, waiting, onRetryFetch, onRetryTest }: {
  discovering: boolean;
  found: Found | null;
  testing: boolean;
  test: TestResult | null;
  /** Nothing ran yet: "wait" the address and key are in and the check starts when typing stops; "leave" it starts once
   *  the address being typed is left; "hint" it will once they are in. */
  waiting: "wait" | "leave" | "hint" | null;
  /** Fetches again, and tests too when the failed fetch was the step's check. */
  onRetryFetch: () => void;
  onRetryTest: () => void;
}) {
  if (!discovering && !found && !testing && !test) {
    if (!waiting) return null;
    const line = waiting === "wait" ? t("settings-providerwizard.check_wait") : waiting === "leave" ? t("settings-providerwizard.check_leave_url") : t("settings-providerwizard.check_hint");
    return <p className="text-[11.5px] leading-relaxed text-ink-3">{line}</p>;
  }
  return (
    <>
      {testing
        ? <BusyLine>{t("settings-providerwizard.testing")}</BusyLine>
        : test && (
            <div role="status" className={cx("flex items-start gap-1.5 text-[12px]", test.ok ? "text-green" : "text-red")}>
              <Icon name={test.ok ? "check" : "warning"} size={12} stroke={2.4} className="mt-0.5 shrink-0" />
              <span className="min-w-0 flex-1 break-words">{testText(test)}</span>
              {!test.ok && <LineAction onClick={onRetryTest}>{t("settings-providerwizard.retry")}</LineAction>}
            </div>
          )}
      {discovering
        ? <BusyLine>{t("settings-providerwizard.discovering")}</BusyLine>
        : found && (
            <div role="status" className={cx("flex items-start gap-1.5 text-[12px]", found.error ? "text-orange" : "text-ink-2")}>
              <span className="min-w-0 flex-1 break-words">
                {found.error ? t("settings-providerwizard.discover_failed", { error: found.error }) : t("settings-providerwizard.discover_found", { count: found.n })}
              </span>
              {found.error && <LineAction onClick={onRetryFetch}>{t("settings-providerwizard.retry")}</LineAction>}
            </div>
          )}
    </>
  );
}

/** What saving a preset gave: the hub's answer, and whether the readiness said, before it, that the computer has no
 *  Claude Code (after it the step may read `unknown`: the hub does not say a provider of its own misses claude).
 *  `direct`: a direct API, which the main conversation and Agents cannot run on (no Claude Code involved). */
type Added = { result: AddResult; asked: boolean; fallback: string; noClaude: boolean; direct: boolean };

/** The computer has no Claude Code: now (asked again after saving) or before saving and not found since. */
function stillNoClaude(added: Added, r: Readiness | null): boolean {
  if (claudeMissing(r)) return true;
  return added.noClaude && r?.steps.find((s) => s.id === "claude")?.state !== "ok";
}

/** ③ for a preset: it is saved; what runs on it now and what was left as it was; that Claude Code is still missing. */
function DoneStep({ added, readiness }: { added: Added; readiness: Readiness | null }) {
  const [help, setHelp] = useState(false);
  const name = added.result.launcher ? providerTitle(added.result.launcher) : added.fallback;
  const s = assignSummary(added.result, added.asked);
  const machine = readiness?.machine?.name ?? "";
  const noClaude = !added.direct && stillNoClaude(added, readiness);
  // "the main conversation can use it" only where the key went to it
  const toMain = added.asked && !!added.result.assigned?.includes("assistant");
  return (
    <div role="status" className="flex flex-col items-center gap-2 py-4 text-center">
      <span className="flex size-10 items-center justify-center rounded-full bg-green-tint text-green">
        <Icon name="check" size={18} stroke={2.6} />
      </span>
      <p className="text-[15px] font-semibold text-ink">{t("settings-providerwizard.done_title", { name })}</p>
      {s.assigned && <p className="text-[13px] text-ink-2">{s.assigned}</p>}
      {s.kept.map((k) => (
        <p key={k} className="max-w-[420px] text-[12.5px] leading-relaxed text-ink-3">
          {k}
        </p>
      ))}
      {!s.assigned && (
        <p className="max-w-[420px] text-[12.5px] leading-relaxed text-ink-3">
          {added.direct ? t("settings-providerwizard.direct_note") : t("settings-providerwizard.done_pick_later")}
        </p>
      )}
      {noClaude && (
        <div data-testid="wizard-no-claude" className="mt-2 flex w-full max-w-[460px] items-start gap-2.5 rounded-card bg-orange-tint p-3 text-left">
          <Icon name="warning" size={14} stroke={2.2} className="mt-0.5 shrink-0 text-orange" />
          <span className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-ink">
            {toMain
              ? machine ? t("settings-providerwizard.done_no_claude", { machine }) : t("settings-providerwizard.done_no_claude_any")
              : machine ? t("settings-providerwizard.done_no_claude_kept", { machine }) : t("settings-providerwizard.done_no_claude_kept_any")}
          </span>
          {!help && (
            <Button tone="secondary" onClick={() => setHelp(true)}>
              {t("settings-providerwizard.how_install")}
            </Button>
          )}
        </div>
      )}
      {noClaude && help && (
        <div className="w-full max-w-[460px] text-left">
          <InstallClaudeHelp machine={machine} />
        </div>
      )}
    </div>
  );
}

/** ③: the models this provider offers. Checked ones are saved; the default is one of them. */
function ModelList({ f, setF, opening }: { f: ProviderForm; setF: React.Dispatch<React.SetStateAction<ProviderForm>>; opening: React.RefObject<number> }) {
  const [adding, setAdding] = useState("");
  const update = (i: number, patch: Partial<FormModel>) =>
    setF((x) => {
      const models = x.models.map((m, j) => (j === i ? { ...m, ...patch } : m));
      // unchecking the default moves it to the first checked one
      const model = models.some((m) => m.checked && m.id === x.model) ? x.model : models.find((m) => m.checked)?.id ?? "";
      return { ...x, models, model };
    });
  const add = () => {
    const id = adding.trim();
    if (!id) return;
    const known = f.models.some((m) => m.id === id);
    setF((x) => (x.models.some((m) => m.id === id) ? { ...x, models: x.models.map((m) => (m.id === id ? { ...m, checked: true } : m)) } : { ...x, models: [...x.models, formModel({ id })], model: x.model || id }));
    setAdding("");
    if (!known) void lookUp(id);
  };
  // What the hub's catalog knows of a typed ID (#4975), as discovery fills a listed one: only what is still empty.
  const lookUp = async (id: string) => {
    const at = opening.current;
    try {
      const r = await api<{ models?: ProviderModel[] }>("/launchers/catalog", { method: "POST", json: { ids: [id] } });
      const found = r.models ?? [];
      if (found.length && at === opening.current) setF((x) => ({ ...x, models: fillFromCatalog(x.models, found, x.kind === "api" && x.api === "chat") }));
    } catch {
      // an older hub has no catalog: the row keeps what was typed
    }
  };
  const checked = f.models.filter((m) => m.checked).length;
  const chat = f.kind === "api" && f.api === "chat";
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-baseline justify-between gap-2">
        <span className="text-[12px] font-medium text-ink-2">{t("settings-providerwizard.models")}</span>
        <span className="text-[11px] text-ink-3 tabular-nums">{t("settings-providerwizard.checked_count", { checked, total: f.models.length })}</span>
      </div>
      {f.models.length === 0 ? (
        <p className="rounded-card bg-inset px-3 py-4 text-center text-[12px] text-ink-3 shadow-hairline">{t("settings-providerwizard.no_models")}</p>
      ) : (
        <ul aria-label={t("settings-providerwizard.models")} className="overflow-hidden rounded-card bg-surface shadow-btn">
          {f.models.map((m, i) => (
            <ModelRow key={m.id + i} m={m} showEffort={!chat} isDefault={m.checked && f.model === m.id} onChange={(p) => update(i, p)} onDefault={() => setF((x) => ({ ...x, model: m.id }))} />
          ))}
        </ul>
      )}
      {chat && <p className="text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerwizard.chat_effort_hint")}</p>}
      <div className="flex gap-2">
        <input value={adding} onChange={(e) => setAdding(e.target.value)} onKeyDown={(e) => e.key === "Enter" && !composing(e.nativeEvent) && (e.preventDefault(), add())} placeholder={t("settings-providerwizard.add_model_id")} aria-label={t("settings-providerwizard.add_model_id")} autoComplete="off" spellCheck={false} className={cx(INPUT, "font-mono")} />
        <Button tone="secondary" icon="plus" disabled={!adding.trim()} onClick={add}>
          {t("settings-providerwizard.add")}
        </Button>
      </div>
    </div>
  );
}

function ModelRow({ m, showEffort, isDefault, onChange, onDefault }: { m: FormModel; showEffort: boolean; isDefault: boolean; onChange: (p: Partial<FormModel>) => void; onDefault: () => void }) {
  const toggleEffort = (e: string) => {
    const efforts = m.efforts.includes(e) ? m.efforts.filter((x) => x !== e) : EFFORT_LEVELS.filter((x) => x === e || m.efforts.includes(x));
    onChange({ efforts, default_effort: efforts.length && !efforts.includes(m.default_effort) ? "" : m.default_effort });
  };
  const levels = m.efforts.length ? m.efforts : [...EFFORT_LEVELS];
  return (
    <li data-model={m.id} className="border-b border-line px-3 py-2.5 last:border-b-0">
      <div className="flex items-center gap-2.5">
        <button type="button" role="checkbox" aria-checked={m.checked} aria-label={t("settings-providerwizard.use_model", { id: m.id })} onClick={() => onChange({ checked: !m.checked })}
          className="touch-hit shrink-0">
          <CheckMark on={m.checked} />
        </button>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-mono text-[12.5px] text-ink">{m.id}</span>
          {m.name && <span className="block truncate text-[11px] text-ink-3">{m.name}</span>}
        </span>
        {m.checked &&
          (isDefault ? (
            <span className="shrink-0 whitespace-nowrap rounded-chip bg-accent-tint px-2 py-0.5 text-[11px] font-medium text-accent">{t("settings-providerwizard.default")}</span>
          ) : (
            <button type="button" onClick={onDefault} className="touch-hit shrink-0 whitespace-nowrap rounded-chip px-2 py-0.5 text-[11px] text-ink-3 hover:bg-hover hover:text-ink">
              {t("settings-providerwizard.set_default")}
            </button>
          ))}
      </div>
      {m.checked && (
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-2 pl-6.5">
          <label className="flex items-center gap-1.5 text-[11.5px] text-ink-3">
            <span className="whitespace-nowrap">{t("settings-providerwizard.context")}</span>
            <input value={m.context_window} onChange={(e) => onChange({ context_window: e.target.value })} inputMode="numeric" placeholder="200000" aria-label={t("settings-providerwizard.context_window", { id: m.id })}
              className={cx(SMALL, "w-[104px] font-mono tabular-nums")} />
          </label>
          {showEffort && <><div role="group" aria-label={t("settings-providerwizard.efforts_supported", { id: m.id })} className="flex flex-wrap gap-1">
            {EFFORT_LEVELS.map((e) => (
              <button key={e} type="button" aria-pressed={m.efforts.includes(e)} onClick={() => toggleEffort(e)}
                className={cx("touch-hit h-6 whitespace-nowrap rounded-chip px-2 text-[11px] transition-colors duration-150", m.efforts.includes(e) ? "bg-accent-tint font-medium text-accent" : "bg-inset text-ink-3 shadow-hairline hover:text-ink-2")}>
                {effortLabel(e)}
              </button>
            ))}
          </div>
          <label className="flex items-center gap-1.5 text-[11.5px] text-ink-3">
            <span className="whitespace-nowrap">{t("settings-providerwizard.default_effort_short")}</span>
            <select value={m.default_effort} onChange={(e) => onChange({ default_effort: e.target.value })} aria-label={t("settings-providerwizard.model_default_effort", { id: m.id })} className={SMALL}>
              <option value="">{t("settings-providerwizard.unspecified")}</option>
              {levels.map((e) => (
                <option key={e} value={e}>
                  {effortLabel(e)}
                </option>
              ))}
            </select>
          </label></>}
        </div>
      )}
    </li>
  );
}

function Advanced({ f, setF, set, hadHeaders, hadEnv }: {
  f: ProviderForm;
  setF: React.Dispatch<React.SetStateAction<ProviderForm>>;
  set: <K extends keyof ProviderForm>(k: K) => (v: ProviderForm[K]) => void;
  hadHeaders: boolean;
  hadEnv: boolean;
}) {
  const list = useId();
  const ids = useMemo(() => checkedIds(f), [f.models]);
  const launcher = f.kind !== "api";
  const chat = !launcher && f.api === "chat";
  if (chat) return <p className="text-[11.5px] leading-relaxed text-ink-3">{t("settings-providerwizard.chat_advanced_hint")}</p>;
  const tier = (k: "opus_model" | "sonnet_model" | "haiku_model" | "fable_model" | "small_model", label: string, hint?: string) => (
    <Labelled label={label} hint={hint}>
      <input value={f[k]} onChange={(e) => set(k)(e.target.value)} list={list} placeholder={t("settings-providerwizard.same_as_default")} aria-label={label} autoComplete="off" spellCheck={false} className={cx(INPUT, "font-mono")} />
    </Labelled>
  );
  return (
    <>
      <datalist id={list}>
        {ids.map((id) => (
          <option key={id} value={id} />
        ))}
      </datalist>
      {launcher && hasTiers(f) && (
        // the tiers people change most are in "Claude Code tiers" above (#4975); Fable and forcing stay here
        <div className="grid items-start gap-3 sm:grid-cols-2">
          <TierSelect label={t("settings-providerwizard.tier_model", { tier: "Fable" })} value={f.fable_model} ids={ids} def={f.model.trim()} auto={f.tier_source.fable === "auto"} onChange={(v) => setF((x) => setTier(x, "fable", v))} />
          <div className="flex items-center gap-3 sm:pt-5">
            <span className="min-w-0 flex-1">
              <span className="block text-[12px] font-medium text-ink-2">{t("settings-providerwizard.force_subagent")}</span>
              <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">{t("settings-providerwizard.force_subagent_hint")}</span>
            </span>
            <Switch on={f.subagent_force} onChange={set("subagent_force")} label={t("settings-providerwizard.force_subagent")} />
          </div>
        </div>
      )}
      {launcher && !hasTiers(f) && (
        <>
          <div className="grid gap-3 sm:grid-cols-2">
            {tier("opus_model", t("settings-providerwizard.tier_model", { tier: "Opus" }))}
            {tier("sonnet_model", t("settings-providerwizard.tier_model", { tier: "Sonnet" }))}
            {tier("haiku_model", t("settings-providerwizard.tier_model", { tier: "Haiku" }))}
            {tier("fable_model", t("settings-providerwizard.tier_model", { tier: "Fable" }))}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            {tier("small_model", t("settings-providerwizard.small_model"), t("settings-providerwizard.small_model_hint"))}
            <Labelled label={t("settings-providerwizard.subagent_model")} hint={t("settings-providerwizard.subagent_hint")}>
              <div className="flex items-center gap-2">
                <input value={f.subagent_model} onChange={(e) => set("subagent_model")(e.target.value)} list={list} placeholder={t("settings-providerwizard.same_as_default")} aria-label={t("settings-providerwizard.subagent_model")} autoComplete="off" spellCheck={false} className={cx(INPUT, "font-mono")} />
                <span className="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-[12px] text-ink-2">
                  {t("settings-providerwizard.force")}
                  <Switch on={f.subagent_force} onChange={set("subagent_force")} label={t("settings-providerwizard.force_label")} />
                </span>
              </div>
            </Labelled>
          </div>
        </>
      )}
      <SecretField
        label={t("settings-providerwizard.headers")}
        multiline
        value={f.headers}
        onChange={(v) => setF((x) => ({ ...x, headers: v, headers_touched: true }))}
        placeholder={hadHeaders && !f.headers_touched ? t("settings-providerwizard.headers_set") : t("settings-providerwizard.headers_placeholder")}
        hint={
          hadHeaders && !f.headers_touched ? (
            <button type="button" onClick={() => setF((x) => ({ ...x, headers: "", headers_touched: true }))} className="touch-hit text-red hover:underline">
              {t("settings-providerwizard.headers_clear")}
            </button>
          ) : f.headers_touched && hadHeaders && !f.headers.trim() ? (
            t("settings-providerwizard.headers_cleared")
          ) : (
            t("settings-providerwizard.headers_hint")
          )
        }
      />
      <SecretField
        label={t("settings-providerwizard.env")}
        multiline
        value={f.env}
        onChange={(v) => setF((x) => ({ ...x, env: v, env_touched: true }))}
        placeholder={hadEnv && !f.env_touched ? t("settings-providerwizard.env_set") : t("settings-providerwizard.env_placeholder")}
        hint={
          hadEnv && !f.env_touched ? (
            <button type="button" onClick={() => setF((x) => ({ ...x, env: "", env_touched: true }))} className="touch-hit text-red hover:underline">
              {t("settings-providerwizard.env_clear")}
            </button>
          ) : f.env_touched && hadEnv && !f.env.trim() ? (
            t("settings-providerwizard.env_cleared")
          ) : (
            t("settings-providerwizard.env_hint")
          )
        }
      />
      <div className="grid gap-3 sm:grid-cols-2">
        <TextField label={t("settings-providerwizard.proxy")} value={f.proxy} onChange={set("proxy")} mono placeholder="http://127.0.0.1:7890" hint="HTTP_PROXY / HTTPS_PROXY" />
        <TextField label="NO_PROXY" value={f.no_proxy} onChange={set("no_proxy")} mono placeholder="localhost,127.0.0.1" />
        <TextField label={t("settings-providerwizard.timeout")} value={f.timeout_ms} onChange={set("timeout_ms")} mono inputMode="numeric" placeholder="3000000" />
        <TextField label={t("settings-providerwizard.compact_window")} value={f.compact_window} onChange={set("compact_window")} mono inputMode="numeric" placeholder={t("settings-providerwizard.same_as_model_context")} />
        <TextField label={t("settings-providerwizard.max_context")} value={f.max_context} onChange={set("max_context")} mono inputMode="numeric" placeholder={t("settings-providerwizard.same_as_model_context")} />
        <Labelled label={t("settings-providerwizard.default_effort")}>
          <select value={f.effort} onChange={(e) => set("effort")(e.target.value)} aria-label={t("settings-providerwizard.default_effort")} className={INPUT}>
            <option value="">{t("settings-providerwizard.follow_model")}</option>
            {EFFORT_LEVELS.map((e) => (
              <option key={e} value={e}>
                {effortLabel(e)}
              </option>
            ))}
          </select>
        </Labelled>
      </div>
      {launcher && (
        <>
          <div className="grid gap-3 sm:grid-cols-2">
            <TextField label={t("settings-providerwizard.custom_model")} value={f.custom_model} onChange={set("custom_model")} mono placeholder={t("settings-providerwizard.custom_model_placeholder")} />
            <TextField label={t("settings-providerwizard.custom_model_name")} value={f.custom_model_name} onChange={set("custom_model_name")} />
          </div>
          <TextField label={t("settings-providerwizard.custom_model_description")} value={f.custom_model_description} onChange={set("custom_model_description")} hint={t("settings-providerwizard.custom_model_hint")} />
          <TextField label={t("settings-providerwizard.args")} value={f.args} onChange={set("args")} mono placeholder="--dangerously-skip-permissions" hint={t("settings-providerwizard.args_hint")} />
        </>
      )}
    </>
  );
}

/** A tier's model: empty (the session's model, `def` unless the session picks another; `empty` names what else it
 *  follows) or one of the checked models; a stored one that is not checked stays on the list. The default model is not
 *  offered again by name: empty already runs it, and follows the session when it picks another. "Auto" when it filled
 *  itself in (#4975). */
function TierSelect({ label, hint, value, ids, def, auto, empty, onChange }: { label: string; hint?: string; value: string; ids: string[]; def: string; auto: boolean; empty?: string; onChange: (v: string) => void }) {
  const id = useId();
  const options = [...ids.filter((m) => m !== def || m === value), ...(value && !ids.includes(value) ? [value] : [])];
  return (
    <Labelled htmlFor={id} hint={hint} label={
      // one height with or without the pill, so fields side by side line up
      <span className="inline-flex min-h-5 items-center gap-1.5">
        {label}
        {auto && <Pill>{t("settings-providerwizard.tier_auto")}</Pill>}
      </span>
    }>
      <select id={id} value={value} onChange={(e) => onChange(e.target.value)} aria-label={label} className={cx(INPUT, "font-mono")}>
        <option value="">{empty ?? (def ? t("settings-providerwizard.same_as_default_named", { model: def }) : t("settings-providerwizard.same_as_default"))}</option>
        {options.map((m) => (
          <option key={m} value={m}>
            {m}
          </option>
        ))}
      </select>
    </Labelled>
  );
}

const TIER_NAME: Record<TierSlot, () => string> = {
  opus: () => "Opus",
  sonnet: () => "Sonnet",
  light: () => t("settings-providerwizard.tier_light"),
  fable: () => "Fable",
};
const ROW_NAME: Record<TierRow, () => string> = {
  opus: TIER_NAME.opus,
  sonnet: TIER_NAME.sonnet,
  light: TIER_NAME.light,
  haiku: () => "Haiku",
  small: () => t("settings-providerwizard.small_model"),
  subagent: () => t("settings-providerwizard.tier_subagents"),
};
const ROW_HINT: Partial<Record<TierRow, () => string>> = {
  light: () => t("settings-providerwizard.tier_light_hint"),
  small: () => t("settings-providerwizard.small_model_hint"),
  subagent: () => t("settings-providerwizard.subagent_hint"),
};

/** "Sonnet → x, Haiku / small model → y": what taking a suggestion sets. */
const tierList = (tiers: Tiers) =>
  (Object.keys(TIER_NAME) as TierSlot[])
    .filter((s) => tiers[s])
    .map((s) => t("settings-providerwizard.tier_pair", { tier: TIER_NAME[s](), model: tiers[s]! }))
    .join(t("settings-providerwizard.tier_separator"));

/** The suggestion line's words: which of the Haiku tier and the background requests sit on the Opus model, if any. */
function adviceText(a: TierAdvice): string {
  const list = tierList(a.tiers);
  if (!a.sameAsOpus) return t("settings-providerwizard.tiers_suggested", { list });
  const v = { model: a.sameAsOpus, list };
  if (a.haiku && a.small) return t("settings-providerwizard.tiers_same_as_opus", v);
  return a.haiku ? t("settings-providerwizard.tiers_haiku_as_opus", v) : t("settings-providerwizard.tiers_small_as_opus", v);
}

/** ③ for a Claude Code provider (#4975): the model each tier, the background requests and subagents run, under the
 *  models, with "Auto" on what filled itself in; Adjust opens the editor in place. Below it, when the Haiku tier lands
 *  on the Opus model while a lighter one is checked, or a saved provider's empty tiers could use a checked model, one
 *  line says so and Apply takes it. Fable and forcing the subagent model stay under Advanced. */
function ClaudeTiers({ f, setF, saved, open, onToggle }: { f: ProviderForm; setF: React.Dispatch<React.SetStateAction<ProviderForm>>; saved: boolean; open: boolean; onToggle: () => void }) {
  const titleId = useId();
  const bodyId = useId();
  const ids = useMemo(() => checkedIds(f), [f.models]);
  const def = f.model.trim();
  // apart or together as the step opened: a row never disappears under the user while they edit it
  const [split] = useState(() => tiersSplit(f));
  const lines = tierLines(f, split);
  const advice = tierAdvice(f, saved);
  const value = (row: TierRow) => ({ opus: f.opus_model, sonnet: f.sonnet_model, light: f.small_model, haiku: f.haiku_model, small: f.small_model, subagent: f.subagent_model })[row];
  const change = (row: TierRow) => (v: string) =>
    setF((x) => (row === "haiku" ? { ...x, haiku_model: v } : row === "subagent" ? { ...x, subagent_model: v } : setTier(x, row === "small" ? "light" : row, v)));
  return (
    <section aria-labelledby={titleId} className="flex flex-col gap-2.5 rounded-card bg-inset p-3 shadow-hairline">
      <div className="flex items-start gap-3">
        <span className="min-w-0 flex-1">
          <span id={titleId} className="block text-[12.5px] font-medium text-ink-2">{t("settings-providerwizard.tiers_title")}</span>
          <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">{t("settings-providerwizard.tiers_intro")}</span>
        </span>
        <button type="button" onClick={onToggle} aria-expanded={open} aria-controls={bodyId}
          className="touch-hit inline-flex shrink-0 items-center gap-1 whitespace-nowrap text-[12px] font-medium text-accent hover:underline">
          {open ? t("settings-providerwizard.tiers_close") : t("settings-providerwizard.tiers_adjust")}
          <Icon name="chevron" size={10} stroke={2.4} className="transition-transform duration-150" style={{ transform: open ? "rotate(180deg)" : "none" }} />
        </button>
      </div>
      <div id={bodyId}>
        {open ? (
          <div className="grid gap-3 sm:grid-cols-2">
            {lines.map((l) => (
              <TierSelect key={l.row} label={ROW_NAME[l.row]()} hint={ROW_HINT[l.row]?.()} value={value(l.row)} ids={ids} def={def} auto={l.auto} onChange={change(l.row)}
                // left empty, the Haiku tier runs the small model
                empty={l.row === "haiku" && f.small_model.trim() ? t("settings-providerwizard.same_as_small", { model: f.small_model.trim() }) : undefined} />
            ))}
          </div>
        ) : (
          <dl className="grid grid-cols-[max-content_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1.5">
            {lines.map((l) => (
              <div key={l.row} className="contents">
                <dt className="text-[11.5px] text-ink-3">{ROW_NAME[l.row]()}</dt>
                <dd className="flex min-w-0 items-center gap-1.5">
                  <span className={cx("truncate font-mono text-[12px]", l.model ? "text-ink" : "text-ink-3")}>{l.model || t("settings-providerwizard.same_as_default")}</span>
                  {l.auto ? <Pill>{t("settings-providerwizard.tier_auto")}</Pill> : l.follows && l.model ? (
                    <span className="shrink-0 whitespace-nowrap text-[11px] text-ink-3">{l.follows === "small" ? t("settings-providerwizard.tier_follows_small") : t("settings-providerwizard.tier_is_default")}</span>
                  ) : null}
                </dd>
              </div>
            ))}
          </dl>
        )}
      </div>
      {lines.some((l) => l.auto) && <p className="text-[11px] leading-relaxed text-ink-3">{t("settings-providerwizard.tiers_auto_note")}</p>}
      {advice && (
        <div role="status" className={cx("flex items-start gap-1.5 text-[12px] leading-relaxed", advice.sameAsOpus ? "text-orange" : "text-ink-2")}>
          <Icon name={advice.sameAsOpus ? "warning" : "sparkles"} size={12} stroke={2.2} className="mt-1 shrink-0" />
          <span className="min-w-0 flex-1 break-words">
            {adviceText(advice)}
          </span>
          <LineAction onClick={() => setF((x) => applyTiers(x, advice.tiers))}>{t("settings-providerwizard.tiers_apply")}</LineAction>
        </div>
      )}
    </section>
  );
}
