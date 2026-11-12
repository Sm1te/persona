// A thought's lightweight inline emphasis (#5155), ported from cindy's thinkingText.ts (packages/maker-shared, Apache-2.0):
// Codex often wraps short reasoning updates in `**strong**` markers and names commands in backticks; showing the
// markers is noise, and the chat's Markdown is too heavy for a one-line row and allows blocks that do not belong there.
// Only paired `**text**` and matching backtick runs are read; anything unmatched, escaped, a glob (`**/*.ts`), triple
// stars or `2 ** 3` stays literal. Lists, headings, links, images and HTML are never interpreted.

export type ThinkingTextToken = { kind: "text"; value: string } | { kind: "strong"; value: string } | { kind: "code"; value: string };

function isEscaped(value: string, index: number): boolean {
  let backslashes = 0;
  for (let i = index - 1; i >= 0 && value[i] === "\\"; i--) backslashes++;
  return backslashes % 2 === 1;
}

function findStrongClose(value: string, contentStart: number): number {
  let cursor = contentStart;
  while (cursor < value.length - 1) {
    const close = value.indexOf("**", cursor);
    if (close < 0) return -1;
    const previous = value[close - 1];
    const after = value[close + 2];
    if (close > contentStart && !isEscaped(value, close) && previous !== "*" && !/\s/.test(previous) && after !== "*") return close;
    cursor = close + 2;
  }
  return -1;
}

function findCodeClose(value: string, contentStart: number, runLength: number): number {
  const delimiter = "`".repeat(runLength);
  let cursor = contentStart;
  while (cursor < value.length) {
    const close = value.indexOf(delimiter, cursor);
    if (close < 0) return -1;
    if (!isEscaped(value, close) && value[close - 1] !== "`" && value[close + runLength] !== "`") return close;
    cursor = close + runLength;
  }
  return -1;
}

/** A code span on one line, one surrounding space each side dropped (as Markdown does). */
function normalizeCodeSpan(value: string): string {
  const line = value.replace(/[\r\n]+/g, " ");
  if (line.length >= 2 && line.startsWith(" ") && line.endsWith(" ") && /\S/.test(line)) return line.slice(1, -1);
  return line;
}

/** Splits a thought into plain text, paired `**strong**` and backtick code; everything else is text as written. */
export function tokenizeThinkingText(value: string): ThinkingTextToken[] {
  const tokens: ThinkingTextToken[] = [];
  let plainStart = 0;
  let cursor = 0;
  const flushPlain = (end: number) => {
    if (end > plainStart) tokens.push({ kind: "text", value: value.slice(plainStart, end) });
  };
  while (cursor < value.length) {
    if (value[cursor] === "`" && !isEscaped(value, cursor)) {
      let runLength = 1;
      while (value[cursor + runLength] === "`") runLength++;
      const contentStart = cursor + runLength;
      const close = findCodeClose(value, contentStart, runLength);
      if (close >= 0) {
        flushPlain(cursor);
        tokens.push({ kind: "code", value: normalizeCodeSpan(value.slice(contentStart, close)) });
        cursor = close + runLength;
        plainStart = cursor;
        continue;
      }
      cursor += runLength;
      continue;
    }
    if (
      value.startsWith("**", cursor) &&
      !isEscaped(value, cursor) &&
      value[cursor - 1] !== "*" &&
      value[cursor + 2] !== "*" &&
      value[cursor + 2] !== undefined &&
      !/\s/.test(value[cursor + 2])
    ) {
      const contentStart = cursor + 2;
      const close = findStrongClose(value, contentStart);
      if (close >= 0) {
        flushPlain(cursor);
        tokens.push({ kind: "strong", value: value.slice(contentStart, close) });
        cursor = close + 2;
        plainStart = cursor;
        continue;
      }
      cursor += 2;
      continue;
    }
    cursor++;
  }
  flushPlain(value.length);
  return tokens;
}

/** The thought as plain words, markers dropped (a row's hover text), from the tokens it is drawn with. */
export const thinkingPlainText = (tokens: readonly ThinkingTextToken[]): string => tokens.map((t) => t.value).join("");
