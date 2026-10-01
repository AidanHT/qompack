package hostperm

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// Effect is what the host's rules say about reading one path.
type Effect int

const (
	// Allow means no deny or ask rule this package can see matches the path. It is not a statement
	// that the host would allow the read; see the package comment.
	Allow Effect = iota
	// Ask means an ask rule matches and no deny rule does. The host would prompt; an archived
	// retrieval cannot, so a caller treats Ask as a refusal with its own reason.
	Ask
	// Deny means a deny rule matches.
	Deny
)

// String names the effect for diagnostics.
func (e Effect) String() string {
	switch e {
	case Deny:
		return "deny"
	case Ask:
		return "ask"
	default:
		return "allow"
	}
}

// Decision is the verdict for one path.
type Decision struct {
	Effect Effect
	// Rule is the matching entry as written and Source the file or registry value it came from.
	// Both are for local diagnostics only: a retrieval refusal must never echo them, because a rule
	// spells the very path it protects.
	Rule   string
	Source string
}

// ErrUnavailable wraps every reason the host's rules could not be established: a settings source
// that exists but cannot be read, parsed or decoded. A caller must fail closed on it for any
// content that has a path.
var ErrUnavailable = errors.New("host permission policy unavailable")

// errEmptyPath is Check's answer for an empty path: there is nothing to evaluate.
var errEmptyPath = errors.New("hostperm: empty path")

// racyWindow is how recently a settings file may have changed before its stat signature stops
// being trusted to stand for its content. A file rewritten twice inside one timestamp tick with the
// same size would otherwise keep a stale rule set; within the window the file is re-read instead.
// This is git's "racily clean" rule, for the same reason.
const racyWindow = 2 * time.Second

// Options configures a Policy. The zero value of every field but ProjectRoot means "this machine's
// real environment".
type Options struct {
	// ProjectRoot is the project the archive belongs to. It stands in for the session's primary
	// working directory, which anchors `path`, `./path` and project/local `/path` rules.
	ProjectRoot string
	// Home is the user's home directory, the anchor of `~/path`. Empty means os.UserHomeDir.
	Home string
	// Getenv reads CLAUDE_CONFIG_DIR and the variables a registry value may expand. Nil means
	// os.Getenv.
	Getenv func(string) string
	// Managed names the managed-policy sources. Nil means DefaultManaged for this platform.
	Managed *ManagedSources
	// Clock stamps the racy-window check. Nil means the system clock.
	Clock core.Clock

	// goos overrides runtime.GOOS for path-shape semantics, so a test can exercise Windows rules
	// on any platform. Link resolution is skipped whenever it differs from the real platform.
	goos string
}

// Policy loads and caches the rule set. It is safe for concurrent use, and every Snapshot re-checks
// every source's signature, so an edit to any settings file is seen by the next request.
type Policy struct {
	o    Options
	goos string
	fold bool

	mu    sync.Mutex
	sigs  []signature
	rules *RuleSet
	err   error
	racy  bool
	loads int
}

// New builds a Policy. It reads nothing until the first Snapshot.
func New(o Options) *Policy {
	goos := o.goos
	if goos == "" {
		goos = runtime.GOOS
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}
	return &Policy{o: o, goos: goos, fold: goos == "windows" || goos == "darwin"}
}

// Check evaluates one absolute path against the current rules.
func (p *Policy) Check(path string) (Decision, error) {
	if path == "" {
		return Decision{}, errEmptyPath
	}
	rs, err := p.Snapshot()
	if err != nil {
		return Decision{}, err
	}
	return rs.Evaluate(path), nil
}

// Snapshot returns the rule set as it stands now.
//
// Its cost is one stat per settings file (plus one registry query per policy value on Windows)
// when nothing changed, and a re-read of every source when anything did. A parse failure is cached
// against the signatures that produced it, so a broken file is not re-parsed on every request; a
// read failure is not cached, because it may be a transient sharing violation.
func (p *Policy) Snapshot() (*RuleSet, error) {
	srcs, err := p.sources()
	if err != nil {
		return nil, err
	}
	sigs := make([]signature, 0, len(srcs))
	for i := range srcs {
		sigs = append(sigs, p.sign(&srcs[i]))
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.loads > 0 && !p.racy && sameSignatures(p.sigs, sigs) {
		return p.rules, p.err
	}
	rules, racy, err := p.build(srcs, sigs)
	var readErr *transientError
	if errors.As(err, &readErr) {
		return nil, err
	}
	p.loads++
	p.sigs, p.rules, p.err, p.racy = sigs, rules, err, racy
	return rules, err
}

// build reads and parses every source whose signature says it is present.
func (p *Policy) build(srcs []source, sigs []signature) (*RuleSet, bool, error) {
	now := p.o.Clock.Now()
	racy := false
	var lists []ruleList
	compiled, segments := 0, 0
	for i := range srcs {
		s, sig := &srcs[i], sigs[i]
		switch {
		case sig.errText != "":
			return nil, false, &transientError{source: s.id, text: sig.errText}
		case !sig.present:
			continue
		case s.opaque:
			return nil, false, sourceError(s.id, "a managed policy in a format this build cannot read is present")
		}
		if sig.mod != 0 && now.Sub(time.Unix(0, sig.mod)) < racyWindow {
			racy = true
		}
		data, err := p.read(s)
		if err != nil {
			return nil, false, err
		}
		got, err := parseSettings(data, s, p.goos, p.fold)
		if err != nil {
			return nil, false, err
		}
		for _, l := range got {
			compiled += len(l.patterns)
			for _, pt := range l.patterns {
				segments += len(pt.segs)
			}
		}
		if compiled > maxReadPatterns || segments > maxReadSegments {
			return nil, false, sourceError(s.id, fmt.Sprintf("the Read path rules in force across the "+
				"settings sources exceed %d patterns or %d path segments", maxReadPatterns, maxReadSegments))
		}
		lists = append(lists, got...)
	}
	rs := p.newRuleSet(lists)
	return rs, racy, nil
}

// transientError is a read failure: reported, never cached.
type transientError struct {
	source string
	text   string
}

func (e *transientError) Error() string {
	return ErrUnavailable.Error() + ": " + e.source + ": " + e.text
}

func (e *transientError) Unwrap() error { return ErrUnavailable }

// sourceError reports a source that exists but cannot be used.
func sourceError(source, text string) error {
	return &parseError{source: source, text: text}
}

// parseError is a failure that follows from a source's content, so it is cached until the source
// changes.
type parseError struct {
	source string
	text   string
}

func (e *parseError) Error() string {
	return ErrUnavailable.Error() + ": " + e.source + ": " + e.text
}

func (e *parseError) Unwrap() error { return ErrUnavailable }

// RuleSet is one immutable snapshot of the host's Read rules.
type RuleSet struct {
	goos    string
	fold    bool
	resolve bool
	deny    []ruleList
	ask     []ruleList
	// shortPending reports that a rule names an 8.3 name its compile could not expand to a real
	// name: after a glob, in a literal rule, or naming an entry that does not exist. Evaluate then
	// also judges each path segment by its own 8.3 name (shortSpellings).
	shortPending bool
}

// Empty reports whether no Read deny or ask rule is in force, in which case Evaluate touches no
// file at all.
func (rs *RuleSet) Empty() bool {
	return rs == nil || (len(rs.deny) == 0 && len(rs.ask) == 0)
}

// Evaluate reports what the rules say about reading the absolute path abs.
//
// Every spelling that opens the same file is checked, and a match on any one is enough: the path
// as written, the name the operating system opens for it (osAlias: on Windows its trailing-dot,
// stream and 8.3 aliases) and the path each of those resolves to through links. The host checks a
// symlink and its target, and a deny rule applies when either matches.
// The path itself is treated as possibly being a directory, so a directory-only rule (`secrets/`)
// also matches a plain file of that name — a refusal the host might not make, taken because an
// archived path's kind at capture time is not recorded.
//
// A path with an 8.3-shaped segment that names nothing on disk (a deleted file's short name) has
// no long name to judge, and a rule on that long name would be walked past, so with any rule in
// force it is refused outright (Rule unresolvedShortName), as unreadable settings are.
//
// A rule that names an 8.3 name it could not be expanded at (shortPending) is matched against each
// path segment's 8.3 name as well as its own. Only an existing path has 8.3 names to compare, so
// while such a rule is in force a path that does not exist is refused (Rule unknownShortName).
// Both refusals answer Deny whichever lists hold the rules, since the rule the unknown name would
// match cannot be told.
func (rs *RuleSet) Evaluate(abs string) Decision {
	if rs.Empty() || abs == "" {
		return Decision{Effect: Allow}
	}
	cands, natives, unresolved := spellings(abs, rs.goos, rs.fold, rs.resolve, nil)
	if unresolved {
		return Decision{Effect: Deny, Rule: unresolvedShortName}
	}
	var alts [][]string
	if rs.shortPending {
		if alts = shortSpellings(cands, natives, rs.goos, rs.fold); alts == nil {
			return Decision{Effect: Deny, Rule: unknownShortName}
		}
	}
	var sc scratch
	for _, l := range rs.deny {
		if rule, ok := l.match(cands, alts, &sc); ok {
			return Decision{Effect: Deny, Rule: rule, Source: l.source}
		}
	}
	for _, l := range rs.ask {
		if rule, ok := l.match(cands, alts, &sc); ok {
			return Decision{Effect: Ask, Rule: rule, Source: l.source}
		}
	}
	return Decision{Effect: Allow}
}

// spellings returns every distinct spelling of the absolute path p as POSIX segments: p itself
// and, when onDisk is set, the name the operating system opens for it and where each of those
// resolves through links. onDisk is false only when a test evaluates another platform's paths.
// memo may be nil; see linkMemo. natives holds each spelling as this platform names it, in the
// same order. unresolved is osAlias's: p holds an 8.3 name it cannot expand.
func spellings(p, goos string, fold, onDisk bool, memo linkMemo) (out [][]string, natives []string,
	unresolved bool,
) {
	out, natives = [][]string{posixSegments(p, goos, fold)}, []string{p}
	if !onDisk {
		return out, natives, false
	}
	a, unresolved := osAlias(p)
	if a != "" {
		var added bool
		if out, added = appendDistinct(out, posixSegments(a, goos, fold)); added {
			natives = append(natives, a)
		}
	}
	// Each spelling found so far is resolved through links; a resolution is not resolved again.
	for _, n := range natives[:len(natives):len(natives)] {
		if r, ok := resolveLinks(n, memo); ok {
			var added bool
			if out, added = appendDistinct(out, posixSegments(r, goos, fold)); added {
				natives = append(natives, r)
			}
		}
	}
	return out, natives, unresolved
}

// shortSpellings returns, for each spelling in cands, the same path with every segment in its 8.3
// spelling (osShortName), aligned segment for segment, or nil where that is not known. It returns
// nil when it is known for none: the path does not exist, so its own 8.3 names cannot be read.
func shortSpellings(cands [][]string, natives []string, goos string, fold bool) [][]string {
	alts := make([][]string, len(cands))
	known := false
	for i, c := range cands {
		s, complete := osShortName(natives[i])
		if !complete {
			continue
		}
		if segs := posixSegments(s, goos, fold); len(segs) == len(c) {
			alts[i], known = segs, true
		}
	}
	if !known {
		return nil
	}
	return alts
}

// unresolvedShortName is the Decision.Rule of Evaluate's refusal of an 8.3 name it cannot expand.
// Like every Rule it is for local diagnostics only.
const unresolvedShortName = "(an 8.3 name that names nothing on disk, so its long name is unknown)"

// unknownShortName is the Decision.Rule of Evaluate's refusal, under a rule naming an 8.3 name it
// could not be expanded at, of a path that does not exist and so has no 8.3 names to compare.
const unknownShortName = "(a rule names an 8.3 name, and a path that does not exist has none to compare)"

// shortNameRe matches a segment that may be, or as a glob may name, a generated 8.3 name: a tilde
// followed by a digit, as in CREDEN~1.SEC or the hashed form 5B2E~1, or by a glob metacharacter
// (concretePrefix's `*?[\`) that can stand for one, as in CREDEN~?.SEC. A tilde followed by
// anything else, as in the backup-file rule Read(**/*~), names no 8.3 name. A long name may contain
// the same characters; such a name is only ever refused when it does not exist, see osAlias. A path
// segment is a name, not a glob: `*`, `?` and `\` cannot occur in a Windows file name, and `~[` in
// one only widens that refusal, which fails closed.
var shortNameRe = regexp.MustCompile(`~[0-9*?\[\\]`)

// shortShaped reports whether the segment seg may be an 8.3 name, or as a rule's glob segment may
// name one. It is the one 8.3-shape predicate: a path's unexpandable segment (osAlias), a rule's
// pending 8.3 name (longNameAliases) and the rule segments compared with a path's 8.3 names
// (literalEqual, prefixRow) are all judged by it.
func shortShaped(seg string) bool { return shortNameRe.MatchString(seg) }

// appendDistinct appends segs unless an identical list is already present.
func appendDistinct(all [][]string, segs []string) ([][]string, bool) {
	for _, s := range all {
		if equalSegments(s, segs) {
			return all, false
		}
	}
	return append(all, segs), true
}

// ruleList is one `deny` or `ask` array from one source, in order.
type ruleList struct {
	effect Effect
	source string
	// toolRule is the first tool-level entry (`Read`, `*`) in the list, which matches every read.
	toolRule string
	// patterns are the path entries, in the order written; carve-outs apply to earlier entries.
	patterns []*pattern
	// settingsDirs are the directories a `/path` entry in this list is measured from.
	settingsDirs []string
	// cwdAnchors are the working-directory anchors the carvable group is measured from.
	cwdAnchors [][]string
}

// match reports whether any candidate spelling of the path is refused by this list, and by which
// entry. alts is nil, or holds each candidate's 8.3 spelling (shortSpellings), which a rule
// segment naming an 8.3 name is compared with as well.
func (l *ruleList) match(cands, alts [][]string, sc *scratch) (string, bool) {
	if l.toolRule != "" {
		return l.toolRule, true
	}
	for i, c := range cands {
		var alt []string
		if alts != nil {
			alt = alts[i]
		}
		for _, p := range l.patterns {
			if p.neg || p.carvable {
				continue
			}
			for _, a := range p.anchors {
				if rel, ok := under(c, a); ok {
					sc.alt = tail(alt, len(a))
					if p.matchSelfOrAncestor(rel, sc) {
						return p.raw, true
					}
				}
			}
		}
		for _, a := range l.cwdAnchors {
			if rel, ok := under(c, a); ok {
				sc.alt = tail(alt, len(a))
				if rule, hit := l.carvedMatch(rel, sc); hit {
					return rule, true
				}
			}
		}
	}
	return "", false
}

// tail returns alt below its first n segments, or nil when there is no alt.
func tail(alt []string, n int) []string {
	if alt == nil {
		return nil
	}
	return alt[n:]
}

// carvedMatch applies gitignore's ordering to the carvable group: a directory above rel that the
// group excludes excludes rel outright, and otherwise the last pattern matching rel decides.
//
// For every prefix of rel — each directory above it, and rel itself — the last carvable pattern
// matching exactly that prefix decides its polarity. One pass over the patterns fills in every
// prefix's answer at once, because each pattern's row answers every prefix.
func (l *ruleList) carvedMatch(rel []string, sc *scratch) (string, bool) {
	n := len(rel)
	sc.rules, sc.pos = resize(sc.rules, n+1), resize(sc.pos, n+1)
	// A carve-out is compared with the path's own names only: the 8.3 comparison adds refusals,
	// never reopenings.
	alt := sc.alt
	for _, p := range l.patterns {
		if !p.carvable {
			continue
		}
		sc.alt = alt
		if p.neg {
			sc.alt = nil
		}
		for k, hit := range p.row(rel, sc) {
			if hit {
				sc.rules[k], sc.pos[k] = p.raw, !p.neg
			}
		}
	}
	sc.alt = alt
	for k := 1; k < n; k++ {
		if sc.pos[k] {
			return sc.rules[k], true
		}
	}
	return sc.rules[n], sc.pos[n]
}

// equalSegments reports whether two segment lists are identical.
func equalSegments(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newRuleSet assigns every pattern its anchors and splits the lists by effect.
func (p *Policy) newRuleSet(lists []ruleList) *RuleSet {
	rs := &RuleSet{goos: p.goos, fold: p.fold, resolve: p.goos == runtime.GOOS}
	if len(lists) == 0 {
		return rs
	}
	memo := linkMemo{}
	cwd := p.anchorVariants(p.o.ProjectRoot, memo)
	home := p.anchorVariants(p.home(), memo)
	for i := range lists {
		l := &lists[i]
		var aliases []*pattern
		for _, pt := range l.patterns {
			var base [][]string
			switch pt.kind {
			case anchorFS:
				base = [][]string{{}}
			case anchorHome:
				base = home
			case anchorSettings:
				base = l.settingsAnchors(p, memo)
			default:
				base = cwd
			}
			for _, a := range base {
				pt.anchors = append(pt.anchors, ancestorsUp(a, pt.up))
			}
			if !pt.neg && pt.kind != anchorCwd {
				aliases = append(aliases, p.throughLinks(pt, memo)...)
			}
			if !pt.neg {
				long, pending := p.longNameAliases(pt, memo)
				aliases = append(aliases, long...)
				rs.shortPending = rs.shortPending || pending
			}
		}
		l.patterns = append(l.patterns, aliases...)
		l.cwdAnchors = cwd
		if l.effect == Deny {
			rs.deny = append(rs.deny, *l)
		} else {
			rs.ask = append(rs.ask, *l)
		}
	}
	return rs
}

// settingsAnchors returns the directories a `/path` rule from this list's source is measured from.
func (l *ruleList) settingsAnchors(p *Policy, memo linkMemo) [][]string {
	var out [][]string
	for _, d := range l.settingsDirs {
		out = append(out, p.anchorVariants(d, memo)...)
	}
	return out
}

// anchorVariants returns dir in POSIX segments, plus every other spelling of it that spellings
// finds: a project root or home supplied through 8.3 names measures its rules from its real name
// too, as well as from the location its links resolve to.
func (p *Policy) anchorVariants(dir string, memo linkMemo) [][]string {
	if dir == "" {
		return nil
	}
	out, _, _ := spellings(dir, p.goos, p.fold, p.goos == runtime.GOOS, memo)
	return out
}

// concretePrefix counts pt's leading segments that name a concrete directory or file: the walk
// stops at the first segment that is a glob, which is as far as a rule names a concrete path.
func concretePrefix(pt *pattern) int {
	prefix := 0
	for prefix < len(pt.segs) && !strings.ContainsAny(pt.segs[prefix], `*?[\`) {
		prefix++
	}
	return prefix
}

// longNameAliases returns the aliases of a rule whose concrete prefix is spelled through a name
// Windows opens under another spelling: an 8.3 name (or a trailing dot, space or stream), which
// osAlias expands to the real name. A path is judged by its own spelling and its real name
// (spellings), so a rule spelled short, or mixing short and long segments as one under a short
// TEMP (C:\Users\RUNNER~1\...) does, would match neither; the rule is applied at its real name too.
// Every rule kind is aliased, since the alias only adds refusals; like compileAlias's, an alias is
// never carvable. osAlias is asked only of a prefix that holds such a segment (spelledAsAlias), so
// a rule set without one pays nothing. The real name of a `//`, `~/` or `/` rule is also walked
// through links (memo), as throughLinks walks the rule as written.
//
// pending reports an 8.3-shaped name the rule holds that no alias expands: one after a glob or in a
// literal rule, where no concrete directory names it, or one naming an entry that does not exist.
// Such a rule is matched against each path segment's 8.3 name instead (RuleSet.shortPending).
func (p *Policy) longNameAliases(pt *pattern, memo linkMemo) (out []*pattern, pending bool) {
	if p.goos != runtime.GOOS || runtime.GOOS != "windows" || pt.inert {
		return nil, false
	}
	if pt.literal {
		return nil, slices.ContainsFunc(pt.segs, shortShaped)
	}
	prefix := concretePrefix(pt)
	pending = slices.ContainsFunc(pt.segs[prefix:], shortShaped)
	if prefix == 0 || !spelledAsAlias(pt.segs[:prefix]) {
		return nil, pending
	}
	for _, a := range pt.anchors {
		lit := append(append([]string{}, a...), pt.segs[:prefix]...)
		alias, unresolved := osAlias(nativePath(lit, p.goos))
		pending = pending || unresolved
		if alias == "" {
			continue
		}
		reals := []string{alias}
		if pt.kind != anchorCwd {
			if r, ok := resolveLinks(alias, memo); ok {
				reals = append(reals, r)
			}
		}
		seen := [][]string{lit}
		for _, r := range reals {
			rp := posixSegments(r, p.goos, p.fold)
			var added bool
			if seen, added = appendDistinct(seen, rp); !added {
				continue
			}
			out = append(out, &pattern{
				raw: pt.raw, kind: pt.kind, segs: append([]string{}, pt.segs[prefix:]...),
				anchors: [][]string{rp},
			})
		}
	}
	return out, pending
}

// spelledAsAlias reports whether any segment is one Windows may open under another spelling: an
// 8.3-shaped name (a tilde), or one ending in a dot or a space, or holding a stream's colon.
func spelledAsAlias(segs []string) bool {
	for _, s := range segs {
		if strings.ContainsAny(s, "~:") || strings.HasSuffix(s, ".") || strings.HasSuffix(s, " ") {
			return true
		}
	}
	return false
}

// throughLinks returns the aliases of an anchored rule written through a symlinked directory:
// the host applies such a rule at the directory's real location too. The walk stops at the first
// segment that is a glob, which is as far as a rule names a concrete directory.
func (p *Policy) throughLinks(pt *pattern, memo linkMemo) []*pattern {
	if p.goos != runtime.GOOS || pt.literal {
		return nil
	}
	prefix := concretePrefix(pt)
	if prefix == 0 {
		return nil
	}
	var out []*pattern
	for _, a := range pt.anchors {
		lit := append(append([]string{}, a...), pt.segs[:prefix]...)
		r, ok := resolveLinks(nativePath(lit, p.goos), memo)
		if !ok {
			continue
		}
		rp := posixSegments(r, p.goos, p.fold)
		if equalSegments(rp, lit) {
			continue
		}
		out = append(out, &pattern{
			raw: pt.raw, kind: pt.kind, segs: append([]string{}, pt.segs[prefix:]...),
			anchors: [][]string{rp},
		})
	}
	return out
}

// nativePath turns POSIX segments back into a path this platform can resolve.
func nativePath(segs []string, goos string) string {
	if goos == "windows" && len(segs) > 0 && len(segs[0]) == 1 {
		return strings.ToUpper(segs[0]) + `:\` + strings.Join(segs[1:], `\`)
	}
	return "/" + strings.Join(segs, "/")
}

// home returns the configured or discovered home directory, or "" when there is none.
func (p *Policy) home() string {
	if p.o.Home != "" {
		return p.o.Home
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
