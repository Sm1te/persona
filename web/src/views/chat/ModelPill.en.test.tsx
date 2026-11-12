// The model pill in English (#5032): Cindy's compact effort labels on the pill only. Persona's English "Very high" is
// "Extra" there and "Max" stays; the tooltip and the effort's own title keep the full name. The catalog is English before
// the modules load, as on an English page (some effort names are read once, at load).
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { initI18n } from "../../i18n";

await initI18n("en");
const { ModelPill, pillEffort } = await import("./ModelPill");

it("says Extra for Very high and Max as it is, on the pill only", () => {
  expect(pillEffort("xhigh")).toBe("Extra");
  expect(pillEffort("max")).toBe("Max");
  expect(pillEffort("high")).toBe("High");
  const html = renderToStaticMarkup(<ModelPill current={{ engine: "claude", provider: "gateway", model: "Opus 5.5", effort: "xhigh" }} onOpen={() => {}} />);
  expect(html).toContain('<span title="Very high" data-pill-effort');
  expect(html).toContain(">Extra</span>");
  expect(html).toContain('title="Claude Code · Opus 5.5 · gateway · Very high"');
  expect(html).toContain('aria-label="Select model. Current: Opus 5.5, thinking effort: Very high"');
});
