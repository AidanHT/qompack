package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/qompack/qompack/internal/core"
)

// `devtool release-check` is the gate a tag has to pass before anything is published.
//
// It exists because `ci-local` — which release.yml ran, and which was the ONLY verification
// between a tag and a published artifact — omits build-all, govulncheck, the import allow-list and
// two of the three doc checks (survey-release-install.md §10.1). A tag could therefore ship what
// ci.yml would have rejected on the same commit. Rather than duplicate ci-local's list, this gate
// REUSES the slice and adds the missing steps after it, so the two can never drift apart.
//
// Ordering is a property, not an accident: the cheap and total checks run first, so a release
// engineer sees a formatting failure in seconds rather than after a six-target cross-build. The
// gate stops at the first FAIL for the same reason — a list of downstream failures caused by one
// upstream break is noise, and the JSON summary records exactly which step stopped it.
const (
	rcPass    = "PASS"
	rcFail    = "FAIL"
	rcSkipped = "SKIPPED"

	// releaseCheckSummary is written on every run, pass or fail. A gate that records nothing when
	// it fails is a gate whose failure has to be reconstructed from a scrollback buffer.
	releaseCheckSummary = "dist/release-check.json"
)

// releaseCheckOptions are the run's inputs.
type releaseCheckOptions struct {
	// Tag is --tag's value ("" when absent), e.g. "v0.2.0".
	Tag string
	// SkipVulncheck records the govulncheck step as SKIPPED instead of running it. It exists for
	// an offline machine; CI never passes it, and the recorded SKIPPED says which run was which.
	SkipVulncheck bool
	// EvidenceCopy is --evidence-copy's path ("" when absent). When set, the same summary written
	// to dist/release-check.json is also written there so the final gate can commit it.
	EvidenceCopy string
}

// releaseCheckOutcome is one step's verdict and the prose that explains it.
type releaseCheckOutcome struct {
	Status string
	Detail string
}

func rcPassf(format string, a ...any) releaseCheckOutcome {
	return releaseCheckOutcome{Status: rcPass, Detail: fmt.Sprintf(format, a...)}
}

func rcFailf(format string, a ...any) releaseCheckOutcome {
	return releaseCheckOutcome{Status: rcFail, Detail: fmt.Sprintf(format, a...)}
}

func rcSkipf(format string, a ...any) releaseCheckOutcome {
	return releaseCheckOutcome{Status: rcSkipped, Detail: fmt.Sprintf(format, a...)}
}

// releaseCheckStep is one ordered gate.
type releaseCheckStep struct {
	name string
	run  func(o releaseCheckOptions) releaseCheckOutcome
}

// releaseCheckResult is one step's row in the summary document.
type releaseCheckResult struct {
	Name    string  `json:"name"`
	Status  string  `json:"status"`
	Seconds float64 `json:"seconds"`
	Detail  string  `json:"detail,omitempty"`
}

// releaseCheckDoc is dist/release-check.json.
type releaseCheckDoc struct {
	Version string               `json:"version"`
	Tag     string               `json:"tag,omitempty"`
	Steps   []releaseCheckResult `json:"steps"`
	OK      bool                 `json:"ok"`
}

// taskReleaseCheck runs the gate and writes the summary.
func taskReleaseCheck(args []string) error {
	fs := flag.NewFlagSet("release-check", flag.ContinueOnError)
	tag := fs.String("tag", "",
		"the tag this release would carry, e.g. v1.2.3; its version must equal internal/core.Version")
	skipVuln := fs.Bool("skip-vulncheck", false,
		"record the govulncheck step as SKIPPED (for a machine that cannot reach the vulnerability database)")
	evidenceCopy := fs.String("evidence-copy", "",
		"also write the same summary to this path so a release-scope evidence root can commit it")
	if err := fs.Parse(args); err != nil {
		return errors.Join(errUsage, err)
	}
	if fs.NArg() > 0 {
		return errors.Join(errUsage, fmt.Errorf("release-check: unexpected argument %q", fs.Arg(0)))
	}

	o := releaseCheckOptions{Tag: *tag, SkipVulncheck: *skipVuln, EvidenceCopy: *evidenceCopy}
	doc := runReleaseCheckSteps(releaseCheckSteps(), o, time.Now)
	if err := writeReleaseCheckSummary(doc); err != nil {
		return err
	}
	if o.EvidenceCopy != "" {
		if err := writeReleaseCheckDoc(doc, rootRelative(o.EvidenceCopy)); err != nil {
			return err
		}
	}
	if !doc.OK {
		return fmt.Errorf("release-check: %s FAILED", doc.Steps[len(doc.Steps)-1].Name)
	}
	fmt.Printf("release-check: %d step(s), all PASS or SKIPPED; summary at %s\n", len(doc.Steps), releaseCheckSummary)
	return nil
}

// runReleaseCheckSteps drives the gate. The step list and the clock are parameters so the
// stop-at-first-FAIL rule, the SKIPPED-is-not-a-failure rule and the document's shape can be
// tested without running a six-target cross-build for each assertion.
func runReleaseCheckSteps(steps []releaseCheckStep, o releaseCheckOptions, now func() time.Time) releaseCheckDoc {
	doc := releaseCheckDoc{Version: core.Version, Tag: o.Tag, OK: true}
	for _, step := range steps {
		fmt.Printf("\n=== release-check: %s ===\n", step.name)
		start := now()
		out := step.run(o)
		secs := math.Round(now().Sub(start).Seconds()*1000) / 1000
		doc.Steps = append(doc.Steps, releaseCheckResult{
			Name: step.name, Status: out.Status, Seconds: secs, Detail: out.Detail,
		})
		fmt.Printf("release-check: %s %s", step.name, out.Status)
		if out.Detail != "" {
			fmt.Printf(" — %s", out.Detail)
		}
		fmt.Println()
		if out.Status == rcFail {
			doc.OK = false
			return doc
		}
	}
	return doc
}

// writeReleaseCheckSummary persists the document under dist/, which is git-ignored: the summary is
// a build output, and a committed one would be a claim about a tree nobody can check it against.
func writeReleaseCheckSummary(doc releaseCheckDoc) error {
	return writeReleaseCheckDoc(doc, filepath.Join(root, filepath.FromSlash(releaseCheckSummary)))
}

func writeReleaseCheckDoc(doc releaseCheckDoc, path string) error {
	raw, err := marshalBundleJSON(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), bundleDirPerm); err != nil {
		return err
	}
	return os.WriteFile(path, raw, bundleFilePerm)
}
