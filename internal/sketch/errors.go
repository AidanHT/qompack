package sketch

import "errors"

// The sentinel errors every decoder in this package returns. They are deliberately fine-grained:
// a corrupt sketch and a truncated one call for different operator responses (bit rot on disk
// versus an interrupted write), and 00-ARCHITECTURE.md §13 invariant 10 requires degradation to
// be loud enough to tell them apart. Callers match with errors.Is; Load and LoadWithLog map every
// one of them to core.ErrNotFound at the package boundary, because a sketch is a cache and a
// missing cache and an unreadable cache are the same event to everything upstream (§13
// invariant 3).
//
// SAVE IS NOT THE SAME CONTRACT, and the asymmetry is deliberate rather than an omission. Load's
// mapping exists because "missing" and "unreadable" both mean "start empty" to a caller; nothing
// analogous is true of a write. Save refuses sketches/tried.bloom with core.ErrAppendOnly wrapping
// ErrGenerational, which names the door to use instead, and passes a marshal failure's own sentinel
// through unchanged, because those are two different bugs in the caller and collapsing them to
// core.ErrNotFound would say that something was looked up and not found when nothing was. A
// filesystem failure under paths.WriteAtomic reaches the caller as itself, since it is not this
// package's to rename. sketchtest's requireLoadError and requireSaveError hold the two halves apart.
//
// Every rejection path in DecodeHeader returns one of these values BARE, never wrapped with
// fmt.Errorf. That is not stylistic: wrapping allocates, and TestHeader_RejectLyingBodyLen holds
// the decoder to zero allocations on the rejection path, which is the whole of the fuzz surface.
var (
	// ErrBadMagic reports that the first four bytes were not "QPKS": the file is not a sketch
	// frame at all, so no length inside it may be trusted.
	ErrBadMagic = errors.New("qompack/sketch: bad magic")
	// ErrUnsupportedVersion reports Ver == 0 or Ver > FormatVersion. A newer plugin's file must
	// never be half-read by an older binary.
	ErrUnsupportedVersion = errors.New("qompack/sketch: unsupported format version")
	// ErrKindMismatch reports that a frame decoded cleanly but describes a different sketch than
	// the receiver — loading touch.cms into a *Bloom, for instance.
	ErrKindMismatch = errors.New("qompack/sketch: kind mismatch")
	// ErrCorrupt reports a CRC32C mismatch: the frame is structurally intact but its bytes
	// changed under us. This is the bit-rot signal.
	ErrCorrupt = errors.New("qompack/sketch: CRC32C mismatch")
	// ErrTruncated reports that a declared length runs past the buffer, or that the declared
	// lengths do not add up to it. This is the interrupted-write signal.
	ErrTruncated = errors.New("qompack/sketch: truncated payload")
	// ErrMalformed reports a structurally invalid frame whose lengths are self-consistent: an
	// unset Kind, an out-of-alphabet param name, unsorted params, a nil receiver.
	ErrMalformed = errors.New("qompack/sketch: malformed payload")
	// ErrShapeMismatch reports that two sketches cannot be merged because they were constructed
	// with different dimensions (CMS width/depth, HLL registers, Misra-Gries k).
	ErrShapeMismatch = errors.New("qompack/sketch: shape mismatch")
	// ErrTooLarge reports that a frame exceeds MaxFrameBytes, on either side of the wire. It is
	// returned by EncodeHeader as well as by the decoder, so a sketch that could not be re-read
	// is never written in the first place.
	ErrTooLarge = errors.New("qompack/sketch: payload exceeds limit")
	// ErrGenerational reports an attempt to overwrite sketches/tried.bloom in place. That path is
	// append-only (00-ARCHITECTURE.md §3.3) and must be replaced through ReplaceGenerational,
	// which renames the previous file to tried.bloom.<seq>.bak first.
	ErrGenerational = errors.New("qompack/sketch: tried.bloom must be replaced via ReplaceGenerational")
)

// The size ceilings. They are package constants rather than per-file literals so that construction
// and decode share one number: a constructor clamps to them, and a decoder rejects past them.
//
// Ceiling rule — every constructible sketch must also be marshallable. MaxBloomBits is 1<<28, not
// 1<<31, precisely so the largest legal Bloom body (m/8 = 32 MiB) plus the 32-byte prefix, four
// params and the 4-byte CRC stays comfortably inside MaxFrameBytes. MaxCMSCells is picked on the
// same basis. Without this, NewBloom(MaxBloomCapacity, 1e-6) would build a filter Save could write
// and Load would then reject as ErrTooLarge — a sketch that cannot survive a restart, which is the
// one failure this package exists to prevent (§6.2).
//
// MaxMGCounters cannot be bounded that way, because a Misra-Gries frame's size depends on key
// lengths as well as counter count. The uniform rule that covers it is enforced once, in
// EncodeHeader: an assembled frame over MaxFrameBytes is ErrTooLarge — never a partial frame,
// never a panic — so all five MarshalBinary implementations inherit the guard by construction
// instead of each re-implementing it.
const (
	// MaxFrameBytes is the universal decode ceiling; it bounds every allocation below it.
	MaxFrameBytes = 64 << 20
	// MaxBloomCapacity is the largest configured entry count a Bloom may be sized for
	// (16 777 216 entries).
	MaxBloomCapacity = 1 << 24
	// MaxBloomBits is the largest bit array a Bloom may allocate: 1<<28 bits = 32 MiB of words.
	// See the ceiling rule above for why it is not larger.
	MaxBloomBits = uint64(1) << 28
	// MaxCMSCells is the largest width×depth a Count-Min sketch may allocate: 8 388 608 cells at
	// 4 bytes each = 32 MiB.
	MaxCMSCells = 1 << 23
	// MaxCMSDepth bounds the number of Count-Min rows independently of MaxCMSCells, so a
	// pathological delta cannot trade all the width away for depth.
	MaxCMSDepth = 64
	// MaxHLLRegisters is the largest HyperLogLog register file (64 KiB of registers), trivially
	// inside MaxFrameBytes.
	MaxHLLRegisters = 65536
	// MinHLLRegisters is the smallest register count with a usable bias correction.
	MinHLLRegisters = 64
	// MaxMGCounters bounds a Misra-Gries counter table.
	MaxMGCounters = 1 << 20
	// MaxMGKeyBytes bounds one Misra-Gries key. It is deliberately 8192 rather than 4096: 4096 is
	// store.chunk.target and is therefore a forbidden literal outside internal/config/defaults.go
	// (§11.6), and a Misra-Gries key — a path or a tool name — never approaches either bound.
	MaxMGKeyBytes = 8192
)
