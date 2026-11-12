import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { startFakeNode, until } from '../../runtime-sdk/testing/fake-node.js'
import { createPaseoBridge, TIMELINES_MAX, uuidOf } from '../lib/bridge.js'
import { PaseoClient } from '../lib/paseo.js'
import { agentSnapshot, entry, mcpServersError, startFakePaseo } from './fake-paseo.js'

const SID = 'aaaaaaaa-0000-4000-8000-000000000001'
const SID2 = 'aaaaaaaa-0000-4000-8000-000000000002'
const FEATURES = ['prefer_running', 'resume_argv', 'handover', 'transcript', 'launchers', 'start_params', 'modes', 'resume']

const PROVIDERS = { claude: { enabled: false }, 'my-claude': { extends: 'claude', label: 'My Claude' }, 'my-codex': { extends: 'codex' }, grok: { extends: 'acp', label: 'Grok' } }
const ENTRIES = [
  { provider: 'claude', enabled: false, status: 'unavailable', models: [] },
  { provider: 'my-claude', enabled: true, status: 'ready', label: 'My Claude', models: [
    { id: 'fable[1m]', label: 'Fable', isDefault: true, thinkingOptions: [{ id: 'low' }, { id: 'high' }, { id: 'max' }], defaultThinkingOptionId: 'max' },
  ] },
  { provider: 'my-codex', enabled: true, status: 'ready', label: 'My Codex', models: [{ id: 'gpt', label: 'GPT', isDefault: true }] },
  { provider: 'grok', enabled: true, status: 'ready', label: 'Grok', models: [] },
]
const PROJECT = { projectKey: 'prj_1', projectName: 'proj', workspaceName: '主线', checkout: { cwd: '/tmp', isGit: false } }

function agents() {
  const claude = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: 'A', status: 'idle', lastUserMessageAt: '2026-11-04T00:00:00Z',
    persistence: { provider: 'my-claude', sessionId: SID, metadata: { provider: 'claude' } }, runtimeInfo: { provider: 'claude', sessionId: SID },
    availableModes: [{ id: 'default' }, { id: 'acceptEdits' }, { id: 'plan' }, { id: 'bypassPermissions' }], model: 'fable[1m]' })
  const codex = agentSnapshot({ provider: 'my-codex', cwd: '/tmp', title: 'B', status: 'running', persistence: { provider: 'my-codex', sessionId: 'th-1', metadata: { provider: 'codex' } } })
  const grok = agentSnapshot({ provider: 'grok', cwd: '/tmp', title: 'C', status: 'closed' })
  const fresh = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: null })
  const labeled = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: 'E', labels: { 'persona.session': 'paseo-e1' },
    persistence: { provider: 'my-claude', sessionId: SID2, metadata: { provider: 'claude' } } })
  return { claude, codex, grok, fresh, labeled }
}

async function setup(t, o = {}) {
  const a = agents()
  const fake = await startFakePaseo(t, { providers: PROVIDERS, entries: ENTRIES, agents: Object.values(a).map((agent) => ({ agent, project: PROJECT })), ...o })
  const node = await startFakeNode(t, { features: FEATURES, strict: o.strict })
  const home = mkdtempSync('/tmp/ppaseo-')
  t.after(() => rmSync(home, { recursive: true, force: true }))
  // Where Paseo's own CLI finds its daemon: the address in the home's paseo.pid.
  writeFileSync(join(home, 'paseo.pid'), JSON.stringify({ pid: process.pid, listen: `127.0.0.1:${fake.port}` }))
  const logs = []
  const paseo = new PaseoClient({ home, retryMin: 30, retryMax: 100, log: (m) => logs.push(m) })
  const bridge = createPaseoBridge({ paseo, log: (m) => logs.push(m) },
    { socketPath: node.path, mcpConfig: o.mcpConfig, allowTools: o.allowTools, retryMs: 30, pushDelayMs: 5, refreshMs: 0, awayGrace: o.awayGrace ?? 400, callWait: 1500,
      listMax: o.listMax, pageSize: o.pageSize, closedChecks: o.closedChecks, sweepMs: o.sweepMs ?? 0 })
  bridge.start()
  t.after(() => bridge.stop())
  await node.ready
  await until('the first list', () => node.last('runtime.sessions')?.sessions?.length >= 4)
  return { fake, node, bridge, paseo, logs, a, rows: () => node.last('runtime.sessions').sessions }
}

const call = (node, method, params) => node.peer.call(method, params)

test("Paseo's agents are the engine's sessions: Claude Code's by its session and transcript, the others by agent", async (t) => {
  const { node, a, rows } = await setup(t)
  assert.equal(node.hello.runtime, 'paseo')
  assert.equal(node.hello.name, 'Paseo')
  assert.equal(node.hello.version, '0.14.0', "Paseo's version")
  assert.deepEqual(node.hello.caps, { start: true, send: true, interrupt: true, rename: true, close: true, assistant: false, modes: true, resume: true, readonly: true })
  assert.deepEqual(node.hello.launchers.map((l) => [l.name, l.agent]), [['my-claude', 'claude'], ['my-codex', 'codex'], ['grok', 'paseo']])
  assert.deepEqual(node.hello.launchers[0].models, [{ id: 'fable[1m]', name: 'Fable', default: true, efforts: ['low', 'high', 'max'], default_effort: 'max' }])
  const byId = Object.fromEntries(rows().map((r) => [r.session_id, r]))
  assert.deepEqual(Object.keys(byId).sort(), [SID, 'paseo-e1', `paseo-${a.codex.id}`, `paseo-${a.grok.id}`].sort(), 'a Claude agent without a session yet is not listed')
  assert.deepEqual([byId[SID].agent, byId[SID].transcript, byId[SID].status, byId[SID].launcher], ['claude', SID, 'done', 'my-claude'])
  assert.deepEqual([byId[SID].space_id, byId[SID].space_name, byId[SID].slot_id, byId[SID].slot_name], ['prj_1', 'proj', undefined, '主线'],
    "the project is the space; Paseo's workspace names where in it, and is no slot")
  assert.deepEqual([byId['paseo-e1'].transcript, byId['paseo-e1'].title], [SID2, 'E'])
  assert.deepEqual([byId[`paseo-${a.codex.id}`].agent, byId[`paseo-${a.codex.id}`].status, byId[`paseo-${a.codex.id}`].transcript], ['codex', 'working', undefined])
  assert.deepEqual([byId[`paseo-${a.grok.id}`].agent_label, byId[`paseo-${a.grok.id}`].status], ['Grok', 'offline'])
})

test('changes in Paseo move the list: a new agent, a status, one archived', async (t) => {
  const { fake, node, a, rows } = await setup(t)
  const n = agentSnapshot({ provider: 'grok', cwd: '/tmp', title: 'new', status: 'running' })
  fake.upsert(n, PROJECT)
  await until('the new agent listed', () => rows().some((r) => r.session_id === `paseo-${n.id}` && r.status === 'working'))
  fake.update(a.codex.id, { status: 'idle', lastUserMessageAt: '2026-11-04T01:00:00Z' })
  await until('the codex agent done', () => rows().find((r) => r.session_id === `paseo-${a.codex.id}`)?.status === 'done')
  fake.update(a.fresh.id, { runtimeInfo: { provider: 'claude', sessionId: 'bbbbbbbb-0000-4000-8000-000000000003' } })
  await until('the Claude agent listed once it has a session', () => rows().some((r) => r.session_id === 'bbbbbbbb-0000-4000-8000-000000000003' && !r.transcript))
  await call(node, 'session.close', { session_id: `paseo-${n.id}` })
  assert.equal(fake.requestsOf('archive_agent_request').at(-1).agentId, n.id)
  await until('the archived agent gone', () => !rows().some((r) => r.session_id === `paseo-${n.id}`))
})

test("an agent's timeline as items, its live text, and what it writes next while watched", async (t) => {
  const { fake, node, a } = await setup(t)
  const id = `paseo-${a.codex.id}`
  fake.setTimeline(a.codex.id, [
    entry(1, { type: 'user_message', text: '跑测试', messageId: 'u1' }),
    entry(2, { type: 'tool_call', callId: 'c1', name: 'shell', status: 'running', error: null, detail: { type: 'shell', command: 'go test ./...' } }),
  ])
  const p = await call(node, 'session.items', { session_id: id, turns: 3 })
  assert.deepEqual(p.items.map((i) => [i.id, i.kind]), [['1#0', 'text'], ['2#0', 'tool_use']])
  assert.equal(p.items[1].tool_name, 'Bash')
  assert.equal(p.history, `paseo:${a.codex.id}:ep1`)

  await call(node, 'session.watch', { session_id: id, since_seq: 0 })
  await until('the timeline followed', () => fake.requestsOf('agent.timeline.set_subscription.request').some((r) => r.agentIds[0] === a.codex.id))
  await until('the subscription made', () => [...fake.subs.values()].some((s) => s.kind === 'timeline'))
  fake.stream(a.codex.id, { type: 'timeline', provider: 'codex', turnId: 't1', item: { type: 'assistant_message', text: '测试', messageId: 'm1' } }, 5)
  fake.stream(a.codex.id, { type: 'timeline', provider: 'codex', turnId: 't1', item: { type: 'assistant_message', text: '都过了', messageId: 'm1' } }, 6)
  await until('the live text', () => node.all('session.delta').some((d) => d.session_id === id && d.text === '测试都过了'))

  // The tool finished and the text was written: the next read pushes them, from where the watch began.
  fake.setTimeline(a.codex.id, [
    entry(1, { type: 'user_message', text: '跑测试', messageId: 'u1' }),
    entry(2, { type: 'tool_call', callId: 'c1', name: 'shell', status: 'completed', error: null, detail: { type: 'shell', command: 'go test ./...', output: 'ok', exitCode: 0 } }, 4),
    entry(5, { type: 'assistant_message', text: '测试都过了' }, 6),
  ])
  fake.update(a.codex.id, { status: 'idle', lastUserMessageAt: '2026-11-04T01:00:00Z' })
  fake.stream(a.codex.id, { type: 'turn_completed', provider: 'codex', turnId: 't1' })
  const pushed = await until('the new items', () => node.all('session.items').filter((x) => x.session_id === id).flatMap((x) => x.items).length >= 2 &&
    node.all('session.items').filter((x) => x.session_id === id))
  const all = pushed.flatMap((x) => x.items)
  assert.deepEqual(all.map((i) => [i.id, i.kind]), [['4#0', 'tool_result'], ['5#0', 'text']])
  assert.equal(pushed[0].from_seq, 2, 'continuous with where the watch began')
  assert.equal(pushed.at(-1).to_seq, 6)
  assert.ok(node.all('session.delta').some((d) => d.session_id === id && d.text === ''), 'the live text ends')
  await call(node, 'session.unwatch', { session_id: id })
  await until('the subscription released', () => fake.requestsOf('subscription.release.request').length > 0)
})

test('whole timelines are kept for the watched agents and the most recently read ones only', async (t) => {
  const many = Array.from({ length: 40 }, (_, i) => agentSnapshot({ provider: 'my-codex', cwd: '/tmp', title: `T${i}`, status: 'idle' }))
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  const { fake, node, bridge } = await setup(t, { agents: [...base, ...many.map((agent) => ({ agent, project: PROJECT }))] })
  for (const [i, x] of many.entries()) {
    fake.setTimeline(x.id, [entry(1, { type: 'user_message', text: `question ${i}`, messageId: `u${i}` }), entry(2, { type: 'assistant_message', text: `answer ${i}` })])
  }
  const sid = (x) => `paseo-${x.id}`
  const reads = (x) => fake.requestsOf('fetch_agent_timeline_request').filter((r) => r.agentId === x.id && r.limit === 0).length
  const kept = bridge.state.timelines
  const [watched, ...others] = many
  const first = await call(node, 'session.items', { session_id: sid(watched), turns: 3 })
  await call(node, 'session.watch', { session_id: sid(watched), since_seq: 0 })
  const pages = []
  for (const x of others) pages.push(await call(node, 'session.items', { session_id: sid(x), turns: 3 }))
  assert.ok(kept.size <= TIMELINES_MAX, `${kept.size} whole timelines kept after reading ${many.length} agents`)
  assert.ok(kept.has(watched.id), "the watched agent's is kept, though read first")
  assert.ok(!kept.has(others[0].id) && kept.has(others.at(-1).id), 'the least recently read go first')

  // A read from the cache counts as recent: the next one to go is the one after it.
  const oldest = others.find((x) => kept.has(x.id))
  const next = others[others.indexOf(oldest) + 1]
  const before = reads(oldest)
  await call(node, 'session.items', { session_id: sid(oldest), turns: 3 })
  assert.equal(reads(oldest), before, 'read from the cache')
  const again = await call(node, 'session.items', { session_id: sid(others[0]), turns: 3 })
  assert.deepEqual(again, pages[0], 'one that went is read from Paseo again, the same')
  assert.ok(kept.has(oldest.id) && !kept.has(next.id), 'the one read from the cache stays; the next least recently read went')
  assert.ok(kept.size <= TIMELINES_MAX)

  // The watched session pages from its kept timeline, as before.
  const watchedReads = reads(watched)
  assert.deepEqual(await call(node, 'session.items', { session_id: sid(watched), turns: 3 }), first)
  assert.equal(reads(watched), watchedReads, 'not read from Paseo again')
  assert.deepEqual(first.items.map((i) => i.text), ['question 0', 'answer 0'])
})

test('a rewind in Paseo goes to the phone as another history, even when the timeline got shorter', async (t) => {
  const { fake, node, a } = await setup(t)
  const id = `paseo-${a.codex.id}`
  fake.setTimeline(a.codex.id, [
    entry(1, { type: 'user_message', text: '一', messageId: 'u1' }),
    entry(2, { type: 'assistant_message', text: '答一' }),
    entry(3, { type: 'user_message', text: '二', messageId: 'u2' }),
    entry(4, { type: 'assistant_message', text: '答二' }),
  ])
  await call(node, 'session.watch', { session_id: id, since_seq: 0 })
  await until('the subscription made', () => [...fake.subs.values()].some((s) => s.kind === 'timeline'))
  // Rewound to before the second entry: a new epoch, two entries.
  fake.setTimeline(a.codex.id, [
    entry(1, { type: 'user_message', text: '一', messageId: 'u1' }),
    entry(2, { type: 'assistant_message', text: '答一' }),
  ], 'ep2')
  fake.emit('timeline', { type: 'agent.timeline.replacement', payload: { agentId: a.codex.id, epoch: 'ep2' } }, (s) => s.agentIds.includes(a.codex.id))
  const b = await until('another history', () => node.all('session.items').find((x) => x.session_id === id && x.history === `paseo:${a.codex.id}:ep2`))
  assert.ok(b.to_seq <= 2, 'the shorter timeline, not the old end')
  // What it writes next goes on from there.
  fake.setTimeline(a.codex.id, [
    entry(1, { type: 'user_message', text: '一', messageId: 'u1' }),
    entry(2, { type: 'assistant_message', text: '答一' }),
    entry(3, { type: 'user_message', text: '二，重来', messageId: 'u3' }),
  ], 'ep2')
  fake.stream(a.codex.id, { type: 'timeline', provider: 'codex', turnId: 't2', item: { type: 'user_message', text: '二，重来', messageId: 'u3' } }, 3)
  const next = await until('the next entry', () => node.all('session.items').find((x) => x.session_id === id && x.items.some((i) => i.text === '二，重来')))
  assert.equal(next.history, `paseo:${a.codex.id}:ep2`)
  assert.equal(next.from_seq, b.to_seq)
})

test("a Claude Code session's watch is its live text only: the node reads the transcript", async (t) => {
  const { fake, node, a } = await setup(t)
  await call(node, 'session.watch', { session_id: SID, since_seq: 0, transcript: true })
  await until('the subscription made', () => [...fake.subs.values()].some((s) => s.kind === 'timeline' && s.agentIds[0] === a.claude.id))
  fake.stream(a.claude.id, { type: 'timeline', provider: 'claude', turnId: 't', item: { type: 'assistant_message', text: '好的', messageId: 'msg_1' } }, 10)
  await until('the live text', () => node.all('session.delta').some((d) => d.session_id === SID && d.text === '好的'))
  fake.stream(a.claude.id, { type: 'timeline', provider: 'claude', turnId: 't', item: { type: 'tool_call', callId: 'toolu_1', name: 'Bash', status: 'running', error: null, detail: { type: 'shell', command: 'ls' } } }, 11)
  await until('it ends at the tool call', () => node.all('session.delta').some((d) => d.session_id === SID && d.text === ''))
  await new Promise((r) => setTimeout(r, 300))
  assert.equal(node.all('session.items').filter((x) => x.session_id === SID).length, 0, 'no items: the transcript has them')
  assert.equal(fake.requestsOf('fetch_agent_timeline_request').filter((r) => r.agentId === a.claude.id).length, 0, 'its timeline is not read')
})

test('a watched session another agent takes on: the watch follows that agent, and its content is another history', async (t) => {
  const { fake, node, bridge, a } = await setup(t)
  // A Claude session (its items are the transcript's): the live text of the agent that has it now.
  fake.update(a.claude.id, { status: 'closed' })
  await until('closed', () => bridge.state.agents.get(a.claude.id)?.agent.status === 'closed')
  await call(node, 'session.watch', { session_id: SID, since_seq: 0, transcript: true })
  await until('followed', () => [...fake.subs.values()].some((s) => s.kind === 'timeline' && s.agentIds.includes(a.claude.id)))
  const b = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: 'A again', status: 'running',
    persistence: { provider: 'my-claude', sessionId: SID, metadata: { provider: 'claude' } }, runtimeInfo: { provider: 'claude', sessionId: SID } })
  fake.upsert(b, PROJECT)
  await until('the new agent followed', () => [...fake.subs.values()].some((s) => s.kind === 'timeline' && s.agentIds.includes(b.id)))
  assert.equal(bridge.state.watches.get(SID).agentId, b.id)
  assert.ok(fake.requestsOf('subscription.release.request').length > 0, 'the old agent is let go')
  fake.stream(b.id, { type: 'timeline', provider: 'claude', turnId: 't1', item: { type: 'assistant_message', text: '接着说', messageId: 'm1' } }, 5)
  await until('its live text', () => node.all('session.delta').some((d) => d.session_id === SID && d.text === '接着说'))

  // An agent whose items are its timeline: the new one's goes out as another history.
  const id = 'paseo-e1' // the labeled agent's session
  fake.setTimeline(a.labeled.id, [entry(1, { type: 'user_message', text: '一', messageId: 'u1' }), entry(2, { type: 'assistant_message', text: '答一' }),
    entry(3, { type: 'user_message', text: '二', messageId: 'u2' })])
  await call(node, 'session.watch', { session_id: id, since_seq: 0 })
  await until('followed', () => [...fake.subs.values()].some((s) => s.kind === 'timeline' && s.agentIds.includes(a.labeled.id)))
  fake.update(a.labeled.id, { status: 'closed' })
  const c = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: 'E again', status: 'running', labels: { 'persona.session': id },
    persistence: { provider: 'my-claude', sessionId: SID2, metadata: { provider: 'claude' } } })
  fake.setTimeline(c.id, [entry(1, { type: 'user_message', text: '一', messageId: 'u1' })])
  fake.upsert(c, PROJECT)
  const pushed = await until('another history', () => node.all('session.items').find((x) => x.session_id === id && x.history?.startsWith(`paseo:${c.id}:`)))
  assert.equal(pushed.to_seq, 1)
  assert.equal(pushed.from_seq, undefined, 'not a continuation of the old agent')
  // The phone reads the new agent's page, then it writes on: pushed from its own seq, not the old agent's.
  await call(node, 'session.items', { session_id: id, turns: 3 })
  fake.setTimeline(c.id, [entry(1, { type: 'user_message', text: '一', messageId: 'u1' }), entry(2, { type: 'assistant_message', text: '新答' })])
  fake.update(c.id, { status: 'idle' })
  fake.stream(c.id, { type: 'turn_completed', provider: 'claude', turnId: 't2' })
  const next = await until('the new agent writes on', () => node.all('session.items').find((x) => x.session_id === id && x.items.some((i) => i.text === '新答')))
  assert.equal(next.from_seq, 1)
  assert.equal(next.history, pushed.history)
})

test('sending: into the running turn, once per message, images as Paseo takes them', async (t) => {
  const { fake, node, a } = await setup(t)
  await call(node, 'session.send', { session_id: SID, text: '继续', client_msg_id: 'cm-1', images: [{ mime: 'image/png', data: 'iVBO' }] })
  await call(node, 'session.send', { session_id: SID, text: '继续', client_msg_id: 'cm-1' })
  const sent = fake.requestsOf('send_agent_message_request')
  assert.equal(sent.length, 1, 'a retry of the same message is not sent again')
  assert.deepEqual({ ...sent[0], requestId: undefined }, { type: 'send_agent_message_request', requestId: undefined, agentId: a.claude.id, text: '继续',
    messageId: uuidOf(`persona:${a.claude.id}:cm-1`), activeTurnBehavior: 'steer', images: [{ data: 'iVBO', mimeType: 'image/png' }] })
  fake.handlers.send_agent_message_request = () => ({ payload: { agentId: a.claude.id, accepted: false, error: 'Agent is archived' } })
  await assert.rejects(call(node, 'session.send', { session_id: SID, text: 'x', client_msg_id: 'cm-2' }), /Paseo 没有收下这条消息：Agent is archived/)
  await assert.rejects(call(node, 'session.send', { session_id: 'paseo-nope', text: 'x' }), /没有这个会话/)
  // Paseo slow to start the run (a closed agent loading): the send is left with it, not failed.
  fake.handlers.send_agent_message_request = () => null
  const t0 = Date.now()
  await call(node, 'session.send', { session_id: SID, text: '慢', client_msg_id: 'cm-3' })
  assert.ok(Date.now() - t0 >= 1400, 'waited for Paseo first')
})

test('interrupt, rename, permission mode and effort go to the agent', async (t) => {
  const { fake, node, a } = await setup(t)
  await call(node, 'session.interrupt', { session_id: SID })
  assert.equal(fake.requestsOf('cancel_agent_request')[0].agentId, a.claude.id)
  await call(node, 'session.rename', { session_id: SID, title: '新名字' })
  assert.equal(fake.requestsOf('update_agent_request')[0].name, '新名字')
  await call(node, 'session.modes', { session_id: SID, permission_mode: 'plan', effort: 'high' })
  assert.deepEqual([fake.requestsOf('set_agent_mode_request')[0].modeId, fake.requestsOf('set_agent_thinking_request')[0].thinkingOptionId], ['plan', 'high'])
  await assert.rejects(call(node, 'session.modes', { session_id: SID, effort: 'xhigh' }), /没有「xhigh」/)
  await assert.rejects(call(node, 'session.modes', { session_id: SID, permission_mode: 'auto' }), /没有「auto」模式/)
  await assert.rejects(call(node, 'session.modes', { session_id: `paseo-${a.codex.id}`, permission_mode: 'plan' }), /没有这个权限模式/)
  // Codex: Persona's bypassPermissions is Paseo's full-access; the other modes are Paseo's own (left alone).
  await call(node, 'session.modes', { session_id: `paseo-${a.codex.id}`, permission_mode: 'bypassPermissions' })
  assert.equal(fake.requestsOf('set_agent_mode_request').at(-1).modeId, 'full-access')
})

const MCP_JSON = {
  mcpServers: {
    persona: { command: '/usr/bin/personad', args: ['mcp'], env: { PERSONA_HOME: '/home/u/.persona' } },
    legacy: { command: '/usr/bin/personad', args: ['mcp'] },
  },
}

test("a new session gets Persona's agent tools, each server naming the session the agent is kept under", async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-mcp-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const mcpConfig = join(dir, 'mcp.json')
  writeFileSync(mcpConfig, JSON.stringify(MCP_JSON))
  const { fake, node } = await setup(t, { mcpConfig })
  for (const launcher of ['my-claude', 'my-codex']) {
    await call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher })
    const req = fake.requestsOf('agent.create.request').at(-1)
    const sessionId = req.labels['persona.session']
    assert.equal(sessionId, `paseo-${req.agentId}`)
    assert.deepEqual(req.config.mcpServers, {
      persona: { type: 'stdio', command: '/usr/bin/personad', args: ['mcp'], env: { PERSONA_HOME: '/home/u/.persona', PERSONA_SESSION_ID: sessionId } },
      legacy: { type: 'stdio', command: '/usr/bin/personad', args: ['mcp'], env: { PERSONA_SESSION_ID: sessionId } },
    })
  }
  const [first, second] = fake.requestsOf('agent.create.request').map((r) => r.config.mcpServers.persona.env.PERSONA_SESSION_ID)
  assert.notEqual(first, second, 'each agent has its own session id')
  assert.equal(JSON.parse(readFileSync(mcpConfig, 'utf8')).mcpServers.persona.env.PERSONA_SESSION_ID, undefined, 'the shared config is not changed')
})

test("the fake daemon takes config.mcpServers only in Paseo's stdio shape", () => {
  const ok = { type: 'stdio', command: 'personad', args: ['mcp'], env: { A: 'b' } }
  assert.equal(mcpServersError({ persona: ok }), null)
  assert.equal(mcpServersError({ persona: { command: 'personad' } }), 'mcpServers.persona.type: expected stdio, http or sse')
  assert.match(mcpServersError({ persona: { ...ok, command: 1 } }), /command/)
  assert.match(mcpServersError({ persona: { ...ok, args: ['a', 2] } }), /args/)
  assert.match(mcpServersError({ persona: { ...ok, env: { A: 1 } } }), /env/)
})

test("an Agent for a provider that takes no MCP servers is created again without them, once, and only for that error", async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-mcp-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const mcpConfig = join(dir, 'mcp.json')
  writeFileSync(mcpConfig, JSON.stringify(MCP_JSON))
  const { fake, node, logs, rows } = await setup(t, { mcpConfig, noMcpProviders: ['grok'] })
  const res = await call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'grok' })
  const [first, second, ...more] = fake.requestsOf('agent.create.request')
  assert.equal(more.length, 0, 'one more request, not a loop')
  assert.ok(first.config.mcpServers.persona.env.PERSONA_SESSION_ID)
  assert.deepEqual(second.config, { provider: 'grok', cwd: '/tmp' }, 'the old configuration, no mcpServers')
  assert.notEqual(second.agentId, first.agentId, 'a new agent id: the failed one has a record in Paseo')
  assert.equal(first.labels['persona.session'], `paseo-${first.agentId}`)
  assert.equal(second.labels['persona.session'], first.labels['persona.session'], 'the Persona session stays; only the Paseo agent is new')
  assert.equal(res.session.session_id, first.labels['persona.session'])
  assert.ok(logs.some((l) => /provider grok takes no MCP servers; creating the agent again without the Persona tools/.test(l)), logs.join('\n'))
  await until('listed', () => rows().some((r) => r.session_id === res.session.session_id))
  assert.equal(fake.agents.has(first.agentId), false, 'the failed attempt left no agent')
  assert.equal(fake.agents.has(second.agentId), true)
  assert.deepEqual([...fake.agents.values()].filter((e) => e.agent.provider === 'grok' && e.agent.labels['persona.session']).map((e) => e.agent.id), [second.agentId], 'one new agent, not two')

  // Any other failure is the answer, not a reason to try again.
  const before = fake.requestsOf('agent.create.request').length
  fake.handlers['agent.create.request'] = () => ({ payload: { agent: null, error: 'Provider unavailable' } })
  await assert.rejects(call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'my-claude' }), /Provider unavailable/)
  assert.equal(fake.requestsOf('agent.create.request').length, before + 1)

  // Without servers to give there is nothing to take away: the failure is final.
  const none = await setup(t, { noMcpProviders: ['grok'], mcpConfig: join(dir, 'absent.json') })
  none.fake.handlers['agent.create.request'] = () => ({ payload: { agent: null, error: "Provider 'grok' does not support MCP servers" } })
  await assert.rejects(call(none.node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'grok' }), /does not support MCP servers/)
  assert.equal(none.fake.requestsOf('agent.create.request').length, 1)
})

test("a refusal that comes after the start was answered keeps the session id the row, the label and the tools named", async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-mcp-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const mcpConfig = join(dir, 'mcp.json')
  writeFileSync(mcpConfig, JSON.stringify(MCP_JSON))
  const { fake, node, rows } = await setup(t, { mcpConfig, noMcpProviders: ['grok'] })
  const known = new Set(rows().map((r) => r.session_id))
  fake.refuseDelay = 2000 // past the bridge's wait for the answer (callWait 1500 in setup)
  const res = await call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'grok' })
  const placeholder = res.session.session_id
  const [first] = fake.requestsOf('agent.create.request')
  assert.equal(fake.requestsOf('agent.create.request').length, 1, 'the row came while the first request was unanswered')
  assert.equal(placeholder, `paseo-${first.agentId}`)
  assert.equal(first.config.mcpServers.persona.env.PERSONA_SESSION_ID, placeholder)
  await until('the second request', () => fake.requestsOf('agent.create.request').length === 2)
  const second = fake.requestsOf('agent.create.request')[1]
  assert.notEqual(second.agentId, first.agentId, 'a new Paseo agent')
  assert.equal(second.config.mcpServers, undefined)
  assert.equal(second.labels['persona.session'], placeholder, 'the same Persona session')
  await until('listed under the row\'s id', () => rows().some((r) => r.session_id === placeholder))
  assert.deepEqual([...fake.agents.values()].filter((e) => e.agent.provider === 'grok' && e.agent.labels['persona.session']).map((e) => [e.agent.id, e.agent.labels['persona.session']]),
    [[second.agentId, placeholder]])
  assert.equal(rows().filter((r) => !known.has(r.session_id) && r.session_id !== placeholder).length, 0, 'no second session id')
})

test('a new session without a usable MCP config is created as before, and the log says why', async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-mcp-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const missing = await setup(t, { mcpConfig: join(dir, 'absent.json') })
  await call(missing.node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'my-claude' })
  assert.deepEqual(missing.fake.requestsOf('agent.create.request')[0].config, { provider: 'my-claude', cwd: '/tmp' })
  assert.ok(missing.logs.some((l) => /cannot read the MCP config .*absent\.json.*no Persona tools/.test(l)), missing.logs.join('\n'))
  const unset = await setup(t)
  await call(unset.node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'my-claude' })
  assert.equal(unset.fake.requestsOf('agent.create.request')[0].config.mcpServers, undefined)
  assert.ok(unset.logs.some((l) => /no MCP config given/.test(l)))
})

test('a new session: an agent of the launcher\'s provider, with the form\'s choices, under an id kept on it', async (t) => {
  const { fake, node, rows } = await setup(t)
  const res = await call(node, 'session.start', { cwd: '/tmp', prompt: '修一下登录', launcher: 'my-claude', model: 'fable[1m]', permission_mode: 'acceptEdits', effort: 'high', workspace_id: 'wks_1' })
  const req = fake.requestsOf('agent.create.request')[0]
  assert.deepEqual(req.config, { provider: 'my-claude', cwd: '/tmp', model: 'fable[1m]', modeId: 'acceptEdits', thinkingOptionId: 'high' })
  assert.equal(req.initialPrompt, '修一下登录')
  assert.equal(req.workspaceId, 'wks_1')
  assert.equal(res.session.session_id, `paseo-${req.agentId}`)
  assert.equal(req.labels['persona.session'], res.session.session_id)
  assert.equal(res.session.agent, 'claude')
  await until('listed', () => rows().some((r) => r.session_id === res.session.session_id))
  // A launcher of the login shell's that Paseo does not have: Paseo's default provider.
  await call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'cc_gateway', shell_launcher: true })
  assert.equal(fake.requestsOf('agent.create.request')[1].config.provider, 'my-claude')
  await assert.rejects(call(node, 'session.start', { cwd: '/nonexistent/dir', prompt: 'x' }), /没有这个目录/)
  fake.handlers['agent.create.request'] = () => ({ payload: { agent: null, error: 'Provider unavailable' } })
  await assert.rejects(call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'grok' }), /Paseo 没能新建：Provider unavailable/)
})

test("a new Codex session: bypassPermissions starts it with Paseo's full-access mode; the other modes are left alone", async (t) => {
  const { fake, node } = await setup(t)
  const start = (permission_mode) => call(node, 'session.start', { cwd: '/tmp', prompt: 'x', launcher: 'my-codex', permission_mode })
    .then(() => fake.requestsOf('agent.create.request').at(-1).config)
  assert.equal((await start('bypassPermissions')).modeId, 'full-access')
  assert.equal((await start('auto')).modeId, undefined)
  assert.equal((await start('default')).modeId, undefined)
})

test('a read-only (plan) start: Paseo\'s plan mode on a Claude provider, never a writable fallback (#3238)', async (t) => {
  const { fake, node } = await setup(t)
  const plan = (params) => call(node, 'session.start', { cwd: '/tmp', prompt: 'x', permission_mode: 'plan', ...params })
  await plan({ launcher: 'my-claude' })
  assert.equal(fake.requestsOf('agent.create.request').at(-1).config.modeId, 'plan')
  // A launcher Paseo does not have, or none: a Claude provider in plan mode, not just the default one.
  await plan({ launcher: 'cc_gateway', shell_launcher: true })
  assert.deepEqual([fake.requestsOf('agent.create.request').at(-1).config.provider, fake.requestsOf('agent.create.request').at(-1).config.modeId], ['my-claude', 'plan'])
  const before = fake.requestsOf('agent.create.request').length
  for (const launcher of ['my-codex', 'grok']) {
    const err = await plan({ launcher }).catch((e) => e)
    assert.equal(err.code, 'readonly_unsupported', launcher)
  }
  assert.equal(fake.requestsOf('agent.create.request').length, before, 'refused before anything was created')
  // Paseo starting it in another mode after all: archived, and refused.
  fake.handlers['agent.create.request'] = (msg) => ({ payload: { agent: agentSnapshot({ id: msg.agentId, provider: 'my-claude', cwd: '/tmp', currentModeId: 'bypassPermissions' }) } })
  const err = await plan({ launcher: 'my-claude' }).catch((e) => e)
  assert.equal(err.code, 'readonly_unsupported')
  assert.equal(fake.requestsOf('archive_agent_request').at(-1).agentId, fake.requestsOf('agent.create.request').at(-1).agentId)
  // A takeover cannot set the mode: refused unless Paseo's own agent is in plan already.
  const take = await call(node, 'session.start', { cwd: '/tmp', resume: 'cccccccc-0000-4000-8000-000000000009', permission_mode: 'plan' }).catch((e) => e)
  assert.equal(take.code, 'readonly_unsupported')
  assert.equal(fake.requestsOf('import_agent_request').length, 0)
})

test("完成未看 follows Paseo: seen there, seen here; a new turn to look at, unread again", async (t) => {
  const { fake, a, rows } = await setup(t)
  const status = () => rows().find((r) => r.session_id === SID)?.status
  assert.equal(status(), 'done', 'a Paseo without the flag: done after a turn')
  fake.update(a.claude.id, { requiresAttention: false, attentionReason: null })
  await until('seen in Paseo', () => status() === 'idle')
  fake.update(a.claude.id, { requiresAttention: true, attentionReason: 'finished' })
  await until('a turn to look at', () => status() === 'done')
})

test('continuing a Claude session: the agent Paseo has, else Paseo imports it under the same id', async (t) => {
  const { fake, node, a } = await setup(t)
  const mine = await call(node, 'session.start', { cwd: '/tmp', resume: SID, open_elsewhere: true })
  assert.equal(mine.session.session_id, SID, "Paseo's own agent: taken as it is, even with its claude running")
  assert.equal(fake.requestsOf('import_agent_request').length, 0)
  const other = 'cccccccc-0000-4000-8000-000000000009'
  const res = await call(node, 'session.start', { cwd: '/tmp', resume: other })
  const imp = fake.requestsOf('import_agent_request')[0]
  assert.deepEqual([imp.provider, imp.sessionId, imp.cwd, imp.labels['persona.session']], [a.claude.provider, other, '/tmp', other])
  assert.equal(res.session.session_id, other)
  assert.equal(res.session.transcript, other)
  const err = await call(node, 'session.start', { cwd: '/tmp', resume: 'dddddddd-0000-4000-8000-000000000004', open_elsewhere: true }).catch((e) => e)
  assert.equal(err.code, 'open_elsewhere', 'a claude Paseo does not run has it: no second writer')
})

test("a project's worktrees are one space; a new session at a worktree goes into Paseo's workspace there", async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-wt-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const app = join(dir, 'app')
  const wt = join(dir, 'brave-fox')
  mkdirSync(app)
  mkdirSync(wt)
  const key = 'prj_app' // the placement's projectKey: Paseo's project record id
  const checkout = (cwd, o = {}) => ({ cwd, isGit: true, currentBranch: null, remoteUrl: null, worktreeRoot: cwd,
    isPaseoOwnedWorktree: o.paseo ?? false, mainRepoRoot: o.main ?? null })
  const placed = [
    { agent: agentSnapshot({ provider: 'my-codex', cwd: app, workspaceId: 'wks_main', title: 'M', updatedAt: '2026-11-01T00:00:00Z' }),
      project: { projectKey: key, projectName: 'app', workspaceName: '主线', checkout: checkout(app) } },
    { agent: agentSnapshot({ provider: 'my-codex', cwd: wt, workspaceId: 'wks_wt', title: 'W', updatedAt: '2026-11-02T00:00:00Z' }),
      project: { projectKey: key, projectName: 'app', workspaceName: '修登录', checkout: checkout(wt, { paseo: true, main: app }) } },
    // A later record Paseo made when handed the worktree's directory alone (#2802): a project of its own, named after the folder.
    { agent: agentSnapshot({ provider: 'my-codex', cwd: wt, workspaceId: 'wks_stray', title: 'S', updatedAt: '2026-11-03T00:00:00Z' }),
      project: { projectKey: 'prj_stray', projectName: 'brave-fox', workspaceName: null, checkout: checkout(wt, { paseo: true, main: app }) } },
  ]
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  const { fake, node, rows } = await setup(t, { agents: [...base, ...placed] })
  await until('the worktree agents', () => ['M', 'W', 'S'].every((title) => rows().some((r) => r.title === title)))
  const byTitle = Object.fromEntries(rows().filter((r) => r.space_id === key).map((r) => [r.title, r]))
  assert.deepEqual(['M', 'W', 'S'].map((x) => [byTitle[x].space_name, byTitle[x].slot_id, byTitle[x].slot_name]),
    [['app', undefined, '主线'], ['app', undefined, '修登录'], ['app', undefined, 'brave-fox']],
    "one space named after the repository's project, the stray record's too; each names its workspace")
  assert.deepEqual(['M', 'W', 'S'].map((x) => byTitle[x].repo_root), [app, app, app], "按仓库: the repository's main checkout (#2840)")

  const started = (params) => call(node, 'session.start', { prompt: 'x', launcher: 'my-codex', ...params })
    .then(() => fake.requestsOf('agent.create.request').at(-1))
  assert.equal((await started({ cwd: wt, workspace_id: key })).workspaceId, 'wks_wt', "the project's space and a worktree: its workspace")
  assert.equal((await started({ cwd: wt })).workspaceId, 'wks_wt', 'a worktree alone: its workspace under the repository, not the stray record')
  assert.equal((await started({ cwd: app, workspace_id: key })).workspaceId, undefined, 'the checkout: a new workspace of the project')
  assert.equal((await started({ cwd: app, workspace_id: 'wks_main' })).workspaceId, 'wks_main', 'a workspace named (older apps): that one')
  const other = 'cccccccc-0000-4000-8000-00000000000a'
  await call(node, 'session.start', { cwd: wt, resume: other })
  assert.equal(fake.requestsOf('import_agent_request').at(-1).workspaceId, 'wks_wt', 'continuing a session at a worktree: its workspace')
  // Paseo slow to answer (its provider starting): the row it will have is in its project already.
  fake.handlers['agent.create.request'] = () => null
  const slow = (params) => call(node, 'session.start', { prompt: 'x', launcher: 'my-codex', ...params }).then((r) => r.session)
  const inWt = await slow({ cwd: wt })
  assert.deepEqual([inWt.space_id, inWt.space_name, inWt.slot_id, inWt.slot_name], [key, 'app', undefined, '修登录'])
  const inApp = await slow({ cwd: app, workspace_id: key })
  assert.deepEqual([inApp.space_id, inApp.space_name, inApp.slot_name], [key, 'app', undefined], 'a new workspace of the project: no name yet')
})

test("an agent that leaves Paseo's list has its Claude session named to the node, until it is listed again", async (t) => {
  const day = 86_400_000
  const past = (id, days, o = {}) => ({ agent: agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: id, status: 'closed',
    archivedAt: new Date(Date.now() - days * day).toISOString(), updatedAt: new Date(Date.now() - days * day).toISOString(),
    persistence: { provider: 'my-claude', sessionId: id, metadata: { provider: 'claude' } }, ...o }), project: PROJECT })
  const RECENT = 'bbbbbbbb-0000-4000-8000-000000000001'
  const OLD = 'bbbbbbbb-0000-4000-8000-000000000002'
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  // Archived while the bridge was not running: one lately, one long ago, one of Codex (no transcript of Claude's).
  const { fake, node } = await setup(t, { agents: [...base, past(RECENT, 1), past(OLD, 30),
    past('th-9', 1, { provider: 'my-codex', persistence: { provider: 'my-codex', sessionId: 'th-9', metadata: { provider: 'codex' } } })] })
  const pushed = () => node.last('runtime.sessions')
  await until('the ones archived before', () => pushed().unlisted_transcripts)
  assert.deepEqual(pushed().unlisted_transcripts, [RECENT], 'archived lately: named; long ago or no Claude session: not')
  assert.equal(pushed().sessions.some((r) => r.title === RECENT), false, 'an archived agent is no row')
  assert.equal(fake.requestsOf('fetch_agents_request').filter((r) => r.filter?.includeArchived).length, 1, 'one page: all of them fit')

  // Closed from the phone: archived in Paseo, gone from the rows, its session named.
  await call(node, 'session.close', { session_id: SID })
  await until('named', () => pushed().unlisted_transcripts?.includes(SID))
  assert.equal(pushed().sessions.some((r) => r.session_id === SID), false)
  const codex = pushed().sessions.find((r) => r.title === 'B').session_id
  await call(node, 'session.close', { session_id: codex })
  await until('the Codex agent gone', () => !pushed().sessions.some((r) => r.session_id === codex))
  assert.deepEqual([...pushed().unlisted_transcripts].sort(), [SID, RECENT].sort(), 'an agent without a Claude session names nothing')

  // Taken over again (a message to the session not running): Paseo lists it, under another agent; named no more.
  await call(node, 'session.start', { cwd: '/tmp', resume: SID })
  await until('listed again', () => pushed().sessions.some((r) => r.session_id === SID))
  await until('named no more', () => !pushed().unlisted_transcripts?.includes(SID))
  assert.deepEqual(pushed().unlisted_transcripts, [RECENT])
})

test("only an archived agent of the user's own is named: not a deleted one, nor an agent another agent created", async (t) => {
  const { fake, node, a } = await setup(t)
  const pushed = () => node.last('runtime.sessions')
  const KID = 'cccccccc-0000-4000-8000-000000000001'
  fake.upsert(agentSnapshot({ id: 'kid', provider: 'my-claude', cwd: '/tmp', title: 'KID', labels: { 'paseo.parent-agent-id': a.claude.id },
    persistence: { provider: 'my-claude', sessionId: KID, metadata: { provider: 'claude' } } }), PROJECT)
  await until('the delegated agent', () => pushed().sessions.some((r) => r.title === 'KID'))
  // Deleted, and the list told of the removal, which says what an archive's does. Asked, Paseo has no such agent: it
  // stays gone.
  fake.delete(a.labeled.id, { told: true })
  await until('asked what became of it', () => fake.requestsOf('fetch_agent_request').some((r) => r.agentId === a.labeled.id))
  await until('off the list', () => !pushed().sessions.some((r) => r.title === 'E'))
  // A removal of an agent that was never listed (Paseo sends one for each change of its internal agents) asks nothing.
  for (let i = 0; i < 3; i++) fake.emit('agents', { type: 'agent_update', payload: { kind: 'remove', agentId: 'internal-1' } })
  // The orchestrator archived in Paseo's own app: the agent it created is archived with it, and only its own session is named.
  fake.archive(a.claude.id)
  await until('named', () => pushed().unlisted_transcripts?.includes(SID))
  await until('asked about both', () => ['kid', a.claude.id].every((id) => fake.requestsOf('fetch_agent_request').some((r) => r.agentId === id)))
  await new Promise((r) => setTimeout(r, 50))
  assert.deepEqual(pushed().unlisted_transcripts, [SID], 'not the deleted agent\'s (SID2), nor the delegated one\'s')
  assert.equal(pushed().sessions.some((r) => r.title === 'KID' || r.title === 'A'), false)
  assert.equal(fake.requestsOf('fetch_agent_request').some((r) => r.agentId === 'internal-1'), false)
  assert.equal(fake.requestsOf('fetch_agent_request').filter((r) => r.agentId === a.claude.id).length, 1, 'asked once, however often the removal is told')
})

test('an agent deleted in another client leaves the list: one that ran once Paseo no longer has it, though Paseo only says it closed', async (t) => {
  const { fake, a, rows } = await setup(t, { closedChecks: [30, 200] })
  const asked = (id) => fake.requestsOf('fetch_agent_request').filter((r) => r.agentId === id).length
  const has = (agent) => rows().some((r) => r.session_id === `paseo-${agent.id}`)
  // Paseo closes the agent, and removes its record a moment later: asked before that, it still has it.
  fake.delete(a.codex.id, { after: 100 })
  await until('seen closed', () => rows().find((r) => r.session_id === `paseo-${a.codex.id}`)?.status === 'offline')
  await until('asked, and Paseo still had it', () => asked(a.codex.id) === 1)
  assert.equal(has(a.codex), true, 'an agent Paseo still has stays')
  await until('asked again: gone', () => !has(a.codex))
  assert.equal(asked(a.codex.id), 2)
  // Its last state comes once more, late (Paseo sends an agent's updates one after another, each when it is ready).
  fake.emit('agents', { type: 'agent_update', payload: { kind: 'upsert', agent: { ...a.codex, status: 'closed' }, project: PROJECT } })
  fake.upsert(agentSnapshot({ provider: 'my-codex', cwd: '/tmp', title: 'LATER' }), PROJECT)
  await until('a later change listed', () => rows().some((r) => r.title === 'LATER'))
  assert.equal(has(a.codex), false, 'an update that comes after it was deleted does not list it again')
  // An agent only stopped is asked for too, and stays.
  fake.update(a.claude.id, { status: 'closed' })
  await until('asked twice', () => asked(a.claude.id) === 2)
  assert.equal(rows().find((r) => r.session_id === SID)?.status, 'offline')
  // A new connection reads the directory afresh: an agent that is in it is listed, whatever was said of its id before.
  fake.agents.set(a.codex.id, { agent: { ...a.codex, status: 'idle' }, project: PROJECT })
  fake.drop()
  await until('listed again', () => has(a.codex))
})

test('the directory is read again now and then: an agent deleted without a word leaves the list, and an archived one is named no more', async (t) => {
  const { fake, node, a, rows } = await setup(t, { sweepMs: 40 })
  const pushed = () => node.last('runtime.sessions')
  const asked = (id) => fake.requestsOf('fetch_agent_request').filter((r) => r.agentId === id).length
  // One that was not running (Paseo says nothing at all), and one archived before: its Claude session was named.
  fake.archive(a.claude.id)
  await until('named', () => pushed().unlisted_transcripts?.includes(SID))
  fake.delete(a.grok.id)
  fake.delete(a.claude.id)
  await until('off the list', () => !rows().some((r) => r.session_id === `paseo-${a.grok.id}`))
  await until('named no more', () => !pushed().unlisted_transcripts?.includes(SID))
  // An archive the list was never told of: found out the same way, and its Claude session is named.
  const LOST = 'eeeeeeee-0000-4000-8000-000000000001'
  const lost = agentSnapshot({ provider: 'my-claude', cwd: '/tmp', title: 'LOST', persistence: { provider: 'my-claude', sessionId: LOST, metadata: { provider: 'claude' } } })
  fake.upsert(lost, PROJECT)
  await until('listed', () => rows().some((r) => r.session_id === LOST))
  const at = new Date().toISOString()
  fake.agents.get(lost.id).agent = { ...lost, status: 'closed', archivedAt: at, updatedAt: at }
  await until('off the list, and named', () => !rows().some((r) => r.session_id === LOST) && pushed().unlisted_transcripts?.includes(LOST))
  // An agent a reading of the directory missed (it moved from one page to another between the two) is asked for: it stays.
  fake.handlers.fetch_agents_request = (msg) => {
    if (msg.subscribe || msg.filter?.includeArchived) return undefined
    const entries = [...fake.agents.values()].filter((e) => !e.agent.archivedAt && e.agent.id !== a.labeled.id)
    return { payload: { subscriptionId: null, entries, pageInfo: { nextCursor: null, prevCursor: null, hasMore: false } } }
  }
  await until('asked for it', () => asked(a.labeled.id) >= 2)
  assert.equal(rows().some((r) => r.session_id === 'paseo-e1'), true)
  assert.equal(asked(a.codex.id), 0, 'an agent the directory lists is not asked for')
})

test('agents archived while the bridge was away are read a page at a time, newest first, as far back as their sessions are named', async (t) => {
  const day = 86_400_000
  const id = (n) => `dddddddd-0000-4000-8000-${String(n).padStart(12, '0')}`
  const past = (n, days) => ({ agent: agentSnapshot({ id: `gone-${n}`, provider: 'my-claude', cwd: '/tmp', title: `GONE${n}`, status: 'closed',
    archivedAt: new Date(Date.now() - days * day).toISOString(), updatedAt: new Date(Date.now() - days * day).toISOString(),
    persistence: { provider: 'my-claude', sessionId: id(n), metadata: { provider: 'claude' } } }), project: PROJECT })
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  // Paseo's directory with the archived ones included, newest first, four to a page: the five listed agents, three
  // archived this week, then older and older ones.
  const { fake, node } = await setup(t, { pageSize: 4, agents: [...base, past(1, 1), past(2, 2), past(3, 3), ...[4, 5, 6, 7, 8, 9, 10].map((n) => past(n, n * 10))] })
  await until('the ones archived lately', () => node.last('runtime.sessions').unlisted_transcripts?.length === 3)
  assert.deepEqual([...node.last('runtime.sessions').unlisted_transcripts].sort(), [id(1), id(2), id(3)])
  const pages = fake.requestsOf('fetch_agents_request').filter((r) => r.filter?.includeArchived)
  assert.deepEqual(pages.map((r) => r.page.cursor), [undefined, '4', '8'], 'the page that ends before the week began is the last one read')
  assert.ok(pages.every((r) => r.sort?.[0]?.key === 'updated_at' && r.sort[0].direction === 'desc'))
})

test('a session Persona started keeps its id once archived: a row not running, its transcript the content; a message takes the agent back under that id (#4771)', async (t) => {
  const { fake, node, a, rows } = await setup(t)
  const pushed = () => node.last('runtime.sessions')
  const row = () => rows().find((r) => r.session_id === 'paseo-e1')
  await until('the archive read', () => pushed().unlisted_transcripts)
  await call(node, 'session.close', { session_id: 'paseo-e1' })
  assert.equal(fake.requestsOf('archive_agent_request').at(-1).agentId, a.labeled.id)
  await until('not running', () => row()?.status === 'offline')
  assert.deepEqual([row().transcript, row().title, row().space_id, row().slot_name, row().agent, row().launcher],
    [SID2, 'E', 'prj_1', '主线', 'claude', 'my-claude'], 'the same row in the same place, its transcript the conversation')
  assert.equal(rows().some((r) => r.session_id === SID2), false, "no row under its transcript's id")
  assert.equal(pushed().unlisted_transcripts.includes(SID2), false, 'nor is its transcript named: the row names it')
  // Archived already: nothing runs, its modes stay as they are, and closing it again closes nothing more.
  const n = (type) => fake.requestsOf(type).length
  const before = [n('cancel_agent_request'), n('archive_agent_request')]
  await call(node, 'session.interrupt', { session_id: 'paseo-e1' })
  await call(node, 'session.close', { session_id: 'paseo-e1' })
  assert.deepEqual([n('cancel_agent_request'), n('archive_agent_request')], before)
  const refused = await call(node, 'session.modes', { session_id: 'paseo-e1', permission_mode: 'plan' }).catch((e) => e)
  assert.equal(refused.code, 'not_running')
  assert.equal(n('set_agent_mode_request'), 0, 'Paseo is not asked: it refuses an archived agent')
  // Renamed: Paseo renames its record and tells no list; the row takes the name all the same.
  await call(node, 'session.rename', { session_id: 'paseo-e1', title: '改过' })
  assert.equal(fake.requestsOf('update_agent_request').at(-1).agentId, a.labeled.id)
  await until('renamed', () => row()?.title === '改过')
  // Watched while archived: its agent is followed, and a message takes it back from the archive under the same id.
  await call(node, 'session.watch', { session_id: 'paseo-e1', since_seq: 0, transcript: true })
  await until('followed', () => [...fake.subs.values()].some((s) => s.kind === 'timeline' && s.agentIds[0] === a.labeled.id))
  await call(node, 'session.send', { session_id: 'paseo-e1', text: '接着来', client_msg_id: 'cm-back' })
  assert.equal(fake.requestsOf('send_agent_message_request').at(-1).agentId, a.labeled.id)
  await until('running again', () => row()?.status === 'working')
  assert.equal(fake.agents.get(a.labeled.id).agent.labels['persona.session'], 'paseo-e1', 'its label as it was')
  fake.stream(a.labeled.id, { type: 'timeline', provider: 'claude', turnId: 't', item: { type: 'assistant_message', text: '在', messageId: 'm' } }, 5)
  await until('its live text', () => node.all('session.delta').some((d) => d.session_id === 'paseo-e1' && d.text === '在'))
  assert.equal(rows().filter((r) => r.transcript === SID2).length, 1, 'one row for the conversation')
  assert.equal(node.all('runtime.sessions').some((p) => p.sessions.some((r) => r.session_id === SID2) || p.unlisted_transcripts?.includes(SID2)), false,
    'never under its transcript id, in no list')
})

test('continuing a session an archived agent keeps: Paseo imports it into that agent, its label as it was', async (t) => {
  const { fake, node, a, rows } = await setup(t)
  await call(node, 'session.close', { session_id: 'paseo-e1' })
  await until('not running', () => rows().find((r) => r.session_id === 'paseo-e1')?.status === 'offline')
  const plan = await call(node, 'session.start', { cwd: '/tmp', resume: 'paseo-e1', permission_mode: 'plan' }).catch((e) => e)
  assert.equal(plan.code, 'readonly_unsupported', 'a takeover keeps its own mode')
  const res = await call(node, 'session.start', { cwd: '/nowhere', resume: 'paseo-e1' })
  const imp = fake.requestsOf('import_agent_request').at(-1)
  assert.deepEqual([imp.provider, imp.sessionId, imp.cwd, imp.labels['persona.session']], ['my-claude', SID2, '/tmp', 'paseo-e1'],
    "its own provider, Claude session and directory: Paseo's record of it")
  assert.equal(res.session.session_id, 'paseo-e1')
  assert.deepEqual([fake.agents.get(a.labeled.id).agent.archivedAt, fake.agents.size], [null, 5], 'the same agent, back: no second one')
  await until('listed as running again', () => rows().find((r) => r.session_id === 'paseo-e1')?.status === 'idle')
  // Listed again, it is the agent Paseo has, whatever the session was called.
  const again = await call(node, 'session.start', { cwd: '/tmp', resume: 'paseo-e1' })
  assert.equal(again.session.session_id, 'paseo-e1')
  assert.equal(fake.requestsOf('import_agent_request').length, 1)
})

test("Paseo's archive at connect: archived agents that keep their id are rows, the latest of the week beside the list's limit; the agents Paseo lists come first; a deleted one leaves", async (t) => {
  const day = 86_400_000
  const transcript = (n) => `ffffffff-0000-4000-8000-${String(n).padStart(12, '0')}`
  const kept = (n, days, o = {}) => {
    const at = new Date(Date.now() - days * day - n * 1000).toISOString()
    return { agent: agentSnapshot({ id: `kept-${n}`, provider: 'my-claude', cwd: '/tmp', title: `K${n}`, status: 'closed', archivedAt: at, updatedAt: at,
      labels: { 'persona.session': `paseo-kept-${n}` }, persistence: { provider: 'my-claude', sessionId: transcript(n), metadata: { provider: 'claude' } }, ...o }),
      project: PROJECT }
  }
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  // 82 archived this week, one 9 days ago; one whose session id an agent Paseo lists has, one whose transcript another has.
  const later = new Date(Date.now() + 60_000).toISOString() // the newest: within the list's limit
  const live = [
    { agent: agentSnapshot({ id: 'live-1', provider: 'my-claude', cwd: '/tmp', title: 'L1', labels: { 'persona.session': 'paseo-kept-90' }, updatedAt: later,
      persistence: { provider: 'my-claude', sessionId: transcript(190), metadata: { provider: 'claude' } } }), project: PROJECT },
    { agent: agentSnapshot({ id: 'live-2', provider: 'my-claude', cwd: '/tmp', title: 'L2', updatedAt: later,
      persistence: { provider: 'my-claude', sessionId: transcript(91), metadata: { provider: 'claude' } } }), project: PROJECT },
  ]
  const archivedOnes = [...Array.from({ length: 82 }, (_, i) => kept(i + 1, 1)), kept(100, 9), kept(90, 1), kept(91, 1)]
  const { fake, node, bridge, paseo, rows } = await setup(t, { listMax: 4, sweepMs: 40, agents: [...base, ...live, ...archivedOnes] })
  const keptOf = (list) => list.filter((r) => r.session_id.startsWith('paseo-kept-') && r.status === 'offline')
  const keptRows = () => keptOf(rows())
  await until('rows of the archived ones', () => keptRows().length === 80)
  assert.deepEqual(keptRows().map((r) => r.title).sort(), Array.from({ length: 80 }, (_, i) => `K${i + 1}`).sort(),
    'the latest 80 of the week: not older ones, and not one whose id or transcript an agent Paseo lists has')
  assert.equal(rows().length - keptRows().length, 4, 'the list keeps its own limit')
  assert.ok(rows().some((r) => r.title === 'L1' && r.session_id === 'paseo-kept-90' && r.status !== 'offline'), 'the agent Paseo lists is the session')
  assert.equal(node.last('runtime.sessions').unlisted_transcripts.some((x) => x.startsWith('ffffffff-')), false, 'no transcript of theirs is named')
  // Deleted in Paseo while archived: found out by the next reading of the archive, its row leaves; the next one in.
  fake.delete('kept-1')
  await until('the deleted one gone', () => !rows().some((r) => r.session_id === 'paseo-kept-1'))
  await until('the next one listed', () => keptRows().some((r) => r.title === 'K81'))
  // The bridge again, from nothing (a restart): the same rows under the same ids, once it has read the archive.
  const ids = keptRows().map((r) => r.session_id).sort()
  bridge.stop()
  const again = createPaseoBridge({ paseo: new PaseoClient({ home: paseo.home, retryMin: 30, retryMax: 100 }) },
    { socketPath: node.path, retryMs: 30, pushDelayMs: 5, refreshMs: 0, callWait: 1500, listMax: 4, sweepMs: 0 })
  const mark = node.events.length
  again.start()
  t.after(() => again.stop())
  const fresh = () => node.events.slice(mark).filter((e) => e.event === 'runtime.sessions').at(-1)?.params.sessions ?? []
  await until('listed again', () => keptOf(fresh()).length === 80, 5000)
  assert.deepEqual(keptOf(fresh()).map((r) => r.session_id).sort(), ids)
})

test('an agent running Codex names its thread, an agent of Claude Code its transcript', async (t) => {
  const THREAD = '0199aaaa-bbbb-7ccc-8ddd-eeeeffff0002'
  const { fake, rows } = await setup(t)
  fake.upsert(agentSnapshot({ provider: 'my-codex', cwd: '/tmp', title: 'CX',
    persistence: { provider: 'my-codex', sessionId: THREAD, metadata: { provider: 'codex', threadId: THREAD } } }), PROJECT)
  await until('the agent', () => rows().some((r) => r.title === 'CX'))
  const by = Object.fromEntries(rows().map((r) => [r.title, r]))
  assert.deepEqual([by.CX.codex_thread, by.CX.transcript, by.A.codex_thread, by.A.transcript], [THREAD, undefined, undefined, SID],
    'the node lists a named thread once, as this session (#4495)')
})

test('a repository opened through a symbolic link is named by its physical directory', async (t) => {
  const dir = mkdtempSync('/tmp/ppaseo-ln-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  mkdirSync(join(dir, 'data', 'app'), { recursive: true })
  symlinkSync(join(dir, 'data'), join(dir, 'home'))
  const opened = join(dir, 'home', 'app') // as the project was opened in Paseo
  const physical = realpathSync(join(dir, 'data', 'app')) // the temporary directory may be behind a link itself
  const placed = { agent: agentSnapshot({ provider: 'my-codex', cwd: opened, workspaceId: 'wks_ln', title: 'LN' }),
    project: { projectKey: 'prj_ln', projectName: 'app', workspaceName: null,
      checkout: { cwd: opened, isGit: true, currentBranch: 'main', remoteUrl: null, worktreeRoot: opened, isPaseoOwnedWorktree: false, mainRepoRoot: null } } }
  const base = Object.values(agents()).map((agent) => ({ agent, project: PROJECT }))
  const { rows } = await setup(t, { agents: [...base, placed] })
  await until('the agent', () => rows().some((r) => r.title === 'LN'))
  const row = rows().find((r) => r.title === 'LN')
  assert.deepEqual([row.cwd, row.repo_root], [opened, physical], "its cwd as Paseo has it; the repository as the node names its working tree (#4492)")
})

test("delegated agents are their creator's subtasks: its session their parent, the unfinished ones its background work", async (t) => {
  const { fake, a, rows } = await setup(t)
  const ran = '2026-11-04T00:00:00Z'
  const kid = (o) => agentSnapshot({ provider: 'my-codex', cwd: '/tmp', labels: { 'paseo.parent-agent-id': a.claude.id, 'paseo.parent-server-id': 'srv_test' }, ...o })
  const k1 = kid({ title: 'K1', status: 'running' })
  const k2 = kid({ title: 'K2', status: 'idle', lastUserMessageAt: ran, requiresAttention: true, attentionReason: 'finished' })
  const far = kid({ title: 'FAR', status: 'idle', lastUserMessageAt: ran, requiresAttention: true, workspaceId: 'wks_far' })
  const lost = agentSnapshot({ provider: 'my-codex', cwd: '/tmp', title: 'LOST', status: 'idle', lastUserMessageAt: ran, requiresAttention: true,
    labels: { 'paseo.parent-agent-id': 'deleted-agent' } })
  for (const x of [k1, k2, far, lost]) fake.upsert(x, PROJECT)
  const titled = () => Object.fromEntries(rows().map((r) => [r.title, r]))
  await until('the delegated agents', () => ['K1', 'K2', 'FAR', 'LOST'].every((x) => titled()[x]))
  const by = titled()
  assert.deepEqual([by.K1.is_subagent, by.K1.parent_session_id, by.K1.status], [true, SID, 'working'])
  assert.deepEqual([by.K2.is_subagent, by.K2.parent_session_id, by.K2.status], [true, SID, 'idle'], 'finished: its creator was told, nobody else has to look')
  assert.deepEqual([by.FAR.is_subagent, by.FAR.parent_session_id, by.FAR.status], [undefined, undefined, 'done'],
    "created from another workspace it is that workspace's own agent, looked at like any other, as in Paseo's sidebar")
  assert.deepEqual([by.LOST.is_subagent, by.LOST.parent_session_id, by.LOST.status], [undefined, undefined, 'idle'],
    'its creator is gone: a row of its own, and as in Paseo nothing says it waits to be looked at')
  await until("the creator's background work", () => titled().A.background?.length === 1)
  assert.deepEqual(titled().A.background.map((b) => [b.id, b.kind, b.description]), [[`paseo-${k1.id}`, 'agent', 'K1']])
  assert.equal(titled().A.status, 'done', 'its own turn ended; the hub shows it working while its agents run')
  // A request of the delegated agent keeps it among the unfinished. Its row still names its creator: it is the node
  // that lists a session waiting for the user without its parent. Once it finished, nothing is left running.
  fake.update(k1.id, { status: 'idle', pendingPermissions: [{ id: 'perm-kid', provider: 'codex', name: 'exec', kind: 'tool', detail: { type: 'shell', command: 'make' } }] })
  await until('waiting for an answer', () => titled().K1.status === 'blocked')
  assert.deepEqual([titled().A.background?.length, titled().K1.parent_session_id], [1, SID])
  fake.update(k1.id, { status: 'idle', pendingPermissions: [] })
  await until('the fleet reported back', () => titled().K1.status === 'idle' && !titled().A.background)
  // The creator closed (Paseo let its process go) while one of its agents works: not Offline, their reports wake it.
  fake.update(k1.id, { status: 'running' })
  fake.update(a.claude.id, { status: 'closed' })
  await until('the creator waits for its agent', () => titled().A.status === 'idle' && titled().A.background?.length === 1)
  fake.update(k1.id, { status: 'idle' })
  await until('nothing of its own left running: offline', () => titled().A.status === 'offline' && !titled().A.background)
  assert.equal(titled().K2.parent_session_id, SID, 'still their parent')
})

test('an agent a delegated agent created is listed under it, however deep, and its card reaches the node; a ring of creators is rows of their own', async (t) => {
  const { fake, node, a, rows } = await setup(t, { strict: true })
  const kid = (id, by, o) => agentSnapshot({ id, provider: 'my-codex', cwd: '/tmp', title: id, labels: { 'paseo.parent-agent-id': by }, ...o })
  const list = [kid('MID', a.claude.id, { status: 'idle' }), kid('LEAF', 'MID', { status: 'running' }),
    kid('RING1', 'RING2', { status: 'idle', requiresAttention: true }), kid('RING2', 'RING1', { status: 'running' })]
  for (const x of list) fake.upsert(x, PROJECT)
  const titled = () => Object.fromEntries(rows().map((r) => [r.title, r]))
  await until('all of them', () => ['MID', 'LEAF', 'RING1', 'RING2'].every((x) => titled()[x]))
  const by = titled()
  assert.deepEqual([by.MID.parent_session_id, by.LEAF.parent_session_id], [SID, by.MID.session_id], 'each under the agent that created it')
  assert.deepEqual([by.RING1.parent_session_id, by.RING2.parent_session_id, by.RING1.status, by.RING2.status], [undefined, undefined, 'idle', 'working'],
    'agents that created one another: neither is under the other, and as in Paseo neither waits to be looked at')
  // MID's own turn ended, but the agent it created still runs: MID has not finished, and so neither has A.
  await until('unfinished up the line', () => titled().A.background?.length === 1)
  assert.deepEqual([titled().MID.background.map((b) => b.description), titled().A.background.map((b) => b.description)], [['LEAF'], ['MID']])
  // The node takes a card only for a session it was told of: LEAF's goes through, and it is answered.
  node.resolve = () => ({ allow: true })
  fake.permission('LEAF', { id: 'perm-leaf', provider: 'claude', name: 'Bash', kind: 'tool', input: { command: 'make' } })
  await until('answered', () => fake.requestsOf('agent_permission_response').length === 1)
  assert.deepEqual([node.refused.length, node.asked[0].session_id], [0, by.LEAF.session_id])
})

test("the list keeps the user's own agents first: delegated agents fill what is left, the busy ones and their creator always", async (t) => {
  const at = (n) => new Date(Date.UTC(2026, 10, 1, 0, n)).toISOString()
  const own = (n, o = {}) => agentSnapshot({ id: `own-${n}`, provider: 'my-codex', cwd: '/tmp', title: `OWN${n}`, status: 'idle', updatedAt: at(n), ...o })
  const kid = (n, parent, o = {}) => agentSnapshot({ id: `kid-${n}`, provider: 'my-codex', cwd: '/tmp', title: `KID${n}`, status: 'closed', updatedAt: at(n),
    labels: { 'paseo.parent-agent-id': parent }, ...o })
  // Newest last: the delegated agents are all newer than the user's own.
  const list = [own(1), own(2), own(3), kid(4, 'own-1', { status: 'running' }), kid(5, 'own-3'), kid(6, 'own-3'), kid(7, 'own-3'), kid(8, 'own-3')]
  const fake = await startFakePaseo(t, { providers: PROVIDERS, entries: ENTRIES, agents: list.map((agent) => ({ agent, project: PROJECT })) })
  const node = await startFakeNode(t, { features: FEATURES })
  const home = mkdtempSync('/tmp/ppaseo-')
  t.after(() => rmSync(home, { recursive: true, force: true }))
  writeFileSync(join(home, 'paseo.pid'), JSON.stringify({ pid: process.pid, listen: `127.0.0.1:${fake.port}` }))
  const paseo = new PaseoClient({ home, retryMin: 30, retryMax: 100 })
  const bridge = createPaseoBridge({ paseo }, { socketPath: node.path, retryMs: 30, pushDelayMs: 5, refreshMs: 0, awayGrace: 400, callWait: 1500, listMax: 5 })
  bridge.start()
  t.after(() => bridge.stop())
  await node.ready
  await until('the list', () => node.last('runtime.sessions')?.sessions?.length >= 5)
  assert.deepEqual(node.last('runtime.sessions').sessions.map((r) => r.title), ['KID8', 'KID4', 'OWN3', 'OWN2', 'OWN1'],
    "all of the user's own, though the delegated agents are newer; of those the working one, then the newest in what is left")
})

test('a busy agent keeps the whole line of its creators listed, past the list limit', async (t) => {
  const at = (n) => new Date(Date.UTC(2026, 10, 1, 0, n)).toISOString()
  const one = (id, n, o = {}) => agentSnapshot({ id, provider: 'my-codex', cwd: '/tmp', title: id, status: 'idle', updatedAt: at(n), ...o })
  const by = (id) => ({ labels: { 'paseo.parent-agent-id': id } })
  // The oldest of the user's own started a line: TOP created MID (long done), MID created LEAF (working).
  const list = [one('TOP', 1), one('OWN2', 2), one('OWN3', 3), one('OWN4', 4), one('MID', 5, { status: 'closed', ...by('TOP') }),
    one('LEAF', 6, { status: 'running', ...by('MID') }), one('IDLE', 7, { status: 'closed', ...by('OWN4') })]
  const fake = await startFakePaseo(t, { providers: PROVIDERS, entries: ENTRIES, agents: list.map((agent) => ({ agent, project: PROJECT })) })
  const node = await startFakeNode(t, { features: FEATURES })
  const home = mkdtempSync('/tmp/ppaseo-')
  t.after(() => rmSync(home, { recursive: true, force: true }))
  writeFileSync(join(home, 'paseo.pid'), JSON.stringify({ pid: process.pid, listen: `127.0.0.1:${fake.port}` }))
  const paseo = new PaseoClient({ home, retryMin: 30, retryMax: 100 })
  const bridge = createPaseoBridge({ paseo }, { socketPath: node.path, retryMs: 30, pushDelayMs: 5, refreshMs: 0, awayGrace: 400, callWait: 1500, listMax: 3 })
  bridge.start()
  t.after(() => bridge.stop())
  await node.ready
  await until('the list', () => node.last('runtime.sessions')?.sessions?.length >= 5)
  const rows = node.last('runtime.sessions').sessions
  assert.deepEqual(rows.map((r) => r.title), ['LEAF', 'MID', 'OWN4', 'OWN3', 'TOP'],
    'the working one, the creator between and the one the line starts at; the rest of the limit to the newest of the user\'s own')
  const sid = Object.fromEntries(rows.map((r) => [r.title, r.session_id]))
  assert.deepEqual(rows.filter((r) => r.parent_session_id).map((r) => [r.title, r.parent_session_id]), [['LEAF', sid.MID], ['MID', sid.TOP]],
    'no row names a parent that is not listed')
})

test("cards: Paseo's requests go to the phone and its answers back; one answered in Paseo leaves the phone", async (t) => {
  const { fake, node, a } = await setup(t)
  let answer
  node.resolve = (ix) => answer(ix)
  answer = () => ({ allow: true })
  fake.permission(a.claude.id, { id: 'permission-1', provider: 'claude', name: 'Bash', kind: 'tool', input: { command: 'npm test' }, metadata: { toolUseId: 'toolu_1' } })
  await until('the response', () => fake.requestsOf('agent_permission_response').length === 1)
  assert.deepEqual(node.asked[0].tool_input, { command: 'npm test' })
  assert.deepEqual([node.asked[0].kind, node.asked[0].session_id, node.asked[0].tool_name, node.asked[0].tool_use_id], ['permission', SID, 'Bash', 'toolu_1'])
  const r = fake.requestsOf('agent_permission_response')[0]
  assert.deepEqual([r.agentId, r.requestId, r.response], [a.claude.id, 'permission-1', { behavior: 'allow' }])

  answer = (ix) => ({ answers: { [ix.questions[0].question]: 'B' } })
  fake.permission(a.claude.id, { id: 'permission-2', provider: 'claude', name: 'AskUserQuestion', kind: 'question',
    input: { questions: [{ question: '选哪个？', header: '选择', multiSelect: false, options: [{ label: 'A' }, { label: 'B' }] }] } })
  await until('the answer', () => fake.requestsOf('agent_permission_response').length === 2)
  assert.deepEqual(fake.requestsOf('agent_permission_response')[1].response, { behavior: 'allow', updatedInput: { answers: { '选哪个？': 'B' } } })

  // Nobody answers on the phone; Paseo's own app does: the card is taken off the phone.
  answer = () => new Promise(() => {})
  fake.permission(a.claude.id, { id: 'permission-3', provider: 'claude', name: 'Edit', kind: 'tool', input: { file_path: '/a' } })
  await until('on the phone', () => node.asked.length === 3)
  fake.resolvePermission(a.claude.id, 'permission-3', { behavior: 'allow' })
  await until('withdrawn', () => node.all('interaction.cancel').some((c) => c.id === node.asked[2].id))
  assert.equal(fake.requestsOf('agent_permission_response').length, 2, 'nothing sent for it')

  // Handed back to the computer: left for Paseo's app.
  answer = () => ({ dismiss: true })
  fake.permission(a.claude.id, { id: 'permission-4', provider: 'claude', name: 'ExitPlanMode', kind: 'plan', input: { plan: 'p' }, metadata: { planText: '## 计划' },
    actions: [{ id: 'reject', behavior: 'deny' }, { id: 'implement', behavior: 'allow' }] })
  await until('asked', () => node.asked.length === 4)
  assert.equal(node.asked[3].plan, '## 计划')
  await new Promise((r) => setTimeout(r, 200))
  assert.equal(fake.requestsOf('agent_permission_response').length, 2)
})

test('an approved plan with a mode: implement, then that mode; the plan read from its call', async (t) => {
  const { fake, node, a } = await setup(t)
  node.resolve = () => ({ approve: true, mode: 'bypassPermissions' })
  // Claude Code gives the permission no input for ExitPlanMode: the plan is in the tool call on the timeline.
  fake.setTimeline(a.claude.id, [entry(7, { type: 'tool_call', callId: 'toolu_p', name: 'ExitPlanMode', status: 'running', error: null, detail: { type: 'plan', text: '## 计划\n\n1. 先写测试' } })])
  fake.permission(a.claude.id, { id: 'permission-9', provider: 'claude', name: 'ExitPlanMode', kind: 'plan', input: {}, metadata: { toolUseId: 'toolu_p' },
    actions: [{ id: 'reject', behavior: 'deny' }, { id: 'implement', behavior: 'allow' }] })
  await until('the mode set', () => fake.requestsOf('set_agent_mode_request').length === 1)
  assert.equal(node.asked[0].plan, '## 计划\n\n1. 先写测试')
  assert.deepEqual(fake.requestsOf('agent_permission_response')[0].response, { behavior: 'allow', selectedActionId: 'implement' })
  assert.equal(fake.requestsOf('set_agent_mode_request')[0].modeId, 'bypassPermissions')
})

test('Paseo restarting: the bridge connects again and keeps the sessions; gone for good, it leaves the node', async (t) => {
  const { fake, node, rows, logs } = await setup(t, { awayGrace: 600 })
  const peer = node.peer
  fake.drop()
  await until('connected again', () => fake.hellos.length === 2)
  await until('subscribed again', () => [...fake.subs.values()].some((s) => s.kind === 'agents'))
  assert.equal(node.peer, peer, 'the node connection stayed')
  assert.ok(rows().length >= 4)
  fake.refuse = 4003
  fake.drop()
  await until('left the node', async () => {
    const gone = await Promise.race([peer.done.then(() => true), new Promise((r) => setTimeout(() => r(false), 50))])
    return gone
  }, 5000)
  assert.ok(logs.some((l) => /away/.test(l)))
})

test("an Agent Persona started calls its own Persona tools without a card; everything else still asks (#4966)", async (t) => {
  const own = ['mcp__persona__report_to_supervisor', 'mcp__persona__todo_decision_add']
  const { fake, node, a } = await setup(t, { allowTools: own })
  node.resolve = () => ({ allow: true })
  fake.permission(a.labeled.id, { id: 'own-1', provider: 'claude', name: 'mcp__persona__todo_decision_add', kind: 'tool',
    input: { decision: 'Use the smaller icon' }, suggestions: [{ type: 'addRules', rules: [{ toolName: 'mcp__persona__todo_decision_add' }], behavior: 'allow', destination: 'session' }] })
  await until('the own tool answered', () => fake.requestsOf('agent_permission_response').length === 1)
  assert.deepEqual(fake.requestsOf('agent_permission_response')[0].response, { behavior: 'allow' }, 'allowed once: no rule kept, no mode set')
  assert.equal(node.asked.length, 0, 'no card for it')
  // Still asked: dispatch and the phone's tools in the same Agent, any tool of an agent made in Paseo's own app, a plan.
  const asks = [
    [a.labeled.id, { id: 'dispatch-1', provider: 'claude', name: 'mcp__persona__dispatch_start', kind: 'tool', input: {} }],
    [a.labeled.id, { id: 'phone-1', provider: 'claude', name: 'mcp__persona__phone_notify', kind: 'tool', input: {} }],
    [a.claude.id, { id: 'mine-1', provider: 'claude', name: 'mcp__persona__report_to_supervisor', kind: 'tool', input: { kind: 'done', text: 'x' } }],
  ]
  for (const [agentId, request] of asks) fake.permission(agentId, request)
  await until('the others asked', () => node.asked.length === asks.length)
  assert.deepEqual(node.asked.map((ix) => [ix.session_id, ix.tool_name]).sort(), [
    ['paseo-e1', 'mcp__persona__dispatch_start'], ['paseo-e1', 'mcp__persona__phone_notify'], [SID, 'mcp__persona__report_to_supervisor'],
  ].sort())
  assert.equal(fake.requestsOf('set_agent_mode_request').length, 0)
})

for (const toolName of ['mcp__persona__report_to_supervisor', 'mcp__jarvis__report_to_supervisor']) {
  test(`a read-only ${toolName} runs once without permission or mode updates`, async (t) => {
    const { fake, node, a, rows } = await setup(t)
    fake.update(a.claude.id, { currentModeId: 'plan' })
    await until('plan mode', () => rows().find((r) => r.session_id === SID)?.permission_mode === 'plan')
    node.resolve = (ix) => {
      assert.equal(ix.kind, 'permission')
      assert.equal(ix.tool_name, toolName)
      return { allow: true }
    }
    const modesBefore = fake.requestsOf('set_agent_mode_request').length
    fake.permission(a.claude.id, { id: 'readonly-report', provider: 'claude', name: toolName, kind: 'tool',
      input: { kind: 'done', text: 'Inspection complete.' },
      suggestions: [{ type: 'addRules', rules: [{ toolName: toolName }], behavior: 'allow', destination: 'session' },
        { type: 'setMode', mode: 'bypassPermissions' }] })
    await until('report response', () => fake.requestsOf('agent_permission_response').length === 1)
    assert.deepEqual(fake.requestsOf('agent_permission_response')[0].response, { behavior: 'allow' })
    assert.equal(fake.requestsOf('set_agent_mode_request').length, modesBefore)
    assert.equal(rows().find((r) => r.session_id === SID)?.permission_mode, 'plan')
  })
}

test('forced close cancels a confirmation and retains the Agent on archive failure', async (t) => {
  const { fake, node, a, rows } = await setup(t)
  fake.update(a.claude.id, { pendingPermissions: [{ id: 'confirm-close', provider: 'claude', name: 'Bash', input: { command: 'echo test' } }] })
  await until('confirmation blocked', () => rows().find(r => r.session_id === SID)?.status === 'blocked')
  fake.handlers.archive_agent_request = () => ({ type: 'agent_archived', payload: { agentId: a.claude.id, error: 'Archive refused' } })
  await assert.rejects(call(node, 'session.close', { session_id: SID, force: true }), /Archive refused/)
  assert.ok(rows().some(r => r.session_id === SID), 'failed close remains visible')
  delete fake.handlers.archive_agent_request
  await call(node, 'session.close', { session_id: SID, force: true })
  assert.equal(fake.requestsOf('archive_agent_request').at(-1).agentId, a.claude.id)
  assert.equal(fake.requestsOf('agent_permission_response').filter(r => r.response?.behavior === 'allow').length, 0)
  await until('force-archived Agent gone', () => !rows().some(r => r.session_id === SID))
})
