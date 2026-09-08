package negknow

import (
	"bytes"
	"sort"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// This file is SP-16 §1's scope half: WHERE a piece of evidence was observed, precisely enough
// that a later reader can decide whether reusing it HERE is legitimate.
//
// The existing Scope type ("session" / "project") answers a different question — whose records a
// Query may see inside one project — and it stays what it is. It cannot answer this one, because
// it has no way to say which project, which working tree, or which branch, and a plugin that
// carries eliminations between two checkouts of the same repository (or, worse, between two
// unrelated repositories that happen to share a directory name) would import conclusions that
// were never about the code in front of it. §12 rates the failure a stale elimination causes as
// High; a cross-repository one is the same failure with a wider blast radius.
//
// Two rules govern every type below, and both exist because the alternative is silently wrong.
//
// An UNKNOWN field is never a MATCHING field. Every identifier here is a string whose empty value
// means "not observed", and Relate refuses to derive any relation at all from an unobserved
// repository rather than treating two blanks as equal. That is why the zero ReuseScope relates to
// itself as RelationUnknown and not as RelationSameSession: a scope nobody observed is not a
// scope two things share.
//
// Identity is OBSERVED, never guessed from a path string. A path is not an identity: git
// worktrees give one repository many roots (this is how the Qompack repo itself is developed), a
// directory can be deleted and recreated around different content, and QOMPACK_PROJECT_ROOT can
// point anywhere. NewRepositoryID therefore takes evidence a caller actually read — the git
// common directory the worktree resolves to, and the repository's root-commit id — and yields the
// unknown id when it was handed neither.

// The domain-separation strings this file mints identities under. Changing one re-keys every
// identity already recorded, so treat them as a wire format (see descriptor.go's domain block,
// which these join).
const (
	// domainRepositoryID is the domain of RepositoryID.
	domainRepositoryID = "qompack.neg.repo.v1"
	// domainWorktreeID is the domain of WorktreeID.
	domainWorktreeID = "qompack.neg.worktree.v1"
)

// RepositoryID is the observed identity of one repository, shared by every worktree of it.
//
// It is a digest of RepositoryEvidence rather than a path, so the two checkouts of one repository
// that git worktrees produce agree on it while two unrelated repositories under the same
// directory name do not. The empty value means the repository was not observed, which Relate
// treats as unknown rather than as a match.
type RepositoryID string

// WorktreeID is the observed identity of one working tree of a repository.
//
// Sibling worktrees of one repository share a RepositoryID and differ here, which is exactly the
// distinction "do not import another checkout's unfinished intent" needs. The empty value means
// the working tree was not observed.
type WorktreeID string

// Unknown reports whether r was never observed. An unknown id participates in no relation: see
// Relate.
func (r RepositoryID) Unknown() bool { return r == "" }

// Unknown reports whether w was never observed.
func (w WorktreeID) Unknown() bool { return w == "" }

// RepositoryEvidence is what a caller actually read about a repository, out of which
// NewRepositoryID mints a RepositoryID.
//
// Both fields are optional and neither is a path to the working tree. CommonDir is git's common
// directory — the one `.git` of an ordinary checkout, or the directory the `gitdir:` line of a
// worktree's `.git` FILE ultimately points into — so every worktree of one repository reports the
// same string. RootCommit is the repository's first commit, which survives the common directory
// moving on disk.
//
// Supplying both is better than either: they fail independently. A repository cloned twice has
// two common directories and one root commit; a repository whose history was rewritten from the
// root has one common directory and two root commits. Callers that can observe both get an
// identity that only changes when the repository genuinely does.
type RepositoryEvidence struct {
	// CommonDir is git's common directory, already cleaned and case-folded by the caller the way
	// paths.Key folds a project path. It is empty when the caller could not resolve one.
	CommonDir string
	// RootCommit is the repository's root-commit object id, lowercase hex. It is empty when the
	// caller did not read one.
	RootCommit string
}

// Observed reports whether e carries any evidence at all.
func (e RepositoryEvidence) Observed() bool {
	return e.CommonDir != "" || e.RootCommit != ""
}

// NewRepositoryID mints the identity for the repository e describes, or the unknown id when e
// carries no evidence.
//
// Returning the unknown id rather than an error for unobserved evidence is deliberate: "I could
// not tell which repository this is" is a normal, expected state on a machine with no git, and
// every consumer below already has to handle the unknown id correctly. Making it an error would
// only tempt a caller into substituting a path.
func NewRepositoryID(e RepositoryEvidence) RepositoryID {
	if !e.Observed() {
		return ""
	}
	var b bytes.Buffer
	b.WriteString(sanitizeField(e.CommonDir))
	b.WriteByte(fieldSep)
	b.WriteString(sanitizeField(strings.ToLower(e.RootCommit)))
	return RepositoryID(core.HashBytes(domainRepositoryID, b.Bytes()).Short())
}

// NewWorktreeID mints the identity of one working tree of repo, from the tree's own normalized
// root path.
//
// A path IS the right evidence here, and only here: a worktree is a directory, and two
// directories of one repository are different worktrees precisely because their paths differ. The
// repository id is mixed in so that the same relative layout under two different repositories
// cannot collide. An empty root, or an unknown repo, yields the unknown id.
func NewWorktreeID(repo RepositoryID, normalizedRoot string) WorktreeID {
	if repo.Unknown() || normalizedRoot == "" {
		return ""
	}
	var b bytes.Buffer
	b.WriteString(string(repo))
	b.WriteByte(fieldSep)
	b.WriteString(sanitizeField(normalizedRoot))
	return WorktreeID(core.HashBytes(domainWorktreeID, b.Bytes()).Short())
}

// ReuseScope is the observed place one piece of evidence belongs to: which repository, which
// working tree of it, which branch and version were checked out, and which session was running.
//
// Every field is independently optional, and an empty one means NOT OBSERVED. That is the whole
// point of the type: a consumer must be able to tell "this evidence came from another branch"
// apart from "nobody recorded which branch this came from", and a struct whose blanks compared
// equal would collapse the two into one silently reusable answer.
//
// Version is deliberately NOT part of the structural relation Relate computes. Which repository
// and tree evidence came from is a question about PLACE and does not change as work proceeds;
// whether the code has moved under it since is a question about FRESHNESS, and this package
// already answers that one through Record.DependsOn and RefreshStaleness. Keeping them apart is
// what stops a commit from looking like a different repository.
type ReuseScope struct {
	// Repository is the observed repository identity; the unknown id when none was observed.
	Repository RepositoryID
	// Worktree is the observed working tree within Repository.
	Worktree WorktreeID
	// Branch is the checked-out branch or ref name, verbatim; "" means not observed.
	Branch string
	// Version is the checked-out commit or version observed at the time, lowercase hex or a
	// caller-chosen version string; "" means not observed.
	Version string
	// Session is the session that made the observation; "" means not observed.
	Session core.SessionID
}

// Relation is the observed structural relationship between two ReuseScopes: how close the place
// one piece of evidence came from is to the place it would be reused.
//
// The values are ordered from narrowest to broadest, with the two non-relations at the ends, and
// Narrower relies on that order. RelationUnknown is the zero value on purpose: a Relation nobody
// computed must not read as permission, and every consumer that switches on it handles the
// unknown case first.
type Relation uint8

const (
	// RelationUnknown means no relation could be established, because the repository was not
	// observed on one side or the other. It is neither "same" nor "different": the caller learned
	// nothing, and must not reuse across it.
	RelationUnknown Relation = iota
	// RelationUnrelated means the two scopes were observed to be DIFFERENT repositories. Nothing
	// authorizes reuse across it — an authorization that claims to is refused (see Authorize).
	RelationUnrelated
	// RelationSameRepository means one repository, but a different working tree and a different
	// (or unobserved) branch.
	RelationSameRepository
	// RelationSameBranch means one repository and one branch name, in different working trees.
	RelationSameBranch
	// RelationSameWorktree means one repository and one working tree, in a different session.
	RelationSameWorktree
	// RelationSameSession means one and the same session: the evidence never left home.
	RelationSameSession
)

// String returns the human-facing spelling of rel, or "unknown" for a value no version of this
// package has minted. It is what an applicability transcript and a /qompack:status line print;
// nothing on disk depends on it.
func (rel Relation) String() string {
	switch rel {
	case RelationUnknown:
		return "unknown"
	case RelationUnrelated:
		return "unrelated"
	case RelationSameRepository:
		return "same-repository"
	case RelationSameBranch:
		return "same-branch"
	case RelationSameWorktree:
		return "same-worktree"
	case RelationSameSession:
		return "same-session"
	}
	return "unknown"
}

// Narrower reports whether rel is at least as narrow as min — the containment test an
// authorization grant applies to a request.
//
// RelationUnknown and RelationUnrelated are narrower than nothing, including themselves: a caller
// asking "is this relation at least RelationUnrelated?" is asking a question with no useful
// answer, and returning true would hand it a grant. Both therefore report false for every min,
// which is what makes "grant the broadest scope you like" still refuse to cross a repository
// boundary.
func (rel Relation) Narrower(min Relation) bool {
	if rel == RelationUnknown || rel == RelationUnrelated {
		return false
	}
	if min == RelationUnknown || min == RelationUnrelated {
		return false
	}
	return rel >= min
}

// Relate reports the narrowest structural relation observation establishes between from — where
// evidence was recorded — and to, where a caller would reuse it.
//
// The rules, in order:
//
//  1. An unknown repository on either side yields RelationUnknown. Nothing below can be trusted
//     without it, and two blanks are not a match.
//  2. Different repositories yield RelationUnrelated, and the fields below are not consulted:
//     two repositories can easily share a branch name.
//  3. Otherwise the narrowest relation whose evidence is present on both sides and agrees wins —
//     same session, then same worktree, then same branch — falling back to RelationSameRepository,
//     which rule 1 has already established.
//
// It is symmetric (Relate(a,b) == Relate(b,a)) and reflexive only for scopes that were actually
// observed: Relate(zero, zero) is RelationUnknown, by rule 1.
func Relate(from, to ReuseScope) Relation {
	if from.Repository.Unknown() || to.Repository.Unknown() {
		return RelationUnknown
	}
	if from.Repository != to.Repository {
		return RelationUnrelated
	}
	if from.Session != "" && from.Session == to.Session {
		return RelationSameSession
	}
	if !from.Worktree.Unknown() && from.Worktree == to.Worktree {
		return RelationSameWorktree
	}
	if from.Branch != "" && from.Branch == to.Branch {
		return RelationSameBranch
	}
	return RelationSameRepository
}

// VersionAgreement is what comparing two scopes' Version fields established.
type VersionAgreement uint8

const (
	// VersionUnobserved means at least one side did not record a version. It is the zero value:
	// an uncompared version is not an agreeing one.
	VersionUnobserved VersionAgreement = iota
	// VersionSame means both sides recorded the same version.
	VersionSame
	// VersionMoved means both sides recorded a version and they differ.
	VersionMoved
)

// Versions reports what comparing from's and to's Version fields established.
//
// A moved version is NOT by itself a reason to refuse reuse, and this function deliberately
// returns a fact rather than a verdict: whether the code an elimination depended on actually
// changed is Record.DependsOn's question, and RefreshStaleness answers it against real file
// hashes. What VersionMoved contributes is the weaker, cheaper signal that SOMETHING moved, which
// Applies records so a transcript can say so even where no dependency was declared.
func Versions(from, to ReuseScope) VersionAgreement {
	if from.Version == "" || to.Version == "" {
		return VersionUnobserved
	}
	if from.Version == to.Version {
		return VersionSame
	}
	return VersionMoved
}

// Unobserved lists, in a stable order, the names of every ReuseScope field s did not record.
//
// It exists so a transcript can say WHICH observation is missing rather than only that coverage
// was incomplete — "reuse withheld: branch and version unobserved" is actionable where "unknown
// applicability" is not. The names are the struct's own field names lowercased, and the order is
// alphabetical so two transcripts of the same gap compare equal.
func (s ReuseScope) Unobserved() []string {
	var out []string
	if s.Repository.Unknown() {
		out = append(out, "repository")
	}
	if s.Worktree.Unknown() {
		out = append(out, "worktree")
	}
	if s.Branch == "" {
		out = append(out, "branch")
	}
	if s.Version == "" {
		out = append(out, "version")
	}
	if s.Session == "" {
		out = append(out, "session")
	}
	sort.Strings(out)
	return out
}

// Observed reports whether s recorded every field. An unobserved scope is still usable — most
// relations need only the repository — but a caller that requires a complete transcript checks
// this first.
func (s ReuseScope) Observed() bool { return len(s.Unobserved()) == 0 }
