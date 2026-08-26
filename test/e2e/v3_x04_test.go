// V3-VERIFY §5 X4: a user prompt captured VERBATIM by the observer (G2.3) becomes the evidence
// root of a negknow elimination record — observer.OnUserPrompt → store → negknow.IngestUserStatement
// (SP-08 + SP-06 + SP-09), composed in-process against a real store, a real dag.Graph, a real
// observer and the real ledger.
package e2e

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/testutil"
)

// x4Session is the one session both the observer and the ledger run under: negknow records are
// session-scoped by default, so the ledger must be opened as the SAME session the prompts were
// observed in for ScopeSession queries to see the record it mints.
const x4Session = core.SessionID("sess_v3_x4")

// The X4 script, verbatim from the plan row. x4Prompt2 carries the curly apostrophe (U+2019) and
// an em dash on purpose: reading it back byte for byte is a statement about the verbatim opt-out,
// not about a prompt nothing would have rewritten anyway.
const (
	x4Prompt1  = "fix the pgbouncer 1.18 pool bypass"
	x4Prompt2  = "That didn’t work — the pool is still saturated."
	x4Path     = "src/db.ts"
	x4Approach = "widen pool timeout"
)

// x4UnresolvedCounter is the plan row's counter name, transcribed once.
const x4UnresolvedCounter = "negknow.user_statement.unresolved"

// x4RecordingStore wraps the real store.Store and records the PutOptions of every PutBytes call.
// It exists because the X4 row asserts a property of the request the observer SENT — Canon.Strip
// non-nil and empty, MinHash disabled — which a real FSStore consumes without exposing. The wrapper
// is the §5 "compose the seam inside the test" pattern: everything is delegated unchanged, so every
// byte still flows through the real redact → canonicalize → chunk pipeline.
type x4RecordingStore struct {
	store.Store

	mu   sync.Mutex
	puts []x4Put
}

// x4Put is one recorded PutBytes call: the tool it was filed under and the options it carried.
type x4Put struct {
	Opts store.PutOptions
}

func (s *x4RecordingStore) PutBytes(ctx context.Context, b []byte, o store.PutOptions) (store.PutResult, error) {
	s.mu.Lock()
	s.puts = append(s.puts, x4Put{Opts: o})
	s.mu.Unlock()
	return s.Store.PutBytes(ctx, b, o)
}

// promptPuts returns the recorded options of every PutBytes filed under the UserPromptSubmit
// pseudo-tool — the verbatim prompt captures, and nothing else.
func (s *x4RecordingStore) promptPuts() []store.PutOptions {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.PutOptions
	for _, p := range s.puts {
		if p.Opts.Tool == "UserPromptSubmit" {
			out = append(out, p.Opts)
		}
	}
	return out
}

// x4Event builders. The observer is driven in-process (the daemon transport is X1's concern, not
// X4's), so events are constructed directly rather than marshalled through hook payloads.
func x4PromptEvent(prompt string) observer.Event {
	return observer.Event{
		HookEventName: "UserPromptSubmit",
		SessionID:     x4Session,
		Prompt:        prompt,
	}
}

func x4EditEvent(t *testing.T, path string) observer.Event {
	t.Helper()
	in, err := json.Marshal(map[string]string{"file_path": path, "old_string": "POOL_TIMEOUT: 30", "new_string": "POOL_TIMEOUT: 60"})
	require.NoError(t, err)
	resp, err := json.Marshal(map[string]string{"content": "The file " + path + " has been updated."})
	require.NoError(t, err)
	return observer.Event{
		HookEventName: "PostToolUse",
		SessionID:     x4Session,
		ToolName:      "Edit",
		ToolUseID:     "toolu_v3_x4_edit",
		ToolInput:     in,
		ToolResponse:  resp,
	}
}

// x4ReadRoot re-materializes root's whole content out of s.
func x4ReadRoot(ctx context.Context, t *testing.T, s store.Store, root core.Hash) []byte {
	t.Helper()
	rc, err := s.Open(ctx, root)
	require.NoError(t, err)
	defer func() { require.NoError(t, rc.Close()) }()
	b, err := io.ReadAll(rc)
	require.NoError(t, err)
	return b
}

func TestV3_VerbatimPromptBecomesEliminationEvidence(t *testing.T) {
	ctx := context.Background()
	p := testutil.NewProject(t)
	p.WithFiles(t, map[string]string{
		x4Path: "export const pool = new Pool({ timeout: 30 });\n",
	})

	// Real store (wrapped only to RECORD the options the observer sends), real graph, real
	// observer, one shared metrics registry the ledger's unresolved counter is read from.
	rec := &x4RecordingStore{Store: p.Store(t)}
	g, err := dag.Open(p.Root, p.Cfg, p.Log)
	require.NoError(t, err)
	reg := obs.New(p.Clock)

	obsv, err := observer.New(observer.Options{
		ProjectRoot: p.Root,
		Cfg:         p.Cfg,
		Store:       rec,
		Graph:       g,
		Log:         p.Log,
		Metrics:     reg,
		Clock:       p.Clock,
	})
	require.NoError(t, err)

	// ── inputs 1–3: the session script ──
	// Turn arithmetic (SP-08 resolved decision 4): a prompt is recorded AT st.Turn and then
	// closes the user turn (0 → 1); PostToolUse never advances the counter; the assistant turn
	// closes at Stop (1 → 2). The Stop between the edit and the second prompt is therefore what
	// the X4 row's "(turn 2)" presumes — a second prompt cannot arrive while the assistant turn
	// that answers the first is still open — and composing it here is this file's seam work.
	_, err = obsv.OnUserPrompt(ctx, x4PromptEvent(x4Prompt1)) // turn 0
	require.NoError(t, err)
	_, err = obsv.OnToolUse(ctx, x4EditEvent(t, x4Path)) // the Edit of src/db.ts, turn 1
	require.NoError(t, err)
	_, err = obsv.OnStop(ctx, observer.Event{HookEventName: "Stop", SessionID: x4Session}, false)
	require.NoError(t, err)
	_, err = obsv.OnUserPrompt(ctx, x4PromptEvent(x4Prompt2)) // turn 2
	require.NoError(t, err)

	// ── step 4: read the turn-2 prompt back through its DERIVED identity ──
	tu, err := rec.ToolUse(ctx, observer.VerbatimPromptID(x4Session, 2))
	require.NoError(t, err,
		"the turn-2 prompt must be indexed under prompt_<session>_2 — the derived identity retrieval reaches it by")

	got := x4ReadRoot(ctx, t, rec, tu.Root)
	require.Equal(t, []byte(x4Prompt2), got,
		"G2.3: the stored object must be the user's bytes, byte for byte")
	require.Contains(t, string(got), "didn’t",
		"the curly apostrophe (U+2019) must survive the store's ingest pipeline intact")

	// The request the observer sent, for BOTH captured prompts: Canon.Strip non-nil and EMPTY —
	// the "no optional class" request; nil would mean *every* class to canon.gateSet — and the
	// near-duplicate MinHash signature disabled. This is what the arch/sp08-observer-seams
	// amendment made FSStore.canonOptions honour (H8's unit-level twin asserts the same).
	prompts := rec.promptPuts()
	require.Len(t, prompts, 2, "exactly one verbatim Put per non-empty prompt")
	for i, o := range prompts {
		require.NotNil(t, o.Canon.Strip,
			"prompt put %d: a NIL Strip is canon's documented \"every class\" — the OPPOSITE request", i)
		require.Empty(t, o.Canon.Strip,
			"prompt put %d: \"no optional class\" is an EMPTY, non-nil Strip", i)
		require.False(t, o.Canon.MinHash.Enabled,
			"prompt put %d: MinHash must be disabled on a verbatim capture", i)
	}

	// ── step 5: the verbatim root becomes elimination evidence ──
	led, err := negknow.Open(p.Root, p.Cfg, nil, negknow.Deps{
		Store:   rec,
		Graph:   g,
		Session: x4Session,
		Log:     p.Log,
		Metrics: reg,
		Clock:   p.Clock,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = led.Close() })
	m, ok := led.(negknow.Maintainer)
	require.True(t, ok, "negknow.Open must return a value satisfying negknow.Maintainer")

	stmt := negknow.UserStatement{
		Prompt:     string(got),
		Turn:       2,
		Path:       x4Path,
		Approach:   x4Approach,
		PromptRoot: tu.Root,
	}
	recs, err := m.IngestUserStatement(ctx, stmt)
	require.NoError(t, err)
	require.Len(t, recs, 1, "exactly one Record per recognized user statement")
	require.Equal(t, negknow.SourceUserStatement, recs[0].Source)
	require.True(t, strings.HasPrefix(recs[0].Reason, "user stated: "),
		"Reason must be prefixed \"user stated: \", got %q", recs[0].Reason)
	require.Equal(t, tu.Root, recs[0].Evidence,
		"the evidence root must be the STORED verbatim prompt, not a re-minted object")

	ans, err := led.Query(ctx, x4Path, x4Approach, negknow.ScopeSession)
	require.NoError(t, err)
	require.Equal(t, negknow.AnswerActive, ans.State,
		"the elimination the user stated must answer AnswerActive at session scope")

	// ── refusal 1: a matched phrase with NO resolvable target produces nothing but a counter ──
	unresolved := stmt
	unresolved.Path, unresolved.Symbol = "", ""
	recs, err = m.IngestUserStatement(ctx, unresolved)
	require.NoError(t, err)
	require.Empty(t, recs, "no target -> no record: a guessed elimination is the §12 High-severity failure")
	require.Equal(t, int64(1), reg.Counter(x4UnresolvedCounter).Value(),
		"the unplaceable statement must be COUNTED, not guessed at")

	// ── refusal 2: a prompt with no elimination phrase produces nothing and moves no counter ──
	benign := stmt
	benign.Prompt = "looks good, ship it"
	recs, err = m.IngestUserStatement(ctx, benign)
	require.NoError(t, err)
	require.Empty(t, recs, "\"looks good, ship it\" states no elimination")
	require.Equal(t, int64(1), reg.Counter(x4UnresolvedCounter).Value(),
		"a non-matching prompt must not move the unresolved counter")
}
