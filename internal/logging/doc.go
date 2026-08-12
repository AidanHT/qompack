// Package logging provides leveled, structured, file-backed logging with a rotation policy and a
// "Loud" channel: the escalation path §12 requires for contract violations and degradation
// transitions, which must never be silent.
//
// logging is foundation-layer (§3.2): it imports internal/core, internal/paths and internal/config
// only. It deliberately does NOT import internal/obs even though the Loud channel is exactly what
// feeds obs's loud.total counter in later subplans — the edge logging -> obs is rejected by
// devtool lint's import-graph check, because §3.2 lists obs as a sibling foundation package, not a
// dependency of logging. AttachLoudObserver is the seam that lets a composition root (internal/cli,
// internal/daemon) wire the two together without either package importing the other.
//
// The exported surface splits into three files:
//
//   - level.go — the Level enum (Debug < Info < Warn < Error < Loud) and its text rendering.
//   - logger.go — the Logger interface, the day-log writer with size-based rotation, New and Nop.
//   - loud.go — the Loud channel: the three-destination write, the process-wide ring buffer
//     LastLoud reads, and the AttachLoudObserver seam.
package logging
