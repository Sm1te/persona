import { describe, expect, it } from "vitest";
import {
  taskConversations,
  blankTask, clockLabel, dayLabel, everyLabel, fromLocalInput, fromTemplate, lastRunText, momentLabel, nextHour, nextText, normalizeDays,
  parseTime, repeatsLabel, ruleText, runDays, runDetail, runsWith, sameTask, splitTasks, taskBody, taskTitle, TEMPLATES, toLocalInput, untilLabel, untilText, withPush,
  upcoming, validateTask, withRepeat, type Run, type Task,
} from "./routines";

// Local times throughout, so the tests pass in any time zone. Mon 2 Nov 2026, 10:00.
const now = new Date(2026, 10, 2, 10, 0);
const at = (d: number, h: number, m = 0) => new Date(2026, 10, d, h, m);
const iso = (d: Date) => d.toISOString();
const task = (p: Partial<Task>): Task => ({ ...blankTask(), id: "t1", title: "早报", prompt: "给我早报", ...p });

describe("words", () => {
  it("says which days", () => {
    expect(repeatsLabel([])).toBe("每天");
    expect(repeatsLabel([1, 2, 3, 4, 5, 6, 7])).toBe("每天");
    expect(repeatsLabel([5, 1, 2, 3, 4])).toBe("工作日");
    expect(repeatsLabel([7, 6])).toBe("周末");
    expect(repeatsLabel([1, 3, 5, 3])).toBe("每周一、三、五");
    expect(normalizeDays([3, 1, 9, 1])).toEqual([1, 3]);
  });

  it("says a clock time as Reminders does", () => {
    expect(clockLabel("09:00")).toBe("上午 9:00");
    expect(clockLabel("08:45")).toBe("早上 8:45");
    expect(clockLabel("12:30")).toBe("中午 12:30");
    expect(clockLabel("17:00")).toBe("下午 5:00");
    expect(clockLabel("21:30")).toBe("晚上 9:30");
    expect(clockLabel("00:15")).toBe("凌晨 12:15");
    expect(clockLabel("9:00")).toBe("9:00"); // not HH:MM: left as it is
  });

  it("says an interval", () => {
    expect(everyLabel(30)).toBe("每 30 分钟");
    expect(everyLabel(120)).toBe("每 2 小时");
    expect(everyLabel(90)).toBe("每 1 小时 30 分钟");
    expect(everyLabel(1440)).toBe("每天");
    expect(everyLabel(2880)).toBe("每 2 天");
  });

  it("says the day and how long until", () => {
    expect(dayLabel(at(2, 18), now)).toBe("今天");
    expect(dayLabel(at(3, 9), now)).toBe("明天");
    expect(dayLabel(at(1, 9), now)).toBe("昨天");
    expect(dayLabel(at(5, 9), now)).toBe("周四");
    expect(dayLabel(at(20, 9), now)).toBe("11月20日");
    expect(momentLabel(at(3, 9, 5), now)).toBe("明天 09:05");
    expect(untilLabel(new Date(now.getTime() + 20_000), now)).toBe("马上");
    expect(untilLabel(at(2, 10, 25), now)).toBe("25 分钟后");
    expect(untilLabel(at(2, 11, 30), now)).toBe("1 小时 30 分钟后");
    expect(untilLabel(at(2, 17), now)).toBe("7 小时后");
    expect(untilLabel(at(5, 9), now)).toBe("3 天后");
    expect(untilLabel(at(2, 9), now)).toBe("已过时间");
  });

  it("says the rule", () => {
    expect(ruleText(task({ repeat: "daily", time: "09:00", weekdays: [1, 2, 3, 4, 5] }))).toBe("每个工作日 上午 9:00");
    expect(ruleText(task({ repeat: "daily", time: "22:00", weekdays: [] }))).toBe("每天 晚上 10:00");
    expect(ruleText(task({ repeat: "daily", time: "17:00", weekdays: [5] }))).toBe("每周五 下午 5:00");
    expect(ruleText(task({ repeat: "interval", every_minutes: 30 }))).toBe("每 30 分钟");
    expect(ruleText(task({ repeat: "once", session_id: "s1" }))).toBe("一次 · 接着原来的对话");
    expect(ruleText(task({ repeat: "once" }))).toBe("一次 · 新对话");
  });

  it("titles a follow-up without one by its prompt", () => {
    expect(taskTitle({ title: "", prompt: "\n周五发周报前核对数据\n第二行" })).toBe("周五发周报前核对数据");
    expect(taskTitle({ title: "  周报 ", prompt: "x" })).toBe("周报");
  });

  it("says the next and the last run", () => {
    expect(nextText(task({ enabled: false, next_run: iso(at(3, 9)) }), now)).toBe("已关闭");
    expect(nextText(task({ next_run: iso(at(3, 9)) }), now)).toBe("下次 明天 09:00 · 23 小时后");
    expect(nextText(task({ repeat: "interval", at: iso(at(2, 10, 12)) }), now)).toBe("下次 今天 10:12 · 12 分钟后");
    expect(lastRunText(task({}), now)).toBeNull();
    expect(lastRunText(task({ last_run: "0001-01-01T00:00:00Z" }), now)).toBeNull(); // Go's zero time
    expect(lastRunText(task({ last_run: iso(at(2, 9, 3)), last_status: "ok" }), now)).toEqual({ text: "上次 今天 09:03 · 已运行", failed: false });
    expect(lastRunText(task({ last_run: iso(at(30, 17)), last_status: "failed" }), at(31, 12))).toEqual({ text: "上次 昨天 17:00 · 失败", failed: true });
  });

  it("says what it runs with when not the default", () => {
    expect(runsWith({})).toBeNull();
    expect(runsWith({ permission_mode: "default" })).toBeNull();
    expect(runsWith({ launcher: "claude-fast", permission_mode: "auto", effort: "low" })).toEqual({ text: "claude-fast · 自动 · 思考低", danger: false });
    expect(runsWith({ permission_mode: "bypassPermissions" })).toEqual({ text: "全部放行", danger: true });
  });

  it("names its launcher by the provider's display name; an rc alias by its ID (#5029)", () => {
    const names = [{ name: "relay-example-com", display_name: "我的中转 站" }, { name: "cc_glm" }];
    expect(runsWith({ launcher: "relay-example-com", effort: "low" }, names)).toEqual({ text: "我的中转 站 · 思考低", danger: false });
    expect(runsWith({ launcher: "cc_glm" }, names)).toEqual({ text: "cc_glm", danger: false });
    expect(runsWith({ launcher: "gone" }, names)).toEqual({ text: "gone", danger: false });
  });
});

describe("the editor", () => {
  it("checks a task before saving", () => {
    expect(validateTask(task({ title: " " }), now)).toBe("要有标题");
    expect(validateTask(task({ repeat: "once", title: "", at: iso(at(2, 15)) }), now)).toBeNull(); // a one-off needs no title
    expect(validateTask(task({ prompt: "  " }), now)).toBe("提示词不能为空");
    expect(validateTask(task({ time: "9:00" }), now)).toBe("时间要写成 HH:MM，比如 09:00");
    expect(validateTask(task({ time: "24:00" }), now)).toBe("时间要写成 HH:MM，比如 09:00");
    expect(validateTask(task({ time: "" }), now)).toBe("时间要写成 HH:MM，比如 09:00");
    expect(validateTask(task({ time: "23:59" }), now)).toBeNull();
    expect(validateTask(task({ repeat: "interval", every_minutes: 4 }), now)).toBe("间隔要在 5 分钟到 7 天之间");
    expect(validateTask(task({ repeat: "interval", every_minutes: 7 * 1440 + 1 }), now)).toBe("间隔要在 5 分钟到 7 天之间");
    expect(validateTask(task({ repeat: "interval", every_minutes: 7.5 }), now)).toBe("间隔要在 5 分钟到 7 天之间");
    expect(validateTask(task({ repeat: "interval", every_minutes: 5 }), now)).toBeNull();
    expect(validateTask(task({ repeat: "interval", every_minutes: 7 * 1440 }), now)).toBeNull();
    expect(validateTask(task({ repeat: "once", at: undefined }), now)).toBe("要选一个运行时间");
    expect(validateTask(task({ repeat: "once", at: iso(at(2, 9)) }), now)).toBe("这个时间已经过了，选一个以后的时间");
    expect(validateTask(task({ repeat: "once", at: iso(at(2, 9)), enabled: false }), now)).toBeNull();
    const passed = "截止时间已经过了。请改晚一点，或清掉。";
    expect(validateTask(task({ repeat: "daily", until: iso(at(2, 9)) }), now)).toBe(passed);
    expect(validateTask(task({ repeat: "interval", every_minutes: 30, until: iso(new Date(now.getTime() - 61_000)) }), now)).toBe(passed);
    expect(validateTask(task({ repeat: "daily", until: iso(new Date(now.getTime() - 60_000)) }), now)).toBeNull();
    expect(validateTask(task({ repeat: "daily", until: iso(at(2, 9)), enabled: false }), now)).toBeNull();
    expect(validateTask(task({ repeat: "daily", until: iso(at(2, 18)) }), now)).toBeNull();
    expect(validateTask(task({ repeat: "daily" }), now)).toBeNull();
    expect(validateTask(task({ repeat: "once", at: iso(at(2, 15)), until: iso(at(2, 9)) }), now)).toBeNull();
  });

  it("sees what changed", () => {
    const t = task({ repeat: "daily", time: "09:00", weekdays: [1, 2, 3, 4, 5], next_run: iso(at(3, 9)) });
    expect(sameTask(t, { ...t, next_run: undefined, last_status: "failed" })).toBe(true); // the hub's own fields
    expect(sameTask(t, { ...t, weekdays: [5, 4, 3, 2, 1] })).toBe(true);
    expect(sameTask(t, { ...t, every_minutes: 45 })).toBe(true); // another rule's field
    expect(sameTask(t, { ...t, time: "09:30" })).toBe(false);
    expect(sameTask(t, { ...t, enabled: false })).toBe(false);
    expect(sameTask(t, { ...t, permission_mode: "auto" })).toBe(false);
    expect(sameTask(t, { ...t, title: "早报 " })).toBe(true);
    expect(sameTask(t, { ...t, launcher: "legacy", effort: "high" })).toBe(true); // models are configured by category
    const end = iso(at(5, 18));
    expect(sameTask(t, { ...t, until: end })).toBe(false);
    expect(sameTask({ ...t, until: end }, { ...t, until: end })).toBe(true);
    expect(sameTask({ ...t, until: end }, { ...t, until: undefined })).toBe(false);
    const once = task({ repeat: "once", at: iso(at(2, 15)), until: end });
    expect(sameTask(once, { ...once, until: undefined })).toBe(true); // a one-off ignores its end time
  });

  it("saves editable fields without task model overrides", () => {
    const saved = task({ repeat: "daily", time: "09:00", weekdays: [1, 2, 3, 4, 5, 6, 7], launcher: "claude", effort: "low", session_id: "s1", at: "0001-01-01T00:00:00Z" });
    const body = taskBody({ ...saved, title: " 早报 ", permission_mode: "auto" });
    expect(body).toEqual({ title: "早报", prompt: "给我早报", repeat: "daily", enabled: true, time: "09:00", weekdays: [], every_minutes: 0, cron: "", session_id: "s1", permission_mode: "auto" });
    expect(taskBody({ ...saved, session_id: undefined }).session_id).toBe(""); // runs go to the task's own conversation
    const once = taskBody(task({ repeat: "once", at: iso(at(2, 15)) }));
    expect(once).toMatchObject({ repeat: "once", at: iso(at(2, 15)), time: "", weekdays: [] });
    expect(once).not.toHaveProperty("launcher");
    expect(once).not.toHaveProperty("effort");
    expect(taskBody(task({ repeat: "interval", every_minutes: 45 }))).toMatchObject({ every_minutes: 45, time: "" });
    expect("at" in taskBody(task({ repeat: "interval", every_minutes: 45 }))).toBe(false);
    const end = at(5, 18);
    expect(taskBody(task({ repeat: "daily", until: iso(end) })).until).toBe(iso(end));
    expect(taskBody(task({ repeat: "interval", every_minutes: 30, until: iso(end) })).until).toBe(iso(end));
    expect("until" in taskBody(task({ repeat: "daily" }))).toBe(false);
    expect("until" in taskBody(task({ repeat: "once", at: iso(at(2, 15)), until: iso(end) }))).toBe(false);
    const ended = task({ repeat: "daily", until: iso(end) });
    expect("until" in taskBody(ended, ended)).toBe(false);
    expect(taskBody({ ...ended, until: undefined }, ended).until).toBeNull();
    expect(taskBody({ ...ended, until: iso(at(6, 9)) }, ended).until).toBe(iso(at(6, 9)));
    expect(taskBody({ ...ended, repeat: "once", at: iso(at(2, 15)) }, ended).until).toBeNull();
    expect("until" in taskBody(task({ repeat: "once", at: iso(at(2, 15)) }), task({ repeat: "daily" }))).toBe(false);
    expect(untilText(task({ repeat: "daily", until: iso(at(2, 18)) }), now)).toBe("截止 今天 18:00");
    expect(untilText(task({ repeat: "interval", every_minutes: 30, until: iso(at(3, 9, 5)) }), now)).toBe("截止 明天 09:05");
    expect(untilText(task({ repeat: "once", until: iso(at(2, 18)) }), now)).toBe("");
    expect(untilText(task({ repeat: "daily" }), now)).toBe("");
    expect(untilText(task({ repeat: "daily", until: "0001-01-01T00:00:00Z" }), now)).toBe("");
  });

  it("跑完推送: compared, saved only when changed, and an asked task leaves the conversation it came from", () => {
    const saved = task({ repeat: "interval", every_minutes: 5, session_id: "main-1" });
    expect(sameTask(saved, { ...saved, push: "" })).toBe(true); // a hub that does not send it: every run
    expect(sameTask(saved, { ...saved, push: "asked" })).toBe(false);
    expect("push" in taskBody(saved, saved)).toBe(false); // unchanged: a hub keeps it, and one that does not know it never gets it
    expect("push" in taskBody(task({ repeat: "interval", every_minutes: 5 }))).toBe(false); // a new task that pushes every run
    const asked = withPush(saved, "asked");
    expect(asked.push).toBe("asked");
    expect(asked.session_id).toBeUndefined(); // it runs in its own conversation
    expect(taskBody(asked, saved)).toMatchObject({ push: "asked", session_id: "" });
    const back = withPush({ ...saved, push: "asked", session_id: undefined }, "");
    expect(back.push).toBe("");
    expect(back.session_id).toBeUndefined(); // switched back, it stays in its own conversation
    expect(taskBody(back, { ...saved, push: "asked" })).toMatchObject({ push: "" });
  });

  it("edits a cron task: says its rule, checks, compares and saves its expression", () => {
    const cron = task({ repeat: "cron", cron: "*/30 9-18 * * 1-5", cron_text: "每隔 30 分钟, 在 09:00 和 18:59 之间, 星期一至星期五", at: "0001-01-01T00:00:00Z" });
    expect(ruleText(cron)).toBe("*/30 9-18 * * 1-5 · 每隔 30 分钟, 在 09:00 和 18:59 之间, 星期一至星期五");
    expect(ruleText({ ...cron, cron_text: "" })).toBe("Cron */30 9-18 * * 1-5"); // the hub has no words for it
    expect(validateTask(cron, now)).toBeNull();
    expect(validateTask({ ...cron, cron: "  " }, now)).toBe("请写一个 cron 表达式，比如 0 9 * * 1-5");
    expect(validateTask({ ...cron, until: iso(at(1, 9)) }, now)).not.toBeNull(); // a past end time, as a daily task's
    expect(sameTask(cron, { ...cron, cron: " */30 9-18 * * 1-5 " })).toBe(true);
    expect(sameTask(cron, { ...cron, cron: "0 9 * * *" })).toBe(false);
    const body = taskBody({ ...cron, cron: " 0 10 1 * * " }, cron);
    expect(body).toMatchObject({ repeat: "cron", cron: "0 10 1 * *", time: "", weekdays: [], every_minutes: 0 });
    expect("at" in body).toBe(false);
    expect(taskBody(task({ repeat: "daily", cron: "0 9 * * *" })).cron).toBe(""); // another rule clears it
    expect(withRepeat(task({ repeat: "daily" }), "cron", now).cron).toBe("0 9 * * 1-5");
    expect(withRepeat(cron, "cron", now).cron).toBe("*/30 9-18 * * 1-5");
    expect(splitTasks([cron]).repeating).toEqual([cron]);
  });

  it("keeps the editor valid when the rule changes", () => {
    const d = withRepeat(task({ repeat: "daily" }), "once", now);
    expect(parseTime(d.at)).toEqual(at(2, 11));
    expect(withRepeat(task({ every_minutes: 0 }), "interval", now).every_minutes).toBe(30);
    expect(withRepeat(task({ repeat: "interval", time: "" }), "daily", now).time).toBe("09:00");
    const future = iso(at(4, 8));
    expect(withRepeat(task({ at: future }), "once", now).at).toBe(future);
    expect(nextHour(at(2, 23, 30))).toEqual(at(3, 0, 0));
  });

  it("reads and writes a datetime-local value in local time", () => {
    expect(toLocalInput(at(2, 9, 5))).toBe("2026-11-02T09:05");
    expect(fromLocalInput("2026-11-02T09:05")).toEqual(at(2, 9, 5));
    expect(fromLocalInput("")).toBeNull();
    expect(toLocalInput(null)).toBe("");
  });

  it("starts from a template", () => {
    const t = fromTemplate(TEMPLATES.find((x) => x.key === "patrol")!);
    expect(t).toMatchObject({ id: "", title: "Agent 巡检", repeat: "interval", every_minutes: 30, enabled: true });
    expect(validateTask(t, now)).toBeNull();
    for (const tpl of TEMPLATES) expect(validateTask(fromTemplate(tpl), now)).toBeNull();
    expect(validateTask(blankTask(), now)).toBe("要有标题");
  });
});

describe("lists", () => {
  it("splits repeating and one-off tasks, one-offs soonest first", () => {
    const list = [
      task({ id: "a", repeat: "once", at: iso(at(3, 9)), next_run: iso(at(3, 9)) }),
      task({ id: "b", repeat: "daily" }),
      task({ id: "c", repeat: "once", at: iso(at(2, 15)), next_run: iso(at(2, 15)) }),
      task({ id: "d", repeat: "interval", every_minutes: 30 }),
    ];
    const { repeating, oneOff } = splitTasks(list);
    expect(repeating.map((t) => t.id)).toEqual(["b", "d"]);
    expect(oneOff.map((t) => t.id)).toEqual(["c", "a"]);
  });

  it("lists what comes up next, without what is off", () => {
    const list = [
      task({ id: "a", next_run: iso(at(3, 9)) }),
      task({ id: "b", enabled: false, next_run: iso(at(2, 11)) }),
      task({ id: "c", repeat: "once", title: "", prompt: "回电话", at: iso(at(2, 12)), next_run: iso(at(2, 12)) }),
    ];
    expect(upcoming(list).map((u) => [u.id, u.title, u.detail])).toEqual([
      ["c", "回电话", "跟进 · 新对话"],
      ["a", "早报", "每个工作日 上午 9:00"],
    ]);
  });

  it("says how a run went and groups runs by day", () => {
    const r = (p: Partial<Run>): Run => ({ id: "r", kind: "routine", ref_id: "t1", title: "早报", due: iso(at(2, 9)), at: iso(at(2, 9)), status: "ok", ...p });
    expect(runDetail(r({}))).toBe("准时");
    expect(runDetail(r({ at: iso(new Date(at(2, 9).getTime() + 21_000)) }))).toBe("晚了 21 秒");
    expect(runDetail(r({ at: iso(at(2, 9, 3)) }))).toBe("晚了 3 分钟");
    expect(runDetail(r({ at: iso(at(2, 11)) }))).toBe("晚了 2 小时");
    expect(runDetail(r({ manual: true }))).toBe("手动运行");
    expect(runDetail(r({ status: "failed", error: "assistant machine is offline" }))).toBe("assistant machine is offline");
    expect(runDetail(r({ status: "skipped" }))).toBe("已跳过");
    const days = runDays([r({ id: "1", at: iso(at(1, 22)) }), r({ id: "2", at: iso(at(2, 9)) }), r({ id: "3", at: iso(at(1, 9)) }), r({ id: "4", at: iso(new Date(2026, 9, 20, 9)) })], now);
    expect(days.map((d) => [d.day, d.runs.map((x) => x.id)])).toEqual([["今天", ["2"]], ["昨天", ["1", "3"]], ["10月20日", ["4"]]]);
  });
});

describe("taskConversations (#2824)", () => {
  const run = (id: string, ref: string, sid?: string) => ({ id, kind: "routine", ref_id: ref, title: "", due: "", at: "", status: "ok", session_id: sid });
  it("lists a task's own conversation, then its runs', once each, without the one it came from", () => {
    const tk = task({ id: "a", repeat: "daily", run_session_id: "own" });
    const sids = (l: { sid: string }[]) => l.map((c) => c.sid);
    expect(sids(taskConversations(tk, [run("1", "a", "r2"), run("2", "b", "x"), run("3", "a", "own"), run("4", "a", "r1"), run("5", "a")]))).toEqual(["own", "r2", "r1"]);
    expect(taskConversations(task({ id: "a", repeat: "once", session_id: "mine" }), [run("1", "a", "mine")])).toEqual([]);
    expect(sids(taskConversations(tk, [run("1", "a", "r1"), run("2", "a", "r2"), run("3", "a", "r3")], 2))).toEqual(["own", "r1"]);
  });

  it("leaves out the user's own conversations after the task stopped coming from them", () => {
    // a follow-up from the main conversation, later moved to its own: its old runs went into the main one
    const tk = task({ id: "a", repeat: "once", run_session_id: "own" });
    const mine = (sid: string) => sid === "main" || sid === "u";
    const sids = (l: { sid: string }[]) => l.map((c) => c.sid);
    expect(sids(taskConversations(tk, [run("1", "a", "main"), run("2", "a", "u"), run("3", "a", "r1")], 3, mine))).toEqual(["own", "r1"]);
    // its own conversation stays, whatever the caller says
    expect(sids(taskConversations(tk, [], 3, () => true))).toEqual(["own"]);
  });

  it("gives each one when a run last went into it", () => {
    const tk = task({ id: "a", repeat: "daily", run_session_id: "own" });
    const r = (id: string, sid: string, at: string) => ({ ...run(id, "a", sid), at });
    expect(taskConversations(tk, [r("1", "own", "2026-11-05T09:00:00Z"), r("2", "own", "2026-11-04T09:00:00Z"), r("3", "old", "2026-11-01T09:00:00Z")]))
      .toEqual([{ sid: "own", at: "2026-11-05T09:00:00Z" }, { sid: "old", at: "2026-11-01T09:00:00Z" }]);
  });
});
