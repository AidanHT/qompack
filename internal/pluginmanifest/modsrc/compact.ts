// The compaction half of the mod, as pure functions: the brief request a compaction sends, and how
// a served brief joins the instructions the compaction already carries. register.ts supplies the
// session's values and makes the call.

import { BRIEF_MARKER, BRIEF_MAX_CHARS, WIRE_VERSION } from './wire.gen.ts'
import type { BriefRequest, BriefResponse } from './wire.gen.ts'

/** Whether the person gave compaction instructions of their own (anything but blank). */
export function hasUserInstructions(instructions: string | undefined): boolean {
  return typeof instructions === 'string' && instructions.trim() !== ''
}

/**
 * The brief request for one compaction. The host version is the `version` field of
 * `$.session.version()`; the Go side gates every brief on it. Only the fact that the person gave
 * instructions travels, never their text.
 */
export function briefRequest(
  sessionId: string,
  cwd: string,
  hostVersion: string,
  trigger: string,
  userInstructions: string | undefined,
): BriefRequest {
  return {
    v: WIRE_VERSION,
    session_id: sessionId,
    cwd,
    host_version: hostVersion,
    trigger,
    user_instructions_present: hasUserInstructions(userInstructions),
    max_chars: BRIEF_MAX_CHARS,
  }
}

/**
 * Whether a compaction's instructions already carry a brief: they contain BRIEF_MARKER, which a
 * served brief's instructions open with. A compaction whose instructions do needs no second brief.
 */
export function hasBriefMarker(instructions: string | undefined): boolean {
  return typeof instructions === 'string' && instructions.includes(BRIEF_MARKER)
}

/**
 * The instructions the compaction runs with once the brief is in. The person's text stays first
 * and verbatim; a served, non-empty brief follows it after a blank line, and the brief part opens
 * with BRIEF_MARKER (decodeBrief refuses one that does not), so the result answers hasBriefMarker
 * true. An unserved or empty brief leaves the instructions exactly as they were, including absent.
 */
export function mergeInstructions(user: string | undefined, brief: BriefResponse): string | undefined {
  if (!brief.ok || brief.instructions === '') return user
  if (!hasUserInstructions(user)) return brief.instructions
  return user + '\n\n' + brief.instructions
}
