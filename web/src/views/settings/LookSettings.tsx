// Settings › General › Appearance's "Theme color" and "Text size" (ui/look.ts; the app's ThemeColorPicker and StyleSettingsPage's "Text size"),
// and the web's own "Recent chats in the sidebar" (lib/sidebar-recent.ts).
import { useSyncExternalStore } from "react";
import { t } from "../../i18n";
import { cx } from "../../ui/atoms";
import { pageToken } from "../../ui/extension-theme";
import { useActiveExtensionTheme } from "../../ui/ExtensionThemePicker";
import { Icon } from "../../ui/icons";
import {
  ACCENT_PRESETS, accentTokens, PRESET_COLORS, setAccentChoice, setTextSize, TEXT_SIZES, useAccentChoice, usePersonaAccent, useTextSize,
  type AccentChoice, type TextSize,
} from "../../ui/look";
import { RECENT_COUNTS, setRecentCount, useRecentCount } from "../../lib/sidebar-recent";
import { usePersona } from "../assistant/Persona";
import { Segmented } from "../assistant/parts";
import { Row } from "./SettingsParts";

function subscribeScheme(l: () => void) {
  const o = new MutationObserver(l);
  o.observe(document.documentElement, { attributes: true, attributeFilter: ["class"] });
  return () => o.disconnect();
}
const darkNow = () => document.documentElement.classList.contains("dark");

/**
 * "Theme color": "Default" (the page's own blue), "Match avatar" when the persona brings colors (or it is the choice already, so it stays in
 * sight), then the five fixed colors, each a disc of its accent with where it comes from under its name.
 */
export function ThemeColorPicker() {
  const choice = useAccentChoice();
  const personaAccent = usePersonaAccent();
  const persona = usePersona();
  const extension = useActiveExtensionTheme();
  const scheme = useSyncExternalStore(subscribeScheme, darkNow) ? "dark" : "light";
  const options: AccentChoice[] = ["default", ...(personaAccent || choice === "persona" ? (["persona"] as const) : []), ...ACCENT_PRESETS];
  // The disc is the color the page gets: the same contrast floor as accentTokens; "Default"'s is styles.css's own.
  const disc = (c: AccentChoice): string => {
    if (c === "default") return pageToken("accent", scheme);
    const pair = c === "persona" ? personaAccent : PRESET_COLORS[c].accent;
    return pair ? accentTokens(pair)[scheme]["--accent"] : pageToken("accent", scheme);
  };
  const sub = (c: AccentChoice) => (c === "persona" ? (personaAccent ? persona.name : t("ui-look.persona_none")) : t(`ui-look.${c}_sub`));
  return (
    <div className="px-4 py-3">
      <div className="text-[13px] text-ink">{t("ui-look.accent")}</div>
      <div className="mt-0.5 text-[11.5px] text-ink-3">{t("ui-look.accent_hint")}</div>
      <div role="radiogroup" aria-label={t("ui-look.accent")} className="mt-3 grid grid-cols-4 gap-x-1 gap-y-3 sm:grid-cols-7">
        {options.map((c) => {
          const selected = c === choice;
          const color = disc(c);
          return (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={selected}
              aria-label={t("ui-look.swatch_label", { name: t(`ui-look.${c}`), origin: sub(c) })}
              onClick={() => !selected && setAccentChoice(c)}
              className="group flex min-w-0 flex-col items-center gap-1.5 rounded-control px-0.5 py-1 transition-colors hover:bg-hover"
            >
              <span
                className="flex size-10 items-center justify-center rounded-full p-[3px] transition-shadow duration-150"
                style={selected ? { boxShadow: `inset 0 0 0 2px ${color}` } : undefined}
              >
                {/* the disc is data (a color to choose), like the persona picker's swatches */}
                <span className="flex size-full items-center justify-center rounded-full text-white shadow-hairline" style={{ background: color }}>
                  {selected && <Icon name="check" size={14} stroke={2.6} />}
                </span>
              </span>
              <span className={cx("max-w-full truncate text-[12px] whitespace-nowrap", selected ? "font-medium text-ink" : "text-ink-2")}>{t(`ui-look.${c}`)}</span>
              <span className="-mt-1 max-w-full truncate text-[11px] whitespace-nowrap text-ink-3">{sub(c)}</span>
            </button>
          );
        })}
      </div>
      {extension && <p className="mt-2.5 text-[11.5px] leading-relaxed text-ink-3">{t("ui-look.extension_over", { name: extension.name })}</p>}
    </div>
  );
}

/** "Text size": "Small" / "Standard" / "Large", the whole page's text (src/lib/text-scale.ts). */
export function TextSizeRow() {
  const size = useTextSize();
  return (
    <Row label={t("ui-look.text_size")} hint={t("ui-look.text_size_hint")}>
      <Segmented<TextSize> label={t("ui-look.text_size")} value={size} options={TEXT_SIZES.map((v) => ({ v, label: t(`ui-look.${v}`) }))} onChange={setTextSize} />
    </Row>
  );
}

/** "Recent chats in the sidebar": how many Persona chats › Recent lists before Show more (lib/sidebar-recent.ts, #5146). */
export function RecentChatsRow() {
  const count = useRecentCount();
  return (
    <Row label={t("ui-look.recent_chats")} hint={t("ui-look.recent_chats_hint")} wrapHint>
      <Segmented label={t("ui-look.recent_chats")} value={String(count)} options={RECENT_COUNTS.map((n) => ({ v: String(n), label: String(n) }))} onChange={(v) => setRecentCount(Number(v))} />
    </Row>
  );
}
