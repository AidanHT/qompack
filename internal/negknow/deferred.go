package negknow

import (
	"context"
	"errors"
	"fmt"

	"github.com/qompack/qompack/internal/sketch"
	"github.com/qompack/qompack/internal/store"
)

// ErrNotOpen is what a Deferred ledger answers while nothing has opened the ledger it stands for,
// for every question it cannot answer truthfully without one.
var ErrNotOpen = errors.New("negknow: no elimination ledger is open")

// Deferred returns a Ledger that stands in for the project's ledger before anything has opened it
// (coordinator decision D49 of the V6 close-out: no ledger yet is not an error). Every call asks
// open AGAIN, so a holder that took the stand-in before any ledger existed reaches the one a
// compaction or a ledger tool opens later. While open answers nil:
//
//   - All answers no records while the project at root holds no elimination (HasRecords), which is
//     the truth then, and ErrNotOpen once records exist on disk that no open ledger serves, so a
//     reader never mistakes unread records for none;
//   - every other method answers ErrNotOpen (Health is zero);
//   - Close is a no-op at all times: the stand-in owns nothing, and the ledger it reaches is its
//     opener's to close.
//
// It opens nothing and creates nothing itself.
func Deferred(open func() Ledger, root string) Ledger {
	return deferred{open: open, root: root}
}

type deferred struct {
	open func() Ledger
	root string
}

// ledger reports the ledger open answers now, or nil.
func (d deferred) ledger() Ledger {
	if d.open == nil {
		return nil
	}
	return d.open()
}

func notOpen(op string) error { return fmt.Errorf("negknow: %s: %w", op, ErrNotOpen) }

func (d deferred) All(ctx context.Context) ([]Record, error) {
	if l := d.ledger(); l != nil {
		return l.All(ctx)
	}
	has, err := HasRecords(d.root)
	switch {
	case has && err != nil:
		return nil, fmt.Errorf("%w: %w", notOpen("all"), err)
	case has:
		return nil, fmt.Errorf("%w: the project holds elimination records", notOpen("all"))
	}
	return nil, nil
}

func (d deferred) Record(ctx context.Context, r Record) (string, error) {
	if l := d.ledger(); l != nil {
		return l.Record(ctx, r)
	}
	return "", notOpen("record")
}

func (d deferred) Query(ctx context.Context, target, approach string, scope Scope) (Answer, error) {
	if l := d.ledger(); l != nil {
		return l.Query(ctx, target, approach, scope)
	}
	return Answer{}, notOpen("query")
}

func (d deferred) Get(ctx context.Context, id string) (Record, error) {
	if l := d.ledger(); l != nil {
		return l.Get(ctx, id)
	}
	return Record{}, notOpen("get")
}

func (d deferred) Active(ctx context.Context, scope Scope) ([]Record, error) {
	if l := d.ledger(); l != nil {
		return l.Active(ctx, scope)
	}
	return nil, notOpen("active")
}

func (d deferred) MarkStale(ctx context.Context, ids, because []string) error {
	if l := d.ledger(); l != nil {
		return l.MarkStale(ctx, ids, because)
	}
	return notOpen("mark stale")
}

func (d deferred) RefreshStaleness(ctx context.Context, s store.Store) ([]string, error) {
	if l := d.ledger(); l != nil {
		return l.RefreshStaleness(ctx, s)
	}
	return nil, notOpen("refresh staleness")
}

func (d deferred) RebuildBloom(ctx context.Context) (*sketch.Bloom, Health, error) {
	if l := d.ledger(); l != nil {
		return l.RebuildBloom(ctx)
	}
	return nil, Health{}, notOpen("rebuild bloom")
}

func (d deferred) Health() Health {
	if l := d.ledger(); l != nil {
		return l.Health()
	}
	return Health{}
}

func (deferred) Close() error { return nil }
