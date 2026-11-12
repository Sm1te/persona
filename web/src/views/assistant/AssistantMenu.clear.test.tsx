// @vitest-environment jsdom
// The main chat's ⋯ menu has "Clear context" (#5095) next to "Compact context" and "New main chat…" when the hub can
// clear it; choosing it clears at once (no dialog, as /clear). Other conversations, and older hubs, have no such row.
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MenuItem } from "../../ui/atoms";
import type { Session } from "../../lib/types";

const hub = vi.hoisted(() => ({ calls: [] as { path: string; method: string; json?: unknown }[] }));
vi.mock("../../lib/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/api")>()),
  api: (path: string, init?: { method?: string; json?: unknown }) => {
    hub.calls.push({ path, method: init?.method ?? "GET", json: init?.json });
    return Promise.resolve(path === "/assistant" ? { machine_id: "hub", main: "", conversations: [] } : { ok: true });
  },
}));
const navigate = vi.hoisted(() => vi.fn());
vi.mock("../../lib/router", async (orig) => ({ ...(await orig<typeof import("../../lib/router")>()), navigate }));

import { forgetMainCleared } from "../../lib/main-clear";
import { getState, reset } from "../../lib/store";
import { useAssistantMenu } from "./AssistantChat";

const SESSION = { machine_id: "hub", session_id: "c1", title: "", cwd: "/", assistant: true, status: "idle" } as Session;
let items: MenuItem[] = [];
function Probe({ session }: { session: Session }) {
  items = useAssistantMenu(session).items;
  return null;
}
const keys = () => items.map((i) => i.key);
function render(session: Session) {
  const host = document.createElement("div");
  const root = createRoot(host);
  act(() => root.render(<Probe session={session} />));
  act(() => root.unmount());
}
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

beforeEach(() => {
  reset();
  hub.calls.length = 0;
  navigate.mockClear();
  Object.assign(getState(), {
    features: ["assistant_compact", "assistant_main_new", "assistant_main_clear"],
    assistant: { machine_id: "hub", main: "c1", conversations: [] },
  });
});
afterEach(() => {
  forgetMainCleared();
  reset();
});

describe("Clear context in the main chat's menu", () => {
  it("sits between Compact context and New main chat…, and clears at once", async () => {
    render(SESSION);
    expect(keys().slice(keys().indexOf("compact"), keys().indexOf("newmain") + 1)).toEqual(["compact", "clear", "newmain"]);
    const clear = items.find((i) => i.key === "clear")!;
    expect(clear.label).toBe("清理上下文");
    clear.onSelect?.();
    await vi.waitFor(() => expect(navigate).toHaveBeenCalledWith({ name: "persona" }));
    expect(hub.calls).toContainEqual({ path: "/assistant/main/clear", method: "POST", json: { expect: "c1" } });
  });

  it("is not offered for another conversation, nor by a hub that cannot clear", () => {
    render({ ...SESSION, session_id: "c9" });
    expect(keys()).not.toContain("clear");
    getState().features = ["assistant_compact", "assistant_main_new"];
    render(SESSION);
    expect(keys()).not.toContain("clear");
  });
});
