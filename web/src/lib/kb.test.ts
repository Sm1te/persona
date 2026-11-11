import { t } from "../i18n";
import { describe, expect, it } from "vitest";
import {
  agentPreset,
  askPersonaText,
  contextXml,
  countLabel,
  dayTitle,
  dirsOf,
  entryFromDetail,
  factsOf,
  formatAt,
  formatSpan,
  groupEntries,
  momentFeed,
  groupResults,
  hitView,
  initial,
  kindOf,
  linkGroups,
  markParts,
  minutesKeptUntil,
  parseLogLine,
  recordingView,
  recordingClock,
  transcriptLineId,
  transcriptTarget,
  durationLabel,
  parseMessageLine,
  parseRecent,
  parseTags,
  profile,
  projectInfo,
  projectKindLabel,
  rankEntries,
  rowTags,
  shortWhen,
  statusLabel,
  tagForm,
  tagUpdate,
  timeline,
  type KBEntry,
  type KBResult,
} from "./kb";

const e = (id: string, o: Partial<KBEntry> = {}): KBEntry => ({ kind: "projects", id, name: id, relevance: 0, ...o });
const NOW = new Date(2026, 10, 2, 12, 0);

describe("kinds", () => {
  it("default to projects", () => {
    expect(kindOf(undefined)).toBe("projects");
    expect(kindOf("nope")).toBe("projects");
    expect(kindOf("people")).toBe("people");
  });
});

describe("project tagging", () => {
  it("reads kind and status from extra", () => {
    expect(projectInfo(e("a", { extra: "long · done" }))).toEqual({ kind: "long", status: "done" });
    expect(projectInfo(e("a"))).toEqual({ kind: "short", status: "active" });
    expect(projectInfo({ kind: "people", extra: "x" })).toBeNull();
    expect(projectKindLabel(e("a", { extra: "long · active" }))).toBe("长期");
    expect(projectKindLabel(e("a", { extra: "short · active" }))).toBe("短期");
    expect(projectKindLabel(e("a"))).toBeNull();
    expect(statusLabel("active")).toBeNull();
    expect(statusLabel("paused")).toBe("暂停");
  });
});

describe("groupEntries", () => {
  it("splits projects into 重点, 其他进行中, 已完成 and a folded 不是项目", () => {
    const items = [
      e("p2", { importance: "P2", relevance: 60, extra: "short · active" }),
      e("p0", { importance: "P0", relevance: 92, extra: "short · active" }),
      e("done", { importance: "P1", extra: "short · done" }),
      e("paused", { importance: "P1", extra: "short · paused" }),
      e("promo", { importance: "P3", extra: "short · ignored" }),
    ];
    const s = groupEntries("projects", items);
    expect(s.map((x) => [x.title, x.items.map((i) => i.id), !!x.fold])).toEqual([
      ["重点", ["p0", "paused"], false],
      ["其他进行中", ["p2"], false],
      ["已完成", ["done"], false],
      ["不是项目", ["promo"], true],
    ]);
  });

  it("calls the rest 进行中 when nothing is in focus, and drops empty sections", () => {
    const s = groupEntries("projects", [e("a", { importance: "P2", extra: "short · active" })]);
    expect(s.map((x) => x.title)).toEqual(["进行中"]);
  });

  it("folds the untagged people under 还没整理的", () => {
    const s = groupEntries("people", [e("x", { kind: "people" }), e("y", { kind: "people", importance: "P1" })]);
    expect(s.map((x) => [x.key, x.title, x.items.map((i) => i.id), !!x.fold])).toEqual([
      ["ranked", "按重要级和相关性", ["y"], false],
      ["rest", "还没整理的", ["x"], true],
    ]);
    expect(s[1].footer).toContain("人");
  });

  it("shows everything open when nobody is tagged yet, and one section with a tag", () => {
    expect(groupEntries("chats", [e("x", { kind: "chats" })]).map((x) => [x.title, !!x.fold])).toEqual([["全部", false]]);
    const t = groupEntries("chats", [e("x", { kind: "chats" }), e("y", { kind: "chats", importance: "P3" })], "工作");
    expect(t.map((x) => [x.title, x.items.length])).toEqual([["#工作", 2]]);
  });
});

describe("rankEntries", () => {
  it("orders by importance, relevance, then recency, stable", () => {
    const list = [
      e("none", { relevance: 99 }),
      e("p2-old", { importance: "P2", relevance: 50, updated: "2026-10-01T00:00:00Z" }),
      e("p2-new", { importance: "P2", relevance: 50, updated: "2026-10-30T00:00:00Z" }),
      e("p2-rel", { importance: "P2", relevance: 80 }),
      e("p0", { importance: "P0" }),
      e("same-a", { importance: "P3" }),
      e("same-b", { importance: "P3" }),
    ];
    expect(rankEntries(list).map((x) => x.id)).toEqual(["p0", "p2-rel", "p2-new", "p2-old", "same-a", "same-b", "none"]);
  });
});

describe("tags", () => {
  it("a row leaves out the filter tag and duplicates, at most max", () => {
    expect(rowTags({ tags: ["iOS", "小组件", "ios", " ", "#前端", "脚本"] }, undefined, 3)).toEqual(["iOS", "小组件", "前端"]);
    expect(rowTags({ tags: ["iOS", "小组件"] }, "ios")).toEqual(["小组件"]);
    expect(rowTags({ tags: null })).toEqual([]);
  });

  it("parses what was typed", () => {
    expect(parseTags(" iOS  #小组件,前端，iOS、 ")).toEqual(["iOS", "小组件", "前端"]);
    expect(parseTags("")).toEqual([]);
  });

  it("formats counts and initials", () => {
    expect(countLabel(1985)).toBe("1985");
    expect(countLabel(12000)).toBe("1万+");
    expect(initial("「设计」评审")).toBe("设");
    expect(initial("  alice")).toBe("A");
    expect(initial("😀 group")).toBe("😀");
    expect(initial("")).toBe("");
  });
});

describe("search", () => {
  it("marks every word, case-insensitive, merging runs", () => {
    expect(markParts("Weather App 天气 app", "app 天气")).toEqual([
      { text: "Weather ", hit: false },
      { text: "App", hit: true },
      { text: " ", hit: false },
      { text: "天气", hit: true },
      { text: " ", hit: false },
      { text: "app", hit: true },
    ]);
    expect(markParts("降雨条", "")).toEqual([{ text: "降雨条", hit: false }]);
    expect(markParts("", "x")).toEqual([]);
    expect(markParts("周五前", "周五 五前")).toEqual([{ text: "周五前", hit: true }]);
  });

  it("groups results by kind in the order of their best one", () => {
    const r = (kind: string, id: string): KBResult => ({ entry: e(id, { kind }), count: 1 });
    const g = groupResults([r("people", "a"), r("chats", "b"), r("people", "c")]);
    expect(g.map((x) => [x.kind, x.title, x.results.map((y) => y.entry.id)])).toEqual([
      ["people", "人", ["a", "c"]],
      ["chats", "会话", ["b"]],
    ]);
  });

  it("says where a hit is and what it says", () => {
    expect(hitView({ path: "chats/oc_team/messages/2026-10-30.md", line: "10:15 韩梅梅: 周五前要一版能装的" }, NOW)).toEqual({
      source: "聊天记录 · 10月30日 周五 10:15",
      text: "韩梅梅：周五前要一版能装的",
      day: "2026-10-30",
    });
    expect(hitView({ path: "projects/weather-app/summary.md", line: "- **现在**：小组件按小时刷新" })).toMatchObject({ source: "档案", text: "**现在**：小组件按小时刷新", day: null });
    expect(hitView({ path: "projects/x/log.md", line: "- 2026-10-30 · 飞书 · 定了" }).source).toBe("时间线");
    expect(hitView({ path: "docs/doc_1.json", line: '  "title": "设计稿 v3",' })).toMatchObject({ source: "资料", text: "设计稿 v3" });
  });

  it("parses archived messages", () => {
    expect(parseMessageLine("- 10:20 我: 可以，今晚先灰度给自己\n  明天再给你")).toEqual({ time: "10:20", sender: "我", text: "可以，今晚先灰度给自己\n明天再给你" });
    expect(parseMessageLine("每周三下午的设计评审群")).toEqual({ time: "", sender: "", text: "每周三下午的设计评审群" });
    expect(parseMessageLine("10:22 韩梅梅: 👍").sender).toBe("韩梅梅");
  });
});

describe("detail", () => {
  it("names a post's feed as archived, else 公司圈", () => {
    expect(momentFeed({ feed: "内部圈" })).toBe("内部圈");
    expect(momentFeed({ feed: " " })).toBe("公司圈");
    expect(momentFeed({})).toBe("公司圈");
  });

  it("reads the timeline newest first", () => {
    expect(parseLogLine("- 2026-10-28 11:02 · Agent · 数据层 · MacBook · 数据层复用逐小时预报")).toEqual({
      when: "2026-10-28 11:02",
      source: "Agent · 数据层 · MacBook",
      text: "数据层复用逐小时预报",
    });
    expect(parseLogLine("- 2026-10-29 18:40 · 邮件 · 设计稿 v3 发来了")).toEqual({ when: "2026-10-29 18:40", source: "邮件", text: "设计稿 v3 发来了" });
    expect(parseLogLine("2026-11-01 更新了进展")).toEqual({ when: "", source: "", text: "2026-11-01 更新了进展" });
    expect(timeline(["- 2026-10-01 · a · one", "", "- 2026-10-02 · b · two"]).map((l) => l.text)).toEqual(["two", "one"]);
    expect(timeline(undefined)).toEqual([]);
    expect(shortWhen("2026-10-30 23:16", NOW)).toBe("10-30 23:16");
    expect(shortWhen("2025-10-30 23:16", NOW)).toBe("2025-10-30 23:16");
  });

  it("takes out of the profile what the header shows", () => {
    const md = "# 天气 App\n\n给自己用的天气 App。\n重要级 P0 · 与我相关性 95 · iOS\n\n- **现在**：按小时刷新";
    expect(profile(md, "给自己用的天气 App。")).toBe("- **现在**：按小时刷新");
    expect(profile("## 照片同步\n\nHEIC 转 JPEG", "HEIC 转 JPEG")).toBe("## 照片同步");
    expect(profile("## 照片同步\n\nHEIC 转 JPEG", "HEIC 转 JPEG", "照片同步")).toBe("");
    expect(profile("## 现状\n进行中", "", "照片同步")).toBe("## 现状\n进行中");
    expect(profile("小组件", "小组件")).toBe("");
    expect(profile(undefined)).toBe("");
  });

  it("merges the loaded meta into the entry", () => {
    const d = {
      kind: "projects",
      id: "weather-app",
      path: "/x",
      meta: { name: "天气 App", importance: "P0", relevance: 92, tags: ["iOS", 3], kind: "long", status: "paused", one_line: "一句话" },
    };
    expect(entryFromDetail(undefined, d)).toEqual({
      kind: "projects",
      id: "weather-app",
      name: "天气 App",
      relevance: 92,
      importance: "P0",
      tags: ["iOS"],
      one_line: "一句话",
      extra: "long · paused",
    });
    expect(entryFromDetail(e("doc", { kind: "docs", name: "old" }), { kind: "docs", id: "doc", path: "", meta: { title: "设计稿" } }).name).toBe("设计稿");
    expect(entryFromDetail(e("x", { name: "keep" }), { kind: "projects", id: "x", path: "", meta: null }).name).toBe("keep");
  });

  it("groups links by kind in the kinds' order", () => {
    const g = linkGroups([e("a", { kind: "docs" }), e("b", { kind: "people" }), e("c", { kind: "people" }), e("d", { kind: "projects" })]);
    expect(g.map((x) => [x.title, x.items.length])).toEqual([
      ["关联的项目", 1],
      ["关联的人", 2],
      ["关联的文档", 1],
    ]);
    expect(linkGroups(null)).toEqual([]);
  });

  it("splits code directories into machine and path", () => {
    expect(dirsOf({ dirs: ["macbook:/Users/me/code/weather", "/srv/x", "server:~/y", 3, ""] })).toEqual([
      { machine: "macbook", path: "/Users/me/code/weather" },
      { machine: "", path: "/srv/x" },
      { machine: "server", path: "~/y" },
    ]);
    expect(dirsOf(undefined)).toEqual([]);
  });

  it("has facts only for docs, meetings and events", () => {
    expect(factsOf("projects", { at: "2026-10-30T14:00:00Z" })).toBeNull();
    expect(factsOf("docs", { at: "0001-01-01T00:00:00Z" })).toBeNull();
    expect(factsOf("meetings", { at: "2026-10-30T14:00:00Z", url: " https://example.com/m " })).toEqual({
      at: "2026-10-30T14:00:00Z",
      url: "https://example.com/m",
      whenLabel: "开会时间",
      openLabel: "打开会议纪要",
    });
  });

  it("formats days and times", () => {
    expect(dayTitle("2026-10-30", NOW)).toBe("10月30日 周五");
    expect(dayTitle("2025-10-30", NOW)).toBe("2025年10月30日 周四");
    expect(dayTitle("nope", NOW)).toBe("nope");
    expect(formatAt("2026-10-30T14:05:00", NOW)).toBe("10月30日 周五 14:05");
    expect(formatAt("2025-01-02T09:00:00", NOW)).toBe("2025年1月2日 09:00");
    // The minutes' span (#4713): the same dates, the end's time only on the start's day; the hub's when without them.
    expect(formatSpan("2026-10-30T14:05:00", "2026-10-30T15:10:00", "x", NOW)).toBe("10月30日 周五 14:05–15:10");
    expect(formatSpan("2026-10-30T23:30:00", "2026-10-31T00:20:00", "x", NOW)).toBe("10月30日 周五 23:30 – 10月31日 周六 00:20");
    expect(formatSpan("2025-01-02T09:00:00", "2025-01-02T10:00:00", "x", NOW)).toBe("2025年1月2日 09:00–10:00");
    expect(formatSpan(undefined, undefined, "2026-10-30 14:05–15:10", NOW)).toBe("2026-10-30 14:05–15:10");
    expect(formatSpan("2026-10-30T14:05:00", undefined, "fallback", NOW)).toBe("fallback");
    expect(formatSpan("nope", "2026-10-30T15:10:00", "fallback", NOW)).toBe("fallback");
  });

  it("reads a chat's recent messages by day", () => {
    const text = "…\n## 2026-10-29\n- 09:12 韩梅梅: 早\n## 2026-10-30\n- 10:20 我: 可以\n  明天再给你\n- 10:22 韩梅梅: 👍\n";
    expect(parseRecent(text)).toEqual([
      { day: "2026-10-29", messages: [{ time: "09:12", sender: "韩梅梅", text: "早" }] },
      {
        day: "2026-10-30",
        messages: [
          { time: "10:20", sender: "我", text: "可以\n明天再给你" },
          { time: "10:22", sender: "韩梅梅", text: "👍" },
        ],
      },
    ]);
    expect(parseRecent("每周三下午的设计评审群")).toEqual([{ day: null, messages: [{ time: "", sender: "", text: "每周三下午的设计评审群" }] }]);
    expect(parseRecent(undefined)).toEqual([]);
  });
});

describe("问 Persona", () => {
  it("writes the context tag the app parses (kind, title, url, in that order)", () => {
    expect(contextXml({ kind: "context", title: "t", text: "x</persona-context>y</persona-context>z" })).toBe('<persona-context kind="context" title="t" url="">\nxyz\n</persona-context>');
    expect(contextXml({ kind: "context", title: 'A & "B" <c>', text: "x</persona-context>y" })).toBe(
      '<persona-context kind="context" title="A &amp; &quot;B&quot; &lt;c&gt;" url="">\nxy\n</persona-context>',
    );
  });

  it("carries the profile, the last ten timeline lines and the folder", () => {
    const log = Array.from({ length: 12 }, (_, i) => `- 2026-10-${String(i + 1).padStart(2, "0")} · 飞书 · ${i}`);
    const t = askPersonaText({ name: "天气 App" }, { summary: "# 天气 App\n档案", log, path: "/home/me/.persona/data/projects/weather-app" });
    expect(t.startsWith('<persona-context kind="context" title="知识库 · 天气 App" url="">\n# 天气 App\n档案\n\n时间线（最近）：\n- 2026-10-03')).toBe(true);
    expect(t).not.toContain("2026-10-02");
    expect(t).toContain("知识库目录：/home/me/.persona/data/projects/weather-app\n</persona-context>\n\n说说「天气 App」现在怎么样了");
    expect(askPersonaText({ name: "韩梅梅", one_line: "产品经理" })).toContain('url="">\n产品经理\n</persona-context>');
  });
});

describe("打标", () => {
  it("starts from the entry and sends a project's kind and status", () => {
    const p = e("w", { importance: "", relevance: 92.4, tags: ["iOS", "小组件"], extra: "long · merged" });
    const f = tagForm(p);
    expect(f).toEqual({ importance: "P2", relevance: 92, tags: "iOS 小组件", projectKind: "long", status: "active" });
    expect(tagUpdate("projects", { ...f, relevance: 130, tags: "#iOS 前端" })).toEqual({ importance: "P2", relevance: 100, tags: ["iOS", "前端"], status: "active", project_kind: "long" });
    expect(tagUpdate("people", tagForm(e("x", { kind: "people", importance: "P1", relevance: 70 })))).toEqual({ importance: "P1", relevance: 70, tags: [] });
  });
});

describe("开 Agent", () => {
  it("starts with the project as context, and its folder on an online computer", () => {
    const d = { summary: "档案", log: ["- 10-30 发布"], path: "/home/me/.persona/data/projects/weather-app", meta: { dirs: ["laptop:/home/me/code/weather", "desk:~/code/weather"] } };
    const p = agentPreset("projects", { name: "天气 App" }, d, ["desk"]);
    expect(p.prompt).toContain('<persona-context kind="context" title="项目 · 天气 App" url="">\n档案\n\n时间线（最近）：\n- 10-30 发布');
    expect(p.prompt.endsWith("</persona-context>\n\n")).toBe(true);
    expect(p).toMatchObject({ machineId: "desk", cwd: "~/code/weather" });
    expect(agentPreset("projects", { name: "天气 App" }, d, [])).toEqual({ prompt: p.prompt });
    expect(agentPreset("projects", { name: "x", one_line: "一句话" }, null, ["desk"]).prompt).toContain("\n一句话\n");
  });
});

describe("recording minutes helpers", () => {
  it("labels minutes search hits", () => {
    expect(hitView({ path: "recordings/example/minutes.md", line: "- Design review" })).toEqual({ source: t("lib-kb.hit_minutes"), text: "Design review", day: null });
  });
  it("uses local clock math across midnight and keeps fractional offsets", () => {
    const start = new Date(2026, 10, 9, 23, 59, 50).toISOString();
    expect(recordingClock(start, 15.5)).toBe("00:00:05");
    expect(recordingClock(start, 3615)).toBe("01:00:05");
    expect(recordingClock("invalid", 65.9)).toBe("1:05");
    expect(recordingClock(undefined, 3723)).toBe("1:02:03");
    expect(transcriptLineId(15.5)).toBe("recording-line-15.5");
    expect(transcriptLineId(15.5, true)).toBe("recording-bookmark-15.5");
  });
  it("targets ordered spoken lines, including gaps, not intervening bookmarks", () => {
    const rec = recordingView("recordings", { bookmarks: [{ at: 12, label: "Review" }, { at: 20.5 }] }, [
      { start: 20.5, end: 25, text: "Second" }, { start: 5, end: 10, text: "First" },
    ])!;
    expect(rec.lines.map((line) => line.seconds)).toEqual([5, 12, 20.5, 20.5]);
    expect(transcriptTarget(rec.lines, 0)).toBe(transcriptLineId(5));
    expect(transcriptTarget(rec.lines, 5)).toBe(transcriptLineId(5));
    expect(transcriptTarget(rec.lines, 18)).toBe(transcriptLineId(5));
    expect(transcriptTarget(rec.lines, 20.5)).toBe(transcriptLineId(20.5));
    expect(transcriptTarget(rec.lines, 100)).toBe(transcriptLineId(20.5));
    expect(transcriptTarget(rec.lines.filter((line) => line.bookmark), 12)).toBeUndefined();
    expect(transcriptTarget([], 0)).toBeUndefined();
  });
});

describe("recordingView", () => {
  it("is only for recordings", () => {
    expect(recordingView("meetings", {}, [])).toBeNull();
  });
  it("puts segments and bookmarks in order at their clock time", () => {
    const start = new Date(2026, 10, 5, 9, 0, 0).toISOString();
    const v = recordingView(
      "recordings",
      { started_at: start, duration: 3733, device: "watch", language: "zh-CN", complete: false, bookmarks: [{ at: 12, label: "灰度" }] },
      [
        { start: 20, end: 25, text: "后一段" },
        { start: 0, end: 9, text: " 第一段 " },
        { start: 30, end: 31, text: "  " },
      ],
    )!;
    expect(v.lines).toEqual([
      { seconds: 0, clock: "09:00:00", text: "第一段" },
      { seconds: 12, clock: "09:00:12", text: "灰度", bookmark: true },
      { seconds: 20, clock: "09:00:20", text: "后一段" },
    ]);
    expect(v.partial).toBe(true);
    expect(v.duration).toBe(durationLabel(3733));
    expect(v.device).not.toBe(recordingView("recordings", { device: "phone" }, [])!.device);
  });
  it("falls back to offsets without a start", () => {
    expect(recordingView("recordings", { complete: true }, [{ start: 3723, end: 3730, text: "x" }])!.lines[0].clock).toBe("1:02:03");
  });
  it.each([
    [{}, undefined, undefined],
    [{ attendance: "off" }, undefined, undefined],
    [{ attendance: "observer" }, undefined, undefined],
    [{ attendance: "listener" }, "listener", undefined],
    [{ attendance: "participant", attending_session: { machine_id: "macbook", session_id: "s1" } }, "participant", { machineId: "macbook", sessionId: "s1" }],
    [{ attendance: "participant", attending_session: { machine_id: "macbook", session_id: "" } }, "participant", undefined],
    [{ attendance: "listener", attending_session: null }, "listener", undefined],
  ])("reads the attendance of %j", (meta, attendance, attendingSession) => {
    const v = recordingView("recordings", meta, [])!;
    expect(v.attendance).toBe(attendance);
    expect(v.attendingSession).toEqual(attendingSession);
  });
  it.each([
    [{ trimmed_at: "2026-11-25T02:00:00Z" }, "2026-11-25T02:00:00Z"],
    [{ trimmed_at: "soon" }, undefined],
    [{ trimmed_at: 1 }, undefined],
    [{}, undefined],
  ])("reads trimmed_at of %j", (meta, trimmedAt) => {
    expect(recordingView("recordings", meta, [])!.trimmedAt).toBe(trimmedAt);
  });
});

describe("minutesKeptUntil", () => {
  it("is the start plus the minutes' days, as a date", () => {
    const start = new Date(2026, 10, 5, 9, 0, 0);
    expect(minutesKeptUntil(start.toISOString(), 30)).toBe(minutesKeptUntil(new Date(start.getTime() + 29 * 86_400_000).toISOString(), 1));
    expect(minutesKeptUntil(start.toISOString(), 30)).not.toBe(minutesKeptUntil(start.toISOString(), 29));
    expect(minutesKeptUntil(start.toISOString(), 365)).toContain("2027");
  });
  it.each([[undefined, 30], ["not a time", 30], ["2026-11-05T09:00:00Z", 0], ["2026-11-05T09:00:00Z", 1.5], ["2026-11-05T09:00:00Z", "30"], ["2026-11-05T09:00:00Z", undefined]])(
    "is unknown for %j and %j", (start, days) => {
      expect(minutesKeptUntil(start, days)).toBeUndefined();
    });
});
