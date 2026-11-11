// @vitest-environment jsdom
// An open page picks up a new web build (#4331): the shell's entry script tells the build; the page reloads by itself
// only when nothing would be lost, else it says so once and reloads at the next quiet moment.
import { describe, expect, it } from "vitest";
import { CHECK_GAP_MS, hasDraft, hasOverlay, isNewBuild, mayReload, newVersionChecker, QUIET_MS, runningEntry, shellEntry, shellFingerprint, type Calm } from "./new-version";

const shell = (entry: string, css = "styles-CCCC.css") =>
  `<!doctype html><html><head><base href="/"><script type="module" crossorigin src="./assets/${entry}"></script><link rel="modulepreload" crossorigin href="./assets/react-AAAA.js"><link rel="stylesheet" href="./assets/${css}"></head><body><div id="root"></div></body></html>`;

describe("the shell's build", () => {
  it("is the entry script it loads", () => {
    expect(shellEntry(shell("index-B1.js"))).toBe("/assets/index-B1.js");
    expect(shellEntry('<script src="/x.js"></script><script type=module src="/assets/index-Z.js"></script>')).toBe("/assets/index-Z.js");
  });

  it("is unknown for a page that is not the app's (a sign-in page) or a dev server", () => {
    expect(shellEntry("<html><body><form>Sign in</form></body></html>")).toBeNull();
    expect(shellEntry('<script type="module" src="/src/main.tsx"></script>')).toBeNull();
  });

  it("is new only when both sides are known and differ", () => {
    expect(isNewBuild("/assets/index-A.js", "/assets/index-B.js")).toBe(true);
    expect(isNewBuild("/assets/index-A.js", "/assets/index-A.js")).toBe(false);
    expect(isNewBuild("/assets/index-A.js", null)).toBe(false);
    expect(isNewBuild(null, "/assets/index-B.js")).toBe(false);
  });

  it("this page runs is read from its own module script", () => {
    document.head.innerHTML = '<script type="module" crossorigin src="./assets/index-RUN.js"></script>';
    expect(runningEntry(document)).toBe("/assets/index-RUN.js");
    document.head.innerHTML = "";
  });
});

describe("whether the page may reload itself", () => {
  const calm = (o: Partial<Calm> = {}): Calm => ({ hidden: false, draft: false, overlay: false, quietMs: QUIET_MS, ...o });
  it("yes when left alone or in the background", () => {
    expect(mayReload(calm())).toBe(true);
    expect(mayReload(calm({ hidden: true, quietMs: 0 }))).toBe(true);
  });
  it("not while in use, with a draft or with a dialog open, even in the background", () => {
    expect(mayReload(calm({ quietMs: QUIET_MS - 1 }))).toBe(false);
    expect(mayReload(calm({ draft: true }))).toBe(false);
    expect(mayReload(calm({ draft: true, hidden: true }))).toBe(false);
    expect(mayReload(calm({ overlay: true, hidden: true }))).toBe(false);
  });
});

describe("the page's drafts and dialogs", () => {
  it("a text field with something typed is a draft; an empty one, focused or not, is none", () => {
    document.body.innerHTML = '<textarea id="c"></textarea><input id="s" type="search">';
    const c = document.getElementById("c") as HTMLTextAreaElement;
    c.focus();
    expect(hasDraft(document)).toBe(false);
    c.value = "half a sentence";
    expect(hasDraft(document)).toBe(true);
    c.value = "";
    (document.getElementById("s") as HTMLInputElement).value = "query";
    expect(hasDraft(document)).toBe(true);
    document.body.innerHTML = '<div contenteditable="true">note</div>';
    expect(hasDraft(document)).toBe(true);
    document.body.innerHTML = '<input type="checkbox" checked><input type="text" readonly value="x">';
    expect(hasDraft(document)).toBe(false);
  });

  it("a dialog or a menu counts as open", () => {
    document.body.innerHTML = "<main></main>";
    expect(hasOverlay(document)).toBe(false);
    document.body.innerHTML = '<div role="dialog" aria-modal="true"></div>';
    expect(hasOverlay(document)).toBe(true);
    document.body.innerHTML = '<div role="menu"></div>';
    expect(hasOverlay(document)).toBe(true);
    document.body.innerHTML = "";
  });
});

describe("the checker", () => {
  function setup(o: { served?: string | Error; calm?: Partial<Calm> } = {}) {
    const log: string[] = [];
    let now = 1_000_000;
    let served: string | Error = o.served ?? shell("index-A.js");
    let calm: Calm = { hidden: false, draft: false, overlay: false, quietMs: QUIET_MS, ...o.calm };
    const c = newVersionChecker({
      running: () => "/assets/index-A.js",
      fetchShell: async () => {
        log.push("fetch");
        if (served instanceof Error) throw served;
        return served;
      },
      calm: () => calm,
      reload: () => log.push("reload"),
      notify: (reloadNow) => {
        log.push("notify");
        reloadLater = reloadNow;
      },
      now: () => now,
    });
    let reloadLater: (() => void) | undefined;
    return {
      c,
      log,
      serve: (s: string | Error) => (served = s),
      setCalm: (x: Partial<Calm>) => (calm = { ...calm, ...x }),
      wait: (ms: number) => (now += ms),
      click: () => reloadLater?.(),
    };
  }

  it("does nothing while the hub serves the same build", async () => {
    const t = setup();
    await t.c.check(true);
    expect(t.log).toEqual(["fetch"]);
    expect(t.c.waiting()).toBe(false);
  });

  it("reloads at once when a new build is served and the page is idle", async () => {
    const t = setup({ served: shell("index-B.js") });
    await t.c.check(true);
    expect(t.log).toEqual(["fetch", "reload"]);
  });

  it("over a draft says so once instead, then reloads when the draft is gone and the page is quiet", async () => {
    const t = setup({ served: shell("index-B.js"), calm: { draft: true } });
    await t.c.check(true);
    t.c.tick();
    await t.c.check(true);
    expect(t.log).toEqual(["fetch", "notify"]);
    t.setCalm({ draft: false, quietMs: 1_000 });
    t.c.tick();
    expect(t.log).toEqual(["fetch", "notify"]);
    t.setCalm({ quietMs: QUIET_MS });
    t.c.tick();
    t.c.tick();
    expect(t.log).toEqual(["fetch", "notify", "reload"]);
  });

  it("notices a build that changed only its stylesheet, against the shell first served for this page's own build", async () => {
    const t = setup();
    await t.c.check(true);
    expect(t.c.waiting()).toBe(false);
    t.serve(shell("index-A.js", "styles-NEW.css"));
    await t.c.check(true);
    expect(t.log).toEqual(["fetch", "fetch", "reload"]);
    expect(shellFingerprint(shell("index-A.js"))).toBe("/assets/index-A.js /assets/react-AAAA.js /assets/styles-CCCC.css");
    expect(shellFingerprint("<html>sign in</html>")).toBe("");
  });

  it("reloads from the notice's button whenever the user wants", async () => {
    const t = setup({ served: shell("index-B.js"), calm: { overlay: true } });
    await t.c.check(true);
    t.click();
    expect(t.log).toEqual(["fetch", "notify", "reload"]);
  });

  it("asks again only after a gap unless forced, and keeps quiet when the hub can't be reached or answers with another page", async () => {
    const t = setup({ served: new Error("offline") });
    await t.c.check();
    await t.c.check();
    expect(t.log).toEqual(["fetch"]);
    t.wait(CHECK_GAP_MS);
    t.serve("<html>sign in</html>");
    await t.c.check();
    await t.c.check(true);
    expect(t.log).toEqual(["fetch", "fetch", "fetch"]);
    expect(t.c.waiting()).toBe(false);
  });
});
