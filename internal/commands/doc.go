// Package commands holds the backends behind the seven `/qompack:*` slash commands of
// 00-ARCHITECTURE.md §5.17. The markdown files in plugin/commands/ are thin shells that shell out
// to the binary; the behaviour lives here, so the slash command and the equivalent `qompack`
// subcommand can never drift apart.
//
// commands is a composition root (00-ARCHITECTURE.md §3.2): it may import anything, and nothing
// may import it. That is what lets Deps name every L1–L7 seam at once without creating a cycle.
//
// SP-01 ships the type set below as real declarations, a real All that returns one Command per
// §5.17 name, and a Run body per command that reports core.ErrNotImplemented. SP-14 owns the real
// implementations. There is no <pkg>test conformance suite for this package: a composition root
// has no seam for another wave to implement against, so the suite would assert nothing.
//
// The Deps fields are deliberately allowed to be nil. §5.4's "Services is the late-bound
// dependency set; nil members mean not built yet" applies here for the same reason — waves 1–2
// run with Checkpoints, Sched and Eval absent, and a command that panics rather than reporting
// "not available in this build" would make the plugin less usable during exactly the period it is
// most likely to be half-built.
package commands
