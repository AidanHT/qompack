package canon

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/sketch"
)

// Registry holds a set of Canonicalizers and dispatches a tool's output through whichever of them
// apply, in registration order (00-ARCHITECTURE.md §5.6).
type Registry interface {
	// Register adds c to the registry; it errors on a duplicate Name.
	Register(c Canonicalizer) error
	// For returns every registered Canonicalizer that applies to tool's output at path, in
	// registration order.
	For(tool, path string) []Canonicalizer
	// Run applies every applicable Canonicalizer to in, in registration order, and returns the
	// combined Result.
	Run(tool, path string, in []byte, o Options) (Result, error)
	// Names returns every registered Canonicalizer's Name, in registration order.
	Names() []string
}

// NewRegistry returns an empty Registry.
//
// The returned value is safe for concurrent use: SP-05's daemon serves several sessions from one
// process and SP-06's store holds a single Registry for the life of the daemon, so Run is called
// from many goroutines while Register may still be adding a project-specific canonicalizer. The
// lock is a read lock on the Run path, which costs tens of nanoseconds against a budget measured
// in milliseconds (Qompack.md §8.1).
func NewRegistry() Registry { return &registry{} }

// registry is the concrete Registry. canons is append-only in registration order, and byName is
// the duplicate-Name index §5.6 requires Register to enforce.
type registry struct {
	mu     sync.RWMutex
	canons []Canonicalizer
	byName map[string]bool
}

// Register adds c under its Name, reporting ErrDuplicateName if that Name is already taken.
//
// A nil Canonicalizer is a documented no-op returning nil rather than an error. Two reasons: there
// is nothing to add and no Name to collide with, so refusing would report a problem that does not
// exist; and test/guards' TestAllStubsReturnNotImplemented reflectively calls every
// error-returning method with zero-valued arguments and accepts only nil or core.ErrNotImplemented
// back, so any other error here would fail a guard that has nothing to do with canonicalization.
func (r *registry) Register(c Canonicalizer) error {
	if c == nil {
		return nil
	}
	name := c.Name()

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byName == nil {
		r.byName = make(map[string]bool)
	}
	if r.byName[name] {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	r.byName[name] = true
	r.canons = append(r.canons, c)
	return nil
}

// For returns the applicable Canonicalizers in registration order.
func (r *registry) For(tool, path string) []Canonicalizer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]Canonicalizer, 0, len(r.canons))
	for _, c := range r.canons {
		if c.Applies(tool, path) {
			out = append(out, c)
		}
	}
	return out
}

// Names returns every registered Name in registration order.
func (r *registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]string, len(r.canons))
	for i, c := range r.canons {
		out[i] = c.Name()
	}
	return out
}

// Run canonicalizes in as tool's output at path.
//
// Composition is a SINGLE PASS over the original input, not a chain of byte transforms. Every
// applicable Canonicalizer reports the spans it would rewrite, all of those spans are expressed
// in one coordinate system, and Run resolves the overlaps once. Chaining would have made
// Delta offsets ambiguous — each stage shifts the previous stage's coordinates — would have made
// Restore depend on the order stages ran in, and would have forced every implementation to
// re-derive the non-growth rule for itself. See acceptCandidates in apply.go for the four
// rejections that make Options.Strip gating and 00-ARCHITECTURE.md §5.6's non-growth property
// structural rather than conventional.
//
// A Canonicalizer that does not implement Matcher contributes nothing and never appears in
// Result.Applied; see ErrNotMatcher for why that is a skip rather than a failure.
func (r *registry) Run(tool, path string, in []byte, o Options) (Result, error) {
	if err := validateStrip(o.Strip); err != nil {
		return Result{}, err
	}
	if len(in) == 0 {
		return Result{Applied: []string{}}, nil
	}

	head, _ := splitCapped(in)
	cands := r.collect(tool, path, head, o)

	canonical, deltas, accepted := compose(in, cands, o)
	res := Result{
		Canonical: canonical,
		Deltas:    deltas,
		Applied:   appliedNames(accepted),
		Reduced:   reduced(len(in), len(canonical)),
	}
	if o.MinHash.Enabled {
		res.Signature = sketch.MinHash(res.Canonical, o.MinHash)
	}
	return res, nil
}

// collect gathers every applicable Canonicalizer's matches over head, tagged with the emitter's
// registration index and Name so overlap resolution and Applied stay deterministic.
func (r *registry) collect(tool, path string, head []byte, o Options) []candidate {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var cands []candidate
	for i, c := range r.canons {
		if !c.Applies(tool, path) {
			continue
		}
		found, err := MatchesOf(c, head, o)
		if errors.Is(err, ErrNotMatcher) {
			continue
		}
		name := c.Name()
		for _, m := range found {
			cands = append(cands, candidate{Match: m, rank: i, owner: name})
		}
	}
	return cands
}

// validateStrip rejects an Options.Strip naming a Class that is not one of KnownClasses.
//
// This is a hard failure rather than a silent skip because a typo in store.canonicalize.strip
// silently disables a canonicalizer, and the only symptom is a dedup ratio that is quietly worse
// than it should be — a bug with no error message and a months-long feedback loop.
func validateStrip(strip []Class) error {
	for _, c := range strip {
		if _, ok := ParseClass(string(c)); !ok {
			return fmt.Errorf("%w: %q (known: %s)", ErrUnknownClass, c, strings.Join(classNames(), ", "))
		}
	}
	return nil
}

// classNames renders KnownClasses for an error message.
func classNames() []string {
	out := make([]string, len(knownClasses))
	for i, c := range knownClasses {
		out[i] = string(c)
	}
	return out
}
