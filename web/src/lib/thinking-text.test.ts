// Ported from cindy's thinkingText.test.ts and thinkingTextRendering.test.tsx (Apache-2.0), #5155.
import { describe, expect, it } from "vitest";
import { thinkingPlainText, tokenizeThinkingText } from "./thinking-text";

describe("tokenizeThinkingText", () => {
  it("reads paired strong markers and keeps inline code", () => {
    expect(tokenizeThinkingText("**Inspecting files** with `git status`")).toEqual([
      { kind: "strong", value: "Inspecting files" },
      { kind: "text", value: " with " },
      { kind: "code", value: "git status" },
    ]);
  });

  it("keeps malformed, escaped, glob and triple-star markers literal", () => {
    for (const content of ["**unfinished", String.raw`\**escaped**`, "**/*.ts", "***nested***", "2 ** 3", "** spaced**"]) {
      expect(tokenizeThinkingText(content)).toEqual([{ kind: "text", value: content }]);
    }
  });

  it("matches backtick runs, without reading unrelated Markdown", () => {
    expect(tokenizeThinkingText("Use ``a ` b`` and [docs](https://example.com)")).toEqual([
      { kind: "text", value: "Use " },
      { kind: "code", value: "a ` b" },
      { kind: "text", value: " and [docs](https://example.com)" },
    ]);
    expect(tokenizeThinkingText("an `unclosed span")).toEqual([{ kind: "text", value: "an `unclosed span" }]);
  });

  it("puts a code span on one line, keeping the text's own line breaks", () => {
    expect(tokenizeThinkingText("first line\nsecond with `git\nstatus` and `` ` ``")).toEqual([
      { kind: "text", value: "first line\nsecond with " },
      { kind: "code", value: "git status" },
      { kind: "text", value: " and " },
      { kind: "code", value: "`" },
    ]);
  });

  it("plain words drop the markers", () => {
    expect(thinkingPlainText(tokenizeThinkingText("**Reviewing** the `diff` now"))).toBe("Reviewing the diff now");
    expect(thinkingPlainText(tokenizeThinkingText(""))).toBe("");
  });
});
