// @vitest-environment jsdom
// Pictures put in the box and never sent free their object URLs when the box goes away or shows another
// conversation (#2378), including ones still being read or a send that fails after the box moved on.
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { addQuote, getQuotes, sendDraft, setQuotes } from "../../lib/drafts";
import { toast } from "../../ui/atoms";
import { Composer, type Outgoing } from "./Composer";
import { t } from "../../i18n";

// imagePayload decodes on a canvas, which jsdom has not: each read waits on a promise the test settles
const reads: ((p: { mime: string; data: string }) => void)[] = [];
// a file upload waits on a promise the test settles too, with the path the machine stored it at
const uploads: ((path: string) => void)[] = [];
vi.mock("../../lib/attachments", async (orig) => ({
  ...(await orig<typeof import("../../lib/attachments")>()),
  imagePayload: () => new Promise<{ mime: string; data: string }>((resolve) => reads.push(resolve)),
  sendFile: () => new Promise<string>((resolve) => uploads.push(resolve)),
}));

vi.mock("../../ui/atoms", async (orig) => ({ ...(await orig<typeof import("../../ui/atoms")>()), toast: vi.fn() }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let urls = 0;
const create = vi.fn(() => `blob:test/${++urls}`);
const revoke = vi.fn();
let host: HTMLDivElement;
let root: Root;

beforeEach(() => {
  urls = 0;
  reads.length = 0;
  uploads.length = 0;
  create.mockClear();
  revoke.mockClear();
  URL.createObjectURL = create;
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

const render = (draftKey: string, onSend: (m: Outgoing) => Promise<boolean> = async () => true) =>
  act(() => root.render(<Composer draftKey={draftKey} placeholder="" onSend={onSend} />));

// an edited picture from the chat: no canvas on this path
const putImage = (draftKey: string) =>
  act(() => {
    sendDraft(draftKey, { type: "image", image: { name: "a.png", mime: "image/png", data: btoa("png") } });
  });

const pickFile = (file: File) =>
  act(() => {
    const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
    Object.defineProperty(input, "files", { value: [file], configurable: true });
    input.dispatchEvent(new Event("change", { bubbles: true }));
  });

const trayImages = () => host.querySelectorAll("img").length;

describe("double Esc recalls the last message", () => {
  const recall = vi.fn();
  const mount = (props: Partial<React.ComponentProps<typeof Composer>> = {}) => act(() => root.render(
    <Composer draftKey="recall" placeholder="" onSend={async () => true} onEditLastMessage={recall} {...props} />,
  ));
  const key = (name = "Escape", extra: KeyboardEventInit = {}) => act(() => {
    host.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: name, bubbles: true, cancelable: true, ...extra }));
  });
  beforeEach(() => recall.mockClear());

  it("requires two presses within 500 ms and ignores held-key repeats", () => {
    let now = 0;
    const clock = vi.spyOn(performance, "now").mockImplementation(() => now);
    try {
      mount(); key();
      expect(recall).not.toHaveBeenCalled();
      now = 501; key();
      expect(recall).not.toHaveBeenCalled();
      now = 600; key();
      expect(recall).toHaveBeenCalledTimes(1);
      key("Escape", { repeat: true });
      expect(recall).toHaveBeenCalledTimes(1);
    } finally { clock.mockRestore(); }
  });

  it.each([
    { working: true }, { needsYou: true }, { disabled: true }, { onEditLastMessage: undefined },
    { blocked: { label: "Not ready" } },
  ])("does not recall while unavailable: %j", (props) => {
    mount(props); key(); key();
    expect(recall).not.toHaveBeenCalled();
  });

  it("keeps draft text, quotes and attachments", () => {
    mount();
    act(() => sendDraft("recall", { type: "replace", text: "Unsent draft" }));
    key(); key();
    expect(recall).not.toHaveBeenCalled();
    expect(host.querySelector("textarea")!.value).toBe("Unsent draft");
    act(() => sendDraft("recall", { type: "replace", text: "" }));
    act(() => addQuote("recall", { text: "Unsent quote" }));
    key(); key();
    expect(recall).not.toHaveBeenCalled();
    act(() => setQuotes("recall", []));
    putImage("recall"); key(); key();
    expect(recall).not.toHaveBeenCalled();
    expect(trayImages()).toBe(1);
  });

  it("does not count Escape used by IME, modifiers, menus or another key", () => {
    mount();
    for (const extra of [{ isComposing: true }, { ctrlKey: true }, { repeat: true }]) {
      key("Escape", extra); key();
      expect(recall).not.toHaveBeenCalled();
      key("ArrowLeft");
    }
    key(); key("ArrowLeft"); key();
    expect(recall).not.toHaveBeenCalled();
    act(() => sendDraft("recall", { type: "replace", text: "/" }));
    key();
    act(() => sendDraft("recall", { type: "replace", text: "" })); key();
    expect(recall).not.toHaveBeenCalled();
  });

  it("clears the first press when focus or the conversation changes", () => {
    mount(); key();
    act(() => { host.querySelector("textarea")!.focus(); host.querySelector("textarea")!.blur(); }); key();
    expect(recall).not.toHaveBeenCalled();
    mount({ draftKey: "another" }); key();
    expect(recall).not.toHaveBeenCalled();
  });

  it("leaves Escape to an open dialog or menu", () => {
    mount();
    for (const role of ["dialog", "menu"]) {
      const overlay = document.createElement("div");
      overlay.setAttribute("role", role);
      host.append(overlay);
      key(); key();
      expect(recall).not.toHaveBeenCalled();
      overlay.remove();
    }
  });

  it("receives the recalled draft while fetching but sends only after completion", async () => {
    const send = vi.fn(async () => true);
    mount({ loadingDraft: true, onSend: send });
    act(() => sendDraft("recall", { type: "replace", text: "Restored prompt" }));
    expect(host.querySelector("textarea")!.readOnly).toBe(true);
    expect(host.querySelector("[inert]")).not.toBeNull();
    expect(host.querySelector("textarea")!.value).toBe("Restored prompt");
    key("Enter");
    expect(send).not.toHaveBeenCalled();
    mount({ loadingDraft: false, onSend: send });
    expect(host.querySelector("[inert]")).toBeNull();
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    expect(send).toHaveBeenCalledWith({ text: "Restored prompt", images: [] });
  });
});

describe("Composer image object URLs", () => {
  it("are freed when the box unmounts", () => {
    render("a");
    putImage("a");
    expect(create).toHaveBeenCalledTimes(1);
    expect(revoke).not.toHaveBeenCalled();
    act(() => root.unmount());
    expect(revoke).toHaveBeenCalledWith("blob:test/1");
    root = createRoot(host); // afterEach unmounts again
  });

  it("are freed when the box switches to another conversation", () => {
    render("a");
    putImage("a");
    render("b");
    expect(revoke).toHaveBeenCalledWith("blob:test/1");
    expect(trayImages()).toBe(0);
  });

  it("are freed, not put back into the other conversation, when a send fails after the switch", async () => {
    let finish!: (ok: boolean) => void;
    render("a", () => new Promise<boolean>((resolve) => (finish = resolve)));
    putImage("a");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    render("b", async () => true);
    expect(revoke).not.toHaveBeenCalled(); // the picture is in flight, not in the box
    await act(async () => finish(false));
    expect(revoke).toHaveBeenCalledWith("blob:test/1");
    expect(trayImages()).toBe(0);
  });

  it("are put back when a send fails in the same conversation", async () => {
    render("a", async () => false);
    putImage("a");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    expect(revoke).not.toHaveBeenCalled();
    expect(trayImages()).toBe(1);
  });

  it("are freed for a picked file whose read finishes after the switch", async () => {
    render("a");
    await pickFile(new File(["png"], "b.png", { type: "image/png" }));
    render("b");
    await act(async () => reads[0]({ mime: "image/png", data: btoa("png") }));
    expect(create).toHaveBeenCalledTimes(1);
    expect(revoke).toHaveBeenCalledWith("blob:test/1");
    expect(trayImages()).toBe(0);
  });

  it("are freed for a picked file whose read finishes after the box unmounted", async () => {
    render("a");
    await pickFile(new File(["png"], "b.png", { type: "image/png" }));
    act(() => root.unmount());
    await act(async () => reads[0]({ mime: "image/png", data: btoa("png") }));
    expect(revoke).toHaveBeenCalledWith("blob:test/1");
    root = createRoot(host);
  });
});

// A file sent to the machine puts its path into the draft of the conversation it was picked in, not into the one
// the box shows when the upload finishes.
describe("Composer file upload that finishes after the box moved on", () => {
  const box = () => host.querySelector("textarea")!;
  const renderOn = (draftKey: string) => act(() => root.render(<Composer draftKey={draftKey} machineId="m1" placeholder="" onSend={async () => true} />));
  const spinning = () => !host.querySelector('button[aria-label="添加图片或文件"] svg');
  const pickNotes = async () => {
    await pickFile(new File(["notes"], "notes.txt", { type: "text/plain" }));
    expect(uploads).toHaveLength(1);
  };

  it("goes into its own conversation's draft, not into the one the box shows now", async () => {
    renderOn("a");
    await pickNotes();
    expect(spinning()).toBe(true);
    renderOn("b");
    expect(spinning()).toBe(false); // b has nothing uploading
    await act(async () => uploads[0]("/tmp/notes.txt"));
    expect(box().value).toBe("");
    expect(localStorage.getItem("draft:a")).toBe("/tmp/notes.txt");
    renderOn("a");
    expect(box().value).toBe("/tmp/notes.txt");
  });

  it("shows its spinner again when the box comes back to its conversation before it finishes", async () => {
    renderOn("a");
    await pickNotes();
    renderOn("b");
    renderOn("a");
    expect(spinning()).toBe(true);
    await act(async () => uploads[0]("/tmp/notes.txt"));
    expect(spinning()).toBe(false);
    expect(box().value).toBe("/tmp/notes.txt");
  });

  it("is kept for the conversation when the box went away before it finished", async () => {
    renderOn("a");
    await pickNotes();
    act(() => root.unmount());
    root = createRoot(host);
    await act(async () => uploads[0]("/tmp/notes.txt"));
    expect(localStorage.getItem("draft:a")).toBe("/tmp/notes.txt");
    renderOn("a");
    expect(box().value).toBe("/tmp/notes.txt");
  });

  it("goes under what the conversation's draft already holds", async () => {
    localStorage.setItem("draft:a", "see");
    renderOn("a");
    await pickNotes();
    renderOn("b");
    await act(async () => uploads[0]("/tmp/my notes.txt"));
    expect(localStorage.getItem("draft:a")).toBe("see\n'/tmp/my notes.txt'");
  });
});

// At most 4 pictures a message, as on iOS / Android.
describe("Composer image cap", () => {
  it("turns away a fifth picture from the chat", () => {
    render("a");
    for (let i = 0; i < 5; i++) putImage("a");
    expect(trayImages()).toBe(4);
  });

  it("reads only the pictures that fit when several are picked", async () => {
    render("a");
    putImage("a");
    act(() => {
      const input = host.querySelector<HTMLInputElement>('input[type="file"]')!;
      const files = Array.from({ length: 5 }, (_, i) => new File(["x"], `p${i}.png`, { type: "image/png" }));
      Object.defineProperty(input, "files", { value: files, configurable: true });
      input.dispatchEvent(new Event("change", { bubbles: true }));
    });
    // one read at a time: settle each so the next starts
    for (let i = 0; i < 3; i++) {
      await act(async () => reads[i]?.({ mime: "image/png", data: btoa("png") }));
    }
    expect(reads.length).toBe(3);
    expect(trayImages()).toBe(4);
  });

  it("holds when five pictures come from the chat in the same tick", () => {
    render("a");
    act(() => {
      for (let i = 0; i < 5; i++) sendDraft("a", { type: "image", image: { name: "a.png", mime: "image/png", data: btoa("png") } });
    });
    expect(trayImages()).toBe(4);
    expect(create).toHaveBeenCalledTimes(4);
  });

  it("counts pictures still being read when a second pick comes before they settle", async () => {
    render("a");
    putImage("a");
    putImage("a");
    putImage("a");
    await pickFile(new File(["x"], "p1.png", { type: "image/png" }));
    await pickFile(new File(["x"], "p2.png", { type: "image/png" }));
    expect(reads.length).toBe(1); // the second is turned away while the first is read
    await act(async () => reads[0]({ mime: "image/png", data: btoa("png") }));
    expect(trayImages()).toBe(4);
  });
});

// Claude's suggestion as a pill over the box (iOS SuggestionPill): a tap fills it in, the arrow sends it.
describe("Composer suggestion", () => {
  const show = (o: { working?: boolean; needsYou?: boolean; onSend?: (m: Outgoing) => Promise<boolean> } = {}) =>
    act(() => root.render(<Composer draftKey="s" placeholder="" suggestion="跑一下测试" working={o.working} needsYou={o.needsYou} onSend={o.onSend ?? (async () => true)} />));
  const pill = () => host.querySelector('[data-testid="composer-suggestion"]');

  it("fills the box on a tap and goes away", () => {
    show();
    act(() => pill()!.querySelector("button")!.click());
    expect(host.querySelector("textarea")!.value).toBe("跑一下测试");
    expect(pill()).toBeNull();
  });

  it("sends it as it is", async () => {
    const onSend = vi.fn(async () => true);
    show({ onSend });
    await act(async () => pill()!.querySelectorAll("button")[1].click());
    expect(onSend).toHaveBeenCalledWith({ text: "跑一下测试", images: [] });
  });

  it("sends once while the send is on its way, and can come back after the agent has worked", async () => {
    const onSend = vi.fn(() => new Promise<boolean>(() => {}));
    show({ onSend });
    await act(async () => pill()!.querySelectorAll("button")[1].click());
    expect(pill()).toBeNull();
    expect(onSend).toHaveBeenCalledTimes(1);
    show({ onSend, working: true });
    show({ onSend });
    expect(pill()).not.toBeNull();
  });

  it("is not there while the agent works or something is in the box", () => {
    show({ working: true });
    expect(pill()).toBeNull();
    show({ needsYou: true }); // waiting on a card: the arrow must not send into it
    expect(pill()).toBeNull();
    show();
    putImage("s");
    expect(pill()).toBeNull();
  });
});

describe("Composer words of a send that fails after the box moved on", () => {
  const box = () => host.querySelector("textarea")!;
  const typeIn = (draftKey: string, text: string) =>
    act(() => {
      sendDraft(draftKey, { type: "replace", text });
    });
  const sendSlow = (draftKey: string) => {
    let finish!: (ok: boolean) => void;
    render(draftKey, () => new Promise<boolean>((resolve) => (finish = resolve)));
    return (ok: boolean) => act(async () => finish(ok));
  };

  it("go back to their own conversation's draft, not into the one the box shows now", async () => {
    const finish = sendSlow("a");
    typeIn("a", "hello a");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    expect(box().value).toBe("");
    render("b", async () => true);
    await finish(false);
    expect(box().value).toBe("");
    render("a");
    expect(box().value).toBe("hello a");
  });

  it("are kept for the conversation when the box went away before the answer", async () => {
    const finish = sendSlow("a");
    typeIn("a", "hello a");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    act(() => root.unmount());
    root = createRoot(host);
    await finish(false);
    expect(localStorage.getItem("draft:a")).toBe("hello a");
    render("a");
    expect(box().value).toBe("hello a");
  });

  it("go under what the conversation's open box has by then", async () => {
    const finish = sendSlow("a");
    typeIn("a", "hello a");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    // the conversation opened again in a new box (a chat page mounts one per conversation) while the send was out
    act(() => root.unmount());
    root = createRoot(host);
    render("a");
    typeIn("a", "more");
    await finish(false);
    expect(box().value).toBe("hello a\nmore");
  });

  it("come back into the conversation's new box even while it cannot send", async () => {
    const finish = sendSlow("a");
    typeIn("a", "hello a");
    act(() => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    act(() => root.unmount());
    root = createRoot(host);
    act(() => root.render(<Composer draftKey="a" placeholder="" onSend={async () => true} disabled />));
    await finish(false);
    expect(box().value).toBe("hello a");
  });
});

describe("Composer draft typed just before leaving (#4591)", () => {
  const typeInto = (text: string) =>
    act(() => {
      const el = host.querySelector("textarea")!;
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(el, text);
      el.dispatchEvent(new Event("input", { bubbles: true }));
    });

  it("is saved when the box goes away before the debounce", () => {
    render("a");
    typeInto("still typing");
    act(() => root.unmount());
    root = createRoot(host);
    expect(localStorage.getItem("draft:a")).toBe("still typing");
  });

  it("is saved for its own conversation when the box moves to another", () => {
    render("a");
    typeInto("for a");
    render("b");
    expect(localStorage.getItem("draft:a")).toBe("for a");
    expect(localStorage.getItem("draft:b")).toBeNull();
    expect(host.querySelector("textarea")!.value).toBe("");
  });

  it("is saved when the page is hidden", () => {
    render("a");
    typeInto("closing the tab");
    act(() => {
      dispatchEvent(new Event("pagehide"));
    });
    expect(localStorage.getItem("draft:a")).toBe("closing the tab");
  });

  it("is not written back after a send emptied the box", async () => {
    render("a", () => new Promise<boolean>(() => {}));
    typeInto("send me");
    await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="发送"]')!.click());
    act(() => root.unmount());
    root = createRoot(host);
    expect(localStorage.getItem("draft:a")).toBeNull();
  });
});

describe("Composer Edit over unsent words (#2450)", () => {
  const box = () => host.querySelector("textarea")!;
  const typeInto = (text: string) =>
    act(() => {
      const el = box();
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(el, text);
      el.dispatchEvent(new Event("input", { bubbles: true }));
    });
  const edit = (text: string) => act(() => void sendDraft("e", { type: "replace", text }));
  const undoToast = () => vi.mocked(toast).mock.calls.find(([text]) => text === t("chat-composer.replaced"));
  beforeEach(() => {
    vi.mocked(toast).mockClear();
    setQuotes("e", []);
  });
  afterEach(() => vi.restoreAllMocks());

  it("replaces the draft without asking, and Undo brings back the words and the quotes", () => {
    const ask = vi.spyOn(window, "confirm");
    render("e");
    typeInto("half a thought");
    act(() => void addQuote("e", { text: "the quoted reply", from: "Claude" }, false));
    const quotes = getQuotes("e").list;
    edit("the sent message");
    expect(ask).not.toHaveBeenCalled();
    expect(box().value).toBe("the sent message");
    expect(getQuotes("e").list).toEqual([]);
    const [, tone, action] = undoToast()!;
    expect(tone).toBe("ok");
    expect(action?.label).toBe(t("chat-composer.undo"));
    act(() => action!.run());
    expect(box().value).toBe("half a thought");
    expect(getQuotes("e").list).toEqual(quotes);
  });

  it("says nothing when the box was empty or held the same words", () => {
    render("e");
    edit("the sent message");
    expect(box().value).toBe("the sent message");
    edit("the sent message ");
    expect(undoToast()).toBeUndefined();
  });

  it.each([
    ["a quote", { text: "the quoted reply", from: "Claude" }],
    ["a comment card", { text: "fetchAll", comment: { body: "rename fetchAll before merging" } }],
  ])("offers Undo for %s on the bar when the box is empty", (_, quote) => {
    render("e");
    act(() => void addQuote("e", quote, false));
    const quotes = getQuotes("e").list;
    edit("the sent message");
    expect(getQuotes("e").list).toEqual([]);
    act(() => undoToast()![2]!.run());
    expect(getQuotes("e").list).toEqual(quotes);
    expect(box().value).toBe("");
  });
});

// ChatView interrupts a working agent on an Escape that reaches the window: an Escape the box uses for itself
// (closing the / menu, cancelling an IME candidate) must stop there.
describe("Composer Escape the box uses itself", () => {
  const seen = vi.fn();
  const onWindow = (e: KeyboardEvent) => {
    if (e.key === "Escape") seen();
  };
  beforeEach(() => {
    seen.mockClear();
    addEventListener("keydown", onWindow);
  });
  afterEach(() => removeEventListener("keydown", onWindow));

  const box = () => host.querySelector("textarea")!;
  const typeInto = (text: string) =>
    act(() => {
      Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(box(), text);
      box().dispatchEvent(new Event("input", { bubbles: true }));
    });
  const escape = (init: KeyboardEventInit = {}) =>
    act(() => {
      box().dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true, ...init }));
    });
  const menuOpen = () => host.querySelector(".bottom-full") !== null;

  it("closes the / menu without reaching the window", () => {
    render("a");
    typeInto("/");
    expect(menuOpen()).toBe(true);
    escape();
    expect(menuOpen()).toBe(false);
    expect(seen).not.toHaveBeenCalled();
  });

  it("does not reach the window while an IME composes", () => {
    render("a");
    typeInto("ni");
    escape({ isComposing: true });
    escape({ keyCode: 229 });
    expect(seen).not.toHaveBeenCalled();
  });

  it("still reaches the window from an empty box", () => {
    render("a");
    escape();
    expect(seen).toHaveBeenCalledTimes(1);
  });
});

describe("sending while another send is in flight", () => {
  const sendButton = () => host.querySelector<HTMLButtonElement>(`button[aria-label="${t("chat-composer.send")}"]`)!;
  const type = (key: string, text: string) => act(() => void sendDraft(key, { type: "replace", text }));
  const click = (el: Element) => act(() => el.dispatchEvent(new MouseEvent("click", { bubbles: true })));

  it("takes the next message and keeps Stop while the first is on its way", async () => {
    const onSend = vi.fn(() => new Promise<boolean>(() => {}));
    const onStop = vi.fn();
    act(() => root.render(<Composer draftKey="busy" placeholder="" working onStop={onStop} onSend={onSend} />));
    type("busy", "one");
    await act(async () => click(sendButton()));
    expect(onSend).toHaveBeenCalledTimes(1);
    expect(host.querySelector(`button[aria-label="${t("chat-composer.interrupt")}"]`)).not.toBeNull();
    type("busy", "two");
    expect(sendButton().disabled).toBe(false);
    await act(async () => click(sendButton()));
    expect(onSend).toHaveBeenCalledTimes(2);
  });

  describe("focus after a send", () => {
    let fine = true;
    beforeEach(() => {
      vi.stubGlobal("matchMedia", (q: string) => ({ get matches() { return q.includes("fine") && fine; }, addEventListener() {}, removeEventListener() {} }));
    });
    afterEach(() => vi.unstubAllGlobals());

    const send = async () => {
      localStorage.setItem("draft:focus", "hello"); // not a draft event: that one focuses the box itself
      act(() => root.render(<Composer draftKey="focus" placeholder="" onSend={async () => true} />));
      await act(async () => click(sendButton()));
    };

    it("asks once while Enter is held down in a blocked box", () => {
      fine = true;
      const onNudge = vi.fn();
      act(() => root.render(<Composer draftKey="held" placeholder="" blocked={{ label: "x", onNudge }} onSend={async () => true} />));
      const press = (repeat: boolean) => act(() => host.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, repeat })));
      press(false);
      press(true);
      press(true);
      expect(onNudge).toHaveBeenCalledTimes(1);
    });

    it("returns to the box with a precise pointer, from the send button", async () => {
      fine = true;
      await send();
      expect(document.activeElement).toBe(host.querySelector("textarea"));
    });

    it("stays away on a touch screen", async () => {
      fine = false;
      await send();
      expect(document.activeElement).not.toBe(host.querySelector("textarea"));
    });

    it("does not take the focus from somewhere else", async () => {
      fine = true;
      const other = document.body.appendChild(document.createElement("button"));
      localStorage.setItem("draft:focus", "hello");
      act(() => root.render(<Composer draftKey="focus" placeholder="" onSend={async () => true} />));
      other.focus();
      await act(async () => click(sendButton()));
      expect(document.activeElement).toBe(other);
      other.remove();
    });
  });
});

// #5032: an Agent's model pill sits at the right of the bottom row, before voice and send, and the card is the container
// whose width it takes its modes from
describe("the trailing slot", () => {
  it("draws what it is given right before voice and send, inside the composer container", () => {
    localStorage.setItem("draft:trailing", "hello"); // a draft: voice is the small icon, send stays
    act(() => root.render(<Composer draftKey="trailing" placeholder="" onSend={async () => true} footer={<span data-footer />} trailing={<span data-pill />} voice={{ mode: "explain", label: "voice", onClick: () => {} }} />));
    const pill = host.querySelector("[data-pill]")!;
    const row = pill.parentElement!;
    const order = [...row.children].map((c) => (c.matches("[data-pill]") ? "pill" : c.querySelector("[data-footer]") ? "footer" : c.getAttribute("data-testid") ?? c.getAttribute("aria-label") ?? c.tagName));
    const at = order.indexOf("pill");
    expect(order[at - 1]).toBe("footer");
    expect(order.slice(at + 1)).toEqual(["composer-voice", t("chat-composer.send")]);
    expect(row.closest("[class*='@container/composer']")).not.toBeNull();
  });
});
