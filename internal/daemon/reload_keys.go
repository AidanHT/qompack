package daemon

import (
	"reflect"
	"strings"

	"github.com/qompack/qompack/internal/config"
)

// What a config reload does with each key it finds changed (V6 close-out D49).
//
// Every key the reload reports as changed takes effect in the running daemon, or the reload says it
// needs a restart. reloadKeyEffects is the one place that decides which, key by key: a key is live
// only where every reader the daemon wires reads the live configuration (config_live.go) at the
// moment it uses the key; a key held by something built once at start is restart, and the live
// configuration keeps the value in effect until then, so no reader sees half of a change.
// TestReloadKeyEffects_ClassifyEveryKey keeps the table total over the configuration schema, so a new
// key cannot arrive unclassified.

// keyEffect is what a reload does with one changed key.
type keyEffect uint8

const (
	// effectLive: every reader in the running daemon reads the key at its next use.
	effectLive keyEffect = iota + 1
	// effectRestart: a reader built at start holds the key. The live configuration keeps the value
	// in effect, and the reload names the key as needing a daemon restart.
	effectRestart
	// effectDeferred: store.chunk.*, applied at the next SessionStart (reload.go), because applying
	// it mid-session would fork the dedup space (§11.2).
	effectDeferred
	// effectOutside: nothing in the daemon process reads the key. The hook or command that reads it
	// loads the configuration itself when it runs, so the reload has nothing to apply.
	effectOutside
)

// reloadKeyEffect classifies one dotted key prefix. why names the readers the classification rests
// on, for the next person who wires a service.
type reloadKeyEffect struct {
	prefix string
	effect keyEffect
	why    string
}

// reloadKeyEffects is matched by longest prefix (effectOf).
var reloadKeyEffects = []reloadKeyEffect{
	{"store.chunk", effectDeferred, "the chunker's boundaries: a mid-session change forks the dedup space"},
	{"store.compression", effectRestart, "the store's object codec, fixed when store.Open builds it"},
	{"store.retention", effectRestart, "the observer's retention stamps, taken from the configuration it was built with"},
	{"store.canonicalize", effectRestart, "the store's and the observer's canonicalizers, built at start"},

	{"scheduler.softFloorPct", effectLive, "scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn)"},
	{"scheduler.hardCeilingMargin", effectLive, "scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn)"},
	{"scheduler.youngDaly", effectLive, "scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn)"},
	{"scheduler.changepoint", effectRestart, "the changepoint detector's shape, built with the scheduler runtime"},
	{"scheduler.cache", effectRestart, "the cache regime, resolved when the scheduler binds a session"},
	{"scheduler.idle.detectAfterSeconds", effectLive, "idle controller, client-spool watcher and scheduler runtime (applyReloaded)"},
	{"scheduler.idle.backgroundWork", effectLive, "scheduler runtime idle tasks (SchedulerRuntimeOptions.CfgFn)"},
	{"scheduler.idle.deepCutWhenCold", effectLive, "scheduler runtime Evaluate (SchedulerRuntimeOptions.CfgFn)"},

	{"checkpoint", effectRestart, "the checkpoint writer and the PreCompact/idle seams, wired with the start configuration"},
	{"sketches", effectRestart, "the sketch set and the elimination filter, sized at start"},

	{"eliminations", effectLive, "rehydrate service, MCP tools, elimination ledger and scheduler read the live configuration"},

	{"retrieval.ephemeralResults", effectLive, "MCP tools (ToolDeps.CfgFn)"},
	{"retrieval.defaultSpan", effectLive, "MCP tools (ToolDeps.CfgFn)"},
	{"retrieval.promoteAfterExpansions", effectRestart, "the MCP expansion promoter, built with its threshold at start"},

	{"selection.slicing", effectRestart, "the dependence DAG, opened with its slicing variant at start"},
	{"selection.deltaScoring", effectOutside, "no reader in this build"},
	{"selection.submodular.lambda", effectLive, "rehydrate service and scheduler runtime read the live configuration"},
	{"selection.submodular.lazyGreedy", effectOutside, "no reader in this build"},

	{"eval", effectOutside, "the eval commands, which load the configuration when they run"},

	{"runtime.mode", effectOutside, "each hook, which loads the configuration when it runs"},
	{"runtime.daemon.enabled", effectLive, "state.bin, rewritten by the reload"},
	{"runtime.daemon.ackDeadlineMs", effectLive, "state.bin, rewritten by the reload"},
	{"runtime.daemon.connectDeadlineMs", effectLive, "state.bin, rewritten by the reload"},
	{"runtime.daemon.idleExitSeconds", effectLive, "the daemon's idle-exit check (currentCfg)"},
	{"runtime.daemon.maxSessions", effectLive, "the session registry (applyReloaded)"},
	{"runtime.hotPath.budgetMs", effectLive, "the breach detector (applyReloaded) and the hot-path handler"},
	{"runtime.hotPath.breachWindows", effectLive, "the breach detector (applyReloaded)"},
	{"runtime.hotPath.spoolOnBreach", effectLive, "the hot-path handler (currentCfg) and state.bin"},
	{"runtime.hotPath.maxPayloadBytes", effectRestart, "the observer's result bound and the MCP server's line bound, set at start"},
	{"runtime.logging", effectOutside, "no reader in the daemon: its log is opened before the configuration loads"},
	{"runtime.redact", effectLive, "capture admission (currentCfg), the store's and the retrieval tools' redactors (NewLiveRedactor)"},
	{"runtime.telemetry", effectOutside, "hardwired off; Validate refuses true"},
	{"runtime.rehydrate", effectLive, "rehydrate service (RehydrateOptions.CfgFn)"},
	{"runtime.mcp", effectLive, "MCP tools (ToolDeps.CfgFn)"},
	{"runtime.scheduler.cache", effectRestart, "the cache regime, resolved when the scheduler binds a session"},
	{"runtime.migration.reinjection.sessionStartCompact", effectLive, "rehydrate service (RehydrateOptions.CfgFn)"},
	{"runtime.migration", effectRestart, "gated capabilities wired at start; Validate refuses the gated switches"},
	{"runtime.phase7", effectRestart, "gated refinements wired at start; Validate refuses the gated switches"},
	{"runtime.budgets", effectLive, "budget checks (currentCfg) and the MCP tools (ToolDeps.CfgFn)"},
	{"runtime.selection.submodularEnabled", effectLive, "rehydrate service (RehydrateOptions.CfgFn)"},
	{"runtime.selection.loopWarningsEnabled", effectOutside, "no reader in this build"},
	{"runtime.tokens", effectRestart, "the token estimators, built with their constants at start"},
}

// effectOf returns the classification of key by longest matching prefix, and false for a key no
// entry covers.
func effectOf(key string) (keyEffect, bool) {
	best, found := -1, keyEffect(0)
	for _, e := range reloadKeyEffects {
		if (key == e.prefix || strings.HasPrefix(key, e.prefix+".")) && len(e.prefix) > best {
			best, found = len(e.prefix), e.effect
		}
	}
	return found, best >= 0
}

// holdForRestart returns next with every restart and deferred key that differs from inEffect put
// back to its value in inEffect, and the restart keys it held. A key the table does not cover is
// held too: a reload never applies what nobody has shown to be safe to apply.
func holdForRestart(inEffect, next config.Config) (config.Config, []string) {
	var restart []string
	for _, key := range diffDottedKeys(inEffect, next) {
		effect, ok := effectOf(key)
		switch {
		case ok && (effect == effectLive || effect == effectOutside):
			continue
		case !ok || effect == effectRestart:
			restart = append(restart, key)
		}
		copyConfigLeaf(&next, inEffect, key)
	}
	// Selection.Submodular.Enabled is derived from runtime.selection.submodularEnabled, not read
	// from any file (config.Load), so it follows whatever that key now holds.
	next.Selection.Submodular.Enabled = next.Runtime.Selection.SubmodularEnabled
	return next, restart
}

// copyConfigLeaf sets the field dotted names (by its json keys) in dst to its value in src. A path
// that names no field is left alone.
func copyConfigLeaf(dst *config.Config, src config.Config, dotted string) {
	d, s := reflect.ValueOf(dst).Elem(), reflect.ValueOf(src)
	for _, name := range strings.Split(dotted, ".") {
		i, ok := jsonField(d.Type(), name)
		if !ok {
			return
		}
		d, s = d.Field(i), s.Field(i)
		if d.Kind() != reflect.Struct {
			break
		}
	}
	d.Set(s)
}

// jsonField is the index of t's field whose json key is name.
func jsonField(t reflect.Type, name string) (int, bool) {
	if t.Kind() != reflect.Struct {
		return 0, false
	}
	for i := range t.NumField() {
		tag, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if tag == name {
			return i, true
		}
	}
	return 0, false
}
