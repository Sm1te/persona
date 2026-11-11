/// <reference types="node" />
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("quiet surfaces independent of restored status tints", () => {
  it("keeps the shared permission/plan/question/MCP/terminal card neutral with amber details", () => {
    const interactions = source("../views/chat/Interactions.tsx");
    const card = interactions.slice(interactions.indexOf("function Card("), interactions.indexOf("function OptionLabel("));
    expect(card).toContain("bg-surface shadow-card");
    expect(card).toContain("border-t border-line bg-inset");
    expect(card).toContain("text-attention");
    expect(card).not.toMatch(/bg-(green|orange|ai|red)-tint|text-red/);
  });
});
