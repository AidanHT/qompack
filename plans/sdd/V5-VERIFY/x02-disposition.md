# V5-VERIFY §4.2 — `TestV5_TombstoneToExpandRoundTrip` disposition

| Field | Value |
|---|---|
| Retained identifier | `TestV5_TombstoneToExpandRoundTrip` |
| Current criterion (plan §4, row 4.2) | SP-20/SP-13 exact authorized handle recovery with fidelity and no historical/current substitution. |
| Disposition | **authored** — every clause of the current criterion is asserted with real producers; one production defect was found and fixed on the way (below). |
| Level and file | e2e: `test/e2e/v5_x02_test.go`. Real binary, real daemon spawned by a real `session-start` hook, real `observe tool` hook, real `qompack mcp` child over real stdio proxying to the daemon-side handlers. |
| Base | `verify/v5 @ 87c0c1d`, branch `v5/x02`. |

## Why e2e, not integration

The criterion's seam crosses two process boundaries — the hook client's capture admission
(`internal/cli`) into the daemon's durable capture (`internal/daemon` ingest → `store.WriteCaptureSidecar`),
and the `qompack mcp` stdio child into the daemon-side `expand`/`re_read` handlers. The historical §4.2 named the
same seam ("over real stdio"). An in-process rig (`v4StartRig`) would have bypassed exactly the client-side nonce
that turned out to be broken, so the historical seam was followed and it was the right call.

## Test names and what each asserts

One top-level test with sequential sections and one named subtest.

`TestV5_TombstoneToExpandRoundTrip`

1. **Capture record is real.** The daemon's own `index/tool_use.jsonl` line for the hook's `tool_use_id`, decoded in
   the file's real on-disk shape (`tuRec` keys), carries `path`, session, `FileRead`, `StatusOK`, `Ephemeral=false`, a
   non-zero root, and `bytes == len(delivered content)` (proof that canonicalization was identity for this body).
2. **SP-20 fidelity is recorded, not assumed.** The capture sidecar the daemon linked to that record
   (`records/captures/<shard>/<observation-id>.json`) has `fidelity == exact`, `outcome == ok`, no capture error, not
   redacted, not truncated, `published == true`, `root == record root`, the session, `host_fields ∋ tool_response`,
   and **`bytes` byte-identical to the exact payload the test wrote to the hook's stdin**.
3. **Tombstone.** `observer.Tombstone(rec)` matches
   `^\[cleared: sha256:[0-9a-f]{12}… · [\d.]+KB · FileRead src/auth\.ts · re-expandable\]$`; its short hash equals
   `rec.Root.Short()` and is a prefix of the full handle.
4. **Exact handle recovery over stdio (by `tool_use_id`).** Following `expand`'s own `next_span` cursor from offset 0
   until it hands back no cursor: every page is exactly the span it reports, pages are contiguous, no page exceeds
   `runtime.mcp.maxResponseBytes`, `truncated` equals `off>0 || end<total` on every page, the hash is constant,
   `_meta.qompack.{ephemeral,untrusted} == true`, `_meta.qompack.tool_use_id` is present (the handler's own testimony
   that it recorded its ephemeral result), the walk ends at `total_bytes`, more than one page was needed, `path` is
   carried, and the **concatenation equals the delivered 200 KB content byte for byte**.
5. **Exact handle recovery (by `sha256:` hash, `full:true`).** Same bytes, one page, `truncated == false`.
6. **No current substitution.** The working tree holds a different version of `src/auth.ts` throughout (asserted by
   reading it back); the expansion contains the historical marker and not the on-disk marker; `re_read` with empty
   `at` reports `source == "store"`, the capture's hash, and the capture's bytes.
7. **No historical substitution.** `re_read` of `src/never-captured.ts` — which exists on disk — reports
   `found:false`, `available:false`, a reason, and no content.
8. **No newest-version substitution.** A second capture of the same path (historical + tail) is indexed under a
   different root. `expand` of the first `tool_use_id` still returns the first delivery exactly; `expand` of the
   second returns the second exactly; unpinned `re_read` returns the newest capture (`source == "store"`); `re_read`
   pinned `at: sha256:<first root>` returns the first.
9. **§8.7 ephemerality.** At least one `eph:true` index record exists after the expansions, its id has the
   `qompack-mcp:` prefix, and `scheduler.DropClassOf(tool, ephemeral, superseded) == DropEphemeral`.

`TestV5_TombstoneToExpandRoundTrip/CorruptObjectIsHonestFailureNotWorkingTreeSubstitution` — the negative control
(below), followed by `p.AssertAppendOnly(t)` in the parent.

## Negative control and how it was proven

A real fault through a real switch, not a source edit: with the daemon stopped, the test opens the store, writes one
record whose stored path is `../outside-the-root/secrets.ts` (exactly as v4_x05 does), closes the store, and
**overwrites the first chunk object file of the captured root with garbage**. `FSStore.getObject` verifies size,
decoding and the content address before returning plaintext, so this is a fault the store detects and quarantines.
A fresh daemon and a fresh `qompack mcp` child are then started, while the working tree *still* holds a readable
file at the same path.

Asserted: `expand` by `tool_use_id` and by hash both report `found:false` (tool error or miss body accepted — both
are honest) and the response text contains neither the on-disk marker nor the historical marker; `re_read` of the
path never contains the on-disk marker and, if it answers at all, says `source == "store"`; `expand` of the escaped
record reports `denied:true`, `found:false`, and does not echo the path.

Why this is the strongest available proof: after the corruption the only bytes that could satisfy a "found" answer
for that path are the working tree's. An implementation that substituted a live read for a lost capture — the
defect the criterion forbids — would pass every positive assertion above and fail here.

Proof that the control bites (authoring probe, reverted): with the one-line edit
`for _, m := range matches[:0] {` (the corruption loop skipped, everything else identical) the subtest fails at
`NEGATIVE CONTROL: a handle whose object is corrupt must not report found` — the handle resolves and returns the
captured bytes, so the `found:false` assertions are load-bearing, not vacuous. With the loop restored the subtest is
green on both required runs. `git diff` after the revert is empty except this row's own files.

## Production defect found and fixed (own commit, `fix(ipc)`)

The hook client minted a 16-byte delivery nonce (`ipc.deliveryNonceBytes = 16` → 32 hex chars) while the daemon's
delivery journal admits only 64-hex tokens (`daemon.validDeliveryToken`) and re-validates persisted leases against
the same rule. Every live delivery was therefore refused a lease with `ErrContract` — the daemon logged
`delivery lease unavailable; identity is a gap` on every `observe.tool` — so **no capture sidecar was ever written on
the shipped path** and SP-20's publication order (M1-01/M1-02) was dead outside unit tests. Each side had pinned its
own width in its own tests (ipc: `Len 32`; cli: `Len 32`; daemon: 63 is "short", 64 valid) and no test crossed the
seam. First run of this test surfaced it (`records/captures` absent, leases file empty).

Fix: `deliveryNonceBytes` 16 → 32 (64 hex), the persistence-side contract being the one not to loosen; the two client
pins updated to 64 with the reason. Compatible: an absent nonce stays absent; an older 32-char spooled nonce behaves
exactly as before (no lease, a counted gap); no journal on disk changes meaning because production never leased.
Focused regression after the fix: `internal/daemon -run 'Delivery|Ingest|Drain|Capture|Sidecar|Publish|Lease|Admission|Gap'`
(60 tests by `-list`) ok; `internal/cli -run 'Capture|Hook|Nonce|Admission'` (29 tests by `-list`) ok; `internal/ipc` whole package ok
except `TestStateWriteIsAtomic`, which failed once with a Windows rename "Access is denied" while two other packages'
tests were running in parallel and passed alone on rerun — a co-load suspect unrelated to the nonce, not chased; e2e
`-run '^(TestV3_VerbatimPromptBecomesEliminationEvidence|TestE2E_ObserverThroughDaemon|TestE2ESpoolSubmodeEndToEnd|TestStdioServerEndToEnd|TestV4_T13HandleResolvesAfterCompactionOverStdio|TestV4_TombstoneToRecallToExpandRoundTrip|TestE2E_SupersessionVisibleAfterRestart)$'`
(7 rows) ok.

## Old-to-new assertion map

| Historical §4.2 expectation | Status | Reason |
|---|---|---|
| Observe one 200 KB `FileRead` of a file containing `refreshToken` | kept | `x02BodyBytes = 200*1024`, `refreshToken` deep in the body. |
| Tombstone matches the §8.1 regex | kept | Identical regex, path `src/auth.ts`. |
| Parse the `sha256:` short hash out of the tombstone; drive `expand {"hash": <full hash>}` over real stdio | kept (corrected) | Short form is asserted as the prefix of the full handle; the full hash comes from the index record because a 12-char short form is not an `expand` argument by contract. |
| `expand` returns a chunk-aligned span ≤ 16384 bytes containing the function | corrected | Chunk alignment is SP-13's unit contract (`mcp_e2e_test.go` already pins it via `mcpE2EChunkBoundaries`). The criterion is exactness: the test walks every page and asserts the concatenation is byte-identical, each page ≤ `runtime.mcp.maxResponseBytes`, contiguous, and the flag/cursor semantics. The "≤ 16384" literal is the config default and is not asserted as a constant. |
| `_meta.qompack.ephemeral == true` | kept | On every page; plus `untrusted` and the handler-published `tool_use_id`. |
| `qompack recall "symbol:refreshToken" --json` slash-command frontend, hit hash equals tombstone root | retired | The SP-14 `recall` command frontend is not part of the current 4.2 criterion (it moved to 4.1/4.5's SP-14 rows) and `recall`'s pointer identity is already asserted by v4_x05. Not re-asserted here to keep the row on its criterion. |
| A `store.ToolUseRecord` with `Ephemeral: true` exists for the expand, and `scheduler.DropClassOf` classifies it `DropEphemeral` | kept | Asserted from the daemon's own index file, decoded in its real shape. |
| (new, from current criterion) fidelity recorded by SP-20 | added | Sidecar `fidelity == exact` and `bytes == stdin payload`. |
| (new) no current / historical / newest-version substitution | added | Sections 6–8 above, each with a differing real artifact. |
| (new) authorized | added | Escaped-path record denied before materialization, in the negative-control subtest. |

## Unverified remainder

- **SP-13 does not yet consume SP-20's retrieval evidence envelope** (plan row "SP-13 consumes the M2 retrieval
  envelope and distinguishes unavailable from absence without changing its negotiated versions" is unchecked and
  `internal/mcp` references no `core.Fidelity`). `expand`/`re_read` responses therefore carry no fidelity field; this
  test proves fidelity from the store side (sidecar + byte identity) and proves the *absence* distinctions SP-13 does
  make (`available:false` vs miss vs `denied`). A future envelope-level assertion belongs here when that handoff lands.
- The `recall` slash-command frontend (historical) is not exercised — see map.
- Supersession of the first record by the second capture is not asserted: it is §8.1 item 3's chunk-superset /
  near-duplicate heuristic, not this criterion, and the on-disk supersession is a separate mutation line.
- `TestStateWriteIsAtomic` (ipc) co-load flake noted above; not this row's producer.

## Run command and result

```
cd C:/Users/Quant/Documents/Programming/Projects/qompack-v5-x02
go test ./test/e2e -list '^TestV5_TombstoneToExpandRoundTrip$'     # prints the one name
go test ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$' -count=1 -timeout=30m
```

Result: pass, twice in a row on the final tree (`ok … 7.223s`, then `-v`:
`--- PASS: TestV5_TombstoneToExpandRoundTrip (9.82s)` with
`--- PASS: …/CorruptObjectIsHonestFailureNotWorkingTreeSubstitution (0.94s)`). The `matches[:0]` probe above
was re-applied on the same tree, failed the subtest at
`NEGATIVE CONTROL: a handle whose object is corrupt must not report found` (exit 1), and was reverted before
commit. `gofmt -l ./test ./internal` empty; `go vet ./test/e2e ./test/integration ./internal/ipc ./internal/cli`
clean; `go run ./tools/devtool lint --only=nomagic,sleepcheck,testdeps,importgraph,runpatterns` PASS on all five.
On this final pass `internal/ipc` passed whole (`ok 37.041s`), including `TestStateWriteIsAtomic`.
