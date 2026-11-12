// A Persona conversation's menu in the sidebar (the iOS sidebar's long press, Android's drawer): "设为常驻" / "取消常驻",
// "Rename…", "Archive", "Copy session ID", then "End…" while it runs. The main conversation is never a lead, renamed or
// archived; the background supervisor (#5082, lib/background-supervisor.ts) is built in and only has "Copy session ID".
// All of it is the hub's (the same calls as "All chats"): 设为常驻 makes the chat a leader (#4331,
// POST …/directions/{id}/make), 取消常驻 ends it (…/end; the tasks it handed out keep their supervisor).
import { api, enc, errorText, sessionPath } from "../lib/api";
import { sessionTitle } from "../lib/format";
import { refreshAssistant } from "../lib/store";
import type { Session } from "../lib/types";
import { copyText, Menu, toast, type MenuItem, type ToastAction } from "../ui/atoms";
import type { MenuAt } from "../ui/menu-model";
import { t } from "../i18n";

/** What the hub does for the menu; tests swap it. */
export interface ConversationMenuDeps {
  rename: (s: Session, title: string) => Promise<unknown>;
  archive: (s: Session) => Promise<unknown>;
  unarchive: (s: Session) => Promise<unknown>;
  end: (s: Session) => Promise<unknown>;
  refresh: () => Promise<unknown>;
  /** Makes the conversation a leader (常驻) or ends it, on the hub for every device. */
  lead: (s: Session, on: boolean) => Promise<unknown>;
  copy: (text: string) => unknown;
  notify: (text: string, tone: "ok" | "error", action?: ToastAction) => void;
}

export const conversationMenuDeps: ConversationMenuDeps = {
  rename: (s, title) => api(`${sessionPath(s.machine_id, s.session_id)}/rename`, { method: "POST", json: { title } }),
  archive: (s) => api(`/assistant/conversations/${enc(s.session_id)}/archive`, { method: "POST", json: {} }),
  unarchive: (s) => api(`/assistant/conversations/${enc(s.session_id)}/unarchive`, { method: "POST", json: {} }),
  end: (s) => api(`${sessionPath(s.machine_id, s.session_id)}/close`, { method: "POST" }),
  refresh: () => refreshAssistant(),
  lead: (s, on) => api(`/assistant/directions/${enc(s.session_id)}/${on ? "make" : "end"}`, { method: "POST", json: {} }),
  copy: copyText,
  notify: (text, tone, action) => toast(text, tone, action),
};

/** A running conversation can be ended (iOS `status.isLive`). */
export const conversationLive = (s: Session) => s.status !== "offline";

const reread = (deps: ConversationMenuDeps) => void deps.refresh().catch(() => {});

async function undoArchive(s: Session, title: string, deps: ConversationMenuDeps) {
  try {
    await deps.unarchive(s);
    deps.notify(t("conversation-menu.unarchived", { title }), "ok");
  } catch (e) {
    deps.notify(t("conversation-menu.undo_failed", { error: errorText(e) }), "error");
  }
  reread(deps);
}

/** 设为常驻 / 取消常驻 for a conversation that is not the main one (`leader`: it is one now). */
export function leaderItem(s: Session, leader: boolean, deps: ConversationMenuDeps = conversationMenuDeps): MenuItem {
  const title = sessionTitle(s);
  return {
    key: "leader",
    label: leader ? t("conversation-menu.end_leader") : t("conversation-menu.make_leader"),
    icon: "pin",
    accel: "p",
    onSelect: async () => {
      try {
        await deps.lead(s, !leader);
        deps.notify(leader ? t("conversation-menu.leader_ended", { title }) : t("conversation-menu.leader_made", { title }), "ok");
      } catch (e) {
        deps.notify(t("conversation-menu.leader_failed", { error: errorText(e) }), "error");
      }
      reread(deps);
    },
  };
}

/** `leader`: the conversation is a leader (常驻) now. `builtIn`: it is the background supervisor, which the hub keeps
 *  listed and running: no 设为常驻, Rename, Archive or End. */
export function conversationMenuItems(s: Session, o: { main: boolean; leader?: boolean; builtIn?: boolean }, deps: ConversationMenuDeps = conversationMenuDeps): MenuItem[] {
  const copy: MenuItem = { key: "copy", label: t("conversation-menu.copy_id"), icon: "copy", accel: "c", onSelect: () => void deps.copy(s.session_id) };
  if (o.builtIn) return [copy];
  const title = sessionTitle(s);
  const own: MenuItem[] = o.main
    ? []
    : [
        leaderItem(s, !!o.leader, deps),
        {
          key: "rename",
          label: t("conversation-menu.rename"),
          icon: "pencil",
          accel: "r",
          onSelect: () => {},
          rename: {
            value: s.title ?? "",
            placeholder: t("conversation-menu.title_placeholder"),
            commit: async (name) => {
              await deps.rename(s, name).catch((e) => Promise.reject(new Error(t("conversation-menu.rename_failed", { error: errorText(e) }))));
              deps.notify(t("conversation-menu.renamed"), "ok");
              reread(deps);
            },
          },
        },
        {
          key: "archive",
          label: t("conversation-menu.archive"),
          icon: "archive",
          onSelect: async () => {
            try {
              await deps.archive(s);
              // Undo puts it back as it was (an open conversation stays open: its page reads the machine's sessions)
              deps.notify(t("conversation-menu.archived", { title }), "ok", { label: t("conversation-menu.undo"), run: () => void undoArchive(s, title, deps) });
            } catch (e) {
              deps.notify(errorText(e), "error");
            }
            reread(deps);
          },
        },
      ];
  return [
    ...own,
    copy,
    ...(conversationLive(s)
      ? [
          {
            key: "end",
            label: t("conversation-menu.end"),
            icon: "stop" as const,
            danger: true,
            divider: true,
            closes: true,
            confirm: t("conversation-menu.end_confirm"),
            inline: true,
            onSelect: async () => {
              await deps.end(s).catch((e) => Promise.reject(new Error(t("conversation-menu.end_failed", { error: errorText(e) }))));
              deps.notify(t("conversation-menu.ended"), "ok");
              reread(deps);
            },
          },
        ]
      : []),
  ];
}

/** The menu of a row (`at`: where a right click opened it; without it, beside `anchor`). */
export function ConversationRowMenu({ s, main, leader, builtIn, anchor, at, onClose }: { s: Session; main: boolean; leader?: boolean; builtIn?: boolean; anchor: HTMLElement; at?: MenuAt; onClose: () => void }) {
  const p = at ?? { anchor };
  return <Menu anchor={p.anchor ?? null} point={p.point} onClose={onClose} align="right" width={200} items={conversationMenuItems(s, { main, leader, builtIn })} />;
}

