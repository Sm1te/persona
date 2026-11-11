// /extensions/catalog.json and /extensions/icons/<id>: the data behind the /extensions page (docs/operations/site.md, "Extensions page").
//
// The official market publishes persona-extensions.json, a signed index {payload, signatures} whose payload is a JSON
// string of {name, extensions: [{manifest, tags, packages, …}], …} (docs/extensions/extensions.md). The page needs a
// small part of it, so the Worker fetches the index, keeps only what the page shows, and serves that.
//
// The slim copy sits in the edge cache with the time it was fetched. Within FRESH_MS it is served as is; after that
// the next request fetches again, and when GitHub cannot be reached the old copy is served (stale: true) for up to
// KEEP_SECONDS. Only with no copy at all does the page get a 503 and show its error state.
//
// Optional fields, read when the catalog carries them: icon_url (absolute https) and downloads (an integer) on each
// extension or its manifest, category / categories, and downloads_updated_at at the top of the payload. Without them
// the page shows a monogram and sorts by name.
//
// Icons go through /extensions/icons/<id>: raw.githubusercontent.com and other hosts are often unreachable from
// mainland China, while this site is not (the Hong Kong entry). Only the icon_url the catalog names for that id is
// fetched, only image types come back, and the response cannot run anything on this origin.

// The page shows the public official market and nothing else: the catalog must say it was generated from this
// repository, and every source link points into it.
export const MARKET_REPO = "nwverse/persona-extensions";
export const CATALOG_URL = `https://raw.githubusercontent.com/${MARKET_REPO}/main/persona-extensions.json`;
const SOURCE_ROOT = `https://github.com/${MARKET_REPO}/`;
// Bump when the slim shape changes, so no edge keeps serving the old shape.
const CACHE_KEY = "https://extensions-catalog.invalid/v2";
export const FRESH_MS = 10 * 60 * 1000;
const KEEP_SECONDS = 30 * 24 * 3600;
const FETCH_TIMEOUT_MS = 8000;
const ICON_MAX_BYTES = 512 * 1024;
const ICON_TYPES = new Set(["image/png", "image/jpeg", "image/webp", "image/gif", "image/svg+xml", "image/avif", "image/x-icon", "image/vnd.microsoft.icon"]);

const str = (v, max = 2000) => (typeof v === "string" ? v.trim().slice(0, max) : "");
const httpsURL = (v) => {
  if (typeof v !== "string") return "";
  try {
    const u = new URL(v);
    return u.protocol === "https:" ? u.href : "";
  } catch {
    return "";
  }
};
const count = (v) => (Number.isSafeInteger(v) && v >= 0 ? v : null);
const first = (...vs) => vs.find((v) => v !== undefined && v !== null);

// slim turns the signed index into what the page shows, most downloaded first (by name when nobody has a count).
export function slim(index) {
  let payload = index?.payload;
  if (typeof payload === "string") payload = JSON.parse(payload);
  if (!payload || !Array.isArray(payload.extensions)) throw new Error("catalog without extensions");
  if (payload.generation?.repository !== MARKET_REPO) throw new Error("catalog not generated from " + MARKET_REPO);
  const extensions = [];
  const seen = new Set();
  for (const entry of payload.extensions) {
    const m = entry?.manifest;
    const id = str(m?.id, 80);
    if (!id || !/^[a-z0-9][a-z0-9._-]*$/i.test(id) || seen.has(id)) continue;
    seen.add(id);
    const categories = [first(entry.categories, m.categories)].flat()
      .concat(first(entry.category, m.category) ?? [])
      .map((c) => str(c, 40)).filter(Boolean);
    const iconURL = httpsURL(first(entry.icon_url, m.icon_url));
    extensions.push({
      id,
      name: str(m.name, 120) || id,
      description: str(m.description),
      version: str(m.version, 40),
      publisher: str(m.publisher, 80),
      // Many manifests declare no homepage; every official extension's source is extensions/<id> in the repository.
      source: httpsURL(m.homepage).startsWith(SOURCE_ROOT) ? httpsURL(m.homepage) : `${SOURCE_ROOT}tree/main/extensions/${encodeURIComponent(id)}`,
      tags: (Array.isArray(entry.tags) ? entry.tags : []).map((t) => str(t, 40)).filter(Boolean).slice(0, 12),
      categories: [...new Set(categories)],
      pack: Array.isArray(m.extension_pack) && m.extension_pack.length > 0,
      icon_url: iconURL,
      icon: iconURL ? `/extensions/icons/${encodeURIComponent(id)}?v=${encodeURIComponent(str(m.version, 40))}` : "",
      downloads: count(first(entry.downloads, m.downloads)),
    });
  }
  const byName = (a, b) => a.name.localeCompare(b.name, "zh-Hans-CN");
  extensions.sort((a, b) => (b.downloads ?? -1) - (a.downloads ?? -1) || byName(a, b));
  return {
    name: str(payload.name, 120),
    description: str(payload.description, 400),
    downloads_updated_at: str(payload.downloads_updated_at, 40),
    source_commit: str(payload.generation?.source_commit, 64),
    extensions,
  };
}

function edgeCache() {
  return typeof caches !== "undefined" && caches.default ? caches.default : null;
}

async function cached(cache) {
  if (!cache) return null;
  const r = await cache.match(CACHE_KEY);
  if (!r) return null;
  try {
    return await r.json();
  } catch {
    return null;
  }
}

async function fetchFresh(env) {
  const r = await fetch(env.EXTENSIONS_CATALOG_URL || CATALOG_URL, {
    headers: { accept: "application/json", "user-agent": "persona-site" },
    signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
  });
  if (!r.ok) throw new Error(`catalog: HTTP ${r.status}`);
  return { ...slim(await r.json()), fetched_at: new Date().toISOString() };
}

// catalogData: the slim catalog, fresh or (when GitHub cannot be reached) stale; null when there is none at all.
export async function catalogData(env, ctx) {
  const cache = edgeCache();
  const old = await cached(cache);
  if (old && Date.now() - Date.parse(old.fetched_at) < FRESH_MS) return old;
  try {
    const data = await fetchFresh(env);
    if (cache) {
      const put = cache.put(CACHE_KEY, new Response(JSON.stringify(data), {
        headers: { "content-type": "application/json", "cache-control": `public, max-age=${KEEP_SECONDS}` },
      }));
      ctx?.waitUntil ? ctx.waitUntil(put) : await put;
    }
    return data;
  } catch (err) {
    if (old) return { ...old, stale: true };
    console.error("extensions catalog:", err?.message || err);
    return null;
  }
}

function onlyRead(request) {
  if (request.method === "GET" || request.method === "HEAD") return null;
  return new Response("method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
}

export async function serveCatalog(request, env, ctx) {
  const refused = onlyRead(request);
  if (refused) return refused;
  const data = await catalogData(env, ctx);
  if (!data) {
    return Response.json({ error: "extension catalog unavailable" }, { status: 503, headers: { "cache-control": "no-store" } });
  }
  const r = Response.json(data, { headers: { "cache-control": data.stale ? "public, max-age=60" : "public, max-age=300" } });
  return request.method === "HEAD" ? new Response(null, { headers: r.headers }) : r;
}

export async function serveIcon(request, env, ctx, id) {
  const refused = onlyRead(request);
  if (refused) return refused;
  const data = await catalogData(env, ctx);
  const ext = data?.extensions.find((e) => e.id === id);
  if (!ext?.icon_url) return new Response("not found", { status: 404 });
  let upstream;
  try {
    upstream = await fetch(ext.icon_url, {
      headers: { accept: "image/*", "user-agent": "persona-site" },
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
      cf: { cacheEverything: true, cacheTtl: 86400 },
    });
  } catch {
    return new Response("icon unavailable", { status: 502 });
  }
  const type = (upstream.headers.get("content-type") || "").split(";")[0].trim().toLowerCase();
  if (!upstream.ok || !ICON_TYPES.has(type)) return new Response("icon unavailable", { status: 502 });
  const body = await upstream.arrayBuffer();
  if (body.byteLength > ICON_MAX_BYTES) return new Response("icon too large", { status: 502 });
  const headers = {
    "content-type": type,
    "cache-control": "public, max-age=86400",
    "x-content-type-options": "nosniff",
    // An SVG opened on its own must not run scripts or load anything on this origin.
    "content-security-policy": "default-src 'none'; style-src 'unsafe-inline'; sandbox",
  };
  return new Response(request.method === "HEAD" ? null : body, { headers });
}
