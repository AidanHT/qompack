package paths

import (
	"errors"
	"os"
)

// ErrNotLeaf is OpenSharedLeaf's refusal of a final path element that is a link: a symbolic link
// on any platform, or on Windows a junction or any other name-surrogate reparse point. It reaches
// the caller wrapped in an *os.PathError, so errors.Is finds it.
var ErrNotLeaf = errors.New("paths: the final path element is a link, not a file")

// OpenSharedLeaf opens p read-only exactly as OpenShared does — with delete sharing, so a concurrent
// writer's replace of p lands while the handle is open — except that it never follows a final path
// element that is a link: that open fails with ErrNotLeaf instead.
//
// It exists for a reader that must know the bytes it reads came from the file the path names and
// not from wherever a link points, and that must not lose that knowledge to a writer replacing the
// file under it. The older way to get both — Lstat that the leaf is a regular file, open it, then
// compare the two with os.SameFile — refuses whenever an atomic save lands between the Lstat and
// the comparison, because the path then names the new file while the handle holds the old one. A
// no-follow open has no such window: it holds the old file or the new one, and either is the
// plain file the path named when the open resolved it. internal/config reads config.json this way
// (00-ARCHITECTURE.md §3.2, owner decision D22).
//
// Off Windows it is open(2) with O_NOFOLLOW, and O_NONBLOCK so that a FIFO at p is opened without
// waiting for a writer. On Windows it is OpenShared's CreateFile plus FILE_FLAG_OPEN_REPARSE_POINT,
// then a look at the handle's reparse tag: a name surrogate (symlink, junction) is refused, and a
// reparse point that stands for the file's own content — a cloud-sync placeholder, a
// deduplicated file — is opened again the ordinary way so its content is recalled, and kept only
// if that second handle is the same file as the first (ErrLeafReplaced otherwise).
//
// Like OpenShared it does not judge what it opened: a directory or a FIFO comes back as a handle,
// and the caller checks the handle's own Stat. Errors have os.Open's shape, an *os.PathError, so
// os.IsNotExist and friends read them unchanged.
func OpenSharedLeaf(p string) (*os.File, error) {
	return openSharedLeaf(Long(p))
}

// ErrLeafReplaced is OpenSharedLeaf's answer on Windows when p is a content reparse point (a
// cloud-sync placeholder, a deduplicated file) that was replaced between the no-follow open and the
// ordinary one that recalls its content. It reaches the caller wrapped in an *os.PathError.
var ErrLeafReplaced = errors.New("paths: the file was replaced while it was being opened")
