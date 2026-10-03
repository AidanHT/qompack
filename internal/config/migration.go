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

// Phase7SettingsVersion is the runtime.phase7.settingsVersion this build understands. It is
// versioned independently of MigrationSettingsVersion because the two blocks move for different
// reasons — one tracks the shipped pipeline's controls, the other SP-16's optional refinements —
// and a shared number would force a reset of one whenever the other changed.
const Phase7SettingsVersion = 1

// The dotted paths of the blocks applyVersionedSection resets wholesale.
const (
	migrationSection = "runtime.migration"
	phase7Section    = "runtime.phase7"
)

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
	{Key: "runtime.phase7.reuse.scopedCandidates", Owner: "SP-16", Gate: "M6-G16-A scoped reuse and authorization"},
	{Key: "runtime.phase7.reuse.warmPrior", Owner: "SP-16", Gate: "M6-G16-D optional-policy value against a simple baseline"},
	{Key: "runtime.phase7.retrieval.reminders", Owner: "SP-16", Gate: "M6-G16-B bounded retrieval and usefulness telemetry"},
	{Key: "runtime.phase7.retrieval.demandPromotion", Owner: "SP-16", Gate: "M6-G16-C promotion of future representations only"},
	{Key: "runtime.phase7.filters.segmentBloom", Owner: "SP-16", Gate: "M6-G16-E filter coverage, staleness and recovery"},
}

// MigrationGates returns a copy of the gate table in switch order.
func MigrationGates() []MigrationGate {
	return append([]MigrationGate(nil), migrationGates...)
}

// LegacyImportGateKey names the build gate that guards SP-20 M1-04's legacy import, writer
// handoff and cutover (T20-M1-08). It is deliberately NOT spelled as a runtime.migration.* dotted
// path: it has no config leaf, and a caller must not be able to turn it on by editing a file.
const LegacyImportGateKey = "store.migrate.legacyImportCutover"

// migrationBuildGates is the second gate table: capabilities whose gate is a BUILD fact with no
// config leaf behind it. MigrationGates above is the config-switch table — every entry there must
// name a bool leaf under runtime.migration, which is what TestMigrationGates_CoverEveryGatedLeaf
// pins. A build gate has no such leaf on purpose: side-by-side legacy import mutates the
// destination store and then transfers the single writer, so it must be unreachable from any
// config file and reachable only from the reviewed commit that flips Passed here, or from a test
// that constructs a passed gate explicitly and says so.
//
// Consumers take the gate as a value (store.MigrateOptions.Gate) rather than reading this table
// directly, so "the gate is closed" is the zero value and a caller cannot forget to ask.
var migrationBuildGates = []MigrationGate{
	{
		Key:   LegacyImportGateKey,
		Owner: "SP-20 M1-04",
		Gate:  "M1 compatible migration (T20-M1-08: import/parity/cutover and the pre- and post-first-write rollback drill)",
	},
}

// MigrationBuildGates returns a copy of the build-gate table.
func MigrationBuildGates() []MigrationGate {
	return append([]MigrationGate(nil), migrationBuildGates...)
}

// LegacyImportGate returns the legacy import/cutover build gate as this build ships it. Passed is
// false until SP-20 M1-04's acceptance evidence lands, so the production wiring of
// store.NewMigrator refuses to import or cut over at all.
//
// Before Passed flips, the import's two unsynced appends must become durable: the mapping line that
// Import commits its cursor past (store/migrate.go importOne, with a publication pass for the
// imported objects) and the new-format write line the handoff records (RecordNewFormatWrite). The
// V6 close-out found both and left them behind this gate (w6-ckptsync review finding 5); the store
// test TestLegacyImportGate_StaysClosedUntilTheImportIsDurable fails if the gate opens first.
func LegacyImportGate() MigrationGate {
	for _, g := range migrationBuildGates {
		if g.Key == LegacyImportGateKey {
			return g
		}
	}
	return MigrationGate{Key: LegacyImportGateKey}
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
	p := c.Runtime.Phase7
	switch key {
	case "runtime.phase7.reuse.scopedCandidates":
		return p.Reuse.ScopedCandidates, true
	case "runtime.phase7.reuse.warmPrior":
		return p.Reuse.WarmPrior, true
	case "runtime.phase7.retrieval.reminders":
		return p.Retrieval.Reminders, true
	case "runtime.phase7.retrieval.demandPromotion":
		return p.Retrieval.DemandPromotion, true
	case "runtime.phase7.filters.segmentBloom":
		return p.Filters.SegmentBloom, true
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
	{"checkpoint.incrementalSpanInstruction", "custom_instructions is PreCompact input, not a summarizer setter, and since C1.18 Qompack emits no PreCompact instruction at all; the key is read for compatibility only (Qompack.md v1.5 §7.3; SP-10 reviewed migration)"},
}

// RetiredMeaningKey is one retired-meaning key as the reference documentation reports it: the
// dotted leaf a user may still set, and what that leaf no longer means. It is the exported shape
// of retiredMeaningKeys, which stays the single source of the rows.
type RetiredMeaningKey struct {
	// Key is the dotted leaf path, still read and still applied.
	Key string
	// Note is what the key no longer means — the same sentence the deprecation warning carries.
	Note string
}

// RetiredMeaningKeys returns the retired-meaning table in table order. The value behind each key
// is still applied; setting one from a non-default layer produces a Deprecated Warning naming the
// file it was set in (migrationDeprecations).
func RetiredMeaningKeys() []RetiredMeaningKey {
	out := make([]RetiredMeaningKey, 0, len(retiredMeaningKeys))
	for _, r := range retiredMeaningKeys {
		out = append(out, RetiredMeaningKey{Key: r.key, Note: r.note})
	}
	return out
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

// applyVersionedSections resets each independently versioned block whose merged document declares
// a settingsVersion newer than this build understands. It returns one Warning per block it reset.
//
// Two blocks carry their own version — runtime.migration (SP-19) and runtime.phase7 (SP-16) — and
// they are reset independently: a refinement schema moving forward must not reset the pipeline's
// own controls, and vice versa. The blocks are the rows of versionedSections, which is also what
// VersionedSections() reports, so adding a third means adding a row there and nothing else.
func applyVersionedSections(merged, defaults map[string]any, prov Provenance) []Warning {
	var out []Warning
	for _, s := range versionedSections {
		if w, reset := applyVersionedSection(merged, defaults, prov, s.Path, s.Version); reset {
			out = append(out, w)
		}
	}
	return out
}

// VersionedSection is one independently versioned config block: its dotted path, and the
// settingsVersion this build understands for it.
type VersionedSection struct {
	// Path is the block's dotted path, e.g. "runtime.migration".
	Path string
	// Version is the settingsVersion this build understands. A merged document declaring a newer
	// one has the whole block reset to defaults (applyVersionedSection).
	Version int
}

// versionedSections is the row source both applyVersionedSections and VersionedSections read, so
// the behaviour of the reset and the documentation of it cannot name different blocks.
var versionedSections = []VersionedSection{
	{Path: migrationSection, Version: MigrationSettingsVersion},
	{Path: phase7Section, Version: Phase7SettingsVersion},
}

// VersionedSections returns a copy of the versioned-block table in reset order.
func VersionedSections() []VersionedSection {
	return append([]VersionedSection(nil), versionedSections...)
}

// applyVersionedSection resets the whole section block in merged to its defaults when the merged
// document declares a settingsVersion newer than build, rewriting the block's provenance to
// OriginDefault. It reports whether it did so and the Warning to record.
//
// It runs before Validate's fallback loop on purpose: that loop restores one violated leaf at a
// time, which would keep every switch a newer file set while resetting only the version number —
// the opposite of "unknown future behaviour disabled". Keys this build does not know under the
// block have already been dropped by deepMerge's unknown-key rule; this handles the ones it does.
func applyVersionedSection(merged, defaults map[string]any, prov Provenance, section string, build int) (Warning, bool) {
	versionKey := section + ".settingsVersion"
	raw, ok := getPath(merged, versionKey)
	if !ok {
		return Warning{}, false
	}
	v, ok := raw.(float64)
	if !ok || v <= float64(build) {
		return Warning{}, false
	}
	loc := prov[versionKey].Location
	restoreDefault(merged, defaults, section)
	for k := range globalSchema.leaves {
		if strings.HasPrefix(k, section+".") {
			prov[k] = Source{Origin: OriginDefault, Location: "reset: newer settingsVersion"}
		}
	}
	return Warning{
		Key:            section,
		Location:       loc,
		VersionedReset: true,
		Message: fmt.Sprintf("settingsVersion %v is newer than this build understands (%d); "+
			"the whole %s block is reset to defaults so unknown switches stay off",
			v, build, section),
	}, true
}
