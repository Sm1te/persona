// A stand-in for the SDK's query() (PERSONA_AGENT_SDK_FAKE): it answers each user message with a stream_event, an
// assistant and a result, reports each Query method call as a fake_call message, and, resumed, first sends again
// the transcript's assistant (by uuid) and a fresh result the way the CLI does.

import { randomUUID } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import { dirname } from 'node:path'

export function query({ prompt, options }) {
  const out = []
  let wake = null
  let closed = false
  const send = (m) => {
    out.push(m)
    if (wake) { const w = wake; wake = null; w() }
  }
  const call = (name, arg) => {
    if (arg === 'boom') throw new Error(`boom with ${process.env.ANTHROPIC_AUTH_TOKEN}`)
    send({ type: 'fake_call', name, arg })
    return name === 'interrupt' ? { still_queued: [] } : undefined
  }

  const { env, stderr, ...rest } = options
  let file
  if (typeof options.settings === 'string') {
    file = {
      mode: statSync(options.settings).mode & 0o777,
      dir_mode: statSync(dirname(options.settings)).mode & 0o777,
      content: JSON.parse(readFileSync(options.settings, 'utf8')),
    }
  }
  // An in-process MCP server is a live object graph, not a stdio config. Record
  // its boundary for the bridge tests instead of serializing the SDK instance.
  const mcpServers = Object.fromEntries(Object.entries(rest.mcpServers ?? {}).map(([name, server]) => [name,
    server?.type === 'sdk'
      ? { type: 'sdk', name: server.name }
      : server,
  ]))
  send({ type: 'fake_options', options: { ...rest, mcpServers }, settings_file: file, env_keys: Object.keys(env ?? {}).length, has_stderr: typeof stderr === 'function' })
  stderr?.(`cli says ${process.env.ANTHROPIC_AUTH_TOKEN} at ${process.env.ANTHROPIC_BASE_URL}\n`)
  if (options.resume) {
    send({ type: 'assistant', uuid: process.env.FAKE_OLD_UUID, session_id: options.resume, message: { content: [{ type: 'text', text: 'old' }] } })
    send({ type: 'result', subtype: 'success', uuid: randomUUID(), num_turns: 0, result: 'old', session_id: options.resume })
  }

  let inputEnded = false
  ;(async () => {
    for await (const m of prompt) {
      send({ type: 'stream_event', event: { type: 'message_start' } })
      if (process.env.FAKE_TURN_MS) await new Promise((r) => setTimeout(r, Number(process.env.FAKE_TURN_MS)))
      if (closed) return
      send({ type: 'assistant', uuid: randomUUID(), echo: m })
      send({ type: 'result', subtype: 'success', uuid: randomUUID(), num_turns: 1, result: 'ok' })
    }
    inputEnded = true
    if (process.env.FAKE_STAY_OPEN !== '1') send(null)
  })()

  return {
    interrupt: () => call('interrupt'),
    setModel: (m) => call('setModel', m),
    setPermissionMode: (m) => call('setPermissionMode', m),
    applyFlagSettings: (s) => call('applyFlagSettings', s),
    initializationResult: async () => ({ commands: [] }),
    close: () => {
      closed = true
      send({ type: 'fake_call', name: 'close' })
      send(null)
    },
    async *[Symbol.asyncIterator]() {
      for (;;) {
        while (!out.length) await new Promise((r) => { wake = r })
        const m = out.shift()
        if (m === null) return
        yield m
        if (closed && !out.length) return
      }
    },
  }
}
