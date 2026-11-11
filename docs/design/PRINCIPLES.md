# Persona design principles
# Design principles

Persona is an Agent product for everyone. Assistant is for everyday people and must stay approachable; Agents is for people who collaborate directly with tools such as Claude Code.

**Audience test:** before shipping, answer in one sentence who this design serves. If the answer is "developers", it belongs in Agents or advanced settings, not in an everyday-facing experience.


**Persona is a personal Agent product for everyone, not only software developers.** Defaults are simple and reliable, complex capabilities open up gradually through settings, and with the user's permission an Agent can help manage that complexity.

- **Assistant** is for everyday people, with no programming or Agent-tool knowledge required.
- **Agents** is for people who want to collaborate directly with Claude Code and other Agents.
- Every design decision starts with **who it serves**. Assistant and other everyday-facing interfaces stay simple and understandable; developer terminology, engineering details and accumulating options must not make them harder to use. Specialist workflows belong in Agents or advanced settings, with details disclosed only when needed.

**How to check the audience**: the issue and PR name the intended audience. For an everyday-facing flow, verify that someone with no programming or Agent-tool knowledge can understand the task and next action without choosing a runtime or repository. For specialist work, name its home in Agents or advanced settings and explain why the detail is needed there.

These six principles decide product questions before any visual rule applies. They cover UI, settings and defaults, and how numbers and states are counted, on every client and in the hub. Each one has a few rules and a "How to check" that a reviewer can verify on a PR. Visual and interaction detail lives in `docs/design/DESIGN_SYSTEM.md`; known gaps against Apple's Human Interface Guidelines are in `docs/design/HIG_AUDIT.md`. Ideas taken from the HIG are rewritten here as Persona rules, not quoted.

Where they apply: requirements (the issue's acceptance criteria), development (the PR template's self-check) and acceptance (the reviewer checks the evidence below). See "How the principles enter the workflow" at the end.

## 1. Defaults first

Common tasks work out of the box, with no setup, and the defaults suit most people.

- A new install with only a hub paired can do the frequent tasks: chat with Persona, start an Agent, answer its question, approve a plan or tool.
- A default is stable and fits in one plain sentence. It never changes silently between versions; a change is announced and the old behavior stays reachable.
- A default the user already set elsewhere wins over a new built-in one (for example the permission mode in the computer's own Agent settings).

**How to check**: the PR states the default in one plain sentence; the change works on a fresh install with untouched settings (screenshot or test on a clean profile / mock hub).

## 2. Progressive disclosure

Complex capabilities sit behind settings or an advanced entry and open on request.

- New advanced options default to off and live in Settings or an advanced group, not on the main chat or Agents screens.
- Detail opens on request: tool execution stays one summary line (super rule 2); expert terms and raw values (model IDs, ports, paths, hub, node, MCP) appear only in the advanced area that needs them, or come with a plain explanation (`DESIGN_SYSTEM.md` section 5).
- A new look is an opt-in style or theme color; the default style stays pixel-identical.

**How to check**: with the feature off, before / after screenshots of the primary screens are identical; the PR names where the option lives and that it is off by default.

## 3. User control

The user can see, change and undo every behavior; what Persona shows can be explained.

- Every behavior-changing setting is visible in Settings with its current value; no hidden flag whose only switch is a chat command or a file.
- Reversible actions offer Undo; irreversible ones confirm first, with a destructive-styled button and Cancel (`DESIGN_SYSTEM.md` section 4.4). An undo or rollback the user made sticks: no background job silently redoes it.
- Background work stops things, it never removes what the user may still open: an Agent always runs in a terminal and keeps it after it stops; only the user closes a terminal (`docs/integrations/engines.md`, "Agents run in a terminal").
- When Persona or an Agent changes a setting for the user, it first says in plain words what changes, where and from when, waits for confirmation, records the change and offers a rollback (reference: model profile switch, `docs/assistant/model-profiles.md`). Every count or status can say what it counts.

**How to check**: for each new setting the PR names where it is shown, how it is changed back, and, if an Agent can change it, the confirmation text and the rollback path (test or screenshot). Each new number has a one-line definition in the docs or in the UI.

## 4. Consistency first

Reuse the existing interaction, visual and data patterns; a new pattern needs a written reason.

- Pages use design-system components and tokens only (`DESIGN_SYSTEM.md` sections 2 and 3); the same action looks and sits the same on every screen and, where the platform allows, on iOS, Android and web (section 7).
- One label, one definition: a number or state shown under the same name ("Not done", "Needs you") is computed by the same rule on every screen and platform, ideally by one shared function.
- Moving or adding an entry point follows the place similar entries already use; a new component, pattern or definition goes into `DESIGN_SYSTEM.md` (or the module doc) with why the existing one did not fit.

**How to check**: the PR lists the components, patterns and definitions it reused; anything new links its reason in the updated doc. For counts: the same data shows the same number in every place that names it (test or side-by-side screenshots).

## 5. Model and product are separate

Speed, model, persona and features are four separate settings; changing one never changes another.

- Model choice and speed live in model settings and profiles; product features never hard-code a model or rely on one model's quirks.
- The avatar, theme color and interface style are independent (`docs/apple/persona.md`); the avatar does not pick a model, and switching models does not change the avatar or the UI.
- Feature switches decide what Persona can do, not which model does it; switching profiles does not turn features on or off.

**How to check**: the PR names which of the four it touches; a test or manual check shows that switching any other one leaves the change's behavior unchanged.

## 6. Rules are checkable

A principle that no one checks is a wish: every rule here ends in evidence someone can verify.

- An issue's acceptance criteria name the principles it touches, each as a checkable statement ("off by default; primary screens unchanged").
- A PR that touches UI, settings or defaults, or how something is counted, fills in the template's principles self-check: each item done, not applicable, or deviated with a reason.
- A violation found after merge becomes a case below, and, where it can, a test or check that catches it next time.

**How to check**: the linked issue has acceptance criteria; the PR body has the self-check with evidence (screenshots, test names, commands and their output) for each item marked done.

## Cases

Recent examples, so the principles are concrete. Numbers are issues and PRs in this repository.

| What happened | Principle | Lesson |
| --- | --- | --- |
| #2315 moved the web sidebar's New chat out of the Persona chats section to its own row under search; #3254 moved it back into that section, the list the new chat joins. | 4 Consistency | Make an entry easier to find (label, visibility) in the place similar entries already use; moving it is a new pattern that needs a reason. |
| The main chat's Progress said 86 while the To-dos page's "Not done" said 32 (#3331): Progress counted every to-do without a done time, the To-dos page left out shelved and accepted ones. #3340 made both use one rule. | 4 Consistency, 3 User control | The same idea needs one definition, and a number has to be explainable; two counts of "open work" that disagree make both untrustworthy. |
| A manual rollback of an extension was reinstalled by auto-upgrade about a minute later (#1826); #1951 records the rolled-back version as skipped. | 3 User control | An undo or rollback the user made must stick; background automation never silently reverses it. |
| A finished task's Agent opened from the iPhone showed "No Terminal" (#4328): the hub had closed its tab when the task was done, and the node closed any Persona tab left with a bare shell after ten minutes. #4754 stops the Agent and keeps its terminal, and a guard test pins every place that may close one. | 3 User control | Automation may stop work, but closing what the user may still want to see (a terminal, a tab) is the user's call. |
| Agents started from the main chat always ran in "Ask every step", ignoring the permission mode the user had set in the computer's Agent settings (#3026); #3047 follows that setting. | 1 Defaults first, 4 Consistency | A built-in default must not override a choice the user already made, and the same start should behave the same from every entry point. |

## How the principles enter the workflow

- **Requirement**: the feature form's acceptance criteria and the claim comment's `Acceptance:` line are written against these principles (`docs/operations/collaboration.md`).
- **Development**: `.github/pull_request_template.md` carries the principles self-check; UI changes also run the "UI change self-check" in `DESIGN_SYSTEM.md`.
- **Acceptance**: whoever merges checks the evidence named under "How to check"; a missing item is marked deviated with a reason, never left blank.
