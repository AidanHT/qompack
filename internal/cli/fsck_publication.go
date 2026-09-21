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
	if err != nil || audit.Incomplete {
		row.defect("publication audit is incomplete; zero observed gaps cannot certify completeness")
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
