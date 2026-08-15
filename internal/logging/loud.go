package logging

import "sync"

// loudRingSize is the capacity of the process-wide Loud ring LastLoud reads from.
const loudRingSize = 32

var (
	loudRingMu  sync.Mutex
	loudRingBuf [loudRingSize]string
	loudRingLen int
	loudRingPos int
)

// recordLoud appends line to the process-wide ring, evicting the oldest entry once the ring is
// full. It is called for every Loud call from every Logger in the process, including Nop's.
func recordLoud(line string) {
	loudRingMu.Lock()
	defer loudRingMu.Unlock()
	loudRingBuf[loudRingPos] = line
	loudRingPos = (loudRingPos + 1) % loudRingSize
	if loudRingLen < loudRingSize {
		loudRingLen++
	}
}

// LastLoud returns the formatted lines of the last 32 Loud calls made anywhere in this process,
// oldest first. /qompack:status (SP-14) surfaces this so degradation and contract violations are
// visible without grepping LOUD.log.
func LastLoud() []string {
	loudRingMu.Lock()
	defer loudRingMu.Unlock()
	out := make([]string, loudRingLen)
	start := loudRingPos - loudRingLen
	if start < 0 {
		start += loudRingSize
	}
	for i := 0; i < loudRingLen; i++ {
		out[i] = loudRingBuf[(start+i)%loudRingSize]
	}
	return out
}

var (
	loudObserverMu sync.RWMutex
	loudObserver   func(msg string, kv ...any)
)

// AttachLoudObserver installs fn to be called, synchronously, on every Loud call made anywhere in
// this process — nil clears the observer, and the most recent call wins. logging cannot depend on
// obs directly (§3.2: obs is a sibling foundation package, and logging -> obs would be an edge
// devtool lint's import-graph check rejects), so a composition root wires the two together at
// startup with a closure over an obs.Registry, e.g. func(msg string, kv ...any) {
// reg.Counter("loud.total").Add(1) }.
func AttachLoudObserver(fn func(msg string, kv ...any)) {
	loudObserverMu.Lock()
	loudObserver = fn
	loudObserverMu.Unlock()
}

func fireLoudObserver(msg string, kv []any) {
	loudObserverMu.RLock()
	fn := loudObserver
	loudObserverMu.RUnlock()
	if fn != nil {
		fn(msg, kv...)
	}
}
