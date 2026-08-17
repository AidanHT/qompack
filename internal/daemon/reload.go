package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// configPendingFileName is state/config-pending.json's basename: where a deferred store.chunk.*
// change is recorded until the next SessionStart picks it up (§11.2: applying it mid-session
// would fork the dedup space).
const configPendingFileName = "config-pending.json"

// configJSONPath and configPendingPath name the two files reload.go reads/writes, both derived
// from paths.Of so they agree with every other package's own layout.
func configJSONPath(root string) string {
	return filepath.Join(paths.Of(root).Dot, "config.json")
}

func configPendingPath(root string) string {
	return filepath.Join(paths.Of(root).State, configPendingFileName)
}

// maybeReloadConfig is reload.go's entry point from Run's idle tick and from the session.start
// route: it stats config.json and reloads only if its mtime or size changed since the last load.
// A missing config.json (no project config file at all) is not an error and not a reload — the
// daemon keeps running on whatever it already has.
func (d *daemon) maybeReloadConfig(ctx context.Context, env config.Env) {
	_, _ = d.reloadConfig(ctx, env, false)
}

// reloadConfig is maybeReloadConfig's body, also reachable unconditionally (force=true) from
// admin.reload. It returns the dotted keys that changed and were actually applied — a deferred
// store.chunk.* change is reported via the Loud line and state/config-pending.json, not via this
// return value, since it was NOT applied to the live config.
func (d *daemon) reloadConfig(ctx context.Context, env config.Env, force bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := configJSONPath(d.root)
	fi, statErr := os.Stat(paths.Long(p))

	d.cfgMu.RLock()
	prevMTime, prevSize := d.lastCfgMTime, d.lastCfgSize
	d.cfgMu.RUnlock()

	if statErr != nil {
		if !force {
			return nil, nil // no project config.json yet: nothing to reload.
		}
	} else if !force && fi.ModTime().Equal(prevMTime) && fi.Size() == prevSize {
		return nil, nil // unchanged since the last load.
	}

	useEnv := env
	if useEnv.ProjectRoot == "" {
		useEnv = d.cfgEnv
	}

	newCfg, _, warns, err := config.Load(useEnv)
	if err != nil {
		return nil, err
	}
	for _, w := range warns {
		d.log.Loud("daemon: config reload warning", "key", w.Key, "message", w.Message, "location", w.Location)
	}

	d.cfgMu.Lock()
	oldCfg := d.cfg
	finalCfg := newCfg
	chunkChanged := !reflect.DeepEqual(oldCfg.Store.Chunk, newCfg.Store.Chunk)
	if chunkChanged {
		finalCfg.Store.Chunk = oldCfg.Store.Chunk // deferred: the live config keeps the OLD block.
	}
	d.cfg = finalCfg
	if fi != nil {
		d.lastCfgMTime = fi.ModTime()
		d.lastCfgSize = fi.Size()
	}
	d.cfgMu.Unlock()

	if chunkChanged {
		d.log.Loud("daemon: store.chunk.* change deferred to next SessionStart",
			"old", oldCfg.Store.Chunk, "new", newCfg.Store.Chunk)
		if perr := d.writeConfigPending(newCfg); perr != nil {
			d.log.Warn("daemon: failed to persist state/config-pending.json", "err", perr)
		}
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
	return changed, nil
}

// writeConfigPending persists cfg — the FULL reloaded config, including the deferred store.chunk.*
// block — to state/config-pending.json, so the next SessionStart (or an operator running `qompack
// doctor`) can see exactly what is waiting to take effect.
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
