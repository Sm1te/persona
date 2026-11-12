// What differs from pi for this package (docs/integrations/omp.md): omp's agent directory and its one-shot runs. The
// extension API and the host itself are pi's (integrations/pi/lib), loaded by omp's pi compatibility layer.

import { homedir } from 'node:os'
import { join } from 'node:path'

// agentDir is where omp keeps its configuration and sessions: PI_CODING_AGENT_DIR when it names one (omp reads the
// same variable), else ~/.omp/agent. A pi's own directory is not used: omp defaults to its own.
export function agentDir(env = process.env) {
  const dir = typeof env.PI_CODING_AGENT_DIR === 'string' ? env.PI_CODING_AGENT_DIR.trim() : ''
  return dir || join(homedir(), '.omp', 'agent')
}

// ompConfigPath is Persona's own settings for this agent, the same file and fields as pi's extension.
export function ompConfigPath(dir = agentDir()) {
  return join(dir, 'persona.json')
}

// oneShot: omp -p / --print, or --mode json, has nothing to drive from the phone. A bare "json" is a word of a
// prompt, not a mode.
export function oneShot(argv = process.argv) {
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]
    if (a === '-p' || a === '--print' || a === '--mode=json') return true
    if (a === '--mode' && argv[i + 1] === 'json') return true
  }
  return false
}
