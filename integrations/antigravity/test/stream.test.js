import assert from 'node:assert/strict'
import { appendFileSync, chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createAgyRuntime } from '../lib/runtime.js'

const FAKE = fileURLToPath(new URL('./fake-acp-stream.mjs', import.meta.url))
const RAW = '11111111-2222-4333-8444-555555555555'
const SESSION = `agy-${RAW}`

async function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'agy-stream-'))
  writeFileSync(join(directory, 'updates.jsonl'), '')
  writeFileSync(join(directory, 'calls.jsonl'), '')
  const binary = join(directory, 'agy')
  writeFileSync(binary, `#!/bin/sh\nexec '${process.execPath}' '${FAKE}' '${directory}' "$@"\n`)
  chmodSync(binary, 0o700)
  mkdirSync(join(directory, 'config/brain', RAW), { recursive: true })
  const node = await startFakeNode(t)
  const runtime = createAgyRuntime({ agyBin: binary, agyDir: join(directory, 'config'), home: directory, socketPath: node.path, log: () => {} })
  t.after(async () => { await runtime.stop(); rmSync(directory, { recursive: true, force: true }) })
  runtime.start()
  await node.ready
  await node.peer.call('session.start', { cwd: directory })
  await node.peer.call('session.watch', { session_id: SESSION })
  const calls = () => readFileSync(join(directory, 'calls.jsonl'), 'utf8').split('\n').filter(Boolean).map(JSON.parse)
  const update = (value) => appendFileSync(join(directory, 'updates.jsonl'), `${JSON.stringify(value)}\n`)
  const chunk = async (text, expected = text) => {
    update({ sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } })
    await until(`live text ${expected}`, () => node.last('session.delta')?.text === expected, 15_000)
  }
  const promptCount = (count) => until(`${count} prompts started`, () => calls().filter((r) => r.method === 'session/prompt').length === count, 15_000)
  return { node, calls, update, chunk, promptCount }
}

test('AGY streams one assistant message at a time across tool calls', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'Make the change.' })
  await promptCount(1)
  await chunk('Preparing ')
  await chunk('the change.', 'Preparing the change.')
  update({ sessionUpdate: 'tool_call', toolCallId: 'tool-1', title: 'Read' })
  update({ sessionUpdate: 'tool_call_update', toolCallId: 'tool-1', status: 'completed' })
  await chunk('The change is ')
  await chunk('complete.', 'The change is complete.')
  update({ end: true })
  await until('live text cleared at completion', () => node.last('session.delta')?.text === '', 15_000)
})

test('a queued prompt does not receive the executing prompt text and starts with an empty stream', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('First reply')
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await chunk(' complete.', 'First reply complete.')
  update({ end: true })
  await promptCount(2)
  await until('queued prompt clears the previous text', () => node.last('session.delta')?.text === '', 15_000)
  await chunk('Second reply.')
  update({ end: true })
  await until('second prompt completes', () => node.last('session.delta')?.text === '', 15_000)
})

test('each prompt clears any live text before its first response', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('First reply.')
  update({ end: true })
  await until('first prompt finishes', () => node.last('session.delta')?.text === '', 15_000)
  const before = node.all('session.delta').length
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await promptCount(2)
  await until('new prompt publishes its empty boundary', () => node.all('session.delta').length > before, 15_000)
  assert.equal(node.last('session.delta').text, '')
  await chunk('First reply.')
  assert.equal(node.last('session.delta').text, 'First reply.', 'a new turn may legitimately repeat earlier words')
  update({ end: true })
})

test('AGY completion retires the cached reply after the viewer leaves', { timeout: 20_000 }, async (t) => {
  const { node, update, chunk, promptCount } = await fixture(t)
  await node.peer.call('session.send', { session_id: SESSION, text: 'First request.' })
  await promptCount(1)
  await chunk('Previous reply.')
  await node.peer.call('session.unwatch', { session_id: SESSION })
  update({ end: true })
  await until('unwatched completion clears the reply', () => node.last('session.delta')?.text === '', 2000)
  await node.peer.call('session.watch', { session_id: SESSION })
  await node.peer.call('session.send', { session_id: SESSION, text: 'Second request.' })
  await promptCount(2)
  assert.equal(node.last('session.delta').text, '')
  await chunk('New reply.')
  update({ end: true })
})
