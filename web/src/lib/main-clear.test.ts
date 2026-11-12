// Clearing the main conversation's context (#5095): /clear is the word alone; the action asks the hub, marks the page
// as cleared before the answer, goes to the main page, and says why when it could not.
import { afterEach, describe, expect, it, vi } from "vitest";
const choice = vi.hoisted(() => ({ manager: null as string | null }));
vi.mock("./manager-choice", async (orig) => ({ ...(await orig<typeof import("./manager-choice")>()), chosenManager: () => choice.manager }));
import { ApiError } from "./api";
import { clearMainContext, forgetMainCleared, isClearCommand, mainCleared, mainCommands, type ClearDeps } from "./main-clear";

function deps(post: ClearDeps["post"] = () => Promise.resolve({ ok: true, archived: "c1" })) {
  const seen: { cleared: boolean[] } = { cleared: [] };
  const d = {
    post: vi.fn((expect: string) => {
      seen.cleared.push(mainCleared());
      return post(expect);
    }),
    refresh: vi.fn(() => Promise.resolve()),
    home: vi.fn(),
    toast: vi.fn(),
  };
  return { d, seen };
}

afterEach(() => {
  forgetMainCleared();
  choice.manager = null;
});

describe("/clear", () => {
  it("is the word alone, any case, without pictures", () => {
    expect(isClearCommand("/clear", 0)).toBe(true);
    expect(isClearCommand("  /CLEAR \n", 0)).toBe(true);
    expect(isClearCommand("/clear", 1)).toBe(false);
    expect(isClearCommand("/clear the table in my notes", 0)).toBe(false);
    expect(isClearCommand("/clea", 0)).toBe(false);
  });

  it("is the main conversation's one command, worded as an Agent's /clear", () => {
    expect(mainCommands().map((c) => [c.word, c.text])).toEqual([["/clear", "/clear"]]);
    expect(mainCommands()[0].confirm).toBeUndefined();
  });
});

describe("clearMainContext", () => {
  it("asks the hub for the one this device shows, marks the page cleared before the answer, then goes to the main page", async () => {
    const { d, seen } = deps();
    expect(await clearMainContext("c1", d)).toBe(true);
    expect(d.post).toHaveBeenCalledWith("c1");
    expect(seen.cleared).toEqual([true]);
    expect(mainCleared()).toBe(true);
    expect(d.refresh).toHaveBeenCalled();
    expect(d.home).toHaveBeenCalled();
    expect(d.toast).not.toHaveBeenCalled();
  });

  it("another device changed the main conversation: shows what the hub has, says so, and the /clear is used up", async () => {
    const { d } = deps(() => Promise.reject(new ApiError(409, "changed", { code: "main_changed" })));
    expect(await clearMainContext("c1", d)).toBe(true);
    expect(mainCleared()).toBe(false);
    expect(d.refresh).toHaveBeenCalled();
    expect(d.home).toHaveBeenCalled();
    expect(d.toast).toHaveBeenCalledWith(expect.any(String), "info");
  });

  it("a failure says why and gives the /clear back; the page is not left marked", async () => {
    const { d } = deps(() => Promise.reject(new ApiError(503, "the assistant is offline", { error: "the assistant is offline" })));
    expect(await clearMainContext("c1", d)).toBe(false);
    expect(mainCleared()).toBe(false);
    expect(d.home).not.toHaveBeenCalled();
    expect(d.toast).toHaveBeenCalledWith(expect.stringContaining("the assistant is offline"), "error");
  });

  it("marks only the manager it was cleared under: another assistant's empty main page is the ordinary one", async () => {
    choice.manager = "m-home";
    await clearMainContext("c1", deps().d);
    expect(mainCleared()).toBe(true);
    expect(mainCleared("m-work")).toBe(false);
    choice.manager = "m-work";
    expect(mainCleared()).toBe(false);
    expect(mainCleared("m-home")).toBe(true);
  });

  it("a second clear while the first is on its way does nothing", async () => {
    let done!: () => void;
    const { d } = deps(() => new Promise<void>((resolve) => (done = resolve)));
    const first = clearMainContext("c1", d);
    expect(await clearMainContext("c1", d)).toBe(true);
    done();
    expect(await first).toBe(true);
    expect(d.post).toHaveBeenCalledTimes(1);
  });
});
