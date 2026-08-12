package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// BenchmarkHookNoop_InProcess measures one `observe tool` invocation end to end, in process.
//
// It is the wave-0 headroom measurement against budget B-A (§2.4, §11.3: hook p99 < 15 ms). What
// it measures is everything the hook does that is NOT the work the hook will eventually do: read
// and parse stdin, resolve the project root, load the five configuration layers, open the day log,
// append one observation line, write the response. Process spawn is excluded because it is not
// this package's to pay — test/e2e measures that half against the real binary.
//
// Treating this as a floor rather than a target is the point: if the no-op path is already close
// to 15 ms, no amount of care in SP-08's observer will bring the hook inside budget.
func BenchmarkHookNoop_InProcess(b *testing.B) {
	dir := b.TempDir()
	home := b.TempDir()

	payload, err := json.Marshal(map[string]any{
		"session_id": "s-bench",
		"cwd":        dir,
		"tool_name":  "FileRead",
	})
	if err != nil {
		b.Fatal(err)
	}

	cmds := All()
	argv := []string{"qompack", "observe", "tool"}
	ctx := context.Background()

	// io.Discard would let the compiler elide the response encoding, which is part of what the
	// budget has to cover; a real buffer keeps the write honest. Reset rather than reallocate so
	// the allocation counts belong to the hook, not to the harness.
	var out, errw bytes.Buffer

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out.Reset()
		errw.Reset()
		code := Dispatch(ctx, cmds, argv, Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(payload),
			Clock:   testClock(),
			HomeDir: home,
		}, &out, &errw)
		if code != ExitOK {
			b.Fatalf("hook exited %d: %s", code, errw.String())
		}
	}
}
