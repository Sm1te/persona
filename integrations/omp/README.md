# Persona for omp

Puts the sessions of an omp (Oh My Pi) process into the Persona apps through the Persona node running on this computer:
the conversation, live text, sending, interrupting, renaming, the `ask_user` question cards and the tool approvals, from
the phone. Without it an omp in a herdr pane is still listed, with its terminal only.

omp reads pi extensions through its compatibility layer, so this package is the pi extension
(`integrations/pi`) running under omp's identity: runtime `omp-<pid>` and card ids starting with `omp`. Details:
`docs/integrations/omp.md`.

## Install

From this checkout:

```sh
omp install /path/to/persona/integrations/omp
```

`omp install` links a local path; it does not copy it, so the extension follows the checkout. Install **only this
package** into omp: `integrations/pi` would report the same sessions under pi's identity, and the node keeps one owner.

Verified with omp 18.18.x and herdr 0.9.5.

## Settings

Optional, in `<agent dir>/persona.json` — `~/.omp/agent/persona.json` by default, or `$PI_CODING_AGENT_DIR/persona.json`
when omp is told to keep its agent directory elsewhere (the same variable omp reads):

```json
{
  "approve": ["bash", "write"],
  "askUser": true,
  "answerFirst": "auto",
  "socket": "/home/me/.persona/runtime.sock"
}
```

| Key | Meaning |
| --- | --- |
| `approve` | tools whose calls ask the phone before they run |
| `askUser` | register the `ask_user` tool, whose questions become cards on the phone (default `true`) |
| `answerFirst` | `auto`, `phone` or `local`: where a card is answered first |
| `socket` | the node's runtime socket, when it is not `~/.persona/runtime.sock` |

## Without a node

The extension stays quiet when no Persona node is listening: omp runs as it always does.

## Notes

- Only the default agent directory is read. An omp started with another profile keeps its sessions elsewhere.
- Only the launcher named exactly `omp` starts omp.
- There is no auto-update for this package: it is installed from a checkout and follows it.
