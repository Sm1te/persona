// How many chats the sidebar's Persona chats › Recent lists (#5146): the count is a setting (Settings › General ›
// Appearance), and each Show more adds that many again until none are left. Both live in this browser only, as the
// text size does, so a reload keeps what was open. (⌘K's list of opened chats is lib/recent-chats.ts, not this.)
import { useSyncExternalStore } from "react";

export const RECENT_COUNTS = [5, 10, 15, 25] as const;
export const RECENT_DEFAULT = 15;
const COUNT_KEY = "persona.sidebar-recent";
const MORE_KEY = "persona.sidebar-recent.more";

const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => void listeners.delete(l);
};

function read(key: string): number {
  try {
    return Number(localStorage.getItem(key) ?? NaN);
  } catch {
    return NaN;
  }
}
function write(key: string, value: number | null) {
  try {
    if (value === null) localStorage.removeItem(key);
    else localStorage.setItem(key, String(value));
  } catch {
    // not kept: the next page starts with the default
  }
  listeners.forEach((l) => l());
}

/** The setting: how many Recent lists before any Show more. */
export function recentCount(): number {
  const n = read(COUNT_KEY);
  return (RECENT_COUNTS as readonly number[]).includes(n) ? n : RECENT_DEFAULT;
}
/** How many times Show more was pressed since the last Show less. */
export function recentMore(): number {
  const n = read(MORE_KEY);
  return Number.isInteger(n) && n > 0 ? n : 0;
}

export const setRecentCount = (n: number) => write(COUNT_KEY, n === RECENT_DEFAULT ? null : n);
export const setRecentMore = (n: number) => write(MORE_KEY, n > 0 ? n : null);
export const useRecentCount = () => useSyncExternalStore(subscribe, recentCount);
export const useRecentMore = () => useSyncExternalStore(subscribe, recentMore);
