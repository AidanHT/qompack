package store

import (
	"context"
	"math"

	"github.com/qompack/qompack/internal/core"
)

// PromptRecovery is an optional capability for leased prompt delivery. The digest
// includes the delivery identity in the prompt's synthetic arguments; the wire
// record and public prompt ID retain their existing shapes.
type PromptRecovery interface {
	PromptFrontier(context.Context, core.SessionID, core.Hash) (core.TurnIndex, ToolUseRecord, error)
}

// PromptFrontier recovers the next turn and an interrupted publication. A bounded
// incomplete scan is unavailable, never evidence that a delivery is new.
func (s *FSStore) PromptFrontier(ctx context.Context, session core.SessionID, digest core.Hash) (core.TurnIndex, ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return 0, ToolUseRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, ToolUseRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	const limit = 1 << 18
	if len(s.toolUse) > limit {
		return 0, ToolUseRecord{}, core.ErrDegraded
	}
	var next core.TurnIndex
	var found ToolUseRecord
	for _, rec := range s.toolUse {
		if err := ctx.Err(); err != nil {
			return 0, ToolUseRecord{}, err
		}
		if rec.Session != session {
			continue
		}
		turn := rec.Turn
		if turn < 0 || turn == math.MaxInt {
			return 0, ToolUseRecord{}, core.ErrDegraded
		}
		if rec.Tool == "UserPromptSubmit" || rec.Tool == "SubagentStop" {
			turn++
		}
		if turn > next {
			next = turn
		}
		if rec.Tool == "UserPromptSubmit" && rec.ArgsDigest == digest {
			if found.ID != "" {
				return 0, ToolUseRecord{}, core.ErrDegraded
			}
			found = *rec
		}
	}
	return next, found, nil
}
