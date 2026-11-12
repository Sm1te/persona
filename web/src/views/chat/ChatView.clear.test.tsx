// /clear in the main conversation (#5095): listed in its command list when the hub can clear it, and sending it clears
// the context instead of handing the word to Persona; other conversations, and a hub without the feature, keep the
// words as words.
import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComposerCommand } from "../../lib/extension-prompts";
import type { Session } from "../../lib/types";
import type { Outgoing } from "./Composer";

const captured = vi.hoisted(() => ({
  onSend: undefined as ((out: Outgoing) => Promise<boolean>) | undefined,
  commands: undefined as ComposerCommand[] | undefined,
  session: { machine_id: "hub", session_id: "c1", title: "Conversation", cwd: "/code", status: "idle", assistant: true } as Session,
  state: { main: "c1", features: ["assistant_main_clear"] },
}));

vi.mock("./Composer", async (importOriginal) => ({
  ...await importOriginal<typeof import("./Composer")>(),
  Composer: ({ onSend, commands }: { onSend: (out: Outgoing) => Promise<boolean>; commands?: ComposerCommand[] }) => {
    captured.onSend = onSend;
    captured.commands = commands;
    return null;
  },
}));
vi.mock("./useTranscript", () => ({ useTranscript: () => ({ items: [], loading: false, error: "", hasMore: false, streaming: "" }) }));
vi.mock("../../lib/store", async (importOriginal) => {
  const store = await importOriginal<typeof import("../../lib/store")>();
  return {
    ...store,
    getSession: () => captured.session,
    useStore: <T,>(select: (s: ReturnType<typeof store.getState>) => T) =>
      select({
        ...store.getState(),
        sessions: { hub: [captured.session] },
        features: captured.state.features,
        assistant: { machine_id: "hub", main: captured.state.main, conversations: [] } as unknown as ReturnType<typeof store.getState>["assistant"],
      }),
  };
});
vi.mock("../../lib/panel", async (importOriginal) => ({ ...await importOriginal<typeof import("../../lib/panel")>(), usePanel: () => null }));
vi.mock("../../ui/atoms", async (importOriginal) => ({ ...await importOriginal<typeof import("../../ui/atoms")>(), toast: vi.fn() }));
const navigate = vi.hoisted(() => vi.fn());
vi.mock("../../lib/router", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/router")>(),
  navigate,
  useRoute: () => ({ name: "persona" }),
}));
vi.mock("../assistant/Persona", () => ({ usePersona: () => ({ name: "Persona" }) }));
vi.mock("../assistant/AssistantChat", () => ({
  AssistantFace: () => null,
  AssistantLauncherChip: () => null,
  CompactionStrip: () => null,
  useAssistantMenu: () => ({ items: [], dialogs: null }),
}));
vi.mock("../assistant/Tasks", () => ({
  DayLine: () => null,
  FeedLineView: () => null,
  QuickResultCard: () => null,
  RouteChip: () => null,
  AssistantActionBar: () => null,
  TaskDock: () => null,
  TaskQuestions: () => null,
}));

import { forgetMainCleared } from "../../lib/main-clear";
import { ChatView } from "./ChatView";

function render(sid = "c1", assistant = true) {
  captured.onSend = undefined;
  renderToStaticMarkup(<ChatView mid="hub" sid={sid} assistant={assistant} />);
  return { send: captured.onSend!, commands: captured.commands ?? [] };
}

let fetcher: ReturnType<typeof vi.fn>;
beforeEach(() => {
  captured.state = { main: "c1", features: ["assistant_main_clear"] };
  navigate.mockClear();
  vi.stubGlobal("localStorage", { getItem: () => "", setItem: vi.fn(), removeItem: vi.fn() });
  fetcher = vi.fn().mockResolvedValue({ ok: true, status: 200, text: async () => "{}" });
  vi.stubGlobal("fetch", fetcher);
});
afterEach(() => {
  forgetMainCleared();
  vi.unstubAllGlobals();
});

const paths = () => fetcher.mock.calls.map(([url, init]) => `${init?.method ?? "GET"} ${url}`);

describe("/clear in the main conversation", () => {
  it("is in its command list, with no confirmation", () => {
    const { commands } = render();
    expect(commands.map((c) => c.word)).toEqual(["/clear"]);
    expect(commands[0].confirm).toBeUndefined();
  });

  it("clears the context in place instead of going to Persona", async () => {
    const { send } = render();
    expect(await send({ text: "/clear", images: [] })).toBe(true);
    expect(paths()).toContain("POST /api/v1/assistant/main/clear");
    expect(paths()).not.toContain("POST /api/v1/sessions/hub/c1/messages");
    const [, init] = fetcher.mock.calls.find(([url]) => url === "/api/v1/assistant/main/clear")!;
    expect(JSON.parse(init.body)).toEqual({ expect: "c1" });
    expect(navigate).toHaveBeenCalledWith({ name: "persona" });
  });

  it("an edit of the last message to /clear stays an edit: its rewind already ran, the words go", async () => {
    const { send } = render();
    expect(await send({ text: "/clear", images: [], edit: { clearDraft: "" } })).toBe(true);
    expect(paths()).toEqual(["POST /api/v1/sessions/hub/c1/messages"]);
  });

  it("anything more than the word is a message", async () => {
    const { send } = render();
    expect(await send({ text: "/clear the table in my notes", images: [] })).toBe(true);
    expect(paths()).toEqual(["POST /api/v1/sessions/hub/c1/messages"]);
  });

  it.each([
    ["another of Persona's conversations", "c9", true, ["assistant_main_clear"]],
    ["a hub that cannot clear it", "c1", true, []],
  ] as const)("not in %s: no command, and /clear goes as words", async (_, sid, assistant, features) => {
    captured.state = { main: "c1", features: [...features] };
    captured.session = { ...captured.session, session_id: sid };
    const { send, commands } = render(sid, assistant);
    expect(commands).toEqual([]);
    expect(await send({ text: "/clear", images: [] })).toBe(true);
    expect(paths()).toEqual([`POST /api/v1/sessions/hub/${sid}/messages`]);
    captured.session = { ...captured.session, session_id: "c1" };
  });
});
