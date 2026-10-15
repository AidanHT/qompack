// Tests for the compaction half's pure functions. Like format.test.ts they run on the materialised
// plugin, hence the ../hooks/ imports.

import { describe, expect, test } from 'claude-code/testing'
import { briefRequest, hasUserInstructions, mergeInstructions } from '../hooks/compact.ts'
import { encodeRequest, unservedBrief } from '../hooks/format.ts'
import { BRIEF_MAX_CHARS } from '../hooks/wire.gen.ts'

// internal/modwire's brief request golden, verbatim: the mod writes exactly these bytes.
const briefRequestGolden =
  '{"v":1,"session_id":"3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b","cwd":"/home/dev/project","host_version":"2.1.300","trigger":"auto","user_instructions_present":true,"max_chars":4000}'

const served = { v: 1, ok: true, instructions: 'Keep the ledger facts.', fact_ids: ['f1'] }

describe('briefRequest', () => {
  test('carries the session values, the host version, the default cap and only whether instructions exist', async () => {
    expect(briefRequest('s-1', '/p', '2.1.300', 'auto', 'keep the db notes')).toEqual({
      v: 1,
      session_id: 's-1',
      cwd: '/p',
      host_version: '2.1.300',
      trigger: 'auto',
      user_instructions_present: true,
      max_chars: BRIEF_MAX_CHARS,
    })
    expect(JSON.stringify(briefRequest('s-1', '/p', '2.1.300', 'auto', 'keep the db notes'))).not.toContain('db notes')
  })

  test('encodes in the Go golden field order', async () => {
    const req = briefRequest('3f2a9c1e-5b7d-4e8f-9a0b-1c2d3e4f5a6b', '/home/dev/project', '2.1.300', 'auto', 'mine')
    expect(encodeRequest(req)).toBe(briefRequestGolden + '\n')
  })

  test('blank instructions are no instructions', async () => {
    expect(hasUserInstructions(undefined)).toBe(false)
    expect(hasUserInstructions('')).toBe(false)
    expect(hasUserInstructions('  \n')).toBe(false)
    expect(briefRequest('s', '/p', '2.1.300', 'manual', ' ').user_instructions_present).toBe(false)
  })
})

describe('mergeInstructions', () => {
  test('the person comes first and verbatim, the brief after a blank line', async () => {
    expect(mergeInstructions('  mine  ', served)).toBe('  mine  \n\nKeep the ledger facts.')
  })

  test('a brief alone stands alone', async () => {
    expect(mergeInstructions(undefined, served)).toBe('Keep the ledger facts.')
    expect(mergeInstructions('', served)).toBe('Keep the ledger facts.')
  })

  test('an unserved or empty brief changes nothing, absent stays absent', async () => {
    expect(mergeInstructions(undefined, unservedBrief('cold'))).toBe(undefined)
    expect(mergeInstructions('mine', unservedBrief('timeout'))).toBe('mine')
    expect(mergeInstructions('mine', { ...served, instructions: '' })).toBe('mine')
  })
})
