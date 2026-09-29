package daemon

import (
	"context"

	"github.com/qompack/qompack/internal/checkpoint"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Forked sessions (F-UAT06-1). `claude --resume <id> --fork-session` starts a new session id whose
// SessionStart source is "fork" and whose conversation is the parent's, so the fork continues the
// parent's task. No later hook says so, and no hook names the parent: the session.start route
// records the lineage when the fork starts (noteFork), and the compact rehydration reads it back
// (rehydrateService.lineage) so item 2 shows the parent's original, not the fork's first prompt.

// sessionSourceFork is the SessionStart source Claude Code reports for a forked session, spelled
// once, by the package that records the lineage.
const sessionSourceFork = checkpoint.LineageFork

// forkNoter is the checkpoint writer's lineage seam (checkpoint.FileWriter.NoteFork). It is reached
// by type assertion because checkpoint.Writer, SP-10's interface, does not declare it.
type forkNoter interface {
	NoteFork(context.Context, core.SessionID, core.UnixMilli) error
}

// noteFork records that s started as a fork at at, the host's stamp on its SessionStart. It is
// bookkeeping rather than an act, so it runs in every degradation mode, and a failure costs only
// the lineage: the fork's own first prompt then stands as its original, which is what every fork
// got before.
func (d *daemon) noteFork(ctx context.Context, s core.SessionID, at core.UnixMilli) {
	fn, ok := d.svc.Checkpoints.(forkNoter)
	if !ok {
		return
	}
	if err := fn.NoteFork(ctx, s, at); err != nil {
		d.log.Warn("daemon: a forked session's lineage was not recorded; its own first prompt stands as its original",
			"session", string(s), "err", err.Error())
	}
}

// lineage is sess's lineage record for the rehydration request, or nil when it has none or the
// record cannot be read — the unforked build, reported as a Warn.
func (s *rehydrateService) lineage(sess core.SessionID) *checkpoint.Lineage {
	if s.o.ProjectRoot == "" {
		return nil
	}
	l, err := checkpoint.ReadLineage(paths.Of(s.o.ProjectRoot), sess)
	if err != nil {
		s.o.Log.Warn("rehydrate: the session's lineage record is unreadable; it is rehydrated as unforked",
			"session", string(sess), "err", err.Error())
		return nil
	}
	return l
}
