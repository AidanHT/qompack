package eval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// Environment variables the importer reads. Both are read through an injected Getenv rather than
// os.Getenv, so a test never has to mutate real process environment to exercise either.
const (
	sessionsDirEnvVar   = "QOMPACK_SESSIONS_DIR"
	allowUnredactedEnv  = "QOMPACK_EVAL_ALLOW_UNREDACTED"
	recordedCorpusTier  = "recorded"
	transcriptExtension = ".jsonl"
	// moduleLine identifies this repository's go.mod, which is how the importer recognizes a
	// destination inside the working tree.
	moduleLine = "module github.com/qompack/qompack"
)

// compactBoundaryMarker is the text a compaction summary turn opens with.
const compactBoundaryMarker = "This session is being continued from a previous conversation"

// Scanner bounds for one transcript line. A single tool result can be very large, and the default
// bufio.Scanner limit of 64 KiB would reject it — silently losing the biggest results, which are
// exactly the ones a compaction decision turns on.
const (
	transcriptScanInitial = 64 << 10
	transcriptScanMax     = 16 << 20
)

// transcriptRecord is the subset of a Claude Code transcript line this importer reads. Every other
// field the host writes is ignored rather than rejected, because the format is the host's and it
// will grow fields this package has never heard of.
type transcriptRecord struct {
	Type             string `json:"type"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Message          struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Usage   struct {
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// contentBlock is one entry of a message's content array.
type contentBlock struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Input      json.RawMessage `json:"input"`
	ToolUseID  string          `json:"tool_use_id"`
	RawContent json.RawMessage `json:"content"`
}

// pathFields are the tool-input keys that name a file.
var pathFields = []string{"file_path", "path", "notebook_path", "pattern"}

// Import reads recorded Claude Code transcripts and writes redacted sessions to a destination
// outside the repository.
//
// This is §6.3's tier 2: the fidelity tier that gates releases. Recorded transcripts carry user
// code, prompts, home paths and secrets, so redaction is on by default at the command layer, the
// bypass needs a second environment variable, and a destination inside the working tree is refused
// outright. "Never committed" is mechanical here rather than a convention someone has to remember.
func Import(ctx context.Context, o ImportOptions) (ImportReport, error) {
	if err := ctx.Err(); err != nil {
		return ImportReport{}, err
	}
	getenv := o.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	from := o.From
	if from == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ImportReport{}, fmt.Errorf("eval: resolving the transcript root: %w", err)
		}
		from = filepath.Join(home, ".claude", "projects")
	}

	to := o.To
	if to == "" {
		to = getenv(sessionsDirEnvVar)
	}
	if to == "" {
		return ImportReport{}, fmt.Errorf(
			"eval: no import destination: pass --to or set %s; recorded transcripts are never "+
				"written inside the repository: %w", sessionsDirEnvVar, core.ErrNotFound)
	}
	if root, inside := insideRepository(to); inside {
		return ImportReport{}, fmt.Errorf(
			"eval: refusing to import into %s: it is inside the qompack repository working tree "+
				"at %s, and recorded sessions are never committed", to, root)
	}

	operatorPatterns, err := compileOperatorPatterns(o)
	if err != nil {
		return ImportReport{}, err
	}

	files, err := transcriptFiles(from)
	if err != nil {
		return ImportReport{}, err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return ImportReport{}, fmt.Errorf("eval: creating %s: %w", to, err)
	}

	var rep ImportReport
	skipped := map[string]bool{}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if o.Limit > 0 && rep.Sessions >= o.Limit {
			break
		}
		rep.Files++

		s, fileSkipped, err := parseTranscript(file)
		if err != nil {
			return rep, err
		}
		for _, k := range fileSkipped {
			skipped[k] = true
		}
		if len(s.Turns) == 0 {
			continue
		}
		if o.Redact {
			var n int
			s, n = Redact(s, operatorPatterns...)
			rep.RedactedSpans += n
		}

		body, err := EncodeSession(s)
		if err != nil {
			return rep, err
		}
		if err := paths.WriteAtomic(filepath.Join(to, s.ID+".json"), body, corpusFilePerm); err != nil {
			return rep, fmt.Errorf("eval: writing %s: %w", s.ID, err)
		}
		rep.Sessions++
		rep.Turns += len(s.Turns)
	}

	for k := range skipped {
		rep.Skipped = append(rep.Skipped, k)
	}
	sort.Strings(rep.Skipped)
	return rep, nil
}

// insideRepository walks upward from dir looking for this module's go.mod, and reports the root it
// found. Detecting the module rather than merely a .git directory is what keeps the check precise:
// a user's own sessions directory may legitimately live inside some unrelated repository.
func insideRepository(dir string) (string, bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for d := abs; ; {
		if raw, err := os.ReadFile(filepath.Join(d, "go.mod")); err == nil {
			if strings.HasPrefix(strings.TrimSpace(string(raw)), moduleLine) {
				return d, true
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", false
		}
		d = parent
	}
}

// transcriptFiles returns every *.jsonl under root, in a stable order.
func transcriptFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), transcriptExtension) {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("eval: walking %s: %w", root, err)
	}
	sort.Strings(out)
	return out, nil
}

// parseTranscript turns one .jsonl transcript into a Session, and reports every record type it did
// not recognize.
func parseTranscript(path string) (Session, []string, error) {
	f, err := os.Open(path) //nolint:gosec // an explicitly named transcript file
	if err != nil {
		return Session{}, nil, fmt.Errorf("eval: opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	s := Session{
		ID:   strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Meta: map[string]string{"corpusTier": recordedCorpusTier, "source": filepath.Base(path)},
	}
	byToolUse := map[string]*ToolCall{}
	var skipped []string

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, transcriptScanInitial), transcriptScanMax)
	for line := 0; sc.Scan(); line++ {
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var rec transcriptRecord
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			return Session{}, nil, fmt.Errorf("eval: parsing %s line %d: %w", path, line+1, err)
		}

		switch rec.Type {
		case roleUser, roleAssistant:
			appendTranscriptTurn(&s, rec, byToolUse, &skipped)
		default:
			// A transcript carries record types this importer has never heard of, and refusing
			// the whole file over one of them would make the recorded tier unusable.
			skipped = append(skipped, rec.Type)
		}
	}
	if err := sc.Err(); err != nil {
		return Session{}, nil, fmt.Errorf("eval: reading %s: %w", path, err)
	}
	return s, skipped, nil
}

// appendTranscriptTurn maps one user or assistant record onto the session.
func appendTranscriptTurn(s *Session, rec transcriptRecord, byToolUse map[string]*ToolCall, skipped *[]string) {
	text, calls, results := decodeContent(rec.Message.Content)

	// A user record carrying nothing but tool results is not a turn: it is the ANSWER to calls
	// made earlier, and attaching it there is what makes the result's tokens count toward the
	// block that produced them.
	if rec.Type == roleUser && text == "" && len(calls) == 0 && len(results) > 0 {
		for id, body := range results {
			if call, ok := byToolUse[id]; ok {
				call.Result = body
			}
		}
		return
	}
	for id, body := range results {
		if call, ok := byToolUse[id]; ok {
			call.Result = body
		}
	}

	idx := core.TurnIndex(len(s.Turns))
	turn := Turn{Index: idx, Role: rec.Type, Text: text, Tokens: core.Tokens(rec.Message.Usage.OutputTokens)}
	if turn.Tokens <= 0 {
		turn.Tokens = core.Tokens((len(text) + bytesPerTokenEstimate - 1) / bytesPerTokenEstimate)
	}
	turn.ToolCalls = calls
	s.Turns = append(s.Turns, turn)

	for i := range s.Turns[len(s.Turns)-1].ToolCalls {
		call := &s.Turns[len(s.Turns)-1].ToolCalls[i]
		byToolUse[string(call.ID)] = call
	}

	if rec.IsCompactSummary || strings.HasPrefix(text, compactBoundaryMarker) {
		s.CompactionAt = append(s.CompactionAt, idx)
	}
	_ = skipped
}

// decodeContent reads a message's content, which the host writes either as a bare string or as an
// array of typed blocks.
func decodeContent(raw json.RawMessage) (text string, calls []ToolCall, results map[string]json.RawMessage) {
	results = map[string]json.RawMessage{}
	if len(raw) == 0 {
		return "", nil, results
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString, nil, results
	}

	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", nil, results
	}

	var texts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			calls = append(calls, ToolCall{
				ID:    core.ToolUseID(b.ID),
				Name:  b.Name,
				Args:  b.Input,
				Paths: pathsFromInput(b.Input),
			})
		case "tool_result":
			results[b.ToolUseID] = toolResultBody(b.RawContent)
		}
	}
	return strings.Join(texts, "\n"), calls, results
}

// toolResultBody normalizes a tool result's content, which may be a string or a block array.
func toolResultBody(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

// pathsFromInput extracts every file path a tool input names, normalized the way the store keys
// them.
func pathsFromInput(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, field := range pathFields {
		v, ok := input[field].(string)
		if !ok || v == "" {
			continue
		}
		key := paths.Key(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out
}

// ── Redaction ───────────────────────────────────────────────────────────────────────────────

// redactRule is one scrubbing pass.
type redactRule struct {
	re   *regexp.Regexp
	with string
}

// redactRules run in this order, and the order is load-bearing in two places.
//
// A credentialed connection string runs BEFORE the e-mail rule. "postgres://admin:pw@db.internal"
// contains something that looks exactly like an e-mail address ("pw@db.internal"), so an e-mail
// rule running first consumes the credential, the connection-string rule then never fires, and the
// result is redacted but for the wrong reason — which is the kind of thing that looks fine until
// the one rule you were relying on turns out to be dead.
//
// The e-mail rule in turn refuses to match immediately after a ">", so it cannot re-consume the
// "<REDACTED>@host" the connection-string rule just produced.
//
// The specific token formats run before the generic "password: <anything>" rule, so a known
// credential is labelled as what it is rather than swallowed by the catch-all.
//
// Every rule is idempotent, and idempotence here means more than "a rule's own replacement never
// matches it again": the table is applied as a whole, so a rule must not match ANOTHER rule's
// replacement either. Every marker this table writes is angle-bracketed (<HOME>, <REDACTED>,
// <TOKEN>, <EMAIL>, <KEY>, <JWT>), so the convention that keeps the whole table idempotent is:
//
//	wherever a rule consumes arbitrary user text — that is, in every negated character class —
//	it excludes < and >.
//
// Two separate non-idempotencies came from breaking that convention, so it is enforced
// mechanically by TestRedactRules_NegatedClassesRejectMarkerBrackets rather than left to review,
// with the FuzzRedact target as the behavioural backstop.
//
// The one rule that consumes free text without a negated class is the catch-all below, whose \S+
// deliberately swallows everything up to whitespace: narrowing it to exclude brackets would end
// the match at the first "<" and leave the tail of the secret in the transcript. It is safe
// because its replacement is a fixed point — "password=<REDACTED>" re-matches it and re-produces
// itself unchanged.
var redactRules = []redactRule{
	// The trailing character class excludes < and > so that the rule's own <HOME> replacement can
	// never be re-consumed as a username on a second pass. Without that, "A:\Users\A:\Users\bob"
	// redacts to "A:\Users\<HOME>" and then to "<HOME>" — a real non-idempotency the fuzz target
	// found, and the reason the seed corpus carries an already-redacted case.
	{regexp.MustCompile(`(?i)[A-Za-z]:\\Users\\[^\\/"'\s<>]+`), "<HOME>"},
	{regexp.MustCompile(`/(?:Users|home)/[^/\s"'<>]+`), "<HOME>"},
	// Both halves of the credential exclude < and > for the same reason, and this one needed a
	// second rule's marker to expose it: the e-mail rule runs later but can leave "<EMAIL>" where
	// a username sits, so "a://0@0.0:0@" redacted to "a://<EMAIL>:0@" and then, once this rule
	// accepted that marker as a username, to "a://<REDACTED>@".
	{regexp.MustCompile(`\b([a-z][a-z0-9+.-]*)://[^/\s:@<>]+:[^/\s@<>]+@`), "${1}://<REDACTED>@"},
	{regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`), "<KEY>"},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), "<TOKEN>"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`), "<TOKEN>"},
	// All five classic GitHub prefixes, with an OPEN body quantifier. Both halves are finding S-4:
	// the body was fixed at {36}, so a 38-character token matched its first 36 characters and then
	// failed the trailing \b — the rule did not fire on it at all — and ghu_/ghs_/ghr_ (a user-to-
	// server, server-to-server and refresh token) were outside the alternation entirely. This is
	// internal/redact's shape; the two rule sets are kept in step by hand because §3.2 gives
	// internal/eval a foundation-only allow-set that does not include internal/redact.
	{regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`), "<TOKEN>"},
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{16,}`), "<TOKEN>"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`), "<TOKEN>"},
	{regexp.MustCompile(`(^|[^>\w.+-])[\w.+-]+@[\w-]+\.[\w.]+`), "${1}<EMAIL>"},
	// The assignment family, widened to internal/redact's key set: passwd, client_secret, the three
	// access/refresh token spellings, a bare `auth`, and a leading underscore. `_authToken=` is how
	// npm writes a registry credential into .npmrc, and a word boundary cannot match between `_` and
	// a letter, so every underscored spelling was outside the rule. The underscore is consumed by
	// the match but is outside group 1, so the replacement drops it — which is still a fixed point,
	// because the output has no leading underscore to re-consume.
	{regexp.MustCompile(`(?i)\b_?(password|passwd|secret|api[_-]?key|access[_-]?key|` +
		`client[_-]?secret|auth[_-]?token|access[_-]?token|refresh[_-]?token|api[_-]?token|` +
		`auth|token)\s*[:=]\s*\S+`), "${1}=<REDACTED>"},
	// The .env line shape, which this rule set had no equivalent of at all: an UPPERCASE key whose
	// name carries one of the secret words, where the word sits mid-identifier and therefore behind
	// no word boundary the assignment rule above could use (API_SECRET, DATABASE_PASSWORD). The key
	// is group 1, so the line keeps its name.
	//
	// The value is `[^\s#<>]\S{7,}` rather than internal/redact's `[^\s#][^\n]{7,}`, and the
	// difference is this package's own marker-bracket convention: a value may not BEGIN with `<`,
	// so a line whose value is already a marker another rule wrote is left alone and the rule is a
	// fixed point. `\S` is not a negated class, so it carries no such hazard and needs no exclusion
	// (TestRedactRules_SpanningClassesRejectMarkerBrackets states the convention).
	{regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?[A-Z][A-Z0-9_]*` +
		`(?:KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL|DSN|PRIVATE)[A-Z0-9_]*)` +
		`[ \t]*=[ \t]*[^\s#<>]\S{7,}`), "${1}=<REDACTED>"},
	// Case-folded, and with the base64 padding a real bearer token can end in. `<REDACTED>` cannot
	// re-match it: `<` is outside the body class.
	{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]{16,}={0,2}`), "Bearer <REDACTED>"},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), "<JWT>"},
}

// Redact scrubs secrets, home paths and e-mail addresses out of a session and reports how many
// spans it replaced.
//
// It reaches Text, tool Args and Results, Paths and Meta — everywhere a transcript can carry a
// secret — and it never mutates its input, so a caller holding the original still has it.
// Redaction changes what a session SAYS, never its shape: turn count, roles, indices, token counts
// and compaction points all survive, because those are what the replay numbers are computed from.
//
// extra is the operator's own `runtime.redact.patterns`, and it is the second half of finding S-4.
// The built-in table above is this package's transcription of internal/redact's — §3.2 gives
// internal/eval a foundation-only allow-set that does not include internal/redact, so the two are
// kept in step by hand — but an operator's private rules exist only in configuration and could not
// be transcribed at all. Without them the export was strictly weaker than the capture path over
// exactly the secrets an operator had told the product about. Each is replaced with <REDACTED> and
// applied AFTER the built-ins, so a configured rule cannot swallow a marker they wrote first.
//
// It is variadic so that every existing caller — the fuzz target and the frozen seed corpus among
// them — keeps compiling against a signature that is still one slice of patterns.
func Redact(s Session, extra ...*regexp.Regexp) (Session, int) {
	total := 0
	scrub := func(in string) string {
		out, n := redactString(in, extra)
		total += n
		return out
	}

	out := s
	out.CompactionAt = append([]core.TurnIndex(nil), s.CompactionAt...)
	out.Turns = make([]Turn, len(s.Turns))
	for i, turn := range s.Turns {
		t := turn
		t.Text = scrub(turn.Text)
		t.ToolCalls = make([]ToolCall, len(turn.ToolCalls))
		for j, call := range turn.ToolCalls {
			c := call
			var n int
			c.Args, n = redactJSON(call.Args, extra)
			total += n
			c.Result, n = redactJSON(call.Result, extra)
			total += n
			c.Paths = make([]string, len(call.Paths))
			for k, p := range call.Paths {
				c.Paths[k] = scrub(p)
			}
			t.ToolCalls[j] = c
		}
		out.Turns[i] = t
	}

	if s.Meta != nil {
		out.Meta = make(map[string]string, len(s.Meta))
		keys := make([]string, 0, len(s.Meta))
		for k := range s.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out.Meta[k] = scrub(s.Meta[k])
		}
	}
	return out, total
}

// compileOperatorPatterns compiles ImportOptions.Patterns, or reports why it will not export.
//
// A pattern that does not compile refuses the whole import and names its INDEX, never its text: the
// pattern itself is the operator's, may describe a secret's shape, and an error message is the one
// place in this command that is certain to be read aloud or pasted into an issue.
func compileOperatorPatterns(o ImportOptions) ([]*regexp.Regexp, error) {
	if !o.Redact || len(o.Patterns) == 0 {
		return nil, nil
	}
	out := make([]*regexp.Regexp, 0, len(o.Patterns))
	for i, pat := range o.Patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf(
				"eval: runtime.redact.patterns[%d] does not compile, so the export would be weaker "+
					"than the capture path believes it is: %w", i, core.ErrDegraded)
		}
		out = append(out, re)
	}
	return out, nil
}

// redactJSON scrubs every string inside a JSON payload and re-encodes it.
//
// It works on the DECODED value rather than on the raw bytes, because a raw payload has its
// backslashes and newlines escaped: a Windows home path reads as C:\\Users\\… and a PEM block as
// one line with \n in it, so pattern matching on the raw form silently misses both. Decoding also
// keeps the result valid JSON, which scrubbing raw bytes cannot promise.
//
// Numbers are decoded with UseNumber so an integer token count cannot come back as 1.4e+02.
func redactJSON(raw json.RawMessage, extra []*regexp.Regexp) (json.RawMessage, int) {
	if len(raw) == 0 {
		return raw, 0
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		// Not valid JSON, so treat it as opaque text; the shape was never load-bearing.
		out, n := redactString(string(raw), extra)
		return json.RawMessage(out), n
	}

	scrubbed, n := redactValue(v, extra)
	if n == 0 {
		return raw, 0
	}
	out, err := marshalNoHTMLEscape(scrubbed)
	if err != nil {
		return raw, 0
	}
	return out, n
}

// marshalNoHTMLEscape encodes without Go's default HTML escaping, so a redaction marker stays
// readable as <KEY> rather than becoming <KEY>. Every other JSON writer in this codebase
// does the same.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// redactValue walks a decoded JSON value, scrubbing every string it contains.
func redactValue(v any, extra []*regexp.Regexp) (any, int) {
	switch t := v.(type) {
	case string:
		out, n := redactString(t, extra)
		return out, n
	case []any:
		total := 0
		for i, item := range t {
			scrubbed, n := redactValue(item, extra)
			t[i] = scrubbed
			total += n
		}
		return t, total
	case map[string]any:
		total := 0
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			scrubbed, n := redactValue(t[k], extra)
			t[k] = scrubbed
			total += n
		}
		return t, total
	default:
		return v, 0
	}
}

// redactString applies every rule in order and counts the spans replaced.
//
// The operator's own patterns run LAST, each replaced wholesale with <REDACTED>. Running them after
// the built-ins is what keeps the marker convention intact: a configured rule written against raw
// transcript text cannot swallow a <HOME> or an <EMAIL> that is not there yet, and a marker already
// written is only re-matched if the operator's own pattern matches it — which reproduces the same
// output and so is still a fixed point.
func redactString(in string, extra []*regexp.Regexp) (string, int) {
	if in == "" {
		return in, 0
	}
	n := 0
	out := in
	apply := func(re *regexp.Regexp, with string) {
		matches := re.FindAllStringIndex(out, -1)
		if len(matches) == 0 {
			return
		}
		replaced := re.ReplaceAllString(out, with)
		if replaced == out {
			// The rule matched but changed nothing, which is what an already-redacted span looks
			// like. Counting it would make the span count grow on every re-run.
			return
		}
		n += len(matches)
		out = replaced
	}
	for _, rule := range redactRules {
		apply(rule.re, rule.with)
	}
	for _, re := range extra {
		if re == nil {
			continue
		}
		apply(re, operatorPatternMarker)
	}
	return out, n
}

// operatorPatternMarker is what a configured `runtime.redact.patterns` rule leaves behind. It is
// the same bracketed-marker shape every built-in uses, so the idempotence convention holds for it
// too, and it is deliberately the generic word: an operator's pattern says what to remove, never
// what the thing it removed was.
const operatorPatternMarker = "<REDACTED>"

// ── qompack eval import ─────────────────────────────────────────────────────────────────────

// ImportCommand is the `qompack eval import` backend. It prints the ImportReport as one JSON
// object and returns a process exit code.
func ImportCommand(args []string, out io.Writer, env config.Env) int {
	fs := flag.NewFlagSet("eval import", flag.ContinueOnError)
	fs.SetOutput(out)
	from := fs.String("from", "", "transcript root (default ~/.claude/projects)")
	to := fs.String("to", "", "destination directory (default $"+sessionsDirEnvVar+")")
	limit := fs.Int("limit", 0, "stop after importing this many sessions (0 = no limit)")
	noRedact := fs.Bool("no-redact", false,
		"import without scrubbing secrets; requires "+allowUnredactedEnv+"=1")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(out, "qompack eval import: %v\n", err)
		return 1
	}

	getenv := env.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if *noRedact && getenv(allowUnredactedEnv) != "1" {
		fmt.Fprintf(out,
			"qompack eval import: --no-redact requires %s=1. An accidental unredacted import is a "+
				"loss-of-privacy event, not a convenience.\n", allowUnredactedEnv)
		return 1
	}

	// The operator's own redaction rules, read the way `config print` reads them. A configuration
	// this loader cannot read is not a reason to export MORE than the operator asked for, so the
	// import refuses rather than falling back to the built-in table alone (finding S-4).
	var patterns []string
	if !*noRedact {
		cfg, _, _, cfgErr := config.Load(env)
		if cfgErr != nil {
			fmt.Fprintf(out, "qompack eval import: reading the redaction configuration: %v\n", cfgErr)
			return 1
		}
		if cfg.Runtime.Redact.Enabled {
			patterns = cfg.Runtime.Redact.Patterns
		}
	}

	rep, err := Import(context.Background(), ImportOptions{
		From: *from, To: *to, Limit: *limit, Redact: !*noRedact, Getenv: getenv,
		Patterns: patterns,
	})
	if err != nil {
		fmt.Fprintf(out, "qompack eval import: %v\n", err)
		return 1
	}

	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintf(out, "qompack eval import: encoding the report: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, string(body))
	return 0
}
