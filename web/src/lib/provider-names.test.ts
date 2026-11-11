import { afterEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => vi.fn());
vi.mock("./api", () => ({ api }));

import { forgetProviderNames, launcherTitle, loadProviderNames, namedLauncher, namesFromProviders, providerIdNote, providerLabel, providerTitle } from "./provider-names";

describe("provider names", () => {
  it("leads with the display name and keeps the ID as the note", () => {
    const l = { name: "team-relay", display_name: "我的中转 站" };
    expect(providerTitle(l)).toBe("我的中转 站");
    expect(providerIdNote(l)).toBe("team-relay");
    expect(providerLabel(l)).toBe("我的中转 站 (team-relay)");
  });

  it("shows the ID alone without a display name, a blank one, or one equal to the ID", () => {
    for (const l of [{ name: "cc_glm" }, { name: "cc_glm", display_name: "  " }, { name: "cc_glm", display_name: "cc_glm" }]) {
      expect(providerTitle(l)).toBe("cc_glm");
      expect(providerIdNote(l)).toBe("");
      expect(providerLabel(l)).toBe("cc_glm");
    }
  });

  it("trims the display name", () => {
    expect(providerTitle({ name: "glm", display_name: " Zhipu GLM " })).toBe("Zhipu GLM");
  });

  it("looks a launcher up by its startup ID", () => {
    const list = [{ name: "glm", display_name: "Zhipu GLM" }, { name: "cc_kimi" }];
    expect(launcherTitle("glm", list)).toBe("Zhipu GLM");
    expect(launcherTitle("cc_kimi", list)).toBe("cc_kimi");
    expect(launcherTitle("gone", list)).toBe("gone");
    expect(launcherTitle("glm", undefined)).toBe("glm");
    expect(namedLauncher("glm", list)).toEqual({ name: "glm", display_name: "Zhipu GLM" });
  });
});

describe("names from the model configuration", () => {
  it("takes launcher providers, the hub's name as the display name", () => {
    const providers = [
      { id: "launcher.relay-example-com", name: "我的中转 站" },
      { id: "launcher.cc_glm", name: "cc_glm" },
      { id: "launcher.claude", name: "Claude Code default" },
      { id: "legacy.ai", name: "Processing" },
      { id: "plugin.voice", name: "Voice" },
    ];
    expect(namesFromProviders(providers)).toEqual([{ name: "relay-example-com", display_name: "我的中转 站" }, { name: "cc_glm" }]);
    expect(namesFromProviders(undefined)).toEqual([]);
  });
});

describe("loadProviderNames", () => {
  afterEach(() => {
    forgetProviderNames();
    api.mockReset();
  });

  it("reads each assistant's providers once, naming the assistant", async () => {
    api.mockImplementation((_path: string, init: { headers?: Record<string, string> }) =>
      Promise.resolve({ providers: [{ id: "launcher.relay", name: init.headers ? "Other relay" : "Team relay" }] }));
    expect(await loadProviderNames()).toEqual([{ name: "relay", display_name: "Team relay" }]);
    expect(await loadProviderNames("work")).toEqual([{ name: "relay", display_name: "Other relay" }]);
    await loadProviderNames();
    expect(api).toHaveBeenCalledTimes(2);
    expect(api).toHaveBeenNthCalledWith(1, "/model-config", { headers: undefined });
    expect(api).toHaveBeenNthCalledWith(2, "/model-config", { headers: { "X-Persona-Manager": "work" } });
  });

  it("gives no names when the hub can't answer, and asks again next time", async () => {
    api.mockRejectedValueOnce(new Error("404"));
    expect(await loadProviderNames()).toEqual([]);
    api.mockResolvedValueOnce({ providers: [{ id: "launcher.relay", name: "Team relay" }] });
    expect(await loadProviderNames()).toEqual([{ name: "relay", display_name: "Team relay" }]);
  });
});
