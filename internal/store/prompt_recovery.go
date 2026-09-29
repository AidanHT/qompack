package store

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/qompack/qompack/internal/core"
)

// promptScanLimit bounds the tool-use index a prompt capability below will scan in full. Past it
// the scan is unavailable (core.ErrDegraded), never a partial answer. It is the bound PromptFrontier
// has always had, shared with EarliestPrompt rather than restated.
const promptScanLimit = 1 << 18

// promptTool is the Tool a verbatim prompt capture is recorded under (observer's userPromptSubmit;
// store may not import observer, 00-ARCHITECTURE.md §3.2).
const promptTool = "UserPromptSubmit"

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
	if len(s.toolUse) > promptScanLimit {
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
		if rec.Tool == promptTool || rec.Tool == "SubagentStop" {
			turn++
		}
		if turn > next {
			next = turn
		}
		if rec.Tool == promptTool && rec.ArgsDigest == digest {
			if found.ID != "" {
				return 0, ToolUseRecord{}, core.ErrDegraded
			}
			found = *rec
		}
	}
	return next, found, nil
}

// SessionPrompts is an optional capability internal/checkpoint reads a session's user intent through:
// every verbatim prompt capture of ONE session, in turn order. The dependence graph cannot answer
// that question — its userprompt node is keyed by turn alone, so two sessions' prompts at one turn
// share a node and the later capture's reference replaces the earlier one's.
type SessionPrompts interface {
	SessionPrompts(context.Context, core.SessionID) ([]ToolUseRecord, error)
}

// SessionPrompts returns session's UserPromptSubmit records in ascending turn order, ties broken by
// id, and an empty slice when it has none. Like EarliestPrompt it is one bounded scan of the loaded
// index: past promptScanLimit records it is core.ErrDegraded, never a partial answer.
func (s *FSStore) SessionPrompts(ctx context.Context, session core.SessionID) ([]ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.toolUse) > promptScanLimit {
		return nil, core.ErrDegraded
	}
	out := []ToolUseRecord{}
	for _, rec := range s.toolUse {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if rec.Session == session && rec.Tool == promptTool {
			out = append(out, *rec)
		}
	}
	slices.SortFunc(out, func(a, b ToolUseRecord) int {
		if c := cmp.Compare(a.Turn, b.Turn); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

// LatestPrompt is an optional capability internal/checkpoint infers a forked session's parent
// through (checkpoint/lineage.go): the newest verbatim prompt capture of any OTHER session at or
// before a moment. The host names no parent for `--fork-session`, and the session the user was last
// talking to when the fork started is the one Claude Code's own "most recent" means.
type LatestPrompt interface {
	LatestPrompt(ctx context.Context, exclude core.SessionID, at core.UnixMilli) (ToolUseRecord, error)
}

// LatestPrompt returns the UserPromptSubmit record with the highest TS at or before at whose session
// is not exclude, the higher turn of two equal stamps and then the greater id, or core.ErrNotFound
// when there is none. Like EarliestPrompt it is one bounded scan of the loaded index: past
// promptScanLimit records it is core.ErrDegraded.
func (s *FSStore) LatestPrompt(ctx context.Context, exclude core.SessionID, at core.UnixMilli) (ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return ToolUseRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return ToolUseRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.toolUse) > promptScanLimit {
		return ToolUseRecord{}, core.ErrDegraded
	}
	var best *ToolUseRecord
	for _, rec := range s.toolUse {
		if err := ctx.Err(); err != nil {
			return ToolUseRecord{}, err
		}
		if rec.Session == exclude || rec.Tool != promptTool || rec.TS > at {
			continue
		}
		if best == nil || newerPrompt(rec, best) {
			best = rec
		}
	}
	if best == nil {
		return ToolUseRecord{}, fmt.Errorf("%w: no prompt of another session at or before %d", core.ErrNotFound, int64(at))
	}
	return *best, nil
}

// newerPrompt orders two prompt records for LatestPrompt: the later stamp, then the higher turn,
// then the greater id, so the answer does not depend on map iteration order.
func newerPrompt(a, b *ToolUseRecord) bool {
	if a.TS != b.TS {
		return a.TS > b.TS
	}
	if a.Turn != b.Turn {
		return a.Turn > b.Turn
	}
	return a.ID > b.ID
}

// PromptOrder is an optional capability internal/rehydrate uses to check that prompt_<s>_0 is the
// prompt the host sent first (SP08-D3, owner decision D35). A prompt's turn is its publication
// position, and a prompt that reached only a hook's client spool can be published after one its
// host sent later; its record's TS is the host's stamp, so the earliest-stamped prompt shows the
// order the turn numbers cannot.
type PromptOrder interface {
	EarliestPrompt(context.Context, core.SessionID) (ToolUseRecord, error)
}

// EarliestPrompt returns session's UserPromptSubmit record with the lowest TS, the lower turn of two
// equal stamps, or core.ErrNotFound when the session has none. Like PromptFrontier it is one bounded
// scan of the loaded index: past promptScanLimit records it is core.ErrDegraded.
func (s *FSStore) EarliestPrompt(ctx context.Context, session core.SessionID) (ToolUseRecord, error) {
	if err := s.use(); err != nil {
		return ToolUseRecord{}, err
	}
	if err := ctx.Err(); err != nil {
		return ToolUseRecord{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.toolUse) > promptScanLimit {
		return ToolUseRecord{}, core.ErrDegraded
	}
	var best *ToolUseRecord
	for _, rec := range s.toolUse {
		if err := ctx.Err(); err != nil {
			return ToolUseRecord{}, err
		}
		if rec.Session != session || rec.Tool != promptTool {
			continue
		}
		if best == nil || rec.TS < best.TS || (rec.TS == best.TS && rec.Turn < best.Turn) {
			best = rec
		}
	}
	if best == nil {
		return ToolUseRecord{}, fmt.Errorf("%w: no prompt for session %s", core.ErrNotFound, session)
	}
	return *best, nil
}
