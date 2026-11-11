/// <reference types="vitest/config" />
import { readFileSync } from "node:fs";
import { basename } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { brotliCopies, FIRST_SCREEN_BUDGET, gzipBytes, IDLE_TIER_BUDGET, idleImports, localeDirs, preloadScript, sentBytes, startupNamespaceGaps } from "./src/startup-preload";
import { textScale } from "./src/lib/text-scale";
import { classicWorkerProblem } from "./src/sw-entry-check";
import { catalogFiles, i18nCatalogs, startupNamespacesFile } from "./i18n-catalogs";
import { pageTokensPlugin } from "./page-tokens";
import { thirdPartyLicenses } from "./third-party-licenses";
import { compatBootstrap } from "./compat-bootstrap";

// `npm run dev` talks to a hub through Vite's proxy: PERSONA_HUB_URL (eval "$(python3 ../scripts/persona_config.py env)").
const environment = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {};
const hub = environment.PERSONA_HUB_URL || "http://127.0.0.1:18660";

// The hub admin UI (docs/operations/admin.md) is its own page, admin/index.html. Its code goes under admin/assets/, which the hub
// serves only to sources inside admin.access; the client's bundle never references it.
// The admin UI's strings (src/i18n/locales/<lang>/admin.ts and its admin-*.json) are admin code too.
const adminStrings = /\/src\/i18n\/locales\/[^/]+\/(?:admin-[\w-]+\.json|_meta\.json\?catalog=admin)$/;
const adminOnly = (ids: string[]) =>
  ids.length > 0 && ids.every((id) => id.includes("/src/admin/") || id.endsWith("/admin/index.html") || (adminStrings.test(id) || id.startsWith("\0i18n-catalog:admin:")));

// Dev only: /admin and its client-side routes get admin/index.html (Vite's own fallback is the root index.html).
const adminFallback: Plugin = {
  name: "admin-fallback",
  apply: "serve",
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      const r = req as unknown as { url?: string };
      const [path, query] = (r.url ?? "").split(/\?(.*)/s);
      if ((path === "/admin" || path.startsWith("/admin/")) && !path.split("/").pop()!.includes(".")) {
        r.url = "/admin/index.html" + (query ? `?${query}` : "");
      }
      next();
    });
  },
};

// Dev only: the component showcase's pictures (showcase.html, #829). The hub serves an extension's own picture (a ref) at
// /api/v1/extensions/<id>/-/image/<token>; the showcase's fake hub writes "dev-showcase.<ref>" as the token (a real one
// never starts so), and this serves the file of that name from src/showcase/images (anything else goes on to the hub).
const showcaseImages: Plugin = {
  name: "showcase-images",
  apply: "serve",
  configureServer(server) {
    const dir = fileURLToPath(new URL("./src/showcase/images/", import.meta.url));
    server.middlewares.use(async (req, res, next) => {
      const m = /^\/api\/v1\/extensions\/showcase\/-\/image\/dev-showcase\.([A-Za-z0-9_.-]+)(?:\?|$)/.exec(req.url ?? "");
      if (!m || m[1].includes("..")) return next();
      const { readFile } = await import("node:fs/promises");
      try {
        const body = await readFile(dir + m[1] + ".png");
        res.setHeader("Content-Type", "image/png");
        res.setHeader("X-Content-Type-Options", "nosniff");
        res.end(body);
      } catch {
        res.statusCode = 404;
        res.end();
      }
    });
  },
};

// Build only: main.tsx loads the language catalog, then React, then App, each a dynamic import that starts only when
// the one before is in, so a cold first load waits for them one after another. This lets the client page's <head>
// start fetching all of them at once (modulepreload fetches and parses, it does not run them: App still runs after
// the catalog is set). The catalog is the preferred language's, picked like i18n/index.ts preferredLanguage (the
// script is src/startup-preload.ts, tested against it); the live screen (/screen/<machine>, screen/screen.ts) never
// loads App, so it gets only the catalog.
//
// The catalog is the first screen's part of it (client-startup, web/i18n-catalogs.ts): the rest loads once the page
// has started (i18n/index.ts startI18n), and holdLazyImports makes every other module wait for it. So the first
// screen's chunks, all that can run before it, must name only startup namespaces: the build fails, naming the
// namespaces to add to src/i18n/startup-namespaces.json, if they name another.
//
// The same files, with main and everything it imports statically and the rest of the catalog, are what a cold first
// screen fetches: once they are written, each of 1 KB or more gets a brotli copy beside it (name.br,
// src/startup-preload.ts brotliCopies), which the hub sends as stored to a browser that takes br (docs/web/web.md
// "Precompressed assets"). The build then fails if any language's first screen sends more than FIRST_SCREEN_BUDGET.
//
// After start the page also fetches, once the browser is idle, the chunks of each lazyPage(…, { idle: true }) and
// what they import beyond those files (src/lib/lazy-page.ts): the build fails if they send more than IDLE_TIER_BUDGET.
function startupPreload(): Plugin {
  let startup: string[] | undefined; // the first screen's files, set by transformIndexHtml for the client page
  let screens: [string, string[]][] = []; // each language's first screen of the App (not the live screen)
  const idle = new Set<string>(); // the modules lazyPage loads at idle, found by transform
  let idleFiles: string[] = []; // their files beyond the first screen's, set by transformIndexHtml
  return {
    name: "startup-preload",
    apply: "build",
    async transform(code, id) {
      if (!id.includes("/src/") || !code.includes("lazyPage(")) return;
      let specs: string[];
      try {
        specs = idleImports(code);
      } catch (e) {
        this.error(`startup-preload: ${(e as Error).message}`); // Rollup names the file
      }
      for (const spec of specs) {
        const target = await this.resolve(spec, id);
        if (!target) this.error(`startup-preload: ${id} loads ${spec} at idle, which does not resolve`);
        idle.add(target.id);
      }
    },
    async writeBundle({ dir }) {
      if (!startup) this.error("startup-preload: index.html was not transformed, so the first screen's files are unknown");
      await brotliCopies(dir!, startup);
      for (const [lang, files] of screens) {
        const bytes = await sentBytes(dir!, files);
        if (bytes > FIRST_SCREEN_BUDGET) {
          this.error(`startup-preload: the ${lang} first screen sends ${bytes} B, over its budget of ${FIRST_SCREEN_BUDGET} B (src/startup-preload.ts FIRST_SCREEN_BUDGET)`);
        }
      }
      const idleBytes = await gzipBytes(dir!, idleFiles);
      if (idleBytes > IDLE_TIER_BUDGET) {
        this.error(`startup-preload: the pages loaded at idle send ${idleBytes} B, over their budget of ${IDLE_TIER_BUDGET} B (src/startup-preload.ts IDLE_TIER_BUDGET): load one on intent (lazyPage routes) instead`);
      }
    },
    transformIndexHtml: {
      order: "post",
      handler(html, ctx) {
        if (!ctx.bundle || ctx.chunk?.name !== "main") return html; // not admin/index.html
        const chunks = Object.values(ctx.bundle).filter((c) => c.type === "chunk");
        // react-dom/client shares a chunk with other modules (no facade): look a module up by the chunk holding it.
        const byModule = (path: string) => chunks.find((c) => c.moduleIds.some((id) => id.endsWith(path)));
        const find = (path: string) => {
          const chunk = byModule(path);
          if (!chunk) throw new Error(`startup-preload: no chunk holds ${path}`);
          return chunk;
        };
        // Each group: the chunks of its roots and everything they import statically, the JS and the CSS, minus the
        // files the page already loads (main and its static imports have their tags; a page group skips the shared one).
        const group = (roots: (typeof chunks)[number][], loaded: Set<string>) => {
          const files: string[] = [];
          const visit = (chunk: (typeof chunks)[number]) => {
            if (loaded.has(chunk.fileName)) return;
            loaded.add(chunk.fileName);
            files.push(chunk.fileName, ...(chunk.viteMetadata?.importedCss ?? []));
            for (const name of chunk.imports) visit(chunks.find((c) => c.fileName === name)!);
          };
          roots.forEach(visit);
          return files;
        };
        const langs = localeDirs(fileURLToPath(new URL("./src/i18n/locales", import.meta.url)));
        const meta = (lang: string) => fileURLToPath(new URL(`./src/i18n/locales/${lang}/_meta.json`, import.meta.url));
        const catalogs = (kind: string) => Object.fromEntries(Object.keys(langs).map((lang) => [lang, find(`i18n-catalog:${kind}:${meta(lang)}.js`).fileName]));
        const main = new Set([ctx.chunk.fileName, ...ctx.chunk.imports]);
        // main, what it imports statically and their CSS: the page's own tags (Vite preloads the whole static closure)
        const entry = group([ctx.chunk], new Set());
        const shared = group(["/react-dom/client.js", "/src/lib/pwa.ts"].map(find), main);
        const page = (roots: string[]) => group(roots.map(find), new Set([...main, ...shared]));
        const files = {
          shared,
          app: page(["/src/ui/ExtensionThemePicker.tsx", "/src/App.tsx"]),
          screen: page(["/src/screen/ScreenView.tsx"]),
          catalogs: catalogs("client-startup"),
          matches: Object.fromEntries(Object.entries(langs).flatMap(([lang, ms]) => ms.map((m) => [m.toLowerCase(), lang]))),
        };
        const script = preloadScript(files);
        // What runs before the rest of the catalog is in: the groups, and main.tsx's other imports (not preloaded).
        const first = new Set([...entry, ...shared, ...files.app, ...files.screen, ...group(["/src/lib/back-close.ts", "/src/lib/error-reports-start.ts"].map(find), new Set())]);
        const gaps = startupNamespaceGaps(
          chunks.filter((c) => first.has(c.fileName)).map((c) => c.code),
          catalogFiles(meta("en"), "client").map((file) => basename(file, ".json")),
          JSON.parse(readFileSync(startupNamespacesFile(meta("en")), "utf8")) as string[],
        );
        if (gaps.length) {
          throw new Error(`startup-preload: the first screen's code names keys of ${gaps.join(", ")}, which the page reads before the rest of the catalog is in: add them to src/i18n/startup-namespaces.json`);
        }
        startup = [...new Set([...entry, ...shared, ...files.app, ...files.screen, ...Object.values(files.catalogs), ...Object.values(catalogs("client-rest"))])];
        if (!idle.size) throw new Error("startup-preload: no lazyPage(…, { idle: true }) was found, so the idle tier's budget would check nothing");
        idleFiles = group([...idle].map(find), new Set([...first, ...startup]));
        screens = Object.entries(files.catalogs).map(([lang, catalog]) => [lang, [...new Set([...entry, ...shared, ...files.app, catalog])]]);
        return { html, tags: [{ tag: "script", children: script, injectTo: "head" }] };
      },
    },
  };
}

// Build only: sw.js must not import anything (src/sw-entry-check.ts); the build fails, saying why and how to fix it.
const classicWorker: Plugin = {
  name: "classic-worker",
  apply: "build",
  generateBundle(_, bundle) {
    const sw = bundle["sw.js"];
    const problem = sw?.type === "chunk" ? classicWorkerProblem(sw.code, [...sw.imports, ...sw.dynamicImports]) : undefined;
    if (problem) this.error(problem);
  },
};

// KaTeX's CSS (src/ui/stream/math.ts) lists each font as woff2, woff and ttf. Every browser the build targets (es2022)
// takes woff2, so the other two are only bytes in every personad: drop them from @font-face, and Rollup no longer
// emits those 40 files. The build fails if a KaTeX update writes its fonts some other way.
const katexFonts: Plugin = {
  name: "katex-fonts",
  enforce: "pre",
  transform(code, id) {
    if (!/\/katex\/dist\/katex(?:\.min)?\.css$/.test(id.split("?")[0])) return;
    const css = code.replace(/,\s*url\([^)]*\)\s*format\(["'](?:woff|truetype)["']\)/g, "");
    if (/\.(?:woff|ttf)\b/.test(css) || !css.includes(".woff2")) this.error(`katex-fonts: ${id} still lists woff or ttf fonts, or no woff2`);
    return { code: css, map: null };
  },
};

// Build only: the page waits for just the first screen's part of the client catalog (src/i18n/index.ts startI18n), so
// every lazy import of the client's code waits for the rest before it runs: `import(x)` becomes one that starts once
// i18nRest() resolves. The download itself does not wait: Vite preloads a lazy chunk (and what it imports) before
// calling the import. Left alone: main.tsx's own imports, which are the first screen (startupPreload checks they read
// only its part); the catalogs' loads; and the admin UI, which waits for the whole catalog.
const holdLazyImports: Plugin = {
  name: "hold-lazy-imports",
  apply: "build",
  renderDynamicImport({ moduleId, targetModuleId }) {
    if (moduleId.endsWith("/src/main.tsx") || moduleId.includes("/src/admin/") || targetModuleId?.startsWith("\0i18n-catalog:")) return null;
    return { left: "Promise.resolve(globalThis.__personaI18nRest?.()).then(()=>import(", right: "))" };
  },
};

export default defineConfig({
  plugins: [katexFonts, i18nCatalogs, pageTokensPlugin, react(), tailwindcss(), adminFallback, showcaseImages, startupPreload(), holdLazyImports, classicWorker, thirdPartyLicenses,
    compatBootstrap(fileURLToPath(new URL("./public/compatibility-bootstrap.js", import.meta.url)))],
  // Text size (src/lib/text-scale.ts): font sizes become calc(size * var(--text-scale, 1)), after Tailwind wrote them.
  css: { postcss: { plugins: [textScale()] } },
  server: {
    host: "127.0.0.1",
    proxy: { "/api": { target: hub, changeOrigin: true, ws: true } },
  },
  // The bundle's files are relative to the page's <base>, which the hub points at the sub-path a reverse proxy serves
  // it under (docs/web/web.md "Subpath"); at the root that is "/", as before.
  base: "./",
  // The terminal loads xterm only when opened: pre-bundle it, or the first open in dev re-optimizes and reloads the page.
  optimizeDeps: { include: ["@xterm/xterm", "@xterm/addon-fit", "@xterm/addon-clipboard", "@xterm/addon-web-links", "@xterm/addon-search", "@xterm/addon-webgl"] },
  // Tests run in zh-Hans, the source language, so their expectations read like the interface (src/i18n/test-setup.ts).
  // Node 25+ has its own localStorage, undefined without --localstorage-file, and it hides jsdom's: turn it off.
  // A node_modules that isn't what package-lock.json pins stops the run before any test (scripts/installed-deps.mjs, #4776).
  test: { globalSetup: ["scripts/installed-deps.mjs"], setupFiles: ["src/i18n/test-setup.ts"], execArgv: ["--no-experimental-webstorage"] },
  build: {
    target: "es2022",
    sourcemap: false,
    chunkSizeWarningLimit: 900,
    rollupOptions: {
      // The service worker (src/sw.ts) is its own entry at a fixed /sw.js: its scope is the folder it's served from.
      input: { main: "index.html", admin: "admin/index.html", sw: "src/sw.ts" },
      output: {
        entryFileNames: (chunk) => (chunk.name === "sw" ? "sw.js" : chunk.name === "admin" ? "admin/assets/[name]-[hash].js" : "assets/[name]-[hash].js"),
        chunkFileNames: (chunk) => (adminOnly(chunk.moduleIds) ? "admin/assets/[name]-[hash].js" : "assets/[name]-[hash].js"),
        assetFileNames: (asset) => ((asset.names ?? []).some((n) => n.startsWith("admin")) ? "admin/assets/[name]-[hash][extname]" : "assets/[name]-[hash][extname]"),
      },
    },
  },
});
