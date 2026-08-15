package tokens

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// identityFactor is Factor's starting value: an uncalibrated estimator is the identity, so every
// Estimate is exactly raw(b, c) with no scaling. This is what makes the arithmetic in the estimate
// tests exact.
const identityFactor = 1.0

// calibWarmupSamples is how many usable Calibrate observations must accumulate before Factor
// leaves the identity.
//
// The warm-up exists because the EWMA is seeded with the FIRST ratio it sees. Without it, a single
// unlucky early observation — a turn whose usage delta happened to include a cache write, say —
// would immediately move every estimate in the session. Five samples is enough to be past that
// without being so many that a genuinely mis-calibrated project stays wrong for a whole session.
const calibWarmupSamples = 5

// calibFilePerm is the permission WriteAtomic applies to the calibration file. It lives outside
// the project and holds nothing secret, but user-global state is not world-readable.
const calibFilePerm = 0o600

// calibState is the calibration half of the exact estimator: the EWMA over observed/estimated
// ratios, its warm-up counter, and the document it persists to.
type calibState struct {
	cfg       config.RTokensCfg
	log       logging.Logger
	calibPath string
	// scope is the string the persisted entry is keyed by: the project root under NewForProject,
	// the calibration file's own path otherwise.
	scope string

	mu      sync.RWMutex
	ewma    float64
	samples int
}

// init seeds the calibration state from any previously persisted entry for scope.
func (c *calibState) init(cfg config.RTokensCfg, calibPath, scope string, log logging.Logger) {
	c.cfg, c.calibPath, c.scope, c.log = cfg, calibPath, scope, log

	if calibPath == "" {
		return
	}
	entry, ok, err := loadCalibEntry(calibPath, calibKey(scope))
	switch {
	case err != nil:
		// A missing file is the normal first-run case and says nothing; a file that exists but
		// cannot be parsed is worth one loud line, because it means a calibrated project silently
		// reverted to the identity.
		if !os.IsNotExist(err) {
			c.log.Loud("tokens: calibration file unreadable, starting from the identity factor",
				"path", calibPath, "err", err)
		}
	case ok:
		c.ewma, c.samples = entry.EWMA, entry.Samples
	}
}

// Factor returns the current calibration factor: the identity until the warm-up threshold is met,
// and the clamped EWMA thereafter (00-ARCHITECTURE.md §5.20).
func (e *exact) Factor() float64 { return e.calib.factor() }

func (c *calibState) factor() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.samples < calibWarmupSamples {
		return identityFactor
	}
	return clampFactor(c.ewma, c.cfg.CalibrationMin, c.cfg.CalibrationMax)
}

// Calibrate nudges the factor toward observed/estimated by one exponential-moving-average step and
// persists the result. Non-positive arguments are ignored entirely: no sample counted, no step
// taken, no write — a zero estimate carries no information about the estimator's accuracy.
func (e *exact) Calibrate(observed, estimated core.Tokens) { e.calib.calibrate(observed, estimated) }

func (c *calibState) calibrate(observed, estimated core.Tokens) {
	if observed <= 0 || estimated <= 0 {
		return
	}
	ratio := float64(observed) / float64(estimated)

	c.mu.Lock()
	if c.samples == 0 {
		c.ewma = ratio // seed with the first observation rather than with the identity
	} else {
		alpha := c.cfg.CalibrationAlpha
		c.ewma = c.ewma*(1-alpha) + alpha*ratio
	}
	c.samples++
	next := identityFactor
	if c.samples >= calibWarmupSamples {
		next = clampFactor(c.ewma, c.cfg.CalibrationMin, c.cfg.CalibrationMax)
	}
	ewma, samples := c.ewma, c.samples
	c.mu.Unlock()

	if c.calibPath == "" {
		return
	}
	// Best-effort: Calibrate has no error return to propagate a write failure through.
	_ = c.persist(next, ewma, samples)
}

// clampFactor bounds v to [lo, hi], leaving it untouched if the bounds are themselves inverted.
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

// calibEntry is one project's calibration state.
type calibEntry struct {
	Factor  float64 `json:"factor"`
	EWMA    float64 `json:"ewma"`
	Samples int     `json:"samples"`
	Updated int64   `json:"updated"`
}

// versionedCalibFile is the richer calibration document shape: it carries the EWMA and the sample
// count, which the flat shape cannot express.
type versionedCalibFile struct {
	Version  int                   `json:"version"`
	Projects map[string]calibEntry `json:"projects"`
}

// flatCalibFile is the calibration document this package WRITES: a flat map from calibKey(scope)
// to the effective factor, so one shared file holds one entry per project.
//
// The flat shape is the written one, and deliberately so. It is what every existing reader of this
// file expects, and keeping it means a calibration document stays a trivially inspectable
// {project: factor} map. The loader below additionally accepts the versioned shape, so a document
// that carries the EWMA and sample count round-trips through this process without losing them;
// what it cannot do is persist them, which costs exactly one warm-up period after a restart.
type flatCalibFile map[string]float64

// loadCalibEntry reads key's entry from p, accepting either document shape.
func loadCalibEntry(p, key string) (calibEntry, bool, error) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return calibEntry{}, false, err
	}

	var versioned versionedCalibFile
	if err := json.Unmarshal(b, &versioned); err == nil && versioned.Projects != nil {
		e, ok := versioned.Projects[key]
		return e, ok, nil
	}

	var flat flatCalibFile
	if err := json.Unmarshal(b, &flat); err != nil {
		return calibEntry{}, false, err
	}
	f, ok := flat[key]
	if !ok {
		return calibEntry{}, false, nil
	}
	// A flat entry records only the settled factor, so it is read back as an already-warmed-up
	// estimator sitting exactly there.
	return calibEntry{Factor: f, EWMA: f, Samples: calibWarmupSamples}, true, nil
}

// persist writes this scope's factor into the shared document, preserving every other entry.
func (c *calibState) persist(factor, _ float64, _ int) error {
	m := flatCalibFile{}
	if b, err := os.ReadFile(paths.Long(c.calibPath)); err == nil {
		var versioned versionedCalibFile
		if uerr := json.Unmarshal(b, &versioned); uerr == nil && versioned.Projects != nil {
			for k, e := range versioned.Projects {
				m[k] = e.Factor
			}
		} else {
			_ = json.Unmarshal(b, &m)
		}
	}
	if m == nil {
		m = flatCalibFile{}
	}
	m[calibKey(c.scope)] = factor

	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return paths.WriteAtomic(c.calibPath, b, calibFilePerm)
}

// calibKey derives the stable, deterministic key one scope's factor is stored under: the first 12
// hex characters of the domain-separated digest of the cleaned scope, reusing core.Hash.Short()'s
// own convention rather than re-implementing it. scope is the project root under NewForProject,
// matching §5.20's sha256(projectRoot).Short().
func calibKey(scope string) string {
	return core.HashBytes(calibKeyDomain, []byte(filepath.Clean(scope))).Short()
}

// calibKeyDomain domain-separates the calibration key from every other HashBytes use in the
// codebase (core/hash.go's domain registry). It is deliberately NOT one of the registry's five
// production domains: those key content the store persists, and a calibration key never reaches
// the store. It is equally deliberately not "qompack.project.v1" — core/hash.go documents at
// length why that domain does not exist and must not be introduced.
const calibKeyDomain = "qompack.tokens.calib.v1"
