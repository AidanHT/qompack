package store

import "context"

// Publication accounting includes the loaded intent state even when a crash
// preceded capture-link publication. Unknown or incomplete bindings cannot be
// hidden by an otherwise empty capture/object scan. This is diagnostic only.
func (s *FSStore) auditObservationBindings(ctx context.Context, budget *scanBudget, audit *PublicationAudit) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.obsSidecarUncertain || len(s.obsAmbiguous) > 0 || len(s.obsUnavailable) > 0 {
		audit.note("observation intent integrity is unavailable")
	}
	for _, binding := range s.obsBindings {
		if ctx.Err() != nil {
			audit.note("observation intent audit was interrupted")
			return
		}
		if budget.entriesLeft <= 0 {
			audit.Truncated = true
			audit.note("observation intent audit reached its entry budget")
			return
		}
		budget.entriesLeft--
		if binding == nil || !binding.committed {
			audit.note("observation publication remains incomplete")
		}
	}
}
