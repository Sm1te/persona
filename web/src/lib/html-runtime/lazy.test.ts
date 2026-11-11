// The html card's runtime stays off the first screen (#4215): QuickJS only in the worker, the worker only from host.ts,
// and the first screen's modules reach the card only through its lazy import (ui/AssistantCard.tsx). The build's
// first-screen budget (src/startup-preload.ts) measures the bytes; this says where they may come from.
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { describe, expect, it } from "vitest";

const SRC = join(__dirname, "../..");
const files = (dir: string): string[] =>
  readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? files(p) : /\.(ts|tsx|js)$/.test(f) && !/\.test\.(ts|tsx)$/.test(f) ? [p] : [];
  });
const ALL = files(SRC).map((p) => ({ path: relative(SRC, p), code: readFileSync(p, "utf8") }));
/** A file's static imports (not `import(…)`), as written. */
const staticImports = (code: string) => [...code.matchAll(/^\s*import\s+(?:type\s+)?[^;]*?from\s+"([^"]+)"/gm)].filter((m) => !/^\s*import\s+type\s/.test(m[0])).map((m) => m[1]);

describe("the html card runtime on the first screen", () => {
  it("imports QuickJS only in its worker", () => {
    // QuickJS's code; the page only takes the WebAssembly's URL, to compile it for the worker
    const users = ALL.filter((f) => staticImports(f.code).some((i) => i.includes("quickjs") && !i.endsWith("/wasm?url")) || /import\(\s*["'][^"']*quickjs/.test(f.code)).map((f) => f.path);
    expect(users).toEqual(["lib/html-runtime/html-runtime.worker.ts"]);
    // the WebAssembly's URL, only where the page compiles it
    expect(ALL.filter((f) => f.code.includes("quickjs-wasmfile-release-sync/wasm")).map((f) => f.path)).toEqual(["lib/html-runtime/host.ts"]);
  });

  it("starts the worker only from host.ts, and only HtmlCard (lazy) and the dev pages load host.ts", () => {
    expect(ALL.filter((f) => /new Worker\(new URL\("\.\/html-runtime\.worker/.test(f.code)).map((f) => f.path)).toEqual(["lib/html-runtime/host.ts"]);
    const host = ALL.filter((f) => staticImports(f.code).some((i) => /html-runtime\/host$|^\.\/host$/.test(i))).map((f) => f.path).sort();
    expect(host).toEqual(["ui/HtmlCard.tsx"]);
  });

  it("reaches the card from the first screen only through a lazy import", () => {
    const card = ALL.find((f) => f.path === "ui/AssistantCard.tsx")!;
    expect(staticImports(card.code).filter((i) => /HtmlCard|html-runtime|html-bridge|html-actions/.test(i))).toEqual([]);
    expect(card.code).toMatch(/lazy\(\(\) => import\("\.\/HtmlCard"\)\)/);
    // nothing but the card itself imports the card, its bridge or its hub calls (type-only imports aside)
    const statics = ALL.filter((f) => f.path !== "ui/HtmlCard.tsx" && staticImports(f.code).some((i) => /\/HtmlCard$|html-bridge$|html-actions$/.test(i))).map((f) => f.path);
    expect(statics).toEqual([]);
  });
});
