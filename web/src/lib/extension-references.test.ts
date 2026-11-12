// The requests behind secret references (docs/extensions/secret-providers.md): a reference is PUT in place of a value,
// and an Always rule of a hand-off is removed by its id.
import { afterEach, describe, expect, it, vi } from "vitest";
// a hub with the new routes (lib/extension-api.ts)
vi.mock("./store", async (orig) => {
  const m = await orig<typeof import("./store")>();
  return { ...m, getState: () => ({ ...m.getState(), features: ["extensions"] }) };
});
import { removeHandoffRule, secretState, setExtensionSecretReference } from "./extensions";

afterEach(() => vi.unstubAllGlobals());

describe("secret references", () => {
  it("are saved and removed where the hub takes them", async () => {
    const calls: string[] = [];
    vi.stubGlobal("localStorage", { getItem: () => "t", setItem: () => {}, removeItem: () => {} });
    vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
      calls.push(`${init.method ?? "GET"} ${url} ${(init.body as string | undefined) ?? ""}`.trim());
      return new Response(JSON.stringify({ id: "a b", name: "x", kind: "k", status: "running" }));
    });
    await setExtensionSecretReference("a b", "api_key", "op://Dev/x/y");
    await removeHandoffRule("a b", "0123456789abcdef");
    expect(calls).toEqual([
      'PUT /api/v1/extensions/a%20b/-/secrets/api_key {"reference":"op://Dev/x/y"}',
      "DELETE /api/v1/extensions/a%20b/-/handoff-rules/0123456789abcdef",
    ]);
  });

  it("say whose a secret is and what it names", () => {
    expect(secretState({ name: "k", title: "K", set: true, reference: { provider: "onepassword", provider_name: "1Password", reference: "op://a/b/c", title: "GitHub token" } }))
      .toBe("来自 1Password");
  });
});
