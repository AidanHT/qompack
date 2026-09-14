package docs

import (
	"path/filepath"
	"strings"
	"testing"
)

// userGuideRel is the page these checks are about, named once.
const userGuideRel = "docs/user-guide.md"

// userGuidePath resolves the guide against the module root.
func userGuidePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), filepath.FromSlash(userGuideRel))
}

// mentions reports whether the page names token in a heading or an inline code span.
//
// Fenced blocks do not count: a token that appears only inside an example is not documentation of
// it. Headings count as written, because a per-command heading is "## `/qompack:status`" and the
// backticks are part of its text; code spans are compared on their contents, so `recall` matches
// the token "recall" exactly rather than by substring, which is what keeps `re_read` from being
// satisfied by a mention of `record_eliminated`.
func mentions(lines []string, token string) bool {
	for _, l := range lines {
		if text, _, ok := headingText(l); ok && strings.Contains(text, token) {
			return true
		}
		for _, span := range inlineCodeRe.FindAllString(l, -1) {
			if strings.Trim(span, "`") == token {
				return true
			}
		}
	}
	return false
}

// TestUserGuideCoversEveryGeneratedCommandAndTool derives both inventories from the generated
// pages, so a command or tool added to the shipped code — and therefore to docs/commands.md or
// docs/mcp-tools.md — fails here until the guide explains it. Nothing here is a count.
func TestUserGuideCoversEveryGeneratedCommandAndTool(t *testing.T) {
	lines := defenced(t, userGuidePath(t))

	for _, name := range generatedCommandNames(t) {
		if token := "/qompack:" + name; !mentions(lines, token) {
			t.Errorf("%s: no heading or code span names %s", userGuideRel, token)
		}
	}
	for _, name := range generatedToolNames(t) {
		if !mentions(lines, name) {
			t.Errorf("%s: no heading or code span names the MCP tool %q", userGuideRel, name)
		}
	}
}

// bindingQualifications are the four sentences the plan binds this page to. They are checked
// verbatim, against the raw bytes, because each one is a qualification a paraphrase loses: the
// difference between "ephemeral" meaning a Qompack record and meaning a host eviction is the
// whole point of the sentence, and a rewrite that drops half of it would still read fine.
var bindingQualifications = []string{
	"An ephemeral tag describes a Qompack record; it does not mean the host evicted anything.",
	"unavailable is not absent: a failed query leaves prior attempts unknown and never prohibits an approach.",
	"The rehydration budget is a target for Qompack-added material, not the total restored native context.",
	"Subscription usage and estimated API price are separate numbers; Qompack does not reconcile invoices.",
}

// TestUserGuideStatesTheBindingQualifications fails if any of them is missing or has been edited.
func TestUserGuideStatesTheBindingQualifications(t *testing.T) {
	body := strings.Join(readLines(t, userGuidePath(t)), "\n")
	for _, want := range bindingQualifications {
		if !strings.Contains(body, want) {
			t.Errorf("%s: does not state, verbatim: %s", userGuideRel, want)
		}
	}
}

// sectionOf returns the lines under the first heading whose text contains token, up to the next
// heading at the same level or above. An absent heading returns ok=false.
func sectionOf(lines []string, token string) ([]string, bool) {
	start, level := -1, 0
	for i, l := range lines {
		text, lv, ok := headingText(l)
		if !ok {
			continue
		}
		if start < 0 {
			if strings.Contains(text, token) {
				start, level = i+1, lv
			}
			continue
		}
		if lv <= level {
			return lines[start:i], true
		}
	}
	if start < 0 {
		return nil, false
	}
	return lines[start:], true
}

// TestUserGuideMarksCheckpointAsUnrouted ties the guide's statement to the generated page rather
// than to a fixed expectation: the condition is read out of docs/commands.md's own table row, so
// when SP-14's H3 route lands and the row stops saying "not yet routed", this test starts
// requiring the guide to stop saying it too. Neither direction is hard-coded.
func TestUserGuideMarksCheckpointAsUnrouted(t *testing.T) {
	const command = "/qompack:checkpoint"
	const phrase = "not yet routed"

	var row string
	for _, l := range readLines(t, filepath.Join(repoRoot(t), "docs", "commands.md")) {
		if strings.HasPrefix(strings.TrimSpace(l), "| `"+command+"`") {
			row = l
			break
		}
	}
	if row == "" {
		t.Fatalf("docs/commands.md: no table row for %s: the generated page changed shape", command)
	}
	unrouted := strings.Contains(row, phrase)

	section, ok := sectionOf(defenced(t, userGuidePath(t)), command)
	if !ok {
		t.Fatalf("%s: no section for %s", userGuideRel, command)
	}
	says := strings.Contains(strings.Join(section, "\n"), phrase)

	switch {
	case unrouted && !says:
		t.Errorf("%s: docs/commands.md still marks %s %q, and the guide's section does not say so",
			userGuideRel, command, phrase)
	case !unrouted && says:
		t.Errorf("%s: docs/commands.md no longer marks %s %q, so the guide must stop saying it",
			userGuideRel, command, phrase)
	}
}
