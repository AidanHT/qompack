package chunk

import (
	"io"

	"github.com/qompack/qompack/internal/core"
)

// Chunker splits byte streams into content-defined Chunks (00-ARCHITECTURE.md §5.5).
type Chunker interface {
	// Split is allocation-free apart from the returned slice, and safe for concurrent use.
	Split(data []byte) []Chunk
	// SplitStream streams the same boundaries Split would find, reading from r and calling fn once
	// per chunk with the chunk's metadata and its bytes.
	SplitStream(r io.Reader, fn func(Chunk, []byte) error) error
}

// New returns a Chunker configured by p. Constructing always succeeds, so wave-0 composition
// roots can wire a chunk.Chunker today, but every operation is a stub until SP-04 lands the real
// FastCDC implementation (00-ARCHITECTURE.md §5.5): Split — which has no error return — always
// returns nil, and SplitStream always reports core.ErrNotImplemented.
//
// New has no error return, matching every other "computational" constructor in this codebase
// (symbols.New, grammar.New, redact.New): building a Chunker performs no I/O by itself, so there
// is nothing for a stub constructor to fail at. New deliberately does not call p.Validate: SP-04's
// real constructor is expected to, but a stub that rejected an unvalidated Params would make
// composition roots fail before SP-04 exists to fix it, contradicting Rule 1 of the stub pattern
// ("constructing must work").
func New(p Params) Chunker {
	return stubChunker{}
}

// stubChunker is the SP-01 placeholder Chunker. SP-04 owns the real FastCDC implementation.
type stubChunker struct{}

// Split always returns nil. Split has no error return (00-ARCHITECTURE.md §5.5), so nil — Rule 1's
// documented zero value for a no-error-return stub method — is the only honest answer until SP-04
// lands: a stub Chunker has performed no boundary detection at all, and any non-nil result would
// be exactly the "plausible-looking fake data" Rule 2 forbids.
func (stubChunker) Split(data []byte) []Chunk { return nil }

// SplitStream always reports core.ErrNotImplemented.
func (stubChunker) SplitStream(r io.Reader, fn func(Chunk, []byte) error) error {
	return core.ErrNotImplemented
}
