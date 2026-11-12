# omp (Oh My Pi)

omp (Oh My Pi) is a pi fork with the same extension API, session files and conversation model. Persona reads it as a pi-family
agent (`docs/integrations/pi.md`). In a herdr pane with the Persona extension installed, omp appears in Agents with its state and
chat (text, thinking, tool calls and results, images) and token-by-token output. From the phone you can send messages, interrupt,
rename, start a new omp with a first message, answer its `ask_user` questions and approve the tools you list.

Verified with omp 18.18.x and herdr 0.9.5. Everything here needs a herdr engine.

Being added in steps (issue #4148). Not here yet: listing stopped omp sessions from disk, resuming them, and starting omp from the
iOS New Agent sheet with its first message.

## Scope

| Works from the phone | Not supported |
| --- | --- |
| Start `omp` in a new herdr tab, with the first message | Choosing its model, thinking effort, permission mode or ultracode (omp's own `config.yml` decides; `omp_options`) |
| List running omp sessions and their state | Engines other than herdr, and Windows |
| Read the chat, including live text | A web client for omp (the equivalent of pi-web) |
| Send messages, interrupt, rename | Sessions under an `omp --profile` directory |
| Answer `ask_user` questions and `approve` cards | Automatic updates of the Persona extension |

Persona never installs or edits herdr's omp integration and never writes into omp's agent directory. Only the extension below is
yours to install.

## Installation

On the computer running omp (in the hub's machine list, with a node that can run omp):

1. Install the Persona extension in omp from this checkout:

   ```sh
   omp install <Persona>/integrations/omp
   ```

   `omp install` links a local directory instead of copying it, so the extension follows the checkout. To update it, update the
   checkout and restart omp; there is no automatic update. Install only this package into omp: the pi extension in the same omp
   would report the same sessions under pi's identity, and the node keeps one owner per session.
2. herdr 0.9.5 recognizes `omp` panes by itself. herdr's own omp integration (`herdr integration install omp`) is optional: it
   reports the session file of the pane, which gives the node the session id sooner.
3. Restart omp. Its sessions reach the node from then on; no companion process is needed.

### Settings

Optional, in `<agent dir>/persona.json`. That is `~/.omp/agent/persona.json` by default, or `$PI_CODING_AGENT_DIR/persona.json`
when omp keeps its agent directory elsewhere (omp reads the same variable):

```json
{
  "approve": ["bash", "write"],
  "askUser": true,
  "answerFirst": "auto"
}
```

| Field | Default | Description |
| --- | --- | --- |
| `approve` | `[]` | Tools whose calls ask for approval on the phone before they run, such as `["bash", "write"]`. omp has no permission dialog of its own, so nothing is blocked by default |
| `askUser` | `true` | Gives the model an `ask_user` tool (a question, optionally with choices), answered on the phone or in omp's terminal |
| `answerFirst` | `auto` | Who gets approvals and questions first. `auto`: the phone if it drove this session within the last 6 hours, otherwise local first. `phone` and `local` fix one side |
| `socket` | `~/.persona/runtime.sock` | The node's runtime socket, when it is not the default. A full path; a leading `~` is not expanded |

## How a session is identified

The node takes an omp session's id from the first source that has it:

1. **herdr's omp integration**, which reports the session file it wrote for the pane (`agent_session {kind: "path"}`). The id is
   the session id in that file.
2. **The Persona extension in that omp process**, which lists the session with the pane it runs in (`pane_id`, from
   `HERDR_PANE_ID`). The node joins that runtime row and herdr's pane into one session row.
3. **A fallback id**, `omp-<pane>` (for example `omp-w1-p2`), when neither has answered 8 seconds after herdr reports the pane.
   The row is usable at once. When the real id arrives, the node aliases the fallback id to it, so an open chat keeps working
   and the list keeps one row. Closing the pane drops the alias.

Without the extension a running omp is still listed, with its terminal only. The chat explains that the extension is missing
(`omp install <Persona>/integrations/omp`), and a send is refused with `omp_no_extension`.

## Differences from pi

- **No pi-web and no companion process.** omp has no web client; its sessions live in its own process.
- **Its own agent directory.** `~/.omp/agent`, or `$PI_CODING_AGENT_DIR` when set. pi's `~/.pi/agent` is never read.
- **Its own runtime identity and slot.** The runtime is `omp-<pid>`, the apps' name and agent are `omp`, and card ids start with
  `omp`. The extension keeps its host in `__personaOmp` (`integrations/pi` uses `__personaPi`), so an omp and a pi loaded in the
  same process do not collide.
- **Its own resume command.** `omp --resume <session file>`, where pi uses `pi --session`.

Everything else is pi's code under omp's identity: the extension API, the conversation mapping, approval and question handling,
live text, and the host's record of where a session last ran (`integrations/pi/lib/pi.js`).

## Known limitations

- Only omp's default agent directory is read. Sessions started with `omp --profile <name>` live elsewhere.
- Only the launcher named exactly `omp` starts omp. A wrapper script or alias around omp is refused as an omp start.
- A start with a model, permission mode, thinking effort or ultracode is refused with `omp_options` before anything runs.
- `omp -p`, `--print` and `--mode json` do not register with Persona: a one-shot run has nothing to drive from the phone.

## Validation

- `cd integrations/omp && npm test`: agent directory and `persona.json` parsing, the omp identity (`agent`, `name`, runtime
  `omp-<pid>`), the `ask_user` and one-shot rules, and the slot shared with pi in one process.
- `cd integrations/pi && npm test`: the configurable host identity.
- Node, with fake herdr and fake runtimes: `server/internal/node/omp_row_test.go` covers the family's rows, the missing extension
  and the fallback row; `server/internal/node/omp_alias_test.go` covers the wait for a session id and the alias.
