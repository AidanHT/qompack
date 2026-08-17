package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"runtime"

	"github.com/qompack/qompack/internal/config"
	"github.com/qompack/qompack/internal/contract"
	"github.com/qompack/qompack/internal/core"
	"github.com/qompack/qompack/internal/paths"
)

// The hot-path state record's layout (SP-05 task-1-spec, "the 32-byte hot-path state record"):
// little-endian, magic 'Q','P','S','1', a CRC32C over everything before it. It exists so a client
// pays one 32-byte os.ReadFile before deciding whether and how to connect, instead of a full
// config.Load (two file opens plus an environment scan) or several stats.
const (
	stateSize = 32 // total record size, bytes

	stateOffMagic             = 0  // 4 bytes: 'Q','P','S','1'
	stateOffMode              = 4  // 1 byte:  contract.Mode (0 full, 1 degraded-passive, 2 off)
	stateOffHot               = 5  // 1 byte:  HotPathMode (0 sync, 1 spool)
	stateOffConnectDeadlineMs = 6  // 2 bytes: uint16
	stateOffAckDeadlineMs     = 8  // 2 bytes: uint16
	stateOffFlags             = 10 // 2 bytes: uint16, bit 0 daemonEnabled, bit 1 spoolOnBreach
	stateOffMaxPayloadBytes   = 12 // 4 bytes: uint32
	stateOffDaemonPID         = 16 // 4 bytes: uint32
	stateOffWritten           = 20 // 8 bytes: int64 unix milliseconds
	stateOffCRC               = 28 // 4 bytes: crc32.Castagnoli over [0:stateOffCRC]
)

// stateMagic is the record's 4-byte identifier: 'Q'ompack 'P'rocess 'S'tate v'1'. Checked before
// anything else so a file written by a future, incompatible layout is recognized as "not this
// format" — and falls back like any other unreadable state file — rather than partially decoded.
var stateMagic = [4]byte{'Q', 'P', 'S', '1'}

// The two flags packed into the 16-bit flags field at stateOffFlags.
const (
	stateFlagDaemonEnabled uint16 = 1 << 0
	stateFlagSpoolOnBreach uint16 = 1 << 1
)

// crc32cTable is computed once: decodeState/encodeState both hash on every call (decodeState on
// every ReadState, which is the hot path this record exists to make cheap), so recomputing the
// table per call would undermine the very budget BenchmarkReadState checks.
var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// statePerm is state.bin's on-disk permission: owner-only, like every other file under .qompack/.
const statePerm = 0o600

// stateFileName is state.bin's name within paths.Layout.Run.
const stateFileName = "state.bin"

// State is the daemon's hot-path summary of everything a client needs before it decides whether and
// how to connect: the contract mode (§12.1), the hot-path submode (§12.2), the two connection
// deadlines, the daemon's own liveness knobs, and when it was last written.
type State struct {
	Mode              contract.Mode
	Hot               HotPathMode
	ConnectDeadlineMs uint16
	AckDeadlineMs     uint16
	DaemonEnabled     bool
	SpoolOnBreach     bool
	MaxPayloadBytes   uint32
	DaemonPID         uint32
	Written           core.UnixMilli
}

// StatePath returns <projectRoot>/.qompack/run/state.bin, the file ReadState/WriteState/RemoveState
// operate on.
func StatePath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).Run, stateFileName)
}

// encodeState renders s into the exact 32-byte wire layout, computing and appending the trailing
// CRC32C over everything before it.
func encodeState(s State) [stateSize]byte {
	var buf [stateSize]byte
	copy(buf[stateOffMagic:], stateMagic[:])
	buf[stateOffMode] = byte(s.Mode)
	buf[stateOffHot] = byte(s.Hot)
	binary.LittleEndian.PutUint16(buf[stateOffConnectDeadlineMs:], s.ConnectDeadlineMs)
	binary.LittleEndian.PutUint16(buf[stateOffAckDeadlineMs:], s.AckDeadlineMs)

	var flags uint16
	if s.DaemonEnabled {
		flags |= stateFlagDaemonEnabled
	}
	if s.SpoolOnBreach {
		flags |= stateFlagSpoolOnBreach
	}
	binary.LittleEndian.PutUint16(buf[stateOffFlags:], flags)

	binary.LittleEndian.PutUint32(buf[stateOffMaxPayloadBytes:], s.MaxPayloadBytes)
	binary.LittleEndian.PutUint32(buf[stateOffDaemonPID:], s.DaemonPID)
	binary.LittleEndian.PutUint64(buf[stateOffWritten:], uint64(s.Written)) //nolint:gosec // wire layout is a fixed-width int64 field

	crc := crc32.Checksum(buf[:stateOffCRC], crc32cTable)
	binary.LittleEndian.PutUint32(buf[stateOffCRC:], crc)
	return buf
}

// decodeState is encodeState's inverse. ok is false for anything that is not a well-formed,
// CRC-valid record of exactly stateSize bytes with the correct magic — the three ways ReadState's
// contract promises to fall back silently: missing (never reaches here), short, bad magic, bad CRC.
func decodeState(buf []byte) (s State, ok bool) {
	if len(buf) != stateSize {
		return State{}, false
	}
	if !bytes.Equal(buf[stateOffMagic:stateOffMagic+len(stateMagic)], stateMagic[:]) {
		return State{}, false
	}
	wantCRC := binary.LittleEndian.Uint32(buf[stateOffCRC:])
	if gotCRC := crc32.Checksum(buf[:stateOffCRC], crc32cTable); gotCRC != wantCRC {
		return State{}, false
	}

	flags := binary.LittleEndian.Uint16(buf[stateOffFlags:])
	return State{
		Mode:              contract.Mode(buf[stateOffMode]),
		Hot:               HotPathMode(buf[stateOffHot]),
		ConnectDeadlineMs: binary.LittleEndian.Uint16(buf[stateOffConnectDeadlineMs:]),
		AckDeadlineMs:     binary.LittleEndian.Uint16(buf[stateOffAckDeadlineMs:]),
		DaemonEnabled:     flags&stateFlagDaemonEnabled != 0,
		SpoolOnBreach:     flags&stateFlagSpoolOnBreach != 0,
		MaxPayloadBytes:   binary.LittleEndian.Uint32(buf[stateOffMaxPayloadBytes:]),
		DaemonPID:         binary.LittleEndian.Uint32(buf[stateOffDaemonPID:]),
		Written:           core.UnixMilli(int64(binary.LittleEndian.Uint64(buf[stateOffWritten:]))), //nolint:gosec // inverse of encodeState's own PutUint64(uint64(int64))
	}, true
}

// ReadState reads projectRoot's state.bin. Any failure to produce a valid record — the file is
// missing (the ordinary first-run case), too short, carries the wrong magic, or fails its own CRC —
// falls back to StateFromConfig(fallback) without logging: a hot-path client cannot afford to
// treat "no state yet" as an error, and a corrupt state file is exactly the kind of thing §12.3
// says should fail toward doing nothing rather than toward a crash.
func ReadState(projectRoot string, fallback config.Config) State {
	buf, err := os.ReadFile(paths.Long(StatePath(projectRoot)))
	if err != nil {
		return StateFromConfig(fallback)
	}
	if s, ok := decodeState(buf); ok {
		return s
	}
	return StateFromConfig(fallback)
}

// writeStateMaxAttempts bounds WriteState's retry of a transient rename failure: on Windows,
// paths.WriteAtomic's finishing rename can fail with ERROR_ACCESS_DENIED when a concurrent
// ReadState (in any process — a hook client, /qompack:status, another daemon worker) briefly holds
// the destination open at the exact instant of the rename. This is a liveness hiccup, not a
// correctness one — decodeState's CRC guard already means a reader can never observe a torn record
// either way — and a small bounded immediate retry (no time.Sleep; runtime.Gosched yields instead
// of a wall-clock wait) is enough to ride out the ordinary case: one writer (the daemon) against
// many short-lived readers, never many writers racing each other for the same destination.
const writeStateMaxAttempts = 64

// WriteState writes s to projectRoot's state.bin through paths.WriteAtomic, so a client reading
// concurrently with a daemon write never observes a torn 32-byte record.
func WriteState(projectRoot string, s State) error {
	buf := encodeState(s)
	p := StatePath(projectRoot)

	var err error
	for attempt := 0; attempt < writeStateMaxAttempts; attempt++ {
		if err = paths.WriteAtomic(p, buf[:], statePerm); err == nil {
			return nil
		}
		if !os.IsPermission(err) {
			return err
		}
		runtime.Gosched()
	}
	return err
}

// RemoveState deletes projectRoot's state.bin. It is idempotent: removing a state file that does
// not exist (a daemon that never got as far as writing one, or a second clean-stop call) is not an
// error.
func RemoveState(projectRoot string) error {
	err := os.Remove(paths.Long(StatePath(projectRoot)))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// StateFromConfig projects cfg into a State: the fallback ReadState returns when no daemon has
// written one yet, and the daemon's own starting point before its first real transition.
//
// runtime.mode's operator vocabulary (auto|full|passive|off) is not contract.Mode.String()'s
// on-disk vocabulary (full|degraded-passive|off) — they are different readers of different
// spellings that happen to share two of them. contract.ParseMode handles the two shared spellings
// ("full", "off"); "passive" is runtime.mode's own word for ModeDegradedPassive, and "auto" (the
// default) together with any unrecognized value fails toward ModeFull — not because §12.3's
// "fail toward do nothing" applies here (ModeFull is the do-something mode), but because "auto"
// means "let the contract monitor decide", and the monitor only ever decides by degrading FROM
// full, never by starting anywhere else.
func StateFromConfig(cfg config.Config) State {
	return State{
		Mode:              modeFromRuntimeString(cfg.Runtime.Mode),
		Hot:               HotSync,
		ConnectDeadlineMs: clampUint16(cfg.Runtime.Daemon.ConnectDeadlineMs),
		AckDeadlineMs:     clampUint16(cfg.Runtime.Daemon.AckDeadlineMs),
		DaemonEnabled:     cfg.Runtime.Daemon.Enabled,
		SpoolOnBreach:     cfg.Runtime.HotPath.SpoolOnBreach,
		MaxPayloadBytes:   clampUint32(cfg.Runtime.HotPath.MaxPayloadBytes),
	}
}

// runtimeModePassive is runtime.mode's own spelling for ModeDegradedPassive — distinct from
// contract.Mode.String()'s "degraded-passive", which is why this is not just a contract.ParseMode
// call.
const runtimeModePassive = "passive"

// modeFromRuntimeString maps runtime.mode's value (enum "auto|full|passive|off",
// internal/config/runtime.go) onto contract.Mode.
func modeFromRuntimeString(mode string) contract.Mode {
	if mode == runtimeModePassive {
		return contract.ModeDegradedPassive
	}
	if m, ok := contract.ParseMode(mode); ok {
		return m // matches "full" or "off" verbatim.
	}
	return contract.ModeFull // "auto", and anything unrecognized.
}

// clampUint16 and clampUint32 protect the fixed-width wire fields from an operator config value
// that does not fit — the schema's rng tags keep this from happening in practice, but a state
// record must never silently wrap a huge value into a tiny, wrong one.
func clampUint16(v int) uint16 {
	switch {
	case v < 0:
		return 0
	case v > math.MaxUint16:
		return math.MaxUint16
	default:
		return uint16(v)
	}
}

func clampUint32(v int) uint32 {
	switch {
	case v < 0:
		return 0
	case v > math.MaxUint32:
		return math.MaxUint32
	default:
		return uint32(v)
	}
}
