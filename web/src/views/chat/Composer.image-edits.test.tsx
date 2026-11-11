// @vitest-environment jsdom
// An edited picture keeps its source, edits and history beside it in the composer (#4991, iOS
// ImageEditsTests.anEditedPhotoKeepsItsEditsUntilItGoes): its tile opens the editor where it stopped, on the picture as
// it came; removing or sending it frees the source with it. The editor itself is a stub here (it draws on a canvas).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { sendDraft, type DraftImage } from "../../lib/drafts";
import { noEdits, noHistory, record, type EditState } from "../../lib/image-edit";
import { Composer, type Outgoing } from "./Composer";
import { t } from "../../i18n";

// what the composer handed the editor last time it opened, and a way to finish it as the editor would
const editor = vi.hoisted(() => ({ props: null as null | { src: string; start?: EditState; onDone: (img: DraftImage, state: EditState) => void; onCancel: () => void } }));
vi.mock("./ImageEditor", () => ({
  ImageEditor: (p: NonNullable<typeof editor.props>) => {
    editor.props = p;
    return null;
  },
}));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let urls = 0;
const revoke = vi.fn();
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  urls = 0;
  editor.props = null;
  revoke.mockClear();
  URL.createObjectURL = vi.fn(() => `blob:test/${++urls}`);
  URL.revokeObjectURL = revoke;
  localStorage.clear();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
});

/** Waits inside act until `check` passes: the editor is a lazy chunk. */
const until = async (check: () => void) => {
  for (let i = 0; i < 200; i++) {
    await act(async () => {
      await new Promise((ok) => setTimeout(ok, 5));
    });
    try {
      return check();
    } catch {
      // not yet
    }
  }
  check();
};

const tile = (name: string) => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("ui-attachmenttray.edit", { name })}"]`)!;
const open = async (name: string) => {
  editor.props = null;
  act(() => tile(name).click());
  await until(() => expect(editor.props).not.toBeNull());
  return editor.props!;
};
const turned = (turns: number, before: EditState = { edits: noEdits(), history: noHistory() }): EditState => ({
  edits: { ...noEdits(), turns },
  history: record(before.history, before.edits),
});
const result = (data: string): DraftImage => ({ name: "image.png", mime: "image/png", data: btoa(data) });

describe("an edited picture in the composer", () => {
  it("opens again where the editor stopped, on the picture as it came, until it is removed", async () => {
    act(() => root.render(<Composer draftKey="k" placeholder="" onSend={async () => true} />));
    act(() => void sendDraft("k", { type: "image", image: { name: "a.png", mime: "image/png", data: btoa("source") } }));
    expect(tile("a.png").querySelector("img")!.getAttribute("src")).toBe("blob:test/1");

    // first edit: from the picture itself, nothing to carry on
    let e = await open("a.png");
    expect(e.src).toBe("blob:test/1");
    expect(e.start).toBeUndefined();
    const first = turned(1);
    act(() => e.onDone(result("turned once"), first));
    expect(tile("image.png").querySelector("img")!.getAttribute("src")).toBe("blob:test/2");
    expect(revoke).not.toHaveBeenCalled(); // the source stays: the edit is drawn from it

    // again: the same source, the edits and their history as they were left
    e = await open("image.png");
    expect(e.src).toBe("blob:test/1");
    expect(e.start).toMatchObject(first);
    act(() => e.onDone(result("turned twice"), turned(2, first)));
    expect(revoke.mock.calls.map(([u]) => u)).toEqual(["blob:test/2"]); // the earlier result goes, the source stays
    e = await open("image.png");
    expect(e.start?.edits.turns).toBe(2);
    expect(e.start?.history.past).toHaveLength(2);

    // cancelled: nothing changes
    act(() => e.onCancel());
    expect(tile("image.png").querySelector("img")!.getAttribute("src")).toBe("blob:test/3");

    // removed: the picture and its source both go
    revoke.mockClear();
    act(() => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("ui-attachmenttray.remove", { name: "image.png" })}"]`)!.click());
    expect(revoke.mock.calls.map(([u]) => u).sort()).toEqual(["blob:test/1", "blob:test/3"]);
  });

  it("drops the edits with the picture once it is sent", async () => {
    const sent: Outgoing[] = [];
    act(() =>
      root.render(
        <Composer
          draftKey="s"
          placeholder=""
          onSend={async (m) => {
            sent.push(m);
            return true;
          }}
        />,
      ),
    );
    act(() => void sendDraft("s", { type: "image", image: { name: "a.png", mime: "image/png", data: btoa("source") } }));
    const e = await open("a.png");
    act(() => e.onDone(result("edited"), turned(3)));
    revoke.mockClear();
    await act(async () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-composer.send")}"]`)!.click());
    expect(sent).toHaveLength(1);
    expect(sent[0].images).toEqual([{ mime: "image/png", data: btoa("edited") }]); // the result goes, not the source
    expect(revoke.mock.calls.map(([u]) => u).sort()).toEqual(["blob:test/1", "blob:test/2"]);
  });

  it("carries on from the viewer's edit, and leaves the chat's own picture alone", async () => {
    act(() => root.render(<Composer draftKey="v" placeholder="" onSend={async () => true} />));
    const fromChat = "data:image/png;base64,Y2hhdA==";
    const state = turned(1);
    act(() => void sendDraft("v", { type: "image", image: result("from the viewer"), edited: { source: fromChat, ...state } }));
    const e = await open("image.png");
    expect(e.src).toBe(fromChat);
    expect(e.start).toMatchObject(state);
    act(() => e.onCancel());
    act(() => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("ui-attachmenttray.remove", { name: "image.png" })}"]`)!.click());
    expect(revoke.mock.calls.map(([u]) => u)).toEqual(["blob:test/1"]);
  });
});
