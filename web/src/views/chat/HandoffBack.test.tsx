// @vitest-environment jsdom
// The way back from an Agent's page to the conversation that handed it work (#5096, cindy's HandoffSourcePill).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { getState, reset } from "../../lib/store";
import type { Session } from "../../lib/types";
import { fromTitle, HandoffBack } from "./HandoffBack";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;
beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});
afterEach(() => {
  act(() => root.unmount());
  host.remove();
  reset();
  vi.restoreAllMocks();
});

const conv = (session_id: string, title: string) => ({ machine_id: "m1", session_id, title, cwd: "", status: "idle" }) as Session;

describe("the way back", () => {
  it("names the conversation: Persona for the main one, its title for another", () => {
    const s = { assistant: { ...getState().assistant!, main: "main-1", conversations: [conv("main-1", "whatever"), conv("c2", "Trip to Kyoto")] } };
    expect(fromTitle(s, "/persona")).toBe("Persona");
    expect(fromTitle(s, "/persona/main-1")).toBe("Persona");
    expect(fromTitle(s, "/persona/c2?machine=m1")).toBe("Trip to Kyoto");
  });

  it("goes back in the history, and is put away by its ✕", () => {
    history.pushState({ handoff: { path: "/persona" } }, "", "/s/m1/s1"); // the card pushed the Agent's page
    const back = vi.spyOn(history, "back").mockImplementation(() => {});
    const dismiss = vi.fn();
    act(() => root.render(<HandoffBack from={{ path: "/persona" }} onDismiss={dismiss} />));
    const [go, close] = [...host.querySelectorAll("button")];
    expect(go!.textContent).toBe(t("chat-chatview.handoff_from", { title: "Persona" }));
    act(() => go!.click());
    expect(back).toHaveBeenCalledOnce();
    expect(close!.getAttribute("aria-label")).toBe(t("chat-chatview.handoff_dismiss"));
    act(() => close!.click());
    expect(dismiss).toHaveBeenCalledOnce();
  });
});
