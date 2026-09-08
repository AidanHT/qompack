# SP-15 role B report — grammar codec and state-aware warnings

**Status: DONE.** Both missions are implemented, tested and green on
`wip/sp15-b-codec-warnings` (worktree `qompack-sp15-b`, branched from `6eac57c`).

Scope worked to: [contract.md](contract.md) §0 (ownership), §4 (codec), §5 (warning record), §7
(switches), §8 (fixture layout), §9 (conformance), plus
[SP-15 §3 "State-aware warnings"](../../V5-SP-15-analyzer-selection-and-grammar.md).

## 1. Files touched

| File | Status | What |
|---|---|---|
| `internal/grammar/codec.go` | new, 345 lines | `EncodeSnapshot` / `DecodeSnapshot` and the versioned frame |
| `internal/grammar/codec_test.go` | new, 457 lines | frame, determinism, round-trip (table + rapid property), compatibility vectors, every rejection path |
| `internal/grammar/statewarn.go` | new, 655 lines | `StateSignature.Key`, `Detector`, `DetectorConfig`, `DetectorStats`, `Outcome`, `IsSelfOriginated` |
| `internal/grammar/statewarn_test.go` | new, 673 lines | one named test per contract §5 bound, plus key/expiry/rendering/determinism |
| `internal/grammar/testdata/codec/v1-empty.golden` | new, 21 B | version-tagged compatibility vector |
| `internal/grammar/testdata/codec/v1-rules.golden` | new, 92 B | version-tagged compatibility vector |
| `internal/grammar/testdata/codec/v1-edges.golden` | new, 46 B | version-tagged compatibility vector |

`internal/grammar/formatwarning.go` was **not modified**. It is in B's write set, but nothing in
either mission needed to change it, and leaving it byte-identical keeps the frozen wording provably
untouched. `types.go`, `sequitur.go`, `doc.go` and `grammartest/` are untouched (A's and Main's).

Imports stay inside grammar's allow-set: `codec.go` uses `encoding/binary`, `fmt` and
`internal/core`; `statewarn.go` uses `bytes`, `slices`, `strconv`, `strings` and `internal/core`.
The tests add only `testify` and `pgregory.net/rapid`; no `internal/testutil`, no composition root.
`devtool lint`'s `importgraph` and `testdeps` sub-checks both pass (below).

## 2. Gates exercised — actual output

### 2.1 The four required gates

```
$ gofmt -l internal/grammar
(no output — clean)

$ go build ./internal/grammar/
(exit 0)

$ go vet ./internal/grammar/...
(exit 0)

$ go test ./internal/grammar/... -count=1
ok  	github.com/qompack/qompack/internal/grammar	1.329s
ok  	github.com/qompack/qompack/internal/grammar/grammartest	0.956s
```

### 2.2 `-v` confirmation that the patterns actually matched cases

`go test -run` prints `ok` when it matches nothing, so the run was repeated verbosely:
**93 PASS lines, 0 FAIL**, plus the two expected Rule W-1 skips.

```
$ go test ./internal/grammar/... -count=1 -v | grep -cE "^(--- |    --- )PASS"
93
$ go test ./internal/grammar/... -count=1 -v | grep -cE "FAIL"
0
```

Top-level results:

```
--- PASS: TestEncodeSnapshot_FrameHeader
--- PASS: TestEncodeSnapshot_Deterministic            (3 subtests)
--- PASS: TestCodec_RoundTripIdentity                 (4 subtests)
--- PASS: TestCodec_CompatibilityVectors              (3 subtests)
--- PASS: TestDecodeSnapshot_RejectsBadFrames         (13 subtests)
--- PASS: TestDecodeSnapshot_RejectsEveryTruncation   (3 subtests)
--- PASS: TestDecodeSnapshot_DoesNotAliasInput
--- PASS: TestDecodeSnapshot_OnlyEverReportsKnownSentinels
--- PASS: TestCodec_RoundTripIdentityProperty         (rapid)
--- PASS: TestFormatWarning                           (unchanged, still 3 subtests)
--- PASS: TestStateSignature_Key                      (3 subtests)
--- PASS: TestStateWarning_IsAdvisoryOnly             (bound 1)
--- PASS: TestDetector_ProgressObservedSuppressesEntirely     (bound 2, 4 subtests)
--- PASS: TestDetector_ProgressUnknownIsOnlyEverUncertain     (bound 3, 5 subtests)
--- PASS: TestDetector_DeduplicatesAndCapsPerSession          (bound 4, 5 subtests)
--- PASS: TestDetector_SelfOriginatedNeverWarns               (bound 5, 3 subtests)
--- PASS: TestIsSelfOriginated                                (9 subtests)
--- PASS: TestDetectorStats_SeparatesUsefulnessFromFrequency  (bound 6, 5 subtests)
--- PASS: TestDetector_ExpiryBoundsDelivery
--- PASS: TestDetector_RendersThroughFormatWarning     (5 subtests)
--- PASS: TestDetector_ZeroConfigTakesDefaults         (1 subtest)
--- PASS: TestDetector_IsDeterministic
--- SKIP: TestRunSequiturSuite_StubIsSkipped           behaviour: implementation is a stub (Rule W-1)
--- SKIP: TestRunSequiturSuite_AgainstQompackStub      behaviour: implementation is a stub (Rule W-1)
```

The two skips are **expected and correct in this tree**: role A's stub `Sequitur` is still in place
here, so `grammartest`'s `/behaviour` block is still gated. The Rule W-1 message is byte-identical
to the literal `devtool lint`'s `stubskips` greps for; nothing in this branch paraphrases it.

### 2.3 Extra gates run

```
$ go test ./internal/grammar/... -count=1 -race
ok  	github.com/qompack/qompack/internal/grammar	2.835s
ok  	github.com/qompack/qompack/internal/grammar/grammartest	1.545s

$ go run ./tools/devtool fmt-check
(exit 0, no output — gofumpt clean)

$ go run -modfile=tools/pinned/go.mod github.com/golangci/golangci-lint/cmd/golangci-lint run ./internal/grammar/...
(exit 0, no findings)

$ go run ./tools/devtool lint
== golangci-lint ==  PASS after fixing one finding (see below)
== nomagic ==        PASS
== importgraph ==    importgraph: OK (65 package(s) checked)   PASS
== testdeps ==       testdeps: OK (67 package(s) checked)      PASS
== bindeps ==        PASS
== sleepcheck ==     PASS
== stubskips ==      not completed — see §5, open question 4
```

`golangci-lint` initially failed on one finding in my own new test file — `misspell` flagged the
deliberate near-miss magic `"qompack-grammer\x00"` in the bad-magic fixture. Fixed by replacing that
case with `another_artifacts_magic`, which points the decoder at internal/sketch's `QPKS` header
instead. That is a better test anyway: a mixed-up handle pointing at a different artifact under
`.qompack/` is the realistic way to meet a bad magic, and a near-miss spelling was never a case a
real reader would see.

## 3. Decisions I made

### 3.1 Codec

1. **Version width: `uint16`, little-endian.** The contract fixes the magic and says "version" but
   not its width. I matched `internal/sketch/header.go`'s QPKS frame, which already froze `uint16`
   LE for a versioned binary artifact under `.qompack/`. The header is therefore 18 bytes:
   16 magic + 2 version.
2. **Payload: varints, in `Snapshot`'s own field order** (Rules, Sequence, NextID). Signed
   (zig-zag) varints for `RuleID`/`Uses`/`Span` because those are Go `int`s — a negative value is
   not something a correct core produces, but a codec that could not represent one would round-trip
   it as a huge positive and turn a caller's bug into a corrupt-looking checkpoint.
3. **Nil and empty slices are distinguished on the wire.** A count is `0` for nil and `len+1` for a
   present slice. This is the one place I spent a byte on purpose: contract §4 asks for round-trip
   identity "for every valid s", Go distinguishes `[]Symbol(nil)` from `[]Symbol{}`, and
   `reflect.DeepEqual` (what `require.Equal` and any future checkpoint-stability assertion reduce
   to) reports them as different. Without it, a core that built an empty `Body` as `[]Symbol{}`
   would come back from a checkpoint unequal to the grammar it was saved from — a failure that only
   shows up as somebody else's flaky save/restore test.
4. **The encoder does not sort.** `Snapshot.Rules` is documented as ID-ascending; if a caller hands
   over an unsorted one, the codec preserves the order rather than silently repairing it. Sorting
   would break round-trip identity *and* launder an ordering bug in the grammar core. Determinism
   comes from there being no map anywhere in the type, which is stated in `EncodeSnapshot`'s doc.
5. **Rejections.** Bad magic, higher version, **zero version**, truncated payload, forged count,
   forged symbol length and **trailing bytes** all report a wrapped `core.ErrDegraded` and return
   the zero `Snapshot`. Forged counts are bounded by the bytes actually remaining rather than by an
   invented maximum, so nothing unbounded ever reaches `make()`.
6. **Zero version is refused** — flagged as open question 1 below.
7. **Vectors are `*.golden`, not `*.bin`.** `.gitattributes` is `* text=auto eol=lf` globally and
   already declares `*.golden -text`. These frames contain NULs, so relying on git's binary sniffing
   would leave fixture integrity to a heuristic; the `.golden` extension makes it explicit.
   Regenerate with `go test ./internal/grammar/ -run TestCodec_CompatibilityVectors -update`.

### 3.2 `StateSignature.Key()` — the domain-separation choice

**Chosen:** `core.HashBytes` under a package-local domain constant, rendered as a prefixed short
digest.

```
stateSignatureDomain = "qompack.grammar.state.v1"     // unexported, statewarn.go
Key() = "gstate_" + HashBytes(domain, goal 0x1f target 0x1f action 0x1f failure).Short()
```

Reasoning, in the order I worked it out:

- **No existing `core` domain fits.** The registry exports `DomainChunk`, `DomainRoot`,
  `DomainNegKnow`, `DomainDecision`, `DomainArgs`. `DomainNegKnow` is the closest by shape and the
  worst by meaning: it is the bloom key of an *elimination*, the one thing contract §5.1 says a
  `StateWarning` must never become, so minting warning keys under it would put the two in one
  collision space precisely where a collision reads as "this approach was already ruled out".
- **Declaring a package-local domain is the house pattern,** not an exception:
  `internal/sketch/hash.go` (3), `internal/negknow/descriptor.go` (4), `internal/daemon/ingest.go`
  (3), `internal/eval/blocks.go`, `internal/mcp/ephemeral.go`, `internal/contract/sentinel.go`,
  `internal/store/gcrun.go` and others all do it. `core`'s registry carries the *shared* domains.
- **Digest rather than a plain text join,** because the key becomes `StateWarning.DedupKey`, which is
  the natural thing for the daemon and the M6-G15-A report to log — and the fields are a goal in the
  user's own words, a repository path and a failure signature. Twelve hex characters identify the
  state exactly as well for dedup and disclose none of it.
- **`0x1f` separator, matching `negknow.Descriptor.Key`,** with the ambiguity documented rather than
  engineered away. A `Failure` containing a literal `0x1f` could in principle alias, and 48 bits is a
  truncation — but both failure modes point the safe way: colliding states share a `DedupKey`, so the
  second warning is *suppressed*. This layer errs toward silence, and a collision can only make it
  quieter. That is the opposite of `negknow`, where a collision widens a match set, and it is why the
  same construction is acceptable here without length-prefixing.

The exact digest is pinned by `TestStateSignature_Key`, which recomputes it with `crypto/sha256` and
spells the domain literal itself, so a silent change to the domain or the preimage layout fails
loudly (the `internal/canon/sketchwire_test.go` "a consumer must be able to disagree with the
producer" pattern).

### 3.3 Detector defaults

`DefaultDetectorConfig()`; the zero `DetectorConfig` normalizes to exactly these, so no caller can
get a divide-by-zero window or a threshold of one.

| Field | Default | Why that number |
|---|---|---|
| `MinRepeats` | **3** | Two occurrences is an ordinary re-check. Three identical (goal, target, action, failure) tuples with nothing observed to change is the smallest count that is hard to explain as deliberate; warning at two is how a detector earns a reputation for crying wolf. |
| `WindowTurns` | **20** | Dedup window *and* the memory bound — at most this many distinct turns can fall in one window, and windows older than the previous one are pruned. Real loops are tight, so 20 contains one comfortably; a state legitimately revisited much later lands in a new window and may be reported once more. |
| `MaxPerSession` | **3** | A warning is injected through UserPromptSubmit, i.e. spent from the context budget this plugin exists to conserve. Three is hard to miss and few enough that a mis-tuned detector cannot become the thing filling the window. A **negative** value means "none" — the in-code equivalent of the config switch being off, and it is tested. |
| `ExpiryTurns` | **5** | Production and delivery are different moments (PostToolUse vs. the next UserPromptSubmit). A warning arriving long after the session moved on is not just stale, it reads as the plugin being confused about the present. |
| `retainedWindows` | **1** (current + previous) | So an observation arriving just after a window boundary joins the run it belongs to instead of starting a fresh count. |

**Dedup key shape:** `Signature.Key() + "#" + ordinal`, where `ordinal = floor(turn / WindowTurns)`.
Joined with `#` rather than concatenated so `gstate_abc` + `"12"` and `gstate_abc1` + `"2"` cannot
alias. Floor (not truncating) division, so a negative turn cannot silently double the window width.

### 3.4 How each of the six bounds is enforced and tested

1. **Advisory only.** `StateWarning` has zero methods on both the value and pointer type, and exactly
   the eight frozen fields; `TestStateWarning_IsAdvisoryOnly` asserts both by reflection, so an
   `Enforce()` method or a `Prohibits` field fails a test rather than landing. This is also why expiry
   is `Detector.Deliverable(w, at)` and not `w.Deliverable(at)` — keeping the record inert.
2. **`ProgressObserved` suppresses entirely,** and it *latches* for the whole window: a run that moved
   once is a run that is moving, and re-arming on the next unchanged observation would report the
   tail of ordinary work as a loop. Suppression is scoped to the window, so a session that made
   progress and then genuinely got stuck can still be told. Every negative assertion has a control
   showing the same stream warns without the guard.
3. **`ProgressUnknown` implies `Uncertain` only,** latched over the window (one confident observation
   at the end cannot launder a partly-unobserved run), and an unrecognized `Progress` value is treated
   as unknown. `PartialCoverage` is a second, independent source of `Uncertain`. The uncertain case
   renders with a different `Message` — see open question 2.
4. **Dedup + cap.** One warning per `DedupKey`; a later window is a new key; `MaxPerSession` is a hard
   cap. The already-delivered ledger is *not* pruned with the windows (it is bounded by
   `MaxPerSession` by construction), so a late out-of-order observation for a pruned window cannot
   produce a second copy of a warning the user already saw.
5. **Self-suppression, two independent guards.** The explicit `Observation.SelfOriginated` flag is the
   mechanism; `IsSelfOriginated` is the backstop for the wiring change that forgets it — an ordinary
   oversight anywhere else, self-amplifying here. It scans **all four** fields (a retrieval result
   arrives as an *action*, an injected warning lands in the next turn's *goal*, plugin state appears
   as a *target*) for `mcp__qompack__`, `/qompack:`, `[qompack]` and `.qompack/`. Self-originated
   observations are excluded *before* the frequency counter, so a burst of retrieval results cannot
   show up in the report as session repetition. The closed-loop test produces a real warning, renders
   it through `FormatWarning`, feeds that text back as the next 30 turns' goal, and asserts the
   detector stays silent.
6. **Separate accounting.** `DetectorStats` counts `Observations`, `RepeatedStates` (raw
   repeated-action frequency), `Ignored`, `SelfSuppressed`, `ProgressSuppressed`, `Deduplicated`,
   `CapSuppressed`, `Expired`, `Delivered`, `Useful`, `FalseAlarms`. `RecordOutcome(dedupKey, Outcome)`
   accepts only a key this detector delivered and only the first judgement per key — an outcome that
   could be revised would make the false-alarm rate a function of who filed the last report. Feedback
   flows one way: nothing in the file reads an `Outcome` back into a decision.

### 3.5 Two smaller calls

- **No `obs.Registry` dependency.** `Stats()` is a plain copied struct. The detector holds no clock,
  no registry and does no I/O, which is what makes a replay of the same observations reproduce
  byte-identical warnings — the property the M6-G15-A report needs. Main can mirror `Stats()` into a
  registry at the composition root; grammar deliberately does not.
- **The detector is not goroutine-safe, on purpose.** One session's observations arrive in turn order
  through one composition root; a mutex would only imply that out-of-order concurrent observation is
  supported, which it is not, since every bound is defined over turns. Stated in the type's doc.

## 4. What is deliberately not here (Main's)

- `runtime.selection.loopWarningsEnabled` (contract §7). The key does not exist in `internal/config`
  yet and config is Main's file set. The detector reads no config at all;
  `DetectorConfig{MaxPerSession: -1}` is the in-code "off" and is tested.
- Wiring the detector to real observations (daemon/observer/checkpoint), and mirroring `Stats()`.
- `MarshalBinary`/`UnmarshalBinary` on the grammar core — role A's, as four-line calls through
  `EncodeSnapshot`/`DecodeSnapshot`. Their signatures are exactly as frozen and were not changed.
  Note for A: `DecodeSnapshot` returns `(Snapshot{}, err)` on failure, so `UnmarshalBinary` satisfies
  "leaves the receiver unchanged" by returning before assigning — do not assign the zero Snapshot.

## 5. Open questions for Main

1. **Zero codec version is refused.** Contract §4 phrases the accept rule as
   "version <= CodecVersion". `CodecVersion` is 1 and always has been, so no build ever wrote a 0 and
   no v0 layout exists; a zero version field is a zeroed or partially written file. Accepting it would
   mean guessing that a zeroed header is followed by a v1 payload — and a fully zeroed 21-byte file
   *is* a well-formed empty-grammar payload, so that guess turns a truncated write into exactly the
   silently-empty grammar §4 forbids. I refuse it with `ErrDegraded`. If Main reads the contract
   literally instead, it is a one-line change in `DecodeSnapshot`'s switch plus one test case flip.
2. **The uncertain warning's `Message` is new text.** FormatWarning's *template* is frozen and
   untouched; `Message` has always been per-warning ("a short, human-readable suggestion"). The
   confident case uses §14.1's own example string verbatim, `"consider a different approach"`. The
   uncertain case needs to read as a question or bound 3 is invisible to the user, so I introduced
   `"observation coverage is incomplete, so this may not be a loop"`. Rendered in full:
   `[qompack] possible loop: Bash repeated 3x (turns 0-2) - observation coverage is incomplete, so
   this may not be a loop` (with the frozen multiplication sign, en dash and em dash). Both strings
   are pinned by `TestDetector_RendersThroughFormatWarning`. **Please confirm the second string, or
   supply the wording you want.**
3. **`TestStateWarning_IsAdvisoryOnly` pins `StateWarning`'s exact field set,** which is Main's type.
   That is intentional — bound 1 is only structurally testable — but it means adding a field to
   `StateWarning` fails B's test. That is the intended tripwire, not an accident; flagging it so the
   failure is not a surprise.
4. **`devtool lint`'s `stubskips` did not complete here.** It runs `go test -json` over the entire
   tree, which exceeded my 10-minute bound; it is unrelated to this branch. Every other lint
   sub-check passes, and my package's two Rule W-1 skips print the exact literal (§2.2). Worth one
   whole-tree run at integration time.
5. **`core/hash.go`'s registry claims to list every domain in use** but does not mention negknow's
   four, daemon's three, eval's, mcp's, contract's or store's. I followed the majority pattern
   (unexported, package-local) and did not touch `core`. If Main wants the registry's completeness
   claim to be literal again, that is a `core` edit somebody owns — not mine to make.
