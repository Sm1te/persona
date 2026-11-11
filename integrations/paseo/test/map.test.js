import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  agentRoot, agentRow, archivedUnderOwnId, codexThreadOf, backgroundOf, defaultProvider, interactionOf, launchersOf, linkedWorktree,
  parentAgentId, paseoMode, providerRoots, responseOf, rowStatus, projectSpaces, sessionIdOf, slotNameOf, spaceIdOf, strayRecord,
  timelineItems, toolCall, transcriptOf,
} from '../lib/map.js'
import { agentSnapshot, entry } from './fake-paseo.js'

const CLAUDE_SID = 'aaaaaaaa-0000-4000-8000-000000000001'

// Providers as Paseo's config has them: built-ins, and custom ones extending them (a chain too).
const CONFIG = {
  claude: { enabled: false },
  'my-claude': { extends: 'claude', label: 'My Claude' },
  'my-claude2': { extends: 'my-claude' },
  'my-codex': { extends: 'codex', label: 'My Codex' },
  grok: { extends: 'acp', label: 'Grok' },
}

test('a custom provider is the built-in it extends, through a chain; an agent records its own', () => {
  const roots = providerRoots(CONFIG, [{ provider: 'pi' }])
  assert.equal(roots['my-claude'], 'claude')
  assert.equal(roots['my-claude2'], 'claude')
  assert.equal(roots['my-codex'], 'codex')
  assert.equal(roots.grok, 'acp')
  assert.equal(roots.pi, 'pi')
  assert.equal(providerRoots({ a: { extends: 'b' }, b: { extends: 'a' } }).a, 'a', 'a loop ends')
  assert.equal(agentRoot({ provider: 'odd', persistence: { metadata: { provider: 'claude' } } }, roots), 'claude')
  assert.equal(agentRoot({ provider: 'my-claude2' }, roots), 'claude')
})

test("an agent's id: Persona's label, else Claude's session for Claude Code, else paseo-<agent id>", () => {
  const roots = providerRoots(CONFIG)
  const claude = agentSnapshot({ provider: 'my-claude', persistence: { provider: 'my-claude', sessionId: CLAUDE_SID, metadata: { provider: 'claude' } } })
  assert.equal(sessionIdOf(claude, roots), CLAUDE_SID)
  assert.equal(transcriptOf(claude, roots), CLAUDE_SID)
  const fresh = agentSnapshot({ provider: 'my-claude', runtimeInfo: { sessionId: CLAUDE_SID } })
  assert.equal(sessionIdOf(fresh, roots), CLAUDE_SID, 'the session its runtime starts with')
  assert.equal(transcriptOf(fresh, roots), undefined, 'no transcript before it is persisted')
  assert.equal(sessionIdOf(agentSnapshot({ provider: 'my-claude' }), roots), undefined, 'none yet: not listed')
  const labeled = agentSnapshot({ provider: 'my-claude', labels: { 'persona.session': 'paseo-x1' }, persistence: { sessionId: CLAUDE_SID } })
  assert.equal(sessionIdOf(labeled, roots), 'paseo-x1')
  assert.equal(transcriptOf(labeled, roots), CLAUDE_SID)
  const codex = agentSnapshot({ id: 'c0ffee', provider: 'my-codex', persistence: { sessionId: '0199-thread' } })
  assert.equal(sessionIdOf(codex, roots), 'paseo-c0ffee')
  assert.equal(transcriptOf(codex, roots), undefined, 'only Claude Code has a transcript the node reads')
  // A Codex agent names the thread its conversation is: the node lists it once, not again from its rollout (#4495).
  const THREAD = '0199aaaa-bbbb-7ccc-8ddd-eeeeffff0001'
  assert.equal(codexThreadOf(codex, roots), undefined, 'no thread id yet (Codex still starting): nothing named')
  const threaded = agentSnapshot({ id: 'c0ffee', provider: 'my-codex', persistence: { sessionId: THREAD, metadata: { provider: 'codex', threadId: THREAD } } })
  assert.equal(codexThreadOf(threaded, roots), THREAD)
  assert.equal(codexThreadOf(agentSnapshot({ provider: 'my-codex', runtimeInfo: { sessionId: THREAD } }), roots), THREAD, 'not persisted yet: the one its runtime runs')
  assert.equal(codexThreadOf(claude, roots), undefined, 'an agent of Claude Code has a transcript, no thread')
  assert.deepEqual([agentRow(threaded, { roots }).codex_thread, agentRow(codex, { roots }).codex_thread, agentRow(claude, { roots }).codex_thread],
    [THREAD, undefined, undefined])
  assert.equal(sessionIdOf(agentSnapshot({ provider: 'grok', labels: { 'persona.session': '../bad' } }), roots).startsWith('paseo-'), true, 'a bad label is not an id')
})

test('status: needing you while a request waits; working; done after a turn; 未运行 when closed', () => {
  assert.equal(rowStatus({ status: 'running', pendingPermissions: [{ id: 'p' }] }), 'blocked')
  assert.equal(rowStatus({ status: 'running' }), 'working')
  assert.equal(rowStatus({ status: 'initializing' }), 'working')
  assert.equal(rowStatus({ status: 'idle', lastUserMessageAt: '2026-01-01T00:00:00Z' }), 'done')
  assert.equal(rowStatus({ status: 'idle', lastUserMessageAt: null }), 'idle')
  assert.equal(rowStatus({ status: 'closed' }), 'offline')
  assert.equal(rowStatus({ status: 'error' }), 'done')
  // Paseo's own seen state (#2831): waiting for you to look at it, or already seen in Paseo.
  const ran = '2026-01-01T00:00:00Z'
  assert.equal(rowStatus({ status: 'idle', lastUserMessageAt: ran, requiresAttention: true, attentionReason: 'finished' }), 'done')
  assert.equal(rowStatus({ status: 'idle', lastUserMessageAt: ran, requiresAttention: false }), 'idle', 'seen in Paseo')
  assert.equal(rowStatus({ status: 'error', requiresAttention: false }), 'idle')
  assert.equal(rowStatus({ status: 'error', requiresAttention: true, attentionReason: 'error' }), 'done')
  assert.equal(rowStatus({ status: 'running', requiresAttention: true }), 'working', 'running first')
  assert.equal(rowStatus({ status: 'closed', requiresAttention: true }), 'offline')
  assert.equal(rowStatus({ status: 'idle', requiresAttention: true, pendingPermissions: [{ id: 'p' }] }), 'blocked')
})

test('a row: its space, the agent the apps know, model, modes and transcript', () => {
  const roots = providerRoots(CONFIG)
  const a = agentSnapshot({
    provider: 'my-claude', cwd: '/w/proj', workspaceId: 'wks_9', title: '  修登录  ', model: 'claude-x[1m]', currentModeId: 'acceptEdits',
    effectiveThinkingOptionId: 'max', persistence: { sessionId: CLAUDE_SID, metadata: { provider: 'claude' } }, updatedAt: '2026-11-04T10:00:00.000Z',
  })
  const row = agentRow(a, { roots, project: { projectKey: 'prj', projectName: 'proj', workspaceName: '登录修复' } })
  assert.deepEqual(row, {
    session_id: CLAUDE_SID, title: '修登录', cwd: '/w/proj', status: 'idle', updated_at: '2026-11-04T10:00:00.000Z',
    rev: Date.parse('2026-11-04T10:00:00.000Z'), space_id: 'prj', space_name: 'proj', slot_name: '登录修复',
    agent: 'claude', launcher: 'my-claude',
    model: 'claude-x[1m]', permission_mode: 'acceptEdits', effort: 'max', transcript: CLAUDE_SID,
  })
  const g = agentRow(agentSnapshot({ id: 'g1', provider: 'grok', cwd: '/w/x', currentModeId: 'yolo', status: 'error', lastError: 'boom' }),
    { roots, labels: { grok: 'Grok · Official' }, lastText: 'ignored' })
  assert.equal(g.agent, 'paseo')
  assert.equal(g.agent_label, 'Grok · Official')
  assert.equal(g.title, 'x')
  assert.equal(g.permission_mode, undefined, "a mode Persona does not have is not shown")
  assert.equal(g.last_text, '出错了：boom')
  assert.equal(agentRow(agentSnapshot({ provider: 'grok', archivedAt: '2026-01-01T00:00:00Z' }), { roots }), undefined)
})

test("an archived agent keeps its row when its id is not its transcript's: a session Persona started, not one of Paseo's own (#4771)", () => {
  const roots = providerRoots(CONFIG)
  const at = '2026-11-04T10:00:00.000Z'
  const claude = (fields) => agentSnapshot({ provider: 'my-claude', cwd: '/w/proj', title: 'T', status: 'closed', archivedAt: at, updatedAt: at,
    persistence: { provider: 'my-claude', sessionId: CLAUDE_SID, metadata: { provider: 'claude' } }, ...fields })
  const started = claude({ id: 'a1', labels: { 'persona.session': 'paseo-a1' } })
  assert.equal(archivedUnderOwnId(started, roots), true, 'started from Persona: paseo-<agent id>, its transcript another id')
  assert.equal(archivedUnderOwnId(claude({}), roots), false, "started in Paseo: its id is its transcript's, which the node lists")
  assert.equal(archivedUnderOwnId(claude({ labels: { 'persona.session': CLAUDE_SID } }), roots), false, 'taken over: the same id either way')
  assert.equal(archivedUnderOwnId({ ...started, archivedAt: null }, roots), false, 'not archived: a row as any other')
  assert.equal(archivedUnderOwnId(claude({ labels: { 'persona.session': 'paseo-a1', 'paseo.parent-agent-id': 'boss' } }), roots), false,
    "another agent created it: its creator's conversation says what it did")
  assert.equal(archivedUnderOwnId({ ...started, persistence: null }, roots), false, 'no transcript: nothing to read')
  assert.equal(archivedUnderOwnId(agentSnapshot({ id: 'c1', provider: 'my-codex', archivedAt: at, labels: { 'persona.session': 'paseo-c1' } }), roots),
    false, 'not Claude Code: no transcript the node reads')
  // Its row: the session's own id, not running, its transcript the conversation.
  const row = agentRow(started, { roots, project: { projectKey: 'prj', projectName: 'proj' }, archived: true })
  assert.deepEqual([row.session_id, row.status, row.transcript, row.title, row.space_id, row.updated_at], ['paseo-a1', 'offline', CLAUDE_SID, 'T', 'prj', at])
  assert.equal(agentRow({ ...started, status: 'idle' }, { roots, archived: true }).status, 'offline', 'whatever its record says')
  assert.equal(agentRow(started, { roots }), undefined, 'without archived: no row, as before')
})

test("a delegated agent is its creator's subtask: its parent, no waiting to be looked at; the unfinished ones are the creator's background work", () => {
  const roots = providerRoots(CONFIG)
  const child = (o = {}) => agentSnapshot({ id: 'kid-1', provider: 'my-codex', cwd: '/w/proj', title: '评审 E5（GPT）', createdAt: '2026-11-04T09:00:00.000Z',
    labels: { 'paseo.parent-agent-id': ' boss-1 ', 'paseo.parent-server-id': 'srv_1' }, ...o })
  assert.equal(parentAgentId(child()), 'boss-1')
  assert.deepEqual([parentAgentId(agentSnapshot()), parentAgentId({ labels: { 'paseo.parent-agent-id': '  ' } }), parentAgentId(undefined)], [undefined, undefined, undefined])
  // Paseo sets the flag on a delegated agent too, but shows it on no workspace, and nobody opens it there: left to
  // the flag it would stay "done, not looked at" for good. The bridge says which agents are quiet that way.
  const ran = '2026-11-04T09:30:00.000Z'
  const quiet = { quiet: true }
  assert.equal(rowStatus(child({ status: 'idle', lastUserMessageAt: ran, requiresAttention: true, attentionReason: 'finished' }), quiet), 'idle')
  assert.equal(rowStatus(child({ status: 'error', requiresAttention: true, attentionReason: 'error' }), quiet), 'idle')
  assert.equal(rowStatus(child({ status: 'idle', lastUserMessageAt: ran }), quiet), 'idle', 'a Paseo without the flag too')
  assert.deepEqual([rowStatus(child({ status: 'running' }), quiet), rowStatus(child({ status: 'closed', requiresAttention: true }), quiet),
    rowStatus(child({ status: 'idle', pendingPermissions: [{ id: 'p' }] }), quiet)], ['working', 'offline', 'blocked'], 'what it does now is still said')
  assert.equal(rowStatus(child({ status: 'idle', lastUserMessageAt: ran, requiresAttention: true })), 'done',
    "not for its label alone: one created from another workspace is that workspace's own, looked at like any agent")

  const row = agentRow(child({ status: 'running' }), { roots, parent: CLAUDE_SID, quiet: true })
  assert.deepEqual([row.is_subagent, row.parent_session_id, row.status], [true, CLAUDE_SID, 'working'])
  assert.equal(agentRow(child({ status: 'idle', requiresAttention: true }), { roots, parent: CLAUDE_SID, quiet: true }).status, 'idle')
  const own = agentRow(child({ status: 'running' }), { roots })
  assert.deepEqual([own.is_subagent, own.parent_session_id], [undefined, undefined], 'its creator is not listed beside it: a row of its own')

  const boss = agentSnapshot({ id: 'boss-1', provider: 'my-claude', cwd: '/w/proj', status: 'idle', requiresAttention: true,
    persistence: { sessionId: CLAUDE_SID, metadata: { provider: 'claude' } } })
  const led = agentRow(boss, { roots, fleet: [child({ status: 'running' }), child({ id: 'kid-2', title: '  ', status: 'running' })] })
  assert.deepEqual(led.background, [
    { id: 'paseo-kid-1', kind: 'agent', description: '评审 E5（GPT）', started_at: '2026-11-04T09:00:00.000Z' },
    { id: 'paseo-kid-2', kind: 'agent', started_at: '2026-11-04T09:00:00.000Z' },
  ])
  assert.equal(led.status, 'done', "its own turn ended; the hub counts it as working while they run")
  assert.equal(agentRow(boss, { roots, fleet: [] }).background, undefined)
  // Paseo let its process go while they still run: their reports wake it, so it is not "Offline" (which a list of
  // what runs leaves out, with them under it). With none left, it is.
  const let_go = { ...boss, status: 'closed' }
  assert.deepEqual([agentRow(let_go, { roots, fleet: [child({ status: 'running' })] }).status, agentRow(let_go, { roots }).status,
    rowStatus(let_go, { fleet: true }), rowStatus(let_go)], ['idle', 'offline', 'idle', 'offline'])
  assert.equal(backgroundOf({ title: 'no id' }), undefined)
})

test("a Codex row: Paseo's full-access is Persona's bypassPermissions, the other Codex modes are not reported", () => {
  const roots = providerRoots(CONFIG)
  const row = (currentModeId) => agentRow(agentSnapshot({ provider: 'my-codex', cwd: '/w/x', currentModeId }), { roots })
  assert.equal(row('full-access').permission_mode, 'bypassPermissions')
  assert.equal(row('auto').permission_mode, undefined)
  assert.equal(row('auto-review').permission_mode, undefined)
  assert.deepEqual([paseoMode('codex', 'bypassPermissions'), paseoMode('codex', 'auto'), paseoMode('claude', 'bypassPermissions')],
    ['full-access', undefined, 'bypassPermissions'])
  assert.equal(paseoMode('acp', 'bypassPermissions'), undefined)
})

test("spaces: a project's checkout and its worktrees are one space; a workspace names where an agent is, and is no slot", () => {
  // The placement's projectKey is Paseo's project record id.
  const at = (dir, o = {}) => ({ cwd: dir, isGit: true, currentBranch: o.branch ?? null, remoteUrl: null, worktreeRoot: dir,
    isPaseoOwnedWorktree: o.paseo ?? false, mainRepoRoot: o.main ?? null })
  const main = { projectKey: 'prj_app', projectName: 'app', workspaceName: '主线', checkout: at('/w/app') }
  const wt = { projectKey: 'prj_app', projectName: 'app', workspaceName: '修登录', checkout: at('/wt/brave-fox', { paseo: true, main: '/w/app' }) }
  // Paseo handed the worktree's directory alone (#2802): a record of its own, named after the folder.
  const stray = { projectKey: 'prj_stray', projectName: 'brave-fox', workspaceName: '  ',
    checkout: at('/wt/brave-fox', { paseo: true, main: '/w/app', branch: 'fix-login' }) }
  const gitWt = { projectKey: 'prj_app', projectName: 'app', workspaceName: null, checkout: at('/w/app-hotfix', { main: '/w/app' }) }
  const folder = { projectKey: 'prj_notes', projectName: 'notes', workspaceName: '周报', checkout: { cwd: '/w/notes', isGit: false } }
  assert.deepEqual([main, wt, stray, gitWt, folder].map(linkedWorktree), [false, true, true, true, false])
  assert.deepEqual([main, wt, stray, gitWt, folder].map(strayRecord), [false, false, true, false, false])
  assert.equal(spaceIdOf(wt), 'prj_app')
  assert.equal(spaceIdOf(undefined), undefined)
  assert.deepEqual([slotNameOf(wt, '/wt/brave-fox'), slotNameOf(stray, '/wt/brave-fox'), slotNameOf(gitWt, '/w/app-hotfix')],
    ['修登录', 'fix-login', 'app-hotfix'], "the workspace's title, else its branch, else its folder")

  const s = projectSpaces([main, wt, stray, gitWt, folder].map((project) => ({ project })))
  assert.deepEqual([main, wt, stray, gitWt, folder, undefined].map(s.idOf), ['prj_app', 'prj_app', 'prj_app', 'prj_app', 'prj_notes', undefined],
    "the stray record goes into its repository's project")
  assert.deepEqual(Object.fromEntries(s.names), { prj_app: 'app', prj_notes: 'notes' })
  const alone = projectSpaces([{ project: stray }])
  assert.deepEqual([alone.idOf(stray), Object.fromEntries(alone.names)], ['prj_stray', { prj_stray: 'app' }],
    "its repository's project unknown: a space of its own, named after the repository's folder")
  // The main checkout reached through a symbolic link: compared as the bridge resolves it.
  const linked = { ...main, checkout: at('/home/me/app') }
  const viaLink = projectSpaces([linked, stray].map((project) => ({ project })), (p) => p.replace('/home/me/', '/w/'))
  assert.equal(viaLink.idOf(stray), 'prj_app')

  const roots = providerRoots(CONFIG)
  const row = (workspaceId, project) => agentRow(agentSnapshot({ provider: 'grok', cwd: project.checkout.cwd, workspaceId }),
    { roots, project, spaceId: s.idOf(project), spaceName: s.names.get(s.idOf(project)) })
  const rows = [row('wks_main', main), row('wks_wt', wt), row('wks_stray', stray)]
  assert.deepEqual(rows.map((r) => [r.space_id, r.space_name, r.slot_id, r.slot_name]), [
    ['prj_app', 'app', undefined, '主线'], ['prj_app', 'app', undefined, '修登录'], ['prj_app', 'app', undefined, 'fix-login'],
  ], "no slot id: the apps draw the agents of one slot as one tab's panes (#4493)")
  assert.deepEqual(rows.map((r) => r.repo_root), ['/w/app', '/w/app', '/w/app'], "按仓库: the repository's main checkout (#2840)")
  assert.equal(row('wks_notes', folder).repo_root, undefined, 'not a git repository: the path says enough')
  const physical = agentRow(agentSnapshot({ provider: 'grok', cwd: main.checkout.cwd, workspaceId: 'wks_main' }),
    { roots, project: main, canon: (path) => `/data${path}` })
  assert.equal(physical.repo_root, '/data/w/app', 'by its physical directory, as the bridge resolves it (#4492)')
  const bare = agentRow(agentSnapshot({ provider: 'grok', cwd: '/w/x', workspaceId: 'wks_x' }), { roots })
  assert.deepEqual([bare.space_id, bare.space_name, bare.slot_id], ['wks_x', 'x', undefined], 'no placement: its workspace, as before')
})

test("launchers: one per usable provider, with the agent it starts and its models' Persona efforts", () => {
  const entries = [
    { provider: 'claude', enabled: false, status: 'unavailable', models: [] },
    { provider: 'my-claude', enabled: true, status: 'ready', label: 'My Claude', models: [
      { id: 'fable[1m]', label: 'Fable (1M)', isDefault: true, thinkingOptions: [{ id: 'low' }, { id: 'medium' }, { id: 'high' }, { id: 'max' }], defaultThinkingOptionId: 'max' },
      { id: 'haiku', label: 'Haiku', thinkingOptions: [], isSelectable: true },
      { id: 'hidden', label: 'Hidden', isSelectable: false },
    ] },
    { provider: 'my-codex', enabled: true, status: 'loading', label: 'My Codex', models: [] },
    { provider: 'grok', enabled: true, status: 'ready', label: 'Grok', models: [{ id: 'g', label: 'G', thinkingOptions: [{ id: 'xhigh' }, { id: 'ultra' }], defaultThinkingOptionId: 'ultra' }] },
    { provider: 'bad.name', enabled: true, status: 'ready', models: [] },
  ]
  const roots = providerRoots(CONFIG, entries)
  assert.deepEqual(launchersOf(entries, roots), [
    { name: 'my-claude', agent: 'claude', model: 'Fable (1M)', models: [
      { id: 'fable[1m]', name: 'Fable (1M)', default: true, efforts: ['low', 'medium', 'high', 'max'], default_effort: 'max' },
      { id: 'haiku', name: 'Haiku' },
    ] },
    { name: 'my-codex', agent: 'codex', model: 'My Codex', models: [] },
    { name: 'grok', agent: 'paseo', model: 'G', models: [{ id: 'g', name: 'G', efforts: ['xhigh'] }] },
  ])
  assert.equal(defaultProvider(entries, roots), 'my-claude', "Claude Code's own is off: the first running Claude Code")
  assert.equal(defaultProvider([{ provider: 'claude', enabled: true, status: 'ready' }, ...entries], roots), 'claude')
})

test("tool calls as the apps draw them: Claude Code's names and arguments", () => {
  assert.deepEqual(toolCall('x', { type: 'shell', command: 'ls -la', cwd: '/w' }), { name: 'Bash', input: { command: 'ls -la', cwd: '/w' } })
  assert.deepEqual(toolCall('x', { type: 'edit', filePath: '/a.go', oldString: 'a', newString: 'b' }), { name: 'Edit', input: { file_path: '/a.go', old_string: 'a', new_string: 'b' } })
  assert.deepEqual(toolCall('x', { type: 'search', query: '*.go', toolName: 'glob' }), { name: 'Glob', input: { pattern: '*.go' } })
  assert.deepEqual(toolCall('x', { type: 'search', query: 'paseo', toolName: 'web_search' }), { name: 'WebSearch', input: { query: 'paseo' } })
  assert.deepEqual(toolCall('mcp__x__y', { type: 'unknown', input: { a: 1 }, output: null }), { name: 'mcp__x__y', input: { a: 1 } })
  assert.deepEqual(toolCall('read', { type: 'read', filePath: '/r', offset: 3 }), { name: 'Read', input: { file_path: '/r', offset: 3 } })
})

test('a timeline as items: results where they finished, the text being written held back, echoes tagged', () => {
  const entries = [
    entry(1, { type: 'user_message', text: '看看测试', messageId: 'm1' }),
    entry(2, { type: 'reasoning', text: '先跑一下' }, 3),
    entry(4, { type: 'tool_call', callId: 'call_1', name: 'shell', status: 'completed', error: null, detail: { type: 'shell', command: 'npm test', output: 'ok', exitCode: 1 } }, 9),
    entry(5, { type: 'tool_call', callId: 'call_2', name: 'read', status: 'running', error: null, detail: { type: 'read', filePath: '/a' } }, 6),
    entry(7, { type: 'assistant_message', text: '测试过了' }, 8),
    entry(10, { type: 'assistant_message', text: '还在写' }, 12),
  ]
  const done = timelineItems(entries, { maxSeq: 12, running: false, echoes: new Map([['m1', 'cm-1']]) })
  assert.deepEqual(done.items.map((i) => [i.id, i.kind, i.text ?? i.tool_name]), [
    ['1#0', 'text', '看看测试'], ['2#0', 'thinking', '先跑一下'], ['4#0', 'tool_use', 'Bash'], ['5#0', 'tool_use', 'Read'],
    ['7#0', 'text', '测试过了'], ['9#0', 'tool_result', 'ok\n（退出码 1）'], ['10#0', 'text', '还在写'],
  ])
  assert.equal(done.items[0].client_msg_id, 'cm-1')
  assert.equal(done.items.find((i) => i.kind === 'tool_result').is_error, true)
  assert.equal(done.toSeq, 12)
  assert.equal(done.safeSeq, 4, 'a running tool can still finish: read again from before it')
  assert.equal(done.lastText, '还在写')

  const live = timelineItems(entries, { maxSeq: 12, running: true })
  assert.equal(live.toSeq, 9, 'the text being written is not final: it goes live, not as an item')
  assert.equal(live.items.at(-1).id, '9#0')
  assert.equal(live.safeSeq, 4)
  const quick = timelineItems([entry(3, { type: 'tool_call', callId: 'c', name: 'shell', status: 'failed', error: 'denied', detail: { type: 'shell', command: 'rm' } })], { maxSeq: 3 })
  assert.deepEqual(quick.items.map((i) => i.id), ['3#0', '3#1'], 'a call and its result in one row')
  assert.equal(quick.items[1].text, 'denied')
})

test('requests as cards: a question, a plan, a permission with what "always" adds', () => {
  const q = interactionOf('s1', { id: 'permission-1', provider: 'claude', name: 'AskUserQuestion', kind: 'question', metadata: { toolUseId: 'toolu_1' },
    input: { questions: [{ question: '用哪个库？', header: '库', multiSelect: false, options: [{ label: 'A', description: '快' }, 'B'] }] } })
  assert.equal(q.kind, 'question')
  assert.equal(q.tool_use_id, 'toolu_1')
  assert.match(q.id, /^paseo-[0-9a-f]{16}$/)
  assert.deepEqual(q.questions, [{ question: '用哪个库？', header: '库', multi_select: false, options: [{ label: 'A', description: '快' }, { label: 'B', description: '' }] }])
  const plan = interactionOf('s1', { id: 'permission-2', provider: 'claude', name: 'ExitPlanMode', kind: 'plan', input: { plan: 'x' }, metadata: { planText: '## 计划' } })
  assert.deepEqual([plan.kind, plan.plan], ['plan', '## 计划'])
  const perm = interactionOf('s1', { id: 'permission-3', provider: 'claude', name: 'Bash', kind: 'tool', input: { command: 'npm test' },
    suggestions: [{ type: 'addRules', rules: [{ toolName: 'Bash', ruleContent: 'npm test' }], behavior: 'allow', destination: 'localSettings' }] })
  assert.deepEqual([perm.kind, perm.tool_name, perm.tool_input.command, perm.summary, perm.always_label], ['permission', 'Bash', 'npm test', 'npm test', '这个项目不再问：Bash(npm test)'])
  const codex = interactionOf('s2', { id: 'p4', provider: 'codex', name: 'exec', kind: 'tool', title: 'Run a command', detail: { type: 'shell', command: 'make' } })
  assert.deepEqual([codex.tool_name, codex.tool_input.command, codex.summary, codex.reason, codex.always_label], ['Bash', 'make', 'make', 'Run a command', undefined],
    'the card says what will run; the generic title is the reason')
  const why = interactionOf('s2', { id: 'p5', provider: 'codex', name: 'exec', kind: 'tool', title: 'Run a command', description: 'build the app', detail: { type: 'shell', command: 'make' } })
  assert.deepEqual([why.summary, why.reason], ['make', 'build the app'])
  const bare = interactionOf('s2', { id: 'p6', provider: 'codex', name: 'mcp_thing', kind: 'tool', title: 'Use the thing', description: 'needs network' })
  assert.deepEqual([bare.summary, bare.reason], ['Use the thing', 'needs network'], 'nothing in the input: the title is the summary')
  assert.notEqual(interactionOf('s1', { id: 'permission-1', kind: 'tool' }).id, interactionOf('s2', { id: 'permission-1', kind: 'tool' }).id)
})

test("the phone's answers as Paseo's responses", () => {
  const question = { id: 'p', kind: 'question', input: { questions: [{ question: '用哪个库？', header: '库', options: [] }] } }
  assert.deepEqual(responseOf(question, { answers: { '用哪个库？': 'A' } }, 'claude'), { response: { behavior: 'allow', updatedInput: { answers: { '用哪个库？': 'A' } } } })
  assert.deepEqual(responseOf(question, { answers: { '用哪个库？': 'A' } }, 'codex'), { response: { behavior: 'allow', updatedInput: { answers: { 库: 'A' } } } }, "others take Paseo's card's key")
  const plan = { id: 'p', kind: 'plan', actions: [{ id: 'reject' }, { id: 'implement' }, { id: 'implement_resume' }] }
  assert.deepEqual(responseOf(plan, { approve: true, mode: 'bypassPermissions' }, 'claude'), { response: { behavior: 'allow', selectedActionId: 'implement_resume' } })
  assert.deepEqual(responseOf(plan, { approve: true, mode: 'acceptEdits' }, 'claude'), { response: { behavior: 'allow', selectedActionId: 'implement' } })
  assert.deepEqual(responseOf({ ...plan, actions: [{ id: 'reject' }, { id: 'implement' }] }, { approve: true, mode: 'bypassPermissions' }, 'claude'),
    { response: { behavior: 'allow', selectedActionId: 'implement' }, setMode: 'bypassPermissions' })
  assert.deepEqual(responseOf(plan, { approve: false, feedback: '先写测试' }, 'claude'),
    { response: { behavior: 'deny', message: '用户在手机上打回了这个计划。修改意见：先写测试', selectedActionId: 'reject' } })
  const tool = { id: 'p', kind: 'tool', suggestions: [{ type: 'addRules', rules: [{ toolName: 'Bash' }], behavior: 'allow', destination: 'session' }, { type: 'setMode', mode: 'x' }] }
  assert.deepEqual(responseOf(tool, { allow: true, always: true }), { response: { behavior: 'allow', updatedPermissions: [tool.suggestions[0]] } })
  assert.deepEqual(responseOf(tool, { allow: true }), { response: { behavior: 'allow' } })
  assert.deepEqual(responseOf(tool, { allow: false, message: '不要' }), { response: { behavior: 'deny', message: '不要' } })
  assert.equal(responseOf(tool, { dismiss: true }), undefined, "handed back: Paseo's app answers it")
})
