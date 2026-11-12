// The runtime side of Persona's runtime protocol (docs/integrations/runtimes.md, wire in docs/protocol/protocol.md "Runtime ↔ node"), shared
// by every runtime: connecting to the node's runtime.sock and reconnecting, runtime.hello, pushing the session list,
// watching sessions with continuous seq batches, throttled live text, asking the phone (surviving a node restart) and
// the "who answers first" policy. A runtime supplies a driver with what only it knows: its sessions, their pages, and
// how to send to them; it calls back append / stream / ask / sessionsChanged as things happen.

import { defaultListenPathMatches, wirePackageName } from './compat.js'
import { randomUUID } from 'node:crypto'
import { existsSync, statSync } from 'node:fs'
import { createServer } from 'node:http'
import { createConnection } from 'node:net'
import { homedir } from 'node:os'
import { join } from 'node:path'
import { envValue, socketDir } from './identity.js'
import { upgrade } from './listen.js'
import { Peer, RpcError } from './wire.js'

/** Version of the protocol this SDK speaks (runtime.hello answers the node's). */
export const PROTOCOL = 1

const PHONE_DRIVEN_FOR = 6 * 60 * 60 * 1000 // a session the phone sent to or started recently: it answers first
const RESUME_WAIT = 2 * 60 * 1000 // how long a pending ask waits for the next node (personad node.ResumeWait)
const APPEND_WINDOW = 80 // ms: committed content within this window goes out as one batch
const STREAM_EVERY = 120 // ms: live text at most this often (the whole text so far each time)
const RETRY_PRESENT = 1000 // ms: retry this often while the socket file is there (a node is, or is restarting)
const TOUCHED = 50 // the sessions the phone sent to or started last, listed whatever a runtime's limits say
const RETRY_MAX = 30_000 // ms: the longest wait while there is no socket file (no node on this machine yet)
const NEW_NODE_EVERY = 5000 // ms: after an older node, look this often for a new one (the socket file replaced)
const NEW_NODE_MAX = 10 * 60_000 // ms: …and connect again after this long anyway
const CONFLICT_RETRY = 30_000 // ms: another process holds this runtime id on the node: try again this often

/** Where a listening runtime takes the node's connection when it serves the endpoint itself (options.listen.path). */
export const DEFAULT_LISTEN_PATH = '/persona/runtime'

/** The node's runtime socket for the user this process runs as. */
export function defaultSocketPath() {
  return envValue('PERSONA_RUNTIME_SOCKET') || join(socketDir(), 'runtime.sock')
}

/**
 * @typedef {object} Driver What a runtime implements. Only listSessions and items are required; each optional
 *   method that is present turns on the matching capability (caps in runtime.hello).
 * @property {() => Promise<object[]>} listSessions - its sessions as Session rows (session_id, title, cwd, status,
 *   updated_at, rev, last_text, space_*…; docs/protocol/protocol.md "Data types").
 * @property {() => string[] | undefined | Promise<string[] | undefined>} [unlistedTranscripts] - the Claude transcripts
 *   on this computer that are sessions of its own though listSessions does not list them (archived in the runtime), by
 *   Claude session id; undefined while it cannot say yet (its archive not read): the node then keeps the ones it was
 *   told last. Sent with every list once named, an empty array too (runtime.sessions' unlisted_transcripts): the node
 *   keeps listing them as sessions not running. Only a runtime that runs Claude Code's command line itself needs it:
 *   its transcripts read like a script's one-shot `claude -p` runs, which the node leaves out.
 * @property {(params: object) => Promise<object>} items - one session.items page (before_seq / turns / limit /
 *   after_seq / from_seq + through_seq → items, has_more, history, to_seq, from_seq, more_after, more).
 * @property {(sessionId: string) => Promise<number | {to_seq: number, history?: string}>} [cursor] - the session's
 *   current to_seq (where a watch from now starts), with its history so that a rewrite of as many entries is told
 *   apart; default: the to_seq and history of a one-turn page.
 * @property {(params: object) => Promise<object>} [image] - session.image → {mime, data}.
 * @property {(params: object) => Promise<object>} [item] - session.item → the whole item.
 * @property {(params: {session_id: string, text: string, images?: object[], client_msg_id?: string}) => Promise<void>} [send]
 * @property {(params: {session_id: string}) => Promise<void>} [interrupt]
 * @property {(params: {cwd: string, prompt?: string}) => Promise<{session: object}>} [start]
 * @property {(params: {session_id: string, title: string}) => Promise<void>} [rename]
 * @property {(params: {session_id: string}) => Promise<void>} [close]
 * @property {(params: {session_id: string}) => Promise<void>} [archive] - the runtime's own reversible archive of an
 *   idle session, without stopping or closing anything (caps.archive: only with unarchive too). The session the user
 *   archives in Persona is archived here as well; already archived answers as done.
 * @property {(params: {session_id: string}) => Promise<void>} [unarchive] - restores what archive archived (already
 *   restored answers as done).
 * @property {(params: {launcher: string, cwd: string, prompt: string}) => Promise<object>} [check] -
 *   launcher availability in the runtime's authentication context (caps.check), returning the launcher-check shape.
 * @property {(params: {launcher?: string, refresh?: boolean}) => Promise<{agent: string, models: object[]}>} [models] -
 *   live native model discovery (caps.models), without inference. Rejections reach the picker.
 * @property {(params: {session_id: string, permission_mode?: string, effort?: string, model?: string}) => Promise<void>} [modes] - the
 *   phone's permission mode / effort for a session (caps.modes; model requires options.modelSwitch); values are Persona's: default / acceptEdits / plan /
 *   auto / bypassPermissions, low … max.
 * @property {() => boolean} [hasLocalClient] - the runtime's own UI is open (the policy gives it first turn).
 * @property {(sessionId: string, watching: boolean, o?: { transcript: boolean }) => void} [watchChanged] - the phone
 *   started (or stopped) watching a session: a runtime that follows a session only while it is watched (Paseo's
 *   timeline subscription) starts and ends that here. transcript: the node reads its items (only live text from here).
 */

/**
 * @param {Driver} driver
 * @param {{ runtime: string, name?: string, agent?: string, version?: string, launcher?: string, launcherModel?: string,
 *   socketPath?: string, answerFirst?: 'auto'|'phone'|'local', log?: (msg: string) => void,
 *   connect?: (path: string) => import('node:net').Socket, pushDelayMs?: number, retryMs?: number,
 *   refreshMs?: number, requires?: string[], onHub?: (connected: boolean) => void, assistant?: boolean,
 *   listen?: { port?: number, host?: string, path?: string, token: string | (() => string) } | boolean,
 *   token?: string | (() => string), package?: { name: string, hash: string }, instance?: string,
 *   onUpdate?: (offer: { name: string, hash: string, version?: string }) => void,
 *   launchers?: { name: string, model?: string, agent?: string, models?: object[] }[], resume?: boolean,
 *   keepAlways?: boolean, readonly?: boolean, modelSwitch?: boolean }} options -
 *   launchers: more entries for the new-session form than options.launcher (a runtime running several agents: one
 *   per provider, each with the agent it starts and its models); rt.launchersChanged replaces them. resume: start
 *   takes {resume: <Claude session id>} — taking a session it does not have over, under that id (caps.resume; the
 *   machine's engine continuing a session not running anywhere). keepAlways: the runtime keeps no "always allow" rule
 *   itself; a permission card with an always_label then gets the hub's "Always allow in this session" for that session and tool
 *   (caps.keep_always, #3974). A runtime whose always is a rule of its own (narrower than the tool) leaves it off.
 *   readonly: start with permission_mode "plan" runs in a read-only mode from the first prompt or throws
 *   RpcError(…, 'readonly_unsupported'), never falling back to a writable agent (caps.readonly; without it the node
 *   refuses such starts itself).
 *   assistant: its launcher may run the Persona assistant (caps.assistant; docs/integrations/runtimes.md "Assistant"). requires: node
 *   features (runtime.hello's features) this runtime cannot do without; on a node lacking one it does not stay
 *   connected, and tries again once the node restarts (its socket file is replaced) or after 10 minutes. onHub: called
 *   after every hello and whenever the node's hub comes or goes (a runtime fetching the hub's tools does it then).
 *   listen: be a daemon (docs/integrations/daemons.md) — the node dials this runtime's WebSocket endpoint instead of the runtime
 *   dialing runtime.sock. With a port ({port, host, path, token}; port 0 picks one) start() serves the endpoint
 *   itself (host 127.0.0.1 and path /persona/runtime by default); without one ({token}, or `true` with
 *   options.token) the runtime's own HTTP server hands upgrades to rt.accept(). The token is what the node's
 *   daemons.json gives for this daemon (a function: read at every connection).
 *   package / onUpdate: the code this runtime runs, for a node that keeps it up to date (docs/integrations/runtimes.md "Auto-update";
 *   the loader, loader.js, passes both): hello says the package, and when the node carries another one its reply
 *   offers it — onUpdate gets the offer, rt.fetchPackage gets the files. instance: this process's identity on the node
 *   (runtime.hello's instance; default a new one per createRuntime). A runtime that replaces itself in the same process
 *   (a new version) passes the same one, so the node takes it for this process reconnecting, not for another process.
 */
export function createRuntime(driver, options) {
  if (options?.package) options = { ...options, package: { ...options.package, name: wirePackageName(options.package.name) } }
  if (!options?.runtime) throw new Error('createRuntime: options.runtime (the runtime id) is required')
  const listen = options.listen ? (options.listen === true ? {} : options.listen) : undefined
  const token = options.token ?? listen?.token
  if (listen && !token) throw new Error('createRuntime: a listening runtime needs a token (options.listen.token)')
  const dialing = !listen
  const socketPath = options.socketPath ?? defaultSocketPath()
  const answerFirst = options.answerFirst ?? 'auto'
  const pushDelay = options.pushDelayMs ?? 300
  const baseRetry = options.retryMs ?? 1000
  const log = options.log ?? (() => {})
  const connect = options.connect ?? ((path) => createConnection(path))
  // This process's identity on the node (runtime.hello's instance): a reconnect is the same instance, another process
  // claiming the same runtime id is not, and the node refuses it (runtime_conflict).
  const instance = typeof options.instance === 'string' && options.instance !== '' ? options.instance : randomUUID()
  let warnedConflict = false
  // The form's entries beside options.launcher (runtime.hello's launchers), replaced by launchersChanged.
  let launchers = Array.isArray(options.launchers) ? options.launchers : undefined

  let peer
  let stopped = false
  // Bumped by stop(): a dialed connection's callbacks dial again only while their run is the current one. Without it a
  // stop() then start() in the same tick (a runtime restarting itself) leaves the old connection's close and adopt
  // tails seeing stopped === false, each dialing once more: two connections displacing each other forever. Only dialing
  // mode has this; in listen mode the node dials.
  let run = 0
  let retryTimer
  let retryMs = baseRetry
  let refreshTimer
  let server // the endpoint start() serves in listen mode
  let pushTimer
  let pushing // the push under way (a promise), if any
  let pushAgain = false
  const listed = new Set() // ids in the last pushed list
  let unlisted // the Claude transcripts the driver named last (driver.unlistedTranscripts); undefined until it has
  const watched = new Map() // session id -> { last, catchingUp, held: [], batch, timer }
  const streams = new Map() // session id -> { text, timer, sentAt }
  const pending = new Map() // session id -> interactions waiting on the phone
  const phoneDriven = new Map() // session id -> when the phone last sent to it or started it (oldest first)
  const sends = new Map() // client_msg_id -> the send's promise: a retry gets the first one's answer (oldest first)
  const drivenBy = (id) => {
    phoneDriven.delete(id)
    phoneDriven.set(id, Date.now())
    while (phoneDriven.size > 200) phoneDriven.delete(phoneDriven.keys().next().value)
  }
  // Whether the node has a hub connected (runtime.hello's hub, then runtime.hub). Older nodes do not say: assume so.
  let hubConnected = true
  // What the node does beyond the first protocol (runtime.hello's features; empty from older nodes).
  let nodeFeatures = []
  // Features this runtime cannot do without (options.requires): it does not stay on a node lacking one.
  const requires = Array.isArray(options.requires) ? options.requires : []
  let warnedRequires = false

  // ---- connection ----

  // After an older node: try again when the socket file is replaced (a node restarting re-creates it: an update), or
  // after NEW_NODE_MAX anyway — not every few seconds, which would fill the node's log with connects and leaves.
  function awaitNewNode() {
    const mine = run
    const id = () => {
      try {
        const st = statSync(socketPath)
        return `${st.ino}:${st.mtimeMs}`
      } catch {
        return ''
      }
    }
    const was = id()
    const since = Date.now()
    const look = () => {
      if (stopped || run !== mine) return
      if (id() !== was || Date.now() - since >= NEW_NODE_MAX) return open()
      retryTimer = setTimeout(look, NEW_NODE_EVERY)
      retryTimer.unref?.()
    }
    clearTimeout(retryTimer)
    retryTimer = setTimeout(look, NEW_NODE_EVERY)
    retryTimer.unref?.()
  }

  function scheduleConnect(delay) {
    if (stopped) return
    clearTimeout(retryTimer)
    retryTimer = setTimeout(open, delay)
    retryTimer.unref?.()
  }

  // The wait before the next try. A socket file means a node is there or coming back (a resident node keeps it while
  // it restarts): try again soon. No file: no node on this machine yet, back off.
  function backoff() {
    if (socketPresent()) {
      retryMs = baseRetry
      return Math.min(baseRetry, RETRY_PRESENT)
    }
    const d = retryMs
    retryMs = Math.min(retryMs * 2, RETRY_MAX)
    return d
  }

  function socketPresent() {
    try {
      return existsSync(socketPath)
    } catch {
      return false
    }
  }

  function hello() {
    const caps = {
      start: typeof driver.start === 'function',
      send: typeof driver.send === 'function',
      interrupt: typeof driver.interrupt === 'function',
      rename: typeof driver.rename === 'function',
      close: typeof driver.close === 'function',
      // It runs the Persona assistant's conversations (docs/integrations/runtimes.md "Assistant"): what that asks of it is the runtime's.
      assistant: options.assistant === true && typeof driver.start === 'function' && typeof driver.send === 'function',
    }
    // Said only when on: a runtime without them says what it always did.
    if (typeof driver.modes === 'function') caps.modes = true
    if (typeof driver.modes === 'function' && options.modelSwitch === true) caps.model = true
    if (typeof driver.models === 'function') caps.models = true
    if (typeof driver.check === 'function') caps.check = true
    // Its own archive, both ways: an archive Persona could not undo is not offered.
    if (typeof driver.archive === 'function' && typeof driver.unarchive === 'function') caps.archive = true
    if (options.resume === true && caps.start) caps.resume = true
    // It keeps no "always allow" rule of its own: the hub remembers one per session and tool for it (#3974).
    if (options.keepAlways === true) caps.keep_always = true
    // It enforces read-only (plan) starts itself, or refuses them (#3238).
    if (options.readonly === true && caps.start) caps.readonly = true
    const h = { runtime: options.runtime, name: options.name ?? options.runtime, version: options.version ?? '', instance, caps }
    if (options.package?.name && options.package?.hash) h.package = { name: options.package.name, hash: options.package.hash }
    if (options.agent) h.agent = options.agent
    if (options.launcher && caps.start) {
      h.launcher = options.launcher
      if (options.launcherModel) h.launcher_model = options.launcherModel
    }
    if (Array.isArray(launchers) && caps.start) h.launchers = launchers
    return h
  }

  function open() {
    if (stopped) return
    let socket
    try {
      socket = connect(socketPath)
    } catch {
      scheduleConnect(backoff())
      return
    }
    // heldOff: left an older node on purpose (awaitNewNode decides when to come back); run: the run that dialed it.
    const state = { heldOff: false, run }
    socket.once('error', () => {}) // handled by close
    socket.once('connect', () => void adopt(socket, state))
    socket.once('close', () => {
      if (state.heldOff || state.run !== run) return
      if (!peer || peer.socket !== socket) scheduleConnect(backoff())
    })
  }

  /**
   * Speak the protocol on a connection to the node: one dialed to runtime.sock, or one the node opened to this
   * daemon's endpoint (listen mode: a newer one replaces the last — the node reconnecting). In listen mode there is
   * nothing to dial again: the node does.
   */
  async function adopt(socket, state = {}) {
    if (dialing && (stopped || state.run !== run)) {
      // Dialed by a run stop() has ended (the socket connected after it): not this runtime's connection any more.
      socket.destroy()
      return
    }
    if (peer) {
      const old = peer
      peer = undefined
      old.close(new Error('replaced by a newer connection from the node'))
    }
    const p = new Peer(socket, { prefix: 'd', onRequest: (m, params) => request(m, params), onEvent: (event, params) => {
      if (event === 'runtime.hub' && peer === p && setHub(params?.connected !== false)) hubChanged()
    } })
    unwatchAll() // a new node asks again for what the phone watches
    listed.clear()
    peer = p // bound before hello: the node may call back the moment it has answered
    try {
      const res = await p.call('runtime.hello', hello(), { timeoutMs: 10_000 })
      setHub(res?.hub !== false)
      nodeFeatures = Array.isArray(res?.features) ? res.features.filter((f) => typeof f === 'string') : []
      const missing = requires.filter((f) => !nodeFeatures.includes(f))
      if (missing.length > 0) {
        // An older node: stay off it, and look again now and then (an updated node restarts and is found).
        if (!warnedRequires) log(`persona: this personad node lacks ${missing.join(', ')}; update personad (connecting again once it restarts)`)
        warnedRequires = true
        if (peer === p) peer = undefined
        state.heldOff = true
        p.close(new Error('node too old'))
        if (dialing && state.run === run) awaitNewNode()
        return
      }
      warnedRequires = false
      warnedConflict = false
      const offer = res?.update
      if (typeof options.onUpdate === 'function' && options.package && offer?.name === options.package.name &&
        typeof offer.hash === 'string' && offer.hash !== options.package.hash) {
        // After this connection is set up: the loader fetches the package through it.
        setImmediate(() => {
          try {
            options.onUpdate({ name: offer.name, hash: offer.hash, version: typeof offer.version === 'string' ? offer.version : '' })
          } catch (err) {
            log(`persona: onUpdate: ${err.message}`)
          }
        })
      }
    } catch (err) {
      if (peer === p) peer = undefined
      if (err instanceof RpcError && err.code === 'runtime_conflict') {
        // Another process is this runtime id on the node (two copies configured alike): do not fight it.
        if (!warnedConflict) log(`persona: ${err.message}; give this one another runtime id (trying again every ${CONFLICT_RETRY / 1000}s)`)
        warnedConflict = true
        state.heldOff = true
        p.close(err)
        if (dialing && state.run === run) scheduleConnect(CONFLICT_RETRY)
        return
      }
      log(`persona: hello failed: ${err.message}`)
      p.close(err)
      if (dialing && state.run === run) scheduleConnect(backoff())
      return
    }
    retryMs = baseRetry
    log('persona: connected to the node')
    schedulePush(0)
    hubChanged()
    const why = await p.done
    if (peer === p) peer = undefined
    log(`persona: node connection closed${why?.message ? `: ${why.message}` : ''}`)
    if (dialing && state.run === run) scheduleConnect(baseRetry)
  }

  /**
   * Listen mode: take an HTTP upgrade (an http.Server 'upgrade' event's arguments) as the node's connection. Checks
   * the token and the handshake; false (the socket closed) when refused.
   */
  function accept(req, socket, head) {
    if (!listen) throw new Error('accept: this runtime dials runtime.sock (no options.listen)')
    if (stopped) {
      socket.destroy()
      return false
    }
    const conn = upgrade(req, socket, head, { token: typeof token === 'function' ? token() : token })
    if (!conn) {
      log('persona: refused a connection (wrong token, or not the runtime protocol)')
      return false
    }
    void adopt(conn)
    return true
  }

  function serve() {
    const path = listen.path ?? DEFAULT_LISTEN_PATH
    server = createServer((req, res) => {
      res.writeHead(404, { 'content-type': 'text/plain' })
      res.end('Not Found')
    })
    server.on('upgrade', (req, socket, head) => {
      let pathname = ''
      try {
        pathname = new URL(req.url ?? '/', 'http://localhost').pathname
      } catch {}
      if (!defaultListenPathMatches(pathname, path, DEFAULT_LISTEN_PATH)) {
        socket.end('HTTP/1.1 404 Not Found\r\nConnection: close\r\nContent-Length: 0\r\n\r\n')
        return
      }
      accept(req, socket, head)
    })
    return new Promise((resolve, reject) => {
      server.once('error', reject)
      server.listen(listen.port ?? 0, listen.host ?? '127.0.0.1', () => {
        server.off('error', reject)
        server.on('error', (err) => log(`persona: endpoint: ${err.message}`))
        resolve(server.address())
      })
    })
  }

  // ---- session list ----

  function schedulePush(delay = pushDelay) {
    if (!peer) return
    clearTimeout(pushTimer)
    pushTimer = setTimeout(pushSessions, delay)
    pushTimer.unref?.()
  }

  /**
   * Push the list now and wait until it went: after a push already under way, however long it takes (it read the list
   * before what is new; a card sent meanwhile is refused for a session not listed yet).
   */
  async function listNow() {
    clearTimeout(pushTimer)
    while (pushing) await pushing
    await pushSessions()
  }

  function pushSessions() {
    if (!peer) return Promise.resolve()
    if (pushing) {
      pushAgain = true
      return pushing
    }
    const p = push().finally(() => {
      if (pushing === p) pushing = undefined
      if (pushAgain) {
        pushAgain = false
        schedulePush()
      }
    })
    pushing = p
    return p
  }

  async function push() {
    try {
      const rows = await driver.listSessions()
      // Read before the list counts as sent: nothing is awaited between `listed` and the push, or a card for a session
      // new in the list could go ahead of it (ask reads `listed`) and be refused. What the driver cannot say now (it
      // answers nothing, or throws) does not hold the list back, and the transcripts it named last stay named.
      if (typeof driver.unlistedTranscripts === 'function') {
        try {
          const named = await driver.unlistedTranscripts()
          if (Array.isArray(named)) unlisted = named.filter((id) => typeof id === 'string' && id)
        } catch (err) {
          log(`persona: unlisted transcripts: ${err.message}`)
        }
      }
      const list = []
      for (const row of rows ?? []) {
        if (!row?.session_id) continue
        // A session with a card on the phone needs you, whatever the runtime says.
        list.push((pending.get(row.session_id) ?? 0) > 0 ? { ...row, status: 'blocked' } : row)
      }
      listed.clear()
      for (const s of list) listed.add(s.session_id)
      // Named, even when none: the node keeps what a list without the field leaves unsaid.
      peer?.notify('runtime.sessions', unlisted ? { sessions: list, unlisted_transcripts: unlisted } : { sessions: list })
    } catch (err) {
      log(`persona: list sessions: ${err.message}`)
    }
  }

  // ---- watching ----

  /** Where a session's content ends now, and its history when the driver says (a cursor() may give only the seq). */
  async function cursorOf(sessionId) {
    const at = typeof driver.cursor === 'function' ? await driver.cursor(sessionId) : await driver.items({ session_id: sessionId, turns: 1 })
    return typeof at === 'number' ? { seq: at } : { seq: at?.to_seq ?? 0, history: at?.history }
  }

  async function watch(params) {
    const id = params.session_id
    const since = typeof params.since_seq === 'number' ? params.since_seq : 0
    const prev = watched.get(id)
    if (prev) clearTimeout(prev.timer)
    if (params.transcript === true) {
      // The node reads this session's items from its Claude transcript (the row's transcript): only the live text
      // goes from here; what append gets for it is dropped.
      watched.set(id, { last: 0, catchingUp: false, held: [], batch: undefined, timer: undefined, transcript: true })
      watchChanged(id, true, { transcript: true })
      return { ok: true }
    }
    const w = { last: since, history: undefined, catchingUp: true, held: [], batch: undefined, timer: undefined }
    watched.set(id, w)
    watchChanged(id, true, { transcript: false })
    try {
      if (since > 0) {
        // Catch up after since, then the live batches go on from where it ends.
        let after = since
        for (let i = 0; i < 8; i++) {
          const page = await driver.items({ session_id: id, after_seq: after })
          if (watched.get(id) !== w) return { ok: true }
          if ((page.items?.length ?? 0) > 0 || page.to_seq > w.last) {
            // As with the hub's backlog cap, only the last page of a truncated replay asks the client to fetch the rest.
            const moreAfter = i === 7 && page.more_after === true
            peer?.notify('session.items', { session_id: id, items: page.items ?? [], history: page.history, from_seq: page.from_seq ?? after, to_seq: page.to_seq, ...(moreAfter ? { more_after: true } : {}) })
            w.last = page.to_seq
          }
          if (page.history !== undefined) w.history = page.history
          if (!page.more_after) break
          after = page.to_seq
        }
      } else {
        const at = await cursorOf(id)
        w.last = at.seq
        w.history = at.history
      }
    } catch (err) {
      log(`persona: catch up ${id}: ${err.message}`)
    } finally {
      w.catchingUp = false
      // In order, each against what went out before it: queue drops what the catch-up already pushed, and a move to
      // another history and back is two rewrites, not one to skip.
      const held = w.held
      w.held = []
      for (const a of held) queue(id, w, a)
    }
    return { ok: true }
  }

  function unwatch(params) {
    const w = watched.get(params.session_id)
    if (w) clearTimeout(w.timer)
    watched.delete(params.session_id)
    if (w) watchChanged(params.session_id, false)
    return { ok: true }
  }

  /** Every watch ends (a new node asks again for what the phone watches; the runtime stops). */
  function unwatchAll() {
    for (const [id, w] of watched) {
      clearTimeout(w.timer)
      watchChanged(id, false)
    }
    watched.clear()
  }

  function watchChanged(id, watching, o) {
    if (typeof driver.watchChanged !== 'function') return
    try {
      driver.watchChanged(id, watching, o)
    } catch (err) {
      log(`persona: watchChanged ${id}: ${err.message}`)
    }
  }

  /**
   * New committed content of a session: the items of what was just written (maybe none — content the chat does not
   * show still moves the seq), from fromSeq (exclusive) to toSeq. Only watched sessions are pushed; batches that touch
   * are merged; a batch that does not start where the last one ended says so (from_seq), and the phone fills the gap.
   */
  function append(sessionId, { items = [], fromSeq, toSeq, history } = {}) {
    if (typeof toSeq !== 'number') throw new Error('append needs toSeq')
    const w = watched.get(sessionId)
    if (!w) {
      if (listed.has(sessionId)) schedulePush(2000) // rev moved: the phone pulls sessions it does not watch
      return
    }
    if (w.transcript) return // the node pushes its transcript's items
    const a = { items, fromSeq: typeof fromSeq === 'number' ? fromSeq : undefined, toSeq, history }
    if (w.catchingUp) {
      w.held.push(a)
      return
    }
    queue(sessionId, w, a)
  }

  /**
   * Whether a batch is of another history than what was pushed or is waiting to go: the content was rewritten (a
   * branch, a rewind) and its seq need not have grown, so it goes out whatever its seq.
   */
  function rewrites(w, a) {
    const current = w.batch ? w.batch.history : w.history
    return a.history !== undefined && current !== undefined && a.history !== current
  }

  function queue(id, w, a) {
    const rewritten = rewrites(w, a)
    if (!rewritten && a.toSeq <= w.last && w.batch === undefined) return // already pushed (caught up past it)
    const b = w.batch
    if (b && !rewritten && (a.fromSeq === undefined || a.fromSeq === b.toSeq) && a.history === b.history) {
      b.items.push(...a.items)
      b.toSeq = Math.max(b.toSeq, a.toSeq)
    } else {
      if (b) flush(id, w)
      // Another history starts over at its own seq: what came before it says nothing of where it goes on from.
      w.batch = { items: [...a.items], fromSeq: a.fromSeq ?? (rewritten ? undefined : w.last), toSeq: a.toSeq, history: a.history, rewritten }
    }
    if (!w.timer) {
      w.timer = setTimeout(() => flush(id, w), APPEND_WINDOW)
      w.timer.unref?.()
    }
  }

  function flush(id, w) {
    clearTimeout(w.timer)
    w.timer = undefined
    const b = w.batch
    w.batch = undefined
    if (!b || watched.get(id) !== w || !peer) return
    const msg = { session_id: id, items: b.items, to_seq: b.toSeq }
    if (b.fromSeq !== undefined) msg.from_seq = b.fromSeq
    if (b.history !== undefined) msg.history = b.history
    peer.notify('session.items', msg)
    w.last = b.rewritten ? b.toSeq : Math.max(w.last, b.toSeq)
    if (b.history !== undefined) w.history = b.history
  }

  // ---- live text ----

  /** The text being generated in a session so far (the whole of it each call); sent at most every 120 ms. */
  function stream(sessionId, text) {
    if (!watched.has(sessionId) || !peer) return
    let s = streams.get(sessionId)
    if (!s) {
      s = { text: '', timer: undefined, sentAt: 0 }
      streams.set(sessionId, s)
    }
    s.text = text
    if (s.timer) return
    const wait = Math.max(0, s.sentAt + STREAM_EVERY - Date.now()) // the first text goes out at once
    s.timer = setTimeout(() => {
      s.timer = undefined
      s.sentAt = Date.now()
      if (streams.get(sessionId) === s) peer?.notify('session.delta', { session_id: sessionId, text: s.text })
    }, wait)
    s.timer.unref?.()
  }

  /** The generated text is done (committed as items, or abandoned). */
  function streamEnd(sessionId) {
    const s = streams.get(sessionId)
    if (s) clearTimeout(s.timer)
    streams.delete(sessionId)
    if (s || watched.has(sessionId)) peer?.notify('session.delta', { session_id: sessionId, text: '' })
  }

  // ---- asking the phone ----

  /**
   * Whether the phone answers this session's approvals and questions first ('auto': the phone drove the session in
   * the last 6 hours, or the runtime's own UI is not open).
   */
  function setHub(connected) {
    if (connected === hubConnected) return false
    hubConnected = connected
    log(`persona: the node's hub ${connected ? 'is back' : 'is gone'}`)
    return true
  }

  function hubChanged() {
    if (typeof options.onHub !== 'function') return
    try {
      options.onHub(peer !== undefined && hubConnected)
    } catch (err) {
      log(`persona: onHub: ${err.message}`)
    }
  }

  function phoneFirst(sessionId) {
    // No hub: nobody can answer on the phone now. The local UI asks (a card already on the phone waits for the hub).
    if (!peer || !hubConnected || !listed.has(sessionId)) return false
    if (answerFirst === 'phone') return true
    if (answerFirst === 'local') return false
    const at = phoneDriven.get(sessionId)
    if (at !== undefined && Date.now() - at < PHONE_DRIVEN_FOR) return true
    return typeof driver.hasLocalClient === 'function' && driver.hasLocalClient() === false
  }

  /**
   * Put a question / plan / permission card on the phone and wait for the answer: the Resolution, or {dismiss: true}
   * when nobody answers there (handed back to the computer, expired, no hub). The session shows as needing you
   * meanwhile. Rejects only when signal aborts (the runtime withdrew it: the card is taken off).
   */
  async function ask(interaction, { signal, timeoutSeconds } = {}) {
    const sessionId = interaction.session_id
    pending.set(sessionId, (pending.get(sessionId) ?? 0) + 1)
    schedulePush(50)
    try {
      const deadline = Date.now() + RESUME_WAIT
      for (;;) {
        if (signal?.aborted) throw signal.reason ?? new Error('aborted')
        const p = peer
        if (!p) {
          if (Date.now() > deadline) return { dismiss: true }
          await sleep(1000, signal)
          continue
        }
        try {
          // The node takes a card only for a session it has listed: one that just appeared goes out first.
          if (!listed.has(sessionId)) await listNow()
          const params = { interaction }
          if (timeoutSeconds) params.timeout_seconds = timeoutSeconds
          return await p.call('interaction.create', params, { timeoutMs: 0, signal })
        } catch (err) {
          if (signal?.aborted) throw err
          if (err instanceof RpcError && (err.code === 'node_gone' || err.code === 'closed') && Date.now() < deadline) {
            await sleep(1000, signal) // the node restarts: ask the next one with the same id
            continue
          }
          log(`persona: ask the phone: ${err.message}`)
          return { dismiss: true }
        }
      }
    } catch (err) {
      if (signal?.aborted && interaction.id) peer?.notify('interaction.cancel', { id: interaction.id })
      throw err
    } finally {
      pending.set(sessionId, Math.max(0, (pending.get(sessionId) ?? 1) - 1))
      schedulePush(50)
    }
  }

  // ---- node → runtime calls ----

  async function request(method, params) {
    const need = (fn, what) => {
      if (typeof fn !== 'function') throw new RpcError(`unsupported: ${what}`, 'unsupported')
      return fn
    }
    switch (method) {
      case 'session.items':
        return driver.items(params)
      case 'session.image':
        return need(driver.image, 'image')(params)
      case 'session.item':
        return need(driver.item, 'item')(params)
      case 'session.watch':
        return watch(params)
      case 'session.unwatch':
        return unwatch(params)
      case 'session.send': {
        need(driver.send, 'send')
        drivenBy(params.session_id)
        // Once per client_msg_id: a retry (the app's, the node's after a reconnect) answers as the first did, or waits for
        // it; a failed one may be sent again.
        const key = typeof params.client_msg_id === 'string' && params.client_msg_id ? params.client_msg_id : undefined
        if (key && sends.has(key)) return sends.get(key)
        const p = (async () => {
          await driver.send(params)
          schedulePush() // what the phone sends to is active now: the list may change (a runtime keeps it listed)
          return { ok: true }
        })()
        if (key) {
          sends.set(key, p)
          p.catch(() => sends.delete(key))
          while (sends.size > 500) sends.delete(sends.keys().next().value)
        }
        return p
      }
      case 'session.interrupt':
        await need(driver.interrupt, 'interrupt')(params)
        return { ok: true }
      case 'session.rename':
        await need(driver.rename, 'rename')(params)
        schedulePush()
        return { ok: true }
      case 'session.close':
        await need(driver.close, 'close')(params)
        schedulePush()
        return { ok: true }
      case 'session.archive':
      case 'session.unarchive':
        if (!(typeof driver.archive === 'function' && typeof driver.unarchive === 'function')) need(undefined, 'archive')
        await (method === 'session.archive' ? driver.archive : driver.unarchive)(params)
        schedulePush()
        return { ok: true }
      case 'launchers.check':
        return need(driver.check, 'check')(params)
      case 'launchers.models':
        return need(driver.models, 'models')(params)
      case 'session.modes':
        await need(driver.modes, 'modes')(params)
        schedulePush()
        return { ok: true }
      case 'session.start': {
        const res = await need(driver.start, 'start')(params)
        const id = res?.session?.session_id
        if (id) drivenBy(id)
        schedulePush()
        return res
      }
    }
    throw new RpcError(`unknown method ${method}`, 'unsupported')
  }

  return {
    /**
     * Connect (and keep reconnecting) to the node; in listen mode with a port, serve the endpoint the node dials
     * (resolves to its address once listening).
     */
    start() {
      stopped = false
      if (options.refreshMs !== 0) {
        refreshTimer = setInterval(() => schedulePush(), options.refreshMs ?? 30_000)
        refreshTimer.unref?.()
      }
      if (!dialing) return typeof listen.port === 'number' ? serve() : Promise.resolve(undefined)
      open()
      return Promise.resolve(undefined)
    },
    accept,
    /** The endpoint's address while start() serves it (listen mode). */
    address: () => server?.address() ?? undefined,
    /** Disconnect for good. */
    stop() {
      stopped = true
      run++
      server?.close()
      server = undefined
      clearTimeout(retryTimer)
      clearTimeout(pushTimer)
      clearInterval(refreshTimer)
      unwatchAll()
      // Forget the live texts too: an entry left with its cleared timer would keep stream() waiting on it after a
      // start(). The next stream() carries the whole text again.
      for (const s of streams.values()) clearTimeout(s.timer)
      streams.clear()
      peer?.close(new Error('runtime stopped'))
      peer = undefined
    },
    /** The session list changed (a session appeared, went away, started or ended a turn, was renamed…). */
    sessionsChanged: (delayMs) => schedulePush(delayMs),
    /**
     * The launchers changed (options.launchers: a provider came or went, its models were found): the node offers
     * these from now on, and the next hello says them.
     * @param {{ name: string, model?: string, agent?: string, models?: object[] }[]} list
     */
    launchersChanged(list) {
      launchers = Array.isArray(list) ? list : []
      if (typeof driver.start === 'function') peer?.notify('runtime.launchers', { launchers })
    },
    append,
    stream,
    streamEnd,
    ask,
    phoneFirst,
    /** Whether the phone is watching a session (no need to compute what nobody gets). */
    watching: (sessionId) => watched.has(sessionId),
    /**
     * Whether the phone sent to or started session id lately (the latest 50): such a session stays listed past a
     * runtime's age and count limits — a chat open on the phone must stay in the list, or the node no longer knows
     * whose it is.
     */
    touched(id) {
      return [...phoneDriven.keys()].slice(-TOUCHED).includes(id)
    },
    /**
     * Which of a runtime's sessions to list (docs/integrations/runtimes.md "What to implement"): those pinned (running, waiting for an answer) and
     * those the phone touched always; the rest updated within `days`, newest first, until `max` in all. Rows are
     * anything; `id(row)` and `updatedAt(row)` (ms) read them (by default a runtime.sessions row). Newest first.
     * @template T
     * @param {T[]} rows
     * @param {{ days?: number, max?: number, pinned?: (row: T) => boolean, id?: (row: T) => string, updatedAt?: (row: T) => number }} [o]
     * @returns {T[]}
     */
    recent(rows, o = {}) {
      const days = o.days ?? 7
      const max = o.max ?? 80
      const id = o.id ?? ((r) => r.session_id)
      const at = o.updatedAt ?? ((r) => Date.parse(r.updated_at) || 0)
      const pinned = o.pinned ?? (() => false)
      const touched = new Set([...phoneDriven.keys()].slice(-TOUCHED))
      const since = Date.now() - days * 24 * 60 * 60 * 1000
      const sorted = [...rows].sort((a, b) => at(b) - at(a))
      const always = sorted.filter((r) => pinned(r) || touched.has(id(r)))
      const rest = sorted.filter((r) => !pinned(r) && !touched.has(id(r)) && at(r) >= since).slice(0, Math.max(0, max - always.length))
      return [...always, ...rest].sort((a, b) => at(b) - at(a))
    },
    /** Whether a node is connected. */
    get connected() {
      return peer !== undefined
    },
    /**
     * Whether the connected node does this (runtime.hello's features): "prefer_running" — when two runtimes list one
     * session, the one running it answers, so listing sessions only on disk (status offline) is safe.
     */
    nodeHas: (feature) => peer !== undefined && nodeFeatures.includes(feature),
    /** Whether that node has a hub connected (the phone can be asked). */
    get hubConnected() {
      return peer !== undefined && hubConnected
    },
    /**
     * The hub's tools for this runtime's agents (mcp.tools: the phone's phone_*, mail_*), [] without a node or hub.
     * @returns {Promise<{name: string, description: string, input_schema: object, shell?: string[]}[]>} - shell: a gated
     *   command's words, which the runtime refuses in an assistant conversation's shell
     */
    async tools({ assistant = false } = {}) {
      const p = peer
      if (!p) return []
      const res = await p.call('mcp.tools', assistant ? { assistant: true } : {}, { timeoutMs: 15_000 })
      return Array.isArray(res?.tools) ? res.tools.filter((t) => t && typeof t.name === 'string' && t.name !== '') : []
    },
    /**
     * Call one of the hub's tools (mcp.call). Phone tools may wait for the phone (woken in the background, or a
     * confirmation on it), so there is no timeout of its own: signal ends the wait.
     * @param {{name: string, args?: object, session_id?: string, assistant?: boolean}} params - assistant: the call is
     *   an assistant conversation's (the node checks it is one this runtime runs).
     * @param {{signal?: AbortSignal}} [opts]
     * @returns {Promise<{content: {type: string, text?: string, data?: string, mime_type?: string}[], is_error?: boolean}>}
     */
    async callTool(params, { signal } = {}) {
      const p = peer
      if (!p) return { content: [{ type: 'text', text: 'Persona is not connected on this machine' }], is_error: true }
      const call = { name: params.name, args: params.args ?? {}, session_id: params.session_id ?? '' }
      if (params.assistant) call.assistant = true
      return p.call('mcp.call', call, { timeoutMs: 0, signal })
    },
    /** Whether a card of this runtime is out on the phone (the loader waits for none before switching versions). */
    busy: () => [...pending.values()].some((n) => n > 0),
    /**
     * The node's package (runtime.package): {name, hash, version, files: [{path, data (base64)}]}; check it with
     * verifyPackage before using it.
     */
    async fetchPackage(name) {
      name = wirePackageName(name)
      const p = peer
      if (!p) throw new Error('not connected to the node')
      return p.call('runtime.package', { name }, { timeoutMs: 60_000 })
    },
    /** Push the list now (tests). */
    pushNow: pushSessions,
  }
}

function sleep(ms, signal) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => {
      clearTimeout(t)
      reject(signal.reason ?? new Error('aborted'))
    }, { once: true })
  })
}
