package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qompack/qompack/internal/paths"
)

// The human-readable MCP handshake record.
//
// This file is NOT the mcp.server_registered contract observable, and the distinction is
// load-bearing rather than pedantic. SP-05 already fixed that mechanism: the assertion reads
// contract.SessionHistory.MCPInitialized out of state/history.json — the path
// contract.HistoryPath returns, deliberately not state/contract.json, which is the Monitor's own
// persisted mode/reason/results — and contract.DeclareProducers declares the producer only once
// daemon.Services.MCPInitialized is bound. Inventing a second mechanism here would leave the
// assertion reporting "not-yet-implemented" for ever while a file on disk said otherwise.
//
// What state/mcp.json IS: the record a human reads. Which protocol version was negotiated, by
// which client, at what time, in which process, over how many tools. /qompack:status renders it
// and test/e2e asserts it, and it is what a user is pointed at when the tools do not show up.
// Both are written from the same place — the daemon's `initialized` op — so they cannot disagree.

// observableFileName is state/mcp.json's basename.
const observableFileName = "mcp.json"

// observablePerm is the mode state/mcp.json is written with: owner-only, like every other file
// under .qompack/.
const observablePerm = 0o600

// ObservablePath returns the state/mcp.json a project's handshake record is written to.
func ObservablePath(projectRoot string) string {
	return filepath.Join(paths.Of(projectRoot).State, observableFileName)
}

// WriteInitializedObservable records o as this project's MCP handshake record, replacing any
// previous one.
//
// It is deliberately a whole-file replace rather than an append: there is exactly one current
// answer to "is the MCP server up, and against which client", and a log of past handshakes would
// make a reader work out which line is the live one.
func WriteInitializedObservable(projectRoot string, o Observable) error {
	if projectRoot == "" {
		return fmt.Errorf("mcp: writing %s: no project root", observableFileName)
	}
	path := ObservablePath(projectRoot)

	b, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: marshalling %s: %w", path, err)
	}
	b = append(b, '\n')

	if err := os.MkdirAll(paths.Long(filepath.Dir(path)), 0o700); err != nil {
		return fmt.Errorf("mcp: mkdir for %s: %w", path, err)
	}
	if err := paths.WriteAtomic(path, b, observablePerm); err != nil {
		return fmt.Errorf("mcp: writing %s: %w", path, err)
	}
	return nil
}

// ReadInitializedObservable reads a project's handshake record back. It is what /qompack:status
// and the e2e suite use, and it reports a missing file as a zero Observable with no error: "no
// handshake has happened" is an answer, not a failure.
func ReadInitializedObservable(projectRoot string) (Observable, error) {
	var o Observable
	if projectRoot == "" {
		return o, nil
	}
	b, err := paths.ReadFileShared(ObservablePath(projectRoot))
	if err != nil {
		return o, nil
	}
	if err := json.Unmarshal(b, &o); err != nil {
		return Observable{}, fmt.Errorf("mcp: parsing %s: %w", ObservablePath(projectRoot), err)
	}
	return o, nil
}
