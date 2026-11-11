// A model provider's mark (#5032): Persona has no vendor logos for launchers, so every place that needs one (the model
// pill, the panel's rail, rows and flyout) draws the provider's first letter in a hairline tile, the way Cindy draws a
// provider it has no logo for: 15 × 15, 4 px corners, a 1 px border and the letter at 10 px semibold, all in the current
// text color. Its own file so the pill imports it without the panel.
import { agentLetter } from "../../lib/agent-kind";
import { cx } from "../atoms";

// the tile's side (the letter is two thirds of it): 15 px unless a class on an ancestor (a container query) sets
// --provider-mark
const SIDE = "var(--provider-mark, 15px)";
const STYLE = { width: SIDE, height: SIDE, fontSize: `calc(${SIDE} * 2 / 3)` };

/** Decorative: the provider's name is said by what it stands beside. */
export function ProviderMark({ name, className }: { name: string; className?: string }) {
  return (
    <span aria-hidden data-provider-mark className={cx("inline-flex shrink-0 items-center justify-center rounded-[4px] border border-current leading-none font-semibold", className)} style={STYLE}>
      {agentLetter(name)}
    </span>
  );
}
