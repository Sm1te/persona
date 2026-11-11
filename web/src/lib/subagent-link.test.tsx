// @vitest-environment jsdom
// A link to a subagent's own conversation opens its parent's chat at its card (#5030).
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it } from "vitest";
import { directionAgentNodes } from "./direction-agent-tree";
import { doneSubagentLink, followSubagentLink, splitSubagentLink, useSubagentLink } from "./subagent-link";
import type { Session } from "./types";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("subagent links", () => {
  it("splits <parent>@<agent> only", () => {
    expect(splitSubagentLink("p1@a1")).toEqual({ parent: "p1", agent: "a1" });
    expect(splitSubagentLink("p1")).toBeNull();
    expect(splitSubagentLink("@a1")).toBeNull();
    expect(splitSubagentLink("p1@")).toBeNull();
  });

  it("opens the parent and asks its chat for the card until the chat did", () => {
    let seen: string | undefined;
    function Probe() {
      seen = useSubagentLink("m", "p1");
      return null;
    }
    const host = document.createElement("div");
    const root = createRoot(host);
    act(() => root.render(<Probe />));
    expect(seen).toBeUndefined();
    act(() => void expect(followSubagentLink("m", "p1@a1")).toBe("p1"));
    expect(seen).toBe("a1");
    expect(followSubagentLink("m", "p2")).toBe("p2");
    act(() => doneSubagentLink("m", "p1"));
    expect(seen).toBeUndefined();
    act(() => root.unmount());
  });

  it("the sidebar's direction tree draws no in-session subagent rows", () => {
    const s = (sid: string): Session => ({ machine_id: "m", session_id: sid, title: sid, cwd: "", status: "", state: "working" });
    const nodes = directionAgentNodes(
      [{ machine_id: "m", session_id: "p1", title: "Lead", state: "working", children: [{ machine_id: "m", session_id: "p1@a1", title: "Sub", state: "working", children: [{ machine_id: "m", session_id: "p1@a2", title: "Nested", state: "working", children: [] }] }, { machine_id: "m", session_id: "codex-kid", title: "Codex kid", state: "working", children: [] }] }],
      new Map([["m/p1", s("p1")], ["m/codex-kid", s("codex-kid")]]),
      new Set(),
      true,
    );
    expect(nodes.map((n) => n.session.session_id)).toEqual(["p1"]);
    expect(nodes[0].children.map((n) => n.session.session_id)).toEqual(["codex-kid"]);
  });
});
