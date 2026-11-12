// Pure mapping between Paseo's shapes and Persona's (docs/integrations/paseo.md "Mapping"): an agent → a session row, its timeline →
// items, its providers → launchers, a permission request ↔ an interaction and its answer. No I/O: the bridge
// (bridge.js) hands in what the daemon said; the tests feed recorded shapes.

import { basename } from 'node:path'
import { appToolName, clipInput, interactionId, snippet, summaryOf } from '../../runtime-sdk/index.js'

/** The runtime id: the engine's (proto.EnginePaseo), the space kind, the agent of sessions the apps do not know. */
export const RUNTIME = 'paseo'
/** The label keeping a session's id in Persona (sessions started or taken over from Persona). */
export const SESSION_LABEL = 'persona.session'
/** The label Paseo puts on an agent another agent created (its create_agent tool): the creating agent's id. */
export const PARENT_LABEL = 'paseo.parent-agent-id'
/** Persona's effort levels, weakest first (proto.EffortLevels). */
export const EFFORTS = ['low', 'medium', 'high', 'xhigh', 'max']
/** Persona's permission modes (proto.PermissionModes): Claude Code's, which Paseo's Claude providers take as they are. */
export const MODES = ['default', 'acceptEdits', 'plan', 'auto', 'bypassPermissions']
/**
 * Persona's modes for the providers Paseo words differently: Codex has auto, auto-review and full-access, and only
 * full access means the same as bypassPermissions (no prompts for edits, commands or the network).
 */
export const CODEX_MODES = { bypassPermissions: 'full-access' }

/** Persona's permission mode in Paseo's words for a provider (undefined: Paseo has no such mode there). */
export function paseoMode(root, mode) {
  if (root === 'claude') return MODES.includes(mode) ? mode : undefined
  if (root === 'codex') return CODEX_MODES[mode]
  return undefined
}

/** Paseo's permission mode in Persona's words for a provider (undefined: Persona has no such mode). */
export function personaMode(root, mode) {
  if (root === 'claude') return MODES.includes(mode) ? mode : undefined
  if (root === 'codex') return Object.keys(CODEX_MODES).find((ours) => CODEX_MODES[ours] === mode)
  return undefined
}
/** A launcher name (the node's nameRe: what a shell function may be called). */
export const LAUNCHER_NAME = /^[A-Za-z_][A-Za-z0-9_-]*$/
/** A session id the node takes (runtimeIDRe). */
export const SESSION_ID = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/
/** A Claude Code session id (the node's idRe). */
export const CLAUDE_ID = /^[0-9a-fA-F-]{8,64}$/
/** A Codex thread id (the node's rolloutUUID). */
export const CODEX_THREAD = /^[0-9a-fA-F]{8}(-[0-9a-fA-F]{4}){3}-[0-9a-fA-F]{12}$/

// ---- providers ----

/**
 * The built-in provider under each provider: a custom one extends another (maybe another custom one, a chain).
 * config: get_daemon_config's providers; entries: the providers snapshot's.
 * @returns {Record<string, string>} provider id → built-in id
 */
export function providerRoots(config = {}, entries = []) {
  const ids = new Set([...Object.keys(config ?? {}), ...(entries ?? []).map((e) => e?.provider).filter(Boolean)])
  const roots = {}
  for (const id of ids) {
    let cur = id
    const seen = new Set()
    while (typeof config?.[cur]?.extends === 'string' && config[cur].extends && !seen.has(cur)) {
      seen.add(cur)
      cur = config[cur].extends
    }
    roots[id] = cur
  }
  return roots
}

/** The built-in provider an agent runs on: what its persistence records (Paseo writes it), else its provider's root. */
export function agentRoot(agent, roots = {}) {
  const m = agent?.persistence?.metadata?.provider
  if (typeof m === 'string' && m) return m
  return roots[agent?.provider] ?? agent?.provider ?? ''
}

/** The agent the apps know a root as: Claude Code and Codex by name, the rest as Paseo's. */
export function appAgent(root) {
  return root === 'claude' ? 'claude' : root === 'codex' ? 'codex' : RUNTIME
}

/** A Paseo model as the new-session form's (proto.AgentModel): Persona's efforts among its thinking options. */
export function formModel(m) {
  const ids = new Set((m?.thinkingOptions ?? []).map((t) => t?.id))
  const efforts = EFFORTS.filter((e) => ids.has(e))
  const out = { id: String(m.id), name: String(m.label || m.id) }
  if (m.description) out.description = String(m.description)
  if (m.isDefault) out.default = true
  if (efforts.length) {
    out.efforts = efforts
    if (efforts.includes(m.defaultThinkingOptionId)) out.default_effort = m.defaultThinkingOptionId
  }
  return out
}

/**
 * The new-session form's launchers: one per provider that can start an agent (enabled, not failing, named like a
 * launcher), with the agent it starts and its models.
 */
export function launchersOf(entries = [], roots = {}) {
  const out = []
  for (const e of entries ?? []) {
    if (!e?.enabled || e.status === 'unavailable' || e.status === 'error' || !LAUNCHER_NAME.test(e.provider ?? '')) continue
    const models = (e.models ?? []).filter((m) => m && m.id && m.isSelectable !== false).map(formModel)
    const def = models.find((m) => m.default) ?? models[0]
    const l = { name: e.provider, agent: appAgent(roots[e.provider] ?? e.provider), models }
    const model = def?.name ?? e.label
    if (model) l.model = String(model)
    out.push(l)
  }
  return out
}

/**
 * The provider a new session runs on when the form named none Paseo has (a launcher of the login shell's): Claude
 * Code's own if it is on, else the first one running Claude Code, else the first one there is.
 */
export function defaultProvider(entries = [], roots = {}) {
  const usable = (entries ?? []).filter((e) => e?.enabled && e.status !== 'unavailable' && e.status !== 'error')
  return (usable.find((e) => e.provider === 'claude') ?? usable.find((e) => roots[e.provider] === 'claude') ?? usable[0])?.provider
}

// ---- sessions ----

/**
 * The id a Paseo agent has in Persona, undefined while it has none: the label Persona left on it (it started or took
 * the session over); else, for Claude Code, its Claude session id — the id the same conversation has everywhere in
 * Persona ("Offline", herdr) — once Paseo has one; else paseo-<agent id>.
 */
export function sessionIdOf(agent, roots = {}) {
  const l = agent?.labels?.[SESSION_LABEL]
  if (typeof l === 'string' && SESSION_ID.test(l)) return l
  if (agentRoot(agent, roots) === 'claude') {
    const c = claudeId(agent)
    return c && SESSION_ID.test(c) ? c : undefined
  }
  return typeof agent?.id === 'string' && agent.id ? `${RUNTIME}-${agent.id}` : undefined
}

/** The Claude session an agent of Claude Code runs: the one persisted, else the one its runtime starts with. */
export function claudeId(agent) {
  for (const id of [agent?.persistence?.sessionId, agent?.runtimeInfo?.sessionId]) {
    if (typeof id === 'string' && CLAUDE_ID.test(id)) return id
  }
  return undefined
}

/**
 * The transcript holding an agent's conversation (runtime.sessions' transcript): its persisted Claude session — on
 * disk by then — for an agent of Claude Code; none for any other.
 */
export function transcriptOf(agent, roots = {}) {
  if (agentRoot(agent, roots) !== 'claude') return undefined
  const id = agent?.persistence?.sessionId
  return typeof id === 'string' && CLAUDE_ID.test(id) ? id : undefined
}

/**
 * The Codex thread an agent's conversation is (runtime.sessions' codex_thread): the thread its Codex persists, for an
 * agent running Codex; none for any other. The node finds the thread's rollout on its own as well: named here, it
 * lists the conversation once, as this session, with the thread's own subagents under it (#4495).
 */
export function codexThreadOf(agent, roots = {}) {
  if (agentRoot(agent, roots) !== 'codex') return undefined
  const p = agent?.persistence
  for (const id of [p?.metadata?.threadId, p?.sessionId, agent?.runtimeInfo?.sessionId]) {
    if (typeof id === 'string' && CODEX_THREAD.test(id)) return id
  }
  return undefined
}

/**
 * The agent that created this one, by its Paseo id: a delegated agent (Paseo's create_agent, as an orchestrator's
 * reviewers). Undefined for an agent the user started, and once Paseo detached it from its creator.
 */
export function parentAgentId(agent) {
  const id = agent?.labels?.[PARENT_LABEL]
  return typeof id === 'string' && id.trim() ? id.trim() : undefined
}

/**
 * Whether an agent archived in Paseo stays a row of its own, not running (#4771): one of the user's own that ran Claude
 * Code, whose session id is not its transcript's: one Persona started (paseo-<agent id>) or took over, which keeps
 * its id on the persona.session label. Named to the node as a transcript (unlisted_transcripts) it would come back
 * under the transcript's id, and what Persona keeps under the session's own (a to-do's link, a pin, an archive mark)
 * would be left behind. An agent whose id is its transcript's (one started in Paseo itself) is named as before.
 */
export function archivedUnderOwnId(agent, roots = {}) {
  if (!agent?.archivedAt || parentAgentId(agent)) return false
  const transcript = transcriptOf(agent, roots)
  const id = sessionIdOf(agent, roots)
  return Boolean(transcript && id && id !== transcript)
}

/**
 * A session row's status for an agent: needing you while a request waits, working, "Offline" when closed. Stopped (idle,
 * or a turn that failed): done while Paseo says it waits for you to look at it (requiresAttention, cleared once you
 * open it in Paseo), idle — seen — once it does not (#2831), as herdr's done / idle carry herdr's own seen state.
 * A Paseo without that flag: done once it has run a turn.
 * @param {object} agent
 * @param {{ quiet?: boolean, fleet?: boolean }} [o]
 *   quiet: an agent another agent created, in that agent's workspace (#4494). It never waits to be looked at: Paseo
 *   sets the flag on it like on any agent, but shows it on no workspace and notifies nobody, its creator is told what
 *   it did, and nobody opens it there to clear the flag.
 *   fleet: agents it created have not finished. Closed meanwhile (Paseo let its process go) it is not "Offline":
 *   their reports wake it, and a list of what runs keeps it, with them under it.
 */
export function rowStatus(agent, o = {}) {
  if ((agent?.pendingPermissions?.length ?? 0) > 0) return 'blocked'
  switch (agent?.status) {
    case 'running':
    case 'initializing':
      return 'working'
    case 'closed':
      return o.fleet ? 'idle' : 'offline'
  }
  if (o.quiet) return 'idle'
  if (typeof agent?.requiresAttention === 'boolean') return agent.requiresAttention ? 'done' : 'idle'
  return agent?.status === 'error' || agent?.lastUserMessageAt ? 'done' : 'idle'
}

// ---- spaces ----

/**
 * The Paseo project of a placement (docs/integrations/paseo.md "Which sessions appear"): a repository's checkout and all its worktrees, as
 * in Paseo's sidebar. The placement's projectKey is Paseo's project record id (its buildProjectPlacementForWorkspace),
 * not the project's own key. Undefined without a placement.
 */
export function spaceIdOf(project) {
  const key = project?.projectKey
  return typeof key === 'string' && key ? key : undefined
}

/** Whether a placement is a worktree of a repository checked out elsewhere (Paseo's own worktree, or one git made). */
export function linkedWorktree(project) {
  const c = project?.checkout
  if (!c?.isGit) return false
  const root = c.worktreeRoot ?? c.cwd
  return c.isPaseoOwnedWorktree === true || (typeof c.mainRepoRoot === 'string' && c.mainRepoRoot !== '' && c.mainRepoRoot !== root)
}

/** Whether a placement's project record is one Paseo made for a single worktree: named after the worktree's folder. */
export function strayRecord(project) {
  if (!linkedWorktree(project)) return false
  const c = project.checkout
  return project.projectName === basename(c.worktreeRoot ?? c.cwd ?? '')
}

/** The name of the Paseo workspace an agent is in (its row's slot_name): its title, else the branch, else the folder. */
export function slotNameOf(project, cwd) {
  const title = typeof project?.workspaceName === 'string' ? project.workspaceName.trim() : ''
  const branch = project?.checkout?.currentBranch
  return title || (typeof branch === 'string' && branch) || basename(cwd ?? '')
}

/** The repository a placement belongs to: the main checkout's directory for a worktree, else its own. */
function repoRootOf(project) {
  const c = project?.checkout
  const root = linkedWorktree(project) ? c.mainRepoRoot : (c?.worktreeRoot ?? c?.cwd)
  return typeof root === 'string' && root ? root : undefined
}

/**
 * The project spaces of what the bridge keeps per agent: each Paseo project is one. A record Paseo made for a single
 * worktree (strayRecord: handed its directory alone, #2802) goes into its repository's project when Persona knows one
 * (the main checkout's, or that of the repository's other worktrees), else stays a space of its own.
 * Names: the project's name most placements give, leaving out stray records unless there is nothing else; then the
 * repository's folder.
 * @param {Iterable<{ project?: object }>} placed
 * @param {(path: string) => string} [canon] - a path as compared (the bridge resolves symbolic links)
 * @returns {{ idOf: (project?: object) => string | undefined, names: Map<string, string> }}
 */
export function projectSpaces(placed, canon = (p) => p) {
  const list = [...placed]
  const byRepo = new Map() // repository → space id
  for (const { project } of list) {
    const id = spaceIdOf(project)
    const repo = repoRootOf(project)
    if (id && repo && !strayRecord(project) && !byRepo.has(canon(repo))) byRepo.set(canon(repo), id)
  }
  const idOf = (project) => {
    const id = spaceIdOf(project)
    if (!id || !strayRecord(project)) return id
    return byRepo.get(canon(repoRootOf(project))) ?? id
  }
  const votes = new Map() // space id → Map(name → count)
  const names = new Map()
  for (const { project } of list) {
    const id = idOf(project)
    const name = typeof project?.projectName === 'string' ? project.projectName.trim() : ''
    if (!id || !name) continue
    if (strayRecord(project)) {
      if (!names.has(id)) names.set(id, basename(repoRootOf(project) ?? '') || name)
      continue
    }
    const v = votes.get(id) ?? new Map()
    v.set(name, (v.get(name) ?? 0) + 1)
    votes.set(id, v)
  }
  for (const [id, v] of votes) {
    names.set(id, [...v].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0))[0][0])
  }
  return { idOf, names }
}

/**
 * The runtime.sessions row of an agent (docs/integrations/runtimes.md "What to implement"), undefined for one not listed.
 * @param {object} agent - an AgentSnapshotPayload
 * @param {{ project?: object, roots?: Record<string, string>, labels?: Record<string, string>, lastText?: string,
 *   spaceId?: string, spaceName?: string, parent?: string, quiet?: boolean, fleet?: object[],
 *   canon?: (path: string) => string, archived?: boolean }} [o]
 *   project: its ProjectPlacement; labels: provider id → its label (the name of a provider the apps do not know);
 *   spaceId, spaceName: its project space and that space's name (projectSpaces); parent: the session of the agent that
 *   created it, when that one is listed beside it; quiet: Paseo shows what it did on no workspace (rowStatus); fleet:
 *   the agents it created that have not finished (the bridge knows all three); canon: a path as its physical
 *   directory (the bridge resolves symbolic links); archived: an agent archived in Paseo that is listed all the same
 *   (archivedUnderOwnId): not running, whatever its record says. Without it an archived agent has no row.
 */
export function agentRow(agent, o = {}) {
  const roots = o.roots ?? {}
  const id = sessionIdOf(agent, roots)
  if (!id || (agent?.archivedAt && !o.archived)) return undefined
  const root = agentRoot(agent, roots)
  const cwd = typeof agent.cwd === 'string' ? agent.cwd : ''
  const p = o.project
  const space = spaceIdOf(p) && (o.spaceId ?? spaceIdOf(p))
  const row = {
    session_id: id,
    title: (typeof agent.title === 'string' && agent.title.trim()) || basename(cwd),
    cwd,
    status: o.archived ? 'offline' : rowStatus(agent, { quiet: o.quiet, fleet: (o.fleet?.length ?? 0) > 0 }),
    updated_at: new Date(Date.parse(agent.updatedAt) || Date.now()).toISOString(),
    rev: Date.parse(agent.updatedAt) || 0,
    space_id: space ?? (agent.workspaceId || cwd),
    space_name: (space && o.spaceName) || p?.projectName || basename(cwd),
    agent: appAgent(root),
    launcher: agent.provider,
  }
  // When it was created: a delegated agent's card in its creator's chat counts its running time from it (#5120).
  const created = Date.parse(agent.createdAt)
  if (created) row.started_at = new Date(created).toISOString()
  // Its Paseo workspace (a worktree, or one of the tasks in a folder) names where in the project it is. It is no slot:
  // a slot is one tab, and the apps draw the agents sharing one as that tab's panes (one row for them all, each named
  // by its agent; one that stopped running "replaced" by another there). A workspace's agents are conversations of
  // their own, as in Paseo's own tabs (#4493).
  if (space && typeof agent.workspaceId === 'string' && agent.workspaceId) row.slot_name = slotNameOf(p, cwd)
  // Its repository, for "By repo" (#2840): Paseo keeps its worktrees under its own folder, where the path names none.
  // By its physical directory (#4492), as the node names the working tree: a repository whose sessions write its path
  // two ways (a home behind a symbolic link) is two projects in the apps.
  const repo = p?.checkout?.isGit ? repoRootOf(p) : undefined
  if (repo) row.repo_root = o.canon ? o.canon(repo) : repo
  if (row.agent === RUNTIME && o.labels?.[agent.provider]) row.agent_label = o.labels[agent.provider]
  const model = agent.model ?? agent.runtimeInfo?.model
  if (typeof model === 'string' && model) row.model = model
  const mode = personaMode(root, agent.currentModeId)
  if (mode) row.permission_mode = mode
  const effort = agent.effectiveThinkingOptionId ?? agent.thinkingOptionId
  if (EFFORTS.includes(effort)) row.effort = effort
  const t = transcriptOf(agent, roots)
  if (t) row.transcript = t
  const thread = codexThreadOf(agent, roots)
  if (thread) row.codex_thread = thread
  const last = agent.status === 'error' && agent.lastError ? `出错了：${agent.lastError}` : o.lastText
  if (last) row.last_text = snippet(last)
  // A delegated agent is its creator's subtask, as a Codex subagent is its chat's (#4494): the apps fold it under
  // that session, and the hub pushes no "done" for it and holds its creator's until the fleet has reported back.
  // While it waits for the user the node lists it without its parent (docs/integrations/runtimes.md).
  if (o.parent) {
    row.is_subagent = true
    row.parent_session_id = o.parent
  }
  // The agents it created that have not finished are its background work: its own turn ended, and theirs wake it.
  const fleet = (o.fleet ?? []).map(backgroundOf).filter(Boolean)
  if (fleet.length) row.background = fleet
  return row
}

/** A delegated agent that has not finished, as a background task of the agent that created it (proto.BackgroundTask). */
export function backgroundOf(agent) {
  if (typeof agent?.id !== 'string' || !agent.id) return undefined
  const title = typeof agent.title === 'string' ? agent.title.trim() : ''
  return {
    id: `${RUNTIME}-${agent.id}`, kind: 'agent', ...(title ? { description: snippet(title) } : {}),
    started_at: ts(agent.createdAt ?? agent.updatedAt),
  }
}

// ---- the timeline ----

const ts = (t) => new Date(Date.parse(t) || Date.now()).toISOString()

/** A tool call's detail as the apps draw that tool: Claude Code's name and arguments. */
export function toolCall(name, detail) {
  const d = detail && typeof detail === 'object' ? detail : {}
  switch (d.type) {
    case 'shell':
      return { name: 'Bash', input: dropEmpty({ command: d.command, cwd: d.cwd }) }
    case 'read':
      return { name: 'Read', input: dropEmpty({ file_path: d.filePath, offset: d.offset, limit: d.limit }) }
    case 'edit':
      return { name: 'Edit', input: clipInput(dropEmpty({ file_path: d.filePath, old_string: d.oldString, new_string: d.newString })) }
    case 'write':
      return { name: 'Write', input: clipInput(dropEmpty({ file_path: d.filePath, content: d.content })) }
    case 'search': {
      const tool = { grep: 'Grep', glob: 'Glob', web_search: 'WebSearch' }[d.toolName] ?? 'Grep'
      return { name: tool, input: dropEmpty(tool === 'WebSearch' ? { query: d.query } : { pattern: d.query }) }
    }
    case 'fetch':
      return { name: 'WebFetch', input: dropEmpty({ url: d.url, prompt: d.prompt }) }
    case 'sub_agent':
      return { name: 'Task', input: dropEmpty({ subagent_type: d.subAgentType, description: d.description }) }
    case 'plan':
      return { name: 'ExitPlanMode', input: clipInput({ plan: d.text ?? '' }) }
    case 'worktree_setup':
      return { name: 'WorktreeSetup', input: dropEmpty({ path: d.worktreePath, branch: d.branchName }) }
    case 'plain_text':
      return { name: appToolName(name || d.label || 'tool'), input: clipInput(dropEmpty({ text: d.text, label: d.label })) }
    default: {
      const input = d.input && typeof d.input === 'object' && !Array.isArray(d.input) ? d.input : d.input === undefined || d.input === null ? {} : { input: d.input }
      return { name: appToolName(name || 'tool'), input: clipInput(input) }
    }
  }
}

/** What a finished tool call gave back, as the text of its result. */
export function toolOutput(item) {
  const d = item?.detail ?? {}
  let out
  switch (d.type) {
    case 'shell':
      out = d.output ?? ''
      if (typeof d.exitCode === 'number' && d.exitCode !== 0) out += `${out ? '\n' : ''}（退出码 ${d.exitCode}）`
      break
    case 'read':
    case 'write':
      out = d.type === 'read' ? d.content ?? '' : ''
      break
    case 'edit':
      out = d.unifiedDiff ?? ''
      break
    case 'search':
      out = d.content ?? (Array.isArray(d.filePaths) && d.filePaths.length ? d.filePaths.join('\n') : '')
      if (!out && Array.isArray(d.webResults)) out = d.webResults.map((r) => `${r.title}\n${r.url}`).join('\n\n')
      break
    case 'fetch':
      out = d.result ?? ''
      break
    case 'sub_agent':
      out = d.log || (d.actions ?? []).map((a) => `${a.toolName}${a.summary ? ` ${a.summary}` : ''}`).join('\n')
      break
    case 'worktree_setup':
      out = d.log ?? ''
      break
    case 'plain_text':
      out = d.text ?? ''
      break
    case 'plan':
      out = ''
      break
    default:
      out = d.output === undefined || d.output === null ? '' : typeof d.output === 'string' ? d.output : JSON.stringify(d.output, null, 2)
  }
  if (item?.status === 'failed' && item.error) {
    const e = typeof item.error === 'string' ? item.error : item.error?.message ?? JSON.stringify(item.error)
    out = out ? `${out}\n${e}` : e
  } else if (item?.status === 'canceled' && !out) out = '（已取消）'
  return String(out)
}

/** Whether a finished tool call failed. */
function toolFailed(item) {
  return item?.status === 'failed' || (item?.detail?.type === 'shell' && typeof item.detail.exitCode === 'number' && item.detail.exitCode !== 0)
}

/**
 * The items of one projected timeline entry (fetch_agent_timeline's). Where each lands: a message, a thought or a
 * note at its first row (seqStart); a tool call at its first row, its result — once it finished — at its last
 * (seqEnd). echoes: Paseo message id → the client_msg_id the phone sent it with.
 */
export function entryItems(entry, echoes) {
  const it = entry?.item
  const s = Number(entry?.seqStart)
  if (!it || !Number.isSafeInteger(s) || s < 1) return []
  const e = Number.isSafeInteger(Number(entry.seqEnd)) ? Math.max(s, Number(entry.seqEnd)) : s
  const at = ts(entry.timestamp)
  const out = []
  const add = (seq, x) => out.push({ id: `${seq}#${out.filter((o) => o.seq === seq).length}`, seq, ts: at, ...x })
  switch (it.type) {
    case 'user_message': {
      if (!it.text) break
      const x = { role: 'user', kind: 'text', text: it.text }
      const cm = echoes?.get?.(it.clientMessageId ?? it.messageId)
      if (cm) x.client_msg_id = cm
      add(s, x)
      break
    }
    case 'assistant_message':
      if (it.text) add(s, { role: 'assistant', kind: 'text', text: it.text })
      break
    case 'reasoning':
      if (it.text) add(s, { role: 'assistant', kind: 'thinking', text: it.text })
      break
    case 'tool_call': {
      const call = toolCall(it.name, it.detail)
      const useId = it.callId || `paseo-${s}`
      add(s, { role: 'assistant', kind: 'tool_use', tool_name: call.name, tool_use_id: useId, tool_input: call.input })
      if (it.status && it.status !== 'running') add(e, { role: 'user', kind: 'tool_result', tool_use_id: useId, is_error: toolFailed(it), text: toolOutput(it) })
      break
    }
    case 'todo': {
      const todos = (it.items ?? []).map((t) => dropEmpty({ content: t.text, status: t.status ?? (t.completed ? 'completed' : 'pending'), activeForm: t.activeForm }))
      add(s, { role: 'assistant', kind: 'tool_use', tool_name: 'TodoWrite', tool_use_id: `paseo-todo-${s}`, tool_input: { todos } })
      break
    }
    case 'error':
      add(s, { role: 'system', kind: 'notice', text: `出错了：${snippet(it.message ?? '')}` })
      break
    case 'notification':
      if (it.message) add(s, { role: 'system', kind: 'notice', text: snippet(it.message) })
      break
    case 'compaction':
      if (it.status === 'completed') {
        const compact = dropEmpty({ trigger: it.trigger, pre_tokens: it.preTokens })
        add(s, { role: 'system', kind: 'notice', text: '已压缩上下文', ...(Object.keys(compact).length ? { compact } : {}) })
      }
      break
    // plugin items: a Paseo plugin's own rendering, nothing the apps can show
  }
  return out
}

/**
 * A timeline page as items, and how far they are final. running: the agent is in a turn — its last entry, when it is
 * text or a thought, is still being written: its item waits (toSeq stops before it) and the text goes live instead.
 * safeSeq: from where a later fetch must start to see every entry that can still change (running tools, that text).
 * @param {object[]} entries - projected entries, oldest first
 * @param {{ maxSeq: number, running?: boolean, echoes?: Map<string, string> }} o
 * @returns {{ items: object[], toSeq: number, safeSeq: number, lastText: string }}
 */
export function timelineItems(entries, { maxSeq, running = false, echoes } = {}) {
  const list = (entries ?? []).filter((x) => Number.isSafeInteger(Number(x?.seqStart))).sort((a, b) => a.seqStart - b.seqStart)
  let toSeq = Math.max(0, Number(maxSeq) || 0)
  for (const x of list) toSeq = Math.max(toSeq, Number(x.seqEnd) || 0)
  let safeSeq = toSeq
  const items = []
  let lastText = ''
  list.forEach((x, i) => {
    const type = x.item?.type
    const open = running && i === list.length - 1 && (type === 'assistant_message' || type === 'reasoning')
    if (open) {
      toSeq = Math.min(toSeq, x.seqStart - 1)
      safeSeq = Math.min(safeSeq, x.seqStart - 1)
      return
    }
    if (type === 'tool_call' && x.item.status === 'running') safeSeq = Math.min(safeSeq, x.seqStart - 1)
    if (type === 'assistant_message' && x.item.text) lastText = x.item.text
    items.push(...entryItems(x, echoes))
  })
  items.sort((a, b) => a.seq - b.seq || a.id.localeCompare(b.id))
  return { items: items.filter((it) => it.seq <= toSeq), toSeq, safeSeq: Math.max(0, safeSeq), lastText }
}

// ---- requests for the user ----

/** The questions of a request, in the apps' shape; undefined when it asks none. */
function questionsOf(input) {
  const qs = Array.isArray(input?.questions) ? input.questions : undefined
  if (!qs?.length) return undefined
  const out = []
  for (const q of qs) {
    if (!q || typeof q.question !== 'string') return undefined
    out.push({
      question: q.question,
      header: typeof q.header === 'string' ? q.header : '',
      multi_select: q.multiSelect === true,
      options: (Array.isArray(q.options) ? q.options : []).map((o) => typeof o === 'string' ? { label: o, description: '' } : { label: String(o?.label ?? ''), description: String(o?.description ?? '') }).filter((o) => o.label),
    })
  }
  return out
}

/** The rule an "always allow" adds, in words (`Bash(npm test)`), and where it applies. */
function alwaysLabel(suggestions) {
  const s = (suggestions ?? []).find((x) => x && x.type === 'addRules' && x.behavior === 'allow' && Array.isArray(x.rules) && x.rules.length)
  if (!s) return undefined
  const rules = s.rules.map((r) => (r?.ruleContent ? `${r.toolName}(${r.ruleContent})` : r?.toolName)).filter(Boolean).join('、')
  if (!rules) return undefined
  const where = s.destination === 'session' ? '本会话' : s.destination === 'userSettings' ? '所有项目' : '这个项目'
  return snippet(`${where}不再问：${rules}`)
}

/**
 * The interaction a pending request becomes on the phone (docs/protocol/protocol.md Interaction): a question, a plan, or a
 * permission. The id is stable (interactionId), so a restart hangs the same request on the card already shown.
 */
export function interactionOf(sessionId, request) {
  const r = request ?? {}
  const input = r.input && typeof r.input === 'object' ? r.input : {}
  const base = { session_id: sessionId }
  const useId = r.metadata?.toolUseId
  if (typeof useId === 'string' && useId) base.tool_use_id = useId
  const questions = r.kind === 'question' ? questionsOf(input) : undefined
  if (questions) {
    return { ...base, id: interactionId(RUNTIME, sessionId, 'question', r.id), kind: 'question', tool_name: 'AskUserQuestion', questions }
  }
  if (r.kind === 'plan') {
    const plan = typeof r.metadata?.planText === 'string' ? r.metadata.planText : typeof input.plan === 'string' ? input.plan : r.description ?? ''
    return { ...base, id: interactionId(RUNTIME, sessionId, 'plan', r.id), kind: 'plan', tool_name: 'ExitPlanMode', plan }
  }
  const claude = r.provider === 'claude' && typeof r.name === 'string' && r.name
  const call = claude ? { name: r.name, input: clipInput(input) } : toolCall(r.name, r.detail ?? { type: 'unknown', input })
  const ix = {
    ...base, id: interactionId(RUNTIME, sessionId, 'permission', r.id), kind: 'permission',
    tool_name: call.name, tool_input: call.input,
  }
  // The line the owner decides on is what will run (the command, the file, the URL); Paseo's title ("Run a command")
  // and description only say why, so they go under reason, and stand in as the summary when the input names nothing.
  const what = summaryOf(call.name, call.input)
  const said = [r.title, r.description].filter((x) => typeof x === 'string' && x.trim())
  if (what && what !== call.name) {
    ix.summary = what
    if (said.length) ix.reason = snippet(said.at(-1))
  } else {
    ix.summary = snippet(said[0] || call.name || '')
    if (said.length > 1) ix.reason = snippet(said[1])
  }
  const always = alwaysLabel(r.suggestions)
  if (always) ix.always_label = always
  return ix
}

/**
 * The answer to send for a request (agent_permission_response's response), undefined when the phone handed it back
 * (Paseo's own app answers it). mode: for an approved plan, the mode the phone chose; the response picks the plan's
 * action, and setMode is what is left to set after it (Paseo's implement switches to acceptEdits).
 * @returns {{ response: object, setMode?: string } | undefined}
 */
export function responseOf(request, res, root) {
  if (!res || res.dismiss) return undefined
  const r = request ?? {}
  if (r.kind === 'question' && res.answers && typeof res.answers === 'object') {
    const qs = Array.isArray(r.input?.questions) ? r.input.questions : []
    const answers = {}
    for (const q of qs) {
      const a = res.answers[q?.question] ?? (q?.header ? res.answers[q.header] : undefined)
      if (typeof a !== 'string') continue
      // Claude Code's AskUserQuestion takes the question text (Paseo maps headers to it); the others, Paseo's card's key.
      answers[root === 'claude' || !q.header ? q.question : q.header] = a
    }
    if (!Object.keys(answers).length) return undefined
    return { response: { behavior: 'allow', updatedInput: { answers } } }
  }
  if (r.kind === 'plan' && typeof res.approve === 'boolean') {
    const ids = new Set((r.actions ?? []).map((a) => a?.id))
    if (!res.approve) {
      const msg = res.feedback?.trim() ? `用户在手机上打回了这个计划。修改意见：${res.feedback.trim()}` : '用户在手机上打回了这个计划。'
      return { response: { behavior: 'deny', message: msg, ...(ids.has('reject') ? { selectedActionId: 'reject' } : {}) } }
    }
    const mode = MODES.includes(res.mode) ? res.mode : 'bypassPermissions'
    if (ids.has('implement_resume') && (mode === 'bypassPermissions' || mode === 'auto')) {
      return { response: { behavior: 'allow', selectedActionId: 'implement_resume' } }
    }
    if (ids.has('implement')) return { response: { behavior: 'allow', selectedActionId: 'implement' }, ...(mode !== 'acceptEdits' ? { setMode: mode } : {}) }
    return { response: { behavior: 'allow' }, ...(root === 'claude' ? { setMode: mode } : {}) }
  }
  if (typeof res.allow === 'boolean') {
    if (!res.allow) return { response: { behavior: 'deny', message: res.message?.trim() || '用户在手机上拒绝了' } }
    const response = { behavior: 'allow' }
    if (res.always) {
      const rules = (r.suggestions ?? []).filter((x) => x && ['addRules', 'replaceRules', 'removeRules'].includes(x.type) && Array.isArray(x.rules))
      if (rules.length) response.updatedPermissions = rules
    }
    return { response }
  }
  return undefined
}

function dropEmpty(o) {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== null && v !== ''))
}
