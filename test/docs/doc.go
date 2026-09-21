// Package docs is the documentation test harness: a composition-root test package (nothing
// imports it, and it imports nothing from internal/) that asserts the properties of this
// repository's prose which a reviewer would otherwise have to check by hand.
//
// It exists because documentation rots silently. A moved file leaves a dead link, a renamed MCP
// tool leaves a stale page, and a new ADR that nobody adds to the index is invisible — none of
// which any compiler or existing test notices. The checks here are deliberately mechanical: they
// verify that what a document POINTS AT exists, not that what it SAYS is true. Claims are the
// reviewer's job; reachability is this package's.
//
// The three checks in this commit:
//
//   - TestRelativeLinksResolve walks README.md and every docs/**/*.md, and resolves every
//     scheme-less markdown link — including heading anchors — against the filesystem.
//   - TestOwnedDocsExist pins the set of hand-written pages SP-18 owns (ownedDocs), which later
//     commits extend as they add pages.
//   - TestADRIndexListsEveryADR derives the ADR set from the directory and requires the index to
//     link every one of them under its own title.
//
// inventory_test.go holds the source-derived inventories (`docs/commands.md` and
// `docs/mcp-tools.md` are generated from internal/commands.Specs() and internal/mcp.ToolDefs, and
// the CI `docs` job fails if they drift), so later commits can assert prose against the real
// command and tool sets rather than against a hand-maintained list.
//
// Stdlib only, by rule: this package must stay runnable against the repository as a tree of files.
package docs
