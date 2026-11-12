// @vitest-environment jsdom
// A finished exchange off screen takes its guessed height (`--seg-est`, docs/web/web.md "Long chats") until it is
// drawn: the model change divider in it (#5034) is part of that guess, so the chat does not jump when it comes in.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { segments, type Segment } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { MODEL_DIVIDER_HEIGHT, SegmentView, segmentHeight } from "./SegmentView";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

const user: Item = { id: "u1#0", seq: 1, role: "user", kind: "text", text: "sort the results by time" };
const call: Item = { id: "t2#0", seq: 2, role: "assistant", kind: "tool_use", tool_use_id: "c2", tool_name: "Bash", tool_input: { command: "ls" }, model: "claude-sonnet-4-5" };
// no answer: the exchange's steps are the very array the view groups, so only the mark differs between the two
const plain: Segment = segments([user, call])[0];
const divided: Segment = { ...plain, modelChange: { from: "claude-opus-5-5", to: "claude-sonnet-4-5" } };

const estimate = () => parseFloat(host.querySelector<HTMLElement>("[data-segment]")!.style.getPropertyValue("--seg-est"));
// the same callbacks every time, as the chat passes them: only the exchange changes between draws
const watch = () => () => {};
const expandFold = async () => {};
const draw = (seg: Segment) => act(() => root.render(<SegmentView seg={seg} live={false} mid="m" sid="s" expandFold={expandFold} offscreen={watch} />));

describe("an exchange's guessed height", () => {
  it("adds the divider as drawn, and the gap before the next block", () => {
    expect(MODEL_DIVIDER_HEIGHT).toBe(42.5); // measured in Chrome: 8 + 1 + 4 + 16.5 + 4 + 1 + 8
    const nodes = [{ kind: "group", id: "g", children: [] }] as never[];
    // the exchange's blocks stand 14 px apart (`gap-3.5`, cindy's stream)
    expect(segmentHeight(undefined, nodes, true) - segmentHeight(undefined, nodes)).toBe(Math.round(28 + 42.5 + 14) - 28);
    const msg = segments([user])[0].user;
    expect(segmentHeight(msg, nodes, true) - segmentHeight(msg, nodes)).toBeGreaterThanOrEqual(56);
    expect(segmentHeight(msg, nodes, true) - segmentHeight(msg, nodes)).toBeLessThanOrEqual(57);
  });

  it("follows the mark when it comes in or goes", () => {
    draw(plain);
    const without = estimate();
    draw(divided);
    expect(host.querySelector("[data-model-change]")).not.toBeNull();
    expect(estimate() - without).toBeGreaterThanOrEqual(56);
    expect(estimate() - without).toBeLessThanOrEqual(57);
    draw(plain);
    expect(estimate()).toBe(without);
  });
});
