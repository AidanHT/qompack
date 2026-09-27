package paths

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"
)

// entries is this process's record of the directory entries it has made durable — a name whose
// parent directory it synced, successfully, while the name existed — and of the entries it created
// and has not yet made durable. The durable writers in barriers.go and layout.go consult it so that a
// directory barrier is never taken as done because a name is merely on disk (w6-ckptsync review
// finding 1):
//
//   - a call whose parent sync failed returns the error with the name already created, and the next
//     call finds the name pending here and issues the sync itself;
//   - a writer that finds a name another goroutine created and is still syncing finds it pending and
//     syncs it too, rather than acting on a barrier that has not returned;
//   - a file some other process created — one that exited before its directory sync, or never issued
//     one — is not recorded here at all, so AppendLinesDurable syncs its directory once in this
//     process before reporting a line in it durable.
//
// What it does not cover is stated where it matters. MkdirAll takes a directory another process
// created as durable (paths.Barriers.MkdirAll says why), and EnsureLayout covers the layout another
// writer started through its .gitignore marker instead.
//
// Keys are filepath.Clean paths. Two spellings of one path are two keys, which costs at most a
// redundant sync, never a skipped one. The ledger only grows; it holds a few names per log and per
// directory a process writes durably, which is a bounded set for every process the product runs.
var entries = entryLedger{m: map[string]entryRecord{}}

type entryLedger struct {
	mu sync.Mutex
	m  map[string]entryRecord
}

// entryRecord is what the ledger knows about one name. gen counts the times this process has created
// the name; a sync credits the name only if gen is unchanged since the sync was decided on, so a name
// removed and re-created while a barrier was in flight is never credited by that barrier.
type entryRecord struct {
	state entryState
	gen   uint64
}

type entryState uint8

const (
	// entryUnknown: this process neither created the name nor synced its parent since it existed.
	entryUnknown entryState = iota
	// entryPending: this process created the name, and no sync of its parent has succeeded since.
	entryPending
	// entryDurable: a sync of the name's parent, decided on after the name existed, returned nil.
	entryDurable
)

// creating records that this process is about to create p: pending, whatever it was before. A
// caller that creates a name marks it BEFORE the create, so a concurrent writer that sees the name on
// disk can never find it recorded durable by an earlier generation.
func (e *entryLedger) creating(p string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := filepath.Clean(p)
	r := e.m[k]
	r.gen++
	r.state = entryPending
	e.m[k] = r
}

// look returns p's state and generation.
func (e *entryLedger) look(p string) (entryState, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.m[filepath.Clean(p)]
	return r.state, r.gen
}

// credit records p durable after a sync of its parent that was decided on at generation gen, unless p
// has been re-created since.
func (e *entryLedger) credit(p string, gen uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	k := filepath.Clean(p)
	r := e.m[k]
	if r.gen != gen {
		return
	}
	r.state = entryDurable
	e.m[k] = r
}

// pendingFrom returns p's ancestors that this process created and has not yet made durable, nearest
// first, stopping at the volume root. It walks the ledger, not the disk: a pending ancestor exists
// (something below it does), and one this process did not create is not its to doubt.
func (e *entryLedger) pendingFrom(p string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for q := filepath.Dir(filepath.Clean(p)); ; {
		if e.m[q].state == entryPending {
			out = append(out, q)
		}
		parent := filepath.Dir(q)
		if parent == q {
			return out
		}
		q = parent
	}
}

// syncEntries makes each name in names durable: it syncs each name's parent — every distinct parent
// once, deepest first, so a directory's own entry is synced only after the entries inside it — and
// credits each name as soon as its parent's sync returns. Every name must exist when it is called.
// The first failed sync stops it: the names that sync covered stay as they were, and op names the
// caller in the error.
func (x Barriers) syncEntries(op string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	type named struct {
		name string
		gen  uint64
	}
	byParent := map[string][]named{}
	for _, n := range names {
		_, gen := entries.look(n)
		parent := filepath.Dir(filepath.Clean(n))
		byParent[parent] = append(byParent[parent], named{name: n, gen: gen})
	}
	order := make([]string, 0, len(byParent))
	for p := range byParent {
		order = append(order, p)
	}
	sort.Slice(order, func(i, j int) bool {
		if len(order[i]) != len(order[j]) {
			return len(order[i]) > len(order[j])
		}
		return order[i] < order[j]
	})
	for _, p := range order {
		if err := x.syncDir(p); err != nil {
			return fmt.Errorf("paths: %s: sync %s: %w", op, p, err)
		}
		for _, n := range byParent[p] {
			entries.credit(n.name, n.gen)
		}
	}
	return nil
}
