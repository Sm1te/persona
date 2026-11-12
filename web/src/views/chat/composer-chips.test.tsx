// The chip under the composer on a phone (#3575): at 390 px the row (+, @, "Ask first", the model pill, send) is wider
// than the box. The chip gives way instead: it may shrink (its label truncates, the chevron stays), and below 340 px it is
// an icon and a short word. The model, its launcher and the thinking effort are the model pill's (#5032, #5033). A unit
// test has no layout, so this holds the classes that make the row fit; the 390 px measurement is in the PR.
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Session } from "../../lib/types";
import { t } from "../../i18n";
import { Chip, ModeChips, modeShort } from "./SessionDialogs";

// the class lists of the markup's elements, in document order
const tags = (node: React.ReactElement) =>
  [...renderToStaticMarkup(node).matchAll(/<(\w+)[^>]*?class="([^"]*)"[^>]*>([^<]*)/g)].map(([, tag, cls, text]) => ({ tag, cls: cls.split(/\s+/), text }));

const session = { machine_id: "m", session_id: "s", permission_mode: "default", effort: "high" } as unknown as Session;

describe("composer chips on a narrow row", () => {
  it("a chip can shrink below its label, truncating the label and keeping the chevron", () => {
    const [button, label, chevron] = tags(<Chip label="claude · opus" title="launcher" onClick={() => {}} />);
    expect(button.tag).toBe("button");
    expect(button.cls).toEqual(expect.arrayContaining(["min-w-0", "max-w-full"]));
    expect(label.text).toBe("claude · opus");
    expect(label.cls).toEqual(expect.arrayContaining(["min-w-0", "truncate"]));
    expect(chevron.tag).toBe("svg");
    expect(chevron.cls).toContain("shrink-0");
  });

  it("the permission chip alone, an Agent's and a Persona conversation's alike: it keeps its width wide and may shrink on a phone", () => {
    for (const assistant of [false, true]) {
      const [wrapper] = tags(<ModeChips session={session} assistant={assistant} />);
      expect(wrapper.cls).toEqual(expect.arrayContaining(["shrink-0", "max-sm:min-w-0", "max-sm:shrink"]));
      const html = renderToStaticMarkup(<ModeChips session={session} assistant={assistant} />);
      expect(html.match(/<button/g)).toHaveLength(1);
      expect(html).toContain(`title="${t("chat-sessiondialogs.permission_mode")}"`);
    }
  });

  // #4316: at 320 px even "Ask first" alone did not fit; below 340 px it is an icon and a short word
  it("below 340 px the mode chip shows an icon and a short word, keeps the full label for screen readers", () => {
    const [, icon, short, label, chevron] = tags(<Chip label="手动确认" narrow={{ icon: "shield", label: "先问" }} title="mode" onClick={() => {}} />);
    expect(icon.tag).toBe("svg");
    expect(icon.cls).toEqual(expect.arrayContaining(["shrink-0", "xs:hidden"]));
    expect(short.text).toBe("先问");
    expect(short.cls).toEqual(expect.arrayContaining(["truncate", "xs:hidden"]));
    expect(label.text).toBe("手动确认");
    expect(label.cls).toContain("max-xs:sr-only");
    expect(chevron.cls).toContain("max-xs:hidden");
    // a chip without it is as before
    const plain = tags(<Chip label="手动确认" title="mode" onClick={() => {}} />);
    expect(plain).toHaveLength(3);
    expect(plain[2].cls).not.toContain("max-xs:hidden");
    expect(renderToStaticMarkup(<ModeChips session={session} assistant />)).toContain(">先问<");
    expect(modeShort("default")).toBe("先问");
    expect(modeShort("bypassPermissions")).toBe("放行");
    expect(modeShort(undefined)).toBe("默认");
    expect(modeShort("someNewMode")).toBe("someNewMode");
  });
});
