package hookio

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/qompack/qompack/internal/core"
)

// Event is the typed form of every Claude Code hook payload (00-ARCHITECTURE.md §5.3). A single
// struct covers all seven hook types the plugin registers; each hook only populates the subset of
// fields that apply to it (SessionStart uses Source, PreCompact uses Trigger, PostToolUse uses
// ToolName/ToolUseID/ToolInput/ToolResponse, and so on). Every field is a value type with a usable
// zero, so a hook payload missing a field never produces an invalid Event, only an empty one.
type Event struct {
	HookEventName  string         `json:"hook_event_name"`
	SessionID      core.SessionID `json:"session_id"`
	TranscriptPath string         `json:"transcript_path"`
	CWD            string         `json:"cwd"`
	// Source is SessionStart's discriminator: startup|resume|compact|clear.
	Source string `json:"source"`
	// Trigger is PreCompact's discriminator: manual|auto.
	Trigger        string          `json:"trigger"`
	ToolName       string          `json:"tool_name"`
	ToolUseID      core.ToolUseID  `json:"tool_use_id"`
	ToolInput      json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage `json:"tool_response"`
	Prompt         string          `json:"prompt"`
	StopHookActive bool            `json:"stop_hook_active"`
	// Extra holds every top-level key the struct tags above do not claim, so a host-side field
	// addition never loses data even before this package is updated to name it explicitly. nil
	// when the payload contributed no unclaimed keys — not an empty, allocated map.
	Extra map[string]json.RawMessage `json:"-"`
}

// claimedKeys is the set of JSON keys Event's own struct tags claim, built once by reflection so
// it can never silently drift from the struct above (a hand-maintained duplicate list could rename
// a tag without updating this set, which would silently start routing a real field into Extra).
var claimedKeys = buildClaimedKeys()

func buildClaimedKeys() map[string]bool {
	t := reflect.TypeOf(Event{})
	keys := make(map[string]bool, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if idx := strings.IndexByte(tag, ','); idx >= 0 {
			tag = tag[:idx]
		}
		if tag != "" {
			keys[tag] = true
		}
	}
	return keys
}

// jsonNullLiteral is what json.RawMessage.UnmarshalJSON stores verbatim when a claimed field's
// value is the JSON literal null: encoding/json calls RawMessage's own UnmarshalJSON with the
// literal bytes "null" rather than leaving the field at its slice zero value (nil), because
// RawMessage implements json.Unmarshaler. ReadEvent normalizes that back to nil so a null
// tool_input/tool_response reads the same as a missing one.
const jsonNullLiteral = "null"

func normalizeRawNull(m json.RawMessage) json.RawMessage {
	if string(m) == jsonNullLiteral {
		return nil
	}
	return m
}

// ReadEvent reads at most limit bytes from r and parses them as an Event.
//
// It reads limit+1 bytes so it can distinguish "the payload is exactly limit bytes" from "the
// payload is larger than limit": if more than limit bytes are available, it returns core.ErrBudget
// together with the first limit bytes read, without ever holding more than limit+1 bytes in
// memory. This is the hot-path payload-size guardrail (runtime.hotPath.maxPayloadBytes).
//
// On malformed JSON it returns a zero Event, the raw bytes actually read, and a wrapped error — so
// a hook subcommand can log the offending payload and still exit 0 (00-ARCHITECTURE.md §2.3)
// rather than propagate a decode failure.
//
// The payload is unmarshalled twice: once into Event's typed fields, once into a raw key/value map
// so every key Event's struct tags do not claim lands in Extra. A null or missing value for any
// claimed field never panics, because every Event field is a value type with a usable zero.
func ReadEvent(r io.Reader, limit int64) (Event, []byte, error) {
	if limit < 0 {
		limit = 0 // defensive: keeps the raw[:limit] slice below always in bounds
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return Event{}, raw, fmt.Errorf("hookio: ReadEvent: read: %w", err)
	}
	if int64(len(raw)) > limit {
		truncated := raw[:limit]
		return Event{}, truncated, fmt.Errorf("%w: hook payload exceeds %d byte limit", core.ErrBudget, limit)
	}

	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return Event{}, raw, fmt.Errorf("hookio: ReadEvent: malformed JSON: %w", err)
	}
	ev.ToolInput = normalizeRawNull(ev.ToolInput)
	ev.ToolResponse = normalizeRawNull(ev.ToolResponse)

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err == nil {
		var extra map[string]json.RawMessage
		for k, v := range fields {
			if claimedKeys[k] {
				continue
			}
			if extra == nil {
				extra = make(map[string]json.RawMessage, len(fields))
			}
			extra[k] = v
		}
		ev.Extra = extra
	}

	return ev, raw, nil
}
