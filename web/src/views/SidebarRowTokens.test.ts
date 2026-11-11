import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

// Sidebar rows take their height from --sidebar-row, never the text's own line box, and headings are not uppercased
// (docs/apple/sidebar.md, #1647): the same size in every language.
const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");
// The sidebar's source is Sidebar.tsx and the files split out of it.
const sidebar = ["./Sidebar.tsx", "./SidebarParts.tsx", "./SidebarRows.tsx", "./SidebarTree.tsx", "./SidebarRail.tsx"].map(source).join("\n");
const css = source("../styles.css");

// A sidebar row: a full-width rounded row with the sidebar's horizontal inset.
const rowClasses = [...sidebar.matchAll(/"([^"]*\brounded-\[7px\] px-2\b[^"]*)"/g)].map((m) => m[1]);

describe("sidebar row tokens", () => {
  it("the row height token is the desktop sidebar's 32px", () => {
    expect(css).toMatch(/:root \{ --sidebar-row: 32px;/);
    expect(css).toMatch(/@utility sidebar-row \{[^}]*min-height: var\(--sidebar-row\)/);
  });

  it("every interactive sidebar row uses sidebar-row, with no vertical padding of its own", () => {
    const rows = rowClasses.filter((c) => /\bflex\b/.test(c) && /\bitems-center\b/.test(c));
    expect(rows.length).toBeGreaterThanOrEqual(6); // the Automatic chats rows went with #4331
    for (const c of rows) {
      expect(c, c).toMatch(/\bsidebar-row\b/);
      expect(c, c).not.toMatch(/\bpy-/);
    }
  });

  it("no heading is uppercased: English and Chinese headings look alike", () => {
    expect(sidebar).not.toMatch(/\buppercase\b/);
    expect(sidebar).not.toMatch(/tracking-\[0\.06em\]/);
  });
});
