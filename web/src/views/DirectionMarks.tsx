// The direction tree's two marks (#4352) on the To-dos page and the Agents page: a direction's counts, in the words and
// the shape of the sidebar's 常驻 rows (lib/direction-names.ts countsLabel: the non-zero parts), and a task's state word.
// Text, so a state never rides on color alone.
import { memo } from "react";
import { t } from "../i18n";
import { countsLabel } from "../lib/direction-names";
import { DIRECTION_STATE_TONE, directionStateName, type DirectionCounts, type DirectionTaskState } from "../lib/direction-tree";
import { cx } from "../ui/atoms";

/** "在做 N · 等你 N" from the hub's counts as the sidebar's 常驻 rows say it (only the parts above zero, nothing when both
 *  are zero); "等你 N" in the needs-you tone. */
export const DirectionCountsText = memo(function DirectionCountsText({ counts, className }: { counts: DirectionCounts; className?: string }) {
  const label = countsLabel(counts, t);
  if (!label) return null;
  const working = counts.working > 0 ? t("sidebar.leader_working", { count: counts.working }) : "";
  const needsYou = counts.needs_you > 0 ? t("sidebar.leader_needs_you", { count: counts.needs_you }) : "";
  return (
    <span title={label} data-direction-counts className={cx("shrink-0 whitespace-nowrap text-[11px] text-ink-3 tabular-nums", className)}>
      <span className="sr-only">{label}</span>
      <span aria-hidden>
        {working}
        {working && needsYou && " · "}
        {needsYou && <span className="font-semibold text-attention">{needsYou}</span>}
      </span>
    </span>
  );
});

/** A task's state word in its tone: 等你 / 待验收 / 卡住 / 在做. */
export const DirectionStateText = memo(function DirectionStateText({ state }: { state: DirectionTaskState }) {
  return <span data-task-state={state} className={cx("shrink-0 text-[11px] font-semibold whitespace-nowrap", DIRECTION_STATE_TONE[state])}>{directionStateName(state)}</span>;
});
