// The omp host (docs/integrations/omp.md): the pi host (integrations/pi/lib/pi.js) saying it serves omp. omp's pi
// compatibility layer loads this package the way pi loads its extension, so the sessions, the conversation, the control
// (send, interrupt, cards) and the cards' ids are all the pi host's; only the identity and the resume command differ.

import { readFileSync } from 'node:fs'
import { createPiHost } from '../../pi/lib/pi.js'
import { agentDir, ompConfigPath } from './compat.js'

// identity is the agent this package serves: runtime and instance omp-<pid>, the apps' name omp, card ids that start
// with omp, and omp's own command to resume a session on disk.
export const identity = {
  agent: 'omp',
  name: 'omp',
  instancePrefix: 'omp',
  interactionPrefix: 'omp',
  resumeCommand: ['omp', '--resume'],
}

// config is <agent dir>/persona.json: { "approve": ["bash"], "askUser": true, "answerFirst": "auto", "socket": "…" }.
export function config(env = process.env) {
  try {
    return JSON.parse(readFileSync(ompConfigPath(agentDir(env)), 'utf8'))
  } catch {
    return {}
  }
}

// options are createPiHost's options for this process, and askUser whether to register the ask_user tool.
export function options(env = process.env, pid = process.pid) {
  const c = config(env)
  return {
    ...identity,
    instance: `${identity.instancePrefix}-${pid}`,
    socketPath: typeof c.socket === 'string' && c.socket ? c.socket : undefined,
    answerFirst: ['auto', 'phone', 'local'].includes(c.answerFirst) ? c.answerFirst : 'auto',
    approve: Array.isArray(c.approve) ? c.approve.filter((t) => typeof t === 'string') : [],
    askUser: c.askUser !== false,
  }
}

// host is this process's host: omp loads the extension once per session but there is one runtime connection per
// process. Its slot is its own (globalThis.__personaOmp), so a pi extension in the same process keeps __personaPi.
export function host(deps, env = process.env) {
  const slot = (globalThis.__personaOmp ??= {})
  if (slot.host) {
    if (slot.host.retired) slot.host.revive()
    return slot.host
  }
  const { askUser, ...hostOptions } = options(env, deps.pid ?? process.pid)
  slot.host = createPiHost(deps, hostOptions)
  globalThis.__personaOmpAskUser = askUser
  return slot.host
}
