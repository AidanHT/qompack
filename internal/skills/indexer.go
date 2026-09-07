package skills

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// The two discovery shapes, relative to projectRoot, searched in this order:
//
//	.claude/skills/<name>/SKILL.md     → Name from frontmatter `name:`, else <name>
//	.claude/skills/<name>.md           → Name from frontmatter `name:`, else <name>
//
// Only the one level under .claude/skills/ is examined: these are the two layouts Claude Code
// itself discovers, and a deeper walk would index files no skill invocation could ever reach.
const (
	skillsDirSlash = ".claude/skills"
	skillFileName  = "SKILL.md"
	mdExt          = ".md"
)

//nomagic:allow byte ceiling for SKILL.md reads; unrelated to runtime.hotPath.maxPayloadBytes
const maxSkillBytes = 1048576

// maxDescriptionRunes caps a rendered index line's description. It is a display cap on a line the
// model reads, not a budget: the budget is the caller's `budget` argument and nothing else.
const maxDescriptionRunes = 100

// descriptionEllipsis is appended to a description cut at maxDescriptionRunes, so a reader can
// tell a genuinely short description from a clipped one.
const descriptionEllipsis = "…"

// noDescription is the literal stand-in for a skill that declares no description and whose body
// offers no usable first line. An index line with an empty right-hand side reads like a bug; this
// reads like the absence it is.
const noDescription = "(no description)"

// bytesPerTokenEstimate is the 4-bytes-per-token approximation this package costs index lines
// with. skills is foundation-only (00-ARCHITECTURE.md §3.2) and may not import internal/tokens,
// and it does not need to: the index is a handful of short ASCII-ish lines, where the cheap
// estimate and a real tokenizer differ by less than the rounding already applied per line.
const bytesPerTokenEstimate = 4

// indexer is the real skills.Indexer: a pure function of the bytes on disk under
// <root>/.claude/skills, with a logger for the drops it makes silently.
type indexer struct {
	log logging.Logger
}

// Option customises the Indexer New returns.
//
// Option and WithLogger mirror internal/rules exactly, and for the same reason: the shipped
// constructor is `func New() Indexer` and internal/skills/skillstest/suite_test.go calls it
// argument-free from a subpackage that may not import logging. A variadic Option keeps that call
// compiling while still letting a composition root inject a real logger.
type Option func(*indexer)

// WithLogger sets the Logger the Indexer reports skipped and truncated entries through. A nil
// Logger is ignored rather than installed, so a caller that passes one by accident degrades to the
// Nop logger instead of panicking on the first skipped file.
func WithLogger(log logging.Logger) Option {
	return func(ix *indexer) {
		if log != nil {
			ix.log = log
		}
	}
}

// New returns the L5 skill Indexer (00-ARCHITECTURE.md §5.15, Qompack.md §8.6, gap G4.4).
// Constructing it never fails and never touches the filesystem.
func New(opts ...Option) Indexer {
	ix := &indexer{log: logging.Nop()}
	for _, o := range opts {
		o(ix)
	}
	return ix
}

// Index discovers every skill under <root>/.claude/skills and returns the deterministic prefix of
// them that fits budget, together with that prefix's token cost.
//
// The rendered line for an entry is "- " + Name + ": " + Description + "\n" and its cost is
// ceil(len(line)/4) tokens. Entries are sorted by Name ascending (byte order), tiebroken by Source
// ascending, and then added in that order for as long as they fit. Filling STOPS at the first
// entry that does not fit; it never skips ahead to a smaller one. That is what makes the index
// reproducible — the same tree and the same budget always yield the same prefix — and it is what
// lets the rehydrator's drop report say "everything after X was dropped" rather than having to
// enumerate an arbitrary subset. Entries that did not fit are not returned.
//
// A budget of 0 or less means "no budget": Index returns ALL discovered entries and their total
// cost. rehydrate depends on this and calls Index exactly twice per rehydration — once with 0 to
// learn the full set the drop report is computed against, and once with the real
// runtime.rehydrate.skillIndexTokens budget to learn what actually ships. The budget is always a
// caller-supplied parameter; this package never carries a default for it.
//
// Discovery is best-effort per file: a symlink, a file over maxSkillBytes, and an unreadable file
// are each skipped with a Debug line rather than failing the index, because a skill index that
// refuses to build is strictly worse than one missing an entry. A missing .claude/skills directory
// yields no entries and no error.
func (ix *indexer) Index(ctx context.Context, root string, budget core.Tokens) ([]Entry, core.Tokens, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}

	found, err := ix.discover(ctx, root)
	if err != nil {
		return nil, 0, err
	}
	slices.SortFunc(found, func(a, b Entry) int {
		if c := strings.Compare(a.Name, b.Name); c != 0 {
			return c
		}
		return strings.Compare(a.Source, b.Source)
	})

	var (
		kept []Entry
		used core.Tokens
	)
	for _, e := range found {
		cost := lineTokens(e)
		if budget > 0 && used+cost > budget {
			break
		}
		kept = append(kept, e)
		used += cost
	}

	if len(kept) < len(found) {
		ix.log.Debug("skills: index truncated to budget",
			"found", len(found), "kept", len(kept), "used", int(used), "budget", int(budget))
	}
	return kept, used, nil
}

// discover reads the one level under <root>/.claude/skills and returns an unsorted Entry for every
// file matching either discovery shape.
func (ix *indexer) discover(ctx context.Context, root string) ([]Entry, error) {
	dir := filepath.Join(root, filepath.FromSlash(skillsDirSlash))
	dirents, err := os.ReadDir(paths.Long(dir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("skills: read %s: %w", skillsDirSlash, err)
	}

	out := make([]Entry, 0, len(dirents))
	for _, de := range dirents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if de.Type()&fs.ModeSymlink != 0 {
			ix.log.Debug("skills: skipping symlink", "name", de.Name())
			continue
		}
		var rel, fallback string
		switch {
		case de.IsDir():
			rel, fallback = path.Join(skillsDirSlash, de.Name(), skillFileName), de.Name()
		case strings.HasSuffix(de.Name(), mdExt):
			rel, fallback = path.Join(skillsDirSlash, de.Name()), strings.TrimSuffix(de.Name(), mdExt)
		default:
			continue
		}
		if e, ok := ix.load(root, rel, fallback); ok {
			out = append(out, e)
		}
	}
	return out, nil
}

// load reads one candidate skill file and builds its Entry. It reports ok=false for everything
// that is not an indexable skill — an absent file (a skill directory with no SKILL.md), a symlink,
// a non-regular file, a file over maxSkillBytes, or an unreadable one — none of which is fatal to
// the index as a whole. rel is the file's project-relative forward-slash path and becomes
// Entry.Source verbatim; fallback is the name to use when the frontmatter declares none.
func (ix *indexer) load(root, rel, fallback string) (Entry, bool) {
	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Lstat(paths.Long(full))
	switch {
	case err != nil:
		return Entry{}, false
	case info.Mode()&fs.ModeSymlink != 0:
		ix.log.Debug("skills: skipping symlinked skill file", "source", rel)
		return Entry{}, false
	case !info.Mode().IsRegular():
		return Entry{}, false
	case info.Size() > maxSkillBytes:
		ix.log.Debug("skills: skipping oversize skill file", "source", rel, "bytes", info.Size())
		return Entry{}, false
	}

	raw, err := paths.ReadFileShared(full)
	if err != nil {
		ix.log.Debug("skills: skipping unreadable skill file", "source", rel, "err", err)
		return Entry{}, false
	}

	meta, offset := parseFrontmatter(string(raw))
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		name = fallback
	}
	desc := clipDescription(meta.Description)
	if desc == "" {
		desc = clipDescription(firstBodyLine(string(raw)[offset:]))
	}
	if desc == "" {
		desc = noDescription
	}
	return Entry{Name: name, Description: desc, Source: rel}, true
}

// BodyTokens reports the baseline token size of a skill body on disk. Used by the drop report to
// flag skills that Claude Code will head-truncate (§2.7, G4.3).
//
// It is a package function rather than a method on Indexer so that skills.Indexer stays exactly
// the two-return-value seam 00-ARCHITECTURE.md §5.15 declares. Frontmatter is excluded: it is
// metadata Claude Code strips before the body is ever injected, so counting it would overstate
// every skill by its own header.
func BodyTokens(root string, e Entry) (core.Tokens, error) {
	full := filepath.Join(root, filepath.FromSlash(e.Source))
	raw, err := paths.ReadFileShared(full)
	if err != nil {
		return 0, fmt.Errorf("skills: body tokens for %s: %w", e.Source, err)
	}
	_, offset := parseFrontmatter(string(raw))
	return estimateTokens(len(raw) - offset), nil
}

// lineTokens is the cost of the one index line e renders to. The rendering is duplicated here
// rather than exported because the line is an implementation detail of the budget: callers pay for
// it, they never build it.
func lineTokens(e Entry) core.Tokens {
	return estimateTokens(len("- ") + len(e.Name) + len(": ") + len(e.Description) + len("\n"))
}

// estimateTokens converts a byte count to the package's token estimate, rounding up so that no
// non-empty string is ever free.
func estimateTokens(n int) core.Tokens {
	return core.Tokens((n + bytesPerTokenEstimate - 1) / bytesPerTokenEstimate)
}

// clipDescription trims s and cuts it to maxDescriptionRunes runes, marking a cut with an
// ellipsis. It counts runes rather than bytes: a description written in a non-ASCII language must
// be clipped at the same visible length as an English one, and slicing bytes would also risk
// splitting a rune in half.
func clipDescription(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxDescriptionRunes {
		return s
	}
	return string([]rune(s)[:maxDescriptionRunes]) + descriptionEllipsis
}

// firstBodyLine returns the first non-empty, non-heading line of body — the fallback description
// for a skill whose frontmatter declares none. Markdown headings are skipped because a heading is
// almost always the skill's own name repeated, which an index line already carries.
func firstBodyLine(body string) string {
	for rest := body; rest != ""; {
		line, n, _ := splitLine(rest)
		rest = rest[n:]
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}
