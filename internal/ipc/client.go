package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
	"github.com/qompack/qompack/internal/paths"
)

// Client is the hook-side half of the transport (00-ARCHITECTURE.md §5.4). Every hook subcommand
// holds exactly one, uses it once, and exits.
type Client interface {
	// Send is the hot path. It connects, writes, awaits ACK within deadline, and returns.
	// On ANY failure it spools to disk and returns (Response{OK:false}, nil) — never an error
	// that a hook would propagate.
	Send(ctx context.Context, req Request, deadline time.Duration) (Response, error)
	Close() error
}

// SpoolWriter is the durability fallback every failure path in Send lands on: an append to
// .qompack/spool/client-<pid>.ndjson, which the daemon drains on start and on every idle tick
// (00-ARCHITECTURE.md §2.4). Data is not lost when the daemon is unreachable; only freshness is.
type SpoolWriter interface {
	Append(req Request) error
	// Path returns the file Append writes to, so /qompack:status and the daemon's drain can name
	// it. A SpoolWriter that has not created its file yet reports the path it will use.
	Path() string
}

// Server is the daemon-side half (00-ARCHITECTURE.md §5.4).
type Server interface {
	// Serve accepts connections until ctx is cancelled or Close is called, dispatching each
	// request to h. It returns nil on a shutdown driven by Close, or ctx's own error (typically
	// context.Canceled) when the shutdown was driven by cancellation instead.
	Serve(ctx context.Context, h Handler) error
	Addr() Addr
	Close() error
}

// Handler processes one request. It runs on the daemon's read path, so it must return promptly:
// §2.4 sends the ACK after the WAL append returns, not after the work finishes.
type Handler func(ctx context.Context, req Request) Response

// The obs.Counter names Client.Send and the real SpoolWriter increment (repo underscore idiom;
// Task-2 controller ruling respells §2.4's dotted prose into this package's actual convention).
const (
	counterL0Spooled      = "l0_spooled"
	counterL0Dropped      = "l0_dropped"
	counterL0Externalized = "l0_externalized"
)

// histDegraded is obs.Budgets()' own histogram name for B-G, the budget covering the synchronous
// spool append this package pays when the daemon cannot take the event. It is resolved once at
// package initialisation rather than per call — Budgets() builds a fresh slice every time — and
// looked up rather than respelled, so the budget table stays the single source of truth for which
// series a budget reads (internal/daemon/metrics.go's histName does the same for B-B and B-C).
var histDegraded = budgetHistName(obs.BG)

// budgetHistName reports the histogram obs.Budgets() associates with id, or "" for an id it does
// not declare — which obs.Registry.Hist treats as its own harmless series rather than panicking.
func budgetHistName(id obs.BudgetID) string {
	for _, b := range obs.Budgets() {
		if b.ID == id {
			return b.Hist
		}
	}
	return ""
}

// spawnLockName is the file lazySpawn takes inside <root>/.qompack/run to serialize detached
// daemon spawns across concurrently-running hook clients.
const spawnLockName = "spawn.lock"

// spawnLockStaleAfter is how old an unclaimed spawn.lock has to be before a later client assumes
// the spawn attempt it recorded never finished and retries.
const spawnLockStaleAfter = 10 * time.Second

// blobFilePrefix, blobFileExt and blobField name the client-side externalization scheme: an
// oversized Event.ToolResponse is written to <spoolDir>/blob-<pid>-<n>.bin, and the request that
// travels the wire in its place names that file, its size, and the field it stood in for.
const (
	blobFilePrefix = "blob-"
	blobFileExt    = ".bin"
	blobField      = "e.tool_response"
)

// ClientOptions configures NewClientWithOptions. Every field's zero value falls back to something
// safe: ProjectRoot empty disables both ReadState and lazy spawn; Self empty or Spawn nil disables
// lazy spawn; the two deadlines and MaxLine fall back to State's own values / MaxLineBytes; a nil
// Clock falls back to core.SystemClock().
type ClientOptions struct {
	ProjectRoot string
	// State supplies mode, hot, deadlines and limits. A non-zero State always wins — trusted
	// outright, never re-read from disk — even when ProjectRoot is also set (fix round 1, Important
	// I-1: a caller that already has a State, e.g. the hot-path hook skeleton's own single 32-byte
	// read, or the daemon's own admin client, must never pay for a second round trip through disk
	// just because ProjectRoot happens to be set too). Only when State is the zero value does
	// ProjectRoot matter for this field: ReadState(ProjectRoot, config.Defaults()) is used, falling
	// back further to StateFromConfig(config.Defaults()) when ProjectRoot is also empty. See
	// NewClientWithOptions's own doc comment for the precedence spelled out in full.
	State State
	// Self is the client's own executable path (os.Executable()); "" disables lazy spawn.
	Self string
	// Spawn is set by cli to daemon.SpawnDetached. ipc may not import daemon (§3.2), so this
	// function-field seam is how NewClientWithOptions launches a detached daemon without ever
	// naming the package that implements it. nil disables lazy spawn.
	Spawn func(projectRoot, self string) error
	// ConnectDeadline bounds dialling the daemon; 0 falls back to State.ConnectDeadlineMs.
	ConnectDeadline time.Duration
	// AckDeadline bounds writing the request and awaiting the one-byte ACK/NAK; 0 falls back to
	// State.AckDeadlineMs.
	AckDeadline time.Duration
	// MaxLine bounds a Reply request's response line; 0 falls back to MaxLineBytes.
	MaxLine int
	// Clock is used only for the lazy-spawn lock's staleness math — never for connection deadlines,
	// which are always real wall-clock time because that is what the OS network stack enforces
	// regardless of what a test's injected Clock says. nil falls back to core.SystemClock().
	Clock core.Clock
}

// client is the real Client (00-ARCHITECTURE.md §5.4, §7.1, §12.2, §12.3). Send never returns an
// error a hook could propagate: every failure route ends in a spool append (or, if spooling itself
// is unavailable, a counted drop) and (Response{OK:false}, nil).
type client struct {
	addr  Addr
	spool SpoolWriter
	log   logging.Logger
	m     obs.Registry
	o     ClientOptions

	mode contract.Mode // read once at construction; a Response never changes it after that
	// threshold is ExternalizeThreshold, precomputed at construction: min(State.MaxPayloadBytes, MaxLine).
	threshold int

	hotMu sync.Mutex
	hot   HotPathMode // read once from state at construction; the NAK path may flip it to HotSpool

	spawnOnce sync.Once
	dropOnce  sync.Once

	externalizeSeq int64
}

// NewClient returns the real Client, equivalent to
// NewClientWithOptions(addr, spool, log, m, ClientOptions{}).
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client {
	return NewClientWithOptions(addr, spool, log, m, ClientOptions{})
}

// NewClientWithOptions is NewClient with every knob exposed. A caller that already has a State —
// because it just read it itself, e.g. the hot-path hook skeleton's own single 32-byte read
// (task-6-spec.md: "one 32-byte read; no config.Load") — should never pay for a second one: a
// non-zero o.State always wins, even when o.ProjectRoot is also set (ProjectRoot still matters for
// lazySpawn's lock path and externalize()'s blob directory, both of which need it independently of
// where State came from). Only when o.State is the zero value does this constructor read for
// itself: ReadState(o.ProjectRoot, config.Defaults()) when a root is given, else
// StateFromConfig(config.Defaults()), so a caller that supplies neither still gets sane deadlines
// rather than a State that reads "daemon disabled, deadlines 0".
func NewClientWithOptions(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry, o ClientOptions) Client {
	if log == nil {
		log = logging.Nop()
	}
	if o.Clock == nil {
		o.Clock = core.SystemClock()
	}

	st := o.State
	switch {
	case st != (State{}):
		// Trust the caller's own already-read State; never re-read from disk.
	case o.ProjectRoot != "":
		st = ReadState(o.ProjectRoot, config.Defaults())
	default:
		st = StateFromConfig(config.Defaults())
	}
	o.State = st

	if o.ConnectDeadline <= 0 {
		o.ConnectDeadline = time.Duration(st.ConnectDeadlineMs) * time.Millisecond
	}
	if o.AckDeadline <= 0 {
		o.AckDeadline = time.Duration(st.AckDeadlineMs) * time.Millisecond
	}
	if o.MaxLine <= 0 {
		o.MaxLine = MaxLineBytes
	}

	threshold := int(st.MaxPayloadBytes)
	if threshold <= 0 || threshold > o.MaxLine {
		threshold = o.MaxLine
	}

	return &client{
		addr: addr, spool: spool, log: log, m: m, o: o,
		mode: st.Mode, hot: st.Hot, threshold: threshold,
	}
}

// Send implements Client.Send exactly per task-2-spec.md's eight-step algorithm.
func (c *client) Send(ctx context.Context, req Request, deadline time.Duration) (Response, error) {
	// 1. A session in ModeOff does nothing at all: no dial, no spool write.
	if c.mode == contract.ModeOff {
		return Response{OK: true, Mode: contract.ModeOff, Hot: c.currentHot()}, nil
	}
	// 2. The operator has disabled the daemon outright.
	if !c.o.State.DaemonEnabled {
		return c.spoolAndReturn(req)
	}
	// 3. The session has already breached its hot-path budget for this op family: never connect.
	if c.currentHot() == HotSpool && req.Op.HotPath() {
		return c.spoolAndReturn(req)
	}

	// 4. Encode, externalizing an oversized Event.ToolResponse before it ever touches the wire.
	line, err := EncodeRequest(req)
	if err != nil {
		req = c.externalize(req)
		if line, err = EncodeRequest(req); err != nil {
			return c.spoolAndReturn(req)
		}
	}
	if len(line) >= c.threshold {
		req = c.externalize(req)
		if line, err = EncodeRequest(req); err != nil {
			return c.spoolAndReturn(req)
		}
	}

	// 5. Connect, bounded by ConnectDeadline (and by ctx, if it carries an earlier deadline).
	conn, err := c.connect(ctx)
	if err != nil {
		// Spool BEFORE spawning, not after. The daemon this spawn launches runs a startup Drain
		// (daemon.Run, before it serves), so a spool file written after the spawn call can be
		// missed by the very drain the spawn exists to trigger — and the next drain is an idle
		// tick up to idleTickMax (30s) away. The entry that causes a cold start is precisely the
		// one at risk, and it loses this race exactly when the machine is loaded.
		//
		// Client latency is unchanged: both operations already happen before Send returns, so
		// this only moves the daemon's start a file-append later.
		resp, spoolErr := c.spoolAndReturn(req)
		c.lazySpawn()
		return resp, spoolErr
	}
	defer func() { _ = conn.Close() }()

	// 6. Write the request line within AckDeadline.
	if err := conn.SetWriteDeadline(time.Now().Add(c.o.AckDeadline)); err != nil {
		return c.spoolAndReturn(req)
	}
	if _, err := conn.Write(line); err != nil {
		return c.spoolAndReturn(req)
	}

	if !req.Reply {
		return c.awaitACK(conn, req)
	}
	return c.awaitReply(conn, req, deadline)
}

// awaitACK is Send's step 7: the fire-and-forget path, which reads exactly one control byte
// within AckDeadline.
func (c *client) awaitACK(conn net.Conn, req Request) (Response, error) {
	if err := conn.SetReadDeadline(time.Now().Add(c.o.AckDeadline)); err != nil {
		return c.spoolAndReturn(req)
	}
	var b [1]byte
	n, err := io.ReadFull(conn, b[:])
	if err != nil || n != 1 {
		return c.spoolAndReturn(req)
	}
	switch b[0] {
	case ACK:
		return Response{OK: true, Mode: c.mode, Hot: HotSync}, nil
	case NAK:
		// The daemon's in-band hint that it has degraded to spool submode (§12.2): the response
		// this call returns still comes from spoolAndReturn, which now sees the updated hot value.
		c.setHot(HotSpool)
		return c.spoolAndReturn(req)
	default:
		return c.spoolAndReturn(req)
	}
}

// awaitReply is Send's step 8: the Reply path, which reads one full NDJSON response line within
// the caller-supplied deadline (e.g. 10 s for session.start).
func (c *client) awaitReply(conn net.Conn, req Request, deadline time.Duration) (Response, error) {
	if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		return c.spoolAndReturn(req)
	}
	line, err := NewLineReader(conn, c.o.MaxLine).ReadLine()
	if err != nil {
		return c.spoolAndReturn(req)
	}
	resp, err := DecodeResponse(line)
	if err != nil {
		return c.spoolAndReturn(req)
	}
	if resp.Hot == HotSpool {
		c.setHot(HotSpool)
	}
	return resp, nil
}

// connect dials c.addr, bounded by whichever is sooner: c.o.ConnectDeadline or ctx's own
// deadline. It never blocks past an already-cancelled ctx — the dial primitive SP-01 shipped
// takes a plain timeout, not a context, so a context that is already done is honoured here
// instead of being handed down to a dial call that could not see it.
func (c *client) connect(ctx context.Context) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	timeout := c.o.ConnectDeadline
	if dl, ok := ctx.Deadline(); ok {
		if remain := time.Until(dl); remain < timeout {
			timeout = remain
		}
	}
	if timeout <= 0 {
		return nil, context.DeadlineExceeded
	}
	return dial(c.addr, timeout)
}

// currentHot and setHot guard the one field Send mutates after construction: a NAK response flips
// the client into spool submode for the rest of the process.
func (c *client) currentHot() HotPathMode {
	c.hotMu.Lock()
	defer c.hotMu.Unlock()
	return c.hot
}

func (c *client) setHot(h HotPathMode) {
	c.hotMu.Lock()
	c.hot = h
	c.hotMu.Unlock()
}

// spoolAndReturn is every failure path's landing point: append to the spool (or, if there is none,
// take the drop path), count the event, and return an honourable, never-erroring response.
func (c *client) spoolAndReturn(req Request) (Response, error) {
	c.appendToSpool(req)
	return Response{OK: false, Mode: c.mode, Hot: c.currentHot()}, nil
}

// appendToSpool is spoolAndReturn's side-effecting half. A nil SpoolWriter means "spool
// unavailable" (the stubs guard constructs exactly this): the event is dropped, counted once as
// dropped rather than spooled, and Loud'd at most once for this client's lifetime.
//
// spool.Append itself already drops-counts-and-Louds internally for an ordinary write failure
// (§12.3), always returning nil for that case — but it still returns a real, non-nil error for
// the size-refusal case (a line that would exceed MaxLineBytes), which externalize() cannot
// always prevent: a request whose bulk lives in Raw rather than Event.ToolResponse reaches Append
// at full size. That error must be treated exactly like the nil-spool case — counted as a drop,
// never miscounted as a successful spool — or the event is lost while l0_spooled claims it was
// durably enqueued.
func (c *client) appendToSpool(req Request) {
	if c.spool == nil {
		c.dropOnce.Do(func() {
			if c.log != nil {
				c.log.Loud("ipc: spool unavailable — event dropped", "op", string(req.Op))
			}
		})
		if c.m != nil {
			c.m.Counter(counterL0Dropped).Add(1)
		}
		return
	}
	if err := c.timedAppend(req); err != nil {
		c.dropOnce.Do(func() {
			if c.log != nil {
				c.log.Loud("ipc: spool append refused — event dropped", "op", string(req.Op), "err", err)
			}
		})
		if c.m != nil {
			c.m.Counter(counterL0Dropped).Add(1)
		}
		return
	}
	if c.m != nil {
		c.m.Counter(counterL0Spooled).Add(1)
	}
}

// timedAppend calls the spool's Append and records how long it took into B-G's histogram, which
// is what gives that budget a clock: this call is the whole of the degraded path's cost and the
// only step of Send no deadline governs. It is measured on both outcomes — obs.Timed observes
// whether or not f errors — because a refused append is a real cost the hook paid too.
//
// A client with no Registry (c.m nil — the stubs guard and several tests construct one) skips the
// measurement rather than observing into nothing, exactly as the counters above do.
func (c *client) timedAppend(req Request) error {
	if c.m == nil {
		return c.spool.Append(req)
	}
	return obs.Timed(c.m.Hist(histDegraded), func() error { return c.spool.Append(req) })
}

// blobRef is the JSON shape a client-externalized request's Raw carries in place of the field it
// stood in for (00-ARCHITECTURE.md task-2-spec.md's "Externalized payloads"). X preserves a
// pre-existing Request.Raw that externalize would otherwise silently overwrite: the wire struct
// (wire.go) allows Event and Raw to be set simultaneously, and a request that legitimately used
// both must not lose the Raw half just because its Event.ToolResponse also happened to be huge.
type blobRef struct {
	Blob  string          `json:"blob"`
	Bytes int             `json:"bytes"`
	Field string          `json:"field"`
	X     json.RawMessage `json:"x,omitempty"`
}

// externalize moves req.Event.ToolResponse to a side blob file when it is present, returning a
// request whose Raw names the blob instead and whose Event.ToolResponse is nulled. A pre-existing
// Request.Raw is preserved, nested under the blob descriptor's own "x" key, rather than
// overwritten — task-2-spec.md's "Raw replaced by {...}" does not contemplate a request that
// already used the field, and losing it silently would be worse than the line-size problem
// externalize exists to solve. A request with no Event (nothing captured under e.tool_response) or
// no spool (nowhere to write the blob) is returned unchanged — the oversize-line case that results
// is still handled correctly, just one layer up: the server's own MaxLineBytes NAK-and-discard
// (00-ARCHITECTURE.md §2.4).
func (c *client) externalize(req Request) Request {
	if req.Event == nil || len(req.Event.ToolResponse) == 0 || c.spool == nil {
		return req
	}

	dir := filepath.Dir(c.spool.Path())
	n := atomic.AddInt64(&c.externalizeSeq, 1)
	name := fmt.Sprintf("%s%d-%d%s", blobFilePrefix, os.Getpid(), n, blobFileExt)

	data := []byte(req.Event.ToolResponse)
	if err := writeBlob(filepath.Join(dir, name), data); err != nil {
		return req
	}

	ref, err := json.Marshal(blobRef{Blob: name, Bytes: len(data), Field: blobField, X: req.Raw})
	if err != nil {
		return req
	}

	out := req
	ev := *req.Event
	ev.ToolResponse = nil
	out.Event = &ev
	out.Raw = ref

	if c.m != nil {
		c.m.Counter(counterL0Externalized).Add(1)
	}
	return out
}

// writeBlob creates dir (0700) if needed and writes data to p exclusively — the counter in the
// blob's own filename already makes p unique per client process, so O_EXCL is a correctness
// safeguard, not a name-collision workaround.
func writeBlob(p string, data []byte) error {
	if err := os.MkdirAll(paths.Long(filepath.Dir(p)), dirPerm); err != nil {
		return fmt.Errorf("ipc: externalize: mkdir: %w", err)
	}
	f, err := paths.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("ipc: externalize: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("ipc: externalize: write: %w", err)
	}
	return nil
}

// Close closes the spool file if one was opened, and is the only Client method that may return a
// non-nil error (cli ignores it after logging). SpoolWriter's Close is on the concrete *spool type
// rather than the interface (SP-01 shipped SpoolWriter without one), so it is reached through a
// type assertion that is a safe no-op against a nil or Close-less SpoolWriter.
func (c *client) Close() error {
	if cl, ok := c.spool.(interface{ Close() error }); ok {
		return cl.Close()
	}
	return nil
}

// lazySpawn launches a detached daemon at most once per client, and only when Self and Spawn are
// both configured and ProjectRoot is non-empty (the lock path is derived from it, so there is
// nowhere to take the lock without it). It is guarded by <root>/.qompack/run/spawn.lock so concurrently-spawning
// clients do not race: the first to create the lock file spawns; a second client within
// spawnLockStaleAfter of that file's own recorded timestamp assumes a spawn is already underway
// and does nothing; past that staleness window, the lock is presumed abandoned and reclaimed.
// The spawning client never waits for the daemon it launched — this method itself is called only
// from a failure path that is about to spool and return.
func (c *client) lazySpawn() {
	if c.o.Self == "" || c.o.Spawn == nil || c.o.ProjectRoot == "" {
		return
	}
	c.spawnOnce.Do(func() {
		runDir := paths.Of(c.o.ProjectRoot).Run
		lockPath := filepath.Join(runDir, spawnLockName)
		content := []byte(strconv.FormatInt(c.o.Clock.Now().UnixMilli(), 10))

		// A client that reaches lazySpawn may be the very first process to ever touch this
		// project's .qompack tree (a cold daemon means nothing has run session-start yet), so
		// run/ cannot be assumed to exist the way it would once a daemon has started once.
		if err := os.MkdirAll(paths.Long(runDir), dirPerm); err != nil {
			return
		}

		if err := paths.CreateNew(lockPath, content); err != nil {
			if !os.IsExist(err) {
				return // could not take the lock for a reason other than contention: give up quietly
			}
			if !spawnLockIsStale(lockPath, c.o.Clock) {
				return // another client is already spawning
			}
			removeSpawnLock(lockPath)
			if err := paths.CreateNew(lockPath, content); err != nil {
				return // lost the retry race; someone else is spawning now
			}
		}
		_ = c.o.Spawn(c.o.ProjectRoot, c.o.Self)
	})
}

// spawnLockIsStale reports whether the spawn.lock at lockPath was written more than
// spawnLockStaleAfter ago. An unreadable or unparseable lock is treated as stale rather than
// blocking lazy spawn forever on a file this process cannot make sense of.
func spawnLockIsStale(lockPath string, clk core.Clock) bool {
	b, err := os.ReadFile(paths.Long(lockPath))
	if err != nil {
		return true
	}
	ms, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return true
	}
	return clk.Now().Sub(time.UnixMilli(ms)) > spawnLockStaleAfter
}

// removeSpawnLock deletes a stale spawn.lock. paths.CreateNew marks its file read-only
// (0o444/FILE_ATTRIBUTE_READONLY) once written, which on Windows blocks DeleteFile outright, so
// the mode is cleared first — harmless on POSIX, where file permissions never gate an unlink.
func removeSpawnLock(lockPath string) {
	_ = os.Chmod(paths.Long(lockPath), 0o600)
	_ = os.Remove(paths.Long(lockPath))
}
