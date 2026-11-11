// The bridge's parts that do not need a process: the input stream, the control requests, the resume replay guard and
// the redaction of stderr. bridge.mjs wires them to stdin, stdout and the SDK's query().

import { chmodSync, existsSync, lstatSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { homedir, tmpdir } from 'node:os'
import { join } from 'node:path'

// The keys of persona_start's options passed on to query() (docs: CONTRACT §3). Anything else is dropped and named
// (only the name) on stderr: a typo must not silently become a CLI default.
export const OPTION_KEYS = new Set([
  'model', 'cwd', 'resume', 'sessionId', 'permissionMode', 'allowedTools', 'tools', 'mcpServers', 'strictMcpConfig', 'plugins', 'settings',
  'guestMcpServers',
  'pathToClaudeCodeExecutable', 'includePartialMessages', 'effort', 'extraArgs',
])

// queryOptions is what query() gets: the allowed keys of start, the bridge's own env (the node computed it), partial
// messages always on (the node's live text is built from stream_event deltas).
export function queryOptions(start, env, log) {
  const opts = {}
  for (const [k, v] of Object.entries(start ?? {})) {
    if (OPTION_KEYS.has(k)) opts[k] = v
    else log(`ignored option ${k}`)
  }
  opts.includePartialMessages = true
  opts.env = env
  return opts
}

// settingsFile writes a settings object to a 0600 file in a fresh private (0700) directory and returns both: the SDK
// hands an object to claude as `--settings <json>`, which would put the provider's env (key, headers) on a command
// line anyone can read with ps; a path keeps it in the file. The caller removes dir when it exits.
export function settingsFile(settings, root = tmpdir()) {
  const dir = mkdtempSync(join(root, `persona-agent-sdk-${process.pid}-`))
  chmodSync(dir, 0o700)
  const path = join(dir, 'settings.json')
  writeFileSync(path, JSON.stringify(settings), { mode: 0o600 })
  return { dir, path }
}

// sweepSettings removes the settings directories of bridges that are gone: one killed (SIGKILL, a crash) runs no exit
// handler, and its file holds the provider's key. A directory names the pid that made it; a pid still running is left.
export function sweepSettings(root = tmpdir()) {
  let names = []
  try {
    names = readdirSync(root)
  } catch {
    return 0
  }
  let swept = 0
  for (const name of names) {
    const m = /^persona-agent-sdk-(\d+)-/.exec(name)
    if (!m || Number(m[1]) === process.pid || alive(Number(m[1]))) continue
    // Only our own: a shared /tmp holds other users' bridges too (their pid looks gone to us), and removing one fails.
    try {
      const st = lstatSync(join(root, name))
      if (!st.isDirectory() || (typeof process.getuid === 'function' && st.uid !== process.getuid())) continue
      rmSync(join(root, name), { recursive: true, force: true })
      swept++
    } catch {
      // Gone already, or not ours to remove: never stops the bridge from starting.
    }
  }
  return swept
}

function alive(pid) {
  try {
    process.kill(pid, 0)
    return true
  } catch (e) {
    return e.code === 'EPERM' // someone else's process: not ours to judge
  }
}

// Inbox is the prompt of a streaming query(): an AsyncIterable the bridge pushes user messages into, ended when the
// node closes stdin.
export class Inbox {
  #queue = []
  #wait = null
  #ended = false

  // #settle hands a result to the reader waiting in next(), if one is; false when none is.
  #settle(result) {
    const w = this.#wait
    this.#wait = null
    w?.(result)
    return Boolean(w)
  }

  push(msg) {
    if (!this.#ended && !this.#settle({ value: msg, done: false })) this.#queue.push(msg)
  }

  end() {
    this.#ended = true
    this.#settle({ value: undefined, done: true })
  }

  [Symbol.asyncIterator]() {
    return {
      next: () => {
        if (this.#queue.length) return Promise.resolve({ value: this.#queue.shift(), done: false })
        if (this.#ended) return Promise.resolve({ value: undefined, done: true })
        return new Promise((resolve) => { this.#wait = resolve })
      },
      return: () => {
        this.end()
        return Promise.resolve({ value: undefined, done: true })
      },
    }
  }
}

// userMessage is a stdin user line as the SDK takes it: claude -p's stream-json input leaves parent_tool_use_id out,
// SDKUserMessage requires it.
export function userMessage(line) {
  return { parent_tool_use_id: null, ...line }
}

// The control requests the node sends (node/modes.go syncAssistantModes, and interrupt), each to its Query method.
const CONTROLS = {
  interrupt: (q) => q.interrupt(),
  set_permission_mode: (q, r) => q.setPermissionMode(r.mode),
  set_model: (q, r) => q.setModel(r.model),
  apply_flag_settings: (q, r) => q.applyFlagSettings(r.settings ?? {}),
}

// control answers a control_request line the way claude -p does: control_response with subtype success (and the
// method's result, if any, under response) or error (and the message under error); node/assistant.go reads
// response.subtype and response.error.
export async function control(q, line, redact) {
  const id = line.request_id
  const req = line.request ?? {}
  const fn = CONTROLS[req.subtype]
  if (!fn) return { type: 'control_response', response: { subtype: 'error', request_id: id, error: `unsupported control request: ${req.subtype}` } }
  try {
    const res = await fn(q, req)
    return { type: 'control_response', response: { subtype: 'success', request_id: id, ...(res != null ? { response: res } : {}) } }
  } catch (err) {
    return { type: 'control_response', response: { subtype: 'error', request_id: id, error: redact(String(err?.message ?? err)) } }
  }
}

// projectDir is Claude Code's directory for a cwd's transcripts: every character but [a-zA-Z0-9] becomes '-'.
export function projectDir(configDir, cwd) {
  return join(configDir, 'projects', cwd.replace(/[^a-zA-Z0-9]/g, '-'))
}

const UUID_RE = /"uuid":"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})"/g

// transcriptUUIDs are the message uuids already in a session's transcript (the main one and its subagents'), read
// before the resumed CLI starts: what it sends again carries one of them. The project directory is the cwd's, else
// any project holding <id>.jsonl (a long cwd's name is shortened). null: no transcript found.
export function transcriptUUIDs(id, cwd, env = process.env) {
  const configDir = env.CLAUDE_CONFIG_DIR || join(env.HOME || homedir(), '.claude')
  let dir = projectDir(configDir, cwd)
  if (!existsSync(join(dir, `${id}.jsonl`))) {
    dir = null
    try {
      for (const e of readdirSync(join(configDir, 'projects'), { withFileTypes: true })) {
        const d = join(configDir, 'projects', e.name)
        if (e.isDirectory() && existsSync(join(d, `${id}.jsonl`))) { dir = d; break }
      }
    } catch {}
    if (!dir) return null
  }
  const files = [join(dir, `${id}.jsonl`)]
  const stack = [join(dir, id, 'subagents')]
  while (stack.length) {
    const d = stack.pop()
    let entries
    try { entries = readdirSync(d, { withFileTypes: true }) } catch { continue }
    for (const e of entries) {
      if (e.isDirectory()) stack.push(join(d, e.name))
      else if (e.name.endsWith('.jsonl')) files.push(join(d, e.name))
    }
  }
  const uuids = new Set()
  for (const f of files) {
    let text
    try { text = readFileSync(f, 'utf8') } catch { continue }
    for (const m of text.matchAll(UUID_RE)) uuids.add(m[1])
  }
  return uuids
}

// ReplayGuard drops what a resumed CLI sends again from before this start (README "Resume dedup"): a message whose
// uuid the transcript already had, and a result before this process streamed anything — a turn of this process
// always streams (partial messages are on) before its result, a replayed result is synthesized fresh and is in no
// transcript.
export class ReplayGuard {
  #seen
  #resumed
  #live = false

  constructor(seen, resumed) {
    this.#seen = seen ?? new Set()
    this.#resumed = resumed
  }

  // keep: whether msg goes to stdout. pending: a user line is waiting for its result — a result then is its answer
  // even with no stream_event before it (an API error at once, a local command like /compact).
  keep(msg, pending = false) {
    if (!this.#resumed) return true
    if (msg.type === 'stream_event') this.#live = true
    if (msg.uuid && this.#seen.has(msg.uuid)) return false
    if (msg.type === 'result' && !this.#live && !pending) return false
    return true
  }
}

// The env names whose values are secrets, or reveal where the gateway is.
const SECRET_RE = /(KEY|TOKEN|SECRET|PASSWORD|AUTH|HEADERS|BASE_URL|PROXY|COOKIE)/i

// secretValues are the values of env's secret-looking vars (long enough to be one).
export function secretValues(env) {
  const out = []
  for (const [k, v] of Object.entries(env)) {
    if (!SECRET_RE.test(k) || typeof v !== 'string') continue
    out.push(v)
    // A header line or its value alone may be quoted in an error, not the whole ANTHROPIC_CUSTOM_HEADERS.
    if (/HEADERS/i.test(k)) {
      for (const line of v.split(/\r?\n/)) {
        const i = line.indexOf(':')
        out.push(line.trim(), i >= 0 ? line.slice(i + 1).trim() : '')
      }
    }
  }
  return [...new Set(out)].filter((s) => s.length >= 6)
}

// redactor replaces the values of the secret-looking env vars in a log line: errors the SDK and the CLI raise may
// quote the request (a header, the gateway's address).
export function redactor(env) {
  const values = secretValues(env).sort((a, b) => b.length - a.length)
  return (s) => {
    let out = String(s)
    for (const v of values) out = out.split(v).join('***')
    return out
  }
}
