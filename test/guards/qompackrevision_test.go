package guards

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is the enforcing guard for `V4-ALL-08`, which V4-report.md section 19 lists as one of
// the five items holding up the V4 gate, and which plans/sdd/V4-VERIFY/inventory.md records as
// "RETIRED AS WRITTEN; GATE MISSING".
//
// The history matters, because the retired form is the reason a weaker guard is not acceptable
// here. `V4-ALL-08` originally asserted that `Qompack.md` is byte-identical to its root commit —
// that `git diff <root> HEAD -- Qompack.md` is empty. That assertion was false on this tree and had
// been false long before the V4 work: the diff reports 268 insertions and 1082 deletions, and the
// document carries a Revision log standing at v1.5. "Read-only" in the plan set means read-only
// *to subplans*, not frozen. Coordinator ruling NC-1a retired the command and put this in its
// place:
//
//	Qompack.md changes only through an authorized entry in its own Revision log with a matching
//	plans/QOMPACK-ERRATA.md record.
//
// The inventory then searched for something enforcing that and found nothing —
// tools/devtool/genconfigdocs.go reads Qompack.md only to generate Appendix C, test/guards had no
// revision-log case, and there is no repo hook — so the row was scored MISSING rather than passed.
// The inventory also named the owner: "Writing that guard is V4/V5 work and belongs to
// `test/guards`." This is that guard.
//
// Three assertions, because the replacement has three clauses and each fails differently:
//
//   - the content pin below detects that an edit happened at all;
//   - the declared version must appear in the Revision log, which is the "authorized entry";
//   - and it must appear in QOMPACK-ERRATA.md, which is the "matching record".
//
// Dropping the pin would leave the common case unguarded. Without it a subplan can rewrite any
// paragraph of Qompack.md, leave the version line alone, and every other check here stays green —
// which is precisely the hole that let the original assertion sit false for the life of the
// project. The pin is the only static evidence that a change occurred.

// qompackDigest is the SHA-256 of Qompack.md at the revision named by qompackPinnedVersion.
//
// Updating this constant is the last step of an authorized revision, never the first. The full
// path is: revise the document, add its Revision-log entry, add the matching QOMPACK-ERRATA.md
// record, then re-pin here in the same commit. A pin bumped on its own is the shape of exactly the
// unauthorized edit this guard exists to catch, and it will still fail the two checks below unless
// the records moved with it.
//
// The digest is over the file's bytes as checked out. That is stable across platforms because
// .gitattributes declares `*.md text eol=lf`, so the working tree is LF everywhere. Normalizing
// line endings here instead would be worse: it would hide a checkout that had silently rewritten
// them, which is a real corruption and not something a guard should paper over.
const qompackDigest = "562cc87c7548650d6b3e7c1c6c84f982311415e82fe30c878d98594999cbff6b"

// qompackPinnedVersion is the version qompackDigest was taken at. Keeping it beside the digest is
// what makes a stale pin legible: a mismatch here names the revision that was skipped, rather than
// reporting two hex strings and leaving the reader to work out which one is current.
const qompackPinnedVersion = "1.8"

var (
	// qompackVersionLine matches the document's own version declaration:
	// `*Design document v1.5 — planning revision, 2026-09-06.*`
	qompackVersionLine = regexp.MustCompile(`(?m)^\*Design document v(\d+\.\d+)`)

	// qompackRevisionEntry matches a Revision-log entry. Both spellings the log actually uses
	// are covered: `**v1.5 — 2026-09-06, ...**` and `**v1.1** — Corrections ...`.
	qompackRevisionEntry = regexp.MustCompile(`(?m)^\*\*v(\d+\.\d+)`)

	// qompackErrataSection matches an errata record heading: `## v1.3 — the §5.1 ...`.
	qompackErrataSection = regexp.MustCompile(`(?m)^## v(\d+\.\d+)`)
)

// TestQompackChangesOnlyThroughAnAuthorizedRevision is the first clause of NC-1a's replacement
// assertion: that a change to Qompack.md is visible at all.
func TestQompackChangesOnlyThroughAnAuthorizedRevision(t *testing.T) {
	body := qompackBody(t)

	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])

	require.Equal(t, qompackDigest, got,
		"Qompack.md has changed since it was pinned at v%s.\n"+
			"V4-ALL-08's replacement assertion (NC-1a) is that the document changes only through an "+
			"authorized entry in its own Revision log with a matching plans/QOMPACK-ERRATA.md record.\n"+
			"If this change is authorized, complete it: add the Revision-log entry, add the errata "+
			"record, and re-pin qompackDigest to %s in the same commit.\n"+
			"If it is not, revert it — read-only means read-only to subplans.", qompackPinnedVersion, got)
}

// TestQompackDeclaredVersionIsRecorded is the second and third clauses: the version the document
// declares must be the one the Revision log authorizes and the one the errata file records.
//
// Checking the DECLARED version rather than the newest entry in either file is deliberate. A
// revision that bumps the log but forgets the title line ships a document that misreports itself,
// and every consumer that reads the version — genconfigdocs.go among them — then disagrees with
// the log about which revision it is reading.
func TestQompackDeclaredVersionIsRecorded(t *testing.T) {
	body := qompackBody(t)

	m := qompackVersionLine.FindSubmatch(body)
	require.NotNil(t, m, "Qompack.md must declare its own version as `*Design document vX.Y ...*`")
	declared := string(m[1])

	require.Equal(t, qompackPinnedVersion, declared,
		"the pinned version and the document's declared version disagree; re-pin both together")

	logged := qompackRevisionVersions(t, body)
	require.Contains(t, logged, declared,
		"Qompack.md declares v%s, but its Revision log has no entry for that revision.\n"+
			"An unlogged revision is the unauthorized edit V4-ALL-08's replacement forbids.\n"+
			"Logged revisions: %v", declared, logged)

	errata := qompackErrataVersions(t)
	require.Contains(t, errata, declared,
		"Qompack.md declares v%s, but plans/QOMPACK-ERRATA.md has no `## v%s` record.\n"+
			"The Revision log says what changed; the errata record says what was checked, what held "+
			"and what could not be verified. NC-1a requires both.\n"+
			"Recorded revisions: %v", declared, declared, errata)
}

// TestQompackErrataAndRevisionLogAgree catches the two files drifting apart in either direction.
//
// The floor exists because the errata file starts at v1.3 — it was created as v1.3's verification
// record, so v1.1 and v1.2 predate it and legitimately have no section. Taking the floor from the
// errata file rather than hardcoding 1.3 keeps this honest if an earlier record is ever backfilled.
func TestQompackErrataAndRevisionLogAgree(t *testing.T) {
	logged := qompackRevisionVersions(t, qompackBody(t))
	errata := qompackErrataVersions(t)
	require.NotEmpty(t, errata, "plans/QOMPACK-ERRATA.md must record at least one revision")

	inLog := make(map[string]bool, len(logged))
	for _, v := range logged {
		inLog[v] = true
	}
	for _, v := range errata {
		require.True(t, inLog[v],
			"plans/QOMPACK-ERRATA.md records v%s, which the Qompack.md Revision log does not mention.\n"+
				"An errata record without a Revision-log entry describes a revision nobody authorized.", v)
	}

	// errata is sorted ascending, so its first element is the floor.
	floor := errata[0]
	for _, v := range logged {
		if qompackVersionLess(v, floor) {
			continue
		}
		require.Contains(t, errata, v,
			"the Qompack.md Revision log carries v%s, but plans/QOMPACK-ERRATA.md has no `## v%s` "+
				"record for it. Every revision from v%s onward carries both.", v, v, floor)
	}
}

// qompackBody reads Qompack.md from the repository root.
//
// Qompack.md and plans/QOMPACK-ERRATA.md are maintainer-only: they stay on the maintainer's disk and
// are not published, so a public checkout has neither. Every test here reads Qompack.md first, so
// its absence skips them all. Any other read error still fails, and with Qompack.md present a
// missing errata file still fails, which keeps the pairing rule enforced where the spec exists.
func qompackBody(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "Qompack.md"))
	if os.IsNotExist(err) {
		t.Skip("platform: Qompack.md is maintainer-only and absent from this checkout")
	}
	require.NoError(t, err, "Qompack.md must be readable from the repository root")
	return body
}

// qompackRevisionVersions returns the distinct versions the Revision log authorizes, sorted.
//
// Only the text at or after the `## Revision log` heading is scanned. The document quotes bolded
// version strings elsewhere, and counting one of those as an authorization would let a mention
// stand in for an entry.
func qompackRevisionVersions(t *testing.T, body []byte) []string {
	t.Helper()
	const heading = "\n## Revision log"
	i := strings.Index(string(body), heading)
	require.GreaterOrEqual(t, i, 0, "Qompack.md must carry a `## Revision log` section")
	return qompackDistinctVersions(qompackRevisionEntry.FindAllStringSubmatch(string(body)[i:], -1))
}

// qompackErrataVersions returns the distinct versions plans/QOMPACK-ERRATA.md records, sorted.
func qompackErrataVersions(t *testing.T) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("..", "..", "plans", "QOMPACK-ERRATA.md"))
	require.NoError(t, err, "plans/QOMPACK-ERRATA.md must be readable")
	return qompackDistinctVersions(qompackErrataSection.FindAllStringSubmatch(string(body), -1))
}

// qompackDistinctVersions collapses regexp submatches to a sorted, deduplicated version list. The
// Revision log carries both `**v1.5 — ...**` and `**v1.5 addendum — ...**`; those are one revision.
func qompackDistinctVersions(matches [][]string) []string {
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, m[1])
	}
	sort.Slice(out, func(i, j int) bool { return qompackVersionLess(out[i], out[j]) })
	return out
}

// qompackVersionLess orders two `major.minor` strings numerically. String order would put v1.10
// before v1.2, which is the wrong side of the errata floor.
func qompackVersionLess(a, b string) bool {
	amaj, amin := qompackVersionParts(a)
	bmaj, bmin := qompackVersionParts(b)
	if amaj != bmaj {
		return amaj < bmaj
	}
	return amin < bmin
}

// qompackVersionParts splits `major.minor`. The regexps above only ever produce that shape, so a
// parse failure here means one of them was widened without this being updated; zero is a safe
// answer for an ordering comparison, and the assertions above still name the offending version.
func qompackVersionParts(v string) (int, int) {
	majorPart, minorPart, ok := strings.Cut(v, ".")
	if !ok {
		return 0, 0
	}
	major, _ := strconv.Atoi(majorPart)
	minor, _ := strconv.Atoi(minorPart)
	return major, minor
}
