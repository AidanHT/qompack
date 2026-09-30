# Wave 14b — w14-safecut round two

Branch `closeout/w14-safecut`. Workflow `wf_029d4490-109`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `18b07806`

### Root cause

Group-replaced rules (credentialed_uri, assignment_secret, dotenv_value, bearer_token) leave the text before their group raw in whole. A cut there has a kept side that is exactly whole's prefix, so the old search's 'smallest cut reproducing p = len(kept)' was always the cut itself. It then stepped back one rune per step, costing about L x (log2 n + 2) full-prefix Redact calls for a raw prefix of L bytes.

### Summary

Round two of w14-safecut on closeout/w14-safecut (worktree C:/Users/Quant/Documents/Programming/Projects/qompack-cx-w14-safecut), one commit on top of f2941913. The verifier's MAJOR latency regression is fixed. Round one's results still hold: no unsafe cuts, no spurious refusals, and nested rows are still maximal.

ROOT CAUSE (confirmed by a RED run). Some redaction rules replace only a group of their match: credentialed_uri, assignment_secret, dotenv_value and bearer_token. They leave the text before the group raw in `whole`: the URI scheme and user, the dotenv key, the bearer keyword and the blank after it. A cut inside that raw prefix has a kept side that redacts to exactly whole's prefix (p == len(kept)). Its rest has lost the text the rule needs, so it leaves the secret raw, and the cut is unsafe. The old search looked for the smallest cut that reproduces p bytes, but no shorter cut reproduces len(kept) bytes. So after about log2(cut) full-prefix Redact calls it returned next == cut and stepped back one rune. For a raw prefix of L bytes that is about L x (log2 n + 2) calls. Measured on the old code (from red-rawprefix-rows.txt):
- 1,000-byte URI user in a 17,033-byte window: 12,636 calls, 2.3 s
- the same URI with keep in the password: 16,919 calls
- dotenv key: 8,397 calls
- dotenv value: 16,946 calls
- bearer blank: 16,876 calls, 8.7 s
- the verifier's 264 KB probe with a 60-byte user: 716 calls

The old comment was wrong for group-replaced rules. It claimed the p == len(kept) case is always a token whose shortest matching prefix sits one rune above a cut that parts from whole.

FIX (internal/mcp/response_bound.go, safeCut). The outer loop now splits on the kept side at an unsafe cut:
- **Kept side parts from whole (p < len(kept)):** unchanged. A binary search finds the smallest cut that reproduces p bytes.
- **Kept side is whole's prefix (p == len(kept)):** no binary search on p. The search backs off from the cut by a doubling distance (1, 2, 4, ...) while the cut is "prefixUnsafe" (unsafe, with a kept side that is whole's prefix). It then binary-searches between the first probe where that fails and the last probe where it held, and takes the largest cut where it fails. That cut is either safe (just before the token or the raw prefix) or parts from whole, which the first branch then resolves.
- If the window's first rune boundary is itself prefixUnsafe, safeCut reports false. The outer loop would reach the same result.

Supporting changes:
- New closures: `split` (safety check that skips redacting the rest when the kept side already parts from whole), `prefixUnsafe`, and `halve` (the shared binary search).
- The comment now describes both cases: the two shapes that give p == len(kept), why stepping back a rune at a time was O(L log n), and the logarithmic bound.
- The final `next >= cut` guard and the never-return-an-unsafe-cut invariant are kept: every returned cut still passes the full split check.

Cost per outer step, with B = bits.Len(n):
- 2 calls to check the cut.
- Region-start search: B + 1 calls.
- Back-off: at most B + 1 doublings plus at most B halvings, 2 calls each, so 4B + 2.
- So one step costs at most 4B + 4 calls. The number of steps per region is constant: 2 from a raw prefix, 3 from a URI password, 4 from a dotenv value that matches on its own.

After the fix (same windows as above):
- URI user: 44 calls against a bound of 128.
- URI password: 59 against 192.
- dotenv key: 40 against 128.
- dotenv value: 70 against 256.
- bearer: 44 against 128.
- The new test takes about 0.6 s in total, including the 264 KB probe and the oracle checks.

TESTS ADDED (internal/mcp/response_bound_test.go):
1. **TestSafeCutRedactCallsAreLogarithmicInARawPrefix.**
   - Five production-rule shapes, each run with a 1,000-byte and a 40-byte raw run inside a window of about 17 KB (8,000 bytes of prose on each side):
     - credentialed URI, keep in the user
     - credentialed URI, keep in the password
     - dotenv key, keep in the key
     - dotenv key, keep in the value
     - bearer keyword, keep in the blank
   - A countingRedactor counts every Redact call. The test asserts calls <= steps * safeCutStepRedacts(len(window)), where safeCutStepRedacts is 4*bits.Len(n)+4. The per-step figure and the per-shape step counts are derived in comments from the algorithm, not from a measurement.
   - It also asserts that the cut is safe, that the returned kept bytes equal Redact(window[:cut]), and, for the 40-byte form, that the cut equals the oracle's largestSafeCut.
   - It pins the verifier's exact probe: 132 KiB of prose + " https://" + 60 x 'u' + ":pass123@host " + 132 KiB of prose, with keep inside the user. The cut must be exactly before the scheme's last letter (" http|s://", the largest safe cut) within 2*(4B+4) calls.
   - RED on f2941913: all 10 subtests and the probe failed.
2. **TestSafeCutIsTheLargestSafeCutOnProductionShapes.** An oracle sweep over every keep on 15 small windows built from the production rules. They cover credentialed URIs (including a word character before the scheme, and multi-byte runes), dotenv with and without export, two dotenv lines, bearer (and a repeated bearer), a password assignment, sk- tokens (including one without a word boundary), a JWT, sk- nested inside a URI password, and a URI inside a PEM block. safeCut must return a safe cut, the largest one at or before keep, and report false only when no cut is safe. It passes on both the old and the new code, so it is a maximality guard against this rewrite, not a RED row.

VERIFIER'S NON-MAXIMAL EXAMPLE: not pinned. I did not have the verifier's exact example, and a temporary diagnostic sweep (removed; now the permanent test above) found no case where the production shapes give a non-maximal cut. If the verifier's example is supplied, it can be added as a documented row.

COMMANDS AND RESULTS (-p 2; no load generators, no Linux container, no e2e or integration runs):
- RED against HEAD f2941913 (old response_bound.go with the new tests): `go test -p 2 -count=1 -timeout=10m -run '^TestSafeCutRedactCallsAreLogarithmicInARawPrefix$' -v ./internal/mcp` exits 1. All 10 subtests plus the verifier probe fail on the call bound. Log: plans/sdd/V6-closeout/w14-safecut/runs/red-rawprefix-rows.txt.
- New rows and all earlier safeCut/bound rows, -count=3, each exit 0. The combined run is in plans/sdd/V6-closeout/w14-safecut/runs/green-rawprefix-rows.txt; per test:
  - `go test -p 2 -count=3 -run '^TestSafeCutRedactCallsAreLogarithmicInARawPrefix$' ./internal/mcp`
  - `-run '^TestSafeCutIsTheLargestSafeCutOnProductionShapes$'`
  - `-run '^TestSafeCutFindsTheSafeCutBelowARedactedRegion$'`
  - `-run '^TestSafeCutReportsFalseOnlyWhenNoCutIsSafe$'`
  - `-run '^TestSafeCutFindsTheLargestSafeCutWithTheProductionRedactor$'`
  - `-run '^TestExpandBoundCutNeverSplitsARedactedRegion$'`
  - `-run '^TestExpandBoundPagesPastARedactedRegionLongerThanHalfThePage$'`
- `go test -p 2 -count=1 -timeout=30m ./internal/mcp`: ok, 75.4 s. Log: plans/sdd/V6-closeout/w14-safecut/runs/mcp-full-round2.txt.
- `go run ./tools/devtool fmt-check`: exit 0.
- `go vet ./internal/mcp` and `GOOS=linux go vet ./internal/mcp`: exit 0.
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns`: all eight PASS.
- No wall-clock failures occurred, so nothing needed a solo re-run.

SCOPE: only internal/mcp/response_bound.go, internal/mcp/response_bound_test.go, and three evidence logs under plans/sdd/V6-closeout/w14-safecut/runs/. No docs or generated inputs changed, so test/docs and gen-*-docs were not needed. No background processes were started. Scratch files are in C:/Users/Quant/AppData/Local/Temp/claude/C--Users-Quant-Documents-Programming-Projects-qompack/9c57653d-e5ff-4791-8ab4-ad49457994c1/scratchpad/w14/safecut/.

### Commits

- 18b07806 fix(mcp): back off a raw prefix in logarithmic safeCut steps

### Tests

- `go test -p 2 -count=1 -timeout=10m -run '^TestSafeCutRedactCallsAreLogarithmicInARawPrefix$' -v ./internal/mcp  (against f2941913's response_bound.go)` — RED as intended: exit 1; all 10 subtests and the verifier probe fail on the call bound (12,636 / 16,919 / 8,397 / 16,946 / 16,876 calls at 1,000 raw bytes; 716 on the 264 KB probe)
- `go test -p 2 -count=3 -timeout=10m -run '^TestSafeCutRedactCallsAreLogarithmicInARawPrefix$' ./internal/mcp` — PASS x3 (44/59/40/70/44 calls against bounds of 128/192/128/256/128)
- `go test -p 2 -count=3 -run '^TestSafeCutIsTheLargestSafeCutOnProductionShapes$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=3 -run '^TestSafeCutFindsTheSafeCutBelowARedactedRegion$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=3 -run '^TestSafeCutReportsFalseOnlyWhenNoCutIsSafe$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=3 -run '^TestSafeCutFindsTheLargestSafeCutWithTheProductionRedactor$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=3 -run '^TestExpandBoundCutNeverSplitsARedactedRegion$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=3 -run '^TestExpandBoundPagesPastARedactedRegionLongerThanHalfThePage$' ./internal/mcp` — PASS x3
- `go test -p 2 -count=1 -timeout=30m ./internal/mcp` — ok, 75.4 s
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/mcp ; GOOS=linux go vet ./internal/mcp` — both exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all eight PASS

### Criterion changes

- Added TestSafeCutRedactCallsAreLogarithmicInARawPrefix. It bounds safeCut's Redact calls by steps * (4*bits.Len(n)+4), a bound derived from the algorithm. It is a test criterion, not a product budget: no constant was added to production code.
- Added TestSafeCutIsTheLargestSafeCutOnProductionShapes, an oracle maximality sweep with the production redactor. No existing assertion, threshold or row was changed or loosened.

### Open issues

- The verifier's specific non-maximal, context-dependent example was not in my inputs, so it is not pinned. The new production-shape oracle sweep (15 windows, every keep) found no non-maximal cut. If the coordinator supplies the example, it can be added as a documented row in TestSafeCutIsTheLargestSafeCutOnProductionShapes.
- Evidence logs quote the combined alternation -run pattern. The report above quotes each test separately, because runpatterns splits alternations at '|'.

## Independent review

### review:safecut: needs-fixes

- **major** `internal/mcp/response_bound.go:205-221` — The new back-off brings back spurious refusals and badly non-maximal cuts. Round 1 fixed both, and the task said to keep that fixed. prefixUnsafe is not monotone across regions. The doubling back-off keeps going while each probe is prefixUnsafe, but it only checks the probes, not the cuts between them. So it jumps over a short safe gap into an earlier region whose cuts are also prefixUnsafe: the inside of a run token, the part of a group value past its minimum length, or another raw key or user. It then either reaches `first` and returns false (line 212-215), or halve() settles on a false-to-true change inside the earlier region and returns a much smaller cut.
  - Evidence: I ran an oracle sweep in a scratch copy made with git archive; the worktree was not touched.

Random compositions of production shapes (sk-, credentialed URI, dotenv, bearer, password=), every keep, 35,692 cases:
- HEAD 18b07806: 0 unsafe, 2,571 non-maximal, 1,813 spurious refusals.
- f2941913 with the same test: 0 / 0 / 0.

Concrete rows, all on HEAD:
- "password=hunter2hunter   password=hunter2hunter " with keep 34 is REFUSED, but cut 25 is safe. The doubling probes are 33, 32, 30, 26, 18 (inside the first value) and 2, and all are prefixUnsafe. Then first is unsafe, so it returns false.
- "sk-"+30a+" y https://"+40u+":pw123@h z": 16 keeps refused, but the largest safe cut is 40.
- "x sk-"+30a+" y https://"+40u+":pw123@h z" with keep 59 returns cut 2; the largest safe cut is 42.
- "ab https://u1:pw1@h https://"+40u+":pw123@h z" with keep 41 returns cut 7; the largest safe cut is 24.
- "ab\nAPP_KEY=s3cr3t-v4lue-0123\nAPP_KEY"+40A+"=..." with keep 40 returns cut 5; the largest safe cut is 31.

The refusal shape is realistic. The previous page cuts at the largest safe cut, which is often just before a secret region, so the next window often starts inside such a region.
  - Fix: Stop driving the back-off with a predicate that also holds in earlier regions. One way: at the unsafe cut, take s = commonSuffixLen(Redact(window[cut:]), whole). While the cut is inside the same region's raw prefix or run, the rest leaves the same secret raw, so its agreement with whole's suffix stays at s. At or below the region's start, the rest holds the whole match and redacts it, so the agreement is greater than s. "commonSuffixLen(rest(c), whole) > s" is therefore false in the region and true below its start. Binary-search it (galloping or plain over [first, cut]) for the largest c where it holds. That costs 1 Redact per probe, stays logarithmic, and cannot jump over a gap. Keep the full split check on the returned cut. Alternatively, keep the doubling, but make each probe also check that its rest disagrees with whole at the same suffix position s.
- **major** `internal/mcp/response_bound_test.go:803` — The new maximality sweep TestSafeCutIsTheLargestSafeCutOnProductionShapes cannot catch this regression. Each window has one long raw prefix, or short raw runs with gaps that are wide compared with the back-off distance. None has two regions close together with a longer raw prefix in the second. The implementer's report says a diagnostic sweep "found no case where the production shapes give a non-maximal cut", but that claim rests on this narrow set.
  - Evidence: All 15 windows pass on HEAD, but the five multi-region windows in the previous finding fail against the same oracle (largestSafeCut) on HEAD and pass on f2941913.
  - Fix: Add multi-region windows to the sweep and confirm they are RED on 18b07806 before the fix: two password= assignments, an sk- run followed by a short gap and a long URI user, two credentialed URIs, two dotenv lines with a long second key, and each of these starting at window offset 0. Also add a seeded random composition sweep (small windows, every keep, oracle equality, and no false result while any safe cut exists). Keep the call-count rows.
- **minor** `internal/mcp/response_bound.go:212-215` — The comment on the early `return 0, nil, false` claims something that is false: that when the first rune boundary is prefixUnsafe, "in the model above, is every cut between it and this one". The doubling probes are not contiguous, so safe cuts can lie between them. The implementer's summary repeats the claim ("The outer loop would reach the same result"). The doc comment at :116-122 ("That cut is safe ... or parts from whole") also leaves out that halve over a non-monotone predicate can settle in an earlier region.
  - Evidence: The first row above: "password=hunter2hunter   password=hunter2hunter " with keep 34 hits this return while cut 25 is safe. The old outer loop, stepping back one rune at a time, returned 25.
  - Fix: After fixing the search, reword the comment so it states only what the new predicate guarantees. Do not return false early unless every cut between first and the last probe is shown to be unsafe; otherwise leave that decision to the outer loop's split check at first.
- **nit** `internal/mcp/response_bound_test.go:803` — The verifier's known non-maximal, context-dependent example (optional in the task) is not pinned. The implementer says they did not have it. The rows in the first finding are documented non-maximal and refusal examples of the same kind, and could fill this slot.
  - Evidence: The implementer's open_issues[0].
  - Fix: Pin one or more of the rows from the first finding as documented rows, with the oracle's expected cut.

## Fix seat (review resolution) — status `done`, head `652fc0c3`

### Root cause

At 18b07806, when an unsafe cut's kept side is exactly a prefix of whole, safeCut backed off by doubling distances and kept going while each probe was "prefixUnsafe" (unsafe, with a kept side that is a prefix of whole). That predicate is also true inside an earlier region of the same kind: a run token, the tail of a group value, or another raw URI user or dotenv key. The gallop checks only its probes, so it jumped a short safe gap into the earlier region. From there it either reached `first` and returned false early, or halve() settled on a transition inside the earlier region. Reproduced exactly as the reviewer described: "password=hunter2hunter   password=hunter2hunter " with keep 34 was refused on 18b07806, although cut 25 is safe (red log committed).

The fix changes the predicate so it describes the cut's own region. A probe counts as inside only if all three hold: (1) the cut is unsafe, (2) its kept side is a prefix of whole, and (3) Redact(window[c:]) matches the end of whole for no more than s bytes, where s is how far the unsafe cut's own rest matches. Each of these ends the gallop: a probe in a safe gap (safe), a probe in an earlier region whose kept side parts from whole (kept not a prefix), and a probe whose rest holds the whole match (it matches more than s). So the gallop cannot jump across the gap.

The reviewer's first proposal was the rest-only test ("matches more than s"), with a binary search or gallop on it. I tried it and it failed the existing oracle rows, so it was not used:
- "ab\nexport APP_KEY=s3cr3t-v4lue-0123\nzz" with keep 29 returned 3; the largest safe cut is 12. Once the kept side parts from whole the rest test stays false, and the gallop ran down to the window start.
- "…\npassword=hunter2password=hunter2…" returned 46 instead of 47. The rest at the region's start reproduces exactly as much as the rest at the unsafe cut.

The combined test (the reviewer's second alternative, made stricter) handles both.

### Summary

## Fix seat, w14-safecut round two (after review)

The review's three findings are all confirmed and fixed in one commit, 652fc0c3, on closeout/w14-safecut. Only `internal/mcp/response_bound.go` and its test file changed, plus evidence logs under `plans/sdd/V6-closeout/w14-safecut/runs/`.

### What changed
- **The back-off from a raw prefix now stays inside its own region.** In the case where the kept side is exactly a prefix of whole, safeCut gallops back from the unsafe cut by doubling distances. It keeps going only while the probe is still in the same region:
  - the cut is unsafe;
  - its kept side is a prefix of whole;
  - its rest matches the end of whole for no more than `s` bytes, where `s` is how far the unsafe cut's own rest matches.
  - It stops at the first probe outside the region, then binary-searches (halves) between that probe and the last one inside.
  - A probe in a safe gap, in an earlier region, or holding the whole match fails that test, so the gallop cannot jump a gap.
- **No early refusal inside the loop.** When `first` is inside the region, `first` becomes the next cut and the outer split check decides.
- **Cost is unchanged.** Each probe costs 2 Redact calls; the bound per step is still `4*bits.Len(n)+4`. Measured call counts on the long raw-prefix rows are 24 to 70, against bounds of 120 to 256. The verifier's 264 KB probe stays within its bound, where the round-1 linear search took about 1,014 calls.
- **New helper** `commonSuffixLen`. `split` now also returns the redacted rest.
- **Doc comment rewritten** (finding 3). It says what the new predicate guarantees, and it states that the "largest safe cut" result holds only under the model. Outside it (overlapping matches, or a rest-side match that depends on the byte at the cut, such as a URI scheme that must start with a letter or a dotenv key that must start with a capital), the search may return a smaller safe cut or refuse, but never an unsafe cut. The inline comment at the `first` exit now says "in the model above".

### New and extended tests
- **`TestSafeCutIsTheLargestSafeCutOnProductionShapes`** gains the reviewer's five multi-region windows plus two variants that start at offset 0 (a URI pair and a dotenv pair).
- **New `TestSafeCutMatchesTheOracleOnRandomProductionCompositions`**: two seeded sweeps of 300 compositions each, every keep, over sk- runs, credentialed URIs, dotenv lines, bearer tokens and password assignments. The separated sweep (blank, newline or punctuation gaps) requires exact equality with the oracle, including refusals. The fused sweep (empty or letter gaps as well) requires every cut to be safe and no refusal while a safe cut exists.
- **New helper `largestSafeCutsByKeep`**: the oracle for every keep in one pass.
- **RED/GREEN evidence.** Both sweeps are RED on 18b07806 ("window 15 keep 34: got cut 0, largest safe 25", and "separated 2 … keep 57: expected 40, actual 4"). They are GREEN on f2941913 (the linear reference) and on 652fc0c3.
- **Temporary diagnostic** (deleted, not committed), 1,000 compositions per sweep:
  - separated: 70,954 cases, 0 unsafe, 0 non-maximal, 0 refused;
  - fused: 69,123 cases, 0 unsafe, 0 refused, 27 non-maximal. All 27 are token fusions outside the model, for example "…ABChttps://…" or "hunter2https://…".

### Review resolution
- **Finding 1 (major, spurious refusals and non-maximal cuts from the back-off): confirmed and fixed.** Reproduced on 18b07806. Fixed with the combined in-region test described above. The reviewer's first proposal, the rest-only test, was tried and rejected because it failed existing oracle rows ("ab\nexport APP_KEY=…" with keep 29 gave 3 instead of 12; "…password=hunter2password=…" gave 46 instead of 47). The stricter form of the reviewer's second alternative passes every row.
- **Finding 2 (major, the sweep could not catch the regression): confirmed and fixed.** Multi-region windows and a seeded random sweep added. Both are RED on 18b07806 and GREEN after the fix. The call-count rows are kept.
- **Finding 3 (minor, false comments): confirmed and fixed.** Comments reworded. The loop no longer returns false early; the decision is left to the split check at `first`.
  - Partial rebuttal on one point: proving that every cut between `first` and the last probe is unsafe would need a linear scan. That is exactly the O(L log n) cost this round removes, and adversarial content could exploit it to slow every call. So the refusal is exact only within the model, and the doc comment says so.

### Criterion changes (with rationale)
- **Oracle equality is required only on separated compositions.** On fused compositions the test checks safety and no spurious refusal, not maximality. Rationale: when tokens run together, a rest-side match can depend on the byte at the cut, so safe and unsafe cuts alternate inside one run of word characters (for example "hunter2|2https" is unsafe while "hunter2h|ttps" is safe). A logarithmic search cannot be exact there; only a linear scan can, and a linear scan is the latency regression this round removes. The task already allowed documenting a known non-maximal, context-dependent case.
- Nothing existing was loosened. Every earlier row is unchanged and green, including `TestExpandBoundCutNeverSplitsARedactedRegion`, the round-1 rows, the nested rows and the call-count bounds.

### Commands and results
Evidence logs are in `plans/sdd/V6-closeout/w14-safecut/runs/`: `red-review2-sweeps-on-18b07806.txt`, `green-review2-rows-count3.txt`, `mcp-full-review2.txt`. The Linux container was stopped per the seat limits, so no Linux run was made; only `GOOS=linux go vet`. No load generators and no background processes were started, and the scratch copies are removed.

### Commits

- 652fc0c3 fix(mcp): keep safeCut's raw-prefix back-off inside its region

### Tests

- `go test -p 2 -count=1 -run '^(TestSafeCutIsTheLargestSafeCutOnProductionShapes|TestSafeCutMatchesTheOracleOnRandomProductionCompositions)$' -v ./internal/mcp/ (scratch copy: git archive 18b07806 + new test file)` — FAIL (RED as intended): window 15 keep 34 got cut 0, largest safe 25; separated 2 keep 57 expected 40 actual 4
- `same command on git archive f2941913 + new test file` — ok (both PASS, 7.5s): linear round-1 reference agrees with the oracle
- `go test -p 2 -count=3 -run '^(TestSafeCutIsTheLargestSafeCutOnProductionShapes|TestSafeCutMatchesTheOracleOnRandomProductionCompositions|TestSafeCutRedactCallsAreLogarithmicInARawPrefix|TestSafeCutFindsTheSafeCutBelowARedactedRegion|TestSafeCutReportsFalseOnlyWhenNoCutIsSafe|TestSafeCutFindsTheLargestSafeCutWithTheProductionRedactor|TestExpandBoundCutNeverSplitsARedactedRegion|TestExpandBoundPagesPastARedactedRegionLongerThanHalfThePage)$' -v ./internal/mcp/` — ok, 24/24 PASS (29.5s)
- `go test -p 2 -count=1 ./internal/mcp/` — ok (76.7s)
- `go run ./tools/devtool fmt-check` — exit 0
- `go vet ./internal/mcp/ ; GOOS=linux go vet ./internal/mcp/` — exit 0 / exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — all eight PASS

### Criterion changes

- TestSafeCutMatchesTheOracleOnRandomProductionCompositions (new) requires oracle equality only on its separated sweep. Its fused sweep checks every cut is safe and no false while a safe cut exists, but not maximality. Rationale: fused tokens put rest-side matches that depend on the byte at the cut outside the model, and exact maximality there needs a linear scan, which is the O(L log n) latency regression this round removes. No existing assertion, row, threshold or bound was changed.

### Open issues

- Outside the model (fused tokens, or a rest-side match that depends on the byte at the cut), safeCut can return a smaller safe cut than the largest one, and in principle can refuse. It never returns an unsafe cut. Measured with a temporary diagnostic over 69,123 fused cases: 27 non-maximal, 0 refused, 0 unsafe. This is documented in the safeCut doc comment and pinned by the fused sweep.
- No Linux -race run this round (container stopped per the seat limits); only GOOS=linux go vet.

## Independent verification of the fix seat: needs-fixes

- **minor** `internal/mcp/response_bound_test.go:876-877 (sweep at :896-912); internal/mcp/response_bound.go:216-243` — The three review findings are resolved. I found no spurious refusals on separated shapes, no unsafe cuts, and call counts stay logarithmic. One class of non-maximal cut is left, though. The new test comment says separated compositions are "the model safeCut's search is exact in", and the fix report says "separated: 0 non-maximal". Neither is true for an ordinary production shape: an `export`-prefixed dotenv line. Inside that one rule match, safe and unsafe cuts alternate. Cuts inside "export" are unsafe; "export |DB" and "export D|B" are safe, because the rest still matches ^[ \t]*[A-Z]...PASSWORD; "DB|_" is unsafe again. The inside() predicate is therefore not contiguous. The gallop and halve settle at the line start, up to 8 bytes below the largest safe cut. The round-1 linear search (f2941913) returns the largest safe cut here. So this is still a small maximality regression against round 1, on a shape the doc calls "separated". The committed separated sweep only includes `APP_KEY` lines with no `export`, and those happen to land exactly. The committed "ab\nexport APP_KEY=..." row passes by the geometry of the probe positions, not by construction.
  - Evidence: All runs were in scratch git-archive copies; the worktree was not touched.

Concrete rows, HEAD 652fc0c3, production redactor:
- "ab \nexport DB_PASSWORD=s3cr3t-v4lue-0123\nzz" with keep 23 returns cut 4; the largest safe cut is 12.
- "\nexport DB_PASSWORD=s3cr3t-v4lue-0123\nzz" with keep 20 returns 1; the largest is 9.
- "sk-"+25a+" \nexport DB_PASSWORD=..." with keep 49 returns 30; the largest is 38.
- Every one of the 260 windows of the form pre+"\nexport DB_PASSWORD"+A*k+"=..." is non-maximal for some keep, with k from 0 to 64 and 4 prefixes. None of the "\nAPP_KEY"+A*k forms is.

HEAD against the f2941913 safeCut on 1,500 separated compositions (gaps " ", "  ", "\n", " y ", "; "): HEAD is worse in 5,753 cases and better in none. Every loss is exactly 8 bytes, and every worse case contains the export-dotenv piece.

A broader separated sweep (about 297k cases, 16 production shapes including ghp_, AKIA, jwt, pem, sk-ant, api_key:, token =):
- HEAD: 0 unsafe, 0 spurious refusals, about 5.9k non-maximal.
- f2941913: 226 non-maximal, all a pre-existing AKIA fixed-length phantom-match class in the p-branch.
- 18b07806: 16.8k non-maximal and 3.8k refusals.

The fused sweep has the same 131 refusals on HEAD and on f2941913, so this round did not cause them. Max Redact calls per safeCut: 53 to 79 on HEAD against about 480 on f2941913. 64 KB adversarial windows (long key runs, export lines, URIs, bearer runs, fused password runs) all finish in 2 to 68 calls. The focused TestSafeCut*/TestExpandBound* suite passes on HEAD.
  - Fix: Keep the code; its cost profile is right, and exact maximality here would need a linear scan. Two fixes, both needed:
1. Make the claim honest. Reword the separated-sweep comment (and the "separated: 0 non-maximal" record) so it names the export/indented-dotenv class as outside the model. Say the class is non-maximal by at most the line prefix before the first key character that still starts a rest-side match. Extend the safeCut doc paragraph at :137-145 to name this class explicitly; "inside a run of word characters" does not cover "export |DB".
2. Pin the class. Add an "export DB_PASSWORD" piece to the fused/safety-only sweep, or add a dedicated row. The row asserts a safe cut, no refusal, and a loss bounded by that prefix. Then a future change cannot widen the loss unnoticed, and the exact sweep no longer rests on probe-geometry luck.

