package store

import (
	"os"
	"sync/atomic"
)

// publicationEntryHook, when set, runs in eachDirEntry after an entry has been enumerated and before
// the pass classifies it: the window in which a Put's pending-write marker, a capture sidecar or an
// object can be removed by the store the pass is accounting for while it goes on serving. It is nil
// in production; only this package's tests (publication_snapshot_test.go) set it, so a test can
// remove a file inside that window deterministically (w15-services review).
var publicationEntryHook atomic.Pointer[func(dir string, e os.DirEntry)]
