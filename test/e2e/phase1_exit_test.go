package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// This file is the Phase 1 exit-criterion harness (Qompack.md §10 Phase 1, plan
// V3-SP-08-observer-l0 Commit 7): the call SEQUENCE is synthesized by eval.Synthesize and the
// BYTES are real, drawn from SP-04's committed corpus under testdata/corpora/toolout/. The
// session is driven through observer.OnUserPrompt/OnToolUse in-process against a real store on
// t.TempDir(), and every ratio asserted is store.Stats().DedupRatio — §5.8's RawBytes/Bytes —
// with no other definition of the ratio anywhere in this file.

// The two session shapes the exit criterion is graded on, verbatim from the plan.
var readHeavy = eval.SynthSpec{
	Turns:          400,
	ToolMix:        map[string]float64{"Read": 0.62, "Grep": 0.14, "Glob": 0.06, "Edit": 0.10, "Bash": 0.08},
	FileRereadRate: 0.55, TestOutputNoise: 0.0,
	Changepoints: 6, Eliminations: 3, SubagentCalls: 2,
	CompactionAt: []core.TurnIndex{180, 330},
}

var testOutputHeavy = eval.SynthSpec{
	Turns:          400,
	ToolMix:        map[string]float64{"Bash": 0.55, "Read": 0.20, "Edit": 0.15, "Grep": 0.10},
	FileRereadRate: 0.25, TestOutputNoise: 0.85,
	Changepoints: 5, Eliminations: 4, SubagentCalls: 1,
	CompactionAt: []core.TurnIndex{200},
}

// The generator seeds, verbatim from the plan.
const seedReadHeavy, seedTestHeavy = 0x5108_0001, 0x5108_0002

// phase1ExitRatio is the §10 Phase 1 exit criterion: store size vs. raw transcript ≥ 4:1 on
// read-heavy sessions. It is a design number, never to be weakened here — a genuine need to move
// it is a §11.3 sign-off, not an edit.
const phase1ExitRatio = 4.0

// canonGapFloor is the operational reading of §10's "the gap on test-output-heavy sessions
// justifies O2 on its own": canonicalization must improve the test-output-heavy dedup ratio by
// ≥ 25%. It matches test/dedup's testrunnerGainFloor by design; if the two disagree, the observer
// is doing something to the bytes on the way in — that is the bug to find, not a reason to move
// either number.
const canonGapFloor = 1.25

// phase1BytesPerCallFloor is TestPhase1_ResponseBytesAreReal's floor: raw bytes per tool call
// must exceed 1 KB, proving the ratio is measured over real tool output rather than over the
// generator's {"tokens":N} envelopes.
const phase1BytesPerCallFloor = 1024

// The sketch dimensions the observer is wired with — the same shapes internal/observer's own
// suite uses (fakes_test.go), so the exploration-cardinality guard below measures the observer's
// real sketch feed, not a differently-tuned one.
const (
	phase1CMSEpsilon   = 0.01
	phase1CMSDelta     = 0.05
	phase1HLLRegisters = 512
	phase1MGCounters   = 16
)

// counterSupersededName is internal/observer's observer.superseded counter, spelled here because
// the constant is unexported there. It is bumped once per MarkSuperseded call, which is what
// makes it the "at least one MarkSuperseded" probe of TestPhase1_PathsReachTheObserver.
const counterSupersededName = "observer.superseded"

// hook event names as this package's payloads spell them.
const (
	hookUserPromptSubmit = "UserPromptSubmit"
	hookPostToolUse      = "PostToolUse"
)

// ── corpus ──────────────────────────────────────────────────────────────────────────────────────

// corpus is testdata/corpora/toolout/<group>/*.txt, keyed by the group directory name, each
// group's files in sorted filename order. The sibling <name>.txt.meta.json carries {"tool":…,
// "path":…}; only the bytes are used here — the tool name comes from the synthesized call.
type corpus map[string][][]byte

// loadCorpus reads the committed corpus once per call. The groups asserted non-empty are the ones
// the group table below routes the two specs' tools into.
func loadCorpus(t *testing.T) corpus {
	t.Helper()
	root, err := moduleRoot()
	require.NoError(t, err)
	dir := filepath.Join(root, "testdata", "corpora", "toolout")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "the SP-04 corpus must be present at %s", dir)

	c := corpus{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		files, err := filepath.Glob(filepath.Join(dir, e.Name(), "*.txt"))
		require.NoError(t, err)
		sort.Strings(files)
		for _, f := range files {
			b, err := os.ReadFile(f) //nolint:gosec // a path enumerated from this repo's own testdata tree
			require.NoError(t, err)
			c[e.Name()] = append(c[e.Name()], b)
		}
	}
	for _, group := range []string{"fileread", "grep", "glob", "bash", "testrunner", "webfetch"} {
		require.NotEmpty(t, c[group], "corpus group %q is required by the Phase 1 group table", group)
	}
	return c
}

// withBashFrom returns a shallow copy of c whose "bash" group serves group's bytes instead.
//
// ADAPTATION (recorded in the task-7 prep report): the plan's payloadFor chooses the Bash group
// per-spec — "testrunner" for testOutputHeavy and "bash" for readHeavy — but its binding
// signature payloadFor(c, tool, paths, seq) has no spec parameter, so the choice is carried by
// the corpus value instead: the test-output-heavy runs pass withBashFrom(c, "testrunner").
func withBashFrom(c corpus, group string) corpus {
	out := make(corpus, len(c))
	for k, v := range c {
		out[k] = v
	}
	out["bash"] = c[group]
	return out
}

// corpusGroupForTool is the plan's group table, extended to cover every tool name
// eval.Synthesize actually emits (an ADAPTATION recorded in the prep report): the generator's
// vocabulary includes "FileRead"/"Write" (forced re-reads, dependency writes), "Test" (the
// thrash cycle), "Task" (subagent calls) and "record_eliminated", none of which the plan's
// five-rule mapping names. File-content tools map to fileread, Test to testrunner, and anything
// else falls back to bash — deterministically, so the same call always carries the same bytes.
var corpusGroupForTool = map[string]string{
	"Read": "fileread", "FileRead": "fileread", "NotebookRead": "fileread",
	"Edit": "fileread", "MultiEdit": "fileread", "FileEdit": "fileread",
	"Write": "fileread", "FileWrite": "fileread",
	"Grep": "grep", "Glob": "glob",
	"Bash": "bash", "PowerShell": "bash",
	"WebFetch": "webfetch", "WebSearch": "webfetch",
	"Test": "testrunner",
}

// corpusFallbackGroup catches every tool the table above does not claim (Task,
// record_eliminated, and whatever the host adds tomorrow).
const corpusFallbackGroup = "bash"

// corpusPayloadFor picks the response bytes for one synthesized call, deterministically: the
// group comes from the tool name, and the file within the group is indexed by a hash of the
// call's first path — so re-reading a path re-serves the SAME bytes and FileRereadRate turns
// into real chunk reuse, while the "-v2" variants in the fileread and sp06 groups supply the
// "same file, two lines changed" case §6.1 names. A pathless call (Grep, Glob, Bash) is indexed
// by seq, cycling the whole group.
//
// For the bash and testrunner groups ONLY, the served copy gets a deterministic, seeded
// volatile-substring refresh (see refreshVolatile below): §8.1's own premise is that timestamps,
// PIDs, durations and addresses make every Bash and test-runner output unique in reality, so
// re-serving committed capture bytes byte-identical would make the canonicalization measurement
// structurally unable to show O2's value — exact dedup would win on both sides and canon would
// pay only its overhead. fileread/grep/glob/webfetch bytes stay byte-identical per path: an
// unchanged file re-read IS identical in reality.
//
// ADAPTATION: the plan names this helper payloadFor, but package e2e already has a payloadFor
// (hooks_test.go) with a different signature, so the loader keeps the plan's parameter list
// under this name — extended by seed, which the refresh ruling derives its values from.
func corpusPayloadFor(c corpus, tool string, paths []string, seq int, seed uint64) []byte {
	group, ok := corpusGroupForTool[tool]
	if !ok {
		group = corpusFallbackGroup
	}
	files := c[group]
	if len(files) == 0 {
		return nil
	}
	idx := seq
	if len(paths) > 0 && paths[0] != "" {
		h := fnv.New32a()
		_, _ = h.Write([]byte(paths[0]))
		idx = int(h.Sum32() % uint32(len(files)))
	}
	if idx < 0 {
		idx = -idx
	}
	b := files[idx%len(files)]
	if group == "bash" || group == "testrunner" {
		return refreshVolatile(b, seed, seq)
	}
	return b
}

// ── volatile refresh (controller ruling on the canonicalization gate) ───────────────────────────
//
// The committed toolout corpus is a set of CAPTURES: each file's timestamps, PIDs, run durations
// and addresses are frozen at capture time. Re-serving those bytes byte-identical for every
// synthesized Bash/test-runner call makes ratioOn ≈ ratioOff by construction — the store's exact
// chunk dedup collapses identical payloads with canonicalization on OR off, so canonicalization
// can only ever pay its bookkeeping overhead and the ≥1.25× gap is unmeasurable no matter what
// internal/observer does. That is a harness artifact, not an observer property: §8.1's premise is
// that in reality every Bash and test-runner invocation prints fresh timestamps, PIDs, durations
// and addresses, which is exactly what canonicalization (O2) exists to strip.
//
// refreshVolatile restores that reality, honestly and narrowly: it rewrites, in the served copy
// only, substrings of EXACTLY the classes the configured strip set (config.Defaults():
// timestamps, pids, addresses, durations) canonicalizes — wall-clock hh:mm:ss (bare and inside
// ISO-8601 date-times), `pid=NNN`-style process ids, `goroutine NNN [` ids, `N.NNs`/`NNNms` run
// durations, and 0x…/@… hex addresses — with values derived from (seed, seq). Every replacement
// preserves the matched span's byte length and shape, so the canon scanners match the refreshed
// span exactly as they matched the original: canonicalization-on collapses all variants of a
// corpus file to one canonical form (plus per-variant deltas), while canonicalization-off must
// store every variant's dirtied chunks. Substituted digit runs are kept ≥ 3 bytes where the canon
// token is 3 bytes, so no refreshed span is ever dropped by canon's no-growth guard.
//
// The function is pure: the same (input bytes, seed, seq) always yields the same output, so the
// canon-on and canon-off runs of a spec ingest byte-identical event streams and RawBytes agree on
// both sides. No committed corpus file is modified and internal/eval is untouched.
var (
	// volClock matches a wall clock hh:mm:ss — bare or as the time-of-day inside an ISO-8601
	// date-time — the shape numeric.go's numClock/numISO scanners strip under ClassTimestamps.
	volClock = regexp.MustCompile(`\b\d{2}:\d{2}:\d{2}\b`)
	// volDur matches fractional run durations (`0.05s`, `1.203 s`, `12.5ms`) and ≥2-digit
	// millisecond magnitudes (`250ms`), the shapes numeric.go strips under ClassDurations. The
	// fractional form is ≥4 bytes and the ms form ≥4 bytes, both clear of the 3-byte token guard.
	volDur = regexp.MustCompile(`\b(?:\d+\.\d{1,3}[ \t]?|\d{2,7})m?s\b`)
	// volPID matches the `pid=NNN` / `PID: NNN` / `pid NNN` spellings generic.go strips under
	// ClassPIDs; ≥3 digits so the refreshed span survives the no-growth guard.
	volPID = regexp.MustCompile(`(?i)\bpid[=: ][ \t]?\d{3,7}\b`)
	// volGoroutine matches the Go panic `goroutine NNN [` id tools.go strips under ClassPIDs.
	volGoroutine = regexp.MustCompile(`\bgoroutine \d{3,6} \[`)
	// volHex matches the 0x… and @… hex-address shapes generic.go strips under ClassAddresses.
	volHex = regexp.MustCompile(`\b0x[0-9a-fA-F]{6,16}\b`)
	volAt  = regexp.MustCompile(`@[0-9a-f]{6,8}\b`)
)

// volRNG is a splitmix64 stream over (seed, seq): tiny, dependency-free and deterministic.
type volRNG struct{ x uint64 }

func newVolRNG(seed uint64, seq int) *volRNG {
	return &volRNG{x: seed ^ (uint64(seq)+1)*0x9E3779B97F4A7C15} //nolint:gosec // seq is a small non-negative event counter
}

func (r *volRNG) next() uint64 {
	r.x += 0x9E3779B97F4A7C15
	z := r.x
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	z ^= z >> 31
	return z
}

func refreshVolatile(b []byte, seed uint64, seq int) []byte {
	rng := newVolRNG(seed, seq)
	out := append([]byte(nil), b...)

	// Decimal digits are substituted in place, one derived digit per original digit, so every
	// span keeps its exact length and digit count.
	substDigits := func(m []byte) []byte {
		n := append([]byte(nil), m...)
		for i, c := range n {
			if c >= '0' && c <= '9' {
				n[i] = byte('0' + rng.next()%10)
			}
		}
		return n
	}
	out = volDur.ReplaceAllFunc(out, substDigits)
	out = volPID.ReplaceAllFunc(out, substDigits)
	out = volGoroutine.ReplaceAllFunc(out, substDigits)

	// A clock keeps hh<24, mm<60, ss<60 so the refreshed value is still a clock to any scanner
	// that validates field ranges.
	out = volClock.ReplaceAllFunc(out, func(m []byte) []byte {
		return []byte(fmt.Sprintf("%02d:%02d:%02d", rng.next()%24, rng.next()%60, rng.next()%60))
	})

	// Hex addresses keep their prefix and length; derived digits are lowercase hex, which both
	// address shapes accept.
	substHex := func(skip int) func(m []byte) []byte {
		return func(m []byte) []byte {
			n := append([]byte(nil), m...)
			for i := skip; i < len(n); i++ {
				n[i] = "0123456789abcdef"[rng.next()%16]
			}
			return n
		}
	}
	out = volHex.ReplaceAllFunc(out, substHex(len("0x")))
	out = volAt.ReplaceAllFunc(out, substHex(len("@")))
	return out
}

// ── event materialization ───────────────────────────────────────────────────────────────────────

// eventsFor turns an eval.Session — a turn list, not a hook stream — into the hook events the
// observer would have seen: one UserPromptSubmit per user turn, one PostToolUse per ToolCall.
//
// ToolInput is built from tc.Paths — eval.ToolCall keeps the paths in their own field and leaves
// Args nil for ordinary calls, so without this the observer would see no path at all and
// PathsFromInput would return nothing (which is exactly how the previous eventsFor failed).
//
// The refresh seed is derived from the session ID, so a session's event stream is a pure
// function of (Synthesize seed, spec) — identical across the canon-on and canon-off runs.
func eventsFor(s eval.Session, c corpus) []hookio.Event {
	sh := fnv.New64a()
	_, _ = sh.Write([]byte(s.ID))
	seed := sh.Sum64()

	var out []hookio.Event
	seq := 0
	for _, t := range s.Turns {
		if t.Role == "user" {
			out = append(out, hookio.Event{
				HookEventName: hookUserPromptSubmit,
				SessionID:     core.SessionID(s.ID), Prompt: t.Text,
			})
			continue
		}
		for _, tc := range t.ToolCalls {
			body := corpusPayloadFor(c, tc.Name, tc.Paths, seq, seed)
			seq++
			out = append(out, hookio.Event{
				HookEventName: hookPostToolUse,
				SessionID:     core.SessionID(s.ID), ToolName: tc.Name, ToolUseID: tc.ID,
				ToolInput:    toolInputFor(tc),
				ToolResponse: mustJSON(map[string]string{"content": string(body)}),
			})
		}
	}
	return out
}

// toolInputFor shapes one synthesized call's tool_input the way the host would have: file tools
// carry {"file_path":…} — the key PathsFromInput reads for Read/Edit/Write — Grep and Glob carry
// {"pattern":…,"path":…}, Bash carries {"command":…} with no path, and a call with its own Args
// (record_eliminated) keeps them. The point is only that a synthesized call arrives at OnToolUse
// looking like the hook payload it stands for.
func toolInputFor(tc eval.ToolCall) json.RawMessage {
	switch tc.Name {
	case "Grep", "Glob":
		in := map[string]string{"pattern": "handler"}
		if len(tc.Paths) > 0 {
			in["path"] = tc.Paths[0]
		}
		return mustJSON(in)
	case "Bash", "PowerShell":
		return mustJSON(map[string]string{"command": "go test ./..."})
	}
	if len(tc.Paths) > 0 {
		return mustJSON(map[string]string{"file_path": tc.Paths[0]})
	}
	if len(tc.Args) > 0 {
		return tc.Args
	}
	return json.RawMessage(`{}`)
}

// mustJSON marshals v or panics; every value it is handed here is a map of strings.
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ── driving the observer ────────────────────────────────────────────────────────────────────────

// phase1Run is everything one driven session leaves behind that the seven tests read.
type phase1Run struct {
	Stats store.Stats
	// FirstHalfBytes / SecondHalfBytes split Stats.Bytes at the event-stream midpoint, for the
	// §11.3 sublinear-growth guardrail.
	FirstHalfBytes  int64
	SecondHalfBytes int64
	// Superseded is the observer.superseded counter: MarkSuperseded calls that actually happened.
	Superseded int64
	// ExploreCardinality is the HLL's estimate after the run: the observer's sketch feed, live.
	ExploreCardinality uint64
	// ToolCalls is how many PostToolUse events were driven.
	ToolCalls int
	// FileVersioned reports whether at least one driven path has a non-empty FileHistory — the
	// AppendFileVersion probe.
	FileVersioned bool
}

// phase1Cache memoizes driven sessions per (seed, canon, bash-group) so the seven tests share
// four store runs instead of re-ingesting 400 turns each.
var (
	phase1Mu    sync.Mutex
	phase1Cache = map[string]*phase1Run{}
)

func phase1Session(t *testing.T, seed int64, spec eval.SynthSpec, canonOn, bashFromTestrunner bool) *phase1Run {
	t.Helper()
	key := fmt.Sprintf("%x-canon=%t-testrunner=%t", seed, canonOn, bashFromTestrunner)
	phase1Mu.Lock()
	defer phase1Mu.Unlock()
	if r, ok := phase1Cache[key]; ok {
		return r
	}
	c := loadCorpus(t)
	if bashFromTestrunner {
		c = withBashFrom(c, "testrunner")
	}
	r := drivePhase1Session(t, eval.Synthesize(seed, spec), canonOn, c)
	phase1Cache[key] = r
	return r
}

// drivePhase1Session materializes s's hook events and drives them, in order, through a real
// observer over a real store.Open on t.TempDir(), with a FakeClock advancing one second per
// event. It closes the store before returning, so t.TempDir's cleanup never races an open handle.
func drivePhase1Session(t *testing.T, s eval.Session, canonOn bool, c corpus) *phase1Run {
	t.Helper()
	ctx := context.Background()

	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Store.Canonicalize.Enabled = canonOn

	clk := testutil.NewFakeClock(testutil.Epoch)
	log := logging.Nop()

	st, err := store.Open(root, cfg, store.Deps{Log: log, Clock: clk})
	require.NoError(t, err)
	closed := false
	defer func() {
		if !closed {
			_ = st.Close()
		}
	}()

	g, err := dag.Open(root, cfg, log)
	require.NoError(t, err)

	metrics := obs.New(clk)
	explore := sketch.NewHLL(phase1HLLRegisters)
	obsv, err := observer.New(observer.Options{
		ProjectRoot: root, Cfg: cfg, Store: st, Graph: g,
		Touch:   sketch.NewCMS(phase1CMSEpsilon, phase1CMSDelta),
		Explore: explore,
		Hot:     sketch.NewMisraGries(phase1MGCounters),
		Log:     log, Metrics: metrics, Clock: clk,
	})
	require.NoError(t, err)

	events := eventsFor(s, c)
	require.NotEmpty(t, events, "session %s produced no events", s.ID)

	toolCalls := 0
	half := len(events) / 2
	var firstHalfBytes int64
	for i, e := range events {
		clk.Advance(time.Second)
		switch e.HookEventName {
		case hookUserPromptSubmit:
			_, err := obsv.OnUserPrompt(ctx, e)
			require.NoError(t, err)
		case hookPostToolUse:
			toolCalls++
			_, err := obsv.OnToolUse(ctx, e)
			require.NoError(t, err)
		default:
			t.Fatalf("eventsFor emitted an unexpected hook event %q", e.HookEventName)
		}
		if i == half-1 {
			mid, err := st.Stats(ctx)
			require.NoError(t, err)
			firstHalfBytes = mid.Bytes
		}
	}

	require.NoError(t, g.Flush(ctx))
	require.NoError(t, st.Flush(ctx))
	stats, err := st.Stats(ctx)
	require.NoError(t, err)

	// The AppendFileVersion probe: at least one path the session touched must have a version
	// history, looked up under the same paths.Norm+Key spelling tooluse.go stores it under.
	fileVersioned := false
scan:
	for _, turn := range s.Turns {
		for _, tc := range turn.ToolCalls {
			if len(tc.Paths) == 0 || tc.Paths[0] == "" {
				continue
			}
			n, err := paths.Norm(root, tc.Paths[0])
			if err != nil {
				continue
			}
			if hist, err := st.FileHistory(ctx, paths.Key(n)); err == nil && len(hist) > 0 {
				fileVersioned = true
				break scan
			}
		}
	}

	superseded := metrics.Snapshot().Counters[counterSupersededName]

	closed = true
	require.NoError(t, st.Close())

	return &phase1Run{
		Stats:          stats,
		FirstHalfBytes: firstHalfBytes, SecondHalfBytes: stats.Bytes - firstHalfBytes,
		Superseded: superseded, ExploreCardinality: explore.Cardinality(),
		ToolCalls: toolCalls, FileVersioned: fileVersioned,
	}
}

// ── the report artifact ─────────────────────────────────────────────────────────────────────────

// phase1Report is the JSON artifact TestPhase1_CanonicalizationGapOnTestOutput writes and
// TestPhase1_ReportArtifact asserts the key set of. The four ratios are Stats().DedupRatio of
// their runs; the size fields describe the read-heavy canonicalization-on run — the run the §10
// exit criterion is graded on.
type phase1Report struct {
	ReadHeavyRatioCanon float64 `json:"read_heavy_ratio_canon"`
	ReadHeavyRatioRaw   float64 `json:"read_heavy_ratio_raw"`
	TestHeavyRatioCanon float64 `json:"test_heavy_ratio_canon"`
	TestHeavyRatioRaw   float64 `json:"test_heavy_ratio_raw"`
	RawBytes            int64   `json:"raw_bytes"`
	StoreBytes          int64   `json:"store_bytes"`
	Objects             int     `json:"objects"`
	ToolUses            int     `json:"tool_uses"`
}

func buildPhase1Report(t *testing.T) phase1Report {
	t.Helper()
	rhOn := phase1Session(t, seedReadHeavy, readHeavy, true, false)
	rhOff := phase1Session(t, seedReadHeavy, readHeavy, false, false)
	thOn := phase1Session(t, seedTestHeavy, testOutputHeavy, true, true)
	thOff := phase1Session(t, seedTestHeavy, testOutputHeavy, false, true)
	return phase1Report{
		ReadHeavyRatioCanon: rhOn.Stats.DedupRatio,
		ReadHeavyRatioRaw:   rhOff.Stats.DedupRatio,
		TestHeavyRatioCanon: thOn.Stats.DedupRatio,
		TestHeavyRatioRaw:   thOff.Stats.DedupRatio,
		RawBytes:            rhOn.Stats.RawBytes,
		StoreBytes:          rhOn.Stats.Bytes,
		Objects:             rhOn.Stats.Objects,
		ToolUses:            rhOn.Stats.ToolUses,
	}
}

// ── the seven Phase 1 rows ──────────────────────────────────────────────────────────────────────

// TestPhase1_DedupRatioReadHeavy is the §10 Phase 1 exit criterion: every ToolCall of
// eval.Synthesize(seedReadHeavy, readHeavy), carrying testdata/corpora/toolout/ bytes, driven
// through observer.OnToolUse against a real store with canonicalization on, must dedup at ≥ 4:1.
func TestPhase1_DedupRatioReadHeavy(t *testing.T) {
	r := phase1Session(t, seedReadHeavy, readHeavy, true, false)
	t.Logf("read-heavy canon: raw=%d stored=%d objects=%d ratio=%.4f",
		r.Stats.RawBytes, r.Stats.Bytes, r.Stats.Objects, r.Stats.DedupRatio)
	require.GreaterOrEqual(t, r.Stats.DedupRatio, phase1ExitRatio,
		"§10 Phase 1 exit criterion: store size vs. raw transcript must dedup at ≥ %.1f:1 on read-heavy sessions",
		phase1ExitRatio)
}

// TestPhase1_CanonicalizationGapOnTestOutput runs testOutputHeavy twice — canonicalization on
// and off — and asserts ratioOn ≥ ratioOff·1.25. Both raw numbers are printed and written to
// phase1-dedup.json in the test's temp dir regardless of pass/fail.
func TestPhase1_CanonicalizationGapOnTestOutput(t *testing.T) {
	on := phase1Session(t, seedTestHeavy, testOutputHeavy, true, true)
	off := phase1Session(t, seedTestHeavy, testOutputHeavy, false, true)

	rep := buildPhase1Report(t)
	encoded, err := json.MarshalIndent(rep, "", "  ")
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "phase1-dedup.json")
	require.NoError(t, os.WriteFile(out, append(encoded, '\n'), 0o600))

	t.Logf("test-heavy canon on:  raw=%d stored=%d ratio=%.4f", on.Stats.RawBytes, on.Stats.Bytes, on.Stats.DedupRatio)
	t.Logf("test-heavy canon off: raw=%d stored=%d ratio=%.4f", off.Stats.RawBytes, off.Stats.Bytes, off.Stats.DedupRatio)
	t.Logf("gap=%.4f (floor %.2f); report written to %s", on.Stats.DedupRatio/off.Stats.DedupRatio, canonGapFloor, out)
	t.Logf("phase1 report JSON: %s", encoded)

	require.GreaterOrEqual(t, on.Stats.DedupRatio, off.Stats.DedupRatio*canonGapFloor,
		"§10: the canonicalization gap on test-output-heavy sessions must be ≥ %.2fx — it is what justifies O2 on its own",
		canonGapFloor)
}

// TestPhase1_PathsReachTheObserver guards against a harness that silently stops exercising
// path-keyed behaviour — which is exactly how the previous eventsFor failed: Stats().Files,
// a real FileHistory entry (AppendFileVersion), the observer.superseded counter (MarkSuperseded)
// and the exploration HLL must all be non-zero after the read-heavy run.
func TestPhase1_PathsReachTheObserver(t *testing.T) {
	r := phase1Session(t, seedReadHeavy, readHeavy, true, false)
	t.Logf("files=%d superseded=%d hll=%d fileVersioned=%t",
		r.Stats.Files, r.Superseded, r.ExploreCardinality, r.FileVersioned)
	require.Positive(t, r.Stats.Files, "no path acquired a file entry: ToolInput is not carrying tc.Paths")
	require.True(t, r.FileVersioned, "no AppendFileVersion happened: file-content results are not path-keyed")
	require.Positive(t, r.Superseded, "no MarkSuperseded happened: re-reads are not superseding priors")
	require.Positive(t, r.ExploreCardinality, "the exploration HLL saw nothing: the sketch feed is dead")
}

// TestPhase1_ResponseBytesAreReal asserts Stats().RawBytes per tool call exceeds 1 KB, so the
// ratio is measured over real tool output rather than over {"tokens":N} envelopes.
func TestPhase1_ResponseBytesAreReal(t *testing.T) {
	r := phase1Session(t, seedReadHeavy, readHeavy, true, false)
	require.Positive(t, r.ToolCalls)
	perCall := float64(r.Stats.RawBytes) / float64(r.ToolCalls)
	t.Logf("raw=%d toolCalls=%d bytes/call=%.0f", r.Stats.RawBytes, r.ToolCalls, perCall)
	require.Greater(t, perCall, float64(phase1BytesPerCallFloor),
		"per-call raw bytes are token-envelope-sized: the corpus bytes are not reaching the observer")
}

// TestPhase1_ReportArtifact asserts the emitted JSON carries exactly the key set the plan names.
func TestPhase1_ReportArtifact(t *testing.T) {
	rep := buildPhase1Report(t)
	encoded, err := json.Marshal(rep)
	require.NoError(t, err)

	var keys map[string]any
	require.NoError(t, json.Unmarshal(encoded, &keys))
	for _, k := range []string{
		"read_heavy_ratio_canon", "read_heavy_ratio_raw",
		"test_heavy_ratio_canon", "test_heavy_ratio_raw",
		"raw_bytes", "store_bytes", "objects", "tool_uses",
	} {
		require.Contains(t, keys, k)
	}
}

// TestPhase1_StoreGrowthSublinear is §11.3's "store growth sublinear in session length after
// dedup", in its bluntest measurable form: the bytes stored over the second half of the
// read-heavy session are strictly less than over the first half.
func TestPhase1_StoreGrowthSublinear(t *testing.T) {
	r := phase1Session(t, seedReadHeavy, readHeavy, true, false)
	t.Logf("firstHalf=%d secondHalf=%d", r.FirstHalfBytes, r.SecondHalfBytes)
	require.Positive(t, r.FirstHalfBytes, "the first half stored nothing: the harness is not ingesting")
	require.Less(t, r.SecondHalfBytes, r.FirstHalfBytes,
		"§11.3: store growth must be sublinear in session length after dedup")
}

// TestPhase1_CorpusSweep replays every committed synthetic session through the same eventsFor —
// so its calls also carry corpus bytes — and logs each ratio. It is informational: it fails only
// if a session panics the pipeline (a panic fails the test on its own).
func TestPhase1_CorpusSweep(t *testing.T) {
	root, err := moduleRoot()
	require.NoError(t, err)
	dir := filepath.Join(root, "testdata", "sessions", "synthetic")
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("no synthetic session corpus at %s: %v", dir, err)
	}

	sessions, err := eval.New(eval.Options{}).Load(dir)
	require.NoError(t, err)

	c := loadCorpus(t)
	for _, s := range sessions {
		r := drivePhase1Session(t, s, true, c)
		t.Logf("%-24s turns=%4d toolCalls=%4d raw=%9d stored=%8d ratio=%.4f",
			s.ID, len(s.Turns), r.ToolCalls, r.Stats.RawBytes, r.Stats.Bytes, r.Stats.DedupRatio)
	}
}

// TestPhase1_HotPathBudgetDocumented asserts the committed ADR records a B-A p99 figure. The
// gate itself is CI's bench-gate job; this test only pins that the measured number was written
// down. A local (Windows) figure satisfies it, with the CI platforms' figures allowed to be
// marked pending until CI has run on the branch — so it asserts a B-A p99 line and at least one
// millisecond figure, not three platform rows.
//
// The ADR is Commit 7's own deliverable: this test is RED until docs/adr/0008-observer-l0.md
// lands on the branch, which is the TDD ordering the plan prescribes for a gate commit.
func TestPhase1_HotPathBudgetDocumented(t *testing.T) {
	root, err := moduleRoot()
	require.NoError(t, err)
	adr := filepath.Join(root, "docs", "adr", "0008-observer-l0.md")

	b, err := os.ReadFile(adr) //nolint:gosec // a fixed path inside this repository
	require.NoError(t, err, "docs/adr/0008-observer-l0.md must exist: it is Commit 7's ADR and records the measured numbers")
	text := string(b)

	require.Regexp(t, regexp.MustCompile(`(?is)B-A.{0,200}?p99|p99.{0,200}?B-A`), text,
		"the ADR must record the hot-path B-A p99 figure (§8.1 performance budget)")
	require.Regexp(t, regexp.MustCompile(`(?i)\d+(\.\d+)?\s*ms`), text,
		"the ADR must carry at least one measured millisecond figure for the hot path")
	if regexp.MustCompile(`(?i)pending`).MatchString(text) {
		t.Logf("ADR marks one or more CI platform figures as pending — acceptable until CI has run on this branch")
	}
}
