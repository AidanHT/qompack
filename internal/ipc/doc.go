// Package ipc implements the local transport between a Qompack hook client and the per-project
// daemon (00-ARCHITECTURE.md §2.4, §5.4): endpoint resolution, the NDJSON request/response framing,
// the one-byte ACK/NAK handshake, the spool that every failure path falls back to, and the client
// and server seams themselves. SP-05 owns the real transport bodies.
//
// # ipc is the only package permitted to import net — and that is not network access
//
// This is the single most likely place in the repository for a reader to conclude that Qompack is
// allowed to talk to the outside world. It is not. Decision D10 is absolute: Qompack performs no
// network I/O, ever, and §7.1 is why — a sidecar that phones home is a sidecar nobody can audit.
//
// What this package dials is a LOCAL, same-machine, same-user endpoint with no network stack
// underneath it in any meaningful sense:
//
//   - Windows: a named pipe, \\.\pipe\qompack.<hash12>, opened through
//     github.com/Microsoft/go-winio and ACL'd to the current user's SID. There is no stdlib
//     named-pipe support, which is the entire reason go-winio is on §2.5's closed dependency list.
//   - POSIX: a SOCK_STREAM Unix domain socket under $XDG_RUNTIME_DIR or the temp directory, mode
//     0600 in a 0700 directory. AF_UNIX carries no packets and reaches no host.
//
// `net` appears here solely because those two things are typed as net.Conn. What remains forbidden,
// in this package as much as in every other: net/http, net/url, crypto/tls, net.Dial to anything
// but "unix", any hostname, any port, any listener bound to an address. .golangci.yml's forbidigo
// rule bans net.Dial repository-wide and exempts exactly this directory; that exemption is for the
// unix-socket dial, and for nothing else. A future reader adding an HTTP client here is not
// extending an existing allowance — they are breaking D10.
//
// ipc may import ONLY hookio and contract, plus the foundation packages core, paths, config,
// logging and obs (00-ARCHITECTURE.md §3.2's ipc allow-set): hookio because a Request carries the
// hook Event verbatim, and contract because every Response reports the mode in force so a client
// honours a degradation immediately (§12.1).
//
// # What SP-01 ships
//
// Resolve and the platform dial helpers are real: §2.4 specifies the endpoint paths and the
// sun_path fallback exactly, so there is nothing to defer. Client, SpoolWriter and Server are
// stubs — constructing succeeds, every operation reports core.ErrNotImplemented — and the framing
// constants (ACK, NAK, MaxLineBytes) are real, because the wire format is normative and the
// conformance suite asserts it.
package ipc
