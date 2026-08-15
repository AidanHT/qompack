package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/paths"
)

// drainBlobToolResponse is the field name a blob-externalized request's Raw carries (client.go's
// own unexported blobField, respelled here since it is unreachable from package daemon).
const drainBlobToolResponse = "e.tool_response"

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
// This is shared, package-level code — not a method on drainer — because ipc.Client externalizes
// on the LIVE wire path (client.go's Send, not only the spool fallback): any request whose
// encoded line reaches ExternalizeThreshold arrives at Accept already carrying a blob descriptor,
// not just at Drain. Both ingest.dispatch and drainer.drainFile call this exact function, so there
// is one place that knows the descriptor shape and one place that deletes the blob file — a
// second, drifted copy is how a resolved-on-one-path/unresolved-on-the-other bug is born.
//
// A descriptor with a nil Event is left untouched — the blob file is neither read nor deleted —
// because there is nowhere to restore the bytes into; that shape is unreachable from the shipped
// client (client.go's externalize only runs when Event != nil) but is reachable from a corrupt or
// hostile spool line, and leaving the file in place is safer than discarding it silently.
func resolveBlob(root string, log logging.Logger, req ipc.Request) ipc.Request {
	if log == nil {
		log = logging.Nop()
	}
	if len(req.Raw) == 0 {
		return req
	}
	var ref blobRef
	if err := json.Unmarshal(req.Raw, &ref); err != nil || ref.Blob == "" || ref.Field != drainBlobToolResponse {
		return req
	}
	if req.Event == nil {
		return req
	}

	blobPath := filepath.Join(paths.Of(root).Spool, ref.Blob)
	data, err := os.ReadFile(paths.Long(blobPath))
	if err != nil {
		log.Warn("daemon: blob resolution failed", "blob", ref.Blob, "err", err)
		return req
	}

	ev := *req.Event
	ev.ToolResponse = data
	req.Event = &ev
	req.Raw = ref.X
	_ = os.Remove(paths.Long(blobPath))
	return req
}
