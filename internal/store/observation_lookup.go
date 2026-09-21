package store

import (
	"context"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// The pure COMMITTED observation → tool_use lookup (superseding coordinator decision 2026-09-21). The
// durable binding lives in the versioned sidecar index/observations.jsonl (observation_publication.go);
// this read never writes and never completes an intent. It returns a record only when the intent's
// legacy publication is complete; an incomplete, ambiguous or unreadable intent is reported degraded
// (unavailable), never as a false absence, and never mints anything. Completing a pending intent is
// ObservationRecovery.RecoverToolUseByObservation, deliberately separate.

// ObservationReader is a narrow, additive capability — reached by a type assertion, not a widening of
// the frozen §5.8 Store interface, like SupersedingRecorder and RefCounter.
type ObservationReader interface {
	// ToolUseByObservation returns the record COMMITTED for id. It reports core.ErrNotFound when no
	// intent and no record carry that observation (legacy evidence without a binding), and
	// core.ErrDegraded — never a false absence, never a new record — when the store is closed, the
	// binding is ambiguous, its intent is not yet published (unavailable), its intent is unreadable, or
	// the committed binding names a record the index no longer holds.
	ToolUseByObservation(ctx context.Context, id core.ObservationID) (ToolUseRecord, error)
}

var _ ObservationReader = (*FSStore)(nil)

// ToolUseByObservation is the committed lookup (ObservationReader).
func (s *FSStore) ToolUseByObservation(ctx context.Context, id core.ObservationID) (ToolUseRecord, error) {
	if err := s.use(); err != nil { // closed store → core.ErrDegraded, not a miss
		return ToolUseRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return ToolUseRecord{}, err
	}
	if !validObservationID(id) {
		return ToolUseRecord{}, fmt.Errorf("%w: observation id is empty or not valid text", core.ErrNotFound)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// Ambiguous and unavailable take PRIORITY over a committed binding: a later conflicting or torn
	// intent for one observation must not let the stale committed record be returned.
	if _, ambiguous := s.obsAmbiguous[id]; ambiguous {
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s is bound to more than one record", core.ErrDegraded, id)
	}
	if _, unavailable := s.obsUnavailable[id]; unavailable {
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s intent is unavailable", core.ErrDegraded, id)
	}
	if b, ok := s.obsBindings[id]; ok {
		if !b.committed {
			return ToolUseRecord{}, fmt.Errorf("%w: observation %s is reserved but not yet published", core.ErrDegraded, id)
		}
		rec, ok := s.toolUse[b.intent.rec.ID]
		if !ok {
			return ToolUseRecord{}, fmt.Errorf("%w: observation %s names an absent record", core.ErrDegraded, id)
		}
		return *rec, nil
	}
	// An unknown observation is a genuine miss ONLY when the sidecar's completeness is proved; while it
	// is uncertain a lost binding line could be this one, so report unavailable rather than false absence.
	if s.obsSidecarUncertain {
		return ToolUseRecord{}, fmt.Errorf("%w: observation %s: sidecar completeness unproved", core.ErrDegraded, id)
	}
	return ToolUseRecord{}, fmt.Errorf("%w: observation %s", core.ErrNotFound, id)
}
