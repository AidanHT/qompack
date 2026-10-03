package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
// config.Load performs the per-leaf fallback and returns the evidence as warnings, but it neither
// logs nor persists: §3.2 keeps logging out of config's allow-set {core, paths} (logging imports
// config, so the reverse edge would be a cycle), and config writes no file by design. The
// reporting half therefore belongs to whoever called Load — and putting it here, rather than at
// each call site, is what stops a caller from forgetting it.
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

	// A KEYLESS warning is a whole layer that is not in effect — config.Load's two keyless producers
	// are `unparseable config: …` and `unreadable config: …`, a file that exists and cannot be read
	// — so the file an operator edited is not in effect at all and every value from it is silently
	// the default. That is finding F4-6: it reached the day log at Warn
	// and nothing stronger, so a corrupt .qompack/config.json stopped the daemon while LOUD.log,
	// self-test and status all stayed clean. §13 invariant 10 makes it Loud. A warning that NAMES a
	// key is the ordinary per-leaf case and stays a Warn; the §11.3 violations below are the ones
	// that Loud individually.
	for _, w := range warns {
		if w.Key == "" {
			log.Loud("configuration unusable, using defaults", "message", w.Message, "location", w.Location)
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

	// config.Load reports a newer-settingsVersion reset as a keyed warning, not a violation, so it
	// is not in this list; the hook path records it (reportCaptureConfig). A load that found neither
	// leaves nothing to record and removes a record an earlier load left, which is what lets doctor's
	// config.violations agree with self-test once an operator has fixed the file (troubleshooting
	// §6). A load with a reset in force but no violation keeps whatever a hook recorded.
	if env.ProjectRoot != "" && (len(violations) > 0 || !anyVersionedReset(warns)) {
		if err := syncViolationsRecord(env.ProjectRoot, violations); err != nil {
			log.Warn("could not update config violations", "err", err.Error())
		}
	}
	return cfg, prov, nil
}

// reportCaptureConfig is the hook path's half of §11.3, and it is the second half of finding S-7
// and of V6 close-out item C1.8: config.LoadForCapture now CLAMPS an out-of-range value and DROPS an
// unknown or mistyped key instead of refusing the delivery, and a fallback nobody records is a
// silent configuration change.
//
// It is deliberately narrower than LoadConfigAndReport. It runs on the hot path, so it does at most
// one durable write, only when the §11.3 list differs from the one on record, and only where there
// is already somewhere durable to write: a project with no .qompack directory has not opted in, and
// a hook must never conjure one out of a diagnostic. A list identical to the record costs one read,
// and a load with no violation removes the record, which costs one Lstat when there is none. Wave
// 20 measured the write it saves: a hook that rewrote an unchanged record paid a staging file, a
// write, an fsync, a rename and a directory fsync every time, about 19 ms on Windows.
//
// Every warning, newer-settingsVersion reset and leaf violation is a Warn here, through the hook
// logger, which materializes a file sink only if logs/ already exists. That is D59's rule for a
// persistent configuration condition: the daemon reports it loudly, once per start (its own
// LoadConfigAndReport) and once per reload of a changed file or forced admin.reload
// (daemon/reload.go), and a hook logs it at warn. Logged Loud here, the same unchanged condition put
// one line per hook process in the never-rotated LOUD.log (finding F-C7-C49-2: 18 lines in 30 s
// after a downgrade), and those lines never reached status, which prints only the daemon's ring.
// Only the violations are persisted: state/config-violations.json is the §11.3 list, and an unknown
// key has never belonged in it. Nothing here can fail the delivery.
func reportCaptureConfig(root, home string, violations []config.Violation, warnings []config.Warning) {
	if root == "" {
		return
	}
	if len(violations) == 0 {
		// Nothing to record: remove what an earlier load recorded. With no .qompack this is one
		// failed Lstat, and it creates nothing.
		if err := syncViolationsRecord(root, nil); err != nil && isDir(paths.Of(root).Dot) {
			withHookLogger(root, home, func(log logging.Logger) {
				log.Warn("could not update config violations", "err", err.Error())
			})
		}
		if len(warnings) == 0 {
			return
		}
	}
	if !isDir(paths.Of(root).Dot) {
		return
	}
	withHookLogger(root, home, func(log logging.Logger) {
		for _, w := range warnings {
			log.Warn("configuration warning", "key", w.Key, "message", w.Message, "location", w.Location)
		}
		for _, v := range violations {
			if isVersionedReset(v) {
				// config.Load returns this reset as a keyed Warning, not a §11.3 violation, and
				// LoadConfigAndReport logs it as one; LoadForCapture types it as a Violation only so
				// that it is recorded below.
				log.Warn("configuration warning", "key", v.Key, "message", v.Message)
				continue
			}
			log.Warn("invalid configuration value, using default",
				"key", v.Key, "got", v.Got, "want", v.Want, "message", v.Message)
		}
		if len(violations) > 0 {
			if err := syncViolationsRecord(root, violations); err != nil {
				log.Warn("could not update config violations", "err", err.Error())
			}
		}
	})
}

// withHookLogger runs fn with a hook logger for root and releases the logger's file handles when fn
// returns, rather than relying on process exit: admission also runs in process, from tests and from
// any future in-process caller, and an unreleased handle is a directory nobody can clean up on
// Windows.
func withHookLogger(root, home string, fn func(logging.Logger)) {
	log := newHookLoggerWithHome(root, home)
	if hl, ok := log.(*hookLogger); ok {
		defer hl.closeSink()
	}
	fn(log)
}

// isVersionedReset reports whether v is LoadForCapture's record of a whole versioned block reset for
// a newer settingsVersion. Such a record is keyed by the block's own path (config.VersionedSections),
// which no §11.3 leaf violation ever is: those are keyed by a leaf.
func isVersionedReset(v config.Violation) bool {
	return isVersionedSection(v.Key)
}

// anyVersionedReset reports whether config.Load's warnings include a whole versioned block reset for
// a newer settingsVersion. config.Load keys that warning by the block's own path, exactly as
// LoadForCapture keys the Violation it records for the same reset.
func anyVersionedReset(warns []config.Warning) bool {
	for _, w := range warns {
		if isVersionedSection(w.Key) {
			return true
		}
	}
	return false
}

// isVersionedSection reports whether key is a versioned block's own path.
func isVersionedSection(key string) bool {
	for _, s := range config.VersionedSections() {
		if key == s.Path {
			return true
		}
	}
	return false
}

// captureConfigDegradedSummary is the one-line account self-test and doctor give of a capture
// configuration the hook path loaded only partly. A "setting" is a §11.3 violation, which is a leaf
// or, for a newer settingsVersion, a whole versioned block.
func captureConfigDegradedSummary(violations []config.Violation, warnings []config.Warning) string {
	return fmt.Sprintf("capture continues: %d setting(s) fell back to the default, %d key(s) not applied",
		len(violations), len(warnings))
}

// captureConfigKeys names every key the capture loader did not apply as written, with the loader's
// own message for it — the same text the day log and state/config-violations.json carry.
func captureConfigKeys(violations []config.Violation, warnings []config.Warning) string {
	parts := make([]string, 0, len(violations)+len(warnings))
	for _, v := range violations {
		parts = append(parts, v.Key+": "+v.Message)
	}
	for _, w := range warnings {
		parts = append(parts, w.Key+": "+w.Message)
	}
	return strings.Join(parts, "; ")
}

// syncViolationsRecord brings state/config-violations.json in line with one load's typed §11.3 list.
// An empty list removes the record. A list whose encoding is byte-for-byte the record's leaves it
// alone: the read is cheap, and the durable write it replaces is not (reportCaptureConfig). Any other
// list is written atomically. A record that cannot be read, including one being renamed over on
// Windows at that moment, is simply written again.
func syncViolationsRecord(projectRoot string, violations []config.Violation) error {
	l := paths.Of(projectRoot)
	p := filepath.Join(l.State, configViolationsFile)
	if len(violations) == 0 {
		if _, err := os.Lstat(paths.Long(p)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("checking %s: %w", p, err)
		}
		if err := os.Remove(paths.Long(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s: %w", p, err)
		}
		return nil
	}
	b, err := json.MarshalIndent(violations, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config violations: %w", err)
	}
	b = append(b, '\n')
	if cur, readErr := paths.ReadFileShared(p); readErr == nil && bytes.Equal(cur, b) {
		return nil
	}
	if err := os.MkdirAll(paths.Long(l.State), 0o700); err != nil {
		return fmt.Errorf("creating the state directory: %w", err)
	}
	if err := paths.WriteAtomic(p, b, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", p, err)
	}
	return nil
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
