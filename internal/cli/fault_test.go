//go:build !noinject

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// resetFaultState clears fault.go's memoized parse so each test gets an independent read of
// QOMPACK_FAULT via t.Setenv.
func resetFaultState(t *testing.T) {
	t.Helper()
	faultOnce = sync.Once{}
	faultMap = nil
	t.Cleanup(func() {
		faultOnce = sync.Once{}
		faultMap = nil
	})
}

func TestFaultActive_UnsetIsInertForAllElevenSites(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")

	for _, site := range allFaultSites {
		arg, on := faultActive(site)
		require.False(t, on, "site %q must be inactive when QOMPACK_FAULT is unset", site)
		require.Empty(t, arg)
	}
}

func TestFaultActive_ParsesBareSiteNames(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "daemon-down,stdin-eof")

	_, on := faultActive(faultDaemonDown)
	require.True(t, on)
	_, on = faultActive(faultStdinEOF)
	require.True(t, on)
	_, on = faultActive(faultSpoolFull)
	require.False(t, on)
}

// TestFaultActive_PanicSiteNamesEmbedTheirOwnColon pins the parser's most delicate case: the site
// names "panic:hook" and "panic:client" both contain a colon that is part of the NAME, not a
// site:arg separator, and must be recognized as whole tokens before any generic split is tried.
func TestFaultActive_PanicSiteNamesEmbedTheirOwnColon(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "panic:hook")

	arg, on := faultActive(faultPanicHook)
	require.True(t, on)
	require.Empty(t, arg)

	_, on = faultActive(faultPanicClient)
	require.False(t, on)
}

func TestFaultActive_ParsesSiteWithArgument(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "spool-full:3")

	arg, on := faultActive(faultSpoolFull)
	require.True(t, on)
	require.Equal(t, "3", arg)
}

func TestFaultActive_UnknownTokensAreIgnored(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "not-a-real-site,daemon-down")

	_, on := faultActive(faultDaemonDown)
	require.True(t, on, "a following valid token must still be recognized")
	_, on = faultActive("not-a-real-site")
	require.False(t, on)
}

func TestFaultStdin_EOFAndGarbage(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultStdinEOF)
	b, err := io.ReadAll(faultStdin(errAlwaysReader{}))
	require.NoError(t, err)
	require.Empty(t, b)

	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultStdinGarbage)
	b, err = io.ReadAll(faultStdin(errAlwaysReader{}))
	require.NoError(t, err)
	require.Equal(t, "{{{not json", string(b))

	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")
	r := faultStdin(strings.NewReader("hi"))
	b, err = io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, "hi", string(b))
}

type errAlwaysReader struct{}

func (errAlwaysReader) Read([]byte) (int, error) {
	return 0, errors.New("should never be read under stdin fault injection")
}

func TestFaultInflateToolResponse_OnlyWhenActive(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultOversize)
	ev := hookio.Event{}
	faultInflateToolResponse(&ev)
	require.Greater(t, len(ev.ToolResponse), faultOversizeBytes)

	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")
	ev2 := hookio.Event{}
	faultInflateToolResponse(&ev2)
	require.Empty(t, ev2.ToolResponse)
}

// fakeSpool is a minimal in-memory ipc.SpoolWriter for exercising wrapFaultSpool without touching
// the filesystem.
type fakeSpool struct{ n int }

func (f *fakeSpool) Append(ipc.Request) error { f.n++; return nil }
func (f *fakeSpool) Path() string             { return "fake" }

func TestWrapFaultSpool_SpoolFullFailsAfterAllowance(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "spool-full:2")

	inner := &fakeSpool{}
	sp := wrapFaultSpool(inner)

	require.NoError(t, sp.Append(ipc.Request{}))
	require.NoError(t, sp.Append(ipc.Request{}))
	err := sp.Append(ipc.Request{})
	require.ErrorIs(t, err, ipc.ErrSpoolFull)
	require.Equal(t, 2, inner.n, "the wrapped spool must have received exactly the allowed writes")
}

func TestWrapFaultSpool_SpoolFullDefaultAllowanceIsOne(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultSpoolFull)

	sp := wrapFaultSpool(&fakeSpool{})
	require.NoError(t, sp.Append(ipc.Request{}))
	require.ErrorIs(t, sp.Append(ipc.Request{}), ipc.ErrSpoolFull)
}

func TestWrapFaultSpool_DiskFullFailsFromTheFirstWrite(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultDiskFull)

	inner := &fakeSpool{}
	sp := wrapFaultSpool(inner)
	err := sp.Append(ipc.Request{})
	require.Error(t, err)
	require.Equal(t, 0, inner.n, "disk-full must never reach the real spool at all")
}

func TestWrapFaultSpool_InertWhenUnset(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")
	inner := &fakeSpool{}
	sp := wrapFaultSpool(inner)
	require.Same(t, ipc.SpoolWriter(inner), sp)
}

// fakeClient is a minimal ipc.Client for exercising wrapFaultClient.
type fakeClient struct{}

func (fakeClient) Send(context.Context, ipc.Request, time.Duration) (ipc.Response, error) {
	return ipc.Response{OK: true}, nil
}
func (fakeClient) Close() error { return nil }

func TestWrapFaultClient_PanicClientPanics(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultPanicClient)

	c := wrapFaultClient(fakeClient{})
	require.Panics(t, func() {
		_, _ = c.Send(context.Background(), ipc.Request{}, time.Second)
	})
}

func TestWrapFaultClient_InertWhenUnset(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")
	c := wrapFaultClient(fakeClient{})
	resp, err := c.Send(context.Background(), ipc.Request{}, time.Second)
	require.NoError(t, err)
	require.True(t, resp.OK)
}

// TestFaultDaemonDownAddr_Inactive covers fix round 2's Minor N-4: faultDaemonDownAddr had no
// direct unit test of its own, only the e2e/66-combo coverage of the site as a whole.
func TestFaultDaemonDownAddr_Inactive(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "")

	in := ipc.Addr{Kind: ipc.UnixSocket, Path: "/tmp/qompack.sock"}
	got := faultDaemonDownAddr(in)
	require.Equal(t, in, got, "unset QOMPACK_FAULT must leave the address completely unchanged")
}

func TestFaultDaemonDownAddr_Active(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultDaemonDown)

	in := ipc.Addr{Kind: ipc.UnixSocket, Path: "/tmp/qompack.sock"}
	got := faultDaemonDownAddr(in)

	require.Equal(t, in.Kind, got.Kind, "the fault mutates the path, never the address Kind")
	require.Equal(t, in.Path+faultDaemonDownAddrSuffix, got.Path,
		"the suffix is appended, not a wholesale replacement, so the result still looks like a "+
			"plausible endpoint of the same kind rather than failing for an unrelated reason")
	require.NotEqual(t, in.Path, got.Path, "the resulting address must differ from the real one")
}

func TestFaultDaemonDownAddr_ActiveAmongOtherSites(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, "stdin-eof,daemon-down,disk-full")

	in := ipc.Addr{Kind: ipc.UnixSocket, Path: "/tmp/qompack.sock"}
	got := faultDaemonDownAddr(in)
	require.Equal(t, in.Path+faultDaemonDownAddrSuffix, got.Path)
}

func TestFaultCorruptStateIfNeeded_OverwritesWithBadRecord(t *testing.T) {
	resetFaultState(t)
	dir := t.TempDir()
	require.NoError(t, ipc.WriteState(dir, ipc.State{AckDeadlineMs: 5}))

	before := ipc.ReadState(dir, config.Defaults())
	require.EqualValues(t, 5, before.AckDeadlineMs)

	t.Setenv(qompackFaultEnv, faultStateCorrupt)
	faultCorruptStateIfNeeded(dir)

	after := ipc.ReadState(dir, config.Defaults())
	require.NotEqual(t, uint16(5), after.AckDeadlineMs,
		"a corrupted state.bin must fail its CRC and fall back to config defaults")

	b, err := os.ReadFile(ipc.StatePath(dir))
	require.NoError(t, err)
	require.Len(t, b, faultCorruptStateBytes)
}

func TestFaultCorruptConfigIfNeeded_WritesUnparseableJSON(t *testing.T) {
	resetFaultState(t)
	dir := t.TempDir()
	t.Setenv(qompackFaultEnv, faultConfigCorrupt)

	faultCorruptConfigIfNeeded(dir)

	p := filepath.Join(paths.Of(dir).Dot, "config.json")
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	var v any
	require.Error(t, json.Unmarshal(b, &v), "the written config.json must not parse")
}

// TestFaultCorruptConfigIfNeeded_NeverConjuresAMissingRoot pins fix round 1's Minor M-7: a root
// that does not exist as a directory must never have .qompack/ created under it by a fault
// injection, undermining hookclient.go's own isDir(root) refuse-to-conjure-a-store guard.
func TestFaultCorruptConfigIfNeeded_NeverConjuresAMissingRoot(t *testing.T) {
	resetFaultState(t)
	parent := t.TempDir()
	missing := filepath.Join(parent, "no-such-project")
	t.Setenv(qompackFaultEnv, faultConfigCorrupt)

	faultCorruptConfigIfNeeded(missing)

	_, err := os.Stat(missing)
	require.True(t, os.IsNotExist(err), "faultCorruptConfigIfNeeded must not create a root that does not exist")
}

// TestSelfTest_ConfigCorruptFallsBackAndExitsZero exercises the config-corrupt fault site for
// real, against the one subcommand task-6-spec.md's fault table names for it (fix round 1, Minor
// M-12: the site was previously inert for every hook subcommand, and nothing pinned it against
// either qompack daemon or self-test). self-test itself now calls faultCorruptConfigIfNeeded
// before its own config.Load (selftest.go), so QOMPACK_FAULT=config-corrupt genuinely replaces
// the project's config.json with truncated JSON here — config.Load's own per-leaf
// fallback-not-crash contract (§11.3) means this must still exit 0, never a critical config.load
// check.
func TestSelfTest_ConfigCorruptFallsBackAndExitsZero(t *testing.T) {
	resetFaultState(t)
	t.Setenv(qompackFaultEnv, faultConfigCorrupt)

	dir := t.TempDir()
	t.Cleanup(contract.ResetProducers)

	var out, errw bytes.Buffer
	code := Dispatch(context.Background(), []Cmd{{Name: "self-test", Run: runSelfTest}},
		[]string{"qompack", "self-test", "--json"}, Env{
			Getenv:  envWith(map[string]string{"QOMPACK_PROJECT_ROOT": dir}),
			Stdin:   bytes.NewReader(nil),
			Clock:   testClock(),
			HomeDir: t.TempDir(),
		}, &out, &errw)

	require.Equal(t, ExitOK, code, "stderr=%s", errw.String())

	// The fault genuinely fired: the project's config.json is the exact truncated bytes fault.go
	// writes, not something config.Load or self-test itself produced.
	b, err := os.ReadFile(filepath.Join(paths.Of(dir).Dot, "config.json"))
	require.NoError(t, err)
	require.Equal(t, `{"store":{"chunk":{"min":`, string(b))
}
