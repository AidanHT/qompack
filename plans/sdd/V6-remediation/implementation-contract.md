# V6 remediation contract — 2026-09-21

Authorized by the user's request to fix all non-human V6 work. Base: `verify/v6` at
`80ee0159a684df913f713eed1ca7766c7661c10e`. The original V6 report and artifacts are historical
observations and remain unchanged. New runs use distinct artifact directories. Human UAT is
authorized for later user participation but has not occurred; release/publish remains separate.

The coordinator owns shared authority, schema, daemon wiring, CLI integration and commits.
Children use measured `claude-opus-4-8` routes, no nesting, at most three active. The actual
coordinator is Codex, not a claimed Fable session.

## Retrieval authority correction

Keep frozen `Store`, `Root` and `ToolUseRecord` wire/interface contracts unchanged. Add a narrow
optional store provenance reader over existing root/tool/file-history indices. An address is
not authority: resolve every root/chunk hash to its recorded origins before materializing bytes.
All known associated path constraints must hold; a second permissive origin must not launder a
denied origin. Missing, unknown or bounded-incomplete provenance fails closed. Cancellation and
store failure remain unavailable, never a confirmed absence.

An empty path from a file-producing Read/Edit/Write capture cannot authorize retrieval. Existing
tool provenance distinguishes these from known genuinely pathless producers without modifying
old record bytes. Unknown producers do not gain authority from an empty path. File containment
checks must run for ID, root hash, chunk hash and recall before any externally visible preview.
Pathless output remains untrusted archive data, with current redaction still required.

Current host permission decisions are a separate boundary. Do not claim that reconstructing some
settings files or checking containment reproduces effective host permission, including live
revocations, managed restrictions and invocation flags. The supported integration decision and
its target evidence must be recorded before claiming that gate passed.

## Publication and recovery correction

Production startup accounting may report incomplete publications but may not delete or silently
repair them. Deliberately unpublished prompts are not failed tool publications. Scans are bounded,
and unreadable/unknown/truncated coverage is explicit. Retain original objects and capture evidence.

An operator maintenance flow must acquire writer exclusion, use a consistent verified backup and
stable frontier, preserve post-backup writes as evidence, verify restored reader compatibility and
remain recoverable after interruption. Do not open the unrelated legacy-import/cutover gate merely
to make backup commands callable. Shared durable-data changes return to the coordinator first.

## Validation allocation

Focused changed-package tests and the new real-capture V6 regressions first. One automated release
matrix per immutable corrected artifact follows when these pass. No historical PASS or same-source
version-stamped upgrade proves old-release compatibility. Cross-platform unavailable runners,
human interactions and any provider trial access constraints retain explicit incomplete states.
