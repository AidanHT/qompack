package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStatusSource_NoDaemonNamesTheReason is the Phase 4 live lane's F-UAT03-3 / F-C49-1: with no
// daemon listening, `qompack status` printed "daemon: status refused: " with nothing after the
// colon. The client answers an undelivered request with OK false and no error text, and the status
// source quoted that empty text as the daemon's refusal. Nothing refused it: nothing received it.
func TestStatusSource_NoDaemonNamesTheReason(t *testing.T) {
	root := mcpCmdRoot(t)
	client := mcpCmdOfflineClient(t, root)
	t.Cleanup(func() { _ = client.Close() })

	_, _, err := fetchDaemonStatus(context.Background(), client)
	require.Error(t, err)
	msg := err.Error()
	require.False(t, strings.HasSuffix(strings.TrimSpace(msg), ":"), "the reason must not be empty: %q", msg)
	require.NotContains(t, msg, "refused", "no daemon received the request, so none refused it: %q", msg)
	require.Contains(t, msg, "no daemon", "the reason must say that no daemon answered: %q", msg)
}
