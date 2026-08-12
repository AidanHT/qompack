package redact

// Match is one redaction: the span it covered in the original input and the name of the rule
// that fired.
type Match struct {
	// Offset is the match's byte offset in the ORIGINAL (pre-redaction) input.
	Offset int
	// Len is the match's length in bytes in the original input.
	Len int
	// Rule names the rule that produced this match (00-ARCHITECTURE.md §5.22a's built-in set:
	// private-key PEM blocks, AKIA…/ASIA… access keys, ghp_/gho_/github_pat_ tokens, sk-/sk-ant-
	// keys, bearer tokens, password=/secret=/token= assignments, .env value lines, JWTs, and
	// connection strings with embedded credentials — plus whatever runtime.redact.patterns adds).
	Rule string
}
