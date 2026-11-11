// The site is static files, except /relay/*, the downloads /dl/*.gz and the extension catalog /extensions/catalog.json
// with its icons /extensions/icons/<id> (extensions.mjs).
//
// /dl/*.gz (the public personad, docs/operations/release.md "Publishing personad") come from the R2 bucket bound as DL: a Workers static
// file is at most 25 MiB and personad outgrew it (#2541). publish.sh uploads a release's files to one of two slots,
// <slot>/<name>, and deploys the site with dl/SLOT naming it; the other slot keeps the release before, so a download
// that started before a deploy still finishes. The small files (VERSION, SHA256SUMS, latest.json, SLOT) stay static
// files, deployed with the site: the manifest and the slot it points at change in one deploy.
//
// /dl/<version>/<name>.gz and /dl/<version>/manifest.json come from the bucket's release/<version>/, where publish.sh
// keeps every release (managers fetch the node build of their hub's version there).
//
// /relay/*: the push relay (docs/operations/push-relay.md), which runs elsewhere (`personad relay`)
// and is reached at RELAY_ORIGIN, e.g. a Cloudflare Tunnel hostname. Both values are Worker secrets, not in the repo:
//   npx wrangler secret put RELAY_ORIGIN         # https://<tunnel host>
//   npx wrangler secret put RELAY_PROXY_SECRET   # the relay's proxy_secret
// Without them /relay/* answers 503 and the rest of the site is unchanged.
//
// An entry in front of the Worker (the Hong Kong entry for mainland China, docs/operations/site.md) reaches it at its origin
// hostname, so cf-connecting-ip is the entry's address. The entry sends the client's address in X-Forwarded-For and
// proves itself with x-persona-edge-secret, the Worker secret EDGE_SECRET (`npx wrangler secret put EDGE_SECRET`);
// only then is X-Forwarded-For believed.
import { serveCatalog, serveIcon } from "./extensions.mjs";

function clientAddress(request, env) {
  const own = request.headers.get("cf-connecting-ip") || "";
  const proof = request.headers.get("x-persona-edge-secret");
  if (!env.EDGE_SECRET || !proof || !sameSecret(proof, env.EDGE_SECRET)) return own;
  return (request.headers.get("x-forwarded-for") || "").split(",")[0].trim() || own;
}

function sameSecret(a, b) {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return d === 0;
}

const download = /^\/dl\/([A-Za-z0-9][A-Za-z0-9._-]*\.gz)$/;
// /dl/<version>/<file>: a release kept in the bucket at release/<version>/ (its node builds and manifest.json), where a
// hub's manager fetches the node build of the hub's own version (docs/operations/node-update.md). Never a slot's.
const versioned = /^\/dl\/([A-Za-z0-9][A-Za-z0-9._-]*)\/([A-Za-z0-9][A-Za-z0-9._-]*\.gz|manifest\.json)$/;

async function servedSlot(request, env) {
  const r = await env.ASSETS.fetch(new Request(new URL("/dl/SLOT", request.url)));
  if (!r.ok) return "";
  const slot = (await r.text()).trim();
  return slot === "a" || slot === "b" ? slot : "";
}

// The bytes R2 returned for a range request, as the start and end (inclusive) of the whole object.
function rangeOf(object) {
  const r = object.range;
  if (!r) return null;
  if ("suffix" in r) {
    const length = Math.min(r.suffix, object.size);
    return { start: object.size - length, end: object.size - 1 };
  }
  const start = r.offset ?? 0;
  const length = r.length ?? object.size - start;
  return { start, end: start + length - 1 };
}

async function serveDownload(request, env, name, version) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return new Response("method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
  }
  if (!env.DL) return new Response("downloads not configured", { status: 503 });
  let key = `release/${version}/${name}`;
  if (version === undefined) {
    const slot = await servedSlot(request, env);
    if (!slot) return new Response("not found", { status: 404 });
    key = `${slot}/${name}`;
  }
  const ranged = request.method === "GET" && request.headers.has("range");
  const object = request.method === "HEAD"
    ? await env.DL.head(key)
    : await env.DL.get(key, { onlyIf: request.headers, ...(ranged ? { range: request.headers } : {}) });
  if (!object) return new Response("not found", { status: 404 });
  const headers = new Headers();
  object.writeHttpMetadata(headers);
  if (!headers.has("content-type")) headers.set("content-type", name.endsWith(".json") ? "application/json" : "application/gzip");
  headers.set("etag", object.httpEtag);
  headers.set("accept-ranges", "bytes");
  headers.set("cache-control", "public, max-age=0, must-revalidate");
  if (request.method === "HEAD") {
    headers.set("content-length", String(object.size));
    return new Response(null, { status: 200, headers });
  }
  // onlyIf did not hold: R2 gives the metadata without a body.
  if (!("body" in object) || !object.body) {
    return new Response(null, { status: request.headers.has("if-none-match") ? 304 : 412, headers });
  }
  const range = ranged ? rangeOf(object) : null;
  if (range) {
    headers.set("content-range", `bytes ${range.start}-${range.end}/${object.size}`);
    headers.set("content-length", String(range.end - range.start + 1));
    return new Response(object.body, { status: 206, headers });
  }
  headers.set("content-length", String(object.size));
  return new Response(object.body, { status: 200, headers });
}

const extensionIcon = /^\/extensions\/icons\/([A-Za-z0-9][A-Za-z0-9._-]*)$/;

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url);
    if (url.pathname === "/extensions/catalog.json") return serveCatalog(request, env, ctx);
    const icon = extensionIcon.exec(url.pathname);
    if (icon) return serveIcon(request, env, ctx, icon[1]);
    const dl = download.exec(url.pathname);
    if (dl) return serveDownload(request, env, dl[1]);
    const kept = versioned.exec(url.pathname);
    if (kept) return serveDownload(request, env, kept[2], kept[1]);
    if (!url.pathname.startsWith("/relay/")) return env.ASSETS.fetch(request);
    if (!env.RELAY_ORIGIN || !env.RELAY_PROXY_SECRET) {
      return Response.json({ error: "push relay not configured" }, { status: 503 });
    }
    const target = new URL(url.pathname + url.search, env.RELAY_ORIGIN);
    const headers = new Headers(request.headers);
    headers.delete("cookie");
    headers.delete("x-relay-proxy-secret"); // only the Worker's own counts
    headers.delete("x-persona-edge-secret");
    headers.set("x-relay-proxy-secret", env.RELAY_PROXY_SECRET);
    // The relay rate-limits registration by client address.
    headers.set("x-forwarded-for", clientAddress(request, env));
    return fetch(target, { method: request.method, headers, body: request.body, redirect: "manual" });
  },
};
