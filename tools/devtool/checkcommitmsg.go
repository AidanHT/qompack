package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// subjectRE is the Conventional Commits subject grammar the implementation spec §20 fixes
// exactly: type, optional (scope), ": ", then 1-64 characters of free text. Because "." matches
// any character, this alone does not reject a trailing period — that is checked separately so the
// error message can call it out specifically.
// The scope character class includes a comma so a commit touching several packages can name them
// all — 00-ARCHITECTURE §10 defines scope as "the Go package … or the subplan slug for
// cross-cutting work", and a foundation commit legitimately lands two or four packages at once
// (feat(core,paths), feat(logging,obs,tokens,hookio)). Without the comma those commits could only
// be spelled with a subplan slug, which says strictly less about what changed.
var subjectRE = regexp.MustCompile(`^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\([a-z0-9/_.,-]+\))?: .{1,64}$`)

// forbiddenTrailerRE matches the attribution trailers §10 and §20 both ban outright.
var forbiddenTrailerRE = regexp.MustCompile(`(?i)co-authored-by|signed-off-by|generated with`)

// refsRE matches a "Refs:" footer line, required on feat and fix commits.
var refsRE = regexp.MustCompile(`(?m)^Refs:`)

// robotEmoji is the disallowed 🤖 marker.
const robotEmoji = "🤖"

// maxBodyLineLen is the §20 body line-length limit.
const maxBodyLineLen = 100

// taskCheckCommitMsg validates the Conventional Commit message in the file named by args[0] (the
// path git's commit-msg hook passes as $1) and rejects attribution trailers.
func taskCheckCommitMsg(args []string) error {
	if len(args) != 1 {
		return errors.Join(errUsage, errors.New("usage: devtool check-commit-msg <commit-msg-file>"))
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("check-commit-msg: reading %s: %w", args[0], err)
	}
	if err := validateCommitMsg(string(b)); err != nil {
		return fmt.Errorf("check-commit-msg: %w", err)
	}
	return nil
}

// validateCommitMsg enforces the implementation spec §20 rules against raw, a full commit
// message: the subject line matches subjectRE with no trailing period; every line — subject
// included, since CI's own grep over `git log --format=%B` checks the whole message — is free of
// the forbidden attribution trailers and the robot emoji; every body line is at most
// maxBodyLineLen runes; and feat/fix subjects require a "Refs:" footer line. It returns nil for a
// valid message, or an error identifying the first violation found and the offending line.
func validateCommitMsg(raw string) error {
	text := strings.ReplaceAll(raw, "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return errors.New("commit message is empty")
	}
	lines := strings.Split(text, "\n")
	subject := lines[0]

	if strings.HasSuffix(subject, ".") {
		return fmt.Errorf("subject must not end with a period: %q", subject)
	}
	m := subjectRE.FindStringSubmatch(subject)
	if m == nil {
		return fmt.Errorf(
			"subject %q does not match Conventional Commits format "+
				"^(feat|fix|docs|test|refactor|perf|build|ci|chore|revert)(\\(scope\\))?: subject (1-64 chars)", subject)
	}

	for i, line := range lines {
		if forbiddenTrailerRE.MatchString(line) {
			return fmt.Errorf("line %d contains a forbidden attribution trailer: %q", i+1, line)
		}
		if strings.Contains(line, robotEmoji) {
			return fmt.Errorf("line %d contains the disallowed robot emoji: %q", i+1, line)
		}
		if i > 0 {
			if n := utf8.RuneCountInString(line); n > maxBodyLineLen {
				return fmt.Errorf("body line %d is %d characters, over the %d-character limit: %q", i+1, n, maxBodyLineLen, line)
			}
		}
	}

	if commitType := m[1]; commitType == "feat" || commitType == "fix" {
		if !refsRE.MatchString(text) {
			return fmt.Errorf("%s commits require a \"Refs:\" footer line", commitType)
		}
	}

	return nil
}
