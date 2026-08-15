package store

import (
	"fmt"
	"path/filepath"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/tokens"
)

// chunkCacheFile is the per-project token measurement cache the exact estimator persists under
// <root>/.qompack/state (G10.2).
const chunkCacheFile = "chunktokens.bin"

// The five append-only index files this store owns. index/files.jsonl and index/sessions.jsonl do
// not appear in Qompack.md §7.4's tree: they are runtime artifacts in exactly the spirit of
// 00-ARCHITECTURE.md §3.3's own "runtime-only, additive" block. files.jsonl is the append-only log
// behind the files.json view §7.4 does name, and sessions.jsonl is what makes GC's "10 sessions"
// retention axis computable. Neither changes a §5 interface, so neither requires an amendment.
const (
	rootsFile    = "roots.jsonl"
	toolUseFile  = "tool_use.jsonl"
	filesLogFile = "files.jsonl"
	filesViewNam = "files.json"
	sessionsFile = "sessions.jsonl"
	segmentsFile = "segments.jsonl"
)

// openFS builds and loads an FSStore rooted at root.
//
// root is the PROJECT root, matching dag.Open and negknow.Open — never <root>/.qompack. Every path
// below is derived from paths.Of(root); nothing hand-joins ".qompack".
func openFS(root string, cfg config.Config, deps Deps) (*FSStore, error) {
	l := paths.Of(root)
	deps = defaultDeps(l, cfg, deps)

	if err := paths.EnsureLayout(l); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", root, err)
	}
	if err := ensureStoreDirs(l); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", root, err)
	}

	s := &FSStore{
		root:      root,
		l:         l,
		cfg:       cfg,
		deps:      deps,
		log:       deps.Log,
		rootIndex: make(map[core.Hash]*rootEntry),
		chunkSet:  make(map[core.Hash]int32),
		refs:      make(map[core.Hash]uint32),
		byPath:    make(map[string][]core.Hash),
		toolUse:   make(map[core.ToolUseID]*ToolUseRecord),
		byPathTU:  make(map[string][]core.ToolUseID),
		fileHist:  make(map[string][]FileVersion),
	}

	var err error
	if s.rootsW, err = openAppendFile(filepath.Join(l.Index, rootsFile)); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", rootsFile, err)
	}

	if s.tuW, err = openAppendFile(filepath.Join(l.Index, toolUseFile)); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", toolUseFile, err)
	}
	if s.filesW, err = openAppendFile(filepath.Join(l.Index, filesLogFile)); err != nil {
		return nil, fmt.Errorf("store: open %s: %w", filesLogFile, err)
	}

	// Replay the indices. A malformed line is counted and skipped inside each loader — only an
	// unreadable directory is fatal, because the daemon then refuses to start and the client
	// spools (§12.3).
	for _, load := range []func() error{s.loadRoots, s.loadToolUse, s.loadFiles} {
		if err := load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// defaultDeps fills every nil member of deps with a working default.
//
// Deps.Redact is the one member whose nil default is load-bearing: it becomes redact.New(cfg),
// NEVER redact.Nop(). A nil redactor must never quietly mean "no redaction" — objects/ is
// content-addressed and immutable, so a secret that reaches it cannot be deleted without breaking
// every root that references its chunk (§13 invariant 7, 00-ARCHITECTURE.md §5.22a).
func defaultDeps(l paths.Layout, cfg config.Config, deps Deps) Deps {
	if deps.Log == nil {
		deps.Log = logging.Nop()
	}
	if deps.Clock == nil {
		deps.Clock = core.SystemClock()
	}
	if deps.Metrics == nil {
		deps.Metrics = obs.New(deps.Clock)
	}
	if deps.Chunker == nil {
		deps.Chunker = chunk.New(chunkParams(cfg))
	}
	if deps.Canon == nil {
		deps.Canon = canon.Default(cfg.Store.Canonicalize)
	}
	if deps.Symbols == nil {
		deps.Symbols = symbols.New()
	}
	if deps.Tokens == nil {
		deps.Tokens = tokens.NewExact(cfg, tokens.DefaultCalibPath(),
			filepath.Join(l.State, chunkCacheFile))
	}
	if deps.Redact == nil {
		deps.Redact = redact.New(cfg)
	}
	return deps
}

// chunkParams reads the chunker parameters from configuration (D11, §11.6: the store never
// hardcodes them), falling back to chunk.DefaultParams when the supplied configuration carries no
// valid chunk block. The fallback matters because callers legitimately construct a store with a
// zero config.Config — the conformance suite's own factory does — and a chunker configured
// min=target=max=0 would be a silent data-shredder rather than a visible failure.
func chunkParams(cfg config.Config) chunk.Params {
	p := chunk.Params{
		Min:    cfg.Store.Chunk.Min,
		Target: cfg.Store.Chunk.Target,
		Max:    cfg.Store.Chunk.Max,
	}
	if p.Validate() != nil {
		return chunk.DefaultParams()
	}
	return p
}
