// Persona runtime for omp (Oh My Pi, docs/integrations/omp.md): an omp extension that puts the sessions of this omp
// process into the Persona apps through the personad node on this machine (~/.persona/runtime.sock,
// docs/integrations/runtimes.md). It does nothing visible until a node listens.
//
// omp loads a pi extension through its compatibility layer, so this package is the pi host (integrations/pi) under
// omp's identity. Configuration (optional): <agent dir>/persona.json — { "approve": ["bash"], "askUser": true,
// "answerFirst": "auto" }. There is no auto-update: install it with `omp install <Persona>/integrations/omp`.

import { SessionManager } from '@earendil-works/pi-coding-agent'
import { Type } from 'typebox'
import { envValue } from '../runtime-sdk/lib/identity.js'
import { herdrPaneOf } from '../pi/lib/pi.js'
import { oneShot } from './lib/compat.js'
import { host } from './lib/host.js'

export default function persona(omp) {
  // Not for one-shot runs (omp -p, --print, --mode json): nothing to drive from the phone.
  if (oneShot(process.argv)) return
  const h = host({
    SessionManager,
    // An omp in a herdr pane: the node shows it as herdr's session (a named herdr session comes with the pane).
    paneId: herdrPaneOf(process.env),
    log: (m) => {
      if (envValue('PERSONA_OMP_DEBUG')) console.error(m)
    },
  })
  h.register(omp, globalThis.__personaOmpAskUser ? { Type } : undefined)
}
