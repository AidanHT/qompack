package symbols

import (
	"bytes"
	"regexp"
	"strings"
)

// The dialect table of 00-ARCHITECTURE.md §5.22b.
//
// §5.22b specifies a *heuristic, parser-free* extractor: one ordered list of anchored regexes per
// language family, plus a block style that says how to find where a declaration ends. That choice
// is deliberate and load-bearing. symbols has four consumers across three waves — store.Query.
// Symbol (SP-06), dag's shared-symbol edges (SP-07), the analyzer's Δ proxy (SP-15) and the mcp
// minimal-sufficient-span widener (SP-13) — and none of them can afford a real parser per language
// on the hot path, nor a build-time dependency on ten grammars. The price of that is stated
// plainly: these rules find *most* declarations in *most* files, and they never claim more.
//
// Two invariants hold across every rule below, and callers depend on both:
//
//   - Kind is always one of func|type|class|const|var. §5.22b fixes that vocabulary; store's
//     symbol index and dag's edge builder switch on it.
//   - A rule matches against one line at a time, with any trailing CR already removed, and the
//     first rule in the list that matches claims the line. Ordering is therefore semantic: the
//     TypeScript arrow-function rule must precede the plain const rule or every arrow function
//     would be recorded as a const, and the JVM class rule must precede the method rule or
//     `public static class Inner` would be recorded as a method.
//
// RE2 has no lookahead and no backreferences, which is why several rules below are written as
// leading-alternation "modifier soup" (`(?:public|private|...|\s)*`) rather than as the negative
// lookahead the same rule would use in PCRE.

// blockStyle says how a dialect's declarations end, which is the only thing that decides a
// Symbol's Len (00-ARCHITECTURE.md §5.22b, "Span computation").
type blockStyle int

const (
	// blockBraces matches { … } from the declaration, ignoring braces inside strings, character
	// literals and comments.
	blockBraces blockStyle = iota
	// blockIndent runs to the first later line indented no further than the declaration itself.
	blockIndent
	// blockLine is the declaration line alone, newline exclusive. It is also what a blockBraces
	// dialect falls back to when a declaration has no block of its own.
	blockLine
	// blockAuto picks braces when an unquoted brace is within the lookahead window and indent
	// otherwise. Only the generic dialect uses it, because only the generic dialect is handed
	// files whose syntax family is unknown.
	blockAuto
)

// commentSyntax is the set of line-comment introducers a dialect uses, as a bit set. It is a bit
// set rather than a []string because the brace scanner consults it once per significant byte over
// every source symbols ever sees, and a bit test is free where a slice walk is not.
type commentSyntax uint8

const (
	// commentSlash is `//`.
	commentSlash commentSyntax = 1 << 0
	// commentHash is `#`.
	commentHash commentSyntax = 1 << 1
)

// singleQuoteStyle says what a `'` opens in a dialect. The distinction is not pedantry: Rust
// lifetimes (`&'a str`) and Ruby/shell string literals are both extremely common, and treating one
// as the other makes the brace scanner swallow the rest of the file.
type singleQuoteStyle int

const (
	// sqChar means `'` opens a character literal — a short, single-character-or-escape token. An
	// apostrophe that does not look like one (a Rust lifetime) is treated as ordinary code.
	sqChar singleQuoteStyle = iota
	// sqString means `'` opens a string literal that runs to the next unescaped `'` on the line.
	sqString
)

// declRule is one anchored declaration pattern: the regex, which submatch holds the name, the
// §5.22b Kind it produces, and the cheap pre-filters that keep the regex from running at all.
type declRule struct {
	// re is the anchored pattern, matched against a single CR-stripped line.
	re *regexp.Regexp
	// group is the 1-based submatch index holding the symbol name.
	group int
	// kind is the §5.22b Kind this rule produces: func|type|class|const|var.
	kind string
	// hints is an any-of set of literal substrings that must appear in the line for re to have any
	// chance of matching. It is a pure optimization and never changes which lines match — every
	// hint is a literal that the corresponding regex requires — but it is the difference between
	// ~30 regex executions per line and ~1. Extract runs over every byte a tool ever produced, so
	// this is the hot path. An empty hints list means "always run re".
	hints [][]byte
	// col0 requires the line to begin with a non-whitespace byte, mirroring a regex anchored at
	// `^` with a non-`\s` first element. It is the single most valuable filter in the package: in
	// any brace language most lines are indented statements, and col0 rules out every top-level
	// declaration rule for all of them with one byte comparison.
	col0 bool
	// trimIndent requires at least two leading whitespace bytes and matches re against the line
	// with that whitespace removed. It replaces a literal `^\s{2,}` prefix on the pattern, and it
	// is a pure equivalence rather than an approximation: what follows the prefix in such a rule
	// can never itself match whitespace, so the greedy `\s{2,}` has exactly one viable split —
	// "consume all of it" — and pre-trimming reaches the same answer.
	//
	// It exists because the *unviable* splits are not free. Given six spaces of indentation the
	// backtracker tries five of them, re-running the whole modifier alternation each time; the
	// class-method rule is the one rule in the table that runs against most lines of a real
	// TypeScript file, and profiling showed those retries as the single largest cost in Extract.
	trimIndent bool
	// deny rejects a match whose captured name is in the set. Only the TypeScript/JavaScript
	// class-method rule uses it: `  if (x) {` is indistinguishable from `  method(x) {` by shape
	// alone, so the control-flow keywords have to be excluded by name.
	deny map[string]bool
}

// dialect is one language family's complete extraction recipe.
type dialect struct {
	// rules is the ordered rule list; the first match on a line wins.
	rules []declRule
	// style is how a matched declaration's span is computed.
	style blockStyle
	// comments are the line-comment introducers the brace scanner skips and the indent terminator
	// ignores.
	comments commentSyntax
	// blockComment enables /* … */ skipping.
	blockComment bool
	// singleQuote says what `'` opens.
	singleQuote singleQuoteStyle
	// goBlocks enables Go's `var (` / `const (` grouped-declaration state machine, whose members
	// are not matchable by any line-anchored rule.
	goBlocks bool
	// rubyEnd extends an indent span through a terminating `end` line, which in Ruby belongs to
	// the block it closes rather than to whatever follows.
	rubyEnd bool
}

// tabWidth is how many columns a tab counts for when comparing indentation depth
// (00-ARCHITECTURE.md §5.22b: "Leading-whitespace width counts a tab as 4 columns").
const tabWidth = 4

// braceLookaheadLines is how far past a declaration line the brace scanner will look for an
// opening brace before giving up and declaring the declaration block-less (§5.22b). Four lines is
// enough for Allman bracing and for a multi-line parameter list, and short enough that a one-line
// `#define` or const does not adopt the next declaration's body.
const braceLookaheadLines = 4

// charLiteralMaxBytes bounds how far past a `'` the scanner will look for the closing quote of a
// character literal. `'\u{1F600}'` is the longest form any supported dialect has.
const charLiteralMaxBytes = 12

// Go. The type rule tolerates leading whitespace on purpose: a local `type Inner struct` inside a
// function body is a real declaration with a real span, and symbolstest's own nesting fixture
// depends on Extract finding it. func/var/const stay anchored at column zero because an indented
// `var` or `const` inside a function body is a statement, not a declaration worth indexing, and
// because grouped `var (`/`const (` members are handled by the block state machine instead.
var (
	goFuncRe        = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)`)
	goTypeRe        = regexp.MustCompile(`^\s*type\s+([A-Za-z_]\w*)`)
	goConstRe       = regexp.MustCompile(`^const\s+([A-Za-z_]\w*)`)
	goVarRe         = regexp.MustCompile(`^var\s+([A-Za-z_]\w*)`)
	goBlockOpenRe   = regexp.MustCompile(`^(var|const)\s*\(\s*$`)
	goBlockMemberRe = regexp.MustCompile(`^\t([A-Za-z_]\w*)`)
)

// TypeScript / JavaScript. `$` is an identifier byte, which is why every name class here is
// [A-Za-z_$][\w$]* rather than \w+.
var (
	tsFuncRe   = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][\w$]*)`)
	tsClassRe  = regexp.MustCompile(`^(?:export\s+)?(?:abstract\s+)?class\s+([A-Za-z_$][\w$]*)`)
	tsTypeRe   = regexp.MustCompile(`^(?:export\s+)?(?:interface|type|enum)\s+([A-Za-z_$][\w$]*)`)
	tsArrowRe  = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_$][\w$]*)\s*=>`)
	tsConstRe  = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)`)
	tsMethodRe = regexp.MustCompile(`^(?:(?:public|private|protected|static|async|readonly|get|set)\s+)*([A-Za-z_$][\w$]*)\s*\(`)
)

// tsMethodDeny is the keyword deny-set for the class-method rule. `  if (x) {` has exactly the
// shape of a method declaration, so the control-flow keywords are excluded by name — the one place
// in this package where a rule's decision depends on the captured text.
var tsMethodDeny = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"return": true, "do": true, "else": true, "try": true, "function": true,
}

// Python. Both the def and class rules capture the indentation in group 1 and the name in group 2,
// so the group index is 2 rather than the 1 every other dialect uses.
var (
	pyDefRe   = regexp.MustCompile(`^(\s*)def\s+([A-Za-z_]\w*)`)
	pyClassRe = regexp.MustCompile(`^(\s*)class\s+([A-Za-z_]\w*)`)
	pyConstRe = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)\s*=`)
	pyVarRe   = regexp.MustCompile(`^([a-z_]\w*)\s*=`)
)

// Rust. The impl rule captures the *implementing type*, not the trait: in `impl Fetcher for
// Client` the span belongs to Client, which is what dag's shared-symbol edges want to key on.
var (
	rustFnRe    = regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?(?:unsafe\s+)?fn\s+(\w+)`)
	rustTypeRe  = regexp.MustCompile(`^\s*(?:pub\s+)?(?:struct|enum|trait|type|union)\s+(\w+)`)
	rustConstRe = regexp.MustCompile(`^\s*(?:pub\s+)?(?:const|static)\s+(\w+)`)
	rustImplRe  = regexp.MustCompile(`^\s*impl(?:<[^>]*>)?\s+(?:[\w:<>, ]+\s+for\s+)?([\w:]+)`)
)

// JVM-family (Java, Kotlin, Scala, C#, Swift). One rule list covers all five because their
// declaration *shapes* coincide even where their keywords do not: a modifier run, then either a
// type-introducing keyword, or `fun`/`func`, or a return type followed by a parenthesized
// parameter list.
var (
	jvmClassRe  = regexp.MustCompile(`^\s*(?:@\w+(?:\([^)]*\))?\s*)*(?:public|private|protected|internal|open|final|static|abstract|override|suspend|sealed|data|partial|\s)*(?:class|interface|enum|struct|object|record|protocol|actor)\s+(\w+)`)
	jvmFunRe    = regexp.MustCompile(`^\s*(?:@\w+\s*)*(?:public|private|protected|internal|open|final|static|override|suspend|async|\s)*(?:fun|func)\s+(\w+)`)
	jvmMethodRe = regexp.MustCompile(`^\s*(?:(?:public|private|protected|internal|static|final|override|virtual|async)\s+)+[\w<>\[\],.?]+\s+(\w+)\s*\([^;]*\)\s*\{?\s*$`)
	jvmConstRe  = regexp.MustCompile(`^\s*(?:public\s+)?(?:static\s+)?(?:final|const|val)\s+[\w<>\[\].]+\s+(\w+)\s*=`)
)

// C family. The function rule's trailing `\s*\{?\s*$` is what separates a definition from a
// declaration: a prototype ends in `;`, which `[^;]*` refuses to cross.
var (
	cTypeRe   = regexp.MustCompile(`^\s*(?:typedef\s+)?(?:struct|enum|union|class|namespace)\s+(\w+)`)
	cDefineRe = regexp.MustCompile(`^#define\s+(\w+)`)
	cFuncRe   = regexp.MustCompile(`^[A-Za-z_][\w \t\*&:<>,]*?\b(\w+)\s*\([^;]*\)\s*\{?\s*$`)
)

// Ruby. Method names may end in `?`, `!` or `=` and may be operator forms like `[]`, so the name
// class is wider here than anywhere else.
var (
	rubyDefRe   = regexp.MustCompile(`^\s*def\s+([\w.?!=\[\]]+)`)
	rubyClassRe = regexp.MustCompile(`^\s*(?:class|module)\s+([\w:]+)`)
	rubyConstRe = regexp.MustCompile(`^\s*([A-Z][A-Z0-9_]*)\s*=`)
)

// Shell (sh, bash, zsh, PowerShell).
var (
	shFuncParenRe = regexp.MustCompile(`^\s*(?:function\s+)?([\w.-]+)\s*\(\)\s*\{`)
	shFuncKwRe    = regexp.MustCompile(`^\s*function\s+([\w.-]+)\s*\{`)
	shConstRe     = regexp.MustCompile(`^([A-Z_][A-Z0-9_]*)=`)
)

// PHP.
var (
	phpFuncRe  = regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*function\s+&?(\w+)`)
	phpClassRe = regexp.MustCompile(`^\s*(?:abstract\s+|final\s+)?(?:class|interface|trait|enum)\s+(\w+)`)
	phpConstRe = regexp.MustCompile(`^\s*const\s+(\w+)`)
)

// hints turns literal substrings into the byte slices declRule.hints holds, so the conversion
// happens once at package initialization rather than once per line.
func hints(lits ...string) [][]byte {
	out := make([][]byte, 0, len(lits))
	for _, l := range lits {
		out = append(out, []byte(l))
	}
	return out
}

// Dialect definitions. Each is a package-level value so its rule slice and hint byte slices are
// built exactly once, at initialization, and shared by every Extract call.
var (
	goDialect = dialect{
		rules: []declRule{
			{re: goFuncRe, group: 1, kind: "func", hints: hints("func"), col0: true},
			{re: goTypeRe, group: 1, kind: "type", hints: hints("type")},
			{re: goConstRe, group: 1, kind: "const", hints: hints("const"), col0: true},
			{re: goVarRe, group: 1, kind: "var", hints: hints("var"), col0: true},
		},
		style:        blockBraces,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqChar,
		goBlocks:     true,
	}

	tsjsDialect = dialect{
		rules: []declRule{
			{re: tsFuncRe, group: 1, kind: "func", hints: hints("function"), col0: true},
			{re: tsClassRe, group: 1, kind: "class", hints: hints("class"), col0: true},
			{re: tsTypeRe, group: 1, kind: "type", hints: hints("interface", "type", "enum"), col0: true},
			{re: tsArrowRe, group: 1, kind: "func", hints: hints("=>"), col0: true},
			{re: tsConstRe, group: 1, kind: "const", hints: hints("const", "let", "var"), col0: true},
			{re: tsMethodRe, group: 1, kind: "func", hints: hints("("), trimIndent: true, deny: tsMethodDeny},
		},
		style:        blockBraces,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqString,
	}

	pythonDialect = dialect{
		rules: []declRule{
			{re: pyDefRe, group: 2, kind: "func", hints: hints("def")},
			{re: pyClassRe, group: 2, kind: "class", hints: hints("class")},
			{re: pyConstRe, group: 1, kind: "const", hints: hints("="), col0: true},
			{re: pyVarRe, group: 1, kind: "var", hints: hints("="), col0: true},
		},
		style:       blockIndent,
		comments:    commentHash,
		singleQuote: sqString,
	}

	rustDialect = dialect{
		rules: []declRule{
			{re: rustFnRe, group: 1, kind: "func", hints: hints("fn")},
			{re: rustTypeRe, group: 1, kind: "type", hints: hints("struct", "enum", "trait", "type", "union")},
			{re: rustConstRe, group: 1, kind: "const", hints: hints("const", "static")},
			{re: rustImplRe, group: 1, kind: "type", hints: hints("impl")},
		},
		style:        blockBraces,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqChar,
	}

	jvmDialect = dialect{
		rules: []declRule{
			{re: jvmClassRe, group: 1, kind: "class", hints: hints("class", "interface", "enum", "struct", "object", "record", "protocol", "actor")},
			{re: jvmFunRe, group: 1, kind: "func", hints: hints("fun", "func")},
			{re: jvmMethodRe, group: 1, kind: "func", hints: hints("(")},
			{re: jvmConstRe, group: 1, kind: "const", hints: hints("final", "const", "val")},
		},
		style:        blockBraces,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqChar,
	}

	cfamilyDialect = dialect{
		rules: []declRule{
			{re: cTypeRe, group: 1, kind: "type", hints: hints("struct", "enum", "union", "class", "namespace")},
			{re: cDefineRe, group: 1, kind: "const", hints: hints("#define"), col0: true},
			{re: cFuncRe, group: 1, kind: "func", hints: hints("("), col0: true},
		},
		style:        blockBraces,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqChar,
	}

	rubyDialect = dialect{
		rules: []declRule{
			{re: rubyDefRe, group: 1, kind: "func", hints: hints("def")},
			{re: rubyClassRe, group: 1, kind: "class", hints: hints("class", "module")},
			{re: rubyConstRe, group: 1, kind: "const", hints: hints("=")},
		},
		style:       blockIndent,
		comments:    commentHash,
		singleQuote: sqString,
		rubyEnd:     true,
	}

	shellDialect = dialect{
		rules: []declRule{
			{re: shFuncParenRe, group: 1, kind: "func", hints: hints("()")},
			{re: shFuncKwRe, group: 1, kind: "func", hints: hints("function")},
			{re: shConstRe, group: 1, kind: "const", hints: hints("="), col0: true},
		},
		style:       blockBraces,
		comments:    commentHash,
		singleQuote: sqString,
	}

	phpDialect = dialect{
		rules: []declRule{
			{re: phpFuncRe, group: 1, kind: "func", hints: hints("function")},
			{re: phpClassRe, group: 1, kind: "class", hints: hints("class", "interface", "trait", "enum")},
			{re: phpConstRe, group: 1, kind: "const", hints: hints("const")},
		},
		style:        blockBraces,
		comments:     commentSlash | commentHash,
		blockComment: true,
		singleQuote:  sqString,
	}

	// noneDialect has no rules at all. Markdown, JSON, YAML, TOML, plain text, lock files and CSV
	// carry no declarations worth indexing, and running ten regexes per line over a 40 MB lock file
	// to prove it is exactly the waste §5.22b's dialect table exists to avoid.
	noneDialect = dialect{style: blockLine}

	// genericDialect is the fallback for every extension not named above: the TypeScript function,
	// class and arrow rules plus the C-family function rule, which between them cover the brace-
	// and-parenthesis declaration shape almost every curly-brace language shares. Its block style
	// is chosen per declaration rather than per dialect, because a generic file's syntax family is
	// by definition unknown.
	genericDialect = dialect{
		rules: []declRule{
			{re: tsFuncRe, group: 1, kind: "func", hints: hints("function"), col0: true},
			{re: tsClassRe, group: 1, kind: "class", hints: hints("class"), col0: true},
			{re: tsArrowRe, group: 1, kind: "func", hints: hints("=>"), col0: true},
			{re: cFuncRe, group: 1, kind: "func", hints: hints("("), col0: true},
		},
		style:        blockAuto,
		comments:     commentSlash,
		blockComment: true,
		singleQuote:  sqString,
	}
)

// dialectsByExt maps a lowercased file extension (leading dot included) to its dialect. Anything
// absent resolves to genericDialect.
var dialectsByExt = map[string]*dialect{
	".go": &goDialect,

	".ts": &tsjsDialect, ".tsx": &tsjsDialect, ".js": &tsjsDialect,
	".jsx": &tsjsDialect, ".mjs": &tsjsDialect, ".cjs": &tsjsDialect,

	".py": &pythonDialect, ".pyi": &pythonDialect,

	".rs": &rustDialect,

	".java": &jvmDialect, ".kt": &jvmDialect, ".kts": &jvmDialect,
	".scala": &jvmDialect, ".cs": &jvmDialect, ".swift": &jvmDialect,

	".c": &cfamilyDialect, ".h": &cfamilyDialect, ".cc": &cfamilyDialect,
	".cpp": &cfamilyDialect, ".hpp": &cfamilyDialect, ".cxx": &cfamilyDialect,
	".m": &cfamilyDialect, ".mm": &cfamilyDialect,

	".rb": &rubyDialect,

	".sh": &shellDialect, ".bash": &shellDialect, ".zsh": &shellDialect, ".ps1": &shellDialect,

	".php": &phpDialect,

	".md": &noneDialect, ".json": &noneDialect, ".yaml": &noneDialect, ".yml": &noneDialect,
	".toml": &noneDialect, ".txt": &noneDialect, ".lock": &noneDialect, ".csv": &noneDialect,
}

// dialectFor resolves a path to its dialect. Only the extension is read; nothing is opened, and
// the path may name a file that does not exist (§5.22b: path is "used only to select a
// language-specific heuristic").
func dialectFor(path string) *dialect {
	if d, ok := dialectsByExt[extOf(path)]; ok {
		return d
	}
	return &genericDialect
}

// extOf returns path's lowercased extension including the leading dot, or "" when the final path
// element has none. It splits on both separators regardless of GOOS, because the paths symbols is
// handed come from tool output and MCP requests, not from the local filesystem, and a Windows path
// must resolve to the same dialect on a Linux daemon as it does on a Windows one.
func extOf(path string) string {
	p := strings.ToLower(path)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i:]
	}
	return ""
}

// minIndentBytes is how many leading whitespace bytes a trimIndent rule requires, mirroring the
// `{2,}` of the `^\s{2,}` prefix it replaces.
const minIndentBytes = 2

// match runs r against one line and returns the captured symbol name, reporting false when the
// line is not a declaration this rule recognizes — including when it has a declaration's shape but
// a denied name. It is the only place a declRule is ever evaluated, so the pre-filter, the optional
// indent trim, the deny-set and the submatch extraction cannot drift apart.
//
// Only the captured *text* is returned, never its position: a Symbol's Offset is its declaration
// line's start (§5.22b), so trimming the subject before matching cannot disturb any offset a
// caller ever sees.
func (r *declRule) match(line []byte) (string, bool) {
	if !r.mayMatch(line) {
		return "", false
	}
	subject := line
	if r.trimIndent {
		subject = line[leadingWhitespaceBytes(line):]
	}
	// Deny check, cheap half: a denied keyword at the very start of the subject settles the line
	// without running the engine. This is exact, not approximate. A deny-set entry is never one of
	// the rule's own modifier keywords, so the modifier group must match empty, so the name group
	// starts at offset zero; and because the name pattern is a maximal identifier run followed by
	// `(`, the engine can only ever capture the whole run — never a prefix of it, since an
	// identifier byte cannot be the `(`. So if the leading run is denied, the regex either captures
	// exactly it (denied) or does not match at all. Both outcomes are "no symbol".
	if r.deny != nil && r.deny[string(leadingIdentifier(subject))] {
		return "", false
	}
	m := r.re.FindSubmatchIndex(subject)
	if m == nil {
		return "", false
	}
	lo, hi := 2*r.group, 2*r.group+1
	if hi >= len(m) || m[lo] < 0 {
		return "", false
	}
	name := string(subject[m[lo]:m[hi]])
	// Deny check, general half: the name may sit behind a run of modifiers ("public if (x) {"),
	// which only the engine can strip.
	if r.deny[name] {
		return "", false
	}
	return name, true
}

// mayMatch is the cheap pre-filter that decides whether r.re runs against line at all.
//
// It is conservative in exactly one direction: it may say yes where the regex would not match, but
// it never says no where the regex would match. Every hint is a literal the corresponding regex
// requires, `col0` mirrors a `^`-anchored pattern whose first element cannot match whitespace, and
// `trimIndent` mirrors an `^\s{2,}` prefix. Getting that direction wrong would silently drop
// declarations, which is why the filters are derived from the patterns rather than guessed.
//
// The order is deliberate: two byte comparisons before any substring search, and any substring
// search before the regex engine. Profiling a 100 KB TypeScript file showed the regex engine and
// the hint search together dominating Extract until col0 removed both for the ~75% of lines in a
// brace language that are indented statements.
func (r *declRule) mayMatch(line []byte) bool {
	if len(line) == 0 {
		return false
	}
	if r.col0 && isRegexpSpace(line[0]) {
		return false
	}
	if r.trimIndent && leadingWhitespaceBytes(line) < minIndentBytes {
		return false
	}
	if len(r.hints) == 0 {
		return true
	}
	for _, h := range r.hints {
		if bytes.Contains(line, h) {
			return true
		}
	}
	return false
}

// isLineComment reports whether line, already trimmed of leading whitespace, opens with one of d's
// line-comment introducers. It is what keeps a comment line from terminating an indent span.
func (d *dialect) isLineComment(trimmed []byte) bool {
	if len(trimmed) == 0 {
		return false
	}
	if d.comments&commentHash != 0 && trimmed[0] == '#' {
		return true
	}
	return d.comments&commentSlash != 0 && len(trimmed) >= 2 && trimmed[0] == '/' && trimmed[1] == '/'
}
