package ipc_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/ipc"
)

// wantOps is every op KnownOps must report: the eight §5.4 operations plus the five admin.*
// operations this commit adds.
var wantOps = []ipc.Op{
	ipc.OpObserveTool, ipc.OpObservePrompt, ipc.OpObserveStop, ipc.OpSessionStart,
	ipc.OpCheckpoint, ipc.OpFlush, ipc.OpStatus, ipc.OpMCP,
	ipc.OpAdminPing, ipc.OpAdminDrain, ipc.OpAdminReload, ipc.OpAdminIdle, ipc.OpAdminShutdown,
}

// TestKnownOps_ListsAllThirteenSorted asserts KnownOps is the complete, sorted vocabulary — the set
// self-test and status render against.
func TestKnownOps_ListsAllThirteenSorted(t *testing.T) {
	ops := ipc.KnownOps()
	require.Len(t, ops, 13)
	require.True(t, sort.SliceIsSorted(ops, func(i, j int) bool { return ops[i] < ops[j] }))
	for _, o := range wantOps {
		require.Contains(t, ops, o)
	}
}

// TestKnownOps_ReturnsAFreshSlice asserts a caller mutating the returned slice cannot corrupt a
// later call's result.
func TestKnownOps_ReturnsAFreshSlice(t *testing.T) {
	ops := ipc.KnownOps()
	ops[0] = ipc.Op("tampered")
	require.NotEqual(t, ipc.Op("tampered"), ipc.KnownOps()[0])
}

// TestAdminOps_SpellingsAreConsistentWithThePrefix pins the five admin.* wire spellings and asserts
// each is built from OpAdminPrefix rather than a hand-respelled literal.
func TestAdminOps_SpellingsAreConsistentWithThePrefix(t *testing.T) {
	require.Equal(t, ipc.Op("admin.ping"), ipc.OpAdminPing)
	require.Equal(t, ipc.Op("admin.drain"), ipc.OpAdminDrain)
	require.Equal(t, ipc.Op("admin.reload"), ipc.OpAdminReload)
	require.Equal(t, ipc.Op("admin.idle"), ipc.OpAdminIdle)
	require.Equal(t, ipc.Op("admin.shutdown"), ipc.OpAdminShutdown)

	admin := []ipc.Op{ipc.OpAdminPing, ipc.OpAdminDrain, ipc.OpAdminReload, ipc.OpAdminIdle, ipc.OpAdminShutdown}
	for _, o := range admin {
		require.True(t, strings.HasPrefix(string(o), ipc.OpAdminPrefix), "%s must start with %q", o, ipc.OpAdminPrefix)
	}
}

// TestOp_Valid asserts every KnownOps entry validates and an arbitrary/empty Op does not.
func TestOp_Valid(t *testing.T) {
	for _, o := range ipc.KnownOps() {
		require.True(t, o.Valid(), "%s must be valid", o)
	}
	require.False(t, ipc.Op("bogus").Valid())
	require.False(t, ipc.Op("").Valid())
	require.False(t, ipc.Op(ipc.OpAdminPrefix).Valid(), "the bare prefix is not itself an op")
}

// TestOp_HotPath pins §8.1's three hot-path ops — the ones that flow through the spool submode —
// and asserts every other known op is not one of them.
func TestOp_HotPath(t *testing.T) {
	hot := map[ipc.Op]bool{
		ipc.OpObserveTool:   true,
		ipc.OpObservePrompt: true,
		ipc.OpObserveStop:   true,
	}
	for _, o := range ipc.KnownOps() {
		require.Equal(t, hot[o], o.HotPath(), "%s", o)
	}
}
