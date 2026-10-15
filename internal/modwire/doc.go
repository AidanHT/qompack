// Package modwire is the wire contract between Qompack's Claude Code mod and the Go binary.
//
// The mod is an in-process TypeScript hooks module. It reaches Qompack only by running the bundled
// binary as `qompack mod <verb>` through the host's process API, writing one JSON object on the
// child's stdin and reading one JSON object from its stdout. This package defines every such
// object, the limits that bound them, and the two small records the Go side writes for each mod
// session (the brief-outcome log and the hello file), so the code that writes them and the code
// that reads them share one type.
//
// The TypeScript half of the contract, internal/pluginmanifest/modsrc/wire.gen.ts, is generated
// from Schema() by `go run ./tools/devtool gen-mod-wire`; CI fails when it drifts. The decisions
// behind the contract are recorded in docs/adr/0015-claude-code-mods-integration.md.
//
// Compatibility rules, which every message obeys:
//
//   - Every message carries "v": Version.
//   - A reader ignores fields it does not know, so a later writer may add one.
//   - Keys are matched as encoding/json matches them: case-insensitively, with a repeated key
//     resolving to its last value, so a case-variant spelling of a known key ("Session_ID",
//     "V") binds as that field rather than being ignored.
//   - A message whose "v" is absent or is not Version is refused with an error object
//     (FailureFor of a *VersionError), never guessed at.
//   - A handled failure is a Status with ok false and one of the Reason values; the CLI exits 0
//     with it. A non-zero exit or unparsable stdout is read by the mod as ReasonError.
//
// The version gate lives here and only here: the Go side applies MeetsMinHostVersion when it
// serves a hello and answers `enabled`; MIN_HOST_VERSION in the generated TypeScript is
// informational. A prerelease of a release (2.1.300-dev...) counts as below that release.
//
// The package is pure: it reads and writes no files and holds no state.
package modwire
