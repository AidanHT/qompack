package sketch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// sketchFilePerm is the mode every sketch file is written with. A sketch records which files a
// session touched and which descriptors it eliminated, so it is exactly as private as the project
// it describes: owner-only, matching the rest of the .qompack tree.
const sketchFilePerm fs.FileMode = 0o600

// triedBloomBackupFormat renders the name paths.ReplaceBloom gives a displaced generation:
// tried.bloom.<seq>.bak, formatted with %d and no zero padding.
//
// internal/paths owns this format — it writes the name and its pruneBloomBackups parses it back
// with strconv.Atoi — so it is reproduced here rather than invented. A second, zero-padded
// spelling would produce backups the pruner cannot see, and §3.3's "keep exactly one generation"
// would quietly stop holding while every test still passed.
const triedBloomBackupFormat = TriedBloomBase + ".%d.bak"

// quarantineInfix separates a corrupt sketch's original path from the timestamp Quarantine appends.
const quarantineInfix = ".corrupt."

// The two messages LoadWithLog shouts on the Loud channel.
//
// They are constants because they are what an operator greps LOUD.log for, and because they are
// what io_test.go asserts on to prove WHICH guard fired: the frame-limit line can only come from
// the os.Stat size check, so seeing it is evidence that a hostile 64 MiB file was refused before
// os.ReadFile pulled it into memory.
const (
	loudOversizeMsg = "sketch file exceeds frame limit"
	loudCorruptMsg  = "sketch corrupt — rebuilding from records"
)

// Save writes s to p atomically, through paths.WriteAtomic.
//
// It REFUSES sketches/tried.bloom outright. §7.4 makes that one file append-only/additive and
// 00-ARCHITECTURE.md §3.3 permits its replacement only through the generational path, so the
// refusal here is the mechanical enforcement of the invariant rather than a comment asking callers
// to be careful — ReplaceGenerational is the sanctioned door. The refusal is checked before the
// sketch is marshalled, so a rejected call costs nothing and can leave nothing behind.
//
// The returned error satisfies BOTH errors.Is(err, ErrGenerational), which names the door to use,
// and errors.Is(err, core.ErrAppendOnly), which is the sentinel every other §7.4 guard in the tree
// already reports and every caller already branches on.
//
// Save does NOT map its failures to core.ErrNotFound the way Load does, and errors.go says why: a
// caller that could not write has to know which of the three things went wrong. The refusal above is
// core.ErrAppendOnly; a sketch that cannot encode returns the encoder's own sentinel unchanged
// (ErrMalformed or ErrTooLarge), because that is a bug in the value the caller is holding; and a
// filesystem failure under paths.WriteAtomic reaches the caller as itself. sketchtest's
// requireSaveError is the conformance statement of that vocabulary.
//
// Save creates no directories: the target's parent must already exist, which for a real project it
// does, because paths.EnsureLayout creates .qompack/sketches/ at daemon start. paths.WriteAtomic
// stages under <root>/.qompack/tmp when p resolves to a project root and under filepath.Dir(p)
// when it does not, so Save also works in a bare temporary directory with no .qompack tree above
// it at all.
func Save(p string, s Sketch) error {
	if filepath.Base(p) == TriedBloomBase {
		return fmt.Errorf("%w: %w: %s", core.ErrAppendOnly, ErrGenerational, p)
	}
	b, err := s.MarshalBinary()
	if err != nil {
		return err
	}
	return paths.WriteAtomic(p, b, sketchFilePerm)
}

// Load reads p into s, checking magic, version and CRC32C, and maps every failure to
// core.ErrNotFound while keeping the underlying sentinel inspectable through errors.Is.
//
// It is the §5.7 signature, and it carries no Logger, so it hands LoadWithLog a logging.Nop.
// Precisely what that costs is worth stating, because it is narrower than "Load is silent": a Nop
// has no destination behind it, so no log LINE is written anywhere — not to the day log, not to
// LOUD.log. But logging.Nop().Loud still appends to the process-wide ring logging.LastLoud reads
// and still fires any observer installed through logging.AttachLoudObserver, so a corrupt file
// loaded through Load does reach an obs counter wired up at a composition root.
//
// That is not a substitute for the real thing. Production call sites — SP-05's SketchSet, SP-09's
// negknow.Open — MUST call LoadWithLog instead, because the ring is a 32-entry debugging aid and
// the durable record an operator reads after the fact is the log line Nop cannot write. Choosing
// the silent path by accident is what 00-ARCHITECTURE.md §13 invariant 10 ("degradation is loud")
// forbids, which is why LoadWithLog is named here rather than only in the package doc.
func Load(p string, s Sketch) error { return LoadWithLog(p, s, logging.Nop()) }

// LoadWithLog is Load plus the Loud report: it is the form every composition root must call.
//
// Every failure — absent, unstattable, oversize, unreadable, corrupt — is reported as
// core.ErrNotFound, so a caller's natural "not found ⇒ start empty" branch is also its corrupt-file
// branch. A sketch is a cache and never the source of truth (§13 invariant 3), so an unreadable one
// and a missing one are the same event upstream. The underlying sentinel is wrapped rather than
// swallowed: a CRC failure satisfies errors.Is against core.ErrNotFound AND ErrCorrupt, which is
// what lets SP-09 implement §12.3's "bloom load fails → rebuild from records/eliminations.jsonl"
// with one branch and still distinguish bit rot from a cold start in the log.
//
// The size guard runs on the os.Stat result, BEFORE os.ReadFile. That ordering is the point of it:
// a forged or truncated-into-existence 64 MiB file is refused for the number in its directory entry
// rather than by reading it into the daemon's heap first.
//
// A missing file is deliberately NOT loud. It is the ordinary cold-start state of every sketch on a
// project's first session, and shouting about it would train an operator to ignore the channel that
// corruption uses.
func LoadWithLog(p string, s Sketch, log logging.Logger) error {
	st, err := os.Stat(paths.Long(p))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", core.ErrNotFound, p)
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", core.ErrNotFound, p, err)
	}
	if st.Size() > MaxFrameBytes {
		log.Loud(loudOversizeMsg, "path", p, "bytes", st.Size())
		return fmt.Errorf("%w: %w: %s", core.ErrNotFound, ErrTooLarge, p)
	}
	b, err := os.ReadFile(paths.Long(p))
	if err != nil {
		return fmt.Errorf("%w: %s: %v", core.ErrNotFound, p, err)
	}
	if err := s.UnmarshalBinary(b); err != nil {
		log.Loud(loudCorruptMsg, "path", p, "err", err.Error(), "bytes", len(b))
		return fmt.Errorf("%w: %w: %s", core.ErrNotFound, err, p)
	}
	return nil
}

// ReplaceGenerational is the ONLY way sketches/tried.bloom may be written
// (00-ARCHITECTURE.md §3.3): the current file is renamed to tried.bloom.<seq>.bak, the new content
// is staged and swapped into place, and exactly one backup generation is kept. It returns the
// backup's path, or "" when p did not exist and there was therefore nothing to displace.
//
// The mechanics live in paths.ReplaceBloom rather than here, and that is deliberate. internal/paths
// owns the §7.4 guard: paths.WriteAtomic refuses this path outright through paths.IsProtected, and
// ReplaceBloom is the one sanctioned exception, which is also why it is the one writer of the
// tried.bloom.<seq>.bak name its own pruner parses. Re-implementing the rename here would mean two
// writers of one filename family and a guard with a hole in it.
//
// What this function adds on top is the two things paths cannot know about: the sketch (it
// marshals s, and refuses to move anything on disk if s cannot encode) and the rollback.
// ReplaceBloom renames the current file to its backup BEFORE it stages the new content, so a
// staging failure would otherwise leave sketches/ with no tried.bloom at all — a state §12.3's
// rebuild path cannot tell apart from a first run. On any error, a displaced file is renamed back.
//
// The layout is reconstructed from p rather than taken as a parameter, so the §5.7-shaped
// (path, sketch) call site is preserved. A p that is not <root>/.qompack/sketches/tried.bloom is
// refused with ErrMalformed: a caller that hands over a path outside a project layout gets a clear
// failure, never a silent write to the wrong place.
//
// # Precondition: seq must exceed every surviving backup's sequence, and it is checked FIRST
//
// seq is not merely a label. paths.pruneBloomBackups keeps the backup with the HIGHEST sequence,
// not the most recently written one, so a call whose seq is at or below a surviving backup's has
// its own backup pruned the instant it is created. SP-09 drives seq from a monotonic rebuild
// counter, which satisfies this; nothing enforces it at the type level, so it is enforced here.
//
// It is a REFUSAL and not a report, and that is the whole of the fix. Detecting the violation
// afterwards left one path that destroys data: paths.ReplaceBloom renames the current file to its
// backup before it stages the new content, the prune then deletes that backup for being
// low-sequenced, and a staging failure at that point finds nothing to roll back — so
// sketches/tried.bloom, the append-only negative-knowledge file §7.4 protects, is simply gone.
// Reporting it accurately is not preventing it. paths.HighestBloomBackupSeq answers the question
// before anything moves, and a violated precondition now costs a wrapped ErrMalformed and no
// filesystem change at all.
//
// The two post-hoc checks below remain, narrower than they were:
//
//   - On success, the backup is stat'd before its path is returned, so a caller is never handed a
//     path to a file that no longer exists.
//   - On failure, the rollback rename is CHECKED, because a rollback that silently no-ops leaves
//     the store with no tried.bloom at all — strictly worse than the write error alone.
//
// Neither can now be reached by a mis-sequenced call: the pre-flight excludes that cause. What is
// left is a concurrent mutator — a second daemon, a stray rm, a scanner holding a handle — and for
// a file this package promises cannot be lost, "that should be impossible" is the wrong thing to
// print instead of an error.
func ReplaceGenerational(p string, s Sketch, seq int) (backup string, err error) {
	l, err := triedBloomLayout(p)
	if err != nil {
		return "", err
	}
	// Before the marshal, not after it: a mis-sequenced call is wrong about the call and not about
	// the sketch, so it must report the same way whether or not the sketch happened to encode.
	surviving, hasBackup, err := paths.HighestBloomBackupSeq(l)
	if err != nil {
		return "", fmt.Errorf("reading %s to check the surviving backup sequence: %w", l.Sketches, err)
	}
	if hasBackup && seq <= surviving {
		return "", fmt.Errorf(
			"%w: replacing %s with seq %d, which does not exceed the surviving backup %s: its own "+
				"backup would be pruned the instant it was made, and a staging failure would then "+
				"leave no %s at all",
			ErrMalformed, p, seq,
			filepath.Join(l.Sketches, fmt.Sprintf(triedBloomBackupFormat, surviving)), TriedBloomBase)
	}
	b, err := s.MarshalBinary()
	if err != nil {
		return "", err
	}

	// Recorded before the call, because afterwards the file at p is the new one either way.
	_, statErr := os.Stat(paths.Long(p))
	hadOld := statErr == nil
	bak := filepath.Join(l.Sketches, fmt.Sprintf(triedBloomBackupFormat, seq))

	if writeErr := paths.ReplaceBloom(l, b, seq); writeErr != nil {
		if hadOld {
			if rollbackErr := os.Rename(paths.Long(bak), paths.Long(p)); rollbackErr != nil {
				return "", fmt.Errorf(
					"%w: and the rollback failed too (%w), so %s no longer exists: %s was created "+
						"by paths.ReplaceBloom and is not there now, which the seq %d pre-flight has "+
						"already ruled out as a pruning — something outside this process moved it",
					writeErr, rollbackErr, p, bak, seq)
			}
		}
		return "", writeErr
	}

	if !hadOld {
		return "", nil
	}
	if _, err := os.Stat(paths.Long(bak)); err != nil {
		return "", fmt.Errorf(
			"%w: %s was replaced, but its backup %s is already gone (%v): seq %d exceeded every "+
				"surviving backup when this call began, so pruning cannot account for it — something "+
				"outside this process removed it",
			ErrMalformed, p, bak, err, seq)
	}
	return bak, nil
}

// triedBloomLayout reconstructs the paths.Layout that owns p, and proves on the way that p really
// is <root>/.qompack/sketches/tried.bloom.
//
// The check is an equality against the Layout's own Sketches directory rather than a string match
// on ".qompack/sketches", so it agrees with internal/paths by construction: if the store layout
// ever moves, this guard moves with it instead of silently admitting the old shape.
func triedBloomLayout(p string) (paths.Layout, error) {
	if filepath.Base(p) != TriedBloomBase {
		return paths.Layout{}, fmt.Errorf(
			"%w: ReplaceGenerational writes %s only, not %s", ErrMalformed, TriedBloomBase, p)
	}
	sketches := filepath.Dir(p)
	l := paths.Of(filepath.Dir(filepath.Dir(sketches)))
	if l.Sketches != sketches {
		return paths.Layout{}, fmt.Errorf(
			"%w: %s is not <root>/.qompack/sketches/%s", ErrMalformed, p, TriedBloomBase)
	}
	return l, nil
}

// Quarantine renames a corrupt sketch out of the way, to "<p>.corrupt.<unix milliseconds>", and
// returns the new path (00-ARCHITECTURE.md §12.3). It assumes no directory layout, so it works on
// any sketch file anywhere, and it creates nothing: the rename frees the original name for a
// rebuild while keeping the damaged bytes on disk for an operator to look at.
//
// It calls os.Rename directly rather than going through paths, so Quarantine(<root>/.qompack/
// sketches/tried.bloom) MOVES the file doc.go says only ReplaceGenerational may replace. That is a
// deliberate, corruption-only exemption and not a hole in the §7.4 guard: §12.3's recovery is
// "bloom load fails → quarantine the bytes → rebuild from records/eliminations.jsonl", and a file
// whose CRC no longer verifies is not negative knowledge any more — refusing to move it would leave
// the store permanently unable to rebuild, which is the opposite of what append-only protects. The
// exemption is narrow by construction: the only caller that reaches it is one whose Load has
// already failed, and a Quarantine of an intact filter would be a caller bug rather than something
// this function can distinguish.
//
// This is the one place in the package that reads a clock, isolated here so that no marshalled byte
// can ever depend on the time. It is deliberately not core.Clock-injected: nothing in this
// package's output, and no test assertion anywhere, depends on the value — only on the shape
// <p>.corrupt.<digits> — so threading a Clock through a two-line rename helper would buy nothing.
func Quarantine(p string) (moved string, err error) {
	moved = p + quarantineInfix + strconv.FormatInt(time.Now().UnixMilli(), 10)
	if err := os.Rename(paths.Long(p), paths.Long(moved)); err != nil {
		return "", err
	}
	return moved, nil
}
