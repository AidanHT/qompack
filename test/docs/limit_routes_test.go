package docs

import (
	"path"
	"regexp"
	"strings"
	"testing"
)

// The release notes, the CHANGELOG and docs/release.md's capability table each list 0.3.0's known
// limits and send the reader to the page that documents each one. Audit 2's wave 22 verifier found
// all three sending the reader to docs/security.md §8 for decision D67(m)'s macOS limit, which
// security.md did not state: release.yml copies the notes verbatim, so the published notes pointed
// at a section without the limit. The check below closes the class: wherever one of those pages
// routes a limit to a document, that document must state the limit.

// ledgerDoc is the close-out ledger. It is kept on verify/v6, and the copy in a seat's tree trails it
// (it stops before the decisions the pages cite), so a route to it is checked by the decision it
// names, not by its text.
const ledgerDoc = "plans/V6-CLOSEOUT-CHECKLIST.md"

// limitRoute is one known limit: token finds where a release page states or names it, and probes
// are the lower-case phrases one block of a routed document must all contain to state it.
type limitRoute struct {
	name   string
	token  *regexp.Regexp
	probes []string
}

// knownLimitRoutes are the limits the release notes' Known limits section lists, one per bullet.
var knownLimitRoutes = []limitRoute{
	{"rotation pause", regexp.MustCompile(`(?i)pauses capture`), []string{"pauses capture"}},
	{
		"deferred note", regexp.MustCompile(`(?i)rehydration deferred|"no answer" note|the deferred note`),
		[]string{"deferred note"},
	},
	{"out of host order", regexp.MustCompile(`(?i)out of host order`), []string{"host-order", "renumbered"}},
	{
		"SessionEnd during a stop", regexp.MustCompile(`(?i)while the daemon is stopping`),
		[]string{"daemon is stopping"},
	},
	{
		"spool submode", regexp.MustCompile(`(?i)spool submode (lasts|does not switch back)`),
		[]string{"spool submode lasts"},
	},
	{"shorter page", regexp.MustCompile(`(?i)can be shorter than`), []string{"shorter than it could be"}},
	{"store write rows", regexp.MustCompile(`(?i)PutBytes`), []string{"miss their budgets"}},
	{
		"PreCompact behind a replay", regexp.MustCompile(`(?i)waits behind it|behind a spool replay`),
		[]string{"already running", "spool"},
	},
	{
		"fsck beside a daemon", regexp.MustCompile(`(?i)beside a (live|running) daemon`),
		[]string{"beside a running daemon"},
	},
	{
		"Windows directory sync", regexp.MustCompile(`(?i)directory[- ]sync`),
		[]string{"directory sync", "ntfs"},
	},
	{
		"session-only Read rules", regexp.MustCompile(`(?i)exist only in the running session`),
		[]string{"only in the running session"},
	},
	{
		"files view after a takeover", regexp.MustCompile(`(?i)idle exit without writing`),
		[]string{"idle exit without writing", "files.json"},
	},
	{
		"evolution entries", regexp.MustCompile(`(?i)evolution entries (are )?not re-admitted|older evolution entries`),
		[]string{"evolution entries", "not re-admitted"},
	},
	{
		"below the smallest loss notice", regexp.MustCompile(`(?i)below the smallest loss notice`),
		[]string{"below the smallest loss notice"},
	},
	{"free-text whitelist", regexp.MustCompile(`(?i)whitelist`), []string{"whitelist", "d64(4)"}},
	{
		"macOS case-insensitive volume", regexp.MustCompile(`(?i)case-insensitive`),
		[]string{"macos", "case-sensitive"},
	},
	{"recorded-corpus tier", regexp.MustCompile(`(?i)recorded-corpus tier`), []string{"recorded-corpus tier"}},
	{
		"quiet live session", regexp.MustCompile(`(?i)counted as ended until its next hook`),
		[]string{"counted as ended until its next hook"},
	},
	{"second daemon", regexp.MustCompile(`(?i)second daemon`), []string{"second daemon", "lock"}},
	{
		"cross-session segment close", regexp.MustCompile(`(?i)another session's tool use`),
		[]string{"another session's tool use"},
	},
	{
		"backup refusal after a downgrade", regexp.MustCompile(`(?i)refuses? while a newer|backup refusal`),
		[]string{"settingsversion", "refuse"},
	},
	{"not in this build", regexp.MustCompile(`(?i)manual checkpoint`), []string{"no manual checkpoint command"}},
}

// docRefRe finds a reference to a repository document: a markdown link's target, a backticked
// docs/ or plans/ path, or the close-out ledger by name.
var docRefRe = regexp.MustCompile("\\]\\(([^)\\s]+)\\)|`((?:docs|plans)/[^`\\s]+\\.md)`|(close-out ledger)")

// publishedPrefix is how the release notes, which release.yml publishes outside the tree, link a
// repository document.
const publishedPrefix = "https://github.com/AidanHT/qompack/blob/v0.3.0/"

// docRefs returns the repository documents text references, resolving a relative link from page.
func docRefs(page, text string) []string {
	var out []string
	for _, m := range docRefRe.FindAllStringSubmatch(text, -1) {
		switch {
		case m[3] != "":
			out = append(out, ledgerDoc)
		case m[2] != "":
			out = append(out, m[2])
		default:
			target, _, _ := strings.Cut(m[1], "#")
			switch {
			case strings.HasPrefix(target, publishedPrefix):
				out = append(out, strings.TrimPrefix(target, publishedPrefix))
			case target == "" || schemeRe.MatchString(target):
			default:
				out = append(out, path.Clean(path.Join(path.Dir(page), target)))
			}
		}
	}
	return out
}

// pageSection returns the normalized blocks of page between the heading start and the next heading
// of the same or a higher level.
func pageSection(t *testing.T, root, page, start string) []string {
	t.Helper()
	body := readDoc(t, root, page)
	i := strings.Index(body, "\n"+start+"\n")
	if i < 0 {
		t.Fatalf("%s: heading %q was not found", page, start)
	}
	rest := body[i+len(start)+2:]
	level := strings.SplitN(start, " ", 2)[0]
	for _, h := range []string{"\n# ", "\n## ", "\n### "} {
		if len(strings.TrimSpace(h)) > len(level) {
			continue
		}
		if j := strings.Index(rest, h); j >= 0 {
			rest = rest[:j]
		}
	}
	return docBlocks(rest)
}

// introClauseRe splits the release notes' routing paragraph into one clause per exception.
var introClauseRe = regexp.MustCompile(`,\s+(?:and\s+)?the\s+`)

// releaseNotesRoutes returns, for each limit, the clause of the release notes' routing paragraph
// that routes it and the documents it names: an exception's own documents, or docs/cannot-do.md for
// every other limit.
func releaseNotesRoutes(t *testing.T, blocks []string) func(limitRoute) (string, []string) {
	t.Helper()
	var intro string
	for _, b := range blocks {
		if strings.HasPrefix(b, "Each was accepted") {
			intro = b
		}
	}
	head, exceptions, ok := strings.Cut(intro, " except ")
	if !ok {
		t.Fatalf("docs/release-notes/v0.3.0.md: the Known limits routing paragraph was not found")
	}
	def := docRefs("docs/release-notes/v0.3.0.md", head)
	clauses := introClauseRe.Split(exceptions, -1)
	return func(l limitRoute) (string, []string) {
		for _, c := range clauses {
			if l.token.MatchString(c) {
				return c, docRefs("docs/release-notes/v0.3.0.md", c)
			}
		}
		return head, def
	}
}

// statesLimit reports whether one block of doc holds every probe of l.
func statesLimit(t *testing.T, root, doc string, l limitRoute) bool {
	t.Helper()
	for _, b := range docBlocks(readDoc(t, root, doc)) {
		lower := strings.ToLower(b)
		all := true
		for _, p := range l.probes {
			all = all && strings.Contains(lower, p)
		}
		if all {
			return true
		}
	}
	return false
}

// checkRoute fails when a page sends l to a document that does not state it. A route to the
// close-out ledger must name the decision the ledger records it under.
func checkRoute(t *testing.T, root, page string, l limitRoute, clause string, docs []string) {
	t.Helper()
	for _, doc := range docs {
		if doc == ledgerDoc {
			if !regexp.MustCompile(`\bD\d+`).MatchString(clause) {
				t.Errorf("%s routes %q to the close-out ledger without naming its decision", page, l.name)
			}
			continue
		}
		if !statesLimit(t, root, doc, l) {
			t.Errorf("%s routes %q to %s, which does not state it (no block holds %q)",
				page, l.name, doc, l.probes)
		}
	}
}

// TestReleasePagesRouteEachLimitToAPageThatStatesIt asserts every document the release notes, the
// CHANGELOG's Known limits and docs/release.md's capability table send a known limit to states that
// limit.
func TestReleasePagesRouteEachLimitToAPageThatStatesIt(t *testing.T) {
	root := repoRoot(t)

	notes := pageSection(t, root, "docs/release-notes/v0.3.0.md", "## Known limits and accepted residuals")
	route := releaseNotesRoutes(t, notes)
	for _, l := range knownLimitRoutes {
		listed := false
		for _, b := range notes {
			listed = listed || (strings.HasPrefix(b, "- ") && l.token.MatchString(b))
		}
		if !listed {
			t.Errorf("docs/release-notes/v0.3.0.md: no Known limits bullet states %q", l.name)
			continue
		}
		clause, docs := route(l)
		if len(docs) == 0 {
			t.Errorf("docs/release-notes/v0.3.0.md routes %q to no document", l.name)
		}
		checkRoute(t, root, "docs/release-notes/v0.3.0.md", l, clause, docs)
	}

	// routed counts the routes each of the other two pages makes, so a page whose routes this
	// check stopped finding fails instead of passing with nothing checked.
	routed := map[string]int{}

	for _, row := range pageSection(t, root, "docs/release.md", "## Capability status at 0.3.0") {
		if !strings.HasPrefix(row, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(row, "| "), "|")
		where := cells[len(cells)-1]
		for _, l := range knownLimitRoutes {
			if l.token.MatchString(strings.Join(cells[:len(cells)-1], "|")) {
				docs := docRefs("docs/release.md", where)
				routed["docs/release.md"] += len(docs)
				checkRoute(t, root, "docs/release.md", l, row, docs)
			}
		}
	}

	for _, b := range pageSection(t, root, "CHANGELOG.md", "### Known limits") {
		lead := ""
		if head, _, ok := strings.Cut(b, ": "); ok && strings.Contains(head, "documented in") {
			lead = head
		}
		for _, clause := range strings.Split(b, "; ") {
			for _, l := range knownLimitRoutes {
				loc := l.token.FindStringIndex(clause)
				if loc == nil {
					continue
				}
				docs := docRefs("CHANGELOG.md", clause[loc[0]:])
				if len(docs) == 0 && lead != "" {
					docs = docRefs("CHANGELOG.md", lead)
				}
				routed["CHANGELOG.md"] += len(docs)
				checkRoute(t, root, "CHANGELOG.md", l, clause, docs)
			}
		}
	}
	for _, page := range []string{"docs/release.md", "CHANGELOG.md"} {
		if routed[page] == 0 {
			t.Errorf("%s: no known limit's route was found, so nothing was checked", page)
		}
	}
}
