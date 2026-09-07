package pins

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"unicode"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Invariant is a single pinned fact: user- or agent-asserted text that must never be summarized,
// truncated, or regenerated (Qompack.md §7.4, 00-ARCHITECTURE.md §5.14). It is defined here and
// aliased by checkpoint.Invariant — see the package comment for why.
//
// The json tags are explicit and frozen: this struct serializes into checkpoints/*.json (the
// "invariants" array) and into pins/invariants.jsonl add records, and
// testdata/golden/contracts/checkpoint/want/0001.json plus
// testdata/golden/contracts/pins/want/{add,tombstone}.jsonl are frozen fixtures (Rule W-2) that
// use exactly these spellings. Do not change them without an amendment.
type Invariant struct {
	// ID is the invariant's stable identifier, for example "inv_7c1a9e2f4b60".
	ID string `json:"id"`
	// Text is the pinned text itself, verbatim.
	Text string `json:"text"`
	// Source names who pinned it: "user" or "agent".
	Source string `json:"source"`
	// Pinned is when this invariant was added.
	Pinned core.UnixMilli `json:"pinned"`
}

// Store is the pins/invariants.json seam (00-ARCHITECTURE.md §5.14). Every operation is
// append-only at the log level: Add appends, Remove writes a tombstone record rather than
// deleting anything, and Materialize regenerates the pins/invariants.json convenience view from
// the log without ever becoming the source of truth itself (00-ARCHITECTURE.md §3.3).
type Store interface {
	// Add appends inv to the log, minting an ID and defaulting Pinned from the clock when the
	// caller supplies neither. It is idempotent by ID: re-adding an invariant that is currently
	// live appends nothing and reports no error.
	Add(ctx context.Context, inv Invariant) error
	// Remove appends a tombstone record for id. It never rewrites or deletes the original add
	// record. An id this store has never recorded reports core.ErrNotFound.
	Remove(ctx context.Context, id string) error
	// All returns every invariant that has not been tombstoned, sorted by Pinned ascending with
	// ties broken by ID ascending.
	All(ctx context.Context) ([]Invariant, error)
	// Materialize regenerates the pins/invariants.json view from the append-only log.
	Materialize(ctx context.Context) error
}

// hashDomainPin is the domain separator MintID hashes under.
//
// It is declared here rather than in core's exported domain registry for the same reason the
// three qompack.sketch.* domains are declared inside internal/sketch: nothing outside this
// package may mint an invariant id, so exporting the string would only invite a second minter.
// Like every other domain it is a wire format — changing it re-keys every id already on disk.
const hashDomainPin = "qompack.pin"

// invariantIDPrefix and invariantIDHexLen give every minted id its shape: "inv_" plus the first
// 12 hex characters of the digest, the same 12-character short form core.Hash.Short uses for
// tombstones and IPC endpoint names. Twelve characters is 48 bits, which is far past collision
// range for the handful of invariants one project ever pins, and short enough to read aloud.
const (
	invariantIDPrefix = "inv_"
	invariantIDHexLen = 12
)

// MintID derives an invariant's stable identifier from its text (§1 of
// plans/V4-SP-10-checkpointer-l4.md): "inv_" plus the first 12 hex characters of the
// domain-separated digest of the whitespace-normalized text.
//
// It is minted from the text rather than from a counter or a random value so that the SAME
// assertion, re-pinned in a later session or by a different hook, lands on the same id — which is
// what makes Add idempotent in practice rather than only in principle. Normalization is what
// makes that hold across a re-wrap: text that a caller reflowed onto different lines is the same
// invariant and must not mint a second id.
func MintID(text string) string {
	h := core.HashBytes(hashDomainPin, []byte(normalizeWS(text)))
	return invariantIDPrefix + hex.EncodeToString(h[:])[:invariantIDHexLen]
}

// normalizeWS collapses every run of unicode.IsSpace to a single U+0020 and trims both ends. It
// is unicode-aware rather than ASCII-only (strings.Fields would do the same job, at the cost of
// an intermediate slice per call) because a non-breaking space pasted from a document is exactly
// the invisible difference that would otherwise fork one invariant into two.
func normalizeWS(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			pendingSpace = true
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

// Open returns the pins.Store rooted at root, wired with a no-op logger, a fresh metrics registry
// and the real clock.
//
// It keeps its single argument deliberately. internal/pins/pinstest — the conformance suite every
// implementation must pass — may import only this package, testutil and core
// (00-ARCHITECTURE.md §3.2), so it cannot construct a logging.Logger, an obs.Registry or a
// core.Clock to hand in. Open is the door that needs none of them; OpenWith is the one every
// composition root that HAS them should use, so that a store's warnings and its pins.badline
// counter reach the same day log and the same registry as everything else in the process.
//
// Unlike store.Open it takes no config.Config: nothing pins.Store does is config-tunable.
func Open(root string) (Store, error) {
	clk := core.SystemClock()
	return OpenWith(root, logging.Nop(), obs.New(clk), clk)
}

// OpenWith returns the pins.Store rooted at root, wired to the caller's own observability seams
// and clock (§2 of plans/V4-SP-10-checkpointer-l4.md).
//
// It creates <root>/.qompack/pins at 0700 — matching paths.EnsureLayout, and done here because
// the L0 hooks that pin an invariant can run before anything has laid out the store — then
// replays pins/invariants.jsonl ONCE into memory. Every later mutation is guarded by a mutex and
// updates the log and that in-memory set together, so All never re-reads the file.
//
// A log that cannot be read is an error rather than an empty store: reporting "no pins" for a
// file that exists but failed to open would let the next checkpoint drop tier-1 content silently,
// which is the one outcome this seam exists to prevent. A log that is merely ABSENT is not an
// error — that is just a project that has pinned nothing yet.
func OpenWith(root string, log logging.Logger, m obs.Registry, clk core.Clock) (Store, error) {
	l := paths.Of(root)
	if err := os.MkdirAll(paths.Long(l.Pins), dirPerm); err != nil {
		return nil, fmt.Errorf("pins: creating %s: %w", l.Pins, err)
	}

	s := &pinStore{
		l:    l,
		log:  log,
		m:    m,
		clk:  clk,
		live: make(map[string]Invariant),
		seen: make(map[string]bool),
	}
	if err := s.replay(); err != nil {
		return nil, err
	}
	return s, nil
}
