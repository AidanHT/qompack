// Tests for the compaction half's pure functions. Like format.test.ts they run on the materialised
// plugin, hence the ../hooks/ imports.

import { describe, expect, test } from 'claude-code/testing'
import { briefRequest, hasUserInstructions, mergeInstructions } from '../hooks/compact.ts'
import { unservedBrief } from '../hooks/format.ts'
import { BRIEF_MAX_CHARS } from '../hooks/wire.gen.ts'

const served = { v: 1, ok: true, instructions: 'Keep the ledger facts.', fact_ids: ['f1'] }

describe('briefRequest', () => {
  test('carries the session values, the default cap and only whether instructions exist', async () => {
    expect(briefRequest('s-1', '/p', 'auto', 'keep the db notes')).toEqual({
      v: 1,
      session_id: 's-1',
      cwd: '/p',
      trigger: 'auto',
      user_instructions_present: true,
      max_chars: BRIEF_MAX_CHARS,
    })
    expect(JSON.stringify(briefRequest('s-1', '/p', 'auto', 'keep the db notes'))).not.toContain('db notes')
  })

  test('blank instructions are no instructions', async () => {
    expect(hasUserInstructions(undefined)).toBe(false)
    expect(hasUserInstructions('')).toBe(false)
    expect(hasUserInstructions('  \n')).toBe(false)
    expect(briefRequest('s', '/p', 'manual', ' ').user_instructions_present).toBe(false)
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
