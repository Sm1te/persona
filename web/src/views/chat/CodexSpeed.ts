// Loaded when the user opens Speed, keeping native speed controls out of the first screen's bundle.
import { t } from "../../i18n";
import type { Session } from "../../lib/types";
import type { MenuItem } from "../../ui/menu-model";
import { setServiceTier } from "./SessionDialogs";

export function codexSpeedOptions(session: Session): MenuItem[] {
  return (session.service_tiers ?? []).map((tier) => ({
    key: tier.id,
    label: tier.id === "default" ? t("chat-sessiondialogs.speed_standard") : tier.id === "priority" ? t("chat-sessiondialogs.speed_fast") : tier.name,
    detail: tier.id === "default" ? t("chat-sessiondialogs.speed_standard_detail") : t("chat-sessiondialogs.speed_paid_detail"),
    radio: true,
    checked: session.service_tier === tier.id,
    // The session push after native acknowledgment owns the displayed value.
    onSelect: () => setServiceTier(session, tier.id),
  }));
}
