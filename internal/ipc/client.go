package ipc

import (
	"context"
	"time"

	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/logging"
	"github.com/qompack/qompack/internal/obs"
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

// Server is the daemon-side half (00-ARCHITECTURE.md §5.4). §5.4 gives it no constructor, and
// SP-01 deliberately does not invent one: the daemon builds and owns the listener, and adding a
// NewServer here would fix a shape SP-05 has to live with.
type Server interface {
	// Serve accepts connections until ctx is cancelled or Close is called, dispatching each
	// request to h. It returns nil on an orderly shutdown.
	Serve(ctx context.Context, h Handler) error
	Addr() Addr
	Close() error
}

// Handler processes one request. It runs on the daemon's read path, so it must return promptly:
// §2.4 sends the ACK after the WAL append returns, not after the work finishes.
type Handler func(ctx context.Context, req Request) Response

// NewClient returns a stub Client. SP-05 owns the real one.
//
// Constructing always succeeds so a wave-0 composition root can wire a Client today; every
// operation reports core.ErrNotImplemented. Note that this deliberately contradicts the interface's
// own "never an error a hook would propagate" contract, and it has to: plans/OWNERS.tsv names Send
// as ipc's Rule W-1 probe, so a stub that already honoured the never-error rule would be
// indistinguishable from a working transport and every ipctest behaviour block would run against a
// Client that does nothing. The never-error contract is asserted in ipctest's behaviour block,
// which is exactly where SP-05's real implementation is graded on it.
func NewClient(addr Addr, spool SpoolWriter, log logging.Logger, m obs.Registry) Client {
	return stubClient{}
}

// stubClient is the SP-01 placeholder Client. SP-05 owns the real implementation.
type stubClient struct{}

// Send always reports core.ErrNotImplemented; see NewClient for why the stub does not yet honour
// the never-error contract.
func (stubClient) Send(ctx context.Context, req Request, deadline time.Duration) (Response, error) {
	return Response{}, core.ErrNotImplemented
}

// Close always reports core.ErrNotImplemented.
func (stubClient) Close() error { return core.ErrNotImplemented }

// NewSpool returns a stub SpoolWriter rooted at dir, which is <root>/.qompack/spool in every real
// caller.
//
// Constructing always succeeds and creates nothing: a stub that pre-created a spool file would
// leave an empty NDJSON file in every project that merely wired a client, and the daemon's drain
// would have to distinguish it from a real one.
func NewSpool(dir string) (SpoolWriter, error) {
	return stubSpool{}, nil
}

// stubSpool is the SP-01 placeholder SpoolWriter. SP-05 owns the real implementation.
type stubSpool struct{}

// Append always reports core.ErrNotImplemented.
func (stubSpool) Append(req Request) error { return core.ErrNotImplemented }

// Path always returns the empty string. Path has no error return, and the empty string is the only
// honest answer a stub can give: reporting the path it WOULD write to would be plausible-looking
// data for a file that does not and will not exist.
func (stubSpool) Path() string { return "" }
