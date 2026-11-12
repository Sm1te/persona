import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createRuntime, interactionId, light, PAGE_TEXT, RpcError } from '../index.js'
import { startFakeNode, until } from '../testing/fake-node.js'
import { createEchoRuntime } from '../examples/echo-runtime.mjs'

const minimal = (extra = {}) => ({ listSessions: async () => [{ session_id: 's1', status: 'idle' }], items: async () => ({ items: [], to_seq: 0 }), ...extra })

test('hello names the runtime and declares only the caps whose driver methods exist', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ send: async () => {} }), { runtime: 'toy', name: 'Toy', agent: 'claude', launcher: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  assert.equal(n.hello.runtime, 'toy')
  assert.equal(n.hello.name, 'Toy')
  assert.equal(n.hello.agent, 'claude')
  assert.deepEqual(n.hello.caps, { start: false, send: true, interrupt: false, rename: false, close: false, assistant: false })
  assert.equal(n.hello.launcher, undefined, 'no launcher without start')
  await until('list', () => n.last('runtime.sessions'))
  await n.peer.call('session.send', { session_id: 's1', text: 'x' })
  await assert.rejects(n.peer.call('session.interrupt', { session_id: 's1' }), /unsupported/)
  assert.throws(() => createRuntime(minimal(), {}), /runtime id/)
})

test('a list carries the transcripts the runtime names as its own though it does not list them', async (t) => {
  const n = await startFakeNode(t)
  let past = ['aaaaaaaa-0000-4000-8000-0000000000a1']
  const rt = createRuntime(minimal({ unlistedTranscripts: async () => past }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('the list', () => n.last('runtime.sessions'))
  assert.deepEqual(n.last('runtime.sessions'), { sessions: [{ session_id: 's1', status: 'idle' }], unlisted_transcripts: past })
  past = []
  rt.sessionsChanged()
  await until('none any more: the list says so', () => n.all('runtime.sessions').length >= 2 && n.last('runtime.sessions').unlisted_transcripts?.length === 0)
  // It cannot say now (its archive is being read again): the field is left out, and the node keeps what it was told.
  past = undefined
  rt.sessionsChanged()
  await until('listed again', () => n.all('runtime.sessions').length >= 3)
  assert.deepEqual(n.last('runtime.sessions').unlisted_transcripts, [], 'what it named last')
  // One that has never said sends the rows alone.
  const q = await startFakeNode(t)
  const unsure = createRuntime(minimal({ unlistedTranscripts: () => undefined }), { runtime: 'toy', socketPath: q.path, retryMs: 20 })
  t.after(() => unsure.stop())
  unsure.start()
  await until('its list', () => q.last('runtime.sessions'))
  assert.deepEqual(q.last('runtime.sessions'), { sessions: [{ session_id: 's1', status: 'idle' }] })
  // A runtime without the method sends the rows alone, as before.
  const m = await startFakeNode(t)
  const plain = createRuntime(minimal(), { runtime: 'toy', socketPath: m.path, retryMs: 20 })
  t.after(() => plain.stop())
  plain.start()
  await until('its list', () => m.last('runtime.sessions'))
  assert.deepEqual(m.last('runtime.sessions'), { sessions: [{ session_id: 's1', status: 'idle' }] })
})

test('a driver that cannot name its transcripts now still lists its sessions, with the ones it named last', async (t) => {
  const n = await startFakeNode(t)
  const past = ['aaaaaaaa-0000-4000-8000-0000000000a1']
  let fail = false
  const logs = []
  const unlistedTranscripts = () => {
    if (fail) throw new Error('boom')
    return [...past, '', 7] // only ids go out
  }
  const rt = createRuntime(minimal({ unlistedTranscripts }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5, refreshMs: 0, log: (m) => logs.push(m) })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('the list', () => n.last('runtime.sessions'))
  assert.deepEqual(n.last('runtime.sessions').unlisted_transcripts, past)
  fail = true
  rt.sessionsChanged()
  await until('listed again', () => n.all('runtime.sessions').length >= 2)
  assert.deepEqual(n.last('runtime.sessions'), { sessions: [{ session_id: 's1', status: 'idle' }], unlisted_transcripts: past })
  assert.ok(logs.some((l) => /unlisted transcripts: boom/.test(l)), 'and the log says why')
})

test('a card for a session new in the list waits for that list, however long the driver takes to name its transcripts', async (t) => {
  const n = await startFakeNode(t, { strict: true })
  n.resolve = () => ({ allow: true })
  let rows = [{ session_id: 's1', status: 'idle' }]
  let naming = 0
  let named = () => {}
  const unlistedTranscripts = async () => {
    if (++naming > 1) await new Promise((r) => { named = r }) // the second list on: as slow as the test says
    return []
  }
  const rt = createRuntime(minimal({ listSessions: async () => rows, unlistedTranscripts }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('the first list', () => n.last('runtime.sessions'))
  rows = [...rows, { session_id: 'new', status: 'working' }]
  rt.sessionsChanged()
  await until('the driver asked for its transcripts', () => naming === 2)
  // The list with the new session is read but not sent yet: its card must not be sent now (the node refuses a card for
  // a session it has not been told of).
  const asking = rt.ask({ id: 'toy-new', kind: 'permission', session_id: 'new', tool_name: 'Bash', tool_input: {} })
  await new Promise((r) => setTimeout(r, 30))
  assert.equal(n.asked.length + n.refused.length, 0, 'held until the list is out')
  const release = setInterval(() => named(), 5) // every list from here on
  t.after(() => clearInterval(release))
  assert.deepEqual(await asking, { allow: true })
  assert.equal(n.refused.length, 0)
})

test('appended content goes out as continuous batches, and a gap says where it starts', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ cursor: async () => 10 }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  rt.append('s1', { items: [{ id: 'x' }], fromSeq: 9, toSeq: 10 }) // not watched: nothing is pushed
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  for (const seq of [11, 12]) rt.append('s1', { items: [{ id: `i${seq}`, seq }], fromSeq: seq - 1, toSeq: seq, history: 'h' })
  rt.append('s1', { items: [], fromSeq: 12, toSeq: 13, history: 'h' }) // a step the chat does not show still moves the seq
  await until('first batch', () => n.all('session.items').length === 1)
  assert.deepEqual(n.all('session.items')[0], { session_id: 's1', items: [{ id: 'i11', seq: 11 }, { id: 'i12', seq: 12 }], from_seq: 10, to_seq: 13, history: 'h' })
  rt.append('s1', { items: [{ id: 'i20', seq: 20 }], fromSeq: 19, toSeq: 20, history: 'h' })
  await until('gap batch', () => n.all('session.items').length === 2)
  assert.equal(n.all('session.items')[1].from_seq, 19)
})

test('content rewritten under another history goes out even where its seq did not grow, and goes on from there', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ items: async () => ({ items: [], history: 'h1', to_seq: 10 }) }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  rt.append('s1', { items: [], toSeq: 10, history: 'h2' }) // another branch of as many entries
  await until('the same seq, another history', () => n.all('session.items').length === 1)
  assert.deepEqual(n.all('session.items')[0], { session_id: 's1', items: [], to_seq: 10, history: 'h2' })
  rt.append('s1', { items: [], toSeq: 4, history: 'h3' }) // a rewind: a shorter one
  await until('a lower seq, another history', () => n.all('session.items').length === 2)
  assert.deepEqual(n.all('session.items')[1], { session_id: 's1', items: [], to_seq: 4, history: 'h3' })
  rt.append('s1', { items: [{ id: 'i5', seq: 5 }], fromSeq: 4, toSeq: 5, history: 'h3' }) // below the old 10, still new
  await until('goes on from the rewrite', () => n.all('session.items').length === 3)
  assert.deepEqual(n.all('session.items')[2], { session_id: 's1', items: [{ id: 'i5', seq: 5 }], from_seq: 4, to_seq: 5, history: 'h3' })
  rt.append('s1', { items: [], fromSeq: 4, toSeq: 5, history: 'h3' }) // the same again: already pushed
  await new Promise((r) => setTimeout(r, 100))
  assert.equal(n.all('session.items').length, 3)
})

test('a capped watch catch-up says there is more after its last batch', async (t) => {
  const n = await startFakeNode(t)
  const { page } = await import('../index.js')
  const all = Array.from({ length: 4500 }, (_, i) => ({ id: `${i + 2}#0`, seq: i + 2, role: 'assistant', kind: 'text', text: `item ${i + 2}` }))
  let calls = 0
  const rt = createRuntime(minimal({
    items: async (params) => {
      calls++
      return page(all, params, { history: 'h', toSeq: 4501 })
    },
  }), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 1 })
  const batches = n.all('session.items')
  assert.equal(calls, 8, 'the existing catch-up cap stays at eight pages')
  assert.equal(batches.length, 8)
  assert.equal(batches.at(-1).to_seq, 4001)
  assert.equal(batches.at(-1).more_after, true, 'the client must fetch the 500 items still missing')
  assert.ok(batches.slice(0, -1).every((b) => b.more_after === undefined), 'only the capped batch needs client recovery')
})

test('content committed while a watch catches up is held, then sent once without repeats', async (t) => {
  const n = await startFakeNode(t)
  let release
  const gate = new Promise((r) => { release = r })
  const rt = createRuntime(minimal({
    items: async (p) => {
      await gate
      return { items: [{ id: 'c', seq: 3 }], history: 'h', from_seq: p.after_seq, to_seq: 3 }
    },
  }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  const watching = n.peer.call('session.watch', { session_id: 's1', since_seq: 2 })
  await until('watching', () => rt.watching('s1'))
  rt.append('s1', { items: [{ id: 'c', seq: 3 }], fromSeq: 2, toSeq: 3, history: 'h' }) // also in the catch-up page
  rt.append('s1', { items: [{ id: 'd', seq: 4 }], fromSeq: 3, toSeq: 4, history: 'h' })
  release()
  await watching
  await until('two batches', () => n.all('session.items').length === 2)
  const [catchUp, live] = n.all('session.items')
  assert.deepEqual([catchUp.from_seq, catchUp.to_seq, live.from_seq, live.to_seq], [2, 3, 3, 4])
  assert.deepEqual(live.items.map((i) => i.id), ['d'])
})

test('asking the phone marks the session as needing you, and a withdrawn ask takes the card off', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ hasLocalClient: () => false }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('listed', () => n.last('runtime.sessions'))
  assert.equal(rt.phoneFirst('s1'), true, 'no local UI open: the phone first')
  assert.equal(rt.phoneFirst('unlisted'), false)
  let answer
  n.resolve = () => new Promise((r) => { answer = r })
  const id = interactionId('toy', 's1', 'permission', 'call-1')
  const asking = rt.ask({ id, kind: 'permission', session_id: 's1', tool_name: 'Bash' })
  await until('blocked', () => n.last('runtime.sessions')?.sessions[0].status === 'blocked')
  answer({ allow: true })
  assert.deepEqual(await asking, { allow: true })
  await until('back to idle', () => n.last('runtime.sessions')?.sessions[0].status === 'idle')

  n.resolve = () => new Promise(() => {}) // never answered
  const ac = new AbortController()
  const withdrawn = rt.ask({ id: 'toy-2', kind: 'question', session_id: 's1' }, { signal: ac.signal })
  await until('asked', () => n.asked.length === 2)
  ac.abort(new Error('gone'))
  await assert.rejects(withdrawn, /gone/)
  await until('cancelled', () => n.last('interaction.cancel')?.id === 'toy-2')
  assert.equal(interactionId('toy', 's1', 'permission', 'call-1'), id, 'stable ids')
})

test('rewrites held while a watch catches up go out in order: to another history and back', async (t) => {
  const n = await startFakeNode(t)
  let release
  const gate = new Promise((r) => { release = r })
  const rt = createRuntime(minimal({
    items: async (p) => {
      await gate
      return { items: [{ id: 'c', seq: 10 }], history: 'h1', from_seq: p.after_seq, to_seq: 10 }
    },
  }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  const watching = n.peer.call('session.watch', { session_id: 's1', since_seq: 9 })
  await until('watching', () => rt.watching('s1'))
  rt.append('s1', { items: [], toSeq: 6, history: 'h2' }) // to a shorter branch
  rt.append('s1', { items: [], toSeq: 10, history: 'h1' }) // and back to the first, as long as it was
  release()
  await watching
  await until('the catch-up and both moves', () => n.all('session.items').length === 3)
  assert.deepEqual(n.all('session.items').map((b) => [b.history, b.to_seq]), [['h1', 10], ['h2', 6], ['h1', 10]])
})

test('live text is throttled to the latest whole text, and ends empty', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  rt.stream('s1', 'ignored: not watched')
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  for (const text of ['H', 'He', 'Hel', 'Hell', 'Hello']) rt.stream('s1', text)
  await until('the latest text', () => n.last('session.delta')?.text === 'Hello', 1000)
  assert.ok(n.all('session.delta').length <= 2, 'updates within one window are merged')
  assert.ok(!n.all('session.delta').some((d) => d.text.startsWith('ignored')), 'unwatched sessions get no deltas')
  rt.streamEnd('s1')
  await until('end', () => n.last('session.delta')?.text === '')
})

test('a stream ending after unwatch clears the published text and cancels its pending batch', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  rt.stream('s1', 'Previous reply.')
  await until('the published reply', () => n.last('session.delta')?.text === 'Previous reply.')
  rt.stream('s1', 'Previous reply. More text.')
  await n.peer.call('session.unwatch', { session_id: 's1' })
  rt.streamEnd('s1')
  await until('the unwatched reply ends', () => n.last('session.delta')?.text === '', 2000)
  await new Promise((resolve) => setTimeout(resolve, 150))
  assert.equal(n.last('session.delta').text, '', 'the pending batch cannot restore the old text')
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  rt.stream('s1', 'Previous reply.')
  await until('a new reply may repeat the earlier words', () => n.last('session.delta')?.text === 'Previous reply.')
  rt.streamEnd('never-watched')
  await new Promise((resolve) => setTimeout(resolve, 20))
  assert.ok(n.all('session.delta').every((delta) => delta.session_id === 's1'))
})

test('live text flows again after stop() then start(): a text pending at stop does not hold the session silent', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  rt.stream('s1', 'a')
  await until('the first text', () => n.last('session.delta')?.text === 'a')
  rt.stream('s1', 'ab') // within the window: waits on the throttle timer
  rt.stop()
  rt.start()
  await until('hello again', () => n.hellos === 2)
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  rt.stream('s1', 'abc')
  await until('the text after the restart', () => n.last('session.delta')?.text === 'abc', 1000)
})

test('item helpers cut long text the way the apps expect', () => {
  const it = light({ kind: 'text', text: 'a'.repeat(PAGE_TEXT + 10) })
  assert.equal(it.full_size, PAGE_TEXT + 10)
  assert.match(it.text, /已截断/)
})

test('the echo example is a complete runtime: start, send, stream, ask, page', async (t) => {
  const n = await startFakeNode(t)
  const { runtime } = createEchoRuntime({ socketPath: n.path, retryMs: 20, pushDelayMs: 5, answerFirst: 'phone' })
  t.after(() => runtime.stop())
  runtime.start()
  await n.ready
  assert.equal(n.hello.launcher, 'echo')
  const { session } = await n.peer.call('session.start', { cwd: '/w/demo' })
  await n.peer.call('session.watch', { session_id: session.session_id, since_seq: 0 })
  n.resolve = () => ({ allow: true })
  await n.peer.call('session.send', { session_id: session.session_id, text: 'please ask first', client_msg_id: 'm1' })
  await until('reply', () => n.all('session.items').flatMap((b) => b.items).some((i) => i.text === 'echo: please ask first'), 3000)
  assert.equal(n.asked[0].kind, 'permission')
  assert.ok(n.all('session.delta').some((d) => d.text.startsWith('echo')))
  const page = await n.peer.call('session.items', { session_id: session.session_id, limit: 10 })
  assert.deepEqual(page.items.map((i) => i.role), ['user', 'assistant'])
  const batches = n.all('session.items')
  assert.deepEqual(batches.slice(1).filter((b, i) => b.from_seq !== batches[i].to_seq), [])
})

test('without a hub on the node the phone is not asked first; it is again once the hub is back', async (t) => {
  const n = await startFakeNode(t, { hub: false })
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5, answerFirst: 'phone' })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('listed', () => n.last('runtime.sessions'))
  assert.equal(rt.hubConnected, false)
  assert.equal(rt.phoneFirst('s1'), false, 'no hub: the local UI asks, even with answerFirst phone')
  n.setHub(true)
  await until('hub back', () => rt.hubConnected)
  assert.equal(rt.phoneFirst('s1'), true)
  n.setHub(false)
  await until('hub gone', () => !rt.hubConnected)
  assert.equal(rt.phoneFirst('s1'), false)
})

test('while the socket file is there (a node restarting) it retries every second, not on the long back-off', async (t) => {
  const { mkdtempSync, rmSync, writeFileSync } = await import('node:fs')
  const { join } = await import('node:path')
  const dir = mkdtempSync('/tmp/jrt-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const path = join(dir, 'runtime.sock')
  writeFileSync(path, '') // what a node that went away leaves: the file, nobody listening
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: path, retryMs: 8000 }) // a long back-off otherwise
  t.after(() => rt.stop())
  rt.start()
  await new Promise((r) => setTimeout(r, 300))
  rmSync(path)
  const started = Date.now()
  const n = await startFakeNode(t, { path }) // the node is back
  await n.ready
  assert.ok(Date.now() - started < 2000, `reconnected after ${Date.now() - started} ms`)
})

test('page: from the end by turns or limit, back from a seq, forward with a cut, and a span', async () => {
  const { page } = await import('../index.js')
  const it = (seq, role, kind = 'text', text = `${role}${seq}`) => ({ id: `${seq}#0`, seq, role, kind, text })
  const all = [it(1, 'user'), it(2, 'assistant'), it(3, 'user'), it(4, 'assistant'), { ...it(4, 'assistant', 'tool_use'), id: '4#1' }, it(5, 'user'), it(6, 'assistant')]
  const at = { history: 'h', toSeq: 9 }
  const last = page(all, { turns: 2 }, at)
  assert.deepEqual(last.items.map((i) => i.seq), [3, 4, 4, 5, 6])
  assert.equal(last.has_more, true)
  assert.equal(last.to_seq, 9)
  const back = page(all, { before_seq: 5, limit: 1 }, at)
  assert.deepEqual(back.items.map((i) => i.id), ['4#0', '4#1'], 'a record is not split')
  assert.equal(back.to_seq, 4)
  const fwd = page(all, { after_seq: 4 }, at)
  assert.deepEqual([fwd.from_seq, fwd.to_seq, fwd.more_after, fwd.items.length], [4, 9, false, 2])
  const span = page(all, { from_seq: 2, through_seq: 3 }, at)
  assert.deepEqual(span.items.map((i) => i.seq), [2, 3])
  const many = Array.from({ length: 600 }, (_, i) => it(i + 1, 'assistant'))
  const cut = page(many, { after_seq: 0 }, { history: 'h', toSeq: 600 })
  assert.deepEqual([cut.items.length, cut.more_after, cut.to_seq], [500, true, 500])
})

test('a runtime that requires a node feature stays off an older node until a new one replaces the socket', async (t) => {
  const { mkdtempSync, rmSync } = await import('node:fs')
  const { join } = await import('node:path')
  const dir = mkdtempSync('/tmp/jrt-')
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  const path = join(dir, 'runtime.sock')
  const old = await startFakeNode(t, { path, features: [] })
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: path, retryMs: 20, pushDelayMs: 5, requires: ['prefer_running'] })
  t.after(() => rt.stop())
  rt.start()
  await old.ready
  await new Promise((r) => setTimeout(r, 1500))
  assert.equal(rt.connected, false)
  assert.equal(old.all('runtime.sessions').length, 0, 'nothing listed to an older node')
  assert.equal(old.hellos, 1, 'not reconnecting to the older node meanwhile')
  old.close() // the node restarts as a newer one: a new socket file at the same path
  rmSync(path, { force: true })
  const fresh = await startFakeNode(t, { path })
  await fresh.ready
  await until('listed to the new node', () => fresh.last('runtime.sessions'), 10_000)
  assert.equal(rt.connected, true)
})

test('a send is made once per client_msg_id: a retry gets the first answer, a failed one may go again', async (t) => {
  const n = await startFakeNode(t)
  let calls = 0
  let fail = false
  const rt = createRuntime({ ...minimal(), send: async () => { calls++; if (fail) throw new Error('down') } }, { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await Promise.all([n.peer.call('session.send', { session_id: 's1', text: 'a', client_msg_id: 'm1' }), n.peer.call('session.send', { session_id: 's1', text: 'a', client_msg_id: 'm1' })])
  await n.peer.call('session.send', { session_id: 's1', text: 'a', client_msg_id: 'm1' })
  assert.equal(calls, 1)
  fail = true
  await assert.rejects(n.peer.call('session.send', { session_id: 's1', text: 'b', client_msg_id: 'm2' }))
  fail = false
  await n.peer.call('session.send', { session_id: 's1', text: 'b', client_msg_id: 'm2' })
  assert.equal(calls, 3)
  assert.equal(rt.touched('s1'), true)
  assert.equal(rt.touched('s2'), false)
})

test('recent: pinned and phone-touched sessions always, the rest by age up to the limit, newest first', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ send: async () => {} }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.send', { session_id: 'old-touched', text: 'x', client_msg_id: 'r1' })
  const day = 86400_000
  const row = (id, ago, running = false) => ({ session_id: id, updated_at: new Date(Date.now() - ago).toISOString(), running })
  const rows = [row('a', 1000), row('b', 2000), row('c', 3000), row('ancient', 30 * day), row('old-running', 40 * day, true), row('old-touched', 50 * day)]
  const got = rt.recent(rows, { days: 7, max: 4, pinned: (r) => r.running })
  assert.deepEqual(got.map((r) => r.session_id), ['a', 'b', 'old-running', 'old-touched'])
  const { appToolName } = await import('../index.js')
  assert.deepEqual([appToolName('bash'), appToolName('find', { find: 'Glob' }), appToolName('mystery')], ['Bash', 'Glob', 'mystery'])
})

test('hello carries one instance per runtime, the same on every reconnect', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  const first = n.hello.instance
  assert.match(first, /^[0-9a-f-]{36}$/)
  n.peer.close(new Error('node restarts'))
  await until('hello again', () => n.hellos >= 2)
  assert.equal(n.hello.instance, first)
  const other = createRuntime(minimal(), { runtime: 'toy2', socketPath: n.path, retryMs: 20 })
  t.after(() => other.stop())
  other.start()
  await until('the other runtime', () => n.hello.runtime === 'toy2')
  assert.notEqual(n.hello.instance, first)
})

test('a runtime id another process holds is not fought over: it is tried again only every 30 seconds', async (t) => {
  const n = await startFakeNode(t)
  n.conflict()
  const logs = []
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, log: (m) => logs.push(m) })
  t.after(() => rt.stop())
  rt.start()
  await until('refused', () => n.hellos >= 1)
  await new Promise((r) => setTimeout(r, 300))
  assert.equal(n.hellos, 1, 'not retried on the short back-off')
  assert.equal(rt.connected, false)
  assert.ok(logs.some((m) => m.includes('another runtime id')), logs.join('\n'))
})

test('the hub\'s tools: listed and called through the node, onHub after hello and as the hub comes and goes', async (t) => {
  const n = await startFakeNode(t, { tools: [{ name: 'phone_contacts', description: 'Contacts', input_schema: { type: 'object' } }] })
  const hubs = []
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, onHub: (c) => hubs.push(c) })
  t.after(() => rt.stop())
  assert.deepEqual(await rt.tools(), [], 'no node yet: none')
  rt.start()
  await n.ready
  await until('onHub after hello', () => hubs.length === 1)
  assert.deepEqual(hubs, [true])
  assert.deepEqual((await rt.tools()).map((x) => x.name), ['phone_contacts'])
  const res = await rt.callTool({ name: 'phone_contacts', args: { q: 'li' }, session_id: 's1' })
  assert.equal(res.content[0].text, 'phone_contacts ran')
  assert.deepEqual(n.toolCalls[0], { name: 'phone_contacts', args: { q: 'li' }, session_id: 's1' })
  n.setHub(false)
  await until('onHub: gone', () => hubs.length === 2)
  n.setHub(false) // no change: not called again
  n.setHub(true)
  await until('onHub: back', () => hubs.length === 3)
  assert.deepEqual(hubs, [true, false, true])
})

test('a refusal with data reaches the node with its data (delivered: none)', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal({ send: async () => { throw new RpcError('handed over', 'handover', { delivered: 'none' }) } }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await assert.rejects(n.peer.call('session.send', { session_id: 's1', text: 'x' }), (err) => err.code === 'handover' && err.data?.delivered === 'none')
})

test('a runtime running several agents: launchers in hello and when they change, modes, takeovers', async (t) => {
  const n = await startFakeNode(t)
  const got = []
  const launchers = [{ name: 'paseo-claude', agent: 'claude', model: 'Opus', models: [{ id: 'opus', name: 'Opus', default: true }] }]
  const rt = createRuntime(minimal({
    start: async (p) => {
      got.push(['start', p])
      return { session: { session_id: p.resume ?? 'new', status: 'idle' } }
    },
    modes: async (p) => got.push(['modes', p]),
  }), { runtime: 'paseo', socketPath: n.path, retryMs: 20, launchers, resume: true, readonly: true })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  assert.equal(n.hello.caps.modes, true)
  assert.equal(n.hello.caps.resume, true)
  assert.equal(n.hello.caps.readonly, true)
  assert.deepEqual(n.hello.launchers, launchers)
  await n.peer.call('session.modes', { session_id: 's1', permission_mode: 'plan', effort: 'high' })
  const res = await n.peer.call('session.start', { cwd: '/w', resume: 'cccccccc-0000-0000-0000-000000000001', launcher: 'paseo-claude', model: 'opus' })
  assert.equal(res.session.session_id, 'cccccccc-0000-0000-0000-000000000001')
  assert.deepEqual(got[0], ['modes', { session_id: 's1', permission_mode: 'plan', effort: 'high' }])
  assert.equal(got[1][1].model, 'opus')
  rt.launchersChanged([{ name: 'paseo-codex', agent: 'paseo' }])
  await until('runtime.launchers', () => n.last('runtime.launchers'))
  assert.deepEqual(n.last('runtime.launchers').launchers, [{ name: 'paseo-codex', agent: 'paseo' }])
})

test('archive: caps.archive only with both ways, calls reach the driver, one way alone is not offered', async (t) => {
  const n = await startFakeNode(t)
  const got = []
  const rt = createRuntime(minimal({
    archive: async (p) => got.push(['archive', p.session_id]),
    unarchive: async (p) => got.push(['unarchive', p.session_id]),
  }), { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  assert.equal(n.hello.caps.archive, true)
  assert.deepEqual(await n.peer.call('session.archive', { session_id: 's1' }), { ok: true })
  assert.deepEqual(await n.peer.call('session.unarchive', { session_id: 's1' }), { ok: true })
  assert.deepEqual(got, [['archive', 's1'], ['unarchive', 's1']])

  const half = await startFakeNode(t)
  const rt2 = createRuntime(minimal({ archive: async () => {} }), { runtime: 'half', socketPath: half.path, retryMs: 20 })
  t.after(() => rt2.stop())
  rt2.start()
  await half.ready
  assert.equal(half.hello.caps.archive, undefined)
  await assert.rejects(half.peer.call('session.archive', { session_id: 's1' }), (err) => err.code === 'unsupported')
})

test('a watch for a session the node reads from its transcript streams live text only', async (t) => {
  const n = await startFakeNode(t)
  let paged = 0
  const rt = createRuntime(minimal({ items: async () => { paged++; return { items: [], to_seq: 5 } } }), { runtime: 'paseo', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0, transcript: true })
  assert.equal(paged, 0, 'no catch-up: the items are the transcript\'s')
  assert.ok(rt.watching('s1'))
  rt.append('s1', { items: [{ id: 'x', seq: 6 }], fromSeq: 5, toSeq: 6, history: 'h' })
  rt.stream('s1', 'thinking about')
  await until('the live text', () => n.all('session.delta').length === 1)
  await new Promise((r) => setTimeout(r, 150))
  assert.equal(n.all('session.items').length, 0)
  assert.equal(n.all('session.delta')[0].text, 'thinking about')
})

test('the driver hears which sessions are watched: started, stopped, and all of them when the node is new', async (t) => {
  const n = await startFakeNode(t)
  const heard = []
  const rt = createRuntime(minimal({ cursor: async () => 0, watchChanged: (id, on, o) => heard.push([id, on, o?.transcript]) }),
    { runtime: 'toy', socketPath: n.path, retryMs: 20 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await n.peer.call('session.watch', { session_id: 's1', since_seq: 0 })
  await n.peer.call('session.watch', { session_id: 's2', since_seq: 0, transcript: true })
  await n.peer.call('session.unwatch', { session_id: 's1' })
  await n.peer.call('session.unwatch', { session_id: 'never' })
  assert.deepEqual(heard, [['s1', true, false], ['s2', true, true], ['s1', false, undefined]])
  n.peer.close(new Error('node restarting'))
  await until('a new hello', () => n.hellos === 2)
  assert.deepEqual(heard.at(-1), ['s2', false, undefined], 'a new node asks again for what it watches')
})

test('a card for a session not listed yet: the list goes out first, so the node takes the card', async (t) => {
  const n = await startFakeNode(t)
  let rows = []
  const rt = createRuntime(minimal({ listSessions: async () => rows }), { runtime: 'toy', socketPath: n.path, retryMs: 20, pushDelayMs: 5000 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('the first list', () => n.last('runtime.sessions'))
  const order = []
  const orig = n.peer.onEvent
  n.peer.onEvent = (event, params) => {
    if (event === 'runtime.sessions') order.push(['list', params.sessions.map((s) => s.session_id)])
    orig(event, params)
  }
  n.resolve = (ix) => {
    order.push(['card', ix.session_id])
    return { allow: true }
  }
  rows = [{ session_id: 'new', status: 'working' }]
  const res = await rt.ask({ id: 'toy-1', kind: 'permission', session_id: 'new', tool_name: 'Bash', tool_input: {} })
  assert.deepEqual(res, { allow: true })
  assert.deepEqual(order.slice(0, 2), [['list', ['new']], ['card', 'new']])
})

test('stop() then start() in the same tick dials once more, not again and again (dialing mode)', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  assert.equal(n.hellos, 1)
  rt.stop()
  rt.start() // as a runtime restarting itself does (Paseo's version changed)
  await new Promise((r) => setTimeout(r, 1000))
  assert.equal(n.hellos, 2, 'the old connection\'s close and tail do not dial again')
})

test('stop() then start() while hello is in flight: the failed hello of the old run does not dial again', async (t) => {
  const n = await startFakeNode(t)
  n.holdHello = new Promise(() => {}) // the first hello is never answered
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await until('hello sent', () => n.hellos === 1)
  n.holdHello = undefined // the next one is answered
  rt.stop()
  rt.start()
  await n.ready
  await new Promise((r) => setTimeout(r, 1000))
  assert.equal(n.hellos, 2, 'the old hello failing does not dial again')
})

test('stop() before the dial has connected: the connection is dropped, not taken (dialing mode)', async (t) => {
  const n = await startFakeNode(t)
  const rt = createRuntime(minimal(), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  rt.stop() // the socket is still connecting
  await new Promise((r) => setTimeout(r, 300))
  assert.equal(n.hellos, 0, 'a stopped runtime says no hello')
  assert.equal(rt.connected, false)
})

test('a card for a new session waits for a slow list already under way, then for the list with it', async (t) => {
  const n = await startFakeNode(t)
  let rows = []
  let slow = false
  const listSessions = async () => {
    const now = rows // what the list is when it is read
    if (slow) {
      slow = false // only the push under way is slow
      await new Promise((r) => setTimeout(r, 6000)) // longer than the 5 s listNow used to wait
    }
    return now
  }
  const rt = createRuntime(minimal({ listSessions }), { runtime: 'toy', socketPath: n.path, retryMs: 20, refreshMs: 0 })
  t.after(() => rt.stop())
  rt.start()
  await n.ready
  await until('the first list', () => n.last('runtime.sessions'))
  slow = true
  rt.sessionsChanged(0) // a slow push starts, before the session is there
  await new Promise((r) => setTimeout(r, 50))
  const order = []
  const orig = n.peer.onEvent
  n.peer.onEvent = (event, params) => {
    if (event === 'runtime.sessions') order.push(['list', params.sessions.map((s) => s.session_id)])
    orig(event, params)
  }
  n.resolve = (ix) => {
    order.push(['card', ix.session_id])
    return { allow: true }
  }
  rows = [{ session_id: 'new', status: 'working' }]
  const res = await rt.ask({ id: 'toy-2', kind: 'permission', session_id: 'new', tool_name: 'Bash', tool_input: {} })
  assert.deepEqual(res, { allow: true })
  const card = order.findIndex((e) => e[0] === 'card')
  assert.ok(order.slice(0, card).some((e) => e[0] === 'list' && e[1].includes('new')), JSON.stringify(order))
})
