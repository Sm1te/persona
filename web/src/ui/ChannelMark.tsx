// A channel's mark (docs/extensions/channels.md "UI"): Feishu's own for the Feishu bot and /agent, else an icon of its kind —
// WeChat's mark and its terms of use are undecided (lib/connectors.ts), so it draws the generic chat icon until they are.
import type { Channel } from "../lib/channels";
import type { BrandId } from "../lib/connectors";
import { externalFrom, ORIGIN_BRAND, ORIGIN_ICON, type ExternalOrigin } from "../lib/conversation-origins";
import { cx } from "./atoms";
import { BrandMark } from "./BrandMark";
import { Icon, type IconName } from "./icons";

/** `size` is the tile's; the apps' tile is filled, the others tinted. */
export function ChannelMark({ c, size }: { c: Pick<Channel, "id" | "kind">; size: number }) {
  if (c.id === "feishu" || c.id === "lark_agent") return <BrandTile brand="lark" size={size} />;
  const icon: IconName = c.kind === "app" ? "spark" : c.kind === "voice" ? "mic" : c.kind === "mention" ? "at" : "chat";
  return (
    <span className={cx("flex shrink-0 items-center justify-center", c.kind === "app" ? "bg-accent text-white" : "bg-accent-tint text-accent-ink")} style={{ width: size, height: size, borderRadius: radius(size) }}>
      <Icon name={icon} size={Math.round(size * 0.48)} />
    </span>
  );
}

/** Where an external conversation came from (#3758, lib/conversation-origins.ts), in a channel mark's place and size: Lark's
 *  tile for Lark /agent, as its channel's; the "Extensions" group's icon in the secondary ink for another extension. One
 *  per row, in place of the channel's. Named "From Lark /agent" / "From an extension", as its tooltip too; `decorative`
 *  beside words that say it already (under the first message). */
export function OriginMark({ origin, size, decorative, className }: { origin: ExternalOrigin; size: number; decorative?: boolean; className?: string }) {
  const brand = ORIGIN_BRAND[origin];
  const label = externalFrom(origin);
  return (
    <span data-origin-mark={origin} {...(decorative ? { "aria-hidden": true } : { role: "img", "aria-label": label, title: label })} className={cx("flex shrink-0 items-center justify-center", className)} style={{ width: size, height: size }}>
      {brand ? <BrandTile brand={brand} size={size} /> : <Icon name={ORIGIN_ICON[origin]} size={size - GLYPH_INSET} className="text-ink-2" />}
    </span>
  );
}

/** How much smaller than its box an icon is drawn, so its weight matches a tile's: 14 in a row's 16, as SourceTag draws
 *  an agent's source, 12 beside a message's 14. */
const GLYPH_INSET = 2;

function BrandTile({ brand, size }: { brand: BrandId; size: number }) {
  return (
    <span className="flex shrink-0 items-center justify-center bg-surface shadow-hairline" style={{ width: size, height: size, borderRadius: radius(size) }}>
      <BrandMark brand={brand} size={size} fit="tile" />
    </span>
  );
}

/** The tile's corner: 9 at a row's 32, in proportion below (a 14 px mark beside a message). */
function radius(size: number): number {
  return Math.max(3, Math.round(size * 0.28));
}
