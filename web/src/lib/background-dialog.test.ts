// A chat's Background dialog opened from a workflow card (#5030): only a dialog of that chat that lists the run opens,
// the one drawn last when the chat is drawn twice; watchers hear each change so the cards redraw.
import { describe, expect, it, vi } from "vitest";
import { canOpenBackgroundDialog, listenBackgroundDialog, openBackgroundDialog, subscribeBackgroundDialogs } from "./background-dialog";

describe("background dialog", () => {
  it("opens the chat's dialog at a run it lists, and says no when none does", () => {
    const open = vi.fn();
    const stop = listenBackgroundDialog("m", "s", ["w1", "w2"], open);
    expect(canOpenBackgroundDialog("m", "s", "w1")).toBe(true);
    expect(canOpenBackgroundDialog("m", "s")).toBe(true);
    expect(canOpenBackgroundDialog("m", "s", "w3")).toBe(false);
    expect(canOpenBackgroundDialog("m", "other", "w1")).toBe(false);
    expect(canOpenBackgroundDialog("other", "s", "w1")).toBe(false);

    expect(openBackgroundDialog("m", "s", "w2")).toBe(true);
    expect(open).toHaveBeenCalledWith("w2");
    expect(openBackgroundDialog("m", "s", "w3")).toBe(false);
    expect(openBackgroundDialog("m", "other", "w1")).toBe(false);
    expect(open).toHaveBeenCalledTimes(1);

    stop();
    expect(canOpenBackgroundDialog("m", "s", "w1")).toBe(false);
    expect(openBackgroundDialog("m", "s", "w1")).toBe(false);
  });

  it("opens the one drawn last of a chat drawn twice, and the other once that one goes", () => {
    const first = vi.fn();
    const second = vi.fn();
    const stopFirst = listenBackgroundDialog("m", "s", ["w1"], first);
    const stopSecond = listenBackgroundDialog("m", "s", ["w1"], second);
    openBackgroundDialog("m", "s", "w1");
    expect(second).toHaveBeenCalledWith("w1");
    expect(first).not.toHaveBeenCalled();
    stopSecond();
    openBackgroundDialog("m", "s", "w1");
    expect(first).toHaveBeenCalledWith("w1");
    stopFirst();
  });

  it("passes over a later dialog that does not list the run", () => {
    const lists = vi.fn();
    const stopA = listenBackgroundDialog("m", "s", ["w1"], lists);
    const stopB = listenBackgroundDialog("m", "s", [], vi.fn());
    expect(openBackgroundDialog("m", "s", "w1")).toBe(true);
    expect(lists).toHaveBeenCalledWith("w1");
    stopA();
    stopB();
  });

  it("tells its watchers when a dialog comes and goes, until they stop watching", () => {
    const watch = vi.fn();
    const unwatch = subscribeBackgroundDialogs(watch);
    const stop = listenBackgroundDialog("m", "s", ["w1"], vi.fn());
    expect(watch).toHaveBeenCalledTimes(1);
    stop();
    expect(watch).toHaveBeenCalledTimes(2);
    unwatch();
    listenBackgroundDialog("m", "s", ["w1"], vi.fn())();
    expect(watch).toHaveBeenCalledTimes(2);
  });
});
