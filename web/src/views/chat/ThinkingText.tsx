// A thought's words with their lightweight inline emphasis (lib/thinking-text.ts, after cindy's ThinkingText.tsx): paired
// `**strong**` in medium weight, backtick code in monospace and upright; nothing else is interpreted.
// The caller tokenizes (tokenizeThinkingText) once and hands the tokens here, so its hover text reads the same ones.
import { Fragment, memo } from "react";
import type { ThinkingTextToken } from "../../lib/thinking-text";

export const ThinkingText = memo(function ThinkingText({ tokens }: { tokens: readonly ThinkingTextToken[] }) {
  return (
    <>
      {tokens.map((token, i) =>
        token.kind === "strong" ? (
          <span key={i} className="font-medium" data-thinking-markup="strong">
            {token.value}
          </span>
        ) : token.kind === "code" ? (
          <code key={i} className="font-mono not-italic" data-thinking-markup="code">
            {token.value}
          </code>
        ) : (
          <Fragment key={i}>{token.value}</Fragment>
        ),
      )}
    </>
  );
});
