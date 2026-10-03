package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// configPendingFileName is state/config-pending.json's basename: the record of a store.chunk.*
// change the running daemon holds until a restart applies it. Nothing re-reads the chunk block after
// store.Open builds the chunker at daemon start, and applying a change mid-session would fork the
// dedup space (§11.2), so a restart is the only thing that applies it. The file lives exactly as long
// as that is true: a reload that holds a chunk change writes it, and the next daemon's Run, which
// loaded config.json itself, removes it (clearConfigPending).
const configPendingFileName = "config-pending.json"

// configJSONPath and configPendingPath name the two files reload.go reads/writes, both derived
// from paths.Of so they agree with every other package's own layout.
func configJSONPath(root string) string {
	return filepath.Join(paths.Of(root).Dot, "config.json")
}

func configPendingPath(root string) string {
	return filepath.Join(paths.Of(root).State, configPendingFileName)
}

// ConfigFileStamp is the project config.json's modification time and size: what reloadConfigKeys
// compares to decide that the file changed. The zero value is "no file seen".
type ConfigFileStamp struct {
	ModTime time.Time
	Size    int64
}

// StampConfigFile stats root's .qompack/config.json the way a reload does, returning the zero stamp
// when there is no such file. A composition root calls it before loading the configuration it hands
// the daemon (Options.CfgStamp): taken in that order, a file that changes between the two is newer
// than the stamp and is reloaded at the first check, and an unchanged one is not.
func StampConfigFile(root string) ConfigFileStamp {
	fi, err := os.Stat(paths.Long(configJSONPath(root)))
	if err != nil {
		return ConfigFileStamp{}
	}
	return ConfigFileStamp{ModTime: fi.ModTime(), Size: fi.Size()}
}

// maybeReloadConfig is reload.go's entry point from Run's idle tick and from the session.start
// route: it stats config.json and reloads only if its mtime or size changed since the last load.
// A missing config.json (no project config file at all) is not an error and not a reload — the
// daemon keeps running on whatever it already has.
func (d *daemon) maybeReloadConfig(ctx context.Context, env config.Env) {
	_, _ = d.reloadConfig(ctx, env, false)
}

// reloadConfig is maybeReloadConfig's body, also reachable unconditionally (force=true) from
// admin.reload. It returns the dotted keys that changed and were applied. A key that needs a restart
// (store.chunk.* among them) and a key with no effect in this build are reported by their own Loud
// lines (reloadConfigKeys), not via this return value, since neither was applied to the live config.
func (d *daemon) reloadConfig(ctx context.Context, env config.Env, force bool) ([]string, error) {
	res, err := d.reloadConfigKeys(ctx, env, force)
	return res.Changed, err
}

// reloadResult is what one reload did with the keys it found changed.
type reloadResult struct {
	// Changed are the keys the reload applied: each is in effect in the running daemon.
	Changed []string
	// Restart are the keys held at their value in effect until a daemon restart applies them.
	Restart []string
	// NoEffect are the keys nothing in this build reads, held at their value in effect.
	NoEffect []string
}

// reloadConfigKeys is reloadConfig that also returns the changed keys it held back, because they
// need a daemon restart or because nothing in this build reads them (reload_keys.go). Every key it returns as changed is in effect in the running
// daemon when it returns (V6 close-out D49): the live configuration holds it, every service the
// wiring built reads that configuration at its next use, and the daemon's own components that keep
// a value derived from it have been updated (applyReloaded). The candidate 4 live re-run's UAT-05
// logged "config reloaded changed=[runtime.rehydrate.maxTokens runtime.rehydrate.minTokens]" and
// went on rehydrating at its startup budget, because the reload replaced the daemon's own copy and
// nothing else.
func (d *daemon) reloadConfigKeys(ctx context.Context, env config.Env, force bool) (reloadResult, error) {
	if err := ctx.Err(); err != nil {
		return reloadResult{}, err
	}
	p := configJSONPath(d.root)
	fi, statErr := os.Stat(paths.Long(p))

	d.cfgMu.Lock()
	defer d.cfgMu.Unlock()
	prevMTime, prevSize := d.lastCfgMTime, d.lastCfgSize

	if statErr != nil {
		if !force {
			return reloadResult{}, nil // no project config.json yet: nothing to reload.
		}
	} else if !force && fi.ModTime().Equal(prevMTime) && fi.Size() == prevSize {
		return reloadResult{}, nil // unchanged since the last load.
	}

	useEnv := env
	if useEnv.ProjectRoot == "" {
		useEnv = d.cfgEnv
	}

	newCfg, _, warns, err := config.Load(useEnv)
	if err != nil {
		return reloadResult{}, err
	}
	for _, w := range warns {
		d.log.Loud("daemon: config reload warning", "key", w.Key, "message", w.Message, "location", w.Location)
	}

	// cfgMu serializes whole reloads (the idle tick, session.start and admin.reload can race), so
	// the configuration read here is the one this reload replaces.
	oldCfg := d.currentCfg()
	chunkChanged := !reflect.DeepEqual(oldCfg.Store.Chunk, newCfg.Store.Chunk)
	finalCfg, restart, inert := holdForRestart(oldCfg, newCfg)
	d.live.store(finalCfg)
	if fi != nil {
		d.lastCfgMTime = fi.ModTime()
		d.lastCfgSize = fi.Size()
	}
	d.applyReloaded(finalCfg)

	if chunkChanged {
		// Named with the other restart keys below; the file records the whole reloaded configuration
		// for an operator until the restart that applies it.
		if perr := d.writeConfigPending(newCfg); perr != nil {
			d.log.Warn("daemon: failed to persist state/config-pending.json", "err", perr)
		}
	}
	if len(restart) > 0 {
		d.log.Loud(LoudReloadNeedsRestart, "keys", restart)
	}
	if len(inert) > 0 {
		d.log.Loud(LoudReloadNoEffect, "keys", inert)
	}

	changed := diffDottedKeys(oldCfg, finalCfg)
	if len(changed) > 0 {
		d.log.Info("daemon: config reloaded", "changed", changed)
		// state.bin carries ConnectDeadlineMs/AckDeadlineMs/MaxPayloadBytes/SpoolOnBreach/
		// DaemonEnabled — every client reads it at construction, so a reload of any of those
		// keys is invisible until this is rewritten (fix round 1, I-4). The idle-tick reload
		// path has no other opportunity to propagate a change at all.
		if err := ipc.WriteState(d.root, d.currentState()); err != nil {
			d.log.Warn("daemon: failed to rewrite state.bin after reload", "err", err)
		}
	}
	return reloadResult{Changed: changed, Restart: restart, NoEffect: inert}, nil
}

// applyReloaded hands a reloaded configuration to the daemon's own components that keep a value
// derived from it rather than reading the live configuration per use: the session registry's
// ceiling, the hot-path breach detector's limit and window count, and the idle horizon the idle
// controller and the client-spool watcher share. A nil component (a daemon value no New built) is
// skipped.
func (d *daemon) applyReloaded(cfg config.Config) {
	if d.registry != nil {
		d.registry.SetMaxSessions(cfg.Runtime.Daemon.MaxSessions)
	}
	if d.breach != nil {
		d.breach.reconfigure(time.Duration(cfg.Runtime.HotPath.BudgetMs)*time.Millisecond, cfg.Runtime.HotPath.BreachWindows)
	}
	if d.idle != nil {
		d.idle.setDetectAfter(cfg.Scheduler.Idle.DetectAfterSeconds)
		if d.spool != nil {
			d.spool.setHorizon(d.idle.detectAfter())
		}
	}
}

// writeConfigPending persists cfg — the FULL reloaded config, including the held store.chunk.*
// block — to state/config-pending.json, so an operator can see exactly what a daemon restart would
// apply.
func (d *daemon) writeConfigPending(cfg config.Config) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	p := configPendingPath(d.root)
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), 0o700); err != nil {
		return err
	}
	return paths.WriteAtomic(p, b, 0o600)
}

// clearConfigPending removes state/config-pending.json. Run calls it once it owns the project: this
// daemon loaded config.json when it started, so the change the file records is in effect, and the
// record would otherwise claim a pending change for ever. A missing file is the ordinary case.
func (d *daemon) clearConfigPending() {
	err := os.Remove(paths.Long(configPendingPath(d.root)))
	if err != nil && !os.IsNotExist(err) {
		d.log.Warn("daemon: failed to remove a stale state/config-pending.json", "err", err)
	}
}

// diffDottedKeys is a best-effort JSON diff between two configs, rendering every leaf that
// changed (or appeared/disappeared, which the schema never actually does, but a future or
// hand-edited config could) as a dotted key, sorted for a stable log line.
func diffDottedKeys(oldCfg, newCfg config.Config) []string {
	oldFlat := flattenConfig(oldCfg)
	newFlat := flattenConfig(newCfg)

	keys := make(map[string]bool, len(newFlat))
	for k, v := range newFlat {
		if ov, ok := oldFlat[k]; !ok || ov != v {
			keys[k] = true
		}
	}
	for k := range oldFlat {
		if _, ok := newFlat[k]; !ok {
			keys[k] = true
		}
	}

	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// flattenConfig renders cfg as dotted-path -> JSON-encoded-leaf-value, via a plain JSON
// marshal/unmarshal round trip — simple and correct for a diff that only needs to name what
// changed, not typed access to the values.
func flattenConfig(cfg config.Config) map[string]string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil
	}
	out := map[string]string{}
	flattenInto(raw, "", out)
	return out
}

func flattenInto(v any, prefix string, out map[string]string) {
	if m, ok := v.(map[string]any); ok {
		for k, vv := range m {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			flattenInto(vv, key, out)
		}
		return
	}
	b, _ := json.Marshal(v)
	out[prefix] = string(b)
}
