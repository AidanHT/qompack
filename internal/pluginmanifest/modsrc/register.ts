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

import type {
  EngineInterface,
  Next,
  ProcessRunResult,
  Register,
  SessionCompactInput,
  SessionCompactResult,
} from 'claude-code'
import { briefRequest, hasBriefMarker, mergeInstructions } from './compact.ts'
import {
  briefLogLine,
  decodeBrief,
  decodeHello,
  encodeRequest,
  failureDetail,
  helloLogLine,
  helloRequest,
  runFailureDetail,
  unservedBrief,
  unservedHello,
  verbArgv,
} from './format.ts'
import { BRIEF_MAX_CHARS, REASON_ERROR, TIMEOUT_MS, VERB_BRIEF, VERB_HELLO } from './wire.gen.ts'
import type { BriefResponse, HelloResponse, Verb } from './wire.gen.ts'

/** The values every request carries: the session's id, its root and the host's version. */
type SessionValues = { id: string; root: string; version: string }

/**
 * The last hello this module instance ran, and the session it ran for. The binary is the only
 * gate: the compaction hook briefs only after a served hello that answered steering true, for the
 * session the compaction belongs to. Held in memory only; the mod keeps no state of its own.
 */
let lastHello: { sessionId: string; answer: HelloResponse } | undefined

export const register: Register = (on) => {
  // Once per session: the engine's own start runs first, then the hello. Its result is the
  // engine's, unchanged, whatever the hello came to.
  on('session.start', async ($, e, next) => {
    const result = await next(e)
    await hello($)
    return result
  })

  // Every compaction: the brief, when there is one, joins the instructions; anything else passes
  // the compaction on as it came.
  on('session.compact', async ($, e, next) => steerCompaction($, e, next))
}

/**
 * Announces this session to the binary as `qompack mod hello`, which writes the session's hello
 * file when the project is served, caches the answer and logs what it came to. It never throws:
 * every failure is an unserved hello.
 */
export async function hello($: EngineInterface): Promise<HelloResponse> {
  let values: SessionValues
  try {
    values = await readSession($)
  } catch (err) {
    lastHello = undefined
    const res = unservedHello(REASON_ERROR, failureDetail('session', err))
    debugLog($, helloLogLine(res))
    return res
  }
  return helloFor($, values)
}

/**
 * The session.compact hook. A subagent's compaction, and one whose instructions already carry a
 * brief, pass on untouched with no call. Otherwise the hook makes sure a served hello for this
 * session is cached (running hello again when it is not), asks for a brief only when that hello
 * answered steering true, and passes the compaction on with the brief after the person's own
 * instructions. Any failure on the way passes the compaction on exactly as it came; next runs
 * once, and its own result or failure is the hook's.
 */
export async function steerCompaction(
  $: EngineInterface,
  e: SessionCompactInput,
  next: Next<'session.compact'>,
): Promise<SessionCompactResult> {
  if (e.agentId !== undefined) return next(e)
  if (hasBriefMarker(e.instructions)) return next(e)
  let instructions: string | undefined
  try {
    instructions = await briefedInstructions($, e)
  } catch {
    instructions = e.instructions
  }
  if (instructions === e.instructions) return next(e)
  return next({ ...e, instructions })
}

/**
 * The instructions one compaction runs with: the person's own, followed by a served brief when
 * the cached hello for this session answered steering true. Without a brief they come back as
 * they were, absent included.
 */
async function briefedInstructions($: EngineInterface, e: SessionCompactInput): Promise<string | undefined> {
  const values = await readSession($)
  let answer = lastHello?.sessionId === values.id ? lastHello.answer : undefined
  if (answer === undefined || !answer.ok) answer = await helloFor($, values)
  if (!answer.ok || !answer.steering) return e.instructions
  const res = await brief($, values, e.trigger, e.instructions)
  debugLog($, briefLogLine(res))
  return mergeInstructions(e.instructions, res)
}

/** Runs the hello child for the session values given, caches its answer and logs it. */
async function helloFor($: EngineInterface, values: SessionValues): Promise<HelloResponse> {
  const res = await announce($, values)
  lastHello = { sessionId: values.id, answer: res }
  debugLog($, helloLogLine(res))
  return res
}

/** Reads the session values every request carries; it throws when the host cannot give them. */
async function readSession($: EngineInterface): Promise<SessionValues> {
  const root = await $.session.root()
  const version = await $.session.version()
  const id = await $.session.id()
  return { id, root, version: version.version }
}

/**
 * Runs the hello child with the session's values: the session root as both the request's cwd and
 * the child's, so the binary resolves the project root the way the classic hooks do.
 */
async function announce($: EngineInterface, values: SessionValues): Promise<HelloResponse> {
  const stdin = encodeRequest(helloRequest(values.id, values.root, values.version))
  try {
    const out = await runVerb($, VERB_HELLO, values.root, stdin)
    return decodeHello(out.exitCode, out.stdout)
  } catch (err) {
    return unservedHello(REASON_ERROR, runFailureDetail(err))
  }
}

/**
 * Runs the brief child for one compaction, with the same cwd rule as the hello. Only whether the
 * person gave instructions travels, never their text. It never throws: every failure is an
 * unserved brief, which leaves the compaction's instructions as they were.
 */
async function brief(
  $: EngineInterface,
  values: SessionValues,
  trigger: string,
  instructions: string | undefined,
): Promise<BriefResponse> {
  const stdin = encodeRequest(briefRequest(values.id, values.root, values.version, trigger, instructions))
  try {
    const out = await runVerb($, VERB_BRIEF, values.root, stdin)
    return decodeBrief(out.exitCode, out.stdout, BRIEF_MAX_CHARS)
  } catch (err) {
    return unservedBrief(REASON_ERROR, runFailureDetail(err))
  }
}

/**
 * Starts `qompack mod <verb>` in `cwd` with one request on its stdin, under the mod's timeout. The
 * one place the module starts a process, for every verb. It rejects when the host does: the binary
 * could not start, outlived the timeout, or the surface has no process noun.
 */
async function runVerb($: EngineInterface, verb: Verb, cwd: string, stdin: string): Promise<ProcessRunResult> {
  return $.process.run(verbArgv($.plugin.root, verb), { cwd, stdin, timeoutMs: TIMEOUT_MS })
}

/** Writes one line to the host's debug log; a host that cannot take it loses only the line. */
function debugLog($: EngineInterface, text: string): void {
  try {
    $.ui.log(text, { to: 'debug' })
  } catch {
    // Diagnostics are best effort.
  }
}
