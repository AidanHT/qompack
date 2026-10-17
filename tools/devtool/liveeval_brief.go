package main

// The steer arm's brief records and the resident-daemon check of `devtool live-eval` (V7 W6 item 5,
// ruling R42). The mod's brief-outcome log is read here with the wire contract's parser
// (coordinator decision D-a) and handed to internal/eval as plain values, which matches it to the
// stream's compactions.

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/daemon"
	"github.com/qompack/qompack/internal/eval"
	"github.com/qompack/qompack/internal/modwire"
)

// liveBriefOutcomes reads the session's state/mod-brief-<session>.jsonl from project and matches it to
// the trial's compactions. A missing log leaves every compaction "missing"; a log that cannot be read
// says why in the outcome's Problem, and its compactions are missing too.
func liveBriefOutcomes(project, sessionID string, compactions []eval.HostCompaction) *eval.LiveBriefOutcomes {
	records, malformed, problem := readLiveBriefRecords(project, sessionID)
	out := eval.MatchLiveBriefs(compactions, records)
	out.Malformed, out.Problem = malformed, problem
	return &out
}

// readLiveBriefRecords parses the session's brief-outcome log line by line with modwire's parser,
// counting the lines it refuses.
func readLiveBriefRecords(project, sessionID string) (records []eval.LiveBriefRecord, malformed int, problem string) {
	name, err := modwire.BriefRecordName(sessionID)
	if err != nil {
		return nil, 0, err.Error()
	}
	raw, err := os.ReadFile(filepath.Join(project, liveStoreDir, modwire.StateDir, name))
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, 0, ""
	case err != nil:
		return nil, 0, "reading " + name + ": " + err.Error()
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64<<10), modwire.MaxMessageBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		r, err := modwire.ParseBriefRecord(line)
		if err != nil {
			malformed++
			continue
		}
		records = append(records, eval.LiveBriefRecord{Trigger: r.Trigger, Outcome: string(r.Outcome), Chars: r.Chars, MS: r.MS})
	}
	if err := sc.Err(); err != nil {
		return records, malformed, "reading " + name + ": " + err.Error()
	}
	return records, malformed, ""
}

// checkLiveResidentDaemon looks, before a Qompack arm's session, for a daemon already resident for
// the trial's fresh project (ruling R42, review M4). The brief record carries no error text, so an
// `error` outcome alone cannot show that a pre-W1a daemon answered "unknown op: mod"; the signal W6
// task 2 chose is this pre-session one: a daemon.lock in the project whose recorded build version
// is not the arm's bundle version (or that cannot be read) is a daemon of another build, and the
// attempt is a configuration error, set aside and run again. No lock is the expected state; a lock
// of the arm's own build is recorded and is no fault.
//
// The check is deliberately broader than "a pre-W1a daemon" (coordinator ruling on review WIRING-3):
// every trial's project is created fresh, so any lock of another build in it is contamination, and the
// attempt fails closed whatever that build is. The daemon's lock records core.Version, which `devtool
// bundle` stamps with the bundle's own version, so a build of the arm's own commit never trips it.
func checkLiveResidentDaemon(project string, id *eval.LivePluginIdentity) (*eval.LiveResidentDaemon, error) {
	if id == nil {
		return nil, nil
	}
	_, statErr := os.Stat(daemon.LockPath(project))
	if absent := errors.Is(statErr, os.ErrNotExist); absent {
		return nil, nil // no lock: no resident daemon, the expected state
	}
	rd := &eval.LiveResidentDaemon{BundleVersion: id.Version}
	info, ok := daemon.ReadLock(project)
	if ok {
		rd.PID, rd.Version = info.PID, info.Version
	}
	if ok && info.Version == id.Version {
		return rd, nil
	}
	rd.Fault = true
	return rd, fmt.Errorf("a daemon of another build (pid %d, version %s) was resident for the trial's project before the "+
		"session, not the arm's bundle %s: it may predate W1a and answer every brief error (\"unknown op: mod\"), so the "+
		"attempt is a harness fault (ruling R42)", rd.PID, orUnknown(rd.Version), id.Version)
}
