# Live-check comparison

- a: Claude Code 2.1.280, run <run a>, suite qompack-live-check-test (sha256 e3bb5c9a8d8d), tier quick
- b: Claude Code 2.1.280, run <run b>, suite qompack-live-check-test (sha256 e3bb5c9a8d8d), tier quick

| check | a: Claude Code 2.1.280, run <run a> | b: Claude Code 2.1.280, run <run b> | change |
|---|---|---|---|
| compact-recall | pass | fail | now fails |
| plugin-loads | pass | pass | same |
