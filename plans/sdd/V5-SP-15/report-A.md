# SP-15 role A — Sequitur core — report

**Status: DONE (implementation), BLOCKED on one Main ruling.**

The real incremental Sequitur is landed, green, and passes every invariant I can assert. It does
not pass `grammartest`'s `/behaviour` block, and **no implementation can**: the frozen fixture
asserts a property that is mathematically unsatisfiable at two of its own appends. Proof and a
three-line amendment are in §4. Everything else is finished and needs no ruling.

Branch `wip/sp15-a-sequitur`, worktree `qompack-sp15-a`, on `6eac57c`.

---

## 1. Files touched

| File | Status |
|---|---|
| `internal/grammar/sequitur.go` | rewritten — interface unchanged, `New` now returns the real induction, plus `Snapshot`/`Restore`/`MarshalBinary`/`UnmarshalBinary` |
| `internal/grammar/rules.go` | new — the linked-symbol representation, digram index, and the four structural operations |
| `internal/grammar/sequitur_test.go` | new — 15 tests / 67 subtests |
| `plans/sdd/V5-SP-15/report-A.md` | new — this file |

Nothing else was edited. `internal/grammar/types.go`, `doc.go`, `formatwarning.go`, `grammartest/**`
and `test/guards/**` were read but not touched — see §5 for the ones that now need Main.

### The B-side scaffold, and why its name differs from the brief

A's `MarshalBinary`/`UnmarshalBinary` are four-line calls through B's `EncodeSnapshot` and
`DecodeSnapshot`, which do not exist yet, so I wrote a local build-only stand-in to compile and run
against, and **deleted it before committing**. The brief named it
`internal/grammar/codec_stub_for_a_test.go`; I used `internal/grammar/codec_stub_for_a.go`
(no `_test` suffix) because a `_test.go` file is not part of the non-test package, so
`go build ./internal/grammar/` — one of the four required gates — would have failed with
`undefined: EncodeSnapshot` and the gate would have proved nothing. The stand-in wrote
`magic || version || JSON` and reported `core.ErrDegraded` for a bad magic, a future version or an
unreadable payload, i.e. contract §4's shape.

**The committed branch therefore does not compile on its own.** That is by design per the brief:
`sequitur.go` references two functions B is writing concurrently, and A+B compile together. No test
of mine calls `MarshalBinary`/`UnmarshalBinary`; the A-side of the seam is tested through
`Snapshot`/`Restore` directly, so a round-trip failure after the merge is attributable to one side.

---

## 2. Gates

Run in `C:/Users/Quant/Documents/Programming/Projects/qompack-sp15-a`, with the B-side scaffold
still present (without it nothing in the package compiles).

```
$ gofmt -l internal/grammar
(no output)

$ go build ./internal/grammar/
(no output, exit 0)

$ go vet ./internal/grammar/...
(no output, exit 0)

$ go test ./internal/grammar/... -count=1
ok  	github.com/qompack/qompack/internal/grammar	1.560s
--- FAIL: TestRunSequiturSuite_AgainstQompackStub (0.00s)
    --- FAIL: TestRunSequiturSuite_AgainstQompackStub/grammar.New-stub/behaviour (0.00s)
        --- FAIL: .../behaviour/invariants_hold_after_every_append (0.00s)
            suite.go:58:
                	Error:      	Should be true
                	Messages:   	after append #8 ("Bash"): digram ("\x00R2","\x00R2")
                	            	appears in both Compressed() and Compressed()
FAIL	github.com/qompack/qompack/internal/grammar/grammartest	1.014s
```

`internal/grammar` itself is green — 15 tests, 67 subtests:

```
--- PASS: TestSequitur_InvariantsHoldAfterEveryAppend (0.52s)   [14 streams]
--- PASS: TestSequitur_ConformanceStreamInducesRules
--- PASS: TestSequitur_Determinism                              [14 streams]
--- PASS: TestSequitur_Reset
--- PASS: TestSequitur_ThrashThreshold
--- PASS: TestSequitur_ThrashIgnoresUnrepeatedWork
--- PASS: TestSequitur_SmallGrammarsAreExact
--- PASS: TestSequitur_RetiresUnderUsedRules
--- PASS: TestSequitur_UtilitySweepIsANet
--- PASS: TestSequitur_SnapshotRestoreRoundTrip                 [14 streams]
--- PASS: TestSequitur_SnapshotIsACopy
--- PASS: TestSequitur_RestoreRefusesCorruptSnapshots           [9 corruptions]
--- PASS: TestSequitur_RestoreAcceptsAnEmptySnapshot
--- PASS: TestSequitur_RestoreCarriesAnUnderUsedRuleAndTheNextAppendSettlesIt
--- PASS: TestSequitur_NewReturnsTheRealImplementation
```

Blast radius beyond the package — every consumer of `grammar.Sequitur`, run unmodified against the
real induction:

```
$ go test ./internal/observer/... ./internal/checkpoint/... ./internal/daemon/... -count=1
ok  	github.com/qompack/qompack/internal/observer                  23.364s
ok  	github.com/qompack/qompack/internal/observer/observertest       2.851s
ok  	github.com/qompack/qompack/internal/checkpoint                67.443s
ok  	github.com/qompack/qompack/internal/checkpoint/checkpointtest   0.996s
ok  	github.com/qompack/qompack/internal/daemon                    50.311s
```

```
$ go test ./test/guards/... -count=1 -run Stub
--- FAIL: TestAllStubsReturnNotImplemented/grammar
    grammar.Restore must report core.ErrNotImplemented, got
    grammar: restore: NextID 0 would collide with rule 0: qompack: running in degraded mode
```

### The A/B seam, verified against B's actual codec

Role B had already committed `internal/grammar/codec.go` on `wip/sp15-b-codec-warnings` (e862515)
with signatures matching the contract exactly. Before committing, I extracted that file read-only
(`git show wip/sp15-b-codec-warnings:internal/grammar/codec.go`) in place of my scaffold, added a
temporary seam test, and ran it. **Both extracted files were deleted before the commit; nothing of
B's is on A's branch.**

```
--- PASS: TestSeam_MarshalUnmarshalRoundTrip     [14/14 streams]
    Rules(), Compressed() and Thrash(n) for n in 0..4 all equal after
    UnmarshalBinary(MarshalBinary(g)) — contract §4's round-trip identity,
    including the empty grammar and the nil-versus-empty slice distinction
--- PASS: TestSeam_UnmarshalRefusesJunk          [nil, empty, bad magic, truncated]
    each reports core.ErrDegraded and leaves the receiver byte-identical
```

So the seam is not merely assumed to fit: A+B compile and round-trip together today. The only thing
that fails on the merged pair is `grammartest`, for the reason in §4.

`go run ./tools/devtool lint` was **not** run: memory records that devtool walks sibling worktrees
of this repo, and A's worktree shares a repo with four other live SP-15 roles. It should be run by
Main on the integrated branch, not from inside a role's worktree.

---

## 3. What was implemented

Classical Sequitur, incremental, with the standard structures: a doubly linked symbol list per rule
with a guard sentinel, a digram index keyed on `(symbol identity, symbol identity)` where a rule
reference is identified by its **pointer** (no per-comparison string building, and no chance of a
terminal colliding with a rendered `RuleRef`), and the four operations `check` -> `match` ->
`substitute` -> `expand`.

Notable points:

- **Determinism.** Rule ids are minted monotonically; every projection (`Rules`, `Thrash`,
  `Compressed`, `Snapshot`) is built from the linked lists and from ids sorted ascending. The two
  maps are lookup-only. `TestSequitur_Determinism` asserts equal output for equal input across all
  14 streams, and Go re-randomizes map order every run, so each CI run is a fresh draw.
- **Losslessness.** `TestSequitur_InvariantsHoldAfterEveryAppend` asserts, after every single
  append, that `Compressed()` expanded through `Rules()` is *exactly* the stream appended so far.
  `grammartest` does not check this at all, and everything else rests on it.
- **Index integrity.** The same test asserts no index entry is stale: every entry names a node still
  in a body, filed under the digram that node actually begins. This is the failure mode an
  incremental Sequitur is most exposed to — a stale entry mints a rule over symbols that were never
  adjacent, several appends later, and `Rules()` reports it with a straight face.
- **Rule utility, belt and braces.** `match` performs the repair the original algorithm performs, at
  the one site the original checks (the digram's first symbol). The *second* symbol's rule can also
  lose a reference in the same step, and I could not convince myself the original's argument that it
  never falls to one is airtight, so `Append` ends with an explicit sweep that inlines any rule left
  at one reference, over ids sorted ascending. `TestSequitur_UtilitySweepIsANet` asserts the sweep
  **never fires** across the whole corpus — so it is a net, not machinery, and if that ever changes
  the test says so rather than a conformance failure saying it.
- **`Restore` refuses nine kinds of corruption** (dangling reference, id below the first induced id,
  non-ascending ids, duplicate ids, empty body, `NextID` collision, unreachable rule, self-reference,
  mutual reference) with `core.ErrDegraded`, building into a fresh value and swapping only on
  success so the receiver is byte-identical on failure — contract §4's compatibility behaviour.

---

## 4. THE BLOCKER — `grammartest`'s behaviour fixture is unsatisfiable

`checkNoDigramTwice` (`grammartest/behaviour.go:33-46`) counts **overlapping** occurrences of a
digram as two. Sequitur's digram-uniqueness invariant is about **non-overlapping** occurrences, and
the difference is not cosmetic.

### 4.1 What fails, and where

Against `thrashStream` = `(Read Edit Bash)x5, user, (Grep Grep)x4`:

- **append #8** (the third `Bash`): the top level is `[R2,R2,R2]` with `R2 -> Read Edit Bash`. The
  pair `(R2,R2)` sits at offsets 0-1 and 1-2, *sharing the middle symbol*.
- **append #18** (the third `Grep`): the top level ends `... user Grep Grep Grep`, same shape.

### 4.2 Why no algorithm can avoid it

The overlap exception is not a shortcut. In `x x x`, replacing one occurrence of `(x,x)` with a rule
leaves that rule referenced exactly once, which invariant 2 (`Uses > 1`) inlines straight back — the
two assertions are in direct contradiction unless overlaps are excluded.

For append #18 the impossibility is absolute, and it does not depend on Sequitur at all. A rule used
k >= 2 times has its expansion occurring k times as *disjoint* substrings (parse phrases never
overlap). For the prefix `REBREBREBREBREBuGGG`:

```
append #18 -> prefix 'REBREBREBREBREBuGGG'
   substrings of length>=2 with 2+ disjoint occurrences:
     BR BRE BREB BREBR BREBRE EB EBR EBRE EBREB EBREBR RE REB REBR REBRE REBREB
   ... of which contain 'G': NONE
```

So **every rule whose expansion contains a `Grep` must span exactly one symbol** — an alias. The
three `Grep`s therefore occupy three adjacent top-level slots, each either the terminal or a span-1
alias. Exhausting those (G = terminal, A/B = distinct span-1 alias rules, preceded by `user`):

```
legal: ('G', 'A', 'A') rule uses: {'A': 2}      legal: ('A', 'G', 'A') rule uses: {'A': 2}
legal: ('G', 'B', 'B') rule uses: {'B': 2}      legal: ('A', 'A', 'G') rule uses: {'A': 2}
legal: ('B', 'G', 'B') rule uses: {'B': 2}      legal: ('B', 'B', 'G') rule uses: {'B': 2}

legal assignments with NO alias rule at all: NONE
```

Every assignment satisfying both literal invariants requires a **span-1 alias rule used twice**.
Sequitur only ever mints a rule from a digram, so every Sequitur rule spans >= 2. **No Sequitur can
pass append #18 of this fixture**, and a non-Sequitur that could would be one that emits alias rules
solely to satisfy a grader — `Thrash` would filter them out again (`len(Expansion) >= 2`), so they
would carry no meaning at all.

Append #8 is the weaker case: a grammar does exist there
(`[Read, C, C, F]`, `C -> F Read`, `F -> Edit Bash`), but only via a global re-parse, and #18 is
unreachable regardless.

### 4.3 The amendment I recommend (Main's call, Main's file)

Three lines in `grammartest/behaviour.go`, recording *where* a digram was seen and skipping an
occurrence that overlaps its predecessor:

```go
type where struct{ label string; seq, pos int }

func checkNoDigramTwice(labeled []labeledSymbols) (ok bool, detail string) {
	seen := make(map[digram]where, 16)
	for si, ls := range labeled {
		for i := 0; i+1 < len(ls.symbols); i++ {
			d := digram{ls.symbols[i], ls.symbols[i+1]}
			if first, dup := seen[d]; dup {
				// Sequitur's digram uniqueness is over NON-OVERLAPPING occurrences: two
				// occurrences of (x,x) inside a run share their middle symbol, and no rule can
				// replace both without leaving a rule used once, which rule utility inlines back.
				if first.seq == si && first.pos+1 == i {
					continue
				}
				return false, fmt.Sprintf("digram (%q,%q) appears in both %s and %s",
					d.a, d.b, first.label, ls.label)
			}
			seen[d] = where{label: ls.label, seq: si, pos: i}
		}
	}
	return true, ""
}
```

This weakens nothing else: it is exactly the classical statement, and every non-overlapping repeat
is still a failure.

**Verified already.** `checkNoRepeatedDigram` in `sequitur_test.go` *is* the amended checker, and
`TestSequitur_InvariantsHoldAfterEveryAppend/conformance_stream` runs it — plus `Uses > 1`, plus
index integrity, plus losslessness — after every one of the fixture's 23 appends, and passes. So the
amendment is sufficient, not merely plausible.

This is a Main decision under contract §9 ("no suite edit is permitted to make a block pass"). I read
§9 as forbidding a role from weakening a grader to dodge a real failure, which is not this: the
assertion is false as stated, for every possible implementation, and I have not touched the file.

---

## 5. Handoffs (Main-owned files A must not edit)

1. **`test/guards/stubs_test.go:111`** — the `grammar` entry still asserts stub behaviour and now
   fails. It needs `pureMethods: allMethodsAreReal` plus the "seam is REAL as of SP-15" comment, in
   the same shape the `dag`, `negknow` and `checkpoint` entries already carry. Keep the registration
   for the completeness check.
2. **`internal/grammar/doc.go`** — still says "Every Sequitur operation is a stub ... until SP-15
   lands the real grammar induction". Prose only; it is now false.
3. **`internal/grammar/grammartest/behaviour.go`** — §4 above.

---

## 6. Decisions I made (each is reversible; flag any you want changed)

1. **Rule id 0 is reserved for the top-level sequence**; induced ids start at 1. Nothing in a grammar
   may reference the top level, so a `RuleRef(0)` in a snapshot is a dangling reference and is
   refused. Consequence for B: encoded rule ids are always >= 1.
2. **`Restore` recomputes `Uses`, `Expansion` and `Span`** from `Body` and `Sequence` rather than
   reading them. They are derived, and recomputing makes round-trip identity independent of whether
   B's codec encodes the derived fields at all — the one degree of freedom the seam leaves open. B
   can encode them or not; both round-trip.
3. **`Restore(Snapshot{})` is accepted** as the empty grammar. A grammar with no rules has nothing
   for `NextID` to collide with, so a decoder that did not write the field is not corrupt, and
   refusing it would make the empty grammar the one value the seam could fail to round-trip. With
   rules present, `NextID` must exceed the largest of them.
4. **`Restore` does not re-impose rule utility.** A peer's rule used once is odd evidence, not
   corrupt evidence, and rewriting another process's record on read is worse than carrying it. The
   next `Append` settles it (the sweep runs there) — pinned by
   `TestSequitur_RestoreCarriesAnUnderUsedRuleAndTheNextAppendSettlesIt`.
5. **`reindex` records, does not resolve.** A restored snapshot that already contained a repeated
   digram gets one occurrence indexed and the other left alone, rather than being refused or
   silently rewritten.
6. **Tests live in `package grammar`, not `grammar_test`** (both conventions exist in this tree).
   Index integrity and the utility-sweep counter are not observable from outside, and asserting them
   is most of the value. `formatwarning_test.go` next door stays external and untouched.

---

## 7. Open questions for Main

1. **The §4 amendment — approve, or rule otherwise?** Until then
   `go test ./internal/grammar/...` is red on `grammartest`. This is the only thing blocking A.
2. **A terminal `Symbol` that looks like a rule reference.** `types.go` picks NUL as the sigil
   because no tool name can contain one, and `ParseRuleRef` degrades an unparseable remainder to
   not-a-reference. But a terminal that is *exactly* `"\x00R7"` would be read back as a reference by
   `Restore` and by any consumer expanding `Compressed()`. The live grammar is immune (identities
   are pointers, not strings); only the encoded form is ambiguous. Worth a `Symbol` validation
   somewhere, or an explicit "cannot happen, here is why" in `types.go`. Main owns that file.
3. **Should `Snapshot`/`Restore` be reachable through an exported interface?** The concrete type is
   unexported, so a consumer needs a local `interface{ Snapshot() grammar.Snapshot }` assertion.
   Contract §4 specifies methods, not an interface, so I added none — say the word if the daemon or
   checkpoint wiring would rather have a `grammar.Snapshotter`.
4. **`Thrash` reads `>` minUses** ("exceeds", per §5.11), so `Thrash(2)` reports a rule used three
   times. `observer/prompt.go` passes `thrashMinUses`; worth a glance that its value still means
   what it did against a stub that returned nil.
