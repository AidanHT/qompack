# SP-04 — carried defects

Six things SP-04 knowingly shipped. Each has a row in `plans/CARRIED-DEFECTS.tsv`, and
`test/guards/carrieddefects_test.go` will not let `V2-VERIFY` write `plans/V2-report.md` while any
of them is still `open`.

**None of these is a reason to hold the merge.** Every one is either bounded (a dedup miss, never
lost content — `Restore` is byte-exact through all of them) or a process trap that announces itself.
They are here because the alternative to writing them down was hoping someone re-derived them.

Fixing a row means setting its status to `fixed`; deciding not to means setting it to
`deferred:<checkpoint>` and adding a paragraph here saying why. Doing neither fails the gate.

---

## SP04-D1 — JSON-escaped Windows temp paths are not canonicalized

**Symptom.** `tmpPathRules` strips `C:\Users\<name>\AppData\Local\Temp\...` to `<tmp>`. It does not
strip `C:\\Users\\<name>\\AppData\\Local\\Temp\\...`, which is what the same path looks like once a
tool has embedded it in a JSON payload. The rule's user-name segment is `[^\\]+`, which cannot cross
a doubled backslash, so the match never starts.

```
{"cwd":"C:\\Users\\alice\\AppData\\Local\\Temp\\build\\x"}   ->   unchanged
C:\Users\alice\AppData\Local\Temp\build\x                     ->   <tmp>
```

**Why it matters twice.** A volatile temp path that survives canonicalization forks the dedup space
exactly the way an unescaped one would — this is an O2 gap, not only a cosmetic one. And because the
path survives into stored content, so does the user name in it: this defect is why
`testdata/corpora/toolout/**` had to be sanitized before commit, and it is the reason a raw capture
from a Windows host cannot be trusted to be anonymous just because `tmpPaths` is enabled.

**Evidence.** `TestCarriedDefect_SP04D1_EscapedTempPathIsNotStripped`. The docker-build fixture keeps
its escaped form deliberately, so the gap stays exercised rather than being papered over by the
sanitization.

**Acceptance.** The escaped spelling canonicalizes to the same `<tmp>` as the plain one; the corpus
goldens are regenerated; `TestPrefilterAgreesWithFullScan`, `FuzzPrefilterAgreesWithFullScan` and
the corpus structural tests stay green. Note the `need` prefilter must be widened with the rule —
``\appdata\`` is matched case-insensitively and would still fire, but a new rule needs its own
necessary literal or it will scan every buffer.

**Watch out for.** Widening `[^\\]+` to admit `\\` risks the match running past the end of the path
in text that contains several escaped paths on one line. Prefer a second rule over a looser first
one, and add a row to `TestTmpPaths_Table` for the multiple-paths-per-line case.

---

## SP04-D2 — canonicalization is not idempotent when a deletion joins two fragments

**Symptom.** `00-ARCHITECTURE.md` §5.6 requires `Canonicalize(Canonicalize(x)) == Canonicalize(x)`.
That holds by construction for every rewrite that SUBSTITUTES — tokens are bracketed in `<`/`>`,
which no rule admits, and the word-boundary invariant in `generic.go` stops a substitution creating
a boundary. It does not hold for a rewrite that DELETES, because deleting concatenates the deleted
span's neighbours and the joined text can match a rule neither neighbour did:

```
"000\x1b0s"  ->  "0000s"  ->  "<d>"
```

**Blast radius is small and worth stating precisely.** `Restore` is byte-exact at every step, so the
cost is a store lookup that misses, never content that cannot be recovered. It does not arise on real
tool output: `TestCorpus_StructuralProperties` asserts unconditional idempotence across all 26
captured files and passes, because colourizers wrap whole tokens rather than splitting values.

**Why SP-04 did not fix it.** The complete fix is to run composition to a fixed point and rebase each
later pass's `Delta` into ORIGINAL coordinates, since a second-pass match's preimage spans the bytes
a first-pass deletion removed. `Delta.Offset` and `Delta.Len` are in canonical coordinates and SP-06
stores against them, so that is a change to a contract SP-04 does not own. It needs a decision, not a
patch.

**A cheaper option worth evaluating first.** Strip ANSI as a pre-pass for MATCHING only: collect the
ansi matches, build a scratch buffer with them removed plus an offset map, run the other matchers
against the scratch, and map their spans back to original coordinates before composing. Composition
still happens once, over the original, so the `Delta` contract is untouched. That closes the ANSI
case — which is every case anyone has actually hit — and it would also let `wordEdge` drop its
escape branch, which is what forces `numeric.go` to walk `(?:escAny)+` as a memoized DAG. Roughly
half of that file exists to reproduce a construct this would delete.

**Evidence.** `TestKnownDeletionMediatedLimit` in `internal/canon/golden_test.go`, next to the
`FuzzCanonicalize` assertion that carves out the exception. Read them together: the fuzz target
hard-fails any non-idempotence a deletion cannot explain, so the carve-out is narrow and checked.

**Acceptance.** Either the exception is removed from `FuzzCanonicalize` and the fixed point is
reached in one pass, or the row is re-deferred with the decision recorded — naming which of the two
options above was chosen and why.

---

## SP04-D3 — three timestamp and duration edges are wrong in the patterns

All three are cases where `internal/canon/numeric.go` is faithful to the regexes it replaced and the
regexes are wrong about the world. They cannot be fixed in the scanner alone: the reference patterns
compiled in `numeric_test.go` are the oracle for the agreement tests that make the scanner
trustworthy, so pattern and scanner move together, pattern first.

| | input | today | should be |
|---|---|---|---|
| (a) | `"in 5\nms"` | `"in <d>"` | unchanged — a duration may not cross a newline |
| (b) | `"2024-01-15T10:32:07Zx"` | unchanged | `"<ts>x"`, or at least the inner clock stripped |
| (c) | `"2024-01-02T03:04:05.1234567890"` | `"<ts>.<ts>"` | a single `<ts>` |

(a) is `\s?` admitting `\n`; Go's `\s` also excludes the vertical tab, so `"5\fms"` is a duration and
`"5\vms"` is not — inherited from the class, not chosen. (b) is `T` being a word byte, so the
bare-clock rule's leading boundary cannot fire inside an RFC-3339 string, and when the ISO rule fails
its own trailing boundary there is no fallback. (c) is the ISO rule capping the fraction at nine
digits and the 10-to-13-digit epoch rule then claiming the digits left outside the match.

**Evidence.** `TestCarriedDefect_SP04D3_TimestampAndDurationEdges`.

**Acceptance.** Patterns corrected, `numeric.go` updated to match, the agreement tests
(`TestNumericMatchesAgreeWithReference`, the rapid property, `FuzzNumericMatchesAgreeWithReference`)
green, goldens regenerated.

---

## SP04-D4 — `landedSubplans` must gain SP-02 and SP-03 at the wave-1 merge

**Symptom.** `tools/devtool/cover.go` decides whether a package's §6.4 coverage floor applies from an
explicit `landedSubplans` set. SP-04 added itself. SP-02 (`eval`) and SP-03 (`sketch`) land in the
same wave-1 merge and are not in it, so on the merged `develop` their floors are exempt and the job
log still calls them stubs.

**Why the set is explicit rather than derived.** Deriving it from `OWNERS.tsv`'s probe column does
not work: `scheduler.Evaluate` returns a `Decision` and no error, and `grammar.Append` has an empty
body, so neither reads as a stub and a probe-only rule turns SP-12's and SP-15's floors on years
early. `probeBlind` names the four packages whose probe carries no signal.

**This one announces itself.** `cover` now fails when a package is exempt but its probe no longer
looks like a stub, naming the package, the owner and both remedies. `sketch.MarshalBinary` is a bare
`core.ErrNotImplemented` today, so the gate fires the moment SP-03 lands. It is recorded here anyway
because the failure arrives during a merge, when the temptation to reach for the quickest silencing
edit is highest, and the quickest edit — adding the package to `probeBlind` — is the wrong one.

**Acceptance.** `landedSubplans` contains every subplan merged into `develop` at V2; `devtool cover`
reports real percentages for `eval` and `sketch` against their floors; no wave-1 package is described
as a stub in the job log.

---

## SP04-D5 — canonicalization cost is now dominated by prefilter scans

**Symptom.** Not a bug: a shape worth knowing before more rules are added. Canonicalizing 100 KB of
`bash/npm-install.txt` costs ~2.0 ms against a 3 ms budget, and roughly all of it is now the
prefilter itself — about 52 µs per literal per 100 KB, times ~41 rules. Regex scanning, which was
110 ms before SP-04's last day of work, is no longer the cost.

**What follows.** Every rule added to `internal/canon` spends ~52 µs per 100 KB whether or not it can
match, so the budget erodes linearly in the number of rules rather than in the number of rules that
fire. There is roughly 1 ms of headroom, or about twenty more rules, before `BenchmarkRun_Bash100KB`
is at its limit — and SP-05, SP-06 and SP-08 all add work to the same PostToolUse call that this 3 ms
is one part of.

**The fix when it is needed.** One multi-literal pass (Aho–Corasick over the union of every rule's
`need` set) answers all ~60 literal questions in a single scan instead of 41, turning the prefilter
from O(rules × bytes) into O(bytes). It was not built here because the budget is met and the
machinery would have been speculative.

**Acceptance.** None required at V2. Re-measure at the checkpoint and record the number; open a
successor row if the headroom has gone.

---

## SP04-D6 — `BenchmarkRun_Bash100KB` is noisy enough to confuse the bench gate

**Symptom.** Ten benchstat samples on the reference host give `2.018 ms ± 33%`. The spread is host
noise, not variance in the code — the same run reports `± 4%` for `RootHash_1000Chunks` and `± 7%`
for `References_100KB_50Names` — but a ±33% benchmark cannot be told apart from a real regression by
a gate whose threshold is 25%.

**Why it is noisy.** The work is one pass of `bytes.Index` per rule over 100 KB, which is
memory-bandwidth bound and sensitive to whatever else the machine is doing; `00-ARCHITECTURE.md` §7's
note about this host's I/O latency applies to its scheduling too.

**Acceptance.** Record `testdata/bench-baseline.txt` for this benchmark from a quiet machine at
`-count 10` or more, and either confirm the spread is narrower there or exempt this one benchmark
from the 25% gate with the measured distribution as the justification. Do not tighten the code to
chase the number: the budget is met at the median and at every sample below the 90th percentile.
