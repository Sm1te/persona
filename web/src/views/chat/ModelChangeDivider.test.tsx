import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { chatLines } from "../../lib/turns";
import type { Item } from "../../lib/types";
import { ModelChangeDivider } from "./ModelChangeDivider";
import { SegmentView } from "./SegmentView";

const user = (seq: number, text: string): Item => ({ id: `u${seq}#0`, seq, role: "user", kind: "text", text });
const said = (seq: number, model: string, text: string): Item => ({ id: `a${seq}#0`, seq, role: "assistant", kind: "text", text, model });

describe("ModelChangeDivider", () => {
  it("is a separator naming both models, the ids only on hover", () => {
    const html = renderToStaticMarkup(<ModelChangeDivider from="claude-opus-5-5" to="claude-sonnet-4-5-20250929" />);
    expect(html).toContain('role="separator"');
    expect(html).toContain('aria-label="模型已从 Opus 5.5 切换到 Sonnet 4.5"'); // tests run in zh-Hans
    expect(html).toContain("Opus 5.5 → Sonnet 4.5");
    expect(html).toContain('title="claude-opus-5-5 → claude-sonnet-4-5-20250929"');
    expect(html).not.toContain("<button");
  });

  it("stands between the message and the reply of the exchange the new model answered", () => {
    const lines = chatLines([user(1, "first question"), said(2, "claude-opus-5-5", "first answer"), user(3, "second question"), said(4, "claude-sonnet-4-5", "second answer")]);
    const html = lines.map((l) => (l.kind === "segment" ? renderToStaticMarkup(<SegmentView seg={l.seg} live={false} mid="m" sid="s" expandFold={async () => {}} />) : "")).join("");
    expect(html.match(/data-model-change/g)).toHaveLength(1);
    const at = html.indexOf("data-model-change");
    expect(html.indexOf("second question")).toBeLessThan(at);
    expect(html.indexOf("second answer")).toBeGreaterThan(at);
    expect(html.indexOf("first answer")).toBeLessThan(html.indexOf("second question"));
  });
});
