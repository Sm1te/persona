// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({ api }));

import { forgetProviderNames, launcherTitle, useProviderNames } from "./provider-names";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function Title({ id }: { id: string }) {
  return <span>{launcherTitle(id, useProviderNames())}</span>;
}

let root: Root | null = null;
let host: HTMLElement | null = null;

afterEach(() => {
  act(() => root?.unmount());
  host?.remove();
  root = host = null;
  forgetProviderNames();
  api.mockReset();
});

describe("useProviderNames", () => {
  it("reads the names again when they are forgotten, without remounting", async () => {
    api.mockResolvedValueOnce({ providers: [{ id: "launcher.relay", name: "Team relay" }] });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    await act(async () => root!.render(<Title id="relay" />));
    expect(host.textContent).toBe("Team relay");

    // renamed in the provider wizard, which forgets the names it saved over
    api.mockResolvedValueOnce({ providers: [{ id: "launcher.relay", name: "团队中转" }] });
    await act(async () => forgetProviderNames());
    expect(host.textContent).toBe("团队中转");
    expect(api).toHaveBeenCalledTimes(2);
  });
});
