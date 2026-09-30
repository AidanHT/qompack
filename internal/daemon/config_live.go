package daemon

import (
	"reflect"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/redact"
)

// The daemon's live configuration (V6 close-out D49).
//
// A config reload used to replace the daemon's own copy of the configuration and nothing else. Every
// service had been handed a copy at wiring time — the rehydrator, the MCP tools, the scheduler, the
// checkpoint seam, the elimination ledger — so a reload logged "config reloaded changed=[...]" and
// the service that read the key went on with the value it started with until a restart (UAT-05:
// runtime.rehydrate.maxTokens; UAT-09: eliminations.staleResponse). liveConfig is the one cell the
// daemon and every service it wires read instead: reload.go stores into it, and a service reads it
// at the moment it uses a key. A key a running daemon cannot apply is never stored into it — the cell
// keeps the value in effect and the reload says the key needs a restart (reload_keys.go).

// liveConfig is the daemon's current configuration, shared by pointer between the Options its
// wiring holds and the daemon New constructs from a copy of them.
type liveConfig struct {
	mu  sync.RWMutex
	cfg config.Config
	set bool
}

// load returns the current configuration, and false while nothing has seeded the cell.
func (c *liveConfig) load() (config.Config, bool) {
	if c == nil {
		return config.Config{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cfg, c.set
}

// seed sets the configuration the daemon starts with, unless something already did. New seeds it
// from Options.Cfg as it stands when the daemon is constructed, which is after every wiring function
// and every test has finished adjusting it.
func (c *liveConfig) seed(cfg config.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.set {
		c.cfg, c.set = cfg, true
	}
}

// store replaces the configuration: a reload's result.
func (c *liveConfig) store(cfg config.Config) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cfg, c.set = cfg, true
}

// ensureLiveConfig creates the live cell on an Options built as a literal. It runs at wiring time,
// on the goroutine that owns the Options and before New copies them, so a service a wiring function
// builds reads the same cell the daemon's reload writes. NewOptions creates it already.
func (o *Options) ensureLiveConfig() {
	if o.live == nil {
		o.live = &liveConfig{}
	}
}

// CurrentCfg returns the configuration in effect: the daemon's live configuration once New has
// seeded it, Options.Cfg before that. A service reads it at the moment it uses a key, never once at
// wiring, so a key the daemon's reload applies reaches it without a restart. A composition root
// hands the method value (opts.CurrentCfg) to anything it wires outside this package.
func (o *Options) CurrentCfg() config.Config {
	if cfg, ok := o.live.load(); ok {
		return cfg
	}
	return o.Cfg
}

// liveRedactor is a redact.Redactor that applies runtime.redact as the live configuration has it
// now. It rebuilds its rules only when that block changes, so the per-call cost is one comparison.
// The store's Put and argument-preview redaction and the retrieval tools' re-check both use one, so a
// redaction rule a reload adds binds the next capture and the next retrieval, as the daemon's capture
// admission (capturePolicies) already did.
type liveRedactor struct {
	cfg func() config.Config

	mu    sync.Mutex
	built bool
	key   config.RedactCfg
	r     redact.Redactor
}

// NewLiveRedactor returns a redactor over cfg's runtime.redact block as it stands at each call.
func NewLiveRedactor(cfg func() config.Config) redact.Redactor {
	return &liveRedactor{cfg: cfg}
}

func (l *liveRedactor) current() redact.Redactor {
	c := l.cfg()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.built || !reflect.DeepEqual(l.key, c.Runtime.Redact) {
		l.r, l.key, l.built = redact.New(c), c.Runtime.Redact, true
	}
	return l.r
}

// Redact applies the current rules.
func (l *liveRedactor) Redact(in []byte) ([]byte, []redact.Match) { return l.current().Redact(in) }

// Rules names the current rules.
func (l *liveRedactor) Rules() []string { return l.current().Rules() }
