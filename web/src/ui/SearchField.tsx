// A search box: the magnifier, the field and "Clear" in one rounded control that lights its edge in the accent while it has
// focus, like every search field of the pages (inbox, knowledge base, history…). Pages that wrote their own forgot that ring and
// showed only a caret (to-dos, Terminal, Agents), so a new search box takes this one.
import type { KeyboardEvent } from "react";
import { t } from "../i18n";
import { cx, IconButton } from "./atoms";
import { Icon } from "./icons";

/** `surface` sits on the page (a raised control); `inset` sits in a list column (a sunken one). */
export type SearchTone = "surface" | "inset";

const TONE: Record<SearchTone, string> = {
  surface: "bg-surface shadow-btn",
  inset: "bg-inset shadow-hairline",
};

// the focus ring: the same inset accent edge as the other search fields, in place of the tone's own edge
export const SEARCH_FOCUS = "focus-within:shadow-[inset_0_0_0_1px_var(--accent)]";

export function SearchField({
  value,
  onChange,
  placeholder,
  label,
  tone = "surface",
  size = "sm",
  icon = true,
  onKeyDown,
  className,
  autoFocus,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  /** The field's accessible name; the placeholder when not given. */
  label?: string;
  tone?: SearchTone;
  /** `sm` 32px high (a list column, a toolbar), `md` 36px (a page's own search). */
  size?: "sm" | "md";
  icon?: boolean;
  onKeyDown?: (e: KeyboardEvent<HTMLInputElement>) => void;
  /** Placement only (margins, shrink); the look is the tone's. */
  className?: string;
  autoFocus?: boolean;
}) {
  return (
    <label className={cx("flex items-center gap-2 rounded-control", size === "sm" ? "h-8 px-2.5" : "h-9 px-3", TONE[tone], SEARCH_FOCUS, className)}>
      {icon && <Icon name="search" size={13} stroke={2} className="shrink-0 text-ink-3" />}
      <input
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={onKeyDown}
        autoFocus={autoFocus}
        placeholder={placeholder}
        aria-label={label ?? placeholder}
        className={cx("min-w-0 flex-1 bg-transparent text-ink outline-none placeholder:text-ink-3", size === "sm" ? "text-[12.5px]" : "text-[13px]")}
      />
      {value && <IconButton icon="x" label={t("ui-searchfield.clear")} size={size === "sm" ? 20 : 22} iconSize={size === "sm" ? 11 : 12} onClick={() => onChange("")} />}
    </label>
  );
}
