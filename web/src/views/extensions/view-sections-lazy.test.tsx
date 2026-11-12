// @vitest-environment jsdom
// The first-screen ViewSections (#3614): while the renderer's chunk is on its way it draws each section's title and an
// empty box, so a card's frame doesn't collapse; once the chunk is in, the same sections as ViewBody draws them.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, expect, it } from "vitest";
import type { UIView } from "../../lib/extension-ui";
import { INERT, ViewSections } from "./view-sections-lazy";

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

it("draws the sections' boxes until the renderer is in, then the sections", async () => {
  const view = { sections: [{ title: "Usage", items: [{ type: "text", text: "Forty requests left" }] }] } as unknown as UIView;
  act(() => root.render(<ViewSections view={view} {...INERT} />));
  expect(host.querySelector("h3")?.textContent).toBe("Usage");
  expect(host.querySelector('section[aria-busy="true"]')).not.toBeNull();
  expect(host.textContent).not.toContain("Forty requests left");
  await import("./ViewBody");
  for (let i = 0; i < 50 && !host.textContent?.includes("Forty requests left"); i++) {
    await act(async () => { await new Promise((r) => setTimeout(r, 20)); });
  }
  expect(host.textContent).toContain("Forty requests left");
  expect(host.querySelector('[aria-busy="true"]')).toBeNull();
}, 60_000);
