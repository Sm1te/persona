import { useEffect, useMemo, useSyncExternalStore } from "react";
import { api, credentialOwner, onCredentialChange, subscribeCredentialOwner } from "./api";
import { shownManager, MANAGER_HEADER, onManagerChoice, useShownManager } from "./manager-choice";
import { sameValue } from "./stable-lines";
import type { WorkspaceTarget as RuntimeWorkspaceTarget } from "./workspace-target";

/** The host inventory, independent of board settings and of the Agents currently running. */
export interface HostWorkspace {
  id: string; machine_id: string; source: string; source_id: string; title: string; cwd: string;
  parent_source_id?: string; project_id?: string; project_name?: string; project_path?: string;
  is_main?: boolean; unread?: boolean; sidebar_status?: string; workspace_kind?: string;
  head?: string; index?: number; branch?: string; online?: boolean; available?: boolean; last_seen?: string;
}
/** Discovered targets always carry their credential/manager partition and human label. */
export interface WorkspaceTarget extends RuntimeWorkspaceTarget {
  owner: string;
  manager: string;
  label: string;
}
export type WorkspaceCatalogOwner = Pick<WorkspaceTarget, "owner" | "manager">;
/** How often a visible page re-reads the catalog while it is watched (#5168). Worktrees change rarely, and the web's own
 *  changes (creating, renaming, removing one) refresh it at once (`refreshWorkspaceCatalog`), as does a page coming back
 *  into view. Each read asks every online computer live (`workspaces.list`), so this is 60 s, as on Apple (#4685). */
export const CATALOG_POLL_MS = 60_000;
export interface WorkspaceCatalogSnapshot extends WorkspaceCatalogOwner { workspaces: HostWorkspace[] }
export interface WorkspaceCatalogList { workspaces: HostWorkspace[]; errors?: { machine_id: string; source?: string; error: string }[] }

export function workspaceCatalogOwner(): WorkspaceCatalogOwner {
  return { owner: credentialOwner(), manager: shownManager() };
}
export function workspaceKey(w: Pick<HostWorkspace, "machine_id" | "source" | "source_id">, owner: string, manager: string): string {
  return JSON.stringify([owner, manager, w.machine_id, w.source, w.source_id]);
}
export function workspaceTarget(w: HostWorkspace, owner: string, manager: string): WorkspaceTarget {
  return { owner, manager, machineId: w.machine_id, host: w.source, workspaceId: w.source_id, cwd: w.cwd, label: w.title || w.branch || w.cwd };
}

type Transport = <T>(path: string, init?: RequestInit) => Promise<T>;
const sameOwner = (a: WorkspaceCatalogOwner, b: WorkspaceCatalogOwner) => a.owner === b.owner && a.manager === b.manager;

/** Each credential/manager partition keeps its last successful inventory. Only the newest read can replace it. */
export class WorkspaceCatalog {
  private state: WorkspaceCatalogSnapshot;
  private listeners = new Set<() => void>();
  private reads = 0;
  private controller?: AbortController;
  private watchers = 0;
  private stopPolling?: () => void;
  constructor(scope: WorkspaceCatalogOwner, private request: Transport = api, private currentOwner = workspaceCatalogOwner) {
    this.state = { ...scope, workspaces: [] };
  }
  snapshot = () => this.state;
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  cancel = () => { this.reads++; this.controller?.abort(); this.controller = undefined; };
  async refresh(): Promise<void> {
    if (!sameOwner(this.state, this.currentOwner())) return;
    this.cancel();
    const read = this.reads;
    const controller = new AbortController();
    this.controller = controller;
    try {
      // Pin even the default manager: a roster switch while this request is in flight cannot redirect it.
      const result = await this.request<WorkspaceCatalogList>("/workspaces", {
        signal: controller.signal, headers: { [MANAGER_HEADER]: this.state.manager },
      });
      if (controller.signal.aborted || read !== this.reads || !sameOwner(this.state, this.currentOwner()) || !Array.isArray(result.workspaces)) return;
      const before = new Map(this.state.workspaces.map((w) => [workspaceKey(w, this.state.owner, this.state.manager), w]));
      const unique = new Map<string, HostWorkspace>();
      for (const w of result.workspaces) {
        if (!w.machine_id || !w.source || !w.source_id) continue;
        const key = workspaceKey(w, this.state.owner, this.state.manager);
        const old = before.get(key);
        unique.set(key, old && sameValue(old, w) ? old : w);
      }
      // Some older managers return partial inventories without cached failed machines. Keep those rows until a
      // successful discovery answers for that machine; a successful empty inventory still removes vanished rows.
      const failed = result.errors ?? [];
      for (const [key, old] of before) {
        if (!unique.has(key) && failed.some((e) => e.machine_id === old.machine_id && (!e.source || e.source === old.source))) unique.set(key, old);
      }
      // Discovery responses may complete in machine order rather than display order. A reorder is not a change.
      const next = [...unique].sort(([a], [b]) => a.localeCompare(b)).map(([, w]) => w);
      if (next.length === this.state.workspaces.length && next.every((w, i) => w === this.state.workspaces[i])) return;
      this.state = { ...this.state, workspaces: next };
      this.listeners.forEach((listener) => listener());
    } catch {
      // Unsupported old hubs and transient failures retain the last good catalog/session-only view.
    } finally {
      if (read === this.reads) this.controller = undefined;
    }
  }
  /** Shared by sidebar and phone consumers. Hidden pages neither poll nor accept an outstanding read. */
  watch(page: Document = document): () => void {
    this.watchers++;
    if (this.watchers === 1) {
      let timer: ReturnType<typeof setTimeout> | undefined;
      let generation = 0;
      const visibility = () => {
        const current = ++generation;
        if (timer) clearTimeout(timer);
        timer = undefined;
        this.cancel();
        if (page.visibilityState === "hidden") return;
        const poll = async () => {
          await this.refresh();
          if (current === generation) timer = setTimeout(() => { void poll(); }, CATALOG_POLL_MS);
        };
        void poll();
      };
      page.addEventListener("visibilitychange", visibility);
      visibility();
      this.stopPolling = () => {
        generation++;
        page.removeEventListener("visibilitychange", visibility);
        if (timer) clearTimeout(timer);
        this.cancel();
      };
    }
    return () => { if (--this.watchers === 0) { this.stopPolling?.(); this.stopPolling = undefined; } };
  }
}

let cachedOwner = "";
const catalogs = new Map<string, WorkspaceCatalog>();
function catalogFor(scope: WorkspaceCatalogOwner): WorkspaceCatalog {
  if (cachedOwner !== scope.owner) {
    catalogs.forEach((catalog) => catalog.cancel());
    catalogs.clear();
    cachedOwner = scope.owner;
  }
  let catalog = catalogs.get(scope.manager);
  if (!catalog) { catalog = new WorkspaceCatalog(scope); catalogs.set(scope.manager, catalog); }
  return catalog;
}
// Retire in-flight reads immediately, including a switch away and back before a response completes.
onManagerChoice(() => catalogs.forEach((catalog) => catalog.cancel()));
onCredentialChange(() => catalogs.forEach((catalog) => catalog.cancel()));

/** Refresh after an acknowledged mutation. Returning to a hidden page always refreshes before resuming polling. */
export function refreshWorkspaceCatalog(): Promise<void> {
  return typeof document !== "undefined" && document.visibilityState === "hidden"
    ? Promise.resolve() : catalogFor(workspaceCatalogOwner()).refresh();
}
export function useWorkspaceCatalog(enabled: boolean): WorkspaceCatalogSnapshot {
  const owner = useSyncExternalStore(subscribeCredentialOwner, credentialOwner, credentialOwner);
  const manager = useShownManager();
  const catalog = useMemo(() => catalogFor({ owner, manager }), [owner, manager]);
  const snapshot = useSyncExternalStore(catalog.subscribe, catalog.snapshot, catalog.snapshot);
  useEffect(() => enabled ? catalog.watch() : undefined, [catalog, enabled]);
  return snapshot;
}
