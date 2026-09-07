package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/stretchr/testify/require"
	"pgregory.net/rapid"
)

// The expansion Promoter (Qompack.md §8.7, third bullet): "repeated expansion of the same hash
// within a session is a signal, not a cost".
//
// Two properties carry the whole mechanism and both are asserted from the outside, through the
// Promoter interface, rather than by reading promoter.st. The COUNT is what SP-16 thresholds on,
// and it must survive a process exit — the process that observes the expansions is not the one
// that writes the next checkpoint. The promoted LIST is what SP-16 actually consumes, and it must
// gain each hash exactly once no matter how many times the count crosses the line, because a list
// with duplicates would weight one hash twice in the pointer tier.
//
// The failure direction is asserted as loudly as the success one. §12.3's doctrine is to fail
// toward doing nothing, so an unreadable state file resets the counts and says so Loud rather than
// refusing to run — and the corrupt bytes are kept, because they are the only evidence of whatever
// wrote them.

// propPromoterCalls bounds one property iteration's call sequence. Every NoteExpansion writes
// promotions.json atomically, so the bound is a running-time budget rather than a coverage claim:
// eight calls over two sessions and three hashes already reaches every count/threshold relation.
const propPromoterCalls = 8

// promoteLoudLogger is a logging.Logger that remembers what was said Loud.
//
// §12.3 requires the quarantine to be loud, and "loud" is a testable claim about a specific call,
// not about a log file: logging.Nop() would satisfy the interface while proving nothing, and
// attaching a process-wide Loud observer would make two parallel tests observe each other.
type promoteLoudLogger struct {
	mu    sync.Mutex
	louds []string
	warns []string
}

// With returns the same recorder: fields are irrelevant to what these tests assert, and returning
// a copy would lose the calls a derived logger makes.
func (l *promoteLoudLogger) With(...any) logging.Logger { return l }

// Debug discards, as logging.Nop() would.
func (l *promoteLoudLogger) Debug(string, ...any) {}

// Info discards, as logging.Nop() would.
func (l *promoteLoudLogger) Info(string, ...any) {}

// Warn records the message, so a test can tell a swallowed warning from silence.
func (l *promoteLoudLogger) Warn(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

// Error discards: nothing in this file's paths reports at Error.
func (l *promoteLoudLogger) Error(string, ...any) {}

// Loud records the message. This is the one method these tests exist to observe.
func (l *promoteLoudLogger) Loud(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.louds = append(l.louds, msg)
}

// loud returns a copy of the recorded Loud messages, taken under the lock so the concurrency test
// can read it safely.
func (l *promoteLoudLogger) loud() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.louds...)
}

// promoteLoudLogger must satisfy the seam PromoterOptions declares.
var _ logging.Logger = (*promoteLoudLogger)(nil)

// promoterHash mints a distinct, stable core.Hash from a label, so a failure names which hash it
// was rather than printing sixty-four indistinguishable hex characters.
func promoterHash(label string) core.Hash {
	return core.HashBytes("qompack.test.promoter", []byte(label))
}

// newTestPromoter opens a promoter over a fresh temporary state file, returning it alongside the
// path and the recorder its diagnostics land in.
func newTestPromoter(t *testing.T, threshold int) (Promoter, string, *promoteLoudLogger) {
	t.Helper()

	root := newFixtureRoot(t, nil)
	path := PromotionsPath(root)
	log := &promoteLoudLogger{}
	p, err := NewPromoterWithOptions(PromoterOptions{
		StatePath: path, Threshold: threshold, Clock: newFakeClock(epoch), Log: log,
	})
	require.NoError(t, err, "NewPromoterWithOptions(%s)", path)
	return p, path, log
}

// TestNoteExpansionPromotesAtThreshold asserts the edge itself: the first expansion counts and
// does not promote, the second reaches the threshold and does.
func TestNoteExpansionPromotesAtThreshold(t *testing.T) {
	p, _, _ := newTestPromoter(t, 2)
	h := promoterHash("threshold")

	count, promoted, err := p.NoteExpansion(t.Context(), testSession, h)
	require.NoError(t, err, "first NoteExpansion")
	require.Equal(t, 1, count, "the first expansion counts one")
	require.False(t, promoted, "one expansion below a threshold of two must not promote")

	count, promoted, err = p.NoteExpansion(t.Context(), testSession, h)
	require.NoError(t, err, "second NoteExpansion")
	require.Equal(t, 2, count, "the second expansion counts two")
	require.True(t, promoted, "reaching the threshold must promote")

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Equal(t, []core.Hash{h}, got, "the promoted list must name the hash exactly once")
}

// TestNoteExpansionStaysPromotedAfterThreshold asserts promoted is `count >= threshold`, not
// `count == threshold`.
//
// The caller is reporting the STATE of a hash, not an edge: a third expansion of an
// already-promoted hash is still an expansion of a promoted hash, and a model that read
// promoted:false there would conclude the promotion had been undone. The list, by contrast, gains
// its entry exactly once.
func TestNoteExpansionStaysPromotedAfterThreshold(t *testing.T) {
	p, _, _ := newTestPromoter(t, 2)
	h := promoterHash("sticky")

	for i := 1; i <= 2; i++ {
		_, _, err := p.NoteExpansion(t.Context(), testSession, h)
		require.NoError(t, err, "NoteExpansion %d", i)
	}

	count, promoted, err := p.NoteExpansion(t.Context(), testSession, h)
	require.NoError(t, err, "third NoteExpansion")
	require.Equal(t, 3, count, "the third expansion counts three")
	require.True(t, promoted, "a hash past its threshold stays promoted")

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Len(t, got, 1, "the promoted list gains an entry exactly once, not once per expansion")
}

// TestPromotedListIsOrderedAndDeduped asserts the list SP-16 reads is the promotion order, with
// each hash present once.
//
// Order is contract, not incidental: SP-16 folds the list into the next checkpoint's pointer tier
// in sequence, so a re-ordered list silently re-weights the rehydration budget.
func TestPromotedListIsOrderedAndDeduped(t *testing.T) {
	p, _, _ := newTestPromoter(t, 2)
	a, b := promoterHash("A"), promoterHash("B")

	for _, h := range []core.Hash{a, a, b, b, a} {
		_, _, err := p.NoteExpansion(t.Context(), testSession, h)
		require.NoError(t, err, "NoteExpansion(%s)", h.Short())
	}

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Equal(t, []core.Hash{a, b}, got,
		"A promoted before B, and A's third expansion must not append it a second time")
}

// TestPromoterStatePersistsAcrossRestart asserts the reason the counts are on disk at all: the
// daemon may exit on idle between the expansion that produced the signal and the checkpoint that
// consumes it.
func TestPromoterStatePersistsAcrossRestart(t *testing.T) {
	p, path, _ := newTestPromoter(t, 2)
	h := promoterHash("restart")

	for i := 1; i <= 2; i++ {
		_, _, err := p.NoteExpansion(t.Context(), testSession, h)
		require.NoError(t, err, "NoteExpansion %d", i)
	}

	reopened, err := NewPromoter(path, 2, newFakeClock(epoch))
	require.NoError(t, err, "reopening %s", path)

	got, err := reopened.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted after restart")
	require.Equal(t, []core.Hash{h}, got, "the promoted list must survive a restart")

	count, promoted, err := reopened.NoteExpansion(t.Context(), testSession, h)
	require.NoError(t, err, "NoteExpansion after restart")
	require.Equal(t, 3, count, "the count must resume from disk, not restart at one")
	require.True(t, promoted, "a hash promoted before the restart is still promoted after it")
}

// requireQuarantined asserts exactly one quarantined copy of the corrupt bytes exists under
// .qompack/tmp/quarantine/, and returns its path.
//
// The bytes are compared rather than merely counted because the whole point of keeping them is
// evidence: a quarantine that wrote an empty file would satisfy every structural assertion and
// preserve nothing.
func requireQuarantined(t *testing.T, root string, want []byte) string {
	t.Helper()

	dir := filepath.Join(paths.Of(root).Tmp, "quarantine")
	entries, err := os.ReadDir(paths.Long(dir))
	require.NoError(t, err, "reading %s: a corrupt state file must be kept, not discarded", dir)
	require.Len(t, entries, 1, "exactly one quarantined copy is expected in %s", dir)

	got, err := paths.ReadFileShared(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err, "reading the quarantined copy")
	require.Equal(t, string(want), string(got), "the quarantined copy must be the original bytes")
	return filepath.Join(dir, entries[0].Name())
}

// TestPromoterCorruptStateQuarantined asserts §12.3's fail-toward-doing-nothing: unparseable state
// resets the counts, keeps the bytes, and says so Loud.
func TestPromoterCorruptStateQuarantined(t *testing.T) {
	root := newFixtureRoot(t, nil)
	path := PromotionsPath(root)
	garbage := []byte("{ this is not json at all\n")
	require.NoError(t, paths.WriteAtomic(path, garbage, promStatePerm), "seeding a corrupt %s", path)

	log := &promoteLoudLogger{}
	p, err := NewPromoterWithOptions(PromoterOptions{
		StatePath: path, Threshold: 2, Clock: newFakeClock(epoch), Log: log,
	})
	require.NoError(t, err, "a corrupt state file must not stop the promoter from opening")

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Empty(t, got, "a quarantined state must come back empty, not partially parsed")

	count, promoted, err := p.NoteExpansion(t.Context(), testSession, promoterHash("after-corrupt"))
	require.NoError(t, err, "NoteExpansion after a quarantine")
	require.Equal(t, 1, count, "counting must start again from one")
	require.False(t, promoted, "nothing is promoted by the first expansion at a threshold of two")

	requireQuarantined(t, root, garbage)
	require.Len(t, log.loud(), 1, "a quarantine is exactly one Loud line (§12.3), got %v", log.loud())
}

// TestPromoterQuarantineIsNotRepeatedOnEveryRestart asserts the corrupt file is REPLACED, not
// merely copied aside.
//
// The distinction only shows up across restarts, which is why it needs its own test. quarantine
// writes the bad bytes to tmp/quarantine/ and leaves promotions.json alone; if the state were then
// rewritten only by the first NoteExpansion, a session that quarantined and never expanded anything
// would leave the same bad bytes for the next process to find — one identical copy and one Loud
// line per start, for a fault already reported once. Reading the file back and finding valid JSON
// is what proves the replacement happened at load time.
func TestPromoterQuarantineIsNotRepeatedOnEveryRestart(t *testing.T) {
	root := newFixtureRoot(t, nil)
	path := PromotionsPath(root)
	garbage := []byte("{ this is not json at all\n")
	require.NoError(t, paths.WriteAtomic(path, garbage, promStatePerm), "seeding a corrupt %s", path)

	log := &promoteLoudLogger{}
	clk := newFakeClock(epoch)
	const restarts = 3
	for i := 0; i < restarts; i++ {
		_, err := NewPromoterWithOptions(PromoterOptions{
			StatePath: path, Threshold: 2, Clock: clk, Log: log,
		})
		require.NoError(t, err, "open %d must succeed on a quarantined state", i)
		// Advanced between opens so a second quarantine would land on a DIFFERENT filename and
		// therefore be visible as a second entry rather than silently overwriting the first.
		clk.Advance(time.Second)
	}

	require.Len(t, log.loud(), 1,
		"a corrupt state file is reported once, not once per restart; got %v", log.loud())
	requireQuarantined(t, root, garbage)

	// The file on disk is now a valid, empty state — which is what stops the next start from
	// treating it as corrupt all over again.
	back, err := paths.ReadFileShared(path)
	require.NoError(t, err, "promotions.json must be replaced, not deleted")
	var st promState
	require.NoError(t, json.Unmarshal(back, &st), "the replacement must be valid JSON: %s", back)
	require.Equal(t, promStateVersion, st.Version, "the replacement carries this build's version")
	require.Empty(t, st.Sessions, "the replacement starts with no counts")
}

// TestPromoterWrongVersionQuarantined asserts a well-formed state this build cannot interpret is
// treated exactly like garbage.
//
// A version mismatch is the MORE dangerous of the two: the JSON parses, so a build that merely
// checked json.Unmarshal would read another schema's fields as its own and carry forward counts
// that mean something else.
func TestPromoterWrongVersionQuarantined(t *testing.T) {
	root := newFixtureRoot(t, nil)
	path := PromotionsPath(root)

	wrong, err := json.Marshal(promState{
		Version:   promStateVersion + 1,
		Threshold: 2,
		Sessions: map[string]*promSession{
			string(testSession): {Counts: map[string]int{promoterHash("ghost").String(): 9}},
		},
	})
	require.NoError(t, err, "marshalling a wrong-version state")
	require.NoError(t, paths.WriteAtomic(path, wrong, promStatePerm), "seeding %s", path)

	log := &promoteLoudLogger{}
	p, err := NewPromoterWithOptions(PromoterOptions{
		StatePath: path, Threshold: 2, Clock: newFakeClock(epoch), Log: log,
	})
	require.NoError(t, err, "a wrong-version state file must not stop the promoter from opening")

	count, _, err := p.NoteExpansion(t.Context(), testSession, promoterHash("ghost"))
	require.NoError(t, err, "NoteExpansion after a version quarantine")
	require.Equal(t, 1, count, "a count from another schema version must not be carried forward")

	requireQuarantined(t, root, wrong)
	require.Len(t, log.loud(), 1, "a quarantine is exactly one Loud line (§12.3), got %v", log.loud())
}

// TestPromoterThresholdFromConfig asserts retrieval.promoteAfterExpansions reaches the promoter
// through the real wiring, driven by real `expand` calls rather than by direct NoteExpansion.
//
// D11/§11.6's rule is that every use site reads its number from configuration, and the only way to
// prove that end to end is to change the number and watch the tool's own answer move with it.
func TestPromoterThresholdFromConfig(t *testing.T) {
	const threshold = 3
	f, c := newSeededFixture(t, withConfig(func(cfg *config.Config) {
		cfg.Retrieval.PromoteAfterExpansions = threshold
	}))

	for i := 1; i <= threshold; i++ {
		var body contentBody
		f.callOK(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()}, &body)
		require.Equal(t, i, body.Expansions, "expand %d must report expansion %d", i, i)
		require.Equal(t, i == threshold, body.Promoted,
			"with promoteAfterExpansions=%d, expand %d must report promoted=%v", threshold, i, i == threshold)
	}

	got, err := f.Prom.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Equal(t, []core.Hash{c.AuthRoot}, got, "the expanded hash must be the promoted one")
}

// TestPromoterRejectsThresholdBelowOne asserts a threshold below one is refused rather than
// clamped.
//
// config.Validate already enforces `promoteAfterExpansions >= 1`, so a zero reaching the
// constructor means a caller bypassed configuration; reading it as "promote everything on first
// sight" would turn the pointer tier into a copy of the retrieval log.
func TestPromoterRejectsThresholdBelowOne(t *testing.T) {
	root := newFixtureRoot(t, nil)

	for _, threshold := range []int{0, -1} {
		p, err := NewPromoter(PromotionsPath(root), threshold, newFakeClock(epoch))
		require.Error(t, err, "NewPromoter must refuse a threshold of %d", threshold)
		require.Nil(t, p, "a refused constructor must return no Promoter")
		require.Contains(t, err.Error(), "at least 1", "the error must say what the bound is")
	}
}

// TestPromoterRejectsEmptyPath asserts a promoter with nowhere to persist is refused at
// construction, where the wiring bug is, rather than at the first expansion.
func TestPromoterRejectsEmptyPath(t *testing.T) {
	p, err := NewPromoter("", 2, newFakeClock(epoch))
	require.Error(t, err, "NewPromoter must refuse an empty state path")
	require.Nil(t, p, "a refused constructor must return no Promoter")
}

// TestPromoterZeroHashIsIgnored asserts a zero hash is a no-op rather than a counted key.
//
// A zero core.Hash is what a caller passes when a resolution silently failed upstream; counting it
// would accumulate demand for content that does not exist and eventually promote it.
func TestPromoterZeroHashIsIgnored(t *testing.T) {
	p, path, _ := newTestPromoter(t, 2)

	count, promoted, err := p.NoteExpansion(t.Context(), testSession, core.Hash{})
	require.NoError(t, err, "a zero hash is not an error")
	require.Equal(t, 0, count, "a zero hash counts nothing")
	require.False(t, promoted, "a zero hash promotes nothing")

	_, statErr := os.Stat(paths.Long(path))
	require.True(t, os.IsNotExist(statErr), "a zero hash must persist nothing to %s", path)
}

// TestRecallDoesNotCountAsExpansion asserts recall is evidence of SEARCHING, not of demand.
//
// recall returns pointers and summaries, never bytes, so counting it would promote whatever the
// model happened to search for twice — which is exactly the content it decided not to expand.
func TestRecallDoesNotCountAsExpansion(t *testing.T) {
	f, _ := newSeededFixture(t)

	for i := 1; i <= 2; i++ {
		var body recallBody
		f.callOK(t, ToolRecall, map[string]any{"query": "pool timeout"}, &body)
		require.True(t, body.Found, "recall %d found nothing in the seeded corpus", i)
	}

	_, statErr := os.Stat(paths.Long(PromotionsPath(f.Root)))
	require.True(t, os.IsNotExist(statErr), "recall must persist no promotion state at all")

	count, _, err := f.Prom.NoteExpansion(t.Context(), testSession, promoterHash("after-recall"))
	require.NoError(t, err, "NoteExpansion")
	require.Equal(t, 1, count, "the first real expansion must be the first count, not the third")
}

// TestExpandMetaCarriesExpansionCount asserts the count and the promotion flag reach the model in
// both places a caller reads them: the _meta envelope and the response body.
//
// They are separate code paths — spanMeta builds one, contentBody the other — so a build that
// updated one and not the other would answer two different numbers to the same question.
func TestExpandMetaCarriesExpansionCount(t *testing.T) {
	f, c := newSeededFixture(t)
	require.Equal(t, 2, f.Cfg.Retrieval.PromoteAfterExpansions, "this test is written against the default threshold")

	f.callOK(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()}, nil)

	var body contentBody
	resp := f.callOK(t, ToolExpand, map[string]any{"hash": c.AuthRoot.String()}, &body)

	require.Equal(t, 2, resp.Meta[metaExpansions], "_meta.%s.%s must report the running count", metaNamespace, metaExpansions)
	require.Equal(t, true, resp.Meta[metaPromoted], "_meta.%s.%s must report the promotion", metaNamespace, metaPromoted)
	require.Equal(t, 2, body.Expansions, "the body must report the same count as the envelope")
	require.True(t, body.Promoted, "the body must report the same promotion as the envelope")
}

// TestPromoterCountsMonotoneProperty states the two invariants over a generated call sequence.
//
// It is the rapid property the plan names PropertyPromoterCountsMonotone; the Test prefix is what
// makes `go test` run it at all, so the name carries both. Generated input is what a table cannot
// reach here: the interesting cases are interleavings of sessions and hashes around an arbitrary
// threshold, and there are more of them than anyone writes by hand.
func TestPromoterCountsMonotoneProperty(t *testing.T) {
	base := t.TempDir()
	var seq atomic.Int64
	hashes := []core.Hash{promoterHash("p0"), promoterHash("p1"), promoterHash("p2")}
	sessions := []core.SessionID{"prop-a", "prop-b"}

	rapid.Check(t, func(rt *rapid.T) {
		dir := filepath.Join(base, strconv.FormatInt(seq.Add(1), 10))
		require.NoError(rt, os.MkdirAll(paths.Long(dir), 0o700), "mkdir %s", dir)

		threshold := rapid.IntRange(1, 4).Draw(rt, "threshold")
		p, err := NewPromoter(filepath.Join(dir, promotionsFileName), threshold, newFakeClock(epoch))
		require.NoError(rt, err, "NewPromoter")

		counts := map[string]int{}
		promotions := map[core.SessionID]int{}

		for i := 0; i < rapid.IntRange(1, propPromoterCalls).Draw(rt, "calls"); i++ {
			sess := sessions[rapid.IntRange(0, len(sessions)-1).Draw(rt, "session")]
			h := hashes[rapid.IntRange(0, len(hashes)-1).Draw(rt, "hash")]
			key := string(sess) + "\x00" + h.String()
			before := counts[key]

			count, promoted, noteErr := p.NoteExpansion(context.Background(), sess, h)
			require.NoError(rt, noteErr, "NoteExpansion")

			counts[key] = before + 1
			require.Equal(rt, counts[key], count, "the count must be the number of expansions so far")
			require.GreaterOrEqual(rt, count, before, "a per-hash count must never decrease")
			require.Equal(rt, count >= threshold, promoted,
				"promoted must be exactly count >= threshold (count=%d threshold=%d)", count, threshold)
			if count == threshold {
				promotions[sess]++
			}
		}

		for _, sess := range sessions {
			got, listErr := p.Promoted(context.Background(), sess)
			require.NoError(rt, listErr, "Promoted(%s)", sess)
			require.Len(rt, got, promotions[sess], "each hash enters %s's promoted list exactly once", sess)
		}
	})
}

// TestPromotedIsReadableByLaterSubplan asserts the values SP-16 receives are real core.Hash values
// that round-trip through their own text form.
//
// promotions.json stores the "sha256:<hex>" spelling, so every read goes through core.ParseHash;
// a value that did not round-trip would mean the file and the interface disagree about what a hash
// is, and SP-16 would silently drop the pointer tier's entries.
func TestPromotedIsReadableByLaterSubplan(t *testing.T) {
	p, _, _ := newTestPromoter(t, 2)
	want := []core.Hash{promoterHash("sp16-a"), promoterHash("sp16-b")}

	for _, h := range want {
		for i := 1; i <= 2; i++ {
			_, _, err := p.NoteExpansion(t.Context(), testSession, h)
			require.NoError(t, err, "NoteExpansion(%s) %d", h.Short(), i)
		}
	}

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Equal(t, want, got, "the promoted list must be the promotion order")

	for _, h := range got {
		parsed, perr := core.ParseHash(h.String())
		require.NoError(t, perr, "a promoted hash must round-trip through core.ParseHash")
		require.Equal(t, h, parsed, "a promoted hash must round-trip to itself")
		require.False(t, h.IsZero(), "a promoted hash must never be the zero hash")
	}
}

// TestPromoterIsSafeUnderConcurrentNoteExpansion asserts the counts are exact — not merely
// non-crashing — when eight goroutines expand the same hash at once.
//
// One SHARED hash rather than eight private ones is the point: contention on distinct keys would
// pass with no mutex at all on some map implementations, whereas the shared key is what proves
// both the increment and the exactly-once promotion append are actually serialized. Run under
// -race, this is also the file's only check that the promoter's own lock covers persistLocked.
func TestPromoterIsSafeUnderConcurrentNoteExpansion(t *testing.T) {
	const goroutines, perGoroutine = 8, 50

	p, _, _ := newTestPromoter(t, 2)
	h := promoterHash("concurrent")

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*perGoroutine)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				if _, _, err := p.NoteExpansion(context.Background(), testSession, h); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "NoteExpansion under concurrency")
	}

	count, promoted, err := p.NoteExpansion(t.Context(), testSession, h)
	require.NoError(t, err, "the settling NoteExpansion")
	require.Equal(t, goroutines*perGoroutine+1, count,
		"every concurrent expansion must be counted exactly once")
	require.True(t, promoted, "a hash far past its threshold is promoted")

	got, err := p.Promoted(t.Context(), testSession)
	require.NoError(t, err, "Promoted")
	require.Equal(t, []core.Hash{h}, got,
		"the threshold is crossed once, so the promoted list holds exactly one entry")
}
