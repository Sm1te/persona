// A stopped Agent's chat (#4754, docs/web/web.md "Terminal ⇄ chat"): it keeps its conversation and its terminal and takes
// no messages, so the composer's place holds one read-only line with the two ways on: its Terminal, and Resume (which
// opens the terminal too, where the resumed Agent shows up). Resume's progress and error are shared with the terminal's
// own Resume (lib/agent-resume.ts).
import { t } from "../../i18n";
import { resumeAgent, useResume } from "../../lib/agent-resume";
import { Button } from "../../ui/atoms";
import { Icon } from "../../ui/icons";

export function StoppedAgentLine({ mid, sid, onTerminal }: { mid: string; sid: string; onTerminal: () => void }) {
  const resume = useResume(mid, sid);
  return (
    <div role="status" data-stopped-agent className="flex flex-wrap items-center gap-x-2 gap-y-1.5 rounded-card bg-inset px-3 py-2 text-xs text-ink-2">
      <Icon name="stop" size={13} className="shrink-0 text-ink-3" />
      <span className="min-w-0 flex-1 basis-48 break-words">{t("chat-chatview.stopped_line")}</span>
      <span className="flex shrink-0 items-center gap-1.5">
        <Button tone="quiet" icon="terminal" onClick={onTerminal}>{t("chat-chatview.terminal")}</Button>
        <Button
          tone="secondary"
          icon="play"
          busy={resume.phase === "resuming"}
          onClick={() => {
            onTerminal();
            void resumeAgent(mid, sid);
          }}
        >
          {t("chat-chatview.resume")}
        </Button>
      </span>
      {resume.phase === "failed" && <p role="alert" className="w-full break-words text-red">{t("chat-chatview.resume_failed", { error: resume.error })}</p>}
    </div>
  );
}
