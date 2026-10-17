package eval

// ── The Stage A arms (V7 W6 item 1) ─────────────────────────────────────────────────────────────
//
// Four arms replace the two-arm design's stock and qompack: stock (no plugin), classic (Qompack
// without the mod), generic (classic plus a fixed `# Compact instructions` section in the fixture's
// CLAUDE.md) and steer (Qompack with the mod, both mod switches on). Each arm declares the state the
// harness sets up and checks; a trial whose configuration contradicts it is a harness error, not a
// scored trial. ArmStock and ArmQompack stay defined for reading the two-arm runs already recorded.

// The Stage A arm names.
const (
	ArmClassic = "classic"
	ArmGeneric = "generic"
	ArmSteer   = "steer"
)

// LiveArms is the Stage A arms in their report order.
var LiveArms = []string{ArmStock, ArmClassic, ArmGeneric, ArmSteer}

// The two bundles a run may load, named by the live-eval flag that supplies each. Both are built
// from one commit: the classic shape for classic and generic, the mods shape for steer.
const (
	LiveBundleClassic = "bundle"
	LiveBundleMods    = "bundle-mods"
)

// LiveArmState is what an arm is: what the harness sets up for each of its trials and checks
// before a trial's outcomes are scored.
type LiveArmState struct {
	Arm string `json:"arm"`
	// Plugin is that the host must load exactly one Qompack plugin, from the arm's own install.
	Plugin bool `json:"plugin"`
	// Bundle names the bundle the plugin comes from: LiveBundleClassic or LiveBundleMods; empty with
	// no plugin.
	Bundle string `json:"bundle,omitempty"`
	// ModEnabled and ModSteering are runtime.mod.enabled and runtime.mod.steering, set by environment.
	ModEnabled  bool `json:"mod_enabled"`
	ModSteering bool `json:"mod_steering"`
	// ClaudeMDCompactSection is that the project's root CLAUDE.md carries the task set's text G under
	// a `# Compact instructions` heading.
	ClaudeMDCompactSection bool `json:"claude_md_compact_section"`
	// StorePrecreated is that an empty .qompack/ exists in the project before the session starts.
	StorePrecreated bool `json:"store_precreated"`
}

// LiveArmSpec is the declared state of a Stage A arm; false for any other name.
func LiveArmSpec(arm string) (LiveArmState, bool) {
	switch arm {
	case ArmStock:
		return LiveArmState{Arm: arm}, true
	case ArmClassic:
		return LiveArmState{Arm: arm, Plugin: true, Bundle: LiveBundleClassic, StorePrecreated: true}, true
	case ArmGeneric:
		return LiveArmState{
			Arm: arm, Plugin: true, Bundle: LiveBundleClassic, StorePrecreated: true, ClaudeMDCompactSection: true,
		}, true
	case ArmSteer:
		return LiveArmState{
			Arm: arm, Plugin: true, Bundle: LiveBundleMods, StorePrecreated: true, ModEnabled: true, ModSteering: true,
		}, true
	}
	return LiveArmState{}, false
}
