import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { staleDependencies, staleMessage } from "./installed-deps.mjs";

let root;
/** A package with `deps` in package.json, `lock` pinned in package-lock.json and `installed` in node_modules. */
const layout = ({ deps = {}, devDeps = {}, lock = {}, installed = {} }) => {
  root = mkdtempSync(join(tmpdir(), "installed-deps-"));
  writeFileSync(join(root, "package.json"), JSON.stringify({ dependencies: deps, devDependencies: devDeps }));
  writeFileSync(join(root, "package-lock.json"), JSON.stringify({ lockfileVersion: 3, packages: { "": {}, ...lock } }));
  for (const [name, version] of Object.entries(installed)) {
    mkdirSync(join(root, "node_modules", name), { recursive: true });
    writeFileSync(join(root, "node_modules", name, "package.json"), JSON.stringify({ name, version }));
  }
  return root;
};
afterEach(() => rmSync(root, { recursive: true, force: true }));

describe("installed dependencies against package-lock.json (#4776)", () => {
  it("passes when every direct dependency is the locked version", () => {
    const dir = layout({ deps: { react: "^19.0.0" }, devDeps: { "@scope/tool": "1.2.3", vitest: "4.0.10" },
      lock: { "node_modules/react": { version: "19.1.0" }, "node_modules/@scope/tool": { version: "1.2.3" }, "node_modules/vitest": { version: "4.0.10" } },
      installed: { react: "19.1.0", "@scope/tool": "1.2.3", vitest: "4.0.10" } });
    expect(staleDependencies(dir)).toEqual([]);
  });

  it("names a dependency installed at another version, one not installed and one the lock lacks", () => {
    const dir = layout({ deps: { react: "^19.0.0" }, devDeps: { jsdom: "27.4.0", vitest: "4.0.10", added: "1.0.0" },
      lock: { "node_modules/react": { version: "19.1.0" }, "node_modules/jsdom": { version: "27.4.0" }, "node_modules/vitest": { version: "4.0.10" } },
      installed: { react: "19.1.0", vitest: "4.0.6", added: "1.0.0" } });
    const stale = staleDependencies(dir);
    expect(stale).toEqual([
      { name: "added", locked: null, installed: "1.0.0" },
      { name: "jsdom", locked: "27.4.0", installed: null },
      { name: "vitest", locked: "4.0.10", installed: "4.0.6" },
    ]);
    const message = staleMessage(stale);
    expect(message).toContain("vitest: installed 4.0.6, package-lock.json 4.0.10");
    expect(message).toContain("jsdom: installed nothing, package-lock.json 27.4.0");
    expect(message).toContain("added: installed 1.0.0, package-lock.json has no entry");
    expect(message).toContain("cd web && npm ci");
  });

  it("leaves linked packages alone", () => {
    const dir = layout({ deps: { local: "file:../local" }, lock: { "node_modules/local": { resolved: "../local", link: true } } });
    expect(staleDependencies(dir)).toEqual([]);
  });
});
