package hostperm

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/paths"
)

// maxLinkHops bounds one resolution against a link cycle. It matches internal/paths' own bound for
// the containment check this package's answer sits beside.
const maxLinkHops = 64

// resolveLinks resolves p component by component from its volume root, following every symlink,
// junction and mount point it meets, and reports false for a cycle.
//
// It is the same walk internal/paths uses for ResolvesInside (which returns only a verdict, not the
// path), repeated here because §3.2 keeps that helper unexported. It does not use
// filepath.EvalSymlinks for the reason paths records: on Windows EvalSymlinks does not follow a
// junction, and a path through one fails outright, so a directory swapped for a junction would slip
// past a check built on it. A component that does not exist contributes nothing to follow, so a
// deleted file still resolves through whatever its surviving ancestors point at — the right answer
// for a historical read.
func resolveLinks(p string) (string, bool) {
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
		target, err := os.Readlink(paths.Long(cur))
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
