package mcp

// Redactor re-applies the CURRENT secret policy to bytes on their way OUT of the store
// (T20-M2-04). It is the retrieval half of redaction; the capture half is a different package's
// job and this one never sees it.
//
// The interface is DECLARED HERE and SATISFIED ELSEWHERE, exactly as DropReporter, Promoter and
// Widener already are, because 00-ARCHITECTURE.md §3.2 does not put internal/redact in this
// package's allow-set. The dependency therefore runs composition-root → mcp, never mcp → redact:
// internal/cli imports both and adapts one to the other (see cli.NewRetrievalRedactor). Widening
// the allow-set instead would have traded an architectural invariant for a shorter import block.
//
// It carries ONE method, and only the shape the retrieval path actually consumes: the redacted
// bytes, plus the name of the rule behind each match so a diagnostic can say WHAT was caught
// without ever quoting it.
type Redactor interface {
	// Redact replaces every match in in with a fixed-width placeholder and returns the redacted
	// bytes together with the name of the rule that fired, one entry per match, in match order.
	//
	// It MUST be idempotent against its own placeholder: retrieval re-runs it over bytes that
	// capture-time redaction may already have scrubbed, and a second pass must change nothing.
	Redact(in []byte) (out []byte, rules []string)
}

// redactorMissingReason is what a content tool reports when no Redactor reached it.
//
// This is the FAIL-CLOSED direction and it is deliberate. A build with no retrieval-side redactor
// cannot know whether a stored record predates a rule that would catch it today, so the only
// answer it can honestly give is "this build cannot serve archive text", never the bytes. The
// tempting alternative — pass the content through unchanged when no redactor was wired — turns one
// wiring omission into every secret in the archive being served in the clear, silently and
// forever. A tool that says it is unavailable is a visible, recoverable failure; that is not.
//
// Each tool then degrades to whatever it can still say honestly. `expand` and `re_read` return
// archive text and nothing else, so they report themselves unavailable and return none of it.
// `recall` returns POINTERS — a root hash, a path, a tool name, a score — which were never
// redaction's subject and are still true; it keeps those, drops every summary, and sets
// recallBody.SummariesWithheld so the omission is stated rather than inferred. No archive text
// reaches the model through any of the three.
const redactorMissingReason = "retrieval redaction is not configured in this build, so archive content cannot be served"
