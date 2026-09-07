package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The expansion Promoter (Qompack.md §8.7, third bullet).
//
// "Repeated expansion of the same hash within a session is a signal, not a cost." Content the
// model keeps asking back is content eager restoration should have included, so the count is kept
// and, at retrieval.promoteAfterExpansions, the hash is marked promoted. SP-16 consumes
// Promoted() into the next checkpoint's pointer tier with a higher slice weight — a demand-driven
// correction to the 8–12K rehydration budget (§10 Phase 7). SP-13 counts and exposes; it does not
// promote anything itself.
//
// The counts are PERSISTED rather than in-memory because the process that observes the expansions
// is not the process that writes the next checkpoint, and because the daemon may exit on idle
// between the two. A count that died with the process would make the signal unobservable in
// exactly the sessions long enough to need it.

// promStateVersion is the on-disk schema version of promotions.json.
const promStateVersion = 1

// promotionsFileName is promotions.json's basename under .qompack/state/.
const promotionsFileName = "promotions.json"

// promStatePerm is the mode promotions.json is written with: owner-only, like every other file
// under .qompack/.
const promStatePerm = 0o600

// promState is promotions.json's whole content.
type promState struct {
	Version   int                     `json:"version"`
	Threshold int                     `json:"threshold"`
	Sessions  map[string]*promSession `json:"sessions"`
}

// promSession is one session's expansion counts and its promotion list.
type promSession struct {
	// Counts maps "sha256:<hex>" to how many times that hash has been re-materialized.
	Counts map[string]int `json:"counts"`
	// Promoted is the promotion order, deduplicated: the sequence SP-16 reads.
	Promoted []string `json:"promoted"`
	Updated  int64    `json:"updated"`
}

// promoter is the file-backed Promoter.
type promoter struct {
	mu        sync.Mutex
	path      string
	threshold int
	clk       core.Clock
	log       logging.Logger
	st        promState
}

// PromoterOptions configures NewPromoterWithOptions. It exists so a composition root can hand the
// promoter a logger without changing NewPromoter's shape, which the plan pins.
type PromoterOptions struct {
	// StatePath is the promotions.json to read and write; "" is an error.
	StatePath string
	// Threshold is retrieval.promoteAfterExpansions; below 1 is an error.
	Threshold int
	// Clock stamps the Updated field; nil means the system clock.
	Clock core.Clock
	// Log receives the quarantine notice for a corrupt state file; nil means logging.Nop().
	Log logging.Logger
}

// PromotionsPath returns the promotions.json a project's promoter reads and writes.
func PromotionsPath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).State, promotionsFileName)
}

// NewPromoter returns a Promoter backed by statePath, promoting a hash once it has been expanded
// threshold times.
func NewPromoter(statePath string, threshold int, clk core.Clock) (Promoter, error) {
	return NewPromoterWithOptions(PromoterOptions{StatePath: statePath, Threshold: threshold, Clock: clk})
}

// NewPromoterWithOptions returns a Promoter configured by o.
//
// A threshold below 1 is refused rather than clamped. config.Validate already enforces
// `promoteAfterExpansions >= 1`, so a zero reaching here means a caller bypassed configuration,
// and silently reading it as "promote everything on first sight" would turn the whole pointer
// tier into a copy of the retrieval log.
func NewPromoterWithOptions(o PromoterOptions) (Promoter, error) {
	if o.StatePath == "" {
		return nil, errors.New("qompack: mcp: a promoter needs a state path")
	}
	if o.Threshold < 1 {
		return nil, fmt.Errorf("qompack: mcp: promotion threshold must be at least 1, got %d", o.Threshold)
	}
	clk := o.Clock
	if clk == nil {
		clk = core.SystemClock()
	}
	log := o.Log
	if log == nil {
		log = logging.Nop()
	}
	p := &promoter{path: o.StatePath, threshold: o.Threshold, clk: clk, log: log}
	p.st = p.load()
	return p, nil
}

// load reads promotions.json, treating a missing file as an empty state and quarantining a
// corrupt one.
//
// §12.3's doctrine is to fail toward doing nothing: a state file this build cannot parse is moved
// aside, logged Loud, and replaced with an empty one, so the next expansion starts counting again
// rather than the tool refusing to run. The corrupt bytes are KEPT, under tmp/quarantine/, because
// they are the only evidence of whatever wrote them.
func (p *promoter) load() promState {
	empty := promState{Version: promStateVersion, Threshold: p.threshold, Sessions: map[string]*promSession{}}

	b, err := paths.ReadFileShared(p.path)
	if err != nil {
		return empty
	}
	var st promState
	if uerr := json.Unmarshal(b, &st); uerr != nil || st.Version != promStateVersion {
		p.quarantine(b)
		// Replace the unreadable file NOW rather than waiting for the first expansion to overwrite
		// it. A session that quarantines and then never expands anything leaves the corrupt bytes
		// in place, so the next process start quarantines the SAME bytes again — an unbounded pile
		// of identical copies under tmp/quarantine/ and one Loud line per restart for a fault that
		// was already reported once. It also keeps the write-set guard's "tmp/ is empty once every
		// write has landed" invariant reachable.
		//
		// persistLocked documents that the caller holds p.mu. It does, vacuously: load runs inside
		// the constructor, before the promoter has been returned to anyone, so no other goroutine
		// can hold a reference to it yet.
		p.st = empty
		if perr := p.persistLocked(); perr != nil {
			p.log.Warn("mcp: could not replace the quarantined promotions.json", "err", perr.Error())
		}
		return empty
	}
	if st.Sessions == nil {
		st.Sessions = map[string]*promSession{}
	}
	st.Threshold = p.threshold
	return st
}

// quarantine moves an unparseable state file aside and says so loudly.
func (p *promoter) quarantine(b []byte) {
	dir := filepath.Join(filepath.Dir(filepath.Dir(p.path)), "tmp", "quarantine")
	name := "promotions-" + strconv.FormatInt(p.clk.Now().UnixNano(), 10) + ".json"
	dest := filepath.Join(dir, name)

	if err := os.MkdirAll(paths.Long(dir), 0o700); err != nil {
		p.log.Loud("mcp: could not quarantine a corrupt promotions.json", "err", err.Error())
		return
	}
	if err := paths.WriteAtomic(dest, b, promStatePerm); err != nil {
		p.log.Loud("mcp: could not quarantine a corrupt promotions.json", "err", err.Error())
		return
	}
	p.log.Loud("mcp: promotions.json was unreadable and has been quarantined; expansion counts reset",
		"path", p.path, "quarantined", dest)
}

// NoteExpansion records one expansion of h in sess and reports the running count and whether that
// count has reached the promotion threshold.
//
// promoted is `count >= threshold`, not `count == threshold`: the caller is reporting the state of
// a hash, not an edge, and a third expansion of an already-promoted hash is still an expansion of
// a promoted hash. The Promoted LIST, by contrast, gains an entry exactly once.
func (p *promoter) NoteExpansion(_ context.Context, sess core.SessionID, h core.Hash) (int, bool, error) {
	if h.IsZero() {
		return 0, false, nil
	}
	key := h.String()

	p.mu.Lock()
	defer p.mu.Unlock()

	s := p.st.Sessions[string(sess)]
	if s == nil {
		s = &promSession{Counts: map[string]int{}}
		p.st.Sessions[string(sess)] = s
	}
	if s.Counts == nil {
		s.Counts = map[string]int{}
	}
	s.Counts[key]++
	count := s.Counts[key]
	promoted := count >= p.threshold
	if count == p.threshold {
		s.Promoted = append(s.Promoted, key)
	}
	s.Updated = p.clk.Now().UnixMilli()

	return count, promoted, p.persistLocked()
}

// Promoted returns every hash promoted in sess, in promotion order.
func (p *promoter) Promoted(_ context.Context, sess core.SessionID) ([]core.Hash, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	s := p.st.Sessions[string(sess)]
	if s == nil {
		return nil, nil
	}
	out := make([]core.Hash, 0, len(s.Promoted))
	for _, key := range s.Promoted {
		h, err := core.ParseHash(key)
		if err != nil {
			// A key that will not parse is a file someone edited by hand. Skip it rather than
			// failing the whole read: the other promotions are still valid signals.
			p.log.Warn("mcp: promotions.json holds an unparseable hash", "key", key)
			continue
		}
		out = append(out, h)
	}
	return out, nil
}

// persistLocked writes the state atomically. The caller holds p.mu.
//
// A write failure is RETURNED rather than swallowed, and the handler that called NoteExpansion
// logs it and answers anyway: counting is advisory, so a full disk must cost the signal and not
// the retrieval.
func (p *promoter) persistLocked() error {
	b, err := json.MarshalIndent(p.st, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: marshalling %s: %w", p.path, err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(paths.Long(filepath.Dir(p.path)), 0o700); err != nil {
		return fmt.Errorf("mcp: mkdir for %s: %w", p.path, err)
	}
	if err := paths.WriteAtomic(p.path, b, promStatePerm); err != nil {
		return fmt.Errorf("mcp: writing %s: %w", p.path, err)
	}
	return nil
}
