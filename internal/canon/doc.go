// Package canon implements the canonicalizer registry of 00-ARCHITECTURE.md §5.6: a pipeline of
// per-tool, per-class text canonicalizers (crlf, ansi, timestamps, durations, pids, addresses,
// tmpPaths, plus per-tool passes for bash/testrunner/grep/glob/fileread/webfetch/git) that strips
// volatile, low-signal content before chunking (Qompack.md §8.1, O2), together with the MinHash
// signature computed over the canonicalized result and the byte-exact Restore inverse.
//
// canon may additionally import sketch (00-ARCHITECTURE.md §3.2: canon's allow-set is sketch, plus
// foundation) because Result.Signature is a sketch.Signature, computed by sketch.MinHash over the
// canonicalized output.
//
// SP-01 ships the complete §5.6 type set as real declarations and every operation as a stub:
// NewRegistry and Default construct a usable, working Registry value — composition roots can wire
// one today — but Register, Run and Restore report core.ErrNotImplemented, and For/Names (which
// have no error return) report the documented nil, until SP-04 lands the real twelve
// canonicalizers and the registry that dispatches to them.
package canon
