// Tests for the module's session.compact hook. Like register.test.ts they run under
// `claude plugin test` on the materialised plugin, where the kit loads hooks/register.ts as the
// plugin. The kit's session.compact stub stands for the engine below the mod: it records the event
// the hook passed on with next(e) and answers the compaction. Every stub answers at once.
//
// The debug log lines each test checks show the hook ran to its end: the kit skips a hook that
// throws and hands the event on unchanged, which would look like a pass-through too.
//
// The tests at the end call the module's compaction handler directly with a stand-in host, because
// the kit's own host always has a process noun, and a surface without one has to be shown too.

import { describe, expect, test } from 'claude-code/testing'
import { briefRequest } from '../hooks/compact.ts'
import { encodeRequest, helloRequest } from '../hooks/format.ts'
import { steerCompaction } from '../hooks/register.ts'
import { BINARY_REL_PATH } from '../hooks/target.gen.ts'
import { BRIEF_MARKER, BRIEF_MAX_CHARS, REASONS, TIMEOUT_MS } from '../hooks/wire.gen.ts'

const ROOT = '/work/project'
const SESSION = 'sess-1'
const VERSION = '2.1.300'

const helloSteering = '{"v":1,"ok":true,"enabled":true,"steering":true}\n'
const helloSteeringOff = '{"v":1,"ok":true,"enabled":true,"steering":false}\n'
const helloBelowMin = '{"v":1,"ok":true,"enabled":false,"steering":false}\n'
const helloCold = '{"v":1,"ok":false,"reason":"cold","enabled":false,"steering":false}\n'

const BRIEF = BRIEF_MARKER + '\nKeep: the failing test is TestLedgerReplay; never edit db/.'
const briefOut = (instructions: string, factIds: string[] = ['inv_0a1b2c3d4e5f']) =>
  JSON.stringify({ v: 1, ok: true, instructions, fact_ids: factIds }) + '\n'

const MESSAGES = [{ role: 'user', text: 'fix the ledger replay', toolUses: [] }]
const COMPACTED = {
  messages: [{ role: 'assistant', text: 'summary of the work so far', toolUses: [] }],
  tokensBefore: 120000,
  tokensAfter: 9000,
}

const start = { surface: 'terminal', isInteractive: true, cwd: ROOT } as const

/** A session.compact input: a manual compaction of MESSAGES unless the fields given say otherwise. */
const compaction = (fields: Record<string, unknown> = {}) => ({ trigger: 'manual', messages: MESSAGES, ...fields }) as never

type On = (name: string, stub: (...args: any[]) => unknown) => unknown
type Answer = { value: { exitCode: number; stdout: string; stderr: string } } | { deny: string }
type Run = { verb: string; argv: string[]; init: Record<string, unknown> }

/** A process run's answer: the child exited with this status and printed this stdout. */
const exited = (stdout: string, exitCode = 0): Answer => ({ value: { exitCode, stdout, stderr: '' } })

/**
 * Registers the stubs for the session values and the debug log, and returns the log lines. The
 * session id is read through `id`, so a test can change it between events.
 */
function stubSession(on: On, id: () => string = () => SESSION): string[] {
  const logs: string[] = []
  on('session.start', () => ({ cwd: ROOT }))
  on('session.id', () => ({ value: id() }))
  on('session.root', () => ({ value: ROOT }))
  on('session.version', () => ({ value: { version: VERSION, base: VERSION } }))
  on('ui.log', ($: unknown, e: { text: string }) => {
    logs.push(e.text)
    return { value: undefined }
  })
  return logs
}

/**
 * Registers the process stub: each run takes the next answer queued for its verb, and is recorded.
 * A run with nothing queued for its verb is refused, and the test's check of the runs catches it.
 */
function stubProcess(on: On, answers: { hello?: Answer[]; brief?: Answer[] }): Run[] {
  const runs: Run[] = []
  on('process.run', ($: unknown, e: { argv: string[]; init: Record<string, unknown> }) => {
    const verb = e.argv[2]
    runs.push({ verb, argv: [...e.argv], init: { ...e.init } })
    const queue = verb === 'hello' ? answers.hello : verb === 'brief' ? answers.brief : undefined
    return queue?.shift() ?? { deny: 'no answer queued for ' + verb }
  })
  return runs
}

/** Registers the engine below the mod: it records each event passed on and answers `result`. */
function stubEngine(on: On, result: unknown = COMPACTED): Record<string, unknown>[] {
  const seen: Record<string, unknown>[] = []
  on('session.compact', ($: unknown, e: Record<string, unknown>) => {
    seen.push({ ...e })
    return result
  })
  return seen
}

const helloLine = 'qompack mod hello: served, enabled, steering on'
const servedLine = (text: string, ids = 1) => 'qompack mod brief: served, ' + text.length + ' chars, ' + ids + ' fact ids'

describe('session.compact steering', () => {
  test("a served brief follows the person's instructions, which stay byte for byte", async ($, on) => {
    const logs = stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(BRIEF))] })
    const seen = stubEngine(on)
    const mine = '  keep the db/ notes\n\twith a tab  \r\n'

    await $.session.start(start)
    expect(await $.session.compact(compaction({ instructions: mine }))).toEqual(COMPACTED)

    expect(seen).toHaveLength(1)
    expect(seen[0].instructions).toBe(mine + '\n\n' + BRIEF)
    expect(seen[0].trigger).toBe('manual')
    expect(seen[0].messages).toEqual(MESSAGES)
    expect(runs.map((r) => r.verb)).toEqual(['hello', 'brief'])
    const { argv, init } = runs[1]
    expect(argv).toHaveLength(3)
    expect(argv[0].split('\\').join('/').endsWith('/' + BINARY_REL_PATH)).toBe(true)
    expect(argv.slice(1)).toEqual(['mod', 'brief'])
    expect(init.cwd).toBe(ROOT)
    expect(init.timeoutMs).toBe(TIMEOUT_MS)
    expect(init.env).toBeUndefined()
    expect(init.stdin).toBe(encodeRequest(briefRequest(SESSION, ROOT, VERSION, 'manual', mine)))
    expect(JSON.parse(String(init.stdin))).toEqual({
      v: 1,
      session_id: SESSION,
      cwd: ROOT,
      host_version: VERSION,
      trigger: 'manual',
      user_instructions_present: true,
      max_chars: BRIEF_MAX_CHARS,
    })
    expect(logs).toEqual([helloLine, servedLine(BRIEF)])
  })

  test('a compaction with no instructions of its own gets the brief alone', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(BRIEF))] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ trigger: 'auto' }))

    expect(seen[0].instructions).toBe(BRIEF)
    const req = JSON.parse(String(runs[1].init.stdin))
    expect(req.trigger).toBe('auto')
    expect(req.user_instructions_present).toBe(false)
  })

  test('a precompute is briefed like any other trigger', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(BRIEF))] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ trigger: 'precompute', instructions: 'mine' }))

    expect(seen[0].trigger).toBe('precompute')
    expect(seen[0].instructions).toBe('mine\n\n' + BRIEF)
    expect(JSON.parse(String(runs[1].init.stdin)).trigger).toBe('precompute')
  })

  test("the person's text is never cut, so the merged text may pass max_chars", async ($, on) => {
    stubSession(on)
    const longest = BRIEF_MARKER + 'x'.repeat(BRIEF_MAX_CHARS - BRIEF_MARKER.length)
    stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(longest))] })
    const seen = stubEngine(on)
    const mine = 'u'.repeat(BRIEF_MAX_CHARS)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: mine }))

    expect(seen[0].instructions).toBe(mine + '\n\n' + longest)
    expect(String(seen[0].instructions).length).toBe(2 * BRIEF_MAX_CHARS + 2)
  })

  test('a skip from below is returned unchanged, briefed or not', async ($, on) => {
    stubSession(on)
    stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(BRIEF))] })
    const skipped = { skip: 'blocked by a PreCompact hook' }
    const seen = stubEngine(on, skipped)

    await $.session.start(start)
    expect(await $.session.compact(compaction({ instructions: 'mine' }))).toEqual(skipped)
    expect(await $.session.compact(compaction({ agentId: 'agent-7' }))).toEqual(skipped)

    expect(seen[0].instructions).toBe('mine\n\n' + BRIEF)
    expect(seen[1].instructions).toBeUndefined()
  })
})

describe('session.compact fails open', () => {
  test('every unserved reason, and a served empty brief, leave the instructions as they were', async ($, on) => {
    const logs = stubSession(on)
    const unserved = REASONS.map((r) =>
      exited(JSON.stringify({ v: 1, ok: false, reason: r, detail: 'd-' + r, instructions: '', fact_ids: [] })),
    )
    stubProcess(on, { hello: [exited(helloSteering)], brief: [...unserved, exited(briefOut('', []))] })
    const seen = stubEngine(on)

    await $.session.start(start)
    for (const reason of REASONS) {
      expect(await $.session.compact(compaction({ instructions: 'mine' }))).toEqual(COMPACTED)
      expect(seen.pop()?.instructions).toBe('mine')
      expect(logs.pop()).toBe('qompack mod brief: ' + reason + ' (d-' + reason + ')')
    }
    expect(await $.session.compact(compaction({ instructions: 'mine' }))).toEqual(COMPACTED)
    expect(seen.pop()?.instructions).toBe('mine')
    expect(logs.pop()).toBe('qompack mod brief: served empty')
  })

  test('a failing brief child leaves the instructions as they were, absent included', async ($, on) => {
    const logs = stubSession(on)
    const tooLong = BRIEF_MARKER + 'x'.repeat(BRIEF_MAX_CHARS - BRIEF_MARKER.length + 1)
    stubProcess(on, {
      hello: [exited(helloSteering)],
      brief: [
        exited('', 1),
        exited('not json'),
        exited('{"v":2,"ok":true,"instructions":"","fact_ids":[]}'),
        exited(briefOut('Keep it all')),
        exited(briefOut(tooLong)),
      ],
    })
    const seen = stubEngine(on)

    await $.session.start(start)
    for (const want of [
      'exit 1',
      'response is not JSON',
      'unsupported wire version 2',
      'instructions do not open with the brief marker',
      'instructions over max_chars',
    ]) {
      expect(await $.session.compact(compaction())).toEqual(COMPACTED)
      const passed = seen.pop() ?? {}
      expect('instructions' in passed).toBe(false)
      expect(logs.pop()).toBe('qompack mod brief: error (' + want + ')')
    }
  })

  test('a brief run that cannot start or times out leaves the instructions as they were', async ($, on) => {
    const logs = stubSession(on)
    stubProcess(on, {
      hello: [exited(helloSteering)],
      brief: [{ deny: 'spawn ENOENT' }, { deny: 'timed out after 2000 ms' }],
    })
    const seen = stubEngine(on)

    await $.session.start(start)
    for (let i = 0; i < 2; i++) {
      expect(await $.session.compact(compaction({ instructions: 'mine' }))).toEqual(COMPACTED)
      expect(seen.pop()?.instructions).toBe('mine')
      expect(logs.pop()).toMatch(/^qompack mod brief: error \(process: /)
    }
  })

  test('a surface whose process noun refuses every call passes the compaction on, no hello served', async ($, on) => {
    const logs = stubSession(on)
    on('process.run', () => ({ deny: 'process is CLI only' }))
    const seen = stubEngine(on)

    await $.session.start(start)
    expect(await $.session.compact(compaction({ instructions: 'mine' }))).toEqual(COMPACTED)

    expect(seen[0].instructions).toBe('mine')
    expect(logs).toHaveLength(2)
    for (const line of logs) expect(line).toMatch(/^qompack mod hello: error \(process: /)
  })
})

describe('session.compact passes on without a brief', () => {
  test('a compaction already carrying a brief makes no call at all', async ($, on) => {
    const logs = stubSession(on)
    const runs = stubProcess(on, {})
    const seen = stubEngine(on)
    const carried = 'mine\n\n' + BRIEF

    expect(await $.session.compact(compaction({ instructions: carried }))).toEqual(COMPACTED)

    expect(seen[0].instructions).toBe(carried)
    expect(runs).toEqual([])
    expect(logs).toEqual([])
  })

  test("a subagent's compaction makes no call at all", async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteering)] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ agentId: 'agent-7', instructions: 'mine' }))

    expect(seen[0].agentId).toBe('agent-7')
    expect(seen[0].instructions).toBe('mine')
    expect(runs.map((r) => r.verb)).toEqual(['hello'])
  })

  test('a hello that answered steering off sends no brief', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteeringOff)] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: 'mine' }))
    await $.session.compact(compaction())

    expect(seen[0].instructions).toBe('mine')
    expect(seen[1].instructions).toBeUndefined()
    expect(runs.map((r) => r.verb)).toEqual(['hello'])
  })

  test('a hello served for a host below the minimum sends no brief', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloBelowMin)] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: 'mine' }))

    expect(seen[0].instructions).toBe('mine')
    expect(runs.map((r) => r.verb)).toEqual(['hello'])
  })
})

describe('session.compact and the cached hello', () => {
  test('with no hello cached, the compaction runs hello before the brief', async ($, on) => {
    const logs = stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloSteering)], brief: [exited(briefOut(BRIEF))] })
    const seen = stubEngine(on)

    await $.session.compact(compaction({ instructions: 'mine' }))

    expect(runs.map((r) => r.verb)).toEqual(['hello', 'brief'])
    expect(runs[0].init.stdin).toBe(encodeRequest(helloRequest(SESSION, ROOT, VERSION)))
    expect(seen[0].instructions).toBe('mine\n\n' + BRIEF)
    expect(logs).toEqual([helloLine, servedLine(BRIEF)])
  })

  test('an unserved hello at session start is run again before the brief', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, {
      hello: [exited(helloCold), exited(helloSteering)],
      brief: [exited(briefOut(BRIEF))],
    })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: 'mine' }))

    expect(runs.map((r) => r.verb)).toEqual(['hello', 'hello', 'brief'])
    expect(seen[0].instructions).toBe('mine\n\n' + BRIEF)
  })

  test('a hello still unserved when run again sends no brief', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, { hello: [exited(helloCold), exited(helloCold)] })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: 'mine' }))

    expect(runs.map((r) => r.verb)).toEqual(['hello', 'hello'])
    expect(seen[0].instructions).toBe('mine')
  })

  test('a served hello is cached: later compactions brief without another hello', async ($, on) => {
    stubSession(on)
    const runs = stubProcess(on, {
      hello: [exited(helloSteering)],
      brief: [exited(briefOut(BRIEF)), exited(briefOut(BRIEF))],
    })
    const seen = stubEngine(on)

    await $.session.start(start)
    await $.session.compact(compaction({ instructions: 'mine' }))
    await $.session.compact(compaction({ trigger: 'auto' }))

    expect(runs.map((r) => r.verb)).toEqual(['hello', 'brief', 'brief'])
    expect(seen.map((e) => e.instructions)).toEqual(['mine\n\n' + BRIEF, BRIEF])
  })

  test('a session id that changed since the hello runs hello again under the new id', async ($, on) => {
    let id = 'sess-1'
    stubSession(on, () => id)
    const runs = stubProcess(on, {
      hello: [exited(helloSteering), exited(helloSteering)],
      brief: [exited(briefOut(BRIEF))],
    })
    const seen = stubEngine(on)

    await $.session.start(start)
    id = 'sess-2'
    await $.session.compact(compaction({ instructions: 'mine' }))

    expect(runs.map((r) => r.verb)).toEqual(['hello', 'hello', 'brief'])
    expect(JSON.parse(String(runs[0].init.stdin)).session_id).toBe('sess-1')
    expect(JSON.parse(String(runs[1].init.stdin)).session_id).toBe('sess-2')
    expect(JSON.parse(String(runs[2].init.stdin)).session_id).toBe('sess-2')
    expect(seen[0].instructions).toBe('mine\n\n' + BRIEF)
  })
})

/**
 * A stand-in host with the given process noun, recording its debug log. Each test gives its own
 * session id, so no hello another test cached can stand for this one's.
 */
function standIn(process: unknown, logs: string[], session: string) {
  return {
    plugin: { name: 'qompack', root: '/plugins/qompack' },
    session: {
      id: async () => session,
      root: async () => ROOT,
      version: async () => ({ version: VERSION }),
    },
    process,
    ui: { log: (text: string) => void logs.push(text) },
  }
}

/** A stand-in for the engine below the mod: it records each event passed on. */
function standInNext(result: unknown = COMPACTED) {
  const passed: unknown[] = []
  const next = async (e: unknown) => {
    passed.push(e)
    return result
  }
  return { passed, next }
}

describe('the compaction hook on a surface without the process noun', () => {
  test('an absent process noun passes the event on unchanged, no hello served', async () => {
    const logs: string[] = []
    const { passed, next } = standInNext()
    const e = compaction({ instructions: 'mine' })

    const r = await steerCompaction(standIn(undefined, logs, 'standin-absent') as never, e, next as never)

    expect(r).toEqual(COMPACTED)
    expect(passed).toHaveLength(1)
    expect(passed[0]).toBe(e)
    expect(logs).toHaveLength(1)
    expect(logs[0]).toMatch(/^qompack mod hello: error \(process: /)
  })

  test('a process noun that rejects the call passes the event on unchanged, no brief sent', async () => {
    const logs: string[] = []
    const calls: string[][] = []
    const process = {
      run: async (argv: string[]) => {
        calls.push(argv)
        throw new Error('process is CLI only')
      },
    }
    const { passed, next } = standInNext()
    const e = compaction({ instructions: 'mine' })

    const r = await steerCompaction(standIn(process, logs, 'standin-rejects') as never, e, next as never)

    expect(r).toEqual(COMPACTED)
    expect(passed).toEqual([e])
    expect(calls.map((argv) => argv[2])).toEqual(['hello'])
  })

  test('a host whose every call fails, logging included, still passes the event on once', async () => {
    const broken = {
      plugin: { name: 'qompack', root: '/plugins/qompack' },
      session: {
        id: async () => {
          throw new Error('no session')
        },
      },
      ui: {
        log: () => {
          throw new Error('no log')
        },
      },
    }
    const { passed, next } = standInNext()
    const e = compaction({ instructions: 'mine' })

    expect(await steerCompaction(broken as never, e, next as never)).toEqual(COMPACTED)
    expect(passed).toEqual([e])
  })

  test('session values that cannot be read pass the event on and leave a debug line', async () => {
    const logs: string[] = []
    const host = {
      ...standIn(undefined, logs, 'standin-no-session'),
      session: {
        id: async () => {
          throw new Error('no session')
        },
        root: async () => ROOT,
        version: async () => ({ version: VERSION }),
      },
    }
    const { passed, next } = standInNext()
    const e = compaction({ instructions: 'mine' })

    expect(await steerCompaction(host as never, e, next as never)).toEqual(COMPACTED)

    expect(passed).toEqual([e])
    expect(logs).toEqual(['qompack mod brief: error (session: no session)'])
  })

  test('a debug log that throws loses only its lines: a served brief still joins the instructions', async () => {
    const tried: string[] = []
    const answers: Record<string, string> = { hello: helloSteering, brief: briefOut(BRIEF) }
    const host = {
      ...standIn({ run: async (argv: string[]) => ({ exitCode: 0, stdout: answers[argv[2]], stderr: '' }) }, [], 'standin-log-throws'),
      ui: {
        log: (text: string) => {
          tried.push(text)
          throw new Error('no log')
        },
      },
    }
    const { passed, next } = standInNext()

    await steerCompaction(host as never, compaction({ instructions: 'mine' }), next as never)

    expect(tried).toEqual([helloLine, servedLine(BRIEF)])
    expect(passed).toHaveLength(1)
    expect((passed[0] as { instructions?: string }).instructions).toBe('mine\n\n' + BRIEF)
  })

  test("the engine's own failure is not retried: next runs once and its rejection stands", async () => {
    const logs: string[] = []
    let calls = 0
    const next = async () => {
      calls++
      throw new Error('summarizer failed')
    }
    let message = ''
    try {
      await steerCompaction(standIn(undefined, logs, 'standin-next') as never, compaction(), next as never)
    } catch (err) {
      message = err instanceof Error ? err.message : String(err)
    }
    expect(message).toBe('summarizer failed')
    expect(calls).toBe(1)
  })
})
