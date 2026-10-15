// Tests for the bridge's pure half. They run under `claude plugin test` on the materialised plugin,
// where the mod sources sit in hooks/ and these tests in tests/, hence the ../hooks/ imports.
//
// The JSON strings below are internal/modwire's response goldens, verbatim; a Go test fails when
// one of those goldens changes and this file does not.

import { describe, expect, test } from 'claude-code/testing'
import {
  binaryPath,
  decodeBrief,
  decodeHello,
  encodeRequest,
  runFailureDetail,
  unservedBrief,
  verbArgv,
} from '../hooks/format.ts'
import { BRIEF_MAX_CHARS, MAX_FACT_IDS, MAX_MESSAGE_BYTES } from '../hooks/wire.gen.ts'

const helloEnabled = '{"v":1,"ok":true,"enabled":true}'
const helloRefused = '{"v":1,"ok":false,"reason":"refused","enabled":false}'
const helloBelowMin = '{"v":1,"ok":true,"enabled":false}'
const briefServed = '{"v":1,"ok":true,"instructions":"Keep: the failing test is TestLedgerReplay; never edit db/.","fact_ids":["neg_7c1d2e3f4a5b","pin_0a1b2c3d4e5f"]}'
const briefCold = '{"v":1,"ok":false,"reason":"cold","instructions":"","fact_ids":[]}'
const unsupportedVersion = '{"v":1,"ok":false,"reason":"error","detail":"modwire: unsupported wire version 2 (this side speaks 1)"}'

describe('binaryPath', () => {
  test('joins with the separator the root uses', async () => {
    expect(binaryPath('/home/u/.claude/plugins/qompack', 'bin/qompack')).toBe('/home/u/.claude/plugins/qompack/bin/qompack')
    expect(binaryPath('C:\\Users\\u\\plugins\\qompack', 'bin/qompack.exe')).toBe('C:\\Users\\u\\plugins\\qompack\\bin\\qompack.exe')
    expect(binaryPath('C:/Users/u/plugins/qompack', 'bin/qompack.exe')).toBe('C:/Users/u/plugins/qompack/bin/qompack.exe')
  })

  test('a trailing separator is not doubled', async () => {
    expect(binaryPath('/p/', 'bin/qompack')).toBe('/p/bin/qompack')
    expect(binaryPath('C:\\p\\', 'bin/qompack.exe')).toBe('C:\\p\\bin\\qompack.exe')
  })

  test('the argv is the binary then mod and the verb', async () => {
    const argv = verbArgv('/p', 'brief')
    expect(argv).toHaveLength(3)
    expect(argv[1]).toBe('mod')
    expect(argv[2]).toBe('brief')
    expect(argv[0].startsWith('/p/bin/qompack')).toBe(true)
  })
})

describe('encodeRequest', () => {
  test('is one JSON object and a newline', async () => {
    const s = encodeRequest({ v: 1, session_id: 's', cwd: '/p', host_version: '2.1.300' })
    expect(s).toBe('{"v":1,"session_id":"s","cwd":"/p","host_version":"2.1.300"}\n')
  })
})

describe('decodeHello', () => {
  test('reads the Go goldens', async () => {
    expect(decodeHello(0, helloEnabled + '\n')).toEqual({ v: 1, ok: true, enabled: true })
    expect(decodeHello(0, helloRefused + '\n')).toEqual({ v: 1, ok: false, reason: 'refused', enabled: false })
  })

  test('a served hello may still say the features are off for this host', async () => {
    expect(decodeHello(0, helloBelowMin)).toEqual({ v: 1, ok: true, enabled: false })
  })

  test('reads a bare error object as unserved', async () => {
    const r = decodeHello(0, unsupportedVersion)
    expect(r.ok).toBe(false)
    expect(r.reason).toBe('error')
    expect(r.enabled).toBe(false)
  })

  test('fails open on anything else', async () => {
    for (const [code, out] of [
      [1, helloEnabled],
      [0, ''],
      [0, 'not json'],
      [0, '[1]'],
      [0, '{"v":2,"ok":true,"enabled":true}'],
      [0, '{"v":1,"enabled":true}'],
      [0, '{"v":1,"ok":true,"reason":"cold","enabled":true}'],
      [0, '{"v":1,"ok":false,"reason":"sleepy","enabled":false}'],
      [0, '{"v":1,"ok":true}'],
    ] as const) {
      const r = decodeHello(code, out)
      expect(r.ok).toBe(false)
      expect(r.reason).toBe('error')
      expect(r.enabled).toBe(false)
    }
  })

  test('drops fields the contract does not name', async () => {
    expect(decodeHello(0, '{"v":1,"ok":true,"enabled":false,"extra":1}')).toEqual({ v: 1, ok: true, enabled: false })
  })
})

describe('decodeBrief', () => {
  test('reads the Go goldens', async () => {
    expect(decodeBrief(0, briefServed + '\n', BRIEF_MAX_CHARS)).toEqual({
      v: 1,
      ok: true,
      instructions: 'Keep: the failing test is TestLedgerReplay; never edit db/.',
      fact_ids: ['neg_7c1d2e3f4a5b', 'pin_0a1b2c3d4e5f'],
    })
    expect(decodeBrief(0, briefCold, BRIEF_MAX_CHARS)).toEqual(unservedBrief('cold'))
  })

  test('reads a bare error object as unserved, keeping its detail', async () => {
    const r = decodeBrief(0, unsupportedVersion, BRIEF_MAX_CHARS)
    expect(r).toEqual(unservedBrief('error', 'modwire: unsupported wire version 2 (this side speaks 1)'))
  })

  test('accepts the fact id shapes the Go side emits', async () => {
    const ids = ['dec_0123456789ab', 'elim_0123456789ab', 'sha256:' + '0f'.repeat(32), 'toolu_01AB', 'intent:original', 'f'.repeat(128)]
    const out = JSON.stringify({ v: 1, ok: true, instructions: 'x', fact_ids: ids })
    expect(decodeBrief(0, out, BRIEF_MAX_CHARS).fact_ids).toEqual(ids)
  })

  test('holds the instructions to max_chars in String length', async () => {
    const text = 'x'.repeat(10)
    const out = JSON.stringify({ v: 1, ok: true, instructions: text, fact_ids: [] })
    expect(decodeBrief(0, out, 10).ok).toBe(true)
    expect(decodeBrief(0, out, 9).reason).toBe('error')
  })

  test('fails open on anything else', async () => {
    const big = JSON.stringify({ v: 1, ok: true, instructions: 'y'.repeat(MAX_MESSAGE_BYTES), fact_ids: [] })
    const ids = JSON.stringify({ v: 1, ok: true, instructions: '', fact_ids: Array(MAX_FACT_IDS + 1).fill('f') })
    for (const [code, out] of [
      [2, briefServed],
      [0, 'garbage'],
      [0, big],
      [0, ids],
      [0, '{"v":1,"ok":true,"fact_ids":[]}'],
      [0, '{"v":1,"ok":true,"instructions":"a"}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":[""]}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":[7]}'],
      [0, '{"v":1,"ok":false,"reason":"cold","detail":"' + 'd'.repeat(257) + '"}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":["a<b"]}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":["a b"]}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":["é"]}'],
      [0, '{"v":1,"ok":true,"instructions":"a","fact_ids":["' + 'f'.repeat(129) + '"]}'],
      [0, '{"v":1,"ok":false,"reason":"cold","instructions":"leak","fact_ids":[]}'],
      [0, '{"v":"1","ok":true,"instructions":"a","fact_ids":[]}'],
    ] as const) {
      const r = decodeBrief(code, out, BRIEF_MAX_CHARS)
      expect(r.ok).toBe(false)
      expect(r.reason).toBe('error')
      expect(r.instructions).toBe('')
      expect(r.fact_ids).toEqual([])
    }
  })
})

describe('runFailureDetail', () => {
  test('describes a rejection without throwing', async () => {
    expect(runFailureDetail(new Error('spawn ENOENT'))).toBe('process: spawn ENOENT')
    expect(runFailureDetail('timeout')).toBe('process: timeout')
    expect(runFailureDetail(new Error('z'.repeat(500))).length).toBe('process: '.length + 200)
  })
})
