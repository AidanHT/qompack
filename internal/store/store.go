package store

import (
	"context"
	"io"
	"time"

	"github.com/qompack/qompack/internal/canon"
	"github.com/qompack/qompack/internal/chunk"
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/redact"
	"github.com/qompack/qompack/internal/symbols"
	"github.com/qompack/qompack/internal/tokens"
)

// Store is the L1 content-addressed store's full seam (00-ARCHITECTURE.md §5.8): content objects,
// the tool_use index, per-path file version history, search, the segment log, size stats and GC.
type Store interface {
	// ── objects ──

	Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error)
	PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error)
	GetChunk(ctx context.Context, h core.Hash) ([]byte, error)
	GetRoot(ctx context.Context, root core.Hash) (Root, error)
	// Open returns the full content of root.
	Open(ctx context.Context, root core.Hash) (io.ReadCloser, error)
	// OpenSpan returns the [off, off+n) byte span of root's content — the minimal-sufficient-span
	// read backing retrieval.defaultSpan = "minimal" (00-ARCHITECTURE.md §8.7).
	OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error)
	// Has reports whether h is present in the object store.
	Has(h core.Hash) bool

	// ── tool_use index ──

	RecordToolUse(ctx context.Context, rec ToolUseRecord) error
	ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error)
	ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error)
	// MarkSuperseded flips older's Status to StatusSuperseded, pointing SupersededBy at by.
	MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error

	// ── file version history (§8.2) ──

	AppendFileVersion(ctx context.Context, path string, v FileVersion) error
	FileHistory(ctx context.Context, path string) ([]FileVersion, error)
	FileAt(ctx context.Context, path string, at time.Time) (FileVersion, error)
	// ChangedSince returns the subset of deps whose current file-version hash differs from the
	// hash recorded in deps (00-ARCHITECTURE.md §8.3 staleness). It takes []core.Dep, NOT
	// []negknow.Dep — store must not import negknow (§3.2): negknow.Dep is only an alias of
	// core.Dep, so this signature is what keeps that dependency edge one-directional.
	ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error)

	// ── search (backs `recall`) ──

	Search(ctx context.Context, q Query) ([]Hit, error)

	// Segments returns this Store's SegmentLog.
	Segments() SegmentLog
	Stats(ctx context.Context) (Stats, error)
	GC(ctx context.Context, p GCPolicy) (GCReport, error)
	// Flush durably persists any buffered writes.
	Flush(ctx context.Context) error
	// Close releases every resource this Store holds (file handles, background workers).
	Close() error
}

// Deps is the set of collaborators Open assembles a Store from (00-ARCHITECTURE.md §5.8).
type Deps struct {
	Chunker chunk.Chunker
	Canon   canon.Registry
	Tokens  tokens.Estimator
	// Symbols backs Query.Symbol and the §8.7 symbol-aware span widener.
	Symbols symbols.Extractor
	// Redact is applied to every byte on the way in (00-ARCHITECTURE.md §5.22a): the single
	// choke point that keeps secrets out of objects/.
	Redact  redact.Redactor
	Log     logging.Logger
	Metrics obs.Registry
	Clock   core.Clock
}

// Open returns the Store rooted at root.
//
// root is the PROJECT root, never <root>/.qompack, matching dag.Open and negknow.Open; every path
// the store touches is derived from paths.Of(root) and nothing hand-joins ".qompack".
//
// Every nil member of deps is filled with a working default. Deps.Redact is the one whose default
// is load-bearing: a nil Redact becomes redact.New(cfg) and NEVER redact.Nop(), because a nil
// redactor must not quietly mean "no redaction" — objects/ is content-addressed and immutable, so
// a secret that reaches it cannot be removed without breaking every root referencing its chunk
// (§13 invariant 7, 00-ARCHITECTURE.md §5.22a).
func Open(root string, cfg config.Config, deps Deps) (Store, error) {
	// Assigned through an explicit nil rather than "return openFS(...)": openFS reports
	// (*FSStore)(nil) alongside its error, and returning that directly would hand the caller a
	// NON-nil Store interface wrapping a nil pointer.
	s, err := openFS(root, cfg, deps)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// The seams every consumer of this package reaches the filesystem store through.
var (
	_ Store      = (*FSStore)(nil)
	_ RefCounter = (*FSStore)(nil)
	_ SegmentLog = (*segLog)(nil)
)
