//go:build !linux

package paths

import "os"

// syncData is f.Sync off Linux: FlushFileBuffers on Windows, and on darwin fcntl(F_FULLFSYNC),
// which Go's own os.File.Sync issues there (GOROOT/src/internal/poll/fd_fsync_darwin.go).
func syncData(f *os.File) error { return f.Sync() }
