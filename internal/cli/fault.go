//go:build !noinject

// Package cli's fault-injection seam. QOMPACK_FAULT is a comma-separated list of "site[:arg]"
// tokens naming task-6-spec.md's eleven fault sites; faultActive(site) is checked at each named
// location in hookclient.go/sessionstart.go and reports whether that site is active and what
// argument (if any) it carries.
//
// The grep-able literal "QOMPACK_FAULT" is deliberately confined to exactly two non-test files —
// this one (fault.go, `const qompackFaultEnv`, where every fault check reads it) and
// internal/daemon/spawn.go (`const qompackFaultEnv`, buildSpawnEnv, which strips it from a spawned
// daemon's environment before exec — a hook process testing its own fault paths must never make
// the daemon it lazily starts fault too). Fix round 1's fault_noinject.go (this package's
// `noinject`-tagged twin) intentionally spells the same constant name without the literal string,
// so `-tags noinject` compiles the whole seam out without also removing that grep's one legitimate
// second hit. Every other occurrence — internal/daemon/spawn_test.go, test/e2e/faultinject_test.go
// — is test code, which the security CI job's grep does not count. This package never
// re-implements spawn.go's stripping; it only ever reads the variable, never propagates it.
package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/hookio"
	"github.com/qompack/qompack/internal/ipc"
	"github.com/qompack/qompack/internal/paths"
)

// dirPermFault and filePermFault are the permissions fault.go's own injected writes use — the
// same owner-only modes every other file under .qompack/ is written with.
const (
	dirPermFault  = 0o700
	filePermFault = 0o600
)

// qompackFaultEnv is the fault-injection environment variable. See the package doc above for why
// this exact literal may appear nowhere else outside a _test.go file.
const qompackFaultEnv = "QOMPACK_FAULT"

// faultSiteSet is allFaultSites (faultsites.go) as a set, built once for loadFaultMap's token
// recognition.
var faultSiteSet = func() map[string]bool {
	m := make(map[string]bool, len(allFaultSites))
	for _, s := range allFaultSites {
		m[s] = true
	}
	return m
}()

var (
	faultOnce sync.Once
	// faultMap is nil when QOMPACK_FAULT is unset (or set to something that names no recognized
	// site), so the common case — fault injection off — is a single nil-map lookup in faultActive,
	// cheap enough to sit unconditionally on the hot path.
	faultMap map[string]string
)

// faultActive reports whether site is named in QOMPACK_FAULT, and its optional argument.
func faultActive(site string) (arg string, on bool) {
	faultOnce.Do(loadFaultMap)
	if faultMap == nil {
		return "", false
	}
	arg, on = faultMap[site]
	return arg, on
}

// loadFaultMap parses QOMPACK_FAULT once per process. A token that exactly matches a known site
// name (including the two whose own name embeds a colon, "panic:hook" and "panic:client") is
// recognized outright; otherwise the token is split on its FIRST colon into site:arg, and the
// site half is checked against the known set. This order matters — checking the whole token first
// is what keeps "panic:hook" from being misparsed as site="panic" arg="hook".
func loadFaultMap() {
	raw := os.Getenv(qompackFaultEnv)
	if raw == "" {
		return
	}
	m := make(map[string]string)
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if faultSiteSet[tok] {
			m[tok] = ""
			continue
		}
		if site, arg, ok := strings.Cut(tok, ":"); ok && faultSiteSet[site] {
			m[site] = arg
		}
	}
	if len(m) > 0 {
		faultMap = m
	}
}

// faultStdin is the stdin-eof / stdin-garbage injection: it replaces the hook's real stdin with an
// empty reader or a fixed malformed-JSON payload, so hookio.ReadEvent sees exactly what those two
// sites promise.
func faultStdin(r io.Reader) io.Reader {
	if _, on := faultActive(faultStdinEOF); on {
		return strings.NewReader("")
	}
	if _, on := faultActive(faultStdinGarbage); on {
		return strings.NewReader("{{{not json")
	}
	return r
}

// maybePanicHook is the panic:hook injection: task-6-spec.md's table places it "immediately after
// ReadEvent", which is exactly where hookclient.go calls this.
func maybePanicHook() {
	if _, on := faultActive(faultPanicHook); on {
		panic("qompack: fault injection: panic:hook")
	}
}

// faultOversizeBytes is the size task-6-spec.md's "oversize" row specifies for the inflated
// ToolResponse: comfortably over both MaxLineBytes (1 MiB) and any realistic
// runtime.hotPath.maxPayloadBytes, so the client's own externalize()/spool path is guaranteed to
// trigger regardless of configuration. //nomagic:allow fault-injection test fixture size, not a
// budget or a config default (§11.6 exemption).
const faultOversizeBytes = 4 << 20

// faultInflateToolResponse is the "oversize" injection: it overwrites ev.ToolResponse with a
// faultOversizeBytes JSON string, unconditionally of which hook is running — every hook body
// carries a hookio.Event, and Client.Send's own externalize()/threshold logic is already agnostic
// to which Op produced an oversized Event.ToolResponse.
func faultInflateToolResponse(ev *hookio.Event) {
	if _, on := faultActive(faultOversize); on {
		big := make([]byte, faultOversizeBytes+2)
		big[0] = '"'
		for i := 1; i < len(big)-1; i++ {
			big[i] = 'x'
		}
		big[len(big)-1] = '"'
		ev.ToolResponse = big
	}
}

// errFaultDiskFull is the disk-full injection's error, standing in for the platform's real ENOSPC
// (syscall.ENOSPC is POSIX-only; Go's syscall package carries no equivalent constant on Windows,
// so a portable fault site cannot reference it directly and instead reports the same condition
// through an ordinary wrapped error).
//
// Scope note (fix round 1, Minor M-4): the spec's row names three write sites —
// "spool.Append, logQuiet and ipc.WriteState return syscall.ENOSPC from their first write". Only
// spool.Append is wrapped here. ipc.WriteState is genuinely moot for this site: no hook subcommand
// ever calls it (only the daemon does, on its own Run/Stop lifecycle, outside cli's file scope).
// logQuiet is reachable from the hook path (a ReadEvent or ErrAddrTooLong failure both call it)
// but is deliberately NOT wrapped: it already swallows every write error unconditionally
// (paths.AppendJSONL's return value is discarded) — an injected ENOSPC there would be
// observationally identical to the disk already being full for real, which the underlying
// filesystem call, not a fault seam, already exercises just as faithfully.
var errFaultDiskFull = errors.New("qompack: fault disk-full: no space left on device")

// defaultFaultSpoolAllowance is spool-full's "faultArg (default 1)": how many Append calls succeed
// before every subsequent one starts failing, when QOMPACK_FAULT names the site with no explicit
// argument.
const defaultFaultSpoolAllowance = 1

// wrapFaultSpool wraps sp to simulate the spool-full and disk-full sites: both fail Append (never
// Path), spool-full after allowing the configured number of successful writes, disk-full from the
// very first one.
func wrapFaultSpool(sp ipc.SpoolWriter) ipc.SpoolWriter {
	if arg, on := faultActive(faultSpoolFull); on {
		allowed := defaultFaultSpoolAllowance
		if n, err := strconv.Atoi(arg); err == nil && n >= 0 {
			allowed = n
		}
		return &faultSpool{SpoolWriter: sp, allowed: int32(allowed), err: ipc.ErrSpoolFull} //nolint:gosec // allowed is a small, test-supplied count
	}
	if _, on := faultActive(faultDiskFull); on {
		return &faultSpool{SpoolWriter: sp, allowed: 0, err: errFaultDiskFull}
	}
	return sp
}

// faultSpool decorates a real ipc.SpoolWriter, failing Append once more than allowed calls have
// been made. It embeds the SpoolWriter interface so Path() falls through unmodified, and forwards
// Close() explicitly — interface embedding only promotes the embedded INTERFACE's own method set,
// which does not include Close (SpoolWriter has none; the real *spool's Close is reached elsewhere
// only via a type assertion), so without this override the client's own "close the spool" step
// would silently do nothing to the wrapped writer.
type faultSpool struct {
	ipc.SpoolWriter
	allowed int32
	n       int32
	err     error
}

func (f *faultSpool) Append(req ipc.Request) error {
	if atomic.AddInt32(&f.n, 1) > f.allowed {
		return f.err
	}
	return f.SpoolWriter.Append(req)
}

func (f *faultSpool) Close() error {
	if cl, ok := f.SpoolWriter.(interface{ Close() error }); ok {
		return cl.Close()
	}
	return nil
}

// wrapFaultClient wraps c to simulate panic:client: every Send panics, which recoverToZero's
// framework equivalent — runGuarded (recover.go) — catches exactly like any other hook panic.
func wrapFaultClient(c ipc.Client) ipc.Client {
	if _, on := faultActive(faultPanicClient); on {
		return &faultClient{Client: c}
	}
	return c
}

type faultClient struct{ ipc.Client }

func (f *faultClient) Send(_ context.Context, _ ipc.Request, _ time.Duration) (ipc.Response, error) {
	panic("qompack: fault injection: panic:client")
}

// faultCorruptStateBytes matches ipc's own state.bin record size (internal/ipc/state.go's
// stateSize): 32 bytes of random data can never satisfy that format's magic-and-CRC check, so
// ReadState is guaranteed to fall back exactly the way task-6-spec.md's "state-corrupt" row
// requires. //nomagic:allow mirrors ipc's own stateSize, not a config default (§11.6 exemption).
const faultCorruptStateBytes = 32

// faultCorruptStateIfNeeded is the state-corrupt injection: it overwrites run/state.bin with
// faultCorruptStateBytes random bytes BEFORE the caller's next ipc.ReadState, so that call is
// guaranteed to see a bad-CRC record and fall back to config.Defaults() silently, per the fault
// table. It is a no-op for an empty root (nothing resolved yet) AND for a root that does not
// exist as a directory: MkdirAll-ing run/ under a nonexistent root would conjure a store for a
// project that does not exist, exactly what hookclient.go's own isDir(root) guard exists to
// refuse — checking here too (fix round 1, Minor M-7) means the guard can never be undermined by
// a fault call that happens to run before it (state-corrupt's own two call sites, one of them
// BEFORE hookclient.go's root-existence check has a chance to run at all).
func faultCorruptStateIfNeeded(root string) {
	if root == "" || !isDir(root) {
		return
	}
	if _, on := faultActive(faultStateCorrupt); on {
		p := ipc.StatePath(root)
		_ = os.MkdirAll(paths.Long(filepath.Dir(p)), dirPermFault)
		buf := make([]byte, faultCorruptStateBytes)
		_, _ = rand.Read(buf)
		_ = os.WriteFile(paths.Long(p), buf, filePermFault)
	}
}

// faultCorruptConfigIfNeeded is the config-corrupt injection: it replaces .qompack/config.json
// with truncated, unparseable JSON. The hot path never parses config.json (hookclient.go reads
// only the 32-byte state record), so this is deliberately inert for the six hook subcommands and
// is exercised for real by `qompack daemon` (daemon.go) and `qompack self-test` (selftest.go),
// both of which call this function themselves, early, before their own config.Load — exactly as
// the fault table's own row says. Like faultCorruptStateIfNeeded, it never MkdirAlls under a root
// that does not exist as a directory (fix round 1, Minor M-7) — safe to call unconditionally from
// any of its three call sites regardless of ordering relative to a caller's own existence guard.
func faultCorruptConfigIfNeeded(root string) {
	if root == "" || !isDir(root) {
		return
	}
	if _, on := faultActive(faultConfigCorrupt); on {
		dot := paths.Of(root).Dot
		_ = os.MkdirAll(paths.Long(dot), dirPermFault)
		p := filepath.Join(dot, "config.json")
		_ = os.WriteFile(paths.Long(p), []byte(`{"store":{"chunk":{"min":`), filePermFault)
	}
}

// faultLockSpoolDirIfNeeded is the spool-readonly injection: it makes spoolDir non-writable
// (platform-specific: denyWriteDir, in fault_lockdir_unix.go / fault_lockdir_windows.go) BEFORE
// the caller constructs its SpoolWriter over it, so the very first Append fails at the filesystem
// level rather than through a wrapped decorator. Best-effort: a failure to lock the directory
// (an unsupported filesystem, insufficient privilege) is swallowed, not propagated — the hook must
// still behave correctly either way, and the worst case is simply that this one site's OS-level
// mechanism did not engage on this host.
func faultLockSpoolDirIfNeeded(spoolDir string) {
	if _, on := faultActive(faultSpoolReadonly); on {
		_ = os.MkdirAll(paths.Long(spoolDir), dirPermFault)
		_ = denyWriteDir(paths.Long(spoolDir))
	}
}

// faultDaemonDownAddrSuffix is appended to the resolved address's own path to guarantee it names
// something nothing is listening on. It is a suffix, not a wholesale replacement, so the result
// stays a plausible-looking endpoint of the same Kind (a named pipe path on Windows, a socket path
// on POSIX) rather than an empty or malformed Addr that might fail for a different, misleading
// reason.
const faultDaemonDownAddrSuffix = ".qompack-fault-daemon-down"

// faultDaemonDownAddr is the daemon-down injection's address half. task-6-spec.md's row requires
// BOTH "ipc.Resolve returns an address nothing is listening on" AND "ClientOptions.Spawn is set to
// a no-op" — hookclient.go's own spawn-side no-op (noopSpawn) satisfies only the second half on
// its own. Without this, the site would be inert against a daemon that happens to already be
// listening at the ordinarily-resolved address (e.g. a project a prior, non-faulted hook already
// brought a daemon up for) — fix round 1, Important I-3. Called AFTER ipc.Resolve succeeds, so the
// ErrAddrTooLong path is never affected by it.
func faultDaemonDownAddr(addr ipc.Addr) ipc.Addr {
	if _, on := faultActive(faultDaemonDown); on {
		addr.Path += faultDaemonDownAddrSuffix
	}
	return addr
}
