package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/paths"
)

// secretPayloadText is planted in every field of the hook payload that a careless implementation
// might echo into the observation log. §13 invariant 7 says the log records THAT a hook fired and
// how big it was, never what it contained.
const secretPayloadText = "SUPER-SECRET-PROMPT-TEXT"

// TestHooks_WriteHookLog runs all six hooks against one temp project and checks the observation log
// is complete, correctly labelled, and free of payload content.
func TestHooks_WriteHookLog(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()

	payload, err := json.Marshal(map[string]any{
		"session_id": "s-hooklog",
		"cwd":        dir,
		"tool_name":  "FileRead",
		"prompt":     secretPayloadText,
		"source":     "startup",
	})
	require.NoError(t, err)

	for _, hook := range hookNames {
		var out, errw bytes.Buffer
		code := Dispatch(context.Background(), All(), argvFor(hook), Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(payload),
			Clock:   testClock(),
			HomeDir: home,
		}, &out, &errw)
		require.Equal(t, ExitOK, code, "hook %q: stderr=%s", hook, errw.String())
	}

	l := paths.Of(dir)
	matches, err := filepath.Glob(filepath.Join(l.Logs, "hooks-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1, "the six hooks share one per-day log")

	raw, err := os.ReadFile(matches[0])
	require.NoError(t, err)

	require.NotContains(t, string(raw), secretPayloadText,
		"the hook log must record that a hook fired, never what it carried")

	var hooks []string
	var records []hookRecord
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec hookRecord
		require.NoError(t, json.Unmarshal([]byte(line), &rec), "line %q", line)
		hooks = append(hooks, rec.Hook)
		records = append(records, rec)
	}
	require.NoError(t, sc.Err())

	require.Len(t, records, 6, "one line per hook")
	require.Equal(t,
		[]string{"PostToolUse", "UserPromptSubmit", "Stop", "SessionStart", "PreCompact", "SessionEnd"},
		hooks,
		"each entry point must record the host-facing hook event name, not the subcommand")

	for _, rec := range records {
		require.Equal(t, "s-hooklog", rec.SessionID)
		require.Equal(t, len(payload), rec.Bytes)
		require.False(t, rec.Truncated)
		require.NotZero(t, rec.TS)
	}
}

// TestHooks_RefuseToCreateStoreUnderMissingRoot pins the guard in runHook.
//
// paths.Resolve is faithful to §3.3, whose last resort is "the payload cwd itself" — so a payload
// carrying a path that is not a real directory resolves cleanly to a path that is not a real
// directory. Without the isDir check the hook would MkdirAll a store there, which is how a
// malformed payload turns into a .qompack/ tree in a location no project occupies.
func TestHooks_RefuseToCreateStoreUnderMissingRoot(t *testing.T) {
	parent := t.TempDir()
	missing := filepath.Join(parent, "no-such-project")

	payload, err := json.Marshal(map[string]any{"session_id": "s1", "cwd": missing})
	require.NoError(t, err)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(), argvFor("observe tool"), Env{
		Getenv:  noEnv,
		Stdin:   bytes.NewReader(payload),
		Clock:   testClock(),
		HomeDir: t.TempDir(),
	}, &out, &errw)

	require.Equal(t, ExitOK, code)
	require.NotEmpty(t, out.String(), "the host is still owed a response")

	_, statErr := os.Stat(missing)
	require.True(t, os.IsNotExist(statErr),
		"a hook must not conjure a project root that does not exist, got %v", statErr)
}

// TestHooks_TruncatedPayloadIsRecordedNotFatal covers the §2.3 rule that an oversized payload is
// observed as truncated rather than dropped or fatal. The bootstrap read uses the DEFAULT limit,
// so the effective-limit branch is what marks the record.
func TestHooks_TruncatedPayloadIsRecordedNotFatal(t *testing.T) {
	dir := t.TempDir()

	// The floor on runtime.hotPath.maxPayloadBytes is 4096, so the payload has to clear that to
	// exercise the over-limit branch — a smaller limit would simply be rejected as invalid and
	// fall back to the default, and the test would pass for the wrong reason. The padding goes in
	// a field the observation record does NOT carry, so this stays a size test, not a content one.
	payload, err := json.Marshal(map[string]any{
		"session_id": "s-trunc",
		"cwd":        dir,
		"tool_name":  "FileRead",
		"prompt":     strings.Repeat("x", 5000),
	})
	require.NoError(t, err)
	require.Greater(t, len(payload), 4096)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), All(),
		argvFor("observe tool", "--set", "runtime.hotPath.maxPayloadBytes=4096"), Env{
			Getenv:  noEnv,
			Stdin:   bytes.NewReader(payload),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)
	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	l := paths.Of(dir)
	matches, err := filepath.Glob(filepath.Join(l.Logs, "hooks-*.jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 1)

	raw, err := os.ReadFile(matches[0])
	require.NoError(t, err)

	var rec hookRecord
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(raw), &rec))
	require.True(t, rec.Truncated, "a payload over the effective limit is recorded as truncated")
	require.Equal(t, len(payload), rec.Bytes, "the recorded size is what was actually read")
}
