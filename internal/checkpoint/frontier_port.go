package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/core"
)

// FrontierAdvancer is the scheduler-facing port for incremental checkpoint work. The
// checkpointer retains sole ownership of drafts: callers supply closed segment IDs, never a
// *Draft, and receive the frontier that the writer actually reached.
type FrontierAdvancer interface {
	Advance(context.Context, core.SessionID, []core.SegmentID) (core.TurnIndex, error)
}

// NewFrontierAdvancer adapts a Writer and its current source supplier to FrontierAdvancer.
// Sources are resolved for each Begin so a sealed draft retry starts from the current durable
// seams; the adapter deliberately retains no draft between calls.
func NewFrontierAdvancer(w Writer, sources func() (SourceSet, error)) FrontierAdvancer {
	return frontierAdvancer{writer: w, sources: sources}
}

type frontierAdvancer struct {
	writer  Writer
	sources func() (SourceSet, error)
}

// Advance begins or reuses the writer's live draft, then advances the supplied closed segments.
// A Finalize or Abort may seal the draft after Begin and before Writer.Advance. That lifecycle
// race gets one fresh Begin and one retry; all other writer outcomes, including a partial
// core.ErrAlreadyEncoded result, retain the writer's adjudication for caller diagnostics.
func (a frontierAdvancer) Advance(ctx context.Context, session core.SessionID, segments []core.SegmentID) (core.TurnIndex, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if len(segments) == 0 {
		return 0, nil
	}

	draft, err := a.begin(ctx, session)
	if err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	frontier, err := a.writer.Advance(ctx, draft, segments)
	if !errors.Is(err, ErrDraftSealed) {
		return frontier, err
	}

	if err := ctx.Err(); err != nil {
		return frontier, err
	}
	draft, retryErr := a.begin(ctx, session)
	if retryErr != nil {
		return frontier, retryErr
	}
	if err := ctx.Err(); err != nil {
		return frontier, err
	}
	return a.writer.Advance(ctx, draft, segments)
}

func (a frontierAdvancer) begin(ctx context.Context, session core.SessionID) (*Draft, error) {
	if a.writer == nil || a.sources == nil {
		return nil, fmt.Errorf("checkpoint: frontier advance unavailable: %w", core.ErrDegraded)
	}
	src, err := a.sources()
	if err != nil {
		return nil, fmt.Errorf("checkpoint: frontier sources unavailable: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	draft, err := a.writer.Begin(ctx, session, 0, src)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, fmt.Errorf("checkpoint: frontier advance unavailable: %w", core.ErrDegraded)
	}
	return draft, nil
}
