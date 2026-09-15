# Commit 3 evidence — archive trust and privacy boundaries

What `test/security` established on this host, what it could not establish here, and what it
returned as a defect. SP17-M7-03's acceptance row — "Denied/symlink/secret/archive/decompression
tests leak no forbidden preview or expansion and stay bounded" — is recorded here.

Every claim below is backed by a JSON record in `commit3-security-windows-amd64/`, written by the
test that made it, and **the tables in §§2-5 are taken from those records rather than written beside
them** — §4's surface table is generated from them by script, the rest are transcribed.
`INDEX.json` in that directory names the records one run produced: a record file is not
self-dating, so the collecting run prunes the directory first and states afterwards exactly what it
wrote. **No row says verified without a record**, and a row nobody ran says `skipped` with
the reason it was skipped rather than nothing at all.

## 1. What was executed, and against what

| | |
| --- | --- |
| host | Windows 11 (10.0.26200), windows/amd64 |
| Go | go1.26.6 |
| bundle | `qompack-plugin-v0.2.0-604-g62a5975-windows-amd64`, assembled once per test binary by `go run ./tools/devtool bundle --target windows/amd64 --out <tempdir>` |
| bundle version string | `v0.2.0-604-g62a5975-dirty` |
| `bin/qompack.exe` sha256 | `b0b0bf83152d04485ca5f5648b896cfd015957467479b1947e157dbf146e4e9a` |
| suite | `go test -count=1 -timeout=30m ./test/security/` — 15 tests, 40 records; two consecutive runs at **39.1 s** and **37.2 s** wall, and the collecting run this evidence set comes from at 37.6 s |

The `-dirty` suffix and the `g62a5975` commit are correct and load-bearing: the bundle was assembled
from the worktree while this commit's own files were still uncommitted, so `git describe` reported
the parent commit plus `-dirty`. `62a5975` is this branch's tip before the commit and is permanently
reachable. Nothing in this evidence set is a release artifact.

Every case drives the **assembled bundle's** `bin/qompack.exe`, never a `go build` of
`./cmd/qompack`. Retrieval is driven through a real `qompack mcp` child launched the way
`plugin/.mcp.json` instructs a host to launch it — the bundled executable, the `mcp` subcommand, no
shell, because the presence of `args` makes that entry exec form — speaking JSON-RPC over its stdio
to a real daemon. Child processes are started with every ambient `QOMPACK_*` and `CLAUDE_*`
variable stripped, so a developer's own installed plugin or exported configuration cannot silently
change what the matrix measures.

Every child's three standard streams are real files rather than pipes, and every wait in the package
is bounded and says on expiry what it was waiting for. That is not incidental: the first version of
this package hung past a thirty-minute timeout on an independent reviewer's machine and left a
daemon alive for over an hour, because `exec.Cmd` will not return from `Wait` while any process
holds an inherited pipe — and a hook spawns a detached daemon. A file descriptor has no copier to
wait on, so `Wait` now ends when the process does.

**40 records: 32 verified, 7 failed, 1 skipped.** The seven failures are RETURNED findings (§6), not
broken builds; the suite is green. Which failures also fail their Go test is a deliberate split
described in `test/security/security.go`'s package comment: a LEAK fails the test, a POLICY
divergence is recorded and the case goes on. No leak was found on this host.

## 2. Archive trust — denied paths, escapes, and command replay

| # | record | what was measured | outcome |
| --- | --- | --- | --- |
| 1 | `archive_trust_parent_directory_replaced_by_link_outside` | after capture, the file's parent directory is replaced by a link pointing outside the root; `recall`, `expand` and `re_read` are then asked for it | **failed** — finding S-1 |
| 2 | `archive_trust_captured_file_replaced_by_link_outside` | the same, with the captured FILE itself replaced by a link outside the root | **skipped** — no link mechanism for a file on this host |
| 3 | `archive_trust_lexical_parent_escape_in_a_stored_record` | a hand-written `tool_use` record whose stored path is `../outside/leak.txt` | **verified** |
| 4 | `archive_trust_re_read_never_reads_the_live_disk` | the captured file is rewritten, then deleted; `re_read` is asked for it both times | **verified** |
| 5 | `archive_trust_stored_text_is_never_executed` | prompt-injection-shaped content and a shell command are captured, then retrieved | **verified** |
| 6 | `archive_trust_mcp_imports_no_exec` | the transitive import set of `internal/mcp` | **verified** |

Rows 1-3 each assert four things that hold regardless of the envelope shape, and all four held in
every case that ran: no response carried a byte of the file outside the project root; no response
echoed the absolute outside path; no refusal echoed the escaping path back to the caller (a refusal
must not become an oracle for what exists outside the project); and every object present before the
retrieval was still present, byte for byte, afterwards.

What rows 1 and 3 DISAGREE about is the envelope, and that disagreement is finding S-1. Transcribed
from the records' own shape column:

| escape | `recall` | `expand` | `re_read` |
| --- | --- | --- | --- |
| parent directory linked outside (row 1) | `hits(1)` | `content(24636 bytes)` | `content(24636 bytes)` |
| lexical `../` in a stored record (row 3) | 0 hits | `denied(found=false)` | `tool_error` |

Row 3 is the shape `internal/mcp/authorize.go` describes. Row 1 is not, and §6 says why. The two
shapes are diagnosed separately in the records rather than sharing one sentence, because they fail
differently: `paths.Norm` rejects a lexical `../` outright, so only the link case can reach a served
response through a passing authorization check.

Row 2 is skipped and is **not** a pass. `os.Symlink` needs `SeCreateSymbolicLinkPrivilege` or
Developer Mode and this host has neither, and the no-privilege alternative — an NTFS directory
junction, which row 1 does use and the record names — exists only for directories. The
file-replaced-by-a-link shape is therefore unmeasured on windows/amd64 and is named again in §7.
A junction and a symlink also differ in what `Lstat` reports, so a future `Lstat`-based fix must be
tested against both rather than against whichever one the fixing host can create.

Row 4 is T13-HISTORY end to end: `re_read` returned the ARCHIVED bytes after the working-tree file
had been replaced wholesale, and again after it had been deleted. The replacement marker never
appeared in either response, so the tool is reading history rather than silently falling back to a
live read of a path it happens to be able to open.

Row 5 captures content that addresses the model directly, claims authority and asks for a tool call,
plus a `Bash` capture whose arguments are `echo owned > <path outside the project>`. **Both** captures
are waited for in the index before anything is retrieved — without that, an unindexed Bash capture
would make `expand` answer `found:false` and the case would claim a capture that never happened.
Every retrieval that carried archived text — `recall`, `expand` and `re_read` — set
`_meta.qompack.untrusted`; the content came back verbatim (the assertion is on a distinctive
fragment of the injection, so span narrowing cannot make it pass by accident); and the sentinel file
was never created, checked after the daemon had finished unwinding so a deferred execution would
still have been caught.

Row 6 asks the build graph rather than the process: 155 transitive packages, none of them `os/exec`.
The retrieval layer has no mechanism to run a program at all. The **no-network** half of the same
question is owned by `test/guards`' `TestGuard_NoNetworkImports` and is cited rather than
duplicated.

## 3. Decode and size bounds

| # | record | what was measured | outcome |
| --- | --- | --- | --- |
| 7 | `bounds_damaged_zstd_frame` | a zstd frame with its middle overwritten | **verified** |
| 8 | `bounds_truncated_object` | a zstd frame cut in half | **verified** |
| 9 | `bounds_physical_size_past_the_read_bound` | an object file larger than `encodedObjectLimit()` | **verified** |
| 10 | `bounds_decompression_bomb` | a valid frame expanding past store's 64 MiB `maxDecodedSize` | **verified** |
| 11 | `bounds_content_hash_mismatch` | a valid frame of the right length whose plaintext hashes elsewhere | **verified** |
| 12 | `bounds_retrieval_and_checkpoint_survive_corruption` | the daemon, the MCP server and `checkpoint` after all five | **verified** |
| 13 | `bounds_corrupt_object_envelope` | how a quarantined object reaches the model, through both address forms | **failed** — finding S-3 |
| 14 | `bounds_hook_capture_cap` | a 6 MiB `tool_response` against the hard 4 MiB capture cap | **verified** |
| 15 | `bounds_config_cannot_raise_the_capture_cap` | `runtime.hotPath.maxPayloadBytes` set to 64 MiB | **failed** — finding S-2 |
| 16 | `bounds_mcp_max_response_bytes` | a full `expand` against `runtime.mcp.maxResponseBytes` of 4096 | **verified** |

Rows 7-11 each assert the same two things and each record carries the store's own refusal text: the
read fails with `core.ErrNotFound`, and the bytes are moved to `.qompack/tmp/quarantine/`. Row 11 is
worth reading closely, because the first version of it measured the wrong thing: substituting
different bytes of a different LENGTH is refused by the indexed-length check, and the content-address
verification — the check that actually defends against a swapped object — is never reached. The
committed version decodes the frame, flips one byte, and re-encodes, so the length is identical and
the refusal is forced through `core.HashBytes`.

Row 10 is a real bomb rather than an assertion about one: 80 MiB of plaintext compressed to a few
kilobytes, and the test itself asserts `store.Decode` refuses it before the frame is ever written to
disk, so "the decoder did not allocate what the frame claims" is measured rather than assumed.

Row 12 is the §12.3 continuation rule: after five hostile objects the daemon still held its lock,
the MCP server still expanded a healthy control object, and the `checkpoint` hook still exited 0
with a host-parseable document. No corrupt object was ever reported `found`, through either address
form.

Row 13 is the envelope itself, and it is a separate record on purpose: a measured divergence with an
owner is a `failed` row, and burying one in the detail line of a `verified` row is how a finding
stops being counted. Both address forms are driven — by `tool_use_id` and by bare chunk hash — and
they diverge differently. See finding S-3.

Row 14 plants its marker 64 KiB into a 6 MiB payload, far past the 4 KiB the refusal retains, so its
absence everywhere under `.qompack/` measures the prefix bound rather than measuring whether
anything was stored at all. The hook still exited 0 with a parseable document.

Row 16: with `runtime.mcp.maxResponseBytes` at its minimum, a full `expand` of a 24 636-byte object
returned exactly 4096 bytes, marked truncated, with a `next_span` cursor. "Full" means "do not
narrow to the matching hunk", never "unbounded".

## 4. Privacy — every durable surface, swept

One session carrying all ten built-in credential families plus an operator's own
`runtime.redact.patterns` rule was driven through the packaged binary: a credential-bearing tool
result, a credential-bearing tool ARGUMENT, a credential-bearing prompt, a `PreCompact` checkpoint
and a `SessionEnd` flush. A real backup was then taken through `store.Migrator.TakeBackup`, a real
export through `eval.Import`, and every file the product wrote was read back — with `objects/`
**decompressed first**, because a sweep that scanned zstd frames would report a clean bill of health
it never earned.

Each surface is scanned for every planted credential's literal value AND for every window one byte
longer than `secretRunLimit` (8), which is what proves nothing reassembled secret material across a
placeholder or a canonicalization boundary. Each file is scanned twice: as written, and with JSON
string escapes undone, so a credential that spans a newline inside an NDJSON line is visible. A file
that is found but cannot be READ is recorded as skipped rather than dropped, and the sweep refuses
to judge any surface holding one.

Every column below is GENERATED from `commit3-security-windows-amd64/privacy-surface-sweep.json`
and that directory's per-surface `privacy_surface_*.json` records — `files` and `bytes swept` from
the artifact's `surfaces`, `redaction placeholders` from its `redaction_placeholders`, `outcome`
from each record — rather than typed beside them. It is generated rather than transcribed because
an earlier hand transcription of this table carried six stale byte counts, and a number nobody can
regenerate is a number nobody can check. The artifact also records every swept file BY NAME, so a
claim about a particular file can be checked against the evidence rather than inferred from a count.

**The byte counts are the committed run's and are not expected to reproduce exactly.** The swept
files carry pids, timestamps and durations — the spool file is even named for the client's pid — so
an identical rerun sweeps the same surfaces at slightly different sizes. A rerun that changes the
records changes this table; regenerate it from the new records rather than reconciling the two.

The `unreadable` column is zero for every surface by construction rather than by luck, which is why
it has no field of its own in the artifact: `requireNothingSkipped` fails the sweep — before any
record is written — when any swept file cannot be read, so a record set exists at all only for a run
whose count was zero.

| surface | files | bytes swept | redaction placeholders | unreadable | outcome |
| --- | --- | --- | --- | --- | --- |
| `objects` | 9 | 21 775 | 36 | 0 | **verified** |
| `index` | 6 | 26 746 | 2 | 0 | **verified** |
| `records` | 4 | 10 207 | 0 | 0 | **verified** |
| `checkpoints` | 2 | 1 163 | 1 | 0 | **verified** |
| `pins` | 1 | 3 | 0 | 0 | **verified** |
| `spool` | 1 | 58 | 0 | 0 | **verified** |
| `state` | 19 | 11 343 | 1 | 0 | **verified** |
| `metrics` | 1 | 2 090 | 0 | 0 | **verified** |
| `logs` | 1 | 848 | 0 | 0 | **verified** |
| `dag` | 1 | 3 355 | 0 | 0 | **verified** |
| `sketches` | 3 | 68 648 | 0 | 0 | **verified** |
| `run` | 1 | 61 | 0 | 0 | **verified** |
| `dot-root` (`.gitignore`, `config.json`) | 2 | 135 | 0 | 0 | **verified** |
| `backup` (tree + manifest) | 49 | 150 157 | 40 | 0 | **verified** |
| `mcp-retrieval` (7 tool responses) | 7 | 10 075 | 23 | 0 | **verified** |
| `eval-export` | 1 | 1 457 | 0 | 0 | **failed** — finding S-4 |

**The `logs` row is one file, and it is the day log.** That is stated rather than glossed, because an
earlier draft of this document claimed "logs/ including LOUD.log swept and clean" on the strength of
exactly this count — and `LOUD.log` is written only when something Louds, which nothing in this
session did. The named claim is carried by row 19 below instead, which arranges a Loud and then
checks the file by name.

A placeholder count is reported rather than a verdict, deliberately: a positive count is evidence
that redaction demonstrably ran on that surface, while a zero is evidence of nothing on its own. The
two are separated by the non-vacuity check the sweep performs before judging anything — `objects`,
`index` and `backup` must each hold at least one file, or the whole result is refused as vacuous.

The `backup` surface is the `.qompack/backup/` subtree, which the split walk already covers; it is
not passed a second time as its own root, because a double-counted tree reports every hit twice.

| # | record | what was measured | outcome |
| --- | --- | --- | --- |
| 17 | `privacy_backup_take` | a consistent 48-file backup through the real `store.Migrator.TakeBackup` | **verified** |
| 18 | `privacy_eval_export` | a real `eval.Import` with the default redaction: 1 session, 18 spans replaced | **verified** |
| 19 | `privacy_loud_log_carries_no_secret` | a Loud ABOUT a credential-bearing object, and the `LOUD.log` it writes | **verified** |
| 20 | `privacy_assignment_rule_underscored_keys` | whether the §5.22a assignment rule reaches an `_authToken=` key | **failed** — finding S-5 |

Row 17 needs one qualification stated plainly, and the record states it: **the legacy-import gate
ships closed**, so no shipped build can reach `TakeBackup` at all. The test supplies a passing gate
value to `store.NewMigrator` and drives the REAL backup code — it does not fork a copy of the walk,
because a fork would measure the fork. What this row establishes is that IF a backup is ever taken,
its tree carries no credential; it does not establish that backups happen today, because they
cannot.

Row 19 is the repaired form of the `LOUD.log` claim, and it ends up stronger than the claim it
replaces. A credential-bearing capture's object is damaged, retrieved through the packaged MCP
server, and quarantined by the daemon — which Louds. `logs/LOUD.log` is then asserted BY NAME to
have been swept, and scanned: no literal and no fragment of any of the eleven planted credentials is
in it. The Loud is about the very object that carried the credentials, which is exactly the case
`internal/store/objects.go` disciplines itself for by logging a twelve-character hash prefix and a
reason rather than any span of the content. Forcing a Loud the obvious way instead — an invalid
configuration key — turned out to disable the whole session, which is finding S-7.

Row 20 is a rule-COVERAGE measurement rather than an invariant, and it is separated from the
ten-family sweep for that reason: the ten families are measured on the key spellings §5.22a names,
and this row asks a different question about a spelling it does not.

## 5. Posture and retention

| # | record | what was measured | outcome |
| --- | --- | --- | --- |
| 21 | `posture_telemetry_is_refused` | a project config setting `runtime.telemetry.enabled: true`, read back through `config print` | **verified** |
| 22 | `posture_out_of_project_capture_is_archived` | a capture whose `tool_input.file_path` names a file outside the project root | **failed** — finding S-6 |
| 23 | `posture_any_config_violation_disables_capture` | one invalid configuration key, and what the hook path does with it | **failed** — finding S-7 |
| 24 | `retention_forced_gc_preserves_evidence` | a GC pass with negative retention on both axes | **verified** |

Rows 21 and 23 measure the same key and disagree, which is the whole of finding S-7. Through
`config print` the value is clamped to false and the violation is written to
`state/config-violations.json` where an operator can find it — §11.3 behaving exactly as specified.
Through the HOOK path the same key makes `config.LoadForCapture` refuse the entire delivery, so no
daemon starts and nothing is captured. Two loaders, one configuration, opposite meanings.

Row 22 is named for what it measures rather than for what the brief expected. The content of an
out-of-project capture IS archived; the path is dropped. §6 gives the mechanism and the coordinator
ruling that settles it.

Row 24 uses the most destructive pass the API can express — negative retention on both axes, the
forcing form — because anything weaker would leave "GC did not collect it" and "GC never considered
it" indistinguishable. It deleted 5 objects and left every piece of diagnostic evidence: the
quarantined object (1 before, 1 after), the `records/captures/**` sidecar (1 before, 1 after), and a
genuine `.corrupt.` sketch produced through `internal/sketch`'s own exported `Quarantine`.

The no-network posture is not re-measured here. `test/guards`' `TestGuard_NoNetworkImports` proves
it of the import graph for the whole tree, and the audit proposal names it as the authority.

## 6. Findings returned (not fixed here)

### S-1 — a post-capture link escape is authorized, because `paths.Norm` discards the outside result

**Owner: `internal/paths` + `internal/mcp`.** Record:
`commit3-security-windows-amd64/archive_trust_parent_directory_replaced_by_link_outside.json`.

`internal/mcp/authorize.go`'s own header states the gap it closes: "a path that was inside the
project when it was captured, but has since become — or been replaced by — a symlink escaping the
project, could be walked back into through the archive even though a live read of the same path
today would be refused." Measured on this host, it is not closed for that shape.

`authorizePath` calls `paths.Norm(root, storedPath)`. `Norm` **does not adopt an outside-landing
resolution**: it calls `EvalSymlinks` and then discards the result when it lands outside the root,
keeping the unresolved spelling. That is a deliberate anti-smuggling rule, pinned by
`internal/paths/norm_test.go`, and the consequence for retrieval is that `src/auth.ts` under a `src`
that is now a junction to another directory normalizes cleanly to `src/auth.ts` with no error.
`authorizePath` therefore sees a path inside the root and authorizes it, and `expand` and `re_read`
both return 24 636 bytes of archived content.

**This is not a data leak, and the record says so in those words.** The bytes served are the ones
captured from inside the project; the sentinel planted in the file the link points at never appeared
in any response, and the case fails its Go test if it ever does. What diverges is the POLICY:
`authorize.go` documents a refusal that does not happen. Three answers are defensible and the choice
belongs to the owning packages, not to this test:

1. refuse when symlink resolution lands outside the root — a behaviour change in `paths.Norm` that
   would touch every store key;
2. refuse in the retrieval layer only — and that means BOTH call sites: `authorizePath`, and
   `re_read`'s own `paths.Norm` gate in `internal/mcp/handlers_span.go`, which is a second, separate
   place the same check has to land;
3. amend `authorize.go` to claim only what it does.

Whichever is chosen, a junction and a symlink differ in what `Lstat` reports, so an `Lstat`-based
fix must be tested against both rather than against whichever one the fixing host can create.

### S-2 — a payload bound above the hard cap disables capture entirely, silently

**Owner: `internal/config` (an upper bound on the key) + `internal/cli` (clamp-and-warn rather than
refuse).** Record: `commit3-security-windows-amd64/bounds_config_cannot_raise_the_capture_cap.json`.

The hard 4 MiB allocation cap held: no byte past the refusal prefix survived. But a configuration
ABOVE the cap is not clamped to it — it takes the product down. `admitHookCapture` returns
`ErrBudget` for the whole delivery when `runtime.hotPath.maxPayloadBytes` exceeds the cap, and that
refusal happens before `session-start` reaches its own daemon bootstrap. Measured, with
`runtime.hotPath.maxPayloadBytes` at 64 MiB:

| observation | result |
| --- | --- |
| hook exit codes and stdout | 0, host-parseable (the contract holds) |
| the 6 MiB payload's marker anywhere under `.qompack/` | absent (the cap holds) |
| a daemon reachable after `session-start` | **no** |
| an ORDINARY capture under the same configuration retained | **no** |
| §11.3 configuration violations recorded | **0** |
| `config print --json` reports the key as | **67108864** |

So an operator who raises the key gets a silently inert plugin: no daemon, no capture, nothing in
`state/config-violations.json`, and an effective-configuration dump that shows their own number as
if it were in force. `Validate` bounds this key only from below, which is why no violation is
written.

Clamping is the safe remedy and the code already shows why: the next use of the value is bounded
again by `internal/cli`'s own `hookCaptureLimit`, so a clamped-and-reported value cannot enlarge any
allocation. S-7 generalizes this finding well past this one key.

### S-3 — a quarantined object reaches the model as a tool error, or as an absence

**Owner: `internal/mcp`.** Record: `commit3-security-windows-amd64/bounds_corrupt_object_envelope.json`.

All five hostile objects are correctly refused by the store and quarantined, and none is ever
reported found. How that reaches the model depends on the ADDRESS FORM, and neither answer is the
right one:

| address form | what the model gets | why it is wrong |
| --- | --- | --- |
| by `tool_use_id` (5 of 5) | a protocol-level tool error | `unavailable` is the envelope vocabulary's explicit third answer for "this build cannot answer"; a tool error is not it |
| by bare chunk hash (5 of 5) | `miss` — that is, ABSENT | §12.3 forbids exactly this: it tells a model the content was never there, when the bytes were refused and preserved |

The second is the more serious, and it is reachable through the "you pasted a chunk hash rather than
a root hash" courtesy branch in `internal/mcp/handlers_span.go`, which maps a `GetChunk` failure
straight to `miss(...)` without distinguishing a quarantine refusal from a genuine absence.
`internal/mcp/unavailable_test.go` already establishes the correct answer for `already_tried`'s
equivalent backend failure, and states the reason: a backend failure's details can carry private
paths, and it establishes neither presence nor absence. No path and no secret appears in any of the
ten answers measured here — they carry the hash's twelve-character short form and nothing else — so
this is a vocabulary defect, not a disclosure one. Both call sites need the fix; repairing one
leaves the other.

### S-4 — the eval exporter's rule set is weaker than `internal/redact`

**Owner: `internal/eval`.** Records: `privacy_surface_eval_export.json`, `privacy_eval_export.json`.

`eval.Redact` carries its own, separate rule table, and it caught 18 spans in the transcript. Two
planted credentials survived it:

| credential | why it survives |
| --- | --- |
| a classic GitHub personal access token | the exporter's rule fixes the token body at exactly 36 characters. The planted token has 38, which is legal for the format, so the trailing word boundary fails and the rule does not fire. `internal/redact`'s own rule quantifies 36-or-more for this exact reason and does catch it. The exporter also has no `ghu`/`ghs`/`ghr` prefixes at all. |
| the operator's own `runtime.redact.patterns` rule | `eval.Redact` has no notion of user patterns. Whatever an operator adds to their configuration protects the store and does not protect an export. |

The gap is narrower than "the exporter misses keyed credentials": its catch-all
`(password|secret|token|api_key)\s*[:=]\s*\S+` does reach a credential written beside a key name.
What it misses is a BARE long token standing on its own line, which is how a PAT, an API key or a
session grant usually appears in a transcript. Neither is a surprise to the design — the exporter is
an opt-in developer path, its destination is refused inside the working tree, and unredacted export
needs a second environment variable — but both belong in `docs/security.md` rather than leaving a
reader to assume the two rule sets agree. This surface's failure does NOT fail the Go test, for the
reason the package comment gives: the exporter is not one of the product's durable surfaces.

### S-5 — the assignment rule cannot reach an underscore-prefixed key, and has no bare `auth` branch

**Owner: `internal/redact`.** Record:
`commit3-security-windows-amd64/privacy_assignment_rule_underscored_keys.json`.

§5.22a's assignment family gates its whole key alternation behind ONE leading word boundary, and a
word boundary cannot match between an underscore and a letter — so `_authToken=`, which is exactly
how npm writes a registry credential into `.npmrc` and how it appears in every `npm config set`
command line, is outside the rule. `_password=` is outside it for the same reason. Measured: the
credential survives in clear in BOTH `index/tool_use.jsonl` (the args preview) and `objects/` (the
tool result), on a run where the same value written as `client_secret=` is redacted on every
surface.

`_auth=` is outside the rule TWICE OVER, and the second reason matters for the remedy: the
alternation carries `auth[_-]?token` and no bare `auth` branch at all, so even plain `auth=` is
unmatched. Allowing an optional leading underscore would therefore fix `_authToken=` and
`_password=` and still not reach either `auth=` or `_auth=`; a bare `auth` alternative is a separate
change.

The rule table is prose in §5.22a rather than a frozen fixture — `internal/redact/redacttest` pins
BEHAVIOUR, not this alternation, and widening it disturbs no committed fixture — so the change is
cheap. It is still the owning package's call, which is why this is returned rather than fixed.

### S-6 — an out-of-project capture is archived with its path dropped

**Owner: `internal/observer` (tool-use normalization) + `internal/cli` (the hook side).** Record:
`commit3-security-windows-amd64/posture_out_of_project_capture_is_archived.json`.

A `PostToolUse` delivery whose `tool_input.file_path` is an absolute path in a sibling directory has
its CONTENT archived like any other capture; its PATH is dropped. The hook contract held (exit 0,
parseable output), and no absolute out-of-project path reached any durable surface.

The mechanism is in `internal/observer`'s tool-use path: `paths.Norm` fails for a path outside the
root, the path key falls to the empty string, and the bytes go to `PutBytes` anyway. That is exactly
the pair of facts the record observed — content persisted, path not persisted.

`internal/admission`'s `Privacy` port is **not** the remedy and could not be: it has no production
importer at all, and its `Permits` takes only a payload, with no path to judge. An earlier draft of
this finding named it as the owner; that was wrong.

**Coordinator ruling, carried here and into the proposal: no capture-time refusal is introduced.**
The host had already permitted the read and the model had already seen the bytes, so archiving them
under the project's own `.qompack/` escalates nothing. The trust boundary is retrieval-time
authorization — the S-1 fix — plus redaction at capture. What is owed is that `docs/security.md`
states this behaviour explicitly rather than implying a refusal that does not exist.

### S-7 — any configuration violation disables capture and the daemon, silently

**Owner: `internal/config` + `internal/cli`.** Record:
`commit3-security-windows-amd64/posture_any_config_violation_disables_capture.json`.

This one was found by accident while repairing the `LOUD.log` overclaim: an attempt to force a Loud
by setting the hardwired-off `runtime.telemetry.enabled` produced a session with no daemon, no
objects and no index at all.

`config.LoadForCapture` ends with `if len(cfg.Validate()) != 0 { return fail() }` — ANY violation, in
ANY key, makes the whole capture unavailable with `core.ErrDegraded`. `internal/cli`'s hook path
then writes empty output and returns before `session-start` reaches its daemon bootstrap. §11.3's
contract for an invalid value is the opposite: clamp to the default and record the violation, which
is precisely what `config print` does through `LoadConfigAndReport` — and what row 21 measures
working correctly on the very same key.

So two loaders read one configuration and disagree about what an invalid key means, and the one on
the hot path fails closed over the entire product rather than over the key. Measured: an ordinary
capture under the same configuration not retained, zero §11.3 violations recorded by the hook path
before anything else touched the file, and no daemon reachable within the 10 s the case waits. The
row's verdict is keyed on the first two — what the product reports and what survived — and not on
the daemon observation, which is wall-clock-bounded and would let a slow cold start on a FIXED build
report this finding as still open. Clamping is safe for the same reason S-2 gives: the next use of
any such value is bounded again downstream. S-2 is the special case of this finding that the brief
happened to name.

## 7. What was NOT established

Stated plainly, because a security document that omits its own gaps is worse than none.

- **A captured FILE replaced by a link pointing outside the root** is unmeasured on this host (row
  2). It skipped, with a reason, and a skip is never a pass. It needs a machine with
  `SeCreateSymbolicLinkPrivilege` or Developer Mode, or a non-Windows runner. The directory form of
  the same escape (row 1) WAS measured, via an NTFS junction, and produced finding S-1; there is no
  reason to expect the file form to behave differently, but "no reason to expect" is not evidence.
- **Only windows/amd64 ran.** The package is platform-independent (`go vet` passes for
  `GOOS=linux GOARCH=amd64`) and will run in the CI test job's OS matrix once this branch is pushed,
  but no linux or darwin result exists. Nothing here may be cited for another target. The
  link-mechanism probe prefers `os.Symlink` everywhere, so a POSIX runner measures the real symlink
  shapes rather than the junction substitute.
- **The backup surface is conditional on a gate that ships closed.** Row 17 supplies the
  legacy-import gate from the test. A shipped build cannot take a backup at all, so "the backup tree
  carries no credential" is a statement about code that is currently unreachable in production.
- **This is not a whole-filesystem claim.** The sweep walks `.qompack/`, the backup subtree, the
  eval export destination and the MCP responses. A write somewhere else entirely is the write-set
  guard's question (`test/guards/writeset_test.go`), not this package's.
- **Redaction was measured on eleven planted credentials, not on the rule engine.** Whether each
  §5.22a rule is correct for every input in its family is `internal/redact`'s own conformance suite.
  Finding S-5 is one instance of the first question found while asking the second.
- **No timing claim of any kind is made.** This package asserts no latency and measures no budget.
  The durations quoted in §1 are wall times of the suite, recorded so a future run can be compared
  against them; they are not budgets and nothing fails on them.
- **An installed host was never involved.** Every retrieval here was driven by this test speaking
  JSON-RPC to a process it launched itself. That a real Claude Code session, having loaded the
  plugin from an installed directory, gets the same answers is the canary suite's open question B01
  and is not established here.
- **`why` and `dropped` were swept but not exercised.** Both may report themselves unavailable in
  this build; their responses were scanned for credentials, and nothing was asserted about their
  content.
- **The trigger for the reviewer's original hang was never root-caused, only bounded.** The unbounded
  `Wait` it exposed is fixed (§1), and the package now runs in under 40 seconds twice consecutively;
  but which process inherited which handle on that host was not determined, and if it recurs the
  `WaitDelay` notice in the log is what will say so.

## 8. Reproducing this

```
go run ./tools/devtool bundle --target windows/amd64 --out <some empty directory>
go test -count=1 -timeout=30m ./test/security/
QOMPACK_SECURITY_ARTIFACTS=<this directory>/commit3-security-windows-amd64 go test -count=1 -timeout=30m ./test/security/
```

The first line is what the suite does for itself, once per test binary; it is written out only so a
reader can see the artifact by hand. The third line is how the records in
`commit3-security-windows-amd64/` were produced: the directory is pruned of its previous `.json`
records at the start of that run and `INDEX.json` is written at the end, so what is committed is
exactly what one run produced.
