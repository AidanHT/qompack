package rehydrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// Reporter persists the last rehydration's drop report so that it outlives the injection itself.
//
// The payload's own section 7 is budgeted and may be truncated to a counted line; this file never
// is. That is the difference between "the agent can see what it lost" and "the agent can ask what
// it lost": the `dropped` retrieval tool reads CurrentDrops, and the complete list is what makes
// its answer true (Qompack.md §8.6 item 7, G4.5).
//
// Reporter also structurally satisfies mcp.DropReporter, which is the interface the `dropped` tool
// is constructed against. That assertion cannot live in this package — rehydrate may not import
// mcp (00-ARCHITECTURE.md §3.2) — so it lives in test/e2e, which is outside internal/ and may
// import both.
type Reporter interface {
	// CurrentDrops returns the complete drop report of sess's last rehydration. A session that has
	// never been rehydrated, or whose state file is unreadable, reports (nil, nil): "nothing has
	// been dropped yet" is an answer, not an error, and the `dropped` tool renders it as an empty
	// list.
	CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error)
	// Record replaces sess's state file with st.
	Record(ctx context.Context, sess core.SessionID, st State) error
	// Reset deletes sess's state file. It backs the SessionStart source=clear branch, so that the
	// next compact injection is a fresh full payload rather than one carrying a stale drop report.
	Reset(ctx context.Context, sess core.SessionID) error
}

// State is one rehydration's persisted record (§8.6, G4.5).
//
// There is deliberately no "sentinel" key. The §12.1 hook.additional_context_delivered probe is
// SP-05's shipped contract.MintSentinel/RenderSentinel mechanism: the daemon mints the token on
// the session.start route, appends contract.RenderSentinel after the injection's close tag, and
// scans the transcript tail for it. Nothing reads a rehydrate state file to find it, and nothing
// recomputes it from (session, seq). This slice mints no sentinel of its own.
type State struct {
	Session core.SessionID     `json:"session"`
	Seq     core.CheckpointSeq `json:"seq"`
	// Emitted is the wall-clock instant the payload was produced. It is the ONLY wall-clock value
	// in L5, and it is stamped by the daemon service from RehydrateOptions.Clock — Build itself
	// reads no clock, which is what lets PropBuild_Deterministic demand byte-identical output.
	Emitted  core.UnixMilli         `json:"emitted"`
	Tokens   core.Tokens            `json:"tokens"`
	Budget   core.Tokens            `json:"budget"`
	Items    []ItemStat             `json:"items"`
	Dropped  []checkpoint.DropEntry `json:"dropped"`
	Degraded bool                   `json:"degraded"`
	// DegradedReason is Result.DegradedReason: why a degraded rehydration is degraded, when the
	// drop report alone would not make it plain (a checkpoint fallback, D49).
	DegradedReason string `json:"degraded_reason,omitempty"`
}

// ItemStat is one emitted Item's accounting row.
//
// There is no synthetic "overhead" row. Result.Tokens is the estimator's price for the COMPLETE
// assembled payload (wrapper and inter-section separators included), and each row's Tokens is an
// ACCOUNTING ALLOCATION of that single number — the rows sum to the total exactly, but a row is not
// the additive tokenization of its own bytes (V6 §5, inventory 1.6.18). An independent overhead row
// would make the sum disagree with the total by construction, which the inherited conformance case
// total_tokens_never_exceed_the_budget rejects at every budget.
type ItemStat struct {
	Kind      string      `json:"kind"`
	Rank      int         `json:"rank"`
	Tokens    core.Tokens `json:"tokens"`
	Truncated bool        `json:"truncated"`
	// Units is how many candidate units were admitted; UnitsSeen is how many existed before
	// budgeting. SP-16 reads the pair to tune the tier boundaries from replay evidence.
	Units     int `json:"units"`
	UnitsSeen int `json:"units_seen"`
}

// maxSessionFileRunes bounds the sanitized session component of a state file name. A session id is
// host-supplied, so it is untrusted input on a path.
const maxSessionFileRunes = 64

// reporter is the on-disk Reporter. The mutex serializes this process's own writes; it is not a
// cross-process lock, and does not need to be — one daemon owns one project root.
type reporter struct {
	root string
	log  logging.Logger

	mu sync.Mutex
	// loudOnce tracks which sessions have already had a corrupt-state-file Loud, so a repeatedly
	// polled `dropped` tool cannot turn one corrupt file into an unbounded log.
	loudOnce map[core.SessionID]struct{}
}

// NewReporter returns a Reporter persisting under <projectRoot>/.qompack/state.
func NewReporter(projectRoot string, log logging.Logger) Reporter {
	if log == nil {
		log = logging.Nop()
	}
	return &reporter{root: projectRoot, log: log, loudOnce: make(map[core.SessionID]struct{})}
}

// sanitizeSession renders sess safe to embed in a file name: every rune outside [A-Za-z0-9._-]
// becomes '_', and the result is truncated to maxSessionFileRunes runes.
//
// It is deliberately lossy and deliberately not reversible. The state file is looked up by the
// same session id that wrote it, so a collision between two sessions whose ids differ only in
// stripped characters costs one stale drop report; admitting a '/' or a ".." would cost a write
// outside .qompack/, which the security job forbids outright.
func sanitizeSession(sess core.SessionID) string {
	var b strings.Builder
	n := 0
	for _, r := range string(sess) {
		if n == maxSessionFileRunes {
			break
		}
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		n++
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// statePath is the state file for sess.
func (rp *reporter) statePath(sess core.SessionID) string {
	return filepath.Join(paths.Of(rp.root).State, "rehydrate-"+sanitizeSession(sess)+".json")
}

// Record writes st to sess's state file.
func (rp *reporter) Record(ctx context.Context, sess core.SessionID, st State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()

	// Marshal the zero-length slices as [] rather than null: the golden is byte-compared, and a
	// consumer that ranges over the result should not have to distinguish the two.
	if st.Items == nil {
		st.Items = []ItemStat{}
	}
	if st.Dropped == nil {
		st.Dropped = []checkpoint.DropEntry{}
	}

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("rehydrate: marshal state: %w", err)
	}
	b = append(b, '\n')

	p := rp.statePath(sess)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return fmt.Errorf("rehydrate: create state dir: %w", err)
	}
	if err := paths.WriteAtomic(p, b, 0o600); err != nil {
		return fmt.Errorf("rehydrate: write state: %w", err)
	}
	return nil
}

// CurrentDrops returns sess's persisted drop report.
func (rp *reporter) CurrentDrops(ctx context.Context, sess core.SessionID) ([]checkpoint.DropEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()

	p := rp.statePath(sess)
	b, err := paths.ReadFileShared(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// Never rehydrated. Not an error.
		return nil, nil
	case err != nil:
		rp.loudCorrupt(sess, "rehydrate: state file unreadable", err)
		return nil, nil
	}

	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		// A corrupt state file is a degraded observable, not a failure: report "nothing dropped",
		// say so loudly once, and delete it so the next build rewrites a good one. Leaving it in
		// place would make every later read repeat the same failure.
		rp.loudCorrupt(sess, "rehydrate: state file corrupt; deleting", err)
		if rmErr := os.Remove(paths.Long(p)); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			rp.log.Warn("rehydrate: could not delete corrupt state file", "path", p, "err", rmErr.Error())
		}
		return nil, nil
	}
	return st.Dropped, nil
}

// Reset deletes sess's state file. A missing file is success.
func (rp *reporter) Reset(ctx context.Context, sess core.SessionID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rp.mu.Lock()
	defer rp.mu.Unlock()

	if err := os.Remove(paths.Long(rp.statePath(sess))); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rehydrate: reset state: %w", err)
	}
	delete(rp.loudOnce, sess)
	return nil
}

// loudCorrupt emits at most one Loud per session for an unreadable or corrupt state file.
func (rp *reporter) loudCorrupt(sess core.SessionID, msg string, err error) {
	if _, done := rp.loudOnce[sess]; done {
		return
	}
	rp.loudOnce[sess] = struct{}{}
	rp.log.Loud(msg, "session", string(sess), "err", err.Error())
}

// itemStats projects the emitted Items onto their persisted rows. It is the one place Item and
// ItemStat are kept in step, and it emits exactly one row per emitted Item — no more, so that
// sum(rows.Tokens) == Result.Tokens holds in the state file exactly as it does in the Result.
func itemStats(items []Item, units, unitsSeen map[ItemKind]int) []ItemStat {
	out := make([]ItemStat, 0, len(items))
	for _, it := range items {
		out = append(out, ItemStat{
			Kind:      it.Kind.String(),
			Rank:      it.Rank,
			Tokens:    it.Tokens,
			Truncated: it.Truncated,
			Units:     units[it.Kind],
			UnitsSeen: unitsSeen[it.Kind],
		})
	}
	return out
}
