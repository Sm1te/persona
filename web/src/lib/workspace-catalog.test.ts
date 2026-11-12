// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { CATALOG_POLL_MS, WorkspaceCatalog, workspaceCatalogOwner, workspaceKey, workspaceTarget, type HostWorkspace } from "./workspace-catalog";
import { credentialOwner, setToken } from "./api";
import { chooseManager, resetManagerChoice, setRosterDefault } from "./manager-choice";

const scope = { owner: "credential-a", manager: "manager-a" };
const row = (title = "Main"): HostWorkspace => ({ id: "qualified-id", machine_id: "machine-a", source: "orca", source_id: "project::/code/app", title, cwd: "/code/app", project_name: "App", is_main: true });
const deferred = <T,>() => { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; };
const transport = (read: () => Promise<unknown>) => vi.fn(read) as unknown as <T>(path: string, init?: RequestInit) => Promise<T>;
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

describe("independent workspace catalog", () => {
  it("pins the chosen assistant and rejects a former assistant's late inventory", async () => {
    resetManagerChoice(); setRosterDefault("manager-a"); setToken("synthetic-catalog-choice");
    try {
      const read = deferred<{ workspaces: HostWorkspace[] }>();
      const catalog = new WorkspaceCatalog(workspaceCatalogOwner(), transport(() => read.promise));
      const pending = catalog.refresh();
      chooseManager("manager-b");
      expect(workspaceCatalogOwner()).toEqual({ owner: credentialOwner(), manager: "manager-b" });
      read.resolve({ workspaces: [row()] });
      await pending;
      expect(catalog.snapshot().workspaces).toEqual([]);
      const request = transport(async () => ({ workspaces: [row()] }));
      await new WorkspaceCatalog(workspaceCatalogOwner(), request).refresh();
      expect(request).toHaveBeenCalledWith("/workspaces", expect.objectContaining({ headers: { "X-Persona-Manager": "manager-b" } }));
    } finally { resetManagerChoice(); setToken(""); }
  });
  it("reads only /workspaces with explicit manager ownership and retains stable rows on unchanged/reordered snapshots", async () => {
    let rows = [row(), { ...row("Child"), source_id: "child" }];
    const request = transport(async () => ({ workspaces: rows }));
    const catalog = new WorkspaceCatalog(scope, request, () => scope);
    const changed = vi.fn(); catalog.subscribe(changed);
    await catalog.refresh();
    const first = catalog.snapshot();
    rows = rows.map((w) => ({ ...w })).reverse();
    await catalog.refresh();
    expect(catalog.snapshot()).toBe(first);
    expect(changed).toHaveBeenCalledTimes(1);
    expect(request).toHaveBeenCalledWith("/workspaces", expect.objectContaining({ headers: { "X-Persona-Manager": scope.manager } }));
    rows = rows.map((w) => w.source_id === "child" ? { ...w, title: "Renamed" } : w);
    await catalog.refresh();
    expect(catalog.snapshot().workspaces.find((w) => w.source_id !== "child")).toBe(first.workspaces.find((w) => w.source_id !== "child"));
    expect(catalog.snapshot().workspaces.find((w) => w.source_id === "child")?.title).toBe("Renamed");
  });

  it("keeps the last good inventory on failures and partial machine failures, and removes it after a successful empty read", async () => {
    let read = async (): Promise<unknown> => ({ workspaces: [row()] });
    const catalog = new WorkspaceCatalog(scope, transport(() => read()), () => scope);
    await catalog.refresh(); const first = catalog.snapshot();
    read = async () => { throw new Error("offline"); };
    await catalog.refresh(); expect(catalog.snapshot()).toBe(first);
    read = async () => ({ workspaces: [], errors: [{ machine_id: "machine-a", error: "offline" }] });
    await catalog.refresh(); expect(catalog.snapshot()).toBe(first);
    read = async () => ({ workspaces: [] });
    await catalog.refresh(); expect(catalog.snapshot().workspaces).toEqual([]);
  });

  it("rejects a superseded read after an acknowledged rename refresh, even if transport ignores abort", async () => {
    const old = deferred<unknown>(), current = deferred<unknown>();
    let read = old;
    const catalog = new WorkspaceCatalog(scope, transport(() => read.promise), () => scope);
    const a = catalog.refresh(); read = current;
    const b = catalog.refresh();
    current.resolve({ workspaces: [row("Renamed")] }); await b;
    old.resolve({ workspaces: [row("Old name")] }); await a;
    expect(catalog.snapshot().workspaces[0].title).toBe("Renamed");
  });

  it("never publishes a former credential or manager's response", async () => {
    for (const next of [{ ...scope, owner: "credential-b" }, { ...scope, manager: "manager-b" }]) {
      const pending = deferred<unknown>(); let owner = scope;
      const catalog = new WorkspaceCatalog(scope, transport(() => pending.promise), () => owner);
      const reading = catalog.refresh(); owner = next;
      pending.resolve({ workspaces: [row()] }); await reading;
      expect(catalog.snapshot().workspaces).toEqual([]);
    }
  });

  it("stops polling and cancels outstanding reads while hidden, then refreshes on return; shares watchers", async () => {
    vi.useFakeTimers();
    let visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility as DocumentVisibilityState);
    const request = transport(async () => ({ workspaces: [row()] }));
    const catalog = new WorkspaceCatalog(scope, request, () => scope);
    const stop = catalog.watch(); const stopOther = catalog.watch();
    await Promise.resolve(); expect(request).toHaveBeenCalledTimes(1);
    stopOther();
    await vi.advanceTimersByTimeAsync(CATALOG_POLL_MS); expect(request).toHaveBeenCalledTimes(2);
    visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(30_000); expect(request).toHaveBeenCalledTimes(2);
    visibility = "visible"; document.dispatchEvent(new Event("visibilitychange"));
    await Promise.resolve(); expect(request).toHaveBeenCalledTimes(3);
    stop(); await vi.advanceTimersByTimeAsync(20_000); expect(request).toHaveBeenCalledTimes(3);
  });

  it("re-reads a watched catalog once a minute, as Apple does (#5168)", async () => {
    vi.useFakeTimers();
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => "visible");
    const request = transport(async () => ({ workspaces: [row()] }));
    const stop = new WorkspaceCatalog(scope, request, () => scope).watch();
    await Promise.resolve(); expect(request).toHaveBeenCalledTimes(1);
    // every 10 s this was 5 more reads by now; each one asks every online computer live (workspaces.list)
    await vi.advanceTimersByTimeAsync(59_000); expect(request).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1_000); expect(request).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(60_000); expect(request).toHaveBeenCalledTimes(3);
    stop();
  });

  it("does not overlap or starve slow discovery reads, and rejects a response received after hiding", async () => {
    vi.useFakeTimers();
    let visibility = "visible";
    vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility as DocumentVisibilityState);
    const pending = deferred<unknown>();
    const request = transport(() => pending.promise);
    const catalog = new WorkspaceCatalog(scope, request, () => scope);
    const stop = catalog.watch();
    await vi.advanceTimersByTimeAsync(30_000); expect(request).toHaveBeenCalledTimes(1);
    visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange"));
    pending.resolve({ workspaces: [row()] }); await Promise.resolve(); await Promise.resolve();
    expect(catalog.snapshot().workspaces).toEqual([]);
    stop();
  });

  it("keys source-local IDs by credential, manager, machine and source and exports the exact runtime target", () => {
    const w = row();
    const key = workspaceKey(w, scope.owner, scope.manager);
    for (const candidate of [workspaceKey(w, "other", scope.manager), workspaceKey(w, scope.owner, "other"), workspaceKey({ ...w, machine_id: "other" }, scope.owner, scope.manager), workspaceKey({ ...w, source: "herdr" }, scope.owner, scope.manager)]) expect(candidate).not.toBe(key);
    expect(workspaceTarget(w, scope.owner, scope.manager)).toEqual({ ...scope, machineId: w.machine_id, host: "orca", workspaceId: w.source_id, cwd: w.cwd, label: "Main" });
  });
});
