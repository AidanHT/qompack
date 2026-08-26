package observer

import "github.com/qompack/qompack/internal/store"

// The two key prefixes that keep the path and tool namespaces disjoint inside one Count-Min
// sketch. They are single bytes plus a NUL separator rather than a human-readable word because
// every Add is on budget B-C and the key is hashed, never displayed.
const (
	sketchPathPrefix = "p\x00"
	sketchToolPrefix = "t\x00"
)

// feedSketches is §8.1 item 5: the Count-Min file-touch frequency sketch of §6.2, the HyperLogLog
// breadth-of-exploration cardinality over DISTINCT paths, and the Misra-Gries top-k that gives
// `/qompack:status` a no-false-positives answer.
//
// # The Bloom filter is never referenced in this file, or anywhere in internal/observer
//
// §8.1 item 5 is explicit: "Feed the Bloom filter ONLY on explicit negative-knowledge events
// (§8.3)", and those events are SP-09's. That is why this package declines the negknow import its
// §3.2 allow-set would permit, and why TestObserverSourceHasNoBloomReference parses every non-test
// file here and requires zero occurrences of the identifier.
func (o *observer) feedSketches(rec store.ToolUseRecord) {
	if rec.Ephemeral {
		return // retrieval is not exploration (resolved decision 6)
	}

	// The three sketches are per-PROJECT and shared by every session, so the per-session lock of
	// decision 9 does not cover them and internal/sketch supplies no locking of its own.
	o.sketchMu.Lock()
	defer o.sketchMu.Unlock()

	if o.opt.Touch != nil {
		o.opt.Touch.Add([]byte(sketchToolPrefix+rec.Tool), 1)
		if rec.Path != "" {
			o.opt.Touch.Add([]byte(sketchPathPrefix+rec.Path), 1)
		}
	}
	if o.opt.Explore != nil && rec.Path != "" {
		o.opt.Explore.Add([]byte(sketchPathPrefix + rec.Path))
	}
	if o.opt.Hot != nil && rec.Path != "" {
		o.opt.Hot.Add(rec.Path, 1)
	}
}
