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
	"github.com/qompack/qompack/internal/store"
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
// NoteFork records the lineage when the fork starts. The parent is INFERRED, since the host says
// nothing more specific: it is the session the user was last talking to, the one whose prompt is
// the project's newest at the moment the fork started (store.LatestPrompt), which is what Claude
// Code's own "most recent session" means for `--continue`. A store that cannot answer that falls
// back to the session that sealed the project's newest checkpoint.
//
// What the fork inherits is what its conversation holds: the parent's prompts up to the moment the
// fork started, read from the parent's own prompt records (and, when the parent was itself a fork,
// its parent's up to the moment IT started, and so on). The origin session's first prompt is the
// fork's original; every later one, the fork's own included, is evolution. The project's newest
// checkpoint at that moment, when the parent sealed it (ParentSeq), is only the fallback copy for
// when those records cannot be enumerated: reading the checkpoint instead lost every correction the
// parent made after its last compaction, and a parent that never compacted had nothing to inherit.
//
// The rehydrator reads the same record to verify the inherited original against the origin
// session's own L0 capture and to say where it came from.
//
// The record is written once and never rewritten, so a replayed SessionStart cannot re-point a fork
// at a session that spoke after it started. When no other session has said anything, the record
// says the parent is unknown rather than guessing, and the fork's own first prompt stands as its
// original. Unlike the other documents under state/, the record cannot be rebuilt from anything
// else (docs/architecture.md), and a backup carries it with the rest of state/.

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
	// ParentSeq is the project's newest verifying checkpoint when the session started, recorded when
	// the parent sealed it: the copy of the inherited intent used when the parent's prompt records
	// cannot be read. Zero otherwise — the parent never compacted, another session compacted since,
	// or the parent is unknown. Finding an older checkpoint of the parent would mean loading every
	// newer one, and NoteFork runs on the session.start route.
	ParentSeq core.CheckpointSeq `json:"parent_seq,omitempty"`
	// ParentSession is the session this one continues: the one whose prompt was the project's
	// newest when it started. Empty when no other session had said anything, which leaves the
	// parent unknown.
	ParentSession core.SessionID `json:"parent_session,omitempty"`
	// OriginSession is the session whose first prompt the inherited original is: the parent, or —
	// when the parent was itself a fork — the session that fork continued, and so on.
	OriginSession core.SessionID `json:"origin_session,omitempty"`
	// At is when the session started (the host's stamp on its SessionStart). The parent's prompts
	// stamped at or before it are the ones this session's conversation holds.
	At core.UnixMilli `json:"at"`
}

// lineagePath is state/lineage-<session>.json.
func lineagePath(l paths.Layout, s core.SessionID) string {
	return filepath.Join(l.State, "lineage-"+string(s)+".json")
}

// NoteFork records that session s was started as a fork (SessionStart source "fork") at at, the
// host's stamp on that SessionStart (zero: now), naming the session it continues (forkParent). It
// is idempotent: an existing record for s is left exactly as it is.
func (w *FileWriter) NoteFork(ctx context.Context, s core.SessionID, at core.UnixMilli) error {
	if err := checkSessionComponent(s); err != nil {
		return err
	}
	p := lineagePath(w.l, s)
	if _, err := os.Lstat(paths.Long(p)); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}

	if at <= 0 {
		at = core.NowMilli(w.clk)
	}
	rec := Lineage{Version: lineageVersion, Session: s, Source: LineageFork, At: at}
	newest, newestSeq := w.newestCheckpoint(ctx, s)
	if parent := w.forkParent(ctx, s, at, newest); parent != "" {
		rec.ParentSession, rec.OriginSession = parent, parent
		if pl, perr := ReadLineage(w.l, parent); perr == nil && pl != nil && pl.OriginSession != "" {
			rec.OriginSession = pl.OriginSession
		}
		if newest == parent {
			rec.ParentSeq = newestSeq
		}
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	// WriteAtomic flushes the staged bytes before its rename and the directory after it on POSIX; on
	// NTFS the next flush on the volume carries the rename (docs/architecture.md, the durability
	// premise), and sealing a checkpoint of the fork is such a flush. So the record is durable
	// before anything is sealed from it.
	if err := paths.WriteAtomic(p, b, draftPerm); err != nil {
		return fmt.Errorf("checkpoint: lineage of %s: %w", s, err)
	}
	w.log.Info("checkpoint: forked session recorded", "session", string(s),
		"parent_session", string(rec.ParentSession), "parent_seq", int(rec.ParentSeq),
		"origin_session", string(rec.OriginSession))
	return nil
}

// forkParent infers the session a fork of s, started at at, continues: the session whose prompt is
// the project's newest at or before at, other than s. A store without store.LatestPrompt, or one
// that cannot answer, falls back to newest, the session that sealed the project's newest verifying
// checkpoint. Empty means unknown: no other session had said anything.
func (w *FileWriter) forkParent(ctx context.Context, s core.SessionID, at core.UnixMilli, newest core.SessionID) core.SessionID {
	if lp, ok := w.coldSources().Store.(store.LatestPrompt); ok {
		rec, err := lp.LatestPrompt(ctx, s, at)
		switch {
		case err == nil:
			return rec.Session
		case errors.Is(err, core.ErrNotFound):
			return ""
		default:
			w.log.Warn("checkpoint: the newest prompt before a fork is unknown; its parent is taken from the newest checkpoint",
				"session", string(s), "err", err.Error())
		}
	}
	return newest
}

// newestCheckpoint names the session that sealed the project's newest verifying checkpoint, and that
// checkpoint, or nothing when there is none, it does not verify, or s sealed it (a fork cannot
// continue itself).
func (w *FileWriter) newestCheckpoint(ctx context.Context, s core.SessionID) (core.SessionID, core.CheckpointSeq) {
	seq := maxSeq(w.l)
	if seq == 0 {
		return "", 0
	}
	pc, _, err := w.reader.Get(ctx, seq)
	if err != nil {
		// The newest checkpoint cannot vouch for itself: it is neither a parent nor a copy.
		w.log.Warn("checkpoint: the project's newest checkpoint is unreadable; a fork cannot inherit from it",
			"session", string(s), "seq", int(seq), "err", err.Error())
		return "", 0
	}
	if pc.Session == s {
		return "", 0
	}
	return pc.Session, seq
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

// forkIntent is the user intent a forked session inherits: what its conversation held when it
// started. It is read from the ancestry's own prompt records when they can be enumerated
// (fromRecords: origin and inherited), and otherwise copied from a checkpoint (original and
// evolution: the parent's newest when the fork started, or the fork's own chain), verbatim either
// way (G2.3: copied, never regenerated). The checkpoint copy is also the seed a refresh keeps when
// the origin's first prompt is no longer readable.
type forkIntent struct {
	fromRecords bool
	// origin is the origin session's first prompt record, or nil when it had none in range.
	origin *store.ToolUseRecord
	// inherited are every later prompt record of the ancestry, oldest first: the origin's own,
	// then each descendant's up to the moment the next one forked from it.
	inherited []store.ToolUseRecord
	original  string
	evolution []string
}

// forkIntentFor returns the intent session s inherits as a fork, or nil when s is not one, or its
// parent is unknown. st is the store the prompt records are read from. own is s's own checkpoint the
// draft descends from, if any: the fork's own chain carries what it inherited, so it is the copy
// used when the parent's checkpoint does not verify and, when s's lineage record itself is
// unreadable, the only evidence left that s is a fork.
func (w *FileWriter) forkIntentFor(ctx context.Context, st store.Store, s core.SessionID, own *Checkpoint) *forkIntent {
	rec, err := ReadLineage(w.l, s)
	if err != nil {
		if own == nil {
			if latest, ok := w.ownLatest(ctx, s); ok {
				own = &latest
			}
		}
		if own != nil && own.UserIntent.Original != "" {
			w.log.Warn("checkpoint: a forked session's lineage record is unreadable; its own chain keeps the intent it inherited",
				"session", string(s), "err", err.Error())
			return &forkIntent{original: own.UserIntent.Original, evolution: slices.Clone(own.UserIntent.Evolution)}
		}
		w.log.Warn("checkpoint: a session's lineage record is unreadable; it is treated as unforked",
			"session", string(s), "err", err.Error())
		return nil
	}
	if rec == nil || rec.ParentSession == "" {
		return nil
	}

	fi := &forkIntent{}
	if rec.ParentSeq != 0 {
		pc, _, gerr := w.reader.Get(ctx, rec.ParentSeq)
		if gerr == nil {
			fi.original, fi.evolution = pc.UserIntent.Original, slices.Clone(pc.UserIntent.Evolution)
		} else {
			w.log.Warn("checkpoint: the parent checkpoint of a fork is unreadable; its prompt records are the only source",
				"session", string(s), "parent_seq", int(rec.ParentSeq), "err", gerr.Error())
		}
	}
	if fi.original == "" && own != nil && own.UserIntent.Original != "" {
		fi.original, fi.evolution = own.UserIntent.Original, slices.Clone(own.UserIntent.Evolution)
	}
	fi.origin, fi.inherited, fi.fromRecords = w.ancestorPrompts(ctx, st, rec)
	if !fi.fromRecords && fi.original == "" {
		w.log.Warn("checkpoint: nothing a fork inherited can be read; its own first prompt stands",
			"session", string(s), "parent_session", string(rec.ParentSession))
		return nil
	}
	return fi
}

// ancestorPrompts reads the prompt records a fork's conversation holds, from the sessions it came
// through: its parent's stamped at or before the moment it started, and — when the parent was
// itself a fork — the grandparent's at or before the moment the parent started, and so on, to a
// session that is not a fork (or whose parent is unknown). It returns that origin session's first
// prompt, every other one oldest first, and false when any session's records cannot be enumerated.
// A session already visited ends the walk, so damaged records cannot loop it.
func (w *FileWriter) ancestorPrompts(ctx context.Context, st store.Store, rec *Lineage) (*store.ToolUseRecord, []store.ToolUseRecord, bool) {
	var spans [][]store.ToolUseRecord // nearest ancestor first
	visited := map[core.SessionID]bool{rec.Session: true}
	for cur := rec; cur != nil && cur.ParentSession != "" && !visited[cur.ParentSession]; {
		parent := cur.ParentSession
		visited[parent] = true
		recs, ok := sessionPromptRecords(ctx, st, parent)
		if !ok {
			return nil, nil, false
		}
		spans = append(spans, stampedBy(recs, cur.At))
		next, err := ReadLineage(w.l, parent)
		if err != nil {
			w.log.Warn("checkpoint: an ancestor's lineage record is unreadable; the fork's history starts at that ancestor",
				"session", string(rec.Session), "ancestor", string(parent), "err", err.Error())
			break
		}
		cur = next
	}
	if len(spans) == 0 {
		return nil, nil, false
	}
	var first *store.ToolUseRecord
	var later []store.ToolUseRecord
	for i := len(spans) - 1; i >= 0; i-- {
		recs := spans[i]
		if i == len(spans)-1 && len(recs) > 0 {
			origin := recs[0]
			first = &origin
			recs = recs[1:]
		}
		later = append(later, recs...)
	}
	return first, later, true
}

// stampedBy keeps the records stamped at or before at, in the order given; at zero keeps them all.
func stampedBy(recs []store.ToolUseRecord, at core.UnixMilli) []store.ToolUseRecord {
	if at <= 0 {
		return recs
	}
	out := recs[:0:0]
	for _, r := range recs {
		if r.TS <= at {
			out = append(out, r)
		}
	}
	return out
}
