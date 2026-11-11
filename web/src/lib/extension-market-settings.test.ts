import { afterEach, describe, expect, it, vi } from "vitest";
import { getExtensionMarketSettings, setExtensionMarketSettings, validMirrorURL } from "./extension-market-settings";
import { credentialOwner, errorText, getToken } from "./api";
import { t } from "../i18n";

const settings = { registry_mirror: "https://registry.example.com/prefix", catalog_mirror: "https://catalog.example.com/persona-extensions.json" };
const context = () => ({ token: getToken(), tokenOwner: credentialOwner(), isAuthContextCurrent: () => true });
afterEach(() => vi.unstubAllGlobals());

describe("extension manager mirror API", () => {
  it("pins GET and PUT to the selected manager and sends empty strings to clear", async () => {
    const calls: { url: string; method: string; body?: string }[] = [];
    vi.stubGlobal("localStorage", { getItem: () => "synthetic", setItem: () => {}, removeItem: () => {} });
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      calls.push({ url, method: init.method ?? "GET", body: init.body as string | undefined });
      return new Response(JSON.stringify(init.body ? JSON.parse(init.body as string) : settings));
    });
    expect(await getExtensionMarketSettings("m/one", context())).toEqual(settings);
    const empty = { registry_mirror: "", catalog_mirror: "" };
    expect(await setExtensionMarketSettings("m/one", empty, context())).toEqual(empty);
    expect(calls).toEqual([
      { url: "/api/v1/extensions/-/market-settings?manager=m%2Fone", method: "GET", body: undefined },
      { url: "/api/v1/extensions/-/market-settings?manager=m%2Fone", method: "PUT", body: JSON.stringify(empty) },
    ]);
  });

  it.each(["GET", "PUT"])("reports invalid %s responses as format errors, not network errors", async (method) => {
    vi.stubGlobal("localStorage", { getItem: () => "synthetic" });
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ registry_mirror: "" })));
    const request = method === "GET" ? getExtensionMarketSettings("assistant", context()) : setExtensionMarketSettings("assistant", settings, context());
    const error = await request.catch((e: unknown) => e);
    expect(error).toBeInstanceOf(Error);
    expect(error).not.toBeInstanceOf(TypeError);
    expect(errorText(error)).toBe(t("settings-hubsettings.mirror_response_invalid"));
  });

  it.each([404, 500])("propagates HTTP %s for reads and writes", async (status) => {
    vi.stubGlobal("localStorage", { getItem: () => "synthetic" });
    vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ error: "Synthetic failure" }), { status }));
    await expect(getExtensionMarketSettings("assistant", context())).rejects.toMatchObject({ status });
    await expect(setExtensionMarketSettings("assistant", settings, context())).rejects.toMatchObject({ status });
  });

  it("accepts HTTPS registry prefixes and complete catalog URLs, or empty values", () => {
    for (const value of ["", settings.registry_mirror, settings.catalog_mirror, "https://registry.example.com/"]) expect(validMirrorURL(value)).toBe(true);
    for (const value of ["http://example.com", "example.com", "https://user:pass@example.com", "https://example.com?token=x", "https://example.com#fragment", "file:///tmp/catalog", "https:/example.com"]) expect(validMirrorURL(value)).toBe(false);
  });
});
