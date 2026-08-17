package ipc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qompack/qompack/internal/ipc"
)

// ipcFrameCorpusDir is the seed corpus this commit creates: the golden request line, an empty JSON
// object, an empty file, a 1 MiB line of 'a', and a chunk of invalid UTF-8 — the shapes DecodeRequest
// must survive without panicking, per task-1-spec's fuzz row.
const ipcFrameCorpusDir = "../../testdata/corpora/ipcframe"

// FuzzDecodeRequest asserts DecodeRequest never panics on arbitrary bytes: it returns a Request or
// an error, and nothing else, for any input a corrupted spool line or a hostile peer could produce.
func FuzzDecodeRequest(f *testing.F) {
	entries, err := os.ReadDir(ipcFrameCorpusDir)
	if err != nil {
		f.Fatalf("reading fuzz seed corpus %s: %v", ipcFrameCorpusDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(ipcFrameCorpusDir, e.Name()))
		if err != nil {
			f.Fatalf("reading fuzz seed %s: %v", e.Name(), err)
		}
		f.Add(b)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = ipc.DecodeRequest(data)
	})
}
