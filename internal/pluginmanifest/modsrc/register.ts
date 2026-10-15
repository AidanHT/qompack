// Qompack's hooks module, and the only mod source that touches the host's `$`.
//
// The host reads this file statically before it loads it: every host call must be written out in
// full, and the host object may be passed only to a function declared at the top level of this
// same file. Keeping every host call here, and every other source (compact.ts, format.ts and the
// two generated files) pure, satisfies that rule whichever way the host reads a call made through
// an imported helper, and keeps the full list of host calls in one place the validator reports.
//
// The mod is fail-open by design: a hook that cannot reach the binary passes the event on
// unchanged, so the classic hooks' behaviour is the floor. A surface without the process noun
// (the host marks it CLI only) reaches the same floor, whether the noun is missing or refuses the
// call. Diagnostics go to the host's debug log only, never to the screen.

import type { EngineInterface, Register } from 'claude-code'
import {
  decodeHello,
  encodeRequest,
  failureDetail,
  helloLogLine,
  helloRequest,
  runFailureDetail,
  unservedHello,
  verbArgv,
} from './format.ts'
import { REASON_ERROR, TIMEOUT_MS, VERB_HELLO } from './wire.gen.ts'
import type { HelloResponse } from './wire.gen.ts'

export const register: Register = (on) => {
  // Once per session: the engine's own start runs first, then the hello. Its result is the
  // engine's, unchanged, whatever the hello came to.
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    await hello($)
    return result
  })
}

/**
 * Announces this session to the binary as `qompack mod hello`, which writes the session's hello
 * file when the project is served, and logs what it came to. It never throws: every failure is an
 * unserved hello.
 */
export async function hello($: EngineInterface): Promise<HelloResponse> {
  const res = await announce($)
  debugLog($, helloLogLine(res))
  return res
}

/**
 * Runs the hello child with the session's values: the session root as both the request's cwd and
 * the child's, so the binary resolves the project root the way the classic hooks do.
 */
async function announce($: EngineInterface): Promise<HelloResponse> {
  let cwd: string
  let stdin: string
  try {
    cwd = await $.session.root()
    const version = await $.session.version()
    stdin = encodeRequest(helloRequest(await $.session.id(), cwd, version.version))
  } catch (err) {
    return unservedHello(REASON_ERROR, failureDetail('session', err))
  }
  try {
    const out = await $.process.run(verbArgv($.plugin.root, VERB_HELLO), { cwd, stdin, timeoutMs: TIMEOUT_MS })
    return decodeHello(out.exitCode, out.stdout)
  } catch (err) {
    return unservedHello(REASON_ERROR, runFailureDetail(err))
  }
}

/** Writes one line to the host's debug log; a host that cannot take it loses only the line. */
function debugLog($: EngineInterface, text: string): void {
  try {
    $.ui.log(text, { to: 'debug' })
  } catch {
    // Diagnostics are best effort.
  }
}
