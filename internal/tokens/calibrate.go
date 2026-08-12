package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// identityFactor is Factor's starting value: an uncalibrated estimator is the identity, so every
// Estimate is exactly raw(b, c) with no scaling. This is what makes the byte-arithmetic in the
// estimate tests exact.
const identityFactor = 1.0

// calibFilePerm is the permission WriteAtomic applies to the calibration file.
const calibFilePerm = 0o600

// estimator is the concrete Estimator. calibPath, if non-empty, is the file Calibrate persists to
// and New reads from at construction; an empty calibPath makes calibration purely in-memory
// (useful for tests and for any caller that has not resolved a home directory yet).
type estimator struct {
	cfg       config.RTokensCfg
	calibPath string
	// calibScope is the string the persisted entry is keyed by. It is the project root when the
	// caller supplied one, and the calibration file's own path otherwise. See NewForProject.
	calibScope string

	mu     sync.RWMutex
	factor float64

	rootMu    sync.Mutex
	rootCache map[core.Hash]core.Tokens
}

// New returns an Estimator reading its constants from cfg.Runtime.Tokens, with calibration scoped
// to calibPath itself. This is the frozen 00-ARCHITECTURE.md §5.20 signature.
//
// Prefer NewForProject. §5.20's prose requires a per-PROJECT entry keyed by
// sha256(projectRoot).Short() inside the single shared paths.Global(home)/calibration.json, and
// this signature has no project-root parameter to supply it. Passing the shared global path to New
// therefore makes every project on the machine share one calibration entry, which is precisely the
// per-project isolation §5.20 exists to provide (a Go-heavy project and a prose-heavy one have
// genuinely different characters-per-token). New is kept because §5.20 fixes it and callers that
// legitimately want file-scoped calibration — tests, and any caller with a per-project calibPath —
// are correct to use it.
func New(cfg config.Config, calibPath string) Estimator {
	return newEstimator(cfg, calibPath, calibPath)
}

// NewForProject returns an Estimator whose persisted calibration entry is keyed by projectRoot,
// so one shared paths.Global(home)/calibration.json holds a distinct factor per project exactly
// as §5.20 describes.
//
// This is an SP-01 addition, not a change: §5.20's New keeps its frozen signature and delegates
// here. Adding to an interface the adding subplan owns is legal under §0, and §14.0 already does
// the same thing for obs.Registry.Persist. The composition roots (cli, daemon) call this one with
// the resolved project root; an empty projectRoot falls back to keying by calibPath.
func NewForProject(cfg config.Config, calibPath, projectRoot string) Estimator {
	scope := projectRoot
	if scope == "" {
		scope = calibPath
	}
	return newEstimator(cfg, calibPath, scope)
}

// newEstimator builds an estimator and seeds Factor from any previously persisted entry for scope.
func newEstimator(cfg config.Config, calibPath, scope string) Estimator {
	e := &estimator{
		cfg:        cfg.Runtime.Tokens,
		calibPath:  calibPath,
		calibScope: scope,
		factor:     identityFactor,
		rootCache:  make(map[core.Hash]core.Tokens),
	}
	if calibPath != "" {
		if m, err := loadCalibFile(calibPath); err == nil {
			if f, ok := m[calibKey(scope)]; ok {
				e.factor = f
			}
		}
	}
	return e
}

// Factor returns the current calibration factor.
func (e *estimator) Factor() float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.factor
}

// Calibrate updates factor = clamp(factor*(1-alpha) + alpha*observed/estimated, calibrationMin,
// calibrationMax), with alpha = cfg.Runtime.tokens.calibrationAlpha, then persists the new factor
// to calibPath (if set) via paths.WriteAtomic. Calls where estimated == 0 are ignored entirely:
// no factor update, no write.
func (e *estimator) Calibrate(observed, estimated core.Tokens) {
	if estimated == 0 {
		return
	}
	ratio := float64(observed) / float64(estimated)

	e.mu.Lock()
	next := e.factor*(1-e.cfg.CalibrationAlpha) + e.cfg.CalibrationAlpha*ratio
	next = clampFactor(next, e.cfg.CalibrationMin, e.cfg.CalibrationMax)
	e.factor = next
	e.mu.Unlock()

	if e.calibPath == "" {
		return
	}
	_ = e.persist(next) // best-effort: Calibrate has no error return to propagate a write failure
}

func clampFactor(v, lo, hi float64) float64 {
	switch {
	case lo <= hi && v < lo:
		return lo
	case lo <= hi && v > hi:
		return hi
	default:
		return v
	}
}

// calibFile is the on-disk shape of a calibration file: a flat map from calibKey(scope) to
// factor, so the single shared global file holds one entry per project.
type calibFile map[string]float64

func loadCalibFile(p string) (calibFile, error) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return nil, err
	}
	var m calibFile
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// calibKey derives the stable, deterministic key one scope's factor is stored under: the first 12
// hex characters of sha256(scope), reusing core.Hash.Short()'s own convention rather than
// re-implementing it. scope is the project root under NewForProject, matching §5.20's
// sha256(projectRoot).Short().
func calibKey(scope string) string {
	return core.HashBytes(calibKeyDomain, []byte(filepath.Clean(scope))).Short()
}

// calibKeyDomain domain-separates the calibration key from every other HashBytes use in the
// codebase (core/hash.go's domain registry). It is deliberately NOT one of the registry's five
// production domains: those key content the store persists, and a calibration key never reaches
// the store.
const calibKeyDomain = "qompack.tokens.calib.v1"

func (e *estimator) persist(factor float64) error {
	m, err := loadCalibFile(e.calibPath)
	if err != nil {
		m = calibFile{}
	}
	if m == nil {
		m = calibFile{}
	}
	m[calibKey(e.calibScope)] = factor

	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(e.calibPath, b, calibFilePerm)
}
