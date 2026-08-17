package cli

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// configViolationsFile is the §11.3 record of every leaf that fell back to its default, relative
// to the layout's state directory.
const configViolationsFile = "config-violations.json"

// LoadConfigAndReport is the single helper every composition root uses to load configuration.
//
// It exists because §11.3 splits one requirement across two packages that cannot see each other.
// config.Load performs the per-leaf fallback and returns the evidence as warnings, but it can
// neither log nor persist: §3.2 gives config the allow-set {core}, so it can reach neither
// logging (which imports config, making the reverse edge a cycle) nor paths. The reporting half
// therefore belongs to whoever called Load — and putting it here, rather than at each call site,
// is what stops a caller from forgetting it.
//
// Every violation is reported through logging.Loud (§12: nothing degrades silently) and the typed
// list is persisted to state/config-violations.json so /qompack:status and the next SessionStart
// both surface it. A failure to persist is itself logged but never propagated: a hook that dies
// over a diagnostic write has traded observability for nothing.
func LoadConfigAndReport(env config.Env, log logging.Logger, reg obs.Registry) (config.Config, config.Provenance, error) {
	cfg, prov, warns, err := config.Load(env)
	if err != nil {
		return cfg, prov, err
	}

	violations := config.ViolationsFromWarnings(warns)

	for _, w := range warns {
		if w.Key == "" {
			log.Warn("configuration warning", "message", w.Message, "location", w.Location)
			continue
		}
		log.Warn("configuration warning", "key", w.Key, "message", w.Message, "location", w.Location)
	}

	for _, v := range violations {
		log.Loud("invalid configuration value, using default",
			"key", v.Key, "got", v.Got, "want", v.Want, "message", v.Message)
		if reg != nil {
			reg.Counter("config.violations").Add(1)
		}
	}

	if len(violations) > 0 && env.ProjectRoot != "" {
		persistViolations(env.ProjectRoot, violations, log)
	}
	return cfg, prov, nil
}

// persistViolations writes the typed §11.3 list to state/config-violations.json.
func persistViolations(projectRoot string, violations []config.Violation, log logging.Logger) {
	l := paths.Of(projectRoot)
	if err := os.MkdirAll(paths.Long(l.State), 0o700); err != nil {
		log.Warn("could not create state directory for config violations", "err", err.Error())
		return
	}
	b, err := json.MarshalIndent(violations, "", "  ")
	if err != nil {
		log.Warn("could not encode config violations", "err", err.Error())
		return
	}
	p := filepath.Join(l.State, configViolationsFile)
	if err := paths.WriteAtomic(p, append(b, '\n'), 0o600); err != nil {
		log.Warn("could not persist config violations", "path", p, "err", err.Error())
	}
}

// homeDir resolves the user-global layer's home, preferring an explicitly injected value so tests
// never touch the real home directory. Used by every composition-root entry point that loads
// configuration (daemon.go, selftest.go, commands.go, hooks.go) — moved here from hookclient.go
// (fix round 1, Minor M-10): it is never called from the hot path itself, and this file is
// already where every other config-loading helper lives.
func homeDir(env Env) string {
	if env.HomeDir != "" {
		return env.HomeDir
	}
	if env.Getenv != nil {
		for _, k := range []string{"HOME", "USERPROFILE"} {
			if v := env.Getenv(k); v != "" {
				return v
			}
		}
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}

// AttachLoudCounter wires logging's Loud channel to an obs counter.
//
// The two packages cannot import each other (§3.2 rejects logging -> obs), so the seam is a plain
// function and a composition root closes it. Calling this once at start-up is what makes
// loud.total observable in /qompack:status.
func AttachLoudCounter(reg obs.Registry) {
	if reg == nil {
		logging.AttachLoudObserver(nil)
		return
	}
	logging.AttachLoudObserver(func(_ string, _ ...any) {
		reg.Counter("loud.total").Add(1)
	})
}
