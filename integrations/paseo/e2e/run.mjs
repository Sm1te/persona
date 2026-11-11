// End-to-end check of Paseo as a machine's engine (docs/integrations/paseo.md "Verification"): a real Paseo daemon (its own home and port,
// a Claude Code provider whose model is the scripted one in model.mjs), a real `personad node --stdio` started by the
// runtime SDK's harness (it plays the hub), and the bridge the node itself writes out and runs once the engine is set
// to Paseo. It starts a session from the "phone", watches it, sends to it, answers a permission, a question and a
// plan, switches the mode, interrupts, renames, has a card answered in Paseo's own app, lives through Paseo
// restarting, closes the session (archived in Paseo, it stays listed under its id) and sends to it again, archives it
// once more across a restart of the bridge and continues it, and checks agents started or archived in Paseo itself.
//
//   PERSONAD=<personad binary, built after go generate> CLAUDE_BIN=<a Claude Code binary> [PASEO_BIN=paseo] \
//     node integrations/paseo/e2e/run.mjs
//
// Nothing outside a fresh temp directory is touched: the daemon's home, the node's HOME (which is also the HOME Claude
// Code runs with, so its transcripts and its own config live there) and the workspace. The daemon listens on a free
// port; the bridge reaches it only through its home's paseo.pid.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { checks, startNode, until } from '../../runtime-sdk/testing/node-harness.mjs'
import { daemonTarget } from '../lib/discover.js'
import { PaseoClient } from '../lib/paseo.js'
import { startModel } from './model.mjs'

const personad = process.env.PERSONAD
const claudeBin = process.env.CLAUDE_BIN
const paseoBin = process.env.PASEO_BIN || 'paseo'
if (!personad || !claudeBin) {
  console.error('set PERSONAD (a personad binary built after go generate ./internal/runtimepkg) and CLAUDE_BIN (a Claude Code binary)')
  process.exit(2)
}
const keep = process.env.KEEP === '1'
const root = mkdtempSync(join('/tmp', 'jpaseo-e2e-'))
const paseoHome = join(root, 'paseo')
const home = join(root, 'home') // the node's and Claude Code's HOME
const claudeDir = join(home, '.claude')
const work = join(root, 'work')
for (const d of [paseoHome, home, claudeDir, work]) mkdirSync(d, { recursive: true })
const { check, summary } = checks()
const model = await startModel()
let daemon
let node
let paseo // the test's own client: Paseo's app answering in its place

const freePort = () => new Promise((r) => {
  const s = createServer()
  s.listen(0, '127.0.0.1', () => {
    const p = s.address().port
    s.close(() => r(p))
  })
})
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function stopDaemon() {
  if (!daemon || daemon.exitCode !== null) return
  const exited = new Promise((r) => daemon.once('exit', r))
  daemon.kill('SIGTERM')
  await Promise.race([exited, sleep(15_000)])
}

async function cleanup() {
  paseo?.stop()
  node?.stop()
  await stopDaemon()
  await model.close()
  if (!keep) rmSync(root, { recursive: true, force: true })
  else console.log(`kept ${root}`)
}
process.on('SIGINT', () => cleanup().finally(() => process.exit(130)))

// ---- Paseo: its own home and port; Claude Code's model is the scripted one ----

const port = await freePort()
const quiet = { DISABLE_AUTOUPDATER: '1', CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1', DISABLE_TELEMETRY: '1', DISABLE_ERROR_REPORTING: '1' }
writeFileSync(join(paseoHome, 'config.json'), JSON.stringify({
  $schema: 'https://paseo.sh/schemas/paseo.config.v1.json',
  version: 1,
  daemon: { listen: `127.0.0.1:${port}`, relay: { enabled: false }, mcp: { injectIntoAgents: false }, browserTools: { enabled: false } },
  pluginsEnabled: false,
  features: { webUi: { enabled: false } },
  agents: {
    metadataGeneration: { providers: [] },
    providers: {
      claude: { enabled: false }, codex: { enabled: false }, copilot: { enabled: false }, opencode: { enabled: false }, pi: { enabled: false }, omp: { enabled: false },
      'e2e-claude': { extends: 'claude', label: 'E2E Claude', enabled: true, command: [claudeBin],
        env: { ANTHROPIC_BASE_URL: model.url, ANTHROPIC_API_KEY: 'sk-e2e-test', CLAUDE_CONFIG_DIR: claudeDir, ...quiet } },
    },
  },
}, null, 2))

const daemonEnv = { PATH: process.env.PATH, HOME: home, PASEO_HOME: paseoHome, LANG: 'C.UTF-8' }
async function startDaemon() {
  daemon = spawn(paseoBin, ['daemon', 'run', '--home', paseoHome], { env: daemonEnv, stdio: ['ignore', 'ignore', 'pipe'] })
  daemon.stderr.on('data', () => {})
  await until('Paseo listening on its own port', () => daemonTarget(paseoHome).target?.port === port, 60_000)
}
await startDaemon()
check('Paseo runs in its own home, on its own port', daemonTarget(paseoHome).target?.port === port)

// ---- the node, with the engine set to Paseo: it runs the bridge ----

node = await startNode({ personad, home, env: { PASEO_HOME: paseoHome, ...quiet } })
await node.call('engine.use', { engine: 'paseo' })
const engine = await until('the Paseo engine available', async () => {
  const info = await node.call('node.info', {})
  const e = (info.engines ?? []).find((x) => x.id === 'paseo')
  return e?.available && e.version ? e : undefined
}, 60_000).catch(() => undefined)
check('node.info: Paseo available, with its version', engine?.version === '0.14.0' || /^\d+\.\d+/.test(engine?.version ?? ''), JSON.stringify(engine))

// A call that may fail: undefined then (the check says what is wrong).
const tryCall = (method, params, ms) => node.call(method, params, ms).catch((e) => {
  console.log(`  (${method}: ${e.message})`)
  return undefined
})
const launchers = (await node.call('launchers.list', {})).launchers ?? []
const launcher = launchers.find((l) => l.name === 'e2e-claude')
check('the new-session form offers Paseo\'s provider, as Claude Code', launcher?.agent_type === 'claude' || launcher?.agent === 'claude', JSON.stringify(launcher))
const models = await until('its models', async () => {
  const m = await tryCall('launchers.models', { name: 'e2e-claude' })
  return m?.models?.length ? m : undefined
}, 30_000).catch(() => ({}))
check('its models with their efforts', (models.models ?? []).some((m) => (m.efforts ?? []).includes('high')), JSON.stringify(models).slice(0, 300))

// ---- a session from the phone ----

const started = await node.call('session.start', { cwd: work, prompt: 'Please E2E_HELLO', launcher: 'e2e-claude', permission_mode: 'default' }, 60_000)
const sid = started.session?.session_id
check('a new session starts in Paseo', /^paseo-[0-9a-f-]{36}$/.test(sid ?? ''), sid)
await node.call('session.watch', { session_id: sid, since_seq: 0 })
const listed = () => node.sessions().find((s) => s.session_id === sid)
const texts = async (id = sid) => ((await tryCall('session.items', { session_id: id, limit: 200 }))?.items ?? [])
const hasText = async (t) => (await texts()).some((i) => typeof i.text === 'string' && i.text.includes(t))
await until('the first answer', () => hasText('Hello from the mock model, step by step.'), 60_000).catch(() => {})
check('its conversation is the Claude transcript (items from the node)', await hasText('Hello from the mock model, step by step.'))
await until('the row names its transcript', () => listed()?.status === 'done' || listed()?.status === 'idle', 30_000).catch(() => {})
check('listed as Claude Code, done, with its space', listed()?.agent === 'claude' && ['done', 'idle'].includes(listed()?.status) && listed()?.space_kind === 'paseo',
  JSON.stringify(listed()))
check('no second row for its transcript (未运行)', node.sessions().filter((s) => s.cwd === work).length === 1, node.sessions().map((s) => s.session_id).join(','))

// Live text while it writes, the message's own echo.
await node.call('session.send', { session_id: sid, text: 'Now E2E_HELLO again', client_msg_id: 'cm-e2e-1' })
await until('the second answer', async () => (await texts()).filter((i) => i.text === 'Hello from the mock model, step by step.').length >= 2, 60_000).catch(() => {})
const items = await texts()
check('a message from the phone and its answer', items.filter((i) => i.text === 'Hello from the mock model, step by step.').length >= 2)
check('the message carries its client_msg_id (the pending bubble becomes it)', items.some((i) => i.role === 'user' && i.client_msg_id === 'cm-e2e-1'),
  JSON.stringify(items.filter((i) => i.role === 'user').map((i) => [i.text, i.client_msg_id])))
check('live text while it was written', node.deltas(sid).some((t) => t.startsWith('Hello') && t.length < 'Hello from the mock model, step by step.'.length), JSON.stringify(node.deltas(sid).slice(0, 6)))

// ---- cards ----

node.resolve = (ix) => (ix.kind === 'permission' ? { allow: true } : { dismiss: true })
await node.call('session.send', { session_id: sid, text: 'Run E2E_BASH', client_msg_id: 'cm-e2e-2' })
await until('the tool ran', () => hasText('Tool said: e2e-ran'), 60_000).catch(() => {})
const perm = node.asked.find((ix) => ix.kind === 'permission')
check('a permission request reaches the phone as Claude Code\'s card', perm?.tool_name === 'Bash' && perm?.tool_input?.command?.includes('echo e2e-ran') && !perm.agent, JSON.stringify(perm)?.slice(0, 300))
check('allowed on the phone: the tool ran and the model got its output', await hasText('Tool said: e2e-ran'))
const after = await texts()
check('the call and its result in the chat', after.some((i) => i.kind === 'tool_use' && i.tool_name === 'Bash') && after.some((i) => i.kind === 'tool_result'))

node.resolve = (ix) => (ix.kind === 'question' ? { answers: { [ix.questions[0].question]: 'Blue' } } : { dismiss: true })
await node.call('session.send', { session_id: sid, text: 'E2E_ASK please', client_msg_id: 'cm-e2e-3' })
await until('the answer used', () => hasText('Blue'), 60_000).catch(() => {})
const q = node.asked.find((ix) => ix.kind === 'question')
check('a question reaches the phone with its options', q?.questions?.[0]?.options?.map((o) => o.label).join(',') === 'Red,Blue', JSON.stringify(q?.questions))
check('the phone\'s answer reaches the model', (await texts()).some((i) => i.kind === 'text' && i.role === 'assistant' && i.text.includes('Blue')))

await node.call('session.modes', { session_id: sid, permission_mode: 'plan' })
await until('plan mode', () => listed()?.permission_mode === 'plan', 20_000).catch(() => {})
check('the permission mode from the phone', listed()?.permission_mode === 'plan', listed()?.permission_mode)
node.resolve = (ix) => (ix.kind === 'plan' ? { approve: true, mode: 'acceptEdits' } : { dismiss: true })
await node.call('session.send', { session_id: sid, text: 'E2E_PLAN now', client_msg_id: 'cm-e2e-4' })
await until('the plan approved', () => listed()?.permission_mode === 'acceptEdits', 60_000).catch(() => {})
const plan = node.asked.find((ix) => ix.kind === 'plan')
check('a plan reaches the phone', plan?.plan?.includes('Write the test'), JSON.stringify(plan?.plan))
check('approved with a mode: Paseo implements in that mode', listed()?.permission_mode === 'acceptEdits', listed()?.permission_mode)

// A card answered in Paseo's own app leaves the phone.
paseo = new PaseoClient({ home: paseoHome })
const paseoUp = new Promise((r) => paseo.once('connected', r))
paseo.start()
await paseoUp
await node.call('session.modes', { session_id: sid, permission_mode: 'default' })
node.resolve = () => new Promise(() => {}) // the phone does not answer
const asked = node.asked.length
await node.call('session.send', { session_id: sid, text: 'Run E2E_BASH again', client_msg_id: 'cm-e2e-5' })
const card = await until('on the phone', () => node.asked.length > asked && node.asked.at(-1), 60_000).catch(() => undefined)
const agentId = sid.slice('paseo-'.length)
const pending = await until('pending in Paseo', async () => (await paseo.request('fetch_agent_request', { agentId })).agent?.pendingPermissions?.[0], 20_000).catch(() => undefined)
if (pending) {
  await paseo.request('agent_permission_response', { agentId, response: { behavior: 'allow' } }, { requestId: pending.id, expect: 'agent_permission_resolved' })
}
await until('withdrawn from the phone', () => node.events.some((e) => e.event === 'interaction.cancel' && e.params?.id === card?.id), 20_000).catch(() => {})
check('answered in Paseo: the card leaves the phone', card && node.events.some((e) => e.event === 'interaction.cancel' && e.params?.id === card.id), card?.id)

// ---- interrupt, rename ----

await until('idle again', () => ['done', 'idle'].includes(listed()?.status), 60_000).catch(() => {})
await node.call('session.send', { session_id: sid, text: 'E2E_SLOW story', client_msg_id: 'cm-e2e-6' })
await until('writing', () => node.deltas(sid).some((t) => t.includes('word3')), 60_000).catch(() => {})
await node.call('session.interrupt', { session_id: sid })
await until('stopped', () => ['done', 'idle'].includes(listed()?.status), 30_000).catch(() => {})
check('interrupted from the phone', ['done', 'idle'].includes(listed()?.status) && !node.deltas(sid).some((t) => t.includes('word59')), listed()?.status)
await node.call('session.rename', { session_id: sid, title: 'E2E 改名' })
await until('renamed', () => listed()?.title === 'E2E 改名', 20_000).catch(() => {})
check('renamed from the phone', listed()?.title === 'E2E 改名', listed()?.title)

// ---- Paseo restarting ----

await stopDaemon()
await startDaemon()
const restarted = Date.now()
await until('reconnected', async () => {
  if ((await tryCall('session.send', { session_id: sid, text: 'After restart E2E_HELLO', client_msg_id: 'cm-e2e-7' })) !== undefined) return true
  await sleep(500)
}, 90_000).catch(() => {})
console.log(`  (the bridge took ${((Date.now() - restarted) / 1000).toFixed(1)} s to be back after Paseo restarted)`)
await until('answered after the restart', async () => (await texts()).some((i) => i.client_msg_id === 'cm-e2e-7'), 60_000).catch(() => {})
check('Paseo restarted: the bridge is back and the session goes on', (await texts()).some((i) => i.client_msg_id === 'cm-e2e-7'))
paseo.stop()
paseo = new PaseoClient({ home: paseoHome })
const paseoBack = new Promise((r) => paseo.once('connected', r))
paseo.start()
await paseoBack

// ---- close: the session stays under its id, not running, and goes on under it (#4771) ----

const transcript = (await tryCall('session.info', { session_id: sid }))?.transcript_path
const claudeSid = transcript?.match(/([0-9a-f-]{36})\.jsonl$/)?.[1]
// Every list the node sends from here on: the conversation is never a row under its transcript's id, a second id.
const closedAt = node.events.length
const underTranscript = (from = closedAt) => node.events.slice(from)
  .some((e) => e.event === 'sessions.changed' && e.params.sessions?.some((s) => s.session_id === claudeSid))
const agentNow = async () => (await paseo.request('fetch_agent_request', { agentId })).agent
await tryCall('session.close', { session_id: sid })
await until('not running', () => listed()?.status === 'offline', 30_000).catch(() => {})
check('closed from the phone: archived in Paseo, and still listed under the same id, not running',
  claudeSid && (await agentNow())?.archivedAt && listed()?.status === 'offline' && listed()?.title === 'E2E 改名', JSON.stringify(listed()))
check('its conversation still reads under that id', await hasText('Hello from the mock model, step by step.') &&
  (await texts()).some((i) => i.client_msg_id === 'cm-e2e-7'))
await tryCall('session.send', { session_id: sid, text: 'Back again E2E_HELLO', client_msg_id: 'cm-e2e-8' }, 60_000)
await until('answered', async () => (await texts()).some((i) => i.client_msg_id === 'cm-e2e-8'), 60_000).catch(() => {})
check('a message to it: Paseo takes it back from the archive, and it goes on under the same id',
  (await texts()).some((i) => i.client_msg_id === 'cm-e2e-8') && listed()?.status !== 'offline', listed()?.status)
const back = await agentNow()
check('the same Paseo agent, back from the archive, its label unchanged', back && !back.archivedAt && back.labels?.['persona.session'] === sid,
  JSON.stringify(back?.labels))

// Archived again, and the bridge started afresh (the engine switched away and back): listed under its id once the
// bridge has read Paseo's archive, and continued (session.start with resume) under that id.
await until('done again', () => ['done', 'idle'].includes(listed()?.status), 60_000).catch(() => {})
await tryCall('session.close', { session_id: sid })
await until('not running again', () => listed()?.status === 'offline', 30_000).catch(() => {})
await node.call('engine.use', { engine: 'herdr' })
await until('the bridge gone', () => !listed(), 30_000).catch(() => {})
const restartedAt = node.events.length
await node.call('engine.use', { engine: 'paseo' })
await until('listed again', () => listed()?.status === 'offline', 60_000).catch(() => {})
check('the bridge restarted: the archived session is listed under its id again', listed()?.status === 'offline' && !underTranscript(restartedAt),
  JSON.stringify(listed()))
const resumed = await tryCall('session.start', { cwd: work, resume_session_id: sid }, 60_000)
await until('back from the archive', async () => !(await agentNow())?.archivedAt, 30_000).catch(() => {})
check('resumed (session.start): the same agent, under the same id', resumed?.session?.session_id === sid &&
  !(await agentNow())?.archivedAt && (await agentNow())?.labels?.['persona.session'] === sid, JSON.stringify(resumed?.session?.session_id))
check('one row for the conversation throughout: never one under its transcript id', claudeSid && !underTranscript() &&
  node.sessions().filter((s) => s.cwd === work).length === 1, node.sessions().filter((s) => s.cwd === work).map((s) => s.session_id).join(','))

// ---- agents started in Paseo itself, never driven from the phone ----

// Paseo runs Claude Code's command line itself, so such an agent's transcript reads like a script's one-shot run, which
// the node leaves out. Archived in Paseo's own app it is a session not running all the same: the bridge names it. One
// deleted there is gone: Paseo says so only to the client that deleted it (this script's), and the bridge finds out.
const inPaseo = async () => {
  const res = await paseo.request('agent.create.request', { agentId: randomUUID(), config: { provider: 'e2e-claude', cwd: work }, initialPrompt: 'Please E2E_HELLO' },
    { timeoutMs: 60_000 }).catch(() => undefined)
  const id = res?.agent?.id
  return until('its first turn over', async () => {
    const a = id && (await paseo.request('fetch_agent_request', { agentId: id })).agent
    const claude = a?.persistence?.sessionId ?? a?.runtimeInfo?.sessionId
    return a?.status === 'idle' && claude ? { id, claude } : undefined
  }, 60_000).catch(() => undefined)
}
const row = (a, runtime) => node.sessions().find((s) => s.session_id === a?.claude && (s.source_kind === 'runtime') === runtime)
const kept = await inPaseo()
const gone = await inPaseo()
await until('both listed', () => row(kept, true) && row(gone, true), 30_000).catch(() => {})
check('agents started in Paseo are sessions under their Claude ids', kept && gone && row(kept, true) && row(gone, true), `${kept?.claude} ${gone?.claude}`)
await paseo.request('archive_agent_request', { agentId: kept?.id }).catch(() => {})
await paseo.request('delete_agent_request', { agentId: gone?.id }).catch(() => {})
await until('the archived one listed as 未运行', () => !row(kept, true) && row(kept, false)?.status === 'offline', 60_000).catch(() => {})
check('archived in Paseo\'s own app: its transcript is a session not running (未运行)', !row(kept, true) && row(kept, false)?.status === 'offline', kept?.claude)
// The node scanned the transcripts afresh to list the archived one: the deleted one's was there to be listed too.
await until('the deleted one off the list', () => !row(gone, true), 30_000).catch(() => {})
check('deleted in Paseo: gone from the list, and its transcript is not listed', gone && !node.sessions().some((s) => s.session_id === gone.claude),
  JSON.stringify(node.sessions().filter((s) => s.session_id === gone?.claude).map((s) => [s.source_kind, s.status])))

// ---- the engine changed: the bridge stops ----

await node.call('engine.use', { engine: 'herdr' })
await until('the bridge gone', () => !node.sessions().some((s) => s.source_kind === 'runtime' && s.space_kind === 'paseo'), 30_000).catch(() => {})
check('another engine: Paseo\'s sessions leave with the bridge', !node.sessions().some((s) => s.space_kind === 'paseo'))
check('the model was only the scripted one', model.requests.length > 0, `${model.requests.length} requests`)

const failed = summary()
if (failed || process.env.DEBUG === '1') {
  console.log('--- the node\'s log (bridge lines) ---')
  console.log(node.log.join('').split('\n').filter((l) => /paseo|runtime/i.test(l)).slice(-60).join('\n'))
}
await cleanup()
process.exit(failed ? 1 : 0)
