// Disconnect this browser (#1873, docs/web/web.md "Sign-in"): revoke its own credential on the hub, then sign out here.
// A revoke that does not go through is said, never signed out over in silence: the credential would stay valid on the
// hub with nothing on screen to tell. Signing out anyway keeps its id apart, so the next sign-in here names it in
// `replaces` (lib/auth.ts pair) and the hub takes it off then.
import { api, ApiError, errorText, setToken } from "./api";
import { forgetForwards } from "./forward-memory";
import { forgetHandOffs } from "./handoff-memory";
import { keepUnrevoked } from "./auth";
import { BASE } from "./base";
import { storedCredentials } from "./compatibility";
import { reset } from "./store";

/** A revoke the hub has not answered by then counts as not done (writes have no time limit of their own). */
export const REVOKE_TIMEOUT_MS = 15_000;

export type Revoke =
  /** Revoked, or nothing valid was left of it (the hub no longer knows it, or no longer takes it). */
  | { ok: true }
  /** Nothing this browser can revoke: it is signed in with the hub's own token, or the hub gives browsers none of their own. */
  | { ok: false; nothing: true }
  /** The hub did not revoke it: why, and the id to keep for the next sign-in. */
  | { ok: false; error: string; id: string };

/** Revoke the credential this browser uses: the id it got signing in, else the one the hub says the request came with
 *  (a device token pasted on the sign-in page). */
export async function revokeThisBrowser(): Promise<Revoke> {
  let id = storedCredentials().tokenId;
  try {
    if (!id) {
      const r = await api<{ current?: string }>("/auth/tokens", { timeout: REVOKE_TIMEOUT_MS }).catch((e: unknown): { current?: string } => {
        if (e instanceof ApiError && e.status === 404) return {}; // a hub from before device tokens
        throw e;
      });
      id = r.current ?? "";
      // the hub's own token (no id), or a pairing code kept by a hub from before the exchange: not a device to revoke
      if (!id || id.startsWith("pair_")) return { ok: false, nothing: true };
    }
    await api(`/auth/tokens/${encodeURIComponent(id)}`, { method: "DELETE", timeout: REVOKE_TIMEOUT_MS });
    return { ok: true };
  } catch (e) {
    // 404: the hub has no such device (revoked already); 401: it no longer takes this credential at all
    if (e instanceof ApiError && (e.status === 404 || e.status === 401)) return { ok: true };
    return { ok: false, error: errorText(e), id };
  }
}

/** Forget this browser's credential and go to the sign-in page. `unrevoked`: the id of one the hub may still take. */
export function signOutHere(unrevoked = "") {
  setToken("");
  forgetHandOffs(); // where Hand to AI sent each source's items: ids on a hub this browser leaves (#2121)
  forgetForwards(); // and where forwards went (#3899)
  if (unrevoked) keepUnrevoked(unrevoked);
  reset();
  location.href = BASE + "/";
}
