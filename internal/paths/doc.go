// Package paths resolves the Qompack project root, defines the on-disk .qompack/ runtime
// layout, and makes the §7.4 append-only invariant mechanical: every function in this package
// that can write into .qompack refuses to truncate or rewrite checkpoints/, pins/, or
// sketches/tried.bloom. It is foundation-layer (§3.2): of this module's own packages it imports
// internal/core alone, so every other package in the repository can depend on it without risking
// an import cycle.
//
// Outside the module it is stdlib-only except in replace_windows.go, which imports
// golang.org/x/sys/windows for the one call the standard library keeps unexported
// (SetFileInformationByHandle, GOROOT/src/syscall/syscall_windows.go's lower-case
// setFileInformationByHandle). That import is confined to the //go:build windows file, adds
// nothing to a non-Windows build, and adds nothing new to a Windows one: x/sys/windows already
// ships in the binary as go-winio's own dependency and is already named in §2.5's runtime list
// and in tools/devtool/bindeps.go's allow-list.
//
// The exported surface splits into these concerns, one per file:
//
//   - resolve.go — Resolve and Global find the project root and the cross-project home
//     directory.
//   - layout.go — Layout and Of/EnsureLayout name and create every directory under .qompack/.
//   - norm.go — Norm, Key and KeyFold normalize a filesystem path into the project's dedup key.
//   - long.go, long_windows.go, long_other.go — Long works around Windows' legacy MAX_PATH so
//     every OpenFile/Stat/Rename this package performs can address a path beyond it.
//   - atomic.go — WriteAtomic, the crash-safe replace used for every non-append write.
//   - shared.go, replace_windows.go, replace_other.go — OpenShared and ReadFileShared, the read
//     that cannot obstruct a concurrent writer, and the POSIX-semantics replace that finishes
//     WriteAtomic even while a reader holds the destination open. On Windows those two are one
//     mechanism seen from its two ends; off Windows both are what os.Open and os.Rename already
//     do.
//   - appendonly.go — the append-only guard itself: IsProtected, OpenFile, AppendOnly,
//     AppendJSONL, CreateNew and ReplaceBloom.
//   - manifest.go — CheckpointPath, ManifestPath and the checkpoints/MANIFEST.jsonl reader/
//     writer built on top of AppendJSONL.
package paths
