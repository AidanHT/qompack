package docs

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// uatPath is the page these tests are about.
const uatPath = "docs/uat.md"

// uatIDs is the fixed inventory of scenario identifiers.
//
// A fixed list is normally the wrong shape for a documentation test — it goes stale the moment the
// thing it counts grows, and it passes while saying nothing. This is the one place where the
// inventory IS the requirement: V6-SP-18 §3 binds twelve retained scenarios, UAT-01 through UAT-12,
// and a page that silently dropped one (or renumbered around a gap) would have lost a scenario the
// subplan retained. So the list is written out, and the test asserts the page carries exactly it,
// in order. Nothing else in this file hard-codes a count.
var uatIDs = []string{
	"UAT-01", "UAT-02", "UAT-03", "UAT-04", "UAT-05", "UAT-06",
	"UAT-07", "UAT-08", "UAT-09", "UAT-10", "UAT-11", "UAT-12",
}

// uatBlocks is the labelled record shape every scenario section repeats, in the order it must
// appear. A reader who has learned one section has learned all twelve, and a row that lost its
// evidence or rollback block would be a scenario nobody could execute reproducibly.
var uatBlocks = []string{
	"**Scenario**",
	"**Preconditions**",
	"**Steps**",
	"**Expected observable result**",
	"**Evidence to record**",
	"**Failure and rollback outcome**",
	"**Result**",
}

// uatRecordFields are the lines the Result block must carry. They are the audit trail of one
// execution: what happened, against which build, when, by whom, where the evidence is, and whether
// the rollback was verified.
var uatRecordFields = []string{
	"Result:", "Snapshot:", "Date:", "Executed by:", "Evidence:", "Rollback verified:",
}

// uatHeadingRe matches a scenario section heading.
var uatHeadingRe = regexp.MustCompile(`^##\s+(UAT-[0-9]{2})\s*$`)

// uatSection is one `## UAT-NN` section: its id and the half-open line range it spans.
type uatSection struct {
	id         string
	start, end int // 0-based, [start,end)
}

// uatSections splits the page into its scenario sections. Headings are read from the de-fenced
// view so a `## UAT-NN` inside a fenced example could never be mistaken for a real section; the
// returned ranges index the RAW lines, because the record block is a fenced block and its content
// has to stay readable.
func uatSections(t *testing.T) ([]string, []uatSection) {
	t.Helper()
	abs := filepath.Join(repoRoot(t), filepath.FromSlash(uatPath))
	raw := readLines(t, abs)
	deFenced := defenced(t, abs)

	var secs []uatSection
	for i, l := range deFenced {
		m := uatHeadingRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		if n := len(secs); n > 0 {
			secs[n-1].end = i
		}
		secs = append(secs, uatSection{id: m[1], start: i, end: len(raw)})
	}
	return raw, secs
}

// TestUATRetainsAllTwelveIDs is the retention check: every scenario the subplan kept is still on
// the page, exactly once, in ascending order.
func TestUATRetainsAllTwelveIDs(t *testing.T) {
	_, secs := uatSections(t)

	var got []string
	for _, s := range secs {
		got = append(got, s.id)
	}
	if strings.Join(got, ",") != strings.Join(uatIDs, ",") {
		t.Fatalf("%s: scenario headings are %v, want %v (each exactly once, ascending)",
			uatPath, got, uatIDs)
	}
}

// TestUATSectionsHaveTheRecordShape asserts the per-row shape: the seven labelled blocks, in order,
// each once, and a Result block carrying all six record fields.
func TestUATSectionsHaveTheRecordShape(t *testing.T) {
	raw, secs := uatSections(t)
	if len(secs) == 0 {
		t.Fatalf("%s: no `## UAT-NN` sections found: the scan is looking in the wrong place", uatPath)
	}

	for _, s := range secs {
		// at[i] is the 0-based line of uatBlocks[i] within the section, or -1.
		at := make([]int, len(uatBlocks))
		for i := range at {
			at[i] = -1
		}
		for i := s.start; i < s.end; i++ {
			line := strings.TrimSpace(raw[i])
			for b, label := range uatBlocks {
				if line != label {
					continue
				}
				if at[b] >= 0 {
					t.Errorf("%s:%d: %s: %s appears more than once", uatPath, i+1, s.id, label)
					continue
				}
				at[b] = i
			}
		}

		ok := true
		for b, label := range uatBlocks {
			if at[b] < 0 {
				t.Errorf("%s:%d: %s: missing %s", uatPath, s.start+1, s.id, label)
				ok = false
			}
		}
		if !ok {
			continue
		}
		for b := 1; b < len(uatBlocks); b++ {
			if at[b] < at[b-1] {
				t.Errorf("%s:%d: %s: %s appears before %s; the order is %v",
					uatPath, at[b]+1, s.id, uatBlocks[b], uatBlocks[b-1], uatBlocks)
			}
		}

		record := strings.Join(raw[at[len(uatBlocks)-1]:s.end], "\n")
		for _, field := range uatRecordFields {
			if !strings.Contains(record, field) {
				t.Errorf("%s:%d: %s: the Result block has no %q line",
					uatPath, at[len(uatBlocks)-1]+1, s.id, field)
			}
		}
	}
}

// uatResultLineRe captures the value of a record's `Result:` line.
var uatResultLineRe = regexp.MustCompile(`^Result:\s*(.*)$`)

// TestUATUnexecutedRowsSayUnverified is the claims check. A row that did not run — anything whose
// Result is neither a pass nor a fail — must say so in the words that carry the consequence: the
// capability it covers is unverified. A blank, a dash or an optimistic "n/a" would let an
// unexecuted scenario read as a clean one.
func TestUATUnexecutedRowsSayUnverified(t *testing.T) {
	raw, secs := uatSections(t)

	for _, s := range secs {
		var resultLine, record string
		for i := s.start; i < s.end; i++ {
			line := strings.TrimSpace(raw[i])
			if m := uatResultLineRe.FindStringSubmatch(line); m != nil {
				resultLine = strings.ToLower(strings.TrimSpace(m[1]))
				record = strings.Join(raw[i:s.end], "\n")
				break
			}
		}
		if resultLine == "" {
			t.Errorf("%s: %s: no `Result:` line", uatPath, s.id)
			continue
		}
		if strings.HasPrefix(resultLine, "pass") || strings.HasPrefix(resultLine, "fail") {
			continue
		}
		if !strings.Contains(strings.ToLower(record), "capability unverified") {
			t.Errorf("%s: %s: Result is %q, which is neither pass nor fail, and the record does "+
				"not say \"capability unverified\"", uatPath, s.id, resultLine)
		}
	}
}

// uatKeyRowRe matches a configuration-reference table row and captures the key in its first cell.
var uatKeyRowRe = regexp.MustCompile("^\\|\\s*`([a-z][A-Za-z0-9.]*)`\\s*\\|")

// uatDottedRe matches a dotted configuration key inside a code span.
//
// Every segment must begin with a lowercase letter, which is what separates a config key
// (`runtime.rehydrate.maxTokens`) from a Go identifier that happens to be dotted
// (`checkpoint.DropEntry`, `store.Migrator`) — those are source references, not settings, and the
// page is allowed to name them.
var uatDottedRe = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:\.[a-z][A-Za-z0-9]*)+\b`)

// uatFileExts are trailing segments that make a dotted candidate a FILE NAME rather than a
// configuration key: `eval.json` is an evidence file, `eliminations.jsonl` is a store file, and
// neither is a setting. A key never ends in one of these, because a key's leaf is a value's name.
var uatFileExts = map[string]bool{
	"json": true, "jsonl": true, "ndjson": true, "md": true, "go": true,
	"txt": true, "log": true, "bloom": true, "lock": true, "hb": true, "yml": true,
}

// uatIsConfigKeyMention reports whether cand, found at byte offset off in text, is a mention of a
// configuration key rather than a path component or a file name.
func uatIsConfigKeyMention(text, cand string, off int) bool {
	if off > 0 && (text[off-1] == '/' || text[off-1] == '\\') {
		return false // a path component: records/eliminations.jsonl
	}
	seg := cand[strings.LastIndexByte(cand, '.')+1:]
	return !uatFileExts[seg]
}

// TestUATConfigKeysExist keeps the page's configuration vocabulary honest against the generated
// reference: a scenario whose precondition names a key this build does not have is a scenario
// nobody can set up. Both sides are derived by parsing — the key set from
// docs/config-reference.md's own table rows, the mentions from the code spans on the UAT page — so
// neither a new key nor a new scenario makes this test stale.
func TestUATConfigKeysExist(t *testing.T) {
	root := repoRoot(t)

	keys := map[string]bool{}
	prefixes := map[string]bool{}
	for _, l := range defenced(t, filepath.Join(root, "docs", "config-reference.md")) {
		m := uatKeyRowRe.FindStringSubmatch(l)
		if m == nil || !strings.Contains(m[1], ".") {
			continue
		}
		keys[m[1]] = true
		prefixes[strings.SplitN(m[1], ".", 2)[0]] = true
	}
	if len(keys) == 0 {
		t.Fatal("docs/config-reference.md: no key rows parsed: the scan is looking in the wrong place")
	}

	for i, line := range defenced(t, filepath.Join(root, filepath.FromSlash(uatPath))) {
		for _, span := range inlineCodeRe.FindAllString(line, -1) {
			body := strings.Trim(span, "`")
			for _, at := range uatDottedRe.FindAllStringIndex(body, -1) {
				cand := body[at[0]:at[1]]
				if !prefixes[strings.SplitN(cand, ".", 2)[0]] || keys[cand] {
					continue
				}
				if !uatIsConfigKeyMention(body, cand, at[0]) {
					continue
				}
				t.Errorf("%s:%d: %s is not a key row in docs/config-reference.md",
					uatPath, i+1, cand)
			}
		}
	}
}
