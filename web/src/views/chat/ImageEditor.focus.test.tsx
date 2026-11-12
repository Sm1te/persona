// @vitest-environment jsdom
// The markup editor and the image viewer are modal: the focus moves in when they open, Tab and Shift+Tab stay among
// their controls, and Esc puts it back on the button that opened them (the composer's, the viewer's "Edit", a thumbnail).
import { act, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
// jsdom has no layout (every control would count as hidden), no ResizeObserver, no matchMedia and does not decode images
vi.hoisted(() => {
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
  window.matchMedia ??= ((query: string) => ({ matches: false, media: query, onchange: null, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false })) as typeof window.matchMedia;
  HTMLImageElement.prototype.decode ??= () => Promise.resolve();
  // jsdom's Blob has no arrayBuffer() (imagePayload reads the picture with it when it is added to the composer)
  Blob.prototype.arrayBuffer ??= function (this: Blob) {
    return new Promise<ArrayBuffer>((ok, fail) => {
      const r = new FileReader();
      r.onload = () => ok(r.result as ArrayBuffer);
      r.onerror = () => fail(r.error);
      r.readAsArrayBuffer(this);
    });
  };
});
import { t } from "../../i18n";
import { ImageEditor } from "./ImageEditor";
import { ImageViewer } from "./ImageViewer";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const SRC = "data:image/png;base64,iVBORw0KGgo=";
let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 5; i++) await act(async () => { await Promise.resolve(); });
};
/**
 * Waits inside act until `check` passes (5 s at most): a lazy chunk's import can take a while on a busy machine, and
 * it has to finish inside an act for React to draw what it suspended on.
 */
const until = async (check: () => void) => {
  for (let i = 0; i < 500; i++) {
    await act(async () => { await new Promise((ok) => setTimeout(ok, 10)); });
    try {
      return check();
    } catch {
      // not yet
    }
  }
  check();
};
const button = (name: string) => [...document.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.getAttribute("aria-label") === name || b.textContent?.trim() === name);
const dialog = (label: string) => document.querySelector<HTMLElement>(`[role="dialog"][aria-label^="${label}"]`)!;
const press = (key: string, shiftKey = false) => {
  const target = (document.activeElement ?? document.body) as HTMLElement;
  act(() => { target.dispatchEvent(new KeyboardEvent("keydown", { key, shiftKey, bubbles: true, cancelable: true })); });
};

beforeEach(() => {
  vi.spyOn(Element.prototype, "getClientRects").mockReturnValue([{}] as unknown as DOMRectList);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.restoreAllMocks();
});

/** A page with the button that opens `modal` and closes it again through the modal's own close callback. */
function Opener({ modal }: { modal: (close: () => void) => React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>open</button>
      {open && modal(() => setOpen(false))}
    </>
  );
}

const open = async () => {
  const opener = button("open")!;
  opener.focus();
  act(() => opener.click());
  await flush();
  return opener;
};

describe("the markup editor", () => {
  it("takes the focus, keeps Tab inside and gives it back to its opener on Esc", async () => {
    act(() => root.render(<Opener modal={(close) => <ImageEditor src={SRC} onDone={close} onCancel={close} />} />));
    const opener = await open();
    const editor = dialog(t("chat-imageeditor.title"));
    expect(document.activeElement).toBe(editor);

    const cancel = button(t("common.cancel"))!;
    const crop = button(t("chat-imageeditor.crop"))!;
    expect(button(t("chat-imageeditor.done"))!.disabled).toBe(false);
    // from the dialog itself Tab lands on the first control, Shift+Tab on the last (undo / redo / clear are disabled:
    // nothing drawn)
    press("Tab");
    expect(document.activeElement).toBe(cancel);
    press("Tab", true);
    expect(document.activeElement).toBe(crop);
    press("Tab");
    expect(document.activeElement).toBe(cancel);

    press("Escape");
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });
});

describe("the markup editor's names", () => {
  it("keeps its own name, and names every button that shows only an icon, Aspect ratio with the shape chosen", async () => {
    // no room for the words anywhere: every row drops to its icons
    vi.spyOn(HTMLElement.prototype, "scrollWidth", "get").mockReturnValue(500);
    vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(300);
    act(() => root.render(<ImageEditor src={SRC} onDone={() => {}} onCancel={() => {}} />));
    await flush();
    // Undo and Redo took the title's place in the bar; the dialog keeps it as its name
    expect(document.querySelector('[role="dialog"]')!.getAttribute("aria-label")).toBe(t("chat-imageeditor.title"));
    const iconOnly = () => {
      const list = [...document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')].filter((b) => !b.textContent!.trim());
      for (const b of list) {
        expect(b.getAttribute("aria-label"), b.outerHTML).toBeTruthy();
        expect(b.getAttribute("title"), b.outerHTML).toBe(b.getAttribute("aria-label"));
      }
      return list.map((b) => b.getAttribute("aria-label"));
    };
    expect(iconOnly()).toEqual(expect.arrayContaining([t("chat-imageeditor.box"), t("chat-imageeditor.pen"), t("chat-imageeditor.crop"), t("chat-imageeditor.undo")]));
    act(() => button(t("chat-imageeditor.crop"))!.click());
    await flush();
    const shape = t("chat-imageeditor.named_value", { label: t("chat-imageeditor.aspect_ratio"), value: t("chat-imageeditor.shape_free") });
    expect(iconOnly()).toEqual(expect.arrayContaining([t("chat-imageeditor.rotate"), shape]));
  });
});

describe("the image viewer", () => {
  const images = [
    { id: "a", src: SRC, label: "a.png" },
    { id: "b", src: SRC, label: "b.png" },
  ];

  it("takes the focus, keeps Tab inside and gives it back to the thumbnail that opened it on Esc", async () => {
    act(() => root.render(<Opener modal={(close) => <ImageViewer images={images} start={0} onClose={close} />} />));
    const opener = await open();
    const viewer = dialog("a.png");
    expect(document.activeElement).toBe(viewer);

    const close = button(t("chat-imageviewer.close"))!;
    press("Tab");
    expect(document.activeElement).toBe(close);
    press("Tab", true);
    expect(viewer.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(close);

    press("Escape");
    await flush();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(opener);
  });

  it("hands the focus to 编辑's editor and gets it back on its 编辑 button when the editor is cancelled", async () => {
    act(() => root.render(<Opener modal={(close) => <ImageViewer images={images} start={0} onClose={close} attach={() => true} />} />));
    const opener = await open();
    const edit = button(t("chat-imageviewer.edit"))!;
    edit.focus();
    // the viewer's editor is its own chunk (lib/lazy-page.ts): its first open waits for the import
    await act(async () => edit.click());
    await until(() => expect(dialog(t("chat-imageeditor.title"))).not.toBeNull());
    await flush();
    expect(document.activeElement).toBe(dialog(t("chat-imageeditor.title")));

    // Esc closes only the editor; the viewer stays and its "Edit" button has the focus again
    press("Escape");
    await flush();
    expect(document.querySelector(`[role="dialog"][aria-label^="${t("chat-imageeditor.title")}"]`)).toBeNull();
    expect(document.activeElement).toBe(edit);

    press("Escape");
    await flush();
    expect(document.activeElement).toBe(opener);
  });

  it("leaves the focus in the composer when an image is added to it", async () => {
    // attaching sends a draft the composer answers by focusing its input; closing the viewer must not take that back
    const input = document.createElement("input");
    document.body.append(input);
    const attach = vi.fn(() => {
      input.focus();
      return true;
    });
    act(() => root.render(<Opener modal={(close) => <ImageViewer images={images} start={0} onClose={close} attach={attach} />} />));
    await open();
    act(() => button(t("chat-imageviewer.edit"))!.click());
    await flush();
    act(() => button(t("chat-imageviewer.add_to_composer"))!.click());
    await flush();
    expect(attach).toHaveBeenCalledTimes(1);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(document.activeElement).toBe(input);
    input.remove();
  });
});
