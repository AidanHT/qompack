# Wave 15a workstream paging

Branch `closeout/w15a-paging`. Workflow `wf_3f954a82-13d`. Subagents cannot write report files in this harness, so the coordinator committed this verbatim from their returned results.

## Implementer — status `done`, head `2a763ca0`

### Root cause

span.go set Truncated as 'less than the whole object' (off>0 || end<total) but NextSpan only when end<total, so every last page reached by following a cursor said truncated with no next_span. An explicit span was capped back to a chunk start in step 7, a different raw window from full's byte cap (so the response bound cut differently), and both the resolver and cutSpan gave explicit spans a chunk.max cursor where full got min(maxResp, rest).

### Summary

W15a paging (D50) is done on closeout/w15a-paging, three commits on efd5bf68, head 2a763ca0. The worktree is clean and nothing I started is still running.

ROOT CAUSES (both in internal/mcp/span.go, with a matching cursor rule in response_bound.go cutSpan):
(1) A final page said truncated:true with no next_span. resolveSpan set `Truncated = off > 0 || end < total`, which means "less than the whole object", but it only set NextSpan when `end < total`. Any page that starts after offset 0 and reaches the end therefore said truncated with no cursor. This is the last page of every walk (live: expand span 217070:137282 gave [217070,354352], truncated true, no next_span). The test/e2e x02 walk even pinned that old meaning.
(2) An explicit span paged differently from full:true over the same range. A full read takes the byte window [0, min(total, maxResp)) and its cursor was end:min(maxResp, rest). An explicit span went through step 7 instead: its window was capped back to the chunk start at or before off+maxResp, and every cursor (the resolver's and cutSpan's) was end:chunk.max. So the raw window going into boundedContent was different, which gave a different cut, and the cursor was 16384 bytes. RED reproduction at the live shape (324,902-byte log at the default bound): the explicit span cut at 229376 with next_span 229376:16384, and full produced a different page sequence.

FIX: one rule, nextSpanFor, now sets Truncated and NextSpan for every window. The resolver and cutSpan both use it:
- A page that stops before the object's end is truncated, and its cursor starts at its End.
- A page that reaches the end is neither truncated nor given a cursor, wherever it started.
- A range read (full, or an explicit span with ExactStart, which is what the retrieval tools always set) goes through rangeWindow, the same code a full read uses: a byte cap at off+maxResp and a rune-safe end. The response bound then makes the identical cut. A page cut short of the range gets a cursor over the rest of the range. SpanResult carries this range end in an unexported field, `want`.
- Minimal spans still continue in chunk.max steps.
- Callers that do not set ExactStart keep the chunk-aligned step-7 cap.
- Side fix: an explicit length near the int64 maximum used to wrap offset+length negative, so "1:9223372036854775807" served 299 bytes. The length is now saturated at the object size.
- The D48 safeCut code is untouched, and every existing response-bound and safeCut test passes.
- docs/mcp-tools.md: I changed the generator's text (tools/devtool/genmcpdocs.go) and regenerated with gen-mcp-docs.

TESTS: all new rows were RED on base efd5bf68 before the fix (product files stashed) and are GREEN after. Evidence is committed in plans/sdd/V6-closeout/w15a-paging/runs/01-05.
New in internal/mcp/paging_test.go:
- TestExpandEveryTruncatedPageCarriesNextSpan: full, minimal, span 0:total, a span from mid-object to the end, and re_read full. It checks that truncated, next_span and "the page stops before the end" agree on every page, in both the body and _meta.
- TestExpandExplicitSpanPagesLikeFull: the live-shaped log at the default bound, and escape-heavy content at the 4096 bound. Page by page it checks the same span, cursor, truncated and bytes, and that every cursor ends at total.
- TestExpandExplicitSubSpanNextSpanCoversTheRestOfTheRequest
- TestResolveSpanExplicitWholeRangeMatchesFull
- TestResolveSpanFullCursorCoversTheRestOfTheObject
- TestResolveSpanExplicitHugeLengthReadsToTheEnd
Strengthened: pageToEndFrom now also asserts the last page is not truncated. That turned six existing rows RED on base:
- TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy
- TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy
- TestExpandMinimalSpanResponseTextFitsMaxResponseBytes
- TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot
- TestExpandFollowsNextSpanWhenDefaultSpanIsFull
- TestExpandPagesNeverSplitARune

CRITERION CHANGES (rationale is D50):
(a) test/e2e/v5_x02_test.go x02ExpandPaged asserted `truncated == (Span[0] > 0 || Span[1] < TotalBytes)`, the old meaning that D50 overturns. It now asserts `truncated == (Span[1] < TotalBytes)` plus `truncated == (next_span != "")`. This is stricter: it adds the cursor check.
(b) full:true's cursor now covers the rest of the object (end:total-end) instead of min(maxResp, rest), so that it matches an explicit span's "rest of the requested span". TestFullTruncatesAtMaxResponseBytes ("262144:137856") is unchanged, because both rules give the same value there.
(c) A cut explicit span's cursor now covers the rest of its range instead of chunk.max.
(d) With ExactStart, a capped explicit window now ends where a full read would, not on a chunk boundary. The span.go property comment says so. The property tests run without ExactStart and are unchanged.
No test was skipped or loosened, and no threshold or golden was changed. docs/mcp-tools.md is a generated page, not a golden.

LIMITS OF "SAME CUT" (for the coordinator's reading):
- The cut is identical when the response envelope is identical. The envelope carries the object's expansion count, which grows with every call, so two walks run one after the other on the same object can differ by one byte (for example "expansions":9 against "expansions":10). The test therefore runs each walk in its own fresh project.
- For an explicit sub-span, "the requested span" is the range the request resolves to, whose end is aligned outward to the next chunk boundary. The outward alignment is existing §5.16 behaviour and is kept. Once that range is served, a page still short of the object is truncated and continues in chunk.max steps.

VERIFICATION (all under daytime limits: -p 2, no -race, no hot-path rows, no whole test/e2e or test/integration, Linux container not started):
- Formatting and vet: `go run ./tools/devtool fmt-check` exit 0; `go vet` on internal/mcp, tools/devtool and test/e2e exit 0 on Windows and with GOOS=linux.
- Lint: `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` exit 0. runpatterns and docmarkers passed again after the evidence commit.
- Docs: `go run ./tools/devtool gen-mcp-docs --check` is up to date.
- Whole packages:
  - internal/mcp `go test -p 2 -count=1 ./internal/mcp`: ok, 209.1 s, at the final code.
  - tools/devtool `go test -p 2 -count=1 -timeout=19m ./tools/devtool`: ok, 188.7 s.
  - test/docs `go test -p 2 -count=1 ./test/docs`: ok.
- Focused e2e: `go test -p 2 -count=1 -timeout=30m ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$'` PASS. It was RED on base with the updated assertion ("truncated must report exactly whether this page stops before the object's end: [193080 204820]").
- Focused rows that read truncated:
  - `go test -p 2 -count=1 -timeout=30m ./test/e2e -run 'TestStdioServerEndToEnd|TestV4_T13HandleResolvesAfterCompactionOverStdio'` PASS.
  - `go test -p 2 -count=1 ./test/security -run '^TestSecurity_MCPResponseStaysWithinItsConfiguredBound$'` PASS.
- One caveat: the e2e, security and test/docs rows ran before a last refactor of span.go's ExactStart branch (error handling only, no behaviour change). The internal/mcp package run and the focused rows were repeated after it.
- No wall-clock failures occurred.

NOT FIXED HERE: docs/uat.md's UAT-12 Result text and report-c4's semantics note describe what candidate 4 did; that is accurate as history and belongs to the docs work in wave 15b. The candidate 5 live re-run of UAT-12 should confirm that span 0:354352 now pages 217070:137282 and that the final page is truncated:false.

### Commits

- 862c0154 fix(mcp): keep truncated honest and page explicit spans like full
- 31641d6d docs(mcp): state the paging rules in the generated tool page
- 2a763ca0 test(v6): record the w15a paging red and green runs

### Tests

- `go test -p 2 ./internal/mcp -count=1 -v -run 'TestExpandEveryTruncatedPageCarriesNextSpan|TestExpandExplicitSpanPagesLikeFull|TestExpandExplicitSubSpanNextSpanCoversTheRestOfTheRequest|TestResolveSpanExplicitWholeRangeMatchesFull|TestResolveSpanFullCursorCoversTheRestOfTheObject|TestResolveSpanExplicitHugeLengthReadsToTheEnd|TestExpandFullResponseTextFitsMaxResponseBytes_EscapeHeavy|TestReReadFullResponseTextFitsMaxResponseBytes_EscapeHeavy|TestExpandMinimalSpanResponseTextFitsMaxResponseBytes|TestExpandFullAtTheDefaultBoundFitsTheLiveOvershoot|TestExpandFollowsNextSpanWhenDefaultSpanIsFull|TestExpandPagesNeverSplitARune' (base efd5bf68, product files stashed)` — RED: all 12 FAIL (runs/01-red-mcp-base.txt)
- `same 12 rows at the fix` — PASS 12/12 (runs/03-green-mcp-focused.txt)
- `go test -p 2 -count=1 -timeout=30m ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$' (base, product stashed)` — RED as intended (runs/02-red-e2e-x02-base.txt)
- `go test -p 2 -count=1 -timeout=30m ./test/e2e -run '^TestV5_TombstoneToExpandRoundTrip$'` — PASS
- `go test -p 2 -count=1 -timeout=30m ./test/e2e -run 'TestStdioServerEndToEnd|TestV4_T13HandleResolvesAfterCompactionOverStdio'` — PASS
- `go test -p 2 -count=1 ./test/security -run '^TestSecurity_MCPResponseStaysWithinItsConfiguredBound$'` — PASS
- `go test -p 2 -count=1 ./internal/mcp` — ok 209.1s (final code)
- `go test -p 2 -count=1 -timeout=19m ./tools/devtool` — ok 188.7s
- `go test -p 2 -count=1 ./test/docs` — ok
- `go run ./tools/devtool gen-mcp-docs --check` — up to date
- `go run ./tools/devtool fmt-check; go vet (Windows and GOOS=linux) ./internal/mcp ./tools/devtool ./test/e2e` — exit 0
- `go run ./tools/devtool lint --only=golangci-lint,nomagic,importgraph,testdeps,bindeps,sleepcheck,docmarkers,runpatterns` — exit 0

### Criterion changes

- test/e2e/v5_x02_test.go x02ExpandPaged: truncated == (Span[0]>0 || Span[1]<Total) replaced by truncated == (Span[1]<Total) plus truncated == (next_span != ""). Stricter; D50 overturns the old meaning.
- internal/mcp pageToEndFrom: the last page must also be truncated:false (strengthened).
- full:true cursor now covers the rest of the object (end:total-end), not min(maxResp, rest), so it matches an explicit span's 'rest of the requested span' (D50). The existing 262144:137856 pin is unchanged.
- A cut explicit span's cursor now covers the rest of its resolved range, not chunk.max (D50).
- With ExactStart, a capped explicit window ends where a full read would (byte cap), not on a chunk boundary. The span.go property comment is updated; the property tests run without ExactStart and are unchanged.

### Open issues

- 'Same cut' holds only for identical envelopes: the expansion count in the envelope can shift the bound's cut by a byte between call histories. The tests use a fresh project per walk.
- An explicit sub-span's range ends at the chunk boundary the request resolves to (the existing §5.16 outward alignment is kept). Once that range is served, pages still short of the object continue in chunk.max steps.
- docs/uat.md's UAT-12 Result and report-c4's semantics note describe candidate 4's behaviour, which is accurate as history. That text belongs to the wave 15b docs work.
- The candidate 5 live re-run of UAT-12 should confirm that span 0:354352 now pages 217070:137282 and that the final page is truncated:false.

## Independent review

### review:paging: sound

- **nit** `tools/devtool/genmcpdocs.go:140-143 (rendered docs/mcp-tools.md:50-53)` — The new paging text says an explicit span is read over "the range it names" and that its next_span "covers the rest of that range". Neither statement is fully accurate. The range actually served ends at the next chunk boundary after the named end, because the existing §5.16 outward alignment is kept, and a line span also starts on a chunk boundary. And once that range has been served, a page that still stops before the object's end carries a chunk.max-sized next_span that goes past the range. A caller who reads the doc literally would expect either no cursor at that point or one that stays inside the range.
  - Evidence: span.go resolveWindow computes end = chunkEndAtOrAfter(starts, total, clamp64(want, off+1, total)), which aligns the end outward. nextSpanFor falls back to spanStr(s.End, minInt64(MaxSpan, Total-End)) when want <= End < Total. TestExpandExplicitSubSpanNextSpanCoversTheRestOfTheRequest pins both behaviours: reqEnd = chunkEndAtOrAfter(...), and the last page of the request is still truncated with a cursor.
  - Fix: In the generator, say the range is the one the span resolves to, which ends on the next chunk boundary, and that once the range is served a page short of the object's end continues in store.chunk.max-sized steps. Then regenerate with `go run ./tools/devtool gen-mcp-docs`. The behaviour itself needs no change.

## Fix seat

Not run: the reviews returned no actionable (non-nit) findings.

