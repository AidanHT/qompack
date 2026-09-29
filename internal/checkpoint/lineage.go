package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Session lineage (F-UAT06-1).
//
// `claude --resume <id> --fork-session` starts a NEW session id whose conversation is the parent's,
// with SessionStart source "fork". The fork continues the parent's task: its original intent is
// the parent's, and its own prompts — its first one included — are later statements of that same
// task. No hook names the parent, though, and the fork's own first prompt is what L0 captures as
// prompt_<fork>_0, so without a record of the lineage the fork's checkpoints started from that
// prompt and the rehydrator presented it as "the verbatim original user intent" (and, on the build
// the live lane ran, overrode the parent's original with it as an intent_mismatch).
//
// NoteFork records the lineage when the fork starts: the project's newest checkpoint at that moment
// is taken as the one the fork continues, since the host says nothing more specific, and the record
// names that checkpoint's session and the session whose first prompt its original is. Every
// checkpoint of the fork then inherits its intent from that checkpoint (forkIntent), and the
// rehydrator reads the same record to verify the inherited original against its own L0 capture
// and to say where it came from.
//
// The record is written once and never rewritten, so a replayed SessionStart cannot re-point a fork
// at a checkpoint sealed after it started. When no checkpoint exists yet, the record says the
// parent is unknown rather than guessing, and the fork's own first prompt stands as its original.

// LineageFork is the SessionStart source Claude Code reports for a forked session, and the only
// lineage this package records.
const LineageFork = "fork"

// lineageVersion is the lineage record's wire version.
const lineageVersion = 1

// Lineage is state/lineage-<session>.json: that Session continues another session's conversation.
type Lineage struct {
	// Version is lineageVersion.
	Version int `json:"v"`
	// Session is the session this record describes.
	Session core.SessionID `json:"session"`
	// Source is how the session started: LineageFork.
	Source string `json:"source"`
	// ParentSeq is the checkpoint the session continues: the project's newest when it started.
	// Zero when the project had none, which leaves the parent unknown.
	ParentSeq core.CheckpointSeq `json:"parent_seq,omitempty"`
	// ParentSession is the session that sealed ParentSeq.
	ParentSession core.SessionID `json:"parent_session,omitempty"`
	// OriginSession is the session whose first prompt ParentSeq's original intent is: the parent,
	// or — when the parent was itself a fork — the session that fork continued, and so on.
	OriginSession core.SessionID `json:"origin_session,omitempty"`
	// At is when the record was written.
	At core.UnixMilli `json:"at"`
}

// lineagePath is state/lineage-<session>.json.
func lineagePath(l paths.Layout, s core.SessionID) string {
	return filepath.Join(l.State, "lineage-"+string(s)+".json")
}

// NoteFork records that session s was started as a fork (SessionStart source "fork"), naming the
// project's newest verifying checkpoint as the one it continues. It is idempotent: an existing
// record for s is left exactly as it is.
func (w *FileWriter) NoteFork(ctx context.Context, s core.SessionID) error {
	if err := checkSessionComponent(s); err != nil {
		return err
	}
	p := lineagePath(w.l, s)
	if _, err := os.Lstat(paths.Long(p)); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}

	rec := Lineage{Version: lineageVersion, Session: s, Source: LineageFork, At: core.NowMilli(w.clk)}
	if seq := maxSeq(w.l); seq != 0 {
		pc, _, err := w.reader.Get(ctx, seq)
		if err != nil {
			// The newest checkpoint cannot vouch for itself; the fork's parent stays unknown rather
			// than being guessed from an artifact that does not verify.
			w.log.Warn("checkpoint: the checkpoint a fork would continue is unreadable; its parent is unknown",
				"session", string(s), "seq", int(seq), "err", err.Error())
		} else {
			rec.ParentSeq, rec.ParentSession, rec.OriginSession = seq, pc.Session, pc.Session
			if pl, perr := ReadLineage(w.l, pc.Session); perr == nil && pl != nil && pl.OriginSession != "" {
				rec.OriginSession = pl.OriginSession
			}
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	if err := paths.WriteAtomic(p, b, draftPerm); err != nil {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	w.log.Info("checkpoint: forked session recorded", "session", string(s),
		"parent_seq", int(rec.ParentSeq), "parent_session", string(rec.ParentSession),
		"origin_session", string(rec.OriginSession))
	return nil
}

// ReadLineage returns session s's lineage record, or nil with no error when s has none. A record
// that does not parse, or that names another session, is an error: it is never read as absent.
func ReadLineage(l paths.Layout, s core.SessionID) (*Lineage, error) {
	if err := checkSessionComponent(s); err != nil {
		return nil, err
	}
	b, err := paths.ReadFileShared(lineagePath(l, s))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	var rec Lineage
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	if rec.Version != lineageVersion || rec.Session != s {
		return nil, fmt.Errorf("checkpoint: lineage of %s: version %d for session %q: %w",
			s, rec.Version, rec.Session, core.ErrContract)
	}
	return &rec, nil
}

// forkIntent is the user intent a forked session inherits: its parent checkpoint's original and
// evolution, verbatim (G2.3: copied, never regenerated).
type forkIntent struct {
	original  string
	evolution []string
}

// forkIntentFor returns the intent session s inherits as a fork, or nil when s is not one, or its
// parent is unknown. own is s's own checkpoint the draft descends from, if any: when the parent
// checkpoint no longer verifies, the fork's own last checkpoint — which inherited from it — still
// carries the same original.
func (w *FileWriter) forkIntentFor(ctx context.Context, s core.SessionID, own *Checkpoint) *forkIntent {
	rec, err := ReadLineage(w.l, s)
	if err != nil {
		w.log.Warn("checkpoint: a session's lineage record is unreadable; it is treated as unforked",
			"session", string(s), "err", err.Error())
		return nil
	}
	if rec == nil || rec.ParentSeq == 0 {
		return nil
	}
	pc, _, gerr := w.reader.Get(ctx, rec.ParentSeq)
	switch {
	case gerr == nil:
		return &forkIntent{original: pc.UserIntent.Original, evolution: slices.Clone(pc.UserIntent.Evolution)}
	case own != nil && own.UserIntent.Original != "":
		w.log.Warn("checkpoint: the checkpoint a fork continues is unreadable; using the fork's own chain",
			"session", string(s), "parent_seq", int(rec.ParentSeq), "err", gerr.Error())
		return &forkIntent{original: own.UserIntent.Original, evolution: slices.Clone(own.UserIntent.Evolution)}
	default:
		w.log.Warn("checkpoint: the checkpoint a fork continues is unreadable; its own first prompt stands",
			"session", string(s), "parent_seq", int(rec.ParentSeq), "err", gerr.Error())
		return nil
	}
}
