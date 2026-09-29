package checkpoint

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/dag"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
	"github.com/qompack/qompack/internal/store"
)

// maxSeqCollisionRetries bounds the O_EXCL retry loop in Finalize. A collision means another
// process claimed the sequence number between maxSeq and CreateNew, which is rare and self-
// resolving; a run of eight means something is claiming sequence numbers faster than this process
// can write, and reporting core.ErrAppendOnly is more useful than spinning.
const maxSeqCollisionRetries = 8

// pointerWhyUnknownTool is the whole Why of a file pointer whose producing tool cannot be
// resolved from the graph (§8): a read-only touch, or a dangling edge. It is a complete reason,
// not a placeholder — the file was referenced, and that is everything the graph knows.
const pointerWhyUnknownTool = "referenced"

// goalMaxRunes caps the derived CurrentWork.Goal at §8's 160 runes: one sentence of the most
// recent prompt, never a paragraph.
const goalMaxRunes = 160

// ttlStateUnknown is the CacheInfo.TTLState every fresh draft starts at (§8 step 4): the writer
// has no scheduler access (§3.2), so it cannot know better until SetCache is called.
const ttlStateUnknown = "unknown"

// maxSessionIDRunes bounds a session id used as a filename component. It is a filesystem guard,
// not an Appendix-C value: "draft-" + id + ".json" has to stay comfortably inside every
// platform's 255-byte component limit even when the id is multi-byte throughout.
const maxSessionIDRunes = 64

// ErrDraftSealed reports that a draft has been closed to further content — Finalize has taken its
// snapshot, or Abort has discarded it — and can accept no more segments.
//
// It is a distinct sentinel rather than a generic failure because the caller's correct response is
// specific: re-Begin the session and encode into the successor draft. Treating it as a store
// failure and dropping the batch would leave the segments encoded into nothing.
var ErrDraftSealed = errors.New("checkpoint: draft is sealed")

// FileWriter is the L4 checkpointer's Writer over one project root (§8). Begin opens or resumes
// a draft, Advance encodes closed segments into it during idle windows (O5), Abort discards it;
// Finalize — the §13 half of the seam — lands in finalize.go.
//
// The writer deliberately holds NO store, ledger, graph or estimator of its own: everything a
// checkpoint is built from arrives in the SourceSet handed to Begin and lives on the draft
// (d.src). That keeps §5.14's "everything comes from the sources" rule literal — a FileWriter
// cannot reach content its caller did not explicitly wire.
type FileWriter struct {
	root string
	// l is the root's Layout, derived once in OpenWriter. §13's Finalize writes through w.l, and
	// Begin's draft persistence and manifest reads resolve against it.
	l   paths.Layout
	cfg config.Config
	log logging.Logger
	m   obs.Registry
	clk core.Clock
	// reader is built by OpenWriter over the same root: Begin copies the verbatim original
	// intent out of the parent checkpoint through it (§8 step 4), which is how the original
	// survives arbitrarily many checkpoint generations (G2.3).
	reader Reader

	mu     sync.Mutex
	drafts map[core.SessionID]*Draft
	// lastSrc is the SourceSet most recently handed to Begin, kept so the daemon's cold paths
	// (coldSources) can reach the live seams without re-wiring them.
	lastSrc SourceSet
	// issuedSeq is the highest sequence number this writer has handed to a draft, whether or not
	// that draft has been finalized. maxSeq reads only the MANIFEST, and only Finalize appends to
	// it, so the manifest alone cannot tell one open draft's number from another's: the daemon
	// Begins a draft for EVERY live session on every idle tick, and all of them would take the
	// same seq. The first to finalize then claims it, and the rest are set aside as .stale.json on
	// their next Begin — losing their accumulated state while their segments stay flagged
	// encoded-once and therefore unreachable forever.
	issuedSeq core.CheckpointSeq
	// barriers are the durability calls Finalize seals a checkpoint through: the artifact's file sync
	// (paths.CreateNew) and the directory and MANIFEST syncs (paths.AppendManifest). The zero value
	// is the real thing and is all production ever uses; a test sets it to count the barriers or to
	// cut the seal at one of them the way a power loss would (export_test.go).
	barriers paths.Barriers
	// begins holds one admission gate per session, so the whole of Begin — the file read, the
	// manifest read and the four source reads included — is serialized per session. w.mu itself
	// must NOT be held across that I/O, and without a second gate Begin is a check-then-act: two
	// concurrent calls for one session both observe no live draft, both publish, and the loser's
	// draft is displaced while still holding the same file path.
	begins map[core.SessionID]*sessionGate
	// handoff carries a sealed draft's prompt-text cache to the successor Finalize opens for the
	// same session (afterSeal), so the successor's intent refresh does not re-read every prompt of
	// the session inside the PreCompact window. Begin consumes the entry; it is never read twice.
	handoff map[core.SessionID]map[core.ToolUseID]string
}

// sessionGate is one session's Begin admission gate, reference-counted so the map does not grow
// one dead entry per session for the life of the daemon.
type sessionGate struct {
	mu   sync.Mutex
	refs int
}

// OpenWriter returns the real Writer rooted at root (§8). It narrows the SP-01 contract's return
// type to the concrete *FileWriter — a widening of what callers get, not a change to the
// parameter list — and installs the package observers exactly once, so the receiver-less
// functions (ExtractDecisions, Truncate, fromStore's counter) observe through the same logger
// and registry as the writer's own methods.
//
// Nil observers and a nil clock degrade to the package defaults (no-op logger, no-op registry,
// the system clock) rather than failing: a half-wired composition root should fail on its
// MISSING sources at Begin, with Validate's message naming the gap, not on its optional
// observability.
func OpenWriter(root string, cfg config.Config, log logging.Logger, m obs.Registry, clk core.Clock) (*FileWriter, error) {
	if log == nil {
		log = logging.Nop()
	}
	if m == nil {
		m = nopRegistry{}
	}
	if clk == nil {
		clk = core.SystemClock()
	}
	SetObservers(log, m)

	r, err := OpenReader(root, log, m)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: open writer: %w", err)
	}
	return &FileWriter{
		root:   root,
		l:      paths.Of(root),
		cfg:    cfg,
		log:    log,
		m:      m,
		clk:    clk,
		reader: r,
		drafts: map[core.SessionID]*Draft{},
		begins: map[core.SessionID]*sessionGate{},
	}, nil
}

// acquireBeginGate takes session s's Begin admission gate, creating it on first use. w.mu is
// released before the gate is taken, so the lock order is always gate → w.mu and never the
// reverse.
func (w *FileWriter) acquireBeginGate(s core.SessionID) *sessionGate {
	w.mu.Lock()
	g := w.begins[s]
	if g == nil {
		g = &sessionGate{}
		w.begins[s] = g
	}
	g.refs++
	w.mu.Unlock()

	g.mu.Lock()
	return g
}

// releaseBeginGate releases s's gate and drops it from the map once nothing is waiting on it.
func (w *FileWriter) releaseBeginGate(s core.SessionID, g *sessionGate) {
	g.mu.Unlock()

	w.mu.Lock()
	g.refs--
	if g.refs == 0 {
		delete(w.begins, s)
	}
	w.mu.Unlock()
}

// claimSeq allocates the sequence number a fresh draft will finalize as: one above both the
// highest sequence the manifest records and the highest this writer has already handed out, and
// past any number whose artifact already exists on disk.
//
// The on-disk probe is what keeps Finalize's O_EXCL retry off the common path. That retry cannot
// be made harmless: store.SegmentLog.MarkEncoded is one-way, so a segment already flagged for this
// draft's number cannot be re-pointed at a different one, and an artifact written at a bumped
// number would leave every encoded segment naming a checkpoint that does not contain it (§8.2's
// "encoded-once flag, and checkpoint reference"). Choosing a free number up front is the cheap
// half of avoiding that; Finalize enforces the other half.
func (w *FileWriter) claimSeq() core.CheckpointSeq {
	w.mu.Lock()
	defer w.mu.Unlock()

	seq := maxSeq(w.l) + 1
	if next := w.issuedSeq + 1; next > seq {
		seq = next
	}
	for range maxSeqCollisionRetries {
		if _, err := os.Stat(paths.Long(paths.CheckpointPath(w.l, seq))); os.IsNotExist(err) {
			break
		}
		seq++
	}
	w.issuedSeq = seq
	return seq
}

// noteSeq records a sequence number this writer did not allocate — a resumed draft's — so a later
// claimSeq cannot hand the same number to a second draft.
func (w *FileWriter) noteSeq(seq core.CheckpointSeq) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if seq > w.issuedSeq {
		w.issuedSeq = seq
	}
}

// relPath renders an absolute checkpoint path as the project-relative, forward-slash form
// ".qompack/checkpoints/0007.json" used in the O1 focus paragraph (§8), so the spelling does not
// depend on the host's separator. (Since C1.18 that paragraph reaches no hop — see
// FocusInstructions.) A path outside the project has no relative form and falls back to its
// absolute slash form.
func (w *FileWriter) relPath(abs string) string {
	rel, err := filepath.Rel(w.root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// OpenDrafts returns the sessions with a live in-memory draft, ascending, so /qompack:status can
// name them deterministically.
func (w *FileWriter) OpenDrafts() []core.SessionID {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]core.SessionID, 0, len(w.drafts))
	for s := range w.drafts {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// DraftFor returns s's live draft, or nil when none is open. The daemon's idle tick uses it to
// decide between advancing an existing draft and Begin-ing one.
func (w *FileWriter) DraftFor(s core.SessionID) *Draft { return w.liveDraft(s) }

// liveDraft is DraftFor's body, shared with Begin and retireDraft.
func (w *FileWriter) liveDraft(s core.SessionID) *Draft {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.drafts[s]
}

// coldSources returns the SourceSet most recently handed to Begin — zero before any Begin. The
// daemon's flush path reads it to reach the live seams without holding its own copies.
// SetSources records the SourceSet the cold paths should use, so that a compaction firing before
// any idle window has run still has live seams to build from.
//
// Without it lastSrc is set only by Begin, and the cold PreCompact path -- the one §15 exists for,
// where compaction beat the first idle tick -- would fail SourceSet.Validate with a nil Store. The
// daemon holds the SourceSet at wiring time and has no reason to wait for a draft to publish it.
//
// Last write wins, and a zero SourceSet is ignored so a mis-wired caller cannot blank live seams.
//
// A REJECTION IS REPORTED, both as a returned error and as a Loud line, and that is not
// decoration. Silently dropping the set turned a wiring bug into a symptom one layer and one
// compaction away: the composition root published a set whose ledger had not been opened yet, this
// method dropped it without a word, and the first PreCompact of the daemon's life failed inside
// Begin with "SourceSet.Store is nil" — naming a seam that was never the problem — behind a single
// Warn and a null hookSpecificOutput. §16's rule for a seam that silently stops working is that it
// must say so; the error names the first seam the caller failed to wire, and the Loud line is for
// the call sites that have nowhere to return one.
func (w *FileWriter) SetSources(src SourceSet) error {
	if err := src.Validate(); err != nil {
		w.log.Loud("checkpoint: source set rejected; the cold PreCompact path keeps whatever seams it already had",
			"err", err.Error())
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastSrc = src
	return nil
}

func (w *FileWriter) coldSources() SourceSet {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSrc
}

// retireDraft drops d from the registry and deletes its state file. It is idempotent, and it is
// keyed on the DRAFT'S IDENTITY, not on its session: if a newer draft has since been opened for
// the same session, the newer draft owns both the registry slot and the file, and retiring the
// stale pointer must touch neither. Abort uses it; so does Finalize (§13) after sealing.
//
// d.path is immutable after construction, so reading it without d.mu is safe.
func (w *FileWriter) retireDraft(d *Draft) {
	if d == nil {
		return
	}
	w.mu.Lock()
	cur := w.drafts[d.session]
	if cur == d {
		delete(w.drafts, d.session)
	}
	w.mu.Unlock()
	if cur != nil && cur != d {
		return
	}
	if err := os.Remove(paths.Long(d.path)); err != nil && !os.IsNotExist(err) {
		w.log.Warn("checkpoint: draft file not removed", "path", d.path, "err", err.Error())
	}
}

// Begin opens a draft for session s, chained to parent (0 for the first checkpoint), reading
// exclusively from src (§8):
//
//  1. src.Validate() — a half-wired SourceSet fails here, by name — and the session id is checked
//     against the filename component it is about to become.
//  2. Resume-or-create from state/draft-<session>.json (§7). A live in-memory draft short-circuits
//     both: Begin is the daemon's restart entry point, and the in-memory draft IS the freshest
//     state there is. A persisted draft resumes when it names this session and its seq is still
//     unclaimed in the manifest; otherwise it is set aside as draft-<session>.stale.json with a
//     Warn and a fresh draft begins.
//  3. Fresh drafts take the next sequence number no manifest line, no artifact and no other live
//     draft has claimed (claimSeq). The parent argument overrides the recorded parent whenever
//     non-zero; a caller that passes 0 gets the latest SEALED sequence, because otherwise the
//     chain would restart at every daemon boot — advanceAllSessions and draftForPreCompact both
//     pass 0, so the only chained caller today is Finalize's own successor.
//  4. Tier 1 that does not depend on segments is seeded: Invariants verbatim from src.Pins.All;
//     UserIntent.Original from the parent chain (Reader.Get) or, with no readable parent, from the
//     earliest UserPrompt node's stored text through fromStore; Eliminated from src.Ledger.All
//     filtered to this session or project scope.
//  5. frontier = src.Segments.Frontier — 0 until any segment is marked encoded, which is what
//     pins the advancement-off arithmetic of the Phase 3 exit criterion.
//  6. Persist and return.
//
// The whole body runs under a per-session admission gate. Begin does file I/O, a manifest read and
// four source reads between deciding that no draft exists and publishing the one it made, and two
// concurrent calls for one session would otherwise both make that decision: the loser's draft is
// displaced from the registry but keeps writing to the same path, and retireDraft — which is
// keyed on the draft's identity — then declines to clean up after it.
func (w *FileWriter) Begin(ctx context.Context, s core.SessionID, parent core.CheckpointSeq, src SourceSet) (*Draft, error) {
	// Resolve, not Validate: step 4 below reads eliminations out of src.Ledger, and a set whose
	// ledger is still an accessor must have it called in HERE — once, at the top of the call —
	// rather than left for a nil dereference several frames down. Resolve reports an accessor that
	// answers nil the same way a nil field is reported, by name.
	src, err := src.Resolve()
	if err != nil {
		return nil, err
	}
	if err := checkSessionComponent(s); err != nil {
		return nil, err
	}

	gate := w.acquireBeginGate(s)
	defer w.releaseBeginGate(s, gate)

	w.mu.Lock()
	w.lastSrc = src
	live := w.drafts[s]
	w.mu.Unlock()

	if live != nil {
		live.mu.Lock()
		live.src = src
		if parent != 0 && parent != live.parent {
			live.parent = parent
			live.cp.Parent = filepath.Base(paths.CheckpointPath(w.l, parent))
			live.dirty = true
		}
		err := live.persistLocked()
		live.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return live, nil
	}

	p := draftPathFor(w.l, s)
	if d, ok := w.resumeDraft(s, p, parent, src); ok {
		// A draft written by an earlier process — possibly an earlier build — carries whatever intent
		// that process computed; it is recomputed from the session's own prompt records now.
		d.mu.Lock()
		d.refreshIntentLocked(ctx)
		d.persistOrLogLocked()
		d.mu.Unlock()
		w.noteSeq(d.seq)
		w.mu.Lock()
		w.drafts[s] = d
		w.mu.Unlock()
		return d, nil
	}

	d := &Draft{
		session:  s,
		seq:      w.claimSeq(),
		parent:   parent,
		encoded:  map[core.SegmentID]bool{},
		src:      src,
		started:  w.clk.Now(),
		dirty:    true,
		path:     p,
		fileTurn: map[string]core.TurnIndex{},
		toolTurn: map[core.ToolUseID]core.TurnIndex{},
		// The sealed predecessor's prompt texts, when Finalize is opening this draft as its
		// successor: prompt records are immutable, so a text read once stays valid.
		promptText: w.takeHandoff(s),
	}
	d.cp = Checkpoint{
		Version:    SchemaVersion,
		Session:    s,
		Seq:        d.seq,
		SketchRefs: defaultSketchRefs(),
		Cache:      CacheInfo{TTLState: ttlStateUnknown},
	}

	if err := w.seedTierOne(ctx, d, parent, src); err != nil {
		return nil, err
	}

	fr, err := src.Segments.Frontier(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: begin: frontier: %w", err)
	}
	d.frontier = fr

	if err := d.persist(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	w.drafts[s] = d
	w.mu.Unlock()
	return d, nil
}

// resumeDraft tries to restore s's persisted draft from p (§7). It reports false — after setting
// the file aside where one existed — whenever a fresh draft must begin instead: no file, a file
// that does not parse, a file naming another session, or a seq the manifest already claims (the
// daemon crashed after Finalize's manifest append and before the draft delete; resuming would
// re-encode a sequence number that is already immutable on disk).
func (w *FileWriter) resumeDraft(s core.SessionID, p string, parent core.CheckpointSeq, src SourceSet) (*Draft, bool) {
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		if !os.IsNotExist(err) {
			w.log.Warn("checkpoint: draft file unreadable; beginning fresh",
				"path", p, "err", err.Error())
			w.setAsideStaleDraft(p)
		}
		return nil, false
	}

	df, cp, err := decodeDraftFile(b)
	switch {
	case err != nil:
		w.log.Warn("checkpoint: draft file corrupt; set aside", "path", p, "err", err.Error())
	case df.Session != s:
		w.log.Warn("checkpoint: draft file names another session; set aside",
			"path", p, "session", string(df.Session))
	case seqClaimed(w.l, df.Seq):
		w.log.Warn("checkpoint: persisted draft's seq is already claimed in the manifest; beginning fresh",
			"path", p, "seq", int(df.Seq))
	default:
		userQ, derived := splitQuestions(cp.OpenQuestions, df.UserQuestions)
		d := &Draft{
			session:      s,
			seq:          df.Seq,
			parent:       df.Parent,
			cp:           cp,
			encoded:      segmentSet(df.Encoded),
			frontier:     df.Frontier,
			src:          src,
			started:      df.Started.Time(),
			workExplicit: df.WorkExplicit,
			// A resumed draft is written back once, unconditionally: the file on disk was produced
			// by whatever build wrote it last, and normalizing it here is what keeps a later
			// resume reading this version's shape.
			dirty:     true,
			path:      p,
			userOQ:    userQ,
			derivedOQ: derived,
			fileTurn:  map[string]core.TurnIndex{},
			toolTurn:  map[core.ToolUseID]core.TurnIndex{},
		}
		if parent != 0 {
			d.parent = parent
			d.cp.Parent = filepath.Base(paths.CheckpointPath(w.l, parent))
		}
		if err := d.persist(); err != nil {
			// The on-disk copy is still the one just read; resuming beats failing.
			w.log.Warn("checkpoint: resumed draft could not be re-persisted",
				"path", p, "err", err.Error())
		}
		return d, true
	}

	w.setAsideStaleDraft(p)
	return nil, false
}

// setAsideStaleDraft renames p to its .stale.json sibling (§7) so a discarded draft stays
// inspectable instead of vanishing. An older set-aside is superseded — state/ is not
// checkpoints/, so the replace is legal.
func (w *FileWriter) setAsideStaleDraft(p string) {
	stale := strings.TrimSuffix(p, ".json") + ".stale.json"
	_ = os.Remove(paths.Long(stale))
	if err := os.Rename(paths.Long(p), paths.Long(stale)); err != nil {
		w.log.Warn("checkpoint: stale draft not set aside", "path", p, "err", err.Error())
	}
}

// seedTierOne fills the §8 step 4 tier-1 fields that do not depend on segments, and settles which
// checkpoint this draft descends from.
//
// A caller that passes parent 0 gets the latest SEALED sequence rather than no parent at all.
// Every production caller but one passes 0 — advanceAllSessions and draftForPreCompact both do —
// so without the default the chain survives only within a single daemon lifetime: after a restart
// Reader.Chain stops at the restart boundary, and G2.3's promise that the user's own words survive
// arbitrarily many checkpoint generations breaks exactly where it matters, with UserIntent.Original
// silently re-derived from the earliest surviving prompt or left empty if that object has been
// collected.
//
// The two parents are not treated alike, and that asymmetry is deliberate. An explicit parent is a
// claim the caller is making about the chain — Finalize naming the sequence it just sealed — so a
// parent it cannot read is a hard error. A DERIVED parent is this function's own guess, and a
// project whose newest artifact has been deleted or corrupted must still be able to checkpoint: the
// guess is logged, dropped (a Parent naming an artifact we could not read would make Reader.Chain
// refuse the whole lineage) and the earliest-prompt branch runs instead.
func (w *FileWriter) seedTierOne(ctx context.Context, d *Draft, parent core.CheckpointSeq, src SourceSet) error {
	invs, err := src.Pins.All(ctx)
	if err != nil {
		return fmt.Errorf("checkpoint: begin: pins: %w", err)
	}
	d.cp.Invariants = invs // verbatim, in pins.All's own order (§8)

	derived := parent == 0
	if derived {
		parent = maxSeq(w.l)
	}

	var own *Checkpoint
	if parent != 0 {
		pc, _, gerr := w.reader.Get(ctx, parent)
		switch {
		case gerr == nil:
			d.parent = parent
			d.cp.Parent = filepath.Base(paths.CheckpointPath(w.l, parent))
			// An explicit parent is the caller's statement about THIS session's chain (Finalize's
			// successor). A derived one is only the project's newest checkpoint, and when another
			// session sealed it, that session's intent is not this one's.
			if !derived || pc.Session == d.session {
				own = &pc
			}
		case derived:
			w.log.Warn("checkpoint: begin: latest checkpoint unreadable; beginning an unchained draft",
				"parent", int(parent), "err", gerr.Error())
		default:
			return fmt.Errorf("checkpoint: begin: parent %d: %w", int(parent), gerr)
		}
	}
	w.seedIntent(ctx, d, own)

	all, err := src.Ledger.All(ctx)
	if err != nil {
		return fmt.Errorf("checkpoint: begin: ledger: %w", err)
	}
	for _, r := range all {
		if r.Session != d.session && r.Scope != negknow.ScopeProject {
			continue
		}
		r.DependsOn = slices.Clone(r.DependsOn)
		r.StaleBecause = slices.Clone(r.StaleBecause)
		// Both active and stale records are carried verbatim: §8.5's status field exists
		// precisely to preserve the distinction.
		d.cp.Eliminated = append(d.cp.Eliminated, r)
	}
	return nil
}

// Advance encodes the CLOSED, UNENCODED segments in segs into d, in ascending id order, and
// returns the turn index the frontier reached (§8's O5 incremental encoder). It runs only during
// idle windows; nothing here is on the hot path.
//
// Per batch there is exactly ONE full-graph scan: nodesInRange over every kind the bullets need,
// partitioned per segment in memory. File nodes are scanned from turn 0 deliberately — dag's
// AddNode keeps a file node's EARLIEST turn (mergeNode), so a [StartTurn, EndTurn] filter would
// silently miss every re-touched file; a file's membership in a segment is decided by its
// shared-file EDGE turns instead (collectFileTouches).
//
// A SEALED draft is refused with ErrDraftSealed and nothing is written: Finalize has already
// decided the artifact's bytes, so marking segments encoded into it now would strand them in no
// checkpoint at all, and persisting would overwrite the successor draft's file (draftPathFor is
// keyed on the session, not on the draft). The caller re-Begins and encodes into the successor.
//
// The DPI guard runs twice, and both failures surface as core.ErrAlreadyEncoded AFTER the draft
// is persisted, with nothing partial rolled back — the draft is a superset, and the manifest is
// written only at Finalize (§8 step 4):
//
//   - a segment already recorded as encoded by a DIFFERENT checkpoint is skipped up front and
//     reported at the end, so one poisoned id cannot block its batch-mates;
//   - MarkEncoded's own batch validation catches the race where another writer encoded a segment
//     between Get and the mark, and its error propagates unchanged.
//
// A segment already encoded into THIS draft (d.encoded) is skipped silently, which is what makes
// Advance idempotent for the same seq; a re-Begin-after-Abort re-encodes legitimately, because
// MarkEncoded is idempotent for the same seq.
func (w *FileWriter) Advance(ctx context.Context, d *Draft, segs []core.SegmentID) (core.TurnIndex, error) {
	if d == nil {
		return 0, fmt.Errorf("checkpoint: advance: nil draft")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sealed {
		return d.frontier, fmt.Errorf("checkpoint: advance: session %s, checkpoint %d: %w",
			d.session, int(d.seq), ErrDraftSealed)
	}
	src := d.src

	ids := slices.Clone(segs)
	slices.Sort(ids)
	ids = slices.Compact(ids)

	var cands []store.Segment
	var skipped []core.SegmentID
	for _, id := range ids {
		if d.encoded[id] {
			w.log.Debug("checkpoint: segment already encoded into this draft; skipped", "segment", int(id))
			continue
		}
		seg, err := src.Segments.Get(ctx, id)
		if err != nil {
			d.persistOrLogLocked()
			return d.frontier, fmt.Errorf("checkpoint: advance: segment %d: %w", int(id), err)
		}
		if !seg.Closed {
			// Only closed segments may be encoded; SP-12 closes them.
			w.log.Debug("checkpoint: segment still open; skipped", "segment", int(id))
			continue
		}
		if seg.EncodedOnce && seg.CheckpointSeq != d.seq {
			w.log.Debug("checkpoint: DPI guard: segment already encoded by another checkpoint; skipped",
				"segment", int(id), "checkpoint_seq", int(seg.CheckpointSeq))
			skipped = append(skipped, id)
			continue
		}
		cands = append(cands, seg)
	}

	if len(cands) > 0 {
		// Everything from here on mutates the accumulated draft, so every error path below must
		// write what it managed to accumulate: losing a partial draft on a transient store error
		// would reset the frontier and let the next PreCompact re-encode an encoded span.
		d.dirty = true

		if err := d.mergeEliminationsLocked(ctx, src); err != nil {
			d.persistOrLogLocked()
			return d.frontier, err
		}
		d.deriveOpenQuestionsLocked()

		maxEnd := cands[0].EndTurn
		for _, seg := range cands[1:] {
			if seg.EndTurn > maxEnd {
				maxEnd = seg.EndTurn
			}
		}
		nodes := nodesInRange(src.Graph,
			[]dag.NodeKind{dag.KindUserPrompt, dag.KindFile, dag.KindToolUse}, 0, maxEnd)
		// nodesInRange returns the graph's own (position) order; every bullet below needs turn
		// order, so the batch is sorted once here.
		sortNodesByTurn(nodes)

		for _, seg := range cands {
			if err := w.encodeSegmentLocked(ctx, d, seg, nodes); err != nil {
				d.persistOrLogLocked()
				return d.frontier, err
			}
		}

		encodable := make([]core.SegmentID, len(cands))
		for i, seg := range cands {
			encodable[i] = seg.ID
		}
		if err := src.Segments.MarkEncoded(ctx, encodable, d.seq); err != nil {
			// ErrAlreadyEncoded propagates unchanged (§8 step 4); the caller logs Loud and drops
			// those ids. MarkEncoded validates the whole batch before writing, so nothing was
			// marked, and the frontier stays put.
			d.persistOrLogLocked()
			return d.frontier, err
		}
		for _, seg := range cands {
			d.encoded[seg.ID] = true
		}

		// The frontier is re-read from the segment log rather than maxed over what this batch
		// happened to encode. store.SegmentLog.Frontier is the EndTurn of the last CONSECUTIVELY
		// encoded segment, and contiguity is the whole claim: a frontier of N asserts that the
		// checkpoint FULLY COVERS the session through turn N, and the O1 focus paragraph says so in
		// writing (§8.5 O1/O5; since C1.18 that paragraph reaches no hop, but the coverage claim
		// is the checkpoint's own and state/precompact.json publishes it).
		//
		// A local maximum misstates that whenever a gap exists — encoding one late segment while
		// earlier ones are still unencoded would advance the frontier past turns that live in
		// neither the checkpoint nor the summary, and the O1 paragraph would tell a summarizer to
		// drop them. One store call per batch, on the idle path, buys the invariant outright.
		//
		// It is clamped to be non-decreasing. A resumed draft's frontier comes from its own state
		// file, which may name coverage the segment log can no longer corroborate, and a frontier
		// that rewound would re-summarize a span already encoded.
		if fr, ferr := src.Segments.Frontier(ctx, d.session); ferr != nil {
			// A frontier we cannot re-read is not a reason to lose the batch: the segments are
			// marked, the draft holds their content, and the next Advance re-reads it.
			w.log.Warn("checkpoint: advance: frontier unreadable; leaving it where it was",
				"session", string(d.session), "err", ferr.Error())
		} else if fr > d.frontier {
			d.frontier = fr
		}
	}

	// The session's intent is recomputed from its own prompt records on every pass — the open
	// segment's prompts included, which no segment encoding reaches (intent.go).
	d.refreshIntentLocked(ctx)

	if err := d.persistLocked(); err != nil {
		return d.frontier, err
	}
	if len(skipped) > 0 {
		return d.frontier, fmt.Errorf("checkpoint: advance: segments %v already encoded by an earlier checkpoint: %w",
			skipped, core.ErrAlreadyEncoded)
	}
	return d.frontier, nil
}

// encodeSegmentLocked runs §8 step 3's node-driven bullets for one segment against the batch's
// single node scan. Caller holds d.mu.
func (w *FileWriter) encodeSegmentLocked(ctx context.Context, d *Draft, seg store.Segment, nodes []dag.Node) error {
	src := d.src
	from, to := seg.StartTurn, seg.EndTurn

	var prompts, tools, filesFirstTouch []dag.Node
	for _, n := range nodes {
		inRange := n.Turn >= from && n.Turn <= to
		switch n.Kind {
		case dag.KindUserPrompt:
			if inRange {
				prompts = append(prompts, n)
			}
		case dag.KindToolUse:
			if inRange {
				tools = append(tools, n)
			}
		case dag.KindFile:
			// A file node's Turn is its FIRST touch (dag.mergeNode keeps the earliest), so this
			// partition only finds files born in this segment; re-touches are found through edge
			// turns in collectFileTouches.
			if inRange {
				filesFirstTouch = append(filesFirstTouch, n)
			}
		}
	}

	// Intent evolution is not read here: Advance recomputes it from the session's own prompt
	// records after the batch (intent.go), because a segment's prompts are only the ones that
	// happened to close in a segment, and the graph's userprompt nodes are shared across sessions.

	// Decisions: §9's extractor, merged by ID. decisions.go is another SP-10 slice; until it
	// lands, its Rule W-1 stub answers ErrNotImplemented and the honest merge input is empty —
	// a tier-2 enrichment gap must not stall the tier-1/tier-3 frontier.
	decs, err := ExtractDecisions(ctx, src, seg.StartTurn)
	switch {
	case err == nil:
		d.mergeDecisionsLocked(decs)
	case core.IsNotImplemented(err):
		w.log.Debug("checkpoint: decision extraction not implemented yet; segment encoded without decisions",
			"segment", int(seg.ID))
	default:
		return fmt.Errorf("checkpoint: advance: decisions for segment %d: %w", int(seg.ID), err)
	}

	// Pointers — files. No file bytes are read: the pointer is path + latest stored version's
	// root + a one-line reason (§4.4, §13 invariant 5).
	touches := collectFileTouches(src.Graph, filesFirstTouch, tools, from, to)
	for _, tc := range touches {
		hist, err := src.Store.FileHistory(ctx, tc.pathKey)
		if err != nil || len(hist) == 0 {
			w.log.Debug("checkpoint: no file history; pointer skipped", "path", tc.pathKey)
			continue
		}
		why := pointerWhyUnknownTool
		if tc.prodTurn >= 0 {
			why = truncRunes(fmt.Sprintf("touched at turn %d via %s", int(tc.prodTurn), tc.tool),
				pointerWhyMaxRunes)
		}
		d.upsertFilePointerLocked(FilePointer{
			Path: tc.pathKey, Hash: hist[len(hist)-1].Root, Why: why,
		}, tc.ord)
	}

	// Pointers — tools. Superseded reads "should never appear in a summary" (§8.1 item 3) and
	// ephemeral results are the first eviction candidates (§8.7), so both are excluded.
	for _, tn := range tools {
		if tn.Ephemeral {
			continue
		}
		_, key, ok := dag.ParseNodeID(tn.ID)
		if !ok {
			continue
		}
		id := core.ToolUseID(key)
		rec, err := src.Store.ToolUse(ctx, id)
		if err != nil {
			w.log.Debug("checkpoint: tool_use record unreadable; pointer skipped",
				"tool_use", key, "err", err.Error())
			continue
		}
		if rec.Status == store.StatusSuperseded {
			continue
		}
		d.upsertToolPointerLocked(ToolPointer{
			// ArgsPreview is raw tool-argument text the store captured verbatim, so it reaches
			// the checkpoint through fromStore like every other store read: a previous
			// injection's tags can appear in the arguments of any tool that was handed a slice
			// of the transcript, and re-encoding one is the compress-a-compression §4.6 forbids.
			ToolUseID: id, Hash: rec.Root, Summary: fromStore([]byte(rec.ArgsPreview)),
		}, tn.Turn)
	}

	// Current work: skipped entirely once SetCurrentWork has spoken (§7). The derived form is
	// one sentence of the most recent prompt, an empty NextStep and a nil BlockedOn — inventing
	// a next step from tool history is exactly the drift this layer exists to eliminate (§8).
	if !d.workExplicit && len(prompts) > 0 {
		if text, ok := readPromptText(ctx, src, prompts[len(prompts)-1]); ok && text != "" {
			d.cp.CurrentWork = CurrentWork{Goal: truncRunes(firstSentence(text), goalMaxRunes)}
		}
	}

	// Narrative: one line per segment, in §8's exact format. SP-15 appends its grammar section
	// to the same field; it never rewrites these lines.
	d.appendNarrativeLocked(seg.ID, fmt.Sprintf("seg %d turns %d-%d: %d tool uses over %d files; %s",
		int(seg.ID), int(from), int(to), len(tools), len(touches), featureSummary(seg.Features)))

	return nil
}

// appendNarrativeLocked adds seg's narrative line unless one is already there, which is what makes
// re-encoding a batch converge instead of accumulating.
//
// Every other accumulator in this file dedups — evolution by exact string, decisions by ID,
// pointers by path and tool id, eliminations by Record.ID — and the narrative was the one that did
// not. That mattered because encodeSegmentLocked runs for the WHOLE batch before MarkEncoded does:
// when MarkEncoded returns the context's error, which the 2 s idle budget makes reachable, the
// draft persists with every narrative line appended and nothing recorded in d.encoded, so the next
// tick encodes the same batch again and appends a second copy — once per tick, for as long as it
// keeps timing out. Caller holds d.mu.
func (d *Draft) appendNarrativeLocked(id core.SegmentID, line string) {
	prefix := fmt.Sprintf("seg %d turns ", int(id))
	for rest := d.cp.Narrative; rest != ""; {
		var have string
		have, rest, _ = strings.Cut(rest, "\n")
		if strings.HasPrefix(have, prefix) {
			return
		}
	}
	d.cp.Narrative += line + "\n"
}

// Abort deletes state/draft-<session>.json, drops the in-memory draft, and returns nil —
// idempotent, a missing file included. It never un-marks encoded segments: those segments are
// legitimately encoded into a draft that will be re-Begin-ned with the same seq, and MarkEncoded
// is idempotent for the same seq (§8). The DPI guard is one-way.
//
// The aborted draft is sealed and stays sealed, so a caller still holding the pointer can neither
// Advance it nor Finalize it into an artifact. A discarded draft that could still be sealed would
// write a checkpoint nobody asked for, at a sequence number the writer has since handed to a live
// draft.
func (w *FileWriter) Abort(d *Draft) error {
	if d == nil {
		return nil
	}
	d.seal()
	w.retireDraft(d)
	return nil
}

// ── draft-side encoding state (methods used only by Advance; callers hold d.mu) ───────────────

// mergeEliminationsLocked re-reads the ledger and merges by Record.ID, so status flips that
// happened since Begin — a dependency hash change flipping active to stale — are reflected (§8).
// The Begin-time visibility filter (this session, or project scope) applies unchanged.
func (d *Draft) mergeEliminationsLocked(ctx context.Context, src SourceSet) error {
	all, err := src.Ledger.All(ctx)
	if err != nil {
		return fmt.Errorf("checkpoint: advance: ledger: %w", err)
	}
	idx := make(map[string]int, len(d.cp.Eliminated))
	for i, r := range d.cp.Eliminated {
		idx[r.ID] = i
	}
	for _, r := range all {
		if r.Session != d.session && r.Scope != negknow.ScopeProject {
			continue
		}
		r.DependsOn = slices.Clone(r.DependsOn)
		r.StaleBecause = slices.Clone(r.StaleBecause)
		if i, ok := idx[r.ID]; ok {
			d.cp.Eliminated[i] = r
		} else {
			idx[r.ID] = len(d.cp.Eliminated)
			d.cp.Eliminated = append(d.cp.Eliminated, r)
		}
	}
	return nil
}

// deriveOpenQuestionsLocked renders §8.3's "previously eliminated, but the evidence has changed"
// as a tier-2 question per stale record: deduped by exact string, capped, and always AFTER the
// explicitly contributed questions (draft.go's materialization order).
func (d *Draft) deriveOpenQuestionsLocked() {
	for i := range d.cp.Eliminated {
		r := &d.cp.Eliminated[i]
		if r.Status != negknow.StatusStale {
			continue
		}
		d.addDerivedQuestionLocked(fmt.Sprintf("re-verify %q for %s — evidence changed (%s)",
			r.Approach, r.Target, strings.Join(r.StaleBecause, ", ")))
	}
}

// mergeDecisionsLocked merges one extraction pass into cp.Decisions by Decision.ID — first
// occurrence wins, and existing entries came from earlier (lower-turn) passes — then restores the
// serialized order §8 fixes: Turn descending, tiebreak ID ascending, capped at maxDraftDecisions
// keeping the head. Newest-first is the tail-first cut order Truncate relies on; the slice-score
// ranking inside ExtractDecisions decides which decisions survive a single pass, not this order.
func (d *Draft) mergeDecisionsLocked(decs []Decision) {
	have := make(map[core.DecisionID]bool, len(d.cp.Decisions))
	for _, dec := range d.cp.Decisions {
		have[dec.ID] = true
	}
	for _, dec := range decs {
		if dec.ID == "" || have[dec.ID] {
			continue
		}
		dec.AlternativesRejected = slices.Clone(dec.AlternativesRejected)
		d.cp.Decisions = append(d.cp.Decisions, dec)
		have[dec.ID] = true
	}
	slices.SortStableFunc(d.cp.Decisions, func(a, b Decision) int {
		if a.Turn != b.Turn {
			return int(b.Turn) - int(a.Turn) // Turn descending
		}
		return strings.Compare(string(a.ID), string(b.ID)) // tiebreak ID ascending
	})
	if len(d.cp.Decisions) > maxDraftDecisions {
		d.cp.Decisions = d.cp.Decisions[:maxDraftDecisions]
	}
}

// upsertFilePointerLocked applies §8's pointer-ordering rule to Pointers.Files: segments are
// encoded in ascending turn order, so each new pointer is PREPENDED — index 0 is newest, the
// tail is oldest, and Truncate's tail-first cut removes the least recently touched material
// first. Re-encountering a path removes the existing entry and prepends the newer one; that is
// what "dedup by path, keep the highest turn" means operationally. The recorded turn makes the
// rule hold even for an out-of-order replay: a lower-turn re-encounter is dropped.
func (d *Draft) upsertFilePointerLocked(fp FilePointer, turn core.TurnIndex) {
	if d.fileTurn == nil {
		d.fileTurn = map[string]core.TurnIndex{}
	}
	if prev, ok := d.fileTurn[fp.Path]; ok && prev > turn {
		return
	}
	d.cp.Pointers.Files = slices.DeleteFunc(d.cp.Pointers.Files, func(e FilePointer) bool {
		return e.Path == fp.Path
	})
	d.cp.Pointers.Files = append([]FilePointer{fp}, d.cp.Pointers.Files...)
	d.fileTurn[fp.Path] = turn
}

// upsertToolPointerLocked is upsertFilePointerLocked's twin for Pointers.Tools, keyed on the
// tool_use id.
func (d *Draft) upsertToolPointerLocked(tp ToolPointer, turn core.TurnIndex) {
	if d.toolTurn == nil {
		d.toolTurn = map[core.ToolUseID]core.TurnIndex{}
	}
	if prev, ok := d.toolTurn[tp.ToolUseID]; ok && prev > turn {
		return
	}
	d.cp.Pointers.Tools = slices.DeleteFunc(d.cp.Pointers.Tools, func(e ToolPointer) bool {
		return e.ToolUseID == tp.ToolUseID
	})
	d.cp.Pointers.Tools = append([]ToolPointer{tp}, d.cp.Pointers.Tools...)
	d.toolTurn[tp.ToolUseID] = turn
}

// ── free helpers ──────────────────────────────────────────────────────────────────────────────

// seqClaimed reports whether the manifest already records seq. It shares maxSeq's tolerance: an
// unreadable manifest claims nothing, and paths.CreateNew's O_EXCL remains the hard guard.
func seqClaimed(l paths.Layout, seq core.CheckpointSeq) bool {
	entries, err := paths.ReadManifest(l)
	if err != nil {
		pkgMetrics().Counter("checkpoint.manifest_badline").Add(1)
		pkgLog().Loud("checkpoint: manifest unreadable during draft resume; treating seq as unclaimed",
			"err", err.Error())
		return false
	}
	for _, e := range entries {
		if e.Seq == seq {
			return true
		}
	}
	return false
}

// draftPathFor names session s's draft file: state/draft-<session>.json (§7). Callers must have
// passed s through checkSessionComponent first — Begin does.
func draftPathFor(l paths.Layout, s core.SessionID) string {
	return filepath.Join(l.State, "draft-"+string(s)+".json")
}

// checkSessionComponent rejects a session id that cannot safely become a filename component.
//
// This is the only place in internal/ that puts a session id into a path, and the id is decoded
// verbatim from the hook payload and validated nowhere else in the tree. filepath.Join CLEANS its
// result, so an id of "../../../../evil" does not produce a rejected path — it produces
// <root>/evil.json, outside .qompack entirely, where paths.WriteAtomic's IsProtected guard never
// fires. retireDraft then os.Removes that path and setAsideStaleDraft renames it, so a hostile or
// merely malformed id turns the draft lifecycle into an arbitrary-path write and unlink.
//
// The id is kept readable rather than hashed because draftFile.Session already carries it and
// resumeDraft cross-checks it; folding it into an opaque filename would cost that check its
// meaning and change §7's documented on-disk shape for every existing draft.
func checkSessionComponent(s core.SessionID) error {
	bad := func(why string) error {
		return fmt.Errorf("checkpoint: session id %q is not a safe filename component: %s", string(s), why)
	}
	switch {
	case s == "":
		return bad("empty")
	case s == "." || s == "..":
		return bad("a relative path element")
	}
	n := 0
	for _, r := range string(s) {
		n++
		if n > maxSessionIDRunes {
			return bad(fmt.Sprintf("longer than %d runes", maxSessionIDRunes))
		}
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alnum && r != '_' && r != '-' && r != '.' {
			return bad(fmt.Sprintf("contains %q", r))
		}
	}
	return nil
}

// segmentSet builds the encoded-set map from a persisted id list.
func segmentSet(ids []core.SegmentID) map[core.SegmentID]bool {
	m := make(map[core.SegmentID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// sortNodesByTurn orders nodes ascending by Turn, tiebreak ascending by ID — the deterministic
// order every per-segment partition relies on. NodesAfter returns position order, which tracks
// turn order only loosely (parallel siblings share a turn; a file node keeps its earliest
// position), so callers must not skip this.
func sortNodesByTurn(nodes []dag.Node) {
	slices.SortFunc(nodes, func(a, b dag.Node) int {
		if a.Turn != b.Turn {
			return int(a.Turn) - int(b.Turn)
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
}

// earliestPrompt reduces the graph to its lowest-turn user prompt (§8 step 4's parent-0 case).
// One nodesInRange scan; ties break on ID for determinism.
func earliestPrompt(g dag.Graph) (dag.Node, bool) {
	prompts := nodesInRange(g, []dag.NodeKind{dag.KindUserPrompt}, 0, core.TurnIndex(math.MaxInt))
	if len(prompts) == 0 {
		return dag.Node{}, false
	}
	best := prompts[0]
	for _, n := range prompts[1:] {
		if n.Turn < best.Turn || (n.Turn == best.Turn && n.ID < best.ID) {
			best = n
		}
	}
	return best, true
}

// readPromptText resolves one user-prompt node to its stored text, through fromStore — every
// string that enters a checkpoint from the store passes that gate (§8.5's regeneration rule).
//
// The root is taken from the node when it carries one. The shipped observer does NOT put it
// there — dag.BuildUserPrompt has no Root parameter, and internal/observer/prompt.go stores the
// verbatim bytes under a ToolUseRecord whose id it writes into the node's Ref — so the fallback
// resolves Ref through src.Store.ToolUse. The plan's §8 assumes the node carries the root
// directly; both shapes are honoured, and the drift is recorded in SP-10 seat E's report.
//
// A prompt that cannot be resolved is skipped with a Debug, never an error: GC may legitimately
// have collected an old prompt's object, and one missing restatement must not fail an Advance.
func readPromptText(ctx context.Context, src SourceSet, n dag.Node) (string, bool) {
	root := n.Root
	if root.IsZero() && n.Ref != "" {
		rec, err := src.Store.ToolUse(ctx, core.ToolUseID(n.Ref))
		if err != nil {
			pkgLog().Debug("checkpoint: prompt record unreadable; prompt skipped",
				"node", string(n.ID), "err", err.Error())
			return "", false
		}
		root = rec.Root
	}
	return readRootText(ctx, src, root, string(n.ID))
}

// fileTouch is one file's presence in one segment: the path, the turn that orders its pointer
// (the latest in-range touch), and — when a write edge resolves — the producing tool and turn.
type fileTouch struct {
	pathKey  string
	ord      core.TurnIndex
	prodTurn core.TurnIndex
	tool     string
}

// collectFileTouches finds every file a segment touched, from two sources: file nodes FIRST
// touched in the range (their node turn is the first touch, per dag.mergeNode), and files whose
// shared-file edges carry an in-range turn — reached through the segment's tool-use nodes, so
// re-touches of old files are found without a second graph scan.
//
// The producing tool is resolved per §8: In(file) edges are the WRITE direction (tool_use →
// file, sharedStateEdge's D-1 orientation), and the edge's From resolves through Graph.Node to a
// tool-use node whose Ref is the tool name. The latest in-range write wins; a file with only
// reads in range has no producing tool and its Why becomes "referenced".
func collectFileTouches(g dag.Graph, filesFirstTouch, tools []dag.Node, from, to core.TurnIndex) []fileTouch {
	cand := make(map[dag.NodeID]bool, len(filesFirstTouch))
	for _, n := range filesFirstTouch {
		cand[n.ID] = true
	}
	for _, tn := range tools {
		for _, e := range g.Out(tn.ID) {
			if e.Kind == dag.EdgeSharedFile && e.Turn >= from && e.Turn <= to {
				cand[e.To] = true
			}
		}
		for _, e := range g.In(tn.ID) {
			if e.Kind == dag.EdgeSharedFile && e.Turn >= from && e.Turn <= to {
				cand[e.From] = true
			}
		}
	}

	touches := make([]fileTouch, 0, len(cand))
	for id := range cand {
		kind, key, ok := dag.ParseNodeID(id)
		if !ok || kind != dag.KindFile {
			continue
		}
		pathKey := key
		node, haveNode := g.Node(id)
		if haveNode && node.Ref != "" {
			pathKey = paths.Key(node.Ref)
		}

		prodTurn, anyTurn := core.TurnIndex(-1), core.TurnIndex(-1)
		tool := ""
		for _, e := range g.In(id) {
			if e.Kind != dag.EdgeSharedFile || e.Turn < from || e.Turn > to {
				continue
			}
			if e.Turn > anyTurn {
				anyTurn = e.Turn
			}
			if e.Turn <= prodTurn {
				continue
			}
			if tn, ok := g.Node(e.From); ok && tn.Kind == dag.KindToolUse && tn.Ref != "" {
				prodTurn, tool = e.Turn, tn.Ref
			}
		}
		for _, e := range g.Out(id) {
			if e.Kind == dag.EdgeSharedFile && e.Turn >= from && e.Turn <= to && e.Turn > anyTurn {
				anyTurn = e.Turn
			}
		}

		ord := anyTurn
		if ord < 0 {
			// No in-range edge at all: candidacy came from the node's first-touch turn.
			if !haveNode || node.Turn < from || node.Turn > to {
				continue
			}
			ord = node.Turn
		}
		touches = append(touches, fileTouch{pathKey: pathKey, ord: ord, prodTurn: prodTurn, tool: tool})
	}

	// Ascending by touch turn, so the prepend in upsertFilePointerLocked leaves the slice
	// descending; path tiebreak for full determinism over the candidate map's iteration order.
	slices.SortFunc(touches, func(a, b fileTouch) int {
		if a.ord != b.ord {
			return int(a.ord) - int(b.ord)
		}
		return strings.Compare(a.pathKey, b.pathKey)
	})
	return touches
}

// firstSentence returns the first sentence of s: everything up to and including the first
// occurrence of ". ", ".\n", "! " or "? ", or all of s when none occurs. This is §9's sentence
// splitter, shared by the CurrentWork.Goal derivation (§8); decisions.go applies the same rule to
// decision text.
func firstSentence(s string) string {
	best := -1
	for _, sep := range []string{". ", ".\n", "! ", "? "} {
		if i := strings.Index(s, sep); i >= 0 && (best < 0 || i < best) {
			best = i
		}
	}
	if best < 0 {
		return s
	}
	return s[:best+1]
}

// truncRunes cuts s to at most n runes, on a rune boundary — §8's caps are spelled in runes, and
// a byte cut could split a multi-byte rune and ship invalid UTF-8 into a frozen artifact.
func truncRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	seen := 0
	for i := range s {
		if seen == n {
			return s[:i]
		}
		seen++
	}
	return s
}

// featureSummary renders a segment's BOCD feature map as §8's narrative fragment: keys sorted
// ascending, each as k=%.2f, joined by ", ". Sorted because the narrative line lands verbatim in
// a frozen artifact and map order must never reach a wire format.
func featureSummary(feats map[string]float64) string {
	if len(feats) == 0 {
		return ""
	}
	keys := make([]string, 0, len(feats))
	for k := range feats {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.2f", k, feats[k]))
	}
	return strings.Join(parts, ", ")
}
