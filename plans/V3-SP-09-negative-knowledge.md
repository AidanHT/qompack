# SP-09: Phase 2 / L2 negative knowledge: canonical descriptors, evidence-linked eliminations, staleness flip, bloom-as-cache rebuilt from records, and the three-way response

> **Recommended model: Opus 5 · max effort**
>
> Elimination staleness is the one §12 risk rated **High**: a wrong `depends_on` flip inverts negative knowledge from the plugin's highest-value feature into a liability that blocks viable approaches. Descriptor canonicalization, the active/stale transition and bloom-rebuild-from-records all need to be right the first time.

**Branch:** `feat/sp09-negative-knowledge` (cut from `develop`) | **Wave:** 2 | **Prerequisites:** the branches of `["SP-01","SP-03","SP-06","SP-07"]` already merged into `develop` | **Runs in parallel with:** sibling subplans of wave 2 (SP-08 observer L0) | **Design sections:** §6.2, §8.3 (negative knowledge + staleness), §10 Phase 2, §12 (stale negative knowledge row), Appendix C `eliminations` | **Gaps closed:** G6.1

---

## Mission

This subplan delivers `internal/negknow` — the elimination ledger — and closes Phase 2 of the build plan. `Qompack.md` §8.3 calls this "the highest-value single feature in the plugin," and the closing note ranks it in the four things that matter if the plan has to be cut down. The failure it removes is the one users report most: after compaction the agent re-attempts an approach it eliminated forty minutes ago, burns time, and eliminates it again (G6.2). Negative knowledge is also the highest-Δ content in the transcript (G6.3) — a ruled-out branch eliminates an entire region of action space — and the current summary schema has no slot for it at all (G6.1).

The mechanism is a canonical descriptor `(normalized_path, symbol_or_null, approach_class, reason_hash)` inserted into a Bloom filter, over an append-only structured record log. The Bloom filter is immortal — a fixed-size bit array is not context, so it survives arbitrarily many compactions without DPI degradation — but it is **only a cache**. The load-bearing correction of §8.3 is that `records/eliminations.jsonl` is the source of truth, every elimination carries `depends_on` hashes of the files and configs its reason rests on, a dependency hash change flips the record to `stale`, and `tried.bloom` is rebuilt from **active records only** on the next idle window. Without that, a stale `already_tried` blocks a now-viable approach and inverts the feature from asset to liability — §12 rates that risk **High**. The three-way `already_tried` response (absent / active / stale-with-re-verification-note) is what keeps the "false positives are the safe direction" claim true.

**How G6.1 is closed, concretely.** G6.1 is "no schema slot for ruled-out approaches"; §9 attributes its closure to "L4 `eliminated[]` + Bloom". This subplan supplies both halves of that: `negknow.Record` **is** the typed schema slot, and its `MarshalJSON` emits exactly the §8.5 `eliminated[]` entry shape, so SP-10 embeds `[]negknow.Record` as checkpoint tier 1 with no transformation and no second schema; `tried.bloom`, keyed on the canonical descriptor and rebuilt only from active records, is the immortal membership half. The residual column in §9 reads "—" because nothing about the slot depends on the summarizer cooperating. The two neighbouring gaps are explicitly *not* this subplan's to close: G6.2 is closed by SP-13's `already_tried` standing instruction (SP-09 supplies the ledger it calls) and G6.3 by SP-15's Δ-scoring (SP-09 supplies the `KindElimination` DAG nodes it scores).

**What exists in the repo when you start.** `develop` carries all of wave 0 and wave 1. From SP-01: `internal/core` (`Hash`, `HashBytes`, `Dep`, `ChunkRef`, `Clock`, the sentinel errors), `internal/paths` (`Norm`, `Key`, `AppendOnly`, `CreateNew`, `WriteAtomic`, `Of`, `ReplaceBloom`, `HighestBloomBackupSeq`), `internal/config` (the full Appendix C schema including `EliminationsCfg` and `SketchesCfg`), `internal/logging`, `internal/obs`, `internal/testutil` (`NewProject`, `FakeClock`), and `testdata/golden/contracts/negknow/`. **Parts of `internal/negknow` are already real and are not this subplan's to re-derive.** The package ships six non-test files plus its `negknowtest` sibling, and every declaration in them is SP-01's: `doc.go`; `types.go` — `Scope`/`Status`/`SourceKind` with their constants, `Descriptor` with its frozen json tags, `Dep = core.Dep`, and a **fully implemented, golden-frozen `Descriptor.Key()`**; `record.go` — the `Record` struct with its frozen json tags; `answer.go` — `AnswerState` with `AnswerAbsent`/`AnswerActive`/`AnswerStale` and the `Answer` struct (`State`, `Record`, `Note`, `BloomOnly`); `detector.go` — the `Detector` interface (`Scan(ctx, dag.Graph, core.TurnIndex) ([]Record, error)`), declared deliberately **without** a constructor, which is the one SP-09 adds; `ledger.go` — `Deps` (`Store`, `Graph`, `Log`, `Metrics`, `Clock`), `Health` (`Records`/`Active`/`Stale`, `FillRatio`/`EstFPRate`, `NeedsResize`), the ten-method `Ledger` interface, and `Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error)`. Three shipped test files pin parts of that and are not this subplan's to rewrite: `key_test.go` (`fixedDescriptor`, `wantDescriptorKeyHex`, `TestDescriptorKey_Stable` and two siblings), `fixture_test.go` (`TestRecord_RoundTripsFrozenEliminationFixture`, `TestRecord_MatchesCheckpointFixtureEliminatedEntry` — the two frozen fixtures), and `types_test.go`. **What is still a stub is narrower than "`Open` is unimplemented", and the difference is load-bearing:** `Open` itself already **succeeds** — it returns `stubLedger{}, nil`, which is why a wave-0 composition root can hold a live `negknow.Ledger` today — and it is the unexported `stubLedger`'s methods that report `core.ErrNotImplemented`, all of them but `Health`, which returns the zero `Health`. SP-09 replaces `stubLedger` and gives `Open` a real body; it does not change `Open`'s signature or its "constructing always succeeds" contract, which SP-05's daemon already relies on. `Canonicalize` is the other stub — it returns the zero `Descriptor`, and its body is replaced in place in `types.go`. In `negknowtest`, `suite.go`'s `RunLedgerSuite` already runs its `/shape` block against the stub and then `skipIfStub` — probing with `Query` — calls `t.Skip(ruleW1SkipMsg)` before the `/behaviour` block; `behaviour.go` ships all three behaviour cases written and unrun (`three_way_absent_active_stale`, `bloom_rebuilt_from_active_records_only`, `bloomonly_consistency`). The unskip is what SP-09 *earns*, not what it writes, and `ruleW1SkipMsg`'s literal is greped by `devtool lint`'s `stubskips` sub-check, so it may not be paraphrased. From SP-03: a real `internal/sketch` with `Bloom`, `NewBloom`, `RebuildBloom(capacity, fpRate, keys iter.Seq[[]byte])`, `FillRatio`, `EstimatedFPRate`, `ResizeTarget`, `Save`, `Load`, **`LoadWithLog`** (the form a composition root must call) and **`ReplaceGenerational`** (the only sanctioned writer of `sketches/tried.bloom`, §3.3). From SP-06: a real `internal/store` with `ChangedSince([]core.Dep)`, `FileHistory`, `PutBytes`, and the CRC-checked object store. From SP-07: a real `internal/dag` with `AddNode`, `AddEdge`, `Node`, `In`, `Out`, `NodesAfter`, `BackwardSlice`, and the `NodeID` constructors every consumer must build ids through.

**What exists when you finish.** `internal/negknow` is complete and its `negknowtest` skips are all removed. The ledger records eliminations from all four §8.3 sources, answers `already_tried` three ways, flips records stale from `store.ChangedSince`, rebuilds `sketches/tried.bloom` from active records only, and never returns a membership answer that is not backed by a record lookup or explicitly flagged `BloomOnly`. SP-13 (`already_tried`, `record_eliminated`), SP-14 (`/qompack:pin --eliminated`), SP-10 (checkpoint tier-1 `eliminated[]`), SP-11 (the eliminations digest) and SP-16 (project-scope carry-forward) all call this ledger rather than reimplementing any of it. A Phase 2 replay assertion in `test/replay/` proves the exit criterion.

---

## Design context (verbatim from Qompack.md)

### §6.2 — Bloom filters and sketches for permanent memory

> **Closes:** G6.1, G6.2, G6.3, G2.2
>
> A Bloom filter over canonicalized attempted-approach descriptors costs ~12KB for 10,000 entries at 1% false positive rate. False positives are the *safe* direction: you skip something you might have retried.
>
> The property that matters: **it is immortal.** A fixed-size bit array is not context, so it survives arbitrarily many compactions without degradation. The DPI cascade does not touch it because it was never compressed.

| Sketch | Purpose | Size |
|---|---|---|
| Bloom | `already_tried(x)` membership | ~12KB / 10K entries @ 1% FP |
| Count-Min | File-touch frequency (which files are hot) | ~54KB @ ε=0.001, δ=0.01 |
| HyperLogLog | Breadth-of-exploration cardinality | ~2KB, 2.3% error |
| Misra-Gries | Deterministic top-k with no false positives | O(k) |

### §8.3 — Negative knowledge extraction (L2 Analyzer)

> **Negative knowledge extraction.** The plugin must recognise and canonicalize elimination events. Sources, in descending reliability:
>
> 1. Explicit agent declaration via the MCP tool `record_eliminated(target, approach, reason)`
> 2. Slash command `/qompack:pin --eliminated`
> 3. Heuristic detection: a test-fail → revert → different-approach pattern in the DAG
> 4. Explicit user statements ("that didn't work," "we tried that")
>
> Canonical descriptor: `(normalized_path, symbol_or_null, approach_class, reason_hash)`. Insert into `tried.bloom`. This is the highest-value single feature in the plugin, and it is roughly 50 lines.

### §8.3 — Staleness

> **Staleness — negative knowledge must be able to expire.** An elimination is a *conditional* fact: "widening the pool timeout doesn't work **given pgbouncer 1.18**." Upgrade pgbouncer and the elimination is void — and a false `already_tried` that blocks a now-viable approach inverts the feature from asset to liability. Standard Bloom filters cannot delete, so the design is:
>
> 1. **The structured `eliminated[]` records are the source of truth; the Bloom filter is only a cache over them.** This was implicitly true; it is now load-bearing.
> 2. Every elimination carries `depends_on`: the hashes of the files or configs the elimination's reason rests on (lockfiles, compose files, the file under test). The Observer already tracks file-version history, so detecting a change to any dependency is a hash comparison it performs anyway.
> 3. When a dependency hash changes, the elimination flips to `status: "stale"`. On the next idle window, `tried.bloom` is **rebuilt from active records only** — cheap, because rebuild is a linear pass over a few thousand structured entries.
> 4. `already_tried` responses distinguish the cases: *active* returns the reason; *stale* returns "previously eliminated, but the evidence has changed since — re-verification may be warranted," which is strictly more useful to the agent than either a block or silence.
> 5. `scope: "session" | "project"` controls cross-session carry-over: session-scoped eliminations ("this test is flaky today") die with the session; project-scoped ones ("this library fundamentally can't do X") persist and warm-start future sessions (§10 Phase 7).
>
> The earlier claim that Bloom false positives are "the safe direction" is hereby scoped: it holds only while evidence is current. Staleness handling is what keeps it true.

### §8.5 — the `eliminated[]` record shape (tier 1, never truncated)

```jsonc
  "eliminated": [                        // negative knowledge, structured
    { "target": "src/auth.ts:refreshToken",
      "approach": "widen pool timeout",
      "reason": "pgbouncer 1.18 ignores it in transaction mode",
      "evidence": "sha256:…",
      "depends_on": [                     // staleness guard (§8.3):
        { "path": "docker-compose.yml", "hash": "sha256:…" },
        { "path": "package-lock.json",  "hash": "sha256:…" }
      ],
      "scope": "project",                 // "session" | "project"
      "status": "active" }                // "active" | "stale"
  ],
```

### §8.7 — the two MCP tools this ledger backs, and the standing instruction

| Tool | Signature | Purpose |
|---|---|---|
| `already_tried` | `(target, approach)` → bool + reason | Bloom membership plus the stored reason when present |
| `record_eliminated` | `(target, approach, reason)` | Write negative knowledge |

> **Design note:** `already_tried` should be surfaced in the rehydrated context as a *standing instruction*, not merely an available tool. "Before committing to an approach, call `already_tried`." Otherwise the affordance exists and goes unused.

### §8.6 item 3 — what the rehydrator asks this ledger for

> 3. **Eliminated-approaches digest** — the top-N most relevant by slice score, plus a note that `already_tried()` covers the rest

### §7.4 — the append-only invariant

> **Invariant:** files under `checkpoints/`, `pins/`, and `sketches/tried.bloom` are **append-only or additive**. Nothing in the system rewrites them from a summary. This is the mechanical enforcement of §4.6.

Directory-layout lines this subplan owns or writes to:

```
├── sketches/
│   ├── tried.bloom                # negative knowledge — NEVER regenerated
```

### §10 Phase 2 — Negative knowledge

> - Bloom filter, canonical descriptor scheme
> - Evidence-linked eliminations (`depends_on` hashes), staleness flip, rebuild-from-records (§8.3)
> - `record_eliminated` and `already_tried` MCP tools, including the three-way active/stale/absent response
> - Standing instruction in rehydrated context
> - Count-Min and HLL companions
>
> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

### §11.2, §11.4 — the metrics that grade this slice

| Metric | Definition |
|---|---|
| Redundant work rate | Re-reads of already-read files; re-attempts of eliminated approaches |

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

### §12 — the two risk rows this slice owns

| Risk | Severity | Mitigation |
|---|---|---|
| Bloom saturation | Low | Monitor fill ratio; resize with a rebuild from `eliminated[]` in checkpoints |
| **Stale negative knowledge blocks a now-viable approach** | High | Evidence-linked eliminations with `depends_on` hashes; Bloom rebuilt from active records on dependency change (§8.3). This risk is why the filter is a cache, never the source of truth. |

### §9 — traceability rows

| Gap | Closed by | Residual |
|---|---|---|
| G6.1 no slot for eliminations | L4 `eliminated[]` + Bloom | — |
| G6.2 re-attempt loop | L6 `already_tried` standing instruction | — |
| G6.3 highest-Δ content dropped | L2 Δ-scoring prioritises it | — |

### Appendix A — Bloom filter sizing (the math SP-03 implements, quoted so the capacity choice here is checkable)

```
m = −n·ln(p) / (ln 2)²          k = (m/n)·ln 2
n = 10_000, p = 0.01  →  m ≈ 95_850 bits ≈ 12 KB, k = 7
```

### Appendix C — the configuration block this subplan reads, verbatim

```jsonc
  "sketches": {
    "bloom": { "capacity": 10000, "fpRate": 0.01 },
    "cms":   { "epsilon": 0.001, "delta": 0.01, "warmStartFromProject": true },
    "hll":   { "registers": 2048 }
  },
  "eliminations": {
    "requireEvidence": true,
    "defaultScope": "session",
    "rebuildOnStale": "nextIdle",
    "staleResponse": "flag"          // "flag" | "drop"
  },
```

Validation rules for these keys, from 00-ARCHITECTURE §11.3: `bloom.capacity ≥ 100`, `fpRate ∈ (0, 0.25)`; `defaultScope ∈ {session, project}`; `rebuildOnStale ∈ {nextIdle, immediate, never}`; `staleResponse ∈ {flag, drop}`.

### 00-ARCHITECTURE §12.3 — the two degradation rows this slice implements

| Failure | Response |
|---|---|
| bloom load fails | rebuild from `records/eliminations.jsonl` (§3.3); if that fails, `already_tried` returns `absent` for everything — never a false positive |

### 00-ARCHITECTURE §3.3 — the tried.bloom replacement rule, verbatim

> `sketches/tried.bloom` may be *replaced* only by `negknow.RebuildBloom`, whose input is `records/eliminations.jsonl` filtered to `status:"active"` — never a checkpoint, never a summary, never context. The rebuild writes a new file and renames; the previous file is kept as `tried.bloom.<seq>.bak` for one generation.

### 00-ARCHITECTURE §13 invariant 3

> **The bloom filter is a cache, never the source of truth (§8.3).** Every membership answer is backed by a record lookup or explicitly flagged `BloomOnly`.

### Inherited constraints from wave 1 (binding on this subplan, **not** quoted from Qompack.md)

Three wave-1 obligations bind this subplan. **Items 1 and 2** are rows of `plans/CARRIED-DEFECTS.tsv`
still marked `deferred:V3-VERIFY`; they compound as this ledger is built on top of them, because
both bear on the two hashes a `Record` does **not** mint itself — `Evidence` and every
`DependsOn[].Hash` — and on the staleness flip that decides whether an elimination still blocks. The
authoritative status of each is its row in `plans/CARRIED-DEFECTS.tsv` — not this file — with the
diagnosis and acceptance criteria in `plans/V2-SP-04-carried-defects.md` and
`plans/V2-WAVE1-carried-defects.md`; V3-VERIFY §0a item 5 owns resolving or consciously re-deferring
them. **Item 3** has no TSV row at all: it is an explicit carry SP-07 addressed to SP-09 in
`plans/V2-SP07-handoff.md` §INHERIT and V3-VERIFY §0a item 1 restates, and it is reproduced here
because `plans/README.md` points the implementing session at this subplan file and nowhere else.
What follows is not background reading. Each item is a constraint on what this subplan may do.
(The fourth compounding TSV row, SP04-D5, is a canonicalization-rule budget on SP-08's L0 hot path;
`negknow` adds no `canon` rule and does not import `canon`, so it does not bind here.)

1. **SP04-D2 + SP04-D3 — canonicalization is not a stable contract yet, and the hashes this ledger
   records against it are append-only.** SP04-D2: canonicalization is not idempotent when a deletion
   joins two fragments into a value neither half contained. SP04-D3: one timestamp edge remains of the
   original three — a word byte immediately after an ISO timestamp defeats the rule. Both are deferred
   by decision rather than by oversight: the complete fix changes the `canon.Delta` contract that
   SP-06's content-addressed store stores against, and it is legal only under fixed-point composition,
   so D3 travels with D2. The evidence test is `TestKnownDeletionMediatedLimit`, whose third row is the
   BOM counterexample (commit 666b120). **The constraint on SP-09:** anything this subplan persists or
   content-addresses must stay re-derivable, or explicitly versioned, across a canonicalization change,
   and no canonical-form hash may be baked into a durable identity, an equality check, or a descriptor
   key it cannot recompute once V3-VERIFY lands the fix. Two fields are exactly that shape and neither
   is minted here: `Evidence`, the root of the reason text `IngestMCP` puts through `store.PutBytes`,
   and every `DependsOn[].Hash`, which `resolveDeps` takes from the newest `store.FileHistory` entry —
   both addresses over the *canonical* bytes, written into an append-only log (§7.4) that outlives the
   canonicalizer that produced them. The equality check is the sharp end: `store.ChangedSince` is
   exactly hash inequality against the newest recorded version, so once canonicalization changes, the
   first version appended for a dependency after the fix re-derives a different root from identical
   bytes and `RefreshStaleness` flips an active record stale on evidence that did not change. Either
   the dep baseline stays re-derivable, or the record carries the canonicalizer generation it was minted
   under and the staleness comparison declines to flip on that axis alone. **The second of those is not
   free, and this plan prices it rather than leaving it as an open hatch.** `Record`'s json tags are
   frozen and normative (§5.10), and two byte-frozen fixtures transcribe that declaration —
   `testdata/golden/contracts/negknow/want/elimination_record.jsonl` and
   `testdata/golden/contracts/checkpoint/want/0001.json`'s `eliminated[0]` — so a generation field is
   not a field this subplan may add. If it is ever added, exactly one shape is admissible: a trailing
   additive key tagged `omitempty` whose zero value means *unversioned*, the same shape `stale_since`
   and `stale_because` already have, so a record minted before the fix still marshals to the frozen
   line byte-for-byte and neither fixture is touched. Even then it needs an `arch/` amendment to §5.10
   landed on `develop` first, because §5.10's declaration is what those fixtures are transcribed from.
   **SP-09 therefore takes the first branch and only the first branch:** the dep baseline stays
   re-derivable, `RefreshStaleness` compares nothing it cannot recompute, and no generation field is
   minted in wave 2. The hatch is recorded here as the option available to whoever lands the D2/D3
   fix — it travels with that change and its amendment, not with this subplan. `Descriptor.Key()` and
   `MatchKey()` are not affected — they hash this package's own canonicalization (`ApproachClass`,
   `reasonHash`) and the bloom is rebuilt from records regardless — and that is not a licence to treat
   the store's hashes as equally stable.

2. **SP05-D1 — the IPC drain is not lossless on abort, so the file-version history this ledger reads
   may be missing an edit.** A drain aborted by idle-budget expiry consumes the line it interrupted:
   the file offset is advanced and the seen-set committed before the binding completed, so the event is
   lost. The deliberate poison-line-consume rule cannot distinguish a dying drain from a refusing
   handler, and separating them needs seen-set rollback, or post-dispatch commit plus a retry cap — a
   re-adjudication of an adjudicated rule rather than a surgical fix. **The constraint on SP-09:** the
   staleness path may not be written as though it sees every change. `AppendFileVersion` has exactly one
   caller in the finished system — SP-08's observer, on the far side of that drain — and
   `store.ChangedSince` skips a path with no history at all, counting it rather than reporting it. A
   lost `PostToolUse` therefore does not manufacture a false flip; it produces a **missed** one, leaving
   an elimination `active` whose evidence has in fact changed. That is the §12 High-severity direction —
   "stale negative knowledge blocks a now-viable approach" — and it is the one case this subplan may not
   file under "false positives are the safe direction," because §8.3 scopes that claim to *while
   evidence is current*. Concretely: `RefreshStaleness` must stay re-runnable and idempotent over the
   same deps, so a later window can catch what an earlier one could not see (its re-flip semantics
   already are — do not optimize them into a checked-once cache); a record's `depends_on` baseline must
   remain re-checkable in a later session rather than only at record time; and the Phase 2 exit
   criterion's "zero stale-block incidents" is a statement about the ledger given the events it
   received, which the replay assertion in `test/replay/` should say rather than imply.

3. **INHERIT (SP-07 → SP-09) — the dependence graph is not acyclic, and this ledger's detector must
   terminate on it anyway.** D-7 forbids exactly one cycle (an assistant consuming the result of the
   tool use it emitted); the **whole graph is not a DAG and cannot be**, because §8.1 item 4 directs a
   shared-file edge *into* a tool use that consumed a file and *out of* one that produced it, so the
   ordinary Read-then-Edit pattern closes a legal loop:
   `tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1`. Every edge there is
   individually correct, and `TestReadThenWriteClosesALegitimateCycle`
   (`internal/dag/builders_test.go`) pins it so it cannot be assumed away. `plans/V2-SP07-handoff.md`
   §INHERIT carries the consequence verbatim: "**SP-09's negative-knowledge detector scans this same
   graph for the test-fail → revert → different-approach pattern and inherits the same obligation.** A
   naïve recursive descent over these edges will not terminate. This is stated in ADR 0007; it is
   repeated here because SP-09 is in a later wave and will not otherwise see it." Slicing survives by
   construction — scores strictly decrease along any path, each node finalizes once, and the `minScore`
   floor bounds the walk — and the detector needs its own equivalent argument. **The constraint on
   SP-09:** the detector's graph access stays the **bounded one-hop `g.In`/`g.Out` walk** the
   `detector.go` section below specifies, or else carries an **explicit visited set**; no recursive
   descent over graph edges may appear anywhere in `internal/negknow`, and the termination argument is
   written down next to the walk rather than left implicit. `TestDetector_TerminatesOnCyclicGraph`
   pins it against a cyclic fixture built on the shape above. V3-VERIFY §0a item 1 owns the
   verification and states its form — "this checkpoint must verify it against a cyclic fixture, not
   only against the acyclic synthetic generator" — so an implementation that argues termination
   without such a fixture has not discharged the obligation.

---

## Out of scope

Implement only `internal/negknow`, its tests, its conformance-suite unskip, one `test/e2e` file, one `test/replay` file, and one ADR. Everything below belongs to a sibling:

| Item | Owner |
|---|---|
| `sketch.Bloom` internals, the Appendix A sizing math, `FillRatio`, `EstimatedFPRate`, `ResizeTarget`, `sketch.RebuildBloom`, `Save`/`Load`/`LoadWithLog`, the internals of `sketch.ReplaceGenerational` and `paths.ReplaceBloom`/`HighestBloomBackupSeq` (the §3.3 generational swap, its pre-flight and its one-generation prune), the versioned CRC header | **SP-03** |
| The Count-Min and HyperLogLog "companions" named in the Phase 2 bullet list — the types themselves | **SP-03** |
| Feeding Count-Min and HyperLogLog from `PostToolUse` (§8.1 item 5: "Feed the Bloom filter *only* on explicit negative-knowledge events") | **SP-08** |
| `store.ChangedSince`, `FileHistory`, `FileAt`, `AppendFileVersion`, `PutBytes`, object layout, GC | **SP-06** |
| `redact.Redactor` and its application at `store.Put` | **SP-06** |
| DAG node/edge model, persistence, `BackwardSlice`, `CrossingEdges`, `Compact` | **SP-07** |
| Emission of `EdgeConsumes`/`EdgeProduces`/`EdgeSharedFile` edges from real tool uses; `observer.Signals`; **any file under `internal/observer`** | **SP-08** (sole writer, §5.21) |
| Calling `RefreshStaleness` from the `SessionStart` startup/resume branch — in `internal/daemon/observer_ops.go`, around SP-08's `OnSessionStart`, never inside `internal/observer` (see the note under this table) | **SP-08** |
| Checkpoint tier-1 embedding of `[]negknow.Record` into `eliminated[]`, `Truncate`, importance ordering | **SP-10** |
| The rehydrator's eliminations digest rendering, `StandingInstruction()` text, the 8-item injection order, the top-N budget key `runtime.rehydrate.eliminationsTopN` | **SP-11** |
| Registering `MaintenanceTask` with `daemon.IdleController`; the `BackgroundTask("rebuild_bloom")` scheduling decision; idle-gap detection | **SP-12** |
| MCP `already_tried` / `record_eliminated` tool registration, JSON-RPC transport, `_meta` ephemeral tagging, `AlreadyTriedResult` marshalling | **SP-13** |
| `/qompack:pin --eliminated` command frontend, `--json` output, `/qompack:status` rendering of ledger health | **SP-14** |
| Δ-scoring, submodular selection, redundancy detection | **SP-15** |
| Cross-session warm start: carrying `scope:"project"` eliminations forward at a fresh `SessionStart`, seeding priors | **SP-16** |
| The replay harness, `eval.Synthesize`, the 24-session corpus, `Belady`, the `replay-gate` CI job, the 2% no-regression rule | **SP-02** (SP-09 adds exactly one new test file that consumes `eval.Harness`; it edits none of SP-02's files) |
| Security audit of free-text elimination fields | **SP-17** |
| `docs/user-guide.md` "how to record and query eliminations" | **SP-18** |

**The one row above that must not stay implicit — `RefreshStaleness`'s production caller.**
`RefreshStaleness` is the mechanism behind the §12 High-severity staleness row this plan carries at
its head, and this table hands its *only* wave-2 production caller to SP-08. Precisely what SP-08
owes: in `internal/daemon/observer_ops.go` — the one file SP-08 adds outside its own package —
`WireObserver` binds `s.SessionStart` from `obsv.OnSessionStart`; that binding wraps the call and,
when the daemon's `Services.Ledger` is non-nil, invokes `s.Ledger.RefreshStaleness(ctx, s.Store)`
on the `startup`/`resume` branch before returning the observer's output, tolerating a nil ledger and
a nil store silently (both are legitimate in a stub build). It cannot live in `internal/observer`:
SP-08's own Done checklist forbids that package importing `negknow` at all, so `observer_ops.go` is
the only sanctioned seam. The gate is a `TestWireObserver_SessionStartRefreshesStaleness` row beside
SP-08's existing `TestWireObserver` in `internal/daemon/observer_ops_test.go`, plus a V3-VERIFY row
asserting the refresh happens **through the daemon path with the ledger bound** — V3-VERIFY's X2
drives `led.RefreshStaleness(ctx, st)` from the test body, so without that row a green wave-2
checkpoint proves nothing about whether a production caller exists at all. The idle-driven caller is
a separate, later thing — the `MaintenanceTask` registration row above, owned by SP-12 in wave 3, so
it cannot stand in for this one. **If SP-08 declines this**, the row is wrong as written and wave 2
closes with a flip nothing calls: change its owner cell to **SP-12**, and record the deferral in
V3-VERIFY §0a rather than leaving the two documents to disagree silently.

---

## Interface contract

### Consumes (exact signatures, unchanged)

```go
// internal/core (SP-01, §4)
type Hash [32]byte
func (h Hash) String() string        // "sha256:" + hex
func (h Hash) Short() string         // first 12 hex chars
func ParseHash(s string) (Hash, error)
func HashBytes(domain string, b []byte) Hash   // sha256(domain || 0x00 || b)
type SessionID string
type ToolUseID string
type TurnIndex int
type UnixMilli int64
type Dep struct{ Path string; Hash Hash }      // path is paths.Key form
type Clock interface{ Now() time.Time; Since(time.Time) time.Duration }
func SystemClock() Clock
var ErrNotFound = errors.New("qompack: not found")
var ErrAppendOnly = errors.New("qompack: append-only violation")

// internal/paths (SP-01, §3.3, §4)
func Norm(projectRoot, p string) (string, error)
func Key(p string) string
func AppendOnly(p string) (io.WriteCloser, error) // O_WRONLY|O_APPEND|O_CREATE; ErrAppendOnly on O_TRUNC
func CreateNew(p string, b []byte) error          // O_EXCL create-and-write
func WriteAtomic(p string, b []byte, perm fs.FileMode) error
func Of(root string) Layout                       // Layout.Records, Layout.Sketches, Layout.Tmp
func ReplaceBloom(l Layout, b []byte, seq int) error                  // §3.3; reached via sketch.ReplaceGenerational
func HighestBloomBackupSeq(l Layout) (seq int, ok bool, err error)    // where a restarted rebuild counter resumes

// internal/config (SP-01, §5.1, Appendix C)
type EliminationsCfg struct {
    RequireEvidence bool   `json:"requireEvidence"`
    DefaultScope    string `json:"defaultScope"`   // "session" | "project"
    RebuildOnStale  string `json:"rebuildOnStale"` // "nextIdle" | "immediate" | "never"
    StaleResponse   string `json:"staleResponse"`  // "flag" | "drop"
}
type BloomCfg struct{ Capacity int `json:"capacity"`; FPRate float64 `json:"fpRate"` }
// The Go field is FPRate, not FpRate: cfg.Sketches.Bloom.FPRate is what compiles
// (internal/config/config.go), and it is the spelling V3-VERIFY's X2 setup uses too.

// internal/sketch (SP-03, §5.7)
func NewBloom(capacity int, fpRate float64) *Bloom
func (b *Bloom) Add(key []byte)
func (b *Bloom) Test(key []byte) bool
func (b *Bloom) Count() int
func (b *Bloom) FillRatio() float64
func (b *Bloom) EstimatedFPRate() float64
func (b *Bloom) Capacity() (n int, fp float64)
func (b *Bloom) MarshalBinary() ([]byte, error)
func (b *Bloom) UnmarshalBinary([]byte) error
func RebuildBloom(capacity int, fpRate float64, keys iter.Seq[[]byte]) *Bloom
func (b *Bloom) ResizeTarget() (capacity int, fp float64, needed bool)
func Load(p string, s Sketch) error                                  // logs nothing — NOT for this call site
func LoadWithLog(p string, s Sketch, log logging.Logger) error       // the form negknow.Open must call
func ReplaceGenerational(p string, s Sketch, seq int) (backup string, err error) // the ONLY writer of tried.bloom
const TriedBloomBase = "tried.bloom"
var ErrCorrupt = errors.New("qompack/sketch: CRC32C mismatch")       // bit rot, distinguishable from a cold start

// internal/store (SP-06, §5.8)
ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error)
FileHistory(ctx context.Context, path string) ([]FileVersion, error)
PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
type FileVersion struct{ TS core.UnixMilli; Root core.Hash; Turn core.TurnIndex; Bytes int64 }

// internal/dag (SP-07, §5.9)
AddNode(n Node) error
AddEdge(e Edge) error
Node(id NodeID) (Node, bool)
In(id NodeID) []Edge
Out(id NodeID) []Edge
NodesAfter(pos int) []Node
type Node struct{ ID NodeID; Kind NodeKind; Turn core.TurnIndex; TS core.UnixMilli; Pos int; Ref string; Root core.Hash; Tokens core.Tokens; Ephemeral bool }
type Edge struct{ From, To NodeID; Kind EdgeKind; Weight float32; Turn core.TurnIndex }
// NodeKind members used here: dag.KindFile, dag.KindElimination
// EdgeKind members used here: dag.EdgeExplains, dag.EdgeConsumes, dag.EdgeSharedFile
// The NodeID constructors — the ONLY way this package may build a NodeID (SP-07 D-2):
func FileNode(pathKey string) NodeID           // "file:<sanitized pathKey>"
func ToolUseNode(id core.ToolUseID) NodeID     // "tooluse:<sanitized id>"
func EliminationNode(recordID string) NodeID   // "elimination:<sanitized record id>"

// internal/logging, internal/obs (SP-01, §5.2)
type Logger interface{ With(...any) Logger; Debug(string, ...any); Info(string, ...any); Warn(string, ...any); Error(string, ...any); Loud(string, ...any) }
func Nop() Logger
type Histogram interface{ Observe(d time.Duration); Snapshot() HistSnapshot; Reset() }
type Registry interface {
    Hist(name string) Histogram
    Counter(name string) Counter
    Gauge(name string) Gauge
    Snapshot() Snapshot
    CheckBudgets(cfg config.Config) []BudgetBreach
}

// internal/eval (SP-02, §5.18) — consumed only by test/replay/phase2_negknow_test.go
type Harness interface {
    Load(dir string) ([]Session, error)
    Replay(ctx context.Context, s Session, p Policy, o ReplayOptions) (Run, error)
    Compare(uncompacted, compacted Run) Divergence
    Belady(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
    ScoreRun(r Run, opt map[core.TurnIndex]KeepSet) Score
    Report(ctx context.Context, scores map[string][]Score) (Report, error)
}
type Policy interface {
    Name() string
    KeepSet(ctx context.Context, s Session, at core.TurnIndex, budget core.Tokens) (KeepSet, error)
}
```

**The `obs` instrument methods, resolved against the tree — not an assumption.** 00-ARCHITECTURE §5.2 names the `Counter` and `Gauge` *types* but not their methods, so the shipped ones are transcribed here and are what every call site in this file means: `internal/obs/counter.go` declares `type Counter interface{ Add(n int64); Value() int64 }` and `type Gauge interface{ Set(v int64); Add(d int64); Value() int64 }`. There is **no `Counter.Inc()`**, and `Gauge.Set` takes an `int64`, not a `float64`. Every `count(...)` helper in this file therefore expands to `l.m.Counter(name).Add(1)`, and every gauge write passes an `int64`. A ratio that must be reported (fill ratio, estimated FP rate) is reported through `Health()` and the log, never through a `Gauge`. `internal/obs` is off limits under Rule W-3 and nothing in it needs to change.

**Two nodes on the `NodeID` constructors.** Every `dag.NodeID` this package produces is built with `dag.FileNode` / `dag.ToolUseNode` / `dag.EliminationNode`, never by concatenating a prefix onto a key. SP-07 makes that mandatory for consumers ("Every consumer builds IDs through these"), and `internal/dag/nodeid.go` gives the reason: the constructors run `sanitizeKey`, which replaces control bytes and invalid UTF-8 with `_` and folds any key over 384 bytes to `head[:360] + "~" + 12 hex`. A concatenated spelling of a long or control-bearing path key parses, is accepted by `AddNode`, and silently produces two disconnected halves of the same graph — with no error anywhere. A test asserts the ids this package emits equal the constructors' output for a >384-byte path key.

### Produces (what later subplans rely on)

Everything in 00-ARCHITECTURE §5.10 is implemented exactly as declared; the additions below are new symbols on types this subplan owns, permitted by §5 ("a subplan may add methods to a struct it owns") and by W-3 (no other subplan's interface is touched).

```go
package negknow

// ── §5.10 verbatim, now real ──────────────────────────────────────────────
type Scope string      // ScopeSession = "session"; ScopeProject = "project"
type Status string     // StatusActive = "active";  StatusStale = "stale"
type SourceKind uint8  // SourceMCP, SourceSlashCommand, SourceHeuristic, SourceUserStatement

type Descriptor struct {
    NormalizedPath string `json:"normalized_path"`
    Symbol         string `json:"symbol"`
    ApproachClass  string `json:"approach_class"`
    ReasonHash     core.Hash `json:"reason_hash"`
}
func Canonicalize(target, approach, reason string) Descriptor  // SP-01 stub body; SP-09 fills it in place
func (d Descriptor) Key() []byte      // ALREADY SHIPPED AND FROZEN by SP-01 — do not re-derive, see below
// Descriptor carries its own JSON codec so ReasonHash renders as "sha256:<hex>" whether the
// descriptor is marshalled standalone (SP-13) or nested inside a Record (SP-10), and so a missing
// or null `symbol` decodes to "". The emitted bytes are identical to the shipped struct tags.
func (d Descriptor) MarshalJSON() ([]byte, error)
func (d *Descriptor) UnmarshalJSON(b []byte) error

type Dep = core.Dep

// §5.10 field set, unchanged; JSON tags per the wire form below.
type Record struct {
    ID           string         `json:"id"`
    Session      core.SessionID `json:"session"`
    TS           core.UnixMilli `json:"ts"`
    Target       string         `json:"target"`
    Approach     string         `json:"approach"`
    Reason       string         `json:"reason"`
    Desc         Descriptor     `json:"descriptor"`
    Evidence     core.Hash      `json:"evidence"`
    DependsOn    []Dep          `json:"depends_on"`
    Scope        Scope          `json:"scope"`
    Status       Status         `json:"status"`
    StaleSince   core.UnixMilli `json:"stale_since,omitempty"`
    StaleBecause []string       `json:"stale_because,omitempty"`
    Source       SourceKind     `json:"source"`
}
func (r Record) MarshalJSON() ([]byte, error)
func (r *Record) UnmarshalJSON(b []byte) error

type AnswerState uint8 // AnswerAbsent, AnswerActive, AnswerStale
type Answer struct{ State AnswerState; Record *Record; Note string; BloomOnly bool }

type Ledger interface {
    Record(ctx context.Context, r Record) (string, error)
    Query(ctx context.Context, target, approach string, scope Scope) (Answer, error)
    Get(ctx context.Context, id string) (Record, error)
    Active(ctx context.Context, scope Scope) ([]Record, error)
    All(ctx context.Context) ([]Record, error)
    MarkStale(ctx context.Context, ids []string, because []string) error
    RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
    RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
    Health() Health
    Close() error
}
type Health struct{ Records, Active, Stale int; FillRatio, EstFPRate float64; NeedsResize bool }
func Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error)

type Detector interface {
    Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error)
}

// ── additions owned by SP-09 ──────────────────────────────────────────────
type Deps struct {
    Store   store.Store              // nil ⇒ staleness refresh and dep resolution are skipped
    Graph   dag.Graph                // nil ⇒ no DAG nodes/edges are emitted
    Session core.SessionID
    Redact  func([]byte) []byte      // optional; supplied by the composition root (no import edge)
    Log     logging.Logger
    Metrics obs.Registry
    Clock   core.Clock
}

// StaleNote is the §8.3 item 4 text, verbatim and exported so exactly one copy exists.
const StaleNote = "previously eliminated, but the evidence has changed since — re-verification may be warranted"

func ApproachClass(approach string) string      // the closed stopword/synonym canonicalizer
func SplitTarget(target string) (path, symbol string)
func CanonicalizeAt(root, target, approach, reason string) (Descriptor, error) // Norm, then Canonicalize
func (d Descriptor) MatchKey() []byte           // 3-field, reason-independent; the key Query tests
func (d Descriptor) MatchHex() string           // hex.EncodeToString(MatchKey()) — the index key
func (a Answer) MCPResult() (state, reason, note, evidence string)  // backs SP-13's AlreadyTriedResult
func NodeIDFor(r Record) dag.NodeID             // dag.EliminationNode(r.ID)
func FileNodeID(pathKey string) dag.NodeID      // dag.FileNode(paths.Key(path))

// Extended ledger surface. Open returns a Ledger; the concrete type is unexported, so callers
// reach these with a type assertion: `m, ok := led.(negknow.Maintainer)`. A compile-time
// assertion `var _ Maintainer = (*ledger)(nil)` in ledger.go keeps the two in sync, and a test
// asserts that the value Open returns satisfies Maintainer.
type Maintainer interface {
    NeedsRebuild() bool
    MaintenanceTask(s store.Store) (name string, prio int, fn func(ctx context.Context) error) // SP-12 wires this
    TopActive(ctx context.Context, scope Scope, n int, score map[string]float64) ([]Record, int, error) // SP-11
    Observe(ctx context.Context, o Observation) error                                          // SP-08 feeds this
    IngestMCP(ctx context.Context, a MCPArgs) (Record, []string, error)                        // SP-13
    IngestPin(ctx context.Context, a PinArgs) (Record, []string, error)                        // SP-14
    IngestUserStatement(ctx context.Context, p UserStatement) ([]Record, error)                // SP-08
}

type MCPArgs struct {
    Target, Approach, Reason string
    Scope     string     // "" ⇒ config eliminations.defaultScope
    DependsOn []string   // project-relative paths; resolved to []core.Dep from FileHistory
    Evidence  core.Hash  // zero ⇒ Reason text is stored via store.PutBytes to mint one
}
type PinArgs struct {
    Target, Approach, Reason string
    Scope     string
    DependsOn []string
    Evidence  string     // "sha256:…" or ""; parsed with core.ParseHash
}
type UserStatement struct {
    Prompt     string
    Turn       core.TurnIndex
    Path       string     // last-edited path (paths.Key form); "" if unknown
    Symbol     string
    Approach   string     // the approach text from the preceding assistant turn
    PromptRoot core.Hash  // evidence: the stored verbatim prompt
}
type ObsKind uint8 // ObsTestFail, ObsTestPass, ObsEdit, ObsRevert
type Observation struct {
    Turn    core.TurnIndex
    Kind    ObsKind
    Path    string
    Symbol  string
    Detail  string
    ToolUse core.ToolUseID
    Root    core.Hash
    TS      core.UnixMilli
}
func NewDetector(src ObservationSource, sess core.SessionID, cfg config.Config, clk core.Clock) Detector
type ObservationSource interface{ Since(turn core.TurnIndex) []Observation }

var ErrNoEvidence = errors.New("qompack: elimination requires evidence (eliminations.requireEvidence)")
var ErrBlind      = errors.New("qompack: elimination ledger is in blind mode")
```

**The `obs` instrument names this subplan mints.** These are strings, not Go symbols, so nothing
catches a typo at compile time and a second spelling simply produces a second, silently empty
instrument. This list is the single authority for how they are written; every occurrence elsewhere
in this plan is a copy of a line here, and no other subplan may re-mint one under a different name.

```text
counters
  negknow.records.appended               a record line was appended to records/eliminations.jsonl
  negknow.records.deduped                an append collapsed onto an existing record
  negknow.records.rejected_no_evidence   refused under eliminations.requireEvidence (ErrNoEvidence)
  negknow.query.<state>                  one per Answer state, suffixed by the state's own name
  negknow.query.bloom_only               a bloom hit with no backing record (a false positive)
  negknow.stale.flipped                  RefreshStaleness moved a record active → stale
  negknow.stale.skipped                  a stale record was not answered as a block
  negknow.log.corrupt_lines              an unparseable line was skipped (Warn, never fatal)
  negknow.log.duplicate_add              a second record line for an ID already materialized
  negknow.log.unknown_op                 a control line carrying an unrecognized "op" key
  negknow.log.orphan_stale               a stale control line naming no known record
  negknow.detector.candidates            source #3 proposed an elimination
  negknow.detector.emitted               a candidate survived to a record
  negknow.detector.dropped_no_evidence   a candidate was dropped for lack of an evidence root
  negknow.user_statement.unresolved      source #4 could not resolve a path or approach
  negknow.bloom.rebuilds                 RebuildBloom completed and swapped a new filter in
  negknow.bloom.blind_mode               the record log was unreadable at Open; blind = true
  negknow.bloom.corrupt_on_load          tried.bloom failed its CRC at Open (bit rot, not cold start)

histograms
  negknow.record    append latency        negknow.refresh   RefreshStaleness latency
  negknow.query     query latency         negknow.rebuild   RebuildBloom latency

idle task name (not an instrument)
  negknow.maintain  the (name, prio, fn) triple MaintenanceTask returns for SP-12 to register
```

`negknow.bloom.corrupt_on_load` is **new in this subplan** and deserves its own sentence, because it
is the one name above that cannot be checked against anything: `git grep -n corrupt_on_load` over
the whole tree returns nothing today, so there is no shipped spelling to conform to and no test
outside this plan that would notice a variant. SP-09 mints it, exactly as written above — lower
snake case, under the `negknow.bloom.` family, singular `corrupt`, `_on_load` not `_at_load`. It is
the counter that separates bit rot from a cold start in §12.3's "bloom load fails" row: incremented
when `errors.Is(err, sketch.ErrCorrupt)` holds, left alone on an ordinary first session, and
asserted at both ends by `TestBloomLoadFailure_RebuildsFromRecords` (`== 1`) and
`TestBloomLoad_ColdStartIsQuiet` (`== 0`).

---

## Implementation spec

Every file below is new unless marked *(modify)*. No file outside this list, the `*_test.go` files and fixtures named in the **Test plan**, and the single ADR named in commit 7, is created or edited.

### `internal/negknow/doc.go`

Package doc quoting §8.3 items 1–5 and stating invariant 3 (bloom is a cache). No code.

### `internal/negknow/classify.go` — approach-class canonicalization

Deterministic, allocation-bounded, no I/O.

```go
func ApproachClass(approach string) string
```

Algorithm, executed exactly in this order:

1. Lowercase with `strings.ToLower`.
2. Replace every run of characters outside `[a-z0-9]` with a single `-`; trim leading/trailing `-`.
3. Split on `-` into tokens.
4. Drop any token present in `stopwords` (closed set, below).
5. Map each surviving token to its stem with `classifyToken(tok)`, which is exactly these five steps in order — no other lookup, no iteration:

   ```go
   func lemma(tok string) string {
       switch {
       case len(tok) > 4 && strings.HasSuffix(tok, "ies"):                        // retries → retry
           return tok[:len(tok)-3] + "y"
       case len(tok) > 4 && strings.HasSuffix(tok, "ing"):                        // pooling → pool
           return tok[:len(tok)-3]
       case len(tok) > 3 && strings.HasSuffix(tok, "ed"):                         // widened → widen
           return tok[:len(tok)-2]
       case len(tok) > 3 && strings.HasSuffix(tok, "s") && !strings.HasSuffix(tok, "ss"):
           return tok[:len(tok)-1]                                                // timeouts → timeout
       }
       return tok
   }

   func classifyToken(tok string) string {
       if v, ok := synonyms[tok]; ok { return v }        // 1. raw token is a key
       l := lemma(tok)                                   // 2. crude lemma, applied once
       if v, ok := synonyms[l]; ok { return v }          // 3. lemma is a key
       if v, ok := synonyms[l+"e"]; ok { return v }      // 4. silent-e restore: increas → increase
       return l                                          // 5. unmapped: keep the lemma
   }
   ```

   Step 4 is what makes the map small: stripping `"ing"`/`"ed"` from an `-e` verb loses the `e` (`increasing → increas`, `disabling → disabl`, `rewriting → rewrit`), so one restore attempt recovers the dictionary form instead of requiring a second entry per verb. Known and accepted limitation: doubled-consonant gerunds (`dropping → dropp`, `skipping → skipp`) are not recovered and classify to their own stem. That is a recall loss on a query, never a wrong answer, because the record lookup is authoritative.
6. Drop empty tokens; deduplicate keeping first occurrence.
7. Truncate each surviving token to `maxTokenBytes = 24` bytes at a UTF-8 boundary, then truncate the token list to the first `maxClassTokens = 4` tokens. (The per-token cap is what bounds the result at ≤ 128 bytes for an arbitrarily long input word.)
8. Join with `-`. If the result is empty, return `"unclassified"`.

```go
var stopwords = map[string]struct{}{ // 48 entries, closed
  "a":{},"an":{},"and":{},"are":{},"as":{},"at":{},"be":{},"but":{},"by":{},"can":{},
  "could":{},"did":{},"do":{},"does":{},"for":{},"from":{},"had":{},"has":{},"have":{},
  "in":{},"into":{},"is":{},"it":{},"its":{},"just":{},"of":{},"on":{},"or":{},"our":{},
  "so":{},"than":{},"that":{},"the":{},"their":{},"them":{},"then":{},"there":{},"this":{},
  "to":{},"too":{},"try":{},"up":{},"was":{},"we":{},"were":{},"will":{},"with":{},"you":{},
}

var synonyms = map[string]string{ // closed; every key maps to exactly one stem
  // magnitude up
  "widen":"widen","wide":"widen","increase":"widen","raise":"widen","bump":"widen",
  "enlarge":"widen","expand":"widen","grow":"widen","extend":"widen","lengthen":"widen","lift":"widen",
  // magnitude down
  "shrink":"shrink","reduce":"shrink","decrease":"shrink","lower":"shrink","shorten":"shrink",
  "narrow":"shrink","tighten":"shrink",
  // objects
  "timeout":"timeout","deadline":"timeout","ttl":"timeout","expiry":"timeout","expiration":"timeout",
  "pool":"pool","pooling":"pool","pooler":"pool","connectionpool":"pool","connection":"pool",
  "retry":"retry","reattempt":"retry","redo":"retry","backoff":"retry",
  "cache":"cache","caching":"cache","memoize":"cache","memoization":"cache",
  "lock":"lock","mutex":"lock","semaphore":"lock",
  "index":"index","indice":"index",
  // verbs
  "disable":"disable","off":"disable","unset":"disable","remove":"disable","delete":"disable",
  "drop":"disable","strip":"disable","skip":"disable",
  "enable":"enable","set":"enable","add":"enable","insert":"enable","introduce":"enable",
  "upgrade":"upgrade","update":"upgrade","migrate":"upgrade","upversion":"upgrade",
  "downgrade":"downgrade","rollback":"downgrade","revert":"downgrade","pin":"downgrade",
  "rewrite":"rewrite","refactor":"rewrite","restructure":"rewrite","reorder":"rewrite",
  "patch":"patch","monkeypatch":"patch","shim":"patch","polyfill":"patch",
  "mock":"mock","stub":"mock","fake":"mock",
  "parallel":"parallel","concurrent":"parallel","async":"parallel","goroutine":"parallel","thread":"parallel",
}
```

The map is single-valued by construction, so `"bump"` classifies to `widen` even in "bump the version"; this is a deliberate coarsening. Likewise `"connection"` maps to `pool`, which is what collapses "connection-pool" and "pooling" onto the same stem. Coarse classes only widen the set of records a *query* can match; the record lookup remains authoritative and the returned `Record.Reason` disambiguates for the agent. A unit test asserts that every value in `synonyms` is also a key mapping to itself, so stems are fixpoints.

Worked examples (asserted in tests — each trace is the normative behaviour of the algorithm above, not an aspiration):

| `approach` | tokens after step 5 | `ApproachClass` |
|---|---|---|
| `"widen pool timeout"` | widen · pool · timeout | `widen-pool-timeout` |
| `"Increasing the connection-pool timeouts"` | widen (`increasing`→`increas`→`increase`) · pool (`connection`) · pool · timeout | `widen-pool-timeout` |
| `"bump the pool deadline"` | widen · pool · timeout (`deadline`) | `widen-pool-timeout` |
| `"disable connection pooling"` | disable · pool (`connection`) · pool (`pooling`) | `disable-pool` |
| `"raise the pool timeout"` | widen (`raise`) · pool · timeout | `widen-pool-timeout` |
| `"rewrite refreshToken with async retries"` | rewrite · refreshtoken · parallel (`async`) · retry (`retries`→`retry`) | `rewrite-refreshtoken-parallel-retry` |
| `"   "` | — | `unclassified` |
| `"try that"` | — (both stopwords) | `unclassified` |

**Reconciliation with the frozen contract fixture — read before writing a golden.**
`testdata/golden/contracts/negknow/want/elimination_record.jsonl` is `state:"frozen"` and carries
`"approach":"widen pool timeout"` alongside `"approach_class":"widen-timeout"`, while the algorithm
above yields `widen-pool-timeout` for that phrase. Both are correct and neither moves. The fixture's
MANIFEST kind is `format`: it freezes the **wire shape** of one `records/eliminations.jsonl` line
byte-for-byte, and its `approach_class` value is a hand-chosen label in that shape, not an assertion
about `ApproachClass`. The behaviour fixture is the separate `three_way_answer` row
(`kind:"behaviour"`, `state:"record-by-owner"`), which SP-09 records. Two consequences, both binding:

- The frozen fixture is **never regenerated** (V3-VERIFY §1 rule 3; Rule W-2). Any test that compares
  against it builds its `Record` — and its `Descriptor` — from **explicit literals transcribed from
  the fixture**, never from `Canonicalize(target, approach, reason)`. `Ledger.Record` recomputes the
  descriptor unconditionally, so a record that went through the ledger will carry
  `widen-pool-timeout` and must not be byte-compared against this fixture.
- `key_test.go`'s `fixedDescriptor` uses the same hand-built `ApproachClass: "widen-timeout"`, and
  `wantDescriptorKeyHex` is frozen over it. It is likewise a literal, not `Canonicalize`'s output, so
  changing the classifier cannot invalidate it — and must not be assumed to.

### `internal/negknow/descriptor.go`

**What this file does and does not add.** `internal/negknow/types.go` already ships `Descriptor` (with
its frozen json tags), `Scope`, `Status`, `SourceKind` and `Dep = core.Dep`, and a **real, frozen**
`Descriptor.Key()`. This file adds `SplitTarget`, `CanonicalizeAt`, `reasonHash`, `sanitizeField`,
`MatchKey` and `MatchHex` only. `Canonicalize`'s SP-01 stub body is replaced **in place in
`types.go`**; nothing already declared there is moved, re-declared or re-implemented, and the
descriptor's own domain constant is `core.DomainNegKnow`, not a package-local copy of the string.

```go
const (
    domainMatch    = "qompack.neg.match.v1"
    domainReason   = "qompack.neg.reason.v1"
    domainRecordID = "qompack.neg.id.v1"
    domainDedup    = "qompack.neg.dedup.v1"
    fieldSep       = byte(0x1F) // ASCII unit separator; cannot appear in a normalized path
)
// The identity key's domain is core.DomainNegKnow ("qompack.neg.v1"), owned by internal/core's
// domain registry and already used by the shipped Descriptor.Key. It is deliberately NOT
// redeclared here: a second spelling of the same string is a second thing to keep in sync.

func SplitTarget(target string) (path, symbol string)
```

`SplitTarget` rules, applied in order:
1. Trim spaces. If empty → `("", "")`.
2. If the target contains `'#'`, split at the **first** `'#'`: left is the path, right is the symbol.
3. Else find the **last** `':'`. If found and the suffix matches `^[A-Za-z_$][A-Za-z0-9_$.]*$` and the suffix is non-empty, split there; otherwise the whole string is the path and the symbol is `""`.
4. `path = paths.Key(path)` (project-relative, forward-slash, case-folded on Windows/macOS). `symbol` is kept verbatim except for surrounding-space trimming.

Rule 3 handles Windows absolute paths correctly: for `C:/proj/src/auth.ts` the suffix after the last colon is `/proj/src/auth.ts`, which fails the symbol regex, so no split occurs. Callers holding an absolute path call `paths.Norm(root, p)` first; `CanonicalizeAt` does that for them.

```go
// Canonicalize: replace the zero-Descriptor stub body in types.go with this. The signature there
// is already correct; do not re-declare the function in descriptor.go.
func Canonicalize(target, approach, reason string) Descriptor {
    p, sym := SplitTarget(target)
    return Descriptor{
        NormalizedPath: p,
        Symbol:         sym,
        ApproachClass:  ApproachClass(approach),
        ReasonHash:     reasonHash(reason),
    }
}
func CanonicalizeAt(root, target, approach, reason string) (Descriptor, error) // Norm then Canonicalize

func reasonHash(reason string) core.Hash {
    // lowercase, collapse every whitespace run to one space, trim
    n := collapseWS(strings.ToLower(reason))
    return core.HashBytes(domainReason, []byte(n))
}
```

`reasonHash("")` = `core.HashBytes(domainReason, []byte(""))` — a fixed, documented value; `Query` always produces it because `Query` has no reason argument, which is precisely why `MatchKey` excludes `ReasonHash`.

**`symbol_or_null` (§8.3) is represented as the empty string in Go and JSON**, matching 00-ARCHITECTURE §5.10's `Symbol string // "" == null`. `Descriptor.MarshalJSON` therefore emits `"symbol":""`, never `null`, so the key is always present and the field is always a string; `UnmarshalJSON` accepts `null`, a missing key, and `""` and normalizes all three to `""`. A golden row in `descriptors.golden.json` covers a symbol-less descriptor.

**`Descriptor.Key()` is already shipped and frozen. Do not re-derive it, do not touch it.**
`internal/negknow/types.go` carries the real implementation, transcribed from §14.1 of
`plans/V1-SP-01-foundation-toolchain-and-contracts.md`, and its own doc comment states the rule:
"do not improvise the byte layout — `TestDescriptorKey_Stable` freezes its output as a golden, and
any change here silently re-keys every bloom entry already on disk." Its preimage, for the record and
so no one reconstructs a different one:

```
core.HashBytes(core.DomainNegKnow,
    NormalizedPath ‖ 0x1f ‖ Symbol ‖ 0x1f ‖ ApproachClass ‖ 0x1f ‖ ReasonHash[:])
```

Three details that a re-derivation gets wrong, listed because each one silently changes every key:
the trailing field is the **raw 32 hash bytes** (`d.ReasonHash[:]`), not the 71-byte `"sha256:<hex>"`
text `Hash.String()` returns; the three text fields are written **unsanitized**; and the domain is
`core.DomainNegKnow`. `internal/negknow/key_test.go` pins the output as
`wantDescriptorKeyHex = "7b2149a504b5227e6b00d912b48cf01aea3a609f1c3582e0d06bb36122b87d5d"` over its
`fixedDescriptor`, asserted by `TestDescriptorKey_Stable`. That test is green today and must stay
green through every commit of this subplan.

`MatchKey()` is the new key this subplan owns, and it is where separator hygiene lives:

```go
// sanitizeField makes fieldSep unambiguous: 0x1F is a control character that cannot legitimately
// appear in a path, a symbol or an approach class, so replacing it removes the only way a caller
// could forge a colliding MatchKey by embedding a separator in a field. It is used by MatchKey
// ONLY — Key's byte layout is frozen and takes its fields raw.
func sanitizeField(s string) string { return strings.ReplaceAll(s, string(rune(fieldSep)), " ") }

func (d Descriptor) MatchKey() []byte {
    var b bytes.Buffer
    b.WriteString(sanitizeField(d.NormalizedPath)); b.WriteByte(fieldSep)
    b.WriteString(sanitizeField(d.Symbol));         b.WriteByte(fieldSep)
    b.WriteString(sanitizeField(d.ApproachClass))
    h := core.HashBytes(domainMatch, b.Bytes())
    out := make([]byte, len(h)); copy(out, h[:]); return out
}
func (d Descriptor) MatchHex() string // hex.EncodeToString(d.MatchKey()) — the in-memory index key
```

**Every record contributes exactly two bloom keys**: `Key()` (identity, used for record-level dedup pre-checks and for the literal §8.3 sentence "Insert into `tried.bloom`") and `MatchKey()` (reason-independent, the key `Query` tests). Effective bloom capacity consumption is therefore `2 × records`; `RebuildBloom` sizes for that.

### `internal/negknow/record.go` *(modify — SP-01 shipped the struct and the constants)*

The `Record` struct and its json tags, and the `Scope`/`Status`/`SourceKind` constants, are **already
on `develop`** (`internal/negknow/record.go`, `internal/negknow/types.go`). Do not re-declare them:
the const order in particular is frozen by two contract fixtures —
`testdata/golden/contracts/negknow/want/elimination_record.jsonl` and
`testdata/golden/contracts/checkpoint/want/0001.json`'s `eliminated[0]` — which both carry
`"source":1`, correct only while `SourceSlashCommand` is the second (0-indexed) value. `types.go`
says so in as many words: "Do not reorder these."

This file adds only the two conversions and the codec:

```go
func (s SourceKind) String() string // "mcp","slash","heuristic","user"; default "unknown"
func ParseSourceKind(s string) (SourceKind, error)
```

`String()`/`ParseSourceKind` are for humans — `/qompack:status` output, log lines, the `--json`
rendering SP-14 owns. They are **not** the wire form: on disk and in a checkpoint, `source` is the
integer, per the frozen fixtures. A unit test asserts both directions and that no golden byte
anywhere contains `"source":"mcp"`.

Field-length bounds enforced by `normalizeRecord` before append (defence against an unbounded agent-supplied string reaching a JSONL line, §12.3 "no unbounded allocation reachable from untrusted input"):

| Field | Limit | Overflow behaviour |
|---|---|---|
| `Target` | 512 bytes | truncated at a UTF-8 boundary, `…` appended |
| `Approach` | 512 bytes | same |
| `Reason` | 2048 bytes | same |
| `DependsOn` | 32 entries | extra entries dropped, warning appended |
| `StaleBecause` | 32 entries | same |

If `Deps.Redact` is non-nil it is applied to `Target`, `Approach` and `Reason` before the bounds check.

**On-disk JSON.** `Record` carries explicit `MarshalJSON`/`UnmarshalJSON`, and the bytes they emit are
**identical to what the shipped struct tags already produce** — lowercase `path`/`hash` keys inside
`depends_on` (`core.Dep`'s own tags), `"sha256:…"` text for every hash (`core.Hash.MarshalJSON`), and
the **integer** `SourceKind` for `source`, exactly as the frozen fixture
`testdata/golden/contracts/negknow/want/elimination_record.jsonl` carries it (`"source":1`). The codec
exists only for the two things tags cannot express — nil `depends_on` rendering as `[]` rather than
`null`, and the tolerant defaulting of a missing `scope`/`status`/`source` or an unparseable hash on
the way in. It changes no key, no key order and no value shape. This also means SP-10's
`checkpoint.Checkpoint.Eliminated []negknow.Record` serializes into `eliminated[]` in the shape §8.5
prints, with no work on SP-10's side.

To be precise about "matches §8.5": the wire form is a **superset**. All seven keys §8.5 shows — `target`, `approach`, `reason`, `evidence`, `depends_on`, `scope`, `status` — appear with exactly §8.5's names, positions relative to one another, and value shapes. `id`, `session`, `ts`, `descriptor` and `source` are additive fields §8.5's illustrative snippet elides; they are required for the log to be replayable and for `already_tried` to be answerable, and no consumer of the checkpoint may reject unknown keys (00-ARCHITECTURE §11.3 makes unknown-key tolerance the house rule). The byte-level authority is the **frozen** contract fixture `testdata/golden/contracts/negknow/want/elimination_record.jsonl`, not a golden this subplan generates: `TestRecordJSON_Golden` marshals a `Record` built from literals transcribed out of that fixture and requires the output to equal the fixture's single line byte-for-byte. A second assertion extracts the seven §8.5 keys and compares them against a literal transcribed from §8.5, so drift against the design document is caught independently of drift against the fixture. The fixture is `state:"frozen"` and is **not** regenerated to make a test pass (V3-VERIFY §1 rule 3): if the marshaller disagrees with it, the marshaller is wrong.

```go
type recordWire struct {
    ID        string     `json:"id"`
    Session   string     `json:"session"`
    TS        int64      `json:"ts"`
    Target    string     `json:"target"`
    Approach  string     `json:"approach"`
    Reason    string     `json:"reason"`
    Desc      descWire   `json:"descriptor"`
    Evidence  string     `json:"evidence"`
    DependsOn []depWire  `json:"depends_on"`
    Scope     string     `json:"scope"`
    Status    string     `json:"status"`
    StaleSince   int64    `json:"stale_since,omitempty"`
    StaleBecause []string `json:"stale_because,omitempty"`
    Source    SourceKind `json:"source"`   // INTEGER on the wire — "source":1 in the frozen fixture
}
type depWire  struct{ Path string `json:"path"`; Hash string `json:"hash"` }
type descWire struct {
    NormalizedPath string `json:"normalized_path"`
    Symbol         string `json:"symbol"`
    ApproachClass  string `json:"approach_class"`
    ReasonHash     string `json:"reason_hash"`
}
```

`UnmarshalJSON` tolerates a missing `source` (→ `SourceMCP`), a missing `status` (→ `StatusActive`), a missing `scope` (→ `ScopeSession`), and an unparseable hash (→ zero `core.Hash` plus a returned error only when the field is `evidence` and `requireEvidence` is set; dep hashes that fail to parse cause the dep to be dropped and a warning logged). `depends_on: null` decodes to a nil slice and marshals back as `[]` — a golden test pins this.

Record ID:

```go
func recordID(sess core.SessionID, ts core.UnixMilli, d Descriptor) string {
    var b bytes.Buffer
    b.WriteString(string(sess)); b.WriteByte(fieldSep)
    fmt.Fprintf(&b, "%d", int64(ts)); b.WriteByte(fieldSep)
    b.Write(d.Key())
    h := core.HashBytes(domainRecordID, b.Bytes())
    return "elim_" + hex.EncodeToString(h[:])[:12]
}
```

The prefix is **`elim_`**, five characters, not `elm_`. It is fixed by the frozen fixture's
`"id":"elim_3f9b2c7d1a48"`, which also fixes the shape: prefix plus exactly 12 lowercase hex
characters. The fixture's twelve digits are a hand-chosen literal — `recordID` is a digest of
`(session, ts, Desc.Key())` and is neither required nor able to reproduce them, so any test that
compares against the fixture sets `ID` explicitly rather than calling `recordID`.

### `internal/negknow/log.go` — the append-only elimination log

Path: `<root>/.qompack/records/eliminations.jsonl`. Opened with `paths.AppendOnly`; `O_TRUNC` is never requested, so `ErrAppendOnly` can only surface as a bug. The directory is created with `os.MkdirAll(dir, 0o755)` on first use.

**A record line is a bare `Record`, with no envelope.** This is settled by the frozen contract fixture
`testdata/golden/contracts/negknow/want/elimination_record.jsonl`, whose MANIFEST entry describes it
as a "`records/eliminations.jsonl` record with the §8.3 staleness guard" and whose content is an
unwrapped record ending `…,"scope":"project","status":"active","source":1`. There is no
`{"op":"add","rec":{…}}` envelope and no `add` op: adding one would put a second, incompatible
spelling of the same line into the same file as a `state:"frozen"` fixture, and the fixture is not
regenerated to accommodate it (V3-VERIFY §1 rule 3).

There are still two line kinds, discriminated now by the **presence** of an `op` key. A `Record` has
no `op` field, so a line that carries one is a control line and a line that does not is a record:

```go
type logKind string
const opStale logKind = "stale"

// logControl is the shape of a control line. A record line is a bare Record and does not use it.
type logControl struct {
    Op      logKind  `json:"op"`
    ID      string   `json:"id,omitempty"`
    TS      int64    `json:"ts,omitempty"`
    Because []string `json:"because,omitempty"`
}

// logProbe is what every line is decoded into first, to route it.
type logProbe struct{ Op logKind `json:"op"` }
```

Byte-for-byte, a record line (one line, `\n`-terminated, no interior newlines, `json.Encoder` with
`SetEscapeHTML(false)`) — this is `want/elimination_record.jsonl` verbatim, and `Record.MarshalJSON`
must reproduce it exactly:

```
{"id":"elim_3f9b2c7d1a48","session":"sess_01J8ZQ5R7N3K2M4P6T8V0X2Y4A","ts":1767225480000,"target":"src/auth.ts:refreshToken","approach":"widen pool timeout","reason":"pgbouncer 1.18 ignores statement_timeout in transaction pooling mode, so the widened timeout never takes effect","descriptor":{"normalized_path":"src/auth.ts","symbol":"refreshToken","approach_class":"widen-timeout","reason_hash":"sha256:9f2c4a7e1b8d3506e9a1c4f7b2d508e3a6c9f1b4d7e0a3c6f9b2d5e8a1c4f7b0"},"evidence":"sha256:4d7a0c3f6b9e2158a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3","depends_on":[{"path":"docker-compose.yml","hash":"sha256:1a4d7c0f3b6e9a2d5c8f1b4e7a0d3c6f9b2e5a8d1c4f7b0e3a6d9c2f5b8e1a4d"}],"scope":"project","status":"active","source":1}
```

Every hash above is exactly 64 hex characters after the `sha256:` prefix, and `source` is the integer
`1` (`SourceSlashCommand`). Key order in the emitted line is the `recordWire` struct field order,
which is the shipped `Record` field order and which `encoding/json` preserves. The **frozen fixture**
is the byte-level authority for one record line; `testdata/golden/contracts/negknow/eliminations.golden.jsonl`
is SP-09's own, non-frozen golden for a multi-line log and must contain that same line unmodified as
one of its rows.

A `stale` control line:

```
{"op":"stale","id":"elim_3f9b2c7d1a48","ts":1754985600000,"because":["docker-compose.yml: dependency hash changed from sha256:1a4d7c0f3b6e"]}
```

**Materialization** (`func replayLog(r io.Reader, log logging.Logger) ([]Record, map[string]int, int, error)`):

1. `bufio.Scanner` with a 1 MiB buffer. Blank lines are skipped.
2. A line that fails to parse as JSON is skipped, counted in `obs.Counter("negknow.log.corrupt_lines")`, logged at `Warn` with the line number — never fatal. A truncated tail line (the last line, no trailing `\n`) is skipped silently: it is the expected shape of a crash mid-append.
3. Decode into `logProbe`. `Op == ""` — a **record line**: decode the same bytes into a `Record`. If its `ID` is empty the line is counted as corrupt and skipped; if the `ID` is already present, the **first** occurrence wins and the duplicate is counted in `negknow.log.duplicate_add`; otherwise append to the slice and index by ID.
4. `Op == "stale"`: decode into `logControl`. If the ID is known, set `Status = StatusStale`, `StaleSince = TS`, `StaleBecause = Because` (replacing, not appending, so a re-flip is idempotent). If unknown, count `negknow.log.orphan_stale` and continue.
5. Any other non-empty `op` value: skipped, counted in `negknow.log.unknown_op`, logged at `Warn`. This is the forward-compatibility path for a newer plugin version, and it is why a record line may never grow an `op` field.
6. Returns records in log order, the ID→index map, and the count of lines read.

Appends are made under the ledger's mutex through a single held append handle (`paths.AppendOnly` returns an `io.WriteCloser`); each append is one `Write` of a fully-built `[]byte` ending in `\n` (atomic for a single sub-`PIPE_BUF` write on POSIX and a single `WriteFile` on Windows). `Close()` closes the handle. There is no `fsync` per append: the WAL/durability boundary for the plugin is the daemon's spool (00-ARCHITECTURE §2.4), and a lost tail line costs at most one elimination, never a corrupt index.

### `internal/negknow/ledger.go`

```go
type ledger struct {
    mu       sync.RWMutex
    root     string
    cfg      config.Config
    elim     config.EliminationsCfg
    bloom    *sketch.Bloom
    blind    bool                       // §12.3: records unreadable ⇒ Query answers absent always
    recs     []Record
    byID     map[string]int
    byMatch  map[string][]int           // MatchHex → record indices, insertion-ordered
    byKey    map[string]int             // dedupHex(session, scope, Desc.Key()) → index of the most
                                        // recently appended record with that identity; the
                                        // idempotence index. Status is read from l.recs[i], so
                                        // MarkStale never has to touch this map.
    ring     []Observation              // bounded ring of the last signalRing observations
    lay      paths.Layout               // paths.Of(root): .Records, .Sketches, .Tmp
    f        io.WriteCloser             // append handle on records/eliminations.jsonl
    closed   bool                       // Close is idempotent
    pending  bool                       // a bloom rebuild is owed (rebuildOnStale == nextIdle)
    seq      int                        // rebuild generation; seeded at Open from
                                        // paths.HighestBloomBackupSeq(l.lay) so it resumes above
                                        // every surviving tried.bloom.<n>.bak. Nothing else on disk
                                        // records it, and nothing else needs to.
    deps     Deps
    log      logging.Logger
    m        obs.Registry
    clk      core.Clock
}
```

`Open(root string, cfg config.Config, b *sketch.Bloom, deps Deps) (Ledger, error)`:

1. Default `deps.Clock` to `core.SystemClock()`, `deps.Log` to `logging.Nop()` when nil. `internal/obs` ships no no-op constructor and W-3 forbids adding one, so when `deps.Metrics` is nil the ledger uses `nopRegistry` — a ~30-line package-local implementation of `obs.Registry` (and of `obs.Histogram`/`Counter`/`Gauge`) defined at the bottom of `ledger.go` whose methods discard everything. `deps.Store` and `deps.Graph` may legitimately be nil.
2. Read and validate the `eliminations` block. Any value outside its Appendix C domain falls back to the Appendix C default and is reported through `log.Loud` — never a crash (00-ARCHITECTURE §11.3). Defaults: `requireEvidence=true`, `defaultScope="session"`, `rebuildOnStale="nextIdle"`, `staleResponse="flag"`.
3. `l.lay = paths.Of(root)`, then seed the rebuild counter from the disk itself: `seq, ok, err := paths.HighestBloomBackupSeq(l.lay)`; `l.seq = seq` when `ok`, else `0`. `paths.HighestBloomBackupSeq` is exported for exactly this — "SP-09 needs it … its rebuild counter drives seq, and nothing on disk otherwise tells a restarted daemon where that counter had got to. This is the answer — the counter resumes above the highest surviving backup." A missing `sketches/` directory is `ok == false` and not an error (a fresh project); a genuine read error is logged at `Debug` and leaves `l.seq = 0`, which the `ReplaceGenerational` pre-flight will refuse rather than silently mis-sequence. **There is no `state/negknow.json`**: a private counter file that could disagree with the backup names on disk is a second source of truth for a number the filesystem already carries, and `paths.pruneBloomBackups` keeps the highest sequence, not the newest file, so the two disagreeing is data loss rather than untidiness.
4. Open the log with `paths.AppendOnly`, read it fully (a separate `O_RDONLY` handle), materialize `recs`/`byID`/`byMatch`/`byKey`. If the file cannot be read at all (permission, I/O error): set `blind = true`, `log.Loud("negknow: elimination records unreadable; already_tried will answer absent for everything", "err", err)`, increment `negknow.bloom.blind_mode`, and continue — `Open` still returns a usable ledger, because a hook that dies takes observability with it.
5. Bloom acquisition:
   - `b != nil` → adopt it (the daemon already loaded it).
   - `b == nil` → `nb := sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate)` then
     `err := sketch.LoadWithLog(filepath.Join(l.lay.Sketches, sketch.TriedBloomBase), nb, deps.Log)`.
     **`LoadWithLog`, never `Load`.** SP-03 names this call site specifically — "SP-05 (`daemon.SketchSet` load-at-session-start) and SP-09 (`negknow.Open`) call `LoadWithLog`, never `Load`" — because `Load` runs on a `logging.Nop()` and writes no line anywhere, which would make a corrupt filter indistinguishable from a first session and violate 00-ARCHITECTURE §13 invariant 10 ("degradation is loud"). `deps.Log` is already defaulted to `logging.Nop()` in step 1, so there is no new parameter to thread and no nil to guard.
     `core.ErrNotFound` (missing **or** CRC-failed, per §5.7) is not an error here: it triggers step 6's immediate rebuild. The two cases are told apart for the log, not for the control flow — `errors.Is(err, sketch.ErrCorrupt)` is true for bit rot and false for a cold start, so a corrupt filter also increments `negknow.bloom.corrupt_on_load` and a missing one does not. `LoadWithLog` has already written the Loud line ("sketch corrupt — rebuilding from records") by then; this branch does not write a second one.
6. **Consistency check against the records** — this is invariant 3 made operational. **Skipped entirely when `blind` is true**: no rebuild is scheduled and none is performed, because the records that would feed one are unreadable and a rebuild would replace a good on-disk cache with an empty one. Under `blind` the ledger keeps whatever bloom it has (adopted or freshly allocated) and relies on `Query` answering `AnswerAbsent` unconditionally. Otherwise:
   - `want := 2 * len(visibleActive())`.
   - `bloom load failed` **or** `b.Count() < want` → the bloom is missing keys, which would produce false *negatives* and silently defeat the feature: call `RebuildBloom` synchronously now. `RebuildBloom` returns a correct in-memory bloom even when only its on-disk persistence failed, so adopt the returned bloom and set `pending = true` to retry the write on the next idle tick. This branch is the *common* case rather than an exception: `Record` adds keys to the in-memory filter only, and §3.3 permits `tried.bloom` to be replaced solely by `RebuildBloom`, so the on-disk filter always lags the log by whatever was recorded since the last rebuild. That is intended — the rebuild is a linear pass over a few thousand entries with a < 50 ms budget.
   - `b.Count() > want` → the bloom carries extra keys: records flipped stale since the last rebuild, and session-scoped records belonging to earlier sessions. That is **safe** — an extra key costs one record lookup that then answers `AnswerAbsent` with `BloomOnly: true`, or `AnswerStale`, both correct — so schedule per `rebuildOnStale`: `"immediate"` rebuilds now, `"nextIdle"` sets `pending = true`, `"never"` does nothing.
7. If `deps.Store != nil`, `!blind`, and `rebuildOnStale != "never"`, run one bounded staleness refresh: `ctx, cancel := context.WithTimeout(context.Background(), openRefreshDeadline)` with `openRefreshDeadline = 250 * time.Millisecond`. On deadline, log at `Debug` and set `pending = true`; the daemon's idle task finishes the job. This makes the ledger correct in wave 2 with zero daemon wiring while still being cheap.
8. Return.

`Close(ctx)`: closes the append handle and sets `closed = true`. It is idempotent — a second call returns `nil` without touching the handle. It does **not** rebuild the bloom, flush anything to `sketches/`, or run a staleness refresh: a `Close` that did work could block a `SessionEnd` hook, and everything it would do is already recoverable from the log at the next `Open`. After `Close`, read methods (`Query`, `Get`, `Active`, `All`, `TopActive`, `Health`) keep answering from the in-memory index exactly as before, and every write path (`Record`, `MarkStale`, `Observe`, `RebuildBloom`) returns `os.ErrClosed` without appending.

**Visibility.** Two helpers, used everywhere so scope semantics have exactly one definition:

```go
// visible reports whether r is answerable under the requested query scope.
//   ScopeProject → project-scoped records only (cross-session carry-over, §8.3 item 5)
//   ScopeSession (and "") → project-scoped records, plus session-scoped records of THIS session
func (l *ledger) visible(r Record, q Scope) bool {
    if r.Scope == ScopeProject { return true }
    if q == ScopeProject { return false }
    return r.Session == l.deps.Session
}
func (l *ledger) visibleActive() []Record // r.Status == StatusActive && l.visible(r, ScopeSession)
```

`Record(ctx, r Record) (string, error)`:

1. Recompute the descriptor unconditionally — `r.Desc = Canonicalize(r.Target, r.Approach, r.Reason)` — so a caller can neither leave it zero nor inject one that disagrees with the text fields.
2. Fill defaults: `r.Session = deps.Session` when empty; `r.TS = core.UnixMilli(clk.Now().UnixMilli())` when zero; `r.Scope = Scope(elim.DefaultScope)` when empty; `r.Status = StatusActive` when empty (a caller may not create a record already stale — `MarkStale` is the only path to `stale`, and passing `StatusStale` here is normalized back to active and logged at `Warn`).
3. `normalizeRecord` (redaction, bounds, dep dedup by `paths.Key(path)` keeping the first hash, dep sort by path ascending for byte-stable lines).
4. **`requireEvidence` enforcement.** If `elim.RequireEvidence` and `r.Evidence == (core.Hash{})`: increment `negknow.records.rejected_no_evidence` and return `("", ErrNoEvidence)`. Nothing is appended. The ingest helpers mint evidence *before* calling `Record`, so this fires only for a caller that genuinely has none.
5. **Identity dedup, before the ID is minted.** Compute `dedupHex(r) = hex(HashBytes(domainDedup, session ‖ 0x1F ‖ scope ‖ 0x1F ‖ Desc.Key()))` and look it up in `byKey`. A hit whose record is still `StatusActive` means this exact elimination — same session, same scope, same path/symbol/approach-class/reason — is already on record: return that record's ID and `nil`, append nothing, and increment `negknow.records.deduped`. Recording the identical elimination twice is a no-op, not an error, because MCP retries and the heuristic detector both re-propose.
   This check is deliberately **not** `byID`-based: `recordID` mixes in `TS`, so an MCP retry a second later would mint a different ID and the log would grow a duplicate. A hit whose record is `StatusStale` is *not* a dedup — re-recording after a staleness flip is exactly the re-verification the design asks for, and it must produce a new active record — so it falls through to step 6.
6. `r.ID = recordID(r.Session, r.TS, r.Desc)` when empty. If `byID[r.ID]` already exists (a caller supplied an explicit ID, or a clock collision), return the existing ID and `nil`.
7. Append the `add` line, then update `recs`, `byID`, `byMatch`, `byKey`, and add **both** `r.Desc.Key()` and `r.Desc.MatchKey()` to the bloom. When `rebuildOnStale == "nextIdle"`, also set `pending = true`: the added keys live only in memory until a rebuild (§3.3 lets nothing else replace `tried.bloom`), so without this the next `Open` would have to rebuild synchronously to recover them.
8. Best-effort DAG emission (`dagedges.go`), errors logged at `Debug` and swallowed.
9. Increment `negknow.records.appended`, observe `negknow.record` histogram, return the ID.

`Query(ctx, target, approach string, scope Scope) (Answer, error)` — the three-way response:

```go
d  := Canonicalize(target, approach, "")   // reason unknown at query time; MatchKey ignores it
mk := d.MatchKey()

if l.blind { return Answer{State: AnswerAbsent}, nil }          // §12.3: never a false positive
if !l.bloom.Test(mk) { count("absent"); return Answer{State: AnswerAbsent}, nil }

idx := l.byMatch[hex(mk)]
cands := filter(idx, func(r Record) bool { return l.visible(r, scope) })
if len(cands) == 0 {                                            // bloom said yes, records say no
    count("bloom_only")
    return Answer{State: AnswerAbsent, BloomOnly: true}, nil
}

if act := pick(cands, StatusActive); act != nil {
    count("active")
    return Answer{State: AnswerActive, Record: act}, nil
}
// every visible match is stale
if l.elim.StaleResponse == "drop" { count("absent"); return Answer{State: AnswerAbsent}, nil }
st := pick(cands, StatusStale)
count("stale")
return Answer{State: AnswerStale, Record: st, Note: StaleNote}, nil
```

Helpers used above: `count(s)` is `l.m.Counter("negknow.query."+s).Add(1)` (`obs.Counter` has no `Inc`); `hex(b)` is `hex.EncodeToString(b)`; `filter` is an inline loop over `idx` into `l.recs`. The whole body runs under `l.mu.RLock()`, and the histogram `negknow.query` is observed on every path via `defer`.

`pick(cands, status)` returns the candidate with the given status having the greatest `TS`; ties broken by the lexicographically greatest `ID`; `nil` when none match. The returned `*Record` points at a **copy**, never into `l.recs`, so a caller cannot mutate ledger state. `MCPResult`'s `AnswerActive`/`AnswerStale` branches are only reachable with a non-nil `Record` by construction, and a unit test asserts `MCPResult` on a hand-built `Answer{State: AnswerActive}` with a nil `Record` returns `("absent","","","")` rather than panicking.

`Answer.MCPResult()`:

```go
func (a Answer) MCPResult() (state, reason, note, evidence string) {
    if a.Record == nil { return "absent", "", "", "" }
    switch a.State {
    case AnswerActive: return "active", a.Record.Reason, "", a.Record.Evidence.String()
    case AnswerStale:  return "stale",  a.Record.Reason, a.Note, a.Record.Evidence.String()
    default:           return "absent", "", "", ""
    }
}
```

`Get`, `Active`, `All`, `TopActive`:

- `Get(id)` → copy, or `core.ErrNotFound`.
- `Active(ctx, scope)` → all records with `Status == StatusActive` and `visible(r, scope)`, ordered by `TS` ascending then `ID` ascending.
- `All(ctx)` → every materialized record in log order, regardless of status, scope or session (this is what `qompack fsck` and SP-16's warm start read).
- `TopActive(ctx, scope, n, score)` → `Active` filtered, sorted by `score[r.ID]` descending (absent → `0`), tie-broken by `TS` descending then `ID` ascending, truncated to `n`; the second return value is `len(all) - len(returned)`, which is what SP-11 renders as "and N more — `already_tried` covers the rest".

### `internal/negknow/staleness.go`

```go
func (l *ledger) MarkStale(ctx context.Context, ids []string, because []string) error
```

For each known, currently-active ID (unknown or already-stale IDs are skipped, counted in `negknow.stale.skipped`): append one `stale` line with `TS = clk.Now().UnixMilli()` and `Because = because`, then update the in-memory record. IDs are processed in the order given. Sets `pending = true` when `rebuildOnStale == "nextIdle"`, calls `RebuildBloom` when `"immediate"`, does neither when `"never"`. Increments `negknow.stale.flipped` by the number actually flipped. Returns the first append error, having already applied the appends that succeeded.

```go
func (l *ledger) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error)
```

1. If `s == nil` return `(nil, nil)`.
2. Snapshot every **active** record (all scopes and sessions — a session-scoped record from another session cannot be queried, but keeping the log truthful is free). Build:
   - `deps []core.Dep` — the union of all `DependsOn` entries, deduplicated on `paths.Key(Path) + "\x1F" + Hash.String()`, ordered by path ascending then hash ascending so the call is deterministic.
   - `owners map[string][]string` — dep key → record IDs.
3. One call: `changed, err := s.ChangedSince(ctx, deps)`. On error, log at `Warn`, return `(nil, err)`; the ledger stays usable and nothing is flipped (failing toward "do nothing").
4. For each `d` in `changed`, for each owning record ID, accumulate a reason string:
   `fmt.Sprintf("%s: dependency hash changed from sha256:%s", d.Path, d.Hash.Short())`.
   `core.Hash.Short()` returns the first 12 hex characters **without** a prefix (§4), so the `sha256:` prefix is written by this format string — which is what makes the golden `stale` line above read `sha256:1a4d7c0f3b6e`. `d.Hash` is the hash the record was recorded against, i.e. the value it changed *from*; `store.ChangedSince` returns the input deps, not the current ones (§5.8).
5. For each affected record, sort its reason strings ascending and deduplicate. Group records by the joined (`"\n"`) reason string. Iterate the groups in ascending order of that joined string; within a group, sort IDs ascending; call `MarkStale(ctx, groupIDs, groupBecause)` once per group. This is how the normative single-`because` signature of §5.10 is honoured without losing per-record precision.
6. Return every flipped ID, sorted ascending. Observe `negknow.refresh` histogram.

```go
func (l *ledger) NeedsRebuild() bool
func (l *ledger) MaintenanceTask(s store.Store) (string, int, func(context.Context) error)
```

`NeedsRebuild()` returns `l.pending && !l.blind && !l.closed`. `MaintenanceTask` returns `("negknow.maintain", 30, fn)` where `fn` runs `RefreshStaleness(ctx, s)` and then, if `NeedsRebuild()`, `RebuildBloom(ctx)`; under `blind` or after `Close` it returns `nil` immediately without doing either. Its shape matches `daemon.IdleController.Register(name string, prio int, fn func(ctx context.Context) error)` exactly, so SP-12's wiring is one line with no decisions left open. Priority `30` places it after frontier advancement and GC in SP-12's ordering; SP-12 may pass a different priority.

**Why `nextIdle` is safe.** A stale record still present in the bloom produces a bloom hit; the record lookup then finds a stale record and `Query` returns `AnswerStale` (or `AnswerAbsent` under `staleResponse: "drop"`). The rebuild only removes keys and lowers the fill ratio — it is a size optimization, never a correctness requirement. A test asserts exactly this: flip a record stale, do **not** rebuild, and confirm `Query` returns `AnswerStale` with `StaleNote`.

### `internal/negknow/bloom.go`

```go
func (l *ledger) RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error)
```

1. If `l.blind` is true, return `(l.bloom, l.health(), ErrBlind)` immediately without touching disk. The records that would feed the rebuild are unreadable, so rebuilding would replace a good on-disk cache with an empty one on the strength of a transient I/O error. This is the only use of `ErrBlind`, and it is why the sentinel exists.
2. `vis := l.visibleActive()` — **active records only**, per §8.3 item 3 and 00-ARCHITECTURE §3.3. There is no code path in this package that feeds the bloom from a checkpoint, a summary, or context; a CI grep test asserts that the only non-test `sketch.RebuildBloom` call site under `internal/` is this function and that its key iterator's only source is `visibleActive`.
3. `need := 2 * len(vis)`. `capacity := l.cfg.Sketches.Bloom.Capacity`; `fpRate := l.cfg.Sketches.Bloom.FPRate` (the field is `FPRate`); `if need > capacity { capacity = roundUpPow2(need) }`. **The configured capacity is honoured whenever it is sufficient** — that is what keeps a normal-sized project's `tried.bloom` at the Appendix A figure (`n = 10_000, p = 0.01 → m ≈ 95_850 bits ≈ 12 KB, k = 7`) instead of silently allocating a multiple of it. Growth happens only when the key count genuinely exceeds the configured capacity, and step 5 is what handles the boundary where the resulting fill ratio still lands above `ResizeTarget`'s 0.5 threshold.
4. `keys := func(yield func([]byte) bool) { for _, r := range vis { if !yield(r.Desc.Key()) { return }; if !yield(r.Desc.MatchKey()) { return } } }`
5. `nb := sketch.RebuildBloom(capacity, fpRate, keys)`.
6. `if c, f, needed := nb.ResizeTarget(); needed { nb = sketch.RebuildBloom(c, f, keys) }` — **at most one retry**, never a loop. If the rebuilt filter still reports `needed`, log `Loud("negknow: bloom saturated after resize", "records", len(vis), "fill", nb.FillRatio(), "fp", nb.EstimatedFPRate())` and continue with it; a saturated bloom degrades to more false positives, each of which is then resolved by the record lookup. (One retry is sufficient in practice because `ResizeTarget` doubles: with `k` rounded up from `(m/n)·ln 2 = 6.64` to `7`, a filter loaded exactly to capacity sits at fill ≈ 0.52, just over the threshold, and one doubling drops it to ≈ 0.31.)
7. **Persist — through the sanctioned door, and through nothing else.** `sketches/tried.bloom` is
   §7.4 append-only: `paths.WriteAtomic` refuses it outright through `paths.IsProtected`, and
   `sketch.Save` refuses it too, returning `core.ErrAppendOnly` + `sketch.ErrGenerational` whose whole
   message is *use the other door*. That door is `sketch.ReplaceGenerational`, which owns the §3.3
   procedure — the pre-flight against the surviving generation, the rename to `tried.bloom.<seq>.bak`,
   the staging and swap in `paths.ReplaceBloom`, the one-generation prune, and the checked rollback if
   staging fails. **This subplan re-implements none of it.** Concretely:
   - `l.seq++` (seeded at `Open` from `paths.HighestBloomBackupSeq`, so the first rebuild of a restarted
     daemon passes a `seq` above every surviving backup — which `ReplaceGenerational` checks *before*
     anything moves, and refuses with `ErrMalformed` if it does not hold).
   - `bak, err := sketch.ReplaceGenerational(filepath.Join(l.lay.Sketches, sketch.TriedBloomBase), nb, l.seq)`.
     The marshal happens inside it, so a sketch that cannot encode moves nothing on disk. `bak` is the
     displaced generation's path, or `""` when there was no previous file.
   - The backup is named with the **incoming** `seq`, not `seq-1`: `paths.ReplaceBloom` writes
     `tried.bloom.<seq>.bak` from the seq it was handed, and `paths.pruneBloomBackups` parses that same
     name and keeps the **highest** sequence. A `seq-1` spelling would be pruned the instant it was
     created, and a staging failure at that point would leave no `tried.bloom` at all. Exactly one
     `.bak` exists after each rebuild, and this function does not remove backups itself.
   - `err != nil`: log `Loud`, keep the **new in-memory** bloom (it is correct), leave the on-disk file
     as `ReplaceGenerational` left it — rolled back — and return the error alongside the bloom and
     health. The caller is not obliged to fail. `l.seq` is **not** decremented: a burned sequence
     number costs nothing and re-using one would violate the next call's pre-flight.
   - Nothing else is written. There is no `state/` file, no hand-rolled `tmp/` staging, and no
     `os.Rename` anywhere in `internal/negknow`; a grep test asserts the package contains no
     `os.Rename` and no `paths.WriteAtomic`/`paths.CreateNew` call naming `tried.bloom`.
8. `l.bloom = nb; l.pending = false`; increment `negknow.bloom.rebuilds`; observe `negknow.rebuild`; return `(nb, l.health(), err)`.

```go
func (l *ledger) Health() Health
```

`Records` = `len(l.recs)` (every materialized record, all statuses and sessions). `Active` = `len(l.visibleActive())`. `Stale` = count of `l.recs` with `Status == StatusStale`. `FillRatio` = `l.bloom.FillRatio()`. `EstFPRate` = `l.bloom.EstimatedFPRate()`. `NeedsResize` = the third return of `l.bloom.ResizeTarget()`. Under `blind`, `FillRatio` and `EstFPRate` are `0` and `NeedsResize` is `false`. This is the struct `/qompack:status` prints (SP-14) and the §11.4 "monitor fill ratio and resize" watch-for.

### `internal/negknow/ingest.go` — the four §8.3 sources

Shared helper:

```go
// resolveDeps turns caller-supplied paths into evidence-bearing core.Dep entries.
// A path with no known version in the store cannot serve as a staleness baseline, so it is
// SKIPPED and named in warnings rather than recorded with a zero hash.
func (l *ledger) resolveDeps(ctx context.Context, paths_ []string, target string) ([]core.Dep, []string)
```

1. Start from the explicit list; if it is empty, derive defaults: the target's own path first, then every entry of `autoDepCandidates` that has a known version:
   `{"package-lock.json","yarn.lock","pnpm-lock.yaml","go.sum","Cargo.lock","poetry.lock","requirements.txt","Gemfile.lock","composer.lock","docker-compose.yml","docker-compose.yaml","Dockerfile"}` — in exactly that order, matching the §8.3 example set (compose file, lockfile).
2. For each candidate: `paths.Key`, then `l.deps.Store.FileHistory(ctx, key)`. Take the entry with the greatest `TS`; use its `Root` as the dep hash. No history, or a nil store → skip and append `"no stored version for <path>; not used as a staleness dependency"` to warnings.
3. Deduplicate on path (first wins), cap at `maxAutoDeps = 8`, sort ascending by path.

```go
func (l *ledger) IngestMCP(ctx context.Context, a MCPArgs) (Record, []string, error)     // source #1
```

Evidence: use `a.Evidence` when non-zero; otherwise mint one by storing the reason text —
`res, err := l.deps.Store.PutBytes(ctx, []byte(a.Reason), store.PutOptions{Tool: "record_eliminated", Ephemeral: false})` and take `res.Root.Hash`. This routes agent-authored reason text through SP-06's redaction choke point and gives the record a real, retrievable evidence object. If the store is nil and `requireEvidence` is set, return `ErrNoEvidence`. Scope: `a.Scope` when it parses to `session`/`project`, else the configured default, else `session` (an unparseable value adds a warning). `Source = SourceMCP`.

```go
func (l *ledger) IngestPin(ctx context.Context, a PinArgs) (Record, []string, error)     // source #2
```

Same as `IngestMCP` except `Evidence` arrives as text and is parsed with `core.ParseHash` (a parse failure is a warning, then falls through to the mint-from-reason path) and `Source = SourceSlashCommand`. This is the backend `/qompack:pin --eliminated` calls; SP-14 owns the flag parsing and the output rendering.

```go
func (l *ledger) IngestUserStatement(ctx context.Context, p UserStatement) ([]Record, error)  // source #4
```

1. Normalize the prompt: lowercase, collapse whitespace, strip `'` and `’` (so "didn't"/"didn’t"/"didnt" all match after the apostrophe is removed).
2. Match against the closed phrase list, in this order, taking the first hit:
   `"that didnt work"`, `"that did not work"`, `"that doesnt work"`, `"that does not work"`, `"we tried that"`, `"we already tried"`, `"i tried that"`, `"already tried"`, `"that didnt help"`, `"didnt fix it"`, `"thats not it"`, `"that was a dead end"`, `"no luck with"`, `"we ruled that out"`.
3. No hit → `(nil, nil)`.
4. A hit with no resolvable target (`p.Path == "" && p.Symbol == ""`) or no approach text (`p.Approach == ""`) → `(nil, nil)` and increment `negknow.user_statement.unresolved`. Guessing a target here would manufacture a wrong elimination, which is exactly the stale-block failure mode the design rates High.
5. Otherwise build one record: `Target = p.Path` (plus `":"+p.Symbol` when a symbol is known), `Approach = p.Approach`, `Reason = "user stated: " + trimmed original prompt (bounded to 2048)`, `Evidence = p.PromptRoot` (falling back to a `PutBytes` mint of the prompt text), `Source = SourceUserStatement`, deps from `resolveDeps(ctx, nil, target)`. Call `Record`.

```go
func (l *ledger) Observe(ctx context.Context, o Observation) error
```

Appends one JSON line to `<root>/.qompack/records/signals.jsonl` (append-only, same discipline as the elimination log) and keeps a bounded in-memory ring of the last `signalRing = 512` observations. SP-08 calls it from `PostToolUse`; when nobody calls it, the detector simply finds nothing and source #3 is inert — no correctness risk. The signals log is not the source of truth for anything; it is regenerable and GC-eligible.

**The ledger is the `ObservationSource`.** `*ledger` also implements

```go
func (l *ledger) Since(turn core.TurnIndex) []Observation
```

which returns, under `RLock`, a copy of the ring entries with `Turn >= turn` in ascending `(Turn, Kind, Path)` order. `ledger.go` carries `var _ ObservationSource = (*ledger)(nil)` alongside the `Maintainer` assertion, and because `Open` returns the `Ledger` interface the intended construction from another package is:

```go
src, ok := led.(negknow.ObservationSource)     // always true for the value Open returns
det := negknow.NewDetector(src, sess, cfg, clk)
```

`TestOpenReturnsObservationSource` asserts the assertion succeeds and that `Since(0)` on a fresh ledger returns an empty, non-nil slice. `ObservationSource` is deliberately kept out of `Maintainer` so `Maintainer` stays at the seven methods the interface listing declares.

### `internal/negknow/detector.go` — source #3, the DAG heuristic

```go
func NewDetector(src ObservationSource, sess core.SessionID, cfg config.Config, clk core.Clock) Detector
func (d *detector) Scan(ctx context.Context, g dag.Graph, since core.TurnIndex) ([]Record, error)
```

**Pattern P (normative).** Over observations with `Turn >= since`, sorted by `(Turn, Kind, Path)`, within a window of `detectWindowTurns = 12` turns:

1. `e0 = ObsEdit` on path `p` at turn `t0`, with non-empty `Detail` (the approach text).
2. `f1 = ObsTestFail` at turn `t1` with `t0 < t1 <= t0+detectWindowTurns` and (`f1.Path == p` **or** `f1.Path == ""`), and no `ObsTestPass` on `p` in `(t0, t1)`.
3. `r2 = ObsRevert` on `p` at turn `t2` with `t1 < t2 <= t0+detectWindowTurns`.
4. `e3 = ObsEdit` on `p` at turn `t3` with `t2 < t3 <= t0+detectWindowTurns` and
   `ApproachClass(e3.Detail) != ApproachClass(e0.Detail)`.

Each `e0` participates in at most one emission (the earliest satisfying `(f1, r2, e3)`); scanning is a single forward pass with a per-path cursor, so the cost is O(observations) plus O(1) DAG lookups per candidate.

**The graph this scans is not acyclic, and `Scan` must terminate on it anyway** (the wave-1 INHERIT above; ADR 0007). §8.1 item 4 directs a shared-file edge *into* a tool use that consumed a file and *out of* one that produced it, so the ordinary Read-then-Edit pattern closes a legal loop — `tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1` — and `TestReadThenWriteClosesALegitimateCycle` (`internal/dag/builders_test.go`) pins it so it cannot be assumed away. A naïve recursive descent over these edges does not terminate. **The termination argument for `Scan`, stated rather than assumed:** the outer loop is a single forward pass over a finite observation slice, each `e0` emitting at most once; the graph is touched only by the **bounded one-hop** `g.In(n.ID)` / `g.Out(n.ID)` fan-out described next, whose results are read for their endpoints and never followed; there is no recursion and no work queue, so no edge can be traversed a second time and the cycle above is simply two of the one-hop neighbours of `tooluse:t1`. If a future change ever needs a walk deeper than one hop, it carries an explicit `visited map[dag.NodeID]struct{}` and a hop cap — a recursive descent without one is not an option in this package. `TestDetector_TerminatesOnCyclicGraph` builds exactly that cycle and requires `Scan` to return; V3-VERIFY §0a item 1 owns verifying it against a cyclic fixture rather than only against the acyclic synthetic generator.

**What the DAG contributes: `depends_on`.** For the candidate's `f1.ToolUse`, look up `n, ok := g.Node(dag.ToolUseNode(f1.ToolUse))` — the constructor, never `dag.NodeID("tooluse:" + …)`, because concatenation skips `sanitizeKey` and a long or control-bearing key then yields an id `dag.ToolUseNode` never produces. When found, walk `g.In(n.ID)` and `g.Out(n.ID)` — **one hop, no recursion** — keep edges of kind `dag.EdgeConsumes` or `dag.EdgeSharedFile` whose other endpoint resolves via `g.Node` to a `dag.KindFile` node with a non-zero `Root`, and emit `core.Dep{Path: paths.Key(fileNode.Ref), Hash: fileNode.Root}`. Deduplicate by path (first wins), sort ascending, cap at `maxAutoDeps`. If `g` is nil, the node is absent, or the walk yields nothing, fall back to `resolveDeps(ctx, nil, target)`. This is the whole of the DAG's role: it answers "which files did the failing test actually read," which is exactly what the elimination's reason rests on.

Emitted record:

- `Target` = `p` (plus `":"+e0.Symbol` when `e0.Symbol != ""`)
- `Approach` = `e0.Detail`
- `Reason` = `fmt.Sprintf("test failed at turn %d and the change was reverted at turn %d; a different approach (%s) was taken at turn %d", t1, t2, ApproachClass(e3.Detail), t3)`
- `Evidence` = `f1.Root` (the stored failing-test output). When zero and `requireEvidence` is set, the candidate is **dropped**, counted in `negknow.detector.dropped_no_evidence`, and logged at `Debug`.
- `DependsOn` as above; `Scope` = configured default; `Status` = active; `Source` = `SourceHeuristic`; `Session` = the detector's session; `TS` = `f1.TS` when non-zero else `clk.Now()`.

`Scan` returns the candidate records **without** appending them. The caller (SP-08's observer, or SP-12's idle task) decides when to call `Ledger.Record`; a test drives both. Counters: `negknow.detector.candidates`, `negknow.detector.emitted`.

### `internal/negknow/dagedges.go`

```go
// Both go through SP-07's exported constructors. Concatenating "elimination:"/"file:" onto a key
// compiles and parses, but it skips dag's sanitizeKey — control bytes, invalid UTF-8 and keys over
// 384 bytes are all rewritten there — so a hand-built id can differ from the constructor's for the
// same input and silently fork the graph into two disconnected halves with no error anywhere
// (internal/dag/nodeid.go).
func NodeIDFor(r Record) dag.NodeID  { return dag.EliminationNode(r.ID) }
func FileNodeID(p string) dag.NodeID { return dag.FileNode(paths.Key(p)) }

func (l *ledger) emitDAG(r Record) // best-effort; every error logged at Debug and swallowed
```

Adds one node `{ID: NodeIDFor(r), Kind: dag.KindElimination, Turn: 0, TS: r.TS, Ref: r.ID, Root: r.Evidence}` and, when `r.Desc.NormalizedPath != ""`, one edge `{From: NodeIDFor(r), To: FileNodeID(r.Desc.NormalizedPath), Kind: dag.EdgeExplains, Weight: 1}`. This is what lets SP-11 rank eliminations by slice score (§8.6 item 3) and SP-15 count them as high-Δ content (G6.3). The two ID conventions are pinned in `testdata/golden/contracts/negknow/node-ids.json` so a divergence from SP-07/SP-08's naming is caught at the wave-2 verification checkpoint rather than at runtime, and `TestNodeIDGolden` additionally asserts `FileNodeID` equals `dag.FileNode(paths.Key(p))` for a path key over 384 bytes — the case where a concatenated spelling and the constructor's output actually diverge.

### `internal/negknow/negknowtest/suite.go` *(modify — SP-01 shipped it with skips)*

Remove every `t.Skip` and add the behaviour assertions the suite promises: three-way `Query`, scope isolation, `requireEvidence`, staleness flip and note text, rebuild-from-active-only, `BloomOnly` on a synthetic false positive, blind-mode absence, and append-only enforcement. The exported entry point keeps the §5.22 suite shape and is not renamed:

```go
// package negknowtest
func RunLedgerSuite(t *testing.T, name string, factory func(t *testing.T) negknow.Ledger)
```

The suite runs against any `Ledger` factory, so SP-13's daemon-backed ledger can reuse it verbatim. Assertions that need more than the `Ledger` interface (rebuild-from-active-only, blind mode) obtain it with `m, ok := led.(negknow.Maintainer); require.True(t, ok)` — a factory whose value lacks the extended surface **fails** the suite. It never skips. That is what stops Rule W-1's "no `t.Skip` remains" check from being satisfiable by weakening the factory instead of implementing the behaviour.

### Performance budgets

| Operation | Budget | Where it comes from | Benchmark |
|---|---|---|---|
| `Query` (bloom hit, 5 000 records) | p99 < 50 µs in-process | must be a rounding error inside B-F (`mcp_tool_call` p95 < 250 ms, 00-ARCH §2.4) | `BenchmarkQueryHit` |
| `Query` (bloom miss) | p99 < 5 µs | one bloom `Test`, no map lookup | `BenchmarkQueryMiss` |
| `Record` (append + bloom add + DAG emit) | p99 < 5 ms | on the MCP path, not the L0 hot path | `BenchmarkRecord` |
| `RebuildBloom` (5 000 active records, 10 000 keys) | < 50 ms | §8.3 item 3 "cheap, because rebuild is a linear pass over a few thousand structured entries" | `BenchmarkRebuildBloom` |
| `RefreshStaleness` ledger-side work (5 000 records, 2 000 distinct deps), excluding `ChangedSince` | < 10 ms | one `ChangedSince` call by construction | `BenchmarkRefreshStaleness` |
| `Open` materialization (20 000 log lines) | < 150 ms | daemon start / first ledger use, off the hot path | `BenchmarkOpen` |
| `Detector.Scan` (5 000 observations, 5 000 DAG nodes) | < 5 ms | §6.4 "sub-millisecond BFS over a few thousand nodes"; we add a linear pass | `BenchmarkDetectorScan` |
| Resident memory, 5 000 records | < 4 MB | ~400 B/record + two 32 B keys | asserted in `TestMemoryFootprint` via `runtime.ReadMemStats` |
| `tried.bloom` on disk at the Appendix C defaults (capacity 10 000, fp 0.01), loaded below capacity | 11–14 KB | Appendix A: `m ≈ 95 850 bits ≈ 12 KB, k = 7` | `TestBloomFileSize` |

`nomagic` note: `10000`, `2048`, `1024` and `4096` are in the forbidden-literal set (00-ARCH §11.6). Bloom capacity and FP rate are read from `cfg.Sketches.Bloom`; benchmark and test literals live in `*_test.go`, which the pass exempts. The only package-level numeric constants are `maxClassTokens = 4`, `maxTokenBytes = 24`, `maxAutoDeps = 8`, `maxDeps = 32`, `maxStaleBecause = 32`, `detectWindowTurns = 12`, `signalRing = 512`, `openRefreshDeadline = 250 * time.Millisecond`, and the field bounds (`512`, `2048`→ expressed as `maxReasonBytes = 2 * maxTextBytes` with `maxTextBytes = 512` to keep `2048` out of the source), none of which appear in the forbidden set. The log scanner's 1 MiB buffer is written `1 << 20`, matching `runtime.hotPath.maxPayloadBytes`'s order of magnitude without duplicating the `1048576` literal.

### Error handling by failure mode

| Failure | Response |
|---|---|
| `records/eliminations.jsonl` unreadable at `Open` | `blind = true`; `Loud` once; `Query` answers `AnswerAbsent` for everything (never a false positive, §12.3); `Record` still attempts to append and still updates the in-memory index, so `Health().Records` counts what this session added; `blind` clears only at the next successful `Open` |
| `RebuildBloom` called while `blind` | returns `(current bloom, health, ErrBlind)` and touches no file — rebuilding from records we cannot read would destroy a good on-disk cache over a transient I/O error. `Open`'s consistency check and the idle `MaintenanceTask` both skip the rebuild under `blind` for the same reason |
| Any write method called after `Close` | `os.ErrClosed`, nothing appended; read methods keep answering from the in-memory index |
| A single corrupt/unparseable log line | skipped, counted, `Warn` with line number; materialization continues |
| Truncated final line (crash mid-append) | skipped silently; expected shape |
| Unknown `op` value | skipped, counted, `Warn`; forward compatibility |
| `sketch.LoadWithLog` returns `core.ErrNotFound` (missing or bad CRC) | rebuild from active records immediately; if that fails, empty bloom + `pending`. The Loud line comes from `LoadWithLog` itself ("sketch corrupt — rebuilding from records"), which is why `Load` must not be called here — it runs on `logging.Nop()` and writes nothing. `errors.Is(err, sketch.ErrCorrupt)` is what separates bit rot from a cold start: true ⇒ also increment `negknow.bloom.corrupt_on_load`; false ⇒ an ordinary first session, no counter, no second log line |
| `b.Count() < 2*len(visibleActive)` | rebuild immediately (missing keys ⇒ false negatives ⇒ the feature silently stops working) |
| `b.Count() > 2*len(visibleActive)` | safe; rebuild per `rebuildOnStale` |
| `store.ChangedSince` errors | `Warn`, flip nothing, return the error; ledger stays usable |
| `store.PutBytes` errors while minting evidence | if `requireEvidence`, return `ErrNoEvidence`; else record with a zero evidence hash and `Warn` |
| `dag.AddNode`/`AddEdge` errors | `Debug`, swallowed; never fails `Record` |
| `sketch.ReplaceGenerational` fails during bloom persistence | `Loud`, keep the new in-memory bloom, return the error; `ReplaceGenerational` has already rolled the displaced generation back, and `l.seq` is not decremented |
| `sketch.ReplaceGenerational` returns `ErrMalformed` on its seq pre-flight | a rebuild counter that did not resume above the surviving backup — treat as the persistence failure above, `Loud` with the seq, and re-seed `l.seq` from `paths.HighestBloomBackupSeq` before the next attempt |
| `requireEvidence` violated | `ErrNoEvidence`, nothing appended, counter incremented |
| A caller passes `Status: "stale"` to `Record` | normalized to active, `Warn`; `MarkStale` is the only path to stale |

---

## Test plan (TDD)

Every test below is written and run **before** the implementation it covers, inside the commit that adds that implementation. `testify/require` (fail-fast; `assert` is banned), `go-cmp` for structural diffs, `pgregory.net/rapid` for the property tests, `testutil.NewProject` for the temp project, `testutil.FakeClock` for every time-dependent test.

### Fixtures

| Fixture | Contents |
|---|---|
| `testdata/golden/contracts/negknow/eliminations.golden.jsonl` | 6 lines: 4 bare records (2 project-scope, 2 session-scope, one with no `depends_on`, one with 2 deps), 1 `op:"stale"` control line, 1 unknown-`op` forward-compat line. The first record line is `want/elimination_record.jsonl`'s single line copied verbatim, so a change to the record wire form breaks this golden and the frozen fixture together |
| `testdata/golden/contracts/negknow/want/elimination_record.jsonl` *(frozen, SP-01)* | the byte-level authority for one record line: `elim_` id, integer `source`, `core.Dep`'s `path`/`hash` keys, every hash as `"sha256:…"`. **Never regenerated** — `TestRecordJSON_Golden` marshals a `Record` built from literals transcribed out of it and requires equality. No `record.golden.json` is added; a second, self-generated copy of a frozen shape is exactly the drift the freeze exists to prevent |
| `testdata/golden/contracts/negknow/descriptors.golden.json` | 24 `(target, approach, reason)` → `(path, symbol, class, reason_hash_hex, key_hex, match_key_hex)` rows, including at least one symbol-less target, one `#`-separated target, one drive-letter target and one `unclassified` approach — pins hashing stability across platforms and Go versions. Row 1 is `key_test.go`'s `fixedDescriptor` spelled as a descriptor literal, and its `key_hex` **is** `wantDescriptorKeyHex` character for character; that row is the self-check that the file was not regenerated around a changed `Key()` |
| `internal/negknow/fakestore_test.go` | the counting/​scripted `store.Store` used by `staleness_test.go`, `ingest_test.go` and `detector_test.go`: records `ChangedSince` call count and arguments, serves a scripted `FileHistory` per path, and returns a deterministic root from `PutBytes`. Every other `store.Store` method returns `core.ErrNotImplemented`; the compile-time assertion `var _ store.Store = (*fakeStore)(nil)` keeps it honest against SP-06's interface |
| `testdata/golden/contracts/negknow/node-ids.json` | `{"elimination":"elimination:elim_3f9b2c7d1a48","file":"file:src/auth.ts"}` — the long-form prefixes `internal/dag/nodeid.go` fixes, and the frozen fixture's `elim_` record id |
| `testdata/golden/contracts/negknow/corrupt.jsonl` | 5 lines: valid, truncated JSON, empty, `{"op":"bogus"}`, valid |

### `classify_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestApproachClass_Table` | the 8-row worked-example table in the spec | exact string match on every row |
| `TestApproachClass_SynonymFixpoint` | every value in `synonyms` | `synonyms[v] == v` for all values |
| `TestApproachClass_SilentERestore` | `"increasing"`, `"disabling"`, `"reducing"`, `"rewriting"`, `"upgrading"`, `"migrating"`, `"introducing"`, `"removing"`, `"deleting"`, `"enlarging"`, `"downgrading"`, `"memoizing"`, `"cached"` | classify to `widen`, `disable`, `shrink`, `rewrite`, `upgrade`, `upgrade`, `enable`, `disable`, `disable`, `widen`, `downgrade`, `cache`, `cache` — i.e. step 4 of `classifyToken` fires for each |
| `TestApproachClass_DoubledConsonantLimitation` | `"dropping"`, `"skipping"` | classify to `dropp`, `skipp` — the documented, accepted limitation, pinned so a future change is deliberate |
| `TestApproachClass_Deterministic` | 1 000 rapid-generated ASCII strings, classified twice | identical both times |
| `TestApproachClass_Bounded` | rapid: strings up to 4 KB, including a single 4 KB word with no separators | result has ≤ 4 `-`-separated tokens, every token ≤ 24 bytes, whole result ≤ 128 bytes, valid UTF-8 |
| `TestApproachClass_Empty` | `""`, `"   "`, `"!!! ???"`, `"the a of"` | all `"unclassified"` |
| `TestLemma` | `"increasing"→"increas"`, `"widened"→"widen"`, `"timeouts"→"timeout"`, `"retries"→"retry"`, `"indices"→"indice"`, `"pass"→"pass"`, `"is"→"is"` | exact |

### `descriptor_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestSplitTarget_Table` | `"src/auth.ts:refreshToken"`, `"src/auth.ts"`, `"C:/proj/src/auth.ts"`, `"src/a.ts#Foo.bar"`, `"pkg/mod.go:12"`, `""`, `":sym"` | `("src/auth.ts","refreshToken")`, `("src/auth.ts","")`, the drive-letter case splits nowhere and yields `paths.Key("C:/proj/src/auth.ts")` — `("c:/proj/src/auth.ts","")` on Windows and macOS, `("C:/proj/src/auth.ts","")` on Linux, so the row is asserted against `paths.Key` of the input rather than a hardcoded string, `("src/a.ts","Foo.bar")`, `("pkg/mod.go","")` (numeric suffix fails the symbol regex), `("","")`, `("","sym")` |
| `TestDescriptorKey_Golden` | `descriptors.golden.json` | every `key_hex` and `match_key_hex` matches byte-for-byte |
| `TestDescriptorKey_Stable` *(already shipped in `internal/negknow/key_test.go`; not rewritten)* | `fixedDescriptor` | `Key()` equals `wantDescriptorKeyHex`. Listed here because it is the gate on the frozen byte layout: it must be green before, during and after every commit of this subplan, and no commit may edit `key_test.go` or `Descriptor.Key` |
| `TestMatchKey_IgnoresReason` | same target+approach, three different reasons | identical `MatchKey`, three distinct `Key` |
| `TestMatchKey_SeparatorInjection` | target `"a\x1Fb"` (no `:` or `#`) vs the pair path `"a"` / symbol `"b"` | distinct `MatchKey`s: `sanitizeField` replaces every `0x1F` in a descriptor field with `0x20` before hashing, so the first case hashes `"a b"` as the path with an empty symbol and cannot collide with the second. The assertion is on `MatchKey` **only** — `Key`'s layout is frozen and takes its fields raw, so it is not the subject of this test |
| `TestReasonHash_Normalization` | `"Pgbouncer  1.18\n IGNORES it"` vs `"pgbouncer 1.18 ignores it"` | identical hashes |
| `TestKey_Length` | any descriptor | `len(Key()) == 32 && len(MatchKey()) == 32` |
| `TestKey_NoAliasing` (property, rapid) | mutate the returned slice, re-call | second call returns unmutated bytes |
| `TestDescriptorJSON_Standalone` | `json.Marshal(Descriptor{"src/auth.ts","refreshToken","widen-pool-timeout",h})` | `{"normalized_path":"src/auth.ts","symbol":"refreshToken","approach_class":"widen-pool-timeout","reason_hash":"sha256:<64 hex>"}`; `Unmarshal` round-trips exactly |

### `record_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestRecordJSON_Golden` | a `Record` built from literals transcribed out of the frozen `want/elimination_record.jsonl` (id `elim_3f9b2c7d1a48`, `Source: SourceSlashCommand`, descriptor `approach_class:"widen-timeout"` — **not** `Canonicalize`'s output) | `MarshalJSON` output equals that fixture's single line byte-for-byte, `source` included as the integer `1`; a second assertion projects out §8.5's seven keys and compares them against a literal transcribed from the design document |
| `TestRecordJSON_RoundTrip` (property, rapid) | 500 generated records | `Unmarshal(Marshal(r)) == r` under `cmp.Diff` |
| `TestRecordJSON_NilDependsOn` | `DependsOn: nil` | marshals to `"depends_on":[]`; unmarshals back to a nil-or-empty slice that round-trips |
| `TestRecordJSON_MissingFields` | `{"id":"x","target":"t","approach":"a","reason":"r"}` | `Scope=="session"`, `Status=="active"`, `Source==SourceMCP`, no error |
| `TestRecordJSON_BadDepHash` | one dep with `"hash":"nothex"` | dep dropped, no error, warning surfaced |
| `TestSourceKind_RoundTrip` | all four kinds | `ParseSourceKind(k.String()) == k`; `ParseSourceKind("nope")` errors; `SourceSlashCommand == 1`, matching the two frozen fixtures' `"source":1` |
| `TestSourceKind_WireFormIsInteger` | `json.Marshal` of a `Record` with each of the four kinds | `"source":0`…`"source":3`; the marshalled bytes never contain `"source":"mcp"` or any other string form |
| `TestNormalizeRecord_Bounds` | 4 KB target, 8 KB reason, 40 deps | 512/2048-byte truncation at UTF-8 boundaries with `…`, 32 deps kept |
| `TestNormalizeRecord_Redact` | `Redact` replacing `"sk-live"` with `"«redacted:apikey»"` | reason contains the placeholder, never the secret |
| `TestRecordID_Stable` | fixed session/ts/descriptor | `"elim_"` + 12 lowercase hex — the prefix and shape the frozen fixture's `"id":"elim_3f9b2c7d1a48"` fixes — identical across runs and platforms |

### `log_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestReplayLog_Golden` | `eliminations.golden.jsonl` | 4 records; the one named by the `stale` line has `Status=="stale"`, `StaleSince==1754985600000`, `StaleBecause` of length 1; the unknown-`op` line is skipped |
| `TestReplayLog_Corrupt` | `corrupt.jsonl` | 2 records recovered, `negknow.log.corrupt_lines == 1`, `negknow.log.unknown_op == 1`, no error |
| `TestReplayLog_TruncatedTail` | golden file with the last 20 bytes chopped | all but the last record recovered, no error, no corrupt-line count |
| `TestReplayLog_DuplicateAdd` | two record lines with the same ID and different reasons | first wins; `negknow.log.duplicate_add == 1` |
| `TestReplayLog_BareRecordLine` | the single line of the frozen `want/elimination_record.jsonl` as the whole log | 1 record, `ID=="elim_3f9b2c7d1a48"`, `Source==SourceSlashCommand`, `Status==StatusActive` — the reader accepts an unwrapped record, which is the on-disk form |
| `TestReplayLog_OrphanStale` | a `stale` line for an unknown ID | skipped, counted, no error |
| `TestAppendOnly_Enforced` | attempt `os.OpenFile(..., O_TRUNC)` on the log, then `p.AssertAppendOnly(t)` | `core.ErrAppendOnly`; the guard test passes |
| `TestAppendLine_NoInteriorNewline` (property, rapid) | records with `\n`, `\r`, `\u2028` in every text field | exactly one `\n` per appended line; re-materialization recovers the original strings |

### `ledger_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestQuery_Absent` | empty ledger, `Query("src/a.ts","widen timeout", ScopeSession)` | `State==AnswerAbsent`, `Record==nil`, `BloomOnly==false` |
| `TestQuery_Active` | record `("src/auth.ts:refreshToken","widen pool timeout","pgbouncer 1.18 ignores it in transaction mode")`, then query the same target/approach | `State==AnswerActive`, `Record.Reason=="pgbouncer 1.18 ignores it in transaction mode"`, `Note==""` |
| `TestQuery_ActiveViaSynonym` | recorded as `"widen pool timeout"`, queried as `"Increasing the connection-pool timeouts"` | `AnswerActive` — the class canonicalization is what makes this work |
| `TestQuery_Stale_FlagNote` | record, `MarkStale`, `staleResponse:"flag"` | `State==AnswerStale`, `Note == negknow.StaleNote` compared against the §8.3 string literal **including the em dash**, `Record.Reason` still returned |
| `TestQuery_Stale_Drop` | same with `staleResponse:"drop"` | `State==AnswerAbsent`, `Record==nil`, `Note==""` |
| `TestQuery_StaleBeforeRebuild` | flip stale, do **not** rebuild the bloom | still `AnswerStale` — proves `nextIdle` is safe |
| `TestQuery_BloomOnly` | inject a key directly into the bloom for a descriptor with no record | `State==AnswerAbsent`, `BloomOnly==true`, `Record==nil`; counter `negknow.query.bloom_only == 1` |
| `TestQuery_PrefersActiveOverStale` | two records, same `MatchKey`, one stale (later TS) one active (earlier TS) | `AnswerActive` |
| `TestQuery_ScopeSession_OtherSessionHidden` | session-scoped record from `"sess-A"`; reopen the ledger for `"sess-B"` with `rebuildOnStale:"immediate"` | `AnswerAbsent`, `BloomOnly==false` — `Open` rebuilt the bloom from `visibleActive()`, which excludes the foreign session's keys |
| `TestQuery_ScopeSession_OtherSessionHidden_NextIdle` | same, but `rebuildOnStale:"nextIdle"` so the on-disk bloom still carries sess-A's keys | `AnswerAbsent`, `BloomOnly==true` — the record lookup, not the bloom, is what makes the answer correct (§13 invariant 3); after `RebuildBloom` the same query returns `BloomOnly==false` |
| `TestQuery_ScopeProject_CrossSession` | project-scoped record from `"sess-A"`, ledger for `"sess-B"` | `AnswerActive` under both `ScopeSession` and `ScopeProject` |
| `TestQuery_ScopeProject_ExcludesSessionScoped` | own-session record, queried with `ScopeProject` | `AnswerAbsent` |
| `TestRecord_RequireEvidence` | `requireEvidence:true`, record with zero `Evidence` | `ErrNoEvidence`; log file unchanged (byte-compared before/after); counter incremented |
| `TestRecord_EvidenceOptional` | `requireEvidence:false`, zero evidence | succeeds |
| `TestRecord_Idempotent` | identical record twice, same `FakeClock` instant | same ID, one log line, `nil` error both times |
| `TestRecord_IdempotentAcrossTimestamps` | identical target/approach/reason recorded twice with the clock advanced 30 s between calls — the MCP-retry shape | same ID returned both times, still one `add` line, `negknow.records.deduped == 1`; proves the dedup is identity-based (`byKey`) and not `byID`-based, since `recordID` mixes in `TS` |
| `TestRecord_ReRecordAfterStaleIsNotDeduped` | record, `MarkStale`, then record the identical elimination again | a **new** active record with a different ID; `Health().Records == 2`, `Active == 1`, `Stale == 1`; `Query` returns `AnswerActive` — re-verification after a staleness flip must not be swallowed by the dedup |
| `TestRecord_RejectsPresetStale` | `Status: StatusStale` | stored as active, `Warn` logged |
| `TestRecord_DescriptorRecomputed` | caller supplies a bogus `Desc` | stored descriptor equals `Canonicalize(target, approach, reason)` |
| `TestGetActiveAll` | 5 records, 2 stale, 1 foreign-session | `Get` by ID returns copies; `Active(ScopeSession)` returns 2 in TS-ascending order; `All` returns 5 |
| `TestTopActive` | 5 active records, scores `{r3:0.9, r1:0.4}` | order `[r3, r1, …]` by score then TS-descending; `n=2` returns 2 records and `remaining==3` |
| `TestAnswerMCPResult` | one answer of each state, plus `Answer{State: AnswerActive, Record: nil}` | `("active", reason, "", "sha256:<64 hex>")`, `("stale", reason, StaleNote, "sha256:<64 hex>")`, `("absent","","","")`, and the nil-record case returns `("absent","","","")` without panicking |
| `TestBlindMode` | chmod/lock the records file so it cannot be read, then `Open` | ledger opens, `Query` returns `AnswerAbsent` with `BloomOnly==false` for a descriptor that *is* in the bloom; `Loud` fired once |
| `TestClose_Idempotent` | `Close()` twice | second call returns `nil` |
| `TestOpenReturnsMaintainer` | `led, _ := negknow.Open(...)` | `led.(negknow.Maintainer)` succeeds; every one of the seven `Maintainer` methods is callable |
| `TestOpenReturnsObservationSource` | same value | `led.(negknow.ObservationSource)` succeeds; `Since(0)` returns an empty, non-nil slice |
| `TestClosedLedgerRejectsWrites` | `Close()`, then `Record`/`MarkStale`/`Observe`/`RebuildBloom` | each returns `os.ErrClosed` and appends nothing; `Query`/`Get`/`Active`/`All`/`Health` still answer from memory |
| `TestConcurrentRecordQuery` (`-race`) | 8 goroutines × 200 `Record`, each goroutine using its own target prefix (`fmt.Sprintf("src/g%d/f%d.ts", g, i)`) so all 1 600 descriptors are distinct and none is deduped, + 8 × 500 `Query` | no race, final `Health().Records == 1600`, `negknow.records.deduped == 0` |

### `staleness_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestRefreshStaleness_Flips` | 3 records depending on `docker-compose.yml`; a fake store whose `ChangedSince` reports that dep changed | all 3 IDs returned sorted ascending; each has `Status=="stale"`, `StaleBecause==["docker-compose.yml: dependency hash changed from sha256:11aa22bb33cc"]` |
| `TestRefreshStaleness_NoChange` | `ChangedSince` returns empty | `(nil, nil)`; nothing flipped; no new log lines |
| `TestRefreshStaleness_SingleChangedSinceCall` | 5 000 records over 2 000 distinct deps; counting fake store | `ChangedSince` called exactly once with a deduplicated, path-then-hash-sorted slice of 2 000 deps |
| `TestRefreshStaleness_GroupsByBecause` | records A,B share dep X; record C depends on Y; both changed | `MarkStale` called exactly twice, groups iterated in ascending joined-because order, IDs sorted within each group |
| `TestRefreshStaleness_MultiDep` | one record depending on both X and Y, both changed | one `stale` line whose `because` has 2 entries, sorted by path ascending |
| `TestRefreshStaleness_NilStore` | `s == nil` | `(nil, nil)`, no error |
| `TestRefreshStaleness_StoreError` | `ChangedSince` returns an error | error propagated, nothing flipped, ledger still answers queries |
| `TestMarkStale_Idempotent` | `MarkStale` twice on the same ID | one flip; second call skipped and counted; `StaleBecause` replaced not appended |
| `TestMarkStale_UnknownID` | ID not in the ledger | no error, counted in `negknow.stale.skipped` |
| `TestRebuildOnStale_Immediate` | `rebuildOnStale:"immediate"` | `RebuildBloom` ran during `MarkStale`; `NeedsRebuild()==false`; `tried.bloom` mtime advanced |
| `TestRebuildOnStale_NextIdle` | default | `NeedsRebuild()==true`, `tried.bloom` untouched, `Query` still correct |
| `TestRebuildOnStale_Never` | `"never"` | `NeedsRebuild()==false` and no rebuild ever runs |
| `TestMaintenanceTask_Shape` | `MaintenanceTask(store)` | returns `("negknow.maintain", 30, fn)`; `fn(ctx)` refreshes then rebuilds; second call is a no-op |
| `TestOpenRefreshBounded` | fake store whose `ChangedSince` blocks 2 s | `Open` returns in < 500 ms with `NeedsRebuild()==true` |

### `bloom_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestRebuildBloom_ActiveOnly` | 10 records, 4 stale | new bloom `Test`s true for all 6 active `MatchKey`s and false for at least 3 of the 4 stale ones (allowing the 1 % FP rate); `Count() == 12` |
| `TestRebuildBloom_ExcludesForeignSession` | 3 own-session, 2 foreign session-scoped, 1 foreign project-scoped | `Count() == 8` (4 visible records × 2 keys) |
| `TestRebuildBloom_NeverFromCheckpoint` | source grep over non-test `.go` files under `internal/` (the definition inside `internal/sketch` and any `*_test.go` call are excluded) | exactly one call to `sketch.RebuildBloom`, in `negknow/bloom.go`; that file contains no reference to `checkpoint`; the whole of `internal/negknow` contains no import of `internal/checkpoint` |
| `TestRebuildBloom_HonoursConfiguredCapacity` | 3 000 active records (6 000 keys) against a configured capacity of 10 000 | capacity stays 10 000 — the configured value is not silently multiplied; `FillRatio() <= 0.5`; no resize retry |
| `TestRebuildBloom_Resizes` | 8 000 active records (16 000 keys) against a configured capacity of 10 000 | capacity grows to ≥ 16 384 (`roundUpPow2(16 000)`); at most one resize retry is performed; the final `FillRatio() <= 0.5` and `EstimatedFPRate() < 0.02` |
| `TestRebuildBloom_Persistence` | pre-existing `tried.bloom`, no backups yet | after rebuild: `tried.bloom` is the new file; exactly one backup exists and it is named `tried.bloom.1.bak` — the **incoming** seq, since `Open` seeded `l.seq = 0` from an empty `paths.HighestBloomBackupSeq` and the rebuild passed `1`; `.qompack/tmp` has no leftovers; **no `state/` file is created** |
| `TestRebuildBloom_SeqResumesFromDisk` | a project whose `sketches/` already holds `tried.bloom.7.bak`, then `Open` + one rebuild | `sketch.ReplaceGenerational` is called with `seq == 8`, its pre-flight passes, and the surviving backup afterwards is `tried.bloom.8.bak` — the counter resumed from the disk, not from a private state file |
| `TestRebuildBloom_OneBakGeneration` | three consecutive rebuilds | exactly one `.bak` file after each, its seq strictly increasing |
| `TestRebuildBloom_UsesSanctionedDoor` | source grep over non-test `.go` files in `internal/negknow` | no `os.Rename`, no `paths.ReplaceBloom` call, and no `paths.WriteAtomic`/`paths.CreateNew` call naming `tried.bloom`: `sketch.ReplaceGenerational` is the only writer |
| `TestBloomLoadFailure_RebuildsFromRecords` | corrupt `tried.bloom` (flipped CRC byte) + a valid records file, `Deps.Log` a capturing logger | `Open` succeeds; `Query` on a recorded elimination returns `AnswerActive`; exactly one Loud line, written by `sketch.LoadWithLog` and reading "sketch corrupt — rebuilding from records"; `negknow.bloom.corrupt_on_load == 1`; `negknow.bloom.rebuilds == 1` |
| `TestBloomLoad_ColdStartIsQuiet` | no `tried.bloom` at all, valid records | `Open` succeeds and rebuilds; **no** Loud line; `negknow.bloom.corrupt_on_load == 0` — a first session must not shout, or an operator learns to ignore the channel corruption uses |
| `TestBloomLoadFailure_NoRecords_NeverFalsePositive` | corrupt bloom, unreadable records | every `Query` returns `AnswerAbsent`, `BloomOnly==false` (property test over 1 000 rapid-generated targets) |
| `TestBloomUndercount_TriggersRebuild` | valid records, bloom built from only half of them | `Open` rebuilds immediately; `Query` correct for all |
| `TestBloomOvercount_SchedulesRebuild` | bloom with 20 extra junk keys | no immediate rebuild under `nextIdle`; `NeedsRebuild()==true`; queries correct |
| `TestBloomFileSize` | 3 000 active records (6 000 keys) at the Appendix C defaults capacity 10 000 / fp 0.01 — a load that leaves headroom, so no resize fires and the filter is the one Appendix A sizes | on-disk `tried.bloom` between 11 264 and 14 336 bytes, i.e. Appendix A's `m ≈ 95_850 bits ≈ 12 KB` plus the `sketch.Header` and CRC |
| `TestHealth` | 10 records, 4 stale, 1 foreign | `Records==10`, `Active==5`, `Stale==4`, `FillRatio` in `(0,0.5]`, `EstFPRate < 0.02`, `NeedsResize==false` |

### `ingest_test.go`

| Test | Setup / input | Expected |
|---|---|---|
| `TestIngestMCP_Full` | `MCPArgs{Target:"src/auth.ts:refreshToken", Approach:"widen pool timeout", Reason:"pgbouncer 1.18 ignores it in transaction mode", Scope:"project", DependsOn:["docker-compose.yml","package-lock.json"]}` with both files in the fake store's history | record with `Source==SourceMCP`, `Scope=="project"`, 2 deps sorted by path, evidence = the `PutBytes` root of the reason text; no warnings |
| `TestIngestMCP_AutoDeps` | `DependsOn` empty; store has `src/auth.ts`, `package-lock.json`, `docker-compose.yml` | 3 deps: target path first, then `package-lock.json`, then `docker-compose.yml` — the `autoDepCandidates` order |
| `TestIngestMCP_UnknownDepSkipped` | `DependsOn:["missing.yml"]` | dep list empty, one warning `"no stored version for missing.yml; not used as a staleness dependency"` |
| `TestIngestMCP_BadScope` | `Scope:"global"` | falls back to the configured default, one warning |
| `TestIngestMCP_NoStore_RequireEvidence` | `Deps.Store == nil`, `requireEvidence:true` | `ErrNoEvidence` |
| `TestIngestPin_EvidenceText` | `Evidence:"sha256:a3f2…"` (full 64 hex) | parsed into `core.Hash`; `Source==SourceSlashCommand` |
| `TestIngestPin_BadEvidenceText` | `Evidence:"garbage"` | warning, then minted from the reason text; record still created |
| `TestIngestUserStatement_Matches` | prompt `"That didn't work — the pool is still saturated."` with `Path:"src/db.ts"`, `Approach:"widen pool timeout"` | 1 record, `Source==SourceUserStatement`, `Reason` prefixed `"user stated: "` |
| `TestIngestUserStatement_ApostropheVariants` | `"that didnt work"`, `"that didn't work"`, `"that didn’t work"` | all three match |
| `TestIngestUserStatement_NoTarget` | same prompt, `Path==""` and `Symbol==""` | 0 records, counter `negknow.user_statement.unresolved == 1` |
| `TestIngestUserStatement_NoMatch` | `"looks good, ship it"` | 0 records, no counter movement |
| `TestObserve_AppendsSignals` | 3 observations | `records/signals.jsonl` has 3 lines; the in-memory ring returns them from `Since(0)` |
| `TestObserve_RingBounded` | 600 observations, `signalRing==512` | `Since(0)` returns 512, the most recent |

### `detector_test.go`

Each test builds a real `dag.Graph` via `dag.Open` on a `testutil.Project` and adds nodes/edges explicitly.

| Test | Setup / input | Expected |
|---|---|---|
| `TestDetector_PatternP` | observations: Edit(`src/db.ts`,"widen pool timeout",t=4) · TestFail(`src/db.ts`,Root=h,t=5) · Revert(`src/db.ts`,t=6) · Edit(`src/db.ts`,"disable connection pooling",t=7) | 1 record: target `src/db.ts`, approach `"widen pool timeout"`, reason `"test failed at turn 5 and the change was reverted at turn 6; a different approach (disable-pool) was taken at turn 7"`, `Source==SourceHeuristic`, `Evidence==h` |
| `TestDetector_SameClassNoEmit` | e3 detail `"raise the pool timeout"` (same class as e0) | 0 records |
| `TestDetector_NoRevertNoEmit` | revert observation omitted | 0 records |
| `TestDetector_TestPassBreaksPattern` | TestPass on the path between t4 and t5 | 0 records |
| `TestDetector_WindowExceeded` | e3 at t=20 with `detectWindowTurns==12` | 0 records |
| `TestDetector_OneEmissionPerEdit` | two revert/re-edit cycles after one failing edit | 1 record |
| `TestDetector_DepsFromDAG` | tool-use node for the failing test with `EdgeConsumes` to two `KindFile` nodes carrying roots | `DependsOn` has both, sorted by path, deduplicated |
| `TestDetector_DepsFallback` | `g == nil` | falls back to `resolveDeps`; `DependsOn` from the store |
| `TestDetector_NoEvidenceDropped` | TestFail with a zero `Root`, `requireEvidence:true` | 0 records, `negknow.detector.dropped_no_evidence == 1` |
| `TestDetector_Since` | observations at turns 1–20, `since=10` | only candidates whose `e0.Turn >= 10` are considered |
| `TestDetector_DoesNotAppend` | after `Scan` | `Health().Records == 0`; a subsequent `Record` call on the returned record makes it 1 |
| `TestDetector_TerminatesOnCyclicGraph` | a real `dag.Graph` carrying the legitimate cycle SP-07's INHERIT names — `tooluse:t1 → toolresult:t1 → assistant:2 → tooluse:t2 → file:a → tooluse:t1`, every node built with `dag.ToolUseNode`/`dag.ToolResultNode`/`dag.AssistantNode`/`dag.FileNode` and the `file:a → tooluse:t1` hop carrying `dag.EdgeSharedFile` — plus a full Pattern-P observation set whose `f1.ToolUse` is `t1` | `Scan` **returns** (the test fails by `t.Deadline`/timeout if it does not), emits the 1 expected record, and its `DependsOn` contains `file:a`'s path exactly once — the one-hop walk sees the cycle's edges as ordinary neighbours and never follows them back |

### `negknowtest/suite.go` and cross-package tests

| Test | Expected |
|---|---|
| `TestLedgerConformance` (in `negknow_test.go`, calling `negknowtest.RunLedgerSuite`) | every behaviour assertion passes; **no `t.Skip` remains anywhere in `negknowtest`** — a `go vet`-adjacent grep test in `verify` asserts this (Rule W-1 merge blocker) |
| `TestNodeIDGolden` | `NodeIDFor`/`FileNodeID` match `node-ids.json` |

### `test/e2e/negknow_test.go`

`TestE2E_EliminationLifecycle`: build a `testutil.Project` with `src/auth.ts`, `docker-compose.yml`, `package-lock.json`; open a real `store.Store`, a real `dag.Graph`, and a real ledger. Then:

1. `IngestMCP` an elimination on `src/auth.ts:refreshToken` / `"widen pool timeout"` with deps on both config files → `Query` returns `AnswerActive`.
2. Close and reopen the ledger from disk → still `AnswerActive` (durability).
3. Rewrite `docker-compose.yml`, re-ingest it into the store so a new file version exists, run `RefreshStaleness` → 1 ID flipped; `Query` returns `AnswerStale` with `StaleNote`.
4. `RebuildBloom` → `tried.bloom` shrinks; `Query` still returns `AnswerStale`; `Health().Active == 0`, `Stale == 1`.
5. `p.AssertAppendOnly(t)` passes; exactly one `tried.bloom.*.bak` exists; no file was written outside `.qompack/`.

`TestE2E_BloomCorruptionRecovery`: flip one byte of `tried.bloom`, reopen, assert the ledger rebuilds and answers correctly, and that `LOUD.log` contains exactly one negknow line.

### `test/replay/phase2_negknow_test.go` — the Phase 2 exit criterion

Consumes `eval.Harness` (SP-02, already on `develop`) and touches none of SP-02's files. `test/replay` is a test composition root and may import any package, matching `test/e2e`.

`TestPhase2ExitCriterion`:

0. Construct the harness exactly the way SP-02's existing `test/replay` driver on `develop` constructs it — the same package-level constructor in `internal/eval`, with the same argument set, copied from that driver. This file adds no constructor, no method and no field to `internal/eval` (Rule W-3); if the constructor's name or arity has moved by integration time, the call site **here** is adapted, never `internal/eval`.
1. `sessions, err := h.Load("testdata/sessions/synthetic")`; `require.NoError(err)` — 24 sessions; `require.GreaterOrEqual(len(sessions), 20)` (Appendix C `eval.minSessions: 20`).
2. For each session, run two policies over the logged action sequence in `Deterministic` mode:
   - `stockPolicy` — SP-02's baseline; no ledger.
   - `negknowPolicy` — a policy defined in this test file that opens a real ledger against a `t.TempDir()` project, ingests every elimination event the session carries, and consults `Query` before each subsequent approach attempt.
3. **Metric 1 — repeated-elimination events.** For every attempt in a run, canonicalize `(target, approach)` and count it as *repeated* when the ledger already holds a visible **active** record with that `MatchKey` and the attempt happened anyway. Assertions:
   - `require.Greater(stockRepeats, 0)` — guards against a vacuous pass on a corpus with no eliminations.
   - `require.LessOrEqual(negknowRepeats, int(0.75*float64(stockRepeats)))` — at least a 25 % reduction, the operationalization of "measurable reduction in repeated-elimination events in replay."
   - `require.Less(negknowRepeats, stockRepeats)` on at least 8 individual sessions.
4. **Metric 2 — stale-block incidents.** Restricted to sessions whose `SynthSpec.DependencyChangeAt` is non-empty. A *stale-block incident* is any `Query` that returned `AnswerActive` at a turn at or after the turn where one of that record's `depends_on` entries changed. Assertion: `require.Zero(staleBlocks)` across every dependency-change session, and `require.Greater(dependencyChangeSessions, 0)`. **What that zero claims, and what it does not** — the qualification inherited constraint 2 (SP05-D1) argues in full, restated here because this is the line that carries it: `require.Zero(staleBlocks)` is a statement about *the ledger given the events it received*, not about the world. SP-05's drain is not lossless on abort; a lost `PostToolUse` never manufactures a false flip but can produce a **missed** one, leaving a record `active` whose evidence did in fact change. The replay corpus is deterministic and drops nothing, so this assertion is structurally incapable of seeing that case and must not be read as excluding it. Write that in the test file as a comment directly above the assertion, in those terms — the exit criterion should *say* its scope rather than imply it.
5. Write the report to `filepath.Join(t.TempDir(), "phase2-negknow.json")` with `{stock_repeats, negknow_repeats, reduction_pct, stale_blocks, sessions, dependency_change_sessions}` and `t.Log` it so the `replay-gate` job surfaces the numbers.

### Benchmarks

`bench_test.go` implements every benchmark named in the performance-budget table. Each has a companion `TestBudget_<Name>` that runs the benchmark for a fixed duration via `testing.Benchmark` and `require`s the budget, so the budgets are enforced by `go test` and not only by a human reading `benchstat` output. `BenchmarkRebuildBloom` and `BenchmarkOpen` populate their fixtures with `eval`-independent synthetic records generated from a fixed seed so numbers are comparable across commits.

---

## Commit plan

Work happens on `feat/sp09-negative-knowledge`, cut from `develop` **after** verification V2 is green and SP-01, SP-03, SP-06 and SP-07 are merged. Exactly 7 commits. Each commit compiles and passes `go run ./tools/devtool test` for the packages it touches. Conventional Commits, footer `Refs:` naming SP-09, the gap and the sections.

> **Do not add Co-Authored-By lines or any attribution trailers to any commit message.**

### Commit 1

```
feat(negknow): canonical descriptor and approach-class canonicalization

The four-field descriptor of §8.3 plus the reason-independent match key that
makes already_tried answerable without a reason argument. Approach classes are
derived through a closed stopword/synonym table so the same idea phrased two
ways collides deliberately; the record lookup stays authoritative.

Refs: SP-09, G6.1, §8.3, §6.2
```

- [ ] `git checkout develop && git pull && git checkout -b feat/sp09-negative-knowledge`
- [ ] Write `internal/negknow/classify_test.go` and `internal/negknow/descriptor_test.go` first and run `go test ./internal/negknow/`. Expected red: a compile failure (symbols absent) when working strictly file-by-file, or — if the Phase A declaration pass below has already landed the empty-bodied signatures — assertion failures on zero-value returns. Either is the red state; a *passing* run at this point means the tests are not actually exercising the new code and must be fixed before proceeding.
- [ ] Add `internal/negknow/doc.go`, `classify.go`, `descriptor.go`. Replace `Canonicalize`'s zero-value stub body **in `internal/negknow/types.go`**; do not re-declare `Descriptor`, `Scope`, `Status`, `SourceKind` or `Dep`, and **do not touch `Descriptor.Key` or `internal/negknow/key_test.go`** — both are shipped and frozen.
- [ ] Add `testdata/golden/contracts/negknow/descriptors.golden.json` (24 rows). It is **not** produced by a `-update` flag: `key_hex` is the output of the already-frozen `Descriptor.Key`, so a regeneration switch is a switch for silently re-keying every bloom entry on disk. Compute the rows once, paste them in, and make row 1 `key_test.go`'s `fixedDescriptor` whose `key_hex` must read `7b2149a504b5227e6b00d912b48cf01aea3a609f1c3582e0d06bb36122b87d5d` — if it does not, the classifier or the descriptor literal is wrong, never the key.
- [ ] `go test -count=1 -run 'TestDescriptorKey_Stable' ./internal/negknow/` green, and `git diff --stat` shows `key_test.go` and `Descriptor.Key` untouched.
- [ ] `go run ./tools/devtool fmt lint test` green for `./internal/negknow/...`.

### Commit 2

```
feat(negknow): record schema and the append-only elimination log

records/eliminations.jsonl is the source of truth (§8.3 item 1). A record line is
a bare Record, matching the frozen contract fixture; status changes are appended
as op:"stale" control lines rather than rewrites, so the §7.4 append-only
invariant holds for the one file the whole feature rests on.

Refs: SP-09, G6.1, §7.4, §8.3, §8.5
```

- [ ] Write `record_test.go` and `log_test.go` first; run and watch them fail.
- [ ] Add `log.go`; extend the shipped `record.go` with `SourceKind.String`, `ParseSourceKind`, `normalizeRecord`, `recordID` and the codec. The `Record` struct and its json tags are already on `develop` and are not re-declared.
- [ ] Add `testdata/golden/contracts/negknow/eliminations.golden.jsonl` and `corrupt.jsonl`. **No `record.golden.json`**: the frozen `want/elimination_record.jsonl` is the byte authority for a record line, and it is not copied, regenerated or superseded.
- [ ] `go test -run 'TestRecord|TestReplayLog|TestAppendOnly' ./internal/negknow/` green.

### Commit 3

```
feat(negknow): ledger with the three-way already_tried response

absent / active / stale, with the §8.3 item 4 re-verification note exported as a
single constant so SP-13 and SP-11 cannot paraphrase it. Every membership answer
is backed by a record lookup or flagged BloomOnly (§13 invariant 3), and blind
mode answers absent for everything rather than risking a false positive.

Refs: SP-09, G6.1, G6.2, §8.3, §8.7, §12
```

- [ ] Write `ledger_test.go` first (all 28 cases in the `ledger_test.go` table); run and watch them fail.
- [ ] Fill in `ledger.go`; give `Open` a real body and delete the SP-01 `stubLedger` whose methods carry the `ErrNotImplemented` returns. `Open` itself already succeeds today (it returns `stubLedger{}, nil`) and must keep doing so — its signature and its "constructing always succeeds" contract are unchanged.
- [ ] `go test -race ./internal/negknow/` green; `TestConcurrentRecordQuery` passes under `-race`.

### Commit 4

```
feat(negknow): evidence-linked staleness flip driven by store.ChangedSince

Every elimination carries depends_on hashes; one ChangedSince call per refresh
flips affected records to stale with a per-dependency reason. rebuildOnStale is
honoured for all three Appendix C values, and nextIdle is provably safe because
a stale-inclusive bloom still resolves to a stale record.

Refs: SP-09, G6.1, §8.3, §12 (stale negative knowledge), Appendix C eliminations
```

- [ ] Write `staleness_test.go` first (14 cases) with a counting fake `store.Store`; run and watch them fail.
- [ ] Add `staleness.go`; wire the bounded refresh into `Open`.
- [ ] `go test -run 'Stale|Refresh|Maintenance|OpenRefresh' ./internal/negknow/` green.

### Commit 5

```
feat(negknow): rebuild tried.bloom from active records only

The bloom is a cache over the record log and nothing else: the rebuild's key
iterator reads visibleActive() and there is no other sketch.RebuildBloom call
site in internal/. Persistence goes through sketch.ReplaceGenerational, the one
sanctioned §3.3 door, with the rebuild counter resumed from the surviving backup
sequence; a corrupt or undersized bloom rebuilds rather than lying.

Refs: SP-09, G6.2, §6.2, §8.3, §12 (bloom saturation), Appendix A
```

- [ ] Write `bloom_test.go` first (16 cases including the single-call-site and sanctioned-door grep tests); run and watch them fail.
- [ ] Add `bloom.go`; extend `Open`'s bloom consistency check.
- [ ] `go test ./internal/negknow/` green; `TestBloomFileSize` confirms 11–14 KB against Appendix A's ~12 KB.

### Commit 6

```
feat(negknow): four elimination sources and the DAG heuristic detector

record_eliminated, /qompack:pin --eliminated, the test-fail -> revert ->
different-approach scan, and explicit user statements — §8.3's four sources in
descending reliability. The DAG's contribution is depends_on discovery: which
files the failing test actually read is exactly what the reason rests on.

Refs: SP-09, G6.1, G6.2, G6.3, §8.3, §6.4
```

- [ ] Write `ingest_test.go` (13 cases) and `detector_test.go` (12 cases, including `TestDetector_TerminatesOnCyclicGraph`) first; run and watch them fail.
- [ ] Add `ingest.go`, `detector.go`, `dagedges.go`.
- [ ] Add `testdata/golden/contracts/negknow/node-ids.json`.
- [ ] `go test -race ./internal/negknow/...` green.

### Commit 7

```
test(negknow): conformance suite, e2e lifecycle, benchmarks, Phase 2 gate

Unskips every behaviour test in negknowtest (Rule W-1 merge blocker), adds the
full elimination lifecycle end to end against a real store and DAG, enforces
every latency and size budget as a test rather than a chart, and asserts the
Phase 2 exit criterion on the synthetic corpus.

Refs: SP-09, §10 Phase 2, §11.2, §11.3
```

- [ ] Modify `internal/negknow/negknowtest/suite.go`: remove every `t.Skip`, add the behaviour assertions.
- [ ] Add `internal/negknow/negknow_test.go` (suite driver), `internal/negknow/bench_test.go`, `test/e2e/negknow_test.go`, `test/replay/phase2_negknow_test.go`.
- [ ] Add `docs/adr/0009-negative-knowledge-bloom-as-cache.md` recording, one short section each: two bloom keys per record and why; a record line as a bare `Record` with control lines discriminated by the presence of an `op` key, and why the frozen fixture settles it; `stale` as an append-op rather than a rewrite; coarse approach classes as a deliberate recall/precision trade, and why the frozen fixture's `approach_class` is a format label rather than a `Canonicalize` assertion; why `nextIdle` rebuild cannot cause a wrong answer; why the rebuild honours the configured capacity instead of over-allocating, so `tried.bloom` stays at Appendix A's ~12 KB; why persistence goes through `sketch.ReplaceGenerational` and the rebuild counter is seeded from `paths.HighestBloomBackupSeq` rather than a private state file; why identity dedup is keyed on `(session, scope, Desc.Key())` rather than on the record ID; and why `blind` mode never rebuilds.
- [ ] `go run ./tools/devtool ci-local` fully green.
- [ ] `go run ./tools/devtool cover` — `negknow` at or above its **90 %** floor (00-ARCHITECTURE §6.4).
- [ ] `go run ./tools/devtool replay` green, including `TestPhase2ExitCriterion`.
- [ ] Push; confirm all nine `ci.yml` jobs are green on the branch: `verify`, `test`, `cover`, `crossbuild`, `bench-gate`, `replay-gate`, `plugin-validate`, `security`, `docs`. (`plugin-validate` must be untouched — this subplan generates no manifest and registers no MCP tool; a diff there means something outside scope was edited.)

---

## Subagent strategy

This subplan is **heavy**. Partition it across four parallel subagents after the main session has fixed the shared type surface, and keep every commit in the main session so the history stays sequential and each commit is verified as a whole.

**Phase A — main session only, no subagents (≈45 min).** Write the complete declarations, with real signatures and doc comments but empty bodies returning zero values, for: `descriptor.go`, `record.go`, `ledger.go` (the `ledger` struct and every method signature), `Deps`, `MCPArgs`, `PinArgs`, `UserStatement`, `Observation`, `ObservationSource`, `StaleNote`, `ErrNoEvidence`, `ErrBlind`. Run `go build ./internal/negknow/` until it compiles. **This file set is the contract the subagents code against and must not be edited by any of them.** Commit nothing yet.

**Phase B — four subagents in parallel.** Each receives: the Phase A files verbatim, the "Design context" and "Implementation spec" sections of this document, and the specific test table it owns. Each returns *file contents only* — no git operations, no edits outside its assigned files.

| Subagent | Owns (creates) | Must not touch | Returns |
|---|---|---|---|
| **S1 — canonicalization** | `classify.go`, `classify_test.go`, the body of `descriptor.go`, `descriptor_test.go`, `descriptors.golden.json` | anything else | The two implementation files plus tests, and the generated golden JSON as literal text |
| **S2 — persistence** | `log.go`, `log_test.go`, the `MarshalJSON`/`UnmarshalJSON`/`normalizeRecord`/`recordID`/`SourceKind.String`/`ParseSourceKind` additions to the shipped `record.go`, `record_test.go`, `eliminations.golden.jsonl`, `corrupt.jsonl` | `ledger.go`, `descriptor.go`, `types.go`, and `testdata/golden/contracts/negknow/want/**` (frozen) | Files plus the byte-exact golden fixtures, and the assertion that its marshaller reproduces `want/elimination_record.jsonl` unchanged |
| **S3 — staleness and bloom** | `staleness.go`, `staleness_test.go`, `bloom.go`, `bloom_test.go`, and a fake `store.Store` in `internal/negknow/fakestore_test.go` | `ledger.go` (it declares against the struct fields, calls no unwritten helper) | Files plus a list of every `ledger` struct field it read or wrote |
| **S4 — ingestion and detection** | `ingest.go`, `ingest_test.go`, `detector.go`, `detector_test.go`, `dagedges.go`, `node-ids.json` | `ledger.go`, `bloom.go` | Files plus the exact `autoDepCandidates` and user-phrase lists it compiled |

Give every subagent these three standing constraints: (1) the only mutable ledger state is behind `l.mu`, and any method that reads `recs`/`byID`/`byMatch` takes `RLock` while any that appends takes `Lock`; (2) no new imports beyond `negknow`'s allowed set (`core paths config logging obs sketch store dag` plus stdlib); (3) no `time.Sleep` and no wall-clock reads — take `core.Clock`.

**Phase C — main session integration (≈60 min).** Fill in `ledger.go`'s bodies against the four returned file sets; resolve any signature drift by editing the *subagent's* file, never by changing the Phase A contract. Run `go test -race ./internal/negknow/...`. Then land commits 1–6 in order, splitting the integrated tree along the file boundaries the commit plan names.

**TDD ordering is preserved through the split, not waived by it.** Because Phase B produces tests and implementation together, each commit is landed in two staged steps so the red→green transition is actually observed:

1. `git add` **only** that commit's `*_test.go` files and fixtures, `git stash push --keep-index` the rest, run `go test ./internal/negknow/...`, and confirm it is red — compile failure or assertion failure, either is acceptable, a green run is not.
2. `git stash pop`, `git add` that commit's implementation files, re-run the package tests, confirm green, and make one commit containing both.

The red output from step 1 is not committed anywhere; it is the check that the test genuinely constrains the implementation. Every `git commit` stays in the main session.

**Stays in the main session, never delegated:** `ledger.go` bodies (every subagent's work converges here, so a parallel edit would collide), `negknowtest/suite.go` (the merge-blocker unskip), `test/replay/phase2_negknow_test.go` (it encodes the phase exit criterion and must be read against §10 by the session that owns the plan), every `git commit`, and the final `ci-local` run.

**Optional Phase D — one subagent, after commit 6 lands.** Delegate `bench_test.go` and `test/e2e/negknow_test.go` in parallel with the main session writing the replay test; both are additive files with no shared symbols.

---

## Exit criteria

### Quoted verbatim from Qompack.md §10, Phase 2

> **Exit criterion:** measurable reduction in repeated-elimination events in replay, with zero stale-block incidents (a viable approach wrongly refused) on sessions that include a dependency change. The core is still ~50 lines; staleness roughly doubles it and is non-negotiable for correctness.

Operationalized by `TestPhase2ExitCriterion`:

- [ ] `stockRepeats > 0` and `negknowRepeats <= 0.75 * stockRepeats` across the 24-session synthetic corpus.
- [ ] `negknowRepeats < stockRepeats` on at least 8 individual sessions.
- [ ] `staleBlocks == 0` across every session whose `SynthSpec.DependencyChangeAt` is non-empty, with at least one such session present — read as inherited constraint 2 (SP05-D1) requires: a statement about the ledger **given the events it received**, not about the world. The deterministic replay corpus loses nothing, so this zero does not exclude the *missed* flip a dropped `PostToolUse` would cause in production, and the test file must carry that qualification as a comment above the assertion.

### Quoted verbatim from Qompack.md §11.4

> - **Bloom false positives compounding.** At 1% they are safe; at 10% the agent starts skipping viable approaches. Monitor fill ratio and resize.

- [ ] `Health()` reports `FillRatio` and `EstFPRate`; `TestRebuildBloom_Resizes` proves `FillRatio <= 0.5` and `EstFPRate < 0.02` at 8 000 active records.

### Quoted verbatim from 00-ARCHITECTURE §13 invariant 3

> **The bloom filter is a cache, never the source of truth (§8.3).** Every membership answer is backed by a record lookup or explicitly flagged `BloomOnly`.

- [ ] `TestQuery_BloomOnly` and `TestBloomLoadFailure_NoRecords_NeverFalsePositive` pass; no code path returns `AnswerActive` or `AnswerStale` without a materialized record.
- [ ] `TestRebuildBloom_NeverFromCheckpoint` proves `sketch.RebuildBloom` has exactly one call site in `internal/`, fed only by `visibleActive()`.

### Local criteria

- [ ] All tests green: `go test -race ./internal/negknow/... ./test/e2e/... ./test/replay/...`.
- [ ] `gofumpt -l internal/negknow` is empty; `golangci-lint run ./internal/negknow/...` clean; `go vet` clean; the in-repo `nomagic` pass reports nothing in this package.
- [ ] Import-graph check passes: `negknow` imports only `sketch`, `store`, `dag` and the foundation packages (00-ARCHITECTURE §3.2).
- [ ] Coverage for `internal/negknow` ≥ **90 %** (§6.4 group 1).
- [ ] No `t.Skip` remains in `internal/negknow/negknowtest` (Rule W-1 merge blocker).
- [ ] Every performance budget in the table is asserted by a passing `TestBudget_*`.
- [ ] `bench-gate` still green: B-A p99 < 15 ms and B-E p99 < 2 s are unaffected (this package is off the L0 hot path; the bench run confirms no regression).
- [ ] `security` job green: no `net/http`, `net/url`, `crypto/tls`, `os/exec` import from `negknow`; the write set is confined to `.qompack/records/` (written directly) plus `.qompack/sketches/` and `.qompack/tmp/` (written only *through* `sketch.ReplaceGenerational`). Nothing under `.qompack/state/` is created.
- [ ] CI green on `feat/sp09-negative-knowledge` for all nine jobs.
- [ ] `Qompack.md` is unmodified (`git diff develop -- Qompack.md` is empty).

---

## Done checklist

- [ ] Every constant, formula, schema and phrase quoted in **Design context** has a corresponding implementation: the four-field descriptor, the two-key bloom insertion, `scope: "session" | "project"`, `status: "active" | "stale"`, `depends_on` as `{path, hash}`, the four sources in descending reliability, the rebuild-from-active-records rule, the §3.3 `.bak` procedure (discharged by calling `sketch.ReplaceGenerational`, not by re-implementing it), the Appendix A ~12 KB / 1 % / k=7 sizing, and all four Appendix C `eliminations` keys.
- [ ] Nothing shipped and frozen was rewritten: `git diff develop..HEAD -- internal/negknow/key_test.go` is empty, `Descriptor.Key`'s body in `internal/negknow/types.go` is byte-identical to `develop`'s, and `git diff develop..HEAD -- testdata/golden/contracts/negknow/want/` is empty.
- [ ] The wave-1 INHERIT is discharged: `TestDetector_TerminatesOnCyclicGraph` passes, `internal/negknow` contains no recursive graph walk, and the termination argument is written down in `detector.go` beside the one-hop `In`/`Out` fan-out.
- [ ] Every `dag.NodeID` in `internal/negknow` comes from a `dag.*Node` constructor: `grep -rn 'dag\.NodeID("' internal/negknow` returns nothing.
- [ ] `StaleNote` is byte-identical to §8.3 item 4 including the em dash, and a test compares it against a literal rather than against itself.
- [ ] Placeholder scan: `grep -rniE "TBD|TODO|FIXME|XXX|implement appropriately|handle edge cases appropriately|panic\(\"unimplemented" internal/negknow test/e2e/negknow_test.go test/replay/phase2_negknow_test.go docs/adr/0009-negative-knowledge-bloom-as-cache.md` returns nothing, and `grep -rn "ErrNotImplemented" internal/negknow` returns nothing.
- [ ] Type consistency with **Interface contract**: `Ledger`, `Descriptor`, `Record`, `Answer`, `AnswerState`, `Health`, `Detector`, `Scope`, `Status`, `SourceKind`, `Dep = core.Dep`, `Open` and `Canonicalize` match 00-ARCHITECTURE §5.10 exactly; every addition is a new symbol on a type this subplan owns, and no method was added to another subplan's interface (Rule W-3).
- [ ] `store.ChangedSince` is called with `[]core.Dep` and never with a `negknow`-local type (§3.2 cycle rule).
- [ ] Commit count verified: exactly **7** commits on the branch (`git rev-list --count develop..HEAD` returns 7), each with a Conventional Commit subject ≤ 72 characters and a `Refs:` footer.
- [ ] No co-author or attribution trailers: `git log develop..HEAD --format=%B | grep -niE "co-authored-by|signed-off-by|generated with|🤖"` returns nothing.
- [ ] Out-of-scope discipline: `git diff --name-only develop..HEAD` touches only `internal/negknow/**`, `testdata/golden/contracts/negknow/**`, `test/e2e/negknow_test.go`, `test/replay/phase2_negknow_test.go`, and `docs/adr/0009-negative-knowledge-bloom-as-cache.md`. In particular, nothing under `internal/observer/`, `internal/daemon/`, `internal/mcp/`, `internal/checkpoint/`, `internal/rehydrate/`, `internal/sketch/`, `internal/store/`, `internal/dag/` or `internal/eval/` was modified.
- [ ] Self-review pass complete: re-read the **Implementation spec** against the shipped code and confirm every named function exists with the stated signature, every table row is implemented, and every error-handling row has a test.
