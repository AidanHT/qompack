# V5-VERIFY §4.17 — `TestV5_AdmissionExtension` disposition

**Identifier (retained):** `TestV5_AdmissionExtension`
**Disposition:** authored, with a recorded unverified remainder
**Base:** `verify/v5` @ 87c0c1d (develop with wave 4: SP-15, SP-16, SP-14, SP-21)
**Branch:** `v5/x17`

## Current criterion (authoritative, plan §4.17)

Four bullets: (a) a fresh owned-result first transformation is distinguished from an
already-processed envelope bypass, then a tiny target-tested host allowlist; (b) durable
capture/authorization and resolution precede replacement, and an unavailable baseline, parser
change or failure-signature change resets relative-delta eligibility; (c) unsupported
binary/multimodal/schema content follows pass-through or privacy-denial policy with
structured/displayed status and errors preserved; (d) supported-task quality/recoverability is
recorded against unmodified output with cost/latency separately, and replacement stays off until
target and regression gates pass. Disabled admission is recorded as disabled, never passed.

## Level and file

**Level:** `test/integration` (in-process). `internal/admission` has no composition root on this
tree — the SP-21 evidence section says so and the code confirms it: the five ports (Privacy,
Capturer, Publisher, Parser, Resolver) have no production adapters, the CLI's "admission" is the
older privacy admission of hook captures, and no daemon route or MCP tool reaches the pipeline.
There is therefore no process boundary to cross; an e2e seam is unwritable. The historical seam
was never written (the §4.17 row was added with SP-21 and has no v5-head-section4 text), so the
in-process level is the first one, not a departure from one.

**File:** `test/integration/v5_x17_test.go`

**Real producers wired to the ports, the way the plan says the composition root would:**

| Port | Real producer |
|---|---|
| Capturer | `store.PutBytes` on the real SP-20 store (`testutil.Project.Store`, internal/store's own default deps), fidelity carried across verbatim |
| Publisher | the same store: `Flush`, `GetRoot`, and `Has` over every chunk the root names |
| Resolver | `(*store.FSStore).RestoreOriginal` — the archived original read back; `ErrNotFound` is unavailable, any other error is uncertain |
| Privacy | the real `redact.CapturePolicy(cfg)`, classified exactly as `internal/daemon.admitDelivery` classifies the same decision |
| Switch | the real `config.Load` over a project config, the real `config.MigrationGates()` table |
| Enablement record | the real `contract.DefaultCapabilityRegister()` |
| Parser | **not a shipped producer.** No parser exists anywhere on the tree for any target; the test supplies a minimal strict-JSON parser for a Qompack-owned result and says so in its doc comment |

## Test names and what each asserts

`TestV5_AdmissionExtension` with five subtests, then `Project.AssertAppendOnly`:

1. `disabled_admission_is_recorded_as_disabled_never_passed` — a gate built from the loaded
   default config is disabled; the pipeline records `pass-through`/`disabled`/`StageNone`, no
   handle, no mark, no form, and the store's object count does not move (a disabled pipeline
   captures nothing on its way to refusing). A project config that sets
   `runtime.migration.replacement.newResult=true` loads as `false` with a `using default` warning
   at that key; the gate table's entry is `Passed=false`, owner `SP-21`; the capability register
   records `new_result_replacement` as `Enabled=false`, `documented` (never `verified_in_target`),
   fallback pass-through; the shipped allowlist is empty.
2. `fresh_owned_result_transforms_once_then_envelope_bypasses` (bullet a) — on an in-process
   authoring gate (the loaded config cannot produce one; subtest 1 proves it), a fresh owned
   delivery is admitted: `transform`/`admitted`, capsule with no base, `archive_only` coverage,
   recoverable fidelity, `Mark == MarkerProducer`, the delivered bytes untouched, the store grown.
   The handle reads back the byte-identical original from the real store with the same fidelity
   the record carries. `admission.Preserves` reports every Meaning field survived and the
   status/stderr/diagnostics/displayed/structured halves equal the delivered ones. A redelivery
   carrying our marker is `already-processed` and captures nothing; a foreign marker is
   `foreign-transform`; both present is ours; a repeated FRESH delivery of the same bytes is
   admitted with the SAME handle and grows the store by nothing. A tiny host allowlist with one
   exact entry admits that exact schema+version, refuses a version bump before capture, and the
   shipped (empty) allowlist refuses the host target.
3. `durable_capture_and_resolution_precede_replacement` (bullet b) — `KeepRaw=false` (the store's
   own canonical-only fidelity) is `capture-failed`/`ErrUnrecoverableCapture` with no handle, and a
   later `KeepRaw=true` redelivery of the same bytes stays canonical (fidelity is a property of the
   root's first put). A closed store is `capture-failed`/`ErrDegraded`. A publisher over a store
   that never published the object is `publication-unverified`/`ErrNotFound`. A resolver over a
   store that lacks the root blocks at `resolution` with `HandleState=unavailable` visible; a
   resolver over a closed store is `uncertain`, never a denial. Then the admitted capsule, a
   baseline READ BACK from the store (`VerifyBaseline` from `RestoreOriginal` facts) supporting a
   delta whose `Base` is the capsule's handle; the same baseline merely named (composite literal)
   resets `unverified`; a baseline unreadable from the resolver's store resets `unverified`; a
   changed signature resets `signature-changed`; a changed target version resets
   `schema-changed`; a canonical-fidelity baseline resets `uncertain-capture`.
4. `unsupported_content_passes_through_or_follows_privacy_policy` (bullet c) — a `media:true`
   payload is `no-representation` at `selection` with the bytes untouched and no actionable
   meaning; an unknown schema is refused before capture; a payload that fails its parser is
   `parse-failed` at `parse` with the parser error and declared target on the record; bytes that
   are not a JSON object make the real policy answer `ErrContract`, which is
   `policy-unavailable` at `policy`, NOT a denial, with nothing captured; an unwired privacy port
   is the same reason with `ErrPortsUnwired`. The denial rule is asserted at the `Decide` level:
   `StagePrivacy` yields `deny`/`privacy-denied` with the switch off and on, and `StagePolicy`
   never does.
5. `quality_is_recorded_against_unmodified_output_and_replacement_stays_off` (bullet d) — a zero
   comparison is `inconclusive` and does not enable; three predeclared layers at full retention are
   `retained` and enable; one layer at half retention is `regressed`; a missing layer or a
   composite-literal `LayerResult` (no predeclared margin) is `inconclusive`. No exported field of
   `Comparison` or `LayerResult` names cost, latency, tokens or price. And with a retained verdict
   in hand the real config still refuses the switch, the register still says disabled, and a
   pipeline over the real config still records `disabled`.

## Negative control and how it was proven

The negative controls are real switches inside the test, not authoring-time sabotage:

- `store.PutOptions.KeepRaw=false` — the store's own canonical-only fidelity refuses replacement.
- `Store.Close()` before capture, and a closed store as the resolver.
- A second real project's store as publisher and as resolver (the object was never published
  there; the root is not there).
- A project `config.json` that opts in, refused by the gate table with a fallback warning.
- The producer marker on a redelivery, and a foreign marker.
- The empty shipped allowlist against the same host target the one-entry allowlist admits.

Each arm asserts the honest failure path (reason, stage, error, empty handle/mark/form/meaning,
unknown coverage) rather than absence. During authoring the row also went red three times for
real-store reasons before it went green; those runs are the store findings below, kept in the
scratchpad logs `x17-run1.txt` and `x17-run2.txt`.

## Old-to-new assertion map

There is no historical §4.17 text: `v5-head-section4.md` (the pre-SP-21 head) ends at §4.16, and
the four bullets were added with SP-21. The map is therefore bullet → assertion, with the
guarantees the plan retires marked as such.

| Historical / plan expectation | Status | Where |
|---|---|---|
| Fresh owned result transformed once; already-processed envelope bypasses | kept | subtest 2 |
| Repeated delivery idempotent | kept, corrected to what the store proves: same bytes, same handle, zero growth | subtest 2 |
| Tiny host allowlist, exact schema+version | kept for exactness; **"target-tested" retired** — B01 keeps the installed host unobserved, so the allowlist entry here is a test value and the shipped allowlist stays empty | subtest 2, subtest 1 |
| Durable capture and verified publication precede replacement | kept | subtest 3 |
| Every handle resolves under current authorization | corrected: resolution is the real store's read-back; **authorization against SP-13/M2 retired** (no adapter exists, per SP-21 evidence T21-RECOVERY-01) | subtest 3 |
| Unavailable baseline / parser change / signature change reset delta eligibility | kept; "unavailable" is a baseline unreadable from the resolver's store | subtest 3 |
| Binary/multimodal/unknown schema pass through | kept | subtest 4 |
| Privacy denial follows denial policy | **partly retired**: no shipped policy on this tree produces `OutcomeDenied`, so the denial is asserted at the `Decide` level only; the real policy's non-answer is proven to pass through, not deny | subtest 4 |
| Structured/displayed status and errors preserved | kept (`Preserves` on the admitted record; stage error and target retained on refusals) | subtests 2, 4 |
| Quality/recoverability recorded against unmodified output, cost separately | kept for the mechanism; **no statistical claim** is made (twenty synthetic counts prove arithmetic, not retention) | subtest 5 |
| Replacement stays off until target and regression gates pass | kept | subtests 1, 5 |
| Disabled admission recorded as disabled, never passed | kept | subtest 1 |
| Host delivered/accepted/displayed a replaced result | **retired** — host-side effect no in-repo test can observe | — |

## Unverified remainder

- **Host allowlist target evidence (B01).** The one-entry allowlist is exact-match tested in
  process; no installed-host canary transcript or competing-hook observation exists, so the
  shipped allowlist stays empty and nothing here supports enablement.
- **Privacy denial end to end.** No producer on this tree returns `OutcomeDenied`
  (`redact.CapturePolicy` redacts, refuses with `ErrContract`, or reports unavailable). The
  denial rule is `Decide`-level only.
- **Authorization at resolution.** The resolver is the store's read-back. Denied/authorized
  handle states under SP-13/M2 have no adapter and are not exercised.
- **The parser.** No production parser exists for any target; the test's strict-JSON parser is
  a test adapter, and every "parse failed" assertion is about the pipeline's handling, not a
  shipped parser's behaviour.
- **Quality comparison as evidence.** Counts are synthetic; T21-QUALITY-01 stays inconclusive.
- **Cost/latency recorded separately.** Asserted structurally (no field on `Comparison` or
  `LayerResult` can carry one); no ledger row is produced or checked.

## Findings in `internal/store` (not fixed here; for the coordinator)

Both surfaced while wiring the resolver to `RestoreOriginal`; neither is needed to assert this
criterion once the test's payloads avoid them, and a fix touches side-record identity and GC
coupling, which is the store owner's call.

1. **Shared side record across roots with identical delta lists.** `putSideRecord` content-
   addresses a delta record over `marshalDeltas(deltas)` alone and returns the existing root when
   known. Two content roots whose canonicalization removed the same volatile token at the same
   offset (e.g. two tool outputs with the same timestamp prefix and different bodies) therefore
   share one side record whose declared base is the FIRST root. The second root's `PutBytes`
   reports `FidelityExact`; its `RestoreOriginal` then fails with
   `ErrDeltaCorrupt: delta X declares base A, not B` and `FidelityCorrupt`. Reproduced in
   `x17-run2.txt` (line 567 of the file at that time). GC coupling makes it worse: the shared
   record is retained by one base only. This is an SP-20 invariant 6 exactness claim that cannot
   be honoured on read.
2. **Put/read fidelity label divergence with no volatile token.** When canonicalization changes
   nothing, `admitRecovery` reports `FidelityExact` with no side record; `RestoreOriginal` cannot
   distinguish that from "no record was asked for" and labels the same bytes `FidelityCanonical`.
   The bytes are right; the label under-claims. Reproduced in `x17-run1.txt` (line 439 at that
   time). `storedFidelity` has the same shape for a dedup hit.

The test's `x17Failure` doc comment records both and explains why every payload carries its own
timestamp seconds value.

## Run command and result

```
cd <worktree> && go test ./test/integration -run '^TestV5_AdmissionExtension$' -count=1 -v
```

- `go test ./test/integration -list 'TestV5_AdmissionExtension'` → exactly `TestV5_AdmissionExtension`
- Pass 1 (`-v`): `ok github.com/qompack/qompack/test/integration 1.248s`, all five subtests PASS
- Pass 2: `ok github.com/qompack/qompack/test/integration 2.099s`
- `gofmt -l ./test ./internal` → none; `go vet ./test/integration` → ok
- `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` → exit 0, all PASS
- No timing gate is involved; the row is not co-load sensitive.

## Production changes

None.
