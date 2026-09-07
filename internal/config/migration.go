package config

import (
	"fmt"
	"strings"
)

// MigrationSettingsVersion is the runtime.migration.settingsVersion this build understands. A
// config file that declares a newer one was written by a later plugin for switches this build
// cannot honour, so Load resets the whole block to Defaults() rather than applying the half of it
// that happens to parse (Qompack.md v1.5 Appendix C, "Schema/config version": unknown future
// behaviour is disabled, never guessed at).
const MigrationSettingsVersion = 1

// migrationSection is the dotted path of the block applyMigrationVersion resets wholesale.
const migrationSection = "runtime.migration"

// MigrationGate records, for one gated runtime.migration switch, which future gate must pass before
// a build may honour a true value. The table is what makes the switches load-bearing before their
// consumers exist: Validate refuses a true value whose gate is still pending, so the only way to
// turn a gated capability on is the reviewed commit that flips Passed here — a config edit against
// a build that cannot honour it falls back to the default and warns (SP-19 exit criterion:
// unsupported controls remain disabled).
type MigrationGate struct {
	// Key is the switch's dotted leaf path.
	Key string
	// Owner is the subplan whose landing flips Passed.
	Owner string
	// Gate names the acceptance gate that must pass first.
	Gate string
	// Passed is true only in a build whose Owner has landed the gate. Every entry is false at
	// SP-19; TestMigrationGates_AllPendingInThisBuild pins that and must be updated, deliberately,
	// by the commit that flips one.
	Passed bool
}

// migrationGates is the gate table, in the order the switches appear in MigrationCfg.
var migrationGates = []MigrationGate{
	{Key: "runtime.migration.capture.rawEvidence", Owner: "SP-20", Gate: "M1 capture fidelity (T20-M1-01/02)"},
	{Key: "runtime.migration.publication.durableFrontier", Owner: "SP-20", Gate: "M1 durable publication (T20-M1-03/04/05)"},
	{Key: "runtime.migration.replacement.newResult", Owner: "SP-21", Gate: "M4 admission (T21 pipeline, pass-through and recovery)"},
	{Key: "runtime.migration.compaction.automaticVeto", Owner: "SP-19 M0-03, then SP-12", Gate: "recovery/proactive distinction verified in the target host"},
	{Key: "runtime.migration.experiments.enabled", Owner: "SP-15 and SP-16", Gate: "M5/M6 selection and refinement acceptance"},
}

// MigrationGates returns a copy of the gate table in switch order.
func MigrationGates() []MigrationGate {
	return append([]MigrationGate(nil), migrationGates...)
}

// migrationSwitch reads the bool behind a gate's key. It is an explicit switch rather than a
// reflective lookup so that the gate table and MigrationCfg cannot drift apart silently:
// TestMigrationGates_CoverEveryGatedLeaf walks every bool leaf under runtime.migration and checks
// that each is either gated here or on the explicit ungated list.
func (c Config) migrationSwitch(key string) (on, known bool) {
	m := c.Runtime.Migration
	switch key {
	case "runtime.migration.capture.rawEvidence":
		return m.Capture.RawEvidence, true
	case "runtime.migration.publication.durableFrontier":
		return m.Publication.DurableFrontier, true
	case "runtime.migration.replacement.newResult":
		return m.Replacement.NewResult, true
	case "runtime.migration.compaction.automaticVeto":
		return m.Compaction.AutomaticVeto, true
	case "runtime.migration.experiments.enabled":
		return m.Experiments.Enabled, true
	}
	return false, false
}

// retiredMeaningKey is an Appendix C key whose reader stays compatible but whose production
// meaning Qompack.md v1.5 retired or placed under reviewed migration (Appendix C row "Young–Daly,
// incrementalSpanInstruction, deepCutWhenCold, ski rental": preserve reader compatibility; retire
// production action/default meaning through reviewed migration). The value is still applied —
// nothing here changes behaviour — but a user who sets one is told what it no longer means.
type retiredMeaningKey struct {
	key, note string
}

// retiredMeaningKeys lists those keys. Ski rental has no config key (SkiRentalShouldWrite reads
// the cache multipliers), so it is not here; its disposition is SP-12's reviewed migration.
var retiredMeaningKeys = []retiredMeaningKey{
	{"scheduler.youngDaly.enabled", "Young–Daly pacing is compatibility/harness-only: no native compaction trigger, cut or veto depends on it (Qompack.md v1.5 Appendix C; SP-12 reviewed migration)"},
	{"scheduler.youngDaly.measuredDeltaSeconds", "Young–Daly pacing is compatibility/harness-only: the measured delta no longer times a native compaction (Qompack.md v1.5 Appendix C; SP-12 reviewed migration)"},
	{"scheduler.idle.deepCutWhenCold", "no native cut is available to a plugin; the key is read for compatibility only and selects no history rewrite (Qompack.md v1.5 §12; SP-12 reviewed migration)"},
	{"checkpoint.incrementalSpanInstruction", "custom_instructions is PreCompact input, not a summarizer setter; the key is read for compatibility only (Qompack.md v1.5 §7.3; SP-10 reviewed migration)"},
}

// migrationDeprecations returns one Warning per retired-meaning key that a non-default layer set.
// Reading prov rather than the merged value is deliberate: a user who writes the default value
// explicitly still deserves the note, and a default that was never touched does not.
func migrationDeprecations(prov Provenance) []Warning {
	var out []Warning
	for _, r := range retiredMeaningKeys {
		src, ok := prov[r.key]
		if !ok || src.Origin == OriginDefault {
			continue
		}
		out = append(out, Warning{
			Key:        r.key,
			Message:    "deprecated meaning: " + r.note,
			Location:   src.Location,
			Deprecated: true,
		})
	}
	return out
}

// applyMigrationVersion resets the whole runtime.migration block in merged to its defaults when the
// merged document declares a settingsVersion newer than MigrationSettingsVersion, rewriting the
// block's provenance to OriginDefault. It reports whether it did so and the Warning to record.
//
// It runs before Validate's fallback loop on purpose: that loop restores one violated leaf at a
// time, which would keep every switch a newer file set while resetting only the version number —
// the opposite of "unknown future behaviour disabled". Keys this build does not know under the
// block have already been dropped by deepMerge's unknown-key rule; this handles the ones it does.
func applyMigrationVersion(merged, defaults map[string]any, prov Provenance) (Warning, bool) {
	const versionKey = migrationSection + ".settingsVersion"
	raw, ok := getPath(merged, versionKey)
	if !ok {
		return Warning{}, false
	}
	v, ok := raw.(float64)
	if !ok || v <= MigrationSettingsVersion {
		return Warning{}, false
	}
	loc := prov[versionKey].Location
	restoreDefault(merged, defaults, migrationSection)
	for k := range globalSchema.leaves {
		if strings.HasPrefix(k, migrationSection+".") {
			prov[k] = Source{Origin: OriginDefault, Location: "reset: newer settingsVersion"}
		}
	}
	return Warning{
		Key:      migrationSection,
		Location: loc,
		Message: fmt.Sprintf("settingsVersion %v is newer than this build understands (%d); "+
			"the whole runtime.migration block is reset to defaults so unknown switches stay off",
			v, MigrationSettingsVersion),
	}, true
}
