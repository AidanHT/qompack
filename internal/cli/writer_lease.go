package cli

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// acquireWriterLease must precede every store open in the operator and daemon
// composition roots. Its release function belongs after all writer closes.
func acquireWriterLease(ctx context.Context, root string) (context.Context, *daemon.Lock, func() error, error) {
	l := paths.Of(root)
	for _, target := range []string{
		l.Dot, l.Run, l.Objects, l.Index, l.Sketches, l.DAG,
		l.Grammar, l.Checkpoints, l.Pins, l.Eval, l.Records, l.State, l.Spool,
		l.Logs, l.Metrics, l.Tmp, l.Migrate, l.Backup,
	} {
		if !paths.ResolvesInside(root, target) {
			return nil, nil, nil, errors.New("writer lease: a store path escapes the project or cannot be resolved safely")
		}
	}
	addr, err := ipc.Resolve(root)
	if err != nil {
		return nil, nil, nil, err
	}
	lease, err := daemon.AcquireLock(root, addr, core.SystemClock())
	if err != nil {
		return nil, nil, nil, err
	}
	workCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	stopHeartbeat := make(chan struct{})
	var heartbeatErr error
	go func() {
		defer close(done)
		// The lock expires after 90 seconds; this matches the daemon's cadence.
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-ticker.C:
				if err := lease.Heartbeat(); err != nil {
					heartbeatErr = err
					cancel()
					return
				}
			}
		}
	}()
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			close(stopHeartbeat)
			cancel()
			<-done
			log := &hookLogger{root: root}
			defer log.closeSink()
			releaseErr = errors.Join(heartbeatErr, lease.ReleaseWithReport(log, "writer owner"))
			if releaseErr != nil {
				log.Loud("writer lease shutdown failed; inspect retained state before recovery", "err", releaseErr.Error())
			}
		})
		return releaseErr
	}
	return workCtx, lease, release, nil
}
