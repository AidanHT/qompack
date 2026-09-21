package daemon

import (
	"errors"
	"path/filepath"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// NewWithLease borrows a singleton lease acquired before opening the store. The
// caller must keep its heartbeat alive, close all supplied writers after Run,
// and only then release the lease. New retains its existing ownership rules.
func NewWithLease(o Options, lease *Lock) (Daemon, error) {
	if lease == nil || filepath.Clean(lease.path) != filepath.Clean(filepath.Join(paths.Of(o.ProjectRoot).Run, lockFileName)) {
		return nil, errors.New("daemon: bootstrap lease belongs to a different project")
	}
	if err := lease.Heartbeat(); err != nil {
		return nil, err
	}
	d, err := New(o)
	if err != nil {
		return nil, err
	}
	impl, ok := d.(*daemon)
	if !ok {
		return nil, errors.New("daemon: unsupported bootstrap implementation")
	}
	impl.borrowedLease = true
	impl.setLock(lease)
	return impl, nil
}

func (d *daemon) releaseRunLease(lease *Lock, where string) error {
	if d.borrowedLease {
		return nil
	}
	return lease.ReleaseWithReport(d.log, where)
}

// ReleaseWithReport releases ownership and reports any remaining newer-format
// seal after the actual release, including when a composition root owns the lease.
func (l *Lock) ReleaseWithReport(log logging.Logger, where string) error {
	err := l.Release()
	reportSealDowngradeResidual(l, log, where)
	return err
}
