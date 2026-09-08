package cli

import (
	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/mcp"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/store"
	"github.com/qompack/qompack/internal/symbols"
)

// The composition-root glue internal/mcp may not hold itself.
//
// §3.2 restricts mcp to store, negknow and checkpoint plus the foundation packages, which keeps
// three things out of it that it nevertheless needs: symbols, for the §8.7 symbol-aware span
// widener; redact, for the retrieval-side secret re-check (T20-M2-04); and ipc, for the transport
// `qompack mcp` forwards over. All three are supplied from here — the widener as an adapter onto
// mcp.Widener, the redactor as an adapter onto mcp.Redactor, the transport as an ipc.Client the
// subcommand builds — so the dependency edges stay one-directional and the import-graph check
// stays green.

// symbolWidener adapts a symbols.Extractor to the two-method mcp.Widener port.
type symbolWidener struct{ ex symbols.Extractor }

// Widen grows [off,end) so that end lands on the end of the symbol enclosing end-1.
//
// It reports ok=false rather than an error for every "nothing to do" case — no enclosing symbol,
// an end outside the buffer, a symbol that ends before the cut already does — because the caller's
// response to all of them is identical: keep the chunk-aligned window.
func (w symbolWidener) Widen(path string, b []byte, off, end int64) (int64, int64, bool) {
	if w.ex == nil || end <= 0 || end > int64(len(b)) {
		return off, end, false
	}
	s, ok := w.ex.Enclosing(path, b, int(end)-1)
	if !ok {
		return off, end, false
	}
	e := int64(s.Offset + s.Len)
	if e <= end {
		return off, end, false
	}
	return off, e, true
}

// Find returns the [start,end) of the named symbol in b.
func (w symbolWidener) Find(path string, b []byte, name string) (int64, int64, bool) {
	if w.ex == nil || name == "" {
		return 0, 0, false
	}
	for _, s := range w.ex.Extract(path, b) {
		if s.Name == name {
			return int64(s.Offset), int64(s.Offset + s.Len), true
		}
	}
	return 0, 0, false
}

// retrievalRedactor adapts a redact.Redactor to the one-method mcp.Redactor port.
//
// The adapter is the whole of the §3.2 fix. internal/mcp used to call redact.New(cfg) itself,
// which put an edge mcp → redact into the graph that §3.2 does not allow; the interface now lives
// in mcp and the implementation is built here, where both packages are already imported. Only the
// rule NAMES cross the seam — never a redact.Match, never an offset — because names are all the
// diagnostic on the far side may say about a secret.
type retrievalRedactor struct{ r redact.Redactor }

// Redact applies today's policy and reports the rule behind each match, one entry per match.
func (rr retrievalRedactor) Redact(in []byte) ([]byte, []string) {
	out, matches := rr.r.Redact(in)
	if len(matches) == 0 {
		return out, nil
	}
	rules := make([]string, len(matches))
	for i, m := range matches {
		rules[i] = m.Rule
	}
	return out, rules
}

// NewRetrievalRedactor builds the mcp.Redactor the eight retrieval tools re-check archive bytes
// with, over the SAME effective configuration every other retrieval bound reads from.
//
// It is exported because it is the only legal way to obtain one: internal/mcp cannot construct a
// redactor and fails closed without it, so a composition root outside this package — the daemon's
// own tests, the end-to-end rigs — has to be able to ask for the production article rather than
// invent a second, weaker one.
//
// redact.New is total: with runtime.redact disabled it returns an identity Redactor, which is a
// deliberate operator choice and quite different from the nil this function never returns.
func NewRetrievalRedactor(cfg config.Config) mcp.Redactor {
	return retrievalRedactor{r: redact.New(cfg)}
}

// liveLedger is the elimination-ledger accessor the MCP tools are wired with.
//
// It closes over the *daemon.Options POINTER and reads the Ledger FIELD on every call, which is
// the whole of the fix: negknow.Open is lazy on purpose — its single production call site is the
// rehydrate service, on the first compaction, because an eager open creates sketches/tried.bloom
// and holds an eliminations.jsonl handle in every daemon that never compacts — so opts.Ledger is
// nil at wiring time. Handing that VALUE to the tools froze the nil for the life of the process
// and left `already_tried` and `record_eliminated` permanently answering "not present in this
// build" beside a ledger that was open. This is the same accessor shape wireScheduler's LedgerFn
// and wireCheckpointSources' SourceSet supplier already use; it opens nothing and owns nothing.
func liveLedger(opts *daemon.Options) func() negknow.Ledger {
	if opts == nil {
		return nil
	}
	return func() negknow.Ledger { return opts.Ledger }
}

// NewToolDeps assembles the collaborator set the eight retrieval tools are bound to.
//
// Every collaborator may be nil and each handler says so rather than failing: a daemon whose store
// would not open still answers `recall` with `available:false`, which is a better session than one
// that cannot start. A nil extractor leaves ToolDeps.Widener nil, which the span resolver already
// tolerates — the spans are then chunk-aligned but not symbol-widened.
//
// The ledger arrives as an ACCESSOR rather than a value; see liveLedger for why. A nil accessor is
// the "no ledger in this build" case and stays nil-tolerant.
//
// The Redactor is the exception to "every collaborator may be nil": it is built HERE, always, from
// cfg. mcp cannot build one for itself (§3.2) and refuses to serve archive content without one, so
// a ToolDeps that left it nil would produce a daemon whose retrieval tools all answer
// available:false. Every path that reaches these tools in production goes through this function.
func NewToolDeps(root string, cfg config.Config, st store.Store, ledger func() negknow.Ledger,
	cr checkpoint.Reader, dr mcp.DropReporter, p mcp.Promoter,
	ex symbols.Extractor, log logging.Logger, m obs.Registry, clk core.Clock,
) mcp.ToolDeps {
	d := mcp.ToolDeps{
		Store: st, LedgerFn: ledger, Checkpoints: cr, Rehydrator: dr, Promoter: p,
		Cfg: cfg, ProjectRoot: root, Clock: clk, Log: log, Metrics: m,
		Redactor: NewRetrievalRedactor(cfg),
	}
	if ex != nil {
		d.Widener = symbolWidener{ex: ex}
	}
	return d
}

// nopSpool is the ipc.SpoolWriter `qompack mcp` hands its client: one that discards.
//
// MCP requests must NEVER be spooled. A spooled `expand` replayed minutes later on the daemon's
// idle drain would write a spurious ephemeral record for a result nobody can receive, and a
// spooled `tools/call` has no caller left to answer. The MCP transport is request/response or it
// is nothing; the failure path is the tool error the subcommand returns, not a queue.
type nopSpool struct{}

// Append discards the request and reports success, because there is nothing to fail at.
func (nopSpool) Append(ipc.Request) error { return nil }

// Path reports that there is no spool file, which /qompack:status renders as "none".
func (nopSpool) Path() string { return "" }
