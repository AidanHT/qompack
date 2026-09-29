package cli

import (
	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/store"
)

// A successful legacy-index scan cannot certify an unresolved observation
// intent. Reuse the production audit and preserve its explicit scan bounds.
func (s *fsckScan) checkPublication() fsckCheck {
	row := newFsckRow("publication", contract.SevWarn)
	opened, err := store.OpenReadOnly(s.root, config.Defaults(), store.Deps{Log: logging.Nop()})
	if err != nil {
		row.defect("publication evidence could not be opened read-only")
		return row.build()
	}
	defer func() { _ = opened.Close() }()
	auditor, ok := opened.(store.PublicationAuditor)
	if !ok {
		row.defect("publication audit is unavailable")
		return row.build()
	}
	audit, err := auditor.AuditPublication(s.ctx, store.DefaultPublicationScanCap())
	row.scan(audit.CapturesScanned + audit.ObjectsScanned)
	switch {
	case err != nil || (audit.Incomplete && !audit.IncompleteOnlyForNewerSchemas()):
		row.defect("publication audit is incomplete; zero observed gaps cannot certify completeness")
	case audit.Incomplete:
		// The ONLY cause is sidecars a newer build wrote: the captures row's "support gap rather than
		// damage" (Qompack.md §7.1), so the same files are not a defect here either. The row still
		// says, by count, what it could not certify, and any other cause beside it is a defect above.
		row.note("publication audit could not classify %d capture sidecar(s) written by a newer build; "+
			"their publication is not certified by this build, a support gap rather than damage",
			audit.NewerSchemaCaptures)
	}
	if audit.LegacyControlCaptures > 0 {
		row.note("%s", fsckLegacyControlCapturesNote(audit.LegacyControlCaptures))
	}
	if audit.LegacyLinkedPrompts > 0 {
		row.note("%s", fsckLegacyLinkedPromptsNote(audit.LegacyLinkedPrompts))
	}
	if audit.HasGaps() {
		row.defect("publication evidence includes %d unlinked captures and %d unindexed object candidates",
			audit.UnpublishedCaptures, audit.UnindexedObjectCandidates)
	}
	for _, note := range audit.Notes {
		row.note("%s", note)
	}
	return row.build()
}
