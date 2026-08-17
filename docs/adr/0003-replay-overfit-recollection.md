# ADR 0003 — Re-collecting the replay corpus, and the schedule the gate enforces

**Status:** accepted (SP-02, wave 1)
**Related:** [ADR 0002](0002-replay-methodology.md), `Qompack.md` §11.4

---

## The problem, stated by the design

`Qompack.md` §11.4, verbatim:

> **Overfitting to replay.** Logged sessions were produced by an agent operating under the
> *current* system. Behaviour changes when the system changes. Re-collect sessions periodically
> under the new policy.

This is a slow failure and an invisible one. Nothing breaks. The suite stays green. The numbers
keep improving. They are simply improving against a world that no longer exists, because the
sessions were recorded before the store, the observer, and the rehydrator changed what a session
looks like — and the policies are now being tuned to a fossil.

"Periodically" is the word that makes this dangerous. A good intention with no trigger is a good
intention that never fires.

---

## Decision

The corpus carries the phase it was last regenerated after, and the gate refuses to run more than
two phases past it.

`testdata/sessions/synthetic/CORPUS.json`:

```jsonc
{
  "generator": "eval.Synthesize/1",
  "regeneratedAfterPhase": 0,
  "sessions": [ /* one entry per file, with its sha256 */ ]
}
```

`test/replay` compares that number against `--phase`:

```
phase > regeneratedAfterPhase + 2   ⟹   FAIL "corpus stale: re-collect sessions under the
                                              current policy (§11.4)"
```

Two phases of drift is tolerated because a phase gate should not be blocked by bookkeeping from
the phase before it. Three is not, because by then the observer and the store have both changed
and the sessions describe a different system.

The failure message points here.

---

## The protocol

Run this when the gate fails, or when a phase lands that changes what a session looks like.

### 1. Bump the synthetic seeds

Seeds move by `+100` per regeneration, so a regenerated corpus is genuinely different sessions
rather than the same 24 with a new label. Edit `CorpusSpecs()` in `internal/eval/corpus.go`: the
1001/1002/1003 family becomes 1101/1102/1103, and so on for all eight shapes.

Keep the shapes themselves. They span the failure space §6.3 names — read-heavy, test-output-heavy,
refactor, long-idle, dependency-change, subagent-heavy, thrash-loop, multi-compaction — and that
span is the reason the corpus is worth anything. If a phase has introduced a *new* failure mode,
add a ninth shape rather than repurposing an existing one.

### 2. Regenerate and set the phase

```sh
go run ./tools/devtool replay --regen-corpus
```

Then set `regeneratedAfterPhase` in `CORPUS.json` to the phase that just landed. The generator
writes the field from its own constant, so update `WriteCorpus` if the value needs to change, and
let regeneration write it — do not hand-edit the manifest, because its per-file digests must match
the bytes beside it and a hand-edit is how those drift apart.

### 3. Re-import the recorded corpus

The synthetic corpus is what CI runs; the recorded corpus is what gates releases (§6.3 tier 2).
Both age, and the recorded one ages faster because it is a direct recording of the old system.

```sh
export QOMPACK_SESSIONS_DIR=~/qompack-sessions
rm -rf "$QOMPACK_SESSIONS_DIR"          # the old sessions describe the old system
qompack eval import --from ~/.claude/projects
```

Import redacts by default. `--no-redact` additionally requires `QOMPACK_EVAL_ALLOW_UNREDACTED=1`,
and the importer refuses any destination inside the repository working tree.

### 4. Re-write the baselines

```sh
go run ./test/replay --write-baseline --baseline testdata/baseline/phase<N>.json
go run ./test/replay --corpus "$QOMPACK_SESSIONS_DIR" \
    --write-baseline --baseline testdata/baseline/phase<N>-recorded.json
```

Both files record their own `corpusTier`, so the synthetic and recorded numbers can never be
confused for one another.

### 5. Commit the discontinuity honestly

A regenerated corpus is a **new measuring instrument**. The numbers before and after are not
comparable, and pretending otherwise is worse than not measuring at all.

- Commit the new corpus, the new baselines and the `regeneratedAfterPhase` bump **together**, in a
  commit whose message says the corpus was regenerated and why.
- Do **not** sign off the resulting metric movements as regressions. They are not regressions;
  they are a different measurement. Re-baselining is the correct response, and the sign-off
  trailer exists for deliberate trade-offs within one instrument, not for instrument changes.
- Keep the previous baseline file in git history. It is the record of what the old instrument
  said, and the only way to reconstruct why a decision was made under it.

---

## What this does not fix

Re-collection keeps the corpus current. It does not make the corpus *representative* — 24
synthetic sessions and a handful of recorded ones are a sample, and a sample that was chosen by
the same people who wrote the policies being measured.

Two honest limitations, recorded here so they are not rediscovered as surprises:

- **The synthetic generator encodes assumptions about agent behaviour.** Temporal locality in
  re-reads, one tool call per assistant turn, a re-read of pre-compaction content after every
  compaction — each of these is defensible and each is a modelling choice that a recency-based
  policy benefits from. The recorded tier exists precisely because those assumptions need
  checking against reality, which is why it gates releases even though it cannot gate pull
  requests.
- **The gate cannot detect a corpus that was always wrong**, only one that has gone stale. A shape
  that never occurs in real sessions will pass this check forever. Comparing the synthetic and
  recorded numbers at each release is what catches that, and a large divergence between the two is
  a finding about the corpus, not about the policy.
