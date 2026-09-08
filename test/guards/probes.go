package guards

import (
	"context"
	"testing"

	"github.com/qompack/qompack/internal/analyzer"
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/store"
)

// A probe calls one package's canonical operation — the method named in that package's
// plans/OWNERS.tsv row — and reports whether it is still a stub.
//
// Probing behaviour rather than reading a build tag or a version constant is deliberate: the
// build-order guards below exist to catch a package that got IMPLEMENTED out of order, and the
// only reliable evidence of that is the package actually doing something. A flag would have to be
// flipped by the same person who would have to remember the ordering rule.
type probe struct {
	// pkg is the package name as plans/OWNERS.tsv spells it.
	pkg string
	// isStub reports core.IsNotImplemented over the package's OWNERS.tsv probe method.
	isStub func(t *testing.T) bool
}

// isStub runs p against a scratch project and reports whether the package is still a stub.
func isStub(t *testing.T, p probe) bool {
	t.Helper()
	return p.isStub(t)
}

// probeDeps is the minimum set of collaborators the constructors need. Every member is either a
// no-op or another stub, which is exactly right: a probe must not depend on anything working.
func probeDeps() (logging.Logger, obs.Registry, core.Clock) {
	clk := core.SystemClock()
	return logging.Nop(), obs.New(clk), clk
}

// evalProbe — OWNERS.tsv names eval's canonical operation as Load.
var evalProbe = probe{pkg: "eval", isStub: func(t *testing.T) bool {
	t.Helper()
	h := eval.New(eval.Options{Cfg: config.Defaults()})
	_, err := h.Load(t.TempDir())
	return core.IsNotImplemented(err)
}}

// storeProbe — OWNERS.tsv names store's canonical operation as PutBytes.
var storeProbe = probe{pkg: "store", isStub: func(t *testing.T) bool {
	t.Helper()
	log, m, clk := probeDeps()
	s, err := store.Open(t.TempDir(), config.Defaults(), store.Deps{Log: log, Metrics: m, Clock: clk})
	if err != nil {
		// A constructor that refuses to build cannot be a working implementation.
		return true
	}
	// A landed store holds open append-only handles; releasing them here keeps the probe from
	// making its caller's t.TempDir cleanup fail on Windows.
	defer func() { _ = s.Close() }()
	_, err = s.PutBytes(context.Background(), []byte("probe"), store.PutOptions{})
	return core.IsNotImplemented(err)
}}

// negknowProbe — OWNERS.tsv names negknow's canonical operation as Query.
var negknowProbe = probe{pkg: "negknow", isStub: func(t *testing.T) bool {
	t.Helper()
	log, m, clk := probeDeps()
	l, err := negknow.Open(t.TempDir(), config.Defaults(), nil, negknow.Deps{Log: log, Metrics: m, Clock: clk})
	if err != nil {
		return true
	}
	// A landed ledger holds an open append-only handle on records/eliminations.jsonl; releasing it
	// here keeps the probe from making its caller's t.TempDir cleanup fail on Windows, exactly as
	// storeProbe above has had to since SP-06.
	defer func() { _ = l.Close() }()
	_, err = l.Query(context.Background(), "probe-target", "probe-approach", negknow.ScopeProject)
	return core.IsNotImplemented(err)
}}

// checkpointProbe — OWNERS.tsv names checkpoint's canonical operation as Begin.
var checkpointProbe = probe{pkg: "checkpoint", isStub: func(t *testing.T) bool {
	t.Helper()
	log, m, clk := probeDeps()
	w, err := checkpoint.OpenWriter(t.TempDir(), config.Defaults(), log, m, clk)
	if err != nil {
		return true
	}
	_, err = w.Begin(context.Background(), "probe-session", 0, checkpoint.SourceSet{})
	return core.IsNotImplemented(err)
}}

// analyzerProbe — OWNERS.tsv names analyzer's canonical operation as Select.
//
// NewSelector is given a block at a position at or after p, so the §13 invariant 4 refusal (which
// is REAL in wave 0) cannot be mistaken for the stub's ErrNotImplemented.
var analyzerProbe = probe{pkg: "analyzer", isStub: func(t *testing.T) bool {
	t.Helper()
	sel, err := analyzer.NewSelector(0, []analyzer.Block{{ID: dag.NodeID("probe"), Pos: 0}},
		dag.Slice{}, nil, defaultLambda, true)
	if err != nil {
		return core.IsNotImplemented(err)
	}
	_, err = sel.Select(context.Background(), probeBudgetTokens)
	return core.IsNotImplemented(err)
}}

// defaultLambda and probeBudgetTokens are arbitrary well-formed inputs, chosen only so the probe
// reaches the method under test. They are not configuration and duplicate no default.
const (
	defaultLambda     = 0.4  //nomagic:allow arbitrary well-formed probe input
	probeBudgetTokens = 1000 //nomagic:allow arbitrary well-formed probe input
)
