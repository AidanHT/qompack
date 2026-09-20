package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Norm returns p as a project-relative, forward-slash, cleaned path: the canonical form every
// store key, DAG node and depends_on entry uses (§4). p may be absolute or relative to
// projectRoot. Symlinks are resolved, but only when doing so keeps the result inside
// projectRoot — a symlink that points outside is left unresolved rather than followed, so Norm
// can never be used to smuggle an outside path back in under a legitimate-looking relative name.
// A path that escapes projectRoot, before or after symlink resolution, is rejected with an error
// wrapping core.ErrNotFound.
func Norm(projectRoot, p string) (string, error) {
	if projectRoot == "" {
		return "", fmt.Errorf("%w: empty project root", core.ErrNotFound)
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(projectRoot, p)
	}
	abs = filepath.Clean(abs)
	root := filepath.Clean(projectRoot)

	// The root has to be resolved before it can be compared against a resolved target.
	// EvalSymlinks canonicalises its WHOLE argument, so when the project's own path contains a
	// symlink the resolved target and the unresolved root share no prefix at all: inside() says
	// no, and a symlink that genuinely lives inside the project is silently left unresolved. That
	// is not hypothetical — macOS puts every TempDir under /var, which is itself a link to
	// /private/var, and it is how this failed on the first CI run this repository ever had, on
	// macOS and Windows both. The consequence in production is worse than a wrong string: the
	// same file keys two different ways depending on how its root was spelled, which forks the
	// dedup space of a content-addressed store.
	//
	// When the resolved target is adopted, the resolved root is adopted with it, so the Rel below
	// measures both against the same base.
	resolvedRoot := root
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		resolvedRoot = rr
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil && inside(resolvedRoot, r) {
		abs, root = r, resolvedRoot
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", fmt.Errorf("%w: path escapes project root: %s", core.ErrNotFound, p)
	}
	return rel, nil
}

// ResolvesInside reports whether p, resolved on disk as far as it exists today, still lands inside
// projectRoot.
//
// It is the question Norm deliberately does NOT answer, and the difference is finding S-1. Norm is
// the store-key normaliser: it calls EvalSymlinks and DISCARDS a resolution that lands outside the
// root, keeping the unresolved spelling. That is an anti-smuggling rule — an outside path must never
// come back in under a legitimate-looking relative name — and it is what makes a key stable. The
// consequence for RETRIEVAL is that a path whose parent directory has since been replaced by a link
// pointing outside the project normalises perfectly cleanly, so an authorization gate built on Norm
// alone has nothing to refuse, and the archive serves content for an address a live read of the same
// path would be denied today.
//
// So this is a separate, retrieval-time question with a separate answer, and it is a REFUSAL
// predicate rather than a normaliser: it never rewrites anything and it never returns a path.
//
// A path that does not exist on disk still has an answer. Resolution walks component by component
// from the volume root and follows every link it MEETS, so components that do not exist simply
// contribute nothing to follow — a captured file that has since been deleted underneath a directory
// now pointing outside the project is refused on the directory, exactly as it would be if the file
// were still there.
//
// A lexically escaping path is refused without touching the disk at all.
func ResolvesInside(projectRoot, p string) bool {
	if projectRoot == "" || p == "" {
		return false
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(projectRoot, p)
	}
	abs = filepath.Clean(abs)
	root := filepath.Clean(projectRoot)
	if !filepath.IsAbs(abs) || !inside(root, abs) {
		return false
	}
	resolvedRoot, rootOK := resolveLinks(root)
	resolved, ok := resolveLinks(abs)
	if !rootOK || !ok {
		// A cycle, or a link chain past the hop bound. There is no on-disk answer, and a gate that
		// cannot establish containment refuses.
		return false
	}
	return inside(resolvedRoot, resolved)
}

// maxLinkHops bounds how many links one resolution may follow before giving up, so a link cycle
// cannot spin here. It is generous next to any real tree and far below what a cycle needs.
//
//nolint:nomagic // a loop bound against a link cycle, not a configurable budget.
const maxLinkHops = 64

// resolveLinks resolves p component by component from its volume root, following every symlink,
// junction and mount point it meets.
//
// It does NOT use filepath.EvalSymlinks, and that is the junction-awareness the authorization gate
// needs. On Windows EvalSymlinks does not follow a junction: given the junction itself it returns
// the junction's own path unchanged, and given a path THROUGH one it fails outright with "the system
// cannot find the path specified". Either answer would let a directory swapped for a junction
// pointing out of the project pass a containment check. os.Readlink does read a junction's target —
// Go implements it for mount points as well as symlinks — so the walk is built on that instead.
//
// A component that does not exist is simply not a link, so a path whose leaf (or whose whole tail)
// is absent resolves to whatever its existing ancestors resolve to, with the missing names appended.
// That is the right answer for a HISTORICAL read: the question is where the path would land, not
// whether anything is there now. Following a link restarts the walk from the target's volume root
// with the target's components plus the unread remainder, so a chain of links is re-walked.
func resolveLinks(p string) (string, bool) {
	p = filepath.Clean(p)
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(p)
	rest := strings.TrimPrefix(p, vol)
	parts := strings.Split(rest, sep)

	cur := vol + sep
	hops := 0
	for i := 0; i < len(parts); {
		part := parts[i]
		if part == "" || part == "." {
			i++
			continue
		}
		cur = filepath.Join(cur, part)
		target, err := os.Readlink(Long(cur))
		if err != nil {
			i++
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", false
		}
		target = strings.TrimPrefix(strings.TrimPrefix(target, longPrefixForTrim), `\\?\`)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		target = filepath.Clean(target)
		tvol := filepath.VolumeName(target)
		trest := strings.TrimPrefix(target, tvol)
		parts = append(strings.Split(trest, sep), parts[i+1:]...)
		cur = tvol + sep
		i = 0
	}
	return cur, true
}

// longPrefixForTrim is the extended-length prefix a Windows reparse-point target can come back
// carrying. It is trimmed so the result is an ordinary path the comparison below can measure; on
// every other platform no target ever carries it and the trim is a no-op.
const longPrefixForTrim = `\??\`

// inside reports whether r is projectRoot itself, or lies under it, once both are cleaned. Norm
// uses it to decide whether a resolved symlink target is safe to adopt.
func inside(projectRoot, r string) bool {
	root := filepath.Clean(projectRoot)
	r = filepath.Clean(r)
	if r == root {
		return true
	}
	rel, err := filepath.Rel(root, r)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel != ".." && !strings.HasPrefix(rel, "../")
}

// DefaultFold reports whether Key should case-fold on this platform: true on Windows and macOS,
// whose default filesystems are case-insensitive, false everywhere else (§4).
func DefaultFold() bool { return runtime.GOOS == "windows" || runtime.GOOS == "darwin" }

// KeyFold returns p lowercased when fold is true, and p unchanged otherwise. It exists so golden
// fixtures can pin a fold setting and stay byte-identical across platforms; production code
// calls Key. Original casing is never destroyed by either — callers keep the Norm result
// alongside whichever Key they compute from it.
func KeyFold(p string, fold bool) string {
	if fold {
		return strings.ToLower(p)
	}
	return p
}

// Key returns the dedup key for p: KeyFold using this platform's DefaultFold. All store keys,
// DAG file nodes, depends_on entries and glob matching use Key.
func Key(p string) string { return KeyFold(p, DefaultFold()) }
