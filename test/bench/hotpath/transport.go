// Package main is the standalone hot-path bench harness (task-7-spec.md): it measures B-A/B-B/
// B-D/B-E against a REAL daemon child process and REAL spawned `qompack` binaries — the only
// honest way to measure B-A, since anything else hides the host's own process-creation cost
// inside a number the design document names as the headline budget.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
)

// adminPingSessionID and statusSessionID are fixed, harness-owned session ids for the admin/
// status probes this file issues directly (bypassing the hook subcommand surface entirely, the
// same pattern test/e2e's e2eStatus/e2eWaitDaemonUp use).
const (
	adminPingSessionID = core.SessionID("bench-admin-ping")
	statusSessionID    = core.SessionID("bench-status")
)

// probeConnectDeadline and probeAckDeadline bound one admin/status round trip against the
// harness's own client. They are deliberately generous relative to the production hot-path
// deadlines (State.ConnectDeadlineMs's default is 5ms, or 25ms on Windows): this traffic is never
// gated, and a tight deadline here would only make the harness itself flaky under host load.
const (
	probeConnectDeadline   = 2 * time.Second
	probeAckDeadline       = 2 * time.Second
	replyRoundTripDeadline = 5 * time.Second
)

// ipcAddrEnvOverride computes a QOMPACK_IPC_ADDR value that keeps the daemon's endpoint anchored
// to this bench run rather than whatever internal/ipc.Resolve would hash the temp project root
// into — task-7-brief.md's binding ruling: "Use QOMPACK_IPC_ADDR + QOMPACK_PROJECT_ROOT to keep
// everything inside the temp project." tag distinguishes concurrent bench runs (a random suffix,
// not derived from the project path, so it never has to worry about the Unix sun_path length
// guard the way a path-derived name would).
func ipcAddrEnvOverride(tag string) (string, error) {
	suffix, err := randomHex(8)
	if err != nil {
		return "", err
	}
	name := "qompack-bench-" + tag + "-" + suffix
	if runtime.GOOS == "windows" {
		return `pipe:\\.\pipe\` + name, nil
	}
	// os.TempDir() + this short, path-independent name comfortably fits inside the 100-byte
	// sun_path guard on every platform this harness runs on.
	return "unix:" + tempSocketPath(name), nil
}

// tempSocketPath builds a short, deterministic-length Unix socket path under os.TempDir() — kept
// independent of the (potentially long) temp project root specifically so it stays comfortably
// under the sun_path guard internal/ipc.parseIPCAddrOverride itself enforces on any unix: value.
func tempSocketPath(name string) string {
	return filepath.Join(os.TempDir(), name+".sock")
}

// randomHex returns n random bytes, hex-encoded — used only to keep concurrent bench runs (e.g.
// a local run alongside CI) from colliding on one named pipe or socket path.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("hotpath: random suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// newProbeClient returns a real ipc.Client configured for the harness's own out-of-band admin/
// status/warm-up traffic. It deliberately reuses internal/ipc's own production Client rather than
// this package dialing the transport itself: 00-ARCHITECTURE.md §3.2/D10 permits importing "net"
// only inside internal/ipc, mechanically enforced repo-wide by
// test/guards.TestGuard_NoNetworkImports, and this harness — scoped to test/bench, devtool and CI
// only — must not touch internal/ipc's shipped surface to work around that. Every call this
// client makes therefore reconnects per request rather than holding one socket open, which is
// arguably the MORE faithful shape anyway: every real hook invocation is its own fresh process
// making exactly one connection, and that is what this same client type does in production.
func newProbeClient(addr ipc.Addr, spool ipc.SpoolWriter, connectDeadline time.Duration) ipc.Client {
	return ipc.NewClientWithOptions(addr, spool, logging.Nop(), obs.New(core.SystemClock()), ipc.ClientOptions{
		ConnectDeadline: connectDeadline,
		AckDeadline:     probeAckDeadline,
	})
}

// errDaemonNeverCameUp is waitForDaemon's error when no admin.ping ever ACKed within bound.
var errDaemonNeverCameUp = errors.New("hotpath: daemon never became reachable")

// waitForDaemon polls addr with a real admin.ping round trip (task-7-spec.md step 2: "wait for
// admin.ping") until one ACKs or ctx/bound expires. time.Sleep between attempts is permitted in
// this directory only (devtool lint's test/bench exemption).
func waitForDaemon(ctx context.Context, addr ipc.Addr, spool ipc.SpoolWriter, bound, tick time.Duration) error {
	c := newProbeClient(addr, spool, probeConnectDeadline)
	defer func() { _ = c.Close() }()

	deadline := time.Now().Add(bound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		req := ipc.Request{Op: ipc.OpAdminPing, Session: adminPingSessionID, TS: core.NowMilli(core.SystemClock())}
		resp, _ := c.Send(ctx, req, probeAckDeadline) // Client.Send never returns a propagating error.
		if resp.OK {
			return nil
		}
		if time.Now().After(deadline) {
			return errDaemonNeverCameUp
		}
		time.Sleep(tick)
	}
}

// shutdownDaemon best-effort asks the daemon to stop (admin.shutdown, fire-and-forget). A failure
// here is not fatal to the bench run: process.go's own teardown falls back to killing the child
// process outright after a bounded wait.
func shutdownDaemon(addr ipc.Addr, spool ipc.SpoolWriter) {
	c := newProbeClient(addr, spool, probeConnectDeadline)
	defer func() { _ = c.Close() }()
	req := ipc.Request{Op: ipc.OpAdminShutdown, Session: adminPingSessionID, TS: core.NowMilli(core.SystemClock())}
	_, _ = c.Send(context.Background(), req, probeAckDeadline)
}
