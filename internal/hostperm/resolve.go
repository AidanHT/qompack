package hostperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

// maxLinkHops bounds one resolution against a link cycle. It matches internal/paths' own bound for
// the containment check this package's answer sits beside.
const maxLinkHops = 64

// linkMemo caches os.Readlink answers for one rule-set build, where every anchored rule re-walks the
// same anchor directories: without it a build paid one Readlink per anchor component per rule, and
// the first request after an edit to a large rule list waited seconds for it. A nil memo reads the
// disk every time, which is what Evaluate uses, because a request must see links as they are now.
type linkMemo map[string]linkAnswer

// linkAnswer is one cached Readlink result.
type linkAnswer struct {
	target string
	err    error
}

// readlink is os.Readlink through the memo.
func (m linkMemo) readlink(p string) (string, error) {
	if m == nil {
		return os.Readlink(paths.Long(p))
	}
	if a, ok := m[p]; ok {
		return a.target, a.err
	}
	t, err := os.Readlink(paths.Long(p))
	m[p] = linkAnswer{target: t, err: err}
	return t, err
}

// resolveLinks resolves p component by component from its volume root, following every symlink,
// junction and mount point it meets, and reports false for a cycle. memo may be nil.
//
// It is the same walk internal/paths uses for ResolvesInside (which returns only a verdict, not the
// path), repeated here because §3.2 keeps that helper unexported. It does not use
// filepath.EvalSymlinks for the reason paths records: on Windows EvalSymlinks does not follow a
// junction, and a path through one fails outright, so a directory swapped for a junction would slip
// past a check built on it. A component that does not exist contributes nothing to follow, so a
// deleted file still resolves through whatever its surviving ancestors point at — the right answer
// for a historical read.
func resolveLinks(p string, memo linkMemo) (string, bool) {
	p = filepath.Clean(p)
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(p)
	parts := strings.Split(strings.TrimPrefix(p, vol), sep)

	cur := vol + sep
	hops := 0
	for i := 0; i < len(parts); {
		part := parts[i]
		if part == "" || part == "." {
			i++
			continue
		}
		cur = filepath.Join(cur, part)
		target, err := memo.readlink(cur)
		if errors.Is(err, fs.ErrNotExist) {
			// Nothing below a missing component exists, so nothing below it is a link: the rest is
			// appended as written, which is what walking it would produce, without a syscall each.
			return filepath.Join(append([]string{cur}, parts[i+1:]...)...), true
		}
		if err != nil {
			i++
			continue
		}
		hops++
		if hops > maxLinkHops {
			return "", false
		}
		target = strings.TrimPrefix(strings.TrimPrefix(target, `\??\`), `\\?\`)
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(cur), target)
		}
		target = filepath.Clean(target)
		tvol := filepath.VolumeName(target)
		parts = append(strings.Split(strings.TrimPrefix(target, tvol), sep), parts[i+1:]...)
		cur = tvol + sep
		i = 0
	}
	return cur, true
}

// posixSegments converts an absolute path into the host's POSIX form, split into segments and
// case-folded when fold is set. On Windows `C:\Users\alice` is `/c/Users/alice`, which is what a
// `//c/...` rule is written against. An extended-length prefix is dropped, and a UNC path keeps its
// server and share as its first two segments.
func posixSegments(p, goos string, fold bool) []string {
	s := p
	if goos == "windows" {
		s = strings.TrimPrefix(strings.TrimPrefix(s, `\\?\UNC\`), `\\?\`)
		s = strings.ReplaceAll(s, `\`, "/")
		if len(s) >= 2 && s[1] == ':' {
			s = "/" + strings.ToLower(s[:1]) + s[2:]
		}
	}
	if fold {
		s = strings.ToLower(s)
	}
	var out []string
	for _, seg := range strings.Split(s, "/") {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	return out
}

// under reports whether p lies at or below anchor, and returns the segments below it.
func under(p, anchor []string) ([]string, bool) {
	if len(p) < len(anchor) {
		return nil, false
	}
	for i := range anchor {
		if p[i] != anchor[i] {
			return nil, false
		}
	}
	return p[len(anchor):], true
}

// ancestorsUp drops n trailing segments. Climbing past the filesystem root stays at the root, as
// `..` does in a path.
func ancestorsUp(anchor []string, n int) []string {
	if n >= len(anchor) {
		return []string{}
	}
	return anchor[:len(anchor)-n]
}
