import { describe, expect, it } from "vitest";
import { PLAN_PERMISSIONS } from "../../lib/feed";
import enTasks from "../../i18n/locales/en/assistant-tasks.json";
import jaTasks from "../../i18n/locales/ja/assistant-tasks.json";
import zhTasks from "../../i18n/locales/zh-Hans/assistant-tasks.json";
import { permissionLabel } from "./PlanEdit";

describe("a plan task's permission (#3026)", () => {
  it("names Auto, which the hub may settle from Claude Code's default mode, instead of calling it 每步都问", () => {
    expect(PLAN_PERMISSIONS).toContain("auto");
    expect(permissionLabel("auto")).toBe("自动判断");
    expect(permissionLabel("default")).toBe("每步都问");
    // Every mode Edit offers reads differently.
    expect(new Set(PLAN_PERMISSIONS.map(permissionLabel)).size).toBe(PLAN_PERMISSIONS.length);
  });
});

describe("a read-only task's label (#4864)", () => {
  it("says the agent was asked to read only, not that Claude has a sandbox", () => {
    expect([enTasks.readonly, zhTasks.readonly, jaTasks.readonly]).toEqual([
      "Read-only requested",
      "已要求只读",
      "読み取り専用を要求",
    ]);
  });
});
