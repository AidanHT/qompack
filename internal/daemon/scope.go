package daemon

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/negknow"
	"github.com/qompack/qompack/internal/paths"
)

// This file is SP-16 §1's observation half: it turns a project root into the ReuseScope
// internal/negknow's applicability gate decides from.
//
// The gate itself is pure and lives in negknow, which is what makes it testable and what lets
// internal/scheduler take its answer as a value without importing it. Somebody still has to look
// at the disk, and that is a composition root's job — this one.
//
// Everything here reads FILES. There is no `git` subprocess and no libgit dependency: git's
// on-disk layout is a documented format, the three things this needs from it (which repository,
// which branch, which commit) are three small text files, and shelling out would put an external
// binary's presence, version and exit status on the path of a decision that must degrade cleanly
// when it cannot be made.
//
// Which is the other rule. NOTHING HERE FAILS. Every reader returns what it could observe and an
// omission for what it could not, because an unobserved field is a legitimate state that the gate
// already handles correctly — it refuses to reuse across it. Returning an error instead would push
// a caller toward either aborting a session over a missing branch file or, far worse, substituting
// a guess.
//
// A WORKTREE IS THE INTERESTING CASE, and it is the one this repository is developed in. A
// worktree's `.git` is a FILE holding `gitdir: <path>`; that directory holds a `commondir` file
// pointing back at the real repository. Two worktrees therefore resolve to one common directory
// and two roots — which is exactly the "same repository, different working tree" relation
// negknow.Relate needs, and exactly the case a path-string identity gets wrong.

// The git on-disk names this file reads. They are a documented format, not implementation detail,
// but they are still names in someone else's tree: they are collected here so that a layout change
// is one edit rather than a search.
const (
	gitMarkerName = ".git"
	gitDirPrefix  = "gitdir:"
	gitCommonDir  = "commondir"
	gitHead       = "HEAD"
	gitRefPrefix  = "ref:"
	gitPackedRefs = "packed-refs"
	gitRefsHeads  = "refs/heads/"
)

// maxGitFileBytes bounds how much of a git metadata file is read: 64 KiB.
//
// These files hold one short line each — packed-refs is the only one that can grow, and it is
// streamed rather than read whole. The bound is what stops a corrupt or hostile `.git` file from
// turning scope observation into an unbounded read on a path that runs at session start.
const maxGitFileBytes = 65536

// ObserveScope reads the repository, worktree, branch and version evidence for root and returns
// the ReuseScope negknow.Applies decides from, plus one omission per thing it could not observe.
//
// The omissions are not errors and the scope is always usable. A scope with an unknown repository
// relates to nothing (negknow.Relate returns RelationUnknown), which withholds cross-scope reuse —
// the correct outcome, reached by the gate rather than by an exception here.
func ObserveScope(root string, sess core.SessionID) (negknow.ReuseScope, []core.Omission) {
	var omissions []core.Omission
	note := func(reason, recovery string) {
		omissions = append(omissions, core.Omission{Reason: reason, Recovery: recovery})
	}

	scope := negknow.ReuseScope{Session: sess}
	if sess == "" {
		note("no session id was supplied",
			"pass the host's session id; a blank session matches no other session")
	}

	gitDir, ok := resolveGitDir(root)
	if !ok {
		note("no readable .git for this project root",
			"reuse stays within this session; there is no observed repository to scope it to")
		return scope, omissions
	}

	common := resolveCommonDir(gitDir)
	scope.Repository = negknow.NewRepositoryID(negknow.RepositoryEvidence{
		CommonDir: paths.Key(filepath.ToSlash(common)),
	})
	if scope.Repository.Unknown() {
		note("the repository's common directory could not be identified",
			"reuse stays within this session")
		return scope, omissions
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	scope.Worktree = negknow.NewWorktreeID(scope.Repository, paths.Key(filepath.ToSlash(abs)))

	branch, version, headOmission := readHead(gitDir, common)
	scope.Branch, scope.Version = branch, version
	if headOmission != (core.Omission{}) {
		omissions = append(omissions, headOmission)
	}
	return scope, omissions
}

// resolveGitDir returns the git directory for root: the `.git` directory of an ordinary checkout,
// or the directory a worktree's `.git` FILE points at.
//
// It does NOT walk upward. paths.Resolve already decided which directory is the project root, and
// walking again here could name a repository the store does not belong to — a `.qompack` under a
// subdirectory of a repo would silently adopt the whole repo's identity.
func resolveGitDir(root string) (string, bool) {
	marker := filepath.Join(root, gitMarkerName)
	fi, err := os.Stat(paths.Long(marker))
	if err != nil {
		return "", false
	}
	if fi.IsDir() {
		return marker, true
	}
	b, err := readSmallFile(marker)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(b))
	if !strings.HasPrefix(line, gitDirPrefix) {
		return "", false
	}
	dir := strings.TrimSpace(strings.TrimPrefix(line, gitDirPrefix))
	if dir == "" {
		return "", false
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir), true
}

// resolveCommonDir returns the repository's common directory: gitDir itself for an ordinary
// checkout, or what gitDir's `commondir` file points at for a worktree.
//
// A gitDir whose commondir is unreadable falls back to gitDir. That is the conservative direction:
// it makes each worktree look like its own repository, so reuse between them is refused as
// cross-repository rather than allowed on a guess.
func resolveCommonDir(gitDir string) string {
	b, err := readSmallFile(filepath.Join(gitDir, gitCommonDir))
	if err != nil {
		return gitDir
	}
	rel := strings.TrimSpace(string(b))
	if rel == "" {
		return gitDir
	}
	if filepath.IsAbs(rel) {
		return filepath.Clean(rel)
	}
	return filepath.Clean(filepath.Join(gitDir, rel))
}

// readHead reads gitDir's HEAD and reports the branch name and the commit it resolves to.
//
// Three shapes are handled, and each yields what it actually knows:
//
//   - `ref: refs/heads/<name>` — the branch is <name>, and the version is whatever that ref
//     resolves to through the loose ref file or the common directory's packed-refs. A branch with
//     no commit yet (a fresh repository) reports the branch and no version, which is true.
//   - a bare object id — a detached HEAD. The version is known and the BRANCH IS NOT, and it is
//     reported blank rather than as "HEAD": two detached checkouts do not share a branch, and
//     naming them both "HEAD" would make negknow.Relate report RelationSameBranch for two places
//     that have nothing to do with each other.
//   - anything else, or an unreadable file — both blank, with an omission.
func readHead(gitDir, common string) (branch, version string, omission core.Omission) {
	b, err := readSmallFile(filepath.Join(gitDir, gitHead))
	if err != nil {
		return "", "", core.Omission{
			Reason:   "HEAD is unreadable, so neither branch nor version was observed",
			Recovery: "reuse is withheld across branches until HEAD can be read",
		}
	}
	line := strings.TrimSpace(string(b))
	if line == "" {
		return "", "", core.Omission{
			Reason:   "HEAD is empty",
			Recovery: "reuse is withheld across branches until HEAD names a ref or a commit",
		}
	}
	if !strings.HasPrefix(line, gitRefPrefix) {
		if isObjectID(line) {
			return "", strings.ToLower(line), core.Omission{
				Reason:   "HEAD is detached, so no branch was observed",
				Recovery: "reuse across branches stays withheld; the commit is still recorded",
			}
		}
		return "", "", core.Omission{
			Reason:   "HEAD is neither a ref nor an object id",
			Recovery: "reuse is withheld across branches",
		}
	}

	ref := strings.TrimSpace(strings.TrimPrefix(line, gitRefPrefix))
	branch = strings.TrimPrefix(ref, gitRefsHeads)
	if branch == "" {
		return "", "", core.Omission{
			Reason:   "HEAD names a ref with no branch name",
			Recovery: "reuse is withheld across branches",
		}
	}

	if v, ok := resolveRef(gitDir, common, ref); ok {
		return branch, v, core.Omission{}
	}
	return branch, "", core.Omission{
		Reason:   "the branch has no resolvable commit yet",
		Recovery: "the branch is still recorded; version agreement is reported as unobserved",
	}
}

// resolveRef resolves ref to an object id, trying the worktree's own loose ref first, then the
// common directory's, then the common directory's packed-refs.
//
// The order matters for a worktree: per-worktree refs live under the worktree's git directory and
// shadow the common one, so reading the common directory first would report the wrong commit for
// exactly the layout this repository is developed in.
func resolveRef(gitDir, common, ref string) (string, bool) {
	for _, dir := range []string{gitDir, common} {
		b, err := readSmallFile(filepath.Join(dir, filepath.FromSlash(ref)))
		if err == nil {
			if id := strings.TrimSpace(string(b)); isObjectID(id) {
				return strings.ToLower(id), true
			}
		}
	}
	return resolvePackedRef(common, ref)
}

// resolvePackedRef scans common/packed-refs for ref.
//
// Only the `<id> <ref>` lines are read: a `^<id>` peel line describes the tag's target rather than
// the ref, and a `#` line is a header. Neither is a branch tip.
func resolvePackedRef(common, ref string) (string, bool) {
	f, err := paths.OpenShared(filepath.Join(common, gitPackedRefs))
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		id, name, found := strings.Cut(line, " ")
		if !found || strings.TrimSpace(name) != ref || !isObjectID(id) {
			continue
		}
		return strings.ToLower(id), true
	}
	return "", false
}

// isObjectID reports whether s looks like a git object id: 40 hex characters for SHA-1, or 64 for
// SHA-256. Nothing here parses the id, so recognising both lengths costs one comparison and keeps
// a SHA-256 repository from reporting every commit as unobserved.
func isObjectID(s string) bool {
	const sha1Len, sha256Len = 40, 64
	if len(s) != sha1Len && len(s) != sha256Len {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// readSmallFile reads at most maxGitFileBytes of p.
func readSmallFile(p string) ([]byte, error) {
	f, err := paths.OpenShared(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return readAtMost(f, maxGitFileBytes)
}

// readAtMost reads at most n bytes from f, returning what it got. A read that stops short is not
// an error here: a truncated HEAD produces a line that does not parse, which the caller already
// reports as an omission.
func readAtMost(f *os.File, n int) ([]byte, error) {
	buf := make([]byte, n)
	read, err := f.Read(buf)
	if read > 0 {
		return buf[:read], nil
	}
	return nil, err
}
