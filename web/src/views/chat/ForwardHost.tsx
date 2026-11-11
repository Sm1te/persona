// Mounted once, with the app shell (#3899): Forward to opens for whichever chat's message asked (lib/forward.ts
// openForward). The dialog is its own chunk, loaded on the first Forward (not at idle: IDLE_TIER_BUDGET).
import { Suspense, useCallback, useEffect, useState } from "react";
import { onOpenForward, type ForwardDraft } from "../../lib/forward";
import { lazyPage } from "../../lib/lazy-page";
import { OverlayBoundary } from "../LazyOverlay";

const ForwardDialog = lazyPage(() => import("./ForwardDialog").then((m) => m.ForwardDialog));

export function ForwardHost() {
  // the last draft stays while the dialog plays its exit; each open is a fresh dialog (no search left from the last)
  const [draft, setDraft] = useState<{ d: ForwardDraft; n: number } | null>(null);
  const [open, setOpen] = useState(false);
  useEffect(
    () =>
      onOpenForward((d) => {
        setDraft((cur) => ({ d, n: (cur?.n ?? 0) + 1 }));
        setOpen(true);
      }),
    [],
  );
  const close = useCallback(() => setOpen(false), []);
  if (!draft) return null;
  return (
    <OverlayBoundary open={open} onClose={close}>
      <Suspense fallback={null}>
        <ForwardDialog key={draft.n} draft={draft.d} open={open} onClose={close} />
      </Suspense>
    </OverlayBoundary>
  );
}
