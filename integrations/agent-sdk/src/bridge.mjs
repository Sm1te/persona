// The assistant's main conversation on the Claude Agent SDK (README.md): the node runs
//   node bridge.mjs
// in the assistant's directory with the provider's env, in place of `claude -p --input-format stream-json`, and talks
// to it the same way. stdin: persona_start first, then user and control_request lines; stdout: every SDK message as
// it comes, and the bridge's own persona_bridge events; stderr: the bridge's log, without env values.

import { rmSync } from 'node:fs'
import { createInterface } from 'node:readline'
import { pathToFileURL } from 'node:url'
import { query as sdkQuery } from '@anthropic-ai/claude-agent-sdk'
import { Inbox, ReplayGuard, control, queryOptions, redactor, settingsFile, sweepSettings, transcriptUUIDs, userMessage } from './lib.mjs'
import { guestMcpServer } from './guest-mcp.mjs'

// A signal ends the process through exit: the exit handlers (the settings file's removal) run.
for (const sig of ['SIGTERM', 'SIGINT', 'SIGHUP']) process.on(sig, () => process.exit(143))

// eslint-disable-next-line no-undef
const SDK_VERSION = typeof __SDK_VERSION__ === 'string' ? __SDK_VERSION__ : 'dev' // esbuild defines it (build.mjs)

const redact = redactor(process.env)
const log = (msg) => process.stderr.write(`agent-sdk bridge: ${redact(msg)}\n`)
const emit = (obj) => process.stdout.write(`${JSON.stringify(obj)}\n`)
const event = (name, extra = {}) => emit({ type: 'persona_bridge', event: name, sdk_version: SDK_VERSION, ...extra })

function fail(why, code = 1) {
  event('error', { error: redact(why) })
  log(why)
  process.exit(code)
}

// PERSONA_AGENT_SDK_FAKE: a module whose query() stands in for the SDK's (the tests).
const query = process.env.PERSONA_AGENT_SDK_FAKE
  ? (await import(pathToFileURL(process.env.PERSONA_AGENT_SDK_FAKE).href)).query
  : sdkQuery

const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })[Symbol.asyncIterator]()

const first = await lines.next()
if (first.done) process.exit(0) // the node went away before starting
let start
try {
  start = JSON.parse(first.value)
} catch {
  fail('the first line is not JSON', 2)
}
if (start?.type !== 'persona_start') fail(`the first line is ${start?.type ?? 'untyped'}, not persona_start`, 2)
const options = queryOptions(start.options, process.env, log)
options.stderr = (data) => { process.stderr.write(redact(data)) }
if (options.settings && typeof options.settings === 'object') {
  // Its env holds the key: claude gets the file's path, not the JSON on its command line.
  sweepSettings()
  const { dir, path } = settingsFile(options.settings)
  options.settings = path
  process.on('exit', () => rmSync(dir, { recursive: true, force: true }))
}
const cwd = options.cwd ?? process.cwd()
let closeGuestMcp = async () => {}
if (options.guestMcpServers) {
  let guest
  try {
    guest = await guestMcpServer(options.guestMcpServers.persona)
  } catch (err) {
    fail(`guest MCP: ${err?.message ?? err}`)
  }
  closeGuestMcp = guest.close
  options.mcpServers = { ...options.mcpServers, persona: guest.server }
  // The SDK documents [] as no built-ins. Register the stdio server's declared
  // tools in-process: a CLI --tools selection filters MCP tools too.
  options.tools = []
  options.settingSources = []
  delete options.guestMcpServers
  process.on('exit', () => { void closeGuestMcp().catch(() => {}) })
}
const seen = options.resume ? transcriptUUIDs(options.resume, cwd) : null
if (options.resume && !seen) log('no transcript found for the resumed session: only replayed results are dropped')
const guard = new ReplayGuard(seen, Boolean(options.resume))

const inbox = new Inbox()
let q
try {
  q = query({ prompt: inbox, options })
} catch (err) {
  fail(`query: ${err?.message ?? err}`)
}

// A turn runs from a user line to its result; closing stdin waits for it (the node's last message gets its answer).
let turns = 0
let inputDone = false
let closing = false
const closeWhenIdle = () => {
  if (inputDone && turns === 0 && !closing) {
    closing = true
    q.close()
  }
}

const output = (async () => {
  for await (const msg of q) {
    if (!guard.keep(msg, turns > 0)) {
      log(`dropped a replayed ${msg.type}${msg.subtype ? `/${msg.subtype}` : ''}`)
      continue
    }
    emit(msg)
    if (msg.type === 'system' && msg.subtype === 'init' && options.tools?.length === 0 && options.mcpServers?.persona?.type === 'sdk') {
      log(`guest tools: ${JSON.stringify({ tools: msg.tools ?? [], mcp_servers: msg.mcp_servers ?? [] })}`)
    }
    if (msg.type === 'result' && turns > 0) {
      turns--
      closeWhenIdle()
    }
  }
})()

// ready as soon as the query is up: the node already knows claude's version, a `claude --version` here only delays it.
Promise.resolve(q.initializationResult?.()).then(
  () => event('ready'),
  (err) => fail(`start: ${err?.message ?? err}`),
)

;(async () => {
  for (;;) {
    const { value, done } = await lines.next()
    if (done) break
    if (!value.trim()) continue
    let line
    try {
      line = JSON.parse(value)
    } catch {
      log('a stdin line is not JSON')
      continue
    }
    if (line.type === 'user') {
      turns++
      inbox.push(userMessage(line))
    } else if (line.type === 'control_request') {
      control(q, line, redact).then(emit)
    } else log(`ignored a ${line.type} line`)
  }
  inputDone = true
  inbox.end()
  closeWhenIdle()
})()

try {
  await output
} catch (err) {
  // close() ends the iteration with an abort; anything else is the SDK's or the CLI's failure.
  if (!closing) fail(`query: ${err?.message ?? err}`)
}
event('exit')
await closeGuestMcp().catch(() => {})
process.exit(0)
