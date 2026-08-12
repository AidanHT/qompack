package ipc

// AddrKind discriminates the two local transports of 00-ARCHITECTURE.md §2.4. §5.4 gives it only
// as a comment on Addr; SP-01 declares it, once, here.
//
// The zero value is deliberately not a valid kind: a zero Addr is an unresolved one, and a caller
// that forgets to call Resolve gets a kind that matches neither branch rather than silently
// dialling a Unix socket on Windows.
type AddrKind uint8

// The two transports. NamedPipe is Windows-only and UnixSocket is everything else; Resolve picks
// between them, and no caller should ever construct one by hand.
const (
	NamedPipe AddrKind = iota + 1
	UnixSocket
)

// Addr is a resolved local endpoint (00-ARCHITECTURE.md §5.4).
type Addr struct {
	Kind AddrKind
	Path string
}

// dirPerm and socketPerm are the modes §2.4 mandates for the endpoint's containing directory and
// for the socket itself: owner-only, both. They are declared here rather than written at the bind
// site so the two numbers §2.4 states appear exactly once in the package that has to honour them.
// They are unexported because binding is this package's own job (SP-05's Server), not a caller's.
const (
	dirPerm    = 0o700
	socketPerm = 0o600
)
