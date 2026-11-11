// The provider wizard's form pieces (#2455): a labelled input, a secret one (keys, headers: never offered to the
// browser's password manager, shown only on request), and the "Advanced" disclosure.
import { useId, useState } from "react";
import { Collapse, cx, SECRET_INPUT } from "../../ui/atoms";
import { Icon } from "../../ui/icons";
import { inputClass, textareaClass } from "../assistant/parts";
import { t } from "../../i18n";

export const INPUT = inputClass;
/** A field's look without its size, for the inline ones (SMALL: in a model row). */
export const FIELD = "rounded-control bg-field text-ink shadow-inset-field outline-none placeholder:text-ink-3 focus:shadow-[inset_0_0_0_1px_var(--accent)] disabled:opacity-60";
export const SMALL = `${FIELD} h-7 px-2 text-[12px]`;

/** `describedById` goes on the hint / error line so the field can point `aria-describedby` at it. */
export function Labelled({ label, hint, error, children, htmlFor, describedById }: { label: React.ReactNode; hint?: React.ReactNode; error?: string; children: React.ReactNode; htmlFor?: string; describedById?: string }) {
  return (
    <div className="block min-w-0">
      <label htmlFor={htmlFor} className="mb-1 block text-[12px] font-medium text-ink-2">
        {label}
      </label>
      {children}
      {(error || hint) && <span id={describedById} className={cx("mt-1 block text-[11px] leading-relaxed", error ? "text-red" : "text-ink-3")}>{error || hint}</span>}
    </div>
  );
}

export function TextField({ label, value, onChange, hint, error, placeholder, mono, disabled, inputMode, onPaste, onBlur }: {
  label: React.ReactNode;
  value: string;
  onChange: (v: string) => void;
  hint?: React.ReactNode;
  error?: string;
  placeholder?: string;
  mono?: boolean;
  disabled?: boolean;
  inputMode?: "numeric" | "url";
  onPaste?: React.ClipboardEventHandler<HTMLInputElement>;
  onBlur?: React.FocusEventHandler<HTMLInputElement>;
}) {
  const id = useId();
  const noteId = useId();
  return (
    <Labelled label={label} hint={hint} error={error} htmlFor={id} describedById={noteId}>
      <input id={id} aria-invalid={error ? true : undefined} aria-describedby={error || hint ? noteId : undefined} value={value}
        onChange={(e) => onChange(e.target.value)} onPaste={onPaste} onBlur={onBlur} disabled={disabled} placeholder={placeholder} inputMode={inputMode} autoComplete="off" spellCheck={false} className={cx(INPUT, mono && "font-mono")} />
    </Labelled>
  );
}

/** A key or other secret: masked, with "Show" / "Hide"; `multiline` for request headers (masked as discs until shown). */
export function SecretField({ label, value, onChange, hint, placeholder, multiline }: {
  label: React.ReactNode;
  value: string;
  onChange: (v: string) => void;
  hint?: React.ReactNode;
  placeholder?: string;
  multiline?: boolean;
}) {
  const id = useId();
  const noteId = useId();
  const [shown, setShown] = useState(false);
  // password managers: none of them should offer to save a gateway key as the hub's password
  const quiet = { ...SECRET_INPUT, autoComplete: "off" } as const;
  const described = hint ? noteId : undefined;
  const toggle = (
    <button type="button" onClick={() => setShown((s) => !s)} aria-label={shown ? t("settings-providerfields.hide") : t("settings-providerfields.show")} aria-pressed={shown}
      className="touch-hit absolute top-1.5 right-1.5 flex size-6 items-center justify-center rounded-chip text-ink-3 hover:bg-hover hover:text-ink">
      <Icon name={shown ? "lock" : "key"} size={12} stroke={2} />
    </button>
  );
  return (
    <Labelled label={label} hint={hint} htmlFor={id} describedById={noteId}>
      <div className="relative">
        {multiline ? (
          <textarea id={id} aria-describedby={described} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} rows={3} {...quiet}
            className={cx(textareaClass, "pr-9 font-mono", !shown && "[-webkit-text-security:disc]")} />
        ) : (
          <input id={id} aria-describedby={described} type={shown ? "text" : "password"} value={value} onChange={(e) => onChange(e.target.value)} placeholder={placeholder} {...quiet} autoComplete="new-password" className={cx(INPUT, "pr-9")} />
        )}
        {toggle}
      </div>
    </Labelled>
  );
}

/** "Advanced": folded by default. */
export function Disclosure({ title, open, onToggle, children, badge }: { title: string; open: boolean; onToggle: () => void; children: React.ReactNode; badge?: string }) {
  return (
    <div className="rounded-card bg-inset shadow-hairline">
      <button type="button" onClick={onToggle} aria-expanded={open} className="touch-hit flex w-full items-center gap-2 px-3 py-2.5 text-left text-[12.5px] font-medium text-ink-2 hover:text-ink">
        <Icon name="chevron" size={10} stroke={2.4} className="transition-transform duration-150" style={{ transform: open ? "none" : "rotate(-90deg)" }} />
        <span className="whitespace-nowrap">{title}</span>
        {badge && <span className="truncate text-[11px] font-normal text-ink-3">{badge}</span>}
      </button>
      <Collapse open={open} lazy>
        <div className="flex flex-col gap-3 px-3 pt-1 pb-3">{children}</div>
      </Collapse>
    </div>
  );
}

/** The tick of a role="checkbox" row. */
export function CheckMark({ on, className }: { on: boolean; className?: string }) {
  return (
    <span aria-hidden className={cx("flex size-4 shrink-0 items-center justify-center rounded-chip transition-colors duration-150", on ? "bg-accent text-white" : "shadow-[inset_0_0_0_1.5px_var(--line-strong)]", className)}>
      {on && <Icon name="check" size={10} stroke={3} />}
    </span>
  );
}
