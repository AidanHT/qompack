// Qompack's hooks module, and the only mod source that touches the host's `$`.
//
// The host reads this file statically before it loads it: every `$` call must be written out in
// full, and `$` may be passed only to a function declared at the top level of this same file.
// Keeping every host call here, and every other source (compact.ts, format.ts and the two
// generated files) pure, satisfies that rule whichever way the host reads a call made through an
// imported helper, and keeps the full list of host calls in one place the validator reports.
//
// The mod is fail-open by design: a hook that cannot reach the binary passes the event on
// unchanged, so the classic hooks' behaviour is the floor. Hooks are registered here as each verb
// is wired; until then the module loads and does nothing.

import type { Register } from 'claude-code'

export const register: Register = () => {}
