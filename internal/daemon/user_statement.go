package daemon

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/observer"
	"github.com/qompack/qompack/internal/redact"
)

// maxStatementApproachBytes bounds the approach text taken from the preceding assistant turn. The
// ledger's own bound is larger and warns on every cut, and the first line of a reply is where an
// agent states what it is about to try.
const maxStatementApproachBytes = 240

// ingestUserStatement is the production caller of negknow's IngestUserStatement (§8.3 source #4):
// the observer hands it every prompt it has just captured durably, on the daemon's worker after
// the WAL, never on the hook's reply path.
//
// It is best-effort by construction. A prompt with no elimination phrase costs one string scan
// and touches nothing; only a match reads the transcript tail and opens the ledger. Every failure
// is a Warn and the capture it follows is already durable, so nothing here can fail or delay it. A
// redelivered prompt never reaches here (the observer absorbs it first), and a prompt captured
// twice still yields one record: the ledger dedups an identical active elimination.
//
// The prompt and the approach are redacted with red before the ingest. The production ledger is
// opened without Deps.Redact, and records/eliminations.jsonl is append-only, so a secret the user
// pasted into the prompt (which the store scrubbed) would otherwise sit there in clear and flow on
// into already_tried answers, checkpoints and the rehydration block.
func ingestUserStatement(ctx context.Context, openLedger func() negknow.Ledger, red redact.Redactor,
	log logging.Logger, c observer.PromptCapture,
) {
	if openLedger == nil || !negknow.IsUserStatement(c.Prompt) {
		return
	}
	led := openLedger()
	m, ok := led.(negknow.Maintainer)
	if !ok {
		return
	}
	scrub := func(s string) string {
		if red == nil {
			return s
		}
		out, _ := red.Redact([]byte(s))
		return string(out)
	}
	stmt := negknow.UserStatement{
		Prompt: scrub(c.Prompt), Turn: c.Turn, Path: c.LastEditPath, PromptRoot: c.Root,
	}
	// With no edited path the ledger refuses and counts the statement unresolved either way, so
	// the transcript is read only when a target exists. The transcript is searched for the RAW
	// prompt, which is what the host wrote there, and the reply is redacted before it is cut, so
	// no cut can split a secret into a prefix the rules no longer match.
	if c.LastEditPath != "" {
		stmt.Approach = statementApproach(scrub(observer.PrecedingAssistantText(c.TranscriptPath, c.Prompt)))
	}
	ctx = negknow.WithCaller(ctx, negknow.Caller{Session: c.Session, Turn: c.Turn})
	if _, err := m.IngestUserStatement(ctx, stmt); err != nil {
		log.Warn("negknow: user-statement ingest failed", "session", string(c.Session), "err", err.Error())
	}
}

// statementApproach is the first non-empty line of an assistant reply, cut on a rune boundary to
// maxStatementApproachBytes.
func statementApproach(text string) string {
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > maxStatementApproachBytes {
			cut := maxStatementApproachBytes
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut]
		}
		return line
	}
	return ""
}
