// A model provider or launcher named in a row (#5029, docs/design/DESIGN_SYSTEM.md "Provider display names"): its
// display name, then its startup ID in small monospace; one without a display name (an rc alias) by its ID alone, in
// monospace as before. The words come from lib/provider-names.ts.
import { providerIdNote, providerTitle, type Named } from "../lib/provider-names";
import { cx } from "./atoms";

/** `className` styles the name, `idClassName` the ID after it (monospace either way). */
export function ProviderName({ of, className, idClassName }: { of: Named; className?: string; idClassName?: string }) {
  const id = providerIdNote(of);
  return (
    <>
      <span className={cx(className, !id && "font-mono")}>{providerTitle(of)}</span>
      {id && (
        <>
          {" "}
          <span className={cx("font-mono", idClassName)}>{id}</span>
        </>
      )}
    </>
  );
}
