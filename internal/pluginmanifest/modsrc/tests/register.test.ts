// Tests for the hooks module itself. They run under `claude plugin test` on the materialised
// plugin, where the kit loads hooks/register.ts as the plugin and answers every host call the
// module makes through the stubs each test registers. Every stub answers at once.
//
// The two tests at the end call the module's hello function directly with a stand-in host,
// because the kit's own host always has a process noun, and a surface without one has to be shown
// too.

import { describe, expect, test } from 'claude-code/testing'
import { hello } from '../hooks/register.ts'
import { BINARY_REL_PATH } from '../hooks/target.gen.ts'
import { REASONS, TIMEOUT_MS } from '../hooks/wire.gen.ts'

const ROOT = '/work/project'
const SESSION = 'sess-1'
const VERSION = '2.1.300'

const helloEnabled = '{"v":1,"ok":true,"enabled":true}\n'

type Seen = { argv: string[]; init: Record<string, unknown> }

/**
 * Registers the stubs for every host call the session.start hook makes besides the process run,
 * and returns the debug log lines the module writes.
 */
function stubSession(on: (name: string, stub: (...args: any[]) => unknown) => unknown): string[] {
  const logs: string[] = []
  on('session.start', () => ({ cwd: ROOT }))
  on('session.id', () => ({ value: SESSION }))
  on('session.root', () => ({ value: ROOT }))
  on('session.version', () => ({ value: { version: VERSION, base: VERSION } }))
  on('ui.log', ($: unknown, e: { text: string }) => {
    logs.push(e.text)
    return { value: undefined }
  })
  return logs
}

const start = { surface: 'terminal', isInteractive: true, cwd: ROOT } as const

describe('session.start', () => {
  test('runs qompack mod hello with the session values and passes the event on', async ($, on) => {
    const logs = stubSession(on)
    const seen: Seen[] = []
    on('process.run', ($, e) => {
      seen.push({ argv: [...e.argv], init: { ...e.init } })
      return { value: { exitCode: 0, stdout: helloEnabled, stderr: '' } }
    })

    expect(await $.session.start(start)).toEqual({ cwd: ROOT })

    expect(seen).toHaveLength(1)
    const { argv, init } = seen[0]
    expect(argv).toHaveLength(3)
    expect(argv[0].split('\\').join('/').endsWith('/' + BINARY_REL_PATH)).toBe(true)
    expect(argv.slice(1)).toEqual(['mod', 'hello'])
    expect(init.cwd).toBe(ROOT)
    expect(init.timeoutMs).toBe(TIMEOUT_MS)
    expect(init.env).toBeUndefined()
    expect(JSON.parse(String(init.stdin))).toEqual({ v: 1, session_id: SESSION, cwd: ROOT, host_version: VERSION })
    expect(logs).toEqual(['qompack mod hello: served, enabled'])
  })

  test('a served hello for a host below the minimum is logged as not enabled', async ($, on) => {
    const logs = stubSession(on)
    on('process.run', () => ({ value: { exitCode: 0, stdout: '{"v":1,"ok":true,"enabled":false}', stderr: '' } }))
    expect(await $.session.start(start)).toEqual({ cwd: ROOT })
    expect(logs).toEqual(['qompack mod hello: served, not enabled for this host'])
  })

  test('every unserved reason passes the event on', async ($, on) => {
    const logs = stubSession(on)
    const answers = REASONS.map((r) => JSON.stringify({ v: 1, ok: false, reason: r, detail: 'd-' + r, enabled: false }))
    on('process.run', () => ({ value: { exitCode: 0, stdout: answers.shift() ?? '', stderr: '' } }))
    for (const reason of REASONS) {
      expect(await $.session.start(start)).toEqual({ cwd: ROOT })
      expect(logs.pop()).toBe('qompack mod hello: ' + reason + ' (d-' + reason + ')')
    }
  })

  test('a failing binary passes the event on', async ($, on) => {
    const logs = stubSession(on)
    const results = [
      { exitCode: 1, stdout: '', stderr: 'boom' },
      { exitCode: 0, stdout: 'not json', stderr: '' },
      { exitCode: 0, stdout: '{"v":2,"ok":true,"enabled":true}', stderr: '' },
    ]
    on('process.run', () => ({ value: results.shift() }))
    for (const want of ['exit 1', 'response is not JSON', 'unsupported wire version 2']) {
      expect(await $.session.start(start)).toEqual({ cwd: ROOT })
      expect(logs.pop()).toBe('qompack mod hello: error (' + want + ')')
    }
  })

  test('a process run that cannot start or times out passes the event on', async ($, on) => {
    const logs = stubSession(on)
    const denials = ['spawn ENOENT', 'timed out after 2000 ms']
    on('process.run', () => ({ deny: denials.shift() }))
    for (let i = 0; i < 2; i++) {
      expect(await $.session.start(start)).toEqual({ cwd: ROOT })
      expect(logs.pop()).toMatch(/^qompack mod hello: error \(process: /)
    }
  })

  test('a surface whose process noun refuses the call passes the event on', async ($, on) => {
    const logs = stubSession(on)
    on('process.run', () => ({ deny: 'process is CLI only' }))
    expect(await $.session.start(start)).toEqual({ cwd: ROOT })
    expect(logs).toHaveLength(1)
    expect(logs[0]).toMatch(/^qompack mod hello: error \(process: /)
  })
})

/** A stand-in host with the given process noun, recording its debug log. */
function standIn(process: unknown, logs: string[]) {
  return {
    plugin: { name: 'qompack', root: '/plugins/qompack' },
    session: {
      id: async () => SESSION,
      root: async () => ROOT,
      version: async () => ({ version: VERSION }),
    },
    process,
    ui: { log: (text: string) => void logs.push(text) },
  }
}

describe('hello on a surface without the process noun', () => {
  test('an absent process noun is an unserved hello, never a throw', async () => {
    const logs: string[] = []
    const r = await hello(standIn(undefined, logs) as never)
    expect(r.ok).toBe(false)
    expect(r.reason).toBe('error')
    expect(r.enabled).toBe(false)
    expect(logs).toHaveLength(1)
    expect(logs[0]).toMatch(/^qompack mod hello: error \(process: /)
  })

  test('a host whose every call fails, logging included, still resolves', async () => {
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
    const r = await hello(broken as never)
    expect(r.ok).toBe(false)
    expect(r.reason).toBe('error')
  })
})
