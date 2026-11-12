// The Paseo bridge's process (docs/integrations/paseo.md): the node runs it while the machine's engine is Paseo —
//   node bridge.mjs --socket <runtime.sock> --paseo-home <Paseo's home> [--mcp-config <the node's mcp.json>]
//     [--allow-tools <the Persona tools its Agents call without a card, comma-separated>]
// and it runs until the node stops it, or its stdin (held open by the node) ends: a node that dies leaves none behind.

import { parseArgs } from 'node:util'
import { createPaseoBridge } from './lib/bridge.js'
import { paseoHome } from './lib/discover.js'

const { values } = parseArgs({
  options: {
    socket: { type: 'string' },
    'paseo-home': { type: 'string' },
    'mcp-config': { type: 'string' },
    'allow-tools': { type: 'string' },
  },
  strict: true,
})

const log = (msg) => process.stderr.write(`${msg}\n`)
const allowTools = (values['allow-tools'] ?? '').split(',').filter(Boolean)
const bridge = createPaseoBridge({ log }, { socketPath: values.socket || undefined, home: values['paseo-home'] || paseoHome(),
  mcpConfig: values['mcp-config'] || undefined, allowTools })
bridge.start()

let stopping = false
function stop(why) {
  if (stopping) return
  stopping = true
  log(`stopping (${why})`)
  bridge.stop()
  setTimeout(() => process.exit(0), 300).unref()
}
process.on('SIGTERM', () => stop('SIGTERM'))
process.on('SIGINT', () => stop('SIGINT'))
// Run by the node, stdin is a pipe it holds; from a terminal it never ends.
if (!process.stdin.isTTY) {
  process.stdin.on('end', () => stop('the node is gone'))
  process.stdin.on('error', () => stop('the node is gone'))
  process.stdin.resume()
}
process.on('uncaughtException', (err) => {
  log(`uncaught: ${err.stack ?? err.message}`)
  process.exit(1)
})
