// @vitest-environment jsdom
/// <reference types="node" />
// Dialogs and popovers opened by a click load with their first open, not with the first screen: Add computer, the
// avatar upload and the model / effort popover (lazyDialog, lazyOverlay; #2798).
import { readFileSync } from "node:fs";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

const reported = vi.hoisted(() => vi.fn());
vi.mock("../lib/error-reports", () => ({ reportBoundary: reported }));

import { lazyDialog, lazyOverlay } from "./LazyOverlay";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

type Props = { open: boolean; onClose: () => void };
const Shown = ({ open }: Props) => <p data-open={String(open)}>dialog</p>;

let root: Root | null = null;
let host: HTMLElement;
const render = async (node: React.ReactNode) => {
  if (!root) {
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  }
  await act(async () => root!.render(node));
  // the chunk's promise, then React's retry of the suspended render
  await act(async () => { await new Promise((done) => setTimeout(done, 0)); });
};

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  host?.remove();
  reported.mockClear();
});

describe("lazyDialog", () => {
  it("loads nothing until its first open, then stays mounted for its exit", async () => {
    const load = vi.fn(async () => Shown);
    const Dialog = lazyDialog(load);
    const onClose = vi.fn();
    await render(<Dialog open={false} onClose={onClose} />);
    expect(load).not.toHaveBeenCalled();
    expect(host.innerHTML).toBe("");

    await render(<Dialog open onClose={onClose} />);
    expect(load).toHaveBeenCalledTimes(1);
    expect(host.querySelector("p")?.dataset.open).toBe("true");

    // closed: still there, told it is closed, so it leaves with its own exit
    await render(<Dialog open={false} onClose={onClose} />);
    expect(host.querySelector("p")?.dataset.open).toBe("false");
    await render(<Dialog open onClose={onClose} />);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("closes when its chunk does not load, and the next open tries again", async () => {
    // as in a browser, not under act(): React retries the import a few times before the boundary takes the error
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = false;
    const error = vi.spyOn(console, "error").mockImplementation(() => {});
    const failing = new TypeError("Failed to fetch dynamically imported module");
    let offline = true;
    const load = vi.fn(async () => {
      if (offline) throw failing;
      return Shown;
    });
    const Dialog = lazyDialog(load);
    const onClose = vi.fn();
    const box = document.createElement("div");
    document.body.append(box);
    const own = createRoot(box);
    try {
      own.render(<Dialog open onClose={onClose} />);
      await vi.waitFor(() => expect(onClose).toHaveBeenCalledTimes(1), { timeout: 3000 });
      expect(reported).toHaveBeenCalledWith(failing, "overlay_boundary", true, expect.anything());
      expect(box.innerHTML).toBe("");

      offline = false;
      // the close the boundary asked for, then a later click: two commits
      own.render(<Dialog open={false} onClose={onClose} />);
      await new Promise((done) => setTimeout(done, 20));
      own.render(<Dialog open onClose={onClose} />);
      await vi.waitFor(() => expect(box.querySelector("p")?.dataset.open).toBe("true"), { timeout: 3000 });
    } finally {
      own.unmount();
      box.remove();
      error.mockRestore();
      (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    }
  });
});

describe("lazyOverlay", () => {
  it("draws the overlay once its chunk is in", async () => {
    const Overlay = lazyOverlay(async () => ({ label }: { label: string; onClose: () => void }) => <p>{label}</p>);
    await render(<Overlay label="popover" onClose={() => {}} />);
    expect(host.textContent).toBe("popover");
  });
});

// The modules the first screen draws must not bring these in with a static import (an `import type` is erased).
const read = (file: string) => readFileSync(new URL(file, import.meta.url), "utf8");
const staticImports = (source: string) => [...source.matchAll(/^import(?!\s+type\b)[^;]*from\s+["']([^"']+)["']/gm)].map((m) => m[1]);

describe("the first screen's dialogs", () => {
  it.each([
    ["./machines/AddComputerEntry.tsx", "./AddComputer"],
    ["./assistant/Readiness.tsx", "../machines/AddComputer"],
    ["./assistant/Persona.tsx", "./LookUpload"],
    ["./chat/ModelPill.tsx", "./SessionModelPanel"],
  ])("%s loads %s on demand", (file, dialog) => {
    const source = read(file);
    expect(staticImports(source)).not.toContain(dialog);
    expect(source).toContain(`import("${dialog}")`);
  });

  it("the dynamic imports resolve to the components drawn", async () => {
    const [add, look, panel] = await Promise.all([import("./machines/AddComputer"), import("./assistant/LookUpload"), import("./chat/SessionModelPanel")]);
    for (const c of [add.AddComputer, look.LookUploadDialog, panel.SessionModelPanel]) expect(typeof c).toBe("function");
  }, 60_000);
});
