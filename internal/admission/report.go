package admission

// This file is SP-21's rollback and quality reporting: the evidence that governs whether admission
// may ever be enabled, and the drill that gets it back off safely.
//
// Neither half enables anything by itself. PermitSchemaWrite says a write can be undone;
// Comparison.Enables says the quality gate did not block enablement. The feature switch stays off
// and the host allowlist stays empty regardless of what either reports.

// RollbackStep is one step of the rollback order.
type RollbackStep int

const (
	// StepDisableReplacement stops the transform. It is first because every later step is taken
	// while the pipeline is quiet; verifying a reader with replacement still running would be
	// asking about a schema that is being written under the question.
	StepDisableReplacement RollbackStep = iota

	// StepRestorePassThrough returns the exact original result to delivery.
	StepRestorePassThrough

	// StepVerifyReaderOrRestore proves the compatible old/new reader, or restores the verified
	// backup.
	StepVerifyReaderOrRestore

	// StepPreserveEvidence retains diagnostics and original captures for audit. It is last
	// because what to keep is only known once it is known which artifact survived — and it is
	// never optional, including on a rollback that failed.
	StepPreserveEvidence
)

// String renders a RollbackStep for the rollback artifact.
func (s RollbackStep) String() string {
	switch s {
	case StepDisableReplacement:
		return "disable-replacement"
	case StepRestorePassThrough:
		return "restore-pass-through"
	case StepVerifyReaderOrRestore:
		return "verify-reader-or-restore"
	case StepPreserveEvidence:
		return "preserve-evidence"
	default:
		return "unknown"
	}
}

// RollbackPlan returns the rollback order, which is fixed.
//
// It is a function returning a fresh slice rather than an exported variable so that a caller cannot
// reorder the shared one. The order is the safety property; a package-level slice anybody could
// sort would not be.
func RollbackPlan() []RollbackStep {
	return []RollbackStep{
		StepDisableReplacement,
		StepRestorePassThrough,
		StepVerifyReaderOrRestore,
		StepPreserveEvidence,
	}
}

// Backup is a verified copy taken before an admission schema write.
//
// Verification is unexported for the same reason Baseline's is, and against the same mistake: "we
// have nightly backups" is a belief about a cron job, while "this backup was verified" is a fact
// about bytes somebody read. Only the second may permit a write.
type Backup struct {
	// ID names the backup. Empty means there is none.
	ID string

	// Version is the schema version the backup holds.
	Version string

	verified bool
}

// Verified reports whether this backup was checked rather than assumed.
func (b Backup) Verified() bool { return b.verified }

// VerifyBackup returns a verified backup from a check the caller performed. An unnamed backup is
// not a backup and stays unverified.
func VerifyBackup(id, version string) Backup {
	return Backup{ID: id, Version: version, verified: id != ""}
}

// SchemaWrite is a proposed write of a new admission record schema version.
type SchemaWrite struct {
	// From is the schema version currently written.
	From string

	// To is the schema version this write would produce.
	To string

	// Downgrade declares that To is deliberately older than From.
	//
	// A backwards write is sometimes right and never accidental. Requiring it to be declared turns
	// a mis-ordered version constant from a silent format regression into a refusal, which is the
	// plan's "never silently downgrade a format".
	Downgrade bool
}

// PermitCause is why a schema write was permitted or refused.
type PermitCause int

const (
	// PermitNoReaderNoBackup refuses: no deployed reader can read the new artifact and there is
	// no verified backup to return to. That is the one move with no way out.
	PermitNoReaderNoBackup PermitCause = iota

	// PermitReaderCompatible permits: every deployed reader already reads the target version.
	PermitReaderCompatible

	// PermitBackupVerified permits: a verified backup exists, so the write can be undone.
	PermitBackupVerified

	// PermitUndeclaredDowngrade refuses a backwards write that was not declared as one.
	PermitUndeclaredDowngrade
)

// String renders a PermitCause for the pre-write proof.
func (c PermitCause) String() string {
	switch c {
	case PermitNoReaderNoBackup:
		return "no-reader-no-backup"
	case PermitReaderCompatible:
		return "reader-compatible"
	case PermitBackupVerified:
		return "backup-verified"
	case PermitUndeclaredDowngrade:
		return "undeclared-downgrade"
	default:
		return "unknown"
	}
}

// PermitSchemaWrite reports whether w may proceed, given the versions every deployed reader
// supports and whatever backup exists.
//
// readers must list EVERY deployed reader's version. One reader still on the old version is one
// process that will fail to read the new artifact, and averaging it away is how a rollout breaks a
// single host and calls itself compatible. An empty reader list proves nothing, so it falls to the
// backup.
func PermitSchemaWrite(w SchemaWrite, readers []string, backup Backup) (bool, PermitCause) {
	if w.To < w.From && !w.Downgrade {
		return false, PermitUndeclaredDowngrade
	}

	if allReadersSupport(readers, w.To) {
		return true, PermitReaderCompatible
	}
	if backup.Verified() {
		return true, PermitBackupVerified
	}
	return false, PermitNoReaderNoBackup
}

// allReadersSupport reports whether every listed reader reads version v. An empty list does not:
// no readers is no evidence, not universal agreement.
func allReadersSupport(readers []string, v string) bool {
	if len(readers) == 0 {
		return false
	}
	for _, r := range readers {
		if r != v {
			return false
		}
	}
	return true
}

// RollbackResult is the post-write drill's artifact.
type RollbackResult struct {
	// OK reports that the rollback reached a readable state — either the new artifact read back,
	// or the backup was restored.
	OK bool

	// Restored reports that the backup was used.
	Restored bool

	// EvidenceRetained is always true, and it is a field rather than an assumption so the
	// artifact says so in writing. Captures, diagnostics and evidence are never deleted to make a
	// retry look clean — least of all on a rollback that failed, which is where the temptation is.
	EvidenceRetained bool
}

// ValidateRollback runs the post-write drill against the artifact that actually exists.
//
// readable is whether the deployed reader could read the newly written artifact. When it could not,
// the verified backup is restored; when there is no verified backup, the rollback did not complete
// and says so rather than reporting a clean retry.
func ValidateRollback(readable bool, backup Backup) RollbackResult {
	switch {
	case readable:
		return RollbackResult{OK: true, EvidenceRetained: true}
	case backup.Verified():
		return RollbackResult{OK: true, Restored: true, EvidenceRetained: true}
	default:
		return RollbackResult{EvidenceRetained: true}
	}
}

// Layer is one of the three things T21-QUALITY-01 compares against unmodified output.
//
// Cost is deliberately not among them. Qompack.md §11.1 keeps task completion, constraint and
// regression failures and evidence recoverability separate from cost, and a cost-only result never
// enables admission.
type Layer int

const (
	// LayerTaskCompletion is whether the task still completes.
	LayerTaskCompletion Layer = iota

	// LayerConstraints is whether declared constraints still hold and regressions stay absent.
	LayerConstraints

	// LayerRecoverability is whether the evidence behind an answer can still be recovered.
	LayerRecoverability
)

// String renders a Layer for the comparison report.
func (l Layer) String() string {
	switch l {
	case LayerTaskCompletion:
		return "task-completion"
	case LayerConstraints:
		return "constraints"
	case LayerRecoverability:
		return "recoverability"
	default:
		return "unknown"
	}
}

// Verdict is a quality comparison's outcome.
//
// VerdictInconclusive is the zero value, and it is the shipped one. An unpopulated comparison
// reports that nothing was established, which is the answer that enables nothing.
type Verdict int

const (
	// VerdictInconclusive means the comparison established nothing — no observations, or no
	// predeclared margin to judge them against.
	VerdictInconclusive Verdict = iota

	// VerdictRetained means every layer held at or above its predeclared margin.
	VerdictRetained

	// VerdictRegressed means at least one layer fell below its margin.
	VerdictRegressed
)

// String renders a Verdict for the comparison report.
func (v Verdict) String() string {
	switch v {
	case VerdictInconclusive:
		return "inconclusive"
	case VerdictRetained:
		return "retained"
	case VerdictRegressed:
		return "regressed"
	default:
		return "unknown"
	}
}

// LayerResult is one layer's measurement against its predeclared margin.
//
// The margin is unexported and set only by Predeclare, which returns a result with no observations.
// That is the whole enforcement of "predeclared": the order the API permits is declare, then
// observe. There is no way to look at the counts and choose a threshold afterwards, and a
// LayerResult assembled as a composite literal has no margin at all, so it is inconclusive whatever
// its counts say.
type LayerResult struct {
	// Observed is how many held-out tasks contributed.
	Observed int

	// Retained is how many kept the unmodified result's answer.
	Retained int

	minRetained float64
	predeclared bool
}

// Predeclare starts a measurement against a minimum retention ratio, before anything is observed.
//
// The package declares no default margin. A default would be a threshold nobody predeclared, which
// is the thing this type exists to prevent.
func Predeclare(minRetained float64) LayerResult {
	return LayerResult{minRetained: minRetained, predeclared: true}
}

// Observe records a held-out run's counts against the predeclared margin.
func (l LayerResult) Observe(retained, total int) LayerResult {
	l.Retained, l.Observed = retained, total
	return l
}

// Verdict reports this layer's outcome.
//
// Zero observations are inconclusive rather than a pass. Zero regressions out of zero tasks is a
// perfect ratio and no information at all; the arithmetic answer and the honest answer differ, and
// the honest one wins. The margin is a minimum, so exactly at it is retained.
func (l LayerResult) Verdict() Verdict {
	if !l.predeclared || l.Observed == 0 {
		return VerdictInconclusive
	}
	if float64(l.Retained)/float64(l.Observed) < l.minRetained {
		return VerdictRegressed
	}
	return VerdictRetained
}

// Comparison is the three-layer comparison against unmodified output.
type Comparison struct {
	// Layers holds one result per Layer. A missing layer is inconclusive, because a layer nobody
	// measured is not a layer that passed.
	Layers map[Layer]LayerResult
}

// Verdict reports the comparison's outcome as the WORST of its three layers.
//
// Not an average and not a majority. Each layer is a separate promise, so recoverability regressing
// while task completion improves is still a regression in recoverability — averaging it away is how
// a real loss gets shipped behind a better headline number. A regression outranks an inconclusive:
// something measurably worse is a stronger fact than something unmeasured.
func (c Comparison) Verdict() Verdict {
	worst := VerdictRetained
	for _, layer := range []Layer{LayerTaskCompletion, LayerConstraints, LayerRecoverability} {
		result, ok := c.Layers[layer]
		if !ok {
			result = LayerResult{}
		}
		switch v := result.Verdict(); {
		case v == VerdictRegressed:
			return VerdictRegressed
		case v == VerdictInconclusive:
			worst = VerdictInconclusive
		}
	}
	return worst
}

// Enables reports that the quality gate did not block enablement.
//
// It never reports that admission may be turned on. The feature switch is still off, the host
// allowlist is still empty under B01, and every other T21 gate still applies. Reading this as
// sufficient is the mistake its name is chosen to resist.
func (c Comparison) Enables() bool { return c.Verdict() == VerdictRetained }
