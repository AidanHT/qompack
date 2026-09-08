package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// drainBlobToolResponse is the field name a blob-externalized request's Raw carries (client.go's
// own unexported blobField, respelled here since it is unreachable from package daemon).
const drainBlobToolResponse = "e.tool_response"

// blobFilePrefix is the shipped client's own naming scheme for a blob file (client.go's
// unexported blobFilePrefix, respelled here for the same reason as drainBlobToolResponse above):
// every legitimate descriptor names exactly one file matching it, directly inside spool/, never a
// path with directory components.
const blobFilePrefix = "blob-"

// blobRef is the JSON shape a client-externalized request's Raw carries in place of the field it
// stood in for (client.go's own unexported blobRef type, respelled here for the same reason).
type blobRef struct {
	Blob  string          `json:"blob"`
	Bytes int             `json:"bytes"`
	Field string          `json:"field"`
	X     json.RawMessage `json:"x,omitempty"`
}

// resolveBlob restores req.Event.ToolResponse from a client-externalized blob file, if req.Raw
// names one, and deletes the blob file afterward. A req whose Raw is not a blob descriptor (the
// ordinary case) is returned unchanged.
//
// This compatibility helper is exercised by legacy unit fixtures. Production ingest and drain
// use readBlob and defer cleanup until durable progress and outstanding references permit it.
//
// A descriptor with a nil Event is left untouched — the blob file is neither read nor deleted —
// because there is nowhere to restore the bytes into; that shape is unreachable from the shipped
// client (client.go's externalize only runs when Event != nil) but is reachable from a corrupt or
// hostile spool line, and leaving the file in place is safer than discarding it silently.
func resolveBlob(root string, log logging.Logger, req ipc.Request) ipc.Request {
	resolved, blob, err := readBlob(root, req)
	if err != nil {
		if log != nil {
			log.Warn("daemon: blob resolution failed", "err", err)
		}
		return req
	}
	if err := removeBlob(root, blob); err != nil && log != nil {
		log.Warn("daemon: blob cleanup deferred", "err", err)
	}
	return resolved
}

// readBlob retains the source until its caller acknowledges successful handling. Returned errors
// contain no payload or caller-supplied path. A retry therefore has the same bytes after a NAK.
func readBlob(root string, req ipc.Request) (ipc.Request, string, error) {
	if len(req.Raw) == 0 {
		return req, "", nil
	}
	var ref blobRef
	if err := json.Unmarshal(req.Raw, &ref); err != nil || ref.Blob == "" || ref.Field != drainBlobToolResponse {
		return req, "", nil
	}
	if req.Event == nil {
		return req, "", fmt.Errorf("blob has no event")
	}
	// A hostile or corrupt spool/WAL line can carry a Blob value containing "..", a separator, or
	// an absolute path — resolveBlob reads (and, on success, deletes) whatever file it names, so a
	// name that would escape spool/ must be refused before it is ever joined. The shipped client
	// only ever writes blob-<pid>-<n>.bin (client.go's own externalize), so requiring
	// filepath.Base(ref.Blob) == ref.Blob and the blob- prefix costs no legitimate case (fix
	// round 2, FR-3).
	if !safeBlobName(ref.Blob) {
		return req, "", fmt.Errorf("unsafe blob name")
	}

	blobPath := filepath.Join(paths.Of(root).Spool, ref.Blob)
	fi, err := os.Lstat(paths.Long(blobPath))
	if err != nil || !fi.Mode().IsRegular() || ref.Bytes < 0 || fi.Size() != int64(ref.Bytes) {
		return req, "", fmt.Errorf("blob missing or invalid size/type")
	}
	f, err := os.Open(paths.Long(blobPath))
	if err != nil {
		return req, "", fmt.Errorf("blob unavailable")
	}
	defer func() { _ = f.Close() }()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(fi, opened) {
		return req, "", fmt.Errorf("blob changed during open")
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(ref.Bytes)+1))
	if err != nil || len(data) != ref.Bytes {
		return req, "", fmt.Errorf("blob read incomplete")
	}

	ev := *req.Event
	ev.ToolResponse = data
	req.Event = &ev
	req.Raw = ref.X
	return req, ref.Blob, nil
}

func safeBlobName(name string) bool {
	return filepath.Base(name) == name && strings.HasPrefix(name, blobFilePrefix) &&
		strings.HasSuffix(name, ".bin") && !strings.ContainsAny(name, ":/\\\x00")
}

func removeBlob(root, name string) error {
	if name == "" {
		return nil
	}
	if !safeBlobName(name) {
		return fmt.Errorf("unsafe blob cleanup name")
	}
	err := os.Remove(paths.Long(filepath.Join(paths.Of(root).Spool, name)))
	if os.IsNotExist(err) {
		return nil // a crash after removal but before state persistence is retryable
	}
	if err != nil {
		return fmt.Errorf("blob cleanup unavailable")
	}
	return nil
}
