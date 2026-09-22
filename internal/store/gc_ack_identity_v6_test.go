package store

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

func TestGCAcknowledgement_V6_InvalidIdentityNeverReleases(t *testing.T) {
	valid := obsIDText("valid acknowledgement identity")
	for _, tc := range []struct {
		name  string
		lease any
		ack   string
	}{
		{"zero", core.Hash{}.String(), core.Hash{}.String()},
		{"bare", strings.TrimPrefix(valid, "sha256:"), strings.TrimPrefix(valid, "sha256:")},
		{"uppercase", "sha256:" + strings.ToUpper(strings.TrimPrefix(valid, "sha256:")), "sha256:" + strings.ToUpper(strings.TrimPrefix(valid, "sha256:"))},
		{"explicit_empty", "", valid},
		{"explicit_null", nil, valid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tp := newTestStore(t)
			root := gcSeed(t, tp, "src/pending.txt", "pending data must survive an invalid acknowledgement\n")
			nonce := deliveryNonce("7")
			writeJSONLLines(t, paths.Of(tp.Root).State, deliveryLeaseFile,
				map[string]any{"v": 1, "delivery": nonce, "request": root.Hash.String(), "observation_id": tc.lease})
			writeJSONLLines(t, paths.Of(tp.Root).State, deliveryAckFile,
				map[string]any{"v": 1, "delivery": nonce, "observation_id": tc.ack})
			rep, err := tp.Store.GC(context.Background(), forceCollect)
			require.NoError(t, err)
			require.False(t, rep.RetentionRootsError, "invalid ACKs conservatively retain the lease")
			_, err = tp.Store.GetRoot(context.Background(), root.Hash)
			require.NoError(t, err, "malformed identities are not legacy absence or a valid identity join")
		})
	}
}
