// Package paths resolves the Qompack project root, defines the on-disk .qompack/ runtime
// layout, and makes the §7.4 append-only invariant mechanical: every function in this package
// that can write into .qompack refuses to truncate or rewrite checkpoints/, pins/, or
// sketches/tried.bloom. It is foundation-layer (§3.2): it imports internal/core and the standard
// library only, so every other package in the repository can depend on it without risking an
// import cycle.
//
// The exported surface splits into five concerns, one per file:
//
//   - resolve.go — Resolve and Global find the project root and the cross-project home
//     directory.
//   - layout.go — Layout and Of/EnsureLayout name and create every directory under .qompack/.
//   - norm.go — Norm, Key and KeyFold normalize a filesystem path into the project's dedup key.
//   - long.go, long_windows.go, long_other.go — Long works around Windows' legacy MAX_PATH so
//     every OpenFile/Stat/Rename this package performs can address a path beyond it.
//   - atomic.go — WriteAtomic, the crash-safe replace used for every non-append write.
//   - appendonly.go — the append-only guard itself: IsProtected, OpenFile, AppendOnly,
//     AppendJSONL, CreateNew and ReplaceBloom.
//   - manifest.go — CheckpointPath, ManifestPath and the checkpoints/MANIFEST.jsonl reader/
//     writer built on top of AppendJSONL.
package paths
