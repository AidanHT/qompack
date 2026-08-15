package daemon

import (
	"errors"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/sketch"
)

// The three sketch filenames under paths.Layout.Sketches that Load/Save read and write.
// tried.bloom is deliberately absent from this list: §3.3 makes it append-only, replaceable only
// by negknow.RebuildBloom, and Save must never touch it.
const (
	touchSketchFile   = "touch.cms"
	exploreSketchFile = "explore.hll"
	triedSketchFile   = "tried.bloom"
)

// SketchSet is the four probabilistic structures the daemon keeps resident (§3.3): the
// negative-knowledge Bloom filter, the touch Count-Min sketch, the exploration HyperLogLog and the
// Misra-Gries top-k counter.
//
// Tried is a pointer to the same Bloom the negknow ledger was opened over, not a copy. §7.4 makes
// tried.bloom append-only precisely because a rebuilt-from-summary Bloom would silently forget
// what has already been tried, so there must be exactly one of it in the process.
//
// Top is the companion to Touch, not an alternative to it: sketch.CMS.HeavyHitters takes a
// *MisraGries because a Count-Min sketch can estimate the count of a key it is handed but cannot
// enumerate which keys are heavy. Without Top resident there is no set of candidates to estimate,
// so the pair has to be wired together or the CMS answers a question nobody can ask.
//
// mu guards concurrent Read/Write access: the worker pool updates sketches from many goroutines,
// and Save reads them back for persistence, both while the set is live.
type SketchSet struct {
	mu sync.RWMutex

	Tried   *sketch.Bloom
	Touch   *sketch.CMS
	Explore *sketch.HLL
	Top     *sketch.MisraGries

	dirty bool
}

// NewSketchSet constructs a SketchSet sized from cfg.Sketches (Appendix A: bloom.capacity,
// bloom.fpRate, cms.epsilon, cms.delta, hll.registers) plus a fixed-capacity Misra-Gries top-k
// counter.
const misraGriesK = 64 //nomagic:allow Appendix A top-k counter size, not a config default

func NewSketchSet(cfg config.Config) *SketchSet {
	return &SketchSet{
		Tried:   sketch.NewBloom(cfg.Sketches.Bloom.Capacity, cfg.Sketches.Bloom.FPRate),
		Touch:   sketch.NewCMS(cfg.Sketches.CMS.Epsilon, cfg.Sketches.CMS.Delta),
		Explore: sketch.NewHLL(cfg.Sketches.HLL.Registers),
		Top:     sketch.NewMisraGries(misraGriesK),
	}
}

// Read runs fn with a read lock held, for a caller that only inspects the sketches (estimating a
// count, checking membership) without mutating them. fn must not call Read or Write again on the
// same SketchSet before returning — sync.RWMutex is not re-entrant, so a nested call deadlocks.
func (s *SketchSet) Read(fn func(*SketchSet)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(s)
}

// Write runs fn with a write lock held and marks the set dirty afterward, for a caller that
// mutates one or more sketches (recording a touch, adding to the Bloom filter). Save is a no-op
// until the set has been written to at least once since the last Save. fn must not call Read or
// Write again on the same SketchSet before returning (see Read's note on re-entrancy).
func (s *SketchSet) Write(fn func(*SketchSet)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s)
	s.dirty = true
}

// Load reads sketches/touch.cms, sketches/explore.hll and sketches/tried.bloom from root into an
// already-constructed set, replacing the in-memory sketch only on a successful decode.
//
// sketch.Load reports core.ErrNotImplemented until SP-03 lands its real encode/decode, and
// core.ErrNotFound for a project that has never persisted a sketch yet — both are expected in
// wave 1 and are logged at Debug rather than Loud: the daemon must run correctly with nothing but
// SP-03's stub. Any other error (a corrupt file, a bad CRC) is Loud'd, and the in-memory sketch —
// freshly constructed by NewSketchSet — is kept either way, so a load failure never leaves the
// daemon without a usable sketch.
func (s *SketchSet) Load(root string, log logging.Logger) {
	if log == nil {
		log = logging.Nop()
	}
	dir := paths.Of(root).Sketches

	s.mu.Lock()
	defer s.mu.Unlock()

	loadOne := func(name string, target sketch.Sketch) {
		p := filepath.Join(dir, name)
		err := sketch.Load(p, target)
		if err == nil {
			return
		}
		if errors.Is(err, core.ErrNotFound) || errors.Is(err, core.ErrNotImplemented) {
			log.Debug("daemon: sketch load skipped", "path", p, "err", err)
			return
		}
		log.Loud("daemon: sketch load failed — keeping the in-memory sketch", "path", p, "err", err)
	}

	loadOne(touchSketchFile, s.Touch)
	loadOne(exploreSketchFile, s.Explore)
	loadOne(triedSketchFile, s.Tried)
}

// Save writes sketches/touch.cms and sketches/explore.hll back to root, and is a no-op when the
// set has not been written to since the last Save. sketches/tried.bloom is never written here
// (§3.3: it may be replaced only by negknow.RebuildBloom) — Save skips it unconditionally, on
// every call, dirty or not.
func (s *SketchSet) Save(root string, log logging.Logger) {
	if log == nil {
		log = logging.Nop()
	}
	dir := paths.Of(root).Sketches

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return
	}

	saveOne := func(name string, src sketch.Sketch) {
		p := filepath.Join(dir, name)
		if err := sketch.Save(p, src); err != nil {
			if errors.Is(err, core.ErrNotImplemented) {
				log.Debug("daemon: sketch save skipped", "path", p, "err", err)
				return
			}
			log.Loud("daemon: sketch save failed", "path", p, "err", err)
		}
	}

	saveOne(touchSketchFile, s.Touch)
	saveOne(exploreSketchFile, s.Explore)
	s.dirty = false
}
