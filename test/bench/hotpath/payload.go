package main

import (
	"encoding/json"
	"fmt"
	"math/rand"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
)

// warmToolKinds are round-robined across the warm-up mix, deterministically, per task-7-spec.md
// step 3: "mixing Read, Bash, Grep, Edit payloads of 4KB-256KB".
var warmToolKinds = [...]string{"Read", "Bash", "Grep", "Edit"}

// minWarmPayloadBytes and maxWarmPayloadBytes bound each warm-up event's synthesized tool-output
// body (task-7-spec.md step 3).
const (
	minWarmPayloadBytes = 4 * 1024
	maxWarmPayloadBytes = 256 * 1024
)

// warmPayloadMeanBytes centers the (right-skewed) size distribution well below the range's
// midpoint, so that 2000 events land near the "~40 MB in total" the spec names: an exponential
// distribution with this mean, clamped into [min,max], averages close to 20 KB/event once the
// clamp's own upward bias off the floor is folded in.
const warmPayloadMeanBytes = 16 * 1024

// payloadAlphabet is the character set randomText draws from — plain ASCII, so the generated
// bodies never collide with JSON's own escaping and stay cheap to encode.
const payloadAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 \n\t.,(){}[]_-"

// payloadGen produces deterministic, seeded synthetic tool-output payloads: the same seed always
// produces the same sequence of events, sizes and bytes, so a bench run is reproducible.
type payloadGen struct {
	rng *rand.Rand
}

// newPayloadGen returns a generator seeded at seed (task-7-spec.md step 3: "seeded at 1").
func newPayloadGen(seed int64) *payloadGen {
	return &payloadGen{rng: rand.New(rand.NewSource(seed))} //nolint:gosec // deterministic synthetic bench payload, not a security primitive
}

// nextSize draws one payload size, clamped into [minWarmPayloadBytes, maxWarmPayloadBytes].
func (g *payloadGen) nextSize() int {
	size := minWarmPayloadBytes + int(g.rng.ExpFloat64()*warmPayloadMeanBytes)
	if size > maxWarmPayloadBytes {
		size = maxWarmPayloadBytes
	}
	return size
}

// randomText returns n deterministic bytes drawn from payloadAlphabet.
func (g *payloadGen) randomText(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = payloadAlphabet[g.rng.Intn(len(payloadAlphabet))]
	}
	return string(b)
}

// jsonString marshals s as a JSON string literal — the helper every case in next() uses to build
// its ToolResponse.
func jsonString(kv map[string]string) json.RawMessage {
	b, err := json.Marshal(kv)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// next returns the i-th deterministic warm-up event for sessionID rooted at cwd: tool kind
// round-robins across warmToolKinds, size is drawn from nextSize.
func (g *payloadGen) next(i int, sessionID core.SessionID, cwd string) hookio.Event {
	kind := warmToolKinds[i%len(warmToolKinds)]
	body := g.randomText(g.nextSize())

	var toolInput, toolResponse json.RawMessage
	switch kind {
	case "Read":
		toolInput = jsonString(map[string]string{"file_path": "src/pkg/file.go"})
		toolResponse = jsonString(map[string]string{"content": body})
	case "Bash":
		toolInput = jsonString(map[string]string{"command": "go test ./..."})
		toolResponse = jsonString(map[string]string{"stdout": body})
	case "Grep":
		toolInput = jsonString(map[string]string{"pattern": "TODO", "path": "."})
		toolResponse = jsonString(map[string]string{"matches": body})
	case "Edit":
		toolInput = jsonString(map[string]string{"file_path": "src/pkg/file.go", "old_string": "foo", "new_string": "bar"})
		toolResponse = jsonString(map[string]string{"diff": body})
	}

	return hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     sessionID,
		CWD:           cwd,
		ToolName:      kind,
		ToolUseID:     core.ToolUseID(fmt.Sprintf("toolu_warm_%d", i)),
		ToolInput:     toolInput,
		ToolResponse:  toolResponse,
	}
}

// representativeObservePayload is the fixed, moderate-size payload every B-A/B-D spawn sample
// uses (task-7-spec.md step 5: "a representative payload on stdin"). It is deliberately NOT drawn
// from payloadGen: every spawn must see byte-identical stdin so the measured spawn-to-spawn
// variance is host/process cost, not payload-size noise.
func representativeObservePayload(sessionID core.SessionID, cwd string, seq int) []byte {
	body := "package main\n\nfunc main() {\n\tprintln(\"hello from the hot-path bench harness\")\n}\n"
	ev := hookio.Event{
		HookEventName: "PostToolUse",
		SessionID:     sessionID,
		CWD:           cwd,
		ToolName:      "Read",
		ToolUseID:     core.ToolUseID(fmt.Sprintf("toolu_ba_%d", seq)),
		ToolInput:     jsonString(map[string]string{"file_path": "src/main.go"}),
		ToolResponse:  jsonString(map[string]string{"content": body}),
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// checkpointPayload is the fixed PreCompact-shaped payload every B-E spawn sample uses
// (task-7-spec.md step 7).
func checkpointPayload(sessionID core.SessionID, cwd string, seq int) []byte {
	ev := hookio.Event{
		HookEventName: "PreCompact",
		SessionID:     sessionID,
		CWD:           cwd,
		Trigger:       "manual",
	}
	_ = seq // reserved: every checkpoint spawn currently shares one session id, matching handleCheckpoint's per-session history record.
	b, err := json.Marshal(ev)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}
