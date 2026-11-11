// Whether web/node_modules holds what package-lock.json pins (#4776). A checkout that merged a lock bump without
// `npm ci` keeps running the old packages, and what its tests then report is about those packages, not the code: #4753
// was Vitest 4.0.6's jsdom environment in a checkout whose lock had pinned 4.0.10 for a day. Only the direct
// dependencies are compared; `npm ci` brings back everything below them too.
//
//   node scripts/installed-deps.mjs   (npm run check:deps)   exits 1 and lists what differs
//   Vitest's globalSetup (vite.config.ts)                     stops the run before any test
import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const read = (path) => JSON.parse(readFileSync(path, "utf8"));

/**
 * The direct dependencies (package.json `dependencies` and `devDependencies`) of the package in `dir` whose installed
 * version isn't the one its package-lock.json pins: `{name, locked, installed}`, `installed` null when it isn't
 * installed and `locked` null when the lock has no entry for it. Linked packages (`file:`) are not compared.
 */
export function staleDependencies(dir) {
  const manifest = read(join(dir, "package.json"));
  const lock = read(join(dir, "package-lock.json")).packages ?? {};
  const names = [...new Set([...Object.keys(manifest.dependencies ?? {}), ...Object.keys(manifest.devDependencies ?? {})])].sort();
  const stale = [];
  for (const name of names) {
    const entry = lock[`node_modules/${name}`];
    if (entry?.link) continue;
    const locked = entry?.version ?? null;
    const file = join(dir, "node_modules", name, "package.json");
    const installed = existsSync(file) ? (read(file).version ?? null) : null;
    if (locked === null || installed !== locked) stale.push({ name, locked, installed });
  }
  return stale;
}

/** What to tell the person running the tests. */
export function staleMessage(stale) {
  return [
    "web/node_modules doesn't match package-lock.json:",
    ...stale.map(({ name, locked, installed }) => `  ${name}: installed ${installed ?? "nothing"}, package-lock.json ${locked ?? "has no entry"}`),
    "Run `cd web && npm ci` first: test results from other package versions say nothing about the code.",
  ].join("\n");
}

const web = fileURLToPath(new URL("..", import.meta.url));

/** Vitest's globalSetup: an error here stops the run before any test file loads. */
export default function setup() {
  const stale = staleDependencies(web);
  if (stale.length) throw new Error(staleMessage(stale));
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const stale = staleDependencies(web);
  if (stale.length) {
    console.error(staleMessage(stale));
    process.exit(1);
  }
  console.log("web/node_modules matches package-lock.json");
}
