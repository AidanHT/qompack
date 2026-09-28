package ipc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClaimSpawn_AnnouncesAndReleases pins the claim protocol EnsureRunning and lazy spawn share:
// the first caller claims, a second finds the spawn in flight, and a release lets the next claim.
func TestClaimSpawn_AnnouncesAndReleases(t *testing.T) {
	root := t.TempDir()
	clk := &fakeSpawnClock{now: time.Now()}

	first, claim := ClaimSpawn(root, clk)
	require.Equal(t, SpawnClaimed, claim)
	require.NotNil(t, first)

	second, claim := ClaimSpawn(root, clk)
	require.Equal(t, SpawnInFlight, claim, "a claim inside the freshness window is a spawn in flight")
	require.Nil(t, second)

	first.Release()
	third, claim := ClaimSpawn(root, clk)
	require.Equal(t, SpawnClaimed, claim, "a released claim can be taken again")
	third.Release()
}

// TestClaimSpawn_ReleaseLeavesAnotherClaimAlone: a claimant whose lock went stale and was reclaimed
// by someone else must not delete the newer claim when it releases.
func TestClaimSpawn_ReleaseLeavesAnotherClaimAlone(t *testing.T) {
	root := t.TempDir()
	clk := &fakeSpawnClock{now: time.Now()}

	stale, claim := ClaimSpawn(root, clk)
	require.Equal(t, SpawnClaimed, claim)

	clk.mu.Lock()
	clk.now = clk.now.Add(spawnLockStaleAfter + time.Second)
	clk.mu.Unlock()
	fresh, claim := ClaimSpawn(root, clk)
	require.Equal(t, SpawnClaimed, claim, "a stale claim is reclaimed")

	stale.Release()
	_, claim = ClaimSpawn(root, clk)
	require.Equal(t, SpawnInFlight, claim, "the old claimant's release must not remove the new claim")
	fresh.Release()
}
