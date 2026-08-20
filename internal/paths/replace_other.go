//go:build !windows

package paths

import "os"

// replace renames tmp onto p. Off Windows this is just os.Rename, which already has exactly the
// semantics replace_windows.go has to go out of its way to obtain: rename(2) unlinks the
// destination from the directory while any process still holding it open keeps reading the inode
// it opened, so a concurrent reader can never make a writer's replace fail.
func replace(tmp, p string) error { return os.Rename(tmp, p) }

// openShared opens p read-only. Off Windows every open is already "shared" in the only sense that
// matters here — a POSIX file descriptor never blocks another process's unlink or rename — so
// this is os.Open unchanged.
func openShared(p string) (*os.File, error) { return os.Open(p) }
