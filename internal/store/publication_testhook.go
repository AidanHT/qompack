package store

import (
	"os"
	"sync/atomic"
)

// publicationEntryHook, when set, runs in eachDirEntry after an entry has been enumerated and before
// the pass classifies it: the window in which a Put's pending-write marker, a capture sidecar or an
// object can be removed by the store the pass is accounting for while it goes on serving. It is
// handed the root the listed directory was opened under and that directory's name there, so the
// entry is parent.Remove(filepath.Join(dirName, e.Name())) on every walk, a leaf listed without a
// root of its own included. It is nil in production; only this package's tests
// (publication_snapshot_test.go) set it, so a test can remove a file inside that window
// deterministically (w15-services review).
var publicationEntryHook atomic.Pointer[func(parent *os.Root, dirName string, e os.DirEntry)]
