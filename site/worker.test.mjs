// node --test site/worker.test.mjs
import assert from "node:assert/strict";
import test from "node:test";
import worker from "./worker.mjs";

const assets = { fetch: async (req) => new Response("asset " + new URL(req.url).pathname) };

test("static paths are the site's files", async () => {
  const r = await worker.fetch(new Request("https://site.example/privacy/"), { ASSETS: assets });
  assert.equal(await r.text(), "asset /privacy/");
});

test("/relay/* without the secrets is 503", async () => {
  const r = await worker.fetch(new Request("https://site.example/relay/v1/health"), { ASSETS: assets });
  assert.equal(r.status, 503);
});

test("/relay/* goes to the relay with the proxy secret and the client address", async () => {
  let seen;
  globalThis.fetch = async (url, init) => {
    seen = { url: String(url), init };
    return new Response("{}", { status: 200 });
  };
  const req = new Request("https://site.example/relay/v1/push?x=1", {
    method: "POST", body: '{"type":"alert"}',
    headers: { authorization: "Bearer g", cookie: "a=b", "x-relay-proxy-secret": "forged", "cf-connecting-ip": "198.51.100.7" },
  });
  const r = await worker.fetch(req, { ASSETS: assets, RELAY_ORIGIN: "https://origin.example", RELAY_PROXY_SECRET: "s3cret" });
  assert.equal(r.status, 200);
  assert.equal(seen.url, "https://origin.example/relay/v1/push?x=1");
  const h = seen.init.headers;
  assert.equal(h.get("x-relay-proxy-secret"), "s3cret");
  assert.equal(h.get("x-forwarded-for"), "198.51.100.7");
  assert.equal(h.get("authorization"), "Bearer g");
  assert.equal(h.get("cookie"), null);
  assert.equal(seen.init.method, "POST");
});

function relayRequest(headers) {
  return new Request("https://site.example/relay/v1/register", { method: "POST", body: "{}", headers });
}

async function forwardedFor(headers, env) {
  let seen;
  globalThis.fetch = async (url, init) => { seen = init.headers; return new Response("{}"); };
  await worker.fetch(relayRequest(headers), { ASSETS: assets, RELAY_ORIGIN: "https://origin.example", RELAY_PROXY_SECRET: "s3cret", ...env });
  return seen;
}

test("an entry with the edge secret passes the client's address on", async () => {
  const h = await forwardedFor({ "cf-connecting-ip": "203.0.113.9", "x-forwarded-for": "198.51.100.7, 192.0.2.1", "x-persona-edge-secret": "edge" }, { EDGE_SECRET: "edge" });
  assert.equal(h.get("x-forwarded-for"), "198.51.100.7");
  assert.equal(h.get("x-persona-edge-secret"), null);
});

test("X-Forwarded-For is not believed without the right edge secret", async () => {
  for (const [headers, env] of [
    [{ "cf-connecting-ip": "203.0.113.9", "x-forwarded-for": "198.51.100.7" }, { EDGE_SECRET: "edge" }],
    [{ "cf-connecting-ip": "203.0.113.9", "x-forwarded-for": "198.51.100.7", "x-persona-edge-secret": "wrong" }, { EDGE_SECRET: "edge" }],
    [{ "cf-connecting-ip": "203.0.113.9", "x-forwarded-for": "198.51.100.7", "x-persona-edge-secret": "edge" }, {}],
  ]) {
    const h = await forwardedFor(headers, env);
    assert.equal(h.get("x-forwarded-for"), "203.0.113.9");
    assert.equal(h.get("x-persona-edge-secret"), null);
  }
});

// A fake R2 bucket: objects by key, with the parts of R2's API serveDownload uses.
function bucket(objects) {
  const meta = (key) => {
    const data = objects[key];
    return {
      key, size: data.length, httpEtag: `"${key}"`,
      writeHttpMetadata(h) { h.set("content-type", "application/gzip"); },
    };
  };
  return {
    calls: [],
    async head(key) { this.calls.push(["head", key]); return key in objects ? meta(key) : null; },
    async get(key, opts = {}) {
      this.calls.push(["get", key, opts]);
      if (!(key in objects)) return null;
      const m = meta(key);
      const inm = opts.onlyIf?.get?.("if-none-match");
      if (inm && inm === m.httpEtag) return m; // no body: the condition did not hold
      let data = objects[key];
      const header = opts.range?.get?.("range");
      if (header) {
        const [, a, b] = /bytes=(\d*)-(\d*)/.exec(header);
        if (a === "") { m.range = { suffix: Number(b) }; data = data.slice(-Number(b)); }
        else { const s = Number(a), e = b === "" ? data.length - 1 : Number(b); m.range = { offset: s, length: e - s + 1 }; data = data.slice(s, e + 1); }
      }
      return { ...m, body: new Blob([data]).stream() };
    },
  };
}

const dlAssets = (slot) => ({
  fetch: async (req) => {
    const p = new URL(req.url).pathname;
    if (p === "/dl/SLOT") return slot === null ? new Response("nope", { status: 404 }) : new Response(slot + "\n");
    return new Response("asset " + p);
  },
});

test("/dl/*.gz comes from the served slot of the bucket", async () => {
  const DL = bucket({ "b/personad-linux-amd64.gz": "new bytes", "a/personad-linux-amd64.gz": "old bytes" });
  const r = await worker.fetch(new Request("https://site.example/dl/personad-linux-amd64.gz"), { ASSETS: dlAssets("b"), DL });
  assert.equal(r.status, 200);
  assert.equal(await r.text(), "new bytes");
  assert.equal(r.headers.get("content-length"), "9");
  assert.equal(r.headers.get("content-type"), "application/gzip");
  assert.equal(r.headers.get("accept-ranges"), "bytes");
});

test("/dl/*.gz HEAD gives the size without reading the object", async () => {
  const DL = bucket({ "a/personad-linux-amd64.gz": "0123456789" });
  const r = await worker.fetch(new Request("https://site.example/dl/personad-linux-amd64.gz", { method: "HEAD" }), { ASSETS: dlAssets("a"), DL });
  assert.equal(r.status, 200);
  assert.equal(r.headers.get("content-length"), "10");
  assert.deepEqual(DL.calls.map((c) => c[0]), ["head"]);
});

test("/dl/*.gz serves a range so a cut download resumes", async () => {
  const DL = bucket({ "a/x.gz": "0123456789" });
  const env = { ASSETS: dlAssets("a"), DL };
  let r = await worker.fetch(new Request("https://site.example/dl/x.gz", { headers: { range: "bytes=4-" } }), env);
  assert.equal(r.status, 206);
  assert.equal(await r.text(), "456789");
  assert.equal(r.headers.get("content-range"), "bytes 4-9/10");
  assert.equal(r.headers.get("content-length"), "6");
  r = await worker.fetch(new Request("https://site.example/dl/x.gz", { headers: { range: "bytes=-3" } }), env);
  assert.equal(await r.text(), "789");
  assert.equal(r.headers.get("content-range"), "bytes 7-9/10");
});

test("/dl/*.gz answers 304 when the client has it", async () => {
  const DL = bucket({ "a/x.gz": "0123456789" });
  const r = await worker.fetch(new Request("https://site.example/dl/x.gz", { headers: { "if-none-match": '"a/x.gz"' } }), { ASSETS: dlAssets("a"), DL });
  assert.equal(r.status, 304);
});

test("/dl/*.gz is 404 for a missing file or no slot, 405 for other methods", async () => {
  const DL = bucket({ "a/x.gz": "x" });
  let r = await worker.fetch(new Request("https://site.example/dl/y.gz"), { ASSETS: dlAssets("a"), DL });
  assert.equal(r.status, 404);
  r = await worker.fetch(new Request("https://site.example/dl/x.gz"), { ASSETS: dlAssets(null), DL });
  assert.equal(r.status, 404);
  r = await worker.fetch(new Request("https://site.example/dl/x.gz"), { ASSETS: dlAssets("../etc"), DL });
  assert.equal(r.status, 404);
  r = await worker.fetch(new Request("https://site.example/dl/x.gz", { method: "PUT", body: "x" }), { ASSETS: dlAssets("a"), DL });
  assert.equal(r.status, 405);
});

test("the other files under /dl/ stay the site's static files", async () => {
  for (const name of ["VERSION", "SHA256SUMS", "latest.json", "SLOT"]) {
    const r = await worker.fetch(new Request("https://site.example/dl/" + name), { ASSETS: dlAssets("a"), DL: bucket({}) });
    if (name !== "SLOT") assert.equal(await r.text(), "asset /dl/" + name);
  }
  // deeper paths, other files under a version and a dot-dot are not downloads
  for (const p of ["/dl/a/b/x.gz", "/dl/v1/latest.json", "/dl/../x.gz", "/dl/v1/.x.gz"]) {
    const r = await worker.fetch(new Request("https://site.example" + p), { ASSETS: dlAssets("a"), DL: bucket({ "a/b/x.gz": "x" }) });
    assert.equal(await r.text(), "asset " + new URL("https://site.example" + p).pathname, p);
  }
});

test("/dl/<version>/ comes from the kept release, never a slot", async () => {
  const DL = bucket({
    "release/v1-202611060000/personad-linux-amd64.gz": "kept bytes", "release/v1-202611060000/manifest.json": '{"version":"v1"}',
    "a/personad-linux-amd64.gz": "slot bytes",
  });
  const env = { ASSETS: dlAssets("a"), DL };
  let r = await worker.fetch(new Request("https://site.example/dl/v1-202611060000/personad-linux-amd64.gz"), env);
  assert.equal(r.status, 200);
  assert.equal(await r.text(), "kept bytes");
  r = await worker.fetch(new Request("https://site.example/dl/v1-202611060000/manifest.json"), env);
  assert.equal(await r.text(), '{"version":"v1"}');
  r = await worker.fetch(new Request("https://site.example/dl/v1-202611060000/personad-linux-amd64.gz", { headers: { range: "bytes=5-" } }), env);
  assert.equal(r.status, 206);
  assert.equal(await r.text(), "bytes");
  r = await worker.fetch(new Request("https://site.example/dl/v1-202611060000/personad-linux-amd64.gz", { method: "HEAD" }), env);
  assert.equal(r.headers.get("content-length"), "10");
  r = await worker.fetch(new Request("https://site.example/dl/a/personad-linux-amd64.gz"), env); // a slot is not a version
  assert.equal(r.status, 404);
  r = await worker.fetch(new Request("https://site.example/dl/v2/manifest.json"), env);
  assert.equal(r.status, 404);
  r = await worker.fetch(new Request("https://site.example/dl/v1-202611060000/manifest.json", { method: "POST", body: "x" }), env);
  assert.equal(r.status, 405);
});

// ---- /extensions: the catalog behind the page and its icons ----

// A signed index like the official market's: the payload is a JSON string.
function index(extensions, extra = {}) {
  return { payload: JSON.stringify({ name: "Official", extensions, generation: { repository: "nwverse/persona-extensions", source_commit: "abc" }, ...extra }), signatures: [] };
}
const entry = (id, name, more = {}, manifest = {}) => ({ manifest: { id, name, version: "0.1.0", description: name + " does things", publisher: "nwverse", homepage: `https://github.com/nwverse/persona-extensions/tree/main/extensions/${id}`, ...manifest }, tags: ["阅读"], packages: [], ...more });

// A fake edge cache (caches.default) and upstream; restored after each test.
function edge() {
  const store = new Map();
  globalThis.caches = { default: {
    async match(key) { const r = store.get(String(key)); return r ? r.clone() : undefined; },
    async put(key, r) { store.set(String(key), r.clone()); },
  } };
  return store;
}
function upstream(handler) {
  const calls = [];
  globalThis.fetch = async (url, init) => { calls.push(String(url)); return handler(String(url), init); };
  return calls;
}
const catalog = (env = {}) => worker.fetch(new Request("https://site.example/extensions/catalog.json"), { ASSETS: assets, ...env }, { waitUntil() {} });

test("/extensions/catalog.json is the slim catalog, most downloaded first", async () => {
  edge();
  const calls = upstream(() => Response.json(index([
    entry("weather", "天气", { downloads: 5 }),
    entry("github", "GitHub", { downloads: 120, icon_url: "https://example.com/gh.png" }),
    entry("markets", "行情", {}, { downloads: 40, icon_url: "http://insecure.example/x.png", category: "Finance" }),
    entry("github", "duplicate"),
    entry("../bad", "bad id"),
  ], { downloads_updated_at: "2026-11-11T00:00:00Z" })));
  const r = await catalog();
  assert.equal(r.status, 200);
  assert.deepEqual(calls, ["https://raw.githubusercontent.com/nwverse/persona-extensions/main/persona-extensions.json"]);
  const d = await r.json();
  assert.deepEqual(d.extensions.map((e) => e.id), ["github", "markets", "weather"]);
  assert.equal(d.downloads_updated_at, "2026-11-11T00:00:00Z");
  assert.equal(d.source_commit, "abc");
  const [gh, mk] = d.extensions;
  assert.equal(gh.downloads, 120);
  assert.equal(gh.icon_url, "https://example.com/gh.png");
  assert.equal(gh.icon, "/extensions/icons/github?v=0.1.0");
  assert.equal(mk.downloads, 40, "a count on the manifest counts too");
  assert.equal(mk.icon_url, "", "only https icons");
  assert.equal(mk.icon, "");
  assert.deepEqual(mk.categories, ["Finance"]);
  assert.deepEqual(gh.tags, ["阅读"]);
  assert.equal(d.extensions[0].packages, undefined, "package URLs are not part of the page's data");
  assert.equal(gh.source, "https://github.com/nwverse/persona-extensions/tree/main/extensions/github", "the homepage, inside the market");
});

test("every extension links to its source in the market repository, and nowhere else", async () => {
  edge();
  upstream(() => Response.json(index([
    entry("weather", "天气", {}, { homepage: undefined }),
    entry("github", "GitHub", {}, { homepage: "https://github.com/nwverse/persona-extensions/blob/main/extensions/github/README.md" }),
    entry("evil", "Elsewhere", {}, { homepage: "https://example.com/extensions/evil" }),
  ])));
  const d = await (await catalog()).json();
  const source = Object.fromEntries(d.extensions.map((e) => [e.id, e.source]));
  assert.deepEqual(source, {
    weather: "https://github.com/nwverse/persona-extensions/tree/main/extensions/weather",
    github: "https://github.com/nwverse/persona-extensions/blob/main/extensions/github/README.md",
    evil: "https://github.com/nwverse/persona-extensions/tree/main/extensions/evil",
  });
});

test("a catalog generated from another repository is not shown", async () => {
  edge();
  for (const generation of [{ repository: "someone/else" }, {}, undefined]) {
    upstream(() => Response.json({ payload: JSON.stringify({ extensions: [entry("github", "GitHub")], generation }), signatures: [] }));
    assert.equal((await catalog()).status, 503, JSON.stringify(generation));
  }
});

test("without download counts the catalog is in name order", async () => {
  edge();
  upstream(() => Response.json(index([entry("zulip", "Zulip"), entry("anytype", "Anytype"), entry("miniflux", "Miniflux", { downloads: "12" })])));
  const d = await (await catalog()).json();
  assert.deepEqual(d.extensions.map((e) => e.id), ["anytype", "miniflux", "zulip"]);
  assert.equal(d.extensions[1].downloads, null, "a count that is not an integer is no count");
});

test("the catalog is fetched once while fresh, and a stale copy outlives GitHub", async () => {
  const store = edge();
  let fail = false;
  const calls = upstream(() => (fail ? new Response("down", { status: 502 }) : Response.json(index([entry("github", "GitHub")]))));
  assert.equal((await catalog()).status, 200);
  assert.equal((await catalog()).status, 200);
  assert.equal(calls.length, 1, "served from the cache while fresh");
  // Age the cached copy past FRESH_MS, then make GitHub fail.
  const [key, r] = [...store.entries()][0];
  const old = await r.json();
  store.set(key, Response.json({ ...old, fetched_at: new Date(Date.now() - 3600_000).toISOString() }));
  fail = true;
  const s = await catalog();
  assert.equal(s.status, 200);
  const d = await s.json();
  assert.equal(d.stale, true);
  assert.deepEqual(d.extensions.map((e) => e.id), ["github"]);
  assert.equal(calls.length, 2);
});

test("with no copy and GitHub down the catalog is 503; writes are refused", async () => {
  edge();
  upstream(() => { throw new Error("unreachable"); });
  const r = await catalog();
  assert.equal(r.status, 503);
  assert.equal(r.headers.get("cache-control"), "no-store");
  upstream(() => Response.json({ payload: "{}" }));
  assert.equal((await catalog()).status, 503, "a catalog without extensions is no catalog");
  const w = await worker.fetch(new Request("https://site.example/extensions/catalog.json", { method: "POST", body: "x" }), { ASSETS: assets }, {});
  assert.equal(w.status, 405);
});

test("/extensions/icons/<id> proxies only the catalog's icon, and only images", async () => {
  edge();
  let iconType = "image/png";
  const calls = upstream((url) => {
    if (url.endsWith("persona-extensions.json")) {
      return Response.json(index([entry("github", "GitHub", { icon_url: "https://icons.example/gh.png" }), entry("weather", "天气")]));
    }
    return new Response("PNGDATA", { headers: { "content-type": iconType } });
  });
  const icon = (id) => worker.fetch(new Request(`https://site.example/extensions/icons/${id}?v=1`), { ASSETS: assets }, { waitUntil() {} });
  let r = await icon("github");
  assert.equal(r.status, 200);
  assert.equal(await r.text(), "PNGDATA");
  assert.equal(r.headers.get("content-type"), "image/png");
  assert.equal(r.headers.get("x-content-type-options"), "nosniff");
  assert.match(r.headers.get("content-security-policy"), /sandbox/);
  assert.ok(calls.includes("https://icons.example/gh.png"));
  assert.equal((await icon("weather")).status, 404, "no icon in the catalog");
  assert.equal((await icon("nope")).status, 404);
  iconType = "text/html";
  assert.equal((await icon("github")).status, 502, "not an image");
});

test("the page itself and other /extensions paths are static files", async () => {
  for (const p of ["/extensions/", "/extensions", "/extensions/icons/a/b", "/extensions/catalog.json.bak"]) {
    const r = await worker.fetch(new Request("https://site.example" + p), { ASSETS: assets }, {});
    assert.equal(await r.text(), "asset " + p, p);
  }
});
