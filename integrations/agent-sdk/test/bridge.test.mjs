// The bridge's protocol (README.md), on the built dist/bridge.mjs with the SDK's query() replaced by fake-sdk.mjs.

import assert from 'node:assert/strict'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { afterEach, test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { projectDir } from '../src/lib.mjs'
import { spawnBridge } from './harness.mjs'

const FAKE = fileURLToPath(new URL('./fake-sdk.mjs', import.meta.url))
const TOKEN = 'sk-test-0123456789abcdef'
const BASE = 'https://gateway.invalid/anthropic'

// A test that fails before end() would leave its bridge waiting on stdin, and node --test waiting on it.
const running = new Set()
afterEach(() => { for (const b of running) b.kill('SIGKILL') })

function bridge(env = {}) {
  const b = spawnBridge({ env: { ...process.env, PERSONA_AGENT_SDK_FAKE: FAKE, ANTHROPIC_AUTH_TOKEN: TOKEN, ANTHROPIC_BASE_URL: BASE, ...env } })
  running.add(b)
  b.exited.then(() => running.delete(b))
  return b
}

const start = (options = {}) => ({ type: 'persona_start', options: { cwd: tmpdir(), ...options } })
const user = (text) => ({ type: 'user', message: { role: 'user', content: text } })
const ctl = (id, request) => ({ type: 'control_request', request_id: id, request })

test('start: options reach query(), then ready', async () => {
  const b = bridge()
  b.send(start({ model: 'claude-haiku-4-5', permissionMode: 'default', tools: ['Bash', 'Read', 'Monitor', 'Skill', 'Agent'], strictMcpConfig: true, settings: { env: { A: '1' } }, bogus: 1, includePartialMessages: false }))
  const ready = await b.until((m) => m.type === 'persona_bridge' && m.event === 'ready')
  assert.match(ready.sdk_version, /^\d+\.\d+\.\d+$/)
  const opts = await b.until((m) => m.type === 'fake_options')
  assert.equal(opts.options.model, 'claude-haiku-4-5')
  assert.equal(opts.options.includePartialMessages, true)
  assert.deepEqual(opts.options.tools, ['Bash', 'Read', 'Monitor', 'Skill', 'Agent'])
  assert.equal(opts.options.strictMcpConfig, true)
  assert.equal(opts.options.bogus, undefined)
  assert.ok(opts.env_keys > 0, 'the bridge passes its env')
  assert.ok(opts.has_stderr)
  assert.match(b.stderr, /ignored option bogus/)
  assert.equal(await b.end(), 0)
})

test('guest: only the stdio server\'s declared tools cross the SDK boundary', async () => {
  const b = bridge()
  b.send(start({
    guestMcpServers: { persona: {
      command: process.execPath,
      args: [fileURLToPath(new URL('./guest-mcp-server.mjs', import.meta.url))],
      env: { PERSONA_GUEST: '1', PERSONA_SESSION_ID: 'guest-test' },
    } },
    mcpServers: {}, allowedTools: ['mcp__persona__exampledocs_search'], strictMcpConfig: true,
  }))
  await b.until((m) => m.event === 'ready')
  const opts = await b.until((m) => m.type === 'fake_options')
  assert.deepEqual(opts.options.tools, [])
  assert.deepEqual(opts.options.settingSources, [])
  assert.equal(opts.options.strictMcpConfig, true)
  assert.equal(opts.options.guestMcpServers, undefined)
  assert.equal(opts.options.mcpServers.persona.type, 'sdk')
  assert.equal(opts.options.mcpServers.persona.name, 'persona')
  assert.equal(await b.end(), 0)
})

test('guest: an empty upstream tool directory fails instead of starting unarmed', async () => {
  const b = bridge()
  b.send(start({ guestMcpServers: { persona: {
    command: process.execPath,
    args: [fileURLToPath(new URL('./guest-mcp-server.mjs', import.meta.url))],
    env: { EMPTY_TOOLS: '1' },
  } } }))
  const failure = await b.until((m) => m.event === 'error')
  assert.match(failure.error, /advertised no tools/)
  assert.equal(await b.exited, 1)
})

test('settings reach query() as a private file, removed at exit', async () => {
  for (const how of ['stdin', 'SIGTERM']) {
    const b = bridge()
    b.send(start({ settings: { env: { ANTHROPIC_AUTH_TOKEN: TOKEN }, hooks: {} } }))
    await b.until((m) => m.event === 'ready')
    const opts = await b.until((m) => m.type === 'fake_options')
    assert.equal(typeof opts.options.settings, 'string', 'settings went as an object (onto the command line)')
    assert.deepEqual(opts.settings_file, { mode: 0o600, dir_mode: 0o700, content: { env: { ANTHROPIC_AUTH_TOKEN: TOKEN }, hooks: {} } })
    assert.ok(existsSync(opts.options.settings))
    if (how === 'stdin') assert.equal(await b.end(), 0)
    else {
      b.kill('SIGTERM')
      await b.exited
    }
    assert.ok(!existsSync(dirname(opts.options.settings)), `${how}: the settings directory is still there`)
  }
})

test('a first line that is not persona_start fails', async () => {
  const b = bridge()
  b.send(user('hi'))
  const err = await b.until((m) => m.event === 'error')
  assert.match(err.error, /persona_start/)
  assert.equal(await b.exited, 2)
})

test('user lines go into the input stream; SDK messages come out as they are', async () => {
  const b = bridge()
  b.send(start(), user('one'), user('two'))
  await b.until(() => b.msgs.filter((m) => m.type === 'result').length === 2)
  const echoes = b.msgs.filter((m) => m.type === 'assistant').map((m) => m.echo)
  assert.deepEqual(echoes, [
    { parent_tool_use_id: null, type: 'user', message: { role: 'user', content: 'one' } },
    { parent_tool_use_id: null, type: 'user', message: { role: 'user', content: 'two' } },
  ])
  assert.equal(b.msgs.filter((m) => m.type === 'result').length, 2)
  assert.ok(b.msgs.some((m) => m.type === 'stream_event'))
  await b.end()
})

test('each control request calls its Query method and is answered like claude -p', async () => {
  const b = bridge()
  b.send(start(),
    ctl('r1', { subtype: 'interrupt' }),
    ctl('r2', { subtype: 'set_permission_mode', mode: 'plan' }),
    ctl('r3', { subtype: 'set_model', model: 'claude-sonnet-4-5' }),
    ctl('r4', { subtype: 'apply_flag_settings', settings: { effortLevel: 'high' } }),
    ctl('r5', { subtype: 'rewind_files' }),
    ctl('r6', { subtype: 'set_model', model: 'boom' }))
  const resp = {}
  for (const id of ['r1', 'r2', 'r3', 'r4', 'r5', 'r6']) {
    resp[id] = (await b.until((m) => m.type === 'control_response' && m.response.request_id === id)).response
  }
  assert.deepEqual(resp.r1, { subtype: 'success', request_id: 'r1', response: { still_queued: [] } })
  for (const id of ['r2', 'r3', 'r4']) assert.deepEqual(resp[id], { subtype: 'success', request_id: id })
  assert.equal(resp.r5.subtype, 'error')
  assert.match(resp.r5.error, /unsupported control request: rewind_files/)
  assert.equal(resp.r6.subtype, 'error')
  assert.match(resp.r6.error, /boom with \*\*\*/)
  // Each control_response is emitted as soon as the Query method's promise settles, while the matching fake_call
  // still has to travel the SDK output iterator, so a response can precede its call. Wait for all four real calls
  // (r5 is unsupported, r6 throws before recording a call) before asserting their order.
  await b.until((m) => m.type === 'fake_call' && m.name === 'applyFlagSettings')
  const calls = b.msgs.filter((m) => m.type === 'fake_call').map((m) => [m.name, m.arg])
  assert.deepEqual(calls.slice(0, 4), [
    ['interrupt', undefined],
    ['setPermissionMode', 'plan'],
    ['setModel', 'claude-sonnet-4-5'],
    ['applyFlagSettings', { effortLevel: 'high' }],
  ])
  await b.end()
})

test('resume: what the transcript had, and a result before anything streamed, are dropped', async () => {
  const home = mkdtempSync(join(tmpdir(), 'agent-sdk-test-'))
  const cwd = join(home, 'work dir')
  const id = '11111111-2222-4333-8444-555555555555'
  const old = 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee'
  mkdirSync(projectDir(home, cwd), { recursive: true })
  writeFileSync(join(projectDir(home, cwd), `${id}.jsonl`), `${JSON.stringify({ type: 'assistant', uuid: old })}\n`)
  try {
    const b = bridge({ CLAUDE_CONFIG_DIR: home, FAKE_OLD_UUID: old })
    b.send(start({ cwd, resume: id }))
    await b.until((m) => m.event === 'ready')
    b.send(user('again'))
    await b.until((m) => m.type === 'result')
    await b.end()
    assert.equal(b.msgs.filter((m) => m.uuid === old).length, 0, 'the old assistant was sent again')
    const results = b.msgs.filter((m) => m.type === 'result')
    assert.equal(results.length, 1)
    assert.equal(results[0].num_turns, 1)
    assert.ok(b.msgs.some((m) => m.type === 'assistant' && m.echo), 'the live turn came through')
  } finally {
    rmSync(home, { recursive: true, force: true })
  }
})

test('without resume nothing is dropped', async () => {
  const b = bridge({ FAKE_OLD_UUID: 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee' })
  b.send(start(), user('x'))
  await b.until((m) => m.type === 'result')
  await b.end()
  assert.equal(b.msgs.filter((m) => m.type === 'result').length, 1)
})

test('closing stdin waits for the running turn, closes the query and exits', async () => {
  const b = bridge({ FAKE_STAY_OPEN: '1', FAKE_TURN_MS: '300' })
  b.send(start(), user('last'))
  await b.until((m) => m.type === 'stream_event')
  assert.equal(await b.end(), 0)
  const types = b.msgs.map((m) => m.type === 'persona_bridge' ? m.event : m.type)
  assert.ok(types.includes('result') && types.indexOf('result') < types.indexOf('fake_call'), `closed before the result: ${types}`)
  assert.equal(b.msgs.find((m) => m.type === 'fake_call')?.name, 'close')
  assert.equal(types.at(-1), 'exit')
})

test('closing stdin of an idle bridge exits', async () => {
  const b = bridge({ FAKE_STAY_OPEN: '1' })
  b.send(start())
  await b.until((m) => m.event === 'ready')
  assert.equal(await b.end(), 0)
  assert.equal(b.msgs.at(-1).event, 'exit')
})

test('no env value reaches stderr or an error', async () => {
  const b = bridge()
  b.send(start(), ctl('x', { subtype: 'set_model', model: 'boom' }), user('hi'))
  await b.until((m) => m.type === 'control_response')
  await b.until((m) => m.type === 'result')
  await b.end()
  assert.match(b.stderr, /cli says \*\*\* at \*\*\*/)
  assert.ok(!b.stderr.includes(TOKEN) && !b.stderr.includes(BASE), b.stderr)
  assert.ok(!b.stdoutRaw.includes(TOKEN), 'stdout carries the token')
})
