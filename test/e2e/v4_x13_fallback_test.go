// The specification of how TestV4_HotPathUnchangedWithTheFullWave3ResidentSet treats the hook's
// client fallback spool: taken out of the write-set equality, and asserted per arm instead. Both
// halves are pinned here without a daemon, so a change to either shows up as a failing unit rather
// than as a row that quietly stopped seeing something.
package e2e

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
)

// TestV4_X13DaemonWriteSetSetsAsideOnlyTheHookFallback proves the equality lost exactly one token.
// Every pair x13v4ConflatedPairs names is planted and walked by the real x13v4WriteSet: the client
// fallback spool is the one planted file x13v4DaemonWriteSet no longer tells apart, and every other
// planted file (a blob, LOUD.log, a hook-quiet record, a set-aside draft) still makes the daemon
// write sets differ.
func TestV4_X13DaemonWriteSetSetsAsideOnlyTheHookFallback(t *testing.T) {
	for _, p := range x13v4ConflatedPairs {
		t.Run(p.name, func(t *testing.T) {
			baseline := x13v4WriteSet(t, x13v4PlantTree(t, p.baseline), nil)
			planted := x13v4WriteSet(t, x13v4PlantTree(t, p.baseline, p.planted), nil)
			require.NotEqual(t, baseline, planted, "x13v4WriteSet itself still sees %s", p.planted)

			if x13v4Normalize(p.planted) == x13v4HookFallbackToken {
				require.Equal(t, x13v4DaemonWriteSet(baseline), x13v4DaemonWriteSet(planted),
					"the client fallback spool is the hook's, and is compared per arm instead")
				return
			}
			require.NotEqual(t, x13v4DaemonWriteSet(baseline), x13v4DaemonWriteSet(planted),
				"%s is the daemon's (%s) and must still fail the equality\nbaseline: %v\nplanted:  %v",
				p.planted, p.meaning, baseline, planted)
		})
	}
}

// x13v4TestReq builds one request as a hook would send it.
func x13v4TestReq(op ipc.Op, sess core.SessionID, id core.ToolUseID, nonce string) ipc.Request {
	req := ipc.Request{Op: op, Session: sess, Nonce: nonce}
	if id != "" {
		req.Event = &hookio.Event{SessionID: sess, ToolUseID: id}
	}
	return req
}

// x13v4TestWAL is the WAL of an arm whose setup prompt and burst were all made durable live, less
// the burst indices in drop.
func x13v4TestWAL(sess core.SessionID, drop ...int) []ipc.Request {
	wal := []ipc.Request{x13v4TestReq(ipc.OpObservePrompt, sess, "", "prompt-nonce")}
	dropped := map[int]bool{}
	for _, i := range drop {
		dropped[i] = true
	}
	for i := range x13v4Turns {
		if !dropped[i] {
			wal = append(wal, x13v4TestReq(ipc.OpObserveTool, sess, x13v4BurstID(i), fmt.Sprintf("nonce-%d", i)))
		}
	}
	return wal
}

// TestV4_X13ClassifyFallback pins what x13v4ClassifyFallback accepts as a timing fallback and what it
// refuses. A late ACK and a delivery that never reached the daemon are timing. A delivery the
// daemon received and did not make durable is a refusal, and a spooled line that is not one of the
// arm's burst deliveries breaks the premise on which the token left the equality.
func TestV4_X13ClassifyFallback(t *testing.T) {
	const sess = x13v4RefSession
	last := x13v4Turns - 1
	lastReq := x13v4TestReq(ipc.OpObserveTool, sess, x13v4BurstID(last), fmt.Sprintf("nonce-%d", last))
	everything := int64(1 + x13v4Turns) // the setup prompt and the whole burst, received live

	t.Run("no fallback", func(t *testing.T) {
		f, err := x13v4ClassifyFallback(sess, nil, x13v4TestWAL(sess), everything)
		require.NoError(t, err)
		require.Empty(t, f.LateACK)
		require.Empty(t, f.NeverLive)
		require.Equal(t, 1+x13v4Turns, f.Durable)
	})
	t.Run("a late ACK is timing", func(t *testing.T) {
		f, err := x13v4ClassifyFallback(sess, []ipc.Request{lastReq}, x13v4TestWAL(sess), everything)
		require.NoError(t, err)
		require.Equal(t, []core.ToolUseID{x13v4BurstID(last)}, f.LateACK)
		require.Empty(t, f.NeverLive)
	})
	t.Run("a delivery that never reached the daemon is timing", func(t *testing.T) {
		f, err := x13v4ClassifyFallback(sess, []ipc.Request{lastReq}, x13v4TestWAL(sess, last), everything-1)
		require.NoError(t, err)
		require.Empty(t, f.LateACK)
		require.Equal(t, []core.ToolUseID{x13v4BurstID(last)}, f.NeverLive)
	})
	t.Run("a delivery the daemon received and did not make durable is a refusal", func(t *testing.T) {
		_, err := x13v4ClassifyFallback(sess, []ipc.Request{lastReq}, x13v4TestWAL(sess, last), everything)
		require.ErrorContains(t, err, "refused a delivery before making it durable")
	})
	for _, tc := range []struct {
		name string
		req  ipc.Request
	}{
		{"another op", x13v4TestReq(ipc.OpObservePrompt, sess, "", "n")},
		{"another session", x13v4TestReq(ipc.OpObserveTool, x13v4Session, x13v4BurstID(0), "n")},
		{"a tool use outside the burst", x13v4TestReq(ipc.OpObserveTool, sess, "toolu_elsewhere", "n")},
		{"no event at all", x13v4TestReq(ipc.OpObserveTool, sess, "", "n")},
	} {
		t.Run("a spooled line from "+tc.name+" breaks the premise", func(t *testing.T) {
			_, err := x13v4ClassifyFallback(sess, []ipc.Request{tc.req}, x13v4TestWAL(sess), everything)
			require.ErrorContains(t, err, "not one of this arm's burst deliveries")
		})
	}
}
