// A fake Paseo daemon for the bridge's tests: its WebSocket endpoint (/ws) with the requests the bridge makes, in the
// shapes Paseo 0.14 / 0.15 use (packages/protocol messages.ts), and helpers to make things happen in it.

import { createHash, randomUUID } from 'node:crypto'
import { createServer } from 'node:http'
import { WsConnection } from '../../runtime-sdk/lib/listen.js'

const GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

/** An agent snapshot with what the bridge reads; fields override. */
export function agentSnapshot(fields = {}) {
  const now = new Date().toISOString()
  return {
    id: randomUUID(), provider: 'claude', cwd: '/tmp', workspaceId: 'wks_1', model: null, thinkingOptionId: null,
    effectiveThinkingOptionId: null, createdAt: now, updatedAt: now, lastUserMessageAt: null, status: 'idle', activeTurn: null,
    capabilities: {}, currentModeId: null, availableModes: [], pendingPermissions: [], persistence: null, runtimeInfo: null,
    title: null, labels: {}, archivedAt: null, ...fields,
  }
}

/**
 * What Paseo 0.13.2's AgentSessionConfigSchema (messages.ts McpServerConfigSchema) takes in config.mcpServers: a record of
 * servers, each stdio (command, args, env), http or sse, discriminated by `type` — which stdio servers must carry.
 * Returns what is wrong, or null.
 */
export function mcpServersError(servers) {
  if (servers === undefined) return null
  const isStrings = (a) => Array.isArray(a) && a.every((x) => typeof x === 'string')
  const isRecord = (o) => o && typeof o === 'object' && !Array.isArray(o) && Object.values(o).every((x) => typeof x === 'string')
  if (!servers || typeof servers !== 'object' || Array.isArray(servers)) return 'mcpServers: not a record'
  for (const [name, s] of Object.entries(servers)) {
    const at = `mcpServers.${name}`
    if (s?.type === 'stdio') {
      if (typeof s.command !== 'string') return `${at}.command: not a string`
      if (s.args !== undefined && !isStrings(s.args)) return `${at}.args: not a list of strings`
      if (s.env !== undefined && !isRecord(s.env)) return `${at}.env: not a record of strings`
    } else if (s?.type === 'http' || s?.type === 'sse') {
      if (typeof s.url !== 'string') return `${at}.url: not a string`
    } else {
      return `${at}.type: expected stdio, http or sse`
    }
  }
  return null
}

/**
 * @param {import('node:test').TestContext} t
 * @param {{ version?: string, providers?: object, entries?: object[], agents?: {agent: object, project?: object}[] }} [o]
 */
export async function startFakePaseo(t, o = {}) {
  const f = {
    version: o.version ?? '0.14.0',
    providers: o.providers ?? {},
    entries: o.entries ?? [],
    agents: new Map((o.agents ?? []).map((e) => [e.agent.id, { agent: e.agent, project: e.project ?? null }])),
    timelines: new Map(), // agent id → { epoch, entries }
    conns: [],
    requests: [], // every session request, in order
    hellos: [],
    subs: new Map(), // subscriptionId → { conn, kind, events?, agentIds? }
    handlers: {}, // a test's own answers: type → (msg) => payload | undefined
    refuse: undefined, // close code for the next hellos
    refuseDelay: 0, // ms before an MCP refusal is answered (a creation slow to fail)
    noMcp: new Set(o.noMcpProviders ?? []), // providers that take no MCP servers (Paseo's requireExternalMcpSupport)
  }
  let n = 0
  const http = createServer()
  http.on('upgrade', (req, socket, head) => {
    const accept = createHash('sha1').update(req.headers['sec-websocket-key'] + GUID).digest('base64')
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`)
    const c = new WsConnection(socket, head)
    c.on('error', () => {})
    f.conns.push(c)
    c.on('data', (line) => onMessage(c, line))
    c.on('close', () => {
      for (const [id, s] of f.subs) if (s.conn === c) f.subs.delete(id)
    })
  })
  await new Promise((r) => http.listen(0, '127.0.0.1', r))
  f.port = http.address().port
  t.after(() => {
    for (const c of f.conns) c.socket.destroy()
    http.close()
  })

  const send = (c, inner) => c.write(JSON.stringify({ type: 'session', message: inner }))
  const reply = (c, type, payload) => send(c, { type, payload })

  function onMessage(c, line) {
    const m = JSON.parse(line)
    if (m.type === 'hello') {
      f.hellos.push(m)
      if (f.refuse) {
        c.fail(f.refuse, 'refused')
        return
      }
      send(c, { type: 'status', payload: { status: 'server_info', protocolVersion: 1, serverId: 'srv_test', hostname: 'test', version: f.version, features: {} } })
      return
    }
    if (m.type === 'ping') {
      c.write('{"type":"pong"}')
      return
    }
    if (m.type !== 'session') return
    const msg = m.message
    f.requests.push(msg)
    const own = f.handlers[msg.type]?.(msg, c)
    if (own === null) return // the test answers later (or never)
    if (own !== undefined) return reply(c, own.type ?? replyType(msg.type), { requestId: msg.requestId, ...own.payload })
    answer(c, msg)
  }

  function answer(c, msg) {
    const id = msg.requestId
    const sub = (kind, extra) => {
      const sid = `sub_${++n}`
      f.subs.set(sid, { conn: c, kind, ...extra })
      return sid
    }
    switch (msg.type) {
      case 'get_daemon_config_request':
        return reply(c, 'get_daemon_config_response', { requestId: id, config: { providers: f.providers, mcp: { injectIntoAgents: true } } })
      case 'get_providers_snapshot_request':
        return reply(c, 'get_providers_snapshot_response', { requestId: id, entries: f.entries, generatedAt: new Date().toISOString() })
      case 'session.events.set_subscription.request':
        return reply(c, 'session.events.set_subscription.response', { requestId: id, subscriptionId: sub('events', { events: msg.events }) })
      case 'fetch_agents_request': {
        // Archived agents only when asked for, and then among the others; in the order asked for, a page at a time.
        const list = [...f.agents.values()].filter((e) => msg.filter?.includeArchived || !e.agent.archivedAt)
        const by = msg.sort?.[0]
        if (by?.key === 'updated_at') {
          const at = (e) => Date.parse(e.agent.updatedAt) || 0
          list.sort((x, y) => (by.direction === 'asc' ? at(x) - at(y) : at(y) - at(x)))
        }
        const from = Number(msg.page?.cursor) || 0
        const limit = msg.page?.limit ?? list.length
        const more = from + limit < list.length
        return reply(c, 'fetch_agents_response', {
          requestId: id, subscriptionId: msg.subscribe ? sub('agents') : null, entries: list.slice(from, from + limit),
          pageInfo: { nextCursor: more ? String(from + limit) : null, prevCursor: null, hasMore: more },
        })
      }
      case 'fetch_agent_request': {
        // An archived agent's record is still there; a deleted one is a null agent, not an error.
        const e = f.agents.get(msg.agentId)
        return reply(c, 'fetch_agent_response', { requestId: id, agent: e?.agent ?? null, project: e?.project ?? null, error: null })
      }
      case 'agent.timeline.set_subscription.request':
        return reply(c, 'agent.timeline.set_subscription.response', { requestId: id, agentIds: msg.agentIds, subscriptionId: sub('timeline', { agentIds: msg.agentIds }) })
      case 'subscription.release.request':
        f.subs.delete(msg.subscriptionId)
        return reply(c, 'subscription.release.response', { requestId: id, subscriptionId: msg.subscriptionId })
      case 'fetch_agent_timeline_request': {
        const a = f.agents.get(msg.agentId)
        const tl = f.timelines.get(msg.agentId) ?? { epoch: 'ep1', entries: [] }
        let list = tl.entries
        if (msg.direction === 'after' && msg.cursor) list = list.filter((e) => e.seqStart > msg.cursor.seq)
        if (msg.direction === 'tail' && msg.limit > 0) list = list.slice(-msg.limit)
        const maxSeq = Math.max(0, ...tl.entries.map((e) => e.seqEnd))
        const stale = msg.cursor && msg.cursor.epoch !== tl.epoch
        return reply(c, 'fetch_agent_timeline_response', {
          requestId: id, agentId: msg.agentId, agent: a?.agent ?? null, direction: msg.direction ?? 'tail', projection: 'projected',
          epoch: tl.epoch, reset: Boolean(stale), staleCursor: Boolean(stale), gap: false,
          window: { minSeq: 1, maxSeq, nextSeq: maxSeq + 1 }, entries: stale ? tl.entries : list, error: a ? null : 'Agent not found',
        })
      }
      case 'send_agent_message_request': {
        // A message to an archived agent takes it back from the archive first, its labels as they were (Paseo 0.14
        // sendPromptToAgent: unarchive, load, run).
        const e = f.agents.get(msg.agentId)
        if (e?.agent.archivedAt) f.upsert({ ...e.agent, archivedAt: null, status: 'running', updatedAt: new Date().toISOString() })
        return reply(c, 'send_agent_message_response', { requestId: id, agentId: msg.agentId, accepted: true, error: null })
      }
      case 'cancel_agent_request':
        return reply(c, 'cancel_agent_response', { requestId: id, agentId: msg.agentId, agent: f.agents.get(msg.agentId)?.agent ?? null, error: null })
      case 'update_agent_request': {
        // An archived agent's record is renamed where it is stored, and no list is told (it lists the agents Paseo has).
        const e = f.agents.get(msg.agentId)
        if (e?.agent.archivedAt && msg.name) e.agent = { ...e.agent, title: msg.name, updatedAt: new Date().toISOString() }
        return reply(c, replyType(msg.type), { requestId: id, agentId: msg.agentId, accepted: true, error: null })
      }
      case 'set_agent_mode_request':
      case 'set_agent_thinking_request': {
        // Paseo loads the agent first, and refuses an archived one (ensureUnarchivedAgentLoaded).
        if (f.agents.get(msg.agentId)?.agent.archivedAt) {
          return reply(c, replyType(msg.type), { requestId: id, agentId: msg.agentId, accepted: false, error: `Agent is archived: ${msg.agentId}` })
        }
        return reply(c, replyType(msg.type), { requestId: id, agentId: msg.agentId, accepted: true, error: null })
      }
      case 'archive_agent_request': {
        const at = new Date().toISOString()
        reply(c, 'agent_archived', { requestId: id, agentId: msg.agentId, archivedAt: at })
        f.archive(msg.agentId, at)
        return
      }
      case 'agent.create.request': {
        const bad = mcpServersError(msg.config.mcpServers)
        if (bad) return reply(c, 'agent.create.response', { requestId: id, agent: null, error: `Invalid agent config: ${bad}` })
        if (Object.keys(msg.config.mcpServers ?? {}).length && f.noMcp.has(msg.config.provider)) {
          const refuse = () => reply(c, 'agent.create.response', { requestId: id, agent: null, error: `Provider '${msg.config.provider}' does not support MCP servers` })
          if (f.refuseDelay) setTimeout(refuse, f.refuseDelay).unref()
          else refuse()
          return
        }
        const agent = agentSnapshot({ id: msg.agentId ?? randomUUID(), provider: msg.config.provider, cwd: msg.config.cwd, labels: msg.labels ?? {},
          model: msg.config.model ?? null, currentModeId: msg.config.modeId ?? null, thinkingOptionId: msg.config.thinkingOptionId ?? null,
          status: msg.initialPrompt ? 'running' : 'idle', workspaceId: msg.workspaceId ?? 'wks_new' })
        f.upsert(agent, { projectKey: 'prj_1', projectName: 'proj', workspaceName: 'ws', checkout: { cwd: agent.cwd, isGit: false } })
        return reply(c, 'agent.create.response', { requestId: id, agent, error: null })
      }
      case 'import_agent_request': {
        // Paseo 0.14 (import-sessions.ts): a session an agent has is not imported twice; one an archived agent has is
        // that agent, back from the archive with the request's labels over its own; any other is a new agent.
        const records = [...f.agents.values()].filter((e) => e.agent.persistence?.provider === msg.provider && e.agent.persistence?.sessionId === msg.sessionId)
        if (records.some((e) => !e.agent.archivedAt)) {
          return send(c, { type: 'status', payload: { status: 'agent_create_failed', requestId: id, error: `Provider session is already imported: ${msg.sessionId}` } })
        }
        const old = records.find((e) => e.agent.archivedAt)
        const agent = old
          ? { ...old.agent, archivedAt: null, status: 'idle', updatedAt: new Date().toISOString(), labels: { ...old.agent.labels, ...msg.labels },
            workspaceId: msg.workspaceId ?? old.agent.workspaceId }
          : agentSnapshot({ provider: msg.provider, cwd: msg.cwd, labels: msg.labels ?? {},
            persistence: { provider: msg.provider, sessionId: msg.sessionId, metadata: { provider: 'claude' } },
            runtimeInfo: { provider: msg.provider, sessionId: msg.sessionId } })
        f.upsert(agent)
        return send(c, { type: 'status', payload: { status: 'agent_resumed', requestId: id, agentId: agent.id, agent } })
      }
      case 'agent_permission_response': {
        const e = f.agents.get(msg.agentId)
        if (!e?.agent.pendingPermissions.some((r) => r.id === msg.requestId)) {
          return reply(c, 'rpc_error', { requestId: msg.requestId, requestType: msg.type, error: 'No pending permission request', code: 'handler_error' })
        }
        f.resolvePermission(msg.agentId, msg.requestId, msg.response, c)
        return
      }
      default:
        return reply(c, 'rpc_error', { requestId: id, requestType: msg.type, error: 'Unknown request', code: 'unknown_schema' })
    }
  }

  /** Send a message to every subscription of a kind (only those whose filter takes it). */
  f.emit = (kind, inner, filter = () => true) => {
    for (const [sid, s] of f.subs) {
      if (s.kind !== kind || !filter(s)) continue
      s.conn.write(JSON.stringify({ type: 'session', message: { ...inner, payload: { ...inner.payload, subscriptionId: sid } } }))
    }
  }
  /** An agent appears or changes (the directory's update). */
  f.upsert = (agent, project) => {
    const prev = f.agents.get(agent.id)
    f.agents.set(agent.id, { agent, project: project ?? prev?.project ?? null })
    f.emit('agents', { type: 'agent_update', payload: { kind: 'upsert', agent, project: project ?? prev?.project ?? null } })
  }
  /**
   * An agent is archived: its record stays, and it leaves the list (told twice, as Paseo does for an agent that ran:
   * when it closes it, and when it stores its record). The agents it created go with it (Paseo's
   * cascadeArchiveChildren): those in its workspace are archived too, one in another workspace is let go of.
   */
  f.archive = (agentId, at = new Date().toISOString()) => {
    const e = f.agents.get(agentId)
    if (!e || e.agent.archivedAt) return
    e.agent = { ...e.agent, archivedAt: at, updatedAt: at }
    for (let i = 0; i < 2; i++) f.emit('agents', { type: 'agent_update', payload: { kind: 'remove', agentId } })
    for (const k of [...f.agents.values()]) {
      if (k.agent.archivedAt || k.agent.labels?.['paseo.parent-agent-id'] !== agentId) continue
      if (k.agent.workspaceId === e.agent.workspaceId) f.archive(k.agent.id, at)
      else {
        const { 'paseo.parent-agent-id': _, ...labels } = k.agent.labels
        f.upsert({ ...k.agent, labels })
      }
    }
  }
  /**
   * An agent is deleted in another client (Paseo's own app, its command line): nothing of it is left. Paseo tells
   * the removal only to the lists of the client that deleted it (session.ts handleDeleteAgentRequest): every other
   * one sees the agent closed when it ran (its last state), and nothing when it did not. told: the lists are told
   * of the removal, as that client's are. after: ms from closing the agent to its record being gone.
   */
  f.delete = (agentId, { told = false, after = 0 } = {}) => {
    const e = f.agents.get(agentId)
    if (!e) return
    if (e.agent.status !== 'closed' && !e.agent.archivedAt) f.upsert({ ...e.agent, status: 'closed' })
    const gone = () => {
      f.agents.delete(agentId)
      if (told) f.emit('agents', { type: 'agent_update', payload: { kind: 'remove', agentId } })
    }
    if (after) setTimeout(gone, after).unref()
    else gone()
  }
  f.update = (agentId, fields) => {
    const e = f.agents.get(agentId)
    f.upsert({ ...e.agent, updatedAt: new Date(Date.now() + 1).toISOString(), ...fields })
  }
  /** A request for the user comes up in an agent. */
  f.permission = (agentId, request) => {
    const e = f.agents.get(agentId)
    f.update(agentId, { pendingPermissions: [...e.agent.pendingPermissions, request] })
    f.emit('events', { type: 'agent_permission_request', payload: { agentId, request } }, (s) => s.events.includes('agent_permission_request'))
  }
  /** It is answered (by the bridge's response, or in Paseo's own app when c is undefined). */
  f.resolvePermission = (agentId, requestId, resolution, c) => {
    const e = f.agents.get(agentId)
    f.update(agentId, { pendingPermissions: e.agent.pendingPermissions.filter((r) => r.id !== requestId) })
    if (c) c.write(JSON.stringify({ type: 'session', message: { type: 'agent_permission_resolved', payload: { agentId, requestId, resolution } } }))
    f.emit('events', { type: 'agent_permission_resolved', payload: { agentId, requestId, resolution } }, (s) => s.events.includes('agent_permission_resolved'))
  }
  /** An agent's timeline (projected entries) and its epoch. */
  f.setTimeline = (agentId, entries, epoch = 'ep1') => f.timelines.set(agentId, { epoch, entries })
  /** A live event of an agent, to the timeline subscriptions following it. */
  f.stream = (agentId, event, seq) => {
    f.emit('timeline', { type: 'agent_stream', payload: { agentId, event, timestamp: new Date().toISOString(), ...(seq ? { seq, epoch: f.timelines.get(agentId)?.epoch } : {}) } },
      (s) => s.agentIds.includes(agentId))
  }
  f.requestsOf = (type) => f.requests.filter((r) => r.type === type)
  /** Close every connection (the daemon restarting). */
  f.drop = () => {
    for (const c of f.conns) c.socket.destroy()
    f.conns = []
  }
  return f
}

function replyType(type) {
  if (type.endsWith('.request')) return `${type.slice(0, -'.request'.length)}.response`
  if (type.endsWith('_request')) return `${type.slice(0, -'_request'.length)}_response`
  return `${type}_response`
}

/** A projected timeline entry. */
export function entry(seqStart, item, seqEnd = seqStart, turnId = 'turn-1') {
  return { provider: 'claude', item, timestamp: new Date(1_800_000_000_000 + seqStart * 1000).toISOString(), seqStart, seqEnd,
    sourceSeqRanges: [{ startSeq: seqStart, endSeq: seqEnd }], turnId, collapsed: [] }
}
