# Proposed prose for `docs/security.md`

This is Role C's PROPOSAL, not the document. `docs/security.md` is written by the coordinator in
Task 7, which owns wording, placement and what else belongs beside this. Every claim below is backed
by a record in `commit3-security-windows-amd64/` or by a file:line in the tree, and nothing here
asserts anything `test/security` did not measure. Where a measurement contradicts a design comment,
this says so rather than repeating the comment.

## 1. Trust boundaries

Qompack stores what the host has already given the model, and hands it back on request. That makes
two boundaries load-bearing, and they run in one direction only.

**Host permission outranks the archive.** A stored content hash or a host `tool_use_id` is an
ADDRESS, never a credential. Supplying a well-formed one is not authorization to materialize the
bytes behind it: `recall` re-checks every hit before it builds a preview, and `expand` and `re_read`
re-check before they touch the store at all (`internal/mcp/authorize.go`). The rule the check
enforces is the same one a live read of that path would face today — not the one that applied when
the content was captured.

**A refusal is not an oracle.** The one refusal sentence the retrieval layer uses never echoes the
offending path, so a caller cannot use denials to probe what exists outside the project. Measured
across three escape shapes and three tools: no response echoed an escaping path or an absolute path
outside the root.

**The archive is not a second filesystem.** `re_read` answers from recorded history and never falls
back to a live disk read: measured after the captured file had been replaced wholesale, and again
after it had been deleted — the archived bytes came back both times, and the replacement never did.

**Archived text is data with provenance, never instruction.** Everything in the store arrived from a
tool result, which is whatever some file or some command printed, which an attacker may control.
Two things keep that safe and both were measured end to end on the packaged binary: every response
carrying stored bytes sets `_meta.qompack.untrusted`, and nothing in the retrieval path can execute
anything — `internal/mcp`'s transitive import set contains no `os/exec` across 155 packages. A
capture whose arguments were a shell command that would create a sentinel file was retrieved through
four tools and the sentinel was never created.

**One measured divergence, and it belongs in this section rather than in a footnote.** For an
address whose path was inside the project at capture and whose PARENT DIRECTORY has since been
replaced by a link pointing outside the root, the authorization check does not refuse — it returns
the archived content. `paths.Norm` does not ADOPT an outside-landing resolution: it calls
`EvalSymlinks` and discards the result when it lands outside the root, keeping the unresolved
spelling — a deliberate anti-smuggling rule, pinned by `internal/paths`' own tests — so the escaping
path normalizes cleanly and there is nothing for the check to reject. No byte of the linked-to file
is ever returned, so this is not a data leak; what it is, is a documented refusal that does not
happen (finding S-1, owner `internal/paths` + `internal/mcp`). A retrieval-layer remedy has to land
in TWO places, not one: `authorizePath`, and `re_read`'s own `paths.Norm` gate in
`internal/mcp/handlers_span.go`. `docs/security.md` should describe the behaviour that exists. The
same escape written LEXICALLY — a stored path containing `../` — is refused with the explicit denied
envelope, and that was measured too.

## 2. What redaction covers, and what it does not

Redaction runs once, on the way IN, at a single choke point (`internal/redact`, ten built-in
credential families plus any `runtime.redact.patterns` an operator adds). The store is
content-addressed and immutable, so a secret that reaches `objects/` cannot be removed without
breaking every root that references its chunk — which is why the choke point is on the write path
and why a nil redactor means the DEFAULT rule set rather than no redaction.

Retrieval re-applies the CURRENT policy to bytes on the way out, so a record captured before a rule
existed is not served in the clear forever; and if no redactor is wired, retrieval serves nothing
and reports itself unavailable rather than serving unchecked bytes.

**Measured coverage.** A session carrying all ten built-in families plus an operator pattern was
driven through the packaged binary, checkpointed, flushed, backed up and exported, and every file
the product wrote was swept for each credential's literal value and for any surviving run longer
than eight bytes. Clean: `objects/`, `index/` (including the `tool_use.jsonl` args preview),
`records/`, `checkpoints/`, `pins/`, `spool/`, `state/`, `metrics/`, `logs/`, `dag/`, `sketches/`,
`run/`, the `.qompack` root itself, the backup tree and its manifest, and the seven retrieval
responses the MCP server put on the wire.

`LOUD.log` is checked by a case of its own rather than by that sweep, and the distinction is worth
keeping: the file is written only when something Louds, so a session in which nothing degrades leaves
`logs/` holding a day log and nothing else. The dedicated case arranges the Loud through the
product's own §12.3 path — it damages the object a credential-bearing capture produced, retrieves it
through the packaged MCP server, and lets the daemon quarantine and Loud about it — then asserts
`logs/LOUD.log` by NAME and scans it. The Loud is about the very object that carried the
credentials, and it carries a twelve-character hash prefix and a reason, never a span of the
content.

**What it does not cover, and what an operator should know.**

- **The eval exporter uses a different, weaker rule set.** `eval.Redact` is not `internal/redact`.
  Two planted credentials survived an export that the store rejected outright: a classic GitHub
  personal access token whose body is longer than the exporter's fixed-length rule expects (the
  exporter also carries no `ghu`/`ghs`/`ghr` prefixes), and the operator's own
  `runtime.redact.patterns` rule, which the exporter has no notion of at all. The gap is narrower
  than "keyed credentials leak": the exporter's catch-all does reach a secret written beside a key
  name such as `password=` or `token=`. What it misses is a BARE long token standing on its own
  line, which is how a PAT, an API key or a session grant usually appears in a transcript. An
  operator's custom pattern protects the store and does not protect an export. The export path is
  opt-in, refuses a destination inside the working tree, and needs a second environment variable to
  emit unredacted output — but "redacted" there means less than it does everywhere else.
- **`LOUD.log` is append-only, is never rotated, and is never backed up.** It grows for the life of
  the project and a backup deliberately skips `logs/` entirely. It was written on purpose, swept by
  name and found clean (see the dedicated case above), and that is a property of caller discipline
  rather than of a redaction layer: `internal/logging` has none.
  Every site that Louds about sensitive material logs a count and rule names, or a twelve-character
  hash prefix, and never a span of the input. A future caller that logs a payload would not be
  caught by any mechanism except review.
- **The spool is not redacted by the store.** What lands in `spool/` is whatever the capture path
  already admitted, and the capture path redacts before it hands anything over — measured clean
  here. But `store`'s backup walk copies the spool, so anything a capture retains is copied with it.
  The protection is upstream, not at the copy.
- **An underscore-prefixed assignment key is outside the rule table, and so is a bare `auth=`.**
  `_authToken=`, the spelling npm writes into `.npmrc`, is not matched by the assignment family,
  because one leading word boundary gates the whole key alternation and a word boundary cannot match
  between an underscore and a letter; `_password=` is out for the same reason. `_auth=` is out
  twice over, and the second reason changes the remedy: the alternation carries `auth[_-]?token` and
  no bare `auth` branch at all, so plain `auth=` is unmatched too and allowing a leading underscore
  would not reach either spelling. Measured: the credential survives in clear in both
  `index/tool_use.jsonl` and `objects/`, while the same value written as `client_secret=` is
  redacted on every surface (finding S-5, owner `internal/redact`).
- **A capture for a path outside the project root is archived, with its path dropped.** A tool
  result whose `tool_input.file_path` names a file in a sibling directory has its CONTENT stored
  like any other capture; the path key falls to empty when `paths.Norm` refuses it, so the
  out-of-project path itself never reaches a durable surface. **This is settled behaviour, not a
  missing guard:** no capture-time refusal is introduced, because the host had already permitted the
  read and the model had already seen the bytes. The trust boundary is retrieval-time authorization
  plus redaction at capture. `docs/security.md` states this explicitly rather than implying a
  refusal that does not exist (finding S-6, owner `internal/observer` + `internal/cli`).
- **Redaction is not erasure across media.** Nothing here promises secure physical erasure of a
  credential that has already been written to a backup, a copy, or a filesystem with snapshots.

## 3. Decode and size bounds

Every bound below is a named constant in the tree, and each was exercised against hostile input on
the packaged binary or the real store. None of them is advisory.

| bound | value | where | what it stops |
| --- | --- | --- | --- |
| `maxDecodedSize` | 64 MiB | `internal/store/compress.go` | a decompression bomb: the decoder is constructed with this memory limit, so a frame that expands past it fails instead of allocating |
| `encodedObjectLimit()` | zstd worst-case framing over `MaxPutBytes` | `internal/store/compress.go` | an object FILE larger than any legal encoding; the read stats first and refuses before the first byte |
| `store.MaxPutBytes` | 64 MiB | `internal/store/fsstore.go` | an unbounded single put; the input is truncated rather than failing, because every producer is a hook |
| `hookCaptureMaxBytes` | 4 MiB | `internal/cli/capture_admission.go` | a host delivery larger than the process will allocate, applied BEFORE configuration is read |
| refusal prefix | 4 KiB | `internal/cli/capture_admission.go` | the evidence a refused delivery may carry forward |
| `runtime.mcp.maxResponseBytes` | 262 144 by default | `internal/config/defaults.go` | an unbounded retrieval response; `full` means "do not narrow to the matching hunk", never "unbounded" |
| `ipc.MaxLineBytes` | 1 MiB | `internal/ipc/wire.go` | an oversize request line; configuration may tighten it and may never raise it |

Measured: an 80 MiB bomb compressed to a few kilobytes is refused by `store.Decode` before it is
ever written to disk; a 128 MiB object file is refused on its size alone; a 6 MiB `tool_response` is
refused by the capture cap with only a bounded prefix retained, and the hook still exits 0 with
host-parseable output; and a full `expand` of a 24 636-byte object against a 4096-byte response
bound returned exactly 4096 bytes, marked truncated, with a cursor to page on.

**One divergence worth a sentence in the document.** Setting `runtime.hotPath.maxPayloadBytes` ABOVE
the hard capture cap does not clamp it — it refuses every delivery, before `session-start` reaches
its own daemon bootstrap. The result is a silently inert plugin: no daemon, no capture, no
configuration violation recorded, and `config print` reporting the operator's own number as though
it were in force. Validation bounds that key only from below (finding S-2). Clamping would be safe
rather than merely convenient, and the code already shows why: the next use of the value is bounded
again by `internal/cli`'s own capture limit, so a clamped-and-reported value cannot enlarge any
allocation.

**And the general case is worse than the one key.** `config.LoadForCapture` refuses the WHOLE
delivery when validation reports anything at all, so one invalid key anywhere — even a hardwired-off
switch an operator can never legitimately enable — leaves the plugin with no daemon and no capture,
while `config print` reads the same file, clamps it and records the violation exactly as §11.3
prescribes. Two loaders, one configuration, opposite meanings (finding S-7). Until both are
resolved, the operator-facing advice is: keep the configuration valid, and check
`state/config-violations.json` after changing it.

## 4. Quarantine and the evidence it leaves

An object whose bytes cannot be trusted is never served and never silently dropped. The read path
verifies, in order: the file is a regular file that did not change between stat and open; its
physical size is within the read bound; it decodes; its plaintext length matches what the index
recorded; and its content hashes to the address it is filed under. Any failure moves the bytes to
`.qompack/tmp/quarantine/<unique attempt directory>/`, Louds with the hash's twelve-character short
form and nothing else, counts `store.quarantined`, and returns `core.ErrNotFound`.

Measured against five hostile objects — a damaged frame, a truncated frame, an oversize file, a
decompression bomb, and a valid frame of the right length whose plaintext hashes elsewhere — all
five were refused and all five were quarantined. Afterwards the daemon still held its lock, the MCP
server still expanded a healthy object, and the `checkpoint` hook still exited 0 with a
host-parseable document. No corrupt object was ever reported as found.

**Quarantined evidence survives collection.** A garbage-collection pass with retention disabled on
both axes — the forcing form, the most destructive the API can express — deleted five objects and
left the quarantined object, a `.corrupt.` sketch produced by `internal/sketch`'s own quarantine
path, and every `records/captures/` sidecar exactly where they were. That is the plan's rule stated
as a measurement: repairs preserve diagnostic and quarantine evidence, and there is no destructive
default cleanup.

**How a corrupt object reaches the model is a defect, not a design, and it is worse by one address
form than it first appears.** Asked by `tool_use_id`, `expand` renders a quarantined object as a
protocol-level tool error rather than as the `unavailable` domain outcome the envelope vocabulary
defines; the distinction matters to a caller, because "unavailable" is a third answer beside "found"
and "not found" and a tool error is not. Asked by BARE CHUNK HASH, the same object comes back as a
plain miss — that is, as ABSENT, which §12.3 forbids outright for a quarantined object, because it
tells a model the content was never there when the bytes were refused and preserved. `already_tried`
already gets this class of failure right. No path and no secret appears in any of those answers —
they carry a twelve-character hash prefix — so this is a vocabulary defect rather than a disclosure
one, and both call sites need the fix (finding S-3, owner `internal/mcp`).

## 5. No network, no telemetry

Qompack talks to nobody. The authority for that is `test/guards`' `TestGuard_NoNetworkImports`,
which proves it of the import graph for the whole tree in both directions and permits the bare `net`
package in `internal/ipc` alone, for the local transport. `docs/security.md` should cite that test
rather than restate its conclusion, and `test/security` deliberately does not duplicate it.

`runtime.telemetry.enabled` exists only to say telemetry is off. It is hardwired: a project
configuration that sets it true is refused, the effective configuration reports false, and the
refusal is recorded in `state/config-violations.json` where an operator can find it. Measured on the
packaged binary.

Any future external service would require explicit user consent and a new architecture decision.
Nothing in this build has one, and no configuration key can create one.

## 6. Findings returned by this work

Seven, all recorded with their owning package and none fixed in commit 3. **Commit 6 fixed six of
them** — S-1, S-2, S-3, S-4, S-5 and S-7 — and regenerated every record in
`commit3-security-windows-amd64/` from a run of the fixed product; the `status` column below says so
per row. S-6 was ruled: it is settled behaviour to DOCUMENT (Task 7), not a guard to add, and its row
is still `failed`. Full detail, with the record that backs each, is in `commit3-evidence.md` §6, and
the change itself is in `commit6-evidence.md`.

| id | summary | owner | status |
| --- | --- | --- | --- |
| S-1 | a post-capture link escape is authorized, because `paths.Norm` discards an outside-landing resolution and keeps the clean spelling; `authorize.go` documents a refusal that does not happen. No data leaked. A retrieval-layer fix needs both call sites. | `internal/paths` + `internal/mcp` | **FIXED (commit 6)** — `paths.ResolvesInside` at both call sites |
| S-2 | `runtime.hotPath.maxPayloadBytes` above the hard cap disables capture and the daemon entirely, with no violation recorded | `internal/config` + `internal/cli` | **FIXED (commit 6)** — `Validate` bounds the key from above |
| S-3 | a quarantined object surfaces as a tool error by `tool_use_id`, and as ABSENT by bare chunk hash, rather than as the `unavailable` domain outcome | `internal/mcp` | **FIXED (commit 6)** — `store.ErrDamaged` and `unavailable` on both forms |
| S-4 | the eval exporter's rule set misses a long GitHub token, the `ghu`/`ghs`/`ghr` prefixes, a bare unkeyed token, and every operator-supplied pattern | `internal/eval` | **FIXED (commit 6)** — rules widened; `ImportOptions.Patterns` carries the operator's |
| S-5 | the assignment rule cannot match an underscore-prefixed key such as `_authToken=`, and has no bare `auth` branch, so `auth=` and `_auth=` need a second change | `internal/redact` | **FIXED (commit 6)** — `\b_?` gate plus a bare `auth` branch |
| S-6 | an out-of-project capture is archived with its path dropped; settled behaviour to document, not a guard to add | `internal/observer` + `internal/cli` | open by ruling — documented in Task 7 |
| S-7 | ANY configuration violation makes the hook path refuse every delivery, so one bad key silently disables the daemon and capture, while `config print` clamps and reports the same key | `internal/config` + `internal/cli` | **FIXED (commit 6)** — `LoadForCapture` clamps and returns the violations |

## 7. What this proposal does not claim

`docs/security.md` should carry `commit3-evidence.md` §7 in some form. The short version: the
file-replaced-by-a-link escape is unmeasured on Windows without the symlink privilege; only
windows/amd64 has run; the backup surface was exercised through a gate that ships closed; the sweep
covers `.qompack/`, one backup, one export and the retrieval responses rather than the whole
filesystem; no installed host was ever involved; and no timing claim of any kind is made here.
