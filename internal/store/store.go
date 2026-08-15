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

// Open returns a Store rooted at root. Constructing always succeeds, so wave-0 composition roots
// can wire a store.Store today, but every operation is a stub until SP-06 lands the real
// content-addressed store (00-ARCHITECTURE.md §5.8): every method with an error return reports
// core.ErrNotImplemented, and Has and Segments — which have no error return — report the
// documented zero value (false) and a usable stub SegmentLog respectively.
func Open(root string, cfg config.Config, deps Deps) (Store, error) {
	return stubStore{}, nil
}

// stubStore is the SP-01 placeholder Store. SP-06 owns the real implementation.
type stubStore struct{}

// Put always reports core.ErrNotImplemented.
func (stubStore) Put(ctx context.Context, r io.Reader, o PutOptions) (PutResult, error) {
	return PutResult{}, core.ErrNotImplemented
}

// PutBytes always reports core.ErrNotImplemented.
func (stubStore) PutBytes(ctx context.Context, b []byte, o PutOptions) (PutResult, error) {
	return PutResult{}, core.ErrNotImplemented
}

// GetChunk always reports core.ErrNotImplemented.
func (stubStore) GetChunk(ctx context.Context, h core.Hash) ([]byte, error) {
	return nil, core.ErrNotImplemented
}

// GetRoot always reports core.ErrNotImplemented.
func (stubStore) GetRoot(ctx context.Context, root core.Hash) (Root, error) {
	return Root{}, core.ErrNotImplemented
}

// Open always reports core.ErrNotImplemented.
func (stubStore) Open(ctx context.Context, root core.Hash) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

// OpenSpan always reports core.ErrNotImplemented.
func (stubStore) OpenSpan(ctx context.Context, root core.Hash, off, n int64) (io.ReadCloser, error) {
	return nil, core.ErrNotImplemented
}

// Has always returns false. Has has no error return, so false — Rule 1's documented zero value —
// is the only honest answer: a stub store has nothing stored.
func (stubStore) Has(h core.Hash) bool { return false }

// RecordToolUse always reports core.ErrNotImplemented.
func (stubStore) RecordToolUse(ctx context.Context, rec ToolUseRecord) error {
	return core.ErrNotImplemented
}

// ToolUse always reports core.ErrNotImplemented.
func (stubStore) ToolUse(ctx context.Context, id core.ToolUseID) (ToolUseRecord, error) {
	return ToolUseRecord{}, core.ErrNotImplemented
}

// ToolUsesByPath always reports core.ErrNotImplemented.
func (stubStore) ToolUsesByPath(ctx context.Context, path string, limit int) ([]ToolUseRecord, error) {
	return nil, core.ErrNotImplemented
}

// MarkSuperseded always reports core.ErrNotImplemented.
func (stubStore) MarkSuperseded(ctx context.Context, older core.ToolUseID, by core.ToolUseID) error {
	return core.ErrNotImplemented
}

// AppendFileVersion always reports core.ErrNotImplemented.
func (stubStore) AppendFileVersion(ctx context.Context, path string, v FileVersion) error {
	return core.ErrNotImplemented
}

// FileHistory always reports core.ErrNotImplemented.
func (stubStore) FileHistory(ctx context.Context, path string) ([]FileVersion, error) {
	return nil, core.ErrNotImplemented
}

// FileAt always reports core.ErrNotImplemented.
func (stubStore) FileAt(ctx context.Context, path string, at time.Time) (FileVersion, error) {
	return FileVersion{}, core.ErrNotImplemented
}

// ChangedSince always reports core.ErrNotImplemented.
func (stubStore) ChangedSince(ctx context.Context, deps []core.Dep) ([]core.Dep, error) {
	return nil, core.ErrNotImplemented
}

// Search always reports core.ErrNotImplemented.
func (stubStore) Search(ctx context.Context, q Query) ([]Hit, error) {
	return nil, core.ErrNotImplemented
}

// Segments always returns a usable stub SegmentLog. Segments has no error return: constructing a
// sub-object accessor is not itself an "operation" under Rule 1, so — like Open returning a
// usable stub Store — it returns a legal, non-nil value whose own methods are the ones that fail.
func (stubStore) Segments() SegmentLog { return stubSegmentLog{} }

// Stats always reports core.ErrNotImplemented.
func (stubStore) Stats(ctx context.Context) (Stats, error) {
	return Stats{}, core.ErrNotImplemented
}

// GC always reports core.ErrNotImplemented.
func (stubStore) GC(ctx context.Context, p GCPolicy) (GCReport, error) {
	return GCReport{}, core.ErrNotImplemented
}

// Flush always reports core.ErrNotImplemented.
func (stubStore) Flush(ctx context.Context) error { return core.ErrNotImplemented }

// Close always reports core.ErrNotImplemented.
func (stubStore) Close() error { return core.ErrNotImplemented }
