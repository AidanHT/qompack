// The bridge's pure half: how a `qompack mod <verb>` child is named, what goes on its stdin, and
// how its stdout is read. Nothing here calls the host; register.ts makes the call and hands the
// result to these functions.
//
// Reading is fail-open. A child that exits non-zero, prints something that is not one valid
// response, or speaks another wire version is reported as an unserved response with reason
// 'error', never thrown, so a failing binary can only ever leave a compaction unsteered.

import { BINARY_REL_PATH } from './target.gen.ts'
import {
  FACT_ID_CHARS,
  MAX_DETAIL_BYTES,
  MAX_FACT_ID_BYTES,
  MAX_FACT_IDS,
  MAX_MESSAGE_BYTES,
  REASON_ERROR,
  REASONS,
  WIRE_VERSION,
} from './wire.gen.ts'
import type { BriefRequest, BriefResponse, HelloRequest, HelloResponse, Reason, Verb } from './wire.gen.ts'

/**
 * The bundled executable's absolute path: the plugin root the host reports, joined with the
 * target's relative binary path. The host's process API expands no placeholders, so the mod joins
 * the path itself, using the root's own separator so a Windows root stays a Windows path.
 */
export function binaryPath(pluginRoot: string, rel: string = BINARY_REL_PATH): string {
  const windows = pluginRoot.includes('\\') && !pluginRoot.includes('/')
  const sep = windows ? '\\' : '/'
  const tail = windows ? rel.split('/').join('\\') : rel
  const root = pluginRoot.endsWith(sep) ? pluginRoot.slice(0, -1) : pluginRoot
  return root + sep + tail
}

/** The argv for one verb: the binary, then `mod <verb>`. No shell is involved. */
export function verbArgv(pluginRoot: string, verb: Verb): string[] {
  return [binaryPath(pluginRoot), 'mod', verb]
}

/** One request as the child's stdin: one JSON object and a newline. */
export function encodeRequest(req: HelloRequest | BriefRequest): string {
  return JSON.stringify(req) + '\n'
}

/** An unserved hello. */
export function unservedHello(reason: Reason, detail?: string): HelloResponse {
  const r: HelloResponse = { v: WIRE_VERSION, ok: false, reason, enabled: false }
  if (detail !== undefined && detail !== '') r.detail = detail
  return r
}

/** An unserved brief: no instructions, no fact ids. */
export function unservedBrief(reason: Reason, detail?: string): BriefResponse {
  const r: BriefResponse = { v: WIRE_VERSION, ok: false, reason, instructions: '', fact_ids: [] }
  if (detail !== undefined && detail !== '') r.detail = detail
  return r
}

/**
 * Describes a failure for an unserved response's detail: the stage that failed, then the error's
 * message cut to 200 characters. It never throws.
 */
export function failureDetail(stage: string, err: unknown): string {
  let text: string
  try {
    text = err instanceof Error ? err.message : String(err)
  } catch {
    text = 'unreadable error'
  }
  return stage + ': ' + text.slice(0, 200)
}

/**
 * Describes why the host's process API rejected (the binary could not start, or outlived the
 * mod's timeout), for an unserved response's detail. It never throws.
 */
export function runFailureDetail(err: unknown): string {
  return failureDetail('process', err)
}

/** The hello request a session sends once it starts. */
export function helloRequest(sessionId: string, cwd: string, hostVersion: string): HelloRequest {
  return { v: WIRE_VERSION, session_id: sessionId, cwd, host_version: hostVersion }
}

/** One diagnostic line saying what a hello came to, for the host's debug log. */
export function helloLogLine(r: HelloResponse): string {
  if (r.ok) return 'qompack mod hello: served, ' + (r.enabled ? 'enabled' : 'not enabled for this host')
  return 'qompack mod hello: ' + r.reason + (r.detail !== undefined && r.detail !== '' ? ' (' + r.detail + ')' : '')
}

const encoder = new TextEncoder()

// Built from the published alphabet, as the Go validator is. Every allowed byte is ASCII, so the
// String length of a matching id is its byte length.
const factIdPattern = new RegExp('^[' + FACT_ID_CHARS + ']+$')

function byteLength(s: string): number {
  return encoder.encode(s).length
}

type Parsed = { ok: true; value: Record<string, unknown> } | { ok: false; detail: string }

/** Parses one child's result into a JSON object that passed the common status checks. */
function parseStatus(exitCode: number, stdout: string): Parsed {
  if (exitCode !== 0) return { ok: false, detail: 'exit ' + exitCode }
  if (byteLength(stdout) > MAX_MESSAGE_BYTES) return { ok: false, detail: 'response too large' }
  let value: unknown
  try {
    value = JSON.parse(stdout)
  } catch {
    return { ok: false, detail: 'response is not JSON' }
  }
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return { ok: false, detail: 'response is not an object' }
  }
  const obj = value as Record<string, unknown>
  if (obj.v !== WIRE_VERSION) return { ok: false, detail: 'unsupported wire version ' + String(obj.v) }
  if (typeof obj.ok !== 'boolean') return { ok: false, detail: 'response has no ok' }
  if (obj.ok && obj.reason !== undefined) return { ok: false, detail: 'served response carries a reason' }
  if (!obj.ok && !REASONS.includes(obj.reason as Reason)) {
    return { ok: false, detail: 'unknown reason ' + String(obj.reason) }
  }
  if (obj.detail !== undefined && typeof obj.detail !== 'string') {
    return { ok: false, detail: 'detail is not a string' }
  }
  if (typeof obj.detail === 'string' && byteLength(obj.detail) > MAX_DETAIL_BYTES) {
    return { ok: false, detail: 'detail too long' }
  }
  return { ok: true, value: obj }
}

/** Reads a hello child's result. Fields the contract does not name are dropped. */
export function decodeHello(exitCode: number, stdout: string): HelloResponse {
  const p = parseStatus(exitCode, stdout)
  if (!p.ok) return unservedHello(REASON_ERROR, p.detail)
  const o = p.value
  // An unserved answer may be a bare status: the error object a side sends for a request it could
  // not read carries no verb fields.
  if (!o.ok) return unservedHello(o.reason as Reason, o.detail as string | undefined)
  if (typeof o.enabled !== 'boolean') return unservedHello(REASON_ERROR, 'enabled is not a boolean')
  return { v: WIRE_VERSION, ok: true, enabled: o.enabled }
}

/**
 * Reads a brief child's result, holding the instructions to `maxChars` (String length, the unit
 * the Go side counts in). Fields the contract does not name are dropped.
 */
export function decodeBrief(exitCode: number, stdout: string, maxChars: number): BriefResponse {
  const p = parseStatus(exitCode, stdout)
  if (!p.ok) return unservedBrief(REASON_ERROR, p.detail)
  const o = p.value
  if (!o.ok) {
    // An unserved answer may be a bare status (see decodeHello); if it does carry the brief's
    // fields they must be empty.
    const emptyText = o.instructions === undefined || o.instructions === ''
    const emptyIds = o.fact_ids === undefined || (Array.isArray(o.fact_ids) && o.fact_ids.length === 0)
    if (!emptyText || !emptyIds) return unservedBrief(REASON_ERROR, 'unserved brief carries content')
    return unservedBrief(o.reason as Reason, o.detail as string | undefined)
  }
  if (typeof o.instructions !== 'string') return unservedBrief(REASON_ERROR, 'instructions is not a string')
  if (!Array.isArray(o.fact_ids)) return unservedBrief(REASON_ERROR, 'fact_ids is not an array')
  if (o.instructions.length > maxChars) return unservedBrief(REASON_ERROR, 'instructions over max_chars')
  if (o.fact_ids.length > MAX_FACT_IDS) return unservedBrief(REASON_ERROR, 'too many fact ids')
  const ids: string[] = []
  for (const id of o.fact_ids) {
    if (typeof id !== 'string' || id.length > MAX_FACT_ID_BYTES || !factIdPattern.test(id)) {
      return unservedBrief(REASON_ERROR, 'bad fact id')
    }
    ids.push(id)
  }
  return { v: WIRE_VERSION, ok: true, instructions: o.instructions, fact_ids: ids }
}
